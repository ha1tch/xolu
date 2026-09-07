// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package tenantexport

// summary.go — a single-round-trip tenant-emptiness/data-presence
// summary (XOT213, wave 15), requested by the xoluman team so its own
// seedapply.CheckEmpty stops making seven separate round trips for
// one yes/no question and stops hand-maintaining its own,
// independently-drifting notion of "what counts as tenant data."
//
// Deliberately reuses every piece of this package's own already-
// audited machinery rather than re-deriving any of it:
//   - PrimaryStoreTables / LocStoreTables / ObjStoreTables: the same
//     three table lists ExportTenant itself iterates. Not a fourth,
//     independently-maintained list.
//   - SQLiteTableSpec.tableName() and ExportSQLiteTable's own
//     WHERE-clause construction rule (TenantFiltered, only when
//     NameFunc is nil): reused via countTable below, never
//     re-derived. NameFunc's own doc comment names two real bugs a
//     hand-rolled version of this exact resolution logic already
//     caused in this package -- that history is the reason this file
//     calls spec.tableName() rather than reimplementing it.
//   - isNoSuchTableError: a table that doesn't exist yet (some are
//     created lazily on first use, not eagerly at tenant boot) is
//     legitimately empty, matching ExportSQLiteTable's own treatment.
//   - The exact same dedicated-file-existence check exportDedicatedFile
//     uses for loc.db/obj.db, and the exact same missing-directory
//     check ExportPebbleStores uses for the ts/cal/bal Pebble stores.
//
// What this file adds that didn't already exist: countTable and
// countPebbleStore, lightweight COUNT-only siblings of
// ExportSQLiteTable/ExportPebbleStore that answer "how many rows/keys"
// without writing anything to disk -- a summary has no reason to
// produce export files, and doing so anyway would make what's meant
// to be a fast check needlessly do real I/O work proportional to a
// tenant's own total data volume.

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cockroachdb/pebble"

	"github.com/ha1tch/xolu/pkg/blob"
	"github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// TenantDataSummary is per-store row/key counts across every place a
// tenant's own data can live -- the same set ExportTenant itself
// captures, no more and no less. A tenant summary where Empty()
// reports true is empty by that same real definition, not a fifth,
// separately-maintained notion of what counts.
type TenantDataSummary struct {
	// Primary holds one entry per PrimaryStoreTables spec, keyed by
	// spec.Name.
	Primary map[string]int `json:"primary"`
	// Loc holds one entry per LocStoreTables spec (loc.db). Present
	// but zero-valued for every entry when the tenant has never used
	// /loc at all (loc.db doesn't exist yet) -- distinct from a
	// tenant that used /loc and then deleted everything, which this
	// summary cannot and does not try to distinguish.
	Loc map[string]int `json:"loc"`
	// Obj holds one entry per ObjStoreTables spec (obj.db) -- this is
	// what closes XOT209's own emptiness-check gap for obj, ahead of
	// or independent of that item's own general REST enumeration
	// endpoint.
	Obj map[string]int `json:"obj"`
	// TS, CalIndex, and BalRollup are key counts in the three
	// per-tenant Pebble stores ExportTenant itself exports
	// (storelayout.TenantTSDir/TenantCalDir/TenantBalRollupDir).
	TS        int `json:"ts"`
	CalIndex  int `json:"cal_index"`
	BalRollup int `json:"bal_rollup"`
	// Blob is the tenant's own uploaded blob key count -- NOT part of
	// ExportTenant's own captured set (blob.Store there is only ever
	// the destination for a packaged export, never a source of
	// counted data; see this file's own XOT216 note on Summarize).
	// Always 0 when Summarize's own bs parameter is nil (blob
	// disabled on this server, or unavailable for this call) -- a
	// disabled primitive summarizing to zero, matching every other
	// per-store field's own "never used this primitive" treatment,
	// not a sentinel meaning "unknown."
	Blob int `json:"blob"`
}

// Empty reports whether every count in s is genuinely zero -- the
// single boolean xoluman's own document named as the minimum
// contract, alongside the full per-store breakdown its document had a
// mild preference for ("would be more useful"). This summary provides
// both from one computation rather than forcing a caller to choose.
func (s *TenantDataSummary) Empty() bool {
	for _, n := range s.Primary {
		if n > 0 {
			return false
		}
	}
	for _, n := range s.Loc {
		if n > 0 {
			return false
		}
	}
	for _, n := range s.Obj {
		if n > 0 {
			return false
		}
	}
	return s.TS == 0 && s.CalIndex == 0 && s.BalRollup == 0 && s.Blob == 0
}

// Summarize computes tenantID's own data-presence summary in one
// pass across every store ExportTenant itself would capture. One
// round trip for a caller, not the several separate primitive-by-
// primitive calls (or, for obj specifically, no call at all -- see
// XOT209) a caller would otherwise need to answer the same question.
//
// NOT atomic across the stores it queries, and cannot be made so --
// confirmed directly, not left as an unstated assumption, by
// TestSummarize_ConcurrentWriteRace: a write landing in an
// already-read store during the call is invisible to that call's own
// result, and Empty() can genuinely report true for a tenant that, by
// the moment the call returns, already holds real data. This is
// inherent to querying independent storage engines (SQL, two
// dedicated SQLite files, three Pebble stores, blob storage) with no
// shared transaction possible across all of them -- not a bug in any
// one query's own logic, and not fixable by tightening one of them.
// A caller should treat the result as a point-in-time snapshot, not a
// live guarantee -- correct in the absence of concurrent writes to
// the tenant during the call, which is the expected case for a
// pre-seed emptiness check (xoluman's own motivating use case) but
// not a property this function can enforce on a caller's behalf.
//
// bs is the tenant's own already-opened blob store (XOT216) -- nil
// when blob is disabled server-wide or otherwise unavailable for this
// call, in which case Blob is left at 0 rather than this function
// failing outright. Mirrors ExportTenant's own bs parameter shape,
// but nilable here where ExportTenant's own is not: blob storage IS
// that function's actual output destination, so a nil there would be
// a real error; here it's just one more thing being counted, and its
// own absence is exactly as legitimate a "not used" case as an absent
// loc.db or an absent ts Pebble directory.
// testHookAfterPrimaryRead is called (if non-nil) immediately after
// Summarize finishes reading PrimaryStoreTables, before it reads
// anything else. Exists ONLY to make an adversarial concurrency test
// deterministic rather than a many-iterations probabilistic one --
// nil on every real call path (never set outside a _test.go file),
// so a nil check plus a no-op call is the only production cost, and
// that cost is unconditional regardless of whether tests ever run.
var testHookAfterPrimaryRead func()

func Summarize(ctx context.Context, primaryDB *sql.DB, basePath string, tenantID tenant.TenantID, bs *blob.Store) (*TenantDataSummary, error) {
	tid := uint16(tenantID)
	summary := &TenantDataSummary{
		Primary: make(map[string]int, len(PrimaryStoreTables)),
		Loc:     make(map[string]int, len(LocStoreTables)),
		Obj:     make(map[string]int, len(ObjStoreTables)),
	}

	for _, spec := range PrimaryStoreTables {
		n, err := countTable(ctx, primaryDB, tid, spec)
		if err != nil {
			return nil, fmt.Errorf("tenantexport: primary store: %w", err)
		}
		summary.Primary[spec.Name] = n
	}
	if testHookAfterPrimaryRead != nil {
		testHookAfterPrimaryRead()
	}

	if err := countDedicatedFile(ctx, storelayout.TenantLocDir(basePath, tenantID), "loc.db", LocStoreTables, tid, summary.Loc); err != nil {
		return nil, fmt.Errorf("tenantexport: loc store: %w", err)
	}
	if err := countDedicatedFile(ctx, storelayout.TenantObjDir(basePath, tenantID), "obj.db", ObjStoreTables, tid, summary.Obj); err != nil {
		return nil, fmt.Errorf("tenantexport: obj store: %w", err)
	}

	var err error
	if summary.TS, err = countPebbleStore(ctx, storelayout.TenantTSDir(basePath, tenantID)); err != nil {
		return nil, fmt.Errorf("tenantexport: ts store: %w", err)
	}
	if summary.CalIndex, err = countPebbleStore(ctx, storelayout.TenantCalDir(basePath, tenantID)); err != nil {
		return nil, fmt.Errorf("tenantexport: cal index: %w", err)
	}
	if summary.BalRollup, err = countPebbleStore(ctx, storelayout.TenantBalRollupDir(basePath, tenantID)); err != nil {
		return nil, fmt.Errorf("tenantexport: bal rollup: %w", err)
	}

	if bs != nil {
		keys, err := bs.List("")
		if err != nil {
			return nil, fmt.Errorf("tenantexport: blob store: %w", err)
		}
		summary.Blob = len(keys)
	}

	return summary, nil
}

// countTable returns spec's own row count, reusing spec.tableName()
// and ExportSQLiteTable's own WHERE-clause construction rule exactly
// -- never re-derived (see this file's own header comment for why).
func countTable(ctx context.Context, db *sql.DB, tenantID uint16, spec SQLiteTableSpec) (int, error) {
	realTable := spec.tableName(tenantID)
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", realTable)
	var args []interface{}
	if spec.NameFunc == nil && spec.TenantFiltered {
		query += " WHERE tenant_id = ?"
		args = append(args, tenantID)
	}
	var n int
	if err := db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		if isNoSuchTableError(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("tenantexport: count %s: %w", realTable, err)
	}
	return n, nil
}

// countDedicatedFile fills counts with one entry per spec in specs,
// querying dbFileName inside primitiveDir -- the exact same
// file-existence check exportDedicatedFile uses: a primitive this
// tenant has never used (the dedicated db file doesn't exist) fills
// every entry with 0, not an error.
func countDedicatedFile(ctx context.Context, primitiveDir, dbFileName string, specs []SQLiteTableSpec, tenantID uint16, counts map[string]int) error {
	for _, spec := range specs {
		counts[spec.Name] = 0
	}
	dbPath := filepath.Join(primitiveDir, dbFileName)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	}
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer func() { _ = db.Close() }()

	for _, spec := range specs {
		n, err := countTable(ctx, db, tenantID, spec)
		if err != nil {
			return err
		}
		counts[spec.Name] = n
	}
	return nil
}

// countPebbleStore returns the key count for the Pebble store at dir
// -- the exact same missing-directory check ExportPebbleStores uses:
// a primitive this tenant has never used (the directory doesn't
// exist) is legitimately empty, not an error, and dir is never opened
// via pebble.Open in that case (which would create an empty directory
// as a side effect of merely checking).
func countPebbleStore(ctx context.Context, dir string) (int, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return 0, nil
	}
	db, err := pebble.Open(dir, &pebble.Options{ReadOnly: true})
	if err != nil {
		return 0, fmt.Errorf("tenantexport: open pebble store %s: %w", dir, err)
	}
	defer func() { _ = db.Close() }()

	iter, err := db.NewIter(nil)
	if err != nil {
		return 0, fmt.Errorf("tenantexport: iterate %s: %w", dir, err)
	}
	defer func() { _ = iter.Close() }()

	n := 0
	for valid := iter.First(); valid; valid = iter.Next() {
		if err := ctx.Err(); err != nil {
			return n, fmt.Errorf("tenantexport: %s count cancelled after %d keys: %w", dir, n, err)
		}
		n++
	}
	return n, nil
}
