// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

// executor_aggregation_test.go — tests for Stage 2.7: aggregation in RETURN.
//
// Graph: buildPredicateGraph() — users:1..5 with name, age, active, email.
// user:1 Alice   age=30  active=true   email=alice@example.com
// user:2 Bob     age=25  active=false  email=bob@corp.org
// user:3 Carol   age=35  active=true   email=carol@example.com
// user:4 ""      age=40  active=true   no email
// user:5 Eve     age=nil active=true   no email (no store entry)

import (
	"context"
	"testing"
)

func aggQuery(t *testing.T, q string) []map[string]interface{} {
	t.Helper()
	g, store := buildPredicateGraph()
	executor := NewExecutor(g, 10).WithStore(store)
	parser := NewParser()
	ast, hint, err := parser.Parse(q)
	if err != nil {
		t.Fatalf("Parse(%q): %v", q, err)
	}
	result, err := executor.Execute(context.Background(), ast, hint)
	if err != nil {
		t.Fatalf("Execute(%q): %v", q, err)
	}
	return result.Data
}

// ── count ──────────────────────────────────────────────────────────────────

func TestAgg_CountStar(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t, "MATCH (u:users) RETURN count(*) AS n")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0]["n"] != 5 {
		t.Errorf("expected count(*)=5, got %v", rows[0]["n"])
	}
}

func TestAgg_CountNode(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t, "MATCH (u:users) RETURN count(u) AS n")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0]["n"] != 5 {
		t.Errorf("expected count(u)=5, got %v", rows[0]["n"])
	}
}

func TestAgg_CountProperty(t *testing.T) {
	t.Parallel()
	// count(u.email) counts non-null values only.
	// users:1,2,3 have email; 4,5 do not.
	rows := aggQuery(t, "MATCH (u:users) RETURN count(u.email) AS n")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0]["n"] != 3 {
		t.Errorf("expected count(u.email)=3, got %v", rows[0]["n"])
	}
}

func TestAgg_CountDistinct(t *testing.T) {
	t.Parallel()
	// active has two distinct values: true and false (nil from user:5 not counted).
	rows := aggQuery(t, "MATCH (u:users) RETURN count(DISTINCT u.active) AS n")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	// true appears for 1,3,4; false for 2; nil for 5 (nil not counted in DISTINCT)
	if rows[0]["n"] != 2 {
		t.Errorf("expected 2 distinct active values, got %v", rows[0]["n"])
	}
}

// ── collect ────────────────────────────────────────────────────────────────

func TestAgg_Collect(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t, "MATCH (u:users) WHERE u.age IS NOT NULL RETURN collect(u.age) AS ages")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	ages, ok := rows[0]["ages"].([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}, got %T", rows[0]["ages"])
	}
	if len(ages) != 4 {
		t.Errorf("expected 4 ages collected, got %d: %v", len(ages), ages)
	}
}

func TestAgg_CollectEmpty(t *testing.T) {
	t.Parallel()
	// collect of null values returns empty list.
	rows := aggQuery(t, "MATCH (u:users) WHERE u.age > 100 RETURN collect(u.age) AS ages")
	if len(rows) == 0 {
		// No rows when nothing matches and no grouping keys — acceptable.
		return
	}
	ages, ok := rows[0]["ages"].([]interface{})
	if !ok {
		t.Fatalf("expected []interface{}")
	}
	if len(ages) != 0 {
		t.Errorf("expected empty collect, got %v", ages)
	}
}

// ── sum / avg / min / max ──────────────────────────────────────────────────

func TestAgg_Sum(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t, "MATCH (u:users) WHERE u.age IS NOT NULL RETURN sum(u.age) AS total")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	// 25+30+35+40 = 130
	v, ok := toFloat64(rows[0]["total"])
	if !ok || v != 130 {
		t.Errorf("expected sum=130, got %v", rows[0]["total"])
	}
}

func TestAgg_Avg(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t, "MATCH (u:users) WHERE u.age IS NOT NULL RETURN avg(u.age) AS mean")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	// (25+30+35+40)/4 = 32.5
	v, ok := toFloat64(rows[0]["mean"])
	if !ok || v != 32.5 {
		t.Errorf("expected avg=32.5, got %v", rows[0]["mean"])
	}
}

func TestAgg_Min(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t, "MATCH (u:users) WHERE u.age IS NOT NULL RETURN min(u.age) AS youngest")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0]["youngest"] != 25 {
		t.Errorf("expected min=25, got %v", rows[0]["youngest"])
	}
}

func TestAgg_Max(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t, "MATCH (u:users) WHERE u.age IS NOT NULL RETURN max(u.age) AS oldest")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0]["oldest"] != 40 {
		t.Errorf("expected max=40, got %v", rows[0]["oldest"])
	}
}

// ── GROUP BY (implicit via non-aggregate RETURN items) ────────────────────

func TestAgg_GroupBy(t *testing.T) {
	t.Parallel()
	// Group users by active status, count each group.
	rows := aggQuery(t, "MATCH (u:users) WHERE u.active IS NOT NULL RETURN u.active, count(u) AS n")
	if len(rows) < 1 {
		t.Fatal("expected at least 1 group")
	}
	// active=true: users 1,3,4 → count=3; active=false: user 2 → count=1
	groups := make(map[interface{}]int)
	for _, row := range rows {
		groups[row["u.active"]] = row["n"].(int)
	}
	if groups[true] != 3 {
		t.Errorf("expected active=true group count=3, got %d", groups[true])
	}
	if groups[false] != 1 {
		t.Errorf("expected active=false group count=1, got %d", groups[false])
	}
}

func TestAgg_GroupBy_Collect(t *testing.T) {
	t.Parallel()
	// Collect names per active group.
	rows := aggQuery(t, "MATCH (u:users) WHERE u.active IS NOT NULL AND u.name IS NOT NULL AND u.name <> '' RETURN u.active, collect(u.name) AS names")
	if len(rows) < 1 {
		t.Fatal("expected at least 1 group")
	}
	groups := make(map[interface{}][]interface{})
	for _, row := range rows {
		groups[row["u.active"]] = row["names"].([]interface{})
	}
	// active=true: Alice(1), Carol(3) — user:4 has empty name
	if len(groups[true]) < 2 {
		t.Errorf("expected at least 2 active=true names, got %v", groups[true])
	}
	if len(groups[false]) != 1 {
		t.Errorf("expected 1 active=false name (Bob), got %v", groups[false])
	}
}

func TestAgg_MultipleAggregates(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t,
		"MATCH (u:users) WHERE u.age IS NOT NULL RETURN count(u) AS n, sum(u.age) AS total, avg(u.age) AS mean")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0]["n"] != 4 {
		t.Errorf("expected n=4, got %v", rows[0]["n"])
	}
	if v, _ := toFloat64(rows[0]["total"]); v != 130 {
		t.Errorf("expected total=130, got %v", rows[0]["total"])
	}
	if v, _ := toFloat64(rows[0]["mean"]); v != 32.5 {
		t.Errorf("expected mean=32.5, got %v", rows[0]["mean"])
	}
}

func TestAgg_WithOrderBy(t *testing.T) {
	t.Parallel()
	// Group by active, order by count descending.
	rows := aggQuery(t,
		"MATCH (u:users) WHERE u.active IS NOT NULL RETURN u.active, count(u) AS n ORDER BY n DESC")
	if len(rows) < 2 {
		t.Fatal("expected at least 2 groups")
	}
	// Largest group (active=true, count=3) should be first.
	if rows[0]["n"].(int) < rows[1]["n"].(int) {
		t.Error("ORDER BY n DESC not respected in aggregation results")
	}
}

// ── WITH aggregation ──────────────────────────────────────────────────────────
// Graph: buildPredicateGraph() — users:1..5 (entity type "users")
// active=true:  users:1(Alice,30), users:3(Carol,35), users:4("",40), users:5(Eve,age=nil)
// active=false: users:2(Bob,25)

func TestWithAgg_CountNode(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t,
		`MATCH (u:users)
		 WITH u.active AS active, count(u) AS n
		 RETURN active, n`)
	// 3 groups: true (users:1,3,4), false (users:2), nil (users:5 has no store entry)
	if len(rows) != 3 {
		t.Fatalf("WITH count(u): want 3 groups (true/false/nil), got %d: %v", len(rows), rows)
	}
	total := 0
	for _, row := range rows {
		total += row["n"].(int)
	}
	if total != 5 {
		t.Errorf("WITH count(u): total should be 5, got %d", total)
	}
}

func TestWithAgg_Collect(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t,
		`MATCH (u:users)
		 WITH u.active AS active, collect(u.name) AS names
		 RETURN active, names`)
	// 3 groups: true, false, nil (users:5 has no active property)
	if len(rows) != 3 {
		t.Fatalf("WITH collect: want 3 groups, got %d", len(rows))
	}
	for _, row := range rows {
		names, ok := row["names"].([]interface{})
		if !ok {
			t.Errorf("WITH collect: names should be a list, got %T", row["names"])
			continue
		}
		// The nil-active group (users:5, no store entry) has u.name=nil which
		// is not collected, so its names list is legitimately empty.
		if row["active"] != nil && len(names) == 0 {
			t.Errorf("WITH collect: names list for active=%v should not be empty", row["active"])
		}
	}
}

func TestWithAgg_Sum(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t,
		`MATCH (u:users)
		 WITH u.active AS active, sum(u.age) AS total
		 RETURN active, total`)
	// 3 groups: true, false, nil
	if len(rows) != 3 {
		t.Fatalf("WITH sum: want 3 groups, got %d", len(rows))
	}
	for _, row := range rows {
		if row["active"] == false {
			// Bob only: age=25.
			if row["total"] == nil {
				t.Error("WITH sum: total for active=false (Bob, age=25) should not be nil")
			}
		}
	}
}

func TestWithAgg_Avg(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t,
		`MATCH (u:users)
		 WITH u.active AS active, avg(u.age) AS avgAge
		 RETURN active, avgAge`)
	// 3 groups: true, false, nil
	if len(rows) != 3 {
		t.Fatalf("WITH avg: want 3 groups, got %d", len(rows))
	}
	for _, row := range rows {
		if row["active"] == false && row["avgAge"] == nil {
			t.Error("WITH avg: avgAge for active=false (Bob, age=25) should not be nil")
		}
	}
}

func TestWithAgg_WhereOnAggregate(t *testing.T) {
	t.Parallel()
	// active=true has 4 members, active=false has 1 — exactly 1 group survives WHERE n > 1.
	rows := aggQuery(t,
		`MATCH (u:users)
		 WITH u.active AS active, count(u) AS n
		 WHERE n > 1
		 RETURN active, n`)
	if len(rows) != 1 {
		t.Fatalf("WITH count WHERE n>1: want 1 row, got %d", len(rows))
	}
	if n := rows[0]["n"].(int); n <= 1 {
		t.Errorf("WITH count WHERE n>1: n=%d, want >1", n)
	}
}

func TestWithAgg_NodePassthrough(t *testing.T) {
	t.Parallel()
	// Grouping by node variable — each node is its own group, count=1.
	// RETURN must still access u.name after aggregation.
	rows := aggQuery(t,
		`MATCH (u:users)
		 WITH u, count(u) AS n
		 RETURN u.name, n`)
	if len(rows) != 5 {
		t.Fatalf("WITH node passthrough: want 5 rows, got %d", len(rows))
	}
	for _, row := range rows {
		if n := row["n"].(int); n != 1 {
			t.Errorf("WITH node passthrough: n should be 1, got %d", n)
		}
	}
}

func TestWithAgg_Min(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t,
		`MATCH (u:users)
		 WITH u.active AS active, min(u.age) AS youngest
		 RETURN active, youngest`)
	// 3 groups: true, false, nil
	if len(rows) != 3 {
		t.Fatalf("WITH min: want 3 groups, got %d", len(rows))
	}
	for _, row := range rows {
		if row["active"] == false && row["youngest"] == nil {
			t.Error("WITH min: youngest for active=false (Bob, age=25) should not be nil")
		}
	}
}

func TestWithAgg_Max(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t,
		`MATCH (u:users)
		 WITH u.active AS active, max(u.age) AS oldest
		 RETURN active, oldest`)
	// 3 groups: true, false, nil
	if len(rows) != 3 {
		t.Fatalf("WITH max: want 3 groups, got %d", len(rows))
	}
	for _, row := range rows {
		if row["active"] == false && row["oldest"] == nil {
			t.Error("WITH max: oldest for active=false (Bob, age=25) should not be nil")
		}
	}
}

func TestWithAgg_PropertyProjection(t *testing.T) {
	t.Parallel()
	// Verify that MATCH ... WITH scalar_prop ... RETURN scalar_prop works
	// (the fundamental property projection bug that blocked aggregation).
	rows := aggQuery(t,
		`MATCH (u:users)
		 WITH u.active AS active
		 RETURN active`)
	if len(rows) != 5 {
		t.Fatalf("WITH property projection: want 5 rows, got %d", len(rows))
	}
	trueCount, falseCount, nilCount := 0, 0, 0
	for _, row := range rows {
		switch row["active"] {
		case true:
			trueCount++
		case false:
			falseCount++
		default:
			nilCount++
		}
	}
	// users:1,3,4 active=true; users:2 active=false; users:5 no entry (nil)
	if trueCount != 3 || falseCount != 1 || nilCount != 1 {
		t.Errorf("WITH property projection: want 3 true, 1 false, 1 nil, got %d true, %d false, %d nil",
			trueCount, falseCount, nilCount)
	}
}

func TestWithAgg_CountStar(t *testing.T) {
	t.Parallel()
	rows := aggQuery(t,
		`MATCH (u:users)
		 WITH u.active AS active, count(*) AS n
		 RETURN active, n`)
	// 3 groups: true (3), false (1), nil (1). Total = 5.
	if len(rows) != 3 {
		t.Fatalf("WITH count(*): want 3 groups, got %d: %v", len(rows), rows)
	}
	total := 0
	for _, row := range rows {
		total += row["n"].(int)
	}
	if total != 5 {
		t.Errorf("WITH count(*): total should be 5, got %d", total)
	}
}
