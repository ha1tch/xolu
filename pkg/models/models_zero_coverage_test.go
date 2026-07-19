// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package models

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Reference
// ---------------------------------------------------------------------------

func TestNewReference(t *testing.T) {
	ref := NewReference("person", 42)
	if ref.Type != "REF" {
		t.Errorf("Type = %q, want %q", ref.Type, "REF")
	}
	if ref.Entity != "person" {
		t.Errorf("Entity = %q, want %q", ref.Entity, "person")
	}
	if ref.ID != 42 {
		t.Errorf("ID = %d, want 42", ref.ID)
	}
}

func TestReference_ToMap(t *testing.T) {
	ref := NewReference("dept", 7)
	m := ref.ToMap()

	if m["type"] != RefTypeValue {
		t.Errorf("type = %v, want %q", m["type"], RefTypeValue)
	}
	if m["entity"] != "dept" {
		t.Errorf("entity = %v, want %q", m["entity"], "dept")
	}
	if m["id"] != int64(7) {
		t.Errorf("id = %v (%T), want int64(7)", m["id"], m["id"])
	}

	// Round-trip: ToMap must produce a map that IsReference accepts.
	back, ok := IsReference(m)
	if !ok {
		t.Fatal("IsReference(ref.ToMap()) = false, want true")
	}
	if back.Entity != ref.Entity || back.ID != ref.ID {
		t.Errorf("round-trip mismatch: got %+v, want entity=%q id=%d", back, ref.Entity, ref.ID)
	}
}

// ---------------------------------------------------------------------------
// TSReference
// ---------------------------------------------------------------------------

func TestNewTSReference(t *testing.T) {
	dims := []uint64{10, 20, 30}
	ref := NewTSReference(5, dims)
	if ref.Type != "TSREF" {
		t.Errorf("Type = %q, want %q", ref.Type, "TSREF")
	}
	if ref.Timeline != 5 {
		t.Errorf("Timeline = %d, want 5", ref.Timeline)
	}
	if len(ref.Dims) != 3 || ref.Dims[1] != 20 {
		t.Errorf("Dims = %v, want [10 20 30]", ref.Dims)
	}
}

func TestTSReference_ToMap(t *testing.T) {
	ref := NewTSReference(3, []uint64{1, 2})
	m := ref.ToMap()

	if m["type"] != "TSREF" {
		t.Errorf("type = %v, want %q", m["type"], "TSREF")
	}
	if m["timeline"] != 3 {
		t.Errorf("timeline = %v, want 3", m["timeline"])
	}
	dims, ok := m["dims"].([]interface{})
	if !ok || len(dims) != 2 {
		t.Errorf("dims = %v, want []interface{} of len 2", m["dims"])
	}

	// Round-trip through IsTSReference.
	back, ok := IsTSReference(m)
	if !ok {
		t.Fatal("IsTSReference(ref.ToMap()) = false, want true")
	}
	if back.Timeline != ref.Timeline {
		t.Errorf("round-trip timeline = %d, want %d", back.Timeline, ref.Timeline)
	}
	if len(back.Dims) != len(ref.Dims) {
		t.Errorf("round-trip dims len = %d, want %d", len(back.Dims), len(ref.Dims))
	}
}

// ---------------------------------------------------------------------------
// IsTSReference — all branches
// ---------------------------------------------------------------------------

func TestIsTSReference(t *testing.T) {
	// Non-map value.
	if _, ok := IsTSReference("not a map"); ok {
		t.Error("IsTSReference(string) should return false")
	}

	// Map without type=TSREF.
	if _, ok := IsTSReference(map[string]interface{}{"type": "REF"}); ok {
		t.Error("IsTSReference({type:REF}) should return false")
	}

	// Missing timeline field.
	if _, ok := IsTSReference(map[string]interface{}{"type": "TSREF", "dims": []interface{}{}}); ok {
		t.Error("IsTSReference(no timeline) should return false")
	}

	// timeline as float64 (JSON unmarshal produces this).
	ref, ok := IsTSReference(map[string]interface{}{
		"type":     "TSREF",
		"timeline": float64(7),
		"dims":     []interface{}{float64(1), float64(2)},
	})
	if !ok {
		t.Fatal("IsTSReference with float64 timeline should return true")
	}
	if ref.Timeline != 7 {
		t.Errorf("timeline = %d, want 7", ref.Timeline)
	}

	// timeline as int.
	ref, ok = IsTSReference(map[string]interface{}{
		"type":     "TSREF",
		"timeline": 9,
		"dims":     []interface{}{uint64(5)},
	})
	if !ok {
		t.Fatal("IsTSReference with int timeline should return true")
	}
	if ref.Timeline != 9 {
		t.Errorf("timeline = %d, want 9", ref.Timeline)
	}

	// dims with uint64 values.
	if len(ref.Dims) != 1 || ref.Dims[0] != 5 {
		t.Errorf("dims = %v, want [5]", ref.Dims)
	}

	// No dims field — should still succeed with empty dims.
	ref, ok = IsTSReference(map[string]interface{}{
		"type":     "TSREF",
		"timeline": 1,
	})
	if !ok {
		t.Fatal("IsTSReference (no dims) should return true")
	}
	if len(ref.Dims) != 0 {
		t.Errorf("dims = %v, want empty", ref.Dims)
	}
}
