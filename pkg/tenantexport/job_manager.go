// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package tenantexport

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/ha1tch/xolu/pkg/tenant"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// JobStatus is an export job's lifecycle state.
type JobStatus string

const (
	JobRunning  JobStatus = "running"
	JobComplete JobStatus = "complete"
	JobFailed   JobStatus = "failed"
)

// Job is one tenant's export job, tracked by ticket ID.
type Job struct {
	Ticket     string
	TenantID   tenant.TenantID
	Status     JobStatus
	BlobKey    string // set once Status == JobComplete
	Error      string // set once Status == JobFailed
	CreatedAt  time.Time
	FinishedAt time.Time
}

// JobManager tracks export jobs in memory and bounds how many run
// concurrently. Not persisted across a process restart -- a job in
// flight when the server restarts is simply lost, matching this
// package's own scope (data collection and job tracking, not a
// durable work queue); a caller needing that guarantee is a different,
// larger piece of work than what was asked for here.
type JobManager struct {
	mu   sync.Mutex
	jobs map[string]*Job

	// runningByTenant enforces the one-job-per-tenant throttle:
	// tenantID -> ticket of the currently running job, absent when
	// none is running.
	runningByTenant map[tenant.TenantID]string

	// sem bounds total concurrent export work server-wide -- the "low
	// priority" requirement: export work never competes unbounded
	// against normal request traffic, regardless of how many tenants
	// try to export at once. A buffered channel used as a semaphore,
	// not a worker pool with pre-spawned goroutines -- Submit's own
	// goroutine blocks acquiring a slot before doing real work, so an
	// over-limit request queues rather than being rejected outright
	// (unlike the per-tenant throttle, which does reject outright --
	// two different failure modes for two different reasons: a
	// second job for the SAME tenant is almost always a mistake or a
	// retry-happy caller; a queued job from a DIFFERENT tenant during
	// a burst is normal and should wait its turn, not fail).
	sem chan struct{}
}

// NewJobManager creates a job tracker allowing at most maxConcurrent
// export jobs to actually run (query/iterate/package) at once,
// server-wide, regardless of how many tenants have called Submit.
func NewJobManager(maxConcurrent int) *JobManager {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &JobManager{
		jobs:            make(map[string]*Job),
		runningByTenant: make(map[tenant.TenantID]string),
		sem:             make(chan struct{}, maxConcurrent),
	}
}

// ErrTenantExportInFlight is returned by Submit when the given tenant
// already has a running export job -- the per-tenant throttle. The
// caller should return this to the client as a 429 with the existing
// ticket, not silently start a redundant second export of the same
// tenant's data.
type ErrTenantExportInFlight struct {
	ExistingTicket string
}

func (e *ErrTenantExportInFlight) Error() string {
	return fmt.Sprintf("xolu: an export is already running for this tenant (ticket %s)", e.ExistingTicket)
}

// Submit starts a new export job for tenantID, running work in a
// background goroutine bounded by the manager's own concurrency
// semaphore. Returns the new job's ticket ID immediately -- work has
// not necessarily started yet if the semaphore is currently full;
// Status() distinguishes "queued behind the concurrency limit" from
// "actually running" only implicitly (both report JobRunning -- this
// package does not currently expose queued-vs-executing as a separate
// state, since a caller polling Status has no actionable difference
// between the two).
//
// Returns *ErrTenantExportInFlight, not a ticket, if tenantID already
// has a running job -- the per-tenant throttle. work is called with no
// arguments; a caller closes over whatever primaryDB/basePath/blobStore
// it needs (this keeps JobManager itself free of any dependency on
// ExportTenant's own signature, so it can track any long-running,
// eventually-blob-producing job, not just this specific export shape).
func (m *JobManager) Submit(tenantID tenant.TenantID, work func() (*PackageResult, error)) (string, error) {
	m.mu.Lock()
	if existing, running := m.runningByTenant[tenantID]; running {
		m.mu.Unlock()
		return "", &ErrTenantExportInFlight{ExistingTicket: existing}
	}
	ticket, err := newTicket()
	if err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("tenantexport: generate ticket: %w", err)
	}
	job := &Job{Ticket: ticket, TenantID: tenantID, Status: JobRunning, CreatedAt: ot.Now().Time()}
	m.jobs[ticket] = job
	m.runningByTenant[tenantID] = ticket
	m.mu.Unlock()

	go func() {
		m.sem <- struct{}{}
		defer func() { <-m.sem }()

		result, err := work()

		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.runningByTenant, tenantID)
		job.FinishedAt = ot.Now().Time()
		if err != nil {
			job.Status = JobFailed
			job.Error = err.Error()
			return
		}
		job.Status = JobComplete
		job.BlobKey = result.Key
	}()

	return ticket, nil
}

// Status returns the job for ticket, or nil, false if no such ticket
// is known (never existed, or -- not currently implemented -- was
// pruned; this package does not yet age out old completed jobs from
// memory).
func (m *JobManager) Status(ticket string) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[ticket]
	if !ok {
		return nil, false
	}
	// Return a copy -- the caller must not be able to mutate the
	// manager's own tracked state through the returned pointer.
	cp := *job
	return &cp, true
}

func newTicket() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "exp_" + hex.EncodeToString(b), nil
}
