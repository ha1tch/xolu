// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package config

import "testing"

func TestTenancyFlags_Derivation(t *testing.T) {
	cases := []struct {
		mode, authMode string
		wantRoute      bool
		wantGrant      bool
	}{
		{"path", "open", false, false},   // today's default
		{"strict", "open", true, false},  // forced routing, open trust
		{"strict", "scoped", true, true}, // the target multi-tenant-untrusted cell
		// scoped+path is rejected by config validation, but the derivation must
		// still be safe if it is ever constructed directly: scoped implies route.
		{"path", "scoped", true, true},
	}
	for _, c := range cases {
		cfg := &Config{TenantMode: c.mode, TenantAuthMode: c.authMode}
		f := cfg.Tenancy()
		if got := f.Has(TenantRequireRoute); got != c.wantRoute {
			t.Errorf("[%s/%s] TenantRequireRoute = %v, want %v", c.mode, c.authMode, got, c.wantRoute)
		}
		if got := f.Has(TenantEnforceGrant); got != c.wantGrant {
			t.Errorf("[%s/%s] TenantEnforceGrant = %v, want %v", c.mode, c.authMode, got, c.wantGrant)
		}
	}
}

func TestTenancyFlags_Has(t *testing.T) {
	both := TenantRequireRoute | TenantEnforceGrant
	if !both.Has(TenantRequireRoute) || !both.Has(TenantEnforceGrant) {
		t.Error("combined flags should report Has for each bit")
	}
	if !both.Has(both) {
		t.Error("combined flags should Has itself")
	}
	only := TenantRequireRoute
	if only.Has(TenantEnforceGrant) {
		t.Error("route-only must not report Has(grant)")
	}
	var none TenancyFlags
	if none.Has(TenantRequireRoute) {
		t.Error("zero flags must not report Has anything")
	}
}
