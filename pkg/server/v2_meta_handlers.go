// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_meta_handlers.go
//
// S3: /api/v2/meta — per-entity key/value metadata sidecar with optional TTL.
//
// Endpoints:
//   GET    /api/v2/meta/{entity}/{id}          List all metadata for an entity
//   GET    /api/v2/meta/{entity}/{id}/{key}     Get a single value
//   PUT    /api/v2/meta/{entity}/{id}/{key}     Set a value (optional expires_at)
//   DELETE /api/v2/meta/{entity}/{id}/{key}     Delete a single key
//   DELETE /api/v2/meta/{entity}/{id}           Delete all metadata for an entity
//
// Storage: entity_meta table (global, tenant_id column).
// Cascade delete: deleteInner in sqlite.go deletes meta rows in the same tx.
// TTL sweep: MetaSweeper implements gc.Sweeper; registered in gcWorkers.

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	gcpkg "github.com/ha1tch/xolu/pkg/gc"
	"github.com/ha1tch/xolu/pkg/storage"
	"strings"
)

// metaKeyRe validates metadata keys: alphanumeric plus underscore, 1–64 chars.
var metaKeyRe = regexp.MustCompile(`^[a-zA-Z0-9_]{1,64}$`)

// metaDB returns the underlying *sql.DB from the store for tenant-aware
// entity_meta queries. entity_meta is a global table (not per-tenant-prefix)
// with tenant_id as a column, so we always use the writer DB directly.
func (s *Server) metaDB(r *http.Request) (*sql.DB, uint16) {
	store := s.getStore(r.Context())
	tenantID := getTenantIDNumeric(r.Context())
	if wdp, ok := store.(storage.WriterDBProvider); ok {
		return wdp.WriterDB(), tenantID
	}
	// Fallback: should not happen in production (SQLiteStore always implements this).
	return nil, tenantID
}

// ─── GET /meta/{entity}/{id} ─────────────────────────────────────────────────

// sweepMetaSubject deletes every annotation for one subject — the
// cascade hook primitive lifecycles call after a successful delete.
// Best-effort by design: the primitive's store and entity_meta live in
// different stores, so this cannot share the delete's transaction (the
// entity cascade in deleteInner can and does). A crash between delete
// and sweep leaves orphan annotations, which are harmless (meta is
// engine-inert) and reclaimable via TTL or an explicit meta DELETE.
func (s *Server) sweepMetaSubject(ctx context.Context, r *http.Request, kind, key string) {
	db, tenantID := s.metaDB(r)
	_, _ = db.ExecContext(ctx,
		`DELETE FROM entity_meta WHERE tenant_id=? AND subject_kind=? AND subject_key=?`,
		tenantID, kind, key)
}

// subjectFields renders a subject for response JSON: every response
// carries "subject" {kind,key}; entity kinds additionally keep the
// legacy "entity" and integer "id" fields for existing clients.
func subjectFields(sub storage.MetaSubject) map[string]interface{} {
	m := map[string]interface{}{
		"subject": map[string]string{"kind": sub.Kind, "key": sub.Key},
	}
	if !strings.Contains(sub.Kind, ".") {
		if id, err := strconv.Atoi(sub.Key); err == nil {
			m["entity"] = sub.Kind
			m["id"] = id
		}
	}
	return m
}

func (s *Server) handleMetaList(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")
	idStr := chi.URLParam(r, "id")
	sub, err := storage.ParseMetaSubject(entity, idStr, validateEntityName)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidID, err.Error())
		return
	}

	db, tenantID := s.metaDB(r)
	rows, err := db.QueryContext(r.Context(),
		`SELECT key, value, expires_at, updated_at
		 FROM entity_meta
		 WHERE tenant_id=? AND subject_kind=? AND subject_key=?
		 ORDER BY key`,
		tenantID, sub.Kind, sub.Key)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = rows.Close() }()

	type entry struct {
		Key       string      `json:"key"`
		Value     interface{} `json:"value"`
		ExpiresAt *time.Time  `json:"expires_at,omitempty"`
		UpdatedAt time.Time   `json:"updated_at"`
	}
	entries := make([]entry, 0)
	for rows.Next() {
		var e entry
		var rawVal string
		var expiresAt sql.NullTime
		if err := rows.Scan(&e.Key, &rawVal, &expiresAt, &e.UpdatedAt); err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		if err := json.Unmarshal([]byte(rawVal), &e.Value); err != nil {
			e.Value = rawVal
		}
		if expiresAt.Valid {
			e.ExpiresAt = &expiresAt.Time
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	resp := subjectFields(sub)
	resp["entries"] = entries
	s.writeJSON(w, http.StatusOK, resp)
}

// ─── GET /meta/{entity}/{id}/{key} ───────────────────────────────────────────

func (s *Server) handleMetaGet(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")
	idStr := chi.URLParam(r, "id")
	key := chi.URLParam(r, "key")

	sub, err := storage.ParseMetaSubject(entity, idStr, validateEntityName)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidID, err.Error())
		return
	}
	if !metaKeyRe.MatchString(key) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMetaInvalidKey,
			"key must match [a-zA-Z0-9_]{1,64}")
		return
	}

	db, tenantID := s.metaDB(r)
	var rawVal string
	var expiresAt sql.NullTime
	var updatedAt time.Time
	err = db.QueryRowContext(r.Context(),
		`SELECT value, expires_at, updated_at
		 FROM entity_meta
		 WHERE tenant_id=? AND subject_kind=? AND subject_key=? AND key=?`,
		tenantID, sub.Kind, sub.Key, key).Scan(&rawVal, &expiresAt, &updatedAt)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrMetaKeyNotFound,
			"metadata key not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	var value interface{}
	if err := json.Unmarshal([]byte(rawVal), &value); err != nil {
		value = rawVal
	}

	resp := subjectFields(sub)
	resp["key"] = key
	for k, v := range map[string]interface{}{
		"value":      value,
		"updated_at": updatedAt,
	} {
		resp[k] = v
	}
	if expiresAt.Valid {
		resp["expires_at"] = expiresAt.Time
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// ─── PUT /meta/{entity}/{id}/{key} ───────────────────────────────────────────

func (s *Server) handleMetaPut(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")
	idStr := chi.URLParam(r, "id")
	key := chi.URLParam(r, "key")

	sub, err := storage.ParseMetaSubject(entity, idStr, validateEntityName)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidID, err.Error())
		return
	}
	if !metaKeyRe.MatchString(key) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMetaInvalidKey,
			"key must match [a-zA-Z0-9_]{1,64}")
		return
	}

	// Size check before decode.
	maxBytes := int64(s.config.MetaMaxValueBytes)
	if maxBytes <= 0 {
		maxBytes = 65536
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"failed to read request body")
		return
	}
	if int64(len(body)) > maxBytes {
		s.writeError(w, http.StatusRequestEntityTooLarge, xoluerr.ErrMetaValueTooLarge,
			"value exceeds XOLU_META_MAX_VALUE_BYTES")
		return
	}

	// Decode: body must be a JSON object with a "value" field and optional "expires_at".
	var req struct {
		Value     json.RawMessage `json:"value"`
		ExpiresAt *string         `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON,
			"request body must be a JSON object with a 'value' field")
		return
	}
	if req.Value == nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON,
			"'value' field is required")
		return
	}

	// Parse optional expires_at.
	var expiresAt *time.Time
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrMetaInvalidExpiry,
				"expires_at must be a valid RFC3339 timestamp")
			return
		}
		t = t.UTC()
		expiresAt = &t
	}

	// Verify entity subjects exist (unchanged behaviour). Namespaced
	// kinds skip the check in v1: meta is an engine-inert annotation
	// surface, and per-kind existence probes are a recorded follow-up
	// (docs/API_V2.md meta section).
	if !strings.Contains(sub.Kind, ".") {
		id, convErr := strconv.Atoi(sub.Key)
		if convErr != nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidID, "invalid entity id")
			return
		}
		store := s.getStore(r.Context())
		if !store.Exists(r.Context(), sub.Kind, id) {
			s.writeError(w, http.StatusNotFound, xoluerr.ErrMetaEntityNotFound,
				"entity not found")
			return
		}
	}

	db, tenantID := s.metaDB(r)
	now := time.Now().UTC()

	if expiresAt != nil {
		_, err = db.ExecContext(r.Context(),
			`INSERT INTO entity_meta (tenant_id, subject_kind, subject_key, key, value, expires_at, updated_at)
			 VALUES (?,?,?,?,?,?,?)
			 ON CONFLICT(tenant_id, subject_kind, subject_key, key)
			 DO UPDATE SET value=excluded.value,
			               expires_at=excluded.expires_at,
			               updated_at=excluded.updated_at`,
			tenantID, sub.Kind, sub.Key, key, string(req.Value), expiresAt.UTC(), now)
	} else {
		_, err = db.ExecContext(r.Context(),
			`INSERT INTO entity_meta (tenant_id, subject_kind, subject_key, key, value, expires_at, updated_at)
			 VALUES (?,?,?,?,?,NULL,?)
			 ON CONFLICT(tenant_id, subject_kind, subject_key, key)
			 DO UPDATE SET value=excluded.value,
			               expires_at=NULL,
			               updated_at=excluded.updated_at`,
			tenantID, sub.Kind, sub.Key, key, string(req.Value), now)
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	resp := subjectFields(sub)
	resp["key"] = key
	for k, v := range map[string]interface{}{
		"updated_at": now,
	} {
		resp[k] = v
	}
	if expiresAt != nil {
		resp["expires_at"] = expiresAt
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// ─── DELETE /meta/{entity}/{id}/{key} ────────────────────────────────────────

func (s *Server) handleMetaDeleteKey(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")
	idStr := chi.URLParam(r, "id")
	key := chi.URLParam(r, "key")

	sub, err := storage.ParseMetaSubject(entity, idStr, validateEntityName)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidID, err.Error())
		return
	}
	if !metaKeyRe.MatchString(key) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMetaInvalidKey,
			"key must match [a-zA-Z0-9_]{1,64}")
		return
	}

	db, tenantID := s.metaDB(r)
	res, err := db.ExecContext(r.Context(),
		`DELETE FROM entity_meta WHERE tenant_id=? AND subject_kind=? AND subject_key=? AND key=?`,
		tenantID, sub.Kind, sub.Key, key)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrMetaKeyNotFound,
			"metadata key not found")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": true})
}

// ─── DELETE /meta/{entity}/{id} ──────────────────────────────────────────────

func (s *Server) handleMetaDeleteAll(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")
	idStr := chi.URLParam(r, "id")
	sub, err := storage.ParseMetaSubject(entity, idStr, validateEntityName)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidID, err.Error())
		return
	}

	db, tenantID := s.metaDB(r)
	res, err := db.ExecContext(r.Context(),
		`DELETE FROM entity_meta WHERE tenant_id=? AND subject_kind=? AND subject_key=?`,
		tenantID, sub.Kind, sub.Key)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": n})
}

// ─── MetaSweeper — gc.Sweeper for expired metadata entries ───────────────────

// MetaSweeper implements gc.Sweeper. It deletes entity_meta rows whose
// expires_at is in the past. Fires no events in S3; event dispatch is
// wired in S9 when the event subscription system lands.
type MetaSweeper struct {
	db *sql.DB
}

// NewMetaSweeper creates a MetaSweeper backed by the given database.
func NewMetaSweeper(db *sql.DB) *MetaSweeper {
	return &MetaSweeper{db: db}
}

// Sweep deletes all expired entity_meta rows and returns a gc.Report.
func (m *MetaSweeper) Sweep(ctx context.Context) (gcpkg.Report, error) {
	now := time.Now().UTC()
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM entity_meta WHERE expires_at IS NOT NULL AND expires_at < ?`, now)
	if err != nil {
		return gcpkg.Report{Errors: 1}, err
	}
	n, _ := res.RowsAffected()
	return gcpkg.Report{
		Examined:  int(n), // examined = candidates deleted (no pre-scan)
		Collected: int(n),
	}, nil
}
