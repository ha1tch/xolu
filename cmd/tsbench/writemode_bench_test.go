// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// writemode_bench_test.go benchmarks the four write-mode combinations:
//
//   default    nosync=false  writecoal=false   one fsync per AppendBatch call
//   nosync     nosync=true   writecoal=false   no fsync; OS decides when to flush
//   writecoal  nosync=false  writecoal=true    fsync amortised over flush window
//   both       nosync=true   writecoal=true    coalesced + no fsync
//
// Run with:
//   go test ./cmd/tsbench/ -run='^$' -bench=BenchmarkWriteMode -benchtime=3s -count=1
//
// Each benchmark runs AppendBatch with batch=500 events for the full
// -benchtime duration. The reported ns/op is the wall time per batch call;
// divide by 500 to get ns/event.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/dynconfig"
	"github.com/ha1tch/xolu/pkg/timeseries"
)

// writeModeConfig describes one combination of write settings.
// writeCoal is now controlled via dynconfig (ts.writecoal key).
type writeModeConfig struct {
	name      string
	noSync    bool
	writeCoal bool // sets ts.writecoal in dynconfig when true
	// coalescer tuning — only meaningful when writeCoal=true
	flushMs int
	maxEvt  int
}

var writeModes = []writeModeConfig{
	{name: "default", noSync: false, writeCoal: false},
	{name: "nosync", noSync: true, writeCoal: false},
	{name: "writecoal", noSync: false, writeCoal: true, flushMs: 10, maxEvt: 2000},
	{name: "both", noSync: true, writeCoal: true, flushMs: 10, maxEvt: 2000},
}

const (
	benchBatchSize = 500
	benchTimeline  = timeseries.TimelineID(1)
)

func openBenchStore(b *testing.B, flushMs, maxEvt int) (timeseries.Store, *dynconfig.DynConfig) {
	b.Helper()
	pcfg := timeseries.PebbleConfig{
		MemtableSize:          64 << 20,
		BlockSize:             32 << 10,
		Compression:           "zstd",
		L0CompactionThreshold: 4,
		CoalFlushIntervalMs:   flushMs,
		CoalMaxEvents:         maxEvt,
	}
	dir := b.TempDir()
	dcPath := filepath.Join(dir, "dynconfig.json")
	if err := os.WriteFile(dcPath, []byte("{}"), 0644); err != nil {
		b.Fatalf("write dynconfig: %v", err)
	}
	dc, err := dynconfig.New(dcPath)
	if err != nil {
		b.Fatalf("dynconfig.New: %v", err)
	}
	store, err := timeseries.NewPebbleStore(dir,
		timeseries.StoreConfig{DefaultRetentionDays: 0}, pcfg, "bench", dc)
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	b.Cleanup(func() { store.Close() })
	return store, dc
}

func makeBatch(base time.Time, offset, n int) []timeseries.Event {
	events := make([]timeseries.Event, n)
	for i := range events {
		v := float64(offset + i)
		events[i] = timeseries.Event{
			Timeline: benchTimeline,
			Dims:     []uint64{1},
			Time:     base.Add(time.Duration(offset+i) * time.Millisecond),
			Nums:     []float64{v, v * 2, v * 3, v * 0.1, v * 0.5, v * 1.1, v * 0.9},
		}
	}
	return events
}

func BenchmarkWriteMode(b *testing.B) {
	for _, mode := range writeModes {
		mode := mode
		b.Run(mode.name, func(b *testing.B) {
			store, dc := openBenchStore(b, mode.flushMs, mode.maxEvt)
			if err := store.DefineTimeline(benchTimeline,
				timeseries.TimelineConfig{Dims: 1}); err != nil {
				b.Fatalf("define: %v", err)
			}
			if err := store.SetWriteConfig(benchTimeline, timeseries.TimelineWriteConfig{
				NoSync: mode.noSync,
			}); err != nil {
				b.Fatalf("set write config: %v", err)
			}
			if mode.writeCoal {
				v, _ := json.Marshal(true)
				if err := dc.Set("tenant.bench", "ts.writecoal", v); err != nil {
					b.Fatalf("set ts.writecoal: %v", err)
				}
			}

			ctx := context.Background()
			base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

			b.ResetTimer()
			b.SetBytes(int64(benchBatchSize)) // report throughput as events/sec via B.SetBytes

			for i := 0; i < b.N; i++ {
				batch := makeBatch(base, i*benchBatchSize, benchBatchSize)
				if _, err := store.AppendBatch(ctx, batch, 0); err != nil {
					b.Fatalf("AppendBatch[%d]: %v", i, err)
				}
			}
		})
	}
}

// BenchmarkWriteMode_FlushTuning exercises writecoal with different flush
// intervals to show the latency/throughput trade-off.
func BenchmarkWriteMode_FlushTuning(b *testing.B) {
	intervals := []struct {
		flushMs int
		maxEvt  int
	}{
		{1, 500},
		{5, 1000},
		{10, 2000},
		{25, 2000},
		{50, 2000},
	}

	for _, iv := range intervals {
		iv := iv
		name := fmt.Sprintf("coal_flush%dms_max%d", iv.flushMs, iv.maxEvt)
		b.Run(name, func(b *testing.B) {
			store, dc := openBenchStore(b, iv.flushMs, iv.maxEvt)
			if err := store.DefineTimeline(benchTimeline,
				timeseries.TimelineConfig{Dims: 1}); err != nil {
				b.Fatalf("define: %v", err)
			}
			v, _ := json.Marshal(true)
			if err := dc.Set("tenant.bench", "ts.writecoal", v); err != nil {
				b.Fatalf("set ts.writecoal: %v", err)
			}
			_ = store

			ctx := context.Background()
			base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

			b.ResetTimer()
			b.SetBytes(int64(benchBatchSize))

			for i := 0; i < b.N; i++ {
				batch := makeBatch(base, i*benchBatchSize, benchBatchSize)
				if _, err := store.AppendBatch(ctx, batch, 0); err != nil {
					b.Fatalf("AppendBatch[%d]: %v", i, err)
				}
			}
		})
	}
}

// BenchmarkWriteMode_Parallel runs each write mode with GOMAXPROCS goroutines
// all calling AppendBatch concurrently. This is where writecoal should show
// its advantage — many callers sharing one fsync per flush interval.
//
// Each goroutine uses its own timeline ID (derived from b.N offset) to avoid
// key collisions on identical timestamps. Timeline IDs 10..10+GOMAXPROCS-1.
func BenchmarkWriteMode_Parallel(b *testing.B) {
	for _, mode := range writeModes {
		mode := mode
		b.Run(mode.name, func(b *testing.B) {
			store, dc := openBenchStore(b, mode.flushMs, mode.maxEvt)
			if mode.writeCoal {
				v, _ := json.Marshal(true)
				if err := dc.Set("tenant.bench", "ts.writecoal", v); err != nil {
					b.Fatalf("set ts.writecoal: %v", err)
				}
			}

			// Pre-define enough timelines for the parallel workers.
			// runtime.GOMAXPROCS(0) goroutines, each gets its own timeline
			// to avoid timestamp collisions under concurrent append.
			const maxWorkers = 32
			for i := 0; i < maxWorkers; i++ {
				tid := timeseries.TimelineID(10 + i)
				if err := store.DefineTimeline(tid, timeseries.TimelineConfig{Dims: 1}); err != nil {
					b.Fatalf("define tl%d: %v", tid, err)
				}
				if err := store.SetWriteConfig(tid, timeseries.TimelineWriteConfig{
					NoSync: mode.noSync,
				}); err != nil {
					b.Fatalf("set write config tl%d: %v", tid, err)
				}
			}

			ctx := context.Background()
			base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

			b.ResetTimer()
			b.SetBytes(int64(benchBatchSize))

			var workerID atomic.Int64
			b.RunParallel(func(pb *testing.PB) {
				id := int(workerID.Add(1)) - 1
				tid := timeseries.TimelineID(10 + (id % maxWorkers))
				iter := 0
				for pb.Next() {
					// Each worker uses its own timeline and its own offset
					// so timestamps are unique within each timeline.
					offset := iter*benchBatchSize + id*1_000_000
					batch := make([]timeseries.Event, benchBatchSize)
					t0 := base.Add(time.Duration(offset) * time.Microsecond)
					for i := range batch {
						v := float64(offset + i)
						batch[i] = timeseries.Event{
							Timeline: tid,
							Dims:     []uint64{1},
							Time:     t0.Add(time.Duration(i) * time.Microsecond),
							Nums:     []float64{v, v * 2, v * 3, v * 0.1, v * 0.5, v * 1.1, v * 0.9},
						}
					}
					if _, err := store.AppendBatch(ctx, batch, 0); err != nil {
						b.Errorf("AppendBatch: %v", err)
					}
					iter++
				}
			})
		})
	}
}
