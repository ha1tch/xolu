// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package authmw

import (
	"context"
	"testing"
)

func TestTenantGrant_Allows(t *testing.T) {
	cases := []struct {
		name   string
		grant  TenantGrant
		tenant string
		want   bool
	}{
		{"admin allows any", TenantGrant{Admin: true}, "acme", true},
		{"admin allows even unlisted", TenantGrant{Admin: true, Tenants: []string{"x"}}, "y", true},
		{"exact single match", TenantGrant{Tenants: []string{"acme"}}, "acme", true},
		{"multi match first", TenantGrant{Tenants: []string{"acme", "globex"}}, "acme", true},
		{"multi match last", TenantGrant{Tenants: []string{"acme", "globex"}}, "globex", true},
		{"non-member denied", TenantGrant{Tenants: []string{"acme"}}, "globex", false},
		{"empty grant denies", TenantGrant{}, "acme", false},
		{"empty tenant string denied", TenantGrant{Tenants: []string{"acme"}}, "", false},
		{"case sensitive", TenantGrant{Tenants: []string{"acme"}}, "ACME", false},
		{"no substring match", TenantGrant{Tenants: []string{"acme"}}, "acm", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.grant.Allows(c.tenant); got != c.want {
				t.Errorf("Allows(%q) = %v, want %v", c.tenant, got, c.want)
			}
		})
	}
}

func TestTenantGrant_IsEmpty(t *testing.T) {
	if !(TenantGrant{}).IsEmpty() {
		t.Error("zero grant should be empty")
	}
	if (TenantGrant{Admin: true}).IsEmpty() {
		t.Error("admin grant is not empty")
	}
	if (TenantGrant{Tenants: []string{"acme"}}).IsEmpty() {
		t.Error("grant with tenants is not empty")
	}
	if !(TenantGrant{Tenants: []string{}}).IsEmpty() {
		t.Error("grant with empty tenant slice should be empty")
	}
}

func TestTenantGrantFromContext(t *testing.T) {
	// Absent → ok=false.
	if _, ok := TenantGrantFromContext(context.Background()); ok {
		t.Error("expected ok=false when no grant in context")
	}
	// Present → round-trips.
	want := TenantGrant{Tenants: []string{"acme", "globex"}}
	ctx := context.WithValue(context.Background(), ContextKeyTenantGrant, want)
	got, ok := TenantGrantFromContext(ctx)
	if !ok {
		t.Fatal("expected ok=true when grant present")
	}
	if got.Admin != want.Admin || len(got.Tenants) != len(want.Tenants) {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, want)
	}
}
