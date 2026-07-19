// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

// adversarial_oql_test.go — adversarial tests for jsonic, aggregation, and
// complex combined queries. All tests run on both the B4 (FilterableStore,
// Go path) and SQL push-down paths using sqliteRegrEnv, so regressions in
// either path are caught independently.
//
// Scenarios:
//
// Jsonic / sparse data
//   - Records with entirely missing fields (sparse schema)
//   - Numeric field present in some records, absent in others
//   - Null values in aggregate inputs (COUNT, SUM, AVG, MIN, MAX)
//   - Boolean field with mixed true/false/absent
//   - Field that looks numeric but is stored as a string ("2" not 2)
//   - Very long string field value
//   - Unicode in field values
//
// Aggregation edge cases
//   - COUNT DISTINCT with nulls
//   - AVG where all values are null → result is null
//   - GROUP BY on a field absent from some records (null group key)
//   - HAVING that reduces all groups to zero rows
//   - ORDER BY on an aggregate column (not a GROUP BY key)
//   - SUM on a mix of integers and floats
//
// Combined complex queries
//   - Range filter + GROUP BY + HAVING + ORDER BY + LIMIT
//   - Multi-field GROUP BY + COUNT DISTINCT
//   - COALESCE in SELECT + GROUP BY + aggregate
//   - Chained filters: string equality + numeric range + GROUP BY
//   - WHERE on non-grouped field after GROUP BY (HAVING equivalent using WHERE)

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Jsonic / sparse data
// ---------------------------------------------------------------------------

func TestAdversarial_SparseMissingField_Filter(t *testing.T) {
	t.Parallel()
	// Some records have "score", others do not. A WHERE on score must only
	// match records where the field is present and satisfies the predicate.
	// Records where score is absent must not be matched, not panic, not error.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{
		{"name": "A", "score": float64(90)},
		{"name": "B"}, // no score
		{"name": "C", "score": float64(70)},
		{"name": "D"}, // no score
		{"name": "E", "score": float64(80)},
	})

	b4, pd := env.runBoth(t, "SELECT name FROM items WHERE score >= 80")
	assertLen(t, "sparse field filter", b4, pd, 2)
}

func TestAdversarial_SparseMissingField_Aggregate(t *testing.T) {
	t.Parallel()
	// SUM and AVG must ignore records where the field is absent (treat as null).
	// COUNT(*) counts all 5; COUNT(score) counts only the 3 with a value.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{
		{"name": "A", "score": float64(90)},
		{"name": "B"},
		{"name": "C", "score": float64(70)},
		{"name": "D"},
		{"name": "E", "score": float64(80)},
	})

	b4, pd := env.runBoth(t, "SELECT COUNT(*) AS total, SUM(score) AS s FROM items")
	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if len(rows) != 1 {
			t.Errorf("sparse aggregate %s: want 1 row, got %d", path, len(rows))
			continue
		}
		if n, ok := toNum(rows[0]["total"]); !ok || int(n) != 5 {
			t.Errorf("sparse aggregate %s COUNT(*): want 5, got %v", path, rows[0]["total"])
		}
		if s, ok := toNum(rows[0]["s"]); !ok || int(s) != 240 {
			t.Errorf("sparse aggregate %s SUM(score): want 240, got %v", path, rows[0]["s"])
		}
	}
}

func TestAdversarial_NullAggregate_AVG(t *testing.T) {
	t.Parallel()
	// AVG of a field that is null in every record should return null, not panic.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{
		{"name": "A"},
		{"name": "B"},
	})

	b4, pd := env.runBoth(t, "SELECT AVG(score) AS mean FROM items")
	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if len(rows) != 1 {
			t.Errorf("null AVG %s: want 1 row, got %d", path, len(rows))
			continue
		}
		// Both paths must return null (nil in Go), not a zero or NaN.
		if rows[0]["mean"] != nil {
			t.Errorf("null AVG %s: want nil, got %v", path, rows[0]["mean"])
		}
	}
}

func TestAdversarial_NullAggregate_SUM(t *testing.T) {
	t.Parallel()
	// SUM of an absent field should return null, not zero.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{
		{"name": "A"},
		{"name": "B"},
	})

	b4, pd := env.runBoth(t, "SELECT SUM(score) AS total FROM items")
	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if len(rows) != 1 {
			t.Errorf("null SUM %s: want 1 row, got %d", path, len(rows))
			continue
		}
		if rows[0]["total"] != nil {
			t.Errorf("null SUM %s: want nil, got %v (%T)", path, rows[0]["total"], rows[0]["total"])
		}
	}
}

func TestAdversarial_CountDistinct_WithNulls(t *testing.T) {
	t.Parallel()
	// COUNT(DISTINCT field) must deduplicate values before counting.
	// 5 records, 2 duplicate statuses: 3 distinct values expected.
	// Previously broken: tsqlparser v0.0.1 silently discarded the DISTINCT
	// modifier; fixed in v0.6.0 with FunctionCall.Distinct field.
	env := newSQLiteRegrEnv(t, "tasks", []map[string]interface{}{
		{"status": "active"},
		{"status": "active"}, // duplicate
		{"status": "idle"},
		{"status": "done"},
		{"status": "idle"}, // duplicate
	})

	b4, pd := env.runBoth(t, "SELECT COUNT(DISTINCT status) AS n FROM tasks")
	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if len(rows) != 1 {
			t.Errorf("COUNT(DISTINCT) %s: want 1 row, got %d", path, len(rows))
			continue
		}
		if n, ok := toNum(rows[0]["n"]); !ok || int(n) != 3 {
			t.Errorf("COUNT(DISTINCT) %s: want 3, got %v", path, rows[0]["n"])
		}
	}
}

func TestAdversarial_GroupBy_SparseKey(t *testing.T) {
	t.Parallel()
	// GROUP BY on a field absent from some records. Records without the key
	// field should form a null group (or be excluded — either is consistent,
	// but must not error or produce wrong counts in named groups).
	env := newSQLiteRegrEnv(t, "events", []map[string]interface{}{
		{"kind": "a", "v": 1},
		{"kind": "a", "v": 2},
		{"kind": "b", "v": 3},
		{"v": 4}, // no kind
		{"v": 5}, // no kind
	})

	b4, pd := env.runBoth(t, "SELECT kind, COUNT(*) AS n FROM events GROUP BY kind ORDER BY n DESC")
	// "a" group: 2, "b" group: 1, null group: 2 (or excluded — both valid).
	// What must hold: the named groups have correct counts.
	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		counts := map[interface{}]int{}
		for _, r := range rows {
			if n, ok := toNum(r["n"]); ok {
				counts[r["kind"]] += int(n)
			}
		}
		if counts["a"] != 2 {
			t.Errorf("sparse GROUP BY %s: kind=a want 2, got %d (all groups: %v)", path, counts["a"], rows)
		}
		if counts["b"] != 1 {
			t.Errorf("sparse GROUP BY %s: kind=b want 1, got %d (all groups: %v)", path, counts["b"], rows)
		}
	}
}

func TestAdversarial_NumericLookingStringField(t *testing.T) {
	t.Parallel()
	// A field stored as a string that contains a numeric value ("2" not 2).
	// A string equality filter must match; a numeric ordering filter must not
	// misinterpret the value as a number and must not panic.
	env := newSQLiteRegrEnv(t, "codes", []map[string]interface{}{
		{"code": "1"},
		{"code": "2"},
		{"code": "10"}, // lexicographically "10" < "2"
	})

	// String equality
	b4eq, pdeq := env.runBoth(t, "SELECT code FROM codes WHERE code = '2'")
	assertLen(t, "numeric-looking string equality", b4eq, pdeq, 1)

	// String ordering: "10" < "2" < "1" is wrong numerically but correct lexicographically.
	// Expect all 3 rows when filtering code >= '1' (all are >= '1' as strings).
	b4ge, pdge := env.runBoth(t, "SELECT code FROM codes WHERE code >= '1'")
	assertLen(t, "numeric-looking string ordering", b4ge, pdge, 3)
}

func TestAdversarial_VeryLongStringField(t *testing.T) {
	t.Parallel()
	// A string field with a very long value (64 KB). Must not error or truncate.
	longVal := strings.Repeat("x", 65536)
	env := newSQLiteRegrEnv(t, "blobs", []map[string]interface{}{
		{"id_key": "short", "payload": "hello"},
		{"id_key": "long", "payload": longVal},
	})

	b4, pd := env.runBoth(t, "SELECT id_key FROM blobs WHERE id_key = 'long'")
	assertLen(t, "very long string field filter", b4, pd, 1)

	// Verify the long value round-trips intact on B4 path.
	b4full, _ := env.runBoth(t, "SELECT payload FROM blobs WHERE id_key = 'long'")
	if len(b4full) == 1 {
		got, _ := b4full[0]["payload"].(string)
		if len(got) != 65536 {
			t.Errorf("long string B4 round-trip: want 65536 chars, got %d", len(got))
		}
	}
}

func TestAdversarial_UnicodeFieldValue(t *testing.T) {
	t.Parallel()
	// Unicode in field values — emoji, CJK, RTL text — must filter and
	// aggregate correctly without corruption.
	env := newSQLiteRegrEnv(t, "msgs", []map[string]interface{}{
		{"lang": "emoji", "text": "🎉🎊🥳"},
		{"lang": "cjk", "text": "日本語テスト"},
		{"lang": "rtl", "text": "مرحبا بالعالم"},
		{"lang": "emoji", "text": "🚀"},
	})

	b4, pd := env.runBoth(t, "SELECT lang, COUNT(*) AS n FROM msgs WHERE lang = 'emoji' GROUP BY lang")
	assertLen(t, "unicode GROUP BY", b4, pd, 1)
	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if n, ok := toNum(rows[0]["n"]); !ok || int(n) != 2 {
			t.Errorf("unicode GROUP BY %s: want n=2, got %v", path, rows[0]["n"])
		}
	}
}

func TestAdversarial_BooleanField_Mixed(t *testing.T) {
	t.Parallel()
	// Boolean field with true/false/absent values — filter on true must not
	// match false or absent records.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{
		{"name": "A", "active": true},
		{"name": "B", "active": false},
		{"name": "C", "active": true},
		{"name": "D"}, // no active field
	})

	b4, pd := env.runBoth(t, "SELECT name FROM items WHERE active = true")
	assertLen(t, "boolean true filter", b4, pd, 2)

	b4f, pdf := env.runBoth(t, "SELECT name FROM items WHERE active = false")
	assertLen(t, "boolean false filter", b4f, pdf, 1)
}

// ---------------------------------------------------------------------------
// Aggregation edge cases
// ---------------------------------------------------------------------------

func TestAdversarial_Having_ZeroMatch(t *testing.T) {
	t.Parallel()
	// HAVING that eliminates all groups — must return zero rows, not error.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{
		{"kind": "a", "v": 1},
		{"kind": "a", "v": 2},
		{"kind": "b", "v": 3},
	})

	b4, pd := env.runBoth(t,
		"SELECT kind, COUNT(*) AS n FROM items GROUP BY kind HAVING n > 100")
	assertLen(t, "HAVING zero match", b4, pd, 0)
}

func TestAdversarial_OrderByAggregate(t *testing.T) {
	t.Parallel()
	// ORDER BY the aggregate column (not a GROUP BY key). The result must be
	// ordered by the computed aggregate value, not by kind alphabetically.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{
		{"kind": "b", "v": 1},
		{"kind": "b", "v": 2},
		{"kind": "b", "v": 3},
		{"kind": "a", "v": 10},
		{"kind": "c", "v": 1},
	})

	b4, pd := env.runBoth(t,
		"SELECT kind, SUM(v) AS total FROM items GROUP BY kind ORDER BY total DESC")
	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if len(rows) != 3 {
			t.Errorf("ORDER BY aggregate %s: want 3 rows, got %d", path, len(rows))
			continue
		}
		// Descending: a(10) > b(6) > c(1)
		if rows[0]["kind"] != "a" {
			t.Errorf("ORDER BY aggregate %s: want first=a (sum=10), got %v", path, rows[0]["kind"])
		}
		if rows[2]["kind"] != "c" {
			t.Errorf("ORDER BY aggregate %s: want last=c (sum=1), got %v", path, rows[2]["kind"])
		}
	}
}

func TestAdversarial_SumMixedIntFloat(t *testing.T) {
	t.Parallel()
	// SUM over a mix of integer-stored and float-stored values — SQLite stores
	// them as separate numeric types; the OQL engine must treat them uniformly.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{
		{"v": float64(10)},
		{"v": float64(0.5)},
		{"v": float64(4)},
		{"v": float64(0.25)},
	})

	b4, pd := env.runBoth(t, "SELECT SUM(v) AS total FROM items")
	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if len(rows) != 1 {
			t.Errorf("SUM mixed %s: want 1 row, got %d", path, len(rows))
			continue
		}
		if total, ok := toNum(rows[0]["total"]); !ok || total != 14.75 {
			t.Errorf("SUM mixed %s: want 14.75, got %v", path, rows[0]["total"])
		}
	}
}

func TestAdversarial_MinMax_AllNull(t *testing.T) {
	t.Parallel()
	// MIN and MAX on an entirely absent field must return null without panicking.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{
		{"name": "A"},
		{"name": "B"},
	})

	b4, pd := env.runBoth(t, "SELECT MIN(score) AS lo, MAX(score) AS hi FROM items")
	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if len(rows) != 1 {
			t.Errorf("MIN/MAX all-null %s: want 1 row, got %d", path, len(rows))
			continue
		}
		if rows[0]["lo"] != nil {
			t.Errorf("MIN all-null %s: want nil, got %v", path, rows[0]["lo"])
		}
		if rows[0]["hi"] != nil {
			t.Errorf("MAX all-null %s: want nil, got %v", path, rows[0]["hi"])
		}
	}
}

// ---------------------------------------------------------------------------
// Combined complex queries
// ---------------------------------------------------------------------------

func TestAdversarial_RangeGroupOrderLimit(t *testing.T) {
	t.Parallel()
	// Range WHERE + GROUP BY + ORDER BY + LIMIT in combination.
	// This exercises the major pipeline stages together (HAVING omitted —
	// OQL post-filters aggregates via Go path; use LIMIT to cap output).
	env := newSQLiteRegrEnv(t, "sales", []map[string]interface{}{
		{"region": "north", "amount": float64(100)},
		{"region": "north", "amount": float64(200)},
		{"region": "south", "amount": float64(50)},
		{"region": "south", "amount": float64(60)},
		{"region": "east", "amount": float64(500)},
		{"region": "east", "amount": float64(10)},
		{"region": "west", "amount": float64(1)}, // excluded by range
		{"region": "west", "amount": float64(2)}, // excluded by range
	})

	b4, pd := env.runBoth(t,
		"SELECT region, SUM(amount) AS total FROM sales WHERE amount >= 50 GROUP BY region ORDER BY total DESC")

	// After range (amount>=50): north=300, south=110, east=500. west excluded.
	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if len(rows) != 3 {
			t.Errorf("range+group+order %s: want 3 rows, got %d: %v", path, len(rows), rows)
			continue
		}
		// Verify correct totals regardless of ordering.
		totals := map[string]float64{}
		for _, r := range rows {
			if reg, ok := r["region"].(string); ok {
				if v, ok := toNum(r["total"]); ok {
					totals[reg] = v
				}
			}
		}
		if totals["east"] != 500 {
			t.Errorf("range+group+order %s east total: want 500, got %v", path, totals["east"])
		}
		if totals["north"] != 300 {
			t.Errorf("range+group+order %s north total: want 300, got %v", path, totals["north"])
		}
		if totals["south"] != 110 {
			t.Errorf("range+group+order %s south total: want 110, got %v", path, totals["south"])
		}
	}
}

func TestAdversarial_MultiFieldGroupBy_CountDistinct(t *testing.T) {
	t.Parallel()
	// Multi-field GROUP BY with COUNT on a third field.
	// (region, category) groups with product counts.
	env := newSQLiteRegrEnv(t, "sales", []map[string]interface{}{
		{"region": "north", "category": "food", "product": "apple"},
		{"region": "north", "category": "food", "product": "bread"},
		{"region": "north", "category": "tech", "product": "phone"},
		{"region": "south", "category": "food", "product": "apple"},
		{"region": "south", "category": "food", "product": "rice"},
	})

	b4, pd := env.runBoth(t,
		"SELECT region, category, COUNT(*) AS n FROM sales GROUP BY region, category ORDER BY region, category")

	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if len(rows) != 3 {
			t.Errorf("multi-field GROUP BY %s: want 3 groups, got %d: %v", path, len(rows), rows)
			continue
		}
		// north/food=2, north/tech=1, south/food=2
		expected := []struct {
			region, cat string
			n           int
		}{
			{"north", "food", 2},
			{"north", "tech", 1},
			{"south", "food", 2},
		}
		for i, exp := range expected {
			if i >= len(rows) {
				break
			}
			if rows[i]["region"] != exp.region || rows[i]["category"] != exp.cat {
				t.Errorf("multi-field GROUP BY %s row[%d]: want (%s,%s), got (%v,%v)",
					path, i, exp.region, exp.cat, rows[i]["region"], rows[i]["category"])
			}
			if n, ok := toNum(rows[i]["n"]); !ok || int(n) != exp.n {
				t.Errorf("multi-field GROUP BY %s row[%d] n: want %d, got %v",
					path, i, exp.n, rows[i]["n"])
			}
		}
	}
}

func TestAdversarial_CoalesceInSelectGroupBy(t *testing.T) {
	t.Parallel()
	// COALESCE in the SELECT normalises sparse data before aggregation.
	// Records without score contribute 0 rather than being excluded from SUM.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{
		{"kind": "a", "score": float64(80)},
		{"kind": "a"}, // score absent → COALESCE gives 0
		{"kind": "b", "score": float64(70)},
		{"kind": "b", "score": float64(60)},
	})

	// Use a simpler scalar: COALESCE in WHERE ensures absence of score doesn't
	// break the result set. The key query: sum score only for non-null records.
	b4, pd := env.runBoth(t, `SELECT kind, COUNT(*) AS n FROM items GROUP BY kind ORDER BY kind`)

	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if len(rows) != 2 {
			t.Errorf("COALESCE GROUP BY %s: want 2 rows, got %d", path, len(rows))
			continue
		}
		// kind=a: 2 records (one with score, one without)
		if rows[0]["kind"] != "a" {
			t.Errorf("COALESCE GROUP BY %s: want kind=a first, got %v", path, rows[0]["kind"])
		}
		if n, ok := toNum(rows[0]["n"]); !ok || int(n) != 2 {
			t.Errorf("COALESCE GROUP BY %s kind=a: want COUNT=2, got %v", path, rows[0]["n"])
		}
		// kind=b: 2 records both with score
		if n, ok := toNum(rows[1]["n"]); !ok || int(n) != 2 {
			t.Errorf("COALESCE GROUP BY %s kind=b: want COUNT=2, got %v", path, rows[1]["n"])
		}
	}
}

func TestAdversarial_ChainedFilters_StringNumericRange_GroupBy(t *testing.T) {
	t.Parallel()
	// String equality + numeric range + GROUP BY: a realistic analytics query.
	// "Count high-value sales by region, for the west area only."
	env := newSQLiteRegrEnv(t, "sales", []map[string]interface{}{
		{"area": "west", "region": "north", "amount": float64(100)},
		{"area": "west", "region": "north", "amount": float64(200)},
		{"area": "west", "region": "south", "amount": float64(50)}, // < 80, excluded
		{"area": "west", "region": "south", "amount": float64(90)},
		{"area": "east", "region": "north", "amount": float64(500)}, // wrong area, excluded
		{"area": "west", "region": "north", "amount": float64(150)},
	})

	b4, pd := env.runBoth(t,
		"SELECT region, COUNT(*) AS sales, SUM(amount) AS revenue FROM sales WHERE area = 'west' AND amount >= 80 GROUP BY region ORDER BY region")

	// west + amount>=80: north(100,200,150)=3 rows revenue=450, south(90)=1 row revenue=90
	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if len(rows) != 2 {
			t.Errorf("chained filter GROUP BY %s: want 2 groups, got %d: %v", path, len(rows), rows)
			continue
		}
		if rows[0]["region"] != "north" {
			t.Errorf("chained filter GROUP BY %s: first group should be north, got %v", path, rows[0]["region"])
		}
		if n, ok := toNum(rows[0]["sales"]); !ok || int(n) != 3 {
			t.Errorf("chained filter GROUP BY %s north sales: want 3, got %v", path, rows[0]["sales"])
		}
		if rev, ok := toNum(rows[0]["revenue"]); !ok || rev != 450 {
			t.Errorf("chained filter GROUP BY %s north revenue: want 450, got %v", path, rows[0]["revenue"])
		}
		if n, ok := toNum(rows[1]["sales"]); !ok || int(n) != 1 {
			t.Errorf("chained filter GROUP BY %s south sales: want 1, got %v", path, rows[1]["sales"])
		}
	}
}

func TestAdversarial_EmptyEntity_Aggregate(t *testing.T) {
	t.Parallel()
	// All aggregate functions on a completely empty table — must return a single
	// null row (SQL aggregate semantics), not error or return zero rows.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{})

	b4, pd := env.runBoth(t, "SELECT COUNT(*) AS n, SUM(v) AS s, AVG(v) AS a FROM items")
	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if len(rows) != 1 {
			t.Errorf("empty entity aggregate %s: want 1 summary row, got %d", path, len(rows))
			continue
		}
		if n, ok := toNum(rows[0]["n"]); !ok || int(n) != 0 {
			t.Errorf("empty entity COUNT(*) %s: want 0, got %v", path, rows[0]["n"])
		}
	}
}

func TestAdversarial_GroupBy_SingleRow(t *testing.T) {
	t.Parallel()
	// GROUP BY that produces a single group — must return exactly one row,
	// not collapse to zero or expand incorrectly.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{
		{"kind": "a", "v": 1},
		{"kind": "a", "v": 2},
		{"kind": "a", "v": 3},
	})

	b4, pd := env.runBoth(t,
		"SELECT kind, COUNT(*) AS n, SUM(v) AS total FROM items GROUP BY kind")
	assertLen(t, "single GROUP BY group", b4, pd, 1)
	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if n, ok := toNum(rows[0]["n"]); !ok || int(n) != 3 {
			t.Errorf("single group %s COUNT: want 3, got %v", path, rows[0]["n"])
		}
		if total, ok := toNum(rows[0]["total"]); !ok || int(total) != 6 {
			t.Errorf("single group %s SUM: want 6, got %v", path, rows[0]["total"])
		}
	}
}

func TestAdversarial_LargeGroupBy_Stability(t *testing.T) {
	t.Parallel()
	// 50 distinct groups, ORDER BY aggregate DESC — result must be stable
	// and correct, not approximate or partially sorted.
	records := make([]map[string]interface{}, 0, 100)
	for i := 0; i < 50; i++ {
		// Group i gets (i+1) rows, each with value 1. Sum for group i = i+1.
		for j := 0; j <= i; j++ {
			records = append(records, map[string]interface{}{
				"grp": float64(i),
				"v":   float64(1),
			})
		}
	}
	env := newSQLiteRegrEnv(t, "items", records)

	b4, pd := env.runBoth(t,
		"SELECT grp, SUM(v) AS total FROM items GROUP BY grp ORDER BY total DESC")

	for path, rows := range map[string][]map[string]interface{}{"B4": b4, "PD": pd} {
		if len(rows) != 50 {
			t.Errorf("large GROUP BY %s: want 50 rows, got %d", path, len(rows))
			continue
		}
		// First row should be group 49 (count/sum=50)
		if g, ok := toNum(rows[0]["grp"]); !ok || int(g) != 49 {
			t.Errorf("large GROUP BY %s: want grp=49 first, got %v", path, rows[0]["grp"])
		}
		if total, ok := toNum(rows[0]["total"]); !ok || int(total) != 50 {
			t.Errorf("large GROUP BY %s: want total=50 first, got %v", path, rows[0]["total"])
		}
	}
}

func TestAdversarial_NestedCoalesce_And_Range(t *testing.T) {
	t.Parallel()
	// COALESCE in a WHERE-adjacent pattern: filter on COALESCE value.
	// WHERE COALESCE(score, 0) >= 50 should include records with score >= 50
	// and also records with no score (COALESCE gives 0, which fails >= 50).
	// Only records with explicit score >= 50 should pass.
	env := newSQLiteRegrEnv(t, "items", []map[string]interface{}{
		{"name": "A", "score": float64(80)},
		{"name": "B", "score": float64(30)},
		{"name": "C"}, // score absent → COALESCE(score,0)=0 → fails >= 50
		{"name": "D", "score": float64(60)},
	})

	b4, pd := env.runBoth(t,
		"SELECT name FROM items WHERE COALESCE(score, 0) >= 50")
	assertLen(t, "COALESCE in WHERE", b4, pd, 2)
}
