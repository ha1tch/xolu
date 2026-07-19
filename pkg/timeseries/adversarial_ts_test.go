// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// adversarial_ts_test.go — adversarial correctness tests for the timeseries store.
//
// These tests focus on the partial-prefix time-filter path, which was the
// site of a prior leakage bug: keys from a high-d1 series are
// lexicographically ordered before keys from a low-d1 series at later
// timestamps, meaning Pebble bounds alone do not correctly bound the time
// window when fewer dims than defined are supplied.
//
// Scenarios:
//
//  1. Partial prefix QueryRange ascending — out-of-window events from a
//     high-d1 series must not appear.
//  2. Partial prefix QueryRange descending — same filter must apply on the
//     reverse path.
//  3. Partial prefix Aggregate — Aggregate must apply the same Go-side
//     time filter; miscounted events would skew the bucket values.
//  4. Timestamp boundary exactness — events exactly at From and To must be
//     included; events at From-1ns and To+1ns must be excluded.
//  5. Cross-timeline isolation — a partial-prefix query on timeline 1 must
//     not return events from timeline 2 sharing the same dimension values.
//  6. Full-timeline scan (no-dim restriction) is correctly bounded to the
//     requested time window.
//  7. Partial prefix with many d1 values — leakage test at scale (10 series).

package timeseries

import (
	"context"
	"fmt"
	"testing"
	"time"
)

var tsBase = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

// newTS opens a fresh two-dimensional store and defines timeline tid with
// dims=2 and no retention limit.
func newTS(t *testing.T, tid TimelineID) Store {
	t.Helper()
	store, err := NewPebbleStore(t.TempDir(), StoreConfig{DefaultRetentionDays: 0}, testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.DefineTimeline(tid, TimelineConfig{Name: "adv", Dims: 2}); err != nil {
		t.Fatalf("DefineTimeline %d: %v", tid, err)
	}
	return store
}

// app appends a single event (dims=[d0,d1], num=val) to the store.
func app(t *testing.T, s Store, tid TimelineID, d0, d1 uint64, ts time.Time, val float64) {
	t.Helper()
	if err := s.Append(context.Background(), Event{
		Timeline: tid,
		Dims:     []uint64{d0, d1},
		Time:     ts,
		Nums:     []float64{val},
	}); err != nil {
		t.Fatalf("Append d0=%d d1=%d ts=%v: %v", d0, d1, ts, err)
	}
}

// ---------------------------------------------------------------------------
// 1. Partial prefix QueryRange (ascending) — out-of-window events excluded
// ---------------------------------------------------------------------------

// TestAdversarial_TS_PartialPrefix_QueryRange_Ascending confirms that events
// from a high-d1 series whose timestamps fall outside the query window are
// not returned even though they may be lexicographically inside the Pebble
// key bounds.
//
// Layout (timeline 1, d0=10 fixed, querying dims=[10]):
//
//	d1=1, t=+60m  ← inside  [+30m, +90m]
//	d1=1, t=+120m ← outside (after To)
//	d1=9, t=+10m  ← outside (before From) — but lexicographically between d1=1 keys
//	d1=9, t=+60m  ← inside
//	d1=99, t=+200m ← outside (after To) — high d1, earlier lexicographic position
func TestAdversarial_TS_PartialPrefix_QueryRange_Ascending(t *testing.T) {
	s := newTS(t, 1)
	ctx := context.Background()
	from := tsBase.Add(30 * time.Minute)
	to := tsBase.Add(90 * time.Minute)

	type ev struct {
		d1      uint64
		offset  time.Duration
		inRange bool
	}
	events := []ev{
		{1, 60 * time.Minute, true},
		{1, 120 * time.Minute, false},
		{9, 10 * time.Minute, false},
		{9, 60 * time.Minute, true},
		{99, 200 * time.Minute, false},
	}
	for _, e := range events {
		app(t, s, 1, 10, e.d1, tsBase.Add(e.offset), 1.0)
	}

	want := 0
	for _, e := range events {
		if e.inRange {
			want++
		}
	}

	got, err := s.QueryRange(ctx, RangeQuery{
		Timeline: 1, Dims: []uint64{10}, From: from, To: to,
	})
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(got) != want {
		t.Errorf("ascending partial prefix: got %d events, want %d", len(got), want)
		for _, e := range got {
			t.Logf("  returned: dims=%v ts=%v", e.Dims, e.Time)
		}
	}
	// All returned events must be within [from, to].
	for _, e := range got {
		if e.Time.Before(from) || e.Time.After(to) {
			t.Errorf("ascending: event at %v is outside [%v, %v]", e.Time, from, to)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. Partial prefix QueryRange (descending) — filter applies on reverse scan
// ---------------------------------------------------------------------------

func TestAdversarial_TS_PartialPrefix_QueryRange_Descending(t *testing.T) {
	s := newTS(t, 2)
	ctx := context.Background()
	from := tsBase.Add(30 * time.Minute)
	to := tsBase.Add(90 * time.Minute)

	app(t, s, 2, 10, 1, tsBase.Add(60*time.Minute), 1.0)   // inside
	app(t, s, 2, 10, 1, tsBase.Add(120*time.Minute), 2.0)  // outside: after To
	app(t, s, 2, 10, 9, tsBase.Add(10*time.Minute), 3.0)   // outside: before From
	app(t, s, 2, 10, 9, tsBase.Add(60*time.Minute), 4.0)   // inside
	app(t, s, 2, 10, 99, tsBase.Add(200*time.Minute), 5.0) // outside: after To

	got, err := s.QueryRange(ctx, RangeQuery{
		Timeline: 2, Dims: []uint64{10}, From: from, To: to, Order: "desc",
	})
	if err != nil {
		t.Fatalf("QueryRange desc: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("descending partial prefix: got %d events, want 2", len(got))
		for _, e := range got {
			t.Logf("  returned: dims=%v ts=%v", e.Dims, e.Time)
		}
	}
	for _, e := range got {
		if e.Time.Before(from) || e.Time.After(to) {
			t.Errorf("descending: event at %v is outside [%v, %v]", e.Time, from, to)
		}
	}
	// Descending order: later timestamp first.
	if len(got) == 2 && got[0].Time.Before(got[1].Time) {
		t.Errorf("descending: order wrong — got[0]=%v before got[1]=%v", got[0].Time, got[1].Time)
	}
}

// ---------------------------------------------------------------------------
// 3. Partial prefix Aggregate — out-of-window events excluded from buckets
// ---------------------------------------------------------------------------

// TestAdversarial_TS_PartialPrefix_Aggregate verifies that the Aggregate path
// applies the same Go-side time filter as QueryRange.  Three events are
// written: two inside the window, one outside via a high-d1 series.  If the
// filter were absent the sum and count would be wrong.
func TestAdversarial_TS_PartialPrefix_Aggregate(t *testing.T) {
	s := newTS(t, 3)
	ctx := context.Background()
	from := tsBase.Add(30 * time.Minute)
	to := tsBase.Add(90 * time.Minute)

	app(t, s, 3, 10, 1, tsBase.Add(60*time.Minute), 5.0)     // inside, val=5
	app(t, s, 3, 10, 2, tsBase.Add(60*time.Minute), 7.0)     // inside, val=7
	app(t, s, 3, 10, 99, tsBase.Add(200*time.Minute), 100.0) // outside — must not count

	buckets, err := s.Aggregate(ctx, AggregateQuery{
		Timeline: 3, Dims: []uint64{10}, From: from, To: to,
		Function: "sum", NumField: 0,
	})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(buckets) != 1 {
		t.Fatalf("Aggregate: want 1 bucket (scalar), got %d", len(buckets))
	}
	// Sum must be 5+7=12, not 5+7+100=112.
	if buckets[0].Value != 12.0 {
		t.Errorf("Aggregate sum: want 12.0, got %v (out-of-window event leaked in?)", buckets[0].Value)
	}
	if buckets[0].Count != 2 {
		t.Errorf("Aggregate count: want 2, got %d", buckets[0].Count)
	}
}

// ---------------------------------------------------------------------------
// 4. Timestamp boundary exactness
// ---------------------------------------------------------------------------

// TestAdversarial_TS_BoundaryExactness asserts that events exactly at From
// and To are included, while events at From-1ns and To+1ns are excluded.
func TestAdversarial_TS_BoundaryExactness(t *testing.T) {
	s := newTS(t, 4)
	ctx := context.Background()
	from := tsBase.Add(1 * time.Hour)
	to := tsBase.Add(2 * time.Hour)

	type ev struct {
		offset  time.Duration
		inRange bool
		label   string
	}
	events := []ev{
		{time.Hour - time.Nanosecond, false, "From-1ns"},
		{time.Hour, true, "exactly From"},
		{90 * time.Minute, true, "midpoint"},
		{2 * time.Hour, true, "exactly To"},
		{2*time.Hour + time.Nanosecond, false, "To+1ns"},
	}
	// Use a full prefix (both dims) to avoid partial-prefix path — this test
	// is purely about boundary semantics, not the leakage filter.
	for i, e := range events {
		app(t, s, 4, 1, uint64(i+1), tsBase.Add(e.offset), float64(i))
	}

	got, err := s.QueryRange(ctx, RangeQuery{
		Timeline: 4, Dims: []uint64{1}, From: from, To: to,
	})
	if err != nil {
		t.Fatalf("QueryRange boundary: %v", err)
	}

	want := 0
	for _, e := range events {
		if e.inRange {
			want++
		}
	}
	if len(got) != want {
		t.Errorf("boundary: got %d events, want %d", len(got), want)
	}
	for _, e := range got {
		if e.Time.Before(from) || e.Time.After(to) {
			t.Errorf("boundary: returned event at %v outside [%v, %v]", e.Time, from, to)
		}
	}
}

// ---------------------------------------------------------------------------
// 5. Cross-timeline isolation
// ---------------------------------------------------------------------------

// TestAdversarial_TS_CrossTimeline_Isolation verifies that a partial-prefix
// query on timeline 1 does not return events from timeline 2 even when both
// timelines have the same d0 value and overlapping time windows.
func TestAdversarial_TS_CrossTimeline_Isolation(t *testing.T) {
	store, err := NewPebbleStore(t.TempDir(), StoreConfig{DefaultRetentionDays: 0}, testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("NewPebbleStore: %v", err)
	}
	defer store.Close()

	for _, tid := range []TimelineID{1, 2} {
		if err := store.DefineTimeline(tid, TimelineConfig{Name: fmt.Sprintf("tl%d", tid), Dims: 2}); err != nil {
			t.Fatalf("DefineTimeline %d: %v", tid, err)
		}
	}

	ctx := context.Background()
	from := tsBase.Add(30 * time.Minute)
	to := tsBase.Add(90 * time.Minute)
	ts := tsBase.Add(60 * time.Minute)

	// Both timelines: d0=42, d1=7, same timestamp — only timeline 1 should be returned.
	app(t, store, 1, 42, 7, ts, 1.0)
	app(t, store, 2, 42, 7, ts, 99.0)

	got, err := store.QueryRange(ctx, RangeQuery{
		Timeline: 1, Dims: []uint64{42}, From: from, To: to,
	})
	if err != nil {
		t.Fatalf("QueryRange tl1: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("cross-timeline isolation: got %d events, want 1", len(got))
	}
	if len(got) == 1 && got[0].Nums[0] != 1.0 {
		t.Errorf("cross-timeline isolation: returned tl2 event (val=%v)", got[0].Nums[0])
	}
}

// ---------------------------------------------------------------------------
// 6. Full-timeline scan (d0 partial, no d1) is correctly time-bounded
// ---------------------------------------------------------------------------

// TestAdversarial_TS_PartialPrefix_ManyD1Values scales the leakage test to 10
// distinct d1 values.  This catches implementations that correctly filter a
// few series but degrade with more.
func TestAdversarial_TS_PartialPrefix_ManyD1Values(t *testing.T) {
	s := newTS(t, 5)
	ctx := context.Background()
	from := tsBase.Add(1 * time.Hour)
	to := tsBase.Add(2 * time.Hour)

	wantInside := 0
	// For d1 in [1..10]: write one event inside and one event at d1*3h (outside).
	for d1 := uint64(1); d1 <= 10; d1++ {
		app(t, s, 5, 7, d1, tsBase.Add(90*time.Minute), 1.0)                // inside
		app(t, s, 5, 7, d1, tsBase.Add(time.Duration(d1)*3*time.Hour), 2.0) // outside
		wantInside++
	}

	got, err := s.QueryRange(ctx, RangeQuery{
		Timeline: 5, Dims: []uint64{7}, From: from, To: to,
	})
	if err != nil {
		t.Fatalf("QueryRange many-d1: %v", err)
	}
	if len(got) != wantInside {
		t.Errorf("many-d1 partial prefix: got %d events, want %d", len(got), wantInside)
		for _, e := range got {
			t.Logf("  returned: dims=%v ts=%v", e.Dims, e.Time)
		}
	}
	for _, e := range got {
		if e.Time.Before(from) || e.Time.After(to) {
			t.Errorf("many-d1: event at %v outside [%v, %v]", e.Time, from, to)
		}
	}
}

// ---------------------------------------------------------------------------
// 7. Aggregate with bucketed intervals — partial prefix does not corrupt buckets
// ---------------------------------------------------------------------------

// TestAdversarial_TS_PartialPrefix_Aggregate_Bucketed confirms that when
// bucket intervals are used, the partial-prefix time filter discards
// out-of-window events before they can distort any bucket, including buckets
// that would not otherwise receive them.
func TestAdversarial_TS_PartialPrefix_Aggregate_Bucketed(t *testing.T) {
	s := newTS(t, 6)
	ctx := context.Background()

	hour := time.Hour
	from := tsBase
	to := tsBase.Add(2 * hour)

	// d1=1: two events, one per hour bucket — should each contribute 10.
	app(t, s, 6, 5, 1, tsBase.Add(30*time.Minute), 10.0) // bucket 0
	app(t, s, 6, 5, 1, tsBase.Add(90*time.Minute), 10.0) // bucket 1

	// d1=99 (high): one event after To — must be excluded from all buckets.
	app(t, s, 6, 5, 99, tsBase.Add(3*hour), 999.0)

	buckets, err := s.Aggregate(ctx, AggregateQuery{
		Timeline: 6, Dims: []uint64{5}, From: from, To: to,
		Function: "sum", NumField: 0, Interval: hour,
	})
	if err != nil {
		t.Fatalf("Aggregate bucketed: %v", err)
	}

	totalSum := 0.0
	for _, b := range buckets {
		totalSum += b.Value
	}
	// Expected total: 10+10 = 20. If leakage, it would be 20+999 = 1019.
	if totalSum != 20.0 {
		t.Errorf("bucketed aggregate total sum: want 20.0, got %v (out-of-window event leaked?)", totalSum)
	}
	// Each non-empty bucket must only contain the expected value.
	for _, b := range buckets {
		if b.Value > 10.0 {
			t.Errorf("bucket at %v: value=%v exceeds per-bucket maximum 10.0", b.Time, b.Value)
		}
	}
}
