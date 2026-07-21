// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package chronicle

import (
	"math/rand"
	"testing"
	"time"
)

func TestMemStore_Contract(t *testing.T) {
	RunBucketStoreContract(t, func() BucketStore[int64] { return NewMemStore[int64]() })
}

// TestFoldRange_EqualsNaiveFineFold is the read-side homomorphism: the
// mixed-grain prefix walk must equal folding every finest bucket in the
// window, for arbitrary grain-0-aligned windows.
func TestFoldRange_EqualsNaiveFineFold(t *testing.T) {
	h := fiveMinHierarchy(t)
	eng, _ := NewEngine[int64](SumInt64{}, h, NewMemStore[int64]())

	base := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	span := 3 * 24 * time.Hour // three days: guarantees whole-day coarse buckets mid-window
	r := rand.New(rand.NewSource(23))
	for i := 0; i < 4000; i++ {
		at := base.Add(time.Duration(r.Intn(int(span/time.Minute))) * time.Minute)
		eng.Append(int64(r.Intn(100)), at)
	}

	naive := func(from, to time.Time) int64 {
		m := SumInt64{}
		acc := m.Identity()
		for start := from; start.Before(to); start = start.Add(5 * time.Minute) {
			acc = m.Combine(acc, eng.Bucket(0, start))
		}
		return acc
	}

	// 200 random grain-0-aligned windows, including ragged ones that
	// force partial-edge recursion at both ends.
	slots := int(span / (5 * time.Minute))
	for i := 0; i < 200; i++ {
		a, b := r.Intn(slots), r.Intn(slots)
		if a > b {
			a, b = b, a
		}
		from := base.Add(time.Duration(a) * 5 * time.Minute)
		to := base.Add(time.Duration(b) * 5 * time.Minute)
		if got, want := eng.FoldRange(from, to), naive(from, to); got != want {
			t.Fatalf("window [%v,%v): FoldRange=%d naive=%d", from, to, got, want)
		}
	}

	// Degenerate windows.
	if eng.FoldRange(base, base) != 0 {
		t.Fatal("empty window must fold to identity")
	}
	if eng.FoldRange(base.Add(time.Hour), base) != 0 {
		t.Fatal("inverted window must fold to identity")
	}
}

// TestAsOf_BalanceSemantics exercises @C03's cumulative read on bal's
// monoid: the balance as of an instant includes that instant's finest
// bucket, and corrections propagate through Recompute.
func TestAsOf_BalanceSemantics(t *testing.T) {
	h := fiveMinHierarchy(t)
	eng, _ := NewEngine[int64](SumInt64{}, h, NewMemStore[int64]())
	epoch := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	// Postings: +100 on day 1, -30 on day 2, +5 on day 3 (same clock time).
	p1 := epoch.Add(10 * time.Hour)
	p2 := epoch.Add(24*time.Hour + 10*time.Hour)
	p3 := epoch.Add(48*time.Hour + 10*time.Hour)
	eng.Append(100, p1)
	eng.Append(-30, p2)
	eng.Append(5, p3)

	if got := eng.AsOf(epoch, p1); got != 100 {
		t.Fatalf("as of p1: %d, want 100", got)
	}
	if got := eng.AsOf(epoch, p2); got != 70 {
		t.Fatalf("as of p2: %d, want 70", got)
	}
	if got := eng.AsOf(epoch, p3); got != 75 {
		t.Fatalf("as of p3: %d, want 75", got)
	}
	// Strictly before p2's bucket: the FoldRange form.
	if got := eng.FoldRange(epoch, h.Grain(0).Truncate(p2)); got != 100 {
		t.Fatalf("before p2: %d, want 100", got)
	}
	// An instant between postings reads the running balance.
	if got := eng.AsOf(epoch, p2.Add(3*time.Hour)); got != 70 {
		t.Fatalf("between p2 and p3: %d, want 70", got)
	}
}
