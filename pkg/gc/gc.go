// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package gc provides a generic background sweep worker used by all GC
// subsystems in xolu: blob storage, timeseries retention, FSM machine
// collection (v2), and event delivery log cleanup (v2).
//
// The package implements the ticker-and-stop-channel lifecycle pattern
// once so that each sweeper only needs to implement the Sweeper interface.
// Workers are registered at server startup and exposed via the admin API
// at POST /api/v1/admin/gc/{name}/run and GET /api/v1/admin/gc.
//
// A panic in any registered Sweeper's own Sweep method is recovered by
// Worker.RunOnce, logged at Error level with a stack trace, and turned
// into a normal error -- one GC subsystem's bug degrades to a logged
// failure for that subsystem's own sweeps, never a crash of the whole
// server. See RunOnce's own doc comment for why this exists.
package gc

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Report summarises the result of a single sweep cycle.
// Zero values are valid; sweepers that do not track a field leave it at 0.
type Report struct {
	Examined    int           // items inspected this cycle
	Collected   int           // items collected (deleted or hard-purged)
	Quarantined int           // items moved to a staging area (two-phase sweepers)
	Errors      int           // non-fatal errors encountered
	Duration    time.Duration // wall time for the sweep
}

// Sweeper is implemented by each GC subsystem.
// Sweep must be safe to call concurrently from RunOnce and the ticker goroutine.
type Sweeper interface {
	Sweep(ctx context.Context) (Report, error)
}

// Worker runs a Sweeper on a ticker interval. It is safe for concurrent use.
type Worker struct {
	name     string
	sweeper  Sweeper
	interval time.Duration
	logger   zerolog.Logger

	mu         sync.Mutex // protects started, stopped, lastReport, lastAt
	started    bool
	stopped    bool
	stop       chan struct{}
	done       chan struct{}
	lastReport Report
	lastAt     time.Time
}

// NewWorker creates a Worker. Start must be called separately.
func NewWorker(name string, s Sweeper, interval time.Duration, logger zerolog.Logger) *Worker {
	return &Worker{
		name:     name,
		sweeper:  s,
		interval: interval,
		logger:   logger,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start launches the background sweep goroutine. Calling Start more than
// once on the same Worker panics.
//
// The panic was always this method's documented contract, but the guard
// itself was never implemented: a second Start silently launched a second
// run() goroutine, and the eventual symptom was a "close of closed
// channel" panic in run()'s own deferred close(w.done) -- in a background
// goroutine, crashing the whole process, far from the call that caused
// it (demonstrated live by TestWorker_StartTwicePanics's own pre-fix
// run, which took down the test binary mid-suite attributed to a
// different test's name). Same lifecycle-unsafety family as pkg/server's
// own pre-T-140 Start/Shutdown race.
func (w *Worker) Start() {
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		panic("gc: Worker.Start called twice on worker " + w.name)
	}
	w.started = true
	w.mu.Unlock()
	go w.run()
}

// Stop signals the worker to stop and blocks until the background
// goroutine has exited. Idempotent. Calling Stop on a worker that was
// never started returns immediately -- there is nothing to wait for.
//
// The previous behaviour, documented and implemented, was to block
// forever on <-w.done in that case: the same sharp edge as pkg/server's
// own pre-T-140 Shutdown-before-Start, kept alive here purely by its
// documentation. Not a live bug on any known call path (every worker
// the server registers is Started at registration, checked directly),
// but a footgun with no upside. A Start after such a Stop launches a
// goroutine that observes the already-closed stop channel and exits at
// once -- harmless by construction.
func (w *Worker) Stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	started := w.started
	w.mu.Unlock()
	close(w.stop)
	if started {
		<-w.done
	}
}

// RunOnce executes a single sweep synchronously. Used by the admin endpoint
// and by tests. Safe to call concurrently with the background goroutine.
//
// Recovers a panic from the underlying Sweeper's own Sweep method,
// logs it at Error level with a full stack trace, and returns it as a
// normal error instead of letting it propagate -- added 2026-08-04
// (T-156: a real type-assertion panic in pkg/timeseries's
// RetentionWorker, registered as exactly this kind of Sweeper, crashed
// CI outright). A server should never go down because one GC
// subsystem has a bug; the worker survives a panicking sweep and keeps
// ticking on schedule, the same way it already survives a sweep that
// returns a normal error.
func (w *Worker) RunOnce(ctx context.Context) (r Report, err error) {
	t := time.Now()
	defer func() {
		if p := recover(); p != nil {
			stack := debug.Stack()
			r = Report{Duration: time.Since(t)}
			err = fmt.Errorf("gc: sweeper %q panicked: %v", w.name, p)
			w.mu.Lock()
			w.lastReport = r
			w.lastAt = t
			w.mu.Unlock()
			w.logger.Error().
				Str("worker", w.name).
				Interface("panic", p).
				Str("stack", string(stack)).
				Dur("duration", r.Duration).
				Msg("GC sweep panicked")
		}
	}()

	r, err = w.sweeper.Sweep(ctx)
	r.Duration = time.Since(t)
	w.mu.Lock()
	w.lastReport = r
	w.lastAt = t
	w.mu.Unlock()
	if err != nil {
		w.logger.Error().Err(err).Str("worker", w.name).Msg("GC sweep error")
	} else {
		w.logReport(r)
	}
	return r, err
}

// Name returns the worker's registered name.
func (w *Worker) Name() string { return w.name }

// LastReport returns the report from the most recent completed sweep and
// the time it started. Returns a zero Report and zero time if no sweep
// has completed yet.
func (w *Worker) LastReport() (Report, time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastReport, w.lastAt
}

func (w *Worker) run() {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), w.interval/2)
			_, _ = w.RunOnce(ctx)
			cancel()
		}
	}
}

func (w *Worker) logReport(r Report) {
	w.logger.Info().
		Str("worker", w.name).
		Int("examined", r.Examined).
		Int("collected", r.Collected).
		Int("quarantined", r.Quarantined).
		Int("errors", r.Errors).
		Dur("duration", r.Duration).
		Msg("GC sweep complete")
}
