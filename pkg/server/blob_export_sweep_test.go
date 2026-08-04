// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/tenant"
)

// TestBlobExportSweeper_MultiTenant proves the sweeper reaches every
// currently-open tenant's store (mirroring
// TestBlobManager_GlobalUsage_MultiTenant's own multi-tenant pattern),
// deletes only export-prefixed, expired blobs, and leaves everything
// else -- other tenants' non-expired exports, and any non-export blob
// regardless of tenant -- completely untouched.
func TestBlobExportSweeper_MultiTenant(t *testing.T) {
	m := newTestBlobManager(t, time.Hour)

	tenants := []tenant.TenantID{1, 2, 3}
	for _, tid := range tenants {
		store, err := m.StoreFor(tid)
		if err != nil {
			t.Fatalf("StoreFor(%d): %v", tid, err)
		}
		if _, _, _, err := store.Put("export-"+tid.String()+".zip", strings.NewReader("export data"), "application/zip"); err != nil {
			t.Fatalf("put export for tenant %d: %v", tid, err)
		}
		if _, _, _, err := store.Put("regular-blob", strings.NewReader("not an export"), "text/plain"); err != nil {
			t.Fatalf("put regular blob for tenant %d: %v", tid, err)
		}
	}

	// TTL 0: everything already written counts as expired, matching
	// tenantexport's own TestSweepExpiredExports_TTLZeroDeletesEverythingMatchingPrefix
	// convention -- deterministic, no sleep-based timing needed.
	sweeper := &blobExportSweeper{mgr: m, ttl: 0}
	report, err := sweeper.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Examined != 3 {
		t.Errorf("Examined: got %d, want 3 (one export blob per tenant)", report.Examined)
	}
	if report.Collected != 3 {
		t.Errorf("Collected: got %d, want 3", report.Collected)
	}

	for _, tid := range tenants {
		store, err := m.StoreFor(tid)
		if err != nil {
			t.Fatalf("StoreFor(%d): %v", tid, err)
		}
		if _, _, err := store.Get("export-" + tid.String() + ".zip"); err == nil {
			t.Errorf("tenant %d: expired export should have been deleted", tid)
		}
		rc, _, err := store.Get("regular-blob")
		if err != nil {
			t.Errorf("tenant %d: regular-blob must survive the export sweep untouched: %v", tid, err)
		} else {
			rc.Close()
		}
	}
}

func TestBlobExportSweeper_NoOpenTenants(t *testing.T) {
	m := newTestBlobManager(t, time.Hour)
	sweeper := &blobExportSweeper{mgr: m, ttl: time.Hour}
	report, err := sweeper.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep with no open tenants: %v", err)
	}
	if report.Examined != 0 || report.Collected != 0 {
		t.Errorf("expected an all-zero report, got %+v", report)
	}
}

func TestBlobExportSweeper_FreshExportSurvives(t *testing.T) {
	m := newTestBlobManager(t, time.Hour)
	store, err := m.StoreFor(tenant.TenantID(1))
	if err != nil {
		t.Fatalf("StoreFor: %v", err)
	}
	if _, _, _, err := store.Put("export-1.zip", strings.NewReader("fresh"), "application/zip"); err != nil {
		t.Fatalf("put: %v", err)
	}

	sweeper := &blobExportSweeper{mgr: m, ttl: time.Hour}
	report, err := sweeper.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if report.Collected != 0 {
		t.Errorf("Collected: got %d, want 0 -- a freshly-written export must not be treated as expired", report.Collected)
	}
	rc, _, err := store.Get("export-1.zip")
	if err != nil {
		t.Errorf("fresh export should still exist: %v", err)
	} else {
		rc.Close()
	}
}
