// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

// dxp_adapter_test.go — ts's dxp.Participant (T-86), tested standalone
// against a hand-wired harness, matching this item's own register
// sequencing note: "can be built alongside item 20 and T-84,
// independent of the coordinator's own implementation." Not exercised
// through dispatchDxpTxnCore or any HTTP path — ts is deliberately not
// wired into dxpPrimitiveOps/dxpEngineOf yet (see dxp_adapter.go's own
// doc for why), so there is no HTTP path to exercise it through until
// the phased execution path exists too.

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/dxp"
)

func mustOpenPebbleStore(t *testing.T) *PebbleStore {
	t.Helper()
	store, err := NewPebbleStore(t.TempDir(), testStoreConfig(), testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	ps, ok := store.(*PebbleStore)
	if !ok {
		t.Fatalf("NewPebbleStore returned %T, want *PebbleStore", store)
	}
	return ps
}

const testTenant = "t0"

func TestTsAdapter_Reserve_Succeeds(t *testing.T) {
	ps := mustOpenPebbleStore(t)
	mustDefine(t, ps, 1, 2)
	cache := dxp.NewMemCache()
	a := NewAdapter(ps, cache)

	ap := AppendParams{Timeline: 1, Dims: []uint64{10, 20}, TimeUnixNs: base.UnixNano(), Nums: []float64{1.5}}
	claim, err := a.Reserve(context.Background(), testTenant, ap, "txn-1", "p1", futureDeadlineNs(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if claim.Primitive != "ts" || claim.Resource != "ts:1" || claim.Amount != 1 {
		t.Errorf("unexpected claim shape: %+v", claim)
	}

	live := cache.ClaimsFor(testTenant, "ts", "ts:1")
	if len(live) != 1 || live[0].Txn != "txn-1" {
		t.Errorf("expected exactly 1 live claim held for txn-1, got %+v", live)
	}
}

func TestTsAdapter_Reserve_UndefinedTimeline_Refused(t *testing.T) {
	ps := mustOpenPebbleStore(t)
	cache := dxp.NewMemCache()
	a := NewAdapter(ps, cache)

	ap := AppendParams{Timeline: 99, Dims: []uint64{1}, TimeUnixNs: base.UnixNano()}
	_, err := a.Reserve(context.Background(), testTenant, ap, "txn-1", "p1", futureDeadlineNs(), dxp.Pessimistic)
	if err == nil {
		t.Fatal("expected refusal for an undefined timeline, got nil error")
	}
}

func TestTsAdapter_Reserve_WrongDimsCount_Refused(t *testing.T) {
	ps := mustOpenPebbleStore(t)
	mustDefine(t, ps, 1, 3) // timeline expects 3 dims
	cache := dxp.NewMemCache()
	a := NewAdapter(ps, cache)

	ap := AppendParams{Timeline: 1, Dims: []uint64{10, 20}, TimeUnixNs: base.UnixNano()} // only 2
	_, err := a.Reserve(context.Background(), testTenant, ap, "txn-1", "p1", futureDeadlineNs(), dxp.Pessimistic)
	if err == nil {
		t.Fatal("expected refusal for a dims-count mismatch, got nil error")
	}
}

func TestTsAdapter_Reserve_InvalidEvent_Refused(t *testing.T) {
	ps := mustOpenPebbleStore(t)
	mustDefine(t, ps, 1, 1)
	cache := dxp.NewMemCache()
	a := NewAdapter(ps, cache)

	// timeline ID 0 is reserved -- validateEvent's own first check.
	// Reserve against timeline 1 (defined) but craft params whose
	// Nums trip validateEvent's own NaN check instead, since Timeline
	// itself is also used for the registry lookup and would fail
	// there first if set to 0.
	ap := AppendParams{Timeline: 1, Dims: []uint64{1}, TimeUnixNs: base.UnixNano(), Nums: []float64{math.NaN()}}
	_, err := a.Reserve(context.Background(), testTenant, ap, "txn-1", "p1", futureDeadlineNs(), dxp.Pessimistic)
	if err == nil {
		t.Fatal("expected refusal for a NaN numeric field, got nil error")
	}
}

func TestTsAdapter_Validate_Succeeds(t *testing.T) {
	ps := mustOpenPebbleStore(t)
	mustDefine(t, ps, 1, 1)
	cache := dxp.NewMemCache()
	a := NewAdapter(ps, cache)

	ap := AppendParams{Timeline: 1, Dims: []uint64{5}, TimeUnixNs: base.UnixNano()}
	claim, err := a.Reserve(context.Background(), testTenant, ap, "txn-1", "p1", futureDeadlineNs(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := a.Validate(context.Background(), claim); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestTsAdapter_Validate_TimelineDeletedSinceReserve_Refused(t *testing.T) {
	ps := mustOpenPebbleStore(t)
	mustDefine(t, ps, 1, 1)
	cache := dxp.NewMemCache()
	a := NewAdapter(ps, cache)

	ap := AppendParams{Timeline: 1, Dims: []uint64{5}, TimeUnixNs: base.UnixNano()}
	claim, err := a.Reserve(context.Background(), testTenant, ap, "txn-1", "p1", futureDeadlineNs(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := ps.reg.delete(1); err != nil {
		t.Fatalf("delete timeline: %v", err)
	}
	if err := a.Validate(context.Background(), claim); err == nil {
		t.Fatal("expected refusal after the timeline was deleted, got nil error")
	}
}

// TestTsAdapter_Execute_WritesViaBatch_ReadableAfterCommit is the core
// proof this adapter's whole point rests on: Execute must write
// through a real *pebble.Batch (dxp.PebbleStore, the coordinator's own
// abstraction, never Append/the coalescer) such that committing that
// batch produces a genuinely readable, correctly-decodable entry --
// not just "Execute returned no error".
func TestTsAdapter_Execute_WritesViaBatch_ReadableAfterCommit(t *testing.T) {
	ps := mustOpenPebbleStore(t)
	mustDefine(t, ps, 1, 1)
	cache := dxp.NewMemCache()
	a := NewAdapter(ps, cache)

	ap := AppendParams{Timeline: 1, Dims: []uint64{7}, TimeUnixNs: base.UnixNano(), Nums: []float64{42.5}}
	claim, err := a.Reserve(context.Background(), testTenant, ap, "txn-1", "p1", futureDeadlineNs(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := a.Validate(context.Background(), claim); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	batch := ps.db.NewBatch()
	store := dxp.NewPebbleStore(batch)
	if _, err := a.Execute(context.Background(), store, claim); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := store.Commit(context.Background()); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	events, err := ps.QueryRange(context.Background(), RangeQuery{
		Timeline: 1, Dims: []uint64{7}, From: base.Add(-time.Minute), To: base.Add(time.Minute), Limit: 10,
	})
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 readable event after commit, got %d", len(events))
	}
	if len(events[0].Nums) != 1 || events[0].Nums[0] != 42.5 {
		t.Errorf("event Nums mismatch: got %v", events[0].Nums)
	}

	// The counter and first-write bookkeeping Execute updates directly
	// (bypassing Append) must reflect the write too, not just the raw
	// pebble entry.
	stats, err := ps.TimelineStats(context.Background(), 1)
	if err != nil {
		t.Fatalf("TimelineStats: %v", err)
	}
	if stats.TotalEvents != 1 {
		t.Errorf("expected counter to show 1 event, got %d", stats.TotalEvents)
	}
}

// TestTsAdapter_Execute_AbortedBatch_NothingPersists confirms an
// Abort()ed batch (dxp.PebbleStore.Abort -> Batch.Close) leaves no
// trace — the coordinator's own rollback path for a torn or refused
// dispatch must not silently persist a ts write.
func TestTsAdapter_Execute_AbortedBatch_NothingPersists(t *testing.T) {
	ps := mustOpenPebbleStore(t)
	mustDefine(t, ps, 1, 1)
	cache := dxp.NewMemCache()
	a := NewAdapter(ps, cache)

	ap := AppendParams{Timeline: 1, Dims: []uint64{9}, TimeUnixNs: base.UnixNano()}
	claim, err := a.Reserve(context.Background(), testTenant, ap, "txn-1", "p1", futureDeadlineNs(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	batch := ps.db.NewBatch()
	store := dxp.NewPebbleStore(batch)
	if _, err := a.Execute(context.Background(), store, claim); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if err := store.Abort(context.Background()); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	events, err := ps.QueryRange(context.Background(), RangeQuery{
		Timeline: 1, Dims: []uint64{9}, From: base.Add(-time.Minute), To: base.Add(time.Minute), Limit: 10,
	})
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected zero events after Abort, got %d", len(events))
	}
}

func TestTsAdapter_Execute_WrongStoreType_Refused(t *testing.T) {
	ps := mustOpenPebbleStore(t)
	mustDefine(t, ps, 1, 1)
	cache := dxp.NewMemCache()
	a := NewAdapter(ps, cache)

	ap := AppendParams{Timeline: 1, Dims: []uint64{1}, TimeUnixNs: base.UnixNano()}
	claim, err := a.Reserve(context.Background(), testTenant, ap, "txn-1", "p1", futureDeadlineNs(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}

	// A SQL-backed store handed to a Pebble-only participant must be
	// refused explicitly, matching every SQL adapter's own symmetric
	// check for a Pebble store handed to them.
	_, err = a.Execute(context.Background(), &dxp.SQLStore{}, claim)
	if err == nil {
		t.Fatal("expected refusal for a non-pebble store, got nil error")
	}
}

func TestTsAdapter_Release_ClearsPending_Idempotent(t *testing.T) {
	ps := mustOpenPebbleStore(t)
	mustDefine(t, ps, 1, 1)
	cache := dxp.NewMemCache()
	a := NewAdapter(ps, cache)

	ap := AppendParams{Timeline: 1, Dims: []uint64{1}, TimeUnixNs: base.UnixNano()}
	claim, err := a.Reserve(context.Background(), testTenant, ap, "txn-1", "p1", futureDeadlineNs(), dxp.Pessimistic)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := a.Release(context.Background(), claim); err != nil {
		t.Errorf("Release: %v", err)
	}
	// Releasing an already-cleared txn is a no-op, never an error.
	if err := a.Release(context.Background(), claim); err != nil {
		t.Errorf("second Release (already cleared): %v", err)
	}
	// Execute after Release must fail -- the pending entry is gone.
	batch := ps.db.NewBatch()
	defer batch.Close()
	if _, err := a.Execute(context.Background(), dxp.NewPebbleStore(batch), claim); err == nil {
		t.Error("expected Execute to fail after Release cleared the pending entry")
	}
}

func futureDeadlineNs() int64 {
	return time.Now().Add(time.Minute).UnixNano()
}
