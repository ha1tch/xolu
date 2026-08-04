// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package dxp

// store_test.go tests SQLStore and PebbleStore directly. Raw
// database/sql + a blank sqlite driver import, not
// storage.NewSQLiteStore — pkg/storage imports pkg/dxp (for the
// entity/fsm adapters), so the reverse would be circular; the same
// constraint pkg/bal's own tests already work under, and this mirrors
// their exact sql.Open setup.

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/cockroachdb/pebble"
	_ "modernc.org/sqlite"
)

func testSQLDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", dir+"/store.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE widgets (id INTEGER PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSQLStore_Owning_CommitActuallyCommits(t *testing.T) {
	db := testSQLDB(t)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := NewSQLStore(tx)
	if s.Engine() != "sql" {
		t.Errorf("Engine() = %q, want \"sql\"", s.Engine())
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES (1, 'gadget')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	var name string
	if err := db.QueryRow(`SELECT name FROM widgets WHERE id = 1`).Scan(&name); err != nil {
		t.Fatalf("row not visible after Commit: %v", err)
	}
	if name != "gadget" {
		t.Errorf("name = %q, want \"gadget\"", name)
	}
}

func TestSQLStore_Owning_AbortActuallyRollsBack(t *testing.T) {
	db := testSQLDB(t)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := NewSQLStore(tx)
	if _, err := tx.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES (1, 'gadget')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Abort(ctx); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM widgets`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows after Abort, got %d", count)
	}
}

// TestSQLStore_Shared_CommitAbortAreNoOps proves the collapse case
// directly: a non-owning store's Commit/Abort must never touch the
// underlying Tx — only the coordinator's own, separate call to the
// real Tx.Commit() may finalize it.
func TestSQLStore_Shared_CommitAbortAreNoOps(t *testing.T) {
	db := testSQLDB(t)
	ctx := context.Background()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	s1 := NewSharedSQLStore(tx)
	s2 := NewSharedSQLStore(tx)

	if _, err := tx.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES (1, 'gadget')`); err != nil {
		t.Fatal(err)
	}

	// Neither non-owning store's Commit may actually finalize anything.
	if err := s1.Commit(ctx); err != nil {
		t.Fatalf("s1.Commit (no-op) returned an error: %v", err)
	}
	if err := s2.Commit(ctx); err != nil {
		t.Fatalf("s2.Commit (no-op) returned an error: %v", err)
	}

	// The tx must still be genuinely open — a real write through it
	// now must still succeed, proving neither "Commit" actually closed it.
	if _, err := tx.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES (2, 'sprocket')`); err != nil {
		t.Fatalf("tx should still be open after two no-op Commits, got: %v", err)
	}

	// Only now does the coordinator itself commit the real, shared tx.
	if err := tx.Commit(); err != nil {
		t.Fatalf("real tx.Commit: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM widgets`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 rows after the real commit, got %d", count)
	}
}

// ─── PebbleStore ────────────────────────────────────────────────────────────

func testPebbleDB(t *testing.T) *pebble.DB {
	t.Helper()
	dir, err := os.MkdirTemp("", "dxp-pebblestore")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	db, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestPebbleStore_CommitActuallyCommits(t *testing.T) {
	db := testPebbleDB(t)
	ctx := context.Background()

	batch := db.NewBatch()
	s := NewPebbleStore(batch)
	if s.Engine() != "pebble" {
		t.Errorf("Engine() = %q, want \"pebble\"", s.Engine())
	}
	if err := s.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if err := batch.Set([]byte("widget:1"), []byte("gadget"), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(ctx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	val, closer, err := db.Get([]byte("widget:1"))
	if err != nil {
		t.Fatalf("key not visible after Commit: %v", err)
	}
	defer closer.Close()
	if string(val) != "gadget" {
		t.Errorf("value = %q, want \"gadget\"", val)
	}
}

func TestPebbleStore_AbortActuallyDiscards(t *testing.T) {
	db := testPebbleDB(t)
	ctx := context.Background()

	batch := db.NewBatch()
	s := NewPebbleStore(batch)
	if err := batch.Set([]byte("widget:1"), []byte("gadget"), nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Abort(ctx); err != nil {
		t.Fatalf("Abort: %v", err)
	}

	_, _, err := db.Get([]byte("widget:1"))
	if err == nil {
		t.Error("expected the key to be absent after Abort, but it was found")
	}
}
