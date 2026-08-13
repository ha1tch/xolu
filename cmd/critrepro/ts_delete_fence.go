// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ha1tch/xolu/pkg/timeseries"
)

// tsDeleteFenceStore is opened once, lazily, on this scenario's own
// first iteration and reused for every iteration thereafter --
// deliberately the opposite shape from five_bal_legs's own per-
// iteration store, and for a specific reason (2026-08-14, XOT192's
// own follow-up): a first attempt at this scenario mirrored
// five_bal_legs exactly (a fresh, real, disk-backed store per
// iteration) and hung reliably within the first few iterations on
// real hardware, inside a plain Append call, unrelated to any of the
// delete-fence logic this scenario exists to test at all -- traced to
// Pebble's own commit-pipeline log writer stuck "runnable" but
// unscheduled, the signature of scheduling/I/O pressure, not a
// logic deadlock. That symptom is a real, separate, worthwhile
// finding on its own (recorded in XOT192), but it is not what this
// scenario is for: conflating "does the delete-fence race hold under
// true parallelism" with "does rapid repeated store open/close hold
// up" would make a failure here ambiguous about which question it
// was even answering. This version opens one store for the whole run
// and never closes it at all -- deliberately, not an oversight: this
// is a short-lived CLI process, and the cleanest way to keep store-
// lifecycle overhead fully out of the experiment is to not have any
// lifecycle beyond process exit. Each iteration instead gets its own
// fresh, unique timeline ID inside that one shared store, so
// iterations remain independent of each other for the property under
// test even though the store itself is not.
var (
	tsDeleteFenceStoreOnce sync.Once
	tsDeleteFenceStore     timeseries.Store
	tsDeleteFenceStoreErr  error
)

func getTSDeleteFenceStore() timeseries.Store {
	tsDeleteFenceStoreOnce.Do(func() {
		tmpDir, err := os.MkdirTemp("", "critrepro_ts_delete_fence_")
		if err != nil {
			tsDeleteFenceStoreErr = err
			return
		}
		tsDeleteFenceStore, tsDeleteFenceStoreErr = timeseries.NewPebbleStore(
			tmpDir,
			timeseries.StoreConfig{DefaultRetentionDays: 30},
			timeseries.PebbleConfig{
				MemtableSize:          4 * 1024 * 1024,
				BlockSize:             4096,
				Compression:           "snappy",
				L0CompactionThreshold: 4,
				MaxOpenFiles:          50,
			},
			"", nil,
		)
	})
	if tsDeleteFenceStoreErr != nil {
		panic(tsDeleteFenceStoreErr)
	}
	return tsDeleteFenceStore
}

// tsDeleteFenceIteration is the critrepro-side verification for
// XOT192's own fix: QueryRange, Latest, and Aggregate (pkg/timeseries/
// store.go) each re-check the registry immediately before returning
// their own scan results, closing a check-then-scan gap that let a
// concurrent DeleteTimeline's own multi-step teardown complete
// entirely inside the window and return a silent, defined-but-empty
// result. The fix's own margin was reasoned, not measured, to narrow
// under true multi-core parallelism (the same structural shape as
// this tool's own canonical five-bal-legs case) -- this scenario is
// that measurement. Each iteration races all three fixed functions
// independently, concurrently, against the same delete, on the
// shared store described above.
func tsDeleteFenceIteration(iter int) (bool, string) {
	s := getTSDeleteFenceStore()
	// @C04d: route through the same sanctioned int64 crossing every
	// other JSON/transport boundary uses (TimelineIDFromJSON) rather
	// than narrowing the loop's own platform-dependent int directly --
	// caught by this project's own c04dcheck linter, not found by
	// inspection.
	tid, err := timeseries.TimelineIDFromJSON(int64(iter) + 1) // 0 is the reserved structural root
	if err != nil {
		return false, fmt.Sprintf("TimelineIDFromJSON: %v", err)
	}

	if err := s.DefineTimeline(tid, timeseries.TimelineConfig{Name: "critrepro", Dims: 1}); err != nil {
		return false, fmt.Sprintf("DefineTimeline: %v", err)
	}

	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 50; i++ {
		if err := s.Append(ctx, timeseries.Event{
			Timeline: tid, Dims: []uint64{0},
			Time: base.Add(time.Duration(i) * time.Second), Nums: []float64{float64(i)},
		}); err != nil {
			return false, fmt.Sprintf("Append: %v", err)
		}
	}

	var wg sync.WaitGroup
	var sawEmptyQueryRange, sawEmptyLatest, sawEmptyAggregate int32
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
			res, err := s.QueryRange(ctx, timeseries.RangeQuery{
				Timeline: tid, Dims: []uint64{0},
				From: base.Add(-time.Hour), To: base.Add(time.Hour), Limit: 100,
			})
			if err == nil && len(res) == 0 {
				atomic.StoreInt32(&sawEmptyQueryRange, 1)
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			res, err := s.Latest(ctx, timeseries.LatestQuery{Timeline: tid, Dims: []uint64{0}, N: 100})
			if err == nil && len(res) == 0 {
				atomic.StoreInt32(&sawEmptyLatest, 1)
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			res, err := s.Aggregate(ctx, timeseries.AggregateQuery{
				Timeline: tid, Dims: []uint64{0},
				From: base.Add(-time.Hour), To: base.Add(time.Hour),
				Function: "count",
			})
			if err == nil && len(res) == 0 {
				atomic.StoreInt32(&sawEmptyAggregate, 1)
				return
			}
		}
	}()

	time.Sleep(2 * time.Millisecond)
	if err := s.DeleteTimeline(ctx, tid); err != nil {
		close(stop)
		wg.Wait()
		return false, fmt.Sprintf("DeleteTimeline: %v", err)
	}
	close(stop)
	wg.Wait()

	switch {
	case atomic.LoadInt32(&sawEmptyQueryRange) == 1:
		return false, "QueryRange observed a defined-but-empty timeline during delete"
	case atomic.LoadInt32(&sawEmptyLatest) == 1:
		return false, "Latest observed a defined-but-empty timeline during delete"
	case atomic.LoadInt32(&sawEmptyAggregate) == 1:
		return false, "Aggregate observed a defined-but-empty timeline during delete"
	}
	return true, ""
}
