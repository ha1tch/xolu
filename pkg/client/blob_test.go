// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// blob_test.go — mock-based tests per the Stage 2 convention: happy
// path plus structured error per method. The real-server round trip
// lives in integration_test.go's TestIntegration_BlobFullFlow, which
// is what catches wire-shape drift these mocks structurally cannot.

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── BlobPut ────────────────────────────────────────────────────────────────

func TestBlobPutHappyPath_WithKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/blob" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method: got %s", r.Method)
		}
		if got := r.Header.Get("X-Blob-Key"); got != "readme.txt" {
			t.Errorf("X-Blob-Key: got %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "text/plain" {
			t.Errorf("Content-Type: got %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "hello blob" {
			t.Errorf("body: got %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"key":"readme.txt","sha256":"abc123","md5":"def456","size":10,"created":true}`))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.BlobPut(context.Background(), "readme.txt", "text/plain", strings.NewReader("hello blob"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Key != "readme.txt" {
		t.Errorf("Key: got %q", result.Key)
	}
	if result.SHA256 != "abc123" {
		t.Errorf("SHA256: got %q", result.SHA256)
	}
	if !result.Created {
		t.Error("Created: got false, want true")
	}
}

func TestBlobPutHappyPath_NoKey_ContentAddressed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Blob-Key"); got != "" {
			t.Errorf("X-Blob-Key should be absent when key is empty, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"key":"contentaddressedhash","sha256":"contentaddressedhash","created":false}`))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.BlobPut(context.Background(), "", "", strings.NewReader("dedup me"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Key != result.SHA256 {
		t.Errorf("content-addressed Key should equal SHA256: key=%q sha256=%q", result.Key, result.SHA256)
	}
	if result.Created {
		t.Error("Created: got true, want false (already existed)")
	}
}

func TestBlobPutDefaultsContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/octet-stream" {
			t.Errorf("Content-Type: got %q, want application/octet-stream default", got)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"key":"k","sha256":"h","created":false}`))
	}))
	defer server.Close()

	c := New(server.URL)
	if _, err := c.BlobPut(context.Background(), "k", "", bytes.NewReader([]byte{1, 2, 3})); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBlobPutInvalidKey_RejectedClientSide(t *testing.T) {
	serverCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := New(server.URL)
	cases := []string{"docs/readme.txt", `back\slash`, ".", "..", ".hidden"}
	for _, key := range cases {
		_, err := c.BlobPut(context.Background(), key, "text/plain", strings.NewReader("x"))
		if err == nil {
			t.Errorf("key %q: expected a validation error, got nil", key)
		}
	}
	if serverCalled {
		t.Error("server was called despite every key being invalid -- validation should happen before the request")
	}
}

func TestBlobGetHeadDelete_EmptyKeyRejectedClientSide(t *testing.T) {
	serverCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverCalled = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := New(server.URL)
	if _, err := c.BlobGet(context.Background(), ""); err == nil {
		t.Error("BlobGet(\"\"): expected an error, got nil")
	}
	if _, err := c.BlobHead(context.Background(), ""); err == nil {
		t.Error("BlobHead(\"\"): expected an error, got nil")
	}
	if _, err := c.BlobDelete(context.Background(), ""); err == nil {
		t.Error("BlobDelete(\"\"): expected an error, got nil")
	}
	if serverCalled {
		t.Error("server was called despite an empty key -- validation should happen before the request")
	}
}

func TestBlobPutQuotaExceeded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		w.Write([]byte(`{"error":{"code":"XOLU-BLOB003","message":"Tenant blob storage quota exceeded","status":413}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.BlobPut(context.Background(), "k", "text/plain", strings.NewReader("x"))
	xoluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusRequestEntityTooLarge {
		t.Errorf("HTTPStatus: got %d", xoluErr.HTTPStatus)
	}
	if xoluErr.Code != "XOLU-BLOB003" {
		t.Errorf("Code: got %q", xoluErr.Code)
	}
}

// ─── BlobGet ────────────────────────────────────────────────────────────────

func TestBlobGetHappyPath_StreamsBody(t *testing.T) {
	content := strings.Repeat("streamed content ", 100)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/v1/blob/my%20key.txt" {
			t.Errorf("escaped path: got %s (space should be escaped on the wire)", r.URL.EscapedPath())
		}
		if r.URL.Path != "/api/v1/blob/my key.txt" {
			t.Errorf("decoded path: got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method: got %s", r.Method)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("X-Blob-SHA256", "abc123")
		w.Header().Set("X-Blob-MD5", "def456")
		w.Header().Set("X-Blob-Size", "1700")
		w.Header().Set("ETag", `"abc123"`)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(content))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.BlobGet(context.Background(), "my key.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer result.Body.Close()

	if result.ContentType != "text/plain" {
		t.Errorf("ContentType: got %q", result.ContentType)
	}
	if result.SHA256 != "abc123" {
		t.Errorf("SHA256: got %q", result.SHA256)
	}
	if result.Size != 1700 {
		t.Errorf("Size: got %d", result.Size)
	}
	if result.ETag != `"abc123"` {
		t.Errorf("ETag: got %q", result.ETag)
	}

	got, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(got) != content {
		t.Errorf("body length: got %d bytes, want %d", len(got), len(content))
	}
}

func TestBlobGetNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-BLOB001","message":"Blob not found","status":404}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.BlobGet(context.Background(), "missing")
	if result != nil {
		t.Errorf("expected nil result on error, got %+v", result)
	}
	xoluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusNotFound {
		t.Errorf("HTTPStatus: got %d", xoluErr.HTTPStatus)
	}
}

// ─── BlobHead ───────────────────────────────────────────────────────────────

func TestBlobHeadHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("method: got %s", r.Method)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", "2048")
		w.Header().Set("X-Blob-SHA256", "abc123")
		w.Header().Set("ETag", `"abc123"`)
		w.Header().Set("X-Blob-Stored-At", "2026-08-03T02:04:33Z")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.BlobHead(context.Background(), "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ContentType != "image/png" {
		t.Errorf("ContentType: got %q", result.ContentType)
	}
	if result.Size != 2048 {
		t.Errorf("Size: got %d", result.Size)
	}
	if result.StoredAt.IsZero() {
		t.Error("StoredAt: not parsed")
	}
}

func TestBlobHeadNotFound_NoBodyOnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Real server sends a bare status with no JSON body for HEAD errors.
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.BlobHead(context.Background(), "missing")
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
	xoluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusNotFound {
		t.Errorf("HTTPStatus: got %d", xoluErr.HTTPStatus)
	}
}

// ─── BlobDelete ─────────────────────────────────────────────────────────────

func TestBlobDeleteHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("method: got %s", r.Method)
		}
		if r.URL.Path != "/api/v1/blob/k" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"key":"k","deleted":true}`))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.BlobDelete(context.Background(), "k")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Deleted {
		t.Error("Deleted: got false")
	}
}

// ─── BlobList ───────────────────────────────────────────────────────────────

func TestBlobListHappyPath_WithPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/blob" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("prefix"); got != "log-" {
			t.Errorf("prefix query param: got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"tenant":"t0000","prefix":"log-","count":2,"blobs":[
			{"key":"log-2026-01.txt","sha256":"h1","size":10,"stored_at":"2026-08-03T00:00:00Z"},
			{"key":"log-2026-02.txt","sha256":"h2","size":20,"stored_at":"2026-08-03T00:01:00Z"}
		]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.BlobList(context.Background(), "log-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Count != 2 || len(result.Blobs) != 2 {
		t.Fatalf("got %d blobs, want 2", len(result.Blobs))
	}
	if result.Blobs[0].Key != "log-2026-01.txt" {
		t.Errorf("Blobs[0].Key: got %q", result.Blobs[0].Key)
	}
}

func TestBlobListNoPrefix_OmitsQueryParam(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("expected no query string, got %q", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"tenant":"t0000","count":0,"blobs":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	if _, err := c.BlobList(context.Background(), ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ─── BlobUsage ──────────────────────────────────────────────────────────────

func TestBlobUsageHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/blob/usage" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"tenant":"t0000","blob_count":5,"key_count":7,"bytes":102400,"sampled_at":"2026-08-03T00:00:00Z"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.BlobUsage(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.BlobCount != 5 || result.Bytes != 102400 {
		t.Errorf("got %+v", result)
	}
	if result.SampledAt == nil {
		t.Error("SampledAt: got nil, want populated")
	}
}

func TestBlobUsageBeforeFirstSample_NilSampledAt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"tenant":"t0000","blob_count":0,"key_count":0,"bytes":0}`))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.BlobUsage(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SampledAt != nil {
		t.Errorf("SampledAt: got %v, want nil before first sample", result.SampledAt)
	}
}

func TestBlobUsageDisabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":{"code":"XOLU-BLOB-DISABLED","message":"Blob store is not enabled","status":503}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.BlobUsage(context.Background())
	xoluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("HTTPStatus: got %d", xoluErr.HTTPStatus)
	}
}
