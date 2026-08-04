// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package tenantexport

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func readJSONArray(t *testing.T, path string) []map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal %s: %v (content: %s)", path, err, data)
	}
	return out
}

// TestExportSQLiteTable_PrefixedTable mirrors t0000_nodes' own shape:
// a table already scoped to one tenant by its NAME, needing no WHERE
// filter at all.
func TestExportSQLiteTable_PrefixedTable(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE t0000_nodes (entity_type TEXT, id INTEGER, data TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO t0000_nodes VALUES ('widget', 1, '{"name":"a"}'), ('widget', 2, '{"name":"b"}')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	outDir := t.TempDir()
	n, err := ExportSQLiteTable(ctx, db, 0, SQLiteTableSpec{Name: "t0000_nodes", TenantFiltered: false}, outDir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if n != 2 {
		t.Errorf("row count: got %d, want 2", n)
	}

	rows := readJSONArray(t, filepath.Join(outDir, "t0000_nodes.json"))
	if len(rows) != 2 {
		t.Fatalf("got %d rows in JSON, want 2", len(rows))
	}
	if rows[0]["entity_type"] != "widget" {
		t.Errorf("entity_type: got %v", rows[0]["entity_type"])
	}
	// []byte from the driver must decode as a plain JSON string, not
	// base64 -- confirms normalizeSQLValue is doing its job.
	if rows[0]["data"] != `{"name":"a"}` {
		t.Errorf("data (should be a plain string, not base64): got %v", rows[0]["data"])
	}
}

// TestExportSQLiteTable_TenantFiltered mirrors cal_bookings' own shape:
// a table shared across every tenant, isolated only by a tenant_id
// column -- and proves the filter actually isolates data, not just
// that it runs without error. Two tenants' rows are seeded in the SAME
// table; tenant 5's export must contain ONLY tenant 5's rows.
func TestExportSQLiteTable_TenantFiltered(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`CREATE TABLE cal_bookings (tenant_id INTEGER, calendar_id TEXT, booking_id TEXT)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cal_bookings VALUES
		(5, 'room-a', 'booking-1'),
		(5, 'room-a', 'booking-2'),
		(7, 'room-b', 'booking-3')`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	outDir := t.TempDir()
	n, err := ExportSQLiteTable(ctx, db, 5, SQLiteTableSpec{Name: "cal_bookings", TenantFiltered: true}, outDir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if n != 2 {
		t.Fatalf("row count for tenant 5: got %d, want 2", n)
	}

	rows := readJSONArray(t, filepath.Join(outDir, "cal_bookings.json"))
	for _, row := range rows {
		// json.Unmarshal decodes SQLite's INTEGER as float64 -- compare
		// as such rather than asserting a specific Go int type.
		if row["tenant_id"] != float64(5) {
			t.Errorf("tenant_id leaked into tenant 5's export: got %v (tenant 7's booking-3 must not appear here)", row["tenant_id"])
		}
		if row["booking_id"] == "booking-3" {
			t.Error("tenant 7's booking-3 leaked into tenant 5's export")
		}
	}
}

// TestExportSQLiteTable_EmptyTable proves an empty table produces a
// valid, present "[]" file, not an error and not a skipped file.
func TestExportSQLiteTable_EmptyTable(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE t0000_empty (id INTEGER)`); err != nil {
		t.Fatalf("create table: %v", err)
	}

	outDir := t.TempDir()
	n, err := ExportSQLiteTable(ctx, db, 0, SQLiteTableSpec{Name: "t0000_empty"}, outDir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if n != 0 {
		t.Errorf("row count: got %d, want 0", n)
	}

	rows := readJSONArray(t, filepath.Join(outDir, "t0000_empty.json"))
	if rows == nil {
		t.Error("empty table must still produce a valid (empty) JSON array, not null/absent")
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

// TestExportSQLiteTables_MultipleTables exercises the batch helper
// across a mix of prefixed and tenant-filtered tables in one call,
// matching how a real tenant export would actually invoke this.
func TestExportSQLiteTables_MultipleTables(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE t0000_nodes (id INTEGER)`); err != nil {
		t.Fatalf("create t0000_nodes: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE cal_bookings (tenant_id INTEGER, id INTEGER)`); err != nil {
		t.Fatalf("create cal_bookings: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO t0000_nodes VALUES (1), (2), (3)`); err != nil {
		t.Fatalf("insert nodes: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cal_bookings VALUES (0, 1), (0, 2), (9, 999)`); err != nil {
		t.Fatalf("insert bookings: %v", err)
	}

	outDir := t.TempDir()
	specs := []SQLiteTableSpec{
		{Name: "t0000_nodes", TenantFiltered: false},
		{Name: "cal_bookings", TenantFiltered: true},
	}
	counts, err := ExportSQLiteTables(ctx, db, 0, specs, outDir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if counts["t0000_nodes"] != 3 {
		t.Errorf("t0000_nodes count: got %d, want 3", counts["t0000_nodes"])
	}
	if counts["cal_bookings"] != 2 {
		t.Errorf("cal_bookings count (tenant 0 only): got %d, want 2", counts["cal_bookings"])
	}
	for _, name := range []string{"t0000_nodes.json", "cal_bookings.json"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Errorf("expected output file %s: %v", name, err)
		}
	}
}

// TestExportSQLiteTables_StopsAtFirstError proves a genuine failure on
// one table doesn't silently continue past it, and that the partial
// counts map still reports what succeeded before the failure. Uses a
// malformed table identifier (a real SQL syntax error) rather than a
// nonexistent table name -- a nonexistent table is deliberately NOT an
// error (see TestExportSQLiteTable_MissingTableIsEmptyNotError), so it
// can't be used to prove this behavior any more.
func TestExportSQLiteTables_StopsAtFirstError(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE t0000_nodes (id INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO t0000_nodes VALUES (1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	outDir := t.TempDir()
	specs := []SQLiteTableSpec{
		{Name: "t0000_nodes", TenantFiltered: false},
		{Name: "not valid ! sql", TenantFiltered: false}, // a real syntax error, not a missing table
	}
	counts, err := ExportSQLiteTables(ctx, db, 0, specs, outDir)
	if err == nil {
		t.Fatal("expected an error for a malformed table identifier, got nil")
	}
	if isNoSuchTableError(err) {
		t.Fatalf("expected a syntax error, got a no-such-table error (test premise broken): %v", err)
	}
	if counts["t0000_nodes"] != 1 {
		t.Errorf("partial progress: got %v, want t0000_nodes=1 recorded before the failure", counts)
	}
}

// TestExportSQLiteTable_MissingTableIsEmptyNotError proves the actual
// fix: a table that doesn't exist yet for this tenant (e.g. n_sch,
// created lazily only once a node-adapted schema is registered) is
// treated as legitimately empty, not a failure -- the real bug caught
// by TestIntegration_BlobExport_FullAsyncFlow in pkg/server against a
// live server, reproduced here at the package level.
func TestExportSQLiteTable_MissingTableIsEmptyNotError(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	// Deliberately create nothing -- the table genuinely does not exist.

	outDir := t.TempDir()
	n, err := ExportSQLiteTable(ctx, db, 0, SQLiteTableSpec{Name: "t0000_n_sch"}, outDir)
	if err != nil {
		t.Fatalf("expected no error for a table that doesn't exist yet, got: %v", err)
	}
	if n != 0 {
		t.Errorf("row count: got %d, want 0", n)
	}
	rows := readJSONArray(t, filepath.Join(outDir, "t0000_n_sch.json"))
	if rows == nil {
		t.Error("a missing table must still produce a valid (empty) JSON array on disk")
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}
