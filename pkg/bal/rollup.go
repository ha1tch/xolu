// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/ha1tch/xolu/pkg/chronicle"
)

// Stage 3 (@B05 rollup plane, @B07 period close): transfers emit signed
// deltas into the chronicle cascade under the SumInt64 monoid, closing
// checkpoints freeze at sealed period boundaries, and balance-as-of
// reads as: nearest sealed checkpoint + SUM of intervening buckets +
// unsealed tail — making as-of independent of journal length.
//
// The plane is DERIVED. No guard consults it (@C04a): admission reads
// `balances` in the write's own transaction. If the rollup is lost or
// wrong it is rebuilt from the journal, and the rebuild oracle proves
// derive(journal) == rollup across the cascade.

// balGrains is bal's rollup tree, rooted at the FINEST grain and
// fanning out to coarser ones:
//
//	          hour            (root)
//	           |
//	          day
//	           |
//	     +-----+-----+
//	     |           |
//	   week        month
//	                 |
//	              quarter
//	                 |
//	               year
//
// Day fans out into two coarsening branches because week and month nest
// in each other not at all (a month is 28–31 days, never a whole number
// of weeks) — the shape a linear chain cannot express, and the reason
// the hierarchy is a tree.
//
// The month branch is what makes financial periods exact: Q1, H1 and
// fiscal years are whole-month spans, so they fold from quarter/year
// buckets directly. Folding them from weeks would consume partial
// buckets at both ends, which is precisely the inexactness the rollup
// plane exists to avoid.
//
// Sealing remains a separate axis: the Sealer's MonthWindows tiles
// periods for close, independent of these grains.
func balGrains() (*chronicle.Hierarchy, error) {
	return chronicle.NewTreeHierarchy(
		chronicle.TreeSpec{Grain: chronicle.FixedGrain("hour", time.Hour), Parent: ""},
		chronicle.TreeSpec{Grain: chronicle.FixedGrain("day", 24 * time.Hour), Parent: "hour"},
		chronicle.TreeSpec{Grain: chronicle.FixedGrain("week", 7 * 24 * time.Hour), Parent: "day"},
		chronicle.TreeSpec{Grain: chronicle.MonthGrain("month"), Parent: "day"},
		chronicle.TreeSpec{Grain: chronicle.MonthsGrain("quarter", 3), Parent: "month"},
		chronicle.TreeSpec{Grain: chronicle.MonthsGrain("year", 12), Parent: "quarter"},
	)
}

func (s *Store) checkpointsTable() string { return s.prefix + "bal_checkpoints" }

// InitRollup verifies the rollup plane's Pebble store is open
// (OpenRollupPebble must be called first — matching cal's
// OpenIndexStore/Manager split). Separate from Init so the SQL plane
// can exist without the derived plane (the derived plane is always
// rebuildable). T-62: previously created a SQL bucketsTable here; the
// rollup plane is now Pebble, opened via OpenRollupPebble, which
// creates its own directory on first use — there is no DDL left to
// run, so this is now a precondition check, not a table creation.
//
// The checkpoints table itself is still created by Init (core
// schema), because Transfer's write path maintains checkpoint
// staleness in-transaction (T-51/T-58) — the journal plane owns that
// table's lifecycle even though the rollup plane is its main reader.
// Checkpoints are a write-locality case, not a guard-locality one
// (rollup_pebble.go doc), which is why they did NOT move here.
func (s *Store) InitRollup(ctx context.Context) error {
	if s.rollup == nil {
		return fmt.Errorf("bal: InitRollup: rollup Pebble store not open (call OpenRollupPebble first)")
	}
	return nil
}

// engineFor builds a cascade engine bound to one account's buckets.
// Returns an error, not a panic, when the rollup Pebble plane was
// never attached (SetRollupPebble) — InitRollup's own doc is explicit
// that "the SQL plane can exist without the derived plane," and
// Transfer's EmitDeltas call is deliberately best-effort (rollup.go:
// "derived-plane failure must never fail an authoritative transfer").
// A nil-pointer panic here would defeat that contract entirely; this
// keeps the SQL-era behaviour (a returned error a caller can catch)
// rather than trading it for a crash.
func (s *Store) engineFor(accountKey int64) (*chronicle.Engine[int64], error) {
	if s.rollup == nil {
		return nil, fmt.Errorf("bal: rollup plane not attached (call SetRollupPebble first)")
	}
	h, err := balGrains()
	if err != nil {
		return nil, err
	}
	return chronicle.NewEngine[int64](chronicle.SumInt64{}, h, s.newBucketStore(accountKey))
}

// EmitDeltas folds a transfer's two signed legs into the rollup cascade.
// Called after the transfer's transaction commits: the rollup is derived,
// so a crash between commit and emit loses only derived state, which the
// rebuild oracle detects and RebuildRollup repairs.
func (s *Store) EmitDeltas(ctx context.Context, srcKey, dstKey, amount int64, at time.Time) error {
	src, err := s.engineFor(srcKey)
	if err != nil {
		return err
	}
	dst, err := s.engineFor(dstKey)
	if err != nil {
		return err
	}
	src.Append(-amount, at)
	dst.Append(amount, at)
	return nil
}

// Checkpoint writes a closing balance at a sealed period boundary. The
// checkpoint is what makes as-of independent of journal length, and what
// permits prefix-collapse retention later (@B05, item 16).
//
// This is for sealing a NEW boundary that has no checkpoint yet — not
// for repairing an existing one after a backdated entry. Existing
// checkpoints are kept correct as of the moment they're written by
// transferInTx's eager delta-adjustment (T-58); this recompute-from-
// journal path is never needed to fix one. Calling Checkpoint again at
// an already-checkpointed boundary is harmless (it recomputes the same
// correct value from source) but is a no-op in effect, not a repair.
func (s *Store) Checkpoint(ctx context.Context, accountID string, at time.Time) error {
	var key, balance, lastEntry int64
	err := s.db.QueryRowContext(ctx,
		`SELECT a.account_key,
		        COALESCE((SELECT SUM(j.amount) FROM `+s.journalTable()+` j
		                  WHERE j.account_key = a.account_key AND j.at <= ?), 0),
		        COALESCE((SELECT MAX(j2.entry_id) FROM `+s.journalTable()+` j2
		                  WHERE j2.account_key = a.account_key AND j2.at <= ?), 0)
		 FROM `+s.accountsTable()+` a WHERE a.account_id = ?`,
		at.UTC(), at.UTC(), accountID).Scan(&key, &balance, &lastEntry)
	if err == sql.ErrNoRows {
		return &UnknownAccountError{AccountID: accountID}
	}
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO `+s.checkpointsTable()+` (account_key, at_unix, balance, entry_id, stale)
		 VALUES (?,?,?,?,0)
		 ON CONFLICT(account_key, at_unix)
		 DO UPDATE SET balance = excluded.balance, entry_id = excluded.entry_id, stale = 0`,
		key, at.UTC().Unix(), balance, lastEntry)
	return err
}

// BalanceAsOf returns the account's balance at an instant, read the fast
// way (@B05): nearest sealed checkpoint at or before t, plus the fold of
// buckets between that checkpoint and t. With no checkpoint it folds
// from the epoch. The exact/audit path is the journal chain, which
// BalanceAsOfExact reads independently — the two must agree, and the
// rollup oracle asserts they do.
func (s *Store) BalanceAsOf(ctx context.Context, accountID string, t time.Time) (int64, error) {
	key, err := s.accountKeyOf(ctx, accountID)
	if err != nil {
		return 0, err
	}

	var ckptAt sql.NullInt64
	var ckptBalance sql.NullInt64
	_ = s.db.QueryRowContext(ctx,
		`SELECT at_unix, balance FROM `+s.checkpointsTable()+`
		 WHERE account_key = ? AND at_unix <= ? AND stale = 0
		 ORDER BY at_unix DESC LIMIT 1`,
		key, t.UTC().Unix()).Scan(&ckptAt, &ckptBalance)

	base := int64(0)
	from := time.Unix(0, 0).UTC()
	if ckptAt.Valid {
		base = ckptBalance.Int64
		from = time.Unix(ckptAt.Int64, 0).UTC()
	}

	eng, err := s.engineFor(key)
	if err != nil {
		return 0, err
	}
	// Half-open [from, t): the checkpoint already includes everything at
	// or before its own instant, so folding from it forward must not
	// double-count the boundary.
	tail := eng.FoldRange(from, t.UTC())
	return base + tail, nil
}

// BalanceAsOfExact reads the same quantity from the authoritative
// journal — the exact/audit path (@B08). Slower (scans entries) but
// independent of the rollup plane, which is what makes it a valid
// oracle for it.
func (s *Store) BalanceAsOfExact(ctx context.Context, accountID string, t time.Time) (int64, error) {
	key, err := s.accountKeyOf(ctx, accountID)
	if err != nil {
		return 0, err
	}
	var v sql.NullInt64
	err = s.db.QueryRowContext(ctx,
		`SELECT SUM(amount) FROM `+s.journalTable()+`
		 WHERE account_key = ? AND at < ?`, key, t.UTC()).Scan(&v)
	if err != nil {
		return 0, err
	}
	return v.Int64, nil
}

func (s *Store) accountKeyOf(ctx context.Context, accountID string) (int64, error) {
	var key int64
	err := s.db.QueryRowContext(ctx,
		`SELECT account_key FROM `+s.accountsTable()+` WHERE account_id = ?`,
		accountID).Scan(&key)
	if err == sql.ErrNoRows {
		return 0, &UnknownAccountError{AccountID: accountID}
	}
	return key, err
}

// journalInstant looks up the recorded `at` for one of transferID's two
// journal legs — both legs share the same at by construction (one
// INSERT, one transaction), so either accountKey's row gives the
// answer. The authoritative execution instant for a dxp-driven
// transfer's own PostCommit rollup fold, re-read from H1 rather than
// trusted from anything captured before commit — matching cal's own
// PostCommit discipline (T-108) exactly, and avoiding what a freshly-
// taken "now" would risk: PostCommit can run some time after the
// transaction it follows, and a delta filed under the wrong bucket
// across a boundary is exactly the kind of drift the rollup oracle
// exists to catch, not something to invite for free.
func (s *Store) journalInstant(ctx context.Context, transferID string, accountKey int64) (time.Time, error) {
	var at time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT at FROM `+s.journalTable()+` WHERE transfer_id = ? AND account_key = ?`,
		transferID, accountKey).Scan(&at)
	if err == sql.ErrNoRows {
		return time.Time{}, fmt.Errorf("bal: journalInstant: no journal entry for transfer %q account_key %d", transferID, accountKey)
	}
	return at, err
}

// RebuildRollup discards and re-derives an account's buckets from the
// journal — the repair path for a rollup lost to a crash between commit
// and emit, and the operation the rebuild oracle's divergence implies.
func (s *Store) RebuildRollup(ctx context.Context, accountID string) error {
	key, err := s.accountKeyOf(ctx, accountID)
	if err != nil {
		return err
	}
	if err := s.deleteAccountBuckets(key); err != nil {
		return err
	}
	eng, err := s.engineFor(key)
	if err != nil {
		return err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT amount, at FROM `+s.journalTable()+`
		 WHERE account_key = ? ORDER BY entry_id`, key)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var amount int64
		var at time.Time
		if err := rows.Scan(&amount, &at); err != nil {
			return err
		}
		eng.Append(amount, at)
	}
	return rows.Err()
}

// RollupOracle proves the derived plane against the authoritative one
// ACROSS THE CASCADE, not merely at the leaf: for every account, the
// week-level fold of the whole journal span must equal the journal's own
// SUM. A cascade that combines correctly at the finest grain but loses a
// carry upward would pass a leaf-only check and fail this one.
func (s *Store) RollupOracle() chronicle.RebuildOracle {
	name := "bal.rollup[" + trimPrefix(s.prefix) + "]"
	return chronicle.RebuildOracle{
		Name: name,
		Derive: func(ctx context.Context) (string, error) {
			return s.rollupFingerprint(ctx, true)
		},
		Current: func(ctx context.Context) (string, error) {
			return s.rollupFingerprint(ctx, false)
		},
	}
}

// rollupFingerprint renders per-account totals either from the journal
// (authoritative) or from the cascade's coarsest fold (derived).
func (s *Store) rollupFingerprint(ctx context.Context, fromJournal bool) (string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT account_key FROM `+s.accountsTable()+` ORDER BY account_key`)
	if err != nil {
		return "", err
	}
	var keys []int64
	for rows.Next() {
		var k int64
		if err := rows.Scan(&k); err != nil {
			_ = rows.Close()
			return "", err
		}
		keys = append(keys, k)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}

	h, err := balGrains()
	if err != nil {
		return "", err
	}
	leaves := h.Leaves()

	out := ""
	for _, k := range keys {
		if fromJournal {
			var v sql.NullInt64
			if err := s.db.QueryRowContext(ctx,
				`SELECT SUM(amount) FROM `+s.journalTable()+` WHERE account_key = ?`, k).Scan(&v); err != nil {
				return "", err
			}
			// The journal side asserts the SAME total once per branch:
			// every branch must independently equal the journal, so a
			// corruption confined to one branch cannot hide behind
			// another. (A single-branch fingerprint would miss a bad
			// week bucket whenever the fold happened to descend the
			// month branch.)
			for _, l := range leaves {
				if v.Int64 != 0 {
					out += fmt.Sprintf("%d %s %d\n", k, h.Grain(l).Name, v.Int64)
				}
			}
			continue
		}
		for _, l := range leaves {
			total := s.foldBranch(k, l)
			if total != 0 {
				out += fmt.Sprintf("%d %s %d\n", k, h.Grain(l).Name, total)
			}
		}
	}
	return out, nil
}

// foldBranch sums an account's whole history along ONE branch of the
// rollup tree, by reading that branch's leaf buckets directly. Reading
// the leaf rather than calling FoldRange is deliberate: FoldRange picks
// whichever branch tiles the window best, so it cannot be used to
// verify a specific branch.
func (s *Store) foldBranch(accountKey int64, level int) int64 {
	prefix := pebbleBucketPrefix(accountKey, int32(level))
	upper := pebbleBucketPrefix(accountKey, int32(level)+1) // next level: exclusive upper bound
	iter, err := s.rollup.db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: upper})
	if err != nil {
		return 0
	}
	defer func() { _ = iter.Close() }()
	var total int64
	for iter.First(); iter.Valid(); iter.Next() {
		val := iter.Value()
		if len(val) != 8 {
			continue
		}
		total += int64(binary.BigEndian.Uint64(val))
	}
	return total
}

func trimPrefix(p string) string {
	if len(p) > 0 && p[len(p)-1] == '_' {
		return p[:len(p)-1]
	}
	return p
}

// CheckpointDivergence records one checkpoint whose frozen balance
// disagrees with the journal's authoritative sum at its boundary.
type CheckpointDivergence struct {
	AccountKey int64
	AtUnix     int64
	Stored     int64
	Journal    int64
}

// VerifyCheckpoints is the T-51 oracle prong: every NON-STALE
// checkpoint must equal SUM(journal WHERE at <= checkpoint boundary).
// This is the check whose absence let a wrong frozen balance ship
// silently.
//
// The stale exemption is now (T-58) a migration-safety net, not the
// everyday case: transferInTx keeps every checkpoint correct by eager
// delta-adjustment as of the moment it's written, so no NEW checkpoint
// is ever stale. The exemption only matters for a checkpoint that was
// marked stale by the OLD (pre-T-58) code path and never recomputed —
// it stays correctly exempted rather than reported as a false
// divergence, and the exemption becomes naturally vacuous as no
// process writes stale=1 anymore. A divergent non-stale checkpoint is
// a defect, unconditionally, under either temporal policy.
// VerifyCheckpoints proves each checkpoint's stored balance against the
// journal ENTRIES IT ACTUALLY OWNS: the delta since the account's
// previous retained checkpoint (zero for the first one seen), not an
// absolute sum from the epoch. This is the "rebuild oracle re-scopes to
// the earliest retained checkpoint" requirement (chronicle-substrate.md
// §4b) — a delta-from-previous check gives an IDENTICAL result to the
// old from-epoch sum when nothing has ever been pruned (there is no
// "previous" to differ from zero), and stays correct once PruneJournal
// removes the entries before an account's earliest retained checkpoint.
// VerifyCheckpoints proves each checkpoint's stored balance against the
// journal ENTRIES IT ACTUALLY OWNS: the delta since the account's
// previous retained checkpoint, not an absolute sum from the epoch.
// This is the "rebuild oracle re-scopes to the earliest retained
// checkpoint" requirement (chronicle-substrate.md §4b) — a
// delta-from-previous check gives an IDENTICAL result to the old
// from-epoch sum when nothing has ever been pruned, and stays correct
// once PruneJournal removes the entries before an account's earliest
// retained checkpoint.
//
// An account's FIRST retained checkpoint is a special case, not just a
// zero baseline: once PruneJournal has run, the journal entries that
// would PROVE that checkpoint correct are gone by design -- that's the
// whole point of retention (§4b: "forgetting is not editing"). This is
// NOT the same as "zero entries because nothing happened before it"
// (a legitimately verifiable case). The two are told apart the only
// way they can be: if ANY journal entry exists at or before the
// checkpoint, there was never any pruning to hide, and the normal
// sum-and-compare applies, still catching real corruption. If NONE
// exist, the checkpoint is trusted as an unverifiable genesis point
// rather than compared against a wrongly-assumed zero -- PruneJournal
// only ever deletes an account's FULL pre-checkpoint range at once
// (never partial), so "zero entries remain" is unambiguous once it's
// true.
func (s *Store) VerifyCheckpoints(ctx context.Context) ([]CheckpointDivergence, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT account_key, at_unix, balance FROM `+s.checkpointsTable()+`
		  WHERE stale = 0 ORDER BY account_key, at_unix`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	type ckpt struct{ key, atUnix, balance int64 }
	var cks []ckpt
	for rows.Next() {
		var c ckpt
		if err := rows.Scan(&c.key, &c.atUnix, &c.balance); err != nil {
			return nil, err
		}
		cks = append(cks, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []CheckpointDivergence
	var prevKey int64 = -1
	var prevAtUnix int64
	var prevBalance int64
	for _, c := range cks {
		var query string
		var args []interface{}
		baseline := int64(0)
		if c.key == prevKey {
			// Not this account's first retained checkpoint: the
			// previous checkpoint's balance is the trusted baseline,
			// and only entries strictly after it are this checkpoint's
			// own delta -- exactly the range PruneJournal leaves intact.
			query = `SELECT COALESCE(SUM(amount), 0) FROM ` + s.journalTable() +
				` WHERE account_key = ? AND at > ? AND at <= ?`
			args = []interface{}{c.key, time.Unix(prevAtUnix, 0).UTC(), time.Unix(c.atUnix, 0).UTC()}
			baseline = prevBalance
		} else {
			// First retained checkpoint for this account: verifiable
			// only if covering evidence still exists.
			var evidenceCount int
			if err := s.db.QueryRowContext(ctx,
				`SELECT COUNT(*) FROM `+s.journalTable()+` WHERE account_key = ? AND at <= ?`,
				c.key, time.Unix(c.atUnix, 0).UTC()).Scan(&evidenceCount); err != nil {
				return nil, err
			}
			if evidenceCount == 0 {
				prevKey, prevAtUnix, prevBalance = c.key, c.atUnix, c.balance
				continue // trusted genesis point: pruned, not corrupt
			}
			query = `SELECT COALESCE(SUM(amount), 0) FROM ` + s.journalTable() + ` WHERE account_key = ? AND at <= ?`
			args = []interface{}{c.key, time.Unix(c.atUnix, 0).UTC()}
		}
		var sum int64
		if err := s.db.QueryRowContext(ctx, query, args...).Scan(&sum); err != nil {
			return nil, err
		}
		if baseline+sum != c.balance {
			out = append(out, CheckpointDivergence{
				AccountKey: c.key, AtUnix: c.atUnix,
				Stored: c.balance, Journal: baseline + sum,
			})
		}
		prevKey, prevAtUnix, prevBalance = c.key, c.atUnix, c.balance
	}
	return out, nil
}
