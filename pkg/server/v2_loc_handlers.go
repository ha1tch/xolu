// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_loc_handlers.go — the spatial primitive's HTTP surface
// (loc-01-rest-api.md, Stage 6, T-118, wave 9). No enable flag: per
// Stage 0's own pinned decision, nothing names a reason for loc to be
// independently optional the way cal/bal are — routes register
// unconditionally, mirroring dxp's own unconditional wiring.
//
// Standalone fence identity (T-127, wave 9b): a fence's subject
// composes onto an entity, addressed via the "kind:key" shorthand
// (mirroring locSubjectJSON.canonical()'s own convention) or a
// REF-shaped object — both invariant, see parseFenceSubject. Format
// validated via pkg/storage's ParseMetaSubject, the same
// engine-inert validator /meta's own handlers use — never a live
// existence check against an entity row, since nothing in this
// codebase's meta-subject addressing does that. Self-anchored fences
// (geometry.center.self=true) are still rejected outright — they
// depend on /obj, wave 10, not built yet.

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/loc"
	"github.com/ha1tch/xolu/pkg/storage"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// ─── Store resolution (Stage 5) ──────────────────────────────────────

func (s *Server) locStore(r *http.Request) (*loc.Store, error) {
	return s.locStoreForTenant(r.Context(), getTenantIDNumeric(r.Context()))
}

// LocStoreForTest exposes locStore's own tenantID-keyed core for
// HTTP-level and dxp-integration tests — the same ForTest pattern as
// BlobStoreForTest/CalManagerForTest.
func (s *Server) LocStoreForTest(ctx context.Context, tenantID tenant.TenantID) (*loc.Store, error) {
	return s.locStoreForTenant(ctx, tenantID)
}

// TenantIDForTest resolves a tenant name (e.g. "default") to its
// numeric tenant.TenantID — the same lookup resolveNumericTenant's
// own registry uses internally, exposed for tests that need to
// construct a node id (tenantID.NodeID) matching exactly what
// server-side code would produce, rather than guessing at the
// tenant-prefix format (T-123's own graph-mirroring proof needed
// this: the legacy /graph/neighbors endpoint is deliberately
// unscoped, "sees all tenant nodes" per its own route comment, so a
// wrong guess here silently queries for a node that was never mirrored
// under that exact key, not a genuine mirror failure).
func (s *Server) TenantIDForTest(name string) (tenant.TenantID, bool) {
	return s.tenantRegistry.Lookup(name)
}

// IsAdaptedForTest reports whether entity has an adapted table on the
// ACTUAL store instance a given tenant's own requests use -- not
// s.storage, which is what naive test code would check first and get a
// wrong answer from for any non-zero tenant. Exists for XOT178's own
// verification: confirms registerAdaptedEverywhere/replayAdaptedSchemas
// genuinely reach the right store, not just that they ran without error.
func (s *Server) IsAdaptedForTest(tenantID tenant.TenantID, entity string) (bool, error) {
	store, err := s.storeForTenant(tenantID)
	if err != nil {
		return false, err
	}
	sqlStore, ok := store.(*storage.SQLiteStore)
	if !ok {
		return false, fmt.Errorf("store for tenant %d is not *storage.SQLiteStore", tenantID)
	}
	return sqlStore.AdaptedRegistry().IsAdapted(entity), nil
}

func (s *Server) locStoreForTenant(ctx context.Context, tenantID tenant.TenantID) (*loc.Store, error) {
	onceI, _ := s.locInit.LoadOrStore(tenantID, &sync.Once{})
	var initErr error
	onceI.(*sync.Once).Do(func() {
		dir := sl.TenantLocDir(s.config.BaseDir, tenantID)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			initErr = err
			return
		}
		db, err := sql.Open("sqlite", dir+"/loc.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
		if err != nil {
			initErr = err
			return
		}
		st := loc.NewStore(db, tenantID)
		if err := st.Init(ctx); err != nil {
			initErr = err
			return
		}
		s.locDB.Store(tenantID, db)
	})
	if initErr != nil {
		return nil, initErr
	}

	dbI, ok := s.locDB.Load(tenantID)
	if !ok {
		return nil, fmt.Errorf("loc store: not initialised for tenant %d", tenantID)
	}
	return loc.NewStore(dbI.(*sql.DB), tenantID), nil
}

// ─── Error mapping ────────────────────────────────────────────────────

func (s *Server) writeLocError(w http.ResponseWriter, err error) {
	var ce *loc.CapacityError
	var ie *loc.InvariantError
	var ule *loc.UnknownLocationError
	var ufe *loc.UnknownFenceError
	var use *loc.UnknownSubjectError
	var rae *loc.RootAnchorError
	var cnp *loc.CapacityOnNonPostableError
	var oe *loc.OccupiedError
	var hce *loc.HasChildrenError
	var sie *loc.SelfIntersectingPolygonError
	var dle *loc.DuplicateLocationError
	var dfe *loc.DuplicateFenceError
	var dpe *loc.DuplicatePatternError
	var upe *loc.UnknownPatternError
	var pce *loc.PatternCapacityConflictError
	var ve *loc.ValidationError
	switch {
	case errors.As(err, &ce):
		code := xoluerr.ErrLocLeafCapacity
		if ce.Kind == "fence" {
			code = xoluerr.ErrLocFenceCapacity
		}
		s.writeError(w, http.StatusConflict, code, err.Error())
	case errors.As(err, &ule):
		s.writeError(w, http.StatusNotFound, xoluerr.ErrLocUnknownLocation, err.Error())
	case errors.As(err, &ufe):
		s.writeError(w, http.StatusNotFound, xoluerr.ErrLocUnknownFence, err.Error())
	case errors.As(err, &use):
		s.writeError(w, http.StatusNotFound, xoluerr.ErrLocUnknownSubject, err.Error())
	case errors.As(err, &rae):
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrLocRootWithoutAnchor, err.Error())
	case errors.As(err, &cnp):
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrLocCapacityOnNonPostable, err.Error())
	case errors.As(err, &oe):
		s.writeError(w, http.StatusConflict, xoluerr.ErrLocDeleteHasAssignedSubject, err.Error())
	case errors.As(err, &hce):
		s.writeError(w, http.StatusConflict, xoluerr.ErrLocDeleteHasChildren, err.Error())
	case errors.As(err, &sie):
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrLocSelfIntersectingPolygon, err.Error())
	case errors.As(err, &dle):
		s.writeError(w, http.StatusConflict, xoluerr.ErrLocDuplicateLocation, err.Error())
	case errors.As(err, &dfe):
		s.writeError(w, http.StatusConflict, xoluerr.ErrLocDuplicateFence, err.Error())
	case errors.As(err, &dpe):
		s.writeError(w, http.StatusConflict, xoluerr.ErrLocDuplicatePattern, err.Error())
	case errors.As(err, &upe):
		s.writeError(w, http.StatusNotFound, xoluerr.ErrLocUnknownPattern, err.Error())
	case errors.As(err, &pce):
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrLocPatternCapacityConflict, err.Error())
	case errors.As(err, &ve):
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
	case errors.As(err, &ie):
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
	default:
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
	}
}

// ─── Wire shapes ──────────────────────────────────────────────────────

// locSubjectJSON is the {"type":"REF","entity":...,"id":...} shape
// loc-01-rest-api.md's own examples use throughout. canonical()
// produces subject_ref exactly as this package's own convention
// elsewhere uses "kind:key"-style compound keys (matching
// leafResource/"loc:leaf:<key>"'s own shape in dxp_adapter.go) — a
// local convention, not exposed on the wire (the wire always carries
// the structured REF, never the compound string).
type locSubjectJSON struct {
	Type   string `json:"type"`
	Entity string `json:"entity"`
	ID     int64  `json:"id"`
}

func (r locSubjectJSON) canonical() string { return fmt.Sprintf("%s:%d", r.Entity, r.ID) }

type locAnchorJSON struct {
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	Alt       float64 `json:"alt"`
	TrueNorth float64 `json:"true_north"`
}

type locPlacementJSON struct {
	OffsetX  float64        `json:"offset_x"`
	OffsetY  float64        `json:"offset_y"`
	OffsetZ  float64        `json:"offset_z"`
	Rotation float64        `json:"rotation"`
	Anchor   *locAnchorJSON `json:"anchor,omitempty"`
}

func placementFromJSON(pj locPlacementJSON) loc.Placement {
	p := loc.Placement{OffsetX: pj.OffsetX, OffsetY: pj.OffsetY, OffsetZ: pj.OffsetZ, Rotation: pj.Rotation}
	if pj.Anchor != nil {
		p.Anchor = &loc.GeoAnchor{Lat: pj.Anchor.Lat, Lon: pj.Anchor.Lon, Alt: pj.Anchor.Alt, TrueNorth: pj.Anchor.TrueNorth}
	}
	return p
}

func placementToJSON(p loc.Placement) locPlacementJSON {
	pj := locPlacementJSON{OffsetX: p.OffsetX, OffsetY: p.OffsetY, OffsetZ: p.OffsetZ, Rotation: p.Rotation}
	if p.Anchor != nil {
		pj.Anchor = &locAnchorJSON{Lat: p.Anchor.Lat, Lon: p.Anchor.Lon, Alt: p.Anchor.Alt, TrueNorth: p.Anchor.TrueNorth}
	}
	return pj
}

type locDefReq struct {
	LocationID string           `json:"location_id"`
	ParentID   *string          `json:"parent_id"`
	Name       string           `json:"name"`
	Postable   *bool            `json:"postable,omitempty"`
	Capacity   *int64           `json:"capacity,omitempty"`
	Pattern    *string          `json:"pattern,omitempty"`
	Placement  locPlacementJSON `json:"placement"`
}

type locResponse struct {
	LocationID     string           `json:"location_id"`
	ParentID       *string          `json:"parent_id"`
	Name           string           `json:"name"`
	Postable       bool             `json:"postable"`
	Capacity       *int64           `json:"capacity,omitempty"`
	Pattern        *string          `json:"pattern,omitempty"`
	PatternID      *string          `json:"pattern_id,omitempty"`
	PatternDeleted *bool            `json:"pattern_deleted,omitempty"`
	Placement      locPlacementJSON `json:"placement"`
	Warnings       []string         `json:"warnings,omitempty"`
}

// locationResponse builds the wire response for one location. parentID
// is resolved by the caller (a Get-by-key lookup for a single record,
// or a local key->id map built once from List's own results for a
// bulk listing) — never a second store round trip per record in the
// list case. Neither this response's own internal LocationKey nor
// any other primitive's uint32 ever appears here: the two-identity
// split (loc-00-design.md §11a) by construction.
func locationResponse(l *loc.Location, parentID *string, capacity *int64, lineage *loc.PatternLineage) locResponse {
	resp := locResponse{
		LocationID: l.ID,
		ParentID:   parentID,
		Name:       l.Name,
		Postable:   l.Postable,
		Capacity:   capacity,
		Placement:  placementToJSON(l.Placement),
	}
	if lineage != nil {
		id := lineage.PatternID
		deleted := lineage.PatternDeleted
		resp.Pattern, resp.PatternID, resp.PatternDeleted = &id, &id, &deleted
	}
	return resp
}

func (s *Server) locCapacityFor(ctx context.Context, st *loc.Store, locationKey uint32) (*int64, error) {
	var ceiling sql.NullInt64
	if err := st.DB().QueryRowContext(ctx,
		`SELECT ceiling FROM loc_capacity WHERE location_key = ?`, locationKey).Scan(&ceiling); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if !ceiling.Valid {
		return nil, nil
	}
	v := ceiling.Int64
	return &v, nil
}

func (s *Server) locParentIDFor(ctx context.Context, st *loc.Store, l *loc.Location) (*string, error) {
	if l.ParentKey == nil {
		return nil, nil
	}
	row := st.DB().QueryRowContext(ctx, `SELECT location_id FROM locations WHERE location_key = ?`, uint32(*l.ParentKey))
	var pid string
	if err := row.Scan(&pid); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("loc invariant violation: parent_key %d referenced but not found", *l.ParentKey)
		}
		return nil, err
	}
	return &pid, nil
}

// ─── Locations ────────────────────────────────────────────────────────

func (s *Server) handleLocDefine(w http.ResponseWriter, r *http.Request) {
	var req locDefReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	def := loc.LocationDef{ID: req.LocationID, ParentID: req.ParentID, Name: req.Name, Postable: true, Placement: placementFromJSON(req.Placement)}
	if req.Postable != nil {
		def.Postable = *req.Postable
	}
	if req.Capacity != nil && req.Pattern != nil {
		s.writeLocError(w, &loc.PatternCapacityConflictError{})
		return
	}
	if req.Capacity != nil && !def.Postable {
		s.writeLocError(w, &loc.CapacityOnNonPostableError{LocationID: req.LocationID})
		return
	}
	if _, err := st.Def(r.Context(), def); err != nil {
		s.writeLocError(w, err)
		return
	}
	if req.Capacity != nil {
		c := req.Capacity
		if err := st.Patch(r.Context(), req.LocationID, loc.PatchParams{Ceiling: &c}); err != nil {
			s.writeLocError(w, err)
			return
		}
	}
	if req.Pattern != nil {
		if _, err := st.ApplyLocationPattern(r.Context(), req.LocationID, *req.Pattern); err != nil {
			s.writeLocError(w, err)
			return
		}
	}
	l, err := st.Get(r.Context(), req.LocationID)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	lineage, err := st.LocationPatternLineage(r.Context(), l.Key)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	capV, err := s.locCapacityFor(r.Context(), st, uint32(l.Key))
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	resp := locationResponse(l, req.ParentID, capV, lineage)
	if warning, err := st.MixedAnchorWarning(r.Context(), l.ParentKey, l.Anchor); err != nil {
		s.writeLocError(w, err)
		return
	} else if warning != "" {
		resp.Warnings = []string{warning}
	}
	s.writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleLocList(w http.ResponseWriter, r *http.Request) {
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	all, err := st.List(r.Context())
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	byKey := make(map[loc.LocationKey]string, len(all))
	for _, l := range all {
		byKey[l.Key] = l.ID
	}
	parentFilter := r.URL.Query().Get("parent_id")

	out := make([]locResponse, 0, len(all))
	for _, l := range all {
		var parentID *string
		if l.ParentKey != nil {
			if pid, ok := byKey[*l.ParentKey]; ok {
				parentID = &pid
			}
		}
		if parentFilter != "" {
			if parentID == nil || *parentID != parentFilter {
				continue
			}
		}
		capV, err := s.locCapacityFor(r.Context(), st, uint32(l.Key))
		if err != nil {
			s.writeLocError(w, err)
			return
		}
		lineage, err := st.LocationPatternLineage(r.Context(), l.Key)
		if err != nil {
			s.writeLocError(w, err)
			return
		}
		out = append(out, locationResponse(l, parentID, capV, lineage))
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"locations": out})
}

func (s *Server) handleLocGet(w http.ResponseWriter, r *http.Request) {
	locationID := pathParam(r, "location_id")
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	l, err := st.Get(r.Context(), locationID)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	parentID, err := s.locParentIDFor(r.Context(), st, l)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	capV, err := s.locCapacityFor(r.Context(), st, uint32(l.Key))
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	lineage, err := st.LocationPatternLineage(r.Context(), l.Key)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, locationResponse(l, parentID, capV, lineage))
}

func (s *Server) handleLocPatch(w http.ResponseWriter, r *http.Request) {
	locationID := pathParam(r, "location_id")
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	var params loc.PatchParams
	if nameRaw, ok := raw["name"]; ok {
		var name string
		if err := json.Unmarshal(nameRaw, &name); err != nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
			return
		}
		params.Name = &name
	}
	if placementRaw, ok := raw["placement"]; ok {
		var pj locPlacementJSON
		if err := json.Unmarshal(placementRaw, &pj); err != nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
			return
		}
		p := placementFromJSON(pj)
		params.Placement = &p
	}
	if capRaw, ok := raw["capacity"]; ok {
		var c *int64
		if err := json.Unmarshal(capRaw, &c); err != nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
			return
		}
		params.Ceiling = &c
	}
	if err := st.Patch(r.Context(), locationID, params); err != nil {
		s.writeLocError(w, err)
		return
	}
	l, err := st.Get(r.Context(), locationID)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	parentID, err := s.locParentIDFor(r.Context(), st, l)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	capV, err := s.locCapacityFor(r.Context(), st, uint32(l.Key))
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	lineage, err := st.LocationPatternLineage(r.Context(), l.Key)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	resp := locationResponse(l, parentID, capV, lineage)
	if params.Placement != nil && params.Placement.Anchor != nil {
		warning, err := st.MixedAnchorWarning(r.Context(), l.ParentKey, l.Anchor)
		if err != nil {
			s.writeLocError(w, err)
			return
		}
		if warning != "" {
			resp.Warnings = []string{warning}
		}
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLocDelete(w http.ResponseWriter, r *http.Request) {
	locationID := pathParam(r, "location_id")
	force := r.URL.Query().Get("force") == "true"
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := st.Delete(r.Context(), locationID, force); err != nil {
		s.writeLocError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Position ─────────────────────────────────────────────────────────

type locMoveReq struct {
	Subject locSubjectJSON `json:"subject"`
	To      string         `json:"to"`
}

func (s *Server) handleLocMove(w http.ResponseWriter, r *http.Request) {
	var req locMoveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	subjectRef := req.Subject.canonical()

	// Compute the tree-aligned delta the SAME way Move's own internal
	// auto-derivation does, before the move commits, so the response
	// can report entered/exited fence_ids (not internal keys) without
	// a second, possibly-inconsistent computation after the fact.
	enteredKeys, exitedKeys, err := st.TreeAlignedFenceDelta(r.Context(), subjectRef, req.To)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	if err := st.Move(r.Context(), loc.MoveParams{SubjectRef: subjectRef, ToLocationID: req.To}); err != nil {
		s.writeLocError(w, err)
		return
	}
	enteredIDs, err := st.FenceIDsFor(r.Context(), enteredKeys)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	exitedIDs, err := st.FenceIDsFor(r.Context(), exitedKeys)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"moved": true,
		"leaf":  req.To,
		"fences": map[string]interface{}{
			"entered": enteredIDs,
			"exited":  exitedIDs,
		},
	})
}

type locPointJSON struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
	Alt float64 `json:"alt,omitempty"`
}

type locReportReq struct {
	Subject    locSubjectJSON `json:"subject"`
	Point      locPointJSON   `json:"point"`
	ReportedAt string         `json:"reported_at,omitempty"`
}

func (s *Server) handleLocReport(w http.ResponseWriter, r *http.Request) {
	var req locReportReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	subjectRef := req.Subject.canonical()

	before, err := st.CurrentFenceKeys(r.Context(), subjectRef)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	if err := st.Report(r.Context(), subjectRef, req.Point.Lat, req.Point.Lon); err != nil {
		s.writeLocError(w, err)
		return
	}
	after, err := st.CurrentFenceKeys(r.Context(), subjectRef)
	if err != nil {
		s.writeLocError(w, err)
		return
	}

	beforeSet := map[loc.FenceKey]bool{}
	for _, fk := range before {
		beforeSet[fk] = true
	}
	afterSet := map[loc.FenceKey]bool{}
	for _, fk := range after {
		afterSet[fk] = true
	}
	changed := len(before) != len(after)
	if !changed {
		for _, fk := range after {
			if !beforeSet[fk] {
				changed = true
				break
			}
		}
	}
	allIDs, err := st.FenceIDsFor(r.Context(), after)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	resp := map[string]interface{}{"changed": changed, "fences": allIDs}
	if changed {
		var enteredKeys, exitedKeys []loc.FenceKey
		for _, fk := range after {
			if !beforeSet[fk] {
				enteredKeys = append(enteredKeys, fk)
			}
		}
		for _, fk := range before {
			if !afterSet[fk] {
				exitedKeys = append(exitedKeys, fk)
			}
		}
		enteredIDs, err := st.FenceIDsFor(r.Context(), enteredKeys)
		if err != nil {
			s.writeLocError(w, err)
			return
		}
		exitedIDs, err := st.FenceIDsFor(r.Context(), exitedKeys)
		if err != nil {
			s.writeLocError(w, err)
			return
		}
		resp["entered"] = enteredIDs
		resp["exited"] = exitedIDs
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLocSubjectPosition(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "invalid subject id")
		return
	}
	subjectRef := (locSubjectJSON{Entity: entity, ID: id}).canonical()
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	pos, err := st.SubjectPosition(r.Context(), subjectRef)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"subject":           locSubjectJSON{Type: "REF", Entity: entity, ID: id},
		"leaf":              pos.Leaf,
		"fences":            pos.Fences,
		"last_report_point": pos.LastReportPoint,
		"as_of":             pos.AsOf,
	})
}

func (s *Server) handleLocSubjectHistory(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "invalid subject id")
		return
	}
	subjectRef := (locSubjectJSON{Entity: entity, ID: id}).canonical()
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	entries, err := st.SubjectHistory(r.Context(), subjectRef, 100)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"entries": entries, "next_cursor": nil})
}

// ─── Fences ───────────────────────────────────────────────────────────

type locCenterJSON struct {
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
	Self bool    `json:"self,omitempty"`
}

type locFenceGeometryJSON struct {
	Type        string         `json:"type"`
	Coordinates [][][2]float64 `json:"coordinates,omitempty"`
	Center      *locCenterJSON `json:"center,omitempty"`
	RadiusM     float64        `json:"radius_m,omitempty"`
}

type locFenceAttachReq struct {
	AlignedTo string               `json:"aligned_to,omitempty"`
	Subject   json.RawMessage      `json:"subject,omitempty"` // "kind:key" shorthand or a REF object -- parseFenceSubject, T-127
	Geometry  locFenceGeometryJSON `json:"geometry"`
	Capacity  *int64               `json:"capacity,omitempty"`
	Pattern   *string              `json:"pattern,omitempty"` // T-131, mutually exclusive with Capacity (XOLU-LOC022)
}

// parseFenceSubject decodes a fences/attach subject field, accepting
// either the compound "kind:key" shorthand (mirroring loc's own
// "entity:id" convention — locSubjectJSON.canonical() produces the
// identical shape for report/move) or a structured REF object
// (locSubjectJSON's own {"type":"REF","entity":...,"id":...} shape).
// Both forms are invariant (T-127): a caller may use whichever is
// convenient, and both resolve identically — proven by
// TestLocAPI_FenceSubject_ShorthandAndREFAreEquivalent. An absent or
// empty subject returns "", "", nil, not an error — a tree-aligned
// fence (aligned_to) needs no subject at all.
func parseFenceSubject(raw json.RawMessage) (kind, key string, err error) {
	if len(raw) == 0 {
		return "", "", nil
	}
	var shorthand string
	if err := json.Unmarshal(raw, &shorthand); err == nil {
		if shorthand == "" {
			return "", "", nil
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

// composedFenceSubjectID validates (kind, key) via the same
// engine-inert, format-only ParseMetaSubject /meta's own handlers use
// (T-127 — see the package doc above for why this is format-only, not
// an existence check) and returns loc's own "kind:key" canonical form
// — never MetaSubject.String()'s "kind/key", since this package uses
// ":" as its compound-key separator everywhere else
// (dxp_adapter.go's "loc:leaf:<key>", locSubjectJSON.canonical()) and
// mixing separators within one package would be its own gap.
func composedFenceSubjectID(kind, key string) (string, error) {
	sub, err := storage.ParseMetaSubject(kind, key, validateEntityName)
	if err != nil {
		return "", &loc.UnknownSubjectError{Detail: err.Error()}
	}
	return sub.Kind + ":" + sub.Key, nil
}

// resolveFenceLookupID composes the fence_id a GET/DELETE path
// addresses. kind == "id" is the established raw-lookup sentinel
// (T-118's own tree-aligned convention — e.g. DELETE
// /loc/fences/id/site%2Fyard; key is used exactly as stored, already
// %2F-decoded by pathParam, no subject semantics at all). Any other
// kind is an entity-composed standalone fence's (kind,key) address,
// validated and canonicalised exactly as attach does (T-127), so the
// same fence resolves identically from both directions.
func resolveFenceLookupID(r *http.Request) (string, error) {
	kind := pathParam(r, "kind")
	key := pathParam(r, "key")
	if kind == "" || kind == "id" {
		return key, nil
	}
	return composedFenceSubjectID(kind, key)
}

// decodeFenceGeometry turns the wire shape into a loc.Geometry, shared
// by attach and PATCH (T-130) so both validate identically — same
// self-intersection/negative-radius checks, same error codes.
func decodeFenceGeometry(g locFenceGeometryJSON) (loc.Geometry, error) {
	switch g.Type {
	case "Polygon":
		p, err := loc.DecodeGeoJSONPolygon(g.Coordinates)
		if err != nil {
			return nil, err
		}
		return p, nil
	case "circle":
		if g.Center == nil {
			return nil, &loc.ValidationError{Detail: "circle geometry requires center"}
		}
		return loc.Circle{CenterLat: g.Center.Lat, CenterLon: g.Center.Lon, RadiusMeters: g.RadiusM}, nil
	default:
		return nil, &loc.ValidationError{Detail: fmt.Sprintf("unknown geometry type %q", g.Type)}
	}
}

// fenceGeometryWarnings computes loc-01-rest-api.md §2's own warnings
// array (T-132): never a hard refusal, informative only. Circle has
// no degenerate case of its own here — a zero-radius circle is a
// legal single point and this item's filed scope is specifically
// "a fence with near-zero polygon area" (T-132's own exit criteria),
// not a Circle check nobody asked for.
func fenceGeometryWarnings(geom loc.Geometry) []string {
	if p, ok := geom.(loc.Polygon); ok && p.IsDegenerate() {
		return []string{"polygon geometry is degenerate (near-zero area or fewer than three effective vertices) -- legal, but this fence can likely never be entered"}
	}
	return nil
}

func (s *Server) handleLocFenceAttach(w http.ResponseWriter, r *http.Request) {
	var req locFenceAttachReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	if req.Geometry.Center != nil && req.Geometry.Center.Self {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON,
			"self-anchored fences (center.self=true) are not supported in v1 -- depends on /obj, wave 10, not built yet")
		return
	}
	kind, key, err := parseFenceSubject(req.Subject)
	if err != nil {
		s.writeLocError(w, &loc.UnknownSubjectError{Detail: err.Error()})
		return
	}
	var fenceID string
	if kind != "" {
		fenceID, err = composedFenceSubjectID(kind, key)
		if err != nil {
			s.writeLocError(w, err)
			return
		}
	}
	if req.AlignedTo != "" && fenceID == "" {
		fenceID = req.AlignedTo // tree-aligned fence: identity is the location itself, v1
	}
	if fenceID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "subject or aligned_to is required")
		return
	}

	if req.Capacity != nil && req.Pattern != nil {
		s.writeLocError(w, &loc.PatternCapacityConflictError{})
		return
	}

	geom, err := decodeFenceGeometry(req.Geometry)
	if err != nil {
		s.writeLocError(w, err)
		return
	}

	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	var alignedTo *string
	if req.AlignedTo != "" {
		alignedTo = &req.AlignedTo
	}
	fk, err := st.DefFence(r.Context(), fenceID, alignedTo)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	if err := st.SetFenceGeometry(r.Context(), fenceID, geom); err != nil {
		s.writeLocError(w, err)
		return
	}
	var capacityOut *int64
	if req.Capacity != nil {
		if _, err := st.DB().ExecContext(r.Context(), `UPDATE loc_fence_capacity SET ceiling = ? WHERE fence_key = ?`, *req.Capacity, uint32(fk)); err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		capacityOut = req.Capacity
	}
	if req.Pattern != nil {
		c, err := st.ApplyFencePattern(r.Context(), fenceID, *req.Pattern)
		if err != nil {
			s.writeLocError(w, err)
			return
		}
		capacityOut = &c
	}
	lineage, err := st.FencePatternLineage(r.Context(), fk)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	resp := map[string]interface{}{
		"fence_id":   fenceID,
		"aligned_to": req.AlignedTo,
		"geometry":   req.Geometry,
		"capacity":   capacityOut,
	}
	if lineage != nil {
		resp["pattern"] = lineage.PatternID
		resp["pattern_id"] = lineage.PatternID
		resp["pattern_deleted"] = lineage.PatternDeleted
	}
	if warnings := fenceGeometryWarnings(geom); len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	s.writeJSON(w, http.StatusCreated, resp)
}

// ─── Fence geometry updates and reconciliation (T-130, wave 9b) ──────

type locFencePatchReq struct {
	Geometry locFenceGeometryJSON `json:"geometry"`
}

// handleLocFencePatch: loc-00-design.md §5c, wire spec loc-01-rest-api.md
// §2b. Same geometry validation as attach (self-intersection rejected,
// XOLU-LOC020) via the shared decodeFenceGeometry helper. Never touches
// loc_fence_capacity.count or loc_fence_membership — only the fence's
// own stored geometry changes; both of those stay exactly as §5c
// requires, correct until the next report/move naturally revisits
// them, or until GET .../reconcile is asked to look.
func (s *Server) handleLocFencePatch(w http.ResponseWriter, r *http.Request) {
	var req locFencePatchReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	fenceID, err := resolveFenceLookupID(r)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	geom, err := decodeFenceGeometry(req.Geometry)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := st.SetFenceGeometry(r.Context(), fenceID, geom); err != nil {
		s.writeLocError(w, err)
		return
	}
	resp := map[string]interface{}{
		"fence_id": fenceID,
		"geometry": req.Geometry,
	}
	if warnings := fenceGeometryWarnings(geom); len(warnings) > 0 {
		resp["warnings"] = warnings
	}
	s.writeJSON(w, http.StatusOK, resp)
}

// handleLocFenceReconcile: loc-01-rest-api.md §2b's advisory drift
// view. Read-only, never a write of any kind.
func (s *Server) handleLocFenceReconcile(w http.ResponseWriter, r *http.Request) {
	fenceID, err := resolveFenceLookupID(r)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	res, err := st.ReconcileFence(r.Context(), fenceID)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	drift := make([]map[string]interface{}, 0, len(res.Drift))
	for _, d := range res.Drift {
		drift = append(drift, map[string]interface{}{
			"subject_ref": d.SubjectRef,
			"recorded":    d.Recorded,
			"observed":    d.Observed,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"fence_id":       fenceID,
		"recorded_count": res.RecordedCount,
		"observed_count": res.ObservedCount,
		"drift":          drift,
	})
}

func (s *Server) handleLocFenceList(w http.ResponseWriter, r *http.Request) {
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	rows, err := st.DB().QueryContext(r.Context(), `SELECT fence_id FROM fences ORDER BY fence_key`)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		out = append(out, id)
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"fences": out})
}

func (s *Server) handleLocFenceGet(w http.ResponseWriter, r *http.Request) {
	fenceID, err := resolveFenceLookupID(r)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	var fenceKey int64
	if err := st.DB().QueryRowContext(r.Context(), `SELECT fence_key FROM fences WHERE fence_id = ?`, fenceID).Scan(&fenceKey); err != nil {
		if err == sql.ErrNoRows {
			s.writeLocError(w, &loc.UnknownFenceError{FenceID: fenceID})
			return
		}
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	lineage, err := st.FencePatternLineage(r.Context(), loc.FenceKey(uint32(fenceKey)))
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	resp := map[string]interface{}{"fence_id": fenceID}
	if lineage != nil {
		resp["pattern"] = lineage.PatternID
		resp["pattern_id"] = lineage.PatternID
		resp["pattern_deleted"] = lineage.PatternDeleted
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLocFenceDelete(w http.ResponseWriter, r *http.Request) {
	fenceID, err := resolveFenceLookupID(r)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	res, err := st.DB().ExecContext(r.Context(), `DELETE FROM fences WHERE fence_id = ?`, fenceID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		s.writeLocError(w, &loc.UnknownFenceError{FenceID: fenceID})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Containment reads ────────────────────────────────────────────────

func (s *Server) handleLocContains(w http.ResponseWriter, r *http.Request) {
	lat, lon, ok := parseLatLon(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "lat and lon query parameters are required")
		return
	}
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	member, err := st.ResolveFenceMembership(r.Context(), lat, lon)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	ids, err := st.FenceIDsFor(r.Context(), member)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"fences": ids})
}

func (s *Server) handleLocNearby(w http.ResponseWriter, r *http.Request) {
	lat, lon, ok := parseLatLon(r)
	if !ok {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "lat and lon query parameters are required")
		return
	}
	radiusStr := r.URL.Query().Get("radius_m")
	radius, err := strconv.ParseFloat(radiusStr, 64)
	if err != nil || radius <= 0 {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "radius_m must be a positive number")
		return
	}
	st, err := s.locStore(r)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	locs, fences, err := st.Nearby(r.Context(), lat, lon, radius)
	if err != nil {
		s.writeLocError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"locations": locs, "fences": fences})
}

func parseLatLon(r *http.Request) (lat, lon float64, ok bool) {
	latStr := r.URL.Query().Get("lat")
	lonStr := r.URL.Query().Get("lon")
	var err error
	lat, err = strconv.ParseFloat(latStr, 64)
	if err != nil {
		return 0, 0, false
	}
	lon, err = strconv.ParseFloat(lonStr, 64)
	if err != nil {
		return 0, 0, false
	}
	return lat, lon, true
}

// pathParam extracts and URL-decodes a chi path parameter. Needed for
// location_id specifically: loc-01-rest-api.md's own examples are
// path-structured ("site-mvd/bldg-a/floor-3/room-204") by convention,
// so a location_id containing "/" arrives %2F-encoded in the URL, and
// chi's own URLParam returns that raw escaped form for a wildcard
// segment rather than decoding it — a real handler bug caught
// directly by the HTTP-level tests exercising a slash-containing id,
// not assumed safe from chi's own routing behaviour.
func pathParam(r *http.Request, name string) string {
	raw := chi.URLParam(r, name)
	if decoded, err := url.PathUnescape(raw); err == nil {
		return decoded
	}
	return raw
}

// ─── Routes ───────────────────────────────────────────────────────────

func (s *Server) setupV2LocRoutes(r chi.Router) {
	r.Post("/loc/def", s.handleLocDefine)
	r.Get("/loc/list", s.handleLocList)
	r.Get("/loc/{location_id}", s.handleLocGet)
	r.Patch("/loc/{location_id}", s.handleLocPatch)
	r.Delete("/loc/{location_id}", s.handleLocDelete)

	s.setupV2LocPatternRoutes(r)

	r.Post("/loc/fences/attach", s.handleLocFenceAttach)
	r.Get("/loc/fences/list", s.handleLocFenceList)
	r.Get("/loc/fences/{kind}/{key}", s.handleLocFenceGet)
	r.Delete("/loc/fences/{kind}/{key}", s.handleLocFenceDelete)
	r.Patch("/loc/fences/{kind}/{key}", s.handleLocFencePatch)
	r.Get("/loc/fences/{kind}/{key}/reconcile", s.handleLocFenceReconcile)

	r.Post("/loc/report", s.handleLocReport)
	r.Post("/loc/move", s.handleLocMove)
	r.Get("/loc/subjects/{entity}/{id}/position", s.handleLocSubjectPosition)
	r.Get("/loc/subjects/{entity}/{id}/history", s.handleLocSubjectHistory)

	r.Get("/loc/contains", s.handleLocContains)
	r.Get("/loc/nearby", s.handleLocNearby)
}
