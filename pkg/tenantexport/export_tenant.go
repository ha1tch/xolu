// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package tenantexport

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ha1tch/xolu/pkg/blob"
	"github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// PrimaryStoreTables is every table in a tenant's own primary store
// (store/xolu.db, shared or per-file per SQLitePerFileTenants) that
// holds real tenant data -- verified directly against each subsystem's
// own real query/schema code (grep for actual WHERE tenant_id=... use
// or, where no query happened to be caught that way, the literal
// CREATE TABLE itself -- cal_participants specifically was confirmed
// via schema, not a query match), not assumed from naming convention
// alone.
//
// Deliberately excluded, and why:
//   - t0000_efts*/t0000_nfts* (full-text search indexes): derived,
//     rebuildable from the entity data already being exported: not
//     source data.
//   - schema_version, schema_version_v2, tenants: server/system-level
//     tables, not scoped to any one tenant.
//
// This list is authoritative until a new subsystem adds tables of its
// own -- extend it there, not by guessing the pattern holds.
var PrimaryStoreTables = []SQLiteTableSpec{
	// Entity/graph core. Each has its own specific TenantID invariant
	// method -- NOT a uniform prefix+suffix shape (NodeSchemaTableName
	// returns "..._n_sch", not "..._node_schema" or similar), so
	// NameFunc calls the exact method rather than reconstructing the
	// same logic by hand. n_sch specifically is created lazily (only
	// once a node-adapted schema is registered), unlike most of its
	// siblings -- ExportSQLiteTable treats a missing table as zero
	// rows for exactly this reason, not an error.
	{Name: "nodes", NameFunc: func(t uint16) string { return tenant.TenantID(t).NodesTableName() }},
	{Name: "edges", NameFunc: func(t uint16) string { return tenant.TenantID(t).EdgePropsTableName() }},
	{Name: "graph", NameFunc: func(t uint16) string { return tenant.TenantID(t).GraphTableName() }},
	{Name: "eseq", NameFunc: func(t uint16) string { return tenant.TenantID(t).EdgeSeqTableName() }},
	{Name: "nseq", NameFunc: func(t uint16) string { return tenant.TenantID(t).NodeSeqTableName() }},
	{Name: "e_sch", NameFunc: func(t uint16) string { return tenant.TenantID(t).EdgeSchemaTableName() }},
	{Name: "n_sch", NameFunc: func(t uint16) string { return tenant.TenantID(t).NodeSchemaTableName() }},
	// bal -- confirmed directly (pkg/bal/store.go's own
	// accountsTable/journalTable/balancesTable and siblings) as a
	// plain "s.prefix + literal suffix" with no abbreviation
	// surprises, unlike the entity/graph tables above -- TenantPrefixed
	// is the correct, still-invariant-respecting tool here since bal
	// exposes no public per-table method of its own to call instead.
	{Name: "bal_accounts", TenantPrefixed: true},
	{Name: "bal_balances", TenantPrefixed: true},
	{Name: "bal_checkpoints", TenantPrefixed: true},
	{Name: "bal_journal", TenantPrefixed: true},
	{Name: "bal_seal", TenantPrefixed: true},
	// cal -- global tables, tenant_id-filtered (pkg/cal/sqlitesource.go
	// and pkg/storage/sqlite.go's own CREATE TABLE, confirmed directly).
	{Name: "cal_bookings", TenantFiltered: true},
	{Name: "cal_calendars", TenantFiltered: true},
	{Name: "cal_participants", TenantFiltered: true},
	{Name: "cal_ord_seq", TenantFiltered: true},
	// dxp -- global, tenant_id-filtered.
	{Name: "dxp_defs", TenantFiltered: true},
	{Name: "dxp_id_seq", TenantFiltered: true},
	{Name: "dxp_txn", TenantFiltered: true},
	// event -- global, tenant_id-filtered.
	{Name: "event_defs", TenantFiltered: true},
	{Name: "event_delivery_log", TenantFiltered: true},
	// fsm -- global, tenant_id-filtered.
	{Name: "fsm_definitions", TenantFiltered: true},
	{Name: "fsm_history", TenantFiltered: true},
	{Name: "fsm_id_seq", TenantFiltered: true},
	{Name: "fsm_machines", TenantFiltered: true},
	{Name: "fsm_terminal_states", TenantFiltered: true},
	// gen (named generators/sequences) -- global, tenant_id-filtered.
	{Name: "gen_definitions", TenantFiltered: true},
	{Name: "sequences", TenantFiltered: true},
	// entity_meta -- global, tenant_id-filtered (own comment in
	// pkg/server/v2_meta_handlers.go says so explicitly).
	{Name: "entity_meta", TenantFiltered: true},
}

// LocStoreTables is every table in loc's own dedicated per-tenant file
// (storelayout.TenantLocDir) -- the whole FILE is already one tenant's
// own (T-115, wave 9), so none of these need a tenant_id filter, unlike
// PrimaryStoreTables' global tables.
var LocStoreTables = []SQLiteTableSpec{
	{Name: "locations", TenantFiltered: false},
	{Name: "loc_patterns", TenantFiltered: false},
	{Name: "loc_capacity", TenantFiltered: false},
	{Name: "fences", TenantFiltered: false},
	{Name: "loc_fence_capacity", TenantFiltered: false},
	{Name: "loc_fence_membership", TenantFiltered: false},
	{Name: "loc_assignment", TenantFiltered: false},
	{Name: "loc_journal", TenantFiltered: false},
}

// ObjStoreTables is every table in obj's own dedicated per-tenant file
// (storelayout.TenantObjDir) -- same reasoning as LocStoreTables.
var ObjStoreTables = []SQLiteTableSpec{
	{Name: "obj_subjects", TenantFiltered: false},
	{Name: "obj_position", TenantFiltered: false},
	{Name: "obj_journal", TenantFiltered: false},
}

// ExportTenant runs a complete export for one tenant: every table in
// its primary store (filtered where the table is shared, unfiltered
// where it's already tenant-scoped), every table in its dedicated
// loc/obj files if those files exist, every per-tenant Pebble store
// (ts, cal's occupancy index, bal's rollup) if that store's directory
// exists, packages the whole collection into one zip, and stores it as
// a blob under exportKey via bs.
//
// Staging happens in a fresh temp directory created directly under the
// tenant's own root (storelayout.TenantRoot) -- not the OS temp
// directory -- per the design settled directly with the team: keeping
// staging co-located with the tenant's own data rather than a shared
// system temp path. Removed unconditionally on return, success or
// failure, via defer -- a failed export must not leave partial JSON
// files behind under the tenant's own directory.
//
// primaryDB is the *sql.DB already open against this tenant's primary
// store (the caller's own handle -- either the tenant's per-file db or
// the shared db, per SQLitePerFileTenants; ExportTenant does not open
// or care which mode is in effect, it just queries what it's given).
func ExportTenant(ctx context.Context, primaryDB *sql.DB, basePath string, tenantID tenant.TenantID, bs *blob.Store, exportKey string) (*PackageResult, error) {
	tenantRoot := storelayout.TenantRoot(basePath, tenantID)
	stagingDir, err := os.MkdirTemp(tenantRoot, "export-staging-")
	if err != nil {
		return nil, fmt.Errorf("tenantexport: create staging dir under %s: %w", tenantRoot, err)
	}
	defer func() { _ = os.RemoveAll(stagingDir) }()

	tid := uint16(tenantID)

	if _, err := ExportSQLiteTables(ctx, primaryDB, tid, PrimaryStoreTables, stagingDir); err != nil {
		return nil, fmt.Errorf("tenantexport: primary store: %w", err)
	}

	if err := exportDedicatedFile(ctx, storelayout.TenantLocDir(basePath, tenantID), "loc.db", LocStoreTables, tid, stagingDir); err != nil {
		return nil, fmt.Errorf("tenantexport: loc store: %w", err)
	}
	if err := exportDedicatedFile(ctx, storelayout.TenantObjDir(basePath, tenantID), "obj.db", ObjStoreTables, tid, stagingDir); err != nil {
		return nil, fmt.Errorf("tenantexport: obj store: %w", err)
	}

	pebbleSpecs := []PebbleStoreSpec{
		{Dir: storelayout.TenantTSDir(basePath, tenantID), Name: "ts"},
		{Dir: storelayout.TenantCalDir(basePath, tenantID), Name: "cal_index"},
		{Dir: storelayout.TenantBalRollupDir(basePath, tenantID), Name: "bal_rollup"},
	}
	if _, err := ExportPebbleStores(ctx, pebbleSpecs, stagingDir); err != nil {
		return nil, fmt.Errorf("tenantexport: pebble stores: %w", err)
	}

	return PackageAndStore(ctx, stagingDir, bs, exportKey)
}

// exportDedicatedFile opens primitiveDir/dbFileName (loc's/obj's own
// file, confirmed directly against their real construction code --
// dir+"/loc.db" and dir+"/obj.db" respectively in
// pkg/server/v2_loc_handlers.go and v2_obj_handlers.go; NOT a
// store/xolu.db layout, which was an unverified assumption caught and
// fixed before this ever ran against a real file) read-only and
// exports every table in specs, unfiltered. If the file does not
// exist (the primitive has never been used by this tenant), this is a
// no-op, not an error -- matching ExportPebbleStores' own treatment of
// a never-used store.
func exportDedicatedFile(ctx context.Context, primitiveDir, dbFileName string, specs []SQLiteTableSpec, tenantID uint16, stagingDir string) error {
	dbPath := filepath.Join(primitiveDir, dbFileName)
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil
	}

	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer func() { _ = db.Close() }()

	_, err = ExportSQLiteTables(ctx, db, tenantID, specs, stagingDir)
	return err
}
