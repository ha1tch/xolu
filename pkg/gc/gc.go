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
package gc

import (
	"context"
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

	mu         sync.Mutex // protects stopped
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
func (w *Worker) Start() {
	go w.run()
}

// Stop signals the worker to stop and blocks until it has exited.
// Calling Stop on a worker that has not been started blocks indefinitely.
func (w *Worker) Stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	w.mu.Unlock()
	close(w.stop)
	<-w.done
}

// RunOnce executes a single sweep synchronously. Used by the admin endpoint
// and by tests. Safe to call concurrently with the background goroutine.
func (w *Worker) RunOnce(ctx context.Context) (Report, error) {
	t := time.Now()
	r, err := w.sweeper.Sweep(ctx)
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
