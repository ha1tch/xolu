// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package refintegrity

import "testing"

func TestParseXRef_DefaultsToRestrict(t *testing.T) {
	raw := map[string]interface{}{"entity": "users"}
	xr, ok, err := ParseXRef("author_id", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	if xr.Entity != "users" {
		t.Errorf("entity = %q, want users", xr.Entity)
	}
	if xr.OnDelete != OnDeleteRestrict {
		t.Errorf("on_delete = %q, want restrict (the default)", xr.OnDelete)
	}
	if xr.Validate {
		t.Errorf("validate should default false")
	}
	if xr.Field != "author_id" {
		t.Errorf("field = %q, want author_id", xr.Field)
	}
}

func TestParseXRef_ExplicitPolicies(t *testing.T) {
	for _, p := range []OnDelete{OnDeleteRestrict, OnDeleteCascade, OnDeleteNullify} {
		raw := map[string]interface{}{"entity": "users", "on_delete": string(p)}
		xr, ok, err := ParseXRef("f", raw)
		if err != nil || !ok {
			t.Fatalf("policy %q: ok=%v err=%v", p, ok, err)
		}
		if xr.OnDelete != p {
			t.Errorf("policy = %q, want %q", xr.OnDelete, p)
		}
	}
}

func TestParseXRef_Validate(t *testing.T) {
	raw := map[string]interface{}{"entity": "users", "validate": true}
	xr, _, err := ParseXRef("f", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !xr.Validate {
		t.Error("validate should be true")
	}
}

func TestParseXRef_NoAnnotation(t *testing.T) {
	_, ok, err := ParseXRef("f", nil)
	if err != nil {
		t.Fatalf("nil x-ref should not error: %v", err)
	}
	if ok {
		t.Error("nil x-ref should return ok=false")
	}
}

func TestParseXRef_Malformed(t *testing.T) {
	cases := []struct {
		name string
		raw  interface{}
	}{
		{"not an object", "users"},
		{"missing entity", map[string]interface{}{"on_delete": "restrict"}},
		{"empty entity", map[string]interface{}{"entity": ""}},
		{"unknown policy", map[string]interface{}{"entity": "users", "on_delete": "delete-everything"}},
		{"non-string policy", map[string]interface{}{"entity": "users", "on_delete": 3}},
		{"non-bool validate", map[string]interface{}{"entity": "users", "validate": "yes"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ok, err := ParseXRef("f", c.raw)
			if err == nil {
				t.Errorf("expected error for %s, got ok=%v", c.name, ok)
			}
		})
	}
}

func TestOnDelete_Valid(t *testing.T) {
	for _, p := range []OnDelete{OnDeleteRestrict, OnDeleteCascade, OnDeleteNullify} {
		if !p.Valid() {
			t.Errorf("%q should be valid", p)
		}
	}
	for _, p := range []OnDelete{"", "delete", "RESTRICT", "set-default"} {
		if OnDelete(p).Valid() {
			t.Errorf("%q should be invalid", p)
		}
	}
}
