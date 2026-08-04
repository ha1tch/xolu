package chronicle

import (
	"testing"
	"time"
)

// Day parents BOTH week and month — the fan-out ts rollups always had
// and the linear extraction lost. Weeks and months cannot nest in each
// other, so this shape is only expressible as a tree.
// balHierarchy is bal's real shape, single-rooted at the FINEST grain
// and fanning out to coarser ones:
//
//	          hour            (root: finest)
//	           |
//	          day
//	           |
//	     +-----+-----+
//	     |           |
//	   week        month      (day fans out: two coarsening branches)
//	                 |
//	              quarter
//	                 |
//	               year
//
// Single-parent: each grain coarsens exactly one finer grain. Fan-out:
// day is coarsened by BOTH week and month, which nest in each other not
// at all — the shape a linear chain cannot express.
func balHierarchy(t *testing.T) *Hierarchy {
	t.Helper()
	h, err := NewTreeHierarchy(
		TreeSpec{Grain: FixedGrain("hour", time.Hour), Parent: ""},
		TreeSpec{Grain: FixedGrain("day", 24 * time.Hour), Parent: "hour"},
		TreeSpec{Grain: FixedGrain("week", 7 * 24 * time.Hour), Parent: "day"},
		TreeSpec{Grain: MonthGrain("month"), Parent: "day"},
		TreeSpec{Grain: MonthsGrain("quarter", 3), Parent: "month"},
		TreeSpec{Grain: MonthsGrain("year", 12), Parent: "quarter"},
	)
	if err != nil {
		t.Fatalf("bal hierarchy: %v", err)
	}
	return h
}

func TestTree_MonthQuarterYearNesting(t *testing.T) {
	h := balHierarchy(t)
	if got := h.Grain(h.Root()).Name; got != "hour" {
		t.Fatalf("root: %s, want hour (finest)", got)
	}
	// Quarter boundaries must be Jan/Apr/Jul/Oct.
	q := MonthsGrain("quarter", 3)
	for _, tc := range []struct{ in, want string }{
		{"2026-02-14T10:00:00Z", "2026-01-01"},
		{"2026-05-01T00:00:00Z", "2026-04-01"},
		{"2026-09-30T23:59:59Z", "2026-07-01"},
		{"2026-12-31T00:00:00Z", "2026-10-01"},
	} {
		in, _ := time.Parse(time.RFC3339, tc.in)
		if got := q.Truncate(in).Format("2006-01-02"); got != tc.want {
			t.Errorf("quarter of %s = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestTree_FoldAcrossCalendarGrains(t *testing.T) {
	h := balHierarchy(t)
	eng, err := NewEngine[int64](SumInt64{}, h, NewMemStore[int64]())
	if err != nil {
		t.Fatal(err)
	}
	// One unit per day across a whole quarter (Q1 2026: Jan+Feb+Mar = 90 days).
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	days := 0
	for d := start; d.Before(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)); d = d.AddDate(0, 0, 1) {
		eng.Append(1, d)
		days++
	}
	if days != 90 {
		t.Fatalf("Q1 2026 should be 90 days, counted %d", days)
	}
	// The whole quarter must fold to exactly 90 — the case weeks cannot express.
	got := eng.FoldRange(
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if got != 90 {
		t.Fatalf("Q1 fold = %d, want 90", got)
	}
	// H1 = Q1+Q2, and a fiscal year = 12 months, both exact.
	h1 := eng.FoldRange(
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if h1 != 90 {
		t.Fatalf("H1 fold = %d, want 90 (only Q1 populated)", h1)
	}
}

func TestTree_RejectsBadNesting(t *testing.T) {
	// Week cannot parent month: months are not whole weeks.
	_, err := NewTreeHierarchy(
		TreeSpec{Grain: MonthGrain("month"), Parent: ""},
		TreeSpec{Grain: FixedGrain("week", 7 * 24 * time.Hour), Parent: "month"},
	)
	if err == nil {
		t.Fatal("week coarsening month must be rejected: weeks do not tile months")
	}
	// Cycle.
	_, err = NewTreeHierarchy(
		TreeSpec{Grain: FixedGrain("a", time.Hour), Parent: "b"},
		TreeSpec{Grain: FixedGrain("b", 2 * time.Hour), Parent: "a"},
	)
	if err == nil {
		t.Fatal("cycle must be rejected")
	}
}
