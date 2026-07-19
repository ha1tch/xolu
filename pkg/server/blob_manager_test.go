// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// White-box unit tests for the per-tenant blob manager, focused on the
// multi-tenant aggregation paths (GlobalUsage, Sweep) that the handler-level
// tests do not exercise with more than one open tenant.

package server

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/blob"
	sl "github.com/ha1tch/xolu/pkg/storelayout"

	"github.com/rs/zerolog"
)

func newTestBlobManager(t *testing.T, sampleEvery time.Duration) *blobManager {
	t.Helper()
	m, err := newBlobManager(
		t.TempDir(),
		1<<20,
		blob.GCConfig{Interval: time.Hour, GracePeriod: 0},
		sampleEvery,
		zerolog.Nop(),
	)
	if err != nil {
		t.Fatalf("newBlobManager: %v", err)
	}
	return m
}

// GlobalUsage must sum sampled usage across multiple open tenants, and must
// skip tenants whose sampler has not completed a walk (SampledAt zero).
func TestBlobManager_GlobalUsage_MultiTenant(t *testing.T) {
	m := newTestBlobManager(t, time.Hour) // sampling enabled (long interval)

	// Open three tenants and write distinct content to two of them.
	for _, tc := range []struct {
		id   uint16
		key  string
		body string
	}{
		{1, "a", "alpha-one"},
		{1, "b", "alpha-two"},
		{2, "c", "beta-one"},
	} {
		st, err := m.StoreFor(tc.id)
		if err != nil {
			t.Fatalf("StoreFor(%d): %v", tc.id, err)
		}
		if _, _, _, err := st.Put(tc.key, bytes.NewReader([]byte(tc.body)), ""); err != nil {
			t.Fatalf("Put: %v", err)
		}
	}
	// Tenant 3 is opened but never written, and we deliberately do NOT sample
	// it — its SampledAt stays zero and it must be skipped by GlobalUsage.
	if _, err := m.StoreFor(3); err != nil {
		t.Fatalf("StoreFor(3): %v", err)
	}

	// Force a sample on tenants 1 and 2 only.
	for _, id := range []uint16{1, 2} {
		smp := m.SamplerFor(id)
		if smp == nil {
			t.Fatalf("SamplerFor(%d) is nil", id)
		}
		smp.ForceResample()
	}

	g := m.GlobalUsage()
	// Two tenants contributed (tenant 3 has no completed sample → skipped).
	if g.TenantCount != 2 {
		t.Errorf("TenantCount = %d, want 2 (tenant 3 unsampled, skipped)", g.TenantCount)
	}
	// 3 keys total (a, b under t1; c under t2); 3 distinct blobs.
	if g.TotalKeyCount != 3 {
		t.Errorf("TotalKeyCount = %d, want 3", g.TotalKeyCount)
	}
	if g.TotalBlobCount != 3 {
		t.Errorf("TotalBlobCount = %d, want 3", g.TotalBlobCount)
	}
	if g.SampledAt.IsZero() {
		t.Error("SampledAt should be the latest non-zero sample time")
	}
}

// GlobalUsage with sampling disabled (no samplers) contributes nothing.
func TestBlobManager_GlobalUsage_SamplingDisabled(t *testing.T) {
	m := newTestBlobManager(t, 0) // sampleEvery 0 → sampler nil branch

	st, err := m.StoreFor(1)
	if err != nil {
		t.Fatalf("StoreFor(1): %v", err)
	}
	if _, _, _, err := st.Put("k", bytes.NewReader([]byte("data")), ""); err != nil {
		t.Fatalf("Put: %v", err)
	}

	g := m.GlobalUsage()
	if g.TenantCount != 0 {
		t.Errorf("TenantCount = %d, want 0 (no samplers)", g.TenantCount)
	}
	if m.SamplerFor(1) != nil {
		t.Error("SamplerFor(1) should be nil when sampling is disabled")
	}
}

// Sweep must aggregate GC across all open tenant stores. An orphaned blob (no
// alias) under each tenant must be quarantined; the aggregate report counts
// every tenant.
func TestBlobManager_Sweep_MultiTenant(t *testing.T) {
	m := newTestBlobManager(t, 0)

	// Two tenants, each with one orphaned blob (PutRaw writes no alias).
	for _, id := range []uint16{1, 2} {
		st, err := m.StoreFor(id)
		if err != nil {
			t.Fatalf("StoreFor(%d): %v", id, err)
		}
		if _, _, _, err := st.PutRaw(bytes.NewReader([]byte("orphan-"+string(rune('0'+id)))), ""); err != nil {
			t.Fatalf("PutRaw(%d): %v", id, err)
		}
	}

	rep, err := m.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	// Grace period is 0, so each orphan is quarantined this pass. Two tenants,
	// one orphan each → at least two quarantined, and Examined counts both
	// tenant sweeps.
	if rep.Quarantined < 2 {
		t.Errorf("Quarantined = %d, want >= 2 (one orphan per tenant)", rep.Quarantined)
	}
	if rep.Examined < 2 {
		t.Errorf("Examined = %d, want >= 2 (one per tenant sweep)", rep.Examined)
	}
}

// Sweep over an empty manager (no open tenants) is a no-op with a zero report.
func TestBlobManager_Sweep_NoTenants(t *testing.T) {
	m := newTestBlobManager(t, 0)
	rep, err := m.Sweep(context.Background())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if rep.Quarantined != 0 || rep.Collected != 0 || rep.Examined != 0 {
		t.Errorf("empty Sweep report = %+v, want all zero", rep)
	}
}

// StoreFor caches: a second call returns the same store, and the directory is
// the per-tenant TenantBlobDir.
func TestBlobManager_StoreFor_CachesAndPlaces(t *testing.T) {
	m := newTestBlobManager(t, 0)
	s1, err := m.StoreFor(7)
	if err != nil {
		t.Fatalf("StoreFor(7): %v", err)
	}
	s2, err := m.StoreFor(7)
	if err != nil {
		t.Fatalf("StoreFor(7) again: %v", err)
	}
	if s1 != s2 {
		t.Error("StoreFor must return the cached store on repeat calls")
	}
	wantDir := sl.TenantBlobDir(m.baseDir, 7)
	if got := s1.Root(); got != wantDir {
		t.Errorf("store root = %q, want %q", got, wantDir)
	}
	if filepath.Base(filepath.Dir(wantDir)) != "t0007" {
		t.Errorf("expected tenant dir t0007, got parent %q", filepath.Dir(wantDir))
	}
}

// newBlobManager startup discovery opens tenants that already have a blobs/
// directory on disk, so a fresh manager over a populated base dir sees them.
func TestBlobManager_StartupDiscovery(t *testing.T) {
	base := t.TempDir()

	// First manager: create blob data for tenant 5.
	m1, err := newBlobManager(base, 1<<20, blob.GCConfig{Interval: time.Hour}, time.Hour, zerolog.Nop())
	if err != nil {
		t.Fatalf("newBlobManager #1: %v", err)
	}
	st, err := m1.StoreFor(5)
	if err != nil {
		t.Fatalf("StoreFor(5): %v", err)
	}
	if _, _, _, err := st.Put("k", bytes.NewReader([]byte("v")), ""); err != nil {
		t.Fatalf("Put: %v", err)
	}
	_ = m1.Close()

	// Second manager over the same base must discover tenant 5 at startup and
	// have a warm sampler for it (sampling enabled).
	m2, err := newBlobManager(base, 1<<20, blob.GCConfig{Interval: time.Hour}, time.Hour, zerolog.Nop())
	if err != nil {
		t.Fatalf("newBlobManager #2: %v", err)
	}
	defer m2.Close()
	if m2.SamplerFor(5) == nil {
		t.Error("startup discovery should have opened tenant 5's store (sampler non-nil)")
	}
}
