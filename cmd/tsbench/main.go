// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// tsbench — timeseries benchmark comparing six backends:
//
//   xolu-default   PebbleStore, 64 MB memtable, zstd, L0threshold=4
//   xolu-tuned     PebbleStore, 256 MB memtable, zstd, L0threshold=8
//   xolu-highbuf   PebbleStore, 256 MB memtable, snappy, L0threshold=2
//   tstorage       nakabonne/tstorage v0.3.6 (WAL + memory partitions)
//   sqlite         modernc/sqlite v1.29.0 (WAL, sync=NORMAL, WITHOUT ROWID)
//   victoria       VictoriaMetrics v1.102.0 (subprocess, HTTP API)
//
// Usage:
//
//	go run ./cmd/tsbench [flags]
//
// Flags:
//
//	-n int        events per scenario (default 100000)
//	-warmup int   warmup events before timing (default 5000)
//	-batch int    batch size (default 500)
//	-runs int     timed repetitions, median reported (default 3)
//	-seq          include slow sequential single-event append scenario
//	-vm string    path to victoria-metrics binary (default: $GOPATH/bin/victoria-metrics)
//	-vmport int   HTTP port for VictoriaMetrics subprocess (default 18428)
//
// Data model notes:
//
//   xolu events carry 7 float64 fields and 1 uint64 dimension.
//   tstorage carries 1 float64 per data point; 7-field scenarios are N/A.
//   SQLite stores 7 float64 columns per row; multi-field aggregate is server-side.
//   VictoriaMetrics carries 1 float64 per series entry; 7-field payload is
//   written as 7 separate series (val0..val6) with the same timestamp, which
//   correctly stresses its ingestion path but means multi-field agg is N/A
//   in the sense of a single-pass accumulator — we query each series separately.
//
// Timestamp note for VictoriaMetrics:
//
//   VM enforces a hard minimum timestamp of (now - retentionPeriod). The shared
//   synthetic baseTime (2024-01-01) falls outside the default 1-month retention.
//   The VM benchmark therefore uses time.Now() as its base, offset by index in
//   milliseconds. All other backends use the fixed synthetic base for
//   reproducibility; the write pattern (monotone, 1ms spacing) is identical.
//
// VictoriaMetrics write caveat:
//
//   VM accepts data via the /api/v1/import JSON endpoint. The write path goes
//   through an HTTP round-trip, which adds latency not present in the embedded
//   backends. This is the unavoidable cost of VM being a standalone daemon rather
//   than an embeddable library. Reported numbers include HTTP overhead.

package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/nakabonne/tstorage"

	"github.com/ha1tch/xolu/pkg/timeseries"
)

// ─── flags ───────────────────────────────────────────────────────────────────

var (
	flagN      = flag.Int("n", 100_000, "events per scenario")
	flagWarmup = flag.Int("warmup", 5_000, "warmup events (not timed)")
	flagBatch  = flag.Int("batch", 500, "batch size")
	flagRuns   = flag.Int("runs", 3, "timed repetitions (median reported)")
	flagSeq    = flag.Bool("seq", false, "include sequential single-event append")
	flagVM     = flag.String("vm", "", "path to victoria-metrics binary (default: $GOPATH/bin/victoria-metrics)")
	flagVMPort = flag.Int("vmport", 18428, "HTTP port for VictoriaMetrics subprocess")
)

// ─── result registry ─────────────────────────────────────────────────────────

type result struct {
	backend  string
	scenario string
	n        int
	median   time.Duration
	unit     string
}

var results []result

func record(backend, scenario string, n int, durations []time.Duration, unit string) {
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	med := durations[len(durations)/2]
	results = append(results, result{backend, scenario, n, med, unit})
	rate := float64(n) / med.Seconds()
	fmt.Printf("  %-16s  %-32s  %10.0f %-12s  (median %d runs, %v)\n",
		backend, scenario, rate, unit, len(durations), med.Round(time.Millisecond))
}

func recordNA(backend, scenario, note string) {
	fmt.Printf("  %-16s  %-32s  %10s %-12s  %s\n",
		backend, scenario, "N/A", "", note)
}

// ─── shared synthetic base time ──────────────────────────────────────────────

// baseTime is used by all backends except VictoriaMetrics (which uses time.Now()).
var baseTime = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

// ─── xolu PebbleStore scenarios ──────────────────────────────────────────────

func benchXolu(label string, pcfg timeseries.PebbleConfig, n, warmup, batchSize, runs int, doSeq bool) {
	fmt.Printf("\n[%s / PebbleStore  memtable=%dMB  compression=%s  L0threshold=%d]\n",
		label, pcfg.MemtableSize>>20, pcfg.Compression, pcfg.L0CompactionThreshold)

	dir, err := os.MkdirTemp("", "tsbench-xolu-*")
	must(err)
	defer func() { _ = os.RemoveAll(dir) }()

	ctx := context.Background()
	store, err := timeseries.NewPebbleStore(dir,
		timeseries.StoreConfig{DefaultRetentionDays: 0},
		pcfg, "", nil)
	must(err)
	defer func() { _ = store.Close() }()

	must(store.DefineTimeline(1, timeseries.TimelineConfig{Dims: 1}))

	// Warmup
	for i := 0; i < warmup; i++ {
		must(store.Append(ctx, timeseries.Event{
			Timeline: 1,
			Dims:     []uint64{1},
			Time:     baseTime.Add(time.Duration(i-warmup) * time.Millisecond),
			Nums:     []float64{float64(i)},
		}))
	}

	// ── optional sequential single-event append (small n, very slow) ─────
	if doSeq {
		const seqN = 2000
		const seqRuns = 3
		var durs []time.Duration
		for r := 0; r < seqRuns; r++ {
			offset := r * seqN
			t0 := time.Now()
			for i := 0; i < seqN; i++ {
				must(store.Append(ctx, timeseries.Event{
					Timeline: 1,
					Dims:     []uint64{99},
					Time:     baseTime.Add(time.Duration(-(seqRuns*seqN)+(offset+i)) * time.Millisecond),
					Nums:     []float64{float64(i)},
				}))
			}
			durs = append(durs, time.Since(t0))
		}
		record(label, "sequential append", seqN, durs, "events/sec")
	}

	// ── batch append ──────────────────────────────────────────────────────
	events := make([]timeseries.Event, batchSize)
	var batchDurs []time.Duration
	for r := 0; r < runs; r++ {
		offset := r * n
		total := 0
		t0 := time.Now()
		for total < n {
			sz := batchSize
			if n-total < sz {
				sz = n - total
				events = events[:sz]
			}
			for i := 0; i < sz; i++ {
				v := float64(total + i)
				events[i] = timeseries.Event{
					Timeline: 1,
					Dims:     []uint64{1},
					Time:     baseTime.Add(time.Duration(offset+total+i) * time.Millisecond),
					Nums:     []float64{v, v * 2, v * 3, v * 0.1, v * 0.5, v * 1.1, v * 0.9},
				}
			}
			written, err := store.AppendBatch(ctx, events, 0)
			must(err)
			total += written
		}
		batchDurs = append(batchDurs, time.Since(t0))
	}
	record(label, "batch append", n, batchDurs, "events/sec")

	// ── range scan via RangeAggregate (bypasses XOLU-TS012 limit) ─────────
	from := baseTime
	to := baseTime.Add(time.Duration(runs*n+1) * time.Millisecond)
	var scanDurs []time.Duration
	var scanCount uint64
	for r := 0; r < runs; r++ {
		t0 := time.Now()
		res, err := store.RangeAggregate(ctx, timeseries.RangeAllQuery{
			Timeline: 1, Dims: []uint64{1}, From: from, To: to,
		})
		must(err)
		scanCount = res.Count
		scanDurs = append(scanDurs, time.Since(t0))
	}
	record(label, "range scan (agg path)", int(scanCount), scanDurs, "events/sec")

	// ── range sum, 1 field ────────────────────────────────────────────────
	var sumDurs []time.Duration
	for r := 0; r < runs; r++ {
		t0 := time.Now()
		_, err := store.RangeSum(ctx, timeseries.RangeNumQuery{
			Timeline: 1, Dims: []uint64{1}, From: from, To: to, NumField: 0,
		})
		must(err)
		sumDurs = append(sumDurs, time.Since(t0))
	}
	record(label, "range sum (1 field)", 1, sumDurs, "queries/sec")

	// ── range aggregate, all 7 fields, single pass ────────────────────────
	var aggDurs []time.Duration
	for r := 0; r < runs; r++ {
		t0 := time.Now()
		_, err := store.RangeAggregate(ctx, timeseries.RangeAllQuery{
			Timeline: 1, Dims: []uint64{1}, From: from, To: to,
		})
		must(err)
		aggDurs = append(aggDurs, time.Since(t0))
	}
	record(label, "range aggregate (7 fields)", 1, aggDurs, "queries/sec")
}

// ─── tstorage scenarios ───────────────────────────────────────────────────────

func benchTstorage(n, warmup, batchSize, runs int, doSeq bool) {
	fmt.Println("\n[tstorage / nakabonne v0.3.6, WAL + memory partitions]")

	dir, err := os.MkdirTemp("", "tsbench-tstorage-*")
	must(err)
	defer func() { _ = os.RemoveAll(dir) }()

	store, err := tstorage.NewStorage(
		tstorage.WithDataPath(dir),
		tstorage.WithTimestampPrecision(tstorage.Nanoseconds),
		tstorage.WithPartitionDuration(24*time.Hour),
		tstorage.WithRetention(365*24*time.Hour),
	)
	must(err)
	defer func() { _ = store.Close() }()

	labels := []tstorage.Label{{Name: "dim", Value: "1"}}

	// Warmup
	wbatch := make([]tstorage.Row, warmup)
	for i := range wbatch {
		wbatch[i] = tstorage.Row{
			Metric:    "m",
			Labels:    labels,
			DataPoint: tstorage.DataPoint{Timestamp: baseTime.Add(time.Duration(i-warmup) * time.Millisecond).UnixNano(), Value: float64(i)},
		}
	}
	must(store.InsertRows(wbatch))

	if doSeq {
		const seqN = 2000
		const seqRuns = 3
		var durs []time.Duration
		seqLabels := []tstorage.Label{{Name: "dim", Value: "99"}}
		for r := 0; r < seqRuns; r++ {
			offset := r * seqN
			t0 := time.Now()
			for i := 0; i < seqN; i++ {
				must(store.InsertRows([]tstorage.Row{{
					Metric:    "m",
					Labels:    seqLabels,
					DataPoint: tstorage.DataPoint{Timestamp: baseTime.Add(time.Duration(-(seqRuns*seqN)+(offset+i)) * time.Millisecond).UnixNano(), Value: float64(i)},
				}}))
			}
			durs = append(durs, time.Since(t0))
		}
		record("tstorage", "sequential append", seqN, durs, "events/sec")
	}

	// batch append
	batch := make([]tstorage.Row, batchSize)
	var batchDurs []time.Duration
	for r := 0; r < runs; r++ {
		offset := r * n
		total := 0
		t0 := time.Now()
		for total < n {
			sz := batchSize
			if n-total < sz {
				sz = n - total
				batch = batch[:sz]
			}
			for i := 0; i < sz; i++ {
				batch[i] = tstorage.Row{
					Metric:    "m",
					Labels:    labels,
					DataPoint: tstorage.DataPoint{Timestamp: baseTime.Add(time.Duration(offset+total+i) * time.Millisecond).UnixNano(), Value: float64(total + i)},
				}
			}
			must(store.InsertRows(batch))
			total += sz
		}
		batchDurs = append(batchDurs, time.Since(t0))
	}
	record("tstorage", "batch append", n, batchDurs, "events/sec")

	// range scan
	fromNs := baseTime.UnixNano()
	toNs := baseTime.Add(time.Duration(runs*n+1) * time.Millisecond).UnixNano()
	var scanDurs []time.Duration
	var scanCount int
	for r := 0; r < runs; r++ {
		t0 := time.Now()
		pts, err := store.Select("m", labels, fromNs, toNs)
		if err != nil && err != tstorage.ErrNoDataPoints {
			must(err)
		}
		scanCount = len(pts)
		scanDurs = append(scanDurs, time.Since(t0))
	}
	record("tstorage", "range scan (agg path)", scanCount, scanDurs, "events/sec")

	// range sum (client-side)
	var sumDurs []time.Duration
	for r := 0; r < runs; r++ {
		t0 := time.Now()
		pts, err := store.Select("m", labels, fromNs, toNs)
		if err != nil && err != tstorage.ErrNoDataPoints {
			must(err)
		}
		var sum float64
		for _, p := range pts {
			sum += p.Value
		}
		_ = sum
		sumDurs = append(sumDurs, time.Since(t0))
	}
	record("tstorage", "range sum (1 field)", 1, sumDurs, "queries/sec")

	recordNA("tstorage", "range aggregate (7 fields)", "(1 float/event; no multi-field API)")
}

// ─── SQLite scenarios ─────────────────────────────────────────────────────────

const sqliteSchema = `
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
PRAGMA cache_size=-65536;
CREATE TABLE IF NOT EXISTS events (
    timeline INTEGER NOT NULL,
    dim0     INTEGER NOT NULL,
    ts       INTEGER NOT NULL,
    val0     REAL NOT NULL,
    val1     REAL, val2 REAL, val3 REAL,
    val4     REAL, val5 REAL, val6 REAL,
    PRIMARY KEY (timeline, dim0, ts)
) WITHOUT ROWID;
`

func benchSQLite(n, warmup, batchSize, runs int, doSeq bool) {
	fmt.Println("\n[sqlite / modernc v1.29.0, WAL mode, sync=NORMAL]")

	dir, err := os.MkdirTemp("", "tsbench-sqlite-*")
	must(err)
	defer func() { _ = os.RemoveAll(dir) }()

	db, err := sql.Open("sqlite", dir+"/ts.db")
	must(err)
	defer func() { _ = db.Close() }()
	_, err = db.Exec(sqliteSchema)
	must(err)

	// Warmup
	tx, err := db.Begin()
	must(err)
	ws, err := tx.Prepare("INSERT OR REPLACE INTO events(timeline,dim0,ts,val0) VALUES(?,?,?,?)")
	must(err)
	for i := 0; i < warmup; i++ {
		_, err = ws.Exec(1, 1, baseTime.Add(time.Duration(i-warmup)*time.Millisecond).UnixNano(), float64(i))
		must(err)
	}
	must(ws.Close())
	must(tx.Commit())

	if doSeq {
		const seqN = 2000
		const seqRuns = 3
		var durs []time.Duration
		ss, err := db.Prepare("INSERT OR REPLACE INTO events(timeline,dim0,ts,val0) VALUES(?,?,?,?)")
		must(err)
		for r := 0; r < seqRuns; r++ {
			offset := r * seqN
			t0 := time.Now()
			for i := 0; i < seqN; i++ {
				tx, err := db.Begin()
				must(err)
				_, err = tx.Stmt(ss).Exec(1, 99, baseTime.Add(time.Duration(-(seqRuns*seqN)+(offset+i))*time.Millisecond).UnixNano(), float64(i))
				must(err)
				must(tx.Commit())
			}
			durs = append(durs, time.Since(t0))
		}
		must(ss.Close())
		record("sqlite", "sequential append", seqN, durs, "events/sec")
	}

	// batch append
	var batchDurs []time.Duration
	for r := 0; r < runs; r++ {
		offset := r * n
		total := 0
		t0 := time.Now()
		for total < n {
			sz := batchSize
			if n-total < sz {
				sz = n - total
			}
			tx, err := db.Begin()
			must(err)
			s, err := tx.Prepare("INSERT OR REPLACE INTO events(timeline,dim0,ts,val0,val1,val2,val3,val4,val5,val6) VALUES(?,?,?,?,?,?,?,?,?,?)")
			must(err)
			for i := 0; i < sz; i++ {
				v := float64(total + i)
				_, err = s.Exec(1, 1, baseTime.Add(time.Duration(offset+total+i)*time.Millisecond).UnixNano(),
					v, v*2, v*3, v*0.1, v*0.5, v*1.1, v*0.9)
				must(err)
			}
			must(s.Close())
			must(tx.Commit())
			total += sz
		}
		batchDurs = append(batchDurs, time.Since(t0))
	}
	record("sqlite", "batch append", n, batchDurs, "events/sec")

	// range scan
	fromNs := baseTime.UnixNano()
	toNs := baseTime.Add(time.Duration(runs*n+1) * time.Millisecond).UnixNano()
	scanStmt, err := db.Prepare("SELECT ts,val0 FROM events WHERE timeline=1 AND dim0=1 AND ts>=? AND ts<=? ORDER BY ts")
	must(err)
	defer func() { _ = scanStmt.Close() }()
	var scanDurs []time.Duration
	var scanCount int
	for r := 0; r < runs; r++ {
		t0 := time.Now()
		rows, err := scanStmt.Query(fromNs, toNs)
		must(err)
		cnt := 0
		for rows.Next() {
			var ts int64
			var v float64
			must(rows.Scan(&ts, &v))
			cnt++
		}
		must(rows.Err())
		_ = rows.Close()
		scanCount = cnt
		scanDurs = append(scanDurs, time.Since(t0))
	}
	record("sqlite", "range scan (agg path)", scanCount, scanDurs, "events/sec")

	// range sum (server-side)
	sumStmt, err := db.Prepare("SELECT SUM(val0) FROM events WHERE timeline=1 AND dim0=1 AND ts>=? AND ts<=?")
	must(err)
	defer func() { _ = sumStmt.Close() }()
	var sumDurs []time.Duration
	for r := 0; r < runs; r++ {
		t0 := time.Now()
		var sv sql.NullFloat64
		must(sumStmt.QueryRow(fromNs, toNs).Scan(&sv))
		sumDurs = append(sumDurs, time.Since(t0))
	}
	record("sqlite", "range sum (1 field)", 1, sumDurs, "queries/sec")

	// multi-field aggregate
	agg7, err := db.Prepare(`SELECT COUNT(*),SUM(val0),MIN(val0),MAX(val0),AVG(val0),
		SUM(val1),MIN(val1),MAX(val1),SUM(val2),MIN(val2),MAX(val2)
		FROM events WHERE timeline=1 AND dim0=1 AND ts>=? AND ts<=?`)
	must(err)
	defer func() { _ = agg7.Close() }()
	var agg7Durs []time.Duration
	for r := 0; r < runs; r++ {
		t0 := time.Now()
		row := agg7.QueryRow(fromNs, toNs)
		var cnt int64
		var s0, mn0, mx0, av0, s1, mn1, mx1, s2, mn2, mx2 sql.NullFloat64
		must(row.Scan(&cnt, &s0, &mn0, &mx0, &av0, &s1, &mn1, &mx1, &s2, &mn2, &mx2))
		agg7Durs = append(agg7Durs, time.Since(t0))
		_ = cnt
	}
	record("sqlite", "range aggregate (7 fields)", 1, agg7Durs, "queries/sec")
}

// ─── VictoriaMetrics scenarios ────────────────────────────────────────────────

// vmImportPayload is the JSON structure for /api/v1/import.
type vmImportPayload struct {
	Metric     map[string]string `json:"metric"`
	Values     []float64         `json:"values"`
	Timestamps []int64           `json:"timestamps"`
}

// vmBase is set at the start of benchVM to time.Now(), ensuring all VM
// timestamps fall within its 1-month retention window.
var vmBase time.Time

func vmURL(port int, path string) string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
}

// vmWaitReady polls VM's /api/v1/status/tsdb until it responds, up to timeout.
func vmWaitReady(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(vmURL(port, "/api/v1/status/tsdb"))
		if err == nil {
			_ = resp.Body.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("victoria-metrics did not become ready within %v", timeout)
}

// vmWriteBatch posts a batch of events for a single series to VM's import endpoint.
// series is used as the value of the "s" label to distinguish write vs read series.
func vmWriteBatch(port int, seriesLabel string, base time.Time, startIdx, count int) error {
	// We send 7 separate payloads — one per field — to match xolu's 7-field model.
	// Fields are named val0..val6 via the metric name suffix.
	for field := 0; field < 7; field++ {
		payload := vmImportPayload{
			Metric:     map[string]string{"__name__": fmt.Sprintf("bench_val%d", field), "s": seriesLabel},
			Values:     make([]float64, count),
			Timestamps: make([]int64, count),
		}
		multipliers := []float64{1, 2, 3, 0.1, 0.5, 1.1, 0.9}
		for i := 0; i < count; i++ {
			v := float64(startIdx + i)
			payload.Values[i] = v * multipliers[field]
			payload.Timestamps[i] = base.Add(time.Duration(startIdx+i) * time.Millisecond).UnixMilli()
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		resp, err := http.Post(vmURL(port, "/api/v1/import"), "application/json", bytes.NewReader(body))
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != 204 {
			return fmt.Errorf("vm import: unexpected status %d", resp.StatusCode)
		}
	}
	return nil
}

// vmQuery runs a MetricsQL instant query and returns the wall time.
func vmQuery(port int, query string, atTime time.Time) (time.Duration, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("time", strconv.FormatInt(atTime.Unix(), 10))
	t0 := time.Now()
	resp, err := http.Get(vmURL(port, "/api/v1/query?"+q.Encode()))
	elapsed := time.Since(t0)
	if err != nil {
		return 0, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return elapsed, nil
}

func benchVM(vmBin string, port, n, warmup, batchSize, runs int, doSeq bool) {
	fmt.Printf("\n[victoria / VictoriaMetrics v1.102.0, HTTP API, port %d]\n", port)

	vmBase = time.Now().UTC().Truncate(time.Millisecond)
	vmDir, err := os.MkdirTemp("", "tsbench-vm-*")
	must(err)
	defer func() { _ = os.RemoveAll(vmDir) }()

	// Start VM subprocess
	cmd := exec.Command(vmBin,
		"-storageDataPath="+vmDir+"/data",
		fmt.Sprintf("-httpListenAddr=127.0.0.1:%d", port),
		"-retentionPeriod=1",
		"-loggerLevel=ERROR",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	must(cmd.Start())
	defer func() {
		_ = cmd.Process.Signal(os.Interrupt)
		_ = cmd.Wait()
	}()

	if err := vmWaitReady(port, 10*time.Second); err != nil {
		fmt.Printf("  victoria  SKIP: %v\n", err)
		return
	}

	// Warmup (writes only, not timed)
	if err := vmWriteBatch(port, "warmup", vmBase, -warmup, warmup); err != nil {
		fmt.Printf("  victoria  warmup error: %v\n", err)
		return
	}

	// ── optional sequential single-event append ──────────────────────────
	if doSeq {
		// VM sequential write = one HTTP request per event per field = 7 HTTP round-trips per event.
		// Very slow, but that's the honest cost. Use tiny n.
		const seqN = 200
		const seqRuns = 3
		seqBase := vmBase.Add(-time.Duration(seqRuns*seqN+warmup+1) * time.Millisecond)
		var durs []time.Duration
		for r := 0; r < seqRuns; r++ {
			offset := r * seqN
			t0 := time.Now()
			for i := 0; i < seqN; i++ {
				for field := 0; field < 7; field++ {
					ts := seqBase.Add(time.Duration(offset+i) * time.Millisecond).UnixMilli()
					v := float64(offset + i)
					multipliers := []float64{1, 2, 3, 0.1, 0.5, 1.1, 0.9}
					payload := vmImportPayload{
						Metric:     map[string]string{"__name__": fmt.Sprintf("seq_val%d", field), "s": "seq"},
						Values:     []float64{v * multipliers[field]},
						Timestamps: []int64{ts},
					}
					body, err := json.Marshal(payload)
					must(err)
					resp, err := http.Post(vmURL(port, "/api/v1/import"), "application/json", bytes.NewReader(body))
					must(err)
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}
			durs = append(durs, time.Since(t0))
		}
		// Report per event, not per field-write
		record("victoria", "sequential append", seqN, durs, "events/sec")
	}

	// ── batch append ──────────────────────────────────────────────────────
	var batchDurs []time.Duration
	for r := 0; r < runs; r++ {
		offset := r * n
		total := 0
		t0 := time.Now()
		for total < n {
			sz := batchSize
			if n-total < sz {
				sz = n - total
			}
			if err := vmWriteBatch(port, "bench", vmBase, offset+total, sz); err != nil {
				must(err)
			}
			total += sz
		}
		batchDurs = append(batchDurs, time.Since(t0))
	}
	record("victoria", "batch append", n, batchDurs, "events/sec")

	// Give VM a moment to flush writes to storage before querying
	time.Sleep(500 * time.Millisecond)

	// ── range scan via sum_over_time (single field, val0) ─────────────────
	// VM has no "scan N events and count them" endpoint; sum_over_time over
	// the full written range is the closest equivalent query under load.
	// We report queries/sec because we can't easily recover event count.
	writeEnd := vmBase.Add(time.Duration(runs*n) * time.Millisecond)
	rangeStr := fmt.Sprintf("%ds", int(writeEnd.Sub(vmBase).Seconds())+2)
	sumQuery := fmt.Sprintf("sum_over_time(bench_val0{s=\"bench\"}[%s])", rangeStr)

	var scanDurs []time.Duration
	for r := 0; r < runs; r++ {
		d, err := vmQuery(port, sumQuery, writeEnd.Add(time.Second))
		must(err)
		scanDurs = append(scanDurs, d)
	}
	record("victoria", "range scan (agg path)", 1, scanDurs, "queries/sec")

	// ── range sum (same query — this IS the native aggregation) ──────────
	var sumDurs []time.Duration
	for r := 0; r < runs; r++ {
		d, err := vmQuery(port, sumQuery, writeEnd.Add(time.Second))
		must(err)
		sumDurs = append(sumDurs, d)
	}
	record("victoria", "range sum (1 field)", 1, sumDurs, "queries/sec")

	// ── 7-field aggregate: 7 separate sum_over_time queries ───────────────
	// VM has no single-pass multi-field accumulator. We issue 7 queries and
	// sum the wall times — this is the real cost of the operation in VM.
	var agg7Total time.Duration
	var agg7Totals []time.Duration
	for r := 0; r < runs; r++ {
		agg7Total = 0
		for field := 0; field < 7; field++ {
			q := fmt.Sprintf("sum_over_time(bench_val%d{s=\"bench\"}[%s])", field, rangeStr)
			d, err := vmQuery(port, q, writeEnd.Add(time.Second))
			must(err)
			agg7Total += d
		}
		agg7Totals = append(agg7Totals, agg7Total)
	}
	record("victoria", "range aggregate (7 fields)", 1, agg7Totals, "queries/sec")
}

// ─── summary table ────────────────────────────────────────────────────────────

func printSummary(backends []string) {
	fmt.Println("\n" + ruler(100))
	fmt.Println("SUMMARY  (median; bar = fraction of best in scenario; N/A = not supported)")
	fmt.Println(ruler(100))

	scenarios := []string{
		"sequential append",
		"batch append",
		"range scan (agg path)",
		"range sum (1 field)",
		"range aggregate (7 fields)",
	}

	idx := map[string]map[string]*result{}
	for i := range results {
		r := &results[i]
		if idx[r.scenario] == nil {
			idx[r.scenario] = map[string]*result{}
		}
		idx[r.scenario][r.backend] = r
	}

	for _, sc := range scenarios {
		byBackend := idx[sc]
		maxRate := 0.0
		for _, b := range backends {
			r, ok := byBackend[b]
			if !ok {
				continue
			}
			rate := float64(r.n) / r.median.Seconds()
			if rate > maxRate {
				maxRate = rate
			}
		}

		fmt.Printf("\n  %s\n", sc)
		for _, b := range backends {
			r, ok := byBackend[b]
			if !ok {
				fmt.Printf("    %-16s  N/A\n", b)
				continue
			}
			rate := float64(r.n) / r.median.Seconds()
			bar := ""
			if maxRate > 0 {
				w := int(math.Round(rate / maxRate * 30))
				if w < 1 {
					w = 1
				}
				bar = "[" + repeat("█", w) + repeat("░", 30-w) + "]"
			}
			fmt.Printf("    %-16s  %10.0f %-12s  %s  %v\n",
				b, rate, r.unit, bar, r.median.Round(time.Millisecond))
		}
	}
	fmt.Println("\n" + ruler(100))
}

// ─── utilities ────────────────────────────────────────────────────────────────

func ruler(n int) string { return strings.Repeat("─", n) }
func repeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(s)
	}
	return b.String()
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// ─── main ─────────────────────────────────────────────────────────────────────

func main() {
	flag.Parse()

	n := *flagN
	warmup := *flagWarmup
	batch := *flagBatch
	runs := *flagRuns
	doSeq := *flagSeq
	port := *flagVMPort

	// Resolve victoria-metrics binary
	vmBin := *flagVM
	if vmBin == "" {
		gopath := os.Getenv("GOPATH")
		if gopath == "" {
			gopath = os.Getenv("HOME") + "/go"
		}
		vmBin = gopath + "/bin/victoria-metrics"
	}
	_, vmErr := os.Stat(vmBin)
	hasVM := vmErr == nil

	fmt.Printf("tsbench  n=%d  warmup=%d  batch=%d  runs=%d  seq=%v\n",
		n, warmup, batch, runs, doSeq)
	fmt.Printf("Go 1.26.4 · %s\n", time.Now().UTC().Format("2006-01-02 15:04:05 UTC"))
	if hasVM {
		fmt.Printf("VictoriaMetrics binary: %s\n", vmBin)
	} else {
		fmt.Printf("VictoriaMetrics binary not found at %s — victoria skipped\n", vmBin)
	}
	fmt.Println(ruler(100))

	// Three xolu configs
	benchXolu("xolu-default", timeseries.PebbleConfig{
		MemtableSize:          64 << 20,
		BlockSize:             32 << 10,
		Compression:           "zstd",
		L0CompactionThreshold: 4,
	}, n, warmup, batch, runs, doSeq)

	benchXolu("xolu-tuned", timeseries.PebbleConfig{
		MemtableSize:          256 << 20,
		BlockSize:             32 << 10,
		Compression:           "zstd",
		L0CompactionThreshold: 8,
	}, n, warmup, batch, runs, doSeq)

	benchXolu("xolu-highbuf", timeseries.PebbleConfig{
		MemtableSize:          256 << 20,
		BlockSize:             64 << 10,
		Compression:           "snappy",
		L0CompactionThreshold: 2,
	}, n, warmup, batch, runs, doSeq)

	benchTstorage(n, warmup, batch, runs, doSeq)
	benchSQLite(n, warmup, batch, runs, doSeq)

	backends := []string{"xolu-default", "xolu-tuned", "xolu-highbuf", "tstorage", "sqlite"}

	if hasVM {
		benchVM(vmBin, port, n, warmup, batch, runs, doSeq)
		backends = append(backends, "victoria")
	}

	printSummary(backends)
}
