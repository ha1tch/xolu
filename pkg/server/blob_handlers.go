// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/ha1tch/xolu/pkg/blob"
	"github.com/ha1tch/xolu/pkg/dynconfig"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// ---------------------------------------------------------------------------
// Shared response types
// ---------------------------------------------------------------------------

type blobPutResponse struct {
	Key     string `json:"key"`
	SHA256  string `json:"sha256"`
	MD5     string `json:"md5,omitempty"`
	Size    int64  `json:"size"`
	Created bool   `json:"created"`
}

type blobMetaResponse struct {
	Key         string    `json:"key"`
	SHA256      string    `json:"sha256"`
	MD5         string    `json:"md5,omitempty"`
	Size        int64     `json:"size"`
	ContentType string    `json:"content_type,omitempty"`
	StoredAt    time.Time `json:"stored_at"`
}

type blobDeleteResponse struct {
	Key     string `json:"key"`
	Deleted bool   `json:"deleted"`
}

type blobListResponse struct {
	Tenant string             `json:"tenant"`
	Prefix string             `json:"prefix,omitempty"`
	Count  int                `json:"count"`
	Blobs  []blobMetaResponse `json:"blobs"`
}

type blobUsageResponse struct {
	Tenant    string     `json:"tenant"`
	BlobCount int64      `json:"blob_count"`
	KeyCount  int64      `json:"key_count"`
	Bytes     int64      `json:"bytes"`
	SampledAt *time.Time `json:"sampled_at,omitempty"`
}

// ---------------------------------------------------------------------------
// Guard helper
// ---------------------------------------------------------------------------

// blobStoreFor resolves the per-tenant blob store for a request: it checks that
// blob support is enabled, resolves and authorises the route tenant (enforcing
// the scoped-grant invariant, identical to the rest of the v1 surface), maps the
// tenant name to its ID, and returns that tenant's single-tenant blob.Store.
//
// On any failure it writes the appropriate error response and returns ok=false,
// so callers return immediately. The returned tenant name is provided for
// logging/usage lookups.
func (s *Server) blobStoreFor(w http.ResponseWriter, r *http.Request) (*blob.Store, string, tenant.TenantID, bool) {
	if s.blobMgr == nil {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrBlobDisabled,
			"Blob storage is not enabled on this server (XOLU_BLOB_ENABLED=true required)")
		return nil, "", 0, false
	}
	tenantName, ok := s.tenantForBlobChecked(w, r)
	if !ok {
		return nil, "", 0, false
	}
	tid, ok := s.blobTenantID(r.Context(), tenantName)
	if !ok {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrEntityNotFound,
			"Unknown tenant: "+tenantName)
		return nil, "", 0, false
	}
	store, err := s.blobMgr.StoreFor(tid)
	if err != nil {
		s.logger.Error().Err(err).Uint16("tenant", uint16(tid)).Msg("blob: open tenant store")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"Failed to open blob store for tenant")
		return nil, "", 0, false
	}
	return store, tenantName, tid, true
}

// blobTenantID maps a blob route tenant name to its tenant ID. The unscoped
// (default) route maps to tenant 0; named tenants are resolved via the
// registry. When TenantAutoRegister is enabled an unknown tenant is registered
// on demand, mirroring the behaviour of the main tenant middleware, so the
// blob plane does not impose a stricter registration requirement than the rest
// of the v1 surface.
func (s *Server) blobTenantID(ctx context.Context, tenantName string) (tenant.TenantID, bool) {
	if tenantName == "" || tenantName == "default" {
		return 0, true
	}
	if tid, ok := s.tenantRegistry.Lookup(tenantName); ok {
		return tid, true
	}
	if s.config.TenantAutoRegister {
		tid, err := s.tenantRegistry.GetOrRegister(ctx, tenantName)
		if err != nil {
			return 0, false
		}
		return tid, true
	}
	return 0, false
}

// tenantForBlob returns the tenant name to use for a blob operation.
// For tenant-scoped routes the name comes from context; for the default route
// it uses "default".
func (s *Server) tenantForBlob(r *http.Request) string {
	if v := r.Context().Value(tenantContextKey); v != nil {
		if name, ok := v.(string); ok && name != "" {
			return name
		}
	}
	return "default"
}

// tenantForBlobChecked resolves the blob tenant and enforces the scoped
// authorisation invariant: under TenantEnforceGrant the caller's identity must
// authorise the resolved tenant, exactly as the normal v1 routes require (see
// the grant check in storeForTenant). This brings the native blob plane to
// parity with the S3 plane and the rest of the v1 surface. The authentication
// itself (producing the grant) is done upstream by AuthMiddleware, which
// deposits the grant in the request context; this function performs only the
// authorisation decision. On refusal it writes a 403 and returns ok=false, so
// callers return immediately.
//
// Fail closed: under TenantEnforceGrant a missing or empty grant authorises
// nothing. In open mode (no TenantEnforceGrant) no grant check is applied.
func (s *Server) tenantForBlobChecked(w http.ResponseWriter, r *http.Request) (string, bool) {
	tenant := s.tenantForBlob(r)
	if !s.authoriseTenantGrant(w, r, tenant) {
		return "", false
	}
	return tenant, true
}

// ---------------------------------------------------------------------------
// POST /api/v1/blob
// POST /api/v1/tenant/{tenant_id}/blob
//
// Stores a blob. The request body is the raw content. The key is taken from
// the X-Blob-Key header or the ?key= query parameter. If neither is supplied
// the SHA-256 of the content is used as the key (purely content-addressed).
// Content-Type is preserved from the request header.
// ---------------------------------------------------------------------------

func (s *Server) handleBlobPut(w http.ResponseWriter, r *http.Request) {
	bs, tenant, tid, ok := s.blobStoreFor(w, r)
	if !ok {
		return
	}

	// Key resolution: header > query param > sha (post-hoc).
	key := r.Header.Get("X-Blob-Key")
	if key == "" {
		key = r.URL.Query().Get("key")
	}
	useSHAAsKey := key == ""

	ct := r.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}

	body := r.Body
	if s.config.BlobMaxSize > 0 {
		body = http.MaxBytesReader(w, r.Body, int64(s.config.BlobMaxSize))
	}

	// Per-tenant quota check. Precedence (highest first):
	//   1. Per-tenant dynconfig override ("tenant.{name}" / "blob.max_bytes")
	//   2. Global dynconfig override     ("global"         / "blob.max_bytes")
	//   3. Static env default            (BlobMaxTotalBytes)
	//
	// Checked against the sampler cache — a soft cap, never stalls a request.
	limit := s.config.BlobMaxTotalBytes
	if s.dynConfig != nil {
		if v, ok := s.dynConfig.GetInt64(dynconfig.TenantNamespace(tenant), "blob.max_bytes"); ok {
			limit = v
		} else if v, ok := s.dynConfig.GetInt64("global", "blob.max_bytes"); ok {
			limit = v
		}
	}
	if limit > 0 {
		if smp := s.blobMgr.SamplerFor(tid); smp != nil {
			u := smp.Current()
			if u.Bytes >= limit {
				s.writeError(w, http.StatusRequestEntityTooLarge, xoluerr.ErrBlobQuotaExceeded,
					"Tenant blob storage quota exceeded")
				return
			}
		}
	}

	// If no key supplied yet, we need to store first then use the SHA as key.
	if !useSHAAsKey {
		if err := blob.ValidateKey(key); err != nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrBlobInvalidKey, err.Error())
			return
		}
	}

	var sha string
	var created bool
	var err error

	var md5hex string
	if useSHAAsKey {
		// No key supplied: store by content address only. The SHA is both the
		// blob identity and the response key; no alias is written.
		sha, md5hex, created, err = bs.PutRaw(body, ct)
	} else {
		sha, md5hex, created, err = bs.Put(key, body, ct)
	}

	if err != nil {
		switch {
		case errors.Is(err, blob.ErrTooLarge):
			s.writeError(w, http.StatusRequestEntityTooLarge, xoluerr.ErrBlobTooLarge,
				"Blob exceeds maximum allowed size")
		case errors.As(err, new(*http.MaxBytesError)):
			// http.MaxBytesReader fires when the request body exceeds
			// BlobMaxSize before the store's own counter can catch it.
			s.writeError(w, http.StatusRequestEntityTooLarge, xoluerr.ErrBlobTooLarge,
				"Blob exceeds maximum allowed size")
		case errors.Is(err, blob.ErrKeyInvalid):
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrBlobInvalidKey, err.Error())
		default:
			s.logger.Error().Err(err).Str("tenant", tenant).Str("key", key).Msg("blob put failed")
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrBlobStoreFailed,
				"Failed to store blob")
		}
		return
	}

	finalKey := key
	if useSHAAsKey {
		finalKey = sha
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	s.writeJSON(w, status, blobPutResponse{
		Key:     finalKey,
		SHA256:  sha,
		MD5:     md5hex,
		Created: created,
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/blob/{key}
// GET /api/v1/tenant/{tenant_id}/blob/{key}
//
// Retrieves blob content. Streams the raw bytes with the original Content-Type.
// ---------------------------------------------------------------------------

func (s *Server) handleBlobGet(w http.ResponseWriter, r *http.Request) {
	bs, tenant, _, ok := s.blobStoreFor(w, r)
	if !ok {
		return
	}
	key := chi.URLParam(r, "key")

	rc, meta, err := bs.Get(key)
	if err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, xoluerr.ErrBlobNotFound,
				"Blob not found")
			return
		}
		if errors.Is(err, blob.ErrKeyInvalid) {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrBlobInvalidKey, err.Error())
			return
		}
		s.logger.Error().Err(err).Str("tenant", tenant).Str("key", key).Msg("blob get failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrBlobStoreFailed,
			"Failed to retrieve blob")
		return
	}
	defer func() { _ = rc.Close() }()

	ct := meta.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}

	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Blob-SHA256", meta.SHA256)
	if meta.MD5 != "" {
		w.Header().Set("X-Blob-MD5", meta.MD5)
	}
	w.Header().Set("X-Blob-Size", itoa(meta.Size))
	w.Header().Set("ETag", `"`+meta.SHA256+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, rc)
}

// ---------------------------------------------------------------------------
// HEAD /api/v1/blob/{key}
// HEAD /api/v1/tenant/{tenant_id}/blob/{key}
//
// Returns metadata headers without the body.
// ---------------------------------------------------------------------------

func (s *Server) handleBlobHead(w http.ResponseWriter, r *http.Request) {
	bs, _, _, ok := s.blobStoreFor(w, r)
	if !ok {
		return
	}
	key := chi.URLParam(r, "key")

	meta, err := bs.Head(key)
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
	w.Header().Set("X-Blob-SHA256", meta.SHA256)
	if meta.MD5 != "" {
		w.Header().Set("X-Blob-MD5", meta.MD5)
	}
	w.Header().Set("ETag", `"`+meta.SHA256+`"`)
	w.Header().Set("X-Blob-Stored-At", meta.StoredAt.Format(time.RFC3339))
	w.WriteHeader(http.StatusOK)
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/blob/{key}
// DELETE /api/v1/tenant/{tenant_id}/blob/{key}
//
// Removes the key alias. The blob file is not immediately removed;
// GC handles unreferenced blobs separately.
// ---------------------------------------------------------------------------

func (s *Server) handleBlobDelete(w http.ResponseWriter, r *http.Request) {
	bs, tenant, _, ok := s.blobStoreFor(w, r)
	if !ok {
		return
	}
	key := chi.URLParam(r, "key")

	if err := bs.Delete(key); err != nil {
		if errors.Is(err, blob.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, xoluerr.ErrBlobNotFound,
				"Blob not found")
			return
		}
		s.logger.Error().Err(err).Str("tenant", tenant).Str("key", key).Msg("blob delete failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrBlobStoreFailed,
			"Failed to delete blob")
		return
	}

	s.writeJSON(w, http.StatusOK, blobDeleteResponse{
		Key:     key,
		Deleted: true,
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/blob
// GET /api/v1/tenant/{tenant_id}/blob
//
// Lists blobs. Optional ?prefix= filter. Results sorted by key ascending.
// ---------------------------------------------------------------------------

func (s *Server) handleBlobList(w http.ResponseWriter, r *http.Request) {
	bs, tenant, _, ok := s.blobStoreFor(w, r)
	if !ok {
		return
	}
	prefix := r.URL.Query().Get("prefix")

	metas, err := bs.List(prefix)
	if err != nil {
		s.logger.Error().Err(err).Str("tenant", tenant).Msg("blob list failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrBlobStoreFailed,
			"Failed to list blobs")
		return
	}

	sort.Slice(metas, func(i, j int) bool {
		return metas[i].Key < metas[j].Key
	})

	items := make([]blobMetaResponse, len(metas))
	for i, m := range metas {
		items[i] = blobMetaResponse{
			Key:         m.Key,
			SHA256:      m.SHA256,
			MD5:         m.MD5,
			Size:        m.Size,
			ContentType: m.ContentType,
			StoredAt:    m.StoredAt,
		}
	}

	s.writeJSON(w, http.StatusOK, blobListResponse{
		Tenant: tenant,
		Prefix: prefix,
		Count:  len(items),
		Blobs:  items,
	})
}

// ---------------------------------------------------------------------------
// GET /api/v1/blob/usage
// GET /api/v1/tenant/{tenant_id}/blob/usage
//
// Returns the most recently sampled disk usage for the tenant's blob
// namespace. Data is served from an in-memory cache maintained by the
// background UsageSampler — the filesystem is never walked at request time.
//
// SampledAt is included in the response so callers can judge data freshness.
// If the sampler has not yet completed its first walk, all counts are zero
// and sampled_at is omitted. No S3-equivalent endpoint exists.
// ---------------------------------------------------------------------------

func (s *Server) handleBlobUsage(w http.ResponseWriter, r *http.Request) {
	if s.blobMgr == nil {
		s.writeError(w, http.StatusServiceUnavailable, xoluerr.ErrBlobDisabled,
			"Blob store is not enabled")
		return
	}
	bs, _, tid, ok := s.blobStoreFor(w, r)
	if !ok {
		return
	}
	_ = bs // store opened (ensures the tenant's sampler exists); usage read below

	var resp blobUsageResponse
	resp.Tenant = s.tenantForBlob(r)

	if smp := s.blobMgr.SamplerFor(tid); smp != nil {
		u := smp.Current()
		resp.BlobCount = u.BlobCount
		resp.KeyCount = u.KeyCount
		resp.Bytes = u.Bytes
		if !u.SampledAt.IsZero() {
			resp.SampledAt = &u.SampledAt
		}
	}
	// If sampling is disabled (interval=0) all counts remain zero. The response
	// is still valid; operators can check sampled_at==null.

	s.writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Small helper — avoids importing strconv just for int64 formatting
// ---------------------------------------------------------------------------

func itoa(n int64) string {
	return fmt.Sprintf("%d", n)
}
