// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package tenantexport

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/tenant"
)

func waitForStatus(t *testing.T, m *JobManager, ticket string, want JobStatus, timeout time.Duration) *Job {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		job, ok := m.Status(ticket)
		if !ok {
			t.Fatalf("ticket %s not found", ticket)
		}
		if job.Status == want {
			return job
		}
		if job.Status != JobRunning {
			t.Fatalf("ticket %s: status %s, want %s", ticket, job.Status, want)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("ticket %s did not reach status %s within %v", ticket, want, timeout)
	return nil
}

func TestJobManager_SuccessfulJob(t *testing.T) {
	m := NewJobManager(4)
	ticket, err := m.Submit(tenant.TenantID(1), func() (*PackageResult, error) {
		return &PackageResult{Key: "export-1.zip", SHA256: "abc", Bytes: 100}, nil
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	job := waitForStatus(t, m, ticket, JobComplete, time.Second)
	if job.BlobKey != "export-1.zip" {
		t.Errorf("BlobKey: got %q", job.BlobKey)
	}
	if job.TenantID != tenant.TenantID(1) {
		t.Errorf("TenantID: got %v", job.TenantID)
	}
	if job.FinishedAt.IsZero() {
		t.Error("FinishedAt not set")
	}
}

func TestJobManager_FailedJob(t *testing.T) {
	m := NewJobManager(4)
	ticket, err := m.Submit(tenant.TenantID(1), func() (*PackageResult, error) {
		return nil, fmt.Errorf("simulated export failure")
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}

	job := waitForStatus(t, m, ticket, JobFailed, time.Second)
	if job.Error != "simulated export failure" {
		t.Errorf("Error: got %q", job.Error)
	}
	if job.BlobKey != "" {
		t.Errorf("BlobKey should be empty on failure, got %q", job.BlobKey)
	}
}

func TestJobManager_UnknownTicket(t *testing.T) {
	m := NewJobManager(4)
	_, ok := m.Status("nonexistent")
	if ok {
		t.Error("expected ok=false for an unknown ticket")
	}
}

// TestJobManager_PerTenantThrottle proves a second Submit for the SAME
// tenant, while the first is still running, is rejected -- not queued,
// not silently allowed to run a redundant second export.
func TestJobManager_PerTenantThrottle(t *testing.T) {
	m := NewJobManager(4)
	release := make(chan struct{})
	firstTicket, err := m.Submit(tenant.TenantID(9), func() (*PackageResult, error) {
		<-release // block until the test releases it
		return &PackageResult{Key: "k"}, nil
	})
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}

	// Give the goroutine a moment to actually register as running --
	// Submit itself marks runningByTenant before returning, so this
	// should already be true, but a brief wait keeps the test robust
	// against scheduling variance.
	time.Sleep(10 * time.Millisecond)

	_, err = m.Submit(tenant.TenantID(9), func() (*PackageResult, error) {
		return &PackageResult{Key: "should-not-run"}, nil
	})
	if err == nil {
		t.Fatal("expected the second Submit for the same tenant to be rejected")
	}
	inFlightErr, ok := err.(*ErrTenantExportInFlight)
	if !ok {
		t.Fatalf("expected *ErrTenantExportInFlight, got %T: %v", err, err)
	}
	if inFlightErr.ExistingTicket != firstTicket {
		t.Errorf("ExistingTicket: got %q, want %q", inFlightErr.ExistingTicket, firstTicket)
	}

	close(release)
	waitForStatus(t, m, firstTicket, JobComplete, time.Second)

	// Now that the first job has finished, a new Submit for the same
	// tenant must succeed -- the throttle is per-in-flight-job, not a
	// permanent one-export-ever lock.
	secondTicket, err := m.Submit(tenant.TenantID(9), func() (*PackageResult, error) {
		return &PackageResult{Key: "k2"}, nil
	})
	if err != nil {
		t.Fatalf("Submit after the first job finished should succeed: %v", err)
	}
	waitForStatus(t, m, secondTicket, JobComplete, time.Second)
}

// TestJobManager_ConcurrencyBound proves the semaphore actually limits
// how many jobs run their work function AT THE SAME TIME, not just
// that it exists -- submits more jobs (different tenants, so the
// per-tenant throttle doesn't interfere) than the concurrency limit,
// and confirms the observed peak concurrent execution never exceeds
// that limit.
func TestJobManager_ConcurrencyBound(t *testing.T) {
	const limit = 2
	const jobCount = 6
	m := NewJobManager(limit)

	var current int32
	var peak int32
	release := make(chan struct{})

	for i := 0; i < jobCount; i++ {
		tid := tenant.TenantID(i) // distinct tenants -- isolates this test from the per-tenant throttle
		_, err := m.Submit(tid, func() (*PackageResult, error) {
			n := atomic.AddInt32(&current, 1)
			for {
				p := atomic.LoadInt32(&peak)
				if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
					break
				}
			}
			<-release
			atomic.AddInt32(&current, -1)
			return &PackageResult{Key: fmt.Sprintf("k%d", tid)}, nil
		})
		if err != nil {
			t.Fatalf("Submit tenant %d: %v", tid, err)
		}
	}

	// Give every goroutine a chance to reach the semaphore.
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&current); got != limit {
		t.Errorf("concurrently executing jobs: got %d, want exactly %d (the semaphore limit) with %d jobs queued behind it",
			got, limit, jobCount-limit)
	}

	close(release)
	time.Sleep(50 * time.Millisecond)

	if got := atomic.LoadInt32(&peak); got > limit {
		t.Errorf("peak concurrent execution: got %d, want <= %d", got, limit)
	}
}
