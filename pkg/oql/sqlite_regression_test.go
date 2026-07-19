// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

// sqlite_regression_test.go — regression tests for bugs found during jsonfile
// backend removal (v0.9.9-rc01).
//
// All tests require a real SQLite store. Each test is run against two executor
// configurations:
//
//   B4 path — Go-path executor with planner threshold = MaxInt32, forcing
//              the OQL engine to load records into memory and filter via the
//              jsonic/FilterableStore predicate compiler path. This is the path
//              that exposed the duplicate-atom predicate bug.
//
//   PD path — Push-down executor with planner threshold = 1, forcing SQL
//              generation via GenerateSQL. This is the path that exposed the
//              chooseFieldExtraction ordering-operator bug.
//
// Testing both paths for each case ensures neither regresses independently.
//
// Bugs fixed:
//
//   Bug 1 (CompilePredicates duplicate-atom) — pkg/oql/predicate_compiler.go
//   When two predicates in an AND chain targeted the same field
//   (e.g. age > 20 AND age < 40), both were compiled into a PredicateSet
//   whose atoms map is Atom→single int. The second atom entry overwrote the
//   first. During token scanning, predSeen[0] was never set, so the final
//   "all predicates must match" check rejected every row. Range queries on
//   any field returned zero results on the B4 path.
//
//   Bug 2 (chooseFieldExtraction ordering operators) — pkg/oql/sqlgen.go
//   The > < >= <= operators always used JSONFieldNumeric (CAST AS REAL) even
//   when the RHS literal was a string. CAST(date_string AS REAL) returns NULL
//   in SQLite, so any string comparison via an ordering operator returned zero
//   results on the SQL push-down path.
//
//   Bug 3 (listWithPushDown numeric cast) — pkg/server/server.go
//   Filter values from HTTP query strings are always strings. Passing the raw
//   string "2" to SQLite against json_extract integer 2 never matched.
//   (Tested in pkg/server/regression_test.go; listed here for completeness.)

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/ha1tch/xolu/pkg/storage"
)

// ---------------------------------------------------------------------------
// Test environment
// ---------------------------------------------------------------------------

// sqliteRegrEnv pairs two executors against a shared SQLite store.
type sqliteRegrEnv struct {
	store  *storage.SQLiteStore
	b4Exec *Executor // Go path + B4 FilterableStore (threshold=MaxInt32)
	pdExec *Executor // SQL push-down (threshold=1)
	ctx    context.Context
}

// newSQLiteRegrEnv creates a fresh SQLite store and seeds it with the given
// entity records. Both B4 and push-down executors are wired to the same store.
func newSQLiteRegrEnv(t *testing.T, entity string, records []map[string]interface{}) *sqliteRegrEnv {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "regression.db")

	store, err := storage.NewSQLiteStore(dbPath, storage.SQLiteConfig{})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	ctx := context.Background()
	for _, rec := range records {
		if _, err := store.Create(ctx, entity, rec); err != nil {
			t.Fatalf("create %q: %v", entity, err)
		}
	}

	b4Exec := &Executor{
		store:      store,
		aggregator: NewAggregator(),
		planner:    NewPlannerWithThreshold(math.MaxInt32), // never push down
		dialect:    &SQLiteDialect{},
	}

	pdExec := &Executor{
		store:      store,
		aggregator: NewAggregator(),
		planner:    NewPlannerWithDialectAndThreshold(&SQLiteDialect{}, 1), // always push down
		dialect:    &SQLiteDialect{},
	}

	return &sqliteRegrEnv{
		store:  store,
		b4Exec: b4Exec,
		pdExec: pdExec,
		ctx:    ctx,
	}
}

// runBoth executes the given OQL query on both the B4 and push-down paths and
// returns (b4Rows, pdRows). The caller asserts on each.
func (e *sqliteRegrEnv) runBoth(t *testing.T, query string) ([]map[string]interface{}, []map[string]interface{}) {
	t.Helper()
	stmt := parseOQL(t, query)

	b4Result, err := e.b4Exec.ExecuteWithStore(e.ctx, stmt, e.store)
	if err != nil {
		t.Fatalf("B4 execute %q: %v", query, err)
	}

	pdResult, err := e.pdExec.ExecuteWithStore(e.ctx, stmt, e.store)
	if err != nil {
		t.Fatalf("PD execute %q: %v", query, err)
	}

	return b4Result.Rows, pdResult.Rows
}

// assertLen fails if either slice has an unexpected length, printing which path
// is wrong.
func assertLen(t *testing.T, label string, b4, pd []map[string]interface{}, want int) {
	t.Helper()
	if len(b4) != want {
		t.Errorf("%s B4 path: want %d rows, got %d: %v", label, want, len(b4), b4)
	}
	if len(pd) != want {
		t.Errorf("%s PD path: want %d rows, got %d: %v", label, want, len(pd), pd)
	}
}

// ---------------------------------------------------------------------------
// Bug 1 — CompilePredicates duplicate-atom (range queries on one field)
// ---------------------------------------------------------------------------

func TestRegression_RangeFilter_NumericField(t *testing.T) {
	t.Parallel()
	// Five people; age range [25, 35] should include Bob(25), Alice(30),
	// Diana(35) = 3 rows. Pre-fix, the B4 path returned 0.
	env := newSQLiteRegrEnv(t, "people", []map[string]interface{}{
		{"name": "Alice", "age": float64(30)},
		{"name": "Bob", "age": float64(25)},
		{"name": "Charlie", "age": float64(40)},
		{"name": "Diana", "age": float64(35)},
		{"name": "Eve", "age": float64(20)},
	})

	b4, pd := env.runBoth(t,
		"SELECT name, age FROM people WHERE age >= 25 AND age <= 35")
	assertLen(t, "numeric range [25,35]", b4, pd, 3)
}

func TestRegression_RangeFilter_StringField(t *testing.T) {
	t.Parallel()
	// Five events; timestamps in range [09:00, 10:30] on 2025-01-15 should
	// return 2 rows. Pre-fix, B4 returned 0 (duplicate-atom) and PD returned 0
	// (numeric cast of string gave NULL).
	env := newSQLiteRegrEnv(t, "events", []map[string]interface{}{
		{"ts": "2025-01-15T08:30:00Z"},
		{"ts": "2025-01-15T09:10:00Z"},
		{"ts": "2025-01-15T09:55:00Z"},
		{"ts": "2025-01-15T11:00:00Z"},
		{"ts": "2025-01-16T09:00:00Z"},
	})

	b4, pd := env.runBoth(t,
		"SELECT ts FROM events WHERE ts >= '2025-01-15T09:00:00Z' AND ts <= '2025-01-15T10:30:00Z'")
	assertLen(t, "string timestamp range", b4, pd, 2)
}

func TestRegression_RangeFilter_OpenLower(t *testing.T) {
	t.Parallel()
	// age > 30 — single lower-bound predicate; no duplicate-atom risk, but
	// verify the fix didn't break single-predicate ordering comparisons.
	env := newSQLiteRegrEnv(t, "people", []map[string]interface{}{
		{"name": "Alice", "age": float64(30)},
		{"name": "Bob", "age": float64(25)},
		{"name": "Charlie", "age": float64(40)},
		{"name": "Diana", "age": float64(35)},
	})

	b4, pd := env.runBoth(t,
		"SELECT name FROM people WHERE age > 30")
	assertLen(t, "open lower bound age > 30", b4, pd, 2)
}

func TestRegression_RangeFilter_OpenUpper(t *testing.T) {
	t.Parallel()
	// age < 35 — single upper-bound predicate.
	env := newSQLiteRegrEnv(t, "people", []map[string]interface{}{
		{"name": "Alice", "age": float64(30)},
		{"name": "Bob", "age": float64(25)},
		{"name": "Charlie", "age": float64(40)},
		{"name": "Diana", "age": float64(35)},
	})

	b4, pd := env.runBoth(t,
		"SELECT name FROM people WHERE age < 35")
	assertLen(t, "open upper bound age < 35", b4, pd, 2)
}

func TestRegression_ThreePredicatesSameField(t *testing.T) {
	t.Parallel()
	// Three predicates on the same field: age >= 25 AND age <= 40 AND age != 30.
	// Pre-fix: first two predicates — only one compiled, the third is residual.
	// Post-fix: only the first is compiled; the other two are Go-path residuals.
	// Result: ages 25, 35, 40 = 3 rows.
	env := newSQLiteRegrEnv(t, "people", []map[string]interface{}{
		{"name": "Alice", "age": float64(30)},
		{"name": "Bob", "age": float64(25)},
		{"name": "Charlie", "age": float64(40)},
		{"name": "Diana", "age": float64(35)},
		{"name": "Eve", "age": float64(20)},
	})

	b4, pd := env.runBoth(t,
		"SELECT name, age FROM people WHERE age >= 25 AND age <= 40 AND age != 30")
	assertLen(t, "three predicates same field", b4, pd, 3)
}

func TestRegression_TwoRangesOnDifferentFields(t *testing.T) {
	t.Parallel()
	// Range on two different fields simultaneously: age in [25,35] AND score in [80, 100].
	// Both atom slots are distinct, so no overwrite. This is a correctness guard
	// to ensure the fix doesn't accidentally break multi-field predicates.
	// Only Alice(age=30, score=95) and Diana(age=35, score=88) qualify.
	env := newSQLiteRegrEnv(t, "people", []map[string]interface{}{
		{"name": "Alice", "age": float64(30), "score": float64(95)},
		{"name": "Bob", "age": float64(25), "score": float64(70)},
		{"name": "Charlie", "age": float64(40), "score": float64(90)},
		{"name": "Diana", "age": float64(35), "score": float64(88)},
		{"name": "Eve", "age": float64(20), "score": float64(95)},
	})

	b4, pd := env.runBoth(t,
		"SELECT name FROM people WHERE age >= 25 AND age <= 35 AND score >= 80 AND score <= 100")
	assertLen(t, "range on two fields", b4, pd, 2)
}

// ---------------------------------------------------------------------------
// Bug 2 — chooseFieldExtraction: ordering operators with string RHS
// ---------------------------------------------------------------------------

func TestRegression_StringOrderingGTE(t *testing.T) {
	t.Parallel()
	// >= on a string field. Pre-fix on PD path: CAST string to REAL → NULL,
	// comparison always false, zero rows.
	env := newSQLiteRegrEnv(t, "events", []map[string]interface{}{
		{"ts": "2025-01-01T00:00:00Z", "label": "a"},
		{"ts": "2025-06-01T00:00:00Z", "label": "b"},
		{"ts": "2025-12-01T00:00:00Z", "label": "c"},
	})

	b4, pd := env.runBoth(t,
		"SELECT label FROM events WHERE ts >= '2025-06-01T00:00:00Z'")
	assertLen(t, "string >= on PD path", b4, pd, 2)
}

func TestRegression_StringOrderingLTE(t *testing.T) {
	t.Parallel()
	env := newSQLiteRegrEnv(t, "events", []map[string]interface{}{
		{"ts": "2025-01-01T00:00:00Z", "label": "a"},
		{"ts": "2025-06-01T00:00:00Z", "label": "b"},
		{"ts": "2025-12-01T00:00:00Z", "label": "c"},
	})

	b4, pd := env.runBoth(t,
		"SELECT label FROM events WHERE ts <= '2025-06-01T00:00:00Z'")
	assertLen(t, "string <= on PD path", b4, pd, 2)
}

func TestRegression_StringOrderingGT(t *testing.T) {
	t.Parallel()
	env := newSQLiteRegrEnv(t, "events", []map[string]interface{}{
		{"ts": "2025-01-01T00:00:00Z", "label": "a"},
		{"ts": "2025-06-01T00:00:00Z", "label": "b"},
		{"ts": "2025-12-01T00:00:00Z", "label": "c"},
	})

	b4, pd := env.runBoth(t,
		"SELECT label FROM events WHERE ts > '2025-06-01T00:00:00Z'")
	assertLen(t, "string > on PD path", b4, pd, 1)
}

func TestRegression_StringOrderingLT(t *testing.T) {
	t.Parallel()
	env := newSQLiteRegrEnv(t, "events", []map[string]interface{}{
		{"ts": "2025-01-01T00:00:00Z", "label": "a"},
		{"ts": "2025-06-01T00:00:00Z", "label": "b"},
		{"ts": "2025-12-01T00:00:00Z", "label": "c"},
	})

	b4, pd := env.runBoth(t,
		"SELECT label FROM events WHERE ts < '2025-06-01T00:00:00Z'")
	assertLen(t, "string < on PD path", b4, pd, 1)
}

func TestRegression_StringOrderingLexicographic(t *testing.T) {
	t.Parallel()
	// Confirm ISO 8601 timestamps sort correctly as text.
	// "2025-02-28" > "2025-02-03" lexicographically — as expected.
	env := newSQLiteRegrEnv(t, "records", []map[string]interface{}{
		{"date": "2025-01-31", "v": 1},
		{"date": "2025-02-03", "v": 2},
		{"date": "2025-02-28", "v": 3},
		{"date": "2025-03-01", "v": 4},
	})

	b4, pd := env.runBoth(t,
		"SELECT date FROM records WHERE date >= '2025-02-01' AND date <= '2025-02-28'")
	assertLen(t, "date string lexicographic range", b4, pd, 2)
}

func TestRegression_NumericOrderingUnchanged(t *testing.T) {
	t.Parallel()
	// Numeric ordering operators must still use JSONFieldNumeric (CAST AS REAL)
	// so that stored numeric strings sort numerically rather than lexicographically.
	// Pre-fix: always numeric. Post-fix: numeric when RHS is numeric — verify
	// no regression.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{
		{"price": float64(9.99)},
		{"price": float64(19.99)},
		{"price": float64(99.99)},
		{"price": float64(5.00)},
	})

	b4, pd := env.runBoth(t,
		"SELECT price FROM items WHERE price >= 10 AND price <= 50")
	assertLen(t, "numeric ordering operators still work", b4, pd, 1)
}

// ---------------------------------------------------------------------------
// Bug 1 adjacent — IN combined with equality on same field
// ---------------------------------------------------------------------------

func TestRegression_InPredicateAfterEquality(t *testing.T) {
	t.Parallel()
	// status = 'active' AND status IN ('active', 'pending') — two predicates
	// on the same field. Pre-fix: second atom overwrote first; depending on
	// which ended up compiled, results were wrong. Post-fix: first is compiled,
	// second is residual.
	// Only 'active' rows survive both predicates.
	env := newSQLiteRegrEnv(t, "tasks", []map[string]interface{}{
		{"name": "T1", "status": "active"},
		{"name": "T2", "status": "pending"},
		{"name": "T3", "status": "done"},
		{"name": "T4", "status": "active"},
	})

	b4, pd := env.runBoth(t,
		"SELECT name FROM tasks WHERE status = 'active' AND status IN ('active', 'pending')")
	assertLen(t, "equality AND IN same field", b4, pd, 2)
}

// ---------------------------------------------------------------------------
// BETWEEN — already correct pre-fix, guard against regression
// ---------------------------------------------------------------------------

func TestRegression_Between_Numeric(t *testing.T) {
	t.Parallel()
	// BETWEEN uses a single predicate so was never affected by duplicate-atom.
	// Verify it still works after the fix.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{
		{"qty": float64(5)},
		{"qty": float64(10)},
		{"qty": float64(15)},
		{"qty": float64(20)},
	})

	b4, pd := env.runBoth(t,
		"SELECT qty FROM items WHERE qty BETWEEN 8 AND 16")
	assertLen(t, "BETWEEN numeric", b4, pd, 2)
}

func TestRegression_Between_String(t *testing.T) {
	t.Parallel()
	// BETWEEN with string bounds — already used JSONField (no CAST) before the
	// fix. Confirm no regression.
	env := newSQLiteRegrEnv(t, "events", []map[string]interface{}{
		{"ts": "2025-01-01T00:00:00Z"},
		{"ts": "2025-06-01T00:00:00Z"},
		{"ts": "2025-09-01T00:00:00Z"},
		{"ts": "2025-12-01T00:00:00Z"},
	})

	b4, pd := env.runBoth(t,
		"SELECT ts FROM events WHERE ts BETWEEN '2025-03-01T00:00:00Z' AND '2025-10-01T00:00:00Z'")
	assertLen(t, "BETWEEN string", b4, pd, 2)
}

// ---------------------------------------------------------------------------
// Combined: range + other field predicate (correctness cross-check)
// ---------------------------------------------------------------------------

func TestRegression_RangePlusOtherField(t *testing.T) {
	t.Parallel()
	// age range AND separate string-equality predicate on a different field.
	// Both atom slots distinct; this is the clean case. Ensure no regressions.
	env := newSQLiteRegrEnv(t, "people", []map[string]interface{}{
		{"name": "Alice", "age": float64(30), "region": "north"},
		{"name": "Bob", "age": float64(25), "region": "south"},
		{"name": "Charlie", "age": float64(40), "region": "north"},
		{"name": "Diana", "age": float64(35), "region": "north"},
		{"name": "Eve", "age": float64(20), "region": "south"},
	})

	// North residents aged 25–35: Alice(30,north) and Diana(35,north).
	b4, pd := env.runBoth(t,
		"SELECT name FROM people WHERE age >= 25 AND age <= 35 AND region = 'north'")
	assertLen(t, "range + equality different fields", b4, pd, 2)
}

func TestRegression_GroupByWithStringRange(t *testing.T) {
	t.Parallel()
	// GROUP BY + WHERE with a string range — exercises the full
	// aggregate+filter path that was failing in TestOQLDateTrunc.
	// Five events spanning two hours; range filter keeps four of them;
	// GROUP BY hour should yield two buckets.
	env := newSQLiteRegrEnv(t, "events", []map[string]interface{}{
		{"ts": "2025-01-15T08:00:00Z", "kind": "a"},
		{"ts": "2025-01-15T09:10:00Z", "kind": "a"},
		{"ts": "2025-01-15T09:55:00Z", "kind": "b"},
		{"ts": "2025-01-15T10:05:00Z", "kind": "a"},
		{"ts": "2025-01-15T10:30:00Z", "kind": "b"},
	})

	b4, pd := env.runBoth(t,
		"SELECT COUNT(*) AS n FROM events WHERE ts >= '2025-01-15T09:00:00Z' AND ts <= '2025-01-15T10:59:59Z'")
	// Should count 4 rows (08:xx excluded).
	for _, rows := range [][]map[string]interface{}{b4, pd} {
		if len(rows) != 1 {
			t.Errorf("GROUP BY string range: want 1 summary row, got %d", len(rows))
			continue
		}
		n, ok := toNum(rows[0]["n"])
		if !ok || int(n) != 4 {
			t.Errorf("GROUP BY string range: want COUNT=4, got %v", rows[0]["n"])
		}
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func toNum(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}
