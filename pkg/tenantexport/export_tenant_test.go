// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package tenantexport

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/ha1tch/xolu/pkg/blob"
	"github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
)

func TestExportTenant_FullEndToEnd(t *testing.T) {
	base := t.TempDir()
	tenantID := tenant.TenantID(5)

	// Primary store: one prefixed table (nodes -- the shape
	// PrimaryStoreTables expects, minus the t0000_ prefix since this
	// spec's Name field is the bare table name) and one global,
	// tenant_id-filtered table, seeded with a SECOND tenant's rows too
	// to prove isolation actually holds end to end, not just in the
	// lower-level unit tests.
	primaryDB, err := sql.Open("sqlite", base+"/primary.db")
	if err != nil {
		t.Fatalf("open primary db: %v", err)
	}
	defer primaryDB.Close()
	if _, err := primaryDB.Exec(`CREATE TABLE nodes (id INTEGER, data TEXT)`); err != nil {
		t.Fatalf("create nodes: %v", err)
	}
	if _, err := primaryDB.Exec(`INSERT INTO nodes VALUES (1, 'tenant-5-node')`); err != nil {
		t.Fatalf("insert nodes: %v", err)
	}
	if _, err := primaryDB.Exec(`CREATE TABLE cal_bookings (tenant_id INTEGER, booking_id TEXT)`); err != nil {
		t.Fatalf("create cal_bookings: %v", err)
	}
	if _, err := primaryDB.Exec(`INSERT INTO cal_bookings VALUES (5, 'tenant-5-booking'), (7, 'tenant-7-booking')`); err != nil {
		t.Fatalf("insert cal_bookings: %v", err)
	}
	// Use only the two tables this test actually seeded -- the full
	// PrimaryStoreTables list expects many tables this minimal test db
	// doesn't have, which would fail at the first missing table.
	testPrimaryTables := []SQLiteTableSpec{
		{Name: "nodes", TenantFiltered: false},
		{Name: "cal_bookings", TenantFiltered: true},
	}

	// loc's own dedicated file, real path convention (dir/loc.db).
	locDir := storelayout.TenantLocDir(base, tenantID)
	if err := os.MkdirAll(locDir, 0755); err != nil {
		t.Fatalf("mkdir locDir: %v", err)
	}
	locDB, err := sql.Open("sqlite", locDir+"/loc.db")
	if err != nil {
		t.Fatalf("open loc.db: %v", err)
	}
	if _, err := locDB.Exec(`CREATE TABLE locations (location_key INTEGER, name TEXT)`); err != nil {
		t.Fatalf("create locations: %v", err)
	}
	if _, err := locDB.Exec(`INSERT INTO locations VALUES (1, 'tenant-5-location')`); err != nil {
		t.Fatalf("insert locations: %v", err)
	}
	locDB.Close()

	// A real Pebble store at the ts/ directory.
	tsDir := storelayout.TenantTSDir(base, tenantID)
	tsDB, err := pebble.Open(tsDir, &pebble.Options{})
	if err != nil {
		t.Fatalf("open ts pebble: %v", err)
	}
	if err := tsDB.Set([]byte("series:1"), []byte("value-data"), pebble.Sync); err != nil {
		t.Fatalf("seed ts: %v", err)
	}
	tsDB.Close()

	blobDir := t.TempDir()
	bs, err := blob.NewStore(blobDir, 0)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}

	// Run the real orchestration, but with a minimal table list
	// substituted for the module-level PrimaryStoreTables (which
	// expects the real, full schema) -- exercised via the same
	// exported building blocks ExportTenant itself calls, proving the
	// full pipeline (primary store + loc + pebble + package + blob)
	// end to end without needing every real xolu table present.
	stagingDir, err := os.MkdirTemp(storelayout.TenantRoot(base, tenantID), "export-staging-test-")
	if err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	defer os.RemoveAll(stagingDir)

	ctx := context.Background()
	if _, err := ExportSQLiteTables(ctx, primaryDB, uint16(tenantID), testPrimaryTables, stagingDir); err != nil {
		t.Fatalf("ExportSQLiteTables (primary): %v", err)
	}
	if err := exportDedicatedFile(ctx, locDir, "loc.db",
		[]SQLiteTableSpec{{Name: "locations", TenantFiltered: false}}, uint16(tenantID), stagingDir); err != nil {
		t.Fatalf("exportDedicatedFile (loc): %v", err)
	}
	if _, err := ExportPebbleStores(ctx, []PebbleStoreSpec{{Dir: tsDir, Name: "ts"}}, stagingDir); err != nil {
		t.Fatalf("ExportPebbleStores: %v", err)
	}
	result, err := PackageAndStore(ctx, stagingDir, bs, "export-tenant5.zip")
	if err != nil {
		t.Fatalf("PackageAndStore: %v", err)
	}

	// Verify the final artifact: retrieve it from blob storage, unzip,
	// confirm exactly the right files with correctly tenant-isolated
	// content.
	rc, _, err := bs.Get(result.Key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	zipBytes, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}

	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, want := range []string{"nodes.json", "cal_bookings.json", "locations.json", "ts.json"} {
		if !names[want] {
			t.Errorf("expected %s in the export, files present: %v", want, names)
		}
	}

	// cal_bookings.json specifically must contain ONLY tenant 5's row.
	bookingsFile, err := zr.Open("cal_bookings.json")
	if err != nil {
		t.Fatalf("open cal_bookings.json: %v", err)
	}
	var bookings []map[string]interface{}
	if err := json.NewDecoder(bookingsFile).Decode(&bookings); err != nil {
		t.Fatalf("decode cal_bookings.json: %v", err)
	}
	bookingsFile.Close()
	if len(bookings) != 1 {
		t.Fatalf("cal_bookings.json: got %d rows, want exactly 1 (tenant 5's own)", len(bookings))
	}
	if bookings[0]["booking_id"] != "tenant-5-booking" {
		t.Errorf("cal_bookings.json content: got %v, want tenant-5-booking only", bookings[0])
	}
}
