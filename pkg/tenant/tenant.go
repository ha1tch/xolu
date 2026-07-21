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

// ---------------------------------------------------------------------------
// Node ID and cache key helpers
// ---------------------------------------------------------------------------

// NodeID returns a graph node identifier scoped to a tenant.
// For tenant 0 (unscoped): "entity:id"
// For non-zero tenants:    "XXXX@entity:id"
func NodeID(tenantID uint16, entity string, id int) string {
	if tenantID == 0 {
		return fmt.Sprintf("%s:%d", entity, id)
	}
	return fmt.Sprintf("%04X@%s:%d", tenantID, entity, id)
}

// GraphNodePrefix returns the XXXX@ prefix used to namespace graph node IDs
// in a shared in-memory graph that spans multiple tenants.
// For tenant 0 (unscoped): "" (no prefix; node IDs are bare "entity:id")
// For non-zero tenants:    "XXXX@" (e.g. "0001@")
//
// Uses uppercase hex. NodeIDPrefix is the corresponding parser; both must
// agree on case. If this format ever changes, NodeIDPrefix must be updated.
func GraphNodePrefix(tenantID uint16) string {
	if tenantID == 0 {
		return ""
	}
	return fmt.Sprintf("%04X@", tenantID)
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

// StorageDirSegment returns the directory name used for tenant-scoped file
// storage (timeseries data, JSON file store). The segment is intended to be
// joined with a base directory using filepath.Join.
// For tenant 0 (unscoped): "" (data lives directly in the base directory)
// For non-zero tenants:    "tXXXX" (e.g. "t0001")
//
// Uses uppercase hex, consistent with GraphNodePrefix.
func StorageDirSegment(tenantID uint16) string {
	if tenantID == 0 {
		return ""
	}
	return fmt.Sprintf("t%04X", tenantID)
}

// ScopeKey prepends the tenant's hex ID to an arbitrary cache or lookup key,
// producing a tenant-scoped key of the form "XXXX:key".
// For tenant 0 (unscoped): the key is returned unchanged.
// For non-zero tenants:    "XXXX:key" (e.g. "0001:post:list:1:10")
//
// This is the generic scoping primitive; use CacheKey, CachePattern, etc.
// for the specialised cache key formats.
func ScopeKey(tenantID uint16, key string) string {
	if tenantID == 0 {
		return key
	}
	return fmt.Sprintf("%04X:%s", tenantID, key)
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

func tablePrefix(tenantID uint16) string {
	return fmt.Sprintf("t%04X", tenantID)
}

// TablePrefix is the exported per-tenant table-name prefix WITH the
// trailing underscore ("t0000_"), for primitives that own their own
// table families (bal). Uses the same encoding as every other tenant
// table name.
func TablePrefix(tenantID uint16) string {
	return tablePrefix(tenantID) + "_"
}

// GraphTableName returns the topology table name for a tenant.
// Stores directed edges as (source_entity, source_id, target_entity, target_id,
// relationship_name, edge_id).
// Example: t0001_graph
func GraphTableName(tenantID uint16) string {
	return tablePrefix(tenantID) + "_graph"
}

// GraphEdgesTableName is the legacy name for GraphTableName.
// Deprecated: use GraphTableName. Retained for the migration command only.
func GraphEdgesTableName(tenantID uint16) string {
	return GraphTableName(tenantID)
}

// NodesTableName returns the blob node store table name for a tenant.
// Replaces the global shared-mode `entities` table.
// Example: t0001_nodes
func NodesTableName(tenantID uint16) string {
	return tablePrefix(tenantID) + "_nodes"
}

// NodeSeqTableName returns the node ID sequence table name for a tenant.
// Replaces the global shared-mode `entity_sequences` table.
// Example: t0001_nseq
func NodeSeqTableName(tenantID uint16) string {
	return tablePrefix(tenantID) + "_nseq"
}

// NodeFTSTableName returns the node full-text search virtual table name for a tenant.
// Replaces the global shared-mode `entities_fts` virtual table.
// Example: t0001_nfts
func NodeFTSTableName(tenantID uint16) string {
	return tablePrefix(tenantID) + "_nfts"
}

// EdgePropsTableName returns the blob edge property store table name for a tenant.
// Stores JSON property blobs for edges whose label has no registered schema.
// Example: t0001_edges
func EdgePropsTableName(tenantID uint16) string {
	return tablePrefix(tenantID) + "_edges"
}

// EdgeSeqTableName returns the edge ID sequence table name for a tenant.
// Provides explicit control over surrogate edge ID assignment, consistent
// with NodeSeqTableName. One row per relationship label per tenant.
// Example: t0001_eseq
func EdgeSeqTableName(tenantID uint16) string {
	return tablePrefix(tenantID) + "_eseq"
}

// NodeSchemaTableName returns the node schema registry table name for a tenant.
// Stores both the raw JSON schema and the derived adapted-table column spec.
// Replaces the global `schemas` and `adapted_table_schemas` tables.
// Example: t0001_n_sch
func NodeSchemaTableName(tenantID uint16) string {
	return tablePrefix(tenantID) + "_n_sch"
}

// EdgeSchemaTableName returns the edge schema registry table name for a tenant.
// Stores the raw JSON schema, derived column spec, and warning-suppression flag
// for each relationship label.
// Example: t0001_e_sch
func EdgeSchemaTableName(tenantID uint16) string {
	return tablePrefix(tenantID) + "_e_sch"
}

// AdaptedNodeTableName returns the adapted native-column table name for a
// schema-registered node entity type.
// Example: t0001_ndata_user, t0001_ndata_user_profile
func AdaptedNodeTableName(tenantID uint16, entityType string) string {
	return tablePrefix(tenantID) + "_ndata_" + entityType
}

// AdaptedEdgeTableName returns the adapted native-column table name for a
// schema-registered edge label.
// Example: t0001_edata_KNOWS, t0001_edata_MEMBER_OF
func AdaptedEdgeTableName(tenantID uint16, relType string) string {
	return tablePrefix(tenantID) + "_edata_" + relType
}

// ---------------------------------------------------------------------------
// Index naming — per-tenant indexes on per-tenant tables
// ---------------------------------------------------------------------------
//
// SQLite index names are global within a database file. In shared-file mode
// where t0000_nodes and t0001_nodes coexist, every index must encode both
// the table name (which already encodes the tenant) to avoid collisions.
// The convention is: idx_<tableName>_<purpose>
//
// These functions derive index names from the table name functions above,
// so the index naming is always consistent with the table naming.

// NodesIndexEntityType returns the index name for entity_type lookups on t<X>_nodes.
// Example: idx_t0001_nodes_etype
func NodesIndexEntityType(tenantID uint16) string {
	return "idx_" + NodesTableName(tenantID) + "_etype"
}

// NodesIndexUpdatedAt returns the index name for updated_at ordering on t<X>_nodes.
// Example: idx_t0001_nodes_updated
func NodesIndexUpdatedAt(tenantID uint16) string {
	return "idx_" + NodesTableName(tenantID) + "_updated"
}

// NodeSeqIndexEntityType returns the index on t<X>_nseq (the PK already covers this,
// but an explicit name is needed for migration assertions).
// Example: idx_t0001_nseq_etype
func NodeSeqIndexEntityType(tenantID uint16) string {
	return "idx_" + NodeSeqTableName(tenantID) + "_etype"
}

// EdgeSeqIndexRelType returns the index on t<X>_eseq.
// Example: idx_t0001_eseq_rel
func EdgeSeqIndexRelType(tenantID uint16) string {
	return "idx_" + EdgeSeqTableName(tenantID) + "_rel"
}

// GraphIndexSource returns the index name for source-side lookups on t<X>_graph.
// Example: idx_t0001_graph_src
func GraphIndexSource(tenantID uint16) string {
	return "idx_" + GraphTableName(tenantID) + "_src"
}

// GraphIndexTarget returns the index name for target-side lookups on t<X>_graph.
// Example: idx_t0001_graph_tgt
func GraphIndexTarget(tenantID uint16) string {
	return "idx_" + GraphTableName(tenantID) + "_tgt"
}

// GraphIndexRel returns the index name for relationship_name lookups on t<X>_graph.
// Example: idx_t0001_graph_rel
func GraphIndexRel(tenantID uint16) string {
	return "idx_" + GraphTableName(tenantID) + "_rel"
}

// AdaptedNodeIndexTenant returns the tenant index name on an adapted node table.
// Example: idx_t0001_ndata_user_tenant
func AdaptedNodeIndexTenant(tenantID uint16, entityType string) string {
	return "idx_" + AdaptedNodeTableName(tenantID, entityType) + "_tenant"
}

// AdaptedNodeIndexField returns a field index name on an adapted node table.
// Example: idx_t0001_ndata_user_email
func AdaptedNodeIndexField(tenantID uint16, entityType, field string) string {
	return "idx_" + AdaptedNodeTableName(tenantID, entityType) + "_" + field
}

// AdaptedEdgeIndexField returns a field index name on an adapted edge table.
// Example: idx_t0001_edata_KNOWS_since
func AdaptedEdgeIndexField(tenantID uint16, relType, field string) string {
	return "idx_" + AdaptedEdgeTableName(tenantID, relType) + "_" + field
}

// EdgeFTSTableName returns the edge full-text search virtual table name for
// a tenant. Used when edge properties carry free-text content that needs
// full-text search — e.g. a contract document on a MARRIED_TO relationship,
// a clinical note on a TREATS relationship, or a lease agreement on LEASES.
// Example: t0001_efts
func EdgeFTSTableName(tenantID uint16) string {
	return tablePrefix(tenantID) + "_efts"
}

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

// CacheKey returns a cache key scoped to a tenant.
// For tenant 0 (unscoped): "entity:id"
// For non-zero tenants:    "XXXX:entity:id"
func CacheKey(tenantID uint16, entity string, id int) string {
	if tenantID == 0 {
		return fmt.Sprintf("%s:%d", entity, id)
	}
	return fmt.Sprintf("%04X:%s:%d", tenantID, entity, id)
}

// CachePattern returns a pattern for cache invalidation scoped to a tenant.
// For tenant 0 (unscoped): "entity:*"
// For non-zero tenants:    "XXXX:entity:*"
func CachePattern(tenantID uint16, entity string) string {
	if tenantID == 0 {
		return fmt.Sprintf("%s:*", entity)
	}
	return fmt.Sprintf("%04X:%s:*", tenantID, entity)
}

// CacheTenantPattern returns a pattern matching all keys for a tenant.
// For tenant 0: "*" (everything)
// For non-zero: "XXXX:*"
func CacheTenantPattern(tenantID uint16) string {
	if tenantID == 0 {
		return "*"
	}
	return fmt.Sprintf("%04X:*", tenantID)
}

// CacheListPattern returns a pattern matching only list cache keys for an
// entity type, scoped to a tenant. This leaves individual GET cache entries
// intact, improving cache hit rate when a single entity is modified.
// For tenant 0 (unscoped): "entity:list:*"
// For non-zero tenants:    "XXXX:entity:list:*"
func CacheListPattern(tenantID uint16, entity string) string {
	if tenantID == 0 {
		return fmt.Sprintf("%s:list:*", entity)
	}
	return fmt.Sprintf("%04X:%s:list:*", tenantID, entity)
}

// ---------------------------------------------------------------------------
// Persistence interface
// ---------------------------------------------------------------------------

// Persister stores and retrieves tenant name-to-ID mappings durably.
// Implementations must be safe for concurrent use.
type Persister interface {
	// LoadAll returns all persisted tenant mappings.
	LoadAll(ctx context.Context) (map[string]uint16, error)
	// Save persists a single tenant mapping. It must be idempotent:
	// saving an already-persisted (name, id) pair is not an error.
	Save(ctx context.Context, name string, id uint16) error
}

// ---------------------------------------------------------------------------
// Tenant registry
// ---------------------------------------------------------------------------

// Registry maps human-readable tenant names (e.g. "acme") to uint16 IDs.
// It is safe for concurrent use. When a Persister is attached, all mutations
// are durably stored, ensuring stable name-to-ID mappings across restarts.
type Registry struct {
	mu        sync.RWMutex
	byName    map[string]uint16
	byID      map[uint16]string
	nextAuto  uint16 // for auto-assignment; starts at 1
	persister Persister
}

// NewRegistry creates an empty tenant registry with no persistence.
// Mappings will be lost on restart. Use SetPersister or LoadFrom to
// attach durable storage.
func NewRegistry() *Registry {
	return &Registry{
		byName:   make(map[string]uint16),
		byID:     make(map[uint16]string),
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
		if id == 0 {
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
func (r *Registry) Register(ctx context.Context, name string, id uint16) error {
	if id == 0 {
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
func (r *Registry) Lookup(name string) (uint16, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.byName[name]
	return id, ok
}

// GetOrRegister returns the tenant ID for a name, auto-registering with
// the next available ID if the name is not yet known. This is intended for
// non-strict tenant modes where tenants are created on first access.
// If a persister is attached, new mappings are durably stored.
func (r *Registry) GetOrRegister(ctx context.Context, name string) (uint16, error) {
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
	if id == 0 {
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
func (r *Registry) Name(id uint16) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	name, ok := r.byID[id]
	return name, ok
}

// List returns all registered tenant name-ID pairs.
func (r *Registry) List() map[string]uint16 {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]uint16, len(r.byName))
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
