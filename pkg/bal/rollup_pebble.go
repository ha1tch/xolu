// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/ha1tch/xolu/pkg/chronicle"
)

// T-62: bal's rollup/cascade plane moved off SQLite onto Pebble. The
// plane is DERIVED and no guard ever reads it (@C04a, confirmed against
// balanceAndFloor's own query — it touches only balances/accounts).
// chronicle.Engine's BucketStore interface was built storage-agnostic
// specifically for this case (chronicle/engine.go doc); the SQL
// bucketStore that shipped at wave 4 contradicted both the interface's
// own design intent and this package's own design proposal
// (bal-conservation-primitive.md's comparison table already said
// "rollup deltas in the Pebble plane"). This file is that plane, built
// the way cal's H1/H3 split already demonstrates: guard-bearing state
// stays SQL-colocated with its guard (bal's journal, checkpoints —
// unchanged, still in store/xolu.db); everything downstream of the
// guard that nothing ever refuses moves to Pebble.
//
// Checkpoints stay in SQL deliberately, NOT because a guard reads them
// either, but for a different reason entirely: transferInTx updates
// checkpoint balances in the SAME transaction as the journal write
// (T-58's eager delta-adjustment) — a write-locality requirement, not
// a guard-locality one. Buckets have no equivalent: EmitDeltas runs
// strictly after commit (rollup.go's own comment), so there is nothing
// here that needs SQL-transaction co-location with anything.

// pebbleBucketKey encodes (accountKey, level, startUnix) into a fixed
// 20-byte big-endian key: 8 (account) + 4 (level) + 8 (start seconds).
// Big-endian on non-negative integers gives byte-lexicographic order
// equal to numeric order, which is what RangeLevel's ascending-Start
// scan needs. One pebbleBucketStore is bound to one account (matching
// the SQL predecessor's per-account newBucketStore), so a scan need
// only fix the account+level prefix and walk forward.
func pebbleBucketKey(accountKey int64, level int32, startUnix int64) []byte {
	k := make([]byte, 20)
	binary.BigEndian.PutUint64(k[0:8], uint64(accountKey))
	binary.BigEndian.PutUint32(k[8:12], uint32(level))
	binary.BigEndian.PutUint64(k[12:20], uint64(startUnix))
	return k
}

// pebbleBucketPrefix is the (accountKey, level) prefix shared by every
// key at one level for one account — the scan bound RangeLevel needs.
func pebbleBucketPrefix(accountKey int64, level int32) []byte {
	k := make([]byte, 12)
	binary.BigEndian.PutUint64(k[0:8], uint64(accountKey))
	binary.BigEndian.PutUint32(k[8:12], uint32(level))
	return k
}

// RollupPebble is the long-lived Pebble handle backing bal's rollup
// plane for one tenant. Opened once (OpenRollupPebble) and attached to
// each freshly-constructed *Store via SetRollupPebble — mirroring
// dxp.MemCache/SetClaimsCache exactly, and for the same reason: bal.Store
// is built fresh per request in pkg/server, but a *pebble.DB handle
// holds an exclusive on-disk lock and cannot be re-opened per request.
// The caller (pkg/server) owns caching one RollupPebble per tenant and
// attaching it to each request's Store; bal itself stays agnostic of
// that lifecycle, same as it is for the claims cache.
type RollupPebble struct {
	db  *pebble.DB
	dir string
}

// OpenRollupPebble opens (creating if needed) the Pebble database
// backing bal's rollup plane at dir — typically
// storelayout.TenantBalRollupDir(base, tenantID), matching cal's
// OpenIndexStore convention exactly (Pebble database lives in dir/db).
// The returned handle is attached to a Store via SetRollupPebble; the
// same handle may be shared across multiple Store instances for the
// same tenant (they all read the same account_key numbering, since
// account_key is stable per tenant regardless of which Store issued
// it).
func OpenRollupPebble(dir string) (*RollupPebble, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("bal: OpenRollupPebble: mkdir %s: %w", dir, err)
	}
	dbDir := filepath.Join(dir, "db")
	db, err := pebble.Open(dbDir, &pebble.Options{})
	if err != nil {
		return nil, fmt.Errorf("bal: OpenRollupPebble: open %s: %w", dbDir, err)
	}
	return &RollupPebble{db: db, dir: dir}, nil
}

// Close closes the underlying Pebble database. The caller must ensure
// no Store still holds this handle (via SetRollupPebble) when Close is
// called — mirroring the same discipline pkg/server already applies to
// dxp.MemCache and cal.Manager's Pebble-backed IndexStore.
func (rp *RollupPebble) Close() error {
	return rp.db.Close()
}

// SetRollupPebble attaches an already-open rollup Pebble handle to this
// Store. Required before InitRollup/EmitDeltas/BalanceAsOf/
// RebuildRollup/RollupOracle — mirrors SetClaimsCache exactly.
func (s *Store) SetRollupPebble(rp *RollupPebble) { s.rollup = rp }

// pebbleBucketStore implements chronicle.BucketStore[int64] over one
// account's slice of the shared Pebble database. Interface-identical
// to the SQL predecessor it replaces (same Get/Put/Delete/RangeLevel
// contract), so engineFor and every caller above it (EmitDeltas,
// BalanceAsOf, RebuildRollup, RollupOracle) are unchanged.
type pebbleBucketStore struct {
	db      *pebble.DB
	account int64
}

func (s *Store) newBucketStore(accountKey int64) *pebbleBucketStore {
	return &pebbleBucketStore{db: s.rollup.db, account: accountKey}
}

func (b *pebbleBucketStore) Get(k chronicle.BucketKey) (int64, bool) {
	key := pebbleBucketKey(b.account, int32(k.Level), k.Start.UTC().Unix())
	v, closer, err := b.db.Get(key)
	if err != nil {
		return 0, false // pebble.ErrNotFound or any other read failure: treat as absent
	}
	defer func() { _ = closer.Close() }()
	if len(v) != 8 {
		return 0, false
	}
	return int64(binary.BigEndian.Uint64(v)), true
}

func (b *pebbleBucketStore) Put(k chronicle.BucketKey, v int64) {
	key := pebbleBucketKey(b.account, int32(k.Level), k.Start.UTC().Unix())
	val := make([]byte, 8)
	binary.BigEndian.PutUint64(val, uint64(v))
	_ = b.db.Set(key, val, pebble.Sync)
}

func (b *pebbleBucketStore) Delete(k chronicle.BucketKey) {
	key := pebbleBucketKey(b.account, int32(k.Level), k.Start.UTC().Unix())
	_ = b.db.Delete(key, pebble.Sync)
}

func (b *pebbleBucketStore) RangeLevel(level int, from, to time.Time, fn func(k chronicle.BucketKey, v int64) bool) {
	lo := pebbleBucketKey(b.account, int32(level), from.UTC().Unix())
	hi := pebbleBucketKey(b.account, int32(level), to.UTC().Unix())
	iter, err := b.db.NewIter(&pebble.IterOptions{LowerBound: lo, UpperBound: hi})
	if err != nil {
		return
	}
	defer func() { _ = iter.Close() }()
	for iter.First(); iter.Valid(); iter.Next() {
		key := iter.Key()
		if len(key) != 20 {
			continue
		}
		startUnix := int64(binary.BigEndian.Uint64(key[12:20]))
		val := iter.Value()
		if len(val) != 8 {
			continue
		}
		bk := chronicle.BucketKey{Level: level, Start: time.Unix(startUnix, 0).UTC()}
		v := int64(binary.BigEndian.Uint64(val))
		if !fn(bk, v) {
			return
		}
	}
}

// deleteAccountBuckets removes every bucket at every level for one
// account — the Pebble equivalent of RebuildRollup's SQL DELETE, via
// DeleteRange over the account's whole key space (all levels, matching
// cal's RebuildFrom precedent for bulk derived-plane clearing).
func (s *Store) deleteAccountBuckets(accountKey int64) error {
	lo := make([]byte, 8)
	binary.BigEndian.PutUint64(lo, uint64(accountKey))
	hi := make([]byte, 8)
	binary.BigEndian.PutUint64(hi, uint64(accountKey)+1) // exclusive upper bound: next account's first byte
	return s.rollup.db.DeleteRange(lo, hi, pebble.Sync)
}
