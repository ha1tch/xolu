// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestMetaMigration_S13_LegacyRebuild: a database with the pre-S13
// entity_meta shape and live rows must come out of init with the
// subject-addressed shape and every row intact (kind=entity name,
// key=CAST(id AS TEXT)).
func TestMetaMigration_S13_LegacyRebuild(t *testing.T) {
	tmp, err := os.MkdirTemp("", "meta-migrate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(tmp) })
	dbPath := tmp + "/t.db"

	// First open: creates the CURRENT schema. Demolish entity_meta and
	// plant the legacy shape with data, simulating a pre-S13 database.
	s1, err := NewSQLiteStore(dbPath, SQLiteConfig{DBPath: dbPath, EnableWAL: true, GraphEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s1.InitV2Schema(ctx); err != nil {
		t.Fatal(err)
	}
	legacy := `
		DROP TABLE entity_meta;
		CREATE TABLE entity_meta (
			tenant_id  INTEGER   NOT NULL DEFAULT 0,
			entity     TEXT      NOT NULL,
			id         INTEGER   NOT NULL,
			key        TEXT      NOT NULL,
			value      TEXT      NOT NULL,
			expires_at TIMESTAMP NULL DEFAULT NULL,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (tenant_id, entity, id, key)
		);
		INSERT INTO entity_meta (tenant_id, entity, id, key, value) VALUES
			(0, 'users', 42, 'colour', '"teal"'),
			(0, 'users', 42, 'notes',  '"vip"'),
			(7, 'posts', 3,  'flag',   'true');
		DELETE FROM schema_version_v2 WHERE version = 13;
	`
	if _, err := s1.DB().ExecContext(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen + re-init: S13 must detect the legacy shape and rebuild.
	s2, err := NewSQLiteStore(dbPath, SQLiteConfig{DBPath: dbPath, EnableWAL: true, GraphEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if err := s2.InitV2Schema(ctx); err != nil {
		t.Fatalf("re-init over legacy shape: %v", err)
	}

	// New shape present, old column gone.
	hasOld, err := s2.tableHasColumn(ctx, "entity_meta", "id")
	if err != nil {
		t.Fatal(err)
	}
	if hasOld {
		t.Fatal("legacy `id` column survived the rebuild")
	}
	hasNew, _ := s2.tableHasColumn(ctx, "entity_meta", "subject_key")
	if !hasNew {
		t.Fatal("subject_key column missing after rebuild")
	}

	// Every row intact under the new address.
	var val string
	if err := s2.DB().QueryRowContext(ctx,
		`SELECT value FROM entity_meta WHERE tenant_id=0 AND subject_kind='users' AND subject_key='42' AND key='colour'`,
	).Scan(&val); err != nil || val != `"teal"` {
		t.Fatalf("migrated row wrong: %q err=%v", val, err)
	}
	var n int
	if err := s2.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM entity_meta`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("row count after migration: %d err=%v", n, err)
	}

	// Idempotence: a third init is a no-op.
	if err := s2.InitV2Schema(ctx); err != nil {
		t.Fatalf("repeat init: %v", err)
	}
}

func TestParseMetaSubject(t *testing.T) {
	ok := func(kind, key, wantKey string) {
		t.Helper()
		s, err := ParseMetaSubject(kind, key, nil)
		if err != nil {
			t.Fatalf("%s/%s: %v", kind, key, err)
		}
		if s.Key != wantKey {
			t.Fatalf("%s/%s: canonical key %q, want %q", kind, key, s.Key, wantKey)
		}
	}
	bad := func(kind, key, wantSub string) {
		t.Helper()
		_, err := ParseMetaSubject(kind, key, nil)
		if err == nil || !strings.Contains(err.Error(), wantSub) {
			t.Fatalf("%s/%s: err=%v, want mention of %q", kind, key, err, wantSub)
		}
	}

	// Entity kinds: positive-int keys, canonical decimal.
	ok("users", "42", "42")
	bad("users", "0", "positive")
	bad("users", "-3", "positive")
	bad("users", "abc", "positive")

	// ts.timeline: @C04d — the full uint32 range through the helper.
	ok("ts.timeline", "70000", "70000")     // above uint16: must survive
	ok("ts.timeline", "4294967295", "4294967295") // MaxTimelineID
	bad("ts.timeline", "4294967296", "")    // one past the width
	bad("ts.timeline", "-1", "")

	// cal kinds: opaque strings.
	ok("cal.calendar", "warehouse-A", "warehouse-A")
	bad("cal.calendar", "", "1..256")
	bad("cal.booking", "has space", "whitespace")

	// Gated kinds: reserved vocabulary, refused.
	bad("bal.account", "warehouse:A", "not yet available")
	bad("dxp.def", "x", "not yet available")

	// Unknown namespaced kind.
	bad("nope.thing", "x", "unknown subject kind")
}
