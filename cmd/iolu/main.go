// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// iolu — interactive xolu — is the administrative CLI. It manages the tenant registry
// and provides operational commands that are deliberately separated from
// the main xolu binary's runtime responsibilities.
//
// All schema creation and data access goes through pkg/storage.NewSQLiteStore,
// the same codepath the running server uses. This guarantees that iolu and xolu
// always agree on table names, DDL, and pragmas — the table-name functions in
// pkg/tenant are the single source of truth for both.
//
// Usage:
//
//	iolu db init     --base-dir /path/to/data [--tenant name[:id]] [--graph] [--mode per-file|shared]
//	iolu db status   --base-dir /path/to/data [--mode per-file|shared]
//	iolu ts status   --base-dir /path/to/data [--tenant <id>]
//	iolu db upgrade  --base-dir /path/to/data [--mode per-file|shared]
//
//	iolu bal prune   --base-dir /path/to/data --before <RFC3339> [--yes]
//
//	iolu tenant create       --base-dir /path/to/data --name <name> [--id <n>] [--graph=false]
//	iolu tenant list         --base-dir /path/to/data
//	iolu tenant info         --base-dir /path/to/data --name <name>
//	iolu tenant delete       --base-dir /path/to/data --name <name> [--force]
//	iolu tenant provision-ts  --base-dir /path/to/data --name <name>
//	iolu tenant provision-cal --base-dir /path/to/data --name <name>
//
//	iolu version
//	iolu help
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
	"github.com/ha1tch/xolu/pkg/timeseries"
	"github.com/ha1tch/xolu/pkg/version"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Printf("iolu %s\n", version.Version)
	case "db":
		if len(os.Args) < 3 {
			printDBUsage()
			os.Exit(1)
		}
		switch os.Args[2] {
		case "init":
			cmdDBInit(os.Args[3:])
		case "status":
			cmdDBStatus(os.Args[3:])
		case "upgrade":
			cmdDBUpgrade(os.Args[3:])
		case "check":
			cmdDBCheck(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "unknown db subcommand: %s\n", os.Args[2])
			printDBUsage()
			os.Exit(1)
		}
	case "tenant":
		if len(os.Args) < 3 {
			printTenantUsage()
			os.Exit(1)
		}
		switch os.Args[2] {
		case "create":
			cmdTenantCreate(os.Args[3:])
		case "list":
			cmdTenantList(os.Args[3:])
		case "info":
			cmdTenantInfo(os.Args[3:])
		case "delete":
			cmdTenantDelete(os.Args[3:])
		case "provision-ts":
			cmdTenantProvisionTS(os.Args[3:])
		case "provision-cal":
			cmdTenantProvisionCal(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "unknown tenant subcommand: %s\n", os.Args[2])
			printTenantUsage()
			os.Exit(1)
		}
	case "ts":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: iolu ts status --base-dir <dir> [--tenant <id>]")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "status":
			cmdTSStatus(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "unknown ts subcommand: %s\n", os.Args[2])
			os.Exit(1)
		}
	case "bal":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: iolu bal prune --base-dir <dir> --before <RFC3339> [--yes]")
			os.Exit(1)
		}
		switch os.Args[2] {
		case "prune":
			cmdBalPrune(os.Args[3:])
		default:
			fmt.Fprintf(os.Stderr, "unknown bal subcommand: %s\n", os.Args[2])
			os.Exit(1)
		}
	case "help", "--help", "-h":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// db init
// ---------------------------------------------------------------------------

func cmdDBInit(args []string) {
	fs := flag.NewFlagSet("iolu db init", flag.ExitOnError)
	baseDir := fs.String("base-dir", "", "xolu data root to initialise (required)")
	mode := fs.String("mode", "per-file", "store organisation: per-file or shared")
	graph := fs.Bool("graph", true, "Create graph edge tables for tenant 0")
	provisionTS := fs.Bool("provision-dirs", true, "Create per-tenant ts/ and blobs/ directories")
	var tenants tenantFlags
	fs.Var(&tenants, "tenant", "Register a tenant: --tenant name or --tenant name:id (repeatable)")
	_ = fs.Parse(args)

	if *baseDir == "" {
		fs.Usage()
		os.Exit(1)
	}
	storeMode, err := resolveStoreMode(*baseDir, *mode, modePerFile)
	if err != nil {
		fatal("%v", err)
	}
	// Refuse to clobber an already-initialised root: the tenant-0 store file
	// existing is the signal that this base dir is in use.
	if _, err := os.Stat(storePathFor(*baseDir, 0, storeMode)); err == nil {
		fatal("data root already initialised: %s\n  use 'iolu db upgrade' to apply migrations", *baseDir)
	}

	fmt.Printf("initialising %s (%s store mode)\n", *baseDir, storeMode)

	// Open via pkg/storage for tenant 0 — this runs createSchema + initialize,
	// producing the correct global tables and tenant-0 table family at the
	// layout-derived path.
	store0, err := openTenantStore(*baseDir, 0, storeMode, *graph)
	if err != nil {
		fatal("init database: %v", err)
	}
	defer func() { _ = store0.Close() }()

	db := store0.DB()
	ctx := context.Background()

	fmt.Printf("  \u2713  core schema (tenants, schema_version)\n")
	fmt.Printf("  \u2713  tenant-0 tables (%s, %s, %s)\n",
		tenant.TenantID(0).NodesTableName(), tenant.TenantID(0).NodeSeqTableName(), tenant.TenantID(0).NodeFTSTableName())
	if *graph {
		fmt.Printf("  \u2713  graph topology table (%s)\n", tenant.TenantID(0).GraphTableName())
	}
	if *provisionTS {
		if err := provisionTenantDirs(*baseDir, 0); err != nil {
			fatal("provision tenant-0 directories: %v", err)
		}
	}

	for _, t := range tenants {
		id, err := registerTenant(ctx, db, t.name, t.id)
		if err != nil {
			fatal("register tenant %q: %v", t.name, err)
		}
		// Open via pkg/storage for this tenant — creates its table family
		// (a separate file in per-file mode, the shared file in shared mode).
		ts, err := openTenantStore(*baseDir, id, storeMode, *graph)
		if err != nil {
			fatal("init tables for tenant %q: %v", t.name, err)
		}
		_ = ts.Close()

		tsNote := ""
		if *provisionTS {
			if err := provisionTenantDirs(*baseDir, id); err != nil {
				fatal("provision directories for tenant %q: %v", t.name, err)
			}
			tsNote = "  dirs=provisioned"
		}
		fmt.Printf("  \u2713  tenant %-20s  id=%-5d  graph=%v%s\n", fmt.Sprintf("%q", t.name), id, *graph, tsNote)
	}

	fmt.Printf("\ndone — data root ready for xolu in strict mode\n")
}

// ---------------------------------------------------------------------------
// db status
// ---------------------------------------------------------------------------

func cmdDBStatus(args []string) {
	fs := flag.NewFlagSet("iolu db status", flag.ExitOnError)
	baseDir := fs.String("base-dir", "", "xolu data root (required)")
	mode := fs.String("mode", "", "store organisation override: per-file or shared (default: auto-detect)")
	_ = fs.Parse(args)

	if *baseDir == "" {
		fs.Usage()
		os.Exit(1)
	}
	storeMode, err := resolveStoreMode(*baseDir, *mode, modePerFile)
	if err != nil {
		fatal("%v", err)
	}
	dbPath := storePathFor(*baseDir, 0, storeMode)

	// Open read-only via pkg/storage for tenant 0 (no graph table creation).
	store, err := openTenantStore(*baseDir, 0, storeMode, false)
	if err != nil {
		fatal("open database: %v", err)
	}
	defer func() { _ = store.Close() }()
	db := store.DB()
	ctx := context.Background()

	info, _ := os.Stat(dbPath)
	walPath := dbPath + "-wal"
	walSize := int64(0)
	if wi, err := os.Stat(walPath); err == nil {
		walSize = wi.Size()
	}

	fmt.Printf("Data root:   %s\n", *baseDir)
	fmt.Printf("Store mode:  %s\n", storeMode)
	fmt.Printf("Database:    %s\n", dbPath)
	if info != nil {
		fmt.Printf("Size:        %s  (WAL: %s)\n", formatBytes(info.Size()), formatBytes(walSize))
		fmt.Printf("Modified:    %s\n", info.ModTime().Format(time.RFC3339))
	}
	fmt.Println()

	// Schema versions.
	var versions []string
	rows, _ := db.QueryContext(ctx, `SELECT version FROM schema_version ORDER BY version`)
	if rows != nil {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var v int
			if err := rows.Scan(&v); err != nil {
				continue
			}
			versions = append(versions, fmt.Sprintf("%d", v))
		}
	}
	if len(versions) > 0 {
		fmt.Printf("Schema:      versions %s\n", strings.Join(versions, ", "))
	} else {
		fmt.Printf("Schema:      \u26a0  no schema_version table\n")
	}
	fmt.Println()

	// Tenants — count nodes from each tenant's own table.
	fmt.Printf("Tenants:\n")
	tRows, err := db.QueryContext(ctx, `SELECT id, name, created_at FROM tenants ORDER BY id`)
	if err != nil {
		fmt.Printf("  \u26a0  could not query tenants table: %v\n", err)
	} else {
		var buffered []tenantRow
		for tRows.Next() {
			var id int
			var name string
			var createdAt sql.NullString
			if err := tRows.Scan(&id, &name, &createdAt); err != nil {
				continue
			}
			created := "-"
			if createdAt.Valid {
				created = createdAt.String
			}
			buffered = append(buffered, tenantRow{id: id, name: name, created: created})
		}
		_ = tRows.Close()
		if len(buffered) == 0 {
			fmt.Printf("  (no tenants registered)\n")
		}
		for _, tr := range buffered {
			nodeCount := tenantNodeCount(*baseDir, tenant.TenantID(tr.id), storeMode, db)
			fmt.Printf("  %-5d  %-24s  %-20s  %d nodes\n", tr.id, tr.name, tr.created, nodeCount)
		}
	}
	fmt.Println()

	// SQL table listings below are read from the open store. In shared mode
	// this is the whole database; in per-file mode it is tenant 0's store only
	// (each other tenant's tables live in its own file — see the Tenants
	// section above for per-tenant counts).
	scopeNote := ""
	if storeMode == modePerFile {
		scopeNote = "  (tenant 0 store)"
	}

	// Per-tenant node tables present in the open store.
	fmt.Printf("Per-tenant tables:%s\n", scopeNote)
	ptNames := queryTableNames(ctx, db,
		`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 't%\_nodes' ESCAPE '\' ORDER BY name`)
	if len(ptNames) == 0 {
		fmt.Printf("  (none)\n")
	}
	for _, name := range ptNames {
		fmt.Printf("  %-32s  %d rows\n", name, tableCount(ctx, db, name))
	}
	fmt.Println()

	// Graph tables present in the open store.
	fmt.Printf("Graph tables:%s\n", scopeNote)
	gtNames := queryTableNames(ctx, db,
		`SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 't%\_graph' ESCAPE '\' ORDER BY name`)
	if len(gtNames) == 0 {
		fmt.Printf("  (none)\n")
	}
	for _, name := range gtNames {
		fmt.Printf("  %-32s  %d edges\n", name, tableCount(ctx, db, name))
	}
	fmt.Println()

	// Per-tenant storage planes (timeseries and blobs), discovered from disk
	// by the normalized layout.
	fmt.Printf("Per-tenant storage:\n")
	entries, _ := os.ReadDir(*baseDir)
	anyPlane := false
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id, ok := sl.ParseTenantSegment(e.Name())
		if !ok {
			continue
		}
		tsDir := sl.TenantTSDir(*baseDir, id)
		blobDir := sl.TenantBlobDir(*baseDir, id)
		tsNote, blobNote := "-", "-"
		if fi, err := os.Stat(tsDir); err == nil && fi.IsDir() {
			tsNote = formatBytes(dirSize(tsDir))
		}
		if fi, err := os.Stat(blobDir); err == nil && fi.IsDir() {
			blobNote = formatBytes(dirSize(blobDir))
		}
		fmt.Printf("  %-6s  ts=%-10s  blobs=%-10s\n", e.Name(), tsNote, blobNote)
		anyPlane = true
	}
	if !anyPlane {
		fmt.Printf("  (no per-tenant ts/ or blobs/ directories)\n")
	}
}

// ---------------------------------------------------------------------------
// db upgrade
// ---------------------------------------------------------------------------

func cmdDBUpgrade(args []string) {
	fs := flag.NewFlagSet("iolu db upgrade", flag.ExitOnError)
	baseDir := fs.String("base-dir", "", "xolu data root (required)")
	mode := fs.String("mode", "", "store organisation override: per-file or shared (default: auto-detect)")
	graph := fs.Bool("graph", false, "Also ensure graph topology tables exist for all tenants")
	_ = fs.Parse(args)

	if *baseDir == "" {
		fs.Usage()
		os.Exit(1)
	}
	storeMode, err := resolveStoreMode(*baseDir, *mode, modePerFile)
	if err != nil {
		fatal("%v", err)
	}

	fmt.Printf("upgrading %s (%s store mode)\n", *baseDir, storeMode)

	// Open for tenant 0 to ensure global + tenant-0 tables exist.
	store0, err := openTenantStore(*baseDir, 0, storeMode, *graph)
	if err != nil {
		fatal("open database: %v", err)
	}
	defer func() { _ = store0.Close() }()
	db := store0.DB()
	ctx := context.Background()

	fmt.Printf("  \u2713  global tables present (schema_version, tenants)\n")
	fmt.Printf("  \u2713  tenant-0 tables present\n")

	// For each registered tenant, open a store scoped to that tenant so that
	// its per-tenant table family is created/verified by pkg/storage.
	tRows, err := db.QueryContext(ctx, `SELECT id FROM tenants ORDER BY id`)
	if err != nil {
		fatal("enumerate tenants: %v", err)
	}
	defer func() { _ = tRows.Close() }()

	for tRows.Next() {
		var id int
		if err := tRows.Scan(&id); err != nil {
			continue
		}
		tid := tenant.TenantID(id)
		ts, err := openTenantStore(*baseDir, tid, storeMode, *graph)
		if err != nil {
			fatal("upgrade tables for tenant %04X: %v", tid, err)
		}
		_ = ts.Close()
		fmt.Printf("  \u2713  tenant %04X tables present\n", tid)
	}

	fmt.Printf("\ndone — database at latest schema\n")
}

// ---------------------------------------------------------------------------
// tenant create
// ---------------------------------------------------------------------------

func cmdTenantCreate(args []string) {
	fs := flag.NewFlagSet("iolu tenant create", flag.ExitOnError)
	baseDir := fs.String("base-dir", "", "xolu data root (required)")
	mode := fs.String("mode", "", "store organisation override: per-file or shared (default: auto-detect)")
	name := fs.String("name", "", "Tenant name (required)")
	id := fs.Int("id", 0, "Tenant ID (optional; auto-assigns next available if omitted)")
	graph := fs.Bool("graph", true, "also create the tenant's own entity/graph tables (default: true, matching "+
		"the server's own GraphEnabled default) -- without this, the tenant is registered but has no storage of "+
		"its own until its first write, and the server logs a hydration warning for it on every boot until then")
	_ = fs.Parse(args)

	if *baseDir == "" || *name == "" {
		fs.Usage()
		os.Exit(1)
	}
	if *id < 0 || *id > 65535 {
		fatal("tenant ID must be between 1 and 65535")
	}
	storeMode, err := resolveStoreMode(*baseDir, *mode, modePerFile)
	if err != nil {
		fatal("%v", err)
	}

	store, err := openTenantStore(*baseDir, 0, storeMode, false)
	if err != nil {
		fatal("open database: %v", err)
	}
	defer func() { _ = store.Close() }()
	db := store.DB()
	ctx := context.Background()

	tenantID, err := registerTenant(ctx, db, *name, tenant.TenantID(*id))
	if err != nil {
		fatal("%v", err)
	}
	fmt.Printf("created tenant %q with ID %d\n", *name, tenantID)

	// Registering the tenant only inserts its row into the shared
	// `tenants` table -- it does not, on its own, create that tenant's
	// own t<XXXX>_* table family (nodes, edges, graph, sequences).
	// Those are created by SQLiteStore.initialize() -> createSchema(),
	// which runs whenever a store is OPENED for a given TenantID -- and
	// the store opened just above is scoped to tenant 0, not the new
	// tenant, so none of that has happened yet for tenantID here.
	//
	// Left unfixed, the new tenant is registered but has no storage of
	// its own: the server's own boot-time graph hydration
	// (loadEntitiesFromEdgeTable in cmd/xolu/main.go) enumerates every
	// registered tenant and tries to scan its graph table, hits "no
	// such table" for this one, and logs a WARN -- every single boot,
	// for as long as the tenant goes without a first write. That's
	// real, misleading noise under completely normal, correct usage
	// (a freshly created, not-yet-used tenant), not a corner case to
	// document around. Opening a second store scoped to the tenant's
	// own ID triggers the same table creation the server would run on
	// that tenant's first write, just proactively -- a "created"
	// tenant should genuinely be a complete, ready one, not a registry
	// row plus a promise.
	//
	// In shared mode this opens a second connection to the SAME file
	// (fine under WAL, which openTenantStore already enables) with a
	// different TenantID in its own config, so createSchema targets
	// t<newID>_* rather than t0000_*. In per-file mode it's a genuinely
	// separate file, same as any other tenant's own store.
	if *graph {
		tenantStore, err := openTenantStore(*baseDir, tenantID, storeMode, true)
		if err != nil {
			fatal("tenant %q was registered (ID %d) but its own tables could not be created: %v\n"+
				"the tenant registry entry now exists without matching storage -- either retry, or "+
				"delete the registry row (iolu tenant delete --name %s) and start over", *name, tenantID, err, *name)
		}
		_ = tenantStore.Close()
		fmt.Printf("provisioned entity/graph tables for tenant %q\n", *name)
	}
}

// ---------------------------------------------------------------------------
// tenant list
// ---------------------------------------------------------------------------

func cmdTenantList(args []string) {
	fs := flag.NewFlagSet("iolu tenant list", flag.ExitOnError)
	baseDir := fs.String("base-dir", "", "xolu data root (required)")
	mode := fs.String("mode", "", "store organisation override: per-file or shared (default: auto-detect)")
	_ = fs.Parse(args)

	if *baseDir == "" {
		fs.Usage()
		os.Exit(1)
	}
	storeMode, err := resolveStoreMode(*baseDir, *mode, modePerFile)
	if err != nil {
		fatal("%v", err)
	}

	store, err := openTenantStore(*baseDir, 0, storeMode, false)
	if err != nil {
		fatal("open database: %v", err)
	}
	defer func() { _ = store.Close() }()
	db := store.DB()
	ctx := context.Background()

	// Buffer all registry rows first, then close the cursor before issuing any
	// per-tenant count queries. In per-file mode each count opens a separate
	// store; iterating the open cursor while doing so would block the
	// connection.
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, created_at FROM tenants ORDER BY id`)
	if err != nil {
		fatal("query tenants: %v", err)
	}
	var trows []tenantRow
	for rows.Next() {
		var id int
		var name string
		var createdAt sql.NullString
		if err := rows.Scan(&id, &name, &createdAt); err != nil {
			_ = rows.Close()
			fatal("scan tenant row: %v", err)
		}
		created := "-"
		if createdAt.Valid {
			created = createdAt.String
		}
		trows = append(trows, tenantRow{id: id, name: name, created: created})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		fatal("iterate tenants: %v", err)
	}
	_ = rows.Close()

	fmt.Printf("%-6s  %-24s  %-20s  %s\n", "ID", "NAME", "CREATED", "NODES")
	fmt.Printf("%-6s  %-24s  %-20s  %s\n", "------", "------------------------", "--------------------", "--------")

	for _, tr := range trows {
		nodeCount := tenantNodeCount(*baseDir, tenant.TenantID(tr.id), storeMode, db)
		fmt.Printf("%-6d  %-24s  %-20s  %d\n", tr.id, tr.name, tr.created, nodeCount)
	}

	fmt.Printf("\n%d tenant(s)\n", len(trows))
}

// ---------------------------------------------------------------------------
// tenant info
// ---------------------------------------------------------------------------

func cmdTenantInfo(args []string) {
	fs := flag.NewFlagSet("iolu tenant info", flag.ExitOnError)
	baseDir := fs.String("base-dir", "", "xolu data root (required)")
	mode := fs.String("mode", "", "store organisation override: per-file or shared (default: auto-detect)")
	name := fs.String("name", "", "Tenant name (required)")
	_ = fs.Parse(args)

	if *baseDir == "" || *name == "" {
		fs.Usage()
		os.Exit(1)
	}
	storeMode, err := resolveStoreMode(*baseDir, *mode, modePerFile)
	if err != nil {
		fatal("%v", err)
	}

	store, err := openTenantStore(*baseDir, 0, storeMode, false)
	if err != nil {
		fatal("open database: %v", err)
	}
	defer func() { _ = store.Close() }()
	db := store.DB()
	ctx := context.Background()

	var id int
	var createdAt sql.NullString
	err = db.QueryRowContext(ctx,
		`SELECT id, created_at FROM tenants WHERE name = ?`, *name).Scan(&id, &createdAt)
	if err == sql.ErrNoRows {
		fatal("tenant %q not found", *name)
	} else if err != nil {
		fatal("query tenant: %v", err)
	}

	created := "-"
	if createdAt.Valid {
		created = createdAt.String
	}
	tid := tenant.TenantID(id)
	nodesTable := tid.NodesTableName()

	// In per-file mode a non-zero tenant's tables live in its own store file,
	// not tenant 0's. Resolve the DB that actually holds this tenant's tables.
	tdb := db
	if storeMode == modePerFile && tid != 0 {
		path := sl.TenantStorePath(*baseDir, tid)
		if _, statErr := os.Stat(path); statErr != nil {
			fatal("tenant %q (ID %d) store not found at %s", *name, id, path)
		}
		ts, err := openTenantStore(*baseDir, tid, storeMode, false)
		if err != nil {
			fatal("open tenant store: %v", err)
		}
		defer func() { _ = ts.Close() }()
		tdb = ts.DB()
	}

	fmt.Printf("Tenant:      %s\n", *name)
	fmt.Printf("ID:          %d\n", id)
	fmt.Printf("Created:     %s\n", created)

	entityRows, err := tdb.QueryContext(ctx,
		fmt.Sprintf(`SELECT entity_type, COUNT(*) AS cnt FROM %s GROUP BY entity_type ORDER BY entity_type`,
			nodesTable))
	if err != nil {
		fatal("query entities: %v", err)
	}
	defer func() { _ = entityRows.Close() }()

	fmt.Printf("\nEntities:\n")
	total := 0
	any := false
	for entityRows.Next() {
		var entityType string
		var cnt int
		if err := entityRows.Scan(&entityType, &cnt); err != nil {
			fatal("scan entity row: %v", err)
		}
		fmt.Printf("  %-20s  %d\n", entityType, cnt)
		total += cnt
		any = true
	}
	if err := entityRows.Err(); err != nil {
		fatal("iterate entities: %v", err)
	}
	if !any {
		fmt.Printf("  (none)\n")
	}
	fmt.Printf("  %-20s  %d\n", "TOTAL", total)

	graphTable := tid.GraphTableName()
	edgeCount := tableCount(ctx, tdb, graphTable)
	fmt.Printf("\nGraph:       %s  (%d edges)\n", graphTable, edgeCount)

	tsDir := sl.TenantTSDir(*baseDir, tid)
	if info, err := os.Stat(tsDir); err == nil && info.IsDir() {
		fmt.Printf("Timeseries:  provisioned at %s  (%s)\n", tsDir, formatBytes(dirSize(tsDir)))
	} else {
		fmt.Printf("Timeseries:  not provisioned\n")
		fmt.Printf("             hint: iolu tenant provision-ts --base-dir %s --name %s\n",
			*baseDir, *name)
	}

	blobDir := sl.TenantBlobDir(*baseDir, tid)
	if info, err := os.Stat(blobDir); err == nil && info.IsDir() {
		fmt.Printf("Blobs:       provisioned at %s  (%s)\n", blobDir, formatBytes(dirSize(blobDir)))
	} else {
		fmt.Printf("Blobs:       not provisioned\n")
	}

	calDir := sl.TenantCalDir(*baseDir, tid)
	if info, err := os.Stat(calDir); err == nil && info.IsDir() {
		fmt.Printf("Cal index:   provisioned at %s  (%s)\n", calDir, formatBytes(dirSize(calDir)))
	} else {
		fmt.Printf("Cal index:   not provisioned\n")
		fmt.Printf("             hint: iolu tenant provision-cal --base-dir %s --name %s\n",
			*baseDir, *name)
	}
}

// ---------------------------------------------------------------------------
// tenant delete
// ---------------------------------------------------------------------------

func cmdTenantDelete(args []string) {
	fs := flag.NewFlagSet("iolu tenant delete", flag.ExitOnError)
	baseDir := fs.String("base-dir", "", "xolu data root (required)")
	mode := fs.String("mode", "", "store organisation override: per-file or shared (default: auto-detect)")
	name := fs.String("name", "", "Tenant name (required)")
	force := fs.Bool("force", false, "Delete even if node data exists (data tables are dropped)")
	_ = fs.Parse(args)

	if *baseDir == "" || *name == "" {
		fs.Usage()
		os.Exit(1)
	}
	storeMode, err := resolveStoreMode(*baseDir, *mode, modePerFile)
	if err != nil {
		fatal("%v", err)
	}

	store, err := openTenantStore(*baseDir, 0, storeMode, false)
	if err != nil {
		fatal("open database: %v", err)
	}
	defer func() { _ = store.Close() }()
	db := store.DB()
	ctx := context.Background()

	var id int
	err = db.QueryRowContext(ctx, `SELECT id FROM tenants WHERE name = ?`, *name).Scan(&id)
	if err == sql.ErrNoRows {
		fatal("tenant %q not found", *name)
	} else if err != nil {
		fatal("query tenant: %v", err)
	}

	tid := tenant.TenantID(id)
	nodesTable := tid.NodesTableName()

	var nodeCount int
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT COUNT(*) FROM %s`, nodesTable)).Scan(&nodeCount); err != nil {
		nodeCount = 0 // table may not exist yet
	}

	if nodeCount > 0 && !*force {
		fatal("tenant %q (ID %d) has %d nodes in %s. Use --force to delete and drop all tenant tables.",
			*name, id, nodeCount, nodesTable)
	}
	if nodeCount > 0 {
		fmt.Fprintf(os.Stderr, "WARNING: tenant %q has %d nodes — all tenant tables will be dropped.\n", *name, nodeCount)
	}

	result, err := db.ExecContext(ctx, `DELETE FROM tenants WHERE id = ?`, id)
	if err != nil {
		fatal("delete tenant: %v", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		fatal("tenant disappeared during delete (concurrent modification?)")
	}

	// Remove on-disk state. In per-file mode the tenant's entire directory
	// (store + ts + blobs) is removed. In shared mode the tenant's tables live
	// in the shared store file and are dropped below; only its ts/ and blobs/
	// directories are removed here.
	if storeMode == modePerFile {
		tenantRoot := sl.TenantRoot(*baseDir, tid)
		if err := os.RemoveAll(tenantRoot); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: could not remove tenant directory %s: %v\n", tenantRoot, err)
		}
	} else {
		for _, d := range []string{sl.TenantTSDir(*baseDir, tid), sl.TenantBlobDir(*baseDir, tid)} {
			if err := os.RemoveAll(d); err != nil {
				fmt.Fprintf(os.Stderr, "WARNING: could not remove %s: %v\n", d, err)
			}
		}
		// Drop the full per-tenant table family from the shared store via the
		// authoritative name functions.
		for _, tbl := range []string{
			tid.NodeFTSTableName(),
			tid.EdgeFTSTableName(),
			tid.NodesTableName(),
			tid.NodeSeqTableName(),
			tid.EdgePropsTableName(),
			tid.EdgeSeqTableName(),
			tid.GraphTableName(),
			tid.NodeSchemaTableName(),
			tid.EdgeSchemaTableName(),
		} {
			_, _ = db.ExecContext(ctx, fmt.Sprintf("DROP TABLE IF EXISTS %s", tbl))
		}
	}

	fmt.Printf("deleted tenant %q (ID %d) and removed its storage\n", *name, id)
}

// ---------------------------------------------------------------------------
// tenant provision-ts
// ---------------------------------------------------------------------------

func cmdTenantProvisionTS(args []string) {
	fs := flag.NewFlagSet("iolu tenant provision-ts", flag.ExitOnError)
	baseDir := fs.String("base-dir", "", "xolu data root (required)")
	mode := fs.String("mode", "", "store organisation override: per-file or shared (default: auto-detect)")
	name := fs.String("name", "", "Tenant name (required)")
	_ = fs.Parse(args)

	if *baseDir == "" || *name == "" {
		fs.Usage()
		os.Exit(1)
	}
	storeMode, err := resolveStoreMode(*baseDir, *mode, modePerFile)
	if err != nil {
		fatal("%v", err)
	}

	store, err := openTenantStore(*baseDir, 0, storeMode, false)
	if err != nil {
		fatal("open database: %v", err)
	}
	defer func() { _ = store.Close() }()
	db := store.DB()
	ctx := context.Background()

	var id int
	err = db.QueryRowContext(ctx, `SELECT id FROM tenants WHERE name = ?`, *name).Scan(&id)
	if err == sql.ErrNoRows {
		fatal("tenant %q not found", *name)
	} else if err != nil {
		fatal("query tenant: %v", err)
	}

	tid := tenant.TenantID(id)
	if err := provisionTenantDirs(*baseDir, tid); err != nil {
		fatal("provision tenant directories: %v", err)
	}
	fmt.Printf("provisioned ts/ and blobs/ for tenant %q (ID %d) at %s\n",
		*name, id, sl.TenantRoot(*baseDir, tid))
}

// ---------------------------------------------------------------------------
// ts status
// ---------------------------------------------------------------------------

// tsStoreMeta mirrors the fields of timeseries.storeMeta that iolu reads
// directly from meta.json. Reading the JSON avoids opening (and locking)
// the Pebble store just to report metadata. Only the fields iolu displays
// are declared; unknown fields are ignored by encoding/json.
type tsStoreMeta struct {
	CreatedAt    string                  `json:"created_at"`
	SysmaskWidth timeseries.SysmaskWidth `json:"sysmask_width"`
}

// cmdTSStatus reports each tenant's timeseries store metadata — notably
// the immutable sysmask width (@S §4, R10). db status covers the SQL
// storage layer; the sysmask width lives in the ts Pebble store's
// meta.json, a separate directory tree, so it is surfaced here.
func cmdTSStatus(args []string) {
	fs := flag.NewFlagSet("iolu ts status", flag.ExitOnError)
	baseDir := fs.String("base-dir", "", "xolu data root (required)")
	tenantID := fs.Int("tenant", -1, "restrict to one tenant ID (default: all provisioned)")
	_ = fs.Parse(args)

	if *baseDir == "" {
		fs.Usage()
		os.Exit(1)
	}

	fmt.Printf("Data root:   %s\n\n", *baseDir)

	// Determine which tenant IDs to inspect: one, or every tXXXX dir that
	// carries a ts/ store.
	var ids []tenant.TenantID
	if *tenantID >= 0 {
		if *tenantID > 0xFFFF {
			fatal("tenant ID %d out of range", *tenantID)
		}
		ids = []tenant.TenantID{tenant.TenantID(*tenantID)}
	} else {
		ids = discoverTSTenants(*baseDir)
		if len(ids) == 0 {
			fmt.Println("No provisioned timeseries stores found.")
			return
		}
	}

	for _, id := range ids {
		tsDir := sl.TenantTSDir(*baseDir, id)
		metaPath := filepath.Join(tsDir, "meta.json")
		data, err := os.ReadFile(metaPath)
		if err != nil {
			fmt.Printf("Tenant %d (t%04X): no ts store (%s)\n", id, id, "meta.json absent")
			continue
		}
		var meta tsStoreMeta
		if err := json.Unmarshal(data, &meta); err != nil {
			fmt.Printf("Tenant %d (t%04X): meta.json unreadable: %v\n", id, id, err)
			continue
		}
		if !meta.SysmaskWidth.Valid() {
			fmt.Printf("Tenant %d (t%04X): \u26a0  invalid sysmask width %d in meta\n",
				id, id, uint8(meta.SysmaskWidth))
			continue
		}
		fmt.Printf("Tenant %d (t%04X):\n", id, id)
		fmt.Printf("  ts dir:        %s\n", tsDir)
		if meta.CreatedAt != "" {
			fmt.Printf("  created:       %s\n", meta.CreatedAt)
		}
		fmt.Printf("  sysmask width: %s\n\n", meta.SysmaskWidth.String())
	}
}

// discoverTSTenants returns the tenant IDs under baseDir that carry a
// ts/ store directory. Best-effort: unreadable base dir yields nil.
func discoverTSTenants(baseDir string) []tenant.TenantID {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}
	var ids []tenant.TenantID
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id, ok := sl.ParseTenantSegment(e.Name())
		if !ok {
			continue
		}
		if _, err := os.Stat(sl.TenantTSDir(baseDir, id)); err == nil {
			ids = append(ids, id)
		}
	}
	return ids
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func registerTenant(ctx context.Context, db *sql.DB, name string, id tenant.TenantID) (tenant.TenantID, error) {
	var existingID int
	err := db.QueryRowContext(ctx, `SELECT id FROM tenants WHERE name = ?`, name).Scan(&existingID)
	if err == nil {
		return 0, fmt.Errorf("tenant %q already exists with ID %d", name, existingID)
	} else if err != sql.ErrNoRows {
		return 0, fmt.Errorf("query tenant name: %w", err)
	}
	if id == 0 {
		var maxID sql.NullInt64
		_ = db.QueryRowContext(ctx, `SELECT MAX(id) FROM tenants`).Scan(&maxID)
		if maxID.Valid {
			next := maxID.Int64 + 1
			if next > 65535 {
				return 0, fmt.Errorf("tenant registry full")
			}
			id = tenant.TenantID(next)
		} else {
			id = 1
		}
	} else {
		var existingName string
		err := db.QueryRowContext(ctx, `SELECT name FROM tenants WHERE id = ?`, int(id)).Scan(&existingName)
		if err == nil {
			return 0, fmt.Errorf("tenant ID %d already assigned to %q", id, existingName)
		} else if err != sql.ErrNoRows {
			return 0, fmt.Errorf("query tenant ID: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO tenants (id, name) VALUES (?, ?)`, int(id), name); err != nil {
		return 0, fmt.Errorf("insert tenant: %w", err)
	}
	return id, nil
}

// provisionTenantDirs creates the per-tenant timeseries and blob directories
// under the data root, derived by pkg/storelayout (tenant-first layout, uniform
// with the running server). Both planes are always per-tenant.
func provisionTenantDirs(base string, tenantID tenant.TenantID) error {
	if err := os.MkdirAll(sl.TenantTSDir(base, tenantID), 0755); err != nil {
		return fmt.Errorf("create ts dir: %w", err)
	}
	if err := os.MkdirAll(sl.TenantBlobDir(base, tenantID), 0755); err != nil {
		return fmt.Errorf("create blobs dir: %w", err)
	}
	return nil
}

// tenantNodeCount returns the node count for a tenant. In shared mode the
// tenant's nodes table lives in the same (tenant-0) store, so sharedDB is used
// directly. In per-file mode the tenant's tables live in its own store file,
// which is opened read-only for the count. A missing table yields 0.
func tenantNodeCount(base string, tid tenant.TenantID, mode storeMode, sharedDB *sql.DB) int {
	ctx := context.Background()
	if mode == modeShared {
		return tableCount(ctx, sharedDB, tid.NodesTableName())
	}
	// Per-file: tenant 0's nodes are in the already-open store; others need
	// their own file opened.
	if tid == 0 {
		return tableCount(ctx, sharedDB, tenant.TenantID(0).NodesTableName())
	}
	path := sl.TenantStorePath(base, tid)
	if _, err := os.Stat(path); err != nil {
		return 0 // store not present (tenant registered but never initialised)
	}
	ts, err := openTenantStore(base, tid, mode, false)
	if err != nil {
		return 0
	}
	defer func() { _ = ts.Close() }()
	return tableCount(ctx, ts.DB(), tid.NodesTableName())
}

// tenantRow is a buffered tenant registry row.
type tenantRow struct {
	id      int
	name    string
	created string
}

// queryTableNames runs a single-column name query and returns all rows,
// closing the cursor before returning so callers may issue further queries on
// the same connection without blocking it.
func queryTableNames(ctx context.Context, db *sql.DB, query string) []string {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		names = append(names, name)
	}
	return names
}

func tableCount(ctx context.Context, db *sql.DB, table string) int {
	var count int
	_ = db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count)
	return count
}

// ---------------------------------------------------------------------------
// tenantFlags — repeatable --tenant name[:id] flag
// ---------------------------------------------------------------------------

type tenantEntry struct {
	name string
	id   tenant.TenantID
}

type tenantFlags []tenantEntry

func (t *tenantFlags) String() string { return fmt.Sprintf("%v", *t) }

func (t *tenantFlags) Set(val string) error {
	parts := strings.SplitN(val, ":", 2)
	name := parts[0]
	if name == "" {
		return fmt.Errorf("tenant name must not be empty")
	}
	var id tenant.TenantID
	if len(parts) == 2 {
		n, err := strconv.ParseUint(parts[1], 10, 16)
		if err != nil || n == 0 {
			return fmt.Errorf("tenant ID must be between 1 and 65535")
		}
		id = tenant.TenantID(n)
	}
	*t = append(*t, tenantEntry{name: name, id: id})
	return nil
}

// ---------------------------------------------------------------------------
// Filesystem helpers
// ---------------------------------------------------------------------------

func dirSize(path string) int64 {
	var size int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		size += info.Size()
		return nil
	})
	return size
}

func formatBytes(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(b)/float64(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "iolu: "+format+"\n", args...)
	os.Exit(1)
}

// ---------------------------------------------------------------------------
// Usage
// ---------------------------------------------------------------------------

func printUsage() {
	w := os.Stderr
	_, _ = fmt.Fprintf(w, "iolu %s — interactive xolu\n\n", version.Version)
	_, _ = fmt.Fprintln(w, "Usage:")
	_, _ = fmt.Fprintln(w, "  iolu db     <command> [flags]")
	_, _ = fmt.Fprintln(w, "  iolu tenant <command> [flags]")
	_, _ = fmt.Fprintln(w, "  iolu ts     <command> [flags]")
	_, _ = fmt.Fprintln(w, "  iolu bal    <command> [flags]")
	_, _ = fmt.Fprintln(w, "  iolu version")
	_, _ = fmt.Fprintln(w, "  iolu help")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Database commands (operate on a stopped server):")
	for _, l := range []struct{ cmd, desc string }{
		{"init", "Create a new xolu database with schema and optional tenants"},
		{"status", "Show schema version, table counts, tenants, graph, timeseries"},
		{"upgrade", "Apply pending schema migrations to an existing database"},
	} {
		_, _ = fmt.Fprintf(w, "  %-14s  %s\n", l.cmd, l.desc)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Tenant commands:")
	for _, l := range []struct{ cmd, desc string }{
		{"create", "Register a new tenant"},
		{"list", "List all tenants"},
		{"info", "Show tenant details, entity breakdown, graph and timeseries status"},
		{"delete", "Remove a tenant from the registry and drop all tenant tables"},
		{"provision-ts", "Provision timeseries storage directory for a tenant"},
		{"provision-cal", "Provision cal occupancy index for a tenant"},
	} {
		_, _ = fmt.Fprintf(w, "  %-14s  %s\n", l.cmd, l.desc)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Timeseries commands:")
	for _, l := range []struct{ cmd, desc string }{
		{"status", "Show each tenant's ts store metadata, including sysmask width"},
	} {
		_, _ = fmt.Fprintf(w, "  %-14s  %s\n", l.cmd, l.desc)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Examples:")
	_, _ = fmt.Fprintln(w, "  iolu db init   --base-dir /data --tenant acme --tenant beta:2 --graph")
	_, _ = fmt.Fprintln(w, "  iolu db status --base-dir /data")
	_, _ = fmt.Fprintln(w, "  iolu ts status --base-dir /data")
	_, _ = fmt.Fprintln(w, "  iolu db upgrade --base-dir /data")
	_, _ = fmt.Fprintln(w, "  iolu tenant create       --base-dir /data --name acme")
	_, _ = fmt.Fprintln(w, "  iolu tenant list         --base-dir /data")
	_, _ = fmt.Fprintln(w, "  iolu tenant info         --base-dir /data --name acme")
	_, _ = fmt.Fprintln(w, "  iolu tenant provision-ts --base-dir /data --name acme")
	_, _ = fmt.Fprintln(w, "  iolu tenant provision-cal --base-dir /data --name acme")
	_, _ = fmt.Fprintln(w, "  iolu tenant delete       --base-dir /data --name acme --force")
}

func printDBUsage() {
	w := os.Stderr
	_, _ = fmt.Fprintln(w, "Usage: iolu db <command> [flags]")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Commands:")
	for _, l := range []struct{ cmd, desc string }{
		{"init", "Create a new xolu database"},
		{"status", "Show database status"},
		{"check", "Run rebuild oracles: derived state == authoritative record"},
		{"upgrade", "Apply pending schema migrations"},
	} {
		_, _ = fmt.Fprintf(w, "  %-10s  %s\n", l.cmd, l.desc)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Run 'iolu db <command> --help' for command-specific flags.")
}

func printTenantUsage() {
	w := os.Stderr
	_, _ = fmt.Fprintln(w, "Usage: iolu tenant <command> [flags]")
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Commands:")
	for _, l := range []struct{ cmd, desc string }{
		{"create", "Register a new tenant"},
		{"list", "List all tenants"},
		{"info", "Show details for a tenant"},
		{"delete", "Remove a tenant from the registry"},
		{"provision-ts", "Provision timeseries storage for a tenant"},
		{"provision-cal", "Provision cal occupancy index for a tenant"},
	} {
		_, _ = fmt.Fprintf(w, "  %-14s  %s\n", l.cmd, l.desc)
	}
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Run 'iolu tenant <command> --help' for command-specific flags.")
}
