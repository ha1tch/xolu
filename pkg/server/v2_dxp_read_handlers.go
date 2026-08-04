// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_dxp_read_handlers.go — GET /dxp/def, GET /dxp/def/{id},
// GET /dxp/txn, GET /dxp/txn/{id}. Item 20's own remaining scope,
// named explicitly in the handover as "genuinely not built (only
// POST exists for either)... a real, named gap, not an oversight."
// Shape follows handleFSMDefList/handleFSMDefGet (v2_fsm_def_handlers.go)
// directly — dxp_defs mirrors fsm_definitions structurally on purpose
// (T-87's own recorded reasoning), so its read surface should too.
//
// GET /dxp/txn supports an optional ?status= filter — the whole point
// of building this now rather than later: T-100's sweeper and any
// future mount-time pass both need to be observable, and there was no
// way to see a swept/expired/stuck instance at all before this file.

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
)

// ─── GET /dxp/def — list ───────────────────────────────────────────────────

func (s *Server) handleDxpDefList(w http.ResponseWriter, r *http.Request) {
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 dxp")
		return
	}

	rows, err := db.QueryContext(r.Context(), `
		SELECT id, name, created_at
		FROM dxp_defs
		WHERE tenant_id = ?
		ORDER BY id`, tenantID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = rows.Close() }()

	type defSummary struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		CreatedAt string `json:"created_at"`
	}
	defs := []defSummary{}
	for rows.Next() {
		var d defSummary
		if err := rows.Scan(&d.ID, &d.Name, &d.CreatedAt); err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		defs = append(defs, d)
	}
	if err := rows.Err(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{"definitions": defs})
}

// ─── GET /dxp/def/{id} — retrieve ──────────────────────────────────────────

func (s *Server) handleDxpDefGet(w http.ResponseWriter, r *http.Request) {
	id, ok := s.dxpParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 dxp")
		return
	}

	var name, specJSON, analysisJSON, bindingsSchemaJSON, createdAt string
	err := db.QueryRowContext(r.Context(), `
		SELECT name, spec_json, analysis_json, bindings_schema_json, created_at
		FROM dxp_defs
		WHERE tenant_id = ? AND id = ?`, tenantID, id).
		Scan(&name, &specJSON, &analysisJSON, &bindingsSchemaJSON, &createdAt)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrDXPDefinitionInvalid,
			"dxp/def not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// Composed without re-parsing spec_json/analysis_json/bindings_schema_json
	// — matches handleFSMDefGet's own approach exactly, same reasoning.
	_, _ = w.Write([]byte(`{"id":` + strconv.FormatInt(id, 10) +
		`,"name":` + strconvQuote(name) +
		`,"created_at":` + strconvQuote(createdAt) +
		`,"spec":` + specJSON +
		`,"analysis":` + analysisJSON +
		`,"bindings_schema":` + bindingsSchemaJSON + `}`))
}

// ─── GET /dxp/txn — list ───────────────────────────────────────────────────

// handleDxpTxnList supports an optional ?status= filter (active,
// committed, released, expired) — with no filter, every instance for
// the tenant is returned, oldest first.
func (s *Server) handleDxpTxnList(w http.ResponseWriter, r *http.Request) {
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 dxp")
		return
	}

	type txnSummary struct {
		ID               int64  `json:"id"`
		DefID            int64  `json:"def_id"`
		DefName          string `json:"def_name"`
		Status           string `json:"status"`
		CommittedThrough int    `json:"committed_through"`
		CreatedAt        string `json:"created_at"`
	}

	statusFilter := r.URL.Query().Get("status")
	var rows *sql.Rows
	var err error
	if statusFilter != "" {
		rows, err = db.QueryContext(r.Context(), `
			SELECT id, dxp_def_id, dxp_def_name, status, committed_through, created_at
			FROM dxp_txn
			WHERE tenant_id = ? AND status = ?
			ORDER BY id`, tenantID, statusFilter)
	} else {
		rows, err = db.QueryContext(r.Context(), `
			SELECT id, dxp_def_id, dxp_def_name, status, committed_through, created_at
			FROM dxp_txn
			WHERE tenant_id = ?
			ORDER BY id`, tenantID)
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = rows.Close() }()

	instances := []txnSummary{}
	for rows.Next() {
		var t txnSummary
		if err := rows.Scan(&t.ID, &t.DefID, &t.DefName, &t.Status, &t.CommittedThrough, &t.CreatedAt); err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		instances = append(instances, t)
	}
	if err := rows.Err(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{"instances": instances})
}

// ─── GET /dxp/txn/{id} — retrieve ──────────────────────────────────────────

func (s *Server) handleDxpTxnGet(w http.ResponseWriter, r *http.Request) {
	id, ok := s.dxpParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 dxp")
		return
	}

	var defID int64
	var defName, snapshotJSON, status, createdAt string
	var committedThrough int
	var deadlineNs int64
	err := db.QueryRowContext(r.Context(), `
		SELECT dxp_def_id, dxp_def_name, snapshot_json, status, committed_through, deadline_ns, created_at
		FROM dxp_txn
		WHERE tenant_id = ? AND id = ?`, tenantID, id).
		Scan(&defID, &defName, &snapshotJSON, &status, &committedThrough, &deadlineNs, &createdAt)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrEntityNotFound,
			"dxp/txn not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"id":` + strconv.FormatInt(id, 10) +
		`,"def_id":` + strconv.FormatInt(defID, 10) +
		`,"def_name":` + strconvQuote(defName) +
		`,"status":` + strconvQuote(status) +
		`,"committed_through":` + strconv.Itoa(committedThrough) +
		`,"deadline_ns":` + strconv.FormatInt(deadlineNs, 10) +
		`,"created_at":` + strconvQuote(createdAt) +
		`,"snapshot":` + snapshotJSON + `}`))
}

// ─── shared helpers ─────────────────────────────────────────────────────────

// dxpParseID parses the {id} URL param for dxp/def and dxp/txn GETs.
// Deliberately not fsmParseID reused directly — that helper's own
// failure path returns ErrFSMDefNotFound, the wrong taxonomy here.
func (s *Server) dxpParseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrEntityNotFound,
			"id must be a positive integer")
		return 0, false
	}
	return id, true
}
