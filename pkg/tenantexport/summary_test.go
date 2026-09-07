// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package tenantexport

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/cockroachdb/pebble"
	"github.com/ha1tch/xolu/pkg/blob"
	"github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// TestSummarize_EmptyTenant confirms a genuinely empty tenant --
// primary db exists but has none of the tables being checked, no
// loc.db, no obj.db, no Pebble stores at all -- summarizes to every
// count at zero and Empty() reporting true. This is the exact case
// xoluman's own tenant-emptiness safety check needs to be able to
// trust (XOT213, wave 15).
func TestSummarize_EmptyTenant(t *testing.T) {
	base := t.TempDir()
	tenantID := tenant.TenantID(9)

	primaryDB, err := sql.Open("sqlite", base+"/primary.db")
	if err != nil {
		t.Fatalf("open primary db: %v", err)
	}
	defer primaryDB.Close()

	testTables := []SQLiteTableSpec{
		{Name: "nodes", TenantFiltered: false},
		{Name: "cal_bookings", TenantFiltered: true},
	}
	orig := PrimaryStoreTables
	origLoc := LocStoreTables
	origObj := ObjStoreTables
	PrimaryStoreTables = testTables
	LocStoreTables = []SQLiteTableSpec{{Name: "locations", TenantFiltered: false}}
	ObjStoreTables = []SQLiteTableSpec{{Name: "obj_subjects", TenantFiltered: false}}
	defer func() { PrimaryStoreTables = orig; LocStoreTables = origLoc; ObjStoreTables = origObj }()

	summary, err := Summarize(context.Background(), primaryDB, base, tenantID, nil)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if !summary.Empty() {
		t.Errorf("Empty(): want true for a genuinely empty tenant, got false: %+v", summary)
	}
	if summary.Blob != 0 {
		t.Errorf("Blob: want 0 when bs is nil, got %d", summary.Blob)
	}
	if summary.Primary["nodes"] != 0 || summary.Primary["cal_bookings"] != 0 {
		t.Errorf("primary counts: want all zero (no such tables exist), got %+v", summary.Primary)
	}
	if summary.Loc["locations"] != 0 {
		t.Errorf("loc count: want 0 (no loc.db), got %+v", summary.Loc)
	}
	if summary.Obj["obj_subjects"] != 0 {
		t.Errorf("obj count: want 0 (no obj.db), got %+v", summary.Obj)
	}
	if summary.TS != 0 || summary.CalIndex != 0 || summary.BalRollup != 0 {
		t.Errorf("pebble counts: want all zero (no directories), got ts=%d cal=%d bal=%d",
			summary.TS, summary.CalIndex, summary.BalRollup)
	}
}

// TestSummarize_PopulatedAcrossAllSixStores seeds real data in every
// single store Summarize touches -- two primary tables (one plain,
// one tenant_id-filtered, with a second tenant's own row proving
// isolation actually holds, not just that a query ran), loc.db,
// obj.db, and all three per-tenant Pebble stores -- and confirms each
// count lands correctly and independently, not just that the overall
// Empty() check flips. This is the real proof the "one authoritative
// list, not a fifth hand-maintained one" claim in this file's own
// header comment actually holds across every source it names.
func TestSummarize_PopulatedAcrossAllSixStores(t *testing.T) {
	base := t.TempDir()
	tenantID := tenant.TenantID(5)

	primaryDB, err := sql.Open("sqlite", base+"/primary.db")
	if err != nil {
		t.Fatalf("open primary db: %v", err)
	}
	defer primaryDB.Close()
	if _, err := primaryDB.Exec(`CREATE TABLE nodes (id INTEGER, data TEXT)`); err != nil {
		t.Fatalf("create nodes: %v", err)
	}
	if _, err := primaryDB.Exec(`INSERT INTO nodes VALUES (1, 'tenant-5-node'), (2, 'tenant-5-node-2')`); err != nil {
		t.Fatalf("insert nodes: %v", err)
	}
	if _, err := primaryDB.Exec(`CREATE TABLE cal_bookings (tenant_id INTEGER, booking_id TEXT)`); err != nil {
		t.Fatalf("create cal_bookings: %v", err)
	}
	if _, err := primaryDB.Exec(`INSERT INTO cal_bookings VALUES (5, 'tenant-5-booking'), (7, 'tenant-7-booking')`); err != nil {
		t.Fatalf("insert cal_bookings: %v", err)
	}

	orig := PrimaryStoreTables
	origLoc := LocStoreTables
	origObj := ObjStoreTables
	PrimaryStoreTables = []SQLiteTableSpec{
		{Name: "nodes", TenantFiltered: false},
		{Name: "cal_bookings", TenantFiltered: true},
	}
	LocStoreTables = []SQLiteTableSpec{{Name: "locations", TenantFiltered: false}}
	ObjStoreTables = []SQLiteTableSpec{{Name: "obj_subjects", TenantFiltered: false}}
	defer func() { PrimaryStoreTables = orig; LocStoreTables = origLoc; ObjStoreTables = origObj }()

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
	if _, err := locDB.Exec(`INSERT INTO locations VALUES (1, 'shelf-a'), (2, 'shelf-b'), (3, 'shelf-c')`); err != nil {
		t.Fatalf("insert locations: %v", err)
	}
	locDB.Close()

	objDir := storelayout.TenantObjDir(base, tenantID)
	if err := os.MkdirAll(objDir, 0755); err != nil {
		t.Fatalf("mkdir objDir: %v", err)
	}
	objDB, err := sql.Open("sqlite", objDir+"/obj.db")
	if err != nil {
		t.Fatalf("open obj.db: %v", err)
	}
	if _, err := objDB.Exec(`CREATE TABLE obj_subjects (subject_ref TEXT)`); err != nil {
		t.Fatalf("create obj_subjects: %v", err)
	}
	if _, err := objDB.Exec(`INSERT INTO obj_subjects VALUES ('vehicles:1')`); err != nil {
		t.Fatalf("insert obj_subjects: %v", err)
	}
	objDB.Close()

	seedPebble := func(dir string, keys ...string) {
		t.Helper()
		db, err := pebble.Open(dir, &pebble.Options{})
		if err != nil {
			t.Fatalf("open pebble %s: %v", dir, err)
		}
		for _, k := range keys {
			if err := db.Set([]byte(k), []byte("v"), pebble.Sync); err != nil {
				t.Fatalf("seed pebble %s key %s: %v", dir, k, err)
			}
		}
		db.Close()
	}
	seedPebble(storelayout.TenantTSDir(base, tenantID), "series:1", "series:2")
	seedPebble(storelayout.TenantCalDir(base, tenantID), "occ:1")
	seedPebble(storelayout.TenantBalRollupDir(base, tenantID), "rollup:1", "rollup:2", "rollup:3")

	summary, err := Summarize(context.Background(), primaryDB, base, tenantID, nil)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if summary.Empty() {
		t.Fatalf("Empty(): want false, every store has real data, got true: %+v", summary)
	}
	if summary.Primary["nodes"] != 2 {
		t.Errorf("primary[nodes]: want 2, got %d", summary.Primary["nodes"])
	}
	if summary.Primary["cal_bookings"] != 1 {
		t.Errorf("primary[cal_bookings]: want 1 (tenant isolation -- only tenant 5's own row), got %d",
			summary.Primary["cal_bookings"])
	}
	if summary.Loc["locations"] != 3 {
		t.Errorf("loc[locations]: want 3, got %d", summary.Loc["locations"])
	}
	if summary.Obj["obj_subjects"] != 1 {
		t.Errorf("obj[obj_subjects]: want 1, got %d", summary.Obj["obj_subjects"])
	}
	if summary.TS != 2 {
		t.Errorf("TS: want 2, got %d", summary.TS)
	}
	if summary.CalIndex != 1 {
		t.Errorf("CalIndex: want 1, got %d", summary.CalIndex)
	}
	if summary.BalRollup != 3 {
		t.Errorf("BalRollup: want 3, got %d", summary.BalRollup)
	}
}

// TestSummarize_BlobOnlyTenant_NotReportedEmpty is XOT216's own real
// proof, not a smoke test: before this fix, a tenant with real
// uploaded blob content and nothing else in any SQL table or Pebble
// store genuinely reported Empty()==true, because ExportTenant's own
// authoritative list -- which Summarize deliberately reuses -- never
// captured blob content at all (blob.Store there is only ever the
// destination for a packaged export, confirmed directly against
// PackageAndStore, never a source of counted data). This test seeds
// nothing but a single blob and asserts the summary now correctly
// says the tenant is not empty.
func TestSummarize_BlobOnlyTenant_NotReportedEmpty(t *testing.T) {
	base := t.TempDir()
	tenantID := tenant.TenantID(11)

	primaryDB, err := sql.Open("sqlite", base+"/primary.db")
	if err != nil {
		t.Fatalf("open primary db: %v", err)
	}
	defer primaryDB.Close()

	orig := PrimaryStoreTables
	origLoc := LocStoreTables
	origObj := ObjStoreTables
	PrimaryStoreTables = []SQLiteTableSpec{{Name: "nodes", TenantFiltered: false}}
	LocStoreTables = []SQLiteTableSpec{{Name: "locations", TenantFiltered: false}}
	ObjStoreTables = []SQLiteTableSpec{{Name: "obj_subjects", TenantFiltered: false}}
	defer func() { PrimaryStoreTables = orig; LocStoreTables = origLoc; ObjStoreTables = origObj }()
	if _, err := primaryDB.Exec(`CREATE TABLE nodes (id INTEGER)`); err != nil {
		t.Fatalf("create nodes: %v", err)
	}
	// Deliberately zero rows -- nodes exists but is empty, matching
	// the "genuinely no data anywhere except blob" scenario this test
	// is named for.

	blobDir := t.TempDir()
	bs, err := blob.NewStore(blobDir, 0)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	if _, _, _, err := bs.Put("seed-file.txt", strings.NewReader("real tenant data"), "text/plain"); err != nil {
		t.Fatalf("blob Put: %v", err)
	}

	// Sanity check first: WITHOUT bs (the pre-fix call shape), this
	// tenant must still report empty -- proving the bug this test is
	// about would have been real, not that the fixture itself already
	// happens to contain non-blob data.
	withoutBlob, err := Summarize(context.Background(), primaryDB, base, tenantID, nil)
	if err != nil {
		t.Fatalf("Summarize (nil bs): %v", err)
	}
	if !withoutBlob.Empty() {
		t.Fatalf("Summarize (nil bs): want Empty()==true (nothing but blob data exists), got false: %+v", withoutBlob)
	}

	summary, err := Summarize(context.Background(), primaryDB, base, tenantID, bs)
	if err != nil {
		t.Fatalf("Summarize (with bs): %v", err)
	}
	if summary.Blob != 1 {
		t.Errorf("Blob: want 1, got %d", summary.Blob)
	}
	if summary.Empty() {
		t.Fatalf("Empty(): want false -- this tenant has real blob data -- got true: %+v", summary)
	}
}
