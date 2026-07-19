// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

import (
	"context"
	"fmt"
	"os"
	"sync"

	sl "github.com/ha1tch/xolu/pkg/storelayout"
)

// DefaultManager manages per-tenant Store lifecycle.
// It is backend-agnostic — the StoreFactory controls which engine is used.
type DefaultManager struct {
	baseDir     string
	factory     StoreFactory
	config      StoreConfig
	stores      sync.Map   // tenantID (uint16) -> Store
	known       sync.Map   // tenantID (uint16) -> struct{}
	tenantNames sync.Map   // tenantID (uint16) -> string (tenant name)
	mu          sync.Mutex // serialises Provision and lazy-open
}

// NewManager creates a timeseries manager rooted at the data root (baseDir).
// Each tenant's timeseries store lives at <baseDir>/tXXXX/ts (tenant-first,
// derived by pkg/storelayout). NewManager scans the data root for existing
// tenant directories that already contain a ts/ subdirectory and registers them
// as provisioned (lazy-open on first request). Tenant names for pre-existing
// stores are not known at scan time; they are recorded on the first StoreFor
// call that provides a name via Provision.
func NewManager(baseDir string, factory StoreFactory, cfg StoreConfig) (*DefaultManager, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("ts manager: mkdir %s: %w", baseDir, err)
	}

	m := &DefaultManager{
		baseDir: baseDir,
		factory: factory,
		config:  cfg,
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("ts manager: scan %s: %w", baseDir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id, ok := sl.ParseTenantSegment(e.Name())
		if !ok {
			continue
		}
		// A tenant counts as having timeseries data only if its ts/ role
		// directory exists. Tenant directories without one are SQLite-only so
		// far and will get a ts/ on first Provision.
		tsDir := sl.TenantTSDir(baseDir, id)
		if info, statErr := os.Stat(tsDir); statErr == nil && info.IsDir() {
			m.known.Store(id, struct{}{})
		}
	}
	return m, nil
}

// Provision creates a timeseries store for a tenant. Idempotent.
// tenantName is stored for use as the dynconfig namespace scope.
func (m *DefaultManager) Provision(ctx context.Context, tenantID uint16, tenantName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.tenantNames.Store(tenantID, tenantName)

	if _, loaded := m.stores.Load(tenantID); loaded {
		return nil
	}
	dir := m.tenantDir(tenantID)
	store, err := m.factory(dir, m.config, tenantName)
	if err != nil {
		return fmt.Errorf("ts provision tenant %d: %w", tenantID, err)
	}
	m.stores.Store(tenantID, store)
	m.known.Store(tenantID, struct{}{})
	return nil
}

// StoreFor returns the Store for a tenant, opening it lazily if needed.
// On lazy open, uses the tenant name previously recorded by Provision (if any).
func (m *DefaultManager) StoreFor(tenantID uint16) (Store, error) {
	if v, ok := m.stores.Load(tenantID); ok {
		return v.(Store), nil
	}
	if _, ok := m.known.Load(tenantID); !ok {
		return nil, fmt.Errorf("tenant %d not provisioned for timeseries (XOLU-TS003)", tenantID)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if v, ok := m.stores.Load(tenantID); ok {
		return v.(Store), nil
	}

	// Retrieve the tenant name recorded during Provision (may be empty string
	// for stores discovered from disk at startup before any Provision call).
	tenantName := ""
	if v, ok := m.tenantNames.Load(tenantID); ok {
		tenantName = v.(string)
	}

	dir := m.tenantDir(tenantID)
	store, err := m.factory(dir, m.config, tenantName)
	if err != nil {
		return nil, fmt.Errorf("ts lazy open tenant %d: %w", tenantID, err)
	}
	m.stores.Store(tenantID, store)
	return store, nil
}

// IsProvisioned reports whether a tenant has timeseries storage.
func (m *DefaultManager) IsProvisioned(tenantID uint16) bool {
	_, ok := m.known.Load(tenantID)
	return ok
}

// Close shuts down all open stores.
func (m *DefaultManager) Close() error {
	var firstErr error
	m.stores.Range(func(key, value any) bool {
		if err := value.(Store).Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		m.stores.Delete(key)
		return true
	})
	return firstErr
}

// --- Internal ---

func (m *DefaultManager) tenantDir(tenantID uint16) string {
	return sl.TenantTSDir(m.baseDir, tenantID)
}
