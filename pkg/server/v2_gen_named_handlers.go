// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_gen_named_handlers.go
//
// S10: HTTP surface for named stateful generators, stored in gen_definitions
// as (tenant_id, type, name, config_json) and invoked by name.
//
//   POST   /api/v2/gen/{type}              Define a named generator
//   GET    /api/v2/gen/{type}              List generators of a type
//   GET    /api/v2/gen/{type}/{name}       Retrieve a definition's metadata
//   GET    /api/v2/gen/{type}/{name}/next  Generate the next value
//   DELETE /api/v2/gen/{type}/{name}       Delete a definition
//
// All value production routes through dispatchGen, the same path @GEN('name')
// uses, so the HTTP and OQL surfaces cannot diverge. The 'sequence' type is not
// addressable here — sequences have their own /gen/seq surface — and is
// rejected so a generic /gen/sequence cannot shadow it.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/storage"
)

// genNameRe validates generator names: same rule as sequences and meta keys.
var genNameRe = regexp.MustCompile(`^[a-zA-Z0-9_]{1,64}$`)

// genDB resolves the writer DB and tenant for a request, mirroring seqDB.
func (s *Server) genDB(r *http.Request) (*sql.DB, uint16) {
	store := s.getStore(r.Context())
	tenantID := getTenantIDNumeric(r.Context())
	if wdp, ok := store.(storage.WriterDBProvider); ok {
		return wdp.WriterDB(), tenantID
	}
	return nil, tenantID
}

// genTypeFromURL extracts and validates the {type} path parameter. It rejects
// 'sequence' (own surface) and any unknown type. Returns "" after writing an
// error if invalid.
func (s *Server) genTypeFromURL(w http.ResponseWriter, r *http.Request) string {
	t := chi.URLParam(r, "type")
	if t == "sequence" || t == "seq" {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrGenNotFound,
			"sequences are managed via /gen/seq, not /gen/{type}")
		return ""
	}
	if !validGenType(t) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrGenInvalidConfig,
			"unknown generator type "+t)
		return ""
	}
	return t
}

// ─── POST /gen/{type} ─────────────────────────────────────────────────────────

func (s *Server) handleGenDefine(w http.ResponseWriter, r *http.Request) {
	gtype := s.genTypeFromURL(w, r)
	if gtype == "" {
		return
	}

	var req struct {
		Name   string          `json:"name"`
		Config json.RawMessage `json:"config"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if !genNameRe.MatchString(req.Name) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrGenInvalidConfig,
			"name must match [a-zA-Z0-9_]{1,64}")
		return
	}

	// Validate the typed config at define time so a bad definition is rejected
	// up front rather than at first use.
	cfg, perr := parseGenConfig(gtype, req.Config)
	if perr != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrGenInvalidConfig, perr.Error())
		return
	}
	if verr := cfg.validate(); verr != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrGenInvalidConfig, verr.Error())
		return
	}

	db, tenantID := s.genDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 generators")
		return
	}

	// Name collision across all generator types (sequences included), matching
	// the seq define behaviour: a name is unique within a tenant regardless of
	// type.
	var existing int
	_ = db.QueryRowContext(r.Context(),
		`SELECT COUNT(*) FROM gen_definitions WHERE tenant_id=? AND name=?`,
		tenantID, req.Name).Scan(&existing)
	if existing > 0 {
		s.writeError(w, http.StatusUnprocessableEntity, xoluerr.ErrGenNameExists,
			"a generator with that name already exists")
		return
	}

	configToStore := req.Config
	if len(configToStore) == 0 {
		configToStore = json.RawMessage("{}")
	}
	_, err := db.ExecContext(r.Context(),
		`INSERT INTO gen_definitions (tenant_id, type, name, config_json) VALUES (?,?,?,?)`,
		tenantID, gtype, req.Name, string(configToStore))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"type":   gtype,
		"name":   req.Name,
		"config": json.RawMessage(configToStore),
	})
}

// ─── GET /gen/{type}/{name}/next ──────────────────────────────────────────────

func (s *Server) handleGenNext(w http.ResponseWriter, r *http.Request) {
	gtype := s.genTypeFromURL(w, r)
	if gtype == "" {
		return
	}
	name := chi.URLParam(r, "name")
	if !genNameRe.MatchString(name) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrGenInvalidConfig,
			"name must match [a-zA-Z0-9_]{1,64}")
		return
	}
	db, tenantID := s.genDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 generators")
		return
	}

	var configJSON string
	err := db.QueryRowContext(r.Context(),
		`SELECT config_json FROM gen_definitions WHERE tenant_id=? AND type=? AND name=?`,
		tenantID, gtype, name).Scan(&configJSON)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrGenNotFound, "generator not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	value, gerr := s.dispatchGenStateful(tenantID, name, gtype, []byte(configJSON))
	if gerr != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrGenInvalidConfig, gerr.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"type":  gtype,
		"name":  name,
		"value": value,
	})
}

// ─── GET /gen/{type} (list) ───────────────────────────────────────────────────

func (s *Server) handleGenList(w http.ResponseWriter, r *http.Request) {
	gtype := s.genTypeFromURL(w, r)
	if gtype == "" {
		return
	}
	db, tenantID := s.genDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 generators")
		return
	}
	rows, err := db.QueryContext(r.Context(),
		`SELECT name, config_json FROM gen_definitions WHERE tenant_id=? AND type=? ORDER BY name`,
		tenantID, gtype)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = rows.Close() }()

	gens := []map[string]interface{}{}
	for rows.Next() {
		var name, configJSON string
		if err := rows.Scan(&name, &configJSON); err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		gens = append(gens, map[string]interface{}{
			"name":   name,
			"config": json.RawMessage(configJSON),
		})
	}
	if err := rows.Err(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"type":       gtype,
		"generators": gens,
	})
}

// ─── GET /gen/{type}/{name} (retrieve metadata) ───────────────────────────────

func (s *Server) handleGenGet(w http.ResponseWriter, r *http.Request) {
	gtype := s.genTypeFromURL(w, r)
	if gtype == "" {
		return
	}
	name := chi.URLParam(r, "name")
	if !genNameRe.MatchString(name) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrGenInvalidConfig,
			"name must match [a-zA-Z0-9_]{1,64}")
		return
	}
	db, tenantID := s.genDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 generators")
		return
	}
	var configJSON string
	err := db.QueryRowContext(r.Context(),
		`SELECT config_json FROM gen_definitions WHERE tenant_id=? AND type=? AND name=?`,
		tenantID, gtype, name).Scan(&configJSON)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrGenNotFound, "generator not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"type":   gtype,
		"name":   name,
		"config": json.RawMessage(configJSON),
	})
}

// ─── DELETE /gen/{type}/{name} ────────────────────────────────────────────────

func (s *Server) handleGenDelete(w http.ResponseWriter, r *http.Request) {
	gtype := s.genTypeFromURL(w, r)
	if gtype == "" {
		return
	}
	name := chi.URLParam(r, "name")
	if !genNameRe.MatchString(name) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrGenInvalidConfig,
			"name must match [a-zA-Z0-9_]{1,64}")
		return
	}
	db, tenantID := s.genDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 generators")
		return
	}
	res, err := db.ExecContext(r.Context(),
		`DELETE FROM gen_definitions WHERE tenant_id=? AND type=? AND name=?`,
		tenantID, gtype, name)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrGenNotFound, "generator not found")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": name})
}
