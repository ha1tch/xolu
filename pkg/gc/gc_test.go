// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package gc_test

import (
	"bytes"
	"context"
	"strings"
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

// panicSweeper always panics from Sweep, simulating a genuine bug in a
// third-party Sweeper implementation -- the exact shape of T-156's own
// real bug (a bad type assertion inside pkg/timeseries's
// RetentionWorker.Sweep).
type panicSweeper struct {
	value any // the value passed to panic(); a string, an error, anything
}

func (p *panicSweeper) Sweep(_ context.Context) (gc.Report, error) {
	panic(p.value)
}

// newCapturingLogger returns a logger writing to buf, so a test can
// inspect the actual log content -- newLogger's own zerolog.Nop()
// discards everything, which is right for tests that only care whether
// something panics, wrong for a test verifying what got logged.
func newCapturingLogger(buf *bytes.Buffer) zerolog.Logger {
	return zerolog.New(buf)
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

// ─── Panic recovery (T-157: "a server should never panic") ────────────────

// TestWorker_RunOnce_SweeperPanic_DoesNotPanic is the direct proof: a
// Sweeper whose Sweep panics must not take down the caller. Runs
// RunOnce with no recover of its own around the call -- if RunOnce's
// own internal recovery didn't work, this test process itself would
// crash, not just fail.
func TestWorker_RunOnce_SweeperPanic_DoesNotPanic(t *testing.T) {
	s := &panicSweeper{value: "simulated bug: index out of range"}
	w := gc.NewWorker("test", s, time.Hour, newLogger())

	r, err := w.RunOnce(context.Background())
	if err == nil {
		t.Fatal("expected a non-nil error when the sweeper panics")
	}
	if !strings.Contains(err.Error(), "simulated bug: index out of range") {
		t.Errorf("error should carry the panic value, got: %v", err)
	}
	if !strings.Contains(err.Error(), "test") {
		t.Errorf("error should name the worker, got: %v", err)
	}
	if r.Duration == 0 {
		t.Error("Duration should still be measured even when the sweep panicked")
	}
}

// TestWorker_RunOnce_SweeperPanic_LogsErrorWithStack verifies the log
// entry itself: Error level, the panic value present, and a real stack
// trace -- not just "didn't crash" but "gave an operator enough to
// actually diagnose it," matching what CI's own crash trace (T-156)
// gave for free before this recovery existed.
func TestWorker_RunOnce_SweeperPanic_LogsErrorWithStack(t *testing.T) {
	var buf bytes.Buffer
	s := &panicSweeper{value: "boom"}
	w := gc.NewWorker("ts-retention", s, time.Hour, newCapturingLogger(&buf))

	_, _ = w.RunOnce(context.Background())

	logLine := buf.String()
	if !strings.Contains(logLine, `"level":"error"`) {
		t.Errorf("expected Error level, got: %s", logLine)
	}
	if !strings.Contains(logLine, "GC sweep panicked") {
		t.Errorf("expected the panic-specific message, got: %s", logLine)
	}
	if !strings.Contains(logLine, "ts-retention") {
		t.Errorf("expected the worker name, got: %s", logLine)
	}
	if !strings.Contains(logLine, "boom") {
		t.Errorf("expected the panic value, got: %s", logLine)
	}
	if !strings.Contains(logLine, "goroutine") {
		t.Errorf("expected a real stack trace (should contain 'goroutine'), got: %s", logLine)
	}
}

// TestWorker_RunOnce_ErrorVsPanic_LogsDistinctMessages confirms a
// normal Sweep error and a Sweep panic produce distinguishable log
// messages -- an operator scanning logs needs to tell "a subsystem
// reported a routine failure" apart from "a subsystem crashed and had
// to be caught."
func TestWorker_RunOnce_ErrorVsPanic_LogsDistinctMessages(t *testing.T) {
	var errBuf, panicBuf bytes.Buffer

	errSweeper := &countSweeper{err: context.DeadlineExceeded, delay: time.Millisecond}
	errWorker := gc.NewWorker("err-worker", errSweeper, time.Hour, newCapturingLogger(&errBuf))
	_, _ = errWorker.RunOnce(context.Background())

	panicSweeper := &panicSweeper{value: "boom"}
	panicWorker := gc.NewWorker("panic-worker", panicSweeper, time.Hour, newCapturingLogger(&panicBuf))
	_, _ = panicWorker.RunOnce(context.Background())

	if strings.Contains(errBuf.String(), "panicked") {
		t.Errorf("a normal sweep error must not be logged as a panic: %s", errBuf.String())
	}
	if !strings.Contains(panicBuf.String(), "panicked") {
		t.Errorf("a sweep panic must be logged distinctly from a normal error: %s", panicBuf.String())
	}
}

// TestWorker_PeriodicLoop_SurvivesSweeperPanic is the end-to-end proof:
// the background ticker goroutine (the actual production path, via
// Start/run, not a direct RunOnce call) survives a panicking sweep and
// keeps ticking afterward -- the exact scenario a registered gc.Worker
// hits in the real server.
func TestWorker_PeriodicLoop_SurvivesSweeperPanic(t *testing.T) {
	s := &countSweeper{err: nil}
	// Wrap countSweeper to panic on the first call only, succeed after --
	// proving the worker recovers AND continues ticking normally.
	first := true
	sweep := panicOnceThenSucceed{first: &first, ok: s}
	w := gc.NewWorker("test", sweep, 20*time.Millisecond, newLogger())
	w.Start()
	defer w.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.count.Load() >= 2 {
			return // panicked once, then ticked again and succeeded -- worker survived
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker did not continue ticking after its sweeper panicked once")
}

type panicOnceThenSucceed struct {
	first *bool
	ok    *countSweeper
}

func (p panicOnceThenSucceed) Sweep(ctx context.Context) (gc.Report, error) {
	if *p.first {
		*p.first = false
		panic("first-call simulated panic")
	}
	return p.ok.Sweep(ctx)
}
