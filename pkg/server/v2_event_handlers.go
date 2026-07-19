// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_event_handlers.go
//
// S9 (Batch 1): event subscription management — the eight endpoints plus the
// storage surface, with no dispatch yet. A subscription binds an event_type to
// an action with a JSON config. Dispatch (matching, template substitution,
// webhook/oql execution, delivery logging) and the trigger wiring arrive in
// later batches.
//
//   POST   /api/v2/event              Create a subscription
//   GET    /api/v2/event              List subscriptions
//   GET    /api/v2/event/{id}         Retrieve one
//   PATCH  /api/v2/event/{id}         Update one
//   DELETE /api/v2/event/{id}         Delete one
//   GET    /api/v2/event/{id}/log     Delivery log for one
//   POST   /api/v2/event/{id}/test    Test-invoke (501 until a later batch)
//
// IDs are allocated from the generic fsm_id_seq allocator (kind 'event_sub' /
// 'event_log'); the table is named for FSM but is structurally a per-tenant,
// per-kind monotonic counter, so reusing it avoids a parallel id-seq table.

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
)

// Recognized event types and action types. Part 1 ships the four entity/fsm
// triggers and the two async actions; everything else is deferred (S13–S17).
var validEventTypes = map[string]bool{
	"entity.created": true,
	"entity.updated": true,
	"entity.deleted": true,
	"fsm.output":     true,
	"fsm.step":       true,
	"commit.applied": true,
}

var validActionTypes = map[string]bool{
	"webhook": true,
	"oql":     true,
}

// eventDef is the wire + storage shape of a subscription.
type eventDef struct {
	ID         int64           `json:"id"`
	EventType  string          `json:"event_type"`
	ActionType string          `json:"action_type"`
	Config     json.RawMessage `json:"config,omitempty"`
	Execution  string          `json:"execution"`
	CreatedAt  string          `json:"created_at,omitempty"`
}

// eventDB resolves the writer DB and tenant for a request (mirrors genDB).
func (s *Server) eventDB(r *http.Request) (*sql.DB, uint16) {
	return s.genDB(r)
}

// allocEventIDTx allocates the next monotonic ID for an event kind within a
// tenant, reusing the generic fsm_id_seq counter. Must be called inside tx.
func allocEventIDTx(r *http.Request, tx *sql.Tx, tenantID uint16, kind string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(r.Context(), `
		INSERT INTO fsm_id_seq (tenant_id, kind, next_id)
		VALUES (?, ?, 1)
		ON CONFLICT(tenant_id, kind) DO UPDATE SET next_id = next_id + 1
		RETURNING next_id`, tenantID, kind).Scan(&id)
	return id, err
}

// ─── POST /event ──────────────────────────────────────────────────────────────

func (s *Server) handleEventCreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EventType  string          `json:"event_type"`
		ActionType string          `json:"action_type"`
		Config     json.RawMessage `json:"config"`
		Execution  string          `json:"execution"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}
	if !validEventTypes[req.EventType] {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrEventInvalid,
			"unknown or unsupported event_type: "+req.EventType)
		return
	}
	if !validActionTypes[req.ActionType] {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrEventInvalid,
			"unknown or unsupported action_type: "+req.ActionType)
		return
	}
	execution := req.Execution
	if execution == "" {
		execution = "async"
	}
	if execution != "async" && execution != "sync" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrEventInvalid,
			"execution must be 'async' or 'sync'")
		return
	}
	config := req.Config
	if len(config) == 0 {
		config = json.RawMessage("{}")
	}

	db, tenantID := s.eventDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 events")
		return
	}

	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()

	id, err := allocEventIDTx(r, tx, tenantID, "event_sub")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	var createdAt string
	err = tx.QueryRowContext(r.Context(), `
		INSERT INTO event_defs (tenant_id, id, event_type, action_type, config_json, execution)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING created_at`,
		tenantID, id, req.EventType, req.ActionType, string(config), execution).Scan(&createdAt)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	// Part 1: a sync request is accepted but always executes async. Signal the
	// downgrade so the caller knows.
	if execution == "sync" {
		w.Header().Set("X-Executed-As", "async")
	}
	s.writeJSON(w, http.StatusCreated, eventDef{
		ID: id, EventType: req.EventType, ActionType: req.ActionType,
		Config: json.RawMessage(config), Execution: execution, CreatedAt: createdAt,
	})
}

// ─── GET /event (list) ────────────────────────────────────────────────────────

func (s *Server) handleEventList(w http.ResponseWriter, r *http.Request) {
	db, tenantID := s.eventDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 events")
		return
	}
	rows, err := db.QueryContext(r.Context(), `
		SELECT id, event_type, action_type, config_json, execution, created_at
		FROM event_defs WHERE tenant_id = ? ORDER BY id`, tenantID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = rows.Close() }()

	subs := []eventDef{}
	for rows.Next() {
		var sub eventDef
		var cfg string
		if err := rows.Scan(&sub.ID, &sub.EventType, &sub.ActionType, &cfg, &sub.Execution, &sub.CreatedAt); err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		sub.Config = json.RawMessage(cfg)
		subs = append(subs, sub)
	}
	if err := rows.Err(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"subscriptions": subs})
}

// ─── GET /event/{id} ──────────────────────────────────────────────────────────

func (s *Server) handleEventGet(w http.ResponseWriter, r *http.Request) {
	id, ok := s.eventParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.eventDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 events")
		return
	}
	sub, err := loadEventDef(r, db, tenantID, id)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrEventNotFound, "subscription not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, sub)
}

// ─── PATCH /event/{id} ────────────────────────────────────────────────────────

func (s *Server) handleEventUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := s.eventParseID(w, r)
	if !ok {
		return
	}
	var req struct {
		Config    *json.RawMessage `json:"config"`
		Execution *string          `json:"execution"`
	}
	if !s.decodeJSON(w, r, &req) {
		return
	}
	db, tenantID := s.eventDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 events")
		return
	}
	// Only config and execution are mutable; event_type/action_type are fixed at
	// creation (changing them is effectively a different subscription).
	sub, err := loadEventDef(r, db, tenantID, id)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrEventNotFound, "subscription not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	newConfig := string(sub.Config)
	if req.Config != nil {
		newConfig = string(*req.Config)
	}
	newExec := sub.Execution
	if req.Execution != nil {
		if *req.Execution != "async" && *req.Execution != "sync" {
			s.writeError(w, http.StatusBadRequest, xoluerr.ErrEventInvalid,
				"execution must be 'async' or 'sync'")
			return
		}
		newExec = *req.Execution
	}
	_, err = db.ExecContext(r.Context(), `
		UPDATE event_defs SET config_json = ?, execution = ?
		WHERE tenant_id = ? AND id = ?`, newConfig, newExec, tenantID, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	sub.Config = json.RawMessage(newConfig)
	sub.Execution = newExec
	s.writeJSON(w, http.StatusOK, sub)
}

// ─── DELETE /event/{id} ───────────────────────────────────────────────────────

func (s *Server) handleEventDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := s.eventParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.eventDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 events")
		return
	}
	res, err := db.ExecContext(r.Context(),
		`DELETE FROM event_defs WHERE tenant_id = ? AND id = ?`, tenantID, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrEventNotFound, "subscription not found")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{"deleted": id})
}

// ─── GET /event/{id}/log ──────────────────────────────────────────────────────

func (s *Server) handleEventLog(w http.ResponseWriter, r *http.Request) {
	id, ok := s.eventParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.eventDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 events")
		return
	}
	rows, err := db.QueryContext(r.Context(), `
		SELECT id, event_type, status, detail, attempted_at
		FROM event_delivery_log WHERE tenant_id = ? AND event_def_id = ?
		ORDER BY id DESC`, tenantID, id)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = rows.Close() }()

	entries := []map[string]interface{}{}
	for rows.Next() {
		var logID int64
		var eventType, status, detail, attemptedAt string
		if err := rows.Scan(&logID, &eventType, &status, &detail, &attemptedAt); err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		entries = append(entries, map[string]interface{}{
			"id": logID, "event_type": eventType, "status": status,
			"detail": detail, "attempted_at": attemptedAt,
		})
	}
	if err := rows.Err(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"subscription": id, "deliveries": entries,
	})
}

// ─── POST /event/{id}/test ────────────────────────────────────────────────────
// Synchronously dispatches a simulated event to one subscription and returns the
// per-subscription outcome. The request body supplies the event payload to
// simulate; absent fields default sensibly. This exercises the dispatcher in
// isolation from real triggers.

func (s *Server) handleEventTest(w http.ResponseWriter, r *http.Request) {
	id, ok := s.eventParseID(w, r)
	if !ok {
		return
	}
	db, tenantID := s.eventDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 events")
		return
	}
	sub, err := loadEventDef(r, db, tenantID, id)
	if err == sql.ErrNoRows {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrEventNotFound, "subscription not found")
		return
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	// Optional caller-supplied event payload. Defaults to the subscription's own
	// event_type with empty data when omitted.
	var body struct {
		Entity string                 `json:"entity"`
		ID     string                 `json:"id"`
		Data   map[string]interface{} `json:"data"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	ev := event{
		Type:   sub.EventType,
		Entity: body.Entity,
		ID:     body.ID,
		Data:   body.Data,
	}
	if ev.Data == nil {
		ev.Data = map[string]interface{}{}
	}

	results := s.dispatchEventSync(tenantID, ev, id)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"subscription": id, "event_type": sub.EventType, "results": results,
	})
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func (s *Server) eventParseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrEventInvalid, "invalid subscription id")
		return 0, false
	}
	return id, true
}

func loadEventDef(r *http.Request, db *sql.DB, tenantID uint16, id int64) (eventDef, error) {
	var sub eventDef
	var cfg string
	err := db.QueryRowContext(r.Context(), `
		SELECT id, event_type, action_type, config_json, execution, created_at
		FROM event_defs WHERE tenant_id = ? AND id = ?`,
		tenantID, id).Scan(&sub.ID, &sub.EventType, &sub.ActionType, &cfg, &sub.Execution, &sub.CreatedAt)
	if err != nil {
		return sub, err
	}
	sub.Config = json.RawMessage(cfg)
	return sub, nil
}
