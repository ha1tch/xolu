// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// Admin API for the dynamic configuration store.
//
// Routes (registered only when DynConfigEnabled && DynConfigAPIEnabled):
//
//	GET    /api/v1/admin/config                     — dump all namespaces/keys
//	GET    /api/v1/admin/config/{namespace}          — dump one namespace
//	GET    /api/v1/admin/config/{namespace}/{key}    — read one value
//	PUT    /api/v1/admin/config/{namespace}/{key}    — set one value
//	DELETE /api/v1/admin/config/{namespace}/{key}    — remove one value
//
// Values are raw JSON: numbers, strings, booleans, objects, arrays, null.
// Namespace and key names must match [a-zA-Z0-9._-]+.

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
)

// dynConfigGuard returns false and writes a 503 if dynConfig is not
// initialised. Handlers call this as their first check.
func (s *Server) dynConfigGuard(w http.ResponseWriter) bool {
	if s.dynConfig == nil {
		s.writeError(w, http.StatusServiceUnavailable, xoluerr.ErrDCDisabled,
			"Dynamic configuration is not enabled")
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// GET /api/v1/admin/config
// ---------------------------------------------------------------------------

func (s *Server) handleDynConfigDump(w http.ResponseWriter, r *http.Request) {
	if !s.dynConfigGuard(w) {
		return
	}
	s.writeJSON(w, http.StatusOK, s.dynConfig.Dump())
}

// ---------------------------------------------------------------------------
// GET /api/v1/admin/config/{namespace}
// ---------------------------------------------------------------------------

func (s *Server) handleDynConfigGetNamespace(w http.ResponseWriter, r *http.Request) {
	if !s.dynConfigGuard(w) {
		return
	}
	ns := chi.URLParam(r, "namespace")
	data := s.dynConfig.Namespace(ns)
	if data == nil {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrDCNotFound,
			"Namespace not found")
		return
	}
	s.writeJSON(w, http.StatusOK, data)
}

// ---------------------------------------------------------------------------
// GET /api/v1/admin/config/{namespace}/{key}
// ---------------------------------------------------------------------------

func (s *Server) handleDynConfigGet(w http.ResponseWriter, r *http.Request) {
	if !s.dynConfigGuard(w) {
		return
	}
	ns := chi.URLParam(r, "namespace")
	key := chi.URLParam(r, "key")

	val := s.dynConfig.Get(ns, key)
	if val == nil {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrDCNotFound,
			"Key not found")
		return
	}
	// Write raw JSON value directly — already well-formed by construction.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(val)
}

// ---------------------------------------------------------------------------
// PUT /api/v1/admin/config/{namespace}/{key}
// ---------------------------------------------------------------------------

func (s *Server) handleDynConfigSet(w http.ResponseWriter, r *http.Request) {
	if !s.dynConfigGuard(w) {
		return
	}
	ns := chi.URLParam(r, "namespace")
	key := chi.URLParam(r, "key")

	body, err := io.ReadAll(io.LimitReader(r.Body, 65536))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrDCInvalidInput,
			"Failed to read request body")
		return
	}
	if len(body) == 0 {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrDCInvalidInput,
			"Request body must be a JSON value")
		return
	}

	if err := s.dynConfig.Set(ns, key, json.RawMessage(body)); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrDCInvalidInput, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]string{
		"namespace": ns,
		"key":       key,
		"status":    "set",
	})
}

// ---------------------------------------------------------------------------
// DELETE /api/v1/admin/config/{namespace}/{key}
// ---------------------------------------------------------------------------

func (s *Server) handleDynConfigDelete(w http.ResponseWriter, r *http.Request) {
	if !s.dynConfigGuard(w) {
		return
	}
	ns := chi.URLParam(r, "namespace")
	key := chi.URLParam(r, "key")

	if err := s.dynConfig.Delete(ns, key); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrDCStoreFailed,
			"Failed to delete config key")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{
		"namespace": ns,
		"key":       key,
		"status":    "deleted",
	})
}
