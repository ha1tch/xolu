// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package tenant provides tenant-scoping helpers for the middle and handler tiers.
//
// The storage tier enforces isolation by construction (see storage.StoreConfig).
// This package provides the supporting utilities that other tiers need:
//
//   - NodeID / CacheKey: construct globally-unique identifiers when a shared
//     in-memory structure (graph, cache) spans multiple tenants.
//   - Registry: maps human-readable tenant names to uint16 identifiers.
//     When a Persister is attached, mappings are durable across restarts.
package tenant

import (
	"context"
	"fmt"
	"sync"
)

// TenantID is the canonical, sole representation of a tenant's
// server-local identity. Every tenant-scoped string anywhere in this
// codebase (table names, directory names, cache keys, node-ID
// prefixes) must derive from a TenantID value via this type's own
// methods — never from a bare uint16 formatted by hand. That is the
// actual invariant this type exists to make impossible to violate by
// construction: found 2026-07-28 when a dxp integration test needed
// one tenant identifier comparable across bal, fsm, and entity, and
// discovered five independent places across the tree had each
// separately reimplemented the same "%04X" hex encoding — a bug class
// a bare uint16 can never prevent, because the compiler has no way to
// distinguish "a tenant ID" from any other uint16 in the program.
//
// Deliberately NOT the wider federation address a future nolu project
// would need to route data between servers (docs/NOLU_EVENTS.md's
// LocalRef already answers "which server" with InstanceURL, a string
// — not a numeric composition of this type). TenantID is only ever
// "which tenant on THIS server," unchanged in width or meaning
// regardless of what federation scheme, if any, eventually wraps it.
// That boundary is deliberate, not an oversight: xolu's own local
// addressing shouldn't have to anticipate a design nolu itself owns.
type TenantID uint16

// String is the canonical bare tenant-ID string: 4-digit uppercase
// hex, no decoration. Every other method on this type adds its own
// prefix/suffix for a specific naming purpose (TablePrefix adds "t"
// and "_" for SQL table names; GraphNodePrefix adds a trailing "@" for
// node-ID namespacing) — this is the undecorated form underneath all
// of them, for contexts that need a tenant represented as an opaque,
// cross-primitive-comparable string with no naming convention baked
// in (dxp.Cache tenant keys are the motivating case).
func (t TenantID) String() string {
	return fmt.Sprintf("%04X", uint16(t))
}

// IsZero reports whether t is the reserved unscoped/default tenant.
func (t TenantID) IsZero() bool { return t == 0 }

func (t TenantID) tablePrefix() string { return "t" + t.String() }

// TablePrefix is the per-tenant table-name prefix WITH the trailing
// underscore ("t0000_"), for primitives that own their own table
// families (bal).
func (t TenantID) TablePrefix() string { return t.tablePrefix() + "_" }

// DirName returns the bare per-tenant directory name ("t0000", no
// trailing underscore — directories don't need TablePrefix's SQL
// separator).
func (t TenantID) DirName() string { return t.tablePrefix() }

// GraphNodePrefix returns the XXXX@ prefix used to namespace graph
// node IDs in a shared in-memory graph spanning multiple tenants.
// Tenant 0 (unscoped): "" — no prefix; node IDs are bare "entity:id".
// Non-zero tenants: "XXXX@" (e.g. "0001@").
func (t TenantID) GraphNodePrefix() string {
	if t.IsZero() {
		return ""
	}
	return t.String() + "@"
}

// StorageDirSegment returns the directory name for tenant-scoped file
// storage (timeseries data, JSON file store). Tenant 0 (unscoped): ""
// — data lives directly in the base directory. Non-zero: "tXXXX".
func (t TenantID) StorageDirSegment() string {
	if t.IsZero() {
		return ""
	}
	return t.tablePrefix()
}

// ScopeKey prepends this tenant's hex ID to an arbitrary cache or
// lookup key, producing "XXXX:key" (or the bare key, unscoped, for
// tenant 0).
func (t TenantID) ScopeKey(key string) string {
	if t.IsZero() {
		return key
	}
	return t.String() + ":" + key
}

// NodeID returns a graph node identifier scoped to this tenant.
// Tenant 0: "entity:id". Non-zero: "XXXX@entity:id".
func (t TenantID) NodeID(entity string, id int) string {
	return t.GraphNodePrefix() + fmt.Sprintf("%s:%d", entity, id)
}

// CacheKey returns a cache key scoped to this tenant.
func (t TenantID) CacheKey(entity string, id int) string {
	return t.ScopeKey(fmt.Sprintf("%s:%d", entity, id))
}

// CachePattern returns a cache-invalidation pattern scoped to this tenant.
func (t TenantID) CachePattern(entity string) string {
	return t.ScopeKey(entity + ":*")
}

// CacheTenantPattern returns a pattern matching every key for this tenant.
func (t TenantID) CacheTenantPattern() string {
	if t.IsZero() {
		return "*"
	}
	return t.String() + ":*"
}

// CacheListPattern returns a pattern matching only list cache keys for
// an entity type, scoped to this tenant.
func (t TenantID) CacheListPattern(entity string) string {
	return t.ScopeKey(entity + ":list:*")
}

// GraphTableName returns the topology table name for this tenant.
// Example: t0001_graph
func (t TenantID) GraphTableName() string { return t.tablePrefix() + "_graph" }

// GraphEdgesTableName is the legacy name for GraphTableName.
// Deprecated: use GraphTableName. Retained for real production callers
// (pkg/server, pkg/storage) as well as the migration command.
func (t TenantID) GraphEdgesTableName() string { return t.GraphTableName() }

// NodesTableName returns the blob node store table name for this tenant.
// Example: t0001_nodes
func (t TenantID) NodesTableName() string { return t.tablePrefix() + "_nodes" }

// NodeSeqTableName returns the node ID sequence table name for this tenant.
// Example: t0001_nseq
func (t TenantID) NodeSeqTableName() string { return t.tablePrefix() + "_nseq" }

// NodeFTSTableName returns the node full-text search table name for this tenant.
// Example: t0001_nfts
func (t TenantID) NodeFTSTableName() string { return t.tablePrefix() + "_nfts" }

// EdgePropsTableName returns the blob edge property table name for this tenant.
// Example: t0001_edges
func (t TenantID) EdgePropsTableName() string { return t.tablePrefix() + "_edges" }

// EdgeSeqTableName returns the edge ID sequence table name for this tenant.
// Example: t0001_eseq
func (t TenantID) EdgeSeqTableName() string { return t.tablePrefix() + "_eseq" }

// NodeSchemaTableName returns the node schema registry table name for this tenant.
// Example: t0001_n_sch
func (t TenantID) NodeSchemaTableName() string { return t.tablePrefix() + "_n_sch" }

// EdgeSchemaTableName returns the edge schema registry table name for this tenant.
// Example: t0001_e_sch
func (t TenantID) EdgeSchemaTableName() string { return t.tablePrefix() + "_e_sch" }

// EdgeFTSTableName returns the edge full-text search table name for this tenant.
// Example: t0001_efts
func (t TenantID) EdgeFTSTableName() string { return t.tablePrefix() + "_efts" }

// AdaptedNodeTableName returns the adapted native-column table name
// for a schema-registered node entity type on this tenant.
// Example: t0001_ndata_user
func (t TenantID) AdaptedNodeTableName(entityType string) string {
	return t.tablePrefix() + "_ndata_" + entityType
}

// AdaptedEdgeTableName returns the adapted native-column table name
// for a schema-registered edge label on this tenant.
// Example: t0001_edata_KNOWS
func (t TenantID) AdaptedEdgeTableName(relType string) string {
	return t.tablePrefix() + "_edata_" + relType
}

// NodesIndexEntityType returns the index name for entity_type lookups.
func (t TenantID) NodesIndexEntityType() string { return "idx_" + t.NodesTableName() + "_etype" }

// NodesIndexUpdatedAt returns the index name for updated_at ordering.
func (t TenantID) NodesIndexUpdatedAt() string { return "idx_" + t.NodesTableName() + "_updated" }

// NodeSeqIndexEntityType returns the index name on the node sequence table.
func (t TenantID) NodeSeqIndexEntityType() string { return "idx_" + t.NodeSeqTableName() + "_etype" }

// EdgeSeqIndexRelType returns the index name on the edge sequence table.
func (t TenantID) EdgeSeqIndexRelType() string { return "idx_" + t.EdgeSeqTableName() + "_rel" }

// GraphIndexSource returns the index name for source-side graph lookups.
func (t TenantID) GraphIndexSource() string { return "idx_" + t.GraphTableName() + "_src" }

// GraphIndexTarget returns the index name for target-side graph lookups.
func (t TenantID) GraphIndexTarget() string { return "idx_" + t.GraphTableName() + "_tgt" }

// GraphIndexRel returns the index name for relationship_name lookups.
func (t TenantID) GraphIndexRel() string { return "idx_" + t.GraphTableName() + "_rel" }

// AdaptedNodeIndexTenant returns the tenant index name on an adapted node table.
func (t TenantID) AdaptedNodeIndexTenant(entityType string) string {
	return "idx_" + t.AdaptedNodeTableName(entityType) + "_tenant"
}

// AdaptedNodeIndexField returns a field index name on an adapted node table.
func (t TenantID) AdaptedNodeIndexField(entityType, field string) string {
	return "idx_" + t.AdaptedNodeTableName(entityType) + "_" + field
}

// AdaptedEdgeIndexField returns a field index name on an adapted edge table.
func (t TenantID) AdaptedEdgeIndexField(relType, field string) string {
	return "idx_" + t.AdaptedEdgeTableName(relType) + "_" + field
}

// NodeIDPrefix extracts the XXXX@ tenant prefix from a graph node ID,
// returning "" if the node ID carries no prefix.
//
// Only uppercase hex digits (0-9, A-F) are recognised, matching the output
// of GraphNodePrefix. Lowercase hex will cause this function to return "".
// This intentional strictness prevents ambiguous or malformed prefixes from
// being silently accepted.
func NodeIDPrefix(nodeID string) string {
	if len(nodeID) < 5 || nodeID[4] != '@' {
		return ""
	}
	for i := 0; i < 4; i++ {
		c := nodeID[i]
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'F')) {
			return ""
		}
	}
	return nodeID[:5]
}

// NodeIDStripped returns nodeID with its XXXX@ tenant prefix removed, if any.
// For bare node IDs (no prefix) the input is returned unchanged.
// Intended for use in error messages that must not leak internal prefixes.
func NodeIDStripped(nodeID string) string {
	if p := NodeIDPrefix(nodeID); p != "" {
		return nodeID[len(p):]
	}
	return nodeID
}

// ---------------------------------------------------------------------------
// SQLite table naming — per-tenant table family
// ---------------------------------------------------------------------------
//
// All per-tenant tables follow the prefix convention:
//
//   t<XXXX>_<family>           — single-table families
//   t<XXXX>_<family>_<name>   — multi-table families (adapted tables)
//
// where XXXX is the zero-padded uppercase hex tenant ID.
//
// Full table family for tenant 0001:
//
//   t0001_graph           — topology (source, target, relationship, edge_id)
//   t0001_nodes           — blob node store (formerly: entities)
//   t0001_edges           — blob edge property store
//   t0001_n_sch           — node schema + adaptation registry
//   t0001_e_sch           — edge schema + adaptation registry
//   t0001_ndata_user      — adapted node entity (native columns)
//   t0001_edata_KNOWS     — adapted edge label (native columns)
//
// Tenant 0 (unscoped) uses the same naming: t0000_nodes, t0000_graph, etc.
// This keeps all storage tables in one namespace regardless of mode.

// ---------------------------------------------------------------------------
// Global (non-tenant-scoped) table names
// ---------------------------------------------------------------------------
//
// These tables are created once per database file and are not scoped to any
// tenant. They do not follow the t<XXXX>_ convention because they are owned
// by the database itself, not by any tenant.

const (
	// TenantsTable is the tenant registry table. One per database file.
	TenantsTable = "tenants"

	// SchemaVersionTable tracks applied migrations. One per database file.
	SchemaVersionTable = "schema_version"
)

// ---------------------------------------------------------------------------
// Persistence interface
// ---------------------------------------------------------------------------

// Persister stores and retrieves tenant name-to-ID mappings durably.
// Implementations must be safe for concurrent use.
type Persister interface {
	// LoadAll returns all persisted tenant mappings.
	LoadAll(ctx context.Context) (map[string]TenantID, error)
	// Save persists a single tenant mapping. It must be idempotent:
	// saving an already-persisted (name, id) pair is not an error.
	Save(ctx context.Context, name string, id TenantID) error
}

// ---------------------------------------------------------------------------
// Tenant registry
// ---------------------------------------------------------------------------

// Registry maps human-readable tenant names (e.g. "acme") to TenantID
// values — the sole store of tenant identity assignment, and thus the
// sole place new TenantID values are minted.
// It is safe for concurrent use. When a Persister is attached, all mutations
// are durably stored, ensuring stable name-to-ID mappings across restarts.
type Registry struct {
	mu        sync.RWMutex
	byName    map[string]TenantID
	byID      map[TenantID]string
	nextAuto  TenantID // for auto-assignment; starts at 1
	persister Persister
}

// NewRegistry creates an empty tenant registry with no persistence.
// Mappings will be lost on restart. Use SetPersister or LoadFrom to
// attach durable storage.
func NewRegistry() *Registry {
	return &Registry{
		byName:   make(map[string]TenantID),
		byID:     make(map[TenantID]string),
		nextAuto: 1,
	}
}

// SetPersister attaches a persistence backend to the registry.
// Must be called before any Register/GetOrRegister calls.
func (r *Registry) SetPersister(p Persister) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.persister = p
}

// LoadFrom loads all tenant mappings from the attached persister into
// the in-memory registry. Existing in-memory mappings are preserved;
// conflicts (same name with different ID) return an error.
// This should be called once at startup, after SetPersister.
func (r *Registry) LoadFrom(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.persister == nil {
		return nil // no persister, nothing to load
	}

	mappings, err := r.persister.LoadAll(ctx)
	if err != nil {
		return fmt.Errorf("load tenant registry: %w", err)
	}

	for name, id := range mappings {
		if id.IsZero() {
			continue // skip reserved ID
		}
		if existing, ok := r.byName[name]; ok && existing != id {
			return fmt.Errorf("tenant %q: persisted ID %d conflicts with in-memory ID %d", name, id, existing)
		}
		if existing, ok := r.byID[id]; ok && existing != name {
			return fmt.Errorf("tenant ID %d: persisted name %q conflicts with in-memory name %q", id, name, existing)
		}
		r.byName[name] = id
		r.byID[id] = name
		if id >= r.nextAuto && id < 65535 {
			r.nextAuto = id + 1
		}
	}

	return nil
}

// Register adds a tenant with an explicit ID.
// Returns an error if the name or ID is already registered.
// If a persister is attached, the mapping is durably stored.
func (r *Registry) Register(ctx context.Context, name string, id TenantID) error {
	if id.IsZero() {
		return fmt.Errorf("tenant ID 0 is reserved for unscoped operation")
	}
	if name == "" {
		return fmt.Errorf("tenant name must not be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Guard against exceeding the usable ID range (1..65535).
	if len(r.byName) >= 65535 {
		return fmt.Errorf("tenant registry full: maximum 65535 tenants reached")
	}

	if existing, ok := r.byName[name]; ok {
		if existing == id {
			return nil // idempotent: same mapping already exists
		}
		return fmt.Errorf("tenant name %q already registered with ID %d", name, existing)
	}
	if existing, ok := r.byID[id]; ok {
		return fmt.Errorf("tenant ID %d already registered as %q", id, existing)
	}

	// Persist before committing to memory
	if r.persister != nil {
		if err := r.persister.Save(ctx, name, id); err != nil {
			return fmt.Errorf("persist tenant %q (ID %d): %w", name, id, err)
		}
	}

	r.byName[name] = id
	r.byID[id] = name

	// Keep nextAuto above the highest registered ID, but cap at 65535
	// to avoid uint16 wrap-around.
	if id >= r.nextAuto && id < 65535 {
		r.nextAuto = id + 1
	}

	return nil
}

// Lookup returns the tenant ID for a name, or 0 and false if not found.
func (r *Registry) Lookup(name string) (TenantID, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byName[name]
	return id, ok
}

// GetOrRegister returns the tenant ID for a name, auto-registering with
// the next available ID if the name is not yet known. This is intended for
// non-strict tenant modes where tenants are created on first access.
// If a persister is attached, new mappings are durably stored.
func (r *Registry) GetOrRegister(ctx context.Context, name string) (TenantID, error) {
	// Fast path: read lock
	r.mu.RLock()
	if id, ok := r.byName[name]; ok {
		r.mu.RUnlock()
		return id, nil
	}
	r.mu.RUnlock()

	// Slow path: write lock, double-check, register
	r.mu.Lock()
	defer r.mu.Unlock()

	if id, ok := r.byName[name]; ok {
		return id, nil
	}

	if name == "" {
		return 0, fmt.Errorf("tenant name must not be empty")
	}

	// Guard against uint16 overflow. ID 0 is reserved, so the usable
	// range is 1..65535 — at most 65535 tenants.
	if len(r.byName) >= 65535 {
		return 0, fmt.Errorf("tenant registry full: maximum 65535 tenants reached")
	}

	id := r.nextAuto
	if id.IsZero() {
		id = 1 // skip reserved 0
	}
	// Skip past IDs that were explicitly registered via Register().
	// This prevents collisions when auto-assign and explicit-assign coexist.
	for {
		if _, taken := r.byID[id]; !taken {
			break
		}
		if id == 65535 {
			return 0, fmt.Errorf("tenant registry full: no available auto-assign IDs")
		}
		id++
	}

	// Persist before committing to memory
	if r.persister != nil {
		if err := r.persister.Save(ctx, name, id); err != nil {
			return 0, fmt.Errorf("persist tenant %q (ID %d): %w", name, id, err)
		}
	}

	r.byName[name] = id
	r.byID[id] = name
	if id < 65535 {
		r.nextAuto = id + 1
	}

	return id, nil
}

// Name returns the tenant name for an ID, or "" and false if not found.
func (r *Registry) Name(id TenantID) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	name, ok := r.byID[id]
	return name, ok
}

// List returns all registered tenant name-ID pairs.
func (r *Registry) List() map[string]TenantID {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]TenantID, len(r.byName))
	for k, v := range r.byName {
		result[k] = v
	}
	return result
}

// Count returns the number of registered tenants.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byName)
}

// ---------------------------------------------------------------------------
// Element kinds
// ---------------------------------------------------------------------------
//
// ElementKind identifies the fundamental kind of a graph element. Both nodes
// and edges implement the Element interface; Vode is a third kind representing
// a forward-reference placeholder node created implicitly by AddEdge when the
// target entity has not yet been written.

// ElementKind is the fundamental kind of a graph element.
type ElementKind uint8

const (
	// ElementNode is a real node with a confirmed entity type and property store.
	// Properties live in t<X>_nodes (blob) or t<X>_ndata_<label> (adapted).
	ElementNode ElementKind = iota

	// ElementEdge is a directed relationship between two nodes.
	// Properties live in t<X>_edges (blob) or t<X>_edata_<label> (adapted).
	ElementEdge

	// ElementVode is a forward-reference placeholder node created implicitly
	// by AddEdge when the target entity has not yet been written. A Vode has
	// topology (it exists in t<X>_graph adjacency) but no property store entry.
	// Its label is always NodeTypeVode ("__vode__"). Vodes are promoted to real
	// nodes when the entity data arrives; a non-zero Vode count at the end of
	// hydration indicates dangling references.
	//
	// Hydration must short-circuit for Vodes: there is no property row to fetch.
	// Query results that include Vodes should be excluded or flagged rather than
	// returned with empty property maps.
	ElementVode
)

// String returns a human-readable name for the ElementKind.
func (k ElementKind) String() string {
	switch k {
	case ElementNode:
		return "node"
	case ElementEdge:
		return "edge"
	case ElementVode:
		return "vode"
	default:
		return "unknown"
	}
}
