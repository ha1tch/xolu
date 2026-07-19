// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Tests for scoped tenant authorization on the S3 gateway (D2): under scoped
// mode an S3 access key must have a configured grant that authorises the
// requested bucket; the access-key string is no longer trusted as the tenant.
//
// See docs/proposals/tenant-access-control.md §8.5 step 6.

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/s3sig"
)

// signedS3Request builds a GET request for the bucket and signs it with the
// given access key + secret via the shared s3sig package, so it verifies on the
// server. Returns the request ready to pass to s3TenantFromRequest.
func signedS3Request(bucket, accessKey, secret string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/"+bucket, nil)
	const amzDate = "20260101T000000Z"
	req.Host = "xolu.example"
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")

	comp := s3sig.Components{
		AccessKey:   accessKey,
		Date:        "20260101",
		Region:      "us-east-1",
		Service:     "s3",
		Method:      http.MethodGet,
		CanonURI:    req.URL.EscapedPath(),
		CanonQuery:  "",
		Headers:     map[string]string{"host": req.Host, "x-amz-date": amzDate, "x-amz-content-sha256": "UNSIGNED-PAYLOAD"},
		PayloadHash: "UNSIGNED-PAYLOAD",
		AmzDate:     amzDate,
	}
	auth := s3sig.Sign(secret, comp, []string{"host", "x-amz-date", "x-amz-content-sha256"})
	req.Header.Set("Authorization", auth)
	return req
}

// callS3Signed signs a request for (bucket, accessKey, secret) and runs it
// through the resolver, returning the resolved tenant and status.
func callS3Signed(s *Server, bucket, accessKey, secret string) (string, int) {
	req := signedS3Request(bucket, accessKey, secret)
	rr := httptest.NewRecorder()
	tenant := s.s3TenantFromRequest(rr, req, bucket)
	return tenant, rr.Code
}

func newS3ScopedServer(t *testing.T, grants []config.S3KeyGrant) *Server {
	return newScopedServerCfg(t, func(c *config.Config) {
		c.AuthType = "bearertoken" // main-server auth type is irrelevant to S3 path
		c.InternalToken = "unused-token-for-s3-tests-000000"
		c.S3Enabled = true
		c.BlobEnabled = true
		c.S3KeyGrants = grants
	})
}

// A key granted to bucket "alpha" resolves alpha and is denied on "beta".
func TestS3Scoped_CrossBucketForbidden(t *testing.T) {
	s := newS3ScopedServer(t, []config.S3KeyGrant{
		{AccessKey: "AKIAALPHA", Secret: "s1", Tenants: []string{"alpha"}},
		{AccessKey: "AKIABETA", Secret: "s2", Tenants: []string{"beta"}},
	})

	if ten, code := callS3Signed(s, "alpha", "AKIAALPHA", "s1"); ten != "alpha" || code == http.StatusForbidden {
		t.Errorf("alpha key on alpha bucket: tenant=%q code=%d (want alpha, not 403)", ten, code)
	}
	if ten, code := callS3Signed(s, "beta", "AKIAALPHA", "s1"); ten != "" || code != http.StatusForbidden {
		t.Errorf("alpha key on beta bucket: tenant=%q code=%d (want \"\", 403)", ten, code)
	}
	if ten, code := callS3Signed(s, "beta", "AKIABETA", "s2"); ten != "beta" || code == http.StatusForbidden {
		t.Errorf("beta key on beta bucket: tenant=%q code=%d (want beta, not 403)", ten, code)
	}
}

// A known, authorised key but with a signature computed from the wrong secret
// is rejected (SignatureDoesNotMatch).
func TestS3Scoped_WrongSignatureForbidden(t *testing.T) {
	s := newS3ScopedServer(t, []config.S3KeyGrant{
		{AccessKey: "AKIAALPHA", Secret: "real-secret", Tenants: []string{"alpha"}},
	})
	// Sign with the wrong secret — access key and bucket are valid, signature is not.
	if ten, code := callS3Signed(s, "alpha", "AKIAALPHA", "wrong-secret"); ten != "" || code != http.StatusForbidden {
		t.Errorf("wrong signature: tenant=%q code=%d (want \"\", 403)", ten, code)
	}
}

// An unknown access key (no configured grant) is denied before signature check.
func TestS3Scoped_UnknownKeyForbidden(t *testing.T) {
	s := newS3ScopedServer(t, []config.S3KeyGrant{
		{AccessKey: "AKIAALPHA", Secret: "s1", Tenants: []string{"alpha"}},
	})
	if ten, code := callS3Signed(s, "alpha", "AKIAUNKNOWN", "whatever"); ten != "" || code != http.StatusForbidden {
		t.Errorf("unknown key: tenant=%q code=%d (want \"\", 403)", ten, code)
	}
}

// A missing Authorization header under scoped is denied (no bucket fallback).
func TestS3Scoped_NoAuthForbidden(t *testing.T) {
	s := newS3ScopedServer(t, []config.S3KeyGrant{
		{AccessKey: "AKIAALPHA", Secret: "s1", Tenants: []string{"alpha"}},
	})
	req := httptest.NewRequest(http.MethodGet, "/alpha", nil)
	rr := httptest.NewRecorder()
	if ten := s.s3TenantFromRequest(rr, req, "alpha"); ten != "" || rr.Code != http.StatusForbidden {
		t.Errorf("no auth: tenant=%q code=%d (want \"\", 403)", ten, rr.Code)
	}
}

// An admin S3 key reaches any bucket (with a valid signature).
func TestS3Scoped_AdminKey(t *testing.T) {
	s := newS3ScopedServer(t, []config.S3KeyGrant{
		{AccessKey: "AKIAADMIN", Secret: "adm", Admin: true},
	})
	for _, bucket := range []string{"alpha", "beta"} {
		if ten, code := callS3Signed(s, bucket, "AKIAADMIN", "adm"); ten != bucket || code == http.StatusForbidden {
			t.Errorf("admin key on %s: tenant=%q code=%d", bucket, ten, code)
		}
	}
}
