// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"database/sql"
	"sync"

	"github.com/ha1tch/xolu/pkg/storelayout"
)

// Manager is the per-tenant assembly point for cal: it binds each tenant's
// SQLite booking source (over the tenant's primary store DB) to its Pebble
// occupancy index (at the storelayout cal path) and a lifecycle engine over the
// pair. The REST handlers sit on the Manager: one CalFor(tenant) call yields a
// ready lifecycle.
//
// Assemblies are built lazily and cached per tenant (CalFor is idempotent), the
// same model the server uses for per-tenant sulpher job managers. The Manager
// does not own the *sql.DB (the caller's store does); it owns the index stores
// it opens, which Close releases.
type Manager struct {
	baseDir string
	db      *sql.DB
	reuse   bool

	mu      sync.Mutex
	tenants map[uint16]*tenantCal
}

type tenantCal struct {
	source *SQLiteBookingSource
	index  *IndexStore
	lc     *Lifecycle
}

// NewManager binds a base directory (under which per-tenant cal index stores are
// opened at storelayout.TenantCalDir) and the tenant primary-store *sql.DB. The
// ordinal-reuse policy defaults to retire (false).
func NewManager(baseDir string, db *sql.DB) *Manager {
	return &Manager{
		baseDir: baseDir,
		db:      db,
		tenants: map[uint16]*tenantCal{},
	}
}

// SetOrdinalReuse selects the OrdinalReuse policy for sources assembled after the
// call (GATE-3 #2). Must be set before the first CalFor for a tenant to take
// effect for that tenant.
func (m *Manager) SetOrdinalReuse(reuse bool) {
	m.mu.Lock()
	m.reuse = reuse
	m.mu.Unlock()
}

// assemble builds (or returns the cached) per-tenant cal assembly.
func (m *Manager) assemble(tenantID uint16) (*tenantCal, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tc, ok := m.tenants[tenantID]; ok {
		return tc, nil
	}
	idxDir := storelayout.TenantCalDir(m.baseDir, tenantID)
	idx, err := OpenIndexStore(idxDir)
	if err != nil {
		return nil, err
	}
	src := NewSQLiteBookingSource(m.db, tenantID, m.reuse)
	lc := NewLifecycle(src, idx)

	// On (re)assembly, rebuild the index from the authoritative SQLite records so
	// the derived occupancy matches the persisted bookings (index == rebuild).
	// This makes a fresh process / first-touch correct without a separate warmup.
	if err := idx.RebuildFrom(src); err != nil {
		_ = idx.Close()
		return nil, err
	}

	tc := &tenantCal{source: src, index: idx, lc: lc}
	m.tenants[tenantID] = tc
	return tc, nil
}

// CalFor returns the lifecycle engine for a tenant, assembling it on first use.
// Idempotent: repeated calls for the same tenant return the same lifecycle.
func (m *Manager) CalFor(tenantID uint16) (*Lifecycle, error) {
	tc, err := m.assemble(tenantID)
	if err != nil {
		return nil, err
	}
	return tc.lc, nil
}

// SourceFor returns the tenant's SQLite booking source (assembling if needed).
// Returns nil only if assembly fails, which CalFor surfaces with an error; use
// CalFor when an error path is needed.
func (m *Manager) SourceFor(tenantID uint16) *SQLiteBookingSource {
	tc, err := m.assemble(tenantID)
	if err != nil {
		return nil
	}
	return tc.source
}

// IndexFor returns the tenant's occupancy index store (assembling if needed).
func (m *Manager) IndexFor(tenantID uint16) *IndexStore {
	tc, err := m.assemble(tenantID)
	if err != nil {
		return nil
	}
	return tc.index
}

// CreateCalendar creates a calendar in the given tenant's persistence layer
// AND registers it with the in-memory index. This is the transactional
// operation callers should almost always use rather than composing
// SourceFor(t).CreateCalendar(c) with IndexFor(t).RegisterCalendar(c)
// themselves.
//
// Without the index registration step, subsequent Lifecycle.Create calls
// against the calendar fail with ErrUnknownCalendar from IndexStore.ordinalFor
// — the index rebuild only runs at first assemble(), so any calendar added
// after that point is invisible to the ordinal map until explicitly
// registered. This facade eliminates that footgun.
//
// Rollback semantics: the SQL insert runs first. If it fails, no index
// change occurs. If it succeeds, the index register cannot fail (it is a
// pure in-memory map update). Consequently the observable state after this
// method returns is either "both persisted and indexed" (nil error) or
// "neither" (non-nil error, with sentinel wrapping via the source layer).
//
// Concurrency: the index register runs under IndexStore's own mutex. The
// SQL insert runs under SQLite's transaction semantics. Callers concurrently
// creating the same calendar will see one succeed and the others fail with
// ErrCalendarExists (wrapped by the source layer).
//
// Introduced in v0.14.10.
func (m *Manager) CreateCalendar(tenantID uint16, c Calendar) (Calendar, error) {
	tc, err := m.assemble(tenantID)
	if err != nil {
		return Calendar{}, err
	}
	created, err := tc.source.CreateCalendar(c)
	if err != nil {
		return Calendar{}, err
	}
	tc.index.RegisterCalendar(created)
	return created, nil
}

// Close releases every assembled tenant's index store. The *sql.DB is owned by
// the caller's store and is NOT closed here.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	for id, tc := range m.tenants {
		if err := tc.index.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(m.tenants, id)
	}
	return firstErr
}
