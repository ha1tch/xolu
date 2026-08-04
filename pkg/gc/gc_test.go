// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package gc_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/gc"
	"github.com/rs/zerolog"
)

// countSweeper counts how many times Sweep is called.
type countSweeper struct {
	count  atomic.Int64
	report gc.Report
	err    error
	delay  time.Duration
}

func (c *countSweeper) Sweep(_ context.Context) (gc.Report, error) {
	c.count.Add(1)
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	return c.report, c.err
}

func newLogger() zerolog.Logger {
	return zerolog.Nop()
}

func TestWorker_RunOnce(t *testing.T) {
	// delay for the same reason as TestWorker_SweepErrorLogged's own
	// comment (the M1 clock-granularity flake): a bare Duration != 0
	// assertion on a zero-work sweep can genuinely fail on a 24 MHz
	// monotonic clock. This test carried the identical fragile
	// assertion and simply hadn't fired yet.
	const delay = time.Millisecond
	s := &countSweeper{report: gc.Report{Examined: 10, Collected: 3}, delay: delay}
	w := gc.NewWorker("test", s, time.Hour, newLogger())

	r, err := w.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if r.Examined != 10 {
		t.Errorf("Examined: want 10, got %d", r.Examined)
	}
	if r.Collected != 3 {
		t.Errorf("Collected: want 3, got %d", r.Collected)
	}
	if r.Duration < delay {
		t.Errorf("Duration: want >= %v, got %v", delay, r.Duration)
	}
	if s.count.Load() != 1 {
		t.Errorf("Sweep call count: want 1, got %d", s.count.Load())
	}
}

func TestWorker_LastReport(t *testing.T) {
	s := &countSweeper{report: gc.Report{Collected: 5}}
	w := gc.NewWorker("test", s, time.Hour, newLogger())

	r0, at0 := w.LastReport()
	if at0 != (time.Time{}) {
		t.Errorf("LastReport before sweep: want zero time, got %v", at0)
	}
	if r0.Collected != 0 {
		t.Errorf("LastReport before sweep: want zero report, got %+v", r0)
	}

	before := time.Now()
	w.RunOnce(context.Background())
	r1, at1 := w.LastReport()
	if r1.Collected != 5 {
		t.Errorf("LastReport after sweep: Collected want 5, got %d", r1.Collected)
	}
	if at1.Before(before) {
		t.Errorf("LastReport time: want >= %v, got %v", before, at1)
	}
}

func TestWorker_StartStop(t *testing.T) {
	s := &countSweeper{report: gc.Report{}}
	// Short interval so the ticker fires at least once.
	w := gc.NewWorker("test", s, 20*time.Millisecond, newLogger())
	w.Start()

	// Wait for at least one sweep.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if s.count.Load() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if s.count.Load() < 1 {
		t.Error("expected at least one sweep after Start(), got 0")
	}

	w.Stop()
	countAfterStop := s.count.Load()
	time.Sleep(30 * time.Millisecond)
	if s.count.Load() != countAfterStop {
		t.Errorf("sweeper called after Stop(): count went from %d to %d",
			countAfterStop, s.count.Load())
	}
}

func TestWorker_StopIdempotent(t *testing.T) {
	s := &countSweeper{}
	w := gc.NewWorker("test", s, time.Hour, newLogger())
	w.Start()
	w.Stop()
	// Second Stop must not block or panic.
	done := make(chan struct{})
	go func() {
		w.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("second Stop() blocked")
	}
}

func TestWorker_Name(t *testing.T) {
	w := gc.NewWorker("blob-gc", &countSweeper{}, time.Hour, newLogger())
	if w.Name() != "blob-gc" {
		t.Errorf("Name(): want 'blob-gc', got %q", w.Name())
	}
}

func TestWorker_RunOnceDuration(t *testing.T) {
	delay := 20 * time.Millisecond
	s := &countSweeper{delay: delay}
	w := gc.NewWorker("test", s, time.Hour, newLogger())
	r, _ := w.RunOnce(context.Background())
	if r.Duration < delay {
		t.Errorf("Duration: want >= %v, got %v", delay, r.Duration)
	}
}

func TestWorker_SweepErrorLogged(t *testing.T) {
	// The sweeper carries a real delay so the assertion below can
	// demand Duration >= delay instead of merely != 0. The original
	// != 0 form was clock-granularity-fragile, and failed for real,
	// intermittently, on an M1 (`make test`, 2026-08-04): Apple
	// Silicon's monotonic clock ticks at 24 MHz -- one tick = 41.67ns
	// -- and a warm, zero-work Sweep (interface dispatch plus one
	// atomic add) genuinely starts and finishes inside a single tick,
	// making time.Since read exactly 0. Measured directly on the Linux
	// sandbox: 97.3% of these windows are under 42ns (min 29ns); that
	// platform's finer clock never reads 0, which is why the failure
	// never reproduced there across hundreds of runs. Cold first-call
	// overhead usually pushes the M1's one measurement over a tick --
	// hence intermittent, not constant. The delay makes the assertion
	// hold by construction on any platform clock, and still proves what
	// this test is actually for: RunOnce assigns Duration on the error
	// path too.
	const delay = time.Millisecond
	s := &countSweeper{err: context.DeadlineExceeded, delay: delay}
	w := gc.NewWorker("test", s, time.Hour, newLogger())
	r, err := w.RunOnce(context.Background())
	if err == nil {
		t.Error("expected error from RunOnce when sweeper returns error")
	}
	if r.Duration < delay {
		t.Errorf("Duration should be measured even on sweep error: want >= %v, got %v", delay, r.Duration)
	}
}

func TestReport_ZeroValues(t *testing.T) {
	var r gc.Report
	if r.Examined != 0 || r.Collected != 0 || r.Quarantined != 0 || r.Errors != 0 {
		t.Errorf("zero Report should have all-zero fields, got %+v", r)
	}
}

// ─── Lifecycle guards (same unsafety family as pkg/server's T-140) ──────────

func TestWorker_StartTwicePanics(t *testing.T) {
	w := gc.NewWorker("test", &countSweeper{}, time.Hour, newLogger())
	w.Start()
	defer w.Stop()
	defer func() {
		if recover() == nil {
			t.Error("second Start must panic, per its own documented contract; without the guard it silently launches a second run() goroutine whose eventual symptom is a close-of-closed-channel panic far from the cause")
		}
	}()
	w.Start()
}

func TestWorker_StopBeforeStartReturnsImmediately(t *testing.T) {
	w := gc.NewWorker("test", &countSweeper{}, time.Hour, newLogger())
	returned := make(chan struct{})
	go func() {
		w.Stop()
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop on a never-started worker blocked forever instead of returning -- the old documented-footgun behaviour, same shape as pre-T-140 Shutdown-before-Start")
	}
}
