// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/dynconfig"
)

// openStoreWithDC opens a PebbleStore with a real DynConfig backed by a
// temp file, returning both the store and the DynConfig for test manipulation.
func openStoreWithDC(t *testing.T) (Store, *dynconfig.DynConfig) {
	t.Helper()
	dir := t.TempDir()
	dcPath := filepath.Join(dir, "dynconfig.json")
	// Write an empty JSON object so dynconfig.New doesn't fail.
	must(t, os.WriteFile(dcPath, []byte("{}"), 0644))
	dc, err := dynconfig.New(dcPath)
	if err != nil {
		t.Fatalf("dynconfig.New: %v", err)
	}
	store, err := NewPebbleStore(dir, testStoreConfig(), testPebbleConfig(), "tenant-test", dc)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store, dc
}

// setCoalEnabled sets ts.writecoal in the tenant namespace of dc.
func setCoalEnabled(t *testing.T, dc *dynconfig.DynConfig, enabled bool) {
	t.Helper()
	val, _ := json.Marshal(enabled)
	if err := dc.Set("tenant.tenant-test", "ts.writecoal", val); err != nil {
		t.Fatalf("set ts.writecoal: %v", err)
	}
}

// ─── NoSync tests ────────────────────────────────────────────────────────────

// TestWriteConfig_DefaultsToZero verifies that a timeline with no explicit
// write config returns the zero value (NoSync=false).
func TestWriteConfig_DefaultsToZero(t *testing.T) {
	store := mustOpenStore(t)
	mustDefine(t, store, 1, 1)

	cfg := store.WriteConfig(1)
	if cfg.NoSync {
		t.Errorf("expected zero WriteConfig, got nosync=%v", cfg.NoSync)
	}
}

// TestWriteConfig_UndefinedTimeline verifies WriteConfig on an unknown ID
// returns the zero value without panicking.
func TestWriteConfig_UndefinedTimeline(t *testing.T) {
	store := mustOpenStore(t)
	cfg := store.WriteConfig(99)
	if cfg.NoSync {
		t.Errorf("expected zero WriteConfig for undefined timeline, got %+v", cfg)
	}
}

// TestSetWriteConfig_RejectsUndefined verifies SetWriteConfig returns an error
// for an undefined timeline.
func TestSetWriteConfig_RejectsUndefined(t *testing.T) {
	store := mustOpenStore(t)
	err := store.SetWriteConfig(42, TimelineWriteConfig{NoSync: true})
	if err == nil {
		t.Fatal("expected error for undefined timeline, got nil")
	}
}

// TestSetWriteConfig_RoundTrip verifies get/set round-trip for NoSync.
func TestSetWriteConfig_RoundTrip(t *testing.T) {
	store := mustOpenStore(t)
	mustDefine(t, store, 1, 1)

	for _, want := range []TimelineWriteConfig{{NoSync: true}, {NoSync: false}} {
		if err := store.SetWriteConfig(1, want); err != nil {
			t.Fatalf("SetWriteConfig(%+v): %v", want, err)
		}
		got := store.WriteConfig(1)
		if got != want {
			t.Errorf("WriteConfig: want %+v, got %+v", want, got)
		}
	}
}

// TestSetWriteConfig_Persists verifies NoSync survives store close/reopen.
func TestSetWriteConfig_Persists(t *testing.T) {
	dir := t.TempDir()

	store, err := NewPebbleStore(dir, testStoreConfig(), testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	must(t, store.DefineTimeline(1, TimelineConfig{Dims: 1}))
	must(t, store.SetWriteConfig(1, TimelineWriteConfig{NoSync: true}))
	store.Close()

	store2, err := NewPebbleStore(dir, testStoreConfig(), testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store2.Close()

	got := store2.WriteConfig(1)
	if !got.NoSync {
		t.Errorf("after reopen: want nosync=true, got %+v", got)
	}
}

// TestNoSync_WritesSucceed verifies writes succeed with NoSync enabled.
func TestNoSync_WritesSucceed(t *testing.T) {
	store := mustOpenStore(t)
	mustDefine(t, store, 1, 1)
	must(t, store.SetWriteConfig(1, TimelineWriteConfig{NoSync: true}))

	ctx := context.Background()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	must(t, store.Append(ctx, Event{
		Timeline: 1, Dims: []uint64{1},
		Time: base, Nums: []float64{1.0},
	}))

	events := make([]Event, 10)
	for i := range events {
		events[i] = Event{
			Timeline: 1, Dims: []uint64{1},
			Time: base.Add(time.Duration(i+1) * time.Millisecond),
			Nums: []float64{float64(i)},
		}
	}
	if _, err := store.AppendBatch(ctx, events, 0); err != nil {
		t.Fatalf("AppendBatch with nosync: %v", err)
	}

	res, err := store.RangeAggregate(ctx, RangeAllQuery{
		Timeline: 1, Dims: []uint64{1},
		From: base, To: base.Add(time.Hour),
	})
	must(t, err)
	if res.Count != 11 {
		t.Errorf("count: want 11, got %d", res.Count)
	}
}

// TestNoSync_MixedBatch verifies a batch mixing nosync and sync timelines
// commits correctly and both timelines' data is readable.
func TestNoSync_MixedBatch(t *testing.T) {
	store := mustOpenStore(t)
	mustDefine(t, store, 1, 1)
	mustDefine(t, store, 2, 1)
	must(t, store.SetWriteConfig(1, TimelineWriteConfig{NoSync: true}))

	ctx := context.Background()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	events := []Event{
		{Timeline: 1, Dims: []uint64{1}, Time: base, Nums: []float64{1.0}},
		{Timeline: 2, Dims: []uint64{1}, Time: base, Nums: []float64{2.0}},
	}
	if _, err := store.AppendBatch(ctx, events, 0); err != nil {
		t.Fatalf("mixed batch: %v", err)
	}
	for _, tid := range []TimelineID{1, 2} {
		res, err := store.RangeAggregate(ctx, RangeAllQuery{
			Timeline: tid, Dims: []uint64{1},
			From: base, To: base.Add(time.Hour),
		})
		must(t, err)
		if res.Count != 1 {
			t.Errorf("timeline %d: want count 1, got %d", tid, res.Count)
		}
	}
}

// ─── WriteCoal via dynconfig tests ───────────────────────────────────────────

// TestWriteCoal_DisabledByDefault verifies coalEnabled() returns false when
// dynconfig has no ts.writecoal key.
func TestWriteCoal_DisabledByDefault(t *testing.T) {
	store, _ := openStoreWithDC(t)
	ps := store.(*PebbleStore)
	if ps.coalEnabled() {
		t.Error("coalEnabled() should be false when dynconfig key is absent")
	}
}

// TestWriteCoal_EnabledViaGlobal verifies coalEnabled() returns true when the
// "global" namespace has ts.writecoal=true.
func TestWriteCoal_EnabledViaGlobal(t *testing.T) {
	store, dc := openStoreWithDC(t)
	ps := store.(*PebbleStore)

	val, _ := json.Marshal(true)
	must(t, dc.Set("global", "ts.writecoal", val))

	if !ps.coalEnabled() {
		t.Error("coalEnabled() should be true after setting global ts.writecoal=true")
	}
}

// TestWriteCoal_TenantOverridesGlobal verifies the tenant namespace takes
// precedence over "global".
func TestWriteCoal_TenantOverridesGlobal(t *testing.T) {
	store, dc := openStoreWithDC(t)
	ps := store.(*PebbleStore)

	// Set global=true, tenant=false — tenant wins.
	glob, _ := json.Marshal(true)
	tenant, _ := json.Marshal(false)
	must(t, dc.Set("global", "ts.writecoal", glob))
	must(t, dc.Set("tenant.tenant-test", "ts.writecoal", tenant))

	if ps.coalEnabled() {
		t.Error("coalEnabled() should be false when tenant override is false")
	}
}

// TestWriteCoal_EventsCommitted verifies that events appended with coalEnabled
// are committed and readable after the flush interval.
func TestWriteCoal_EventsCommitted(t *testing.T) {
	store, dc := openStoreWithDC(t)
	mustDefine(t, store, 1, 1)
	setCoalEnabled(t, dc, true)

	ctx := context.Background()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	const n = 50

	for i := 0; i < n; i++ {
		must(t, store.Append(ctx, Event{
			Timeline: 1, Dims: []uint64{1},
			Time: base.Add(time.Duration(i) * time.Millisecond),
			Nums: []float64{float64(i)},
		}))
	}
	time.Sleep(100 * time.Millisecond)

	res, err := store.RangeAggregate(ctx, RangeAllQuery{
		Timeline: 1, Dims: []uint64{1},
		From: base, To: base.Add(time.Hour),
	})
	must(t, err)
	if res.Count != n {
		t.Errorf("count: want %d, got %d", n, res.Count)
	}
}

// TestWriteCoal_BatchAppend verifies AppendBatch under coalEnabled.
func TestWriteCoal_BatchAppend(t *testing.T) {
	store, dc := openStoreWithDC(t)
	mustDefine(t, store, 1, 1)
	mustDefine(t, store, 2, 1)
	setCoalEnabled(t, dc, true)

	ctx := context.Background()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	events := make([]Event, 20)
	for i := range events {
		tid := TimelineID(1 + (i % 2))
		events[i] = Event{
			Timeline: tid, Dims: []uint64{1},
			Time: base.Add(time.Duration(i) * time.Millisecond),
			Nums: []float64{float64(i)},
		}
	}
	accepted, err := store.AppendBatch(ctx, events, 0)
	must(t, err)
	if accepted != 20 {
		t.Errorf("accepted: want 20, got %d", accepted)
	}
	time.Sleep(100 * time.Millisecond)

	for _, tc := range []struct {
		tid   TimelineID
		wantN uint64
	}{{1, 10}, {2, 10}} {
		res, err := store.RangeAggregate(ctx, RangeAllQuery{
			Timeline: tc.tid, Dims: []uint64{1},
			From: base, To: base.Add(time.Hour),
		})
		must(t, err)
		if res.Count != tc.wantN {
			t.Errorf("timeline %d: want %d events, got %d", tc.tid, tc.wantN, res.Count)
		}
	}
}

// TestWriteCoal_ConcurrentSenders verifies the coalescer handles concurrent
// Append calls from multiple goroutines without dropping events.
func TestWriteCoal_ConcurrentSenders(t *testing.T) {
	store, dc := openStoreWithDC(t)
	mustDefine(t, store, 1, 1)
	setCoalEnabled(t, dc, true)

	ctx := context.Background()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	const goroutines = 8
	const perGoroutine = 50
	var wg sync.WaitGroup
	var errCount atomic.Int64

	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		g := g
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				ts := base.Add(time.Duration(g*10000+i) * time.Microsecond)
				if err := store.Append(ctx, Event{
					Timeline: 1, Dims: []uint64{1},
					Time: ts, Nums: []float64{float64(g*perGoroutine + i)},
				}); err != nil {
					errCount.Add(1)
				}
			}
		}()
	}
	wg.Wait()

	if n := errCount.Load(); n > 0 {
		t.Errorf("%d Append calls failed under concurrency", n)
	}
	time.Sleep(100 * time.Millisecond)

	res, err := store.RangeAggregate(ctx, RangeAllQuery{
		Timeline: 1, Dims: []uint64{1},
		From: base, To: base.Add(time.Hour),
	})
	must(t, err)
	want := uint64(goroutines * perGoroutine)
	if res.Count != want {
		t.Errorf("count: want %d, got %d", want, res.Count)
	}
}

// TestWriteCoal_LiveToggle verifies toggling coalEnabled via dynconfig
// mid-session keeps data intact.
func TestWriteCoal_LiveToggle(t *testing.T) {
	store, dc := openStoreWithDC(t)
	mustDefine(t, store, 1, 1)

	ctx := context.Background()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	// Phase 1: coal on
	setCoalEnabled(t, dc, true)
	for i := 0; i < 10; i++ {
		must(t, store.Append(ctx, Event{
			Timeline: 1, Dims: []uint64{1},
			Time: base.Add(time.Duration(i) * time.Millisecond),
			Nums: []float64{float64(i)},
		}))
	}
	time.Sleep(50 * time.Millisecond)

	// Phase 2: coal off — goes direct
	setCoalEnabled(t, dc, false)
	for i := 10; i < 20; i++ {
		must(t, store.Append(ctx, Event{
			Timeline: 1, Dims: []uint64{1},
			Time: base.Add(time.Duration(i) * time.Millisecond),
			Nums: []float64{float64(i)},
		}))
	}

	// Phase 3: coal on again
	setCoalEnabled(t, dc, true)
	for i := 20; i < 30; i++ {
		must(t, store.Append(ctx, Event{
			Timeline: 1, Dims: []uint64{1},
			Time: base.Add(time.Duration(i) * time.Millisecond),
			Nums: []float64{float64(i)},
		}))
	}
	time.Sleep(50 * time.Millisecond)

	res, err := store.RangeAggregate(ctx, RangeAllQuery{
		Timeline: 1, Dims: []uint64{1},
		From: base, To: base.Add(time.Hour),
	})
	must(t, err)
	if res.Count != 30 {
		t.Errorf("count: want 30, got %d", res.Count)
	}
}

// TestCoalParams_LiveTuning verifies that coalParams() reads dynconfig values
// rather than the compile-time defaults.
func TestCoalParams_LiveTuning(t *testing.T) {
	store, dc := openStoreWithDC(t)
	ps := store.(*PebbleStore)

	// No override: should return the store's initialised defaults.
	interval, maxEvt := ps.coalParams()
	if interval != ps.coalFlushInterval {
		t.Errorf("default interval: want %v, got %v", ps.coalFlushInterval, interval)
	}
	if maxEvt != ps.coalMaxEvents {
		t.Errorf("default maxEvt: want %d, got %d", ps.coalMaxEvents, maxEvt)
	}

	// Set via tenant namespace.
	iv, _ := json.Marshal(int64(25))
	mx, _ := json.Marshal(int64(777))
	must(t, dc.Set("tenant.tenant-test", "ts.coal_flush_interval_ms", iv))
	must(t, dc.Set("tenant.tenant-test", "ts.coal_max_events", mx))

	interval2, maxEvt2 := ps.coalParams()
	if interval2 != 25*time.Millisecond {
		t.Errorf("tuned interval: want 25ms, got %v", interval2)
	}
	if maxEvt2 != 777 {
		t.Errorf("tuned maxEvt: want 777, got %d", maxEvt2)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
