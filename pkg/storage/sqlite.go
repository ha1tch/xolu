// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ha1tch/xolu/pkg/models"
	"github.com/ha1tch/xolu/pkg/tenant"
	"github.com/rs/zerolog"
	_ "modernc.org/sqlite" // Pure Go SQLite driver
)

// SQLiteStore implements Store interface using SQLite database.
//
// It maintains two connection pools against the same WAL-mode database:
//   - db (writer): MaxOpenConns=1, serialises all writes.
//   - readDB (reader): MaxOpenConns=NumCPU, query_only=ON, parallel reads.
//
// Under WAL mode, readers never block the writer and vice-versa.
type SQLiteStore struct {
	db          *sql.DB // writer pool (1 conn, serialised)
	readDB      *sql.DB // reader pool (N conns, parallel, query_only)
	dbPath      string
	config      SQLiteConfig
	storeConfig StoreConfig
	alock       *AdaptiveLock
	adapted     *AdaptedRegistry // nil-safe: Get() returns nil for unknown entity types
	dialect     StorageDialect   // backend-specific SQL generation
	stmtCache   *StmtCache       // prepared statement cache for reader pool
	logger      zerolog.Logger   // structured logger; zerolog.Nop() by default

	// edgeWarnSuppressed tracks relationship labels for which the
	// unregistered-schema warning has already fired (or been suppressed).
	// Keyed by rel label. Protected by edgeWarnMu.
	// Reset on restart — suppression is in-memory only.
	edgeWarnMu         sync.Mutex
	edgeWarnSuppressed map[string]bool
}

// DB returns the underlying *sql.DB for advanced operations such as
// batch seeding or direct SQL execution. Use with care — callers must
// respect the store's locking and schema conventions.
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

// WriterDB implements storage.WriterDBProvider.
func (s *SQLiteStore) WriterDB() *sql.DB {
	return s.db
}

// ReaderDB returns the underlying reader connection pool.
// Used by the tenant persister for read-only queries.
func (s *SQLiteStore) ReaderDB() *sql.DB {
	return s.readDB
}

// ContentiontLock returns the store's adaptive lock, allowing runtime
// configuration of the contention threshold via SetThreshold().
func (s *SQLiteStore) ContentionLock() *AdaptiveLock {
	return s.alock
}

// Config returns the store's StoreConfig.
func (s *SQLiteStore) Config() StoreConfig {
	return s.storeConfig
}

// IsPerFileTenant reports whether this store operates in per-file tenant mode.
// Implements TenantModeProvider. When true, each tenant has its own database
// file and the tenant_id column is absent from the schema.
func (s *SQLiteStore) IsPerFileTenant() bool {
	return s.config.PerFileTenants
}

// nodesTable returns the per-tenant blob node store table name (t<XXXX>_nodes).
// All node CRUD methods use this rather than the hardcoded "entities" string.
func (s *SQLiteStore) nodesTable() string {
	return tenant.NodesTableName(s.config.TenantID)
}

// NodesTable returns the per-tenant blob node store table name (t<XXXX>_nodes).
// Implements storage.TableNamer; used by the OQL SQL generator to build
// correct push-down queries without hardcoding the table name.
func (s *SQLiteStore) NodesTable() string {
	return tenant.NodesTableName(s.config.TenantID)
}

// nodeSeqTable returns the per-tenant node ID sequence table name (t<XXXX>_nseq).
func (s *SQLiteStore) nodeSeqTable() string {
	return tenant.NodeSeqTableName(s.config.TenantID)
}

// nodeFTSTable returns the per-tenant node FTS virtual table name (t<XXXX>_nfts).
func (s *SQLiteStore) nodeFTSTable() string {
	return tenant.NodeFTSTableName(s.config.TenantID)
}

// AdaptedRegistry returns the store's adapted table registry.
// Returns nil only if the store was not properly initialized.
func (s *SQLiteStore) AdaptedRegistry() *AdaptedRegistry {
	return s.adapted
}

// WithLogger attaches a zerolog.Logger to the store. Returns the store so it
// can be chained: store := NewSQLiteStore(...).WithLogger(logger).
// Until this is called the store uses zerolog.Nop() and logs nothing.
func (s *SQLiteStore) WithLogger(logger zerolog.Logger) *SQLiteStore {
	s.logger = logger
	return s
}

// RegisterAdaptedEntity derives an adapted table for the given entity type
// from its JSON Schema and creates the table if it doesn't exist.
// This is called by the server layer when a schema is loaded or registered.
func (s *SQLiteStore) RegisterAdaptedEntity(ctx context.Context, entity string, schema map[string]interface{}) error {
	return RegisterAdaptedTable(ctx, s.db, s.adapted, entity, schema, s.dialect, s.config.TenantID)
}

// SQLiteConfig holds SQLite-specific configuration
type SQLiteConfig struct {
	DBPath            string
	EnableWAL         bool // Write-Ahead Logging for better concurrency
	EnableForeignKeys bool
	CacheSize         int    // Page cache size in KB
	BusyTimeout       int    // Milliseconds to wait on locked database
	FullTextEnabled   bool   // Enable FTS5 full-text search indexing
	GraphEnabled      bool   // Enable graph edge table maintenance
	TenantID          uint16 // 0 = no tenant scoping

	// Performance tuning (zero = use backend defaults)
	//   SQLite defaults: MaxOpenConns=1 (WAL single-writer),
	//   MaxIdleConns=1, ReadPoolSize=NumCPU.
	MaxOpenConns        int // Max open write connections (0 = backend default)
	MaxIdleConns        int // Max idle write connections (0 = backend default)
	ReadPoolSize        int // Max open read connections (0 = backend default)
	ContentionThreshold int // Adaptive lock threshold 0-100 (default 95)

	// PerFileTenants mirrors StoreConfig.SQLitePerFileTenants.
	// When true, tenant isolation is provided by separate database files
	// rather than a tenant_id column; schema DDL and all query methods
	// omit tenant_id accordingly.
	PerFileTenants bool
}

// sqliteBusyRetries is the number of times to retry an operation that fails
// with SQLITE_BUSY after the busy_timeout has already been exhausted.
// Each retry uses an exponential backoff starting at 25ms.
const sqliteBusyRetries = 7

// isSQLiteBusy returns true if the error is a SQLITE_BUSY error.
func isSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "SQLITE_BUSY") ||
		strings.Contains(err.Error(), "database is locked")
}

// withRetryNoLock is withRetry without acquiring the adaptive lock — for
// callers already holding a serialising mutex (the RI ForceLock path).
// Re-acquiring the same RWMutex on one goroutine would deadlock.
func (s *SQLiteStore) withRetryNoLock(fn func() error) error {
	err := fn()
	if err == nil {
		s.alock.RecordSuccess()
		return nil
	}
	if !isSQLiteBusy(err) {
		return err
	}
	s.alock.RecordFailure()
	backoff := 25 * time.Millisecond
	for attempt := 0; attempt < sqliteBusyRetries; attempt++ {
		jitter := time.Duration(rand.Int63n(int64(backoff) / 2))
		time.Sleep(backoff + jitter)
		backoff *= 2
		err = fn()
		if err == nil {
			s.alock.RecordSuccess()
			return nil
		}
		if !isSQLiteBusy(err) {
			return err
		}
		s.alock.RecordFailure()
	}
	return err
}

// RILock and RIUnlock expose the store's serialising mutex to the policy
// layer (the server's RI strategy, G-12). The server takes RILock around
// an RI-relevant write pair (a delete-with-restrictors against a
// create/update carrying the same ref) so they cannot interleave into
// write-skew under WAL snapshot isolation. Mechanism only; the server
// owns the decision of when to call it.
func (s *SQLiteStore) RILock()   { s.alock.ForceLock() }
func (s *SQLiteStore) RIUnlock() { s.alock.ForceUnlock() }

// withRetry executes fn, using the adaptive lock for serialisation under
// contention and retrying on SQLITE_BUSY with exponential backoff.
func (s *SQLiteStore) withRetry(fn func() error) error {
	if locked := s.alock.Lock(); locked {
		defer s.alock.Unlock()
	}
	err := fn()
	if err == nil {
		s.alock.RecordSuccess()
		return nil
	}
	if !isSQLiteBusy(err) {
		return err
	}
	s.alock.RecordFailure()
	backoff := 25 * time.Millisecond
	for attempt := 0; attempt < sqliteBusyRetries; attempt++ {
		jitter := time.Duration(rand.Int63n(int64(backoff) / 2))
		time.Sleep(backoff + jitter)
		backoff *= 2
		err = fn()
		if err == nil {
			s.alock.RecordSuccess()
			return nil
		}
		if !isSQLiteBusy(err) {
			return err
		}
		s.alock.RecordFailure()
	}
	return err
}

// withRetryRead executes a read operation, using the adaptive lock's RLock
// when engaged. Reads don't retry — SQLITE_BUSY on reads is extremely rare
// with WAL mode, and when the lock is engaged reads are already protected.
func (s *SQLiteStore) withRetryRead(fn func() (map[string]interface{}, error)) (map[string]interface{}, error) {
	if locked := s.alock.RLock(); locked {
		defer s.alock.RUnlock()
	}
	val, err := fn()
	if err == nil {
		s.alock.RecordSuccess()
		return val, nil
	}
	if !isSQLiteBusy(err) {
		return val, err
	}
	// Read hit SQLITE_BUSY — record and retry once
	s.alock.RecordFailure()
	time.Sleep(25 * time.Millisecond)
	val, err = fn()
	if err == nil {
		s.alock.RecordSuccess()
	} else if isSQLiteBusy(err) {
		s.alock.RecordFailure()
	}
	return val, err
}

// withRetryCreateVal is like withRetry but for Create which returns (int, error).
func (s *SQLiteStore) withRetryCreateVal(fn func() (int, error)) (int, error) {
	if locked := s.alock.Lock(); locked {
		defer s.alock.Unlock()
	}
	val, err := fn()
	if err == nil {
		s.alock.RecordSuccess()
		return val, nil
	}
	if !isSQLiteBusy(err) {
		return val, err
	}
	s.alock.RecordFailure()
	backoff := 25 * time.Millisecond
	for attempt := 0; attempt < sqliteBusyRetries; attempt++ {
		jitter := time.Duration(rand.Int63n(int64(backoff) / 2))
		time.Sleep(backoff + jitter)
		backoff *= 2
		val, err = fn()
		if err == nil {
			s.alock.RecordSuccess()
			return val, nil
		}
		if !isSQLiteBusy(err) {
			return val, err
		}
		s.alock.RecordFailure()
	}
	return val, err
}

// NewSQLiteStore creates a new SQLite-based storage with separate reader and
// writer connection pools. Under WAL mode the writer never blocks readers and
// vice-versa, so splitting pools maximises concurrency.
func NewSQLiteStore(dbPath string, config SQLiteConfig) (*SQLiteStore, error) {
	if dbPath == "" {
		dbPath = "xolu.db"
	}

	// Ensure the parent directory exists before opening. The on-disk layout is
	// derived from the data root by invariant (see pkg/storelayout), so the
	// store owns creation of its own directory rather than relying on every
	// caller to do it. In-memory databases have no directory.
	if dbPath != ":memory:" && !strings.Contains(dbPath, ":memory:") {
		if dir := filepath.Dir(dbPath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("failed to create store directory %q: %w", dir, err)
			}
		}
	}

	// Base DSN with pragmas inherited by every connection in both pools.
	baseDSN := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)&_pragma=cache_size(-%d)&_pragma=busy_timeout(%d)",
		dbPath, config.CacheSize, config.BusyTimeout)

	// --- Writer pool (single connection, serialised) ---
	db, err := sql.Open("sqlite", baseDSN)
	if err != nil {
		return nil, fmt.Errorf("failed to open writer database: %w", err)
	}
	// SQLite WAL allows exactly one writer at a time. Limiting the pool to
	// 1 connection means Go-side serialisation matches the database constraint
	// and avoids pointless SQLITE_BUSY retries between our own connections.
	maxOpen := config.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 1
	}
	maxIdle := config.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 1
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)

	// --- Reader pool (N connections, parallel) ---
	// query_only=ON prevents accidental writes through the reader pool.
	readDSN := baseDSN + "&_pragma=query_only(ON)"
	readDB, err := sql.Open("sqlite", readDSN)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to open reader database: %w", err)
	}
	readPoolSize := config.ReadPoolSize
	if readPoolSize <= 0 {
		readPoolSize = runtime.NumCPU()
		if readPoolSize < 2 {
			readPoolSize = 2
		}
	}
	readDB.SetMaxOpenConns(readPoolSize)
	readDB.SetMaxIdleConns(readPoolSize)

	contentionThreshold := config.ContentionThreshold
	if contentionThreshold == 0 {
		contentionThreshold = 95 // default when unset
	}

	store := &SQLiteStore{
		db:     db,
		readDB: readDB,
		dbPath: dbPath,
		config: config,
		storeConfig: StoreConfig{
			Type:                      "sqlite",
			DBPath:                    dbPath,
			FullTextEnabled:           config.FullTextEnabled,
			GraphEnabled:              config.GraphEnabled,
			TenantID:                  config.TenantID,
			SQLiteCacheSize:           config.CacheSize,
			SQLiteBusyTimeout:         config.BusyTimeout,
			SQLiteMaxOpenConns:        maxOpen,
			SQLiteMaxIdleConns:        maxIdle,
			SQLiteContentionThreshold: contentionThreshold,
			SQLitePerFileTenants:      config.PerFileTenants,
		},
		alock:              NewAdaptiveLock(contentionThreshold),
		adapted:            NewAdaptedRegistry(),
		dialect:            &SQLiteStorageDialect{},
		stmtCache:          NewStmtCache(readDB, 0), // default size; prepares against reader pool
		logger:             zerolog.Nop(),           // silent until WithLogger is called
		edgeWarnSuppressed: make(map[string]bool),
	}

	// Initialize database schema
	initCtx, initCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer initCancel()
	if err := store.initialize(initCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	// Load adapted table registry from metadata
	loadCtx, loadCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer loadCancel()
	adapted, err := LoadAdaptedRegistry(loadCtx, db, config.TenantID)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to load adapted table registry: %w", err)
	}
	store.adapted = adapted

	// Pre-suppress edge schema warnings for labels already registered in t<X>_e_sch.
	if err := store.loadEdgeSchemaSuppressions(loadCtx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to load edge schema suppressions: %w", err)
	}

	// Load adapted edge specs (column_spec populated) from t<X>_e_sch.
	if err := store.loadAdaptedEdgeSpecs(loadCtx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to load adapted edge specs: %w", err)
	}

	return store, nil
}

// initialize creates the necessary tables and triggers

// createSchema creates the per-tenant table family for this store's TenantID,
// plus the global tables (schema_version, tenants) that are shared across all
// tenants in the same database file.
//
// The two historical modes (shared vs per-file) are now unified: table names
// encode the tenant, so no tenant_id column is needed inside any data table.
// In shared-file mode all tenants share one file but each gets its own
// t<XXXX>_* tables. In per-file mode each tenant has a separate file; the
// tables are still named t<XXXX>_* for consistency and to allow future
// consolidation without data migration.
func (s *SQLiteStore) createSchema(ctx context.Context) error {
	tid := s.config.TenantID
	nodes := tenant.NodesTableName(tid)
	nseq := tenant.NodeSeqTableName(tid)
	nfts := tenant.NodeFTSTableName(tid)
	nIdxE := tenant.NodesIndexEntityType(tid)
	nIdxU := tenant.NodesIndexUpdatedAt(tid)

	// Global tables — one per database file regardless of tenant.
	// schema_version and tenants are intentionally not prefixed.
	globalDDL := `
		CREATE TABLE IF NOT EXISTS schema_version (
			version    INTEGER PRIMARY KEY,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS tenants (
			id         INTEGER NOT NULL PRIMARY KEY,
			name       TEXT NOT NULL UNIQUE,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`
	if _, err := s.db.ExecContext(ctx, globalDDL); err != nil {
		return fmt.Errorf("createSchema: global tables: %w", err)
	}

	// Per-tenant node tables.
	nodeDDL := fmt.Sprintf(`
		-- Blob node store: one row per node, JSON data column.
		-- The table name encodes the tenant; no tenant_id column needed.
		CREATE TABLE IF NOT EXISTS %s (
			entity_type TEXT NOT NULL,
			id          INTEGER NOT NULL,
			data        TEXT NOT NULL,
			_version    INTEGER NOT NULL DEFAULT 1,
			created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (entity_type, id)
		);
		CREATE INDEX IF NOT EXISTS %s ON %s(entity_type);
		CREATE INDEX IF NOT EXISTS %s ON %s(updated_at);

		-- Node ID sequences: one row per entity type.
		CREATE TABLE IF NOT EXISTS %s (
			entity_type TEXT NOT NULL,
			next_id     INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY (entity_type)
		);

		-- Node full-text search virtual table (FTS5).
		CREATE VIRTUAL TABLE IF NOT EXISTS %s USING fts5(
			entity_type UNINDEXED,
			entity_id   UNINDEXED,
			content
		);
	`, nodes, nIdxE, nodes, nIdxU, nodes, nseq, nfts)
	if _, err := s.db.ExecContext(ctx, nodeDDL); err != nil {
		return fmt.Errorf("createSchema: node tables for tenant %04X: %w", tid, err)
	}

	// Per-tenant edge FTS table — created unconditionally alongside the node
	// tables (not gated on GraphEnabled) so it is always available for text
	// search over edge property content regardless of graph mode.
	efts := tenant.EdgeFTSTableName(tid)
	eftsDDL := fmt.Sprintf(`
		-- Edge full-text search virtual table (FTS5).
		-- Indexes the text content of edge properties for full-text search.
		-- rel:      relationship label (e.g. "KNOWS")
		-- edge_id:  surrogate edge ID from t<X>_eseq (0 for topology-only edges)
		-- content:  searchable text extracted from the property blob
		CREATE VIRTUAL TABLE IF NOT EXISTS %s USING fts5(
			rel      UNINDEXED,
			edge_id  UNINDEXED,
			content
		);
	`, efts)
	if _, err := s.db.ExecContext(ctx, eftsDDL); err != nil {
		return fmt.Errorf("createSchema: edge FTS table for tenant %04X: %w", tid, err)
	}

	return nil
}

func (s *SQLiteStore) initialize(ctx context.Context) error {
	// Apply pragmas for performance and consistency
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
		fmt.Sprintf("PRAGMA cache_size = -%d", s.config.CacheSize),
		fmt.Sprintf("PRAGMA busy_timeout = %d", s.config.BusyTimeout),
	}

	for _, pragma := range pragmas {
		if _, err := s.db.ExecContext(ctx, pragma); err != nil {
			return fmt.Errorf("failed to set pragma: %w", err)
		}
	}

	// Create per-tenant table family (nodes, sequences, FTS) and global
	// tables (schema_version, tenants). Graph table created below when enabled.
	if err := s.createSchema(ctx); err != nil {
		return err
	}

	// Create per-tenant graph topology table when graph is enabled.
	if s.config.GraphEnabled {
		tid := s.config.TenantID
		table := tenant.GraphTableName(tid)
		graphDDL := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				source_entity     TEXT NOT NULL,
				source_id         INTEGER NOT NULL,
				target_entity     TEXT NOT NULL,
				target_id         INTEGER NOT NULL,
				relationship_name TEXT NOT NULL,
				edge_id           INTEGER,
				created_at        TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY (source_entity, source_id, target_entity, target_id, relationship_name)
			);
			CREATE INDEX IF NOT EXISTS %s ON %s(source_entity, source_id);
			CREATE INDEX IF NOT EXISTS %s ON %s(target_entity, target_id);
			CREATE INDEX IF NOT EXISTS %s ON %s(relationship_name);
		`, table,
			tenant.GraphIndexSource(tid), table,
			tenant.GraphIndexTarget(tid), table,
			tenant.GraphIndexRel(tid), table)
		if _, err := s.db.ExecContext(ctx, graphDDL); err != nil {
			return fmt.Errorf("failed to create graph table %s: %w", table, err)
		}

		// Blob edge property store: one row per edge with properties,
		// keyed by surrogate edge ID. Edges without properties have no row here;
		// their edge_id in t<X>_graph stays NULL.
		edgesTable := tenant.EdgePropsTableName(tid)
		edgesDDL := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				edge_id    INTEGER PRIMARY KEY,
				rel        TEXT NOT NULL,
				data       TEXT NOT NULL,
				_version   INTEGER NOT NULL DEFAULT 1,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);
		`, edgesTable)
		if _, err := s.db.ExecContext(ctx, edgesDDL); err != nil {
			return fmt.Errorf("failed to create edge props table %s: %w", edgesTable, err)
		}

		// Edge ID sequences: one row per relationship label, auto-incrementing.
		// Mirrors t<X>_nseq for nodes.
		eseqTable := tenant.EdgeSeqTableName(tid)
		eseqDDL := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				rel     TEXT NOT NULL,
				next_id INTEGER NOT NULL DEFAULT 1,
				PRIMARY KEY (rel)
			);
		`, eseqTable)
		if _, err := s.db.ExecContext(ctx, eseqDDL); err != nil {
			return fmt.Errorf("failed to create edge seq table %s: %w", eseqTable, err)
		}

		// Edge schema registry: one row per registered relationship label.
		// Presence here suppresses the warn-once log in AddEdgeWithProps and
		// is a prerequisite for adapted edge tables (Stage 7).
		// column_spec and has_extra are NULL for schema-only registrations
		// (Stage 6) and populated when an adapted table is created (Stage 7).
		eschTable := tenant.EdgeSchemaTableName(tid)
		eschDDL := fmt.Sprintf(`
			CREATE TABLE IF NOT EXISTS %s (
				rel         TEXT PRIMARY KEY,
				schema_hash TEXT NOT NULL,
				schema_json TEXT NOT NULL,
				column_spec TEXT,
				has_extra   INTEGER,
				created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);
		`, eschTable)
		if _, err := s.db.ExecContext(ctx, eschDDL); err != nil {
			return fmt.Errorf("failed to create edge schema table %s: %w", eschTable, err)
		}
	}

	// Graph triggers: not implemented — see initGraphSchema for manual sync strategy.
	if err := s.initGraphSchema(ctx); err != nil {
		return fmt.Errorf("failed to initialise graph schema: %w", err)
	}

	// Mark schema versions.
	// v2: initial shared schema. v3: _version column (part of DDL for new databases).
	// v4: per-tenant table naming (t<XXXX>_nodes replaces entities).
	// v5: edge property infrastructure (t<X>_edges, t<X>_eseq, edge_id on t<X>_graph).
	for _, v := range []int{2, 3, 4, 5} {
		if _, err := s.db.ExecContext(ctx,
			"INSERT OR IGNORE INTO schema_version (version) VALUES (?)", v); err != nil {
			return fmt.Errorf("failed to set schema version %d: %w", v, err)
		}
	}

	return nil
}

// initGraphSchema is called during schema initialisation. Graph edge
// synchronisation is handled manually inside each write transaction
// via syncGraphEdges() — not via SQLite triggers. See the comment block
// below for the rationale.
func (s *SQLiteStore) initGraphSchema(ctx context.Context) error {
	// NOTE: Graph synchronization strategy
	// =====================================
	// We use MANUAL graph synchronization instead of triggers for the following reasons:
	//
	// 1. Reliability: json_each() in triggers can cause "malformed JSON" errors in some
	//    SQLite builds, particularly with the pure-Go modernc.org/sqlite driver.
	//
	// 2. Integrity is maintained through transactions:
	//    - All CRUD operations (Create/Update/Patch/Delete/Save) use transactions
	//    - Graph sync happens within the SAME transaction as the document operation
	//    - If either operation fails, the entire transaction rolls back
	//    - This provides ACID guarantees equivalent to triggers
	//
	// 3. Explicit control: Manual sync makes the graph update logic visible and debuggable,
	//    and allows for easier testing and modification.
	//
	// The syncGraphEdges() method is called within every transaction that modifies documents,
	// ensuring document-graph consistency is always maintained atomically.

	return nil
}

// initV2Schema creates the v2-specific schema tables on first enable.
//
// --- v2 schema versioning convention ---
//
// v1 schema versions live in the shared `schema_version` table (integers 2-5
// at the time v2 work begins). v2 schema versions live in a separate
// `schema_version_v2` table so that:
//
//   - Disabling v2 (XOLU_API_V2_ENABLED=false) leaves no orphaned version
//     markers in the v1 schema_version table.
//   - The v2 table is only created when v2 is first enabled; it does not
//     appear in v1-only deployments at all.
//   - Each v2 development stage (S1-S10 in the plan) inserts its own row.
//
// v2 version numbers correspond directly to plan stages:
//
//   Stage | Version | Tables created
//   ------|---------|-----------------------------------------------
//   S1    |   1     | schema_version_v2 itself (bootstrap)
//   S3    |   3     | entity_meta
//   S5    |   5     | gen_definitions, sequences
//   S7    |   7     | fsm_definitions, fsm_machines,
//         |         | fsm_history, fsm_terminal_states,
//         |         | fsm_id_seq (created)
//   S9    |   9     | event_defs, event_delivery_log
//
// initV2Schema is called by the server when XOLU_API_V2_ENABLED=true, before
// any v2 handler is registered. It is idempotent: repeated calls are safe
// because all DDL uses CREATE TABLE IF NOT EXISTS and INSERT OR IGNORE.
// A failure here logs a warning and disables v2 for this run; it does not
// prevent the server from starting.

// InitV2Schema implements storage.V2SchemaInitialiser.
func (s *SQLiteStore) InitV2Schema(ctx context.Context) error {
	return s.initV2Schema(ctx)
}

func (s *SQLiteStore) initV2Schema(ctx context.Context) error {
	// Bootstrap: create the v2 version table if not present.
	bootstrapDDL := `
		CREATE TABLE IF NOT EXISTS schema_version_v2 (
			version    INTEGER PRIMARY KEY,
			stage      TEXT NOT NULL,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`
	if _, err := s.db.ExecContext(ctx, bootstrapDDL); err != nil {
		return fmt.Errorf("v2 schema bootstrap failed: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO schema_version_v2 (version, stage) VALUES (?, ?)", 1, "S1-scaffold"); err != nil {
		return fmt.Errorf("v2 schema version 1 insert failed: %w", err)
	}

	// Subsequent stages add their DDL here (S3, S5, S7, S9 ...).
	// Each block is guarded by CREATE TABLE IF NOT EXISTS and INSERT OR IGNORE,
	// so running initV2Schema after a stage upgrade is safe.

	// S3: metadata sidecar with TTL support. Since S13 (meta subject
	// generalisation, @C04c) the address is (subject_kind, subject_key):
	// fresh databases get the new shape here; legacy (entity, id INTEGER)
	// tables are rebuilt by the S13 block below.
	metaDDL := `
		CREATE TABLE IF NOT EXISTS entity_meta (
			tenant_id    INTEGER   NOT NULL DEFAULT 0,
			subject_kind TEXT      NOT NULL,
			subject_key  TEXT      NOT NULL,
			key          TEXT      NOT NULL,
			value        TEXT      NOT NULL,
			expires_at   TIMESTAMP NULL DEFAULT NULL,
			updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (tenant_id, subject_kind, subject_key, key)
		);
		CREATE INDEX IF NOT EXISTS idx_entity_meta_expires
			ON entity_meta(expires_at) WHERE expires_at IS NOT NULL;
	`
	// The subject index is created after the S13 block below: on a
	// legacy database this table still has the old columns here, and an
	// index on subject_kind would fail before the rebuild runs.
	if _, err := s.db.ExecContext(ctx, metaDDL); err != nil {
		return fmt.Errorf("v2 schema S3 (entity_meta) failed: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO schema_version_v2 (version, stage) VALUES (?, ?)", 3, "S3-meta"); err != nil {
		return fmt.Errorf("v2 schema version 3 insert failed: %w", err)
	}

	// S5: named sequence definitions and state.
	seqDDL := `
		CREATE TABLE IF NOT EXISTS gen_definitions (
			tenant_id   INTEGER NOT NULL DEFAULT 0,
			type        TEXT    NOT NULL,
			name        TEXT    NOT NULL,
			config_json TEXT    NOT NULL DEFAULT '{}',
			created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (tenant_id, type, name)
		);
		CREATE TABLE IF NOT EXISTS sequences (
			tenant_id    INTEGER NOT NULL DEFAULT 0,
			name         TEXT    NOT NULL,
			current_val  INTEGER NOT NULL,
			start_val    INTEGER NOT NULL DEFAULT 1,
			increment_by INTEGER NOT NULL DEFAULT 1,
			min_val      INTEGER,
			max_val      INTEGER,
			cycle        INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (tenant_id, name)
		);
	`
	if _, err := s.db.ExecContext(ctx, seqDDL); err != nil {
		return fmt.Errorf("v2 schema S5 (sequences) failed: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO schema_version_v2 (version, stage) VALUES (?, ?)", 5, "S5-sequences"); err != nil {
		return fmt.Errorf("v2 schema version 5 insert failed: %w", err)
	}

	// S7: FSM definitions and machines.
	//
	// Prototype-snapshot model: fsm_machines.fsm_def_id records lineage
	// only and has no foreign-key constraint. A definition may be deleted
	// without affecting machines already derived from it; each machine holds
	// a self-contained snapshot in snapshot_json. fsm_terminal_states is a
	// denormalised per-machine terminal-state index for fast walk checks.
	fsmDDL := `
		CREATE TABLE IF NOT EXISTS fsm_definitions (
			tenant_id     INTEGER NOT NULL DEFAULT 0,
			id            INTEGER NOT NULL,
			name          TEXT    NOT NULL,
			spec_json     TEXT    NOT NULL,
			analysis_json TEXT    NOT NULL DEFAULT '{}',
			created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (tenant_id, id)
		);
		CREATE INDEX IF NOT EXISTS idx_fsm_definitions_name
			ON fsm_definitions(tenant_id, name);

		CREATE TABLE IF NOT EXISTS fsm_machines (
			tenant_id       INTEGER NOT NULL DEFAULT 0,
			id              INTEGER NOT NULL,
			fsm_def_id      INTEGER NOT NULL,
			definition_name TEXT    NOT NULL,
			snapshot_json   TEXT    NOT NULL,
			state           TEXT    NOT NULL,
			vars_json       TEXT    NOT NULL DEFAULT '{}',
			ref             TEXT    NULL DEFAULT NULL,
			created_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (tenant_id, id)
		);
		CREATE INDEX IF NOT EXISTS idx_fsm_machines_definition
			ON fsm_machines(tenant_id, fsm_def_id);
		CREATE INDEX IF NOT EXISTS idx_fsm_machines_state
			ON fsm_machines(tenant_id, state);
		CREATE INDEX IF NOT EXISTS idx_fsm_machines_ref
			ON fsm_machines(tenant_id, ref) WHERE ref IS NOT NULL;

		CREATE TABLE IF NOT EXISTS fsm_history (
			tenant_id   INTEGER NOT NULL DEFAULT 0,
			id          INTEGER NOT NULL,
			machine_id  INTEGER NOT NULL,
			from_state  TEXT    NULL DEFAULT NULL,
			to_state    TEXT    NOT NULL,
			input       TEXT    NULL DEFAULT NULL,
			payload_json TEXT   NULL DEFAULT NULL,
			vars_json   TEXT    NOT NULL DEFAULT '{}',
			output_json TEXT    NULL DEFAULT NULL,
			note        TEXT    NULL DEFAULT NULL,
			at          TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (tenant_id, id)
		);
		CREATE INDEX IF NOT EXISTS idx_fsm_history_machine
			ON fsm_history(tenant_id, machine_id, id);

		CREATE TABLE IF NOT EXISTS fsm_terminal_states (
			tenant_id  INTEGER NOT NULL DEFAULT 0,
			machine_id INTEGER NOT NULL,
			state      TEXT    NOT NULL,
			PRIMARY KEY (tenant_id, machine_id, state)
		);

		-- Per-tenant, per-kind monotonic ID allocator for FSM definitions,
		-- machines, and history rows. Allocation uses the same atomic
		-- INSERT ... ON CONFLICT DO UPDATE SET next_id = next_id + 1 RETURNING
		-- pattern as the node-sequence allocator (see nextNodeID), chosen over
		-- a per-insert MAX(id)+1 scan for consistency with existing storage
		-- conventions and to avoid read-modify-write contention under
		-- concurrent inserts. 'kind' is one of 'def', 'machine', 'history'.
		CREATE TABLE IF NOT EXISTS fsm_id_seq (
			tenant_id INTEGER NOT NULL DEFAULT 0,
			kind      TEXT    NOT NULL,
			next_id   INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY (tenant_id, kind)
		);
	`
	if _, err := s.db.ExecContext(ctx, fsmDDL); err != nil {
		return fmt.Errorf("v2 schema S7 (fsm) failed: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO schema_version_v2 (version, stage) VALUES (?, ?)", 7, "S7-fsm"); err != nil {
		return fmt.Errorf("v2 schema version 7 insert failed: %w", err)
	}

	// S9: event subscriptions and delivery log.
	//
	// A subscription binds an event_type (entity.created/updated/deleted or
	// fsm.output) to an action (webhook or oql) with a JSON config. execution
	// records the requested mode; Part 1 always dispatches async regardless and
	// reports the downgrade via an X-Executed-As header. The delivery log records
	// one row per dispatch attempt (Part 1 is single-attempt, at-most-once), so a
	// dropped or failed delivery is observable after the fact.
	eventDDL := `
		CREATE TABLE IF NOT EXISTS event_defs (
			tenant_id   INTEGER NOT NULL DEFAULT 0,
			id          INTEGER NOT NULL,
			event_type  TEXT    NOT NULL,
			action_type TEXT    NOT NULL,
			config_json TEXT    NOT NULL DEFAULT '{}',
			execution   TEXT    NOT NULL DEFAULT 'async',
			created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (tenant_id, id)
		);
		CREATE INDEX IF NOT EXISTS idx_event_defs_type
			ON event_defs(tenant_id, event_type);

		CREATE TABLE IF NOT EXISTS event_delivery_log (
			tenant_id       INTEGER NOT NULL DEFAULT 0,
			id              INTEGER NOT NULL,
			event_def_id    INTEGER NOT NULL,
			event_type      TEXT    NOT NULL,
			status          TEXT    NOT NULL,
			detail          TEXT    NOT NULL DEFAULT '',
			attempted_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (tenant_id, id)
		);
		CREATE INDEX IF NOT EXISTS idx_event_delivery_log_sub
			ON event_delivery_log(tenant_id, event_def_id);
	`
	if _, err := s.db.ExecContext(ctx, eventDDL); err != nil {
		return fmt.Errorf("v2 schema S9 (events) failed: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO schema_version_v2 (version, stage) VALUES (?, ?)", 9, "S9-events"); err != nil {
		return fmt.Errorf("v2 schema version 9 insert failed: %w", err)
	}

	// S11: cal scheduling primitive — the authoritative booking record (H1) the
	// derived Pebble occupancy index is rebuilt from. Follows the fsm table
	// convention (tenant_id column + PRIMARY KEY (tenant_id, ...), unprefixed
	// names) rather than the prefixed per-tenant data-table convention: these are
	// definition/instance/history records like the fsm family, not high-volume
	// blob/graph data tables.
	//
	// Times are absolute UTC instants stored as INTEGER UnixNano (the xolutime
	// invariant; cal never stores local_time + zone_id — that intention is the
	// caller's, per R-T1). The Pebble index (occupancy bitmap, rollup, ordinal
	// map) is derived and rebuildable from these tables; losing it is never a lost
	// booking (H1). Secondary indices target the LiveBookingsOn(calendar, state)
	// hot path used by every lifecycle mutation, Move check, and MatchCommit
	// pre-check (see docs/KNOWN_ISSUES.md "cal design — schema gaps").
	calDDL := `
		CREATE TABLE IF NOT EXISTS cal_calendars (
			tenant_id     INTEGER NOT NULL DEFAULT 0,
			calendar_id   TEXT    NOT NULL,
			ordinal       INTEGER NOT NULL,
			entity_ref    INTEGER NOT NULL DEFAULT 0,
			capacity      INTEGER NOT NULL DEFAULT 1,
			default_state TEXT    NOT NULL DEFAULT 'binding',
			match_policy  TEXT    NOT NULL DEFAULT 'binding',
			created_at    TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (tenant_id, calendar_id)
		);
		-- ordinal is unique within a tenant (it is the dense key-space coordinate).
		CREATE UNIQUE INDEX IF NOT EXISTS idx_cal_calendars_ordinal
			ON cal_calendars(tenant_id, ordinal);

		CREATE TABLE IF NOT EXISTS cal_bookings (
			tenant_id        INTEGER NOT NULL DEFAULT 0,
			calendar_id      TEXT    NOT NULL,
			booking_id       TEXT    NOT NULL,
			state            TEXT    NOT NULL,
			start_utc        INTEGER NOT NULL,           -- UnixNano, UTC
			end_utc          INTEGER NOT NULL,           -- UnixNano, UTC, half-open
			mode             TEXT    NOT NULL DEFAULT 'exclusive',
			bearer           INTEGER NOT NULL DEFAULT 0, -- entity handle (0 = EntityNil)
			buffer_after_utc INTEGER NULL DEFAULT NULL,  -- UnixNano, UTC; NULL = no buffer
			created_utc      INTEGER NOT NULL,
			updated_utc      INTEGER NOT NULL,
			detail_ref       TEXT    NULL DEFAULT NULL,
			PRIMARY KEY (tenant_id, calendar_id, booking_id)
		);
		-- The hot path: LiveBookingsOn(calendar_id, plane) filters by calendar and
		-- state on every lifecycle mutation, Move feasibility check, and
		-- MatchCommit pre-check.
		CREATE INDEX IF NOT EXISTS idx_cal_bookings_cal_state
			ON cal_bookings(tenant_id, calendar_id, state);
		-- bookings/list?state=missed and other cross-calendar state scans.
		CREATE INDEX IF NOT EXISTS idx_cal_bookings_state
			ON cal_bookings(tenant_id, state);

		-- Reserved for per-participant optionality (optionality = per-participant).
		-- Created with the family per xolu's schema convention (cf. the fsm_* set);
		-- empty until the participant model lands.
		CREATE TABLE IF NOT EXISTS cal_participants (
			tenant_id   INTEGER NOT NULL DEFAULT 0,
			calendar_id TEXT    NOT NULL,
			booking_id  TEXT    NOT NULL,
			entity      INTEGER NOT NULL,
			required    INTEGER NOT NULL DEFAULT 1,  -- 1 = required, 0 = optional
			PRIMARY KEY (tenant_id, calendar_id, booking_id, entity)
		);

		-- Per-tenant monotonic ordinal allocator (GATE-3 #5), mirroring fsm_id_seq.
		-- next_ord is the dense uint32 counter (ascending from 1). The retired-pool
		-- for the reuse policy lives in the Pebble 0x03 metadata, not here.
		CREATE TABLE IF NOT EXISTS cal_ord_seq (
			tenant_id INTEGER NOT NULL DEFAULT 0,
			next_ord  INTEGER NOT NULL DEFAULT 1,
			PRIMARY KEY (tenant_id)
		);
	`
	if _, err := s.db.ExecContext(ctx, calDDL); err != nil {
		return fmt.Errorf("v2 schema S11 (cal) failed: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO schema_version_v2 (version, stage) VALUES (?, ?)", 11, "S11-cal"); err != nil {
		return fmt.Errorf("v2 schema version 11 insert failed: %w", err)
	}

	// S13: meta subject-addressing generalisation (@C04c; plan item 7).
	// entity_meta's (entity TEXT, id INTEGER) address becomes
	// (subject_kind TEXT, subject_key TEXT): entities keep kind=name,
	// key=decimal id; namespaced kinds (ts.timeline, cal.calendar, …)
	// become first-class. SQLite cannot retype a column, so legacy
	// tables are rebuilt: detect the old shape by its `id` column,
	// copy with CAST, swap. Idempotent: the new shape is detected and
	// left alone.
	legacyMeta, err := s.tableHasColumn(ctx, "entity_meta", "id")
	if err != nil {
		return fmt.Errorf("v2 schema S13 (meta subjects) detect: %w", err)
	}
	if legacyMeta {
		metaMigrate := `
			CREATE TABLE entity_meta_new (
				tenant_id    INTEGER   NOT NULL DEFAULT 0,
				subject_kind TEXT      NOT NULL,
				subject_key  TEXT      NOT NULL,
				key          TEXT      NOT NULL,
				value        TEXT      NOT NULL,
				expires_at   TIMESTAMP NULL DEFAULT NULL,
				updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				PRIMARY KEY (tenant_id, subject_kind, subject_key, key)
			);
			INSERT INTO entity_meta_new
				(tenant_id, subject_kind, subject_key, key, value, expires_at, updated_at)
				SELECT tenant_id, entity, CAST(id AS TEXT), key, value, expires_at, updated_at
				FROM entity_meta;
			DROP TABLE entity_meta;
			ALTER TABLE entity_meta_new RENAME TO entity_meta;
			CREATE INDEX IF NOT EXISTS idx_entity_meta_subject
				ON entity_meta(tenant_id, subject_kind, subject_key);
			CREATE INDEX IF NOT EXISTS idx_entity_meta_expires
				ON entity_meta(expires_at) WHERE expires_at IS NOT NULL;
		`
		if _, err := s.db.ExecContext(ctx, metaMigrate); err != nil {
			return fmt.Errorf("v2 schema S13 (meta subjects) rebuild: %w", err)
		}
	}
	// By here the table is guaranteed subject-shaped (fresh via S3, or
	// legacy via the rebuild above): the subject index is safe.
	if _, err := s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_entity_meta_subject
			ON entity_meta(tenant_id, subject_kind, subject_key)`); err != nil {
		return fmt.Errorf("v2 schema S13 subject index failed: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		"INSERT OR IGNORE INTO schema_version_v2 (version, stage) VALUES (?, ?)", 13, "S13-meta-subjects"); err != nil {
		return fmt.Errorf("v2 schema version 13 insert failed: %w", err)
	}

	return nil
}

// tableHasColumn reports whether the named table currently has a column,
// via PRAGMA table_info — the legacy-shape detector for rebuilds.
func (s *SQLiteStore) tableHasColumn(ctx context.Context, table, column string) (bool, error) {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Info returns store information
func (s *SQLiteStore) Info() StoreInfo {
	return StoreInfo{
		Type:                "sqlite",
		Version:             "1.0.0",
		SupportsSearch:      true,
		SupportsBatch:       true,
		SupportsTransaction: true,
	}
}

// Create inserts a new entity with auto-generated ID
func (s *SQLiteStore) Create(ctx context.Context, entity string, data map[string]interface{}) (int, error) {
	return s.withRetryCreateVal(func() (int, error) {
		return s.createInner(ctx, entity, data)
	})
}

// CreateNoLock is Create for a caller already holding RILock (the
// serialise strategy, when the payload carries REF edges). Skips the
// adaptive lock to avoid re-locking the same mutex.
func (s *SQLiteStore) CreateNoLock(ctx context.Context, entity string, data map[string]interface{}) (int, error) {
	var out int
	err := s.withRetryNoLock(func() error {
		v, e := s.createInner(ctx, entity, data)
		out = v
		return e
	})
	return out, err
}

func (s *SQLiteStore) createInner(ctx context.Context, entity string, data map[string]interface{}) (int, error) {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	// Get next ID from per-tenant sequence table (no tenant_id column — table is the boundary).
	var nextID int
	seqSQL := `
		INSERT INTO ` + s.nodeSeqTable() + ` (entity_type, next_id)
		VALUES (?, 1)
		ON CONFLICT(entity_type) DO UPDATE SET next_id = next_id + 1
		RETURNING next_id`
	err = tx.QueryRowContext(ctx, seqSQL, entity).Scan(&nextID)
	if err != nil {
		return 0, fmt.Errorf("failed to get next ID: %w", err)
	}

	// Create a copy of data to avoid mutating input
	dataCopy := make(map[string]interface{}, len(data)+1)
	for k, v := range data {
		dataCopy[k] = v
	}
	dataCopy["id"] = nextID

	// Insert entity: adapted table or blob
	if spec := s.adapted.Get(entity); spec != nil {
		if err := adaptedCreate(ctx, tx, spec, s.dialect, nextID, dataCopy); err != nil {
			return 0, err
		}
	} else {
		// Marshal to JSON (only needed for blob storage)
		jsonData, err := json.Marshal(dataCopy)
		if err != nil {
			return 0, fmt.Errorf("failed to marshal data: %w", err)
		}
		_, insErr := tx.ExecContext(ctx, `
			INSERT INTO `+s.nodesTable()+` (entity_type, id, data)
			VALUES (?, ?, ?)
		`, entity, nextID, string(jsonData))
		if insErr != nil {
			return 0, fmt.Errorf("failed to insert entity: %w", insErr)
		}
	}

	// Manually sync graph edges
	if err := s.syncGraphEdges(ctx, tx, entity, nextID, dataCopy); err != nil {
		return 0, fmt.Errorf("failed to sync graph: %w", err)
	}

	// Index for full-text search
	if err := s.indexForFTS(ctx, tx, entity, nextID, dataCopy); err != nil {
		return 0, fmt.Errorf("failed to index for FTS: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit: %w", err)
	}

	return nextID, nil
}

// syncGraphEdges extracts REF fields and creates graph edges.
//
// All REFs for the entity are collected first, then inserted via a single
// prepared statement — one ExecContext per edge rather than one
// fmt.Sprintf + ExecContext. When an entity has no REF fields the INSERT
// is skipped entirely.
func (s *SQLiteStore) syncGraphEdges(ctx context.Context, tx *sql.Tx, sourceEntity string, sourceID int, data map[string]interface{}) error {
	if !s.config.GraphEnabled {
		return nil
	}

	table := tenant.GraphEdgesTableName(s.config.TenantID)

	// Validate and collect edges before touching the database. Failing here
	// avoids issuing a DELETE that would then be rolled back on extraction error.
	rawEdges, err := models.ExtractEntityEdges(data)
	if err != nil {
		return fmt.Errorf("syncGraphEdges: %w", err)
	}

	// In-transaction REF target existence (G-12 create-side closure;
	// @R02.3 write validation, shipped early). The handler's in-memory
	// pre-check races the delete path: the target can vanish between
	// that check and this transaction. Re-checking HERE, inside the
	// write's own transaction, makes create-vs-delete linearisable under
	// serialised writers: if the delete committed first the target row
	// is gone and this rejects; if this commits first, the delete's own
	// in-tx referrer check (DeleteWithRestrict) sees the new edge.
	// Two-pronged like the delete side: adapted targets in their spec
	// table, blob targets in the nodes table. The row being written in
	// this very transaction is visible to these SELECTs (same tx), so
	// self-references work.
	checked := map[string]bool{}
	for _, ee := range rawEdges {
		key := fmt.Sprintf("%s/%d", ee.TargetEntity, ee.TargetID)
		if checked[key] {
			continue
		}
		checked[key] = true
		var one int
		var qerr error
		if spec := s.adapted.Get(ee.TargetEntity); spec != nil {
			qerr = tx.QueryRowContext(ctx,
				`SELECT 1 FROM `+spec.TableName()+` WHERE id = ?`, int64(ee.TargetID)).Scan(&one)
		} else {
			qerr = tx.QueryRowContext(ctx,
				`SELECT 1 FROM `+s.nodesTable()+` WHERE entity_type = ? AND id = ?`,
				ee.TargetEntity, int64(ee.TargetID)).Scan(&one)
		}
		if qerr == sql.ErrNoRows {
			return &RefTargetMissingError{
				SourceEntity: sourceEntity, SourceID: sourceID,
				TargetEntity: ee.TargetEntity, TargetID: int(ee.TargetID),
			}
		}
		if qerr != nil {
			return fmt.Errorf("syncGraphEdges: ref target check %s: %w", key, qerr)
		}
	}

	// Delete old edges from this entity (always a single statement).
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(
		`DELETE FROM %s WHERE source_entity = ? AND source_id = ?`, table),
		sourceEntity, sourceID,
	); err != nil {
		return err
	}
	var edges []rebuildEdge
	for _, ee := range rawEdges {
		edges = append(edges, rebuildEdge{
			sourceEntity: sourceEntity,
			sourceID:     sourceID,
			targetEntity: ee.TargetEntity,
			targetID:     int64(ee.TargetID),
			relationship: ee.Relationship,
		})
	}

	// Nothing to insert — skip the prepare round-trip.
	if len(edges) == 0 {
		return nil
	}

	// Prepare once, execute once per edge.
	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(
		`INSERT INTO %s (source_entity, source_id, target_entity, target_id, relationship_name)
		 VALUES (?, ?, ?, ?, ?)`, table))
	if err != nil {
		return fmt.Errorf("syncGraphEdges: prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, e := range edges {
		if _, err := stmt.ExecContext(ctx, e.sourceEntity, e.sourceID, e.targetEntity, e.targetID, e.relationship); err != nil {
			return fmt.Errorf("syncGraphEdges: insert edge: %w", err)
		}
	}

	return nil
}

// Get retrieves an entity by ID
func (s *SQLiteStore) Get(ctx context.Context, entity string, id int) (map[string]interface{}, error) {
	return s.withRetryRead(func() (map[string]interface{}, error) {
		return s.getInner(ctx, entity, id)
	})
}

func (s *SQLiteStore) getInner(ctx context.Context, entity string, id int) (map[string]interface{}, error) {

	// Adapted table path
	if spec := s.adapted.Get(entity); spec != nil {
		return adaptedGet(ctx, s.readDB, spec, s.dialect, id)
	}

	var jsonData string
	var version int
	err := s.readDB.QueryRowContext(ctx, `
		SELECT data, _version FROM `+s.nodesTable()+` 
		WHERE `+`entity_type = ? AND id = ?
	`, entity, id).Scan(&jsonData, &version)

	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query entity: %w", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal([]byte(jsonData), &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %w", err)
	}

	result["_version"] = version
	return result, nil
}

// GetMany fetches multiple " + s.nodesTable() + " of the same type in a single query.
// Returns a map[id]data for every id found; missing ids are absent.
func (s *SQLiteStore) GetMany(ctx context.Context, entity string, ids []int) (map[int]map[string]interface{}, error) {
	if len(ids) == 0 {
		return map[int]map[string]interface{}{}, nil
	}
	results := make(map[int]map[string]interface{}, len(ids))

	// Adapted table path — fall back to individual Gets (rare in practice).
	if spec := s.adapted.Get(entity); spec != nil {
		for _, id := range ids {
			data, err := s.getInner(ctx, entity, id)
			if err == ErrNotFound {
				continue
			}
			if err != nil {
				return nil, err
			}
			results[id] = data
		}
		return results, nil
	}

	// Blob path: one query with WHERE id IN (...).
	placeholders := make([]string, len(ids))
	args := []interface{}{entity}
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	query := `SELECT id, data, _version FROM ` + s.nodesTable() + ` WHERE ` +
		`entity_type = ? AND id IN (` +
		strings.Join(placeholders, ",") + `)`

	rows, err := s.readDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("GetMany query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id, version int
		var jsonData string
		if err := rows.Scan(&id, &jsonData, &version); err != nil {
			return nil, fmt.Errorf("GetMany scan: %w", err)
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
			return nil, fmt.Errorf("GetMany unmarshal id=%d: %w", id, err)
		}
		data["_version"] = version
		results[id] = data
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("GetMany rows: %w", err)
	}
	return results, nil
}

// Update replaces an entity completely
func (s *SQLiteStore) Update(ctx context.Context, entity string, id int, data map[string]interface{}) error {
	return s.withRetry(func() error {
		return s.updateInner(ctx, entity, id, data)
	})
}

func (s *SQLiteStore) updateInner(ctx context.Context, entity string, id int, data map[string]interface{}) error {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Extract _version for optimistic concurrency (opt-in: if absent, no check)
	var expectVersion int
	var hasVersion bool
	if v, ok := data["_version"]; ok {
		hasVersion = true
		switch tv := v.(type) {
		case float64:
			expectVersion = int(tv)
		case int:
			expectVersion = tv
		}
	}

	// Create a copy to avoid mutating input; strip _version from the JSON blob
	dataCopy := make(map[string]interface{}, len(data)+1)
	for k, v := range data {
		if k == "_version" {
			continue
		}
		dataCopy[k] = v
	}
	dataCopy["id"] = id

	// Update entity: adapted table or blob
	if spec := s.adapted.Get(entity); spec != nil {
		if err := adaptedUpdate(ctx, tx, spec, s.dialect, id, dataCopy, expectVersion, hasVersion); err != nil {
			return err
		}
	} else {
		// Marshal to JSON (only needed for blob storage)
		jsonData, err := json.Marshal(dataCopy)
		if err != nil {
			return fmt.Errorf("failed to marshal data: %w", err)
		}
		// Update entity with optional version check
		var result sql.Result
		if hasVersion {
			result, err = tx.ExecContext(ctx, `
				UPDATE `+s.nodesTable()+` 
				SET data = ?, _version = _version + 1, updated_at = CURRENT_TIMESTAMP 
				WHERE `+`entity_type = ? AND id = ? AND _version = ?
			`, string(jsonData), entity, id, expectVersion)
		} else {
			result, err = tx.ExecContext(ctx, `
				UPDATE `+s.nodesTable()+` 
				SET data = ?, _version = _version + 1, updated_at = CURRENT_TIMESTAMP 
				WHERE `+`entity_type = ? AND id = ?
			`, string(jsonData), entity, id)
		}
		if err != nil {
			return fmt.Errorf("failed to update entity: %w", err)
		}

		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			// Distinguish "not found" from "version mismatch"
			if hasVersion {
				var exists int
				_ = tx.QueryRowContext(ctx, `
					SELECT 1 FROM `+s.nodesTable()+` 
					WHERE `+`entity_type = ? AND id = ?
				`, entity, id).Scan(&exists)
				if exists == 1 {
					return ErrConflict
				}
			}
			return ErrNotFound
		}
	}

	// Manually sync graph edges
	if err := s.syncGraphEdges(ctx, tx, entity, id, dataCopy); err != nil {
		return fmt.Errorf("failed to sync graph: %w", err)
	}

	// Update FTS index
	if err := s.indexForFTS(ctx, tx, entity, id, dataCopy); err != nil {
		return fmt.Errorf("failed to update FTS index: %w", err)
	}

	return tx.Commit()
}

// Patch partially updates an entity
func (s *SQLiteStore) Patch(ctx context.Context, entity string, id int, updates map[string]interface{}) error {
	return s.withRetry(func() error {
		return s.patchInner(ctx, entity, id, updates, nil)
	})
}

// PatchValidated applies a partial update inside a transaction and runs
// the validator against the merged data before committing.
func (s *SQLiteStore) PatchValidated(ctx context.Context, entity string, id int, updates map[string]interface{}, validate func(merged map[string]interface{}) error) error {
	return s.withRetry(func() error {
		return s.patchInner(ctx, entity, id, updates, validate)
	})
}

func (s *SQLiteStore) patchInner(ctx context.Context, entity string, id int, updates map[string]interface{}, validate func(merged map[string]interface{}) error) error {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Extract _version for optimistic concurrency (opt-in: if absent, no check)
	var expectVersion int
	var hasVersion bool
	if v, ok := updates["_version"]; ok {
		hasVersion = true
		switch tv := v.(type) {
		case float64:
			expectVersion = int(tv)
		case int:
			expectVersion = tv
		}
	}

	spec := s.adapted.Get(entity)

	// Get existing data (adapted or blob path)
	var existing map[string]interface{}
	if spec != nil {
		var currentVersion int
		existing, currentVersion, err = adaptedGetInTx(ctx, tx, spec, s.dialect, id)
		if err != nil {
			return err
		}
		// Version check for adapted path
		if hasVersion && currentVersion != expectVersion {
			return ErrConflict
		}
	} else {
		var jsonData string
		err = tx.QueryRowContext(ctx,
			`SELECT data FROM `+s.nodesTable()+` WHERE `+`entity_type = ? AND id = ?`,
			entity, id).Scan(&jsonData)

		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("failed to query entity: %w", err)
		}

		if err := json.Unmarshal([]byte(jsonData), &existing); err != nil {
			return fmt.Errorf("failed to unmarshal data: %w", err)
		}
	}

	// Merge updates into existing data; skip _version (it's metadata, not document content).
	// nil values are stored as-is (JSON null). The handler is responsible for
	// removing keys from the patch map when PatchNullBehavior is "delete".
	for key, value := range updates {
		if key == "id" || key == "_version" {
			continue
		}
		existing[key] = value
	}

	// Ensure ID is set
	existing["id"] = id

	// Run validation against the merged data (inside the transaction)
	if validate != nil {
		if err := validate(existing); err != nil {
			return err
		}
	}

	// Strip _version from the data (it lives in the column, not the document)
	delete(existing, "_version")

	// Write back: adapted or blob path
	if spec != nil {
		// For adapted path, use adaptedUpdate with version already checked above
		if err := adaptedUpdate(ctx, tx, spec, s.dialect, id, existing, 0, false); err != nil {
			return err
		}
	} else {
		// Marshal back to JSON
		updatedJSON, err := json.Marshal(existing)
		if err != nil {
			return fmt.Errorf("failed to marshal data: %w", err)
		}

		// Update with optional version check
		var result sql.Result
		if hasVersion {
			result, err = tx.ExecContext(ctx,
				`UPDATE `+s.nodesTable()+` SET data = ?, _version = _version + 1, updated_at = CURRENT_TIMESTAMP WHERE `+`entity_type = ? AND id = ? AND _version = ?`,
				string(updatedJSON), entity, id, expectVersion)
		} else {
			result, err = tx.ExecContext(ctx,
				`UPDATE `+s.nodesTable()+` SET data = ?, _version = _version + 1, updated_at = CURRENT_TIMESTAMP WHERE `+`entity_type = ? AND id = ?`,
				string(updatedJSON), entity, id)
		}
		if err != nil {
			return fmt.Errorf("failed to update entity: %w", err)
		}

		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			if hasVersion {
				// Entity exists but version didn't match
				return ErrConflict
			}
			return ErrNotFound
		}
	}

	// Manually sync graph edges
	if err := s.syncGraphEdges(ctx, tx, entity, id, existing); err != nil {
		return fmt.Errorf("failed to sync graph: %w", err)
	}

	// Update FTS index
	if err := s.indexForFTS(ctx, tx, entity, id, existing); err != nil {
		return fmt.Errorf("failed to update FTS index: %w", err)
	}

	return tx.Commit()
}

// Delete removes an entity
func (s *SQLiteStore) Delete(ctx context.Context, entity string, id int) error {
	return s.withRetry(func() error {
		return s.deleteInner(ctx, entity, id, nil)
	})
}

// RestrictViolationError reports that a delete was refused because live
// referrers with a restrict on_delete policy exist. Referrers holds up to a
// bounded number of "entity:id" keys for the error message.
type RestrictViolationError struct {
	Referrers []string
}

func (e *RestrictViolationError) Error() string {
	return fmt.Sprintf("delete restricted by %d live referrer(s): %v", len(e.Referrers), e.Referrers)
}

// RefTargetMissingError reports that a write was refused because a REF
// field names a target that does not exist at commit time — the
// create-side sibling of RestrictViolationError, checked inside the
// write's own transaction (G-12 create-side closure; @R02.3 shipped
// early). Maps to XOLU-RI003 / HTTP 400 at the handler.
type RefTargetMissingError struct {
	SourceEntity string
	SourceID     int
	TargetEntity string
	TargetID     int
}

func (e *RefTargetMissingError) Error() string {
	return fmt.Sprintf("%s/%d references %s/%d which does not exist",
		e.SourceEntity, e.SourceID, e.TargetEntity, e.TargetID)
}

// DeleteWithRestrict deletes entity:id, refusing with *RestrictViolationError
// if any entity named in restrictedBy still references the target. The
// referrer check runs INSIDE the delete's own transaction (@C04a: a guard
// must live where its transaction lives), which closes the check-then-act
// window the handler-level in-memory pre-check cannot (G-12): a concurrent
// referrer create either commits its edge row before our read (we see it and
// refuse) or serialises after our commit.
//
// Referrer discovery is two-pronged, matching where REF-derived state
// authoritatively lives per storage class:
//   - blob entities: the SQL edge table (synced transactionally by
//     syncGraphEdges on every write);
//   - adapted entities: their own REF_{field}_entity/_id columns, probed
//     spec-driven via the IsREFEntity/IsREFID flags (adapted writes do not
//     populate the edge table).
//
// restrictedBy is the set of referring entity names that carry a restrict
// policy toward this target (the caller derives it from the schema x-ref
// registry). Empty/nil means no restrict policies exist and this behaves
// exactly like Delete.
func (s *SQLiteStore) DeleteWithRestrict(ctx context.Context, entity string, id int, restrictedBy []string) error {
	return s.withRetry(func() error {
		return s.deleteInner(ctx, entity, id, restrictedBy)
	})
}

// DeleteWithRestrictNoLock is DeleteWithRestrict for a caller that
// already holds RILock (the serialise strategy). It skips the adaptive
// lock to avoid deadlocking on the same RWMutex.
func (s *SQLiteStore) DeleteWithRestrictNoLock(ctx context.Context, entity string, id int, restrictedBy []string) error {
	return s.withRetryNoLock(func() error {
		return s.deleteInner(ctx, entity, id, restrictedBy)
	})
}

func (s *SQLiteStore) deleteInner(ctx context.Context, entity string, id int, restrictedBy []string) error {

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// In-transaction restrict check (G-12). Runs before the row delete so a
	// refused delete does no work; under the same tx so no concurrent
	// referrer create can slip between check and act.
	if len(restrictedBy) > 0 {
		refs, err := s.restrictReferrersInTx(ctx, tx, entity, id, restrictedBy)
		if err != nil {
			return fmt.Errorf("restrict referrer check: %w", err)
		}
		if len(refs) > 0 {
			return &RestrictViolationError{Referrers: refs}
		}
	}

	// Delete entity: adapted table or blob
	if spec := s.adapted.Get(entity); spec != nil {
		if err := adaptedDelete(ctx, tx, spec, s.dialect, id); err != nil {
			return err
		}
	} else {
		result, err := tx.ExecContext(ctx,
			`DELETE FROM `+s.nodesTable()+` WHERE `+`entity_type = ? AND id = ?`,
			entity, id)
		if err != nil {
			return fmt.Errorf("failed to delete entity: %w", err)
		}

		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrNotFound
		}
	}

	// Clean up graph edges
	if s.config.GraphEnabled {
		edgeTable := tenant.GraphEdgesTableName(s.config.TenantID)
		_, err = tx.ExecContext(ctx, fmt.Sprintf(`
			DELETE FROM %s 
			WHERE (source_entity = ? AND source_id = ?)
			   OR (target_entity = ? AND target_id = ?)
		`, edgeTable), entity, id, entity, id)
		if err != nil {
			return fmt.Errorf("failed to delete graph edges: %w", err)
		}
	}

	// Remove from FTS index
	idStr := fmt.Sprintf("%d", id)

	_, err = tx.ExecContext(ctx, `
			DELETE FROM `+s.nodeFTSTable()+` WHERE entity_type = ? AND entity_id = ?
		`, entity, idStr)

	if err != nil {
		return fmt.Errorf("failed to delete from FTS index: %w", err)
	}

	// Cascade delete v2 entity metadata (entity_meta is a global table;
	// always attempt regardless of v2 enabled state — the table may not
	// exist on v1-only deployments, in which case the error is ignored).
	sub := EntitySubject(entity, id)
	_, _ = tx.ExecContext(ctx,
		`DELETE FROM entity_meta WHERE tenant_id=? AND subject_kind=? AND subject_key=?`,
		s.config.TenantID, sub.Kind, sub.Key)

	return tx.Commit()
}

// Save creates an entity with a specific ID (fails if exists)
// restrictReferrersInTx discovers live referrers of entity:id among the
// restrictedBy entities, inside the caller's transaction. Returns up to
// maxNamedReferrers "entity:id" keys. See DeleteWithRestrict for the
// two-pronged discovery rationale.
func (s *SQLiteStore) restrictReferrersInTx(ctx context.Context, tx *sql.Tx, entity string, id int, restrictedBy []string) ([]string, error) {
	const maxNamedReferrers = 10
	var refs []string

	// Partition restrictedBy into blob referrers (edge table) and adapted
	// referrers (REF column probe).
	var blobReferrers []string
	var adaptedSpecs []*AdaptedTableSpec
	for _, refEntity := range restrictedBy {
		if spec := s.adapted.Get(refEntity); spec != nil {
			adaptedSpecs = append(adaptedSpecs, spec)
		} else {
			blobReferrers = append(blobReferrers, refEntity)
		}
	}

	// Prong 1 — blob referrers via the edge table (transactionally synced).
	if len(blobReferrers) > 0 && s.config.GraphEnabled {
		edgeTable := tenant.GraphEdgesTableName(s.config.TenantID)
		placeholders := strings.Repeat("?,", len(blobReferrers))
		placeholders = placeholders[:len(placeholders)-1]
		args := make([]interface{}, 0, len(blobReferrers)+2)
		args = append(args, entity, id)
		for _, e := range blobReferrers {
			args = append(args, e)
		}
		rows, err := tx.QueryContext(ctx, fmt.Sprintf(
			`SELECT source_entity, source_id FROM %s
			 WHERE target_entity = ? AND target_id = ?
			   AND source_entity IN (%s)
			 LIMIT %d`, edgeTable, placeholders, maxNamedReferrers), args...)
		if err != nil {
			return nil, fmt.Errorf("edge-table referrer query: %w", err)
		}
		for rows.Next() {
			var se string
			var si int
			if err := rows.Scan(&se, &si); err != nil {
				_ = rows.Close()
				return nil, err
			}
			refs = append(refs, fmt.Sprintf("%s:%d", se, si))
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
		if len(refs) >= maxNamedReferrers {
			return refs[:maxNamedReferrers], nil
		}
	}

	// Prong 2 — adapted referrers via their REF columns, spec-driven.
	// Adapted writes do not populate the edge table, but their REF fields
	// decompose into REF_{field}_entity/_id columns (IsREFEntity/IsREFID),
	// which are probed directly in the same transaction.
	for _, spec := range adaptedSpecs {
		// Collect the (entityCol, idCol) pairs per JSON field.
		type refPair struct{ entityCol, idCol string }
		pairs := map[string]*refPair{}
		for _, col := range spec.Columns {
			if !col.IsREF {
				continue
			}
			p := pairs[col.JSONField]
			if p == nil {
				p = &refPair{}
				pairs[col.JSONField] = p
			}
			if col.IsREFEntity {
				p.entityCol = col.Name
			} else if col.IsREFID {
				p.idCol = col.Name
			}
		}
		if len(pairs) == 0 {
			continue
		}
		var conds []string
		var args []interface{}
		for _, p := range pairs {
			if p.entityCol == "" || p.idCol == "" {
				continue
			}
			conds = append(conds, fmt.Sprintf("(%s = ? AND %s = ?)", p.entityCol, p.idCol))
			args = append(args, entity, id)
		}
		if len(conds) == 0 {
			continue
		}
		q := fmt.Sprintf(`SELECT id FROM %s WHERE %s LIMIT %d`,
			spec.TableName(), strings.Join(conds, " OR "), maxNamedReferrers-len(refs))
		rows, err := tx.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("adapted referrer query (%s): %w", spec.Entity, err)
		}
		for rows.Next() {
			var rid int
			if err := rows.Scan(&rid); err != nil {
				_ = rows.Close()
				return nil, err
			}
			refs = append(refs, fmt.Sprintf("%s:%d", spec.Entity, rid))
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
		if len(refs) >= maxNamedReferrers {
			return refs[:maxNamedReferrers], nil
		}
	}

	return refs, nil
}

func (s *SQLiteStore) Save(ctx context.Context, entity string, id int, data map[string]interface{}) (bool, error) {
	var created bool
	err := s.withRetry(func() error {
		var innerErr error
		created, innerErr = s.saveInner(ctx, entity, id, data)
		return innerErr
	})
	return created, err
}

func (s *SQLiteStore) saveInner(ctx context.Context, entity string, id int, data map[string]interface{}) (bool, error) {

	// Extract _version for optional optimistic concurrency check.
	// If present in the request, the overwrite path becomes a conditional write.
	var expectVersion int
	var hasVersion bool
	if v, ok := data["_version"]; ok {
		hasVersion = true
		switch tv := v.(type) {
		case float64:
			expectVersion = int(tv)
		case int:
			expectVersion = tv
		}
	}

	// Create a copy to avoid mutating input; strip _version (column, not document content).
	dataCopy := make(map[string]interface{}, len(data)+1)
	for k, v := range data {
		if k == "_version" {
			continue
		}
		dataCopy[k] = v
	}
	dataCopy["id"] = id

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	// Check existence inside the transaction to prevent TOCTOU races.
	spec := s.adapted.Get(entity)

	var exists bool
	if spec != nil {
		err = tx.QueryRowContext(ctx, fmt.Sprintf(
			"SELECT EXISTS(SELECT 1 FROM %s WHERE id = ?)",
			spec.TableName()), id).Scan(&exists)
	} else {
		err = tx.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM `+s.nodesTable()+` WHERE `+`entity_type = ? AND id = ?)`,
			entity, id).Scan(&exists)
	}
	if err != nil {
		return false, fmt.Errorf("failed to check existence: %w", err)
	}

	if exists {
		// Overwrite path: conditional or unconditional update in place.
		if spec != nil {
			if err := adaptedUpdate(ctx, tx, spec, s.dialect, id, dataCopy, expectVersion, hasVersion); err != nil {
				return false, err
			}
		} else {
			jsonData, err := json.Marshal(dataCopy)
			if err != nil {
				return false, fmt.Errorf("failed to marshal data: %w", err)
			}
			var result sql.Result
			if hasVersion {
				result, err = tx.ExecContext(ctx,
					`UPDATE `+s.nodesTable()+` SET data = ?, _version = _version + 1, updated_at = CURRENT_TIMESTAMP WHERE `+`entity_type = ? AND id = ? AND _version = ?`,
					string(jsonData), entity, id, expectVersion)
			} else {
				result, err = tx.ExecContext(ctx,
					`UPDATE `+s.nodesTable()+` SET data = ?, _version = _version + 1, updated_at = CURRENT_TIMESTAMP WHERE `+`entity_type = ? AND id = ?`,
					string(jsonData), entity, id)
			}
			if err != nil {
				return false, fmt.Errorf("failed to overwrite entity: %w", err)
			}
			if hasVersion {
				rows, err := result.RowsAffected()
				if err != nil {
					return false, err
				}
				if rows == 0 {
					return false, ErrConflict
				}
			}
		}
	} else {
		// Create path: insert new record.

		// Update sequence so future auto-IDs stay above this one.
		var seqErr error

		_, seqErr = tx.ExecContext(ctx, `
				INSERT INTO `+s.nodeSeqTable()+` (entity_type, next_id)
				VALUES (?, ?)
				ON CONFLICT(entity_type) DO UPDATE
				SET next_id = MAX(next_id, excluded.next_id + 1)
			`, entity, id+1)

		if seqErr != nil {
			return false, fmt.Errorf("failed to update sequence: %w", seqErr)
		}

		if spec != nil {
			if err := adaptedCreate(ctx, tx, spec, s.dialect, id, dataCopy); err != nil {
				return false, err
			}
		} else {
			jsonData, err := json.Marshal(dataCopy)
			if err != nil {
				return false, fmt.Errorf("failed to marshal data: %w", err)
			}
			var insErr error

			_, insErr = tx.ExecContext(ctx, `
					INSERT INTO `+s.nodesTable()+` (entity_type, id, data)
					VALUES (?, ?, ?)
				`, entity, id, string(jsonData))

			if insErr != nil {
				return false, fmt.Errorf("failed to save entity: %w", insErr)
			}
		}
	}

	// Sync graph edges and FTS index regardless of create/update.
	if err := s.syncGraphEdges(ctx, tx, entity, id, dataCopy); err != nil {
		return false, fmt.Errorf("failed to sync graph: %w", err)
	}
	if !exists {
		// FTS indexing on create — update path already covered by updateInner convention.
		if err := s.indexForFTS(ctx, tx, entity, id, dataCopy); err != nil {
			return false, fmt.Errorf("failed to update FTS index: %w", err)
		}
	}

	return !exists, tx.Commit()
}

// Commit performs an atomic upsert + one or more inserts in a single
// SQLite transaction. The upsert supports optional CAS via Update.Version.
// All operations share one BEGIN/COMMIT boundary; any failure rolls back
// the entire set.
func (s *SQLiteStore) Commit(ctx context.Context, req CommitRequest) (CommitResult, error) {
	var result CommitResult
	// withRetry is intentional here. It retries only on SQLITE_BUSY, which
	// means another writer held the WAL write lock. On a BUSY error the
	// transaction was never committed, so retrying commitInner from scratch
	// is safe. CAS semantics are preserved across retries: if a concurrent
	// writer advances the version between two retry attempts, the subsequent
	// attempt reads the new version and returns ErrConflict (not BUSY), which
	// exits the retry loop immediately and propagates 409 to the caller.
	// A retry cannot silently double-write or mask a conflict.
	err := s.withRetry(func() error {
		var innerErr error
		result, innerErr = s.commitInner(ctx, req)
		return innerErr
	})
	return result, err
}

func (s *SQLiteStore) commitInner(ctx context.Context, req CommitRequest) (CommitResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommitResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	updateResult, err := s.saveInTx(ctx, tx, req.Update)
	if err != nil {
		return CommitResult{}, err
	}

	appended := make([]CommitAppendResult, 0, len(req.Append))
	for _, a := range req.Append {
		id, err := s.createInTx(ctx, tx, a)
		if err != nil {
			return CommitResult{}, err
		}
		appended = append(appended, CommitAppendResult{Entity: a.Entity, ID: id})
	}

	// FSM walk, atomic with the entity write. The walk runs on this same
	// transaction, so a walk failure rolls back the document update and an
	// entity-write failure prevents the state advance. Walk errors carry an
	// XOLU-FSM code; the server maps a walk failure inside a commit to
	// XOLU-FSM008.
	var fsmResult *CommitFsmWalkResult
	if req.FsmWalk != nil {
		wr, werr := s.FsmWalkInTx(ctx, tx, s.config.TenantID,
			int64(req.FsmWalk.Machine), req.FsmWalk.Input, req.FsmWalk.Payload, nil)
		if werr != nil {
			return CommitResult{}, werr
		}
		fsmResult = &CommitFsmWalkResult{
			Machine:    req.FsmWalk.Machine,
			Previous:   wr.Previous,
			Current:    wr.Current,
			Terminal:   wr.Terminal,
			Outputs:    wr.Outputs,
			Vars:       wr.Vars,
			Definition: wr.Definition,
		}
	}

	if err := tx.Commit(); err != nil {
		return CommitResult{}, fmt.Errorf("commit transaction failed: %w", err)
	}

	return CommitResult{Update: updateResult, Appended: appended, FsmWalk: fsmResult}, nil
}

// saveInTx performs the upsert half of a Commit inside an existing transaction.
// It mirrors saveInner but accepts a caller-owned *sql.Tx and returns the
// resulting _version rather than committing.
func (s *SQLiteStore) saveInTx(ctx context.Context, tx *sql.Tx, u CommitUpdate) (CommitUpdateResult, error) {
	dataCopy := make(map[string]interface{}, len(u.Data)+1)
	for k, v := range u.Data {
		if k == "_version" {
			continue
		}
		dataCopy[k] = v
	}
	dataCopy["id"] = u.ID

	spec := s.adapted.Get(u.Entity)

	var exists bool
	var existsErr error
	if spec != nil {
		existsErr = tx.QueryRowContext(ctx, fmt.Sprintf(
			"SELECT EXISTS(SELECT 1 FROM %s WHERE id = ?)",
			spec.TableName()), u.ID).Scan(&exists)
	} else {
		existsErr = tx.QueryRowContext(ctx,
			`SELECT EXISTS(SELECT 1 FROM `+s.nodesTable()+` WHERE `+`entity_type = ? AND id = ?)`,
			u.Entity, u.ID).Scan(&exists)
	}
	if existsErr != nil {
		return CommitUpdateResult{}, fmt.Errorf("saveInTx: existence check: %w", existsErr)
	}

	var newVersion int
	var created bool

	if exists {
		// Overwrite path — conditional or unconditional.
		if spec != nil {
			var expectVersion int
			var hasVersion bool
			if u.Version != nil {
				hasVersion = true
				expectVersion = *u.Version
			}
			if err := adaptedUpdate(ctx, tx, spec, s.dialect, u.ID, dataCopy, expectVersion, hasVersion); err != nil {
				return CommitUpdateResult{}, err
			}
			// Retrieve the new version from the adapted table column.
			if err := tx.QueryRowContext(ctx, fmt.Sprintf(
				"SELECT _version FROM %s WHERE id = ?",
				spec.TableName()), u.ID).Scan(&newVersion); err != nil {
				return CommitUpdateResult{}, fmt.Errorf("saveInTx: read adapted version: %w", err)
			}
		} else {
			jsonData, err := json.Marshal(dataCopy)
			if err != nil {
				return CommitUpdateResult{}, fmt.Errorf("saveInTx: marshal: %w", err)
			}
			if u.Version != nil {
				err = tx.QueryRowContext(ctx,
					`UPDATE `+s.nodesTable()+` SET data = ?, _version = _version + 1, updated_at = CURRENT_TIMESTAMP WHERE `+`entity_type = ? AND id = ? AND _version = ? RETURNING _version`,
					string(jsonData), u.Entity, u.ID, *u.Version).Scan(&newVersion)
			} else {
				err = tx.QueryRowContext(ctx,
					`UPDATE `+s.nodesTable()+` SET data = ?, _version = _version + 1, updated_at = CURRENT_TIMESTAMP WHERE `+`entity_type = ? AND id = ? RETURNING _version`,
					string(jsonData), u.Entity, u.ID).Scan(&newVersion)
			}
			if err == sql.ErrNoRows && u.Version != nil {
				return CommitUpdateResult{}, ErrConflict
			}
			if err != nil {
				return CommitUpdateResult{}, fmt.Errorf("saveInTx: update: %w", err)
			}
		}
		created = false
	} else {
		// Create path — update sequence, then insert.
		var seqErr error

		_, seqErr = tx.ExecContext(ctx, `
				INSERT INTO `+s.nodeSeqTable()+` (entity_type, next_id)
				VALUES (?, ?)
				ON CONFLICT(entity_type) DO UPDATE
				SET next_id = MAX(next_id, excluded.next_id + 1)
			`, u.Entity, u.ID+1)

		if seqErr != nil {
			return CommitUpdateResult{}, fmt.Errorf("saveInTx: sequence: %w", seqErr)
		}
		if spec != nil {
			if err := adaptedCreate(ctx, tx, spec, s.dialect, u.ID, dataCopy); err != nil {
				return CommitUpdateResult{}, err
			}
		} else {
			jsonData, err := json.Marshal(dataCopy)
			if err != nil {
				return CommitUpdateResult{}, fmt.Errorf("saveInTx: marshal: %w", err)
			}
			var insErr error

			_, insErr = tx.ExecContext(ctx, `
					INSERT INTO `+s.nodesTable()+` (entity_type, id, data) VALUES (?, ?, ?)
				`, u.Entity, u.ID, string(jsonData))

			if insErr != nil {
				return CommitUpdateResult{}, fmt.Errorf("saveInTx: insert: %w", insErr)
			}
		}
		newVersion = 1
		created = true
	}

	if err := s.syncGraphEdges(ctx, tx, u.Entity, u.ID, dataCopy); err != nil {
		return CommitUpdateResult{}, fmt.Errorf("saveInTx: graph: %w", err)
	}
	if !exists {
		if err := s.indexForFTS(ctx, tx, u.Entity, u.ID, dataCopy); err != nil {
			return CommitUpdateResult{}, fmt.Errorf("saveInTx: fts: %w", err)
		}
	}

	return CommitUpdateResult{Entity: u.Entity, ID: u.ID, Created: created, Version: newVersion}, nil
}

// createInTx performs one append insert inside an existing transaction.
// If a.ID is nil, an ID is generated from the tenant sequence. If a.ID is
// set and that ID already exists, ErrAlreadyExists is returned.
func (s *SQLiteStore) createInTx(ctx context.Context, tx *sql.Tx, a CommitAppend) (int, error) {
	spec := s.adapted.Get(a.Entity)

	dataCopy := make(map[string]interface{}, len(a.Data)+1)
	for k, v := range a.Data {
		if k == "_version" {
			continue
		}
		dataCopy[k] = v
	}

	var id int
	if a.ID == nil {
		// Auto-generate via sequence.
		seqErr := tx.QueryRowContext(ctx, `
				INSERT INTO `+s.nodeSeqTable()+` (entity_type, next_id)
				VALUES (?, 1)
				ON CONFLICT(entity_type) DO UPDATE SET next_id = next_id + 1
				RETURNING next_id
			`, a.Entity).Scan(&id)

		if seqErr != nil {
			return 0, fmt.Errorf("createInTx: sequence: %w", seqErr)
		}
	} else {
		id = *a.ID
		// Keep sequence ahead of explicit IDs.
		var seqErr error

		_, seqErr = tx.ExecContext(ctx, `
				INSERT INTO `+s.nodeSeqTable()+` (entity_type, next_id)
				VALUES (?, ?)
				ON CONFLICT(entity_type) DO UPDATE
				SET next_id = MAX(next_id, excluded.next_id + 1)
			`, a.Entity, id+1)

		if seqErr != nil {
			return 0, fmt.Errorf("createInTx: sequence bump: %w", seqErr)
		}
	}

	dataCopy["id"] = id

	if spec != nil {
		if err := adaptedCreate(ctx, tx, spec, s.dialect, id, dataCopy); err != nil {
			return 0, err
		}
	} else {
		jsonData, err := json.Marshal(dataCopy)
		if err != nil {
			return 0, fmt.Errorf("createInTx: marshal: %w", err)
		}
		var insErr error

		_, insErr = tx.ExecContext(ctx, `
				INSERT INTO `+s.nodesTable()+` (entity_type, id, data) VALUES (?, ?, ?)
			`, a.Entity, id, string(jsonData))

		if insErr != nil {
			if strings.Contains(insErr.Error(), "UNIQUE constraint failed") {
				return 0, ErrAlreadyExists
			}
			return 0, fmt.Errorf("createInTx: insert: %w", insErr)
		}
	}

	if err := s.syncGraphEdges(ctx, tx, a.Entity, id, dataCopy); err != nil {
		return 0, fmt.Errorf("createInTx: graph: %w", err)
	}
	if err := s.indexForFTS(ctx, tx, a.Entity, id, dataCopy); err != nil {
		return 0, fmt.Errorf("createInTx: fts: %w", err)
	}

	return id, nil
}

// List returns all `+s.nodesTable()+` of a given type
func (s *SQLiteStore) List(ctx context.Context, entity string) ([]map[string]interface{}, error) {

	// Adapted table path
	if spec := s.adapted.Get(entity); spec != nil {
		return adaptedList(ctx, s.readDB, spec, s.dialect)
	}

	rows, err := s.readDB.QueryContext(ctx,
		`SELECT data, _version FROM `+s.nodesTable()+` WHERE `+`entity_type = ? ORDER BY id`,
		entity)
	if err != nil {
		return nil, fmt.Errorf("failed to list "+s.nodesTable()+": %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []map[string]interface{}
	for rows.Next() {
		var jsonData string
		var version int
		if err := rows.Scan(&jsonData, &version); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
			return nil, fmt.Errorf("failed to unmarshal data: %w", err)
		}

		data["_version"] = version
		results = append(results, data)
	}

	return results, rows.Err()
}

// Exists checks if an entity exists
func (s *SQLiteStore) Exists(ctx context.Context, entity string, id int) bool {

	// Adapted table path
	if spec := s.adapted.Get(entity); spec != nil {
		return adaptedExists(ctx, s.readDB, spec, s.dialect, id)
	}

	var exists bool
	err := s.readDB.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM `+s.nodesTable()+` WHERE `+`entity_type = ? AND id = ?)`,
		entity, id).Scan(&exists)

	return err == nil && exists
}

// Ping verifies that the database connection is alive.
func (s *SQLiteStore) Ping(ctx context.Context) error {
	var one int
	return s.readDB.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// Close closes the database connection
func (s *SQLiteStore) Close() error {
	if s.alock != nil {
		s.alock.Stop()
	}
	// Close cached prepared statements before the pools they reference.
	if s.stmtCache != nil {
		s.stmtCache.Close()
	}
	// Close reader pool first (drains in-flight queries), then writer.
	if s.readDB != nil {
		_ = s.readDB.Close()
	}
	return s.db.Close()
}

// Search implements field-based search using JSON extraction
func (s *SQLiteStore) Search(ctx context.Context, entity string, field string, query string, matchType string) ([]map[string]interface{}, error) {

	var sqlQuery string
	var args []interface{}

	switch matchType {
	case "exact":
		sqlQuery = `SELECT data, _version FROM ` + s.nodesTable() + ` WHERE ` + `entity_type = ? AND json_extract(data, '$.' || ?) = ? ORDER BY id`
		args = []interface{}{entity, field, query}
	case "contains":
		sqlQuery = `SELECT data, _version FROM ` + s.nodesTable() + ` WHERE ` + `entity_type = ? AND json_extract(data, '$.' || ?) LIKE ? ORDER BY id`
		args = []interface{}{entity, field, "%" + query + "%"}
	case "starts":
		sqlQuery = `SELECT data, _version FROM ` + s.nodesTable() + ` WHERE ` + `entity_type = ? AND json_extract(data, '$.' || ?) LIKE ? ORDER BY id`
		args = []interface{}{entity, field, query + "%"}
	case "ends":
		sqlQuery = `SELECT data, _version FROM ` + s.nodesTable() + ` WHERE ` + `entity_type = ? AND json_extract(data, '$.' || ?) LIKE ? ORDER BY id`
		args = []interface{}{entity, field, "%" + query}
	default:
		return nil, fmt.Errorf("invalid match type: %s", matchType)
	}

	rows, err := s.readDB.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []map[string]interface{}
	for rows.Next() {
		var jsonData string
		var version int64
		if err := rows.Scan(&jsonData, &version); err != nil {
			return nil, err
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
			return nil, err
		}

		data["_version"] = version
		results = append(results, data)
	}

	return results, rows.Err()
}

// VerifyGraphIntegrity checks whether the tenant edge table matches the REF
// fields in stored entity JSON. Returns a joined error listing every
// discrepancy found (missing edges + unexpected edges); does not stop at the
// ---------------------------------------------------------------------------
// Edge property storage — implements EdgePropertyStore
// ---------------------------------------------------------------------------

// nextEdgeID atomically increments the global edge ID sequence in t<X>_eseq
// and returns the assigned ID. A single row keyed "__global__" provides a
// monotonically increasing ID that is unique across all relationship labels
// within the tenant — required because GetEdge and GetManyEdges look up edges
// by ID without knowing the label in advance.
func (s *SQLiteStore) nextEdgeID(ctx context.Context, tx *sql.Tx, _ string) (int, error) {
	const globalSeqKey = "__global__"
	eseq := tenant.EdgeSeqTableName(s.config.TenantID)
	_, err := tx.ExecContext(ctx,
		"INSERT INTO "+eseq+" (rel, next_id) VALUES (?, 2)"+
			" ON CONFLICT(rel) DO UPDATE SET next_id = next_id + 1",
		globalSeqKey)
	if err != nil {
		return 0, fmt.Errorf("nextEdgeID: upsert global seq: %w", err)
	}
	var id int
	if err := tx.QueryRowContext(ctx,
		"SELECT next_id - 1 FROM "+eseq+" WHERE rel = ?", globalSeqKey,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("nextEdgeID: read global seq: %w", err)
	}
	return id, nil
}

// AddEdgeWithProps persists edge topology and, if props is non-nil and
// non-empty, writes a property blob to t<X>_edges and sets edge_id in
// t<X>_graph. Returns the assigned surrogate edge ID (0 = no props stored).
func (s *SQLiteStore) AddEdgeWithProps(ctx context.Context, from, to, relationship string, props map[string]interface{}) (int, error) {
	if !s.config.GraphEnabled {
		return 0, fmt.Errorf("AddEdgeWithProps: graph not enabled")
	}

	// Auto-register the edge label in t<X>_e_sch before opening the write
	// transaction. RegisterEdgeSchema uses s.db directly, so it must not run
	// inside a tx to avoid a writer deadlock.
	if len(props) > 0 && !s.isEdgeSuppressed(relationship) {
		if registered, _ := s.IsEdgeSchemaRegistered(ctx, relationship); !registered {
			_ = s.RegisterEdgeSchema(ctx, relationship, map[string]interface{}{})
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("AddEdgeWithProps: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	graphTable := tenant.GraphTableName(s.config.TenantID)

	// Parse node IDs: "entity:id" or "XXXX@entity:id"
	parseNode := func(nodeID string) (string, int, error) {
		stripped := tenant.NodeIDStripped(nodeID)
		parts := strings.SplitN(stripped, ":", 2)
		if len(parts) != 2 {
			return "", 0, fmt.Errorf("malformed node ID: %q", nodeID)
		}
		var id int
		if _, err := fmt.Sscanf(parts[1], "%d", &id); err != nil {
			return "", 0, fmt.Errorf("malformed node ID %q: %w", nodeID, err)
		}
		return parts[0], id, nil
	}

	srcEntity, srcID, err := parseNode(from)
	if err != nil {
		return 0, err
	}
	dstEntity, dstID, err := parseNode(to)
	if err != nil {
		return 0, err
	}

	// Determine edge ID: assign one only if there are properties to store.
	var edgeID int
	hasProps := len(props) > 0
	if hasProps {
		// Fire warn-once if this label has no registered schema.
		if !s.isEdgeSuppressed(relationship) {
			s.warnOnceEdge(relationship)
		}

		eid, err := s.nextEdgeID(ctx, tx, relationship)
		if err != nil {
			return 0, err
		}
		edgeID = eid

		if spec := s.edgeSpecFor(relationship); spec != nil {
			// Adapted path: write to t<X>_edata_<label>.
			if err := adaptedCreate(ctx, tx, spec, s.dialect, edgeID, props); err != nil {
				return 0, fmt.Errorf("AddEdgeWithProps: adapted write to %s: %w", spec.TableName(), err)
			}
		} else {
			// Blob path: write to t<X>_edges.
			dataJSON, err := json.Marshal(props)
			if err != nil {
				return 0, fmt.Errorf("AddEdgeWithProps: marshal props: %w", err)
			}
			edgesTable := tenant.EdgePropsTableName(s.config.TenantID)
			_, err = tx.ExecContext(ctx,
				"INSERT INTO "+edgesTable+" (edge_id, rel, data) VALUES (?, ?, ?)"+
					" ON CONFLICT(edge_id) DO UPDATE SET data = excluded.data, _version = _version + 1, updated_at = CURRENT_TIMESTAMP",
				edgeID, relationship, string(dataJSON))
			if err != nil {
				return 0, fmt.Errorf("AddEdgeWithProps: write props: %w", err)
			}
		}
	}

	// Upsert topology row. If edge_id is 0 (no props), the column stays NULL
	// via the COALESCE so that an existing non-NULL edge_id is not overwritten.
	var upsertSQL string
	if hasProps {
		upsertSQL = fmt.Sprintf(
			"INSERT INTO %s (source_entity, source_id, target_entity, target_id, relationship_name, edge_id)"+
				" VALUES (?, ?, ?, ?, ?, ?)"+
				" ON CONFLICT(source_entity, source_id, target_entity, target_id, relationship_name)"+
				" DO UPDATE SET edge_id = excluded.edge_id",
			graphTable)
		_, err = tx.ExecContext(ctx, upsertSQL, srcEntity, srcID, dstEntity, dstID, relationship, edgeID)
	} else {
		upsertSQL = fmt.Sprintf(
			"INSERT OR IGNORE INTO %s (source_entity, source_id, target_entity, target_id, relationship_name)"+
				" VALUES (?, ?, ?, ?, ?)",
			graphTable)
		_, err = tx.ExecContext(ctx, upsertSQL, srcEntity, srcID, dstEntity, dstID, relationship)
	}
	if err != nil {
		return 0, fmt.Errorf("AddEdgeWithProps: upsert topology: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("AddEdgeWithProps: commit: %w", err)
	}

	// Index edge property content in the FTS table after the transaction
	// commits, so that a query failure doesn't roll back the property write.
	// FTS index is best-effort: failures are logged but not returned to caller.
	if hasProps {
		if err := s.IndexEdgeContent(ctx, relationship, edgeID, props); err != nil {
			s.logger.Warn().Err(err).
				Str("rel", relationship).
				Int("edge_id", edgeID).
				Msg("AddEdgeWithProps: FTS indexing failed (non-fatal)")
		}
	}

	return edgeID, nil
}

// GetEdge retrieves property data for a single edge by surrogate ID.
// Routes to the adapted table (t<X>_edata_<label>) when the relationship
// label has an adapted spec; otherwise reads from the blob t<X>_edges table.
func (s *SQLiteStore) GetEdge(ctx context.Context, edgeID int) (*EdgePropsResult, error) {
	if !s.config.GraphEnabled {
		return nil, ErrNotFound
	}

	// Resolve the relationship label from the topology table.
	graphTable := tenant.GraphTableName(s.config.TenantID)
	var rel string
	err := s.readDB.QueryRowContext(ctx,
		"SELECT relationship_name FROM "+graphTable+" WHERE edge_id = ?", edgeID,
	).Scan(&rel)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetEdge(%d): resolve rel: %w", edgeID, err)
	}

	// Adapted path.
	if spec := s.edgeSpecFor(rel); spec != nil {
		data, err := adaptedGet(ctx, s.readDB, spec, s.dialect, edgeID)
		if err != nil {
			return nil, fmt.Errorf("GetEdge(%d): adapted get from %s: %w", edgeID, spec.TableName(), err)
		}
		return &EdgePropsResult{EdgeID: edgeID, Rel: rel, Properties: data}, nil
	}

	// Blob path.
	edgesTable := tenant.EdgePropsTableName(s.config.TenantID)
	var dataJSON string
	err = s.readDB.QueryRowContext(ctx,
		"SELECT data FROM "+edgesTable+" WHERE edge_id = ?", edgeID,
	).Scan(&dataJSON)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("GetEdge(%d): %w", edgeID, err)
	}
	var props map[string]interface{}
	if err := json.Unmarshal([]byte(dataJSON), &props); err != nil {
		return nil, fmt.Errorf("GetEdge(%d): unmarshal: %w", edgeID, err)
	}
	return &EdgePropsResult{EdgeID: edgeID, Rel: rel, Properties: props}, nil
}

// GetManyEdges retrieves property data for multiple edge IDs in one pass.
// Dispatches each ID to adapted or blob path based on the relationship label.
// Edge IDs with no property row are absent from the result map.
func (s *SQLiteStore) GetManyEdges(ctx context.Context, edgeIDs []int) (map[int]*EdgePropsResult, error) {
	if !s.config.GraphEnabled || len(edgeIDs) == 0 {
		return make(map[int]*EdgePropsResult), nil
	}

	result := make(map[int]*EdgePropsResult, len(edgeIDs))

	// Resolve relationship labels for all IDs from the topology table.
	placeholders := make([]string, len(edgeIDs))
	args := make([]interface{}, len(edgeIDs))
	for i, id := range edgeIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	graphTable := tenant.GraphTableName(s.config.TenantID)
	relRows, err := s.readDB.QueryContext(ctx,
		"SELECT edge_id, relationship_name FROM "+graphTable+
			" WHERE edge_id IN ("+strings.Join(placeholders, ",")+")",
		args...)
	if err != nil {
		return nil, fmt.Errorf("GetManyEdges: resolve rels: %w", err)
	}

	// Partition into adapted and blob buckets.
	// adaptedBuckets: spec.TableName() → {spec, []id}
	type bucket struct {
		spec *AdaptedTableSpec
		ids  []int
	}
	adaptedBuckets := make(map[string]*bucket)
	blobIDs := make([]int, 0)
	idToRel := make(map[int]string, len(edgeIDs))

	for relRows.Next() {
		var eid int
		var rel string
		if err := relRows.Scan(&eid, &rel); err != nil {
			_ = relRows.Close()
			return nil, fmt.Errorf("GetManyEdges: scan rel: %w", err)
		}
		idToRel[eid] = rel
		if spec := s.edgeSpecFor(rel); spec != nil {
			tbl := spec.TableName()
			if adaptedBuckets[tbl] == nil {
				adaptedBuckets[tbl] = &bucket{spec: spec}
			}
			adaptedBuckets[tbl].ids = append(adaptedBuckets[tbl].ids, eid)
		} else {
			blobIDs = append(blobIDs, eid)
		}
	}
	_ = relRows.Close()

	// Fetch adapted edges per table.
	for _, b := range adaptedBuckets {
		for _, eid := range b.ids {
			data, err := adaptedGet(ctx, s.readDB, b.spec, s.dialect, eid)
			if err != nil {
				continue // absent rows are silently skipped per contract
			}
			result[eid] = &EdgePropsResult{EdgeID: eid, Rel: idToRel[eid], Properties: data}
		}
	}

	// Fetch blob edges.
	if len(blobIDs) > 0 {
		blobPH := make([]string, len(blobIDs))
		blobArgs := make([]interface{}, len(blobIDs))
		for i, id := range blobIDs {
			blobPH[i] = "?"
			blobArgs[i] = id
		}
		edgesTable := tenant.EdgePropsTableName(s.config.TenantID)
		blobRows, err := s.readDB.QueryContext(ctx,
			"SELECT edge_id, data FROM "+edgesTable+
				" WHERE edge_id IN ("+strings.Join(blobPH, ",")+")",
			blobArgs...)
		if err != nil {
			return nil, fmt.Errorf("GetManyEdges: blob query: %w", err)
		}
		defer func() { _ = blobRows.Close() }()
		for blobRows.Next() {
			var eid int
			var dataJSON string
			if err := blobRows.Scan(&eid, &dataJSON); err != nil {
				return nil, fmt.Errorf("GetManyEdges: blob scan: %w", err)
			}
			var props map[string]interface{}
			if err := json.Unmarshal([]byte(dataJSON), &props); err != nil {
				return nil, fmt.Errorf("GetManyEdges: unmarshal edge %d: %w", eid, err)
			}
			result[eid] = &EdgePropsResult{EdgeID: eid, Rel: idToRel[eid], Properties: props}
		}
	}

	return result, nil
}

// first violation.
//
// Both reads (" + s.nodesTable() + " and edge table) are issued inside a single read
// transaction so that concurrent writes cannot produce false violations.
//
// Memory: only one map is materialised (expected edges derived from entity
// JSON). Actual edges from the edge table are streamed and checked against
// that map rather than accumulated into a second map.
func (s *SQLiteStore) VerifyGraphIntegrity(ctx context.Context) error {
	tx, err := s.readDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("VerifyGraphIntegrity: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Phase 1: build expected edge set from entity JSON.
	expectedEdges := make(map[string]bool)

	entityRows, err := tx.QueryContext(ctx,
		"SELECT entity_type, id, data FROM "+s.nodesTable()+" WHERE 1=1")
	if err != nil {
		return fmt.Errorf("VerifyGraphIntegrity: query "+s.nodesTable()+": %w", err)
	}
	defer func() { _ = entityRows.Close() }()

	for entityRows.Next() {
		var entity string
		var id int
		var jsonData string
		if err := entityRows.Scan(&entity, &id, &jsonData); err != nil {
			return fmt.Errorf("VerifyGraphIntegrity: scan entity: %w", err)
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
			// Unparseable entity: skip — not a graph integrity violation.
			continue
		}
		ees, err := models.ExtractEntityEdges(data)
		if err != nil {
			// Duplicate REF target in stored JSON: this IS a data integrity
			// issue but it belongs in the ExtractEntityEdges error, not here.
			// Collect it as a violation rather than aborting.
			expectedEdges[fmt.Sprintf("CORRUPT[%s:%d]: %v", entity, id, err)] = false
			continue
		}
		for _, ee := range ees {
			key := fmt.Sprintf("%s:%d:%s:%d:%s",
				entity, id, ee.TargetEntity, ee.TargetID, ee.Relationship)
			expectedEdges[key] = false // false = not yet seen in actual edges
		}
	}
	if err := entityRows.Err(); err != nil {
		return fmt.Errorf("VerifyGraphIntegrity: iterate "+s.nodesTable()+": %w", err)
	}

	// Phase 2: stream the actual edge table; mark expected edges as seen and
	// collect any edges that have no expected counterpart.
	edgeTable := tenant.GraphEdgesTableName(s.config.TenantID)
	edgeRows, err := tx.QueryContext(ctx,
		fmt.Sprintf("SELECT source_entity, source_id, target_entity, target_id, relationship_name FROM %s", edgeTable))
	if err != nil {
		return fmt.Errorf("VerifyGraphIntegrity: query edge table: %w", err)
	}
	defer func() { _ = edgeRows.Close() }()

	var violations []string
	for edgeRows.Next() {
		var source, target, rel string
		var sourceID, targetID int
		if err := edgeRows.Scan(&source, &sourceID, &target, &targetID, &rel); err != nil {
			return fmt.Errorf("VerifyGraphIntegrity: scan edge: %w", err)
		}
		key := fmt.Sprintf("%s:%d:%s:%d:%s", source, sourceID, target, targetID, rel)
		if _, expected := expectedEdges[key]; expected {
			expectedEdges[key] = true // mark as seen
		} else {
			violations = append(violations, fmt.Sprintf("unexpected edge: %s", key))
		}
	}
	if err := edgeRows.Err(); err != nil {
		return fmt.Errorf("VerifyGraphIntegrity: iterate edge table: %w", err)
	}

	// Phase 3: any expected edge not marked as seen is missing from the table.
	for key, seen := range expectedEdges {
		if !seen {
			violations = append(violations, fmt.Sprintf("missing edge: %s", key))
		}
	}

	if len(violations) == 0 {
		return nil
	}

	// Sort for deterministic output, then join.
	sortStrings(violations)
	return fmt.Errorf("graph integrity: %d violation(s):\n%s",
		len(violations), strings.Join(violations, "\n"))
}

// sortStrings sorts a string slice in place. Defined here to avoid importing
// sort just for this one use; inlined so the compiler can inline if needed.
func sortStrings(ss []string) { sort.Strings(ss) }

// rebuildBatchSize is the number of edges flushed per INSERT during RebuildGraph.
// SQLite's variable-binding limit is 32766; at 5 columns per row the ceiling is
// ~6500 rows. 500 keeps memory bounded while staying well under that limit.
const rebuildBatchSize = 500

// GraphTenantIDs implements TenantIDLister. It returns all tenant IDs for which
// a graph_tXXXX edge table should be hydrated at startup. Tenant 0 is always
// included first (it is implicit and never appears in the tenants registry
// table). Registered non-zero tenants follow in ascending order.
func (s *SQLiteStore) GraphTenantIDs(ctx context.Context) ([]uint16, error) {
	// Tenant 0 is the implicit default; it is never inserted into the
	// tenants table, so we always prepend it manually.
	ids := []uint16{0}

	rows, err := s.readDB.QueryContext(ctx, "SELECT id FROM tenants WHERE id > 0 ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("GraphTenantIDs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("GraphTenantIDs: scan: %w", err)
		}
		if id > 0 && id <= 65535 {
			ids = append(ids, uint16(id))
		}
	}
	return ids, rows.Err()
}

// ScanGraphEdges implements GraphEdgeScanner. It streams every row from the
// tenant-scoped graph_tXXXX edge table, calling fn once per row. Iteration stops
// on the first non-nil error returned by fn. Rows are read via the reader pool
// (query_only, parallel-safe). All tenants, including tenant 0, use graph_tXXXX.
func (s *SQLiteStore) ScanGraphEdges(ctx context.Context, tenantID uint16, fn func(GraphEdge) error) error {
	table := tenant.GraphEdgesTableName(tenantID)
	rows, err := s.readDB.QueryContext(ctx,
		"SELECT source_entity, source_id, target_entity, target_id, relationship_name, COALESCE(edge_id, 0) FROM "+table)
	if err != nil {
		return fmt.Errorf("ScanGraphEdges: query %s: %w", table, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var e GraphEdge
		if err := rows.Scan(&e.SourceEntity, &e.SourceID, &e.TargetEntity, &e.TargetID, &e.Relationship, &e.EdgeID); err != nil {
			return fmt.Errorf("ScanGraphEdges: scan: %w", err)
		}
		if err := fn(e); err != nil {
			return err
		}
	}
	return rows.Err()
}

// rebuildEdge holds the five columns of one graph edge row collected during rebuild.
type rebuildEdge struct {
	sourceEntity string
	sourceID     int
	targetEntity string
	targetID     int64
	relationship string
}

// RebuildGraph rebuilds the tenant edge table from stored entity JSON.
//
// Correctness: uses models.ExtractEntityEdges for REF extraction so that @REFS
// ([]interface{} of REF maps) and TSREF exclusion are handled identically to
// the live syncGraphEdges path.
//
// Performance: one PrepareContext call outside the row loop; edges are
// accumulated and flushed in batches of rebuildBatchSize rather than one
// ExecContext per edge.
func (s *SQLiteStore) RebuildGraph(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	rebuildTable := tenant.GraphEdgesTableName(s.config.TenantID)

	// Clear existing edges for this tenant.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s", rebuildTable)); err != nil {
		return err
	}

	// Prepare a single-row INSERT statement reused for every flush.
	stmt, err := tx.PrepareContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (source_entity, source_id, target_entity, target_id, relationship_name)
		VALUES (?, ?, ?, ?, ?)
	`, rebuildTable))
	if err != nil {
		return fmt.Errorf("RebuildGraph: prepare insert: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	flushEdges := func(batch []rebuildEdge) error {
		for _, e := range batch {
			if _, err := stmt.ExecContext(ctx, e.sourceEntity, e.sourceID, e.targetEntity, e.targetID, e.relationship); err != nil {
				return fmt.Errorf("RebuildGraph: insert edge: %w", err)
			}
		}
		return nil
	}

	rows, err := tx.QueryContext(ctx,
		"SELECT entity_type, id, data FROM "+s.nodesTable()+" WHERE 1=1")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	var batch []rebuildEdge

	for rows.Next() {
		var entity string
		var id int
		var jsonData string

		if err := rows.Scan(&entity, &id, &jsonData); err != nil {
			return err
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
			// Unparseable entity — skip rather than aborting the whole rebuild.
			continue
		}

		ees, edgeErr := models.ExtractEntityEdges(data)
		if edgeErr != nil {
			// Corrupt entity: duplicate REF targets in stored JSON. Skip the
			// entity rather than aborting the entire rebuild so that the rest
			// of the graph can be salvaged. The integrity checker will report
			// this entity as a violation.
			s.logger.Warn().
				Str("entity", entity).Int("id", id).
				Err(edgeErr).
				Msg("RebuildGraph: skipping entity with duplicate edge target")
			continue
		}
		for _, ee := range ees {
			batch = append(batch, rebuildEdge{
				sourceEntity: entity,
				sourceID:     id,
				targetEntity: ee.TargetEntity,
				targetID:     int64(ee.TargetID),
				relationship: ee.Relationship,
			})
			if len(batch) >= rebuildBatchSize {
				if err := flushEdges(batch); err != nil {
					return err
				}
				batch = batch[:0]
			}
		}
	}

	if err := rows.Err(); err != nil {
		return err
	}

	// Flush any remaining edges below the batch threshold.
	if len(batch) > 0 {
		if err := flushEdges(batch); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// indexForFTS extracts text content from entity and indexes it for full-text search
func (s *SQLiteStore) indexForFTS(ctx context.Context, tx *sql.Tx, entity string, id int, data map[string]interface{}) error {
	// Skip if FTS is not enabled
	if !s.config.FullTextEnabled {
		return nil
	}

	// Convert id to string for FTS storage
	idStr := fmt.Sprintf("%d", id)

	// First, delete any existing FTS entry for this entity
	var ftsDeleteErr error

	_, ftsDeleteErr = tx.ExecContext(ctx, `
			DELETE FROM `+s.nodeFTSTable()+` WHERE entity_type = ? AND entity_id = ?
		`, entity, idStr)

	if ftsDeleteErr != nil {
		return ftsDeleteErr
	}

	// Extract searchable text content from the entity
	content := extractTextContent(data)
	if content == "" {
		return nil // Nothing to index
	}

	// Insert into FTS index
	var ftsInsertErr error

	_, ftsInsertErr = tx.ExecContext(ctx, `
			INSERT INTO `+s.nodeFTSTable()+` (entity_type, entity_id, content)
			VALUES (?, ?, ?)
		`, entity, idStr, content)

	err := ftsInsertErr

	return err
}

// extractTextContent recursively extracts all string values from a map
func extractTextContent(data map[string]interface{}) string {
	var parts []string

	for key, value := range data {
		// Skip internal fields
		if key == "id" || key == "created_at" || key == "updated_at" {
			continue
		}

		switch v := value.(type) {
		case string:
			if v != "" {
				parts = append(parts, v)
			}
		case map[string]interface{}:
			// Skip REF objects — they are structural links, not text content.
			if _, isRef := models.IsReference(v); isRef {
				continue
			}
			if nested := extractTextContent(v); nested != "" {
				parts = append(parts, nested)
			}
		case []interface{}:
			for _, item := range v {
				if str, ok := item.(string); ok && str != "" {
					parts = append(parts, str)
				} else if m, ok := item.(map[string]interface{}); ok {
					if nested := extractTextContent(m); nested != "" {
						parts = append(parts, nested)
					}
				}
			}
		}
	}

	return strings.Join(parts, " ")
}

// FullTextSearch performs a full-text search across " + s.nodesTable() + "
func (s *SQLiteStore) FullTextSearch(ctx context.Context, query string, entity string) ([]map[string]interface{}, error) {

	if query == "" {
		return []map[string]interface{}{}, nil
	}

	// Sanitise query for FTS5 MATCH syntax.
	// FTS5 treats quotes, dashes, semicolons, and other punctuation as
	// syntax operators. Unescaped, they cause parse errors that surface
	// as 500s. We strip everything that isn't alphanumeric, whitespace,
	// or underscore, then collapse runs of whitespace.
	sanitised := sanitiseFTSQuery(query)
	if sanitised == "" {
		// Query was entirely special characters — return empty results
		return []map[string]interface{}{}, nil
	}

	// Add prefix matching with * for partial word matches
	ftsQuery := sanitised + "*"

	var rows *sql.Rows
	var err error

	if entity != "" {
		// Search within specific entity type

		rows, err = s.readDB.QueryContext(ctx, `
				SELECT e.entity_type, e.id, e.data
				FROM `+s.nodeFTSTable()+` fts
				JOIN `+s.nodesTable()+` e ON fts.entity_type = e.entity_type AND CAST(fts.entity_id AS INTEGER) = e.id
				WHERE fts.entity_type = ? AND `+s.nodeFTSTable()+` MATCH ?
				ORDER BY rank LIMIT 100
			`, entity, ftsQuery)

	} else {
		// Search across all `+s.nodesTable()+`

		rows, err = s.readDB.QueryContext(ctx, `
				SELECT e.entity_type, e.id, e.data
				FROM `+s.nodeFTSTable()+` fts
				JOIN `+s.nodesTable()+` e ON fts.entity_type = e.entity_type AND CAST(fts.entity_id AS INTEGER) = e.id
				WHERE `+s.nodeFTSTable()+` MATCH ?
				ORDER BY rank LIMIT 100
			`, ftsQuery)

	}

	if err != nil {
		return nil, fmt.Errorf("full-text search failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []map[string]interface{}
	for rows.Next() {
		var entityType string
		var id int
		var jsonData string

		if err := rows.Scan(&entityType, &id, &jsonData); err != nil {
			return nil, err
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
			continue
		}

		// Add metadata
		data["_entity"] = entityType
		results = append(results, data)
	}

	if results == nil {
		results = []map[string]interface{}{}
	}

	return results, rows.Err()
}

// sanitiseFTSQuery strips FTS5 syntax characters from user input.
// FTS5 interprets quotes, colons, parentheses, dashes, asterisks, carets,
// and other punctuation as query operators. Passing them raw causes parse
// errors. We keep only alphanumeric characters, whitespace, and underscores,
// then collapse multiple spaces into one and trim.
func sanitiseFTSQuery(query string) string {
	var b strings.Builder
	b.Grow(len(query))
	prevSpace := false
	for _, r := range query {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
			prevSpace = false
		} else if r == ' ' || r == '\t' || r == '\n' {
			if !prevSpace && b.Len() > 0 {
				b.WriteByte(' ')
				prevSpace = true
			}
		}
		// All other characters (quotes, semicolons, dashes, etc.) are dropped
	}
	return strings.TrimSpace(b.String())
}

// ---------------------------------------------------------------------------
// Queryable interface — predicate push-down support for the OQL planner
// ---------------------------------------------------------------------------

// Capabilities reports that the SQLite backend can handle WHERE, ORDER BY,
// LIMIT, and COUNT natively via json_extract() push-down.
func (s *SQLiteStore) Capabilities() QueryCapabilities {
	return QueryCapabilities{
		Where:   true,
		OrderBy: true,
		Limit:   true,
		Count:   true,
	}
}

// CountEntities returns the number of records for an entity type without
// fetching the data. This is a single indexed COUNT(*) — typically <10µs.
func (s *SQLiteStore) CountEntities(ctx context.Context, entity string) (int, error) {

	// Adapted table path
	if spec := s.adapted.Get(entity); spec != nil {
		cntSQL := fmt.Sprintf(`SELECT COUNT(*) FROM %s`, spec.TableName())

		stmt, err := s.stmtCache.Get(cntSQL)
		if err != nil {
			return 0, fmt.Errorf("count adapted prepare: %w", err)
		}
		var count int

		err = stmt.QueryRowContext(ctx).Scan(&count)

		if err != nil {
			return 0, fmt.Errorf("count adapted "+s.nodesTable()+": %w", err)
		}
		return count, nil
	}

	cntSQL := `SELECT COUNT(*) FROM ` + s.nodesTable() + ` WHERE ` + `entity_type = ?`
	stmt, err := s.stmtCache.Get(cntSQL)
	if err != nil {
		return 0, fmt.Errorf("count prepare: %w", err)
	}
	var count int
	err = stmt.QueryRowContext(ctx, entity).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count "+s.nodesTable()+": %w", err)
	}
	return count, nil
}

// QueryWithPlan executes a pre-built SQL query (generated by the OQL planner)
// and returns the results as maps, in the same format as List().
func (s *SQLiteStore) QueryWithPlan(ctx context.Context, sqlQuery string, args []interface{}) ([]map[string]interface{}, error) {

	stmt, err := s.stmtCache.Get(sqlQuery)
	if err != nil {
		return nil, fmt.Errorf("push-down prepare failed: %w", err)
	}

	rows, err := stmt.QueryContext(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("push-down query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []map[string]interface{}
	for rows.Next() {
		var jsonData string
		var version int64
		if err := rows.Scan(&jsonData, &version); err != nil {
			return nil, fmt.Errorf("scan push-down row: %w", err)
		}

		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
			return nil, fmt.Errorf("unmarshal push-down data: %w", err)
		}

		data["_version"] = version
		results = append(results, data)
	}

	return results, rows.Err()
}

// Compile-time interface check
var _ Queryable = (*SQLiteStore)(nil)

// Compile-time check: SQLiteStore implements PagedLister
var _ PagedLister = (*SQLiteStore)(nil)

// ListPaged returns a single page of entities plus total count, using
// SQL LIMIT/OFFSET so only the requested page is deserialised.
func (s *SQLiteStore) ListPaged(ctx context.Context, entity string, limit, offset int) (*PagedResult, error) {

	// Adapted table path: delegate to adaptedList and paginate in Go.
	// The adapted table doesn't store data in the `+s.nodesTable()+` table, so the
	// blob-based SQL below would return zero rows.
	if spec := s.adapted.Get(entity); spec != nil {
		all, err := adaptedList(ctx, s.readDB, spec, s.dialect)
		if err != nil {
			return nil, err
		}
		total := len(all)
		if offset >= total {
			return &PagedResult{Data: []map[string]interface{}{}, TotalItems: total}, nil
		}
		end := offset + limit
		if end > total {
			end = total
		}
		return &PagedResult{Data: all[offset:end], TotalItems: total}, nil
	}

	// Count total
	var total int
	err := s.readDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM `+s.nodesTable()+` WHERE `+`entity_type = ?`,
		entity,
	).Scan(&total)
	if err != nil {
		return nil, fmt.Errorf("count "+s.nodesTable()+": %w", err)
	}

	if total == 0 {
		return &PagedResult{Data: []map[string]interface{}{}, TotalItems: 0}, nil
	}

	// Fetch page
	rows, err := s.readDB.QueryContext(ctx,
		`SELECT data, _version FROM `+s.nodesTable()+` WHERE `+`entity_type = ? ORDER BY id LIMIT ? OFFSET ?`,
		entity, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list paged: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var results []map[string]interface{}
	for rows.Next() {
		var jsonData string
		var version int64
		if err := rows.Scan(&jsonData, &version); err != nil {
			return nil, fmt.Errorf("scan paged row: %w", err)
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(jsonData), &data); err != nil {
			return nil, fmt.Errorf("unmarshal paged data: %w", err)
		}
		data["_version"] = version
		results = append(results, data)
	}
	if results == nil {
		results = []map[string]interface{}{}
	}

	return &PagedResult{Data: results, TotalItems: total}, nil
}

// ListEntities returns all distinct entity types in the database.
// It unions blob entities (from the entities table) with adapted " + s.nodesTable() + "
// (from the in-memory registry), so that adapted entity types are visible
// to the OQL validator even when they have no blob rows.
func (s *SQLiteStore) ListEntities(ctx context.Context) ([]string, error) {
	rows, err := s.readDB.QueryContext(ctx,
		"SELECT DISTINCT entity_type FROM "+s.nodesTable()+" WHERE 1=1 ORDER BY entity_type")
	if err != nil {
		return nil, fmt.Errorf("query entity types: %w", err)
	}
	defer func() { _ = rows.Close() }()

	seen := map[string]bool{}
	var entities []string
	for rows.Next() {
		var entityType string
		if err := rows.Scan(&entityType); err != nil {
			return nil, fmt.Errorf("scan entity type: %w", err)
		}
		if !seen[entityType] {
			seen[entityType] = true
			entities = append(entities, entityType)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Also include adapted entities — they live in separate tables and
	// have no rows in the entities table, so the query above misses them.
	for _, name := range s.adapted.Entities() {
		if !seen[name] {
			seen[name] = true
			entities = append(entities, name)
		}
	}

	return entities, nil
}
