// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// S3-compatible blob API.
//
// Runs on a separate listener (XOLU_S3_PORT, default 9091).
// Implements the minimal S3 surface needed for existing S3 client libraries
// to work without modification:
//
//   PUT  /{bucket}/{key}          — store a blob (PutObject)
//   GET  /{bucket}/{key}          — retrieve a blob (GetObject)
//   HEAD /{bucket}/{key}          — metadata only (HeadObject)
//   DELETE /{bucket}/{key}        — remove key alias (DeleteObject)
//   GET  /{bucket}?list-type=2    — list objects (ListObjectsV2)
//   HEAD /{bucket}                — check bucket exists (HeadBucket)
//
// Authentication: the Authorization header is parsed for AWS Signature V4
// access key ID, which is treated as the tenant name. The signature itself is
// not verified. Configure S3 clients with any non-empty secret key.
//
// Bucket semantics: bucket name = tenant name. Auto-registration is not
// performed; the tenant must already exist.
//
// Response format: XML where S3 clients require it (ListObjectsV2, errors).
// Raw bytes for GetObject. No body for PutObject success (ETag header only).

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ha1tch/xolu/pkg/blob"
	"github.com/ha1tch/xolu/pkg/config"
	xoluMiddleware "github.com/ha1tch/xolu/pkg/middleware"
	"github.com/ha1tch/xolu/pkg/s3sig"
)

// ---------------------------------------------------------------------------
// XML response types (S3 wire format)
// ---------------------------------------------------------------------------

type s3Error struct {
	XMLName   xml.Name `xml:"Error"`
	Code      string   `xml:"Code"`
	Message   string   `xml:"Message"`
	Resource  string   `xml:"Resource"`
	RequestID string   `xml:"RequestId"`
}

type s3ListObjectsV2Response struct {
	XMLName     xml.Name   `xml:"ListBucketResult"`
	Xmlns       string     `xml:"xmlns,attr"`
	Name        string     `xml:"Name"`
	Prefix      string     `xml:"Prefix"`
	KeyCount    int        `xml:"KeyCount"`
	MaxKeys     int        `xml:"MaxKeys"`
	IsTruncated bool       `xml:"IsTruncated"`
	Contents    []s3Object `xml:"Contents"`
}

type s3Object struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"` // RFC3339
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
}

// ---------------------------------------------------------------------------
// S3 router — called from setupS3Routes
// ---------------------------------------------------------------------------

func (s *Server) setupS3Routes() *chi.Mux {
	r := chi.NewRouter()

	// S3-formatted error responses for unmatched routes/methods, so clients
	// always receive parseable XML rather than chi's plain-text default.
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		s3WriteError(w, http.StatusNotFound, "NoSuchKey",
			"The specified key does not exist.", req.URL.Path)
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		s3WriteError(w, http.StatusMethodNotAllowed, "MethodNotAllowed",
			"The specified method is not allowed against this resource.", req.URL.Path)
	})

	// Bucket-level operations — registered before /{bucket}/{key} to ensure
	// HeadBucket and ListObjectsV2 are not swallowed by the object routes.
	// Both slash forms are registered: S3 clients commonly probe with a trailing
	// slash (e.g. "GET /{bucket}/?location=").
	r.Head("/{bucket}", s.handleS3HeadBucket)
	r.Get("/{bucket}", s.handleS3ListObjects)
	r.Head("/{bucket}/", s.handleS3HeadBucket)
	r.Get("/{bucket}/", s.handleS3ListObjects)

	// Object operations.
	r.Put("/{bucket}/{key}", s.handleS3PutObject)
	r.Get("/{bucket}/{key}", s.handleS3GetObject)
	r.Head("/{bucket}/{key}", s.handleS3HeadObject)
	r.Delete("/{bucket}/{key}", s.handleS3DeleteObject)

	return r
}

// ---------------------------------------------------------------------------
// Auth helper — extracts tenant name from Sig V4 Authorization header.
//
// AWS Authorization header format:
//   AWS4-HMAC-SHA256 Credential=<access_key>/<date>/<region>/<service>/aws4_request, ...
//
// We extract only the access_key and treat it as the tenant name.
// Falls back to the bucket name when Authorization is absent or unparseable.
// ---------------------------------------------------------------------------

// s3StoreFor maps an already-resolved S3 tenant name to its single-tenant
// blob.Store via the per-tenant manager. The authorisation decision (the grant
// check) has already been made by s3TenantFromRequest against the SigV4
// signature; this only resolves the name to a store. On failure it writes an
// S3-style error and returns ok=false.
func (s *Server) s3StoreFor(ctx context.Context, w http.ResponseWriter, tenant, path string) (*blob.Store, bool) {
	tid, ok := s.blobTenantID(ctx, tenant)
	if !ok {
		s3WriteError(w, http.StatusNotFound, "NoSuchBucket",
			fmt.Sprintf("The specified bucket does not exist: %s", tenant), path)
		return nil, false
	}
	store, err := s.blobMgr.StoreFor(tid)
	if err != nil {
		s3WriteError(w, http.StatusInternalServerError, "InternalError",
			"Failed to open blob store for tenant", path)
		return nil, false
	}
	return store, true
}

// s3TenantFromRequest extracts the tenant name from the request.
//
// When Authorization is present the access key ID from the AWS Sig V4
// Credential field is used as the tenant name, which lets each S3 client
// identity operate against its own tenant namespace.
//
// When Authorization is absent the bucket name is used as a fallback, but a
// structured warning is logged so operators can detect misconfigured clients.
// If config.S3RequireAuth is true, requests without Authorization are rejected
// with 403 AccessDenied and the method returns "". Callers must treat an empty
// return as a signal that the response has already been written and bail out.
func (s *Server) s3TenantFromRequest(w http.ResponseWriter, r *http.Request, bucket string) string {
	auth := r.Header.Get("Authorization")

	// Scoped authorisation: the S3 caller's access key must have a configured
	// grant that authorises the requested tenant (the bucket), and the request's
	// SigV4 signature must verify against the grant's secret. The access-key
	// string is no longer trusted as the tenant name.
	if s.config.Tenancy().Has(config.TenantEnforceGrant) {
		ak := s3AccessKeyFromAuth(auth)
		if ak == "" {
			s3WriteError(w, http.StatusForbidden, "AccessDenied",
				"Authorization with access key required", r.URL.Path)
			return ""
		}
		keyGrant, ok := s.s3KeyGrantForAccessKey(ak)
		if !ok {
			s3WriteError(w, http.StatusForbidden, "AccessDenied",
				"unknown access key", r.URL.Path)
			return ""
		}
		grant := xoluMiddleware.TenantGrant{Admin: keyGrant.Admin, Tenants: keyGrant.Tenants}
		if !grant.Allows(bucket) {
			s3WriteError(w, http.StatusForbidden, "AccessDenied",
				"access key not authorised for bucket", r.URL.Path)
			return ""
		}
		// Verify the SigV4 signature against the grant's secret.
		if err := s.verifyS3Signature(r, auth, keyGrant.Secret); err != nil {
			s3WriteError(w, http.StatusForbidden, "SignatureDoesNotMatch",
				"request signature verification failed", r.URL.Path)
			return ""
		}
		return bucket
	}

	if auth == "" {
		// Under scoped authorisation the bucket-name fallback is an
		// unauthenticated tenant-selection path and must never be taken; scoped
		// therefore implies S3RequireAuth even if the operator did not set it
		// explicitly (mirrors scoped ⇒ TenantRequireRoute on the main server).
		if s.config.S3RequireAuth || s.config.Tenancy().Has(config.TenantEnforceGrant) {
			s3WriteError(w, http.StatusForbidden, "AccessDenied",
				"Authorization header required", r.URL.Path)
			return ""
		}
		s.logger.Warn().
			Str("bucket", bucket).
			Str("remote", r.RemoteAddr).
			Msg("S3 request missing Authorization header; falling back to bucket as tenant")
		return bucket
	}
	// Find "Credential=" field.
	const prefix = "Credential="
	idx := strings.Index(auth, prefix)
	if idx < 0 {
		return bucket
	}
	cred := auth[idx+len(prefix):]
	// Credential value ends at comma or end of string.
	if end := strings.IndexByte(cred, ','); end >= 0 {
		cred = cred[:end]
	}
	// Credential format: <access_key>/<date>/...
	if slash := strings.IndexByte(cred, '/'); slash >= 0 {
		ak := strings.TrimSpace(cred[:slash])
		if ak != "" {
			return ak
		}
	}
	return bucket
}

// s3AccessKeyFromAuth extracts the access key ID from a SigV4 Authorization
// header value ("...Credential=<access_key>/<date>/..."). Returns "" if absent.
func s3AccessKeyFromAuth(auth string) string {
	const prefix = "Credential="
	idx := strings.Index(auth, prefix)
	if idx < 0 {
		return ""
	}
	cred := auth[idx+len(prefix):]
	if end := strings.IndexByte(cred, ','); end >= 0 {
		cred = cred[:end]
	}
	if slash := strings.IndexByte(cred, '/'); slash >= 0 {
		return strings.TrimSpace(cred[:slash])
	}
	return strings.TrimSpace(cred)
}

// s3KeyGrantForAccessKey looks up the full S3KeyGrant (including secret) for an
// access key. The access-key comparison is constant-time.
func (s *Server) s3KeyGrantForAccessKey(accessKey string) (config.S3KeyGrant, bool) {
	for _, g := range s.config.S3KeyGrants {
		if g.AccessKey != "" && subtle.ConstantTimeCompare([]byte(accessKey), []byte(g.AccessKey)) == 1 {
			return g, true
		}
	}
	return config.S3KeyGrant{}, false
}

// verifyS3Signature verifies the request's SigV4 signature against the given
// secret. It reconstructs the signing inputs from the request and delegates to
// pkg/s3sig (the same derivation used to sign, validated against AWS's
// known-answer vector).
func (s *Server) verifyS3Signature(r *http.Request, auth, secret string) error {
	comp, err := s3sig.ParseAuthorization(auth)
	if err != nil {
		return err
	}
	comp.Method = r.Method
	comp.CanonURI = r.URL.EscapedPath()
	comp.CanonQuery = s3CanonicalQuery(r)
	comp.PayloadHash = r.Header.Get("X-Amz-Content-Sha256")
	comp.AmzDate = r.Header.Get("X-Amz-Date")
	comp.Headers = make(map[string]string, len(comp.SignedHeaders))
	for _, h := range comp.SignedHeaders {
		if strings.EqualFold(h, "host") {
			comp.Headers["host"] = r.Host
			continue
		}
		comp.Headers[h] = r.Header.Get(h)
	}
	return comp.Verify(secret)
}

// sha256HexToBase64 converts a hex-encoded SHA-256 digest to the base64 form
// used by the x-amz-checksum-sha256 header. Returns "" if the input is not a
// valid SHA-256 hex digest.
func sha256HexToBase64(hexSHA string) string {
	raw, err := hex.DecodeString(hexSHA)
	if err != nil || len(raw) != sha256.Size {
		return ""
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// s3WantsSHA256Checksum reports whether the client opted in to receiving an
// x-amz-checksum-sha256 response header, either by sending the checksum itself
// or by requesting SHA256 via the SDK checksum-algorithm header.
func s3WantsSHA256Checksum(r *http.Request) bool {
	if r.Header.Get("X-Amz-Checksum-Sha256") != "" {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Amz-Sdk-Checksum-Algorithm"), "SHA256")
}

// s3ChecksumModeEnabled reports whether the client requested checksum retrieval
// on GET/HEAD via "x-amz-checksum-mode: ENABLED" (the AWS opt-in for returning
// stored object checksums on read).
func s3ChecksumModeEnabled(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("X-Amz-Checksum-Mode"), "ENABLED")
}

// s3ObjectETag returns the quoted ETag for an object. S3 defines the ETag of a
// simple (non-multipart) object as the hex MD5 of its content; we return that
// when available, falling back to the SHA-256 for blobs written before MD5
// sidecars existed.
func s3ObjectETag(meta blob.Meta) string {
	if meta.MD5 != "" {
		return `"` + meta.MD5 + `"`
	}
	return `"` + meta.SHA256 + `"`
}

// s3CanonicalQuery builds the SigV4 canonical query string: parameters sorted by
// name, URL-encoded, joined by '&'. A parameter present with an empty value
// (e.g. "?location=") must still appear as "key=" — Go's url.Values collapses
// that to an empty list, so we parse RawQuery directly to preserve it.
func s3CanonicalQuery(r *http.Request) string {
	raw := r.URL.RawQuery
	if raw == "" {
		return ""
	}
	type kv struct{ k, v string }
	var pairs []kv
	for _, part := range strings.Split(raw, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		dk, err := url.QueryUnescape(k)
		if err != nil {
			dk = k
		}
		dv, err := url.QueryUnescape(v)
		if err != nil {
			dv = v
		}
		pairs = append(pairs, kv{dk, dv})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = url.QueryEscape(p.k) + "=" + url.QueryEscape(p.v)
	}
	return strings.Join(parts, "&")
}

// ---------------------------------------------------------------------------
// s3WriteError writes an S3-style XML error response.
// ---------------------------------------------------------------------------

func s3WriteError(w http.ResponseWriter, status int, code, message, resource string) {
	body, _ := xml.Marshal(s3Error{
		Code:      code,
		Message:   message,
		Resource:  resource,
		RequestID: "xolu",
	})
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(body)
}

// ---------------------------------------------------------------------------
// HeadBucket — HEAD /{bucket}
// S3 clients use this to verify the bucket exists and they have access.
// ---------------------------------------------------------------------------

func (s *Server) handleS3HeadBucket(w http.ResponseWriter, r *http.Request) {
	if s.blobMgr == nil {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}
	bucket := chi.URLParam(r, "bucket")
	tenant := s.s3TenantFromRequest(w, r, bucket)
	if tenant == "" {
		return
	}
	store, ok := s.s3StoreFor(r.Context(), w, tenant, "/"+bucket)
	if !ok {
		return
	}

	// Check that the tenant directory exists by doing a list with limit 0.
	_, err := store.List("")
	if err != nil {
		s3WriteError(w, http.StatusNotFound, "NoSuchBucket",
			fmt.Sprintf("The specified bucket does not exist: %s", bucket),
			"/"+bucket)
		return
	}
	w.Header().Set("X-Blob-Tenant", tenant)
	w.WriteHeader(http.StatusOK)
}

// ---------------------------------------------------------------------------
// ListObjectsV2 — GET /{bucket}?list-type=2
// Falls back to ListObjects behaviour when list-type is absent.
// ---------------------------------------------------------------------------

func (s *Server) handleS3ListObjects(w http.ResponseWriter, r *http.Request) {
	if s.blobMgr == nil {
		s3WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"Blob storage is not enabled", "/")
		return
	}

	bucket := chi.URLParam(r, "bucket")
	tenant := s.s3TenantFromRequest(w, r, bucket)
	if tenant == "" {
		return
	}
	store, ok := s.s3StoreFor(r.Context(), w, tenant, "/"+bucket)
	if !ok {
		return
	}
	prefix := r.URL.Query().Get("prefix")

	metas, err := store.List(prefix)
	if err != nil {
		s3WriteError(w, http.StatusInternalServerError, "InternalError",
			"Failed to list objects", "/"+bucket)
		return
	}

	contents := make([]s3Object, len(metas))
	for i, m := range metas {
		contents[i] = s3Object{
			Key:          m.Key,
			LastModified: m.StoredAt.UTC().Format(time.RFC3339),
			ETag:         `"` + m.SHA256 + `"`,
			Size:         m.Size,
			StorageClass: "STANDARD",
		}
	}

	resp := s3ListObjectsV2Response{
		Xmlns:       "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:        bucket,
		Prefix:      prefix,
		KeyCount:    len(contents),
		MaxKeys:     1000,
		IsTruncated: false,
		Contents:    contents,
	}

	body, err := xml.Marshal(resp)
	if err != nil {
		s3WriteError(w, http.StatusInternalServerError, "InternalError",
			"Failed to encode response", "/"+bucket)
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(body)
}

// ---------------------------------------------------------------------------
// PutObject — PUT /{bucket}/{key}
// ---------------------------------------------------------------------------

func (s *Server) handleS3PutObject(w http.ResponseWriter, r *http.Request) {
	if s.blobMgr == nil {
		s3WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"Blob storage is not enabled", "/")
		return
	}

	bucket := chi.URLParam(r, "bucket")
	key := chi.URLParam(r, "key")
	tenant := s.s3TenantFromRequest(w, r, bucket)
	if tenant == "" {
		return
	}
	store, ok := s.s3StoreFor(r.Context(), w, tenant, "/"+bucket)
	if !ok {
		return
	}

	ct := r.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}

	body := r.Body
	if s.config.BlobMaxSize > 0 {
		body = http.MaxBytesReader(w, r.Body, int64(s.config.BlobMaxSize))
	}

	hexSHA, hexMD5, _, err := store.Put(key, body, ct)
	if err != nil {
		switch {
		case errors.Is(err, blob.ErrTooLarge):
			s3WriteError(w, http.StatusRequestEntityTooLarge, "EntityTooLarge",
				"Your proposed upload exceeds the maximum allowed size", "/"+bucket+"/"+key)
		case errors.Is(err, blob.ErrKeyInvalid):
			s3WriteError(w, http.StatusBadRequest, "InvalidObjectName",
				"The specified object name is not valid", "/"+bucket+"/"+key)
		default:
			s.logger.Error().Err(err).Str("tenant", tenant).Str("key", key).Msg("s3 put failed")
			s3WriteError(w, http.StatusInternalServerError, "InternalError",
				"We encountered an internal error. Please try again.", "/"+bucket+"/"+key)
		}
		return
	}

	// Modern S3 additional checksum: x-amz-checksum-sha256 is the base64 of the
	// raw SHA-256 digest. We compute SHA-256 anyway (content addressing), so we
	// surface it. If the client sent its own value, validate and reject on
	// mismatch (BadDigest); otherwise return it when the client opted in via
	// x-amz-checksum-sha256 or x-amz-sdk-checksum-algorithm: SHA256.
	b64SHA := sha256HexToBase64(hexSHA)
	if b64SHA != "" {
		if want := r.Header.Get("X-Amz-Checksum-Sha256"); want != "" && want != b64SHA {
			s3WriteError(w, http.StatusBadRequest, "BadDigest",
				"The SHA256 checksum sent did not match the calculated checksum.", "/"+bucket+"/"+key)
			return
		}
		if s3WantsSHA256Checksum(r) {
			w.Header().Set("x-amz-checksum-sha256", b64SHA)
		}
	}

	w.Header().Set("ETag", `"`+hexMD5+`"`)
	w.WriteHeader(http.StatusOK)
}

// ---------------------------------------------------------------------------
// GetObject — GET /{bucket}/{key}
// ---------------------------------------------------------------------------

func (s *Server) handleS3GetObject(w http.ResponseWriter, r *http.Request) {
	if s.blobMgr == nil {
		s3WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"Blob storage is not enabled", "/")
		return
	}

	bucket := chi.URLParam(r, "bucket")
	key := chi.URLParam(r, "key")
	tenant := s.s3TenantFromRequest(w, r, bucket)
	if tenant == "" {
		return
	}
	store, ok := s.s3StoreFor(r.Context(), w, tenant, "/"+bucket)
	if !ok {
		return
	}

	rc, meta, err := store.Get(key)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			s3WriteError(w, http.StatusNotFound, "NoSuchKey",
				"The specified key does not exist.", "/"+bucket+"/"+key)
			return
		}
		s3WriteError(w, http.StatusInternalServerError, "InternalError",
			"We encountered an internal error.", "/"+bucket+"/"+key)
		return
	}
	defer func() { _ = rc.Close() }()

	ct := meta.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", itoa(meta.Size))
	w.Header().Set("ETag", s3ObjectETag(meta))
	w.Header().Set("Last-Modified", meta.StoredAt.UTC().Format(http.TimeFormat))
	if s3ChecksumModeEnabled(r) {
		if b64 := sha256HexToBase64(meta.SHA256); b64 != "" {
			w.Header().Set("x-amz-checksum-sha256", b64)
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// ---------------------------------------------------------------------------
// HeadObject — HEAD /{bucket}/{key}
// ---------------------------------------------------------------------------

func (s *Server) handleS3HeadObject(w http.ResponseWriter, r *http.Request) {
	if s.blobMgr == nil {
		w.WriteHeader(http.StatusNotImplemented)
		return
	}

	bucket := chi.URLParam(r, "bucket")
	key := chi.URLParam(r, "key")
	tenant := s.s3TenantFromRequest(w, r, bucket)
	if tenant == "" {
		return
	}
	store, ok := s.s3StoreFor(r.Context(), w, tenant, "/"+bucket)
	if !ok {
		return
	}

	meta, err := store.Head(key)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	ct := meta.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", itoa(meta.Size))
	w.Header().Set("ETag", s3ObjectETag(meta))
	w.Header().Set("Last-Modified", meta.StoredAt.UTC().Format(http.TimeFormat))
	if s3ChecksumModeEnabled(r) {
		if b64 := sha256HexToBase64(meta.SHA256); b64 != "" {
			w.Header().Set("x-amz-checksum-sha256", b64)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// ---------------------------------------------------------------------------
// DeleteObject — DELETE /{bucket}/{key}
// ---------------------------------------------------------------------------

func (s *Server) handleS3DeleteObject(w http.ResponseWriter, r *http.Request) {
	if s.blobMgr == nil {
		s3WriteError(w, http.StatusNotImplemented, "NotImplemented",
			"Blob storage is not enabled", "/")
		return
	}

	bucket := chi.URLParam(r, "bucket")
	key := chi.URLParam(r, "key")
	tenant := s.s3TenantFromRequest(w, r, bucket)
	if tenant == "" {
		return
	}
	store, ok := s.s3StoreFor(r.Context(), w, tenant, "/"+bucket)
	if !ok {
		return
	}

	if err := store.Delete(key); err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			// S3 DeleteObject is idempotent — returns 204 even when key absent.
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.logger.Error().Err(err).Str("tenant", tenant).Str("key", key).Msg("s3 delete failed")
		s3WriteError(w, http.StatusInternalServerError, "InternalError",
			"We encountered an internal error.", "/"+bucket+"/"+key)
		return
	}

	// S3 DeleteObject returns 204 No Content on success.
	w.WriteHeader(http.StatusNoContent)
}
