// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	sl "github.com/ha1tch/xolu/pkg/storelayout"
)

// TestTSStoreMeta_Parse verifies iolu reads the sysmask width out of a
// ts store's meta.json exactly as timeseries writes it — the two sides
// agree on the JSON field name and type. A drift here would make
// `iolu ts status` silently report width 0 for every store.
func TestTSStoreMeta_Parse(t *testing.T) {
	// The shape timeseries.storeMeta serialises (only the fields iolu reads).
	raw := `{"created_at":"2026-07-20T10:00:00Z","compression":"","sysmask_width":8}`
	var m tsStoreMeta
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.SysmaskWidth != 8 {
		t.Errorf("sysmask_width = %d, want 8 — field name/type drift with timeseries.storeMeta?", uint8(m.SysmaskWidth))
	}
	if !m.SysmaskWidth.Valid() {
		t.Errorf("width 8 should be valid")
	}
	if m.CreatedAt != "2026-07-20T10:00:00Z" {
		t.Errorf("created_at = %q", m.CreatedAt)
	}
}

// TestTSStoreMeta_AbsentWidthDefaultsZero confirms a meta.json without a
// sysmask_width field (a store predating the feature) parses as width 0
// — the correct "no reservation" default, not an error.
func TestTSStoreMeta_AbsentWidthDefaultsZero(t *testing.T) {
	raw := `{"created_at":"2026-01-01T00:00:00Z","compression":""}`
	var m tsStoreMeta
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.SysmaskWidth != 0 {
		t.Errorf("absent sysmask_width should default to 0, got %d", uint8(m.SysmaskWidth))
	}
}

// TestDiscoverTSTenants finds only tenant dirs that carry a ts/ store.
func TestDiscoverTSTenants(t *testing.T) {
	base := t.TempDir()

	// Tenant 1 has a ts/ dir; tenant 2 does not; a non-tenant dir is ignored.
	if err := os.MkdirAll(sl.TenantTSDir(base, 1), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sl.TenantRoot(base, 2), "store"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(base, "not-a-tenant"), 0755); err != nil {
		t.Fatal(err)
	}

	ids := discoverTSTenants(base)
	if len(ids) != 1 || ids[0] != 1 {
		t.Errorf("discoverTSTenants = %v, want [1] (only tenant with a ts/ store)", ids)
	}
}
