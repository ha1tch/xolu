// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/timeseries"
)

// ─── shared helpers ───────────────────────────────────────────────────────────

// tsParseRollupTimeline resolves the store and validates that the timeline_id
// path parameter is non-zero (timeline 0 is the structural root and is
// forbidden for all rollup operations).
func (s *Server) tsParseRollupTimeline(w http.ResponseWriter, r *http.Request) (timeseries.Store, timeseries.TimelineID, bool) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return nil, 0, false
	}
	raw := chi.URLParam(r, "timeline_id")
	tidInt, err := strconv.ParseUint(raw, 10, 16)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.Code("XOLU-TS004"),
			fmt.Sprintf("invalid timeline_id %q", raw))
		return nil, 0, false
	}
	if tidInt == 0 {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSRootTimeline,
			"timeline 0 is the structural root and cannot be used with rollup operations")
		return nil, 0, false
	}
	tid := timeseries.TimelineID(tidInt)
	if _, ok := store.Timeline(tid); !ok {
		s.writeError(w, http.StatusNotFound, xoluerr.Code("XOLU-TS004"),
			fmt.Sprintf("timeline %d not defined", tidInt))
		return nil, 0, false
	}
	return store, tid, true
}

// tsParseRollupID resolves store, timeline_id, and rollup_id from the path.
func (s *Server) tsParseRollupID(w http.ResponseWriter, r *http.Request) (timeseries.Store, timeseries.TimelineID, timeseries.RollupID, bool) {
	store, tid, ok := s.tsParseRollupTimeline(w, r)
	if !ok {
		return nil, 0, "", false
	}
	rid := timeseries.RollupID(chi.URLParam(r, "rollup_id"))
	if rid == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSRollupNotFound, "missing rollup_id")
		return nil, 0, "", false
	}
	return store, tid, rid, true
}

// ─── define / list / get / delete ─────────────────────────────────────────────

// tsDefineRollupRequest is the body for POST /rollup/def.
type tsDefineRollupRequest struct {
	DestTID        uint16 `json:"dest_tid"`
	BucketDuration string `json:"bucket_duration"` // Go duration string, e.g. "5m"
	LateWindow     string `json:"late_window,omitempty"`
}

// tsRollupDefResponse is the response body for rollup definition endpoints.
type tsRollupDefResponse struct {
	ID             timeseries.RollupID `json:"id"`
	SourceTID      uint16              `json:"source_tid"`
	DestTID        uint16              `json:"dest_tid"`
	BucketDuration string              `json:"bucket_duration"`
	LateWindow     string              `json:"late_window,omitempty"`
	Running        bool                `json:"running"`
	CreatedAt      time.Time           `json:"created_at"`
}

func defToResponse(d timeseries.RollupDef) tsRollupDefResponse {
	r := tsRollupDefResponse{
		ID:             d.ID,
		SourceTID:      uint16(d.SourceTID),
		DestTID:        uint16(d.DestTID),
		BucketDuration: d.BucketDuration.String(),
		Running:        d.Running,
		CreatedAt:      d.CreatedAt,
	}
	if d.LateWindow > 0 {
		r.LateWindow = d.LateWindow.String()
	}
	return r
}

// HandleTSDefineRollup creates a rollup definition on a source timeline.
//
//	POST /api/v1/tenant/{tenant_id}/ts/timelines/{timeline_id}/rollup/def
func (s *Server) HandleTSDefineRollup(w http.ResponseWriter, r *http.Request) {
	store, tid, ok := s.tsParseRollupTimeline(w, r)
	if !ok {
		return
	}

	var req tsDefineRollupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSInvalidWriteConfig, "invalid request body")
		return
	}
	bucketDur, err := time.ParseDuration(req.BucketDuration)
	if err != nil || bucketDur <= 0 {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSInvalidInterval,
			fmt.Sprintf("invalid bucket_duration %q: must be a positive Go duration (e.g. \"5m\")", req.BucketDuration))
		return
	}
	var lateWindow time.Duration
	if req.LateWindow != "" {
		lateWindow, err = time.ParseDuration(req.LateWindow)
		if err != nil || lateWindow < 0 {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSInvalidInterval,
				fmt.Sprintf("invalid late_window %q", req.LateWindow))
			return
		}
	}

	def := timeseries.RollupDef{
		DestTID:        timeseries.TimelineID(req.DestTID),
		BucketDuration: bucketDur,
		LateWindow:     lateWindow,
	}
	if def.DestTID == 0 {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSRootTimeline,
			"timeline 0 cannot be a rollup destination")
		return
	}
	id, err := store.DefineRollup(tid, def)
	if err != nil {
		status := http.StatusBadRequest
		var code xoluerr.Code
		switch {
		case errContains(err, string(xoluerr.ErrTSRollupCycle)):
			code = xoluerr.ErrTSRollupCycle
		case errContains(err, string(xoluerr.ErrTSRollupDepth)):
			code = xoluerr.ErrTSRollupDepth
		case errContains(err, string(xoluerr.ErrTSRollupDestInUse)):
			code = xoluerr.ErrTSRollupDestInUse
		case errContains(err, string(xoluerr.ErrTSRootTimeline)):
			code = xoluerr.ErrTSRootTimeline
		case errContains(err, "XOLU-TS004"):
			// Undefined source or destination timeline.
			code = xoluerr.Code("XOLU-TS004")
		default:
			status = http.StatusInternalServerError
			code = xoluerr.ErrTSInternal
		}
		s.writeError(w, status, code, err.Error())
		return
	}

	created, _ := store.GetRollup(tid, id)
	s.writeJSON(w, http.StatusCreated, defToResponse(created))
}

// HandleTSListRollups lists all rollup definitions for a source timeline.
//
//	GET /api/v1/tenant/{tenant_id}/ts/timelines/{timeline_id}/rollup/list
func (s *Server) HandleTSListRollups(w http.ResponseWriter, r *http.Request) {
	store, tid, ok := s.tsParseRollupTimeline(w, r)
	if !ok {
		return
	}
	defs, err := store.ListRollups(tid)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrTSInternal, err.Error())
		return
	}
	resp := make([]tsRollupDefResponse, len(defs))
	for i, d := range defs {
		resp[i] = defToResponse(d)
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// HandleTSGetRollup returns a specific rollup definition.
//
//	GET /api/v1/tenant/{tenant_id}/ts/timelines/{timeline_id}/rollup/{rollup_id}
func (s *Server) HandleTSGetRollup(w http.ResponseWriter, r *http.Request) {
	store, tid, rid, ok := s.tsParseRollupID(w, r)
	if !ok {
		return
	}
	def, err := store.GetRollup(tid, rid)
	if err != nil {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrTSRollupNotFound, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, defToResponse(def))
}

// HandleTSDeleteRollup removes a rollup definition and stops its worker.
//
//	DELETE /api/v1/tenant/{tenant_id}/ts/timelines/{timeline_id}/rollup/{rollup_id}
func (s *Server) HandleTSDeleteRollup(w http.ResponseWriter, r *http.Request) {
	store, tid, rid, ok := s.tsParseRollupID(w, r)
	if !ok {
		return
	}
	if err := store.DeleteRollup(tid, rid); err != nil {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrTSRollupNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── parent / tree ────────────────────────────────────────────────────────────

// HandleTSRollupParent returns the rollup definition for which this timeline
// is the destination — i.e. its parent in the rollup tree.
//
//	GET /api/v1/tenant/{tenant_id}/ts/timelines/{timeline_id}/rollup/parent
func (s *Server) HandleTSRollupParent(w http.ResponseWriter, r *http.Request) {
	store, tid, ok := s.tsParseRollupTimeline(w, r)
	if !ok {
		return
	}
	def, found := store.RollupParent(tid)
	if !found {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrTSRollupNotFound,
			fmt.Sprintf("timeline %d has no rollup parent", tid))
		return
	}
	s.writeJSON(w, http.StatusOK, defToResponse(def))
}

// tsRollupTreeResponse is the body for GET /rollup/tree.
type tsRollupTreeResponse struct {
	TID      uint16                  `json:"tid"`
	Def      *tsRollupDefResponse    `json:"def,omitempty"`
	Children []*tsRollupTreeResponse `json:"children,omitempty"`
}

func treeNodeToResponse(n *timeseries.RollupTreeNode) *tsRollupTreeResponse {
	if n == nil {
		return nil
	}
	resp := &tsRollupTreeResponse{TID: uint16(n.TID)}
	if n.Def != nil {
		r := defToResponse(*n.Def)
		resp.Def = &r
	}
	for _, child := range n.Children {
		resp.Children = append(resp.Children, treeNodeToResponse(child))
	}
	return resp
}

// HandleTSRollupTree returns the full rollup tree for the tenant.
//
//	GET /api/v1/tenant/{tenant_id}/ts/rollup/tree
func (s *Server) HandleTSRollupTree(w http.ResponseWriter, r *http.Request) {
	store := s.tsStore(w, r, chi.URLParam(r, "tenant_id"))
	if store == nil {
		return
	}
	tree := store.RollupTree()
	s.writeJSON(w, http.StatusOK, treeNodeToResponse(tree))
}

// ─── run / status ─────────────────────────────────────────────────────────────

// tsRunRollupRequest is the optional body for POST /rollup/{id}/run.
type tsRunRollupRequest struct {
	From    string `json:"from,omitempty"`    // RFC3339
	To      string `json:"to,omitempty"`      // RFC3339
	Cascade bool   `json:"cascade,omitempty"` // if true, run all descendants too
}

// HandleTSRunRollup manually triggers a rollup execution for the given range.
// If cascade is true in the request body, all descendant rollup definitions
// are also run for the corresponding time windows, in source→destination order.
// Workers are started for this definition and all cascaded descendants.
//
//	POST /api/v1/tenant/{tenant_id}/ts/timelines/{timeline_id}/rollup/{rollup_id}/run
func (s *Server) HandleTSRunRollup(w http.ResponseWriter, r *http.Request) {
	store, tid, rid, ok := s.tsParseRollupID(w, r)
	if !ok {
		return
	}

	var from, to time.Time
	var req tsRunRollupRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSInvalidAggFunc, "invalid request body")
			return
		}
		if req.From != "" {
			var err error
			from, err = time.Parse(time.RFC3339, req.From)
			if err != nil {
				s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSInvalidTimestamp,
					fmt.Sprintf("invalid from: %v", err))
				return
			}
		}
		if req.To != "" {
			var err error
			to, err = time.Parse(time.RFC3339, req.To)
			if err != nil {
				s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSInvalidTimestamp,
					fmt.Sprintf("invalid to: %v", err))
				return
			}
		}
	}

	if err := store.RunRollup(r.Context(), tid, rid, from, to, req.Cascade); err != nil {
		code := xoluerr.ErrTSInternal
		status := http.StatusInternalServerError
		if errContains(err, string(xoluerr.ErrTSRollupNotFound)) {
			code = xoluerr.ErrTSRollupNotFound
			status = http.StatusNotFound
		}
		s.writeError(w, status, code, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// HandleTSRollupStatus returns the operational status of a rollup worker.
//
//	GET /api/v1/tenant/{tenant_id}/ts/timelines/{timeline_id}/rollup/{rollup_id}/status
func (s *Server) HandleTSRollupStatus(w http.ResponseWriter, r *http.Request) {
	store, tid, rid, ok := s.tsParseRollupID(w, r)
	if !ok {
		return
	}
	status, err := store.RollupStatus(tid, rid)
	if err != nil {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrTSRollupNotFound, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, status)
}

// ─── data deletion ────────────────────────────────────────────────────────────

// HandleTSDeleteTimelineData removes all events from a timeline.
//
//	DELETE /api/v1/tenant/{tenant_id}/ts/timelines/{timeline_id}/data
func (s *Server) HandleTSDeleteTimelineData(w http.ResponseWriter, r *http.Request) {
	store, tid, ok := s.tsParseRollupTimeline(w, r) // reuses root-guard
	if !ok {
		return
	}
	if err := store.DeleteTimelineData(r.Context(), tid); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrTSInternal, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleTSDeleteTimeline removes a timeline definition together with its event
// data and rollups (the inverse of define; distinct from DeleteTimelineData,
// which keeps the definition). When rollup cascade is disabled and the timeline
// still has rollups, the store returns ErrTSRollupDestInUse and this responds
// 409 so the caller knows to remove the rollups first; an unknown timeline is
// 404.
//
//	DELETE /api/v1/tenant/{tenant_id}/ts/tl/{timeline_id}
func (s *Server) HandleTSDeleteTimeline(w http.ResponseWriter, r *http.Request) {
	store, tid, ok := s.tsParseRollupTimeline(w, r) // reuses root-guard
	if !ok {
		return
	}
	if err := store.DeleteTimeline(r.Context(), tid); err != nil {
		status := http.StatusInternalServerError
		code := xoluerr.ErrTSInternal
		switch {
		case errContains(err, string(xoluerr.ErrTSRollupDestInUse)):
			// Timeline still has rollups and cascade is off — caller must
			// remove them first.
			status = http.StatusConflict
			code = xoluerr.ErrTSRollupDestInUse
		case errContains(err, string(xoluerr.ErrTSRootTimeline)):
			status = http.StatusBadRequest
			code = xoluerr.ErrTSRootTimeline
		case errContains(err, "XOLU-TS004"):
			status = http.StatusNotFound
			code = xoluerr.Code("XOLU-TS004")
		}
		s.writeError(w, status, code, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// tsPurgeTimelineRequest is the body for POST /data/purge.
type tsPurgeTimelineRequest struct {
	From string `json:"from"` // RFC3339
	To   string `json:"to"`   // RFC3339
}

// HandleTSPurgeTimelineRange removes events in a time range from a timeline.
//
//	POST /api/v1/tenant/{tenant_id}/ts/timelines/{timeline_id}/data/purge
func (s *Server) HandleTSPurgeTimelineRange(w http.ResponseWriter, r *http.Request) {
	store, tid, ok := s.tsParseRollupTimeline(w, r)
	if !ok {
		return
	}
	var req tsPurgeTimelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSInvalidAggFunc, "invalid request body")
		return
	}
	from, err := time.Parse(time.RFC3339, req.From)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSInvalidTimestamp,
			fmt.Sprintf("invalid from: %v", err))
		return
	}
	to, err := time.Parse(time.RFC3339, req.To)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSInvalidTimestamp,
			fmt.Sprintf("invalid to: %v", err))
		return
	}
	if !to.After(from) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrTSInvalidAggFunc,
			"to must be after from")
		return
	}
	if err := store.PurgeTimelineRange(r.Context(), tid, from, to); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrTSInternal, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── internal helpers ─────────────────────────────────────────────────────────

func errContains(err error, sub string) bool {
	return err != nil && strings.Contains(err.Error(), sub)
}
