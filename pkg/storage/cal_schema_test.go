// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"os"
	"testing"
)

// TestCalSchemaCreated verifies the S11 cal stage creates the booking-record
// tables and their indices (docs/proposals/cal-gate3-booking-record.md, and the
// index set from docs/KNOWN_ISSUES.md "cal design — schema gaps").
func TestCalSchemaCreated(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "cal-schema")
	defer os.RemoveAll(tmpDir)

	store, err := NewSQLiteStore(tmpDir+"/cal.db", SQLiteConfig{
		DBPath: tmpDir + "/cal.db",
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	if err := store.InitV2Schema(context.Background()); err != nil {
		t.Fatalf("InitV2Schema: %v", err)
	}

	db := store.DB()

	// Tables present.
	for _, table := range []string{"cal_calendars", "cal_bookings", "cal_participants", "cal_ord_seq"} {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Fatalf("table %q not created: %v", table, err)
		}
	}

	// Indices present — in particular the hot-path (calendar_id, state) index.
	for _, idx := range []string{
		"idx_cal_calendars_ordinal",
		"idx_cal_bookings_cal_state",
		"idx_cal_bookings_state",
	} {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?", idx).Scan(&name)
		if err != nil {
			t.Fatalf("index %q not created: %v", idx, err)
		}
	}

	// S11 recorded in schema_version_v2.
	var stage string
	if err := db.QueryRow(
		"SELECT stage FROM schema_version_v2 WHERE version=11").Scan(&stage); err != nil {
		t.Fatalf("S11 not recorded in schema_version_v2: %v", err)
	}
	if stage != "S11-cal" {
		t.Fatalf("stage = %q, want S11-cal", stage)
	}

	// Idempotency: running the migration again is a no-op (CREATE ... IF NOT
	// EXISTS), not an error.
	if err := store.InitV2Schema(context.Background()); err != nil {
		t.Fatalf("InitV2Schema (rerun) should be idempotent: %v", err)
	}

	// A round-trip insert/select against cal_bookings, exercising the columns.
	_, err = db.Exec(`INSERT INTO cal_calendars
		(tenant_id, calendar_id, ordinal, entity_ref, capacity, default_state, match_policy)
		VALUES (0, 'room-1', 1, 42, 1, 'binding', 'binding')`)
	if err != nil {
		t.Fatalf("insert calendar: %v", err)
	}
	_, err = db.Exec(`INSERT INTO cal_bookings
		(tenant_id, calendar_id, booking_id, state, start_utc, end_utc, mode, bearer, created_utc, updated_utc)
		VALUES (0, 'room-1', 'b1', 'binding', 1000, 2000, 'exclusive', 100, 500, 500)`)
	if err != nil {
		t.Fatalf("insert booking: %v", err)
	}
	var cnt int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM cal_bookings WHERE tenant_id=0 AND calendar_id='room-1' AND state='binding'").Scan(&cnt); err != nil {
		t.Fatalf("select booking: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected 1 binding booking, got %d", cnt)
	}
}
