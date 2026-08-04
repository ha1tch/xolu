// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ha1tch/xolu/pkg/blob"
	gcpkg "github.com/ha1tch/xolu/pkg/gc"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
	"github.com/ha1tch/xolu/pkg/tenantexport"

	"github.com/rs/zerolog"
)

// blobManager manages per-tenant blob.Store lifecycle, mirroring
// timeseries.DefaultManager. Each tenant's blobs live at <baseDir>/tXXXX/blobs
// (tenant-first, derived by pkg/storelayout); the manager hands out one Store
// per tenant ID, lazily opened and cached, and owns that tenant's GC worker and
// usage sampler.
//
// A Store is single-tenant (its root is the tenant's blobs directory). Tenant
// isolation is therefore structural: distinct tenants have distinct store
// roots, with no shared namespace to leak across. Tenant 0 is a tenant like any
// other and gets <baseDir>/t0000/blobs.
type blobManager struct {
	baseDir string
	maxSize int64
	logger  zerolog.Logger

	gcCfg         blob.GCConfig
	sampleEvery   time.Duration
	sampleEnabled bool

	mu      sync.Mutex // serialises lazy-open
	entries sync.Map   // tenantID (uint16) -> *blobTenant
}

// blobTenant is the per-tenant bundle the manager owns.
type blobTenant struct {
	store   *blob.Store
	sampler *blob.UsageSampler // nil when sampling disabled
}

// newBlobManager creates a blob manager rooted at the data root (baseDir) and
// scans for existing tenant directories that already contain a blobs/ role
// directory, registering them for lazy open. GC and sampler configuration is
// captured here and applied per tenant when each store is first opened.
func newBlobManager(
	baseDir string,
	maxSize int64,
	gcCfg blob.GCConfig,
	sampleEvery time.Duration,
	logger zerolog.Logger,
) (*blobManager, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("blob manager: mkdir %s: %w", baseDir, err)
	}

	m := &blobManager{
		baseDir:       baseDir,
		maxSize:       maxSize,
		logger:        logger,
		gcCfg:         gcCfg,
		sampleEvery:   sampleEvery,
		sampleEnabled: sampleEvery > 0,
	}

	// Startup discovery: open any tenant that already has a blobs/ role
	// directory, so its GC worker and sampler run from boot and GlobalUsage
	// covers it without waiting for the first request to that tenant.
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("blob manager: scan %s: %w", baseDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id, ok := sl.ParseTenantSegment(e.Name())
		if !ok {
			continue
		}
		blobDir := sl.TenantBlobDir(baseDir, id)
		if info, statErr := os.Stat(blobDir); statErr == nil && info.IsDir() {
			if _, openErr := m.StoreFor(id); openErr != nil {
				logger.Warn().Err(openErr).Uint16("tenant", uint16(id)).
					Msg("blob manager: failed to open discovered tenant store")
			}
		}
	}
	return m, nil
}

// tenantDir returns the per-tenant blob directory: <baseDir>/tXXXX/blobs.
func (m *blobManager) tenantDir(tenantID tenant.TenantID) string {
	return sl.TenantBlobDir(m.baseDir, tenantID)
}

// StoreFor returns the blob.Store for a tenant, opening it (and starting its GC
// worker and sampler) lazily on first access. Subsequent calls return the
// cached store.
func (m *blobManager) StoreFor(tenantID tenant.TenantID) (*blob.Store, error) {
	if v, ok := m.entries.Load(tenantID); ok {
		return v.(*blobTenant).store, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Re-check under lock.
	if v, ok := m.entries.Load(tenantID); ok {
		return v.(*blobTenant).store, nil
	}

	dir := m.tenantDir(tenantID)
	store, err := blob.NewStore(dir, m.maxSize)
	if err != nil {
		return nil, fmt.Errorf("blob manager: open tenant %04X: %w", tenantID, err)
	}

	bt := &blobTenant{store: store}

	if m.sampleEnabled {
		s := blob.NewUsageSampler(store, m.sampleEvery)
		s.Start()
		bt.sampler = s
	}

	m.entries.Store(tenantID, bt)
	m.logger.Info().Str("dir", dir).Uint16("tenant", uint16(tenantID)).Msg("Blob store opened")
	return store, nil
}

// Sweep runs blob GC across every currently-open tenant store and aggregates
// the per-store reports into one. It implements gcpkg.Sweeper, so a single
// server-level worker named "blob-gc" can drive GC for all tenants — preserving
// the operator-facing admin API while keeping each blob.Store single-tenant.
// Tenants whose stores are not open hold no live process state; their GC runs
// once they are opened (on first request or startup discovery).
func (m *blobManager) Sweep(ctx context.Context) (gcpkg.Report, error) {
	var agg gcpkg.Report
	var firstErr error
	m.entries.Range(func(_, v any) bool {
		bt := v.(*blobTenant)
		sweeper := blob.NewGCWorker(bt.store, m.gcCfg, nil)
		rep, err := sweeper.Sweep(ctx)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		agg.Examined += rep.Examined
		agg.Collected += rep.Collected
		agg.Quarantined += rep.Quarantined
		agg.Errors += rep.Errors
		return true
	})
	return agg, firstErr
}

// blobExportSweeper implements gc.Sweeper for the async-export TTL
// sweep (T-149) -- deliberately a separate type from blobManager
// itself rather than a second method on it: blobManager.Sweep above is
// already registered as its own gc.Worker for ordinary blob GC
// (unreferenced-content reclamation), a different lifecycle with a
// different config (BlobGCEnabled/IntervalSecs/GracePeriodSecs) and a
// different meaning entirely -- an export blob IS referenced (by its
// own key alias) for as long as it hasn't expired, so blobManager's
// own GC would never touch it regardless. Wrapping the same m.entries
// iteration in its own type keeps the two sweeps independently
// configurable and independently schedulable, per T-149's own design
// (BlobExportSweepEnabled/IntervalSecs/TTLSecs, separate from the
// BlobGC* family).
type blobExportSweeper struct {
	mgr *blobManager
	ttl time.Duration
}

func (s *blobExportSweeper) Sweep(ctx context.Context) (gcpkg.Report, error) {
	var agg gcpkg.Report
	var firstErr error
	s.mgr.entries.Range(func(_, v any) bool {
		bt := v.(*blobTenant)
		rep, err := tenantexport.SweepExpiredExports(ctx, bt.store, s.ttl)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		agg.Examined += rep.Examined
		agg.Collected += rep.Collected
		agg.Errors += rep.Errors
		return true
	})
	return agg, firstErr
}

// SamplerFor returns the usage sampler for a tenant, or nil if the tenant's
// store has not been opened yet or sampling is disabled. It does not force an
// open: usage for an unopened tenant is zero by definition.
func (m *blobManager) SamplerFor(tenantID tenant.TenantID) *blob.UsageSampler {
	if v, ok := m.entries.Load(tenantID); ok {
		return v.(*blobTenant).sampler
	}
	return nil
}

// GlobalUsage aggregates the most recently sampled usage across all currently
// open tenant stores. Tenants whose stores have not been opened contribute
// nothing (their on-disk usage is, by construction, only reachable once opened).
func (m *blobManager) GlobalUsage() blob.GlobalUsage {
	var g blob.GlobalUsage
	m.entries.Range(func(_, v any) bool {
		bt := v.(*blobTenant)
		if bt.sampler == nil {
			return true
		}
		u := bt.sampler.Current()
		if u.SampledAt.IsZero() {
			return true
		}
		g.TotalBlobCount += u.BlobCount
		g.TotalKeyCount += u.KeyCount
		g.TotalBytes += u.Bytes
		g.TenantCount++
		if u.SampledAt.After(g.SampledAt) {
			g.SampledAt = u.SampledAt
		}
		return true
	})
	return g
}

// Close stops all per-tenant samplers. Blob stores are filesystem-backed and
// hold no handles, so there is nothing to close on the store itself. GC workers
// are stopped via the server's shared worker list, not here, to avoid
// double-stop.
func (m *blobManager) Close() error {
	m.entries.Range(func(key, v any) bool {
		bt := v.(*blobTenant)
		if bt.sampler != nil {
			bt.sampler.Stop()
		}
		m.entries.Delete(key)
		return true
	})
	return nil
}
