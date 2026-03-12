// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// iolu is the administrative CLI for olu. It manages the tenant registry
// and provides operational commands that are deliberately separated from
// the main olu binary's runtime responsibilities.
//
// Usage:
//
//	iolu tenant create --name acme [--id 42] --db /path/to/data.db
//	iolu tenant list   --db /path/to/data.db
//	iolu tenant info   --name acme --db /path/to/data.db [--base-dir /path/to/base]
//	iolu tenant delete --name acme --db /path/to/data.db [--force]
//	iolu version
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

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
		default:
			fmt.Fprintf(os.Stderr, "unknown tenant subcommand: %s\n", os.Args[2])
			printTenantUsage()
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
// tenant create
// ---------------------------------------------------------------------------

func cmdTenantCreate(args []string) {
	fs := flag.NewFlagSet("iolu tenant create", flag.ExitOnError)
	dbPath := fs.String("db", "", "Path to the olu SQLite database (required)")
	name := fs.String("name", "", "Tenant name (required)")
	id := fs.Int("id", 0, "Tenant ID (optional; auto-assigns next available if omitted)")
	_ = fs.Parse(args)

	if *dbPath == "" || *name == "" {
		fs.Usage()
		os.Exit(1)
	}
	if *id < 0 || *id > 65535 {
		fatal("tenant ID must be between 1 and 65535")
	}
	if *id == 0 && *name == "" {
		fatal("--name is required")
	}

	db := openDB(*dbPath)
	defer db.Close()
	ctx := context.Background()

	// Validate name doesn't conflict
	var existingID int
	err := db.QueryRowContext(ctx, `SELECT id FROM tenants WHERE name = ?`, *name).Scan(&existingID)
	if err == nil {
		fatal("tenant %q already exists with ID %d", *name, existingID)
	} else if err != sql.ErrNoRows {
		fatal("query tenant name: %v", err)
	}

	tenantID := uint16(*id)
	if tenantID == 0 {
		// Auto-assign: find the highest existing ID and add 1
		var maxID sql.NullInt64
		if err := db.QueryRowContext(ctx, `SELECT MAX(id) FROM tenants`).Scan(&maxID); err != nil {
			fatal("query max tenant ID: %v", err)
		}
		if maxID.Valid {
			next := maxID.Int64 + 1
			if next > 65535 {
				fatal("tenant registry full: maximum 65535 tenants reached")
			}
			tenantID = uint16(next)
		} else {
			tenantID = 1
		}
	} else {
		// Explicit ID: check it isn't taken
		var existingName string
		err := db.QueryRowContext(ctx, `SELECT name FROM tenants WHERE id = ?`, int(tenantID)).Scan(&existingName)
		if err == nil {
			fatal("tenant ID %d already assigned to %q", tenantID, existingName)
		} else if err != sql.ErrNoRows {
			fatal("query tenant ID: %v", err)
		}
	}

	_, err = db.ExecContext(ctx,
		`INSERT INTO tenants (id, name) VALUES (?, ?)`,
		int(tenantID), *name)
	if err != nil {
		fatal("insert tenant: %v", err)
	}

	fmt.Printf("created tenant %q with ID %d\n", *name, tenantID)
}

// ---------------------------------------------------------------------------
// tenant list
// ---------------------------------------------------------------------------

func cmdTenantList(args []string) {
	fs := flag.NewFlagSet("iolu tenant list", flag.ExitOnError)
	dbPath := fs.String("db", "", "Path to the olu SQLite database (required)")
	_ = fs.Parse(args)

	if *dbPath == "" {
		fs.Usage()
		os.Exit(1)
	}

	db := openDB(*dbPath)
	defer db.Close()
	ctx := context.Background()

	rows, err := db.QueryContext(ctx, `
		SELECT t.id, t.name, t.created_at,
		       COALESCE(e.cnt, 0) AS entity_count
		FROM tenants t
		LEFT JOIN (
			SELECT tenant_id, COUNT(*) AS cnt
			FROM entities
			GROUP BY tenant_id
		) e ON e.tenant_id = t.id
		ORDER BY t.id`)
	if err != nil {
		fatal("query tenants: %v", err)
	}
	defer rows.Close()

	fmt.Printf("%-6s  %-24s  %-20s  %s\n", "ID", "NAME", "CREATED", "ENTITIES")
	fmt.Printf("%-6s  %-24s  %-20s  %s\n", "------", "------------------------", "--------------------", "--------")

	count := 0
	for rows.Next() {
		var id int
		var name string
		var createdAt sql.NullString
		var entityCount int
		if err := rows.Scan(&id, &name, &createdAt, &entityCount); err != nil {
			fatal("scan tenant row: %v", err)
		}
		created := "-"
		if createdAt.Valid {
			created = createdAt.String
		}
		fmt.Printf("%-6d  %-24s  %-20s  %d\n", id, name, created, entityCount)
		count++
	}
	if err := rows.Err(); err != nil {
		fatal("iterate tenants: %v", err)
	}

	fmt.Printf("\n%d tenant(s)\n", count)
}

// ---------------------------------------------------------------------------
// tenant info
// ---------------------------------------------------------------------------

func cmdTenantInfo(args []string) {
	fs := flag.NewFlagSet("iolu tenant info", flag.ExitOnError)
	dbPath := fs.String("db", "", "Path to the olu SQLite database (required)")
	name := fs.String("name", "", "Tenant name (required)")
	baseDir := fs.String("base-dir", "", "olu base directory (default: directory containing --db)")
	_ = fs.Parse(args)

	if *dbPath == "" || *name == "" {
		fs.Usage()
		os.Exit(1)
	}

	db := openDB(*dbPath)
	defer db.Close()
	ctx := context.Background()

	// Look up tenant
	var id int
	var createdAt sql.NullString
	err := db.QueryRowContext(ctx,
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
	fmt.Printf("Tenant:      %s\n", *name)
	fmt.Printf("ID:          %d\n", id)
	fmt.Printf("Created:     %s\n", created)

	// Entity breakdown by type
	rows, err := db.QueryContext(ctx, `
		SELECT entity_type, COUNT(*) AS cnt
		FROM entities
		WHERE tenant_id = ?
		GROUP BY entity_type
		ORDER BY entity_type`, id)
	if err != nil {
		fatal("query entities: %v", err)
	}
	defer rows.Close()

	fmt.Printf("\nEntities:\n")
	total := 0
	any := false
	for rows.Next() {
		var entityType string
		var cnt int
		if err := rows.Scan(&entityType, &cnt); err != nil {
			fatal("scan entity row: %v", err)
		}
		fmt.Printf("  %-20s  %d\n", entityType, cnt)
		total += cnt
		any = true
	}
	if err := rows.Err(); err != nil {
		fatal("iterate entities: %v", err)
	}
	if !any {
		fmt.Printf("  (none)\n")
	}
	fmt.Printf("  %-20s  %d\n", "TOTAL", total)

	// Timeseries check
	base := *baseDir
	if base == "" {
		base = filepath.Dir(*dbPath)
	}
	tsDir := filepath.Join(base, "ts", fmt.Sprintf("t%04x", id))
	if info, err := os.Stat(tsDir); err == nil && info.IsDir() {
		size := dirSize(tsDir)
		fmt.Printf("\nTimeseries:  provisioned (%s)\n", formatBytes(size))
	} else {
		fmt.Printf("\nTimeseries:  not provisioned\n")
	}
}

// ---------------------------------------------------------------------------
// tenant delete
// ---------------------------------------------------------------------------

func cmdTenantDelete(args []string) {
	fs := flag.NewFlagSet("iolu tenant delete", flag.ExitOnError)
	dbPath := fs.String("db", "", "Path to the olu SQLite database (required)")
	name := fs.String("name", "", "Tenant name (required)")
	force := fs.Bool("force", false, "Delete even if entity data exists (data becomes orphaned)")
	_ = fs.Parse(args)

	if *dbPath == "" || *name == "" {
		fs.Usage()
		os.Exit(1)
	}

	db := openDB(*dbPath)
	defer db.Close()
	ctx := context.Background()

	// Look up tenant
	var id int
	err := db.QueryRowContext(ctx, `SELECT id FROM tenants WHERE name = ?`, *name).Scan(&id)
	if err == sql.ErrNoRows {
		fatal("tenant %q not found", *name)
	} else if err != nil {
		fatal("query tenant: %v", err)
	}

	// Check for existing entity data
	var entityCount int
	err = db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM entities WHERE tenant_id = ?`, id).Scan(&entityCount)
	if err != nil {
		fatal("count entities: %v", err)
	}

	if entityCount > 0 && !*force {
		fatal("tenant %q (ID %d) has %d entities. Use --force to delete anyway.\n"+
			"WARNING: entity data will be orphaned (tenant_id=%d in entities table).",
			*name, id, entityCount, id)
	}

	if entityCount > 0 {
		fmt.Fprintf(os.Stderr, "WARNING: tenant %q has %d entities that will be orphaned.\n", *name, entityCount)
	}

	result, err := db.ExecContext(ctx, `DELETE FROM tenants WHERE id = ?`, id)
	if err != nil {
		fatal("delete tenant: %v", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		fatal("tenant disappeared during delete (concurrent modification?)")
	}

	fmt.Printf("deleted tenant %q (ID %d)\n", *name, id)
	if entityCount > 0 {
		fmt.Printf("NOTE: %d orphaned entities remain in the entities table with tenant_id=%d\n", entityCount, id)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func openDB(path string) *sql.DB {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fatal("database not found: %s", path)
	}

	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		fatal("open database: %v", err)
	}

	// Verify connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		fatal("ping database: %v", err)
	}

	return db
}

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

func printUsage() {
	w := os.Stderr
	fmt.Fprintf(w, "iolu %s — olu administrative CLI\n\n", version.Version)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  iolu tenant <command> [flags]")
	fmt.Fprintln(w, "  iolu version")
	fmt.Fprintln(w, "  iolu help")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Tenant commands:")
	fmt.Fprintln(w, "  create    Register a new tenant")
	fmt.Fprintln(w, "  list      List all tenants")
	fmt.Fprintln(w, "  info      Show details for a tenant")
	fmt.Fprintln(w, "  delete    Remove a tenant from the registry")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global flags:")
	fmt.Fprintln(w, "  --db      Path to the olu SQLite database (required for tenant commands)")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Examples:")
	fmt.Fprintln(w, "  iolu tenant create --db data.db --name acme")
	fmt.Fprintln(w, "  iolu tenant create --db data.db --name acme --id 42")
	fmt.Fprintln(w, "  iolu tenant list   --db data.db")
	fmt.Fprintln(w, "  iolu tenant info   --db data.db --name acme")
	fmt.Fprintln(w, "  iolu tenant delete --db data.db --name acme")
	fmt.Fprintln(w, "  iolu tenant delete --db data.db --name acme --force")
}

func printTenantUsage() {
	w := os.Stderr
	fmt.Fprintln(w, "Usage: iolu tenant <command> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	lines := []struct{ cmd, desc string }{
		{"create", "Register a new tenant"},
		{"list", "List all tenants"},
		{"info", "Show details for a tenant"},
		{"delete", "Remove a tenant from the registry"},
	}
	for _, l := range lines {
		fmt.Fprintf(w, "  %-10s  %s\n", l.cmd, l.desc)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run 'iolu tenant <command> --help' for command-specific flags.")
}
