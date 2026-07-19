// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Tests for S3 protocol-compatibility fixes surfaced by real-client interop
// (tests/interop): the PUT ETag must be the content MD5 (not SHA-256), bucket
// routes must accept a trailing slash, and unmatched routes must return
// S3-formatted XML errors rather than plain text.

package server_test

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
)

func s3CompatServer(t *testing.T) *blobTestServer {
	return setupBlobTestServer(t, func(c *config.Config) {
		c.BlobEnabled = true
		// open mode: no signature required, so these tests exercise the
		// response/routing behaviour directly.
		c.TenantMode = "path"
		c.TenantAuthMode = "open"
		c.TenantAutoRegister = true
	})
}

// PutObject must return an ETag equal to the hex MD5 of the body, quoted.
func TestS3Compat_PutETagIsMD5(t *testing.T) {
	b := s3CompatServer(t)
	defer b.cleanup()

	content := []byte("the quick brown fox")
	sum := md5.Sum(content)
	wantETag := `"` + hex.EncodeToString(sum[:]) + `"`

	resp := b.s3Do("PUT", "/acme/fox.txt", content, nil)
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("PUT status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("ETag"); got != wantETag {
		t.Errorf("ETag = %s, want %s (MD5)", got, wantETag)
	}
}

// When the client opts in (x-amz-sdk-checksum-algorithm: SHA256), PutObject
// returns x-amz-checksum-sha256 as base64 of the raw SHA-256 digest.
func TestS3Compat_ChecksumSHA256OptIn(t *testing.T) {
	b := s3CompatServer(t)
	defer b.cleanup()

	content := []byte("the quick brown fox")
	wantB64 := "nss2VhNB0Y62VIToM+/qYe3HS4TPXmrhuBxjUz4l/I8="

	// Opt in via the SDK checksum-algorithm header.
	resp := b.s3Do("PUT", "/acme/cks.txt", content,
		map[string]string{"x-amz-sdk-checksum-algorithm": "SHA256"})
	defer resp.Body.Close()
	if got := resp.Header.Get("x-amz-checksum-sha256"); got != wantB64 {
		t.Errorf("x-amz-checksum-sha256 = %q, want %q", got, wantB64)
	}
}

// Without opt-in, no checksum header is returned (it stays dormant).
func TestS3Compat_ChecksumSHA256NotReturnedByDefault(t *testing.T) {
	b := s3CompatServer(t)
	defer b.cleanup()

	resp := b.s3Do("PUT", "/acme/plain.txt", []byte("data"), nil)
	defer resp.Body.Close()
	if got := resp.Header.Get("x-amz-checksum-sha256"); got != "" {
		t.Errorf("unexpected checksum header without opt-in: %q", got)
	}
}

// A client-supplied checksum that does not match the content is rejected.
func TestS3Compat_ChecksumSHA256Mismatch(t *testing.T) {
	b := s3CompatServer(t)
	defer b.cleanup()

	resp := b.s3Do("PUT", "/acme/bad.txt", []byte("the quick brown fox"),
		map[string]string{"x-amz-checksum-sha256": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Errorf("mismatched checksum: status = %d, want 400 (BadDigest)", resp.StatusCode)
	}
	body := string(readBody(t, resp))
	if !strings.Contains(body, "BadDigest") {
		t.Errorf("expected BadDigest error, got: %q", body)
	}
}

// A client-supplied checksum that matches the content is accepted and echoed.
func TestS3Compat_ChecksumSHA256Match(t *testing.T) {
	b := s3CompatServer(t)
	defer b.cleanup()

	content := []byte("the quick brown fox")
	correct := "nss2VhNB0Y62VIToM+/qYe3HS4TPXmrhuBxjUz4l/I8="
	resp := b.s3Do("PUT", "/acme/good.txt", content,
		map[string]string{"x-amz-checksum-sha256": correct})
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("matching checksum: status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("x-amz-checksum-sha256"); got != correct {
		t.Errorf("echoed checksum = %q, want %q", got, correct)
	}
}

// The native (non-S3) blob API exposes both digests as explicit headers:
// X-Blob-SHA256 and X-Blob-MD5 on GET and HEAD.
func TestBlobNative_MD5Header(t *testing.T) {
	b := s3CompatServer(t)
	defer b.cleanup()

	content := []byte("native md5 header check")
	wantMD5 := hex.EncodeToString(func() []byte { s := md5.Sum(content); return s[:] }())

	post := b.blobDo("POST", "/api/v1/blob", content, map[string]string{"X-Blob-Key": "nm.txt"})
	post.Body.Close()

	get := b.blobDo("GET", "/api/v1/blob/nm.txt", nil, nil)
	defer get.Body.Close()
	if got := get.Header.Get("X-Blob-MD5"); got != wantMD5 {
		t.Errorf("GET X-Blob-MD5 = %q, want %q", got, wantMD5)
	}
	if get.Header.Get("X-Blob-SHA256") == "" {
		t.Error("GET X-Blob-SHA256 missing")
	}

	head := b.blobDo("HEAD", "/api/v1/blob/nm.txt", nil, nil)
	defer head.Body.Close()
	if got := head.Header.Get("X-Blob-MD5"); got != wantMD5 {
		t.Errorf("HEAD X-Blob-MD5 = %q, want %q", got, wantMD5)
	}
}

// GET and HEAD now return the ETag as the content MD5 (matching PUT), not the
// SHA-256, closing the previous read-side ETag gap.
func TestS3Compat_GetHeadETagIsMD5(t *testing.T) {
	b := s3CompatServer(t)
	defer b.cleanup()

	content := []byte("the quick brown fox")
	sum := md5.Sum(content)
	wantETag := `"` + hex.EncodeToString(sum[:]) + `"`

	put := b.s3Do("PUT", "/acme/etag.txt", content, nil)
	put.Body.Close()

	get := b.s3Do("GET", "/acme/etag.txt", nil, nil)
	defer get.Body.Close()
	if got := get.Header.Get("ETag"); got != wantETag {
		t.Errorf("GET ETag = %s, want %s (MD5)", got, wantETag)
	}

	head := b.s3Do("HEAD", "/acme/etag.txt", nil, nil)
	defer head.Body.Close()
	if got := head.Header.Get("ETag"); got != wantETag {
		t.Errorf("HEAD ETag = %s, want %s (MD5)", got, wantETag)
	}
}

// GET with x-amz-checksum-mode: ENABLED returns the stored SHA-256 checksum.
func TestS3Compat_GetChecksumMode(t *testing.T) {
	b := s3CompatServer(t)
	defer b.cleanup()

	content := []byte("the quick brown fox")
	wantB64 := "nss2VhNB0Y62VIToM+/qYe3HS4TPXmrhuBxjUz4l/I8="
	put := b.s3Do("PUT", "/acme/g.txt", content, nil)
	put.Body.Close()

	// With mode enabled -> checksum present.
	resp := b.s3Do("GET", "/acme/g.txt", nil,
		map[string]string{"x-amz-checksum-mode": "ENABLED"})
	defer resp.Body.Close()
	if got := resp.Header.Get("x-amz-checksum-sha256"); got != wantB64 {
		t.Errorf("GET checksum = %q, want %q", got, wantB64)
	}

	// Without mode -> no checksum header.
	resp2 := b.s3Do("GET", "/acme/g.txt", nil, nil)
	defer resp2.Body.Close()
	if got := resp2.Header.Get("x-amz-checksum-sha256"); got != "" {
		t.Errorf("GET without mode returned checksum: %q", got)
	}
}

// HEAD with x-amz-checksum-mode: ENABLED returns the stored SHA-256 checksum.
func TestS3Compat_HeadChecksumMode(t *testing.T) {
	b := s3CompatServer(t)
	defer b.cleanup()

	content := []byte("the quick brown fox")
	wantB64 := "nss2VhNB0Y62VIToM+/qYe3HS4TPXmrhuBxjUz4l/I8="
	put := b.s3Do("PUT", "/acme/h.txt", content, nil)
	put.Body.Close()

	resp := b.s3Do("HEAD", "/acme/h.txt", nil,
		map[string]string{"x-amz-checksum-mode": "ENABLED"})
	defer resp.Body.Close()
	if got := resp.Header.Get("x-amz-checksum-sha256"); got != wantB64 {
		t.Errorf("HEAD checksum = %q, want %q", got, wantB64)
	}

	resp2 := b.s3Do("HEAD", "/acme/h.txt", nil, nil)
	defer resp2.Body.Close()
	if got := resp2.Header.Get("x-amz-checksum-sha256"); got != "" {
		t.Errorf("HEAD without mode returned checksum: %q", got)
	}
}

// Bucket routes must accept a trailing slash (S3 clients probe with
// "GET /{bucket}/?location=").
func TestS3Compat_TrailingSlashBucket(t *testing.T) {
	b := s3CompatServer(t)
	defer b.cleanup()

	// Seed a tenant by writing an object first (auto-register).
	put := b.s3Do("PUT", "/acme/seed.txt", []byte("x"), nil)
	put.Body.Close()

	for _, path := range []string{"/acme/", "/acme/?location="} {
		resp := b.s3Do("GET", path, nil, nil)
		resp.Body.Close()
		if resp.StatusCode == 404 {
			t.Errorf("GET %q returned 404 (trailing-slash bucket route missing)", path)
		}
	}
}

// Unmatched routes must return S3-formatted XML, not chi's plain-text 404.
func TestS3Compat_XMLErrorOnNotFound(t *testing.T) {
	b := s3CompatServer(t)
	defer b.cleanup()

	resp := b.s3Do("GET", "/acme/does-not-exist-key", nil, nil)
	defer resp.Body.Close()
	body := string(readBody(t, resp))

	// Whether 404 or 200-empty, the body (if any) must be XML, never the chi
	// plain-text default "404 page not found".
	if strings.Contains(body, "404 page not found") {
		t.Errorf("got chi plain-text 404 body, want S3 XML; body=%q", body)
	}
	if resp.StatusCode == 404 && !strings.Contains(body, "<Error>") {
		t.Errorf("404 body is not S3 XML: %q", body)
	}
}

// A completely unmatched path returns S3 XML via the NotFound handler.
func TestS3Compat_UnmatchedPathXML(t *testing.T) {
	b := s3CompatServer(t)
	defer b.cleanup()

	resp := b.s3Do("GET", "/", nil, nil)
	defer resp.Body.Close()
	body := string(readBody(t, resp))
	if strings.Contains(body, "404 page not found") {
		t.Errorf("root path returned plain-text 404, want S3 XML; body=%q", body)
	}
}
