// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// T-60 regression coverage: resolveNumericTenant must parse the numeric
// tenant-ID fallback as hex, matching pkg/client.WithTenantID's wire format
// and every other tenant-ID string in the substrate, not decimal.
//
// Until 2026-07-28 this parsed as base-10 decimal while every producer of
// the string (pkg/client included) sent base-16 hex. The two encodings only
// coincide for tenant IDs 0-9. For ID 10-15 decimal parsing hard-fails
// ("000B" is not a valid decimal number). For ID 16 and any tenant ID whose
// hex digits also happen to form a valid smaller decimal number ("0010" hex
// = 16, read as decimal = 10), decimal parsing SILENTLY resolved to the
// wrong tenant with no error — the dangerous case this file exists to
// pin down, not just the loud one.

package server

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
	"github.com/rs/zerolog"
)

// newNumericFallbackTestServer creates a strict-mode server with tenants
// registered at IDs chosen specifically to exercise every branch of the
// hex/decimal bug class, not just the coincidentally-safe single digits.
func newNumericFallbackTestServer(t *testing.T) *Server {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "numeric_fallback.db")
	baseStore, err := storage.NewStoreFromConfig(storage.StoreConfig{
		Type:     "sqlite",
		DBPath:   dbPath,
		TenantID: 0,
	})
	if err != nil {
		t.Fatalf("base store: %v", err)
	}
	t.Cleanup(func() { baseStore.Close() })

	cfg := config.Default()
	cfg.StorageType = "sqlite"
	cfg.BaseDir = filepath.Dir(dbPath)
	cfg.TenantMode = "strict"
	cfg.TenantAutoRegister = false
	cfg.AuthType = "none"
	cfg.MaxEntitySize = 1 << 20
	cfg.DefaultPageSize = 100
	cfg.MaxQueryDepth = 10
	cfg.AsyncJobRetentionTTL = 30
	cfg.GraphMaxVisitedNodes = 10000
	cfg.QueryTimeout = 30

	logger := zerolog.Nop()
	g := graph.NewFlatGraph()
	s := New(cfg, baseStore, &noopCache{}, g, &noopValidator{}, logger)

	ctx := context.Background()
	for _, reg := range []struct {
		name string
		id   tenant.TenantID
	}{
		{"single-digit", 5},  // decimal and hex coincide: must keep working
		{"needs-hex-a", 11},  // 0x0B: not valid decimal at all under the old bug
		{"needs-hex-b", 16},  // 0x10: ALSO reads as valid decimal 10 -- the silent case
		{"decimal-ten", 10},  // exists specifically so a wrong hex/decimal mix would collide with it
	} {
		if err := s.tenantRegistry.Register(ctx, reg.name, reg.id); err != nil {
			t.Fatalf("register %s (id %d): %v", reg.name, reg.id, err)
		}
	}
	return s
}

func TestResolveNumericTenant_HexNotDecimal(t *testing.T) {
	s := newNumericFallbackTestServer(t)

	cases := []struct {
		name     string
		raw      string // the wire string, as pkg/client.WithTenantID would send it
		wantID   tenant.TenantID
		wantName string
		wantOK   bool
	}{
		{"single digit hex==decimal", "0005", 5, "single-digit", true},
		{"double digit, decimal would hard-fail", "000B", 11, "needs-hex-a", true},
		{
			// The dangerous case: "0010" is hex for 16, but ALSO a valid
			// decimal string for 10. Under the old base-10 bug this
			// resolved to tenant 10 ("decimal-ten") with no error --
			// silently the wrong tenant. It must resolve to 16.
			"hex 0010 must be sixteen, not ten (the silent case)",
			"0010", 16, "needs-hex-b", true,
		},
		{"lowercase hex accepted by ParseUint", "000b", 11, "needs-hex-a", true},
		{"zero is reserved, never resolves", "0000", 0, "", false},
		{"non-hex characters rejected", "000G", 0, "", false},
		{"empty string rejected", "", 0, "", false},
		{"unregistered but valid hex", "00FF", 0, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotID, gotName, gotOK := s.resolveNumericTenant(tc.raw)
			if gotOK != tc.wantOK {
				t.Fatalf("resolveNumericTenant(%q) ok = %v, want %v", tc.raw, gotOK, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if gotID != tc.wantID {
				t.Errorf("resolveNumericTenant(%q) id = %d, want %d", tc.raw, gotID, tc.wantID)
			}
			if gotName != tc.wantName {
				t.Errorf("resolveNumericTenant(%q) name = %q, want %q", tc.raw, gotName, tc.wantName)
			}
		})
	}
}

// TestResolveNumericTenant_MatchesClientWireFormat proves the fix end to
// end: tenant.TenantID.String() is exactly what pkg/client.WithTenantID
// sends, and resolveNumericTenant must round-trip it for every ID in the
// range that matters, not just the ones a hand-picked table happens to
// cover.
func TestResolveNumericTenant_MatchesClientWireFormat(t *testing.T) {
	s := newNumericFallbackTestServer(t)

	for _, id := range []tenant.TenantID{5, 10, 11, 16} {
		wire := id.String() // identical construction to pkg/client.WithTenantID's fmt.Sprintf("%04X", id)
		gotID, _, ok := s.resolveNumericTenant(wire)
		if !ok {
			t.Errorf("resolveNumericTenant(%q) for tenant %d: ok = false, want true", wire, id)
			continue
		}
		if gotID != id {
			t.Errorf("resolveNumericTenant(%q): resolved to tenant %d, want %d", wire, gotID, id)
		}
	}
}
