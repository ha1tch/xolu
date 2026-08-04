// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/models"
	"github.com/ha1tch/xolu/pkg/oql"
	"github.com/ha1tch/xolu/pkg/qs"
	"github.com/ha1tch/xolu/pkg/storage"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/sulpher"
	"github.com/ha1tch/xolu/pkg/refintegrity"
	"github.com/ha1tch/xolu/pkg/tenant"
	"github.com/ha1tch/xolu/pkg/timeseries"
	"github.com/ha1tch/xolu/pkg/version"
)

// handlePatch partially updates an entity
func (s *Server) handlePatch(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")
	idStr := chi.URLParam(r, "id")

	if err := validateEntityName(entity); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidEntity, err.Error())
		return
	}

	id, err := strconv.Atoi(idStr)
	if err != nil || id < 0 {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidID, "Invalid ID")
		return
	}

	var patchData map[string]interface{}
	if !s.decodeJSON(w, r, &patchData) {
		return
	}

	// Track which fields are being updated (for the response)
	updatedFields := []string{}
	for key := range patchData {
		if key != "id" {
			updatedFields = append(updatedFields, key)
		}
	}

	// When PatchNullBehavior is "store", nil values should be stored as
	// JSON null (the default patchInner behaviour deletes nil keys).
	// We signal "store null" by replacing nil with a json.RawMessage("null")
	// which the storage layer will persist as a JSON null value.
	// When PatchNullBehavior is "delete" (default), nil means remove the key,
	// which is what patchInner already does.
	// (No transformation needed for "delete" mode.)

	// Collect fields to delete (nil values in "delete" mode)
	var deleteKeys []string
	if s.config.PatchNullBehavior == "delete" {
		for key, value := range patchData {
			if key != "id" && value == nil {
				deleteKeys = append(deleteKeys, key)
				delete(patchData, key) // don't send nil to the store
			}
		}
	}

	store := s.getStore(r.Context())

	// Validation errors captured by the callback
	var validationErrors []string
	var validationFailed bool

	validate := func(merged map[string]interface{}) error {
		// Apply key deletions inside the transaction
		for _, key := range deleteKeys {
			delete(merged, key)
		}

		valid, errs := s.validator.Validate(entity, merged)
		if !valid {
			validationFailed = true
			validationErrors = errs
			return fmt.Errorf("validation failed")
		}

		// Pre-validate graph edges on the fully merged entity (Bug 1 fix).
		// The validate callback runs inside the transaction, so if this returns
		// an error the entire patch is rolled back — the edge table stays clean.
		if err := s.validateGraphEdges(r.Context(), entity, id, merged); err != nil {
			return err
		}

		return nil
	}

	if err := store.PatchValidated(r.Context(), entity, id, patchData, validate); err != nil {
		var rtm *storage.RefTargetMissingError
		if errors.As(err, &rtm) {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrRIValidateTarget, rtm.Error())
			return
		}
		if validationFailed {
			s.writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"error": map[string]interface{}{
					"code":    string(xoluerr.ErrValidationFailed),
					"message": "Validation failed",
					"status":  http.StatusBadRequest,
				},
				"details": validationErrors,
			})
			return
		}
		if errors.Is(err, graph.ErrCycleDetected) {
			s.writeError(w, http.StatusConflict, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		if errors.Is(err, storage.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, xoluerr.ErrEntityNotFound,
				fmt.Sprintf("Resource of entity %s with id %d not found", entity, id))
			return
		}
		if errors.Is(err, storage.ErrConflict) {
			currentVer := s.fetchCurrentVersion(r.Context(), entity, id)
			s.writeJSON(w, http.StatusConflict, map[string]interface{}{
				"error": map[string]interface{}{
					"code":    string(xoluerr.ErrVersionConflict),
					"message": fmt.Sprintf("Version conflict: %s with id %d has been modified by another request", entity, id),
					"status":  http.StatusConflict,
				},
				"current_version": currentVer,
			})
			return
		}
		if errors.Is(err, models.ErrDuplicateEdgeTarget) {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrDuplicateEdgeRef, err.Error())
			return
		}
		s.logger.Error().Err(err).Msg("Failed to patch entity")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, "Failed to patch entity")
		return
	}

	// Fetch the merged entity for graph update (post-commit, best-effort).
	// On SQLite the edge table is already correct (updated atomically in the
	// transaction); this re-fetch only drives the in-memory FlatGraph.
	// If the Get fails the in-memory graph will be stale until next restart or
	// until a subsequent write to the same entity succeeds.
	if merged, err := store.Get(r.Context(), entity, id); err == nil {
		s.updateGraph(r.Context(), entity, id, merged)
	} else {
		s.logger.Warn().Err(err).Str("entity", entity).Int("id", id).
			Msg("handlePatch: post-commit Get failed; in-memory graph not updated for this entity")
	}

	s.invalidateCacheForID(r.Context(), entity, id)
	s.logger.Info().Str("entity", entity).Int("id", id).Msg("Patched entity")

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":        fmt.Sprintf("%s with id %d patched successfully", entity, id),
		"updated_fields": updatedFields,
	})
}

// handleDelete deletes an entity
func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")
	idStr := chi.URLParam(r, "id")

	if err := validateEntityName(entity); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidEntity, err.Error())
		return
	}

	id, err := strconv.Atoi(idStr)
	if err != nil || id < 0 {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidID, "Invalid ID")
		return
	}

	// Check if entity exists (using tenant-scoped store)
	store := s.getStore(r.Context())
	if !store.Exists(r.Context(), entity, id) {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrEntityNotFound,
			fmt.Sprintf("Resource of entity %s with id %d not found", entity, id))
		return
	}

	// Referential integrity: restrict enforcement (@R02.2, wave 2 stage 2).
	// The in-memory pre-check is a fast path to refusal: a positive result
	// (a committed referrer found) refuses immediately. The authoritative
	// check runs in-transaction inside the store's delete (DeleteWithRestrict,
	// @C04a) — the pre-check cannot close the check-then-act window on its
	// own, but the in-tx check does, because it reads the SQL edge state
	// inside the delete's own transaction. This enforcement requires the
	// graph subsystem; a schema with x-ref policies and the graph disabled
	// is caught loudly by the parity guard in rebuildRIRegistry.
	if refs, blocked := s.restrictReferrers(r.Context(), entity, id); blocked {
		s.writeError(w, http.StatusConflict, xoluerr.ErrRIRestrictViolation,
			fmt.Sprintf("cannot delete %s:%d — it is referenced by: %s",
				entity, id, strings.Join(refs, ", ")))
		return
	}

	// Handle cascading delete
	deletedRefs := []string{fmt.Sprintf("%s:%d", entity, id)}
	if s.config.CascadingDelete {
		refs, err := s.cascadeDelete(r.Context(), entity, id)
		if err != nil {
			s.logger.Error().Err(err).Msg("Cascade delete failed")
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		deletedRefs = refs
	} else {
		// Restrict-aware delete: the authoritative in-transaction referrer
		// check (@C04a). restrictedBy is the set of entities that
		// restrict-reference this one; empty ⇒ a plain delete.
		restrictedBy := s.restrictingEntities(entity)
		var derr error
		if rd, ok := store.(interface {
			DeleteWithRestrict(ctx context.Context, entity string, id int, restrictedBy []string) error
		}); ok && len(restrictedBy) > 0 {
			derr = rd.DeleteWithRestrict(r.Context(), entity, id, restrictedBy)
		} else {
			derr = store.Delete(r.Context(), entity, id)
		}
		if derr != nil {
			var rve *storage.RestrictViolationError
			if errors.As(derr, &rve) {
				s.writeError(w, http.StatusConflict, xoluerr.ErrRIRestrictViolation,
					fmt.Sprintf("cannot delete %s:%d — it is referenced by: %s",
						entity, id, strings.Join(rve.Referrers, ", ")))
				return
			}
			s.logger.Error().Err(derr).Msg("Failed to delete entity")
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, "Failed to delete entity")
			return
		}

		// Update graph
		s.removeGraph(r.Context(), entity, id)
	}

	s.invalidateCacheForID(r.Context(), entity, id)
	s.logger.Info().Str("entity", entity).Int("id", id).Msg("Deleted entity")

	s.fireEntityEvent(r, "entity.deleted", entity, id, nil)

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"message":          fmt.Sprintf("%s with id %d deleted successfully", entity, id),
		"cascaded_deletes": deletedRefs,
	})
}

// fetchCurrentVersion retrieves the _version of an entity for inclusion in
// version-conflict 409 responses. Returns -1 if the entity cannot be read.
func (s *Server) fetchCurrentVersion(ctx context.Context, entity string, id int) int {
	store := s.getStore(ctx)
	data, err := store.Get(ctx, entity, id)
	if err != nil {
		return -1
	}
	switch v := data["_version"].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return -1
}

// handleSave upserts an entity with a caller-specified ID.
// Creates the entity if it does not exist; overwrites it if it does.
// Returns 201 Created on first write, 200 OK on subsequent overwrites.
//
// Optimistic concurrency: include "_version" in the request body to make the
// write conditional. If the stored version does not match, the response is
// 409 Conflict with "current_version" in the body.
func (s *Server) handleSave(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")
	idStr := chi.URLParam(r, "id")

	if err := validateEntityName(entity); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidEntity, err.Error())
		return
	}

	id, err := strconv.Atoi(idStr)
	if err != nil || id < 0 {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidID, "Invalid ID")
		return
	}

	var data map[string]interface{}
	if !s.decodeJSON(w, r, &data) {
		return
	}

	// Validate
	data["id"] = id
	if valid, errors := s.validator.Validate(entity, data); !valid {
		s.writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": map[string]interface{}{
				"code":    string(xoluerr.ErrValidationFailed),
				"message": "Validation failed",
				"status":  http.StatusBadRequest,
			},
			"details": errors,
		})
		return
	}

	// Pre-validate graph edges before the store write.
	if err := s.validateGraphEdges(r.Context(), entity, id, data); err != nil {
		if errors.Is(err, graph.ErrCycleDetected) {
			s.writeError(w, http.StatusConflict, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		if errors.Is(err, models.ErrDuplicateEdgeTarget) {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrDuplicateEdgeRef, err.Error())
			return
		}
	}

	// Upsert
	store := s.getStore(r.Context())
	created, err := store.Save(r.Context(), entity, id, data)
	if err != nil {
		var rtm *storage.RefTargetMissingError
		if errors.As(err, &rtm) {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrRIValidateTarget, rtm.Error())
			return
		}
		if errors.Is(err, storage.ErrConflict) {
			currentVer := s.fetchCurrentVersion(r.Context(), entity, id)
			s.writeJSON(w, http.StatusConflict, map[string]interface{}{
				"error": map[string]interface{}{
					"code":    string(xoluerr.ErrVersionConflict),
					"message": fmt.Sprintf("Version conflict: %s with id %d has been modified", entity, id),
					"status":  http.StatusConflict,
				},
				"current_version": currentVer,
			})
			return
		}
		if errors.Is(err, models.ErrDuplicateEdgeTarget) {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrDuplicateEdgeRef, err.Error())
			return
		}
		s.logger.Error().Err(err).Msg("Failed to save entity")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, "Failed to save entity")
		return
	}

	// Update graph
	s.updateGraph(r.Context(), entity, id, data)

	s.invalidateCacheForID(r.Context(), entity, id)
	s.logger.Info().Str("entity", entity).Int("id", id).Bool("created", created).Msg("Saved entity")

	status := http.StatusOK
	verb := "updated"
	if created {
		status = http.StatusCreated
		verb = "created"
	}
	s.writeJSON(w, status, map[string]interface{}{
		"message": fmt.Sprintf("Resource of entity %s with id %d %s successfully", entity, id, verb),
	})
}

// handleGraphPath finds a path between two nodes
func (s *Server) handleGraphPath(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	var req struct {
		From     string `json:"from"`
		To       string `json:"to"`
		MaxDepth int    `json:"max_depth"`
	}

	if !s.decodeJSON(w, r, &req) {
		return
	}

	if req.From == "" || req.To == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "from and to are required")
		return
	}

	if req.MaxDepth <= 0 {
		req.MaxDepth = s.config.MaxQueryDepth
	}

	path, err := s.graph.FindPath(req.From, req.To, req.MaxDepth)
	if err != nil {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrEntityNotFound, err.Error())
		return
	}

	length := len(path) - 1
	if length < 0 {
		length = 0
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"from":   req.From,
		"to":     req.To,
		"path":   path,
		"length": length,
	})
}

// handleGraphNeighbors gets neighbors of a node
func (s *Server) handleGraphNeighbors(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	var req struct {
		NodeID    string `json:"node_id"`
		Direction string `json:"direction"` // "out", "in", or "both"
	}

	if !s.decodeJSON(w, r, &req) {
		return
	}

	if req.NodeID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "node_id required")
		return
	}

	if req.Direction == "" {
		req.Direction = "out"
	}
	if req.Direction != "out" && req.Direction != "in" && req.Direction != "both" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, `direction must be "out", "in", or "both"`)
		return
	}

	result := make(map[string]interface{})

	if req.Direction == "out" || req.Direction == "both" {
		neighbors, err := s.graph.GetNeighbors(req.NodeID)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		result["outgoing"] = neighbors
	}

	if req.Direction == "in" || req.Direction == "both" {
		incoming, err := s.graph.GetIncomingEdges(req.NodeID)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		result["incoming"] = incoming
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"neighbors": result,
	})
}

// handleGraphStats returns graph statistics
func (s *Server) handleGraphStats(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"node_count": s.graph.NodeCount(),
		"edge_count": s.graph.EdgeCount(),
		"has_cycle":  s.graph.HasCycle(),
	})
}

// handleGraphNodeInfo returns detailed info about a specific node
// GET /api/v1/graph/nodes/{node_id}
func (s *Server) handleGraphNodeInfo(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	nodeID := chi.URLParam(r, "node_id")
	if nodeID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "node_id required")
		return
	}

	info, err := s.graph.GetNodeInfo(nodeID)
	if err != nil {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrEntityNotFound, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, info)
}

// handleGraphNodeDegree returns degree counts for a node
// GET /api/v1/graph/nodes/{node_id}/degree
func (s *Server) handleGraphNodeDegree(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	nodeID := chi.URLParam(r, "node_id")
	if nodeID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "node_id required")
		return
	}

	degree, err := s.graph.GetDegree(nodeID)
	if err != nil {
		// Node absent from in-memory graph — may be an edge-free adapted entity.
		// Fall back to edge table COUNT before returning 404.
		entityParts := strings.SplitN(strings.TrimPrefix(nodeID, tenant.NodeIDPrefix(nodeID)), ":", 2)
		if sqlStore, ok := s.storage.(*storage.SQLiteStore); ok && len(entityParts) == 2 {
			var id int
			if _, scanErr := fmt.Sscanf(entityParts[1], "%d", &id); scanErr == nil {
				if d, found := s.degreeFromStorage(r.Context(), sqlStore, 0, entityParts[0], id); found {
					s.writeJSON(w, http.StatusOK, map[string]interface{}{"node_id": nodeID, "degree": d})
					return
				}
			}
		}
		s.writeError(w, http.StatusNotFound, xoluerr.ErrEntityNotFound, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"node_id": nodeID,
		"degree":  degree,
	})
}

// handleGraphIncoming returns incoming edges to a node
// GET /api/v1/graph/{node_id}/in
func (s *Server) handleGraphIncoming(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	nodeID := chi.URLParam(r, "node_id")
	if nodeID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "node_id required")
		return
	}

	incoming, err := s.graph.GetIncomingEdges(nodeID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	// Format as array of edge objects for rserv compatibility
	edges := make([]map[string]string, 0, len(incoming))
	for source, ref := range incoming {
		edges = append(edges, map[string]string{
			"source":       source,
			"target":       nodeID,
			"relationship": ref.Rel,
		})
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"node_id": nodeID,
		"edges":   edges,
		"count":   len(edges),
	})
}

// handleGraphOutgoing returns outgoing edges from a node
// GET /api/v1/graph/{node_id}/out
func (s *Server) handleGraphOutgoing(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	nodeID := chi.URLParam(r, "node_id")
	if nodeID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "node_id required")
		return
	}

	outgoing, err := s.graph.GetNeighbors(nodeID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	// Format as array of edge objects for rserv compatibility
	edges := make([]map[string]string, 0, len(outgoing))
	for target, ref := range outgoing {
		edges = append(edges, map[string]string{
			"source":       nodeID,
			"target":       target,
			"relationship": ref.Rel,
		})
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"node_id": nodeID,
		"edges":   edges,
		"count":   len(edges),
	})
}

// handleGraphShortestPath finds shortest path between two nodes
// POST /api/v1/graph/shortestPath
func (s *Server) handleGraphShortestPath(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	var req struct {
		From     string `json:"from"`
		To       string `json:"to"`
		MaxDepth int    `json:"max_depth"`
	}

	if !s.decodeJSON(w, r, &req) {
		return
	}

	if req.From == "" || req.To == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "from and to are required")
		return
	}

	if req.MaxDepth <= 0 {
		req.MaxDepth = s.config.MaxQueryDepth
	}

	path, err := s.graph.FindPath(req.From, req.To, req.MaxDepth)
	if err != nil {
		s.writeJSON(w, http.StatusOK, map[string]interface{}{
			"from":   req.From,
			"to":     req.To,
			"exists": false,
			"path":   nil,
			"length": 0,
		})
		return
	}

	length := len(path) - 1
	if length < 0 {
		length = 0
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"from":   req.From,
		"to":     req.To,
		"exists": true,
		"path":   path,
		"length": length,
	})
}

// handleGraphPathExists checks if a path exists between two nodes
// POST /api/v1/graph/pathExists
func (s *Server) handleGraphPathExists(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	var req struct {
		From     string `json:"from"`
		To       string `json:"to"`
		MaxDepth int    `json:"max_depth"`
	}

	if !s.decodeJSON(w, r, &req) {
		return
	}

	if req.From == "" || req.To == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "from and to are required")
		return
	}

	if req.MaxDepth <= 0 {
		req.MaxDepth = s.config.MaxQueryDepth
	}

	exists, length, err := s.graph.PathExists(req.From, req.To, req.MaxDepth)
	if err != nil {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrEntityNotFound, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"from":   req.From,
		"to":     req.To,
		"exists": exists,
		"length": length,
	})
}

// handleGraphCommonNeighbors finds shared out-neighbours of two nodes
// POST /api/v1/graph/commonNeighbors
func (s *Server) handleGraphCommonNeighbors(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	var req struct {
		NodeA string `json:"node_a"`
		NodeB string `json:"node_b"`
	}

	if !s.decodeJSON(w, r, &req) {
		return
	}

	if req.NodeA == "" || req.NodeB == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "node_a and node_b are required")
		return
	}

	common, err := s.graph.SharedOutNeighbors(req.NodeA, req.NodeB)
	if err != nil {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrEntityNotFound, err.Error())
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"node_a": req.NodeA,
		"node_b": req.NodeB,
		"common": common,
		"count":  len(common),
	})
}

// handleGraphNodeSearch searches for nodes by entity type
// POST /api/v1/graph/nodes/search
func (s *Server) handleGraphNodeSearch(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	var req struct {
		Entity string `json:"entity"`
		Limit  int    `json:"limit"`
	}

	if !s.decodeJSON(w, r, &req) {
		return
	}

	var nodes []string
	if req.Entity != "" {
		// Prefer the adapted table when available: it returns every entity of
		// that type, including those with no edges absent from the graph index.
		if sqlStore, ok := s.storage.(*storage.SQLiteStore); ok {
			if ids := s.adaptedEntityIDs(r.Context(), sqlStore, req.Entity, 0); ids != nil {
				nodes = ids
			}
		}
		if nodes == nil {
			nodes = s.graph.GetNodesByType(req.Entity)
		}
	} else {
		nodes = s.graph.GetAllNodes()
	}

	// Apply limit
	if req.Limit > 0 && len(nodes) > req.Limit {
		nodes = nodes[:req.Limit]
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"nodes": nodes,
		"count": len(nodes),
	})
}

// Sulpher Query Handlers

// handleSulpherQuery executes a Sulpher query synchronously
// POST /api/v1/graph/query
func (s *Server) handleSulpherQuery(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}
	if s.sulpherJobs == nil {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrQueryEngineNotInit, "Sulpher query engine not initialized")
		return
	}
	tid := getTenantIDNumeric(r.Context())
	s.executeSulpherQueryBody(w, r, s.sulpherJobs, tid, "sulpher", 0)
}

// executeSulpherQueryBody is the shared implementation of handleSulpherQuery
// and handleTenantSulpherQuery. It decodes the request, checks the result
// cache, executes the BFS, caches and writes the response.
//
// jm is the job manager scoped to the appropriate tenant.
// tid is the numeric tenant ID (0 for the default tenant).
// logType is the "type" label used in the slow-query warning log.
// logTenantID is included in the slow-query log when non-zero.
func (s *Server) executeSulpherQueryBody(
	w http.ResponseWriter, r *http.Request,
	jm *sulpher.JobManager,
	tid tenant.TenantID, logType string, logTenantID tenant.TenantID,
) {
	var req struct {
		Query    string `json:"query"`
		MaxDepth int    `json:"max_depth"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if req.Query == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrQueryRequired, "Query is required")
		return
	}
	if req.MaxDepth <= 0 {
		req.MaxDepth = s.config.MaxQueryDepth
	}

	timeout := time.Duration(s.config.QueryTimeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// Whole-query result cache: check before executing the BFS.
	var cacheKey string
	if s.config.GraphQueryCacheTTL > 0 {
		cacheKey = graphQueryCacheKey(tid, req.Query, req.MaxDepth)
		if cached, err := s.cache.Get(ctx, cacheKey); err == nil {
			if hit, ok := cached.([]byte); ok {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Cache", "HIT")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(hit)
				return
			}
		}
	}

	result, err := jm.ExecuteSync(ctx, req.Query, req.MaxDepth)
	if err != nil {
		code := xoluerr.ErrGraphFailed
		status := http.StatusBadRequest
		if ctx.Err() != nil {
			code = xoluerr.ErrQueryTimeout
			status = http.StatusGatewayTimeout
		} else if errors.Is(err, sulpher.ErrVisitedNodeLimit) {
			code = xoluerr.ErrGraphVisitedLimit
			status = http.StatusRequestEntityTooLarge
		} else if errors.Is(err, sulpher.ErrResultLimit) {
			code = xoluerr.ErrGraphResultLimit
			status = http.StatusRequestEntityTooLarge
		}
		s.writeError(w, status, code, err.Error())
		return
	}

	response := map[string]interface{}{
		"status": "completed",
		"result": result.Data,
		"stats": map[string]interface{}{
			"nodes_traversed":   result.Stats.NodesTraversed,
			"paths_found":       result.Stats.PathsFound,
			"execution_time_ms": result.Stats.ExecutionTime.Milliseconds(),
		},
	}

	maxBytes := s.config.QueryMaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024
	}
	encoded, jsonErr := json.Marshal(response)
	if jsonErr != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrQueryFailed, "failed to encode response")
		return
	}
	if len(encoded) > maxBytes {
		s.writeError(w, http.StatusRequestEntityTooLarge, xoluerr.ErrQueryResponseSize,
			fmt.Sprintf("response too large: %d bytes (max %d)", len(encoded), maxBytes))
		return
	}

	if cacheKey != "" {
		ttl := time.Duration(s.config.GraphQueryCacheTTL) * time.Second
		_ = s.cache.Set(ctx, cacheKey, encoded, ttl)
		w.Header().Set("X-Cache", "MISS")
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)

	if result.Stats.ExecutionTime > 5*time.Second {
		ev := s.logger.Warn().
			Str("type", logType).
			Int64("duration_ms", result.Stats.ExecutionTime.Milliseconds()).
			Int("nodes_traversed", result.Stats.NodesTraversed).
			Int("paths_found", result.Stats.PathsFound).
			Int("response_bytes", len(encoded))
		if logTenantID != 0 {
			ev = ev.Uint16("tenant_id", uint16(logTenantID))
		}
		ev.Msg("Slow query")
	}
}

// handleSulpherQueryAsync submits a Sulpher query for async execution
// POST /api/v1/graph/query/async
func (s *Server) handleSulpherQueryAsync(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	if s.sulpherJobs == nil {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrQueryEngineNotInit, "Sulpher query engine not initialized")
		return
	}

	var req struct {
		Query    string `json:"query"`
		MaxDepth int    `json:"max_depth"`
	}

	if !s.decodeJSON(w, r, &req) {
		return
	}

	if req.Query == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrQueryRequired, "Query is required")
		return
	}

	if req.MaxDepth <= 0 {
		req.MaxDepth = s.config.MaxQueryDepth
	}

	job, err := s.sulpherJobs.Submit(req.Query, req.MaxDepth)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidEntity, err.Error())
		return
	}

	s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"query_id":   job.ID,
		"status":     sulpher.StatusPending, // always pending at submission time; avoid reading from shared pointer
		"created_at": job.CreatedAt,
	})
}

// handleSulpherQueryStatus gets the status of an async query
// GET /api/v1/graph/query/{query_id}
func (s *Server) handleSulpherQueryStatus(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	if s.sulpherJobs == nil {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrQueryEngineNotInit, "Sulpher query engine not initialized")
		return
	}

	queryID := chi.URLParam(r, "query_id")
	if queryID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "query_id required")
		return
	}

	job, exists := s.sulpherJobs.GetJob(queryID)
	if !exists {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrQueryNotFound, "Query not found")
		return
	}

	response := map[string]interface{}{
		"query_id":   job.ID,
		"query":      job.Query,
		"status":     job.Status,
		"created_at": job.CreatedAt,
	}

	if job.StartedAt != nil {
		response["started_at"] = job.StartedAt
	}
	if job.EndedAt != nil {
		response["ended_at"] = job.EndedAt
	}
	if job.Error != "" {
		response["error"] = job.Error
	}

	s.writeJSON(w, http.StatusOK, response)
}

// handleSulpherQueryResult gets the result of a completed async query
// GET /api/v1/graph/query/{query_id}/result
func (s *Server) handleSulpherQueryResult(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	if s.sulpherJobs == nil {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrQueryEngineNotInit, "Sulpher query engine not initialized")
		return
	}

	queryID := chi.URLParam(r, "query_id")
	if queryID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "query_id required")
		return
	}

	job, exists := s.sulpherJobs.GetJob(queryID)
	if !exists {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrQueryNotFound, "Query not found")
		return
	}

	if job.Status == "pending" || job.Status == "running" {
		s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"query_id": job.ID,
			"status":   job.Status,
			"message":  "Query is still processing",
		})
		return
	}

	if job.Status == "failed" {
		s.writeJSON(w, http.StatusOK, map[string]interface{}{
			"query_id": job.ID,
			"status":   job.Status,
			"error":    job.Error,
		})
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"query_id": job.ID,
		"status":   job.Status,
		"result":   job.Result.Data,
		"stats": map[string]interface{}{
			"nodes_traversed":   job.Result.Stats.NodesTraversed,
			"paths_found":       job.Result.Stats.PathsFound,
			"execution_time_ms": job.Result.Stats.ExecutionTime.Milliseconds(),
		},
	})
}

// =============================================================================
// OQL (SQL) Query Handlers
// =============================================================================

// handleOQLQuery executes an OQL query synchronously
// POST /api/v1/oql/query
// POST /api/v1/tenant/{tenant_id}/oql/query
func (s *Server) handleOQLQuery(w http.ResponseWriter, r *http.Request) {
	if s.oqlJobs == nil {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrQueryEngineNotInit, "OQL query engine not initialized")
		return
	}

	var req struct {
		Query string `json:"query"`
	}

	if !s.decodeJSON(w, r, &req) {
		return
	}

	if req.Query == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrQueryRequired, "Query is required")
		return
	}

	// Enforce server-side query timeout
	timeout := time.Duration(s.config.QueryTimeout) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	// Execute using tenant-scoped store
	store := s.getStore(r.Context())
	result, err := s.oqlJobs.ExecuteSyncWithStore(ctx, req.Query, store)
	if err != nil {
		code := xoluerr.ErrQueryFailed
		status := http.StatusBadRequest
		if ctx.Err() != nil {
			code = xoluerr.ErrQueryTimeout
			status = http.StatusGatewayTimeout
		} else if errors.Is(err, oql.ErrScanLimit) {
			code = xoluerr.ErrQueryScanLimit
			status = http.StatusRequestEntityTooLarge
		} else if errors.Is(err, oql.ErrResultLimit) {
			code = xoluerr.ErrQueryRowLimit
			status = http.StatusRequestEntityTooLarge
		}
		s.writeError(w, status, code, err.Error())
		return
	}

	response := map[string]interface{}{
		"status": "completed",
		"data":   result.Rows,
		"stats": map[string]interface{}{
			"rows_scanned":      result.Stats.RowsScanned,
			"rows_returned":     result.Stats.RowsReturned,
			"rows_affected":     result.Stats.RowsAffected,
			"execution_time_ms": result.Stats.ExecutionTime.Milliseconds(),
		},
	}

	// Enforce response size limit
	maxBytes := s.config.QueryMaxResponseBytes
	if maxBytes <= 0 {
		maxBytes = 10 * 1024 * 1024 // 10 MB default
	}
	encoded, jsonErr := json.Marshal(response)
	if jsonErr != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrQueryFailed, "failed to encode response")
		return
	}
	if len(encoded) > maxBytes {
		s.writeError(w, http.StatusRequestEntityTooLarge, xoluerr.ErrQueryResponseSize,
			fmt.Sprintf("response too large: %d bytes (max %d)", len(encoded), maxBytes))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)

	// Log slow queries
	if result.Stats.ExecutionTime > 5*time.Second {
		s.logger.Warn().
			Str("type", "oql").
			Int64("duration_ms", result.Stats.ExecutionTime.Milliseconds()).
			Int("rows_scanned", result.Stats.RowsScanned).
			Int("rows_returned", result.Stats.RowsReturned).
			Int("response_bytes", len(encoded)).
			Msg("Slow query")
	}
}

// handleOQLQueryAsync submits an OQL query for async execution
// POST /api/v1/oql/query/async
func (s *Server) handleOQLQueryAsync(w http.ResponseWriter, r *http.Request) {
	if s.oqlJobs == nil {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrQueryEngineNotInit, "OQL query engine not initialized")
		return
	}

	var req struct {
		Query string `json:"query"`
	}

	if !s.decodeJSON(w, r, &req) {
		return
	}

	if req.Query == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrQueryRequired, "Query is required")
		return
	}

	// Capture the tenant-scoped store so the background goroutine
	// executes against the correct tenant, not the default store.
	store := s.getStore(r.Context())
	queryID := s.oqlJobs.Submit(req.Query, store)

	s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"query_id": queryID,
		"status":   "pending",
	})
}

// handleOQLQueryStatus gets the status of an async OQL query
// GET /api/v1/oql/query/{query_id}
func (s *Server) handleOQLQueryStatus(w http.ResponseWriter, r *http.Request) {
	if s.oqlJobs == nil {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrQueryEngineNotInit, "OQL query engine not initialized")
		return
	}

	queryID := chi.URLParam(r, "query_id")
	if queryID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "query_id required")
		return
	}

	job := s.oqlJobs.GetJob(queryID)
	if job == nil {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrQueryNotFound, "Query not found")
		return
	}

	response := map[string]interface{}{
		"query_id":   job.ID,
		"query":      job.Query,
		"status":     job.Status,
		"created_at": job.CreatedAt,
		"updated_at": job.UpdatedAt,
	}

	if job.Error != "" {
		response["error"] = job.Error
	}

	s.writeJSON(w, http.StatusOK, response)
}

// handleOQLQueryResult gets the result of a completed async OQL query
// GET /api/v1/oql/query/{query_id}/result
func (s *Server) handleOQLQueryResult(w http.ResponseWriter, r *http.Request) {
	if s.oqlJobs == nil {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrQueryEngineNotInit, "OQL query engine not initialized")
		return
	}

	queryID := chi.URLParam(r, "query_id")
	if queryID == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "query_id required")
		return
	}

	job := s.oqlJobs.GetJob(queryID)
	if job == nil {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrQueryNotFound, "Query not found")
		return
	}

	if job.Status == "pending" || job.Status == "running" {
		s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
			"query_id": job.ID,
			"status":   job.Status,
			"message":  "Query is still processing",
		})
		return
	}

	if job.Status == "failed" {
		s.writeJSON(w, http.StatusOK, map[string]interface{}{
			"query_id": job.ID,
			"status":   job.Status,
			"error":    job.Error,
		})
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"query_id": job.ID,
		"status":   job.Status,
		"data":     job.Result.Rows,
		"stats": map[string]interface{}{
			"rows_scanned":      job.Result.Stats.RowsScanned,
			"rows_returned":     job.Result.Stats.RowsReturned,
			"rows_affected":     job.Result.Stats.RowsAffected,
			"execution_time_ms": job.Result.Stats.ExecutionTime.Milliseconds(),
		},
	})
}

// handleCreateSchema creates or updates a schema
func (s *Server) handleCreateSchema(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")

	if err := validateEntityName(entity); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidEntity, err.Error())
		return
	}

	var schema map[string]interface{}
	if !s.decodeJSON(w, r, &schema) {
		return
	}

	// D-009: reject schemas whose property keys are not bare SQL identifiers
	// before any persistence or table derivation. Field names become column
	// and index names in derived DDL, which cannot be parameterised; a
	// non-identifier key is a DDL-injection vector. Rejecting here returns a
	// clean 400 rather than a 500 after the schema is half-persisted.
	if err := validateSchemaFieldNames(schema); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidEntity, err.Error())
		return
	}

	if err := s.validator.LoadSchema(entity, schema); err != nil {
		s.logger.Error().Err(err).Msg("Failed to load schema")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrSchemaLoadFailed, "Failed to load schema")
		return
	}

	// A new or changed schema may add or alter x-ref annotations, so the
	// referential-integrity registry must be rebuilt to reflect them
	// (@R, wave 2 stage 2). Cheap: it re-reads the loaded schemas' x-ref
	// keys, which are few.
	s.rebuildRIRegistry()

	// Persist schema to disk so it survives restarts and is visible
	// to subsystems that scan the schema directory (e.g. OQL validator).
	if err := s.validator.SaveSchema(entity, schema); err != nil {
		s.logger.Warn().Err(err).Str("entity", entity).Msg("Failed to persist schema to disk")
	}

	// Register adapted table if the store supports it.
	// This creates or updates the optimised column-per-field table
	// for this entity, enabling direct SQL queries instead of JSON
	// blob extraction.
	if sqlStore, ok := s.storage.(*storage.SQLiteStore); ok {
		if err := sqlStore.RegisterAdaptedEntity(r.Context(), entity, schema); err != nil {
			s.logger.Error().Err(err).Str("entity", entity).Msg("Failed to register adapted table")
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
				"Schema loaded but adapted table registration failed")
			return
		}
		s.logger.Info().Str("entity", entity).Msg("Registered adapted table")
	}

	s.logger.Info().Str("entity", entity).Msg("Created/updated schema")

	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"message": fmt.Sprintf("Schema for %s created/updated successfully", entity),
	})
}

// handleGetSchema retrieves a schema
func (s *Server) handleGetSchema(w http.ResponseWriter, r *http.Request) {
	entity := chi.URLParam(r, "entity")

	if err := validateEntityName(entity); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidEntity, err.Error())
		return
	}

	if !s.validator.HasSchema(entity) {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrSchemaNotFound, fmt.Sprintf("No schema found for %s", entity))
		return
	}

	schema, err := s.validator.GetSchema(entity)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrSchemaLoadFailed, "Failed to retrieve schema")
		return
	}

	s.writeJSON(w, http.StatusOK, schema)
}

// handleListSchemas enumerates the entity types that have a registered
// schema (T-24). Names only: the validator holds no registration
// timestamps, so the envelope does not invent one. Sorted for stable
// output. Schemas are server-global (growth mode), not tenant-scoped.
func (s *Server) handleListSchemas(w http.ResponseWriter, r *http.Request) {
	entities := s.validator.LoadedEntities()
	sort.Strings(entities)
	schemas := make([]map[string]interface{}, 0, len(entities))
	for _, e := range entities {
		schemas = append(schemas, map[string]interface{}{"name": e})
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"schemas": schemas,
		"count":   len(schemas),
	})
}

// Helper functions

func (s *Server) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) writeError(w http.ResponseWriter, status int, code xoluerr.Code, message string) {
	s.writeJSON(w, status, models.ErrorResponse{
		Error: struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Status  int    `json:"status"`
		}{
			Code:    string(code),
			Message: message,
			Status:  status,
		},
	})
}

// decodeJSON reads and decodes JSON from the request body. It returns true
// on success. On failure it writes the appropriate error response (413 for
// oversized bodies, 400 for malformed JSON) and returns false.
func (s *Server) decodeJSON(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	err := json.NewDecoder(r.Body).Decode(dst)
	if err == nil {
		return true
	}
	// MaxBytesReader wraps the body and returns a MaxBytesError when the
	// limit is exceeded. Detect this and return 413.
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		s.writeError(w, http.StatusRequestEntityTooLarge, xoluerr.ErrEntityTooLarge, "Request body too large")
		return false
	}
	s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "Invalid JSON")
	return false
}

func (s *Server) invalidateCache(ctx context.Context, entity string) {
	cacheCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tid := getTenantIDNumeric(ctx)
	pattern := tid.CachePattern(entity)
	_ = s.cache.DeletePattern(cacheCtx, pattern)
}

// invalidateCacheForID removes the individual GET cache entry for a specific
// entity instance, plus all list caches for the entity type (since lists
// include the modified record). Unlike invalidateCache, this preserves GET
// cache entries for other entity instances of the same type.
func (s *Server) invalidateCacheForID(ctx context.Context, entity string, id int) {
	cacheCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tid := getTenantIDNumeric(ctx)

	// Delete the individual entity cache entry
	key := tid.CacheKey(entity, id)
	_ = s.cache.Delete(cacheCtx, key)

	// Invalidate list caches (since the list contents changed)
	listPattern := tid.CacheListPattern(entity)
	_ = s.cache.DeletePattern(cacheCtx, listPattern)
}

// handleFullTextSearch performs full-text search across entities
func (s *Server) handleFullTextSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "Missing 'q' query parameter")
		return
	}

	entity := r.URL.Query().Get("entity")

	// Check if full-text search is enabled
	if !s.config.FullTextEnabled {
		s.writeError(w, http.StatusServiceUnavailable, xoluerr.ErrSearchDisabled, "Full-text search is not enabled")
		return
	}

	store := s.getStore(r.Context())
	results, err := store.FullTextSearch(r.Context(), query, entity)
	if err != nil {
		s.logger.Error().Err(err).Str("query", query).Msg("Full-text search failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrSearchFailed, "Search failed")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"query":   query,
		"entity":  entity,
		"count":   len(results),
		"results": results,
	})
}

func (s *Server) embedReferences(ctx context.Context, data map[string]interface{}, depth int) map[string]interface{} {
	if depth <= 0 {
		return data
	}

	result := make(map[string]interface{})
	for k, v := range data {
		result[k] = s.embedValue(ctx, v, depth)
	}

	return result
}

// embedValue recursively embeds references in any value type
func (s *Server) embedValue(ctx context.Context, v interface{}, depth int) interface{} {
	if depth <= 0 {
		return v
	}

	// Check if it's a REF
	if ref, isRef := models.IsReference(v); isRef {
		store := s.getStore(ctx)
		if refData, err := store.Get(ctx, ref.Entity, int(ref.ID)); err == nil {
			return s.embedReferences(ctx, refData, depth-1)
		}
		return v
	}

	// Check if it's a map (nested object)
	if m, ok := v.(map[string]interface{}); ok {
		result := make(map[string]interface{})
		for mk, mv := range m {
			result[mk] = s.embedValue(ctx, mv, depth)
		}
		return result
	}

	// Check if it's an array
	if arr, ok := v.([]interface{}); ok {
		result := make([]interface{}, len(arr))
		for i, av := range arr {
			result[i] = s.embedValue(ctx, av, depth)
		}
		return result
	}

	// Scalar value, return as-is
	return v
}

// cascadeDelete deletes the target entity and, transitively, every entity
// that refers to it — bounded by MaxCascadeDeletions.
//
// T-41 (@R08 stage 1): the pre-fix version seeded its work queue with only
// the target and reported cascaded_deletes without ever discovering any
// referents, so CascadingDelete=true was semantically identical to plain
// delete. This implementation walks inbound graph edges to discover
// referents, then deletes them in BFS order — bounded, cycle-safe.
//
// This is the minimum fix, not the full RI programme: policies remain
// binary (cascade all, restrict all) rather than per-x-ref; the full
// per-field cascade|restrict|nullify design lives in
// docs/proposals/referential-integrity.md (@R) and lands in wave 2.
//
// Behaviour when the graph is disabled: no referents can be discovered,
// so cascade degrades to a plain delete of the target — with a warning
// logged so operators see the discovery gap rather than a silent bypass.
func (s *Server) cascadeDelete(ctx context.Context, entity string, id int) ([]string, error) {
	deletedRefs := []string{}
	toCheck := []struct {
		entity string
		id     int
	}{{entity, id}}
	checked := make(map[string]bool)

	tid := getTenantIDNumeric(ctx)
	graphAvailable := s.config.GraphEnabled && s.graph != nil
	if !graphAvailable {
		s.logger.Warn().Str("entity", entity).Int("id", id).
			Msg("cascade delete requested but graph disabled; referents cannot be discovered")
	}

	for len(toCheck) > 0 && len(deletedRefs) < s.config.MaxCascadeDeletions {
		current := toCheck[0]
		toCheck = toCheck[1:]

		key := fmt.Sprintf("%s:%d", current.entity, current.id)
		if checked[key] {
			continue
		}
		checked[key] = true
		deletedRefs = append(deletedRefs, key)

		// Discover referents via inbound graph edges before deleting —
		// the delete itself removes the incoming-edge target, so
		// discovery must happen first.
		if graphAvailable {
			nodeID := tid.NodeID(current.entity, current.id)
			incoming, err := s.graph.GetIncomingEdges(nodeID)
			if err != nil {
				s.logger.Error().Err(err).Str("node", nodeID).
					Msg("cascade delete: incoming-edge lookup failed; continuing without this node's referents")
			} else {
				for referrerNodeID := range incoming {
					refEntity, refID, ok := parseNodeID(referrerNodeID)
					if !ok {
						s.logger.Warn().Str("node", referrerNodeID).
							Msg("cascade delete: unparseable referrer nodeID; skipped")
						continue
					}
					toCheck = append(toCheck, struct {
						entity string
						id     int
					}{refEntity, refID})
				}
			}
		}

		// Delete the entity.
		store := s.getStore(ctx)
		if err := store.Delete(ctx, current.entity, current.id); err != nil {
			s.logger.Error().Err(err).Str("entity", current.entity).Int("id", current.id).
				Msg("cascade delete: entity delete failed")
		}

		// Remove from graph.
		s.removeGraph(ctx, current.entity, current.id)
	}

	return deletedRefs, nil
}

// parseNodeID splits a tenant-scoped graph node ID back into its
// (entity, id) components. Handles both bare form ("entity:id", tenant 0)
// and tenant-prefixed form ("XXXX@entity:id"). Returns ok=false if the
// input does not match either shape.
func parseNodeID(nodeID string) (entity string, id int, ok bool) {
	stripped := tenant.NodeIDStripped(nodeID)
	i := strings.LastIndex(stripped, ":")
	if i <= 0 || i == len(stripped)-1 {
		return "", 0, false
	}
	n, err := strconv.Atoi(stripped[i+1:])
	if err != nil {
		return "", 0, false
	}
	return stripped[:i], n, true
}

// rebuildRIRegistry rebuilds the referential-integrity registry from the
// validator's currently loaded schemas (@R, wave 2 stage 2). Called at
// server start and after schema changes. A malformed x-ref on one entity
// is logged and that entity is skipped, rather than failing the whole
// rebuild — one bad schema should not disable RI everywhere.
func (s *Server) rebuildRIRegistry() {
	reg := refintegrity.NewRegistry()
	le, ok := s.validator.(interface{ LoadedEntities() []string })
	if !ok {
		// A validator that can't enumerate entities (e.g. the no-op) has
		// no schemas to read x-refs from; leave the registry empty.
		s.riRegistry = reg
		return
	}
	for _, entity := range le.LoadedEntities() {
		raw, err := s.validator.GetSchema(entity)
		if err != nil {
			continue
		}
		fields := refintegrity.FieldsFromRawSchema(raw)
		if err := reg.AddEntitySchema(entity, fields); err != nil {
			s.logger.Warn().Err(err).Str("entity", entity).
				Str("code", string(xoluerr.ErrRISchemaXRef)).
				Msg("referential integrity: malformed x-ref; entity's refs not enforced")
			continue
		}
	}
	s.riRegistry = reg

	// Parity guard (defensive regression check): if any schema carries an
	// x-ref policy but the graph subsystem is disabled, RI enforcement
	// silently does not run — restrictReferrers can't discover referrers
	// and syncGraphEdges short-circuits, so deletes that should be
	// refused succeed and dangling references form. This exact
	// misconfiguration (a map-built store defaulting graph off) hid a
	// referential-integrity gap through several debugging cycles. Make it
	// loud: error-log always, and fail hard when XOLU_STRICT_SUBSYSTEMS
	// is set (CI and tests set it, so the combination cannot ship).
	if reg.HasAnyPolicy() && !s.config.GraphEnabled {
		s.logger.Error().
			Str("code", string(xoluerr.ErrRISchemaXRef)).
			Msg("referential integrity: x-ref policies are present but the graph " +
				"subsystem is DISABLED — restrict/cascade enforcement will NOT run; " +
				"enable the graph subsystem or remove the x-ref annotations")
		if os.Getenv("XOLU_STRICT_SUBSYSTEMS") != "" {
			s.logger.Fatal().
				Str("code", string(xoluerr.ErrRISchemaXRef)).
				Msg("referential integrity: x-ref present with graph disabled and " +
					"XOLU_STRICT_SUBSYSTEMS set — refusing to run with silent RI gap")
		}
	}
}

// restrictingEntities returns the names of entities that reference `entity`
// through an x-ref with on_delete=restrict — the set the store-level
// in-transaction check (DeleteWithRestrict) needs. Empty when no restrict
// policy targets this entity, letting callers skip the checked path.
func (s *Server) restrictingEntities(entity string) []string {
	if s.riRegistry == nil || !s.riRegistry.HasRestrictReferrers(entity) {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range s.riRegistry.ReferrersOf(entity) {
		if p.OnDelete == refintegrity.OnDeleteRestrict && !seen[p.ReferringEntity] {
			seen[p.ReferringEntity] = true
			out = append(out, p.ReferringEntity)
		}
	}
	return out
}

// restrictReferrers reports whether entity:id has any live referrer under
// a restrict on_delete policy, and if so returns up to a bounded number
// of referrer keys for the error message (@R02.2). The registry pre-check
// (HasRestrictReferrers) gates the inbound-edge query so entities that
// nothing restrict-references cost nothing at delete time.
//
// "Live" is what matters: the edge table records only current references,
// so a referrer that was itself deleted does not block. Discovery uses
// the same inbound-edge query T-41's cascade path uses.
func (s *Server) restrictReferrers(ctx context.Context, entity string, id int) (refs []string, blocked bool) {
	if !s.riRegistry.HasRestrictReferrers(entity) {
		return nil, false
	}
	if !s.config.GraphEnabled || s.graph == nil {
		// Without the graph we cannot discover referrers. Rather than
		// silently allow a delete the schema says to restrict, log and
		// allow — but make the gap visible. (A graph-less deployment that
		// wants RI must enable the graph; flagged here so it is not a
		// silent bypass.)
		s.logger.Warn().Str("entity", entity).Int("id", id).
			Msg("referential integrity: restrict policy present but graph disabled; cannot enforce")
		return nil, false
	}

	// Which referring entities carry a restrict policy toward this target.
	restrictBy := make(map[string]bool)
	for _, p := range s.riRegistry.ReferrersOf(entity) {
		if p.OnDelete == refintegrity.OnDeleteRestrict {
			restrictBy[p.ReferringEntity] = true
		}
	}

	tid := getTenantIDNumeric(ctx)
	nodeID := tid.NodeID(entity, id)
	incoming, err := s.graph.GetIncomingEdges(nodeID)
	if err != nil {
		// A discovery failure must not silently permit the delete when a
		// restrict policy is in force; treat it as blocking, naming the
		// error, so the operator investigates rather than losing data.
		s.logger.Error().Err(err).Str("node", nodeID).
			Msg("referential integrity: inbound-edge lookup failed; blocking delete conservatively")
		return []string{"(referrer discovery failed; delete blocked for safety)"}, true
	}

	const maxNamed = 10
	for referrerNodeID := range incoming {
		refEntity, refID, ok := parseNodeID(referrerNodeID)
		if !ok {
			continue
		}
		if restrictBy[refEntity] {
			refs = append(refs, fmt.Sprintf("%s:%d", refEntity, refID))
			if len(refs) >= maxNamed {
				break
			}
		}
	}
	return refs, len(refs) > 0
}

func validateEntityName(entity string) error {
	// Canonical strict-identifier policy lives in pkg/qs, shared with the storage
	// schema validators.
	if entity == "" {
		return fmt.Errorf("entity name cannot be empty")
	}
	if !qs.IsValidStrictIdentifier(entity) {
		return fmt.Errorf("invalid entity name: must start with a letter and contain only letters, numbers, and underscores")
	}
	return nil
}

// validateFieldName checks that a filter field name is safe for use in
// json_extract SQL paths. Allows dotted paths for nested fields (e.g.
// "address.city") where each segment matches the entity name pattern.
// Rejects anything that could be used for SQL injection.
func validateFieldName(field string) error {
	if field == "" {
		return fmt.Errorf("field name cannot be empty")
	}
	if !qs.IsValidStrictFieldPath(field) {
		return fmt.Errorf("invalid field name %q: each segment must start with a letter and contain only letters, numbers, and underscores", field)
	}
	return nil
}

// validateSchemaFieldNames checks every property key of a JSON-schema document
// against the strict identifier allowlist (single bare identifier, no dots).
// Property keys become SQL column and index names in derived adapted-table DDL
// (see pkg/storage.DeriveAdaptedTableSpecFrom), which cannot be parameterised,
// so a non-identifier key is a DDL-injection vector (D-009). Rejecting at the
// handler boundary returns a clean 400 before any persistence or derivation.
//
// The "id" key is exempted because it is treated as a system column and never
// reaches column-name derivation.
func validateSchemaFieldNames(schema map[string]interface{}) error {
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		// No properties object: nothing to derive into columns.
		return nil
	}
	for name := range props {
		if name == "id" {
			continue
		}
		if !qs.IsValidStrictIdentifier(name) {
			return fmt.Errorf("invalid schema field name %q: must start with a letter and contain only letters, numbers, and underscores", name)
		}
	}
	return nil
}

// handleExport exports all data as a zip archive
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	// Generate timestamp for filename
	timestamp := time.Now().UTC().Format("2006-01-02T150405Z")
	filename := fmt.Sprintf("xolu-export-%s.zip", timestamp)

	// Set headers for zip download
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))

	// Create zip writer directly to response
	zw := zip.NewWriter(w)
	defer func() { _ = zw.Close() }()

	// Create manifest
	manifest := map[string]interface{}{
		"version":       version.Version,
		"exported_at":   time.Now().UTC().Format(time.RFC3339),
		"storage_type":  s.config.StorageType,
		"graph_enabled": s.config.GraphEnabled,
	}

	// Add SQLite database if using sqlite storage
	// Checkpoint the WAL so all data is in the main database file.
	// Without this, recent writes may only exist in the WAL and
	// the exported .db file would be missing data.
	if sqlStore, ok := s.storage.(*storage.SQLiteStore); ok {
		if _, err := sqlStore.DB().ExecContext(r.Context(), "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
			s.logger.Warn().Err(err).Msg("WAL checkpoint before export failed")
		}
	}
	var dbPath string
	if s.config.SQLitePerFileTenants {
		dbPath = sl.TenantStorePath(s.config.BaseDir, 0)
	} else {
		dbPath = sl.SharedStorePath(s.config.BaseDir)
	}
	if err := s.addFileToZip(zw, dbPath, "xolu.db"); err != nil {
		s.logger.Error().Err(err).Str("path", dbPath).Msg("Failed to add database to export")
		// Can't write error response since we've started writing the zip
		return
	}
	manifest["database_file"] = "xolu.db"

	// Add graph if enabled
	if s.config.GraphEnabled {
		// Export graph as JSON for analysis. The graph itself lives in the
		// tenant's edge tables inside the main database (exported above); this
		// JSON is a convenience representation.
		if s.graph != nil {
			if err := s.addGraphJSONToZip(zw); err != nil {
				s.logger.Error().Err(err).Msg("Failed to add graph JSON to export")
			} else {
				manifest["graph_json"] = "graph.json"
			}
		}
	}

	// Write manifest
	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	mw, err := zw.Create("manifest.json")
	if err == nil {
		_, _ = mw.Write(manifestBytes)
	}

	s.logger.Info().Str("filename", filename).Msg("Export completed")
}

// addFileToZip adds a file to the zip archive
func (s *Server) addFileToZip(zw *zip.Writer, srcPath, destName string) error {
	file, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return err
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = destName
	header.Method = zip.Deflate

	writer, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}

	_, err = io.Copy(writer, file)
	return err
}

// addGraphJSONToZip exports graph data as JSON.
func (s *Server) addGraphJSONToZip(zw *zip.Writer) error {
	allNodes := s.graph.GetAllNodes()

	graphData := map[string]interface{}{
		"exported_at": time.Now().UTC().Format(time.RFC3339),
		"stats": map[string]int{
			"nodes": s.graph.NodeCount(),
			"edges": s.graph.EdgeCount(),
		},
		"nodes": []map[string]interface{}{},
		"edges": []map[string]interface{}{},
	}

	nodes := []map[string]interface{}{}
	edges := []map[string]interface{}{}

	for _, nodeID := range allNodes {
		nodes = append(nodes, map[string]interface{}{
			"id": nodeID,
		})
		neighbors, err := s.graph.GetNeighbors(nodeID)
		if err == nil {
			for target, ref := range neighbors {
				edges = append(edges, map[string]interface{}{
					"from":         nodeID,
					"to":           target,
					"relationship": ref.Rel,
				})
			}
		}
	}

	graphData["nodes"] = nodes
	graphData["edges"] = edges

	jsonBytes, err := json.MarshalIndent(graphData, "", "  ")
	if err != nil {
		return err
	}
	writer, err := zw.Create("graph.json")
	if err != nil {
		return err
	}
	_, err = writer.Write(jsonBytes)
	return err
}

// adaptedEntityIDs queries the adapted table for an entity and returns all
// node ID strings (in the form "entity:id" for tenant 0, "XXXX@entity:id"
// for non-zero tenants). Returns nil if the entity has no adapted table, so
// the caller can fall back to the in-memory graph index.
//
// This is the source-of-truth path for GetNodesByType when the entity has an
// adapted table: it returns every entity of that type, including those with no
// edges that would be absent from the graph index.
func (s *Server) adaptedEntityIDs(ctx context.Context, sqlStore *storage.SQLiteStore, entity string, tenantID tenant.TenantID) []string {
	reg := sqlStore.AdaptedRegistry()
	if reg == nil || !reg.IsAdapted(entity) {
		return nil
	}
	spec := reg.Get(entity)
	if spec == nil {
		return nil
	}

	db := sqlStore.DB()
	rows, err := db.QueryContext(ctx,
		"SELECT id FROM "+spec.TableName()+" WHERE tenant_id = ? ORDER BY id",
		int(tenantID))
	if err != nil {
		s.logger.Warn().Err(err).Str("entity", entity).Msg("adaptedEntityIDs: query failed")
		return nil
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, tenantID.NodeID(entity, id))
	}
	if err := rows.Err(); err != nil {
		s.logger.Warn().Err(err).Str("entity", entity).Msg("adaptedEntityIDs: rows error")
	}
	return ids
}

// degreeFromStorage computes the degree of a node by querying the tenant edge table
// directly. Called when GetDegree returns "not found" — i.e. when the node
// has no edges in the in-memory graph (adapted entities with zero connections).
//
// Returns (degree, true) when the entity exists but has no edges.
// Returns (zero, false) when the entity does not exist in storage at all.
func (s *Server) degreeFromStorage(ctx context.Context, sqlStore *storage.SQLiteStore, tenantID tenant.TenantID, entity string, entityID int) (graph.Degree, bool) {
	table := tenantID.GraphEdgesTableName()
	db := sqlStore.ReaderDB()

	var outDeg, inDeg int
	row := db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+table+" WHERE source_entity = ? AND source_id = ?",
		entity, entityID)
	if err := row.Scan(&outDeg); err != nil {
		s.logger.Warn().Err(err).Str("entity", entity).Int("id", entityID).Msg("degreeFromStorage: out-degree query failed")
		return graph.Degree{}, false
	}

	row = db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM "+table+" WHERE target_entity = ? AND target_id = ?",
		entity, entityID)
	if err := row.Scan(&inDeg); err != nil {
		s.logger.Warn().Err(err).Str("entity", entity).Int("id", entityID).Msg("degreeFromStorage: in-degree query failed")
		return graph.Degree{}, false
	}

	// Fast path: at least one edge found — entity definitely exists.
	if outDeg > 0 || inDeg > 0 {
		return graph.Degree{Out: outDeg, In: inDeg, Total: outDeg + inDeg}, true
	}

	// Both counts are zero: could be a genuinely edge-free entity OR a node
	// that was deleted. Verify existence via the store to distinguish the two.
	tenantStore, err := s.storeForTenant(tenantID)
	if err != nil {
		return graph.Degree{}, false
	}
	if _, err := tenantStore.Get(ctx, entity, entityID); err != nil {
		return graph.Degree{}, false // not found
	}
	return graph.Degree{Out: 0, In: 0, Total: 0}, true
}

// handleGraphVerify checks that the SQLite tenant edge table is consistent
// with the REF fields stored in entity JSON. Returns 200 on success or 409
// with a description of the first inconsistency found.
//
// Only available when the store implements storage.GraphIntegrity (SQLite).
func (s *Server) handleGraphVerify(w http.ResponseWriter, r *http.Request) {
	gi, ok := s.getStore(r.Context()).(storage.GraphIntegrity)
	if !ok {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrStorageFailed,
			"graph integrity check not supported by this storage backend")
		return
	}
	if err := gi.VerifyGraphIntegrity(r.Context()); err != nil {
		s.writeError(w, http.StatusConflict, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "ok",
		"message":    "tenant edge table is consistent with entity REF fields",
		"vode_count": s.graph.VodeCount(),
	})
}

// handleCreateEdge writes an edge with properties via AddEdgeWithProps.
// This is the KL-2 fix: previously there was no REST endpoint for edge properties;
// all relationships were topology-only via REF fields.
//
// POST /api/v1/graph/edges
// Body: {"from": "entity:id", "to": "entity:id", "rel": "LABEL", "props": {...}}
// Response: {"edge_id": N, "rel": "LABEL", "from": "...", "to": "..."}
func (s *Server) handleCreateEdge(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	var req struct {
		From  string                 `json:"from"`
		To    string                 `json:"to"`
		Rel   string                 `json:"rel"`
		Props map[string]interface{} `json:"props"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if req.From == "" || req.To == "" || req.Rel == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrValidationFailed, "from, to, and rel are required")
		return
	}

	store := s.getStore(r.Context())
	eps, ok := store.(storage.EdgePropertyStore)
	if !ok {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrStorageFailed, "edge property storage not supported")
		return
	}

	edgeID, err := eps.AddEdgeWithProps(r.Context(), req.From, req.To, req.Rel, req.Props)
	if err != nil {
		s.logger.Error().Err(err).Msg("handleCreateEdge: AddEdgeWithProps failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	// Update the in-memory graph so Sulpher queries see the new edge immediately.
	if s.graph != nil {
		if addErr := s.graph.AddEdgeWithID(req.From, req.To, req.Rel, edgeID); addErr != nil {
			s.logger.Debug().Err(addErr).
				Str("from", req.From).Str("to", req.To).Str("rel", req.Rel).
				Msg("handleCreateEdge: in-memory graph edge add (non-fatal)")
		}
	}
	s.invalidateGraphQueryCache(r.Context())

	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"edge_id": edgeID,
		"rel":     req.Rel,
		"from":    req.From,
		"to":      req.To,
	})
}

// handleTenantCreateEdge is the tenant-scoped variant of handleCreateEdge.
// POST /api/v1/tenant/{tenant_id}/graph/edges
func (s *Server) handleTenantCreateEdge(w http.ResponseWriter, r *http.Request) {
	if !s.config.GraphEnabled {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrGraphDisabled, "Graph operations are disabled")
		return
	}

	var req struct {
		From  string                 `json:"from"`
		To    string                 `json:"to"`
		Rel   string                 `json:"rel"`
		Props map[string]interface{} `json:"props"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if req.From == "" || req.To == "" || req.Rel == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrValidationFailed, "from, to, and rel are required")
		return
	}

	store := s.getStore(r.Context())
	eps, ok := store.(storage.EdgePropertyStore)
	if !ok {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrStorageFailed, "edge property storage not supported")
		return
	}

	tid := getTenantIDNumeric(r.Context())
	prefix := tid.GraphNodePrefix()
	from := addPrefix(prefix, req.From)
	to := addPrefix(prefix, req.To)

	edgeID, err := eps.AddEdgeWithProps(r.Context(), from, to, req.Rel, req.Props)
	if err != nil {
		s.logger.Error().Err(err).Msg("handleTenantCreateEdge: AddEdgeWithProps failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	// Update in-memory graph.
	if s.graph != nil {
		if addErr := s.graph.AddEdgeWithID(from, to, req.Rel, edgeID); addErr != nil {
			s.logger.Debug().Err(addErr).
				Str("from", from).Str("to", to).Str("rel", req.Rel).
				Msg("handleTenantCreateEdge: in-memory graph edge add (non-fatal)")
		}
	}
	s.invalidateGraphQueryCache(r.Context())

	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"edge_id": edgeID,
		"rel":     req.Rel,
		"from":    req.From,
		"to":      req.To,
	})
}

// handleGraphRebuild drops and rebuilds the SQLite tenant edge table from
// entity JSON. Use after a manual data migration or if VerifyGraphIntegrity
// reports inconsistencies.
//
// Only available when the store implements storage.GraphIntegrity (SQLite).
func (s *Server) handleGraphRebuild(w http.ResponseWriter, r *http.Request) {
	store := s.getStore(r.Context())
	gi, ok := store.(storage.GraphIntegrity)
	if !ok {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrStorageFailed,
			"graph rebuild not supported by this storage backend")
		return
	}
	if err := gi.RebuildGraph(r.Context()); err != nil {
		s.logger.Error().Err(err).Msg("graph rebuild failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"graph rebuild failed: "+err.Error())
		return
	}

	// Reload the in-memory FlatGraph from the now-repaired edge table so that
	// graph queries reflect the rebuilt state without requiring a restart.
	if s.graph != nil {
		if err := s.reloadGraphFromStore(r.Context(), store); err != nil {
			s.logger.Warn().Err(err).Msg("graph rebuild: edge table rebuilt but in-memory graph reload failed; restart recommended")
		} else {
			s.logger.Info().Msg("graph rebuild: in-memory graph reloaded from rebuilt edge table")
		}
	}

	if s.graph != nil {
		if n := s.graph.VodeCount(); n > 0 {
			vodes := s.graph.GetNodesByType(graph.NodeTypeVode)
			sample := vodes
			if len(sample) > 10 {
				sample = sample[:10]
			}
			wev := s.logger.Warn().Int("vode_count", n).Strs("vode_sample", sample)
			if len(vodes) > 10 {
				wev = wev.Int("vode_remaining", len(vodes)-10)
			}
			wev.Msg("graph rebuild complete but vode nodes remain — dangling REF references in store data")
		}
	}
	s.logger.Info().Msg("tenant edge table rebuilt")
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":     "ok",
		"message":    "tenant edge table rebuilt from entity data; in-memory graph reloaded",
		"vode_count": s.graph.VodeCount(),
	})
}

// handleCommit executes an atomic upsert + optional entity appends + optional
// timeseries events in a single logical operation.
// See docs/COMMIT_ENDPOINT.md for the full spec.
//
//	POST /api/v1/tenant/{tenant_id}/commit
//	POST /api/v1/commit
//
// Availability: SQLite backend only.
// storage.ErrNotSupported, which is mapped to 501 XOLU-CM009 here.
//
// Strict mode (XOLU_STRICT_COMMIT=true, the default): schema validation and
// graph cycle prechecks are run before the storage transaction, matching the
// guarantees of save/create/patch. Set XOLU_STRICT_COMMIT=false only when the
// caller is trusted infrastructure that manages its own invariants.
//
// Timeseries write ordering:
//  1. Validate all fields (entity + TS) — no writes yet.
//  2. If req.Timeseries is non-empty: write to Pebble first.
//     On Pebble failure → return XOLU-CM015; SQLite is untouched; caller retries.
//  3. Execute SQLite transaction (upsert + appends).
//     On SQLite failure → call DeleteKeys to tombstone the Pebble writes
//     synchronously before returning XOLU-CM008 or XOLU-CM016.
func (s *Server) handleCommit(w http.ResponseWriter, r *http.Request) {

	var req storage.CommitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "Invalid JSON in request body")
		return
	}

	// --- Structural validation (always, regardless of strict mode) ----------

	if req.Update.Entity == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMUpdateMissing, "update object is required")
		return
	}
	if err := validateEntityName(req.Update.Entity); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMInvalidUpdateEntity,
			fmt.Sprintf("invalid update entity: %s", err.Error()))
		return
	}
	if req.Update.ID <= 0 {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidID, "update.id must be a positive integer")
		return
	}

	// At least one of append, timeseries, or fsm_walk must carry work.
	// A request with all three absent or empty is rejected with XOLU-CM003.
	// Note: fsm_walk is a v2 field — when API v2 is disabled, FsmWalk is
	// always nil and this check falls through to the append/timeseries path.
	if len(req.Append) == 0 && len(req.Timeseries) == 0 && req.FsmWalk == nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMAppendEmpty,
			"at least one of append, timeseries, or fsm_walk must be non-empty")
		return
	}
	if len(req.Append) > 25 {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMAppendTooLarge,
			fmt.Sprintf("append array exceeds limit of 25 entries (got %d)", len(req.Append)))
		return
	}
	for i, a := range req.Append {
		if a.Entity == "" {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMInvalidAppendEntity,
				fmt.Sprintf("append[%d].entity is required", i))
			return
		}
		if err := validateEntityName(a.Entity); err != nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMInvalidAppendEntity,
				fmt.Sprintf("append[%d]: invalid entity: %s", i, err.Error()))
			return
		}
		if a.ID != nil && *a.ID <= 0 {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidID,
				fmt.Sprintf("append[%d].id must be a positive integer", i))
			return
		}
	}

	// --- API v2 guard --------------------------------------------------------
	//
	// fsm_walk is a v2 field. When v2 is disabled (s.v2Enabled == false),
	// a non-nil FsmWalk is rejected with a clear error rather than silently
	// ignored. This prevents v1-only deployments from accepting a v2 commit
	// body and appearing to succeed while skipping the walk.
	if req.FsmWalk != nil && !s.v2Enabled() {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMNotAvailable,
			"fsm_walk requires API v2 to be enabled (XOLU_API_V2_ENABLED=true)")
		return
	}

	// --- Timeseries pre-flight validation ------------------------------------
	//
	// All TS checks fire before any write so that a misconfigured request gets
	// a clean 400 without touching either store.

	var (
		tsStore       timeseries.Store // non-nil only when req.Timeseries non-empty
		tsEvents      []timeseries.Event
		tsEncodedKeys [][]byte
	)

	if len(req.Timeseries) > 0 {
		if s.tsManager == nil {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMTSDisabled,
				"timeseries array requires timeseries to be enabled (XOLU-CM010)")
			return
		}

		tenantID := getTenantIDNumeric(r.Context())
		if !s.tsManager.IsProvisioned(tenantID) {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMTSNotProvisioned,
				"tenant not provisioned for timeseries; POST /ts/provision first (XOLU-CM011)")
			return
		}

		var err error
		tsStore, err = s.tsManager.StoreFor(tenantID)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrCMTSWriteFailed,
				fmt.Sprintf("could not obtain timeseries store: %s", err.Error()))
			return
		}

		maxBatch := s.config.TSMaxBatchSize
		if maxBatch <= 0 {
			maxBatch = 5000
		}
		if len(req.Timeseries) > maxBatch {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMTSBatchTooLarge,
				fmt.Sprintf("timeseries array exceeds XOLU_TS_MAX_BATCH_SIZE (%d events, max %d) (XOLU-CM014)",
					len(req.Timeseries), maxBatch))
			return
		}

		// Validate each event and pre-encode keys so we have them for rollback
		// without re-encoding under error conditions.
		tsEvents = make([]timeseries.Event, len(req.Timeseries))
		tsEncodedKeys = make([][]byte, len(req.Timeseries))
		for i, e := range req.Timeseries {
			if e.Timeline == 0 {
				s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMTSBadTimeline,
					fmt.Sprintf("timeseries[%d]: timeline 0x0000 is reserved (XOLU-CM012)", i))
				return
			}
			tid, tidErr := timeseries.TimelineIDFromJSON(e.Timeline)
			if tidErr != nil {
				s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMTSBadTimeline,
					fmt.Sprintf("timeseries[%d]: %s (XOLU-CM012)", i, tidErr))
				return
			}
			cfg, ok := tsStore.Timeline(tid)
			if !ok {
				s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMTSBadTimeline,
					fmt.Sprintf("timeseries[%d]: timeline %d not defined for tenant (XOLU-CM012)", i, e.Timeline))
				return
			}
			if uint8(len(e.Dims)) != cfg.Dims {
				s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMTSBadDims,
					fmt.Sprintf("timeseries[%d]: timeline %d expects %d dims, got %d (XOLU-CM013)",
						i, e.Timeline, cfg.Dims, len(e.Dims)))
				return
			}
			if e.Time.IsZero() {
				s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMTSBadTimeline,
					fmt.Sprintf("timeseries[%d]: time must not be zero (XOLU-CM012)", i))
				return
			}
			key, err := timeseries.EncodeKey(tid, cfg.Dims, e.Dims, e.Time)
			if err != nil {
				s.writeError(w, http.StatusBadRequest, xoluerr.ErrCMTSBadTimeline,
					fmt.Sprintf("timeseries[%d]: %s", i, err.Error()))
				return
			}
			tsEncodedKeys[i] = key
			tsEvents[i] = timeseries.Event{
				Timeline: tid,
				Dims:     e.Dims,
				Time:     e.Time,
				Nums:     e.Nums,
				Payload:  e.Payload,
			}
		}
	}

	// --- Strict mode: schema validation + graph cycle prechecks --------------

	if s.config.StrictCommit {
		updateData := make(map[string]interface{}, len(req.Update.Data)+1)
		for k, v := range req.Update.Data {
			updateData[k] = v
		}
		updateData["id"] = req.Update.ID
		if valid, errs := s.validator.Validate(req.Update.Entity, updateData); !valid {
			s.writeJSON(w, http.StatusBadRequest, map[string]interface{}{
				"error": map[string]interface{}{
					"code":    string(xoluerr.ErrValidationFailed),
					"message": fmt.Sprintf("schema validation failed for update entity %q", req.Update.Entity),
					"status":  http.StatusBadRequest,
				},
				"details": errs,
			})
			return
		}
		if err := s.validateGraphEdges(r.Context(), req.Update.Entity, req.Update.ID, updateData); err != nil {
			if errors.Is(err, graph.ErrCycleDetected) {
				s.writeError(w, http.StatusConflict, xoluerr.ErrCycleDetected, err.Error())
				return
			}
			if errors.Is(err, models.ErrDuplicateEdgeTarget) {
				s.writeError(w, http.StatusBadRequest, xoluerr.ErrDuplicateEdgeRef, err.Error())
				return
			}
		}

		for i, a := range req.Append {
			appendData := make(map[string]interface{}, len(a.Data)+1)
			for k, v := range a.Data {
				appendData[k] = v
			}
			if a.ID != nil {
				appendData["id"] = *a.ID
			}
			if valid, errs := s.validator.Validate(a.Entity, appendData); !valid {
				s.writeJSON(w, http.StatusBadRequest, map[string]interface{}{
					"error": map[string]interface{}{
						"code":    string(xoluerr.ErrValidationFailed),
						"message": fmt.Sprintf("schema validation failed for append[%d] entity %q", i, a.Entity),
						"status":  http.StatusBadRequest,
					},
					"details": errs,
				})
				return
			}
			if err := s.validateGraphEdges(r.Context(), a.Entity, 0, appendData); err != nil {
				if errors.Is(err, graph.ErrCycleDetected) {
					s.writeError(w, http.StatusConflict, xoluerr.ErrCycleDetected,
						fmt.Sprintf("append[%d]: %s", i, err.Error()))
					return
				}
				if errors.Is(err, models.ErrDuplicateEdgeTarget) {
					s.writeError(w, http.StatusBadRequest, xoluerr.ErrDuplicateEdgeRef,
						fmt.Sprintf("append[%d]: %s", i, err.Error()))
					return
				}
			}
		}
	}

	// --- Step 1: Write timeseries to Pebble (before SQLite) ------------------
	//
	// If this fails, SQLite is untouched and the caller can retry the whole
	// request safely — no rollback needed.

	if tsStore != nil {
		if _, err := tsStore.AppendBatch(r.Context(), tsEvents, s.config.TSMaxBatchSize); err != nil {
			s.logger.Error().Err(err).Msg("handleCommit: Pebble timeseries write failed; SQLite not touched")
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrCMTSWriteFailed,
				"timeseries write failed; SQLite state unchanged — retry is safe (XOLU-CM015)")
			return
		}
	}

	// --- Step 2: SQLite transaction ------------------------------------------
	//
	// On failure, tombstone the Pebble writes we issued in step 1 before
	// returning the error. The tombstone is synchronous and must complete
	// before the response is sent so that no query can observe an orphaned
	// timeseries entry whose entity state was never advanced.

	entityStore := s.getStore(r.Context())
	result, err := entityStore.Commit(r.Context(), req)
	if err != nil {
		// Tombstone any Pebble writes that succeeded in step 1.
		if tsStore != nil && len(tsEncodedKeys) > 0 {
			if rbErr := tsStore.DeleteKeys(r.Context(), tsEncodedKeys); rbErr != nil {
				// The entity is unchanged (SQLite failed) but the timeseries
				// store now has an orphaned entry we could not tombstone.
				// Log at error level and return a distinct code so operators
				// can identify the situation unambiguously.
				s.logger.Error().
					Err(rbErr).
					AnErr("sqliteErr", err).
					Msg("handleCommit: SQLite failed AND Pebble rollback (DeleteKeys) failed — orphaned TS entry possible (XOLU-CM016)")
				s.writeError(w, http.StatusInternalServerError, xoluerr.ErrCMTSRollbackFailed,
					"commit failed and timeseries rollback also failed; manual remediation may be required (XOLU-CM016)")
				return
			}
			s.logger.Warn().Err(err).Msg("handleCommit: SQLite failed; Pebble TS writes tombstoned cleanly")
		}

		if errors.Is(err, storage.ErrNotSupported) {
			s.writeError(w, http.StatusNotImplemented, xoluerr.ErrCMNotAvailable,
				"POST /commit is not available with the current storage backend. "+
					"Use SQLite (XOLU_STORAGE_TYPE=sqlite). "+
					"See XOLU-CM009.")
			return
		}
		if errors.Is(err, storage.ErrConflict) {
			currentVer := s.fetchCurrentVersion(r.Context(), req.Update.Entity, req.Update.ID)
			s.writeJSON(w, http.StatusConflict, map[string]interface{}{
				"error": map[string]interface{}{
					"code":    string(xoluerr.ErrCMVersionConflict),
					"message": fmt.Sprintf("Version conflict: %s id %d has been modified", req.Update.Entity, req.Update.ID),
					"status":  http.StatusConflict,
				},
				"current_version": currentVer,
			})
			return
		}
		if errors.Is(err, storage.ErrAlreadyExists) {
			s.writeError(w, http.StatusConflict, xoluerr.ErrCMAppendIDExists,
				"an append entry specifies an ID that already exists; commit rolled back")
			return
		}
		// An FSM walk embedded in the commit failed. The whole transaction
		// rolled back (document write included). Surface as XOLU-FSM008 with the
		// underlying walk reason, per the spec's commit-conflict semantics.
		var walkErr *storage.FsmWalkError
		if errors.As(err, &walkErr) {
			s.writeError(w, http.StatusConflict, xoluerr.ErrFSMCommitConflict,
				"fsm_walk failed; commit rolled back: "+walkErr.Message)
			return
		}
		s.logger.Error().Err(err).Msg("handleCommit: SQLite transaction failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrCMTransactionFailed, "commit transaction failed")
		return
	}

	result.TSAccepted = len(tsEvents)

	// --- Post-commit: update in-memory graph and invalidate caches -----------
	//
	// Unconditional (not gated on strict mode): a stale FlatGraph after a
	// successful write is a correctness bug, not a "relax guarantees" choice.

	if merged, err := entityStore.Get(r.Context(), req.Update.Entity, result.Update.ID); err == nil {
		s.updateGraph(r.Context(), req.Update.Entity, result.Update.ID, merged)
	} else {
		s.logger.Warn().Err(err).
			Str("entity", req.Update.Entity).Int("id", result.Update.ID).
			Msg("handleCommit: post-commit Get for graph update failed; in-memory graph may be stale")
	}
	for _, a := range result.Appended {
		if appended, err := entityStore.Get(r.Context(), a.Entity, a.ID); err == nil {
			s.updateGraph(r.Context(), a.Entity, a.ID, appended)
		} else {
			s.logger.Warn().Err(err).
				Str("entity", a.Entity).Int("id", a.ID).
				Msg("handleCommit: post-commit Get for appended graph update failed; in-memory graph may be stale")
		}
	}

	s.invalidateCacheForID(r.Context(), req.Update.Entity, req.Update.ID)
	for _, a := range result.Appended {
		s.invalidateCacheForID(r.Context(), a.Entity, a.ID)
	}

	// --- Post-commit: fire events --------------------------------------------
	//
	// All error paths above have returned; reaching here means the atomic
	// transaction committed successfully. Per docs/EVENT_MODEL.md §3.2 this is
	// what makes the request copy in commit.applied an accurate record of what
	// was committed, not merely what was requested. Firing is best-effort and
	// asynchronous (via dispatchEvent), never able to roll back the commit.
	if s.v2Enabled() {
		// Build the affected-entity REF set: the update plus every append.
		affected := make([]map[string]interface{}, 0, 1+len(result.Appended))
		affected = append(affected, map[string]interface{}{
			"ref": map[string]interface{}{
				"type":   models.RefTypeValue,
				"entity": result.Update.Entity,
				"id":     int64(result.Update.ID),
			},
			"created": result.Update.Created,
			"version": result.Update.Version,
		})
		for _, a := range result.Appended {
			affected = append(affected, map[string]interface{}{
				"ref": map[string]interface{}{
					"type":   models.RefTypeValue,
					"entity": a.Entity,
					"id":     int64(a.ID),
				},
			})
		}

		s.fireCommitAppliedEvent(r, affected, req)

		// If the commit carried an embedded fsm_walk, fire the walk's latches
		// too. A walk is a walk regardless of how it was invoked; the step
		// event additionally carries the commit's affected-entity context.
		if result.FsmWalk != nil {
			mid := int64(result.FsmWalk.Machine)
			walkInput := ""
			if req.FsmWalk != nil {
				walkInput = req.FsmWalk.Input
			}
			s.fireFSMOutputEvents(r, mid, walkInput, result.FsmWalk.Previous, result.FsmWalk.Current, result.FsmWalk.Outputs, result.FsmWalk.Definition)
			s.fireFSMStepEvent(r, mid, walkInput, result.FsmWalk.Previous, result.FsmWalk.Current,
				result.FsmWalk.Terminal, result.FsmWalk.Vars, affected, result.FsmWalk.Definition)
		}
	}

	s.writeJSON(w, http.StatusOK, result)
}
