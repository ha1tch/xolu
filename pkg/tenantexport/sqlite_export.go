// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package tenantexport implements per-tenant data export as a pair of
// iterate-and-serialize algorithms, one per storage engine xolu uses
// (SQLite, Pebble) -- not a reconstruction of a valid subset database
// file. Each source (a SQLite table, a Pebble store) becomes one JSON
// file: every row or key/value pair in that source, serialized, no
// schema/constraint/index fidelity attempted or needed.
//
// Design settled directly with Horacio (2026-08-03), correcting an
// earlier, substantially overcomplicated draft that tried to
// reconstruct a byte-valid SQLite file containing only one tenant's
// tables (ATTACH + CREATE TABLE AS SELECT, preserving schema/indexes).
// That problem doesn't need solving: a JSON export doesn't care about
// SQL structure fidelity, only about the data itself.
//
// See docs/proposals/tenant-export.md for the full design (async job
// flow, blob-backed result, TTL, throttling). Wired into
// pkg/server/blob_export_handlers.go's POST/GET .../blob/export
// routes.
package tenantexport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ha1tch/xolu/pkg/tenant"
)

// SQLiteTableSpec describes one table to export.
type SQLiteTableSpec struct {
	// Name is a label for this table, used for the output JSON
	// filename and in error messages. When NameFunc is nil, Name is
	// ALSO the literal table name to query (optionally prefixed, see
	// TenantPrefixed) -- when NameFunc is set, Name is label-only and
	// NameFunc alone determines the real table name.
	Name string
	// NameFunc, when set, is the authoritative source for this
	// table's real name -- e.g. tenant.TenantID.NodesTableName,
	// which this package MUST call directly rather than re-derive the
	// same "prefix + suffix" pattern by hand. This is not a style
	// preference: a hand-rolled version of this exact logic already
	// caused two real bugs in this package (a missing table,
	// NodeSchemaTableName's own t<XXXX>_n_sch, that a generic
	// prefix+suffix builder had no way to know about since the
	// abbreviation "n_sch" isn't Name+prefix-shaped at all; and a
	// query against the literal, unprefixed string "nodes", which
	// doesn't exist -- both caught by TestIntegration_BlobExport_
	// FullAsyncFlow against a real server, not assumed away).
	// TenantPrefixed and TenantFiltered are both ignored when NameFunc
	// is set -- NameFunc already encodes the correct scoping.
	NameFunc func(tenantID uint16) string
	// TenantPrefixed is true when this table's REAL name is Name with
	// the tenant's own table prefix prepended
	// (tenant.TenantID.TablePrefix(), e.g. "bal_accounts" ->
	// "t0000_bal_accounts") -- verified directly against source for
	// every table this applies to (pkg/bal/store.go's own
	// accountsTable/journalTable/balancesTable and siblings in
	// rollup.go/seal.go, all "s.prefix + literal suffix", confirmed
	// simple with no abbreviation surprises, unlike the entity/graph
	// tables above). Used only when NameFunc is nil.
	TenantPrefixed bool
	// TenantFiltered is true when this table is shared across all
	// tenants and needs a "WHERE tenant_id = ?" filter (e.g.
	// cal_bookings, dxp_txn, fsm_machines -- confirmed directly
	// against their own real WHERE clauses, not assumed). Used only
	// when NameFunc is nil.
	TenantFiltered bool
}

// tableName returns the actual table name to query.
func (spec SQLiteTableSpec) tableName(tenantID uint16) string {
	if spec.NameFunc != nil {
		return spec.NameFunc(tenantID)
	}
	if spec.TenantPrefixed {
		return tenant.TenantID(tenantID).TablePrefix() + spec.Name
	}
	return spec.Name
}

// isNoSuchTableError reports whether err is SQLite's own "no such
// table" error. Checked by message text, not Error.Code() --
// modernc.org/sqlite's Code() returns the generic SQLITE_ERROR (1) for
// this case, which is indistinguishable at the code level from any
// other SQL logic error (a genuine typo, a malformed query); SQLite's
// C API does not subdivide SQLITE_ERROR any further, so matching the
// message text is the standard, accepted way to detect this specific
// case, not a driver-specific workaround.
func isNoSuchTableError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// writeEmptyJSONArray writes a valid, empty "[]" to
// outDir/<name>.json -- the same shape ExportSQLiteTable produces for
// a real table with zero matching rows, so a table that doesn't exist
// yet for this tenant is indistinguishable, on disk, from one that
// exists but happens to be empty. Both are legitimate "no data" cases;
// a caller reading the export later should not need to care which.
func writeEmptyJSONArray(outDir, name string) (int, error) {
	outPath := filepath.Join(outDir, name+".json")
	if err := os.WriteFile(outPath, []byte("[]\n"), 0644); err != nil {
		return 0, fmt.Errorf("tenantexport: write %s: %w", outPath, err)
	}
	return 0, nil
}

// ExportSQLiteTable runs one table's export: SELECT (filtered by
// tenant_id when TenantFiltered) every row, and write it as a JSON
// array to outDir/<spec.Name>.json. Column names and values are taken
// directly from the driver's own row description -- no struct
// mapping, no assumption about a table's schema beyond what the
// database itself reports at query time.
//
// Returns the number of rows written and any error. An empty table
// (zero rows) still produces a valid file containing "[]", not an
// error and not a skipped file -- a caller reconstructing a tenant's
// full export later should not have to distinguish "this table had no
// data" from "this table was never exported".
func ExportSQLiteTable(ctx context.Context, db *sql.DB, tenantID uint16, spec SQLiteTableSpec, outDir string) (int, error) {
	realTable := spec.tableName(tenantID)
	query := fmt.Sprintf("SELECT * FROM %s", realTable)
	var args []interface{}
	if spec.NameFunc == nil && spec.TenantFiltered {
		query += " WHERE tenant_id = ?"
		args = append(args, tenantID)
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		if isNoSuchTableError(err) {
			// A legitimate "no data" case, not a failure: some tables
			// are created lazily on first use rather than eagerly at
			// tenant boot (confirmed directly -- t0000_n_sch, unlike
			// t0000_e_sch, does not exist until a node-adapted schema
			// is actually registered; caught by
			// TestIntegration_BlobExport_FullAsyncFlow against a real
			// server, not assumed). Matches ExportPebbleStores' own
			// treatment of a missing Pebble directory: a primitive a
			// tenant has never used produces an empty export for that
			// piece, not a hard error for the whole export.
			return writeEmptyJSONArray(outDir, spec.Name)
		}
		return 0, fmt.Errorf("tenantexport: query %s: %w", realTable, err)
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return 0, fmt.Errorf("tenantexport: columns for %s: %w", realTable, err)
	}

	outPath := filepath.Join(outDir, spec.Name+".json")
	f, err := os.Create(outPath)
	if err != nil {
		return 0, fmt.Errorf("tenantexport: create %s: %w", outPath, err)
	}
	defer func() { _ = f.Close() }()

	enc := json.NewEncoder(f)
	if _, err := f.WriteString("[\n"); err != nil {
		return 0, fmt.Errorf("tenantexport: write %s: %w", outPath, err)
	}

	rowNum := 0
	rawValues := make([]interface{}, len(cols))
	scanTargets := make([]interface{}, len(cols))
	for i := range rawValues {
		scanTargets[i] = &rawValues[i]
	}

	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return rowNum, fmt.Errorf("tenantexport: %s export cancelled after %d rows: %w", spec.Name, rowNum, err)
		}
		if err := rows.Scan(scanTargets...); err != nil {
			return rowNum, fmt.Errorf("tenantexport: scan %s row %d: %w", spec.Name, rowNum, err)
		}

		record := make(map[string]interface{}, len(cols))
		for i, col := range cols {
			record[col] = normalizeSQLValue(rawValues[i])
		}

		if rowNum > 0 {
			if _, err := f.WriteString(",\n"); err != nil {
				return rowNum, fmt.Errorf("tenantexport: write %s: %w", outPath, err)
			}
		}
		if err := enc.Encode(record); err != nil {
			return rowNum, fmt.Errorf("tenantexport: encode %s row %d: %w", spec.Name, rowNum, err)
		}
		rowNum++
	}
	if err := rows.Err(); err != nil {
		return rowNum, fmt.Errorf("tenantexport: iterating %s: %w", spec.Name, err)
	}

	if _, err := f.WriteString("]\n"); err != nil {
		return rowNum, fmt.Errorf("tenantexport: write %s: %w", outPath, err)
	}
	return rowNum, nil
}

// normalizeSQLValue converts a database/sql-scanned value into
// something encoding/json can serialize sensibly. SQLite's Go driver
// returns []byte for TEXT columns (not string) -- left as []byte, the
// standard library base64-encodes it, which is correct for genuine
// blob columns but wrong (unreadable) for the much more common case of
// ordinary text. Convert []byte to string unconditionally: every table
// this package exports stores JSON blobs, decimal strings, and plain
// text in its TEXT columns, never binary data that would need real
// base64 preservation.
func normalizeSQLValue(v interface{}) interface{} {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

// ExportSQLiteTables runs ExportSQLiteTable for every spec in order,
// stopping at the first error. Returns a map of table name to row
// count for every table that completed successfully before any
// failure -- a caller can log partial progress even on a failed run.
func ExportSQLiteTables(ctx context.Context, db *sql.DB, tenantID uint16, specs []SQLiteTableSpec, outDir string) (map[string]int, error) {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return nil, fmt.Errorf("tenantexport: mkdir %s: %w", outDir, err)
	}
	counts := make(map[string]int, len(specs))
	for _, spec := range specs {
		n, err := ExportSQLiteTable(ctx, db, tenantID, spec, outDir)
		if err != nil {
			return counts, err
		}
		counts[spec.Name] = n
	}
	return counts, nil
}
