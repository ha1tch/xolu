// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"os"
	"testing"
)

// ---------------------------------------------------------------------------
// Factory: ListStores, NewStore
// ---------------------------------------------------------------------------

func TestListStores(t *testing.T) {
	stores := ListStores()
	if len(stores) < 1 {
		t.Errorf("Expected at least 1 registered store, got %d", len(stores))
	}

	found := map[string]bool{}
	for _, s := range stores {
		found[s] = true
	}
	if !found["sqlite"] {
		t.Error("Expected 'sqlite' in registered stores")
	}
}

func TestNewStore_Unknown(t *testing.T) {
	_, err := NewStore("nonexistent", nil)
	if err == nil {
		t.Error("Expected error for unknown store type")
	}
}

func TestNewStoreFromConfig_UnknownType(t *testing.T) {
	_, err := NewStoreFromConfig(StoreConfig{Type: "badtype"})
	if err == nil {
		t.Error("Expected error for unknown store type in NewStoreFromConfig")
	}
}

// ---------------------------------------------------------------------------
// AdaptiveLock accessors
// ---------------------------------------------------------------------------

func TestAdaptiveLock_Accessors(t *testing.T) {
	al := NewAdaptiveLock(95)

	if al.Threshold() != 95 {
		t.Errorf("Threshold() = %d, want 95", al.Threshold())
	}

	// Initially should not be engaged (95 < 100)
	if al.Engaged() {
		t.Error("Expected lock to not be engaged initially at 95%")
	}

	// SetThreshold
	al.SetThreshold(50)
	if al.Threshold() != 50 {
		t.Errorf("After SetThreshold(50), Threshold() = %d", al.Threshold())
	}

	// Edge: 100 = always engaged
	al.SetThreshold(100)
	if !al.Engaged() {
		t.Error("Expected lock to be engaged at 100%")
	}

	// Edge: 0 = never engaged
	al.SetThreshold(0)
	if al.Engaged() {
		t.Error("Expected lock to not be engaged at 0%")
	}

	// Clamping: negative
	al.SetThreshold(-10)
	if al.Threshold() != 0 {
		t.Errorf("Negative threshold should clamp to 0, got %d", al.Threshold())
	}

	// Clamping: over 100
	al.SetThreshold(200)
	if al.Threshold() != 100 {
		t.Errorf("Over-100 threshold should clamp to 100, got %d", al.Threshold())
	}
}

func TestAdaptiveLock_EdgeCases(t *testing.T) {
	// threshold = 0: disabled
	al := NewAdaptiveLock(0)
	if al.Engaged() {
		t.Error("Lock with threshold 0 should never engage")
	}

	// threshold = 100: always engaged
	al = NewAdaptiveLock(100)
	if !al.Engaged() {
		t.Error("Lock with threshold 100 should always be engaged")
	}
}

// ---------------------------------------------------------------------------
// SQLite DB() and ContentionLock() accessors
// ---------------------------------------------------------------------------

func TestSQLiteStore_Accessors(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "storage-acc")
	defer os.RemoveAll(tmpDir)

	store, err := NewSQLiteStore(tmpDir+"/acc.db", SQLiteConfig{
		DBPath:    tmpDir + "/acc.db",
		EnableWAL: true,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore failed: %v", err)
	}
	defer store.Close()

	if store.DB() == nil {
		t.Error("DB() should return non-nil database")
	}

	if store.ContentionLock() == nil {
		t.Error("ContentionLock() should return non-nil lock")
	}
}

// ---------------------------------------------------------------------------
// ---------------------------------------------------------------------------
