// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

import (
	"testing"
)

// TestSysmaskWidth_FrozenAtCreation verifies the immutability contract
// (@S §4): the width is set once at store creation, persisted in
// meta.json, and read back on reopen. A different width supplied to a
// reopen of the same directory is IGNORED — the persisted value wins.
func TestSysmaskWidth_FrozenAtCreation(t *testing.T) {
	dir := t.TempDir()

	// Create with width 8.
	s1, err := NewPebbleStore(dir, StoreConfig{SysmaskWidth: 8}, testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	if got := s1.SysmaskWidth(); got != 8 {
		t.Fatalf("after creation: width = %d, want 8", got)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close s1: %v", err)
	}

	// Reopen the SAME directory with a DIFFERENT width (16). The persisted
	// width (8) must win; the config value must be ignored. This is the
	// immutability guarantee — a store's id classification can never drift.
	s2, err := NewPebbleStore(dir, StoreConfig{SysmaskWidth: 16}, testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer s2.Close()
	if got := s2.SysmaskWidth(); got != 8 {
		t.Errorf("after reopen with width=16: width = %d, want 8 (persisted wins) — immutability violated", got)
	}
}

// TestSysmaskWidth_DefaultZero verifies that a store created without an
// explicit width defaults to 0 (no system reservation) — the correct
// behaviour for every store predating the feature.
func TestSysmaskWidth_DefaultZero(t *testing.T) {
	dir := t.TempDir()
	s, err := NewPebbleStore(dir, StoreConfig{}, testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close()
	if got := s.SysmaskWidth(); got != 0 {
		t.Errorf("default width = %d, want 0", got)
	}
	// With width 0, no id is ever system.
	if s.SysmaskWidth().IsSystem(0xFFFFFFFF) {
		t.Errorf("width 0 must classify all ids as user")
	}
}

// TestSysmaskWidth_SurvivesFlush verifies the width is preserved across
// a metadata flush (flushMeta rebuilds storeMeta from scratch; the width
// must not be dropped). Regression guard for the flush-drops-fields
// quirk noted in the store metadata code.
func TestSysmaskWidth_SurvivesFlush(t *testing.T) {
	dir := t.TempDir()
	s, err := NewPebbleStore(dir, StoreConfig{SysmaskWidth: 12}, testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Define a timeline and append, which will eventually flush meta
	// (counters). Force a close (which flushes) then reopen.
	if err := s.DefineTimeline(1, TimelineConfig{Name: "t", Dims: 1}); err != nil {
		t.Fatalf("define: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s2, err := NewPebbleStore(dir, StoreConfig{}, testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if got := s2.SysmaskWidth(); got != 12 {
		t.Errorf("width after flush+reopen = %d, want 12 — flush dropped the width", got)
	}
}
