// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Adversarial tests for the S3 gateway's credential parsing and scoped tenant
// resolution. These assert that malformed, injected, or boundary-case
// Authorization headers are handled safely (no panic) and never authorise a
// bucket the access key is not granted.
//
// Happy-path scoped S3 behaviour is in tenant_scoped_s3_test.go; this file
// targets the SigV4 parsing surface (s3AccessKeyFromAuth) and the scoped
// resolver under hostile input.

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
)

// ── s3AccessKeyFromAuth parsing (no panic, correct extraction) ───────────────

func TestAdvS3_AccessKeyParsing(t *testing.T) {
	cases := []struct {
		name string
		auth string
		want string
	}{
		{"empty", "", ""},
		{"no credential field", "AWS4-HMAC-SHA256 SignedHeaders=host", ""},
		{"credential no slash", "Credential=onlyaccesskey", "onlyaccesskey"},
		{"credential normal", "Credential=AKIA123/20260101/us-east-1/s3/aws4_request, Signature=x", "AKIA123"},
		{"credential trailing comma only", "Credential=AKIA123,", "AKIA123"},
		{"credential empty value", "Credential=", ""},
		{"credential empty before slash", "Credential=/20260101/us-east-1", ""},
		{"credential with spaces", "Credential=  AKIA123  /date", "AKIA123"},
		{"multiple credential fields", "Credential=FIRST/d, Credential=SECOND/d", "FIRST"},
		{"injection attempt in key", "Credential=AKIA'; DROP/date", "AKIA'; DROP"},
		{"very long key", "Credential=" + strings.Repeat("A", 10000) + "/d", strings.Repeat("A", 10000)},
		{"null byte", "Credential=AKIA\x00X/d", "AKIA\x00X"},
		{"only credential prefix", "Credential", ""},
		{"unicode", "Credential=café/d", "café"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// Must not panic.
			got := s3AccessKeyFromAuth(c.auth)
			if got != c.want {
				t.Errorf("s3AccessKeyFromAuth(%q) = %q, want %q", c.auth, got, c.want)
			}
		})
	}
}

// ── scoped resolver under hostile Authorization headers ──────────────────────

func TestAdvS3_ScopedResolverHostileInput(t *testing.T) {
	s := newScopedServerCfg(t, func(c *config.Config) {
		c.AuthType = "bearertoken"
		c.InternalToken = "unused-0000000000000000000000000"
		c.S3Enabled = true
		c.BlobEnabled = true
		c.S3KeyGrants = []config.S3KeyGrant{
			{AccessKey: "AKIAALPHA", Secret: "s", Tenants: []string{"alpha"}},
		}
	})

	// Every hostile header must resolve to "" (denied) with a 403, never to a
	// tenant, and never panic.
	hostile := []struct {
		name   string
		bucket string
		auth   string
	}{
		{"garbage auth", "alpha", "garbage"},
		{"empty credential", "alpha", "Credential="},
		{"unknown key", "alpha", "Credential=AKIAUNKNOWN/d, Sig=x"},
		{"injection in key", "alpha", "Credential=AKIA'; DROP TABLE/d"},
		{"known key wrong bucket", "beta", "Credential=AKIAALPHA/d, Sig=x"},
		{"empty bucket known key", "", "Credential=AKIAALPHA/d, Sig=x"},
		{"null byte key", "alpha", "Credential=AKIA\x00/d"},
		{"very long key", "alpha", "Credential=" + strings.Repeat("A", 5000) + "/d"},
		{"bucket name as key attempt", "alpha", "Credential=alpha/d, Sig=x"},
	}
	for _, h := range hostile {
		t.Run(h.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/"+h.bucket, nil)
			req.Header.Set("Authorization", h.auth)
			rr := httptest.NewRecorder()
			tenant := s.s3TenantFromRequest(rr, req, h.bucket)
			if tenant != "" {
				t.Errorf("%s: resolved tenant %q, expected denial", h.name, tenant)
			}
			if rr.Code != http.StatusForbidden {
				t.Errorf("%s: expected 403, got %d", h.name, rr.Code)
			}
		})
	}
}

// The "bucket name as key" case is the specific pre-fix vulnerability: under
// scoped, presenting the bucket name as the access key must NOT grant access
// (the access-key string is no longer trusted as the tenant).
func TestAdvS3_BucketNameNotTrustedAsKey(t *testing.T) {
	s := newScopedServerCfg(t, func(c *config.Config) {
		c.AuthType = "bearertoken"
		c.InternalToken = "unused-0000000000000000000000000"
		c.S3Enabled = true
		c.BlobEnabled = true
		c.S3KeyGrants = []config.S3KeyGrant{
			{AccessKey: "AKIAALPHA", Secret: "s", Tenants: []string{"alpha"}},
		}
	})
	// Attacker names "alpha" as both bucket and access key — must be denied
	// because "alpha" is not a configured access key.
	req := httptest.NewRequest(http.MethodGet, "/alpha", nil)
	req.Header.Set("Authorization", "Credential=alpha/20260101/r, Signature=x")
	rr := httptest.NewRecorder()
	if tenant := s.s3TenantFromRequest(rr, req, "alpha"); tenant != "" {
		t.Errorf("bucket-name-as-key granted tenant %q; must be denied", tenant)
	}
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

// Under open mode (not scoped), the resolver retains its legacy behaviour:
// the parsed access key is returned as the tenant. This guards against an
// accidental behaviour change for non-scoped deployments.
func TestAdvS3_OpenModeUnchanged(t *testing.T) {
	s := newScopedServerCfg(t, func(c *config.Config) {
		c.TenantMode = "path"
		c.TenantAuthMode = "open"
		c.AuthType = "none"
		c.S3Enabled = true
		c.BlobEnabled = true
		c.S3RequireAuth = false
	})
	req := httptest.NewRequest(http.MethodGet, "/mybucket", nil)
	req.Header.Set("Authorization", "Credential=AKIAXYZ/d, Sig=x")
	rr := httptest.NewRecorder()
	// open mode: access key returned as tenant (legacy behaviour preserved).
	if tenant := s.s3TenantFromRequest(rr, req, "mybucket"); tenant != "AKIAXYZ" {
		t.Errorf("open mode: expected legacy tenant 'AKIAXYZ', got %q", tenant)
	}
}
