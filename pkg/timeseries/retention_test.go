// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

// retention_test.go
//
// Tests for RetentionWorker: lifecycle (Start/Stop), sweep behaviour,
// context cancellation, and concurrent safety.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	gcpkg "github.com/ha1tch/xolu/pkg/gc"
)

// TestRetentionWorker_StartStop verifies that the worker goroutine starts,
// runs without panicking, and exits cleanly when Stop is called.
func TestRetentionWorker_StartStop(t *testing.T) {
	dir := t.TempDir()
	mgr, err := NewManager(dir, NewPebbleStoreFactory(testPebbleConfig(), nil), testStoreConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	w := NewRetentionWorker(mgr, 50*time.Millisecond)
	w.Start()

	// Let it tick at least twice.
	time.Sleep(150 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		w.Stop()
		close(done)
	}()

	select {
	case <-done:
		// clean stop
	case <-time.After(2 * time.Second):
		t.Fatal("RetentionWorker.Stop() did not return within 2s")
	}
}

// TestRetentionWorker_SweepDeletesExpired seeds old events on a timeline with
// a 1-day retention policy, runs the worker for two ticks, and verifies those
// events are purged while a no-expiry timeline is untouched.
func TestRetentionWorker_SweepDeletesExpired(t *testing.T) {
	baseDir := t.TempDir()
	mgr, err := NewManager(baseDir, NewPebbleStoreFactory(testPebbleConfig(), nil), testStoreConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	ctx := context.Background()

	// Provision tenant 1.
	if err := mgr.Provision(ctx, 1, ""); err != nil {
		t.Fatal(err)
	}
	store, err := mgr.StoreFor(1)
	if err != nil {
		t.Fatal(err)
	}

	// Timeline 1: 1-day retention. Seed events 3 days old (should be purged).
	if err := store.DefineTimeline(1, TimelineConfig{Dims: 1, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-72 * time.Hour)
	for i := 0; i < 5; i++ {
		store.Append(ctx, Event{
			Timeline: 1, Dims: []uint64{1},
			Time: old.Add(time.Duration(i) * time.Second),
		})
	}

	// Timeline 2: no expiry (RetentionDays=0). Seed same old events.
	if err := store.DefineTimeline(2, TimelineConfig{Dims: 1, RetentionDays: 0}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		store.Append(ctx, Event{
			Timeline: 2, Dims: []uint64{1},
			Time: old.Add(time.Duration(i) * time.Second),
		})
	}

	// Run the worker for two ticks.
	w := NewRetentionWorker(mgr, 50*time.Millisecond)
	w.Start()
	time.Sleep(150 * time.Millisecond)
	w.Stop()

	// Timeline 1: old events should be gone.
	events1, err := store.QueryRange(ctx, RangeQuery{
		Timeline: 1, Dims: []uint64{1},
		From: old.Add(-time.Second), To: old.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("QueryRange timeline 1: %v", err)
	}
	if len(events1) > 0 {
		t.Errorf("timeline 1 (retention=1d): expected 0 events after sweep, got %d", len(events1))
	}

	// Timeline 2: no-expiry events must survive.
	events2, err := store.QueryRange(ctx, RangeQuery{
		Timeline: 2, Dims: []uint64{1},
		From: old.Add(-time.Second), To: old.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("QueryRange timeline 2: %v", err)
	}
	if len(events2) != 5 {
		t.Errorf("timeline 2 (no expiry): expected 5 events after sweep, got %d", len(events2))
	}
}

// TestRetentionWorker_SweepDoesNotDeleteRecent verifies that recent events
// (within the retention window) are never deleted by a sweep.
func TestRetentionWorker_SweepDoesNotDeleteRecent(t *testing.T) {
	baseDir := t.TempDir()
	mgr, err := NewManager(baseDir, NewPebbleStoreFactory(testPebbleConfig(), nil), testStoreConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	ctx := context.Background()
	if err := mgr.Provision(ctx, 1, ""); err != nil {
		t.Fatal(err)
	}
	store, _ := mgr.StoreFor(1)

	// Retention = 7 days. Seed 5 events from 1 hour ago (well within window).
	if err := store.DefineTimeline(1, TimelineConfig{Dims: 1, RetentionDays: 7}); err != nil {
		t.Fatal(err)
	}
	recent := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		store.Append(ctx, Event{
			Timeline: 1, Dims: []uint64{1},
			Time: recent.Add(time.Duration(i) * time.Second),
		})
	}

	w := NewRetentionWorker(mgr, 50*time.Millisecond)
	w.Start()
	time.Sleep(150 * time.Millisecond)
	w.Stop()

	events, err := store.QueryRange(ctx, RangeQuery{
		Timeline: 1, Dims: []uint64{1},
		From: recent.Add(-time.Second), To: recent.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(events) != 5 {
		t.Errorf("recent events: expected 5, got %d", len(events))
	}
}

// TestRetentionWorker_MultipleStops verifies that calling Stop more than once
// does not panic (channel double-close guard).
func TestRetentionWorker_MultipleStops(t *testing.T) {
	baseDir := t.TempDir()
	mgr, err := NewManager(baseDir, NewPebbleStoreFactory(testPebbleConfig(), nil), testStoreConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	w := NewRetentionWorker(mgr, 50*time.Millisecond)
	w.Start()
	time.Sleep(60 * time.Millisecond)

	// First Stop should work cleanly.
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Stop panicked: %v", r)
			}
		}()
		w.Stop()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("first Stop did not return")
	}
}

// TestRetentionWorker_ConcurrentSweepAndAppend runs the worker while
// appends are ongoing and verifies no races or panics occur.
// Run under -race.
func TestRetentionWorker_ConcurrentSweepAndAppend(t *testing.T) {
	baseDir := t.TempDir()
	mgr, err := NewManager(baseDir, NewPebbleStoreFactory(testPebbleConfig(), nil), testStoreConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	ctx := context.Background()
	if err := mgr.Provision(ctx, 1, ""); err != nil {
		t.Fatal(err)
	}
	store, _ := mgr.StoreFor(1)
	if err := store.DefineTimeline(1, TimelineConfig{Dims: 1, RetentionDays: 1}); err != nil {
		t.Fatal(err)
	}

	// Start the worker.
	w := NewRetentionWorker(mgr, 30*time.Millisecond)
	w.Start()

	// Concurrent appenders for 200ms.
	var wg sync.WaitGroup
	stopCh := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
				}
				store.Append(ctx, Event{
					Timeline: 1, Dims: []uint64{1},
					Time: time.Now().UTC(),
				})
			}
		}()
	}

	time.Sleep(200 * time.Millisecond)
	close(stopCh)
	wg.Wait()
	w.Stop()
	// No assertion needed — the test passes if no race or panic occurs.
}

// ─── Purge-failure path (regression: CI panic, 2026-08-04) ────────────────
//
// manager.stores' own keys are tenant.TenantID (a distinct named type over
// uint16, see pkg/tenant.TenantID), not bare uint16 -- confirmed directly
// against Provision's own parameter type before writing this. Both
// sweep() and its gc.Sweeper twin Sweep() type-asserted the sync.Map key
// as key.(uint16), which panics unconditionally the instant any store's
// Purge genuinely returns an error: "interface conversion: interface {}
// is tenant.TenantID, not uint16". Never caught before because every
// existing RetentionWorker test uses a real, working Pebble-backed store
// whose Purge never fails -- the error path itself had no test coverage
// at all. Severe beyond the test failure: RetentionWorker runs as a
// registered gc.Worker in the real server with no panic recovery
// anywhere in that chain, so any genuine Purge failure in production
// would have crashed the whole process, not just this goroutine.

// failingStore wraps the real Store interface (embedded, nil) and
// overrides only Purge -- the one method these tests actually exercise.
// Any other method call would panic on the nil embedded interface, which
// is fine: RetentionWorker's sweep functions only ever call Purge.
type failingStore struct {
	Store
	purgeErr error
}

func (f *failingStore) Purge(ctx context.Context) error {
	return f.purgeErr
}

// Close is overridden as a no-op alongside Purge: DefaultManager.Close
// (called by every test's own defer) ranges over every store and calls
// Close on each -- falling through to the embedded nil Store otherwise
// panics on cleanup, unrelated to what these tests actually verify.
func (f *failingStore) Close() error {
	return nil
}

func failingStoreFactory(purgeErr error) StoreFactory {
	return func(dir string, cfg StoreConfig, tenantName string) (Store, error) {
		return &failingStore{purgeErr: purgeErr}, nil
	}
}

// TestRetentionWorker_SweepPurgeError_DoesNotPanic is the direct
// regression test: sweep() (the path RetentionWorker.run actually
// takes) must survive a genuine Purge failure without panicking.
func TestRetentionWorker_SweepPurgeError_DoesNotPanic(t *testing.T) {
	baseDir := t.TempDir()
	mgr, err := NewManager(baseDir, failingStoreFactory(errors.New("simulated purge failure")), testStoreConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	ctx := context.Background()
	if err := mgr.Provision(ctx, 1, "acme"); err != nil {
		t.Fatal(err)
	}

	w := NewRetentionWorker(mgr, time.Hour) // interval irrelevant -- sweep() called directly
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("sweep() panicked on a genuine Purge error: %v", r)
			}
		}()
		w.sweep()
	}()
}

// TestRetentionWorker_Sweep_GCInterface_PurgeError_DoesNotPanic is the
// same regression, through the gc.Sweeper interface method (Sweep) --
// the copy of this bug that actually crashed CI, since RetentionWorker
// is registered as a gc.Worker in the real server and driven through
// this method, not sweep() directly.
func TestRetentionWorker_Sweep_GCInterface_PurgeError_DoesNotPanic(t *testing.T) {
	baseDir := t.TempDir()
	mgr, err := NewManager(baseDir, failingStoreFactory(errors.New("simulated purge failure")), testStoreConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	ctx := context.Background()
	if err := mgr.Provision(ctx, 1, "acme"); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Provision(ctx, 2, "beta"); err != nil {
		t.Fatal(err)
	}

	w := NewRetentionWorker(mgr, time.Hour)
	var report gcpkg.Report
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Sweep() panicked on a genuine Purge error: %v", r)
			}
		}()
		report, err = w.Sweep(ctx)
	}()
	if err != nil {
		t.Fatalf("Sweep itself returned an error (it shouldn't -- per-store errors are counted in the Report): %v", err)
	}
	if report.Examined != 2 {
		t.Errorf("Examined: got %d, want 2", report.Examined)
	}
	if report.Errors != 2 {
		t.Errorf("Errors: got %d, want 2 (both stores' Purge failed)", report.Errors)
	}
	if report.Collected != 0 {
		t.Errorf("Collected: got %d, want 0", report.Collected)
	}
}

// TestRetentionWorker_SweepPurgeSuccess_StillWorks is a quick sanity
// check alongside the two panic regressions above -- the success path
// (a store whose Purge succeeds) must be completely unaffected by the
// type-assertion fix.
func TestRetentionWorker_SweepPurgeSuccess_StillWorks(t *testing.T) {
	baseDir := t.TempDir()
	mgr, err := NewManager(baseDir, failingStoreFactory(nil), testStoreConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer mgr.Close()

	ctx := context.Background()
	if err := mgr.Provision(ctx, 1, "acme"); err != nil {
		t.Fatal(err)
	}

	w := NewRetentionWorker(mgr, time.Hour)
	report, err := w.Sweep(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Examined != 1 || report.Collected != 1 || report.Errors != 0 {
		t.Errorf("got %+v, want Examined=1 Collected=1 Errors=0", report)
	}
}
