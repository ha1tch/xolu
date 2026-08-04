// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_seq_handlers.go
//
// S5: named monotonic sequence endpoints.
//
//   POST   /api/v2/gen/seq            — define a sequence
//   GET    /api/v2/gen/seq/{name}     — get definition
//   GET    /api/v2/gen/seq/{name}/next — increment and return next value
//   POST   /api/v2/gen/seq/{name}/reset — reset to start value
//   DELETE /api/v2/gen/seq/{name}     — delete definition and state
//
// All paths are also accessible via /api/v2/seq/* alias.
//
// OQL integration: NEXT VALUE FOR name and @CURRENT_VALUE('name') are
// handled in executor_sequences.go. The server wires seqIncrementor
// into the OQL executor on startup.

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// seqNameRe validates sequence names: same rule as meta keys.
var seqNameRe = regexp.MustCompile(`^[a-zA-Z0-9_]{1,64}$`)

// seqDB returns the writer DB and tenant ID for sequence operations.
func (s *Server) seqDB(r *http.Request) (*sql.DB, tenant.TenantID) {
	store := s.getStore(r.Context())
	tenantID := getTenantIDNumeric(r.Context())
	if wdp, ok := store.(storage.WriterDBProvider); ok {
		return wdp.WriterDB(), tenantID
	}
	return nil, tenantID
}

// ─── POST /gen/seq — define ───────────────────────────────────────────────────

func (s *Server) handleSeqDefine(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name        string `json:"name"`
		Start       *int64 `json:"start"`
		IncrementBy *int64 `json:"increment_by"`
		MinVal      *int64 `json:"min_val"`
		MaxVal      *int64 `json:"max_val"`
		Cycle       bool   `json:"cycle"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if !seqNameRe.MatchString(req.Name) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMetaInvalidKey,
			"name must match [a-zA-Z0-9_]{1,64}")
		return
	}

	start := int64(1)
	if req.Start != nil {
		start = *req.Start
	}
	inc := int64(1)
	if req.IncrementBy != nil {
		inc = *req.IncrementBy
		if inc == 0 {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrGenInvalidConfig,
				"increment_by must not be zero")
			return
		}
	}
	if req.MinVal != nil && req.MaxVal != nil && *req.MinVal > *req.MaxVal {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrGenInvalidConfig,
			"min_val must be <= max_val")
		return
	}
	if req.MinVal != nil && start < *req.MinVal {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrGenInvalidConfig,
			"start must be >= min_val")
		return
	}
	if req.MaxVal != nil && start > *req.MaxVal {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrGenInvalidConfig,
			"start must be <= max_val")
		return
	}

	db, tenantID := s.seqDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 sequences")
		return
	}

	// Check for name collision across all generator types.
	var existing int
	_ = db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM gen_definitions WHERE tenant_id=? AND name=?`,
		tenantID, req.Name).Scan(&existing)
	if existing > 0 {
		s.writeError(w, http.StatusUnprocessableEntity, xoluerr.ErrGenNameExists,
			"a generator with that name already exists")
		return
	}

	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.ExecContext(r.Context(),
		`INSERT INTO gen_definitions (tenant_id, type, name) VALUES (?,?,?)`,
		tenantID, "sequence", req.Name)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	cycle := 0
	if req.Cycle {
		cycle = 1
	}
	_, err = tx.ExecContext(r.Context(),
		`INSERT INTO sequences (tenant_id, name, current_val, start_val, increment_by, min_val, max_val, cycle)
		 VALUES (?,?,?,?,?,?,?,?)`,
		tenantID, req.Name, start-inc, start, inc, req.MinVal, req.MaxVal, cycle)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"name":         req.Name,
		"start":        start,
		"increment_by": inc,
		"min_val":      req.MinVal,
		"max_val":      req.MaxVal,
		"cycle":        req.Cycle,
	})
}

// ─── GET /gen/seq/{name} — definition ────────────────────────────────────────

// handleSeqList enumerates the tenant's named sequences (T-25).
// Field names match handleSeqGet's response; there is no created_at
// column in the sequences table, so none is reported. The static
// GET /gen/seq route takes chi precedence over GET /gen/{type}, which
// previously caught this path as type="seq".
func (s *Server) handleSeqList(w http.ResponseWriter, r *http.Request) {
	db, tenantID := s.seqDB(r)
	rows, err := db.QueryContext(r.Context(),
		`SELECT name, current_val, increment_by, cycle
		 FROM sequences WHERE tenant_id=? ORDER BY name`,
		tenantID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = rows.Close() }()

	seqs := []map[string]interface{}{}
	for rows.Next() {
		var name string
		var currentVal, incBy int64
		var cycle int
		if err := rows.Scan(&name, &currentVal, &incBy, &cycle); err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		seqs = append(seqs, map[string]interface{}{
			"name":         name,
			"current":      currentVal,
			"increment_by": incBy,
			"cycle":        cycle == 1,
		})
	}
	if err := rows.Err(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"sequences": seqs,
		"count":     len(seqs),
	})
}

func (s *Server) handleSeqGet(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !seqNameRe.MatchString(name) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMetaInvalidKey,
			"name must match [a-zA-Z0-9_]{1,64}")
		return
	}

	db, tenantID := s.seqDB(r)
	var startVal, currentVal, incBy int64
	var minVal, maxVal sql.NullInt64
	var cycle int
	err := db.QueryRowContext(r.Context(),
		`SELECT start_val, current_val, increment_by, min_val, max_val, cycle
		 FROM sequences WHERE tenant_id=? AND name=?`,
		tenantID, name).Scan(&startVal, &currentVal, &incBy, &minVal, &maxVal, &cycle)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrGenNotFound,
			"sequence not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	resp := map[string]interface{}{
		"name":         name,
		"start":        startVal,
		"current":      currentVal,
		"increment_by": incBy,
		"cycle":        cycle == 1,
	}
	if minVal.Valid {
		resp["min_val"] = minVal.Int64
	}
	if maxVal.Valid {
		resp["max_val"] = maxVal.Int64
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// ─── GET /gen/seq/{name}/next — increment ────────────────────────────────────

func (s *Server) handleSeqNext(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !seqNameRe.MatchString(name) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMetaInvalidKey,
			"name must match [a-zA-Z0-9_]{1,64}")
		return
	}
	db, tenantID := s.seqDB(r)
	val, err := seqIncrement(r.Context(), db, tenantID, name)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrGenNotFound, "sequence not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusUnprocessableEntity, xoluerr.ErrGenExhausted, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":  name,
		"value": val,
	})
}

// ─── POST /gen/seq/{name}/reset — reset ──────────────────────────────────────

func (s *Server) handleSeqReset(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !seqNameRe.MatchString(name) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMetaInvalidKey,
			"name must match [a-zA-Z0-9_]{1,64}")
		return
	}

	var req struct {
		Value *int64 `json:"value"` // nil = reset to start_val
	}
	// Body is optional; ignore decode errors.
	_ = json.NewDecoder(r.Body).Decode(&req)

	db, tenantID := s.seqDB(r)

	// Fetch start_val and increment_by first.
	var startVal, incBy int64
	err := db.QueryRowContext(r.Context(),
		`SELECT start_val, increment_by FROM sequences WHERE tenant_id=? AND name=?`,
		tenantID, name).Scan(&startVal, &incBy)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrGenNotFound, "sequence not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	newVal := startVal - incBy // so first NEXT VALUE FOR returns startVal
	if req.Value != nil {
		newVal = *req.Value - incBy
	}

	_, err = db.ExecContext(r.Context(),
		`UPDATE sequences SET current_val=? WHERE tenant_id=? AND name=?`,
		newVal, tenantID, name)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"name":  name,
		"reset": true,
		"next":  newVal + incBy,
	})
}

// ─── DELETE /gen/seq/{name} ───────────────────────────────────────────────────

func (s *Server) handleSeqDelete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if !seqNameRe.MatchString(name) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMetaInvalidKey,
			"name must match [a-zA-Z0-9_]{1,64}")
		return
	}

	db, tenantID := s.seqDB(r)
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(r.Context(),
		`DELETE FROM sequences WHERE tenant_id=? AND name=?`, tenantID, name)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrGenNotFound, "sequence not found")
		return
	}
	_, _ = tx.ExecContext(r.Context(),
		`DELETE FROM gen_definitions WHERE tenant_id=? AND type='sequence' AND name=?`,
		tenantID, name)
	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": true})
}

// ─── seqIncrement — atomic increment used by HTTP handler and OQL ─────────────

// seqIncrement atomically increments the named sequence for the given tenant
// and returns the new value. Returns sql.ErrNoRows if the sequence doesn't
// exist, or an error if the sequence is exhausted and not cyclic.
func seqIncrement(ctx context.Context, db *sql.DB, tenantID tenant.TenantID, name string) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	next, err := storage.SeqIncrementTx(ctx, tx, tenantID, name)
	if err != nil {
		return 0, err
	}
	return next, tx.Commit()
}

// serverSeqIncrementor returns the OQL seqIncrementor closure for this server.
func (s *Server) serverSeqIncrementor() func(tenantID tenant.TenantID, name string) (int64, error) {
	return func(tenantID tenant.TenantID, name string) (int64, error) {
		store, err := s.storeForTenant(tenantID)
		if err != nil {
			return 0, err
		}
		wdp, ok := store.(storage.WriterDBProvider)
		if !ok {
			return 0, sql.ErrNoRows
		}
		return seqIncrement(context.Background(), wdp.WriterDB(), tenantID, name)
	}
}
