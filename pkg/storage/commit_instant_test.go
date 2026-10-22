// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"testing"
	"time"
)

// restoreCommitClock puts the process-wide clock back after a test that moved it,
// so other tests see the wall clock as before.
func restoreCommitClock(t *testing.T) {
	t.Helper()
	commitClock.mu.Lock()
	saved := commitClock.last
	commitClock.mu.Unlock()
	t.Cleanup(func() {
		commitClock.mu.Lock()
		commitClock.last = saved
		commitClock.mu.Unlock()
	})
}

func openInstantStore(t *testing.T) *SQLiteStore {
	t.Helper()
	path := t.TempDir() + "/instant.db"
	store, err := NewSQLiteStore(path, SQLiteConfig{DBPath: path})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func recordInstant(t *testing.T, store *SQLiteStore, at time.Time) {
	t.Helper()
	ctx := context.Background()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := store.persistCommitInstant(ctx, tx, at.UTC().Format(CommitInstantLayout)); err != nil {
		tx.Rollback()
		t.Fatalf("persistCommitInstant: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// XOT247: an instant recorded by an earlier process, later than the wall clock
// (the clock stepped back across the restart), still bounds the next instant.
func TestCommitInstant_SeededFromThePersistedRecord(t *testing.T) {
	restoreCommitClock(t)
	store := openInstantStore(t)
	future := time.Now().UTC().Add(time.Hour)

	recordInstant(t, store, future)
	recordInstant(t, store, future.Add(-30*time.Minute)) // an earlier one never lowers the record
	store.seedCommitInstant(context.Background())

	got, err := time.Parse(CommitInstantLayout, NextCommitInstant())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !got.After(future) {
		t.Fatalf("next instant %v is not after the recorded %v", got, future)
	}
}

// With the switch off nothing is read back, and the clock is the wall clock.
func TestCommitInstant_PersistenceCanBeSwitchedOff(t *testing.T) {
	restoreCommitClock(t)
	store := openInstantStore(t)
	future := time.Now().UTC().Add(time.Hour)
	recordInstant(t, store, future)

	SetCommitInstantPersistence(false)
	defer SetCommitInstantPersistence(true)
	store.seedCommitInstant(context.Background())

	got, err := time.Parse(CommitInstantLayout, NextCommitInstant())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.After(future) {
		t.Fatalf("the clock was seeded (%v) although persistence is off", got)
	}
}

// A store that never recorded anything seeds nothing and does not fail.
func TestCommitInstant_NoRecordIsNotAnError(t *testing.T) {
	store := openInstantStore(t)
	store.seedCommitInstant(context.Background())
}
