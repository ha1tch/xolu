// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_obj_handlers.go — T-119 (wave 10), Stage 0-1: obj-01-rest-api.md
// §1 (attach/detach), §2 (move/report/position), restricted to the
// two non-containment target kinds. No enable flag, mirroring loc's
// own Stage 0 decision — nothing names a reason for obj to be
// independently optional.

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/obj"
	"github.com/ha1tch/xolu/pkg/storage"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// ─── Store resolution ──────────────────────────────────────────────

func (s *Server) objStore(r *http.Request) (*obj.Store, error) {
	return s.objStoreForTenant(r.Context(), getTenantIDNumeric(r.Context()))
}

// ObjStoreForTest mirrors LocStoreForTest's own purpose exactly.
func (s *Server) ObjStoreForTest(ctx context.Context, tenantID tenant.TenantID) (*obj.Store, error) {
	return s.objStoreForTenant(ctx, tenantID)
}

func (s *Server) objStoreForTenant(ctx context.Context, tenantID tenant.TenantID) (*obj.Store, error) {
	onceI, _ := s.objInit.LoadOrStore(tenantID, &sync.Once{})
	var initErr error
	onceI.(*sync.Once).Do(func() {
		dir := sl.TenantObjDir(s.config.BaseDir, tenantID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			initErr = err
			return
		}
		db, err := sql.Open("sqlite", dir+"/obj.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
		if err != nil {
			initErr = err
			return
		}
		st := obj.NewStore(db, tenantID)
		if err := st.Init(ctx); err != nil {
			initErr = err
			return
		}
		s.objDB.Store(tenantID, db)
	})
	if initErr != nil {
		return nil, initErr
	}
	dbI, ok := s.objDB.Load(tenantID)
	if !ok {
		return nil, fmt.Errorf("obj store: not initialised for tenant %d", tenantID)
	}
	st := obj.NewStore(dbI.(*sql.DB), tenantID)
	// T-123: only call SetGraph when s.graph is genuinely non-nil.
	// s.graph is a *graph.FlatGraph; passing it unconditionally into
	// SetGraph's own objGraph interface parameter would wrap a nil
	// *graph.FlatGraph in a non-nil interface value (the classic Go
	// footgun), defeating Store's own "if s.graph == nil" check
	// entirely -- graph-disabled tenants would silently attempt real
	// method calls against a nil receiver instead of skipping the
	// mirror cleanly.
	if s.graph != nil {
		st.SetGraph(s.graph)
	}
	return st, nil
}

// ─── Error mapping ────────────────────────────────────────────────────

func (s *Server) writeObjError(w http.ResponseWriter, err error) {
	var use *obj.UnknownSubjectError
	var nae *obj.NotAttachedError
	var aae *obj.AlreadyAttachedError
	var dre *obj.DetachRefusedError
	var ce *obj.CapacityError
	var cyc *obj.ContainmentCycleError
	var cna *obj.ContainerNotAttachedError
	var rre *obj.RetireRefusedError
	var are *obj.AlreadyRetiredError
	var cie *obj.CapacityInvalidError
	var ve *obj.ValidationError
	switch {
	case errors.As(err, &use):
		s.writeError(w, http.StatusNotFound, xoluerr.ErrObjUnknownSubject, err.Error())
	case errors.As(err, &nae):
		s.writeError(w, http.StatusNotFound, xoluerr.ErrObjUnknownSubject, err.Error())
	case errors.As(err, &aae):
		s.writeError(w, http.StatusConflict, xoluerr.ErrObjAlreadyAttached, err.Error())
	case errors.As(err, &dre):
		s.writeError(w, http.StatusConflict, xoluerr.ErrObjDetachRefused, err.Error())
	case errors.As(err, &ce):
		s.writeError(w, http.StatusConflict, xoluerr.ErrObjCapacity, err.Error())
	case errors.As(err, &cyc):
		s.writeError(w, http.StatusConflict, xoluerr.ErrObjCycle, err.Error())
	case errors.As(err, &cna):
		s.writeError(w, http.StatusConflict, xoluerr.ErrObjContainerNotAttached, err.Error())
	case errors.As(err, &rre):
		s.writeError(w, http.StatusConflict, xoluerr.ErrObjRetireRefused, err.Error())
	case errors.As(err, &are):
		s.writeError(w, http.StatusConflict, xoluerr.ErrObjRetireRefused, err.Error())
	case errors.As(err, &cie):
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrObjCapacityInvalid, err.Error())
	case errors.As(err, &ve):
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
	default:
		// /loc's own typed errors pass through Move/Report unwrapped
		// (position.go's own doc comment) -- reuse loc's own mapper
		// rather than re-deriving the same switch here.
		s.writeLocError(w, err)
	}
}

// ─── Subject addressing (mirrors T-127's own parseFenceSubject/
// composedFenceSubjectID exactly — obj's subject is always required,
// so no "empty means fallback" case) ─────────────────────────────────

// parseObjSubject decodes an attach/report/move request's "subject"
// field, accepting the identical two forms T-127 established for loc:
// the "kind:key" shorthand or a REF-shaped object.
func parseObjSubject(raw json.RawMessage) (kind, key string, err error) {
	if len(raw) == 0 {
		return "", "", fmt.Errorf("subject is required")
	}
	var shorthand string
	if err := json.Unmarshal(raw, &shorthand); err == nil {
		if shorthand == "" {
			return "", "", fmt.Errorf("subject is required")
		}
		k, v, ok := strings.Cut(shorthand, ":")
		if !ok {
			return "", "", fmt.Errorf("subject shorthand must be \"kind:key\": %q", shorthand)
		}
		return k, v, nil
	}
	var ref locSubjectJSON
	if err := json.Unmarshal(raw, &ref); err != nil {
		return "", "", fmt.Errorf("subject must be a \"kind:key\" string or a {type:REF,...} object: %w", err)
	}
	if ref.Type != "" && ref.Type != "REF" {
		return "", "", fmt.Errorf("subject.type must be \"REF\", got %q", ref.Type)
	}
	if ref.Entity == "" {
		return "", "", fmt.Errorf("subject.entity is required for a REF-shaped subject")
	}
	return ref.Entity, strconv.FormatInt(ref.ID, 10), nil
}

// composedObjSubjectRef mirrors composedFenceSubjectID exactly — same
// engine-inert, format-only ParseMetaSubject validation, T-119's own
// package doc names why this is the consistent choice, not a
// live existence check.
func composedObjSubjectRef(kind, key string) (string, error) {
	sub, err := storage.ParseMetaSubject(kind, key, validateEntityName)
	if err != nil {
		return "", &obj.UnknownSubjectError{Detail: err.Error()}
	}
	return sub.Kind + ":" + sub.Key, nil
}

// resolveObjSubjectRef composes the subject_ref a GET/DELETE
// {kind}/{key} path addresses — always entity-composed for obj
// (unlike loc's fences, there is no "id" raw-lookup sentinel here:
// obj has no tree-aligned-equivalent identity shortcut).
func resolveObjSubjectRef(r *http.Request) (string, error) {
	kind := pathParam(r, "kind")
	key := pathParam(r, "key")
	return composedObjSubjectRef(kind, key)
}

// ─── Wire shapes ──────────────────────────────────────────────────────

type objCapacityJSON struct {
	MaxWeightKg *float64 `json:"max_weight_kg,omitempty"`
	MaxVolumeM3 *float64 `json:"max_volume_m3,omitempty"`
	MaxCount    *int64   `json:"max_count,omitempty"`
}

func capacityFromJSON(c objCapacityJSON) obj.Capacity {
	return obj.Capacity{MaxWeightKg: c.MaxWeightKg, MaxVolumeM3: c.MaxVolumeM3, MaxCount: c.MaxCount}
}

func capacityToJSON(c obj.Capacity) objCapacityJSON {
	return objCapacityJSON{MaxWeightKg: c.MaxWeightKg, MaxVolumeM3: c.MaxVolumeM3, MaxCount: c.MaxCount}
}

type objAttachReq struct {
	Subject  json.RawMessage `json:"subject"`
	Capacity objCapacityJSON `json:"capacity,omitempty"`
}

type objSubjectResponse struct {
	Subject      string          `json:"subject"`
	Capacity     objCapacityJSON `json:"capacity,omitempty"`
	PositionKind string          `json:"position_kind"`
	LocLeafID    *string         `json:"loc_leaf_id,omitempty"`
}

func subjectResponse(sub *obj.Subject) objSubjectResponse {
	resp := objSubjectResponse{
		Subject:      sub.Ref,
		Capacity:     capacityToJSON(sub.Capacity),
		PositionKind: string(sub.Position.Kind),
	}
	if sub.Position.Kind == obj.PositionKindLocLeaf {
		id := sub.Position.LocLeafID
		resp.LocLeafID = &id
	}
	return resp
}

// ─── Attach / Get / Detach (obj-01-rest-api.md §1) ─────────────────────

func (s *Server) handleObjAttach(w http.ResponseWriter, r *http.Request) {
	var req objAttachReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	kind, key, err := parseObjSubject(req.Subject)
	if err != nil {
		s.writeObjError(w, &obj.UnknownSubjectError{Detail: err.Error()})
		return
	}
	subjectRef, err := composedObjSubjectRef(kind, key)
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	st, err := s.objStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := st.Attach(r.Context(), subjectRef, capacityFromJSON(req.Capacity)); err != nil {
		s.writeObjError(w, err)
		return
	}
	sub, err := st.Get(r.Context(), subjectRef)
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, subjectResponse(sub))
}

func (s *Server) handleObjGet(w http.ResponseWriter, r *http.Request) {
	subjectRef, err := resolveObjSubjectRef(r)
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	st, err := s.objStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	sub, err := st.Get(r.Context(), subjectRef)
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, subjectResponse(sub))
}

func (s *Server) handleObjDetach(w http.ResponseWriter, r *http.Request) {
	subjectRef, err := resolveObjSubjectRef(r)
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	st, err := s.objStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := st.Detach(r.Context(), subjectRef); err != nil {
		s.writeObjError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Position: move / report / position (obj-01-rest-api.md §2) ───────

type objMoveTargetJSON struct {
	Kind       string `json:"kind"`
	LocationID string `json:"location_id,omitempty"`
	Subject    string `json:"subject,omitempty"` // "kind:key" shorthand, required when Kind == "obj" (T-120)
}

type objMoveReq struct {
	To *objMoveTargetJSON `json:"to"`
}

func (s *Server) handleObjMove(w http.ResponseWriter, r *http.Request) {
	subjectRef, err := resolveObjSubjectRef(r)
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	var req objMoveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	var target obj.MoveTarget
	if req.To == nil {
		target = obj.MoveTarget{Kind: obj.PositionKindUnassigned}
	} else {
		switch req.To.Kind {
		case "loc_leaf":
			target = obj.MoveTarget{Kind: obj.PositionKindLocLeaf, LocLeafID: req.To.LocationID}
		case "obj":
			if req.To.Subject == "" {
				s.writeObjError(w, &obj.ValidationError{Detail: "move target kind \"obj\" requires subject"})
				return
			}
			k, key, found := strings.Cut(req.To.Subject, ":")
			if !found {
				s.writeObjError(w, &obj.ValidationError{Detail: fmt.Sprintf("move target subject must be \"kind:key\": %q", req.To.Subject)})
				return
			}
			containerRef, err := composedObjSubjectRef(k, key)
			if err != nil {
				s.writeObjError(w, err)
				return
			}
			target = obj.MoveTarget{Kind: obj.PositionKindObj, ContainerRef: containerRef}
		default:
			s.writeObjError(w, &obj.ValidationError{Detail: fmt.Sprintf("unknown move target kind %q", req.To.Kind)})
			return
		}
	}
	objSt, err := s.objStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	locSt, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := objSt.Move(r.Context(), subjectRef, target, locSt); err != nil {
		s.writeObjError(w, err)
		return
	}
	sub, err := objSt.Get(r.Context(), subjectRef)
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	s.fireObjEvent(r, "obj.move", subjectRef, objPositionEventData(sub))
	s.writeJSON(w, http.StatusOK, subjectResponse(sub))
}

type objReportReq struct {
	Point locPointJSON `json:"point"`
}

func (s *Server) handleObjReport(w http.ResponseWriter, r *http.Request) {
	subjectRef, err := resolveObjSubjectRef(r)
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	var req objReportReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	objSt, err := s.objStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	locSt, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := objSt.Report(r.Context(), subjectRef, req.Point.Lat, req.Point.Lon, locSt); err != nil {
		s.writeObjError(w, err)
		return
	}
	s.fireObjEvent(r, "obj.report", subjectRef, map[string]interface{}{
		"lat": req.Point.Lat, "lon": req.Point.Lon,
	})
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleObjPosition(w http.ResponseWriter, r *http.Request) {
	subjectRef, err := resolveObjSubjectRef(r)
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	st, err := s.objStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	resolved, err := st.ResolvePosition(r.Context(), subjectRef)
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	resp := map[string]interface{}{
		"resolved": map[string]interface{}{"kind": string(resolved.Kind)},
		"chain":    resolved.Chain,
		"as_of":    "live",
	}
	if resolved.Kind == obj.PositionKindLocLeaf {
		resp["resolved"].(map[string]interface{})["location_id"] = resolved.LocLeafID
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// ─── Retire, capacity, contents (obj-01-rest-api.md §3/§4/§6, T-124) ──

func (s *Server) handleObjRetire(w http.ResponseWriter, r *http.Request) {
	subjectRef, err := resolveObjSubjectRef(r)
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	st, err := s.objStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := st.Retire(r.Context(), subjectRef); err != nil {
		s.writeObjError(w, err)
		return
	}
	s.fireObjEvent(r, "obj.retire", subjectRef, nil)
	w.WriteHeader(http.StatusOK)
}

type objCapacityPatchReq struct {
	MaxWeightKg *float64 `json:"max_weight_kg"`
	MaxVolumeM3 *float64 `json:"max_volume_m3"`
	MaxCount    *int64   `json:"max_count"`
}

func (s *Server) handleObjCapacityPatch(w http.ResponseWriter, r *http.Request) {
	subjectRef, err := resolveObjSubjectRef(r)
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	var req objCapacityPatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	st, err := s.objStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	capacity := obj.Capacity{MaxWeightKg: req.MaxWeightKg, MaxVolumeM3: req.MaxVolumeM3, MaxCount: req.MaxCount}
	if err := st.SetCapacity(r.Context(), subjectRef, capacity); err != nil {
		s.writeObjError(w, err)
		return
	}
	sub, err := st.Get(r.Context(), subjectRef)
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, subjectResponse(sub))
}

func (s *Server) handleObjContents(w http.ResponseWriter, r *http.Request) {
	subjectRef, err := resolveObjSubjectRef(r)
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	st, err := s.objStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	var contents []string
	if r.URL.Query().Get("depth") == "all" {
		contents, err = st.TransitiveContents(r.Context(), subjectRef)
	} else {
		contents, err = st.DirectContents(r.Context(), subjectRef)
	}
	if err != nil {
		s.writeObjError(w, err)
		return
	}
	if contents == nil {
		contents = []string{}
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"contents": contents})
}

// ─── Routes ───────────────────────────────────────────────────────────

func (s *Server) setupV2ObjRoutes(r chi.Router) {
	r.Post("/obj/attach", s.handleObjAttach)
	r.Get("/obj/{kind}/{key}", s.handleObjGet)
	r.Delete("/obj/{kind}/{key}", s.handleObjDetach)
	r.Put("/obj/{kind}/{key}/move", s.handleObjMove)
	r.Post("/obj/{kind}/{key}/report", s.handleObjReport)
	r.Get("/obj/{kind}/{key}/position", s.handleObjPosition)
	r.Post("/obj/{kind}/{key}/retire", s.handleObjRetire)
	r.Patch("/obj/{kind}/{key}/capacity", s.handleObjCapacityPatch)
	r.Get("/obj/{kind}/{key}/contents", s.handleObjContents)
	r.Post("/obj/promote", s.handleObjPromote)
	r.Post("/obj/demote", s.handleObjDemote)
}
