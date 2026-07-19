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
	s := &countSweeper{report: gc.Report{Examined: 10, Collected: 3}}
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
	if r.Duration == 0 {
		t.Error("Duration should be non-zero")
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
	s := &countSweeper{err: context.DeadlineExceeded}
	w := gc.NewWorker("test", s, time.Hour, newLogger())
	r, err := w.RunOnce(context.Background())
	if err == nil {
		t.Error("expected error from RunOnce when sweeper returns error")
	}
	if r.Duration == 0 {
		t.Error("Duration should be set even on sweep error")
	}
}

func TestReport_ZeroValues(t *testing.T) {
	var r gc.Report
	if r.Examined != 0 || r.Collected != 0 || r.Quarantined != 0 || r.Errors != 0 {
		t.Errorf("zero Report should have all-zero fields, got %+v", r)
	}
}
