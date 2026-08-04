// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ha1tch/xolu/pkg/tenant"
)

var (
	// ErrNotFound is returned when an entity is not found
	ErrNotFound = errors.New("entity not found")
	// ErrAlreadyExists is returned when an entity already exists
	ErrAlreadyExists = errors.New("entity already exists")
	// ErrInvalidEntity is returned when entity name is invalid
	ErrInvalidEntity = errors.New("invalid entity name")
	// ErrInvalidID is returned when ID is invalid
	ErrInvalidID = errors.New("invalid ID")
	// ErrConflict is returned when an optimistic concurrency check fails
	ErrConflict = errors.New("version conflict")
	// ErrNotSupported is returned when a storage backend does not implement
	// a particular operation. Handlers that receive this error should map it
	// to an appropriate HTTP 501 Not Implemented response.
	ErrNotSupported = errors.New("operation not supported by this storage backend")
)

// StoreConfig is the canonical configuration for all store backends.
// A store is constructed with a StoreConfig and scoped to that config
// for its entire lifetime. TenantID 0 means no tenant scoping.
type StoreConfig struct {
	Type            string // "sqlite"
	BaseDir         string // data root; layout paths are derived from this via pkg/storelayout
	DBPath          string // resolved SQLite database file path (derived from BaseDir by the caller)
	FullTextEnabled bool   // controls FTS indexing in backend
	GraphEnabled    bool   // controls graph edge table maintenance
	TenantID        tenant.TenantID // 0 = no tenant scoping

	// Performance tuning (SQLite-specific; zero = use defaults)
	SQLiteCacheSize           int // Page cache size in KB
	SQLiteBusyTimeout         int // Milliseconds to wait on locked database
	SQLiteMaxOpenConns        int // Max open database connections
	SQLiteMaxIdleConns        int // Max idle database connections
	SQLiteReadPoolSize        int // Max open read connections (0 = auto)
	SQLiteContentionThreshold int // Adaptive lock threshold 0-100

	// SQLitePerFileTenants mirrors config.Config.SQLitePerFileTenants.
	// When true, each tenant gets its own SQLite database file. The flag
	// governs storeForTenant file routing in server.go and is unrelated to
	// the schema DDL (which uses t<XXXX>_* table names regardless of mode).
	SQLitePerFileTenants bool
}

// TenantModeProvider is implemented by storage backends that support
// per-file tenant isolation. The OQL layer uses this to decide whether
// to inject tenant_id scoping into pushed-down SQL queries.
type TenantModeProvider interface {
	IsPerFileTenant() bool
}

// Store defines the core interface for entity storage backends
type Store interface {
	// Config returns the store's configuration.
	Config() StoreConfig

	// Entity CRUD operations
	Create(ctx context.Context, entity string, data map[string]interface{}) (int, error)
	Get(ctx context.Context, entity string, id int) (map[string]interface{}, error)
	// GetMany fetches multiple entities of the same type in a single query.
	// Returns a map from id → data for each id that was found; ids that do not
	// exist are absent from the result (no error). The caller must not assume
	// the result map contains all requested ids.
	GetMany(ctx context.Context, entity string, ids []int) (map[int]map[string]interface{}, error)
	Update(ctx context.Context, entity string, id int, data map[string]interface{}) error
	Patch(ctx context.Context, entity string, id int, data map[string]interface{}) error
	// PatchValidated is like Patch but runs a validation function against the
	// merged data inside the transaction. If the validator returns an error,
	// the transaction is rolled back and the error is returned to the caller.
	// This avoids TOCTOU races where a Get-merge-Update sequence can observe
	// stale data between the Get and the Update.
	PatchValidated(ctx context.Context, entity string, id int, data map[string]interface{}, validate func(merged map[string]interface{}) error) error
	Delete(ctx context.Context, entity string, id int) error
	// Save upserts an entity with the caller-specified ID: creates it if it
	// does not exist, overwrites it if it does. Returns (true, nil) when a
	// new record was created and (false, nil) when an existing record was
	// replaced. Never returns an error solely because the ID already exists.
	Save(ctx context.Context, entity string, id int, data map[string]interface{}) (bool, error)

	// Commit performs an atomic upsert + one or more inserts in a single
	// storage transaction. The upsert (req.Update) supports optional
	// optimistic concurrency via Version. Each entry in req.Append is an
	// unconditional insert; a duplicate explicit ID returns ErrAlreadyExists
	// and rolls back the entire commit. Returns ErrConflict when the Update
	// version check fails.
	Commit(ctx context.Context, req CommitRequest) (CommitResult, error)

	// Query operations
	List(ctx context.Context, entity string) ([]map[string]interface{}, error)
	Exists(ctx context.Context, entity string, id int) bool
	Search(ctx context.Context, entity string, field string, query string, matchType string) ([]map[string]interface{}, error)

	// Full-text search (optional - may return empty if not supported)
	FullTextSearch(ctx context.Context, query string, entity string) ([]map[string]interface{}, error)

	// Ping verifies that the storage backend is reachable. Returns nil on
	// success. Used by health and readiness probes.
	Ping(ctx context.Context) error

	// Lifecycle
	Close() error
}

// CommitRequest is the payload for the atomic commit endpoint.
// It performs one conditional upsert (Update), zero or more unconditional
// entity inserts (Append), and zero or more timeseries events (Timeseries).
// At least one of Append, Timeseries, or FsmWalk must be non-empty.
//
// When Timeseries is non-empty the server writes those events to the Pebble
// timeseries store BEFORE opening the SQLite transaction. If the Pebble write
// succeeds but the SQLite transaction subsequently fails, the server issues a
// synchronous DeleteKeys call to tombstone the written events before returning
// the error to the caller. See docs/COMMIT_ENDPOINT.md.
//
// FsmWalk is a v2 field. When API v2 is disabled, it is accepted in the
// request body (JSON unmarshalling ignores unknown fields) but is never
// acted upon — the server treats a request with only FsmWalk set the same
// as a request with all fields empty, returning XOLU-CM003. This ensures
// v1-only deployments that accidentally receive a v2 request body fail
// cleanly rather than silently discarding the walk.
type CommitRequest struct {
	Update     CommitUpdate    `json:"update"`
	Append     []CommitAppend  `json:"append"`
	Timeseries []CommitTSEvent `json:"timeseries,omitempty"`
	// FsmWalk is set when a state machine walk must be atomic with the
	// entity write. Populated only when API v2 is enabled. Nil otherwise.
	FsmWalk *CommitFsmWalk `json:"fsm_walk,omitempty"`
}

// CommitTSEvent is one timeseries event carried inside a CommitRequest.
// It maps directly onto timeseries.Event; the Timeline must already be defined
// for the tenant via POST /ts/timelines before /commit is called.
type CommitTSEvent struct {
	Timeline int64     `json:"timeline"`
	Dims     []uint64  `json:"dims"`
	Time     time.Time `json:"time"`
	Nums     []float64 `json:"nums,omitempty"`
	Payload  []byte    `json:"payload,omitempty"`
}

// CommitUpdate describes the entity to upsert in a Commit operation.
// If Version is non-nil, the write is conditional: it proceeds only if the
// stored _version equals *Version. A mismatch returns ErrConflict.
type CommitUpdate struct {
	Entity  string                 `json:"entity"`
	ID      int                    `json:"id"`
	Version *int                   `json:"version,omitempty"`
	Data    map[string]interface{} `json:"data"`
}

// CommitAppend describes one record to insert in a Commit operation.
// If ID is nil, the backend auto-generates an ID. If ID is non-nil and a
// record with that ID already exists in the entity type, ErrAlreadyExists
// is returned and the entire commit is rolled back.
type CommitAppend struct {
	Entity string                 `json:"entity"`
	ID     *int                   `json:"id,omitempty"`
	Data   map[string]interface{} `json:"data"`
}

// CommitFsmWalk describes an FSM walk to execute atomically with the commit.
// This is a v2 type; it is defined here so that CommitRequest can carry it
// without a circular import. The walk is executed by the server's v2 FSM
// handler when API v2 is enabled; when v2 is disabled, the field is ignored
// and the request is rejected if no append or timeseries work is present.
type CommitFsmWalk struct {
	Machine int                    `json:"machine"`
	Input   string                 `json:"input"`
	Payload map[string]interface{} `json:"payload,omitempty"`
}

// CommitResult is returned on a successful Commit.
type CommitResult struct {
	Update     CommitUpdateResult   `json:"update"`
	Appended   []CommitAppendResult `json:"appended"`
	TSAccepted int                  `json:"ts_accepted,omitempty"`
	// FsmWalk is populated when the commit included an fsm_walk field and
	// API v2 is enabled. Nil otherwise.
	FsmWalk *CommitFsmWalkResult `json:"fsm_walk,omitempty"`
}

// CommitFsmWalkResult describes the outcome of an FSM walk executed
// atomically within a commit. This is a v2 type.
type CommitFsmWalkResult struct {
	Machine    int                    `json:"machine"`
	Previous   string                 `json:"previous"`
	Current    string                 `json:"current"`
	Terminal   bool                   `json:"terminal"`
	Outputs    []string               `json:"outputs,omitempty"`
	Vars       map[string]interface{} `json:"vars,omitempty"`
	Definition interface{}            `json:"-"`
}

// CommitUpdateResult describes the outcome of the upsert in a Commit.
// Created is true when a new record was inserted; false when an existing
// record was overwritten. Version is the _version value after the commit.
type CommitUpdateResult struct {
	Entity  string `json:"entity"`
	ID      int    `json:"id"`
	Created bool   `json:"created"`
	Version int    `json:"version"`
}

// CommitAppendResult describes one inserted record in a Commit response.
// ID is always set; for auto-generated IDs it contains the assigned value.
type CommitAppendResult struct {
	Entity string `json:"entity"`
	ID     int    `json:"id"`
}

// IDGenerator defines interface for ID generation strategies
type IDGenerator interface {
	NextID(ctx context.Context, entity string) (int, error)
}

// Migrator defines optional schema migration support
// Useful for database backends
type Migrator interface {
	Migrate(ctx context.Context) error
	Version(ctx context.Context) (int, error)
}

// Searcher defines optional search capabilities
type Searcher interface {
	Search(ctx context.Context, entity string, field string, query string, matchType string) ([]map[string]interface{}, error)
}

// Batcher defines optional batch operation support
type Batcher interface {
	BatchCreate(ctx context.Context, entity string, items []map[string]interface{}) ([]int, error)
	BatchDelete(ctx context.Context, entity string, ids []int) error
}

// GraphNeighbors defines optional graph neighbor queries
type GraphNeighbors interface {
}

// GraphIntegrity defines optional graph integrity checking
type GraphIntegrity interface {
	VerifyGraphIntegrity(ctx context.Context) error
	RebuildGraph(ctx context.Context) error
}

// EdgePropertyStore is an optional interface implemented by backends that
// support edge property storage. When the store implements this interface,
// callers can write and retrieve property blobs associated with a specific
// edge (identified by the surrogate edge ID in t<X>_eseq).
//
// Backends that do not implement this interface (e.g. test stubs) return
// ErrNotFound for all GetEdge calls; callers must handle this gracefully.
type EdgePropertyStore interface {
	// AddEdgeWithProps persists edge topology and writes a property blob if
	// props is non-nil and non-empty. Returns the assigned surrogate edge ID
	// (0 if no property row was written) and any error.
	AddEdgeWithProps(ctx context.Context, from, to, relationship string, props map[string]interface{}) (edgeID int, err error)

	// GetEdge retrieves the property blob for a single edge identified by its
	// surrogate edge ID. Returns ErrNotFound if the edge has no property row.
	GetEdge(ctx context.Context, edgeID int) (*EdgePropsResult, error)

	// GetManyEdges retrieves property blobs for multiple edges in a single
	// query. Edge IDs with no property row are absent from the result map;
	// they are not errors. The caller must not assume the result map contains
	// all requested IDs.
	GetManyEdges(ctx context.Context, edgeIDs []int) (map[int]*EdgePropsResult, error)
}

// StoreInfo provides metadata about the store implementation
type StoreInfo struct {
	Type                string // "sqlite"
	Version             string
	SupportsSearch      bool
	SupportsBatch       bool
	SupportsTransaction bool
}

// InfoProvider allows stores to provide metadata about their capabilities
type InfoProvider interface {
	Info() StoreInfo
}

// EntityLister defines optional entity type listing support
type EntityLister interface {
	ListEntities(ctx context.Context) ([]string, error)
}

// TableNamer provides the tenant-scoped table name for the blob node store.
// The OQL SQL generator uses this to build correct push-down queries without
// hardcoding "entities". Implementations return tenant.NodesTableName(tenantID).
type TableNamer interface {
	NodesTable() string
}

// PagedResult holds a page of results plus the total count.
type PagedResult struct {
	Data       []map[string]interface{}
	TotalItems int
}

// PagedLister is an optional interface for storage backends that support
// server-side pagination. Backends that implement this avoid loading every
// record into memory for paginated list requests.
type PagedLister interface {
	// ListPaged returns a single page of entities, plus the total count.
	// limit and offset are applied at the storage layer (SQL LIMIT/OFFSET).
	ListPaged(ctx context.Context, entity string, limit, offset int) (*PagedResult, error)
}

// GraphEdge holds the columns of one row from the graph edges table.
// EdgeID is 0 when the edge has no property row.
type GraphEdge struct {
	SourceEntity string
	SourceID     int
	TargetEntity string
	TargetID     int
	Relationship string
	EdgeID       int
}

// EdgePropsResult is the result of a GetEdge call.
// Properties contains the JSON property map stored for the edge; it is nil
// when the edge has no property row (EdgeID == 0 in the topology table).
type EdgePropsResult struct {
	EdgeID     int
	Rel        string
	Properties map[string]interface{}
}

// EdgeSchemaStore is an optional interface implemented by backends that
// support the per-tenant edge schema registry (t<X>_e_sch).
//
// Registering a schema for an edge label suppresses the warn-once log that
// fires when AddEdgeWithProps is called for an unregistered label, and is a
// prerequisite for Stage 7 (adapted edge tables). Labels used only for
// topology (plain AddEdge, no properties) never trigger the warning and do
// not need registration.
type EdgeSchemaStore interface {
	// RegisterEdgeSchema persists a JSON Schema for the given relationship
	// label in t<X>_e_sch and suppresses the unregistered-label warning for
	// that label. Idempotent when called with the same schema hash.
	RegisterEdgeSchema(ctx context.Context, rel string, schema map[string]interface{}) error

	// SuppressEdgeSchemaWarning silences the unregistered-label warning for
	// rel without registering a schema. Useful when the caller intentionally
	// uses a schemaless label with properties and does not want log noise.
	// The suppression is in-memory only and resets on restart.
	SuppressEdgeSchemaWarning(rel string)

	// IsEdgeSchemaRegistered reports whether rel has a persisted schema entry
	// in t<X>_e_sch.
	IsEdgeSchemaRegistered(ctx context.Context, rel string) (bool, error)
}

// EdgeFTSStore is an optional interface implemented by backends that support
// full-text search over edge property content via t<X>_efts.
type EdgeFTSStore interface {
	// IndexEdgeContent extracts searchable text from props and writes a row
	// to t<X>_efts keyed by (rel, edgeID). Idempotent — updates on conflict.
	IndexEdgeContent(ctx context.Context, rel string, edgeID int, props map[string]interface{}) error

	// SearchEdges executes a full-text search against t<X>_efts and returns
	// matching (rel, edgeID) pairs in BM25 rank order. limit ≤ 0 returns all.
	SearchEdges(ctx context.Context, query string, limit int) ([]EdgeFTSResult, error)
}

// EdgeFTSResult is one row returned by SearchEdges.
type EdgeFTSResult struct {
	Rel    string // relationship label
	EdgeID int    // surrogate edge ID (0 for topology-only edges)
}

// EdgeLister is an optional interface for storage backends that can list all
// edge property rows for a given relationship label.
//
// SELECT * FROM KNOWS in OQL routes here when KNOWS is a blob edge label.
// For adapted edge labels (t<X>_edata_KNOWS), the existing adapted.Get()
// path in List() already handles it without this interface.
type EdgeLister interface {
	// ListEdges returns all property rows for rel from t<X>_edges (blob path).
	// Each row includes edge_id, rel, and all properties from the JSON blob.
	ListEdges(ctx context.Context, rel string) ([]map[string]interface{}, error)

	// IsEdgeLabel reports whether rel is a registered edge label (adapted or
	// blob) rather than a node entity type. Used by the OQL executor to route
	// FROM <label> queries correctly.
	IsEdgeLabel(ctx context.Context, rel string) (bool, error)

	// ResolveEdgeRelName returns the canonical (registry-cased) relationship
	// label for rel. OQL normalises entity names to lowercase; this method
	// restores the original casing so adapted.Get() and table queries work.
	// Returns rel unchanged when no canonical name is found.
	ResolveEdgeRelName(ctx context.Context, rel string) string
}

// enumerate all tenant IDs for which a graph_tXXXX edge table exists. The
// returned slice must always include tenant 0 (the implicit default). Used
// during startup graph hydration to restore graph state for all tenants.
//
// Backends that do not implement this interface fall back to scanning only
// tenant 0 via a direct ScanGraphEdges call.
type TenantIDLister interface {
	GraphTenantIDs(ctx context.Context) ([]tenant.TenantID, error)
}

// GraphEdgeScanner is an optional interface for storage backends that can
// stream graph edges directly from their edge table without deserialising full
// entity JSON. Implementing this interface enables O(edges) startup graph
// hydration instead of O(entities × JSON size).
//
// ScanGraphEdges calls fn once per edge row. Iteration stops on the first
// non-nil error returned by fn. A nil error from ScanGraphEdges means all
// rows were scanned (or fn stopped iteration early with a sentinel — callers
// must define their own sentinel if needed).
//
// tenantID scopes the scan to a specific tenant's edge table. Pass 0 for the
// default (tenant-0) table. Future SQL backends may extend this to scan all
// tenant tables in a single call; the current SQLite implementation scans one
// tenant at a time, matching the existing startup scope.
type GraphEdgeScanner interface {
	ScanGraphEdges(ctx context.Context, tenantID tenant.TenantID, fn func(GraphEdge) error) error
}

// WriterDBProvider gives access to the underlying write connection pool.
// Used by v2 handlers that need direct SQL access to global tables like
// entity_meta which are not modelled in the Store interface.
type WriterDBProvider interface {
	WriterDB() *sql.DB
}

// FsmWalker is implemented by stores that can execute an FSM walk on a
// caller-supplied transaction. It lets the server run the standalone
// POST /fsm/machine/{id}/walk through the same code path as the
// commit-embedded walk, without depending on the concrete store type.
type FsmWalker interface {
	FsmWalkInTx(ctx context.Context, tx *sql.Tx, tenantID tenant.TenantID,
		machineID int64, input string, payload map[string]interface{},
		queryBindings map[string]interface{}) (*FsmWalkResult, error)
}

// V2SchemaInitialiser is implemented by storage backends that support the
// API v2 schema. The server calls InitV2Schema once on startup when
// XOLU_API_V2_ENABLED is true, before registering any v2 routes.
// The call is idempotent; stores that do not implement this interface
// cause v2 initialisation to be skipped with a warning.
type V2SchemaInitialiser interface {
	InitV2Schema(ctx context.Context) error
}
