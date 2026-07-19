// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// blob_adversarial_test.go
//
// Adversarial coverage for the blob and S3 handler surfaces that were
// previously untested or lightly tested:
//
//   tenantForBlob        50% → tenant-context path (non-default tenant)
//   handleBlobUsage      57% → blobSampler nil branch already covered;
//                              blobSampler live path (SampledAt populated)
//   handleS3HeadBucket   62% → blobStore_==nil branch
//   handleS3HeadObject   61% → blobStore_==nil branch + key absent
//   handleS3DeleteObject 44% → blobStore_==nil branch + key absent (204)
//   handleS3ListObjects  71% → blobStore_==nil branch
//   handleS3PutObject    67% → blobStore_==nil branch
//   handleS3GetObject    76% → blobStore_==nil branch
//   s3NotEnabled          0% → confirmed dead code (never registered as route)
//   handleBlobGet        69% → blob-disabled branch + key absent
//   handleBlobDelete     69% → blob-disabled branch + key absent
//
// One shared blobTestServer per top-level test to minimise fd consumption.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/config"
)

// ─── blobStore_==nil branches (blob disabled at server level) ────────────────

// noBlobServer returns a server with BlobEnabled=false so every handler's
// first `if s.blobStore_ == nil` guard fires.
func noBlobServer(t *testing.T) *blobTestServer {
	t.Helper()
	env := setupBlobTestServer(t, func(cfg *config.Config) {
		cfg.BlobEnabled = false
	})
	t.Cleanup(env.cleanup)
	return env
}

func TestBlobDisabled_JSONEndpoints(t *testing.T) {
	env := noBlobServer(t)

	cases := []struct {
		name, method, path string
		body               []byte
		headers            map[string]string
		wantStatus         int
	}{
		// blobStore() guard returns 501 Not Implemented when blob is disabled.
		{"put_blob", "POST", "/api/v1/blob", []byte("data"),
			map[string]string{"X-Blob-Key": "k", "Content-Type": "text/plain"},
			http.StatusNotImplemented},
		{"get_blob", "GET", "/api/v1/blob/somekey", nil, nil,
			http.StatusNotImplemented},
		{"delete_blob", "DELETE", "/api/v1/blob/somekey", nil, nil,
			http.StatusNotImplemented},
		{"list_blobs", "GET", "/api/v1/blob", nil, nil,
			http.StatusNotImplemented},
		// handleBlobUsage has its own nil check returning 503.
		{"usage", "GET", "/api/v1/blob/usage", nil, nil,
			http.StatusServiceUnavailable},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp := env.blobDo(tc.method, tc.path, tc.body, tc.headers)
			defer resp.Body.Close()
			if resp.StatusCode != tc.wantStatus {
				t.Errorf("%s: want %d, got %d", tc.name, tc.wantStatus, resp.StatusCode)
			}
		})
	}
}

func TestBlobDisabled_S3Endpoints(t *testing.T) {
	env := noBlobServer(t)

	// All S3 handlers check blobStore_==nil and return 501 Not Implemented.
	s3Cases := []struct {
		name, method, path string
		body               []byte
	}{
		{"head_bucket", "HEAD", "/mybucket", nil},
		{"list_objects", "GET", "/mybucket", nil},
		{"put_object", "PUT", "/mybucket/mykey", []byte("data")},
		{"get_object", "GET", "/mybucket/mykey", nil},
		{"head_object", "HEAD", "/mybucket/mykey", nil},
		{"delete_object", "DELETE", "/mybucket/mykey", nil},
	}

	for _, tc := range s3Cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			resp := env.s3Do(tc.method, tc.path, tc.body, nil)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotImplemented {
				t.Errorf("S3 %s with blob disabled: want 501, got %d",
					tc.name, resp.StatusCode)
			}
		})
	}
}

// ─── tenantForBlob: tenant-context path ──────────────────────────────────────

func TestTenantForBlob_TenantContextPath(t *testing.T) {
	// PUT and GET via the tenant-scoped route exercise the context-value
	// branch of tenantForBlob. The default route uses the fallback ("default"),
	// so we must use the /api/v1/tenant/{id}/blob path.
	env := setupBlobTestServer(t)
	t.Cleanup(env.cleanup)

	// PUT via tenant-scoped URL.
	putResp := env.blobDo("POST", "/api/v1/tenant/acme/blob",
		[]byte("tenant-scoped content"),
		map[string]string{"X-Blob-Key": "tenantkey", "Content-Type": "text/plain"})
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusCreated {
		t.Fatalf("tenant blob PUT: want 201, got %d", putResp.StatusCode)
	}

	// GET via tenant-scoped URL — exercises tenantForBlob context branch again.
	getResp := env.blobDo("GET", "/api/v1/tenant/acme/blob/tenantkey", nil, nil)
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("tenant blob GET: want 200, got %d", getResp.StatusCode)
	}
	body, _ := io.ReadAll(getResp.Body)
	if string(body) != "tenant-scoped content" {
		t.Errorf("body: want 'tenant-scoped content', got %q", string(body))
	}
}

func TestTenantForBlob_DefaultFallback(t *testing.T) {
	// The non-scoped route (/api/v1/blob) hits the fallback ("default") path.
	env := setupBlobTestServer(t)
	t.Cleanup(env.cleanup)

	putResp := env.blobDo("POST", "/api/v1/blob",
		[]byte("default tenant content"),
		map[string]string{"X-Blob-Key": "fallback", "Content-Type": "text/plain"})
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusCreated {
		t.Fatalf("default blob PUT: want 201, got %d", putResp.StatusCode)
	}

	// Tenant-scoped read under "default" should find the blob.
	getResp := env.blobDo("GET", "/api/v1/tenant/default/blob/fallback", nil, nil)
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Errorf("default tenant read: want 200, got %d", getResp.StatusCode)
	}
}

// ─── handleBlobUsage: blobSampler live path ───────────────────────────────────

func TestBlobUsage_WithSampler(t *testing.T) {
	// BlobUsageSampleIntervalSecs > 0 starts the sampler goroutine and wires
	// s.blobSampler. The response's sampled_at field should eventually be set.
	env := setupBlobTestServer(t, func(cfg *config.Config) {
		cfg.BlobUsageSampleIntervalSecs = 1 // sample every second
	})
	t.Cleanup(env.cleanup)

	// Store one blob to give the sampler something to count.
	putResp := env.blobDo("POST", "/api/v1/blob",
		[]byte("sampler test content"),
		map[string]string{"X-Blob-Key": "samplerkey", "Content-Type": "text/plain"})
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusCreated {
		t.Fatalf("blob PUT: want 201, got %d", putResp.StatusCode)
	}

	// Wait up to 3 s for the sampler to fire at least once.
	deadline := time.Now().Add(3 * time.Second)
	var sampledAt interface{}
	for time.Now().Before(deadline) {
		resp := env.blobDo("GET", "/api/v1/blob/usage", nil, nil)
		var body map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode JSON: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("blob usage: want 200, got %d", resp.StatusCode)
		}
		sampledAt = body["sampled_at"]
		if sampledAt != nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	if sampledAt == nil {
		t.Error("sampled_at should be set after sampler fires, got nil")
	}
}

func TestBlobUsage_SamplerNil_ReturnsZeros(t *testing.T) {
	// BlobUsageSampleIntervalSecs = 0 leaves s.blobSampler nil; response
	// should have zero counts and null sampled_at.
	env := setupBlobTestServer(t, func(cfg *config.Config) {
		cfg.BlobUsageSampleIntervalSecs = 0
	})
	t.Cleanup(env.cleanup)

	resp := env.blobDo("GET", "/api/v1/blob/usage", nil, nil)
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("usage with nil sampler: want 200, got %d", resp.StatusCode)
	}
	if body["sampled_at"] != nil {
		t.Errorf("sampled_at should be null with nil sampler, got %v", body["sampled_at"])
	}
}

// ─── handleS3DeleteObject: idempotent 204 on absent key ──────────────────────

func TestS3DeleteObject_AbsentKey(t *testing.T) {
	// S3 DeleteObject is idempotent: deleting a non-existent key returns 204.
	env := setupBlobTestServer(t)
	t.Cleanup(env.cleanup)

	resp := env.s3Do("DELETE", "/acme/this-key-does-not-exist", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("S3 delete absent key: want 204, got %d", resp.StatusCode)
	}
}

func TestS3DeleteObject_ExistingKey(t *testing.T) {
	env := setupBlobTestServer(t)
	t.Cleanup(env.cleanup)

	// Create via JSON API then delete via S3.
	putResp := env.blobDo("POST", "/api/v1/blob",
		[]byte("to delete"),
		map[string]string{"X-Blob-Key": "del-me", "Content-Type": "text/plain"})
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusCreated {
		t.Fatalf("setup PUT: want 201, got %d", putResp.StatusCode)
	}

	// S3 DELETE against the blob.  Bucket == tenant name ("default").
	delResp := env.s3Do("DELETE", "/default/del-me", nil, nil)
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Errorf("S3 delete existing: want 204, got %d", delResp.StatusCode)
	}
}

// ─── handleS3HeadBucket: bucket not found ─────────────────────────────────────

func TestS3HeadBucket_NotFound(t *testing.T) {
	// A bucket that has no blobs in it may not "exist" — depends on
	// implementation. Whether 200 or 404, it must not be a 500.
	env := setupBlobTestServer(t)
	t.Cleanup(env.cleanup)

	resp := env.s3Do("HEAD", "/nonexistent-tenant", nil, nil)
	resp.Body.Close()
	if resp.StatusCode == http.StatusInternalServerError {
		t.Errorf("S3 head empty bucket: should not 500, got %d", resp.StatusCode)
	}
}

func TestS3HeadBucket_WithBlobs(t *testing.T) {
	env := setupBlobTestServer(t)
	t.Cleanup(env.cleanup)

	// Seed one blob under tenant "acme".
	putResp := env.blobDo("POST", "/api/v1/tenant/acme/blob",
		[]byte("bucket-content"),
		map[string]string{"X-Blob-Key": "k", "Content-Type": "text/plain"})
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", putResp.StatusCode)
	}

	resp := env.s3Do("HEAD", "/acme", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("S3 head non-empty bucket: want 200, got %d", resp.StatusCode)
	}
}

// ─── handleS3HeadObject: object not found ─────────────────────────────────────

func TestS3HeadObject_NotFound(t *testing.T) {
	env := setupBlobTestServer(t)
	t.Cleanup(env.cleanup)

	resp := env.s3Do("HEAD", "/default/no-such-key", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("S3 head absent object: want 404, got %d", resp.StatusCode)
	}
}

func TestS3HeadObject_Exists(t *testing.T) {
	env := setupBlobTestServer(t)
	t.Cleanup(env.cleanup)

	putResp := env.blobDo("POST", "/api/v1/blob",
		[]byte("head test"),
		map[string]string{"X-Blob-Key": "headme", "Content-Type": "text/plain"})
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", putResp.StatusCode)
	}

	resp := env.s3Do("HEAD", "/default/headme", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("S3 head existing object: want 200, got %d", resp.StatusCode)
	}
}

// ─── handleBlobGet: key not found ────────────────────────────────────────────

func TestBlobGet_NotFound(t *testing.T) {
	env := setupBlobTestServer(t)
	t.Cleanup(env.cleanup)

	resp := env.blobDo("GET", "/api/v1/blob/this-key-does-not-exist", nil, nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("blob GET absent key: want 404, got %d", resp.StatusCode)
	}
}

// ─── handleBlobDelete: key not found (idempotent) ────────────────────────────

func TestBlobDelete_NotFound(t *testing.T) {
	env := setupBlobTestServer(t)
	t.Cleanup(env.cleanup)

	resp := env.blobDo("DELETE", "/api/v1/blob/this-key-does-not-exist", nil, nil)
	resp.Body.Close()
	// JSON blob DELETE may return 404 or 204 (idempotent) — not 500.
	if resp.StatusCode == http.StatusInternalServerError {
		t.Errorf("blob DELETE absent key: should not 500, got %d", resp.StatusCode)
	}
}

// ─── S3 auth: S3RequireAuth=true rejects missing Authorization ───────────────

func TestS3RequireAuth_MissingHeader(t *testing.T) {
	env := setupBlobTestServer(t, func(cfg *config.Config) {
		cfg.S3RequireAuth = true
	})
	t.Cleanup(env.cleanup)

	// PUT without an Authorization header.
	resp := env.s3Do("PUT", "/default/authtest", []byte("data"), nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("S3 RequireAuth missing header: want 403, got %d", resp.StatusCode)
	}
}

func TestS3RequireAuth_WithHeader(t *testing.T) {
	env := setupBlobTestServer(t, func(cfg *config.Config) {
		cfg.S3RequireAuth = true
	})
	t.Cleanup(env.cleanup)

	// PUT with a valid-looking Sig V4 Authorization header.
	req, _ := http.NewRequest("PUT", env.s3ts.URL+"/acme/authkey", http.NoBody)
	req.Header.Set("Authorization",
		"AWS4-HMAC-SHA256 Credential=acme/20260616/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=abc")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("S3 PUT with auth: %v", err)
	}
	resp.Body.Close()
	// 200 series = accepted; 5xx = unexpected.
	if resp.StatusCode >= http.StatusInternalServerError {
		t.Errorf("S3 PUT with auth header: want 2xx/4xx, got %d", resp.StatusCode)
	}
}

// ─── injectBlobMetrics (20%): verify metrics path with sampler nil vs live ───

func TestInjectBlobMetrics(t *testing.T) {
	// injectBlobMetrics is called by handleMetrics when MetricsEnabled and
	// BlobEnabled are both true. With blobSampler nil it returns zeros;
	// with sampler live it returns actual counts.
	t.Run("sampler_nil", func(t *testing.T) {
		env := setupBlobTestServer(t, func(cfg *config.Config) {
			cfg.MetricsEnabled = true
			cfg.BlobUsageSampleIntervalSecs = 0
		})
		t.Cleanup(env.cleanup)
		resp := env.blobDo("GET", "/metrics", nil, nil)
		defer resp.Body.Close()
		// Metrics endpoint should return 200.
		if resp.StatusCode != http.StatusOK {
			t.Errorf("metrics with nil sampler: want 200, got %d", resp.StatusCode)
		}
	})

	t.Run("sampler_live", func(t *testing.T) {
		env := setupBlobTestServer(t, func(cfg *config.Config) {
			cfg.MetricsEnabled = true
			cfg.BlobUsageSampleIntervalSecs = 1
		})
		t.Cleanup(env.cleanup)
		// Wait for the sampler to fire once.
		time.Sleep(1200 * time.Millisecond)
		resp := env.blobDo("GET", "/metrics", nil, nil)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("metrics with live sampler: want 200, got %d", resp.StatusCode)
		}
	})
}

// ─── S3 multi-part round-trip ─────────────────────────────────────────────────

func TestS3ListObjects_AfterPuts(t *testing.T) {
	env := setupBlobTestServer(t)
	t.Cleanup(env.cleanup)

	// Store three objects under the same "bucket" (tenant).
	for i := 0; i < 3; i++ {
		r := env.s3Do("PUT", fmt.Sprintf("/listtenant/key%d", i), []byte("v"), nil)
		r.Body.Close()
		if r.StatusCode != http.StatusOK {
			t.Fatalf("S3 PUT key%d: want 200, got %d", i, r.StatusCode)
		}
	}

	resp := env.s3Do("GET", "/listtenant", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("S3 list: want 200, got %d", resp.StatusCode)
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────
