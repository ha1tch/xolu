// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// Handler tests for the blob JSON API (pkg/server/blob_handlers.go) and the
// S3-compatible surface (pkg/server/blob_s3_handlers.go).
//
// These tests use an httptest.Server built around a blob-enabled Server
// instance. All tests run without network access — the blob store is backed
// by a t.TempDir().

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/cache"
	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/server"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/validation"
	"github.com/rs/zerolog"
	"os"
	"path/filepath"
)

// ---------------------------------------------------------------------------
// Blob-enabled test server
// ---------------------------------------------------------------------------

type blobTestServer struct {
	srv  *server.Server
	ts   *httptest.Server
	s3ts *httptest.Server // wired to the S3 chi router
	cfg  *config.Config
	t    *testing.T
}

func setupBlobTestServer(t *testing.T, opts ...func(*config.Config)) *blobTestServer {
	t.Helper()
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Host:                "localhost",
		Port:                0,
		BaseDir:             tmpDir,
		Schema:              "test_schema",
		SchemaDir:           filepath.Join(tmpDir, "test_schema"),
		CacheType:           "memory",
		CacheTTL:            300,
		GraphEnabled:        true,
		GraphMode:           "flat",
		FullTextEnabled:     false,
		CascadingDelete:     false,
		RefEmbedDepth:       3,
		MaxEmbedDepth:       10,
		MaxEntitySize:       1048576,
		PatchNullBehavior:   "store",
		MaxCascadeDeletions: 100,
		TenantMode:          "path",
		TenantAutoRegister:  true,

		BlobEnabled:                 true,
		BlobMaxSize:                 1048576, // 1 MiB per blob
		BlobMaxTotalBytes:           0,       // no quota by default
		BlobUsageSampleIntervalSecs: 0,       // sampler off in tests
		S3Enabled:                   false,   // S3 listener managed manually below
		S3Port:                      0,
		S3RequireAuth:               false,
	}
	for _, opt := range opts {
		opt(cfg)
	}

	// Place the base store where the invariant layout dictates (t0000/store or
	// shared/store), not at a loose path. This keeps the test's on-disk tree
	// conformant with storelayout.Check and representative of a real instance.
	dbPath := baseStorePath(cfg)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatalf("setupBlobTestServer: mkdir base store dir: %v", err)
	}
	store, err := storage.NewStore("sqlite", map[string]interface{}{"db_path": dbPath})
	if err != nil {
		t.Fatal(err)
	}
	memCache := cache.NewMemoryCache(1000, time.Duration(cfg.CacheTTL)*time.Second)
	g := graph.NewFlatGraph()
	schemaDir := filepath.Join(cfg.BaseDir, cfg.Schema, "_schemas")
	validator := validation.NewJSONSchemaValidator(schemaDir)
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	srv := server.New(cfg, store, memCache, g, validator, logger)
	ts := httptest.NewServer(srv.Handler())

	// Wire up the S3 router to its own httptest server so tests can reach it
	// without an actual TCP port.
	s3ts := httptest.NewServer(srv.S3Handler())

	return &blobTestServer{
		srv:  srv,
		ts:   ts,
		s3ts: s3ts,
		cfg:  cfg,
		t:    t,
	}
}

func (b *blobTestServer) cleanup() {
	b.ts.Close()
	b.s3ts.Close()
}

// blobDo sends a request to the main JSON API.
func (b *blobTestServer) blobDo(method, path string, body []byte, headers map[string]string) *http.Response {
	b.t.Helper()
	var bodyR io.Reader
	if body != nil {
		bodyR = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, b.ts.URL+path, bodyR)
	if err != nil {
		b.t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	return resp
}

// s3Do sends a request to the S3-compatible listener.
func (b *blobTestServer) s3Do(method, path string, body []byte, headers map[string]string) *http.Response {
	b.t.Helper()
	var bodyR io.Reader
	if body != nil {
		bodyR = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, b.s3ts.URL+path, bodyR)
	if err != nil {
		b.t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	return resp
}

func readBody(t *testing.T, r *http.Response) []byte {
	t.Helper()
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body := readBody(t, resp)
		t.Fatalf("status = %d, want %d\nbody: %s", resp.StatusCode, want, body)
	}
}

// sigV4Auth returns a minimal (unverified) AWS Sig V4 Authorization header
// with the given access key as tenant name.
func sigV4Auth(accessKey string) string {
	return fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/20260608/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=deadbeef",
		accessKey,
	)
}

// ---------------------------------------------------------------------------
// JSON API — PUT
// ---------------------------------------------------------------------------

func TestBlobHandler_Put_WithKey(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	content := []byte("hello blob handler")
	resp := ts.blobDo("POST", "/api/v1/blob", content, map[string]string{
		"X-Blob-Key":   "mykey",
		"Content-Type": "text/plain",
	})
	assertStatus(t, resp, http.StatusCreated)

	var got map[string]interface{}
	json.Unmarshal(readBody(t, resp), &got)
	if got["key"] != "mykey" {
		t.Errorf("key = %v, want mykey", got["key"])
	}
	if got["sha256"] == "" {
		t.Error("sha256 missing from response")
	}
}

func TestBlobHandler_Put_NoKey_UsesSHA(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	content := []byte("content addressed")
	resp := ts.blobDo("POST", "/api/v1/blob", content, map[string]string{
		"Content-Type": "application/octet-stream",
	})
	assertStatus(t, resp, http.StatusCreated)

	var got map[string]interface{}
	json.Unmarshal(readBody(t, resp), &got)
	// Key and SHA256 should be identical for PutRaw path.
	if got["key"] != got["sha256"] {
		t.Errorf("PutRaw: key=%v sha256=%v — should be equal", got["key"], got["sha256"])
	}
}

func TestBlobHandler_Put_InvalidKey(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	resp := ts.blobDo("POST", "/api/v1/blob", []byte("data"), map[string]string{
		"X-Blob-Key": "bad/key",
	})
	assertStatus(t, resp, http.StatusBadRequest)
}

func TestBlobHandler_Put_TooLarge(t *testing.T) {
	ts := setupBlobTestServer(t, func(c *config.Config) {
		c.BlobMaxSize = 10
	})
	defer ts.cleanup()

	resp := ts.blobDo("POST", "/api/v1/blob", bytes.Repeat([]byte("x"), 20), map[string]string{
		"X-Blob-Key": "k",
	})
	assertStatus(t, resp, http.StatusRequestEntityTooLarge)
}

func TestBlobHandler_Put_Disabled(t *testing.T) {
	// Use the standard test server which never sets BlobEnabled — this removes
	// the implicit dependency on option-application ordering in
	// setupBlobTestServer and makes the disabled state structurally guaranteed.
	ts := setupTestServer(t)
	defer ts.cleanup()

	resp, _ := ts.doRequest("POST", "/api/v1/blob", []byte("data"))
	assertStatus(t, resp, http.StatusNotImplemented)
}

// ---------------------------------------------------------------------------
// JSON API — GET
// ---------------------------------------------------------------------------

func TestBlobHandler_Get_Found(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	content := []byte("retrieve me")
	ts.blobDo("POST", "/api/v1/blob", content, map[string]string{
		"X-Blob-Key":   "getkey",
		"Content-Type": "text/plain; charset=utf-8",
	})

	resp := ts.blobDo("GET", "/api/v1/blob/getkey", nil, nil)
	assertStatus(t, resp, http.StatusOK)

	got := readBody(t, resp)
	if !bytes.Equal(got, content) {
		t.Errorf("body = %q, want %q", got, content)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/plain; charset=utf-8")
	}
	if resp.Header.Get("X-Blob-SHA256") == "" {
		t.Error("X-Blob-SHA256 header missing")
	}
	if resp.Header.Get("ETag") == "" {
		t.Error("ETag header missing")
	}
}

func TestBlobHandler_Get_NotFound(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	resp := ts.blobDo("GET", "/api/v1/blob/nosuchkey", nil, nil)
	assertStatus(t, resp, http.StatusNotFound)
}

// ---------------------------------------------------------------------------
// JSON API — HEAD
// ---------------------------------------------------------------------------

func TestBlobHandler_Head_Found(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	content := []byte("head test")
	ts.blobDo("POST", "/api/v1/blob", content, map[string]string{
		"X-Blob-Key":   "headkey",
		"Content-Type": "image/png",
	})

	resp := ts.blobDo("HEAD", "/api/v1/blob/headkey", nil, nil)
	assertStatus(t, resp, http.StatusOK)
	readBody(t, resp)

	if resp.Header.Get("X-Blob-SHA256") == "" {
		t.Error("X-Blob-SHA256 missing from HEAD response")
	}
	if resp.Header.Get("Content-Length") == "" {
		t.Error("Content-Length missing from HEAD response")
	}
	if resp.Header.Get("Content-Type") != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", resp.Header.Get("Content-Type"))
	}
}

func TestBlobHandler_Head_NotFound(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	resp := ts.blobDo("HEAD", "/api/v1/blob/nosuchkey", nil, nil)
	assertStatus(t, resp, http.StatusNotFound)
	readBody(t, resp)
}

// ---------------------------------------------------------------------------
// JSON API — DELETE
// ---------------------------------------------------------------------------

func TestBlobHandler_Delete_Found(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	ts.blobDo("POST", "/api/v1/blob", []byte("bye"), map[string]string{"X-Blob-Key": "delkey"})

	resp := ts.blobDo("DELETE", "/api/v1/blob/delkey", nil, nil)
	assertStatus(t, resp, http.StatusOK)

	var got map[string]interface{}
	json.Unmarshal(readBody(t, resp), &got)
	if got["deleted"] != true {
		t.Errorf("deleted = %v, want true", got["deleted"])
	}

	// Subsequent GET must 404.
	resp2 := ts.blobDo("GET", "/api/v1/blob/delkey", nil, nil)
	assertStatus(t, resp2, http.StatusNotFound)
	readBody(t, resp2)
}

func TestBlobHandler_Delete_NotFound(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	resp := ts.blobDo("DELETE", "/api/v1/blob/nosuchkey", nil, nil)
	assertStatus(t, resp, http.StatusNotFound)
	readBody(t, resp)
}

// ---------------------------------------------------------------------------
// JSON API — LIST
// ---------------------------------------------------------------------------

func TestBlobHandler_List_Empty(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	resp := ts.blobDo("GET", "/api/v1/blob", nil, nil)
	assertStatus(t, resp, http.StatusOK)

	var got map[string]interface{}
	json.Unmarshal(readBody(t, resp), &got)
	if got["count"].(float64) != 0 {
		t.Errorf("count = %v, want 0", got["count"])
	}
}

func TestBlobHandler_List_WithPrefix(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	for _, k := range []string{"apple", "apricot", "banana"} {
		ts.blobDo("POST", "/api/v1/blob", []byte(k), map[string]string{"X-Blob-Key": k})
	}

	resp := ts.blobDo("GET", "/api/v1/blob?prefix=ap", nil, nil)
	assertStatus(t, resp, http.StatusOK)

	var got map[string]interface{}
	json.Unmarshal(readBody(t, resp), &got)
	if got["count"].(float64) != 2 {
		t.Errorf("count = %v, want 2 for prefix=ap", got["count"])
	}
}

func TestBlobHandler_List_SortedAscending(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	for _, k := range []string{"cherry", "apple", "banana"} {
		ts.blobDo("POST", "/api/v1/blob", []byte(k), map[string]string{"X-Blob-Key": k})
	}

	resp := ts.blobDo("GET", "/api/v1/blob", nil, nil)
	var got struct {
		Blobs []struct {
			Key string `json:"key"`
		} `json:"blobs"`
	}
	json.Unmarshal(readBody(t, resp), &got)

	keys := make([]string, len(got.Blobs))
	for i, b := range got.Blobs {
		keys[i] = b.Key
	}
	want := []string{"apple", "banana", "cherry"}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("list[%d] = %q, want %q", i, keys[i], want[i])
		}
	}
}

// ---------------------------------------------------------------------------
// JSON API — USAGE
// ---------------------------------------------------------------------------

func TestBlobHandler_Usage_SamplerOff(t *testing.T) {
	// Sampler disabled (interval=0) — response is valid with zero counts.
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	ts.blobDo("POST", "/api/v1/blob", []byte("data"), map[string]string{"X-Blob-Key": "k"})

	resp := ts.blobDo("GET", "/api/v1/blob/usage", nil, nil)
	assertStatus(t, resp, http.StatusOK)

	var got map[string]interface{}
	json.Unmarshal(readBody(t, resp), &got)
	if got["tenant"] == nil {
		t.Error("tenant field missing from usage response")
	}
	// sampled_at should be absent when sampler hasn't run.
	if _, present := got["sampled_at"]; present {
		t.Error("sampled_at present but sampler was not enabled")
	}
}

func TestBlobHandler_Usage_Disabled(t *testing.T) {
	ts := setupBlobTestServer(t, func(c *config.Config) { c.BlobEnabled = false })
	defer ts.cleanup()

	resp := ts.blobDo("GET", "/api/v1/blob/usage", nil, nil)
	assertStatus(t, resp, http.StatusServiceUnavailable)
	readBody(t, resp)
}

// ---------------------------------------------------------------------------
// Quota enforcement
// ---------------------------------------------------------------------------

func TestBlobHandler_QuotaExceeded(t *testing.T) {
	// Seed the server's tenant-0 blob store with content that already fills the
	// quota. Blobs are per-tenant (TenantBlobDir), so the seed goes through the
	// server's own store for tenant 0 — the same store the running server reads.
	// Any further Put must be rejected with XOLU-BL006.
	ts := setupBlobTestServer(t, func(c *config.Config) {
		c.BlobMaxTotalBytes = 5              // 5 bytes — exactly filled by "hello"
		c.BlobUsageSampleIntervalSecs = 3600 // long interval — we drive the sampler manually
	})
	defer ts.cleanup()

	// Write through the server's own tenant-0 blob store so the sampler walk
	// finds it and the per-tenant sampler is created.
	seedStore, err := ts.srv.BlobStoreForTest(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = seedStore.Put("seed", bytes.NewReader([]byte("hello")), ""); err != nil {
		t.Fatal(err)
	}

	// Drive the sampler synchronously so the cache is deterministically
	// primed before we assert quota behaviour. No polling, no timers.
	if smp := ts.srv.BlobSamplerFor(0); smp != nil {
		smp.ForceResample()
	}

	// Any further Put for the default tenant should be rejected.
	resp := ts.blobDo("POST", "/api/v1/blob", []byte("one more"), map[string]string{
		"X-Blob-Key": "overflow",
	})
	assertStatus(t, resp, http.StatusRequestEntityTooLarge)

	body := readBody(t, resp)
	if !strings.Contains(string(body), "XOLU-BL006") {
		t.Errorf("expected XOLU-BL006 in response, got: %s", body)
	}
}

// ---------------------------------------------------------------------------
// S3 — PutObject / GetObject / HeadObject / DeleteObject
// ---------------------------------------------------------------------------

func TestS3Handler_PutGet(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	content := []byte("s3 content")
	resp := ts.s3Do("PUT", "/mybucket/mykey", content, map[string]string{
		"Authorization": sigV4Auth("mybucket"),
		"Content-Type":  "text/plain",
	})
	assertStatus(t, resp, http.StatusOK)
	readBody(t, resp)

	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Error("ETag header missing from PutObject response")
	}

	resp2 := ts.s3Do("GET", "/mybucket/mykey", nil, map[string]string{
		"Authorization": sigV4Auth("mybucket"),
	})
	assertStatus(t, resp2, http.StatusOK)
	got := readBody(t, resp2)
	if !bytes.Equal(got, content) {
		t.Errorf("GetObject body = %q, want %q", got, content)
	}
}

func TestS3Handler_HeadObject(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	content := []byte("head me s3")
	ts.s3Do("PUT", "/b/k", content, map[string]string{
		"Authorization": sigV4Auth("b"),
		"Content-Type":  "application/octet-stream",
	})

	resp := ts.s3Do("HEAD", "/b/k", nil, map[string]string{
		"Authorization": sigV4Auth("b"),
	})
	assertStatus(t, resp, http.StatusOK)
	readBody(t, resp)

	if resp.Header.Get("ETag") == "" {
		t.Error("ETag missing from HeadObject response")
	}
	if resp.Header.Get("Content-Length") == "" {
		t.Error("Content-Length missing from HeadObject response")
	}
}

func TestS3Handler_DeleteObject(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	ts.s3Do("PUT", "/b/k", []byte("delete me"), map[string]string{
		"Authorization": sigV4Auth("b"),
	})

	resp := ts.s3Do("DELETE", "/b/k", nil, map[string]string{
		"Authorization": sigV4Auth("b"),
	})
	assertStatus(t, resp, http.StatusNoContent)
	readBody(t, resp)

	resp2 := ts.s3Do("GET", "/b/k", nil, map[string]string{
		"Authorization": sigV4Auth("b"),
	})
	assertStatus(t, resp2, http.StatusNotFound)
	readBody(t, resp2)
}

func TestS3Handler_GetObject_NotFound(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	resp := ts.s3Do("GET", "/b/nosuchkey", nil, map[string]string{
		"Authorization": sigV4Auth("b"),
	})
	assertStatus(t, resp, http.StatusNotFound)

	// Response must be S3-style XML.
	body := readBody(t, resp)
	var xmlErr struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
	}
	if err := xml.Unmarshal(body, &xmlErr); err != nil {
		t.Errorf("S3 error response is not valid XML: %v\nbody: %s", err, body)
	}
	if xmlErr.Code == "" {
		t.Error("S3 error XML missing Code element")
	}
}

// ---------------------------------------------------------------------------
// S3 — ListObjectsV2
// ---------------------------------------------------------------------------

func TestS3Handler_ListObjects(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	for _, k := range []string{"a", "b", "c"} {
		ts.s3Do("PUT", "/bucket/"+k, []byte(k), map[string]string{
			"Authorization": sigV4Auth("bucket"),
		})
	}

	resp := ts.s3Do("GET", "/bucket?list-type=2", nil, map[string]string{
		"Authorization": sigV4Auth("bucket"),
	})
	assertStatus(t, resp, http.StatusOK)

	body := readBody(t, resp)
	var result struct {
		XMLName  xml.Name `xml:"ListBucketResult"`
		KeyCount int      `xml:"KeyCount"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("ListObjectsV2 XML parse error: %v\nbody: %s", err, body)
	}
	if result.KeyCount != 3 {
		t.Errorf("KeyCount = %d, want 3", result.KeyCount)
	}
}

// ---------------------------------------------------------------------------
// S3 — HeadBucket
// ---------------------------------------------------------------------------

func TestS3Handler_HeadBucket(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	// Write something so the tenant directory exists.
	ts.s3Do("PUT", "/mybkt/k", []byte("x"), map[string]string{
		"Authorization": sigV4Auth("mybkt"),
	})

	resp := ts.s3Do("HEAD", "/mybkt", nil, map[string]string{
		"Authorization": sigV4Auth("mybkt"),
	})
	assertStatus(t, resp, http.StatusOK)
	readBody(t, resp)
}

// ---------------------------------------------------------------------------
// S3 — Auth behaviour
// ---------------------------------------------------------------------------

func TestS3Handler_NoAuth_FallbackWarns(t *testing.T) {
	// Without S3RequireAuth, missing Authorization falls back to bucket name.
	// Should succeed (no 403).
	ts := setupBlobTestServer(t, func(c *config.Config) {
		c.S3RequireAuth = false
	})
	defer ts.cleanup()

	resp := ts.s3Do("PUT", "/fallbacktenant/k", []byte("data"), map[string]string{
		"Content-Type": "application/octet-stream",
		// No Authorization header.
	})
	// 200 OK expected — bucket name used as tenant.
	assertStatus(t, resp, http.StatusOK)
	readBody(t, resp)
}

func TestS3Handler_RequireAuth_RejectsNoAuth(t *testing.T) {
	ts := setupBlobTestServer(t, func(c *config.Config) {
		c.S3Enabled = true
		c.S3RequireAuth = true
	})
	defer ts.cleanup()

	resp := ts.s3Do("PUT", "/b/k", []byte("data"), nil) // no Authorization
	assertStatus(t, resp, http.StatusForbidden)

	body := readBody(t, resp)
	var xmlErr struct {
		XMLName xml.Name `xml:"Error"`
		Code    string   `xml:"Code"`
	}
	if err := xml.Unmarshal(body, &xmlErr); err != nil {
		t.Errorf("S3 403 response not valid XML: %v\nbody: %s", err, body)
	}
	if xmlErr.Code != "AccessDenied" {
		t.Errorf("Code = %q, want AccessDenied", xmlErr.Code)
	}
}

// ---------------------------------------------------------------------------
// Quota precedence chain
// ---------------------------------------------------------------------------

// setupBlobDCTestServer creates a blob-enabled server that also has dynconfig
// (including the admin API) active, so the quota precedence chain
// (per-tenant → global → static) can be exercised via HTTP.
func setupBlobDCTestServer(t *testing.T, opts ...func(*config.Config)) *blobTestServer {
	t.Helper()
	dc := func(c *config.Config) {
		c.DynConfigEnabled = true
		c.DynConfigAPIEnabled = true
		c.DynConfigFile = filepath.Join(c.BaseDir, "dynconfig.json")
		c.DynConfigReloadSecs = 3600
	}
	all := append([]func(*config.Config){dc}, opts...)
	return setupBlobTestServer(t, all...)
}

// dcSet sets a dynconfig key via the admin API on a blobTestServer.
func dcSet(t *testing.T, ts *blobTestServer, namespace, key string, val []byte) {
	t.Helper()
	path := fmt.Sprintf("/api/v1/admin/config/%s/%s", namespace, key)
	req, err := http.NewRequest("PUT", ts.ts.URL+path, bytes.NewReader(val))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("dcSet %s/%s: status %d, body: %s", namespace, key, resp.StatusCode, b)
	}
}

// TestBlobHandler_QuotaPrecedence_GlobalDynConfigOverridesStatic verifies that
// a global dynconfig override (level 2) takes precedence over the static env
// default (level 3), blocking a Put when the dynamic limit is lower.
func TestBlobHandler_QuotaPrecedence_GlobalDynConfigOverridesStatic(t *testing.T) {
	ts := setupBlobDCTestServer(t, func(c *config.Config) {
		c.BlobMaxTotalBytes = 0              // level 3: no static limit
		c.BlobUsageSampleIntervalSecs = 3600 // sampler driven manually
	})
	defer ts.cleanup()

	// Seed the default tenant (ID 0) with 10 bytes via the server's store.
	st, err := ts.srv.BlobStoreForTest(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := st.Put("seed", bytes.NewReader(bytes.Repeat([]byte("x"), 10)), ""); err != nil {
		t.Fatal(err)
	}
	if smp := ts.srv.BlobSamplerFor(0); smp != nil {
		smp.ForceResample()
	}

	// Set level 2 global dynconfig limit to 10 bytes (exactly at capacity).
	dcSet(t, ts, "global", "blob.max_bytes", []byte("10"))

	// A further Put must be rejected: sampled usage = 10 >= limit = 10.
	overflow := ts.blobDo("POST", "/api/v1/blob", []byte("overflow"), map[string]string{
		"X-Blob-Key": "overflow",
	})
	assertStatus(t, overflow, http.StatusRequestEntityTooLarge)
	body := readBody(t, overflow)
	if !strings.Contains(string(body), "XOLU-BL006") {
		t.Errorf("expected XOLU-BL006 in response, got: %s", body)
	}
}

// TestBlobHandler_QuotaPrecedence_PerTenantOverridesGlobal verifies that a
// per-tenant dynconfig override (level 1) takes precedence over the global
// dynconfig override (level 2).
func TestBlobHandler_QuotaPrecedence_PerTenantOverridesGlobal(t *testing.T) {
	ts := setupBlobDCTestServer(t, func(c *config.Config) {
		c.BlobMaxTotalBytes = 0              // level 3: no static limit
		c.BlobUsageSampleIntervalSecs = 3600 // sampler not triggered
	})
	defer ts.cleanup()

	// level 2: global limit of 1 byte — would reject if level 1 didn't win.
	dcSet(t, ts, "global", "blob.max_bytes", []byte("1"))
	// level 1: per-tenant limit of 1 MiB — must win over global.
	dcSet(t, ts, "tenant.default", "blob.max_bytes", []byte("1048576"))

	// Sampler not primed: Bytes=0, so the per-tenant check passes.
	// If global were used instead, the 1-byte limit would block the request.
	resp := ts.blobDo("POST", "/api/v1/blob", []byte("small content"), map[string]string{
		"X-Blob-Key": "ok-key",
	})
	assertStatus(t, resp, http.StatusCreated)
	readBody(t, resp)
}

// ---------------------------------------------------------------------------
// Cross-tenant isolation, read back through BOTH APIs
//
// Writes distinct blobs to two tenants (one via the native JSON API, one via
// the S3 API), then reads each back through both APIs and confirms:
//   - each tenant sees its own content through either API;
//   - neither tenant's key is visible under the other tenant;
//   - on disk, each tenant has its own <base>/tXXXX/blobs directory.
// This is the end-to-end proof that the per-tenant (B-clean) blob layout keeps
// tenants isolated regardless of which API wrote or read the data.
// ---------------------------------------------------------------------------

func TestBlob_CrossTenant_DualAPI_Isolation(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	const (
		alphaContent = "alpha-secret-payload"
		betaContent  = "beta-secret-payload"
		key          = "report.txt"
	)

	// --- Write alpha via the NATIVE JSON API ---
	resp := ts.blobDo("POST", "/api/v1/tenant/alpha/blob", []byte(alphaContent), map[string]string{
		"X-Blob-Key":   key,
		"Content-Type": "text/plain",
	})
	assertStatus(t, resp, http.StatusCreated)
	readBody(t, resp)

	// --- Write beta via the S3 API (bucket name = tenant) ---
	resp = ts.s3Do("PUT", "/beta/"+key, []byte(betaContent), map[string]string{
		"Authorization": sigV4Auth("beta"),
		"Content-Type":  "text/plain",
	})
	assertStatus(t, resp, http.StatusOK)
	readBody(t, resp)

	// --- Read alpha back through BOTH APIs ---
	resp = ts.blobDo("GET", "/api/v1/tenant/alpha/blob/"+key, nil, nil)
	assertStatus(t, resp, http.StatusOK)
	if got := string(readBody(t, resp)); got != alphaContent {
		t.Errorf("alpha via native API: got %q, want %q", got, alphaContent)
	}
	resp = ts.s3Do("GET", "/alpha/"+key, nil, map[string]string{"Authorization": sigV4Auth("alpha")})
	assertStatus(t, resp, http.StatusOK)
	if got := string(readBody(t, resp)); got != alphaContent {
		t.Errorf("alpha via S3 API: got %q, want %q", got, alphaContent)
	}

	// --- Read beta back through BOTH APIs ---
	resp = ts.s3Do("GET", "/beta/"+key, nil, map[string]string{"Authorization": sigV4Auth("beta")})
	assertStatus(t, resp, http.StatusOK)
	if got := string(readBody(t, resp)); got != betaContent {
		t.Errorf("beta via S3 API: got %q, want %q", got, betaContent)
	}
	resp = ts.blobDo("GET", "/api/v1/tenant/beta/blob/"+key, nil, nil)
	assertStatus(t, resp, http.StatusOK)
	if got := string(readBody(t, resp)); got != betaContent {
		t.Errorf("beta via native API: got %q, want %q", got, betaContent)
	}

	// --- Isolation: the same key under each tenant returns that tenant's
	//     content, never the other's (proves no shared namespace). ---
	resp = ts.blobDo("GET", "/api/v1/tenant/alpha/blob/"+key, nil, nil)
	assertStatus(t, resp, http.StatusOK)
	if got := string(readBody(t, resp)); got == betaContent {
		t.Error("alpha returned beta's content — tenants are NOT isolated")
	}

	// --- On-disk layout: each tenant has its own tXXXX/blobs directory, and
	//     there is no server-level <base>/blobs directory. ---
	base := ts.cfg.BaseDir
	for _, seg := range []string{"t0001", "t0002"} {
		dir := filepath.Join(base, seg, "blobs")
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("expected per-tenant blob directory %s to exist", dir)
		}
	}
	if _, err := os.Stat(filepath.Join(base, "blobs")); err == nil {
		t.Error("server-level <base>/blobs must not exist under the per-tenant layout")
	}
}

// ---------------------------------------------------------------------------
// Unknown tenant with auto-register disabled: blob requests must 404 rather
// than silently creating a tenant. Exercises the error path in blobTenantID /
// blobStoreFor / s3StoreFor that the default (auto-register on) harness never
// reaches.
// ---------------------------------------------------------------------------

func TestBlob_UnknownTenant_NoAutoRegister(t *testing.T) {
	ts := setupBlobTestServer(t, func(c *config.Config) {
		c.TenantAutoRegister = false
		c.TenantMode = "strict"
	})
	defer ts.cleanup()

	// Native API: PUT to an unregistered tenant → 404.
	resp := ts.blobDo("POST", "/api/v1/tenant/ghost/blob", []byte("x"),
		map[string]string{"X-Blob-Key": "k", "Content-Type": "text/plain"})
	assertStatus(t, resp, http.StatusNotFound)
	readBody(t, resp)

	// Native API: GET from an unregistered tenant → 404.
	resp = ts.blobDo("GET", "/api/v1/tenant/ghost/blob/k", nil, nil)
	assertStatus(t, resp, http.StatusNotFound)
	readBody(t, resp)

	// S3 API: PUT to an unregistered bucket → 404 (NoSuchBucket).
	resp = ts.s3Do("PUT", "/ghost/k", []byte("x"),
		map[string]string{"Authorization": sigV4Auth("ghost"), "Content-Type": "text/plain"})
	assertStatus(t, resp, http.StatusNotFound)
	readBody(t, resp)
}
