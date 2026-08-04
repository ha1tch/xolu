// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_loc_pattern_handlers.go — T-131 (wave 9b): fence-type patterns,
// loc-01-rest-api.md §2a. A pattern is not a fence or a location —
// addressed by plain (tenant, id), no subject convention, mirroring
// obj-01-rest-api.md §4a's own def/list/get/delete shape (minus
// extract/pattern_after — see patterns.go's own package doc for why).

package server

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/loc"
)

type locPatternDefReq struct {
	Name     string `json:"name"`
	Capacity int64  `json:"capacity"`
}

type locPatternResponse struct {
	Name     string `json:"name"`
	Capacity int64  `json:"capacity"`
}

func patternResponse(p loc.Pattern) locPatternResponse {
	return locPatternResponse{Name: p.ID, Capacity: p.Capacity}
}

func (s *Server) handleLocPatternsDef(w http.ResponseWriter, r *http.Request) {
	var req locPatternDefReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	if req.Name == "" {
		s.writeLocError(w, &loc.ValidationError{Detail: "name is required"})
		return
	}
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := st.DefPattern(r.Context(), req.Name, req.Capacity); err != nil {
		s.writeLocError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, locPatternResponse{Name: req.Name, Capacity: req.Capacity})
}

func (s *Server) handleLocPatternsList(w http.ResponseWriter, r *http.Request) {
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	all, err := st.ListPatterns(r.Context())
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	out := make([]locPatternResponse, 0, len(all))
	for _, p := range all {
		out = append(out, patternResponse(p))
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"patterns": out})
}

func (s *Server) handleLocPatternsGet(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	p, err := st.GetPattern(r.Context(), id)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, patternResponse(*p))
}

func (s *Server) handleLocPatternsDelete(w http.ResponseWriter, r *http.Request) {
	id := pathParam(r, "id")
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := st.DeletePattern(r.Context(), id); err != nil {
		s.writeLocError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setupV2LocPatternRoutes(r chi.Router) {
	r.Post("/loc/patterns/def", s.handleLocPatternsDef)
	r.Get("/loc/patterns/list", s.handleLocPatternsList)
	r.Get("/loc/patterns/{id}", s.handleLocPatternsGet)
	r.Delete("/loc/patterns/{id}", s.handleLocPatternsDelete)
}
