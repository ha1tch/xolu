// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─── test helpers ─────────────────────────────────────────────────────────────

// rollupStore opens a PebbleStore whose StoreConfig has RollupCascadeDelete
// set to the supplied value.
func rollupStore(t *testing.T, cascadeDelete bool) *PebbleStore {
	t.Helper()
	cfg := testStoreConfig()
	cfg.RollupCascadeDelete = cascadeDelete
	store, err := NewPebbleStore(t.TempDir(), cfg, testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store.(*PebbleStore)
}

// mustDefineRollup defines a rollup from srcTID→dstTID with the given duration
// and returns the assigned ID.
func mustDefineRollup(t *testing.T, s *PebbleStore, srcTID, dstTID TimelineID, dur time.Duration) RollupID {
	t.Helper()
	id, err := s.DefineRollup(srcTID, RollupDef{
		DestTID:        dstTID,
		BucketDuration: dur,
	})
	if err != nil {
		t.Fatalf("DefineRollup %d→%d: %v", srcTID, dstTID, err)
	}
	return id
}

// defineTimelines defines timelines 1..n each with dims=1 and the given store.
func defineTimelines(t *testing.T, s *PebbleStore, ids ...TimelineID) {
	t.Helper()
	for _, id := range ids {
		if err := s.DefineTimeline(id, TimelineConfig{Name: "test", Dims: 1}); err != nil {
			t.Fatalf("DefineTimeline %d: %v", id, err)
		}
	}
}

// appendEvents writes n events starting at base with 1-second spacing.
func appendEvents(t *testing.T, s *PebbleStore, tid TimelineID, dims []uint64, base time.Time, n int, val float64) {
	t.Helper()
	ctx := context.Background()
	for i := 0; i < n; i++ {
		err := s.Append(ctx, Event{
			Timeline: tid,
			Dims:     dims,
			Time:     base.Add(time.Duration(i) * time.Second),
			Nums:     []float64{val},
		})
		if err != nil {
			t.Fatalf("Append event %d: %v", i, err)
		}
	}
}

// epoch is a valid point in the past usable as a query lower bound.
// time.Time{} (zero value) predates the Unix epoch and is rejected by EncodeKey.
var epoch = time.Unix(0, 0).UTC()

// countEvents returns the number of events readable in timeline tid.
func countEvents(t *testing.T, s *PebbleStore, tid TimelineID) uint64 {
	t.Helper()
	res, err := s.RangeAggregate(context.Background(), RangeAllQuery{
		Timeline: tid,
		Dims:     []uint64{0},
		From:     epoch,
		To:       time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("RangeAggregate on timeline %d: %v", tid, err)
	}
	return res.Count
}

// readRollupEvent reads the single rollup event expected in tid after a run.
func readRollupEvent(t *testing.T, s *PebbleStore, tid TimelineID) Event {
	t.Helper()
	events, err := s.QueryRange(context.Background(), RangeQuery{
		Timeline: tid,
		Dims:     []uint64{0},
		From:     epoch,
		To:       time.Now().Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("QueryRange on timeline %d: %v", tid, err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 rollup event in timeline %d, got %d", tid, len(events))
	}
	return events[0]
}

var rollupBase = time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)

// ─── registry: structural constraints ────────────────────────────────────────

func TestRollup_DefineRejectsTimeline0AsSource(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2)
	_, err := s.DefineRollup(0, RollupDef{DestTID: 2, BucketDuration: time.Minute})
	if err == nil {
		t.Fatal("expected error for timeline 0 as source")
	}
}

func TestRollup_DefineRejectsTimeline0AsDest(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1)
	_, err := s.DefineRollup(1, RollupDef{DestTID: 0, BucketDuration: time.Minute})
	if err == nil {
		t.Fatal("expected error for timeline 0 as destination")
	}
}

func TestRollup_DefineRejectsSelfLoop(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1)
	_, err := s.DefineRollup(1, RollupDef{DestTID: 1, BucketDuration: time.Minute})
	if err == nil {
		t.Fatal("expected error for self-loop (src == dest)")
	}
}

func TestRollup_DefineRejectsTwoNodeCycle(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2)
	mustDefineRollup(t, s, 1, 2, time.Minute)
	// Now try to define 2→1, which closes the cycle.
	_, err := s.DefineRollup(2, RollupDef{DestTID: 1, BucketDuration: time.Minute})
	if err == nil {
		t.Fatal("expected cycle error for 2→1 after 1→2")
	}
}

func TestRollup_DefineRejectsThreeNodeCycle(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2, 3)
	mustDefineRollup(t, s, 1, 2, time.Minute)
	mustDefineRollup(t, s, 2, 3, 5*time.Minute)
	// 3→1 would close the cycle.
	_, err := s.DefineRollup(3, RollupDef{DestTID: 1, BucketDuration: 15 * time.Minute})
	if err == nil {
		t.Fatal("expected cycle error for 3→1")
	}
}

func TestRollup_DefineRejectsSingleParentViolation(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2, 3)
	mustDefineRollup(t, s, 1, 2, time.Minute)
	// Two different sources both trying to write to timeline 2.
	_, err := s.DefineRollup(3, RollupDef{DestTID: 2, BucketDuration: time.Minute})
	if err == nil {
		t.Fatal("expected error: timeline 2 already has a parent (1→2)")
	}
}

func TestRollup_DefineRejectsDepthExceeded(t *testing.T) {
	// Default max depth is 4 (raw→L1→L2→L3→L4).
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 11, 101, 1001, 2001, 3001)
	mustDefineRollup(t, s, 1, 11, time.Minute)
	mustDefineRollup(t, s, 11, 101, 5*time.Minute)
	mustDefineRollup(t, s, 101, 1001, 15*time.Minute)
	mustDefineRollup(t, s, 1001, 2001, time.Hour)
	// One more level would exceed depth 4.
	_, err := s.DefineRollup(2001, RollupDef{DestTID: 3001, BucketDuration: 6 * time.Hour})
	if err == nil {
		t.Fatal("expected depth error on 5th level")
	}
}

func TestRollup_DefineAllowsMaxDepthExactly(t *testing.T) {
	// A chain of exactly MaxRollupDepth levels must be accepted.
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 11, 101, 1001, 2001)
	mustDefineRollup(t, s, 1, 11, time.Minute)
	mustDefineRollup(t, s, 11, 101, 5*time.Minute)
	mustDefineRollup(t, s, 101, 1001, 15*time.Minute)
	mustDefineRollup(t, s, 1001, 2001, time.Hour) // depth 4 — exactly at limit
}

func TestRollup_DefineRejectsNegativeBucketDuration(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2)
	_, err := s.DefineRollup(1, RollupDef{DestTID: 2, BucketDuration: -time.Minute})
	if err == nil {
		t.Fatal("expected error for non-positive bucket_duration")
	}
}

func TestRollup_DefineRejectsUndefinedSource(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 2)
	_, err := s.DefineRollup(99, RollupDef{DestTID: 2, BucketDuration: time.Minute})
	if err == nil {
		t.Fatal("expected error for undefined source timeline")
	}
}

func TestRollup_DefineRejectsUndefinedDest(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1)
	_, err := s.DefineRollup(1, RollupDef{DestTID: 99, BucketDuration: time.Minute})
	if err == nil {
		t.Fatal("expected error for undefined destination timeline")
	}
}

// ─── registry: read operations ────────────────────────────────────────────────

func TestRollup_GetReturnsDefinition(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2)
	id := mustDefineRollup(t, s, 1, 2, 5*time.Minute)

	def, err := s.GetRollup(1, id)
	if err != nil {
		t.Fatalf("GetRollup: %v", err)
	}
	if def.SourceTID != 1 || def.DestTID != 2 || def.BucketDuration != 5*time.Minute {
		t.Errorf("GetRollup returned wrong def: %+v", def)
	}
}

func TestRollup_GetRejectsWrongSource(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2, 3)
	id := mustDefineRollup(t, s, 1, 2, time.Minute)
	_, err := s.GetRollup(3, id) // id belongs to source 1, not 3
	if err == nil {
		t.Fatal("expected error: rollup ID does not belong to timeline 3")
	}
}

func TestRollup_ListReturnsOnlySourceChildren(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2, 3, 11, 21)
	mustDefineRollup(t, s, 1, 11, time.Minute)
	mustDefineRollup(t, s, 2, 21, time.Minute)

	defs, err := s.ListRollups(1)
	if err != nil {
		t.Fatalf("ListRollups: %v", err)
	}
	if len(defs) != 1 || defs[0].DestTID != 11 {
		t.Errorf("ListRollups(1): expected [{src:1 dst:11}], got %+v", defs)
	}

	defs2, err := s.ListRollups(2)
	if err != nil {
		t.Fatalf("ListRollups(2): %v", err)
	}
	if len(defs2) != 1 || defs2[0].DestTID != 21 {
		t.Errorf("ListRollups(2): expected [{src:2 dst:21}], got %+v", defs2)
	}
}

func TestRollup_ParentReturnsParentDef(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2, 3)
	mustDefineRollup(t, s, 1, 2, time.Minute)
	mustDefineRollup(t, s, 2, 3, 5*time.Minute)

	// Parent of timeline 2 should be the 1→2 definition.
	def, ok := s.RollupParent(2)
	if !ok {
		t.Fatal("RollupParent(2): expected to find parent, got none")
	}
	if def.SourceTID != 1 || def.DestTID != 2 {
		t.Errorf("RollupParent(2): want src=1 dst=2, got %+v", def)
	}

	// Timeline 1 has no rollup parent.
	_, ok = s.RollupParent(1)
	if ok {
		t.Error("RollupParent(1): expected no parent for raw timeline")
	}
}

// ─── rollup tree ──────────────────────────────────────────────────────────────

func TestRollup_TreeStructure(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2, 11, 111, 21)
	mustDefineRollup(t, s, 1, 11, time.Minute)
	mustDefineRollup(t, s, 11, 111, 5*time.Minute)
	mustDefineRollup(t, s, 2, 21, time.Minute)

	tree := s.RollupTree()
	if tree.TID != 0 {
		t.Errorf("tree root TID: want 0, got %d", tree.TID)
	}

	// Find the two root-level raw timelines.
	found := map[TimelineID]bool{}
	for _, child := range tree.Children {
		found[child.TID] = true
	}
	if !found[1] || !found[2] {
		t.Errorf("tree root children: want [1, 2], got %v", tree.Children)
	}

	// Timeline 1 should have timeline 11 as a child.
	var tl1Node *RollupTreeNode
	for _, c := range tree.Children {
		if c.TID == 1 {
			tl1Node = c
		}
	}
	if tl1Node == nil || len(tl1Node.Children) != 1 || tl1Node.Children[0].TID != 11 {
		t.Errorf("tree[1].children: want [11], got %v", tl1Node)
	}
	// Timeline 11 should have timeline 111 as a child.
	tl11Node := tl1Node.Children[0]
	if len(tl11Node.Children) != 1 || tl11Node.Children[0].TID != 111 {
		t.Errorf("tree[11].children: want [111], got %v", tl11Node.Children)
	}
}

// ─── runBucket: output correctness ───────────────────────────────────────────

func TestRollup_RunBucketFieldLayout(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2)
	id := mustDefineRollup(t, s, 1, 2, time.Minute)

	// Write 6 events over 1 minute with known values: 10, 20, 30, 40, 50, 60.
	ctx := context.Background()
	vals := []float64{10, 20, 30, 40, 50, 60}
	for i, v := range vals {
		must(t, s.Append(ctx, Event{
			Timeline: 1,
			Dims:     []uint64{0},
			Time:     rollupBase.Add(time.Duration(i) * 10 * time.Second),
			Nums:     []float64{v},
		}))
	}

	bucketFrom := rollupBase
	bucketTo := rollupBase.Add(time.Minute)
	if err := s.RunRollup(ctx, 1, id, bucketFrom, bucketTo, false); err != nil {
		t.Fatalf("RunRollup: %v", err)
	}

	ev := readRollupEvent(t, s, 2)

	// val0=mean, val1=min, val2=max, val3=sum, val4=count
	wantMean := (10.0 + 20 + 30 + 40 + 50 + 60) / 6
	wantMin := 10.0
	wantMax := 60.0
	wantSum := 210.0
	wantCount := 6.0

	if ev.Nums[0] != wantMean {
		t.Errorf("val0 (mean): want %.2f, got %.2f", wantMean, ev.Nums[0])
	}
	if ev.Nums[1] != wantMin {
		t.Errorf("val1 (min): want %.2f, got %.2f", wantMin, ev.Nums[1])
	}
	if ev.Nums[2] != wantMax {
		t.Errorf("val2 (max): want %.2f, got %.2f", wantMax, ev.Nums[2])
	}
	if ev.Nums[3] != wantSum {
		t.Errorf("val3 (sum): want %.2f, got %.2f", wantSum, ev.Nums[3])
	}
	if ev.Nums[4] != wantCount {
		t.Errorf("val4 (count): want %.2f, got %.2f", wantCount, ev.Nums[4])
	}

	// Timestamp must be the bucket close.
	if !ev.Time.Equal(bucketTo) {
		t.Errorf("rollup event time: want %v, got %v", bucketTo, ev.Time)
	}
}

func TestRollup_RunBucketEmptySourceProducesNoEvent(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2)
	id := mustDefineRollup(t, s, 1, 2, time.Minute)

	// No events in timeline 1.
	ctx := context.Background()
	if err := s.RunRollup(ctx, 1, id, rollupBase, rollupBase.Add(time.Minute), false); err != nil {
		t.Fatalf("RunRollup on empty source: %v", err)
	}
	if n := countEvents(t, s, 2); n != 0 {
		t.Errorf("expected 0 rollup events for empty source, got %d", n)
	}
}

func TestRollup_RunBucketDefaultsToLastClosedBucket(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2)
	id := mustDefineRollup(t, s, 1, 2, time.Minute)

	// Write one event within the last closed 1-minute bucket.
	now := time.Now().UTC()
	lastBucketEnd := now.Truncate(time.Minute)
	lastBucketStart := lastBucketEnd.Add(-time.Minute)
	mid := lastBucketStart.Add(30 * time.Second)

	ctx := context.Background()
	must(t, s.Append(ctx, Event{
		Timeline: 1, Dims: []uint64{0},
		Time: mid, Nums: []float64{42},
	}))

	// Run with zero from/to — should default to the last closed bucket.
	if err := s.RunRollup(ctx, 1, id, time.Time{}, time.Time{}, false); err != nil {
		t.Fatalf("RunRollup (default bucket): %v", err)
	}
	if n := countEvents(t, s, 2); n != 1 {
		t.Errorf("expected 1 rollup event, got %d", n)
	}
}

// ─── cascade run ──────────────────────────────────────────────────────────────

func TestRollup_CascadeRunNoDuplicates(t *testing.T) {
	// The cascade bug would produce 2 events in each child timeline per
	// parent bucket. This test verifies exactly 1 event per level.
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 11, 111)
	id1 := mustDefineRollup(t, s, 1, 11, time.Minute)
	mustDefineRollup(t, s, 11, 111, 5*time.Minute)

	ctx := context.Background()
	appendEvents(t, s, 1, []uint64{0}, rollupBase, 60, 1.0)

	// Run parent with cascade=true.
	from := rollupBase
	to := rollupBase.Add(time.Minute)
	if err := s.RunRollup(ctx, 1, id1, from, to, true); err != nil {
		t.Fatalf("RunRollup cascade: %v", err)
	}

	// Timeline 11 should have exactly 1 rollup event.
	if n := countEvents(t, s, 11); n != 1 {
		t.Errorf("timeline 11 (L1 rollup): want 1 event, got %d", n)
	}
	// Timeline 111 should have exactly 1 rollup event covering the 5-minute bucket.
	if n := countEvents(t, s, 111); n != 1 {
		t.Errorf("timeline 111 (L2 rollup): want 1 event, got %d", n)
	}
}

func TestRollup_CascadeRunThreeLevels(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 11, 111, 1111)
	id1 := mustDefineRollup(t, s, 1, 11, time.Minute)
	mustDefineRollup(t, s, 11, 111, 5*time.Minute)
	mustDefineRollup(t, s, 111, 1111, 15*time.Minute)

	ctx := context.Background()
	appendEvents(t, s, 1, []uint64{0}, rollupBase, 60, 2.0)

	from := rollupBase
	to := rollupBase.Add(time.Minute)
	if err := s.RunRollup(ctx, 1, id1, from, to, true); err != nil {
		t.Fatalf("RunRollup 3-level cascade: %v", err)
	}

	for _, tc := range []struct {
		tid  TimelineID
		want uint64
	}{
		{11, 1}, {111, 1}, {1111, 1},
	} {
		if n := countEvents(t, s, tc.tid); n != tc.want {
			t.Errorf("timeline %d: want %d events, got %d", tc.tid, tc.want, n)
		}
	}
}

func TestRollup_CascadeRunMultiBucketParent(t *testing.T) {
	// Parent window spans 3 buckets. Each child (5-minute) should produce
	// buckets covering the same span.
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 11, 111)
	id1 := mustDefineRollup(t, s, 1, 11, time.Minute)
	mustDefineRollup(t, s, 11, 111, 5*time.Minute)

	ctx := context.Background()
	// 3 minutes of events.
	appendEvents(t, s, 1, []uint64{0}, rollupBase, 180, 1.0)

	from := rollupBase
	to := rollupBase.Add(3 * time.Minute)
	if err := s.RunRollup(ctx, 1, id1, from, to, true); err != nil {
		t.Fatalf("RunRollup multi-bucket cascade: %v", err)
	}

	// 3 buckets at L1 (timeline 11).
	if n := countEvents(t, s, 11); n != 3 {
		t.Errorf("timeline 11: want 3 events (1 per minute), got %d", n)
	}
	// 1 bucket at L2 (timeline 111): 3 minutes fits in one 5-minute window.
	if n := countEvents(t, s, 111); n != 1 {
		t.Errorf("timeline 111: want 1 event, got %d", n)
	}
}

func TestRollup_CascadeFalseDoesNotRunChildren(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 11, 111)
	id1 := mustDefineRollup(t, s, 1, 11, time.Minute)
	mustDefineRollup(t, s, 11, 111, 5*time.Minute)

	ctx := context.Background()
	appendEvents(t, s, 1, []uint64{0}, rollupBase, 60, 1.0)

	if err := s.RunRollup(ctx, 1, id1, rollupBase, rollupBase.Add(time.Minute), false); err != nil {
		t.Fatalf("RunRollup no-cascade: %v", err)
	}

	// L1 rollup (11) should have data.
	if n := countEvents(t, s, 11); n != 1 {
		t.Errorf("timeline 11: want 1 event, got %d", n)
	}
	// L2 rollup (111) should have nothing — cascade=false.
	if n := countEvents(t, s, 111); n != 0 {
		t.Errorf("timeline 111: want 0 events (no cascade), got %d", n)
	}
}

// ─── worker lifecycle ─────────────────────────────────────────────────────────

func TestRollup_WorkerNotStartedOnDefine(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2)
	id := mustDefineRollup(t, s, 1, 2, time.Minute)

	// Worker should not be running immediately after definition.
	status, err := s.RollupStatus(1, id)
	if err != nil {
		t.Fatalf("RollupStatus: %v", err)
	}
	if status.Running {
		t.Error("worker should not be running immediately after DefineRollup")
	}
}

func TestRollup_WorkerStartedByRunRollup(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2)
	id := mustDefineRollup(t, s, 1, 2, time.Minute)

	ctx := context.Background()
	if err := s.RunRollup(ctx, 1, id, rollupBase, rollupBase.Add(time.Minute), false); err != nil {
		t.Fatalf("RunRollup: %v", err)
	}

	status, err := s.RollupStatus(1, id)
	if err != nil {
		t.Fatalf("RollupStatus: %v", err)
	}
	if !status.Running {
		t.Error("worker should be running after RunRollup")
	}
}

func TestRollup_CascadeStartsDescendantWorkers(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 11, 111)
	id1 := mustDefineRollup(t, s, 1, 11, time.Minute)
	id11 := mustDefineRollup(t, s, 11, 111, 5*time.Minute)

	ctx := context.Background()
	if err := s.RunRollup(ctx, 1, id1, rollupBase, rollupBase.Add(time.Minute), true); err != nil {
		t.Fatalf("RunRollup cascade: %v", err)
	}

	// Both workers should now be running.
	for _, tc := range []struct {
		src TimelineID
		id  RollupID
	}{
		{1, id1}, {11, id11},
	} {
		st, err := s.RollupStatus(tc.src, tc.id)
		if err != nil {
			t.Fatalf("RollupStatus(%d): %v", tc.src, err)
		}
		if !st.Running {
			t.Errorf("rollup %s (src=%d) should be running after cascade", tc.id, tc.src)
		}
	}
}

// ─── persistence ─────────────────────────────────────────────────────────────

func TestRollup_PersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := testStoreConfig()
	cfg.RollupCascadeDelete = true

	// Phase 1: create definitions, run one (so it persists Running=true),
	// leave the other unstarted.
	s1, err := NewPebbleStore(dir, cfg, testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("open store phase 1: %v", err)
	}
	ps1 := s1.(*PebbleStore)
	if err := ps1.DefineTimeline(1, TimelineConfig{Name: "raw", Dims: 1}); err != nil {
		t.Fatalf("define tl1: %v", err)
	}
	if err := ps1.DefineTimeline(2, TimelineConfig{Name: "r1", Dims: 1}); err != nil {
		t.Fatalf("define tl2: %v", err)
	}
	if err := ps1.DefineTimeline(3, TimelineConfig{Name: "r2", Dims: 1}); err != nil {
		t.Fatalf("define tl3: %v", err)
	}

	id12 := mustDefineRollup(t, ps1, 1, 2, time.Minute)
	id23 := mustDefineRollup(t, ps1, 2, 3, 5*time.Minute)

	// Start 1→2 only.
	ctx := context.Background()
	if err := ps1.RunRollup(ctx, 1, id12, rollupBase, rollupBase.Add(time.Minute), false); err != nil {
		t.Fatalf("RunRollup: %v", err)
	}
	ps1.Close()

	// Phase 2: reopen and verify.
	s2, err := NewPebbleStore(dir, cfg, testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("open store phase 2: %v", err)
	}
	ps2 := s2.(*PebbleStore)
	t.Cleanup(func() { ps2.Close() })

	// 1→2 was running — should have restarted.
	st12, err := ps2.RollupStatus(1, id12)
	if err != nil {
		t.Fatalf("RollupStatus 1→2 after reopen: %v", err)
	}
	if !st12.Running {
		t.Error("1→2 rollup should be running after reopen (was running before close)")
	}

	// 2→3 was never started — should remain stopped.
	st23, err := ps2.RollupStatus(2, id23)
	if err != nil {
		t.Fatalf("RollupStatus 2→3 after reopen: %v", err)
	}
	if st23.Running {
		t.Error("2→3 rollup should NOT be running after reopen (was never started)")
	}
}

func TestRollup_DefinitionsSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	cfg := testStoreConfig()

	s1, _ := NewPebbleStore(dir, cfg, testPebbleConfig(), "", nil)
	ps1 := s1.(*PebbleStore)
	if err := ps1.DefineTimeline(1, TimelineConfig{Dims: 1}); err != nil {
		t.Fatal(err)
	}
	if err := ps1.DefineTimeline(2, TimelineConfig{Dims: 1}); err != nil {
		t.Fatal(err)
	}
	id := mustDefineRollup(t, ps1, 1, 2, time.Minute)
	ps1.Close()

	s2, _ := NewPebbleStore(dir, cfg, testPebbleConfig(), "", nil)
	ps2 := s2.(*PebbleStore)
	t.Cleanup(func() { ps2.Close() })

	def, err := ps2.GetRollup(1, id)
	if err != nil {
		t.Fatalf("GetRollup after reopen: %v", err)
	}
	if def.SourceTID != 1 || def.DestTID != 2 || def.BucketDuration != time.Minute {
		t.Errorf("definition mismatch after reopen: %+v", def)
	}
}

// ─── status ───────────────────────────────────────────────────────────────────

func TestRollup_StatusTracksEventsWritten(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2)
	id := mustDefineRollup(t, s, 1, 2, time.Minute)

	ctx := context.Background()
	appendEvents(t, s, 1, []uint64{0}, rollupBase, 10, 5.0)
	if err := s.RunRollup(ctx, 1, id, rollupBase, rollupBase.Add(time.Minute), false); err != nil {
		t.Fatalf("RunRollup: %v", err)
	}

	st, err := s.RollupStatus(1, id)
	if err != nil {
		t.Fatalf("RollupStatus: %v", err)
	}
	if st.EventsWritten != 1 {
		t.Errorf("EventsWritten: want 1, got %d", st.EventsWritten)
	}
	if st.LastBucketEnd.IsZero() {
		t.Error("LastBucketEnd should be set after a run")
	}
	if st.LastError != "" {
		t.Errorf("LastError should be empty after a successful run, got %q", st.LastError)
	}
}

func TestRollup_StatusNotFoundError(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1)
	_, err := s.RollupStatus(1, "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent rollup ID")
	}
}

// ─── delete: cascade on/off ───────────────────────────────────────────────────

func TestRollup_DeleteLeafSucceeds(t *testing.T) {
	s := rollupStore(t, false) // cascade=false; leaf has no children
	defineTimelines(t, s, 1, 2)
	id := mustDefineRollup(t, s, 1, 2, time.Minute)

	if err := s.DeleteRollup(1, id); err != nil {
		t.Fatalf("DeleteRollup leaf: %v", err)
	}
	defs, _ := s.ListRollups(1)
	if len(defs) != 0 {
		t.Errorf("expected 0 defs after delete, got %d", len(defs))
	}
}

func TestRollup_DeleteWithDescendantsCascadeTrue(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 11, 111)
	id1 := mustDefineRollup(t, s, 1, 11, time.Minute)
	mustDefineRollup(t, s, 11, 111, 5*time.Minute)

	// Deleting 1→11 should cascade and also delete 11→111.
	if err := s.DeleteRollup(1, id1); err != nil {
		t.Fatalf("DeleteRollup cascade: %v", err)
	}

	defs1, _ := s.ListRollups(1)
	defs11, _ := s.ListRollups(11)
	if len(defs1) != 0 || len(defs11) != 0 {
		t.Errorf("after cascade delete: tl1 defs=%d tl11 defs=%d, want both 0",
			len(defs1), len(defs11))
	}
}

func TestRollup_DeleteWithDescendantsCascadeFalseRejects(t *testing.T) {
	s := rollupStore(t, false) // cascade disabled
	defineTimelines(t, s, 1, 11, 111)
	id1 := mustDefineRollup(t, s, 1, 11, time.Minute)
	mustDefineRollup(t, s, 11, 111, 5*time.Minute)

	err := s.DeleteRollup(1, id1)
	if err == nil {
		t.Fatal("expected error: cannot delete parent when cascade is disabled")
	}

	// Both definitions should still exist.
	defs1, _ := s.ListRollups(1)
	defs11, _ := s.ListRollups(11)
	if len(defs1) != 1 || len(defs11) != 1 {
		t.Errorf("after rejected delete: tl1 defs=%d tl11 defs=%d, want both 1",
			len(defs1), len(defs11))
	}
}

func TestRollup_DeleteCascadeOrderLeavesFirst(t *testing.T) {
	// Asymmetric tree: 1→11→111 and 1→12 (different depths).
	// Cascade delete of 1→11 should remove 111 before 11.
	// Verify by checking the registry is consistent after each removal.
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 11, 111, 12)
	id1_11 := mustDefineRollup(t, s, 1, 11, time.Minute)
	mustDefineRollup(t, s, 11, 111, 5*time.Minute)
	mustDefineRollup(t, s, 1, 12, time.Minute) // sibling branch

	if err := s.DeleteRollup(1, id1_11); err != nil {
		t.Fatalf("DeleteRollup cascade asymmetric: %v", err)
	}

	// 11 and 111 should be gone.
	defs11, _ := s.ListRollups(11)
	if len(defs11) != 0 {
		t.Errorf("timeline 11 defs after cascade delete: want 0, got %d", len(defs11))
	}
	// The sibling branch (1→12) should be unaffected.
	defs12, _ := s.ListRollups(1)
	if len(defs12) != 1 || defs12[0].DestTID != 12 {
		t.Errorf("timeline 1 remaining defs: want [1→12], got %+v", defs12)
	}
}

func TestRollup_DeleteStopsRunningWorker(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2)
	id := mustDefineRollup(t, s, 1, 2, time.Minute)

	ctx := context.Background()
	// Start the worker via RunRollup.
	if err := s.RunRollup(ctx, 1, id, rollupBase, rollupBase.Add(time.Minute), false); err != nil {
		t.Fatalf("RunRollup: %v", err)
	}

	if err := s.DeleteRollup(1, id); err != nil {
		t.Fatalf("DeleteRollup: %v", err)
	}

	// Worker map should no longer contain the entry.
	s.rollupWorkersMu.RLock()
	_, stillRunning := s.rollupWorkers[id]
	s.rollupWorkersMu.RUnlock()
	if stillRunning {
		t.Error("worker goroutine still in map after DeleteRollup")
	}
}

// ─── data deletion ────────────────────────────────────────────────────────────

func TestRollup_DeleteTimelineDataClearsAll(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1)
	ctx := context.Background()

	appendEvents(t, s, 1, []uint64{0}, rollupBase, 100, 1.0)
	if n := countEvents(t, s, 1); n != 100 {
		t.Fatalf("pre-delete count: want 100, got %d", n)
	}

	if err := s.DeleteTimelineData(ctx, 1); err != nil {
		t.Fatalf("DeleteTimelineData: %v", err)
	}
	if n := countEvents(t, s, 1); n != 0 {
		t.Errorf("post-delete count: want 0, got %d", n)
	}
}

func TestRollup_DeleteTimelineDataRejectsTimeline0(t *testing.T) {
	s := rollupStore(t, true)
	ctx := context.Background()
	if err := s.DeleteTimelineData(ctx, 0); err == nil {
		t.Fatal("expected error for DeleteTimelineData on timeline 0")
	}
}

func TestRollup_DeleteTimelineDataPreservesDefinition(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1)
	ctx := context.Background()
	appendEvents(t, s, 1, []uint64{0}, rollupBase, 10, 1.0)
	if err := s.DeleteTimelineData(ctx, 1); err != nil {
		t.Fatalf("DeleteTimelineData: %v", err)
	}
	// Timeline definition must still be readable.
	_, ok := s.Timeline(1)
	if !ok {
		t.Error("timeline 1 definition should survive DeleteTimelineData")
	}
	// Should be able to append new events.
	if err := s.Append(ctx, Event{
		Timeline: 1, Dims: []uint64{0},
		Time: rollupBase.Add(time.Hour), Nums: []float64{99},
	}); err != nil {
		t.Errorf("Append after DeleteTimelineData: %v", err)
	}
}

func TestRollup_PurgeTimelineRangeHappyPath(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1)
	ctx := context.Background()

	// 10 events from t+0s to t+9s.
	appendEvents(t, s, 1, []uint64{0}, rollupBase, 10, 1.0)

	// Purge t+3s to t+7s — should remove events at 3, 4, 5, 6 (to is exclusive).
	purgeFrom := rollupBase.Add(3 * time.Second)
	purgeTo := rollupBase.Add(7 * time.Second)
	if err := s.PurgeTimelineRange(ctx, 1, purgeFrom, purgeTo); err != nil {
		t.Fatalf("PurgeTimelineRange: %v", err)
	}

	// Expect 6 events remaining: 0,1,2 before and 7,8,9 after the range.
	if n := countEvents(t, s, 1); n != 6 {
		t.Errorf("after purge: want 6 events, got %d", n)
	}
}

func TestRollup_PurgeTimelineRangeRejectsTimeline0(t *testing.T) {
	s := rollupStore(t, true)
	ctx := context.Background()
	err := s.PurgeTimelineRange(ctx, 0, rollupBase, rollupBase.Add(time.Hour))
	if err == nil {
		t.Fatal("expected error for PurgeTimelineRange on timeline 0")
	}
}

func TestRollup_PurgeTimelineRangeRejectsInvertedRange(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1)
	ctx := context.Background()
	err := s.PurgeTimelineRange(ctx, 1, rollupBase.Add(time.Hour), rollupBase)
	if err == nil {
		t.Fatal("expected error for to <= from")
	}
}

func TestRollup_PurgeTimelineRangePreservesOutsideEvents(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1)
	ctx := context.Background()
	appendEvents(t, s, 1, []uint64{0}, rollupBase, 60, 1.0)

	// Purge the middle 20 seconds.
	if err := s.PurgeTimelineRange(ctx, 1,
		rollupBase.Add(20*time.Second),
		rollupBase.Add(40*time.Second),
	); err != nil {
		t.Fatalf("PurgeTimelineRange: %v", err)
	}
	// 60 - 20 = 40 events should remain.
	if n := countEvents(t, s, 1); n != 40 {
		t.Errorf("want 40 events after partial purge, got %d", n)
	}
}

// ─── guard: root timeline ─────────────────────────────────────────────────────

func TestRollup_AllMethodsRejectTimeline0(t *testing.T) {
	s := rollupStore(t, true)
	defineTimelines(t, s, 1)
	ctx := context.Background()

	// Every timeline-scoped rollup method must reject TID=0.
	if _, err := s.DefineRollup(0, RollupDef{DestTID: 1, BucketDuration: time.Minute}); err == nil {
		t.Error("DefineRollup(0, ...) should fail")
	}
	if _, err := s.GetRollup(0, "any"); err == nil {
		t.Error("GetRollup(0, ...) should fail")
	}
	if _, err := s.ListRollups(0); err == nil {
		t.Error("ListRollups(0) should fail")
	}
	if err := s.DeleteRollup(0, "any"); err == nil {
		t.Error("DeleteRollup(0, ...) should fail")
	}
	if err := s.RunRollup(ctx, 0, "any", time.Time{}, time.Time{}, false); err == nil {
		t.Error("RunRollup(0, ...) should fail")
	}
	if _, err := s.RollupStatus(0, "any"); err == nil {
		t.Error("RollupStatus(0, ...) should fail")
	}
}

// ─── rollup registry: depth bookkeeping with branching tree ──────────────────

func TestRollup_DepthWithInsertionAtMiddle(t *testing.T) {
	// Build a chain of depth 3 (raw→L1→L2→L3), then try to insert a new
	// node that would push the total depth to 5.
	// This specifically tests that maxChildDepth accounts for existing subtrees.
	s := rollupStore(t, true)
	defineTimelines(t, s, 1, 2, 3, 4, 5, 6)

	// Chain: 1→2→3→4 (depth 3).
	mustDefineRollup(t, s, 1, 2, time.Minute)
	mustDefineRollup(t, s, 2, 3, 5*time.Minute)
	mustDefineRollup(t, s, 3, 4, 15*time.Minute)

	// 4→5 (depth 4 — at the limit).
	mustDefineRollup(t, s, 4, 5, time.Hour)

	// 5→6 would push depth to 5 — must be rejected.
	_, err := s.DefineRollup(5, RollupDef{DestTID: 6, BucketDuration: 6 * time.Hour})
	if err == nil {
		t.Fatal("expected depth error: 5→6 would exceed max depth of 4")
	}
}

// ─── registry file path ───────────────────────────────────────────────────────

func TestRollup_RegistryFileCreated(t *testing.T) {
	dir := t.TempDir()
	cfg := testStoreConfig()
	s, err := NewPebbleStore(dir, cfg, testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	ps := s.(*PebbleStore)
	if err := ps.DefineTimeline(1, TimelineConfig{Dims: 1}); err != nil {
		t.Fatal(err)
	}
	if err := ps.DefineTimeline(2, TimelineConfig{Dims: 1}); err != nil {
		t.Fatal(err)
	}
	mustDefineRollup(t, ps, 1, 2, time.Minute)
	ps.Close()

	// rollup_defs.json must exist after a definition is saved.
	defPath := filepath.Join(dir, rollupDefsFile)
	if _, err := os.Stat(defPath); os.IsNotExist(err) {
		t.Errorf("rollup_defs.json not created at %s", defPath)
	}
}

// TestDeleteTimeline_StoreLevel covers PebbleStore.DeleteTimeline at the store
// layer, where both cascade directions are reachable (the HTTP e2e server runs
// cascade-on only). It checks: a plain delete removes the registry entry; with
// cascade off a timeline that still has rollups refuses deletion and is left
// intact; with cascade on the same delete removes the rollup and the timeline;
// and the structural root (id 0) and undefined timelines are rejected.
func TestDeleteTimeline_StoreLevel(t *testing.T) {
	t.Run("plain delete removes definition", func(t *testing.T) {
		s := rollupStore(t, true)
		defineTimelines(t, s, 1)
		if err := s.DeleteTimeline(context.Background(), 1); err != nil {
			t.Fatalf("DeleteTimeline: %v", err)
		}
		if _, ok := s.Timeline(1); ok {
			t.Error("timeline 1 still defined after delete")
		}
	})

	t.Run("cascade off refuses when rollups exist", func(t *testing.T) {
		s := rollupStore(t, false)
		defineTimelines(t, s, 1, 2)
		mustDefineRollup(t, s, 1, 2, time.Minute)
		err := s.DeleteTimeline(context.Background(), 1)
		if err == nil {
			t.Fatal("expected error deleting timeline with rollups under cascade-off")
		}
		// The timeline must be untouched — delete is all-or-nothing.
		if _, ok := s.Timeline(1); !ok {
			t.Error("timeline 1 was removed despite the refusal")
		}
	})

	t.Run("cascade on removes rollups then timeline", func(t *testing.T) {
		s := rollupStore(t, true)
		defineTimelines(t, s, 1, 2)
		rid := mustDefineRollup(t, s, 1, 2, time.Minute)
		if err := s.DeleteTimeline(context.Background(), 1); err != nil {
			t.Fatalf("DeleteTimeline cascade-on: %v", err)
		}
		if _, ok := s.Timeline(1); ok {
			t.Error("timeline 1 still defined after cascade delete")
		}
		if _, err := s.GetRollup(1, rid); err == nil {
			t.Error("rollup still present after cascade delete of its source timeline")
		}
	})

	t.Run("root timeline 0 is rejected", func(t *testing.T) {
		s := rollupStore(t, true)
		if err := s.DeleteTimeline(context.Background(), 0); err == nil {
			t.Error("expected error deleting structural root timeline 0")
		}
	})

	t.Run("undefined timeline is rejected", func(t *testing.T) {
		s := rollupStore(t, true)
		if err := s.DeleteTimeline(context.Background(), 42); err == nil {
			t.Error("expected error deleting undefined timeline")
		}
	})
}

// TestDeleteTimeline_DeletingMarker covers the in-memory deleting marker that
// makes DeleteTimeline observably atomic to concurrent readers: once marked,
// get() reports the timeline as not-found, so a racing reader sees a clean
// not-found rather than a defined-but-empty timeline. It also checks that a
// failed delete (cascade-off with rollups) rolls the marker back, leaving the
// timeline fully usable.
func TestDeleteTimeline_DeletingMarker(t *testing.T) {
	t.Run("marked timeline reads as not-found", func(t *testing.T) {
		s := rollupStore(t, true)
		defineTimelines(t, s, 1)
		if err := s.reg.markDeleting(1); err != nil {
			t.Fatalf("markDeleting: %v", err)
		}
		// Public get hides it…
		if _, ok := s.reg.get(1); ok {
			t.Error("get returned a timeline that is marked deleting")
		}
		// …but the delete path can still read it.
		if _, ok := s.reg.getForDelete(1); !ok {
			t.Error("getForDelete should still see a marked timeline")
		}
	})

	t.Run("failed cascade-off delete rolls the marker back", func(t *testing.T) {
		s := rollupStore(t, false)
		defineTimelines(t, s, 1, 2)
		mustDefineRollup(t, s, 1, 2, time.Minute)
		if err := s.DeleteTimeline(context.Background(), 1); err == nil {
			t.Fatal("expected cascade-off delete to fail")
		}
		// The marker must be cleared: the timeline is visible and usable again.
		if _, ok := s.reg.get(1); !ok {
			t.Error("timeline 1 still hidden after a failed delete — marker not rolled back")
		}
	})

	t.Run("concurrent reader during delete never sees defined-but-empty", func(t *testing.T) {
		s := rollupStore(t, true)
		defineTimelines(t, s, 1)
		// Seed some data so a defined-but-empty read would be detectable.
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		for i := 0; i < 50; i++ {
			if err := s.Append(context.Background(), Event{
				Timeline: 1, Dims: []uint64{0},
				Time: base.Add(time.Duration(i) * time.Second), Nums: []float64{float64(i)},
			}); err != nil {
				t.Fatalf("append: %v", err)
			}
		}

		var wg sync.WaitGroup
		var sawEmptyDefined int32 // reader saw the timeline as defined but with 0 events
		stop := make(chan struct{})
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				cfg, ok := s.reg.get(1)
				if !ok {
					continue // clean not-found — the acceptable outcome
				}
				_ = cfg
				res, err := s.QueryRange(context.Background(), RangeQuery{
					Timeline: 1, Dims: []uint64{0},
					From: base.Add(-time.Hour), To: base.Add(time.Hour), Limit: 100,
				})
				// If get said "defined" but the query came back empty, that is
				// exactly the defined-but-empty race the marker exists to
				// prevent. (An error here is fine — it means get/query
				// disagreed, which resolves as not-found upstream.)
				if err == nil && len(res) == 0 {
					atomic.StoreInt32(&sawEmptyDefined, 1)
					return
				}
			}
		}()

		// Let the reader spin, then delete concurrently.
		time.Sleep(2 * time.Millisecond)
		if err := s.DeleteTimeline(context.Background(), 1); err != nil {
			t.Fatalf("DeleteTimeline: %v", err)
		}
		close(stop)
		wg.Wait()

		if atomic.LoadInt32(&sawEmptyDefined) == 1 {
			t.Error("reader observed a defined-but-empty timeline during delete — marker did not fence the read")
		}
	})
}
