// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// event_dispatch.go
//
// S9 (Batch 2): the event dispatcher. Given an event, it finds the matching
// subscriptions for the tenant, substitutes template variables into each
// action's config, runs the action, and records the outcome in the delivery
// log. This file contains the dispatch logic only; the trigger points that
// originate events (CRUD handlers, the FSM walk path) are wired in later
// batches. For Batch 2 the dispatcher is exercised through dispatchEventSync,
// driven by the /event/{id}/test endpoint, so the logic is verifiable in
// isolation from the trigger plumbing.
//
// Part 1 contract: at-most-once, best-effort, single attempt. Dispatch from a
// real trigger is async (fire-and-forget goroutine after the originating
// operation commits); the test path runs it synchronously so the result can be
// observed. Either way every attempt writes one delivery_log row, so a failed
// or dropped delivery is observable after the fact.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ha1tch/xolu/pkg/jsonplate"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
	"github.com/ha1tch/xolu/pkg/version"
)

// event is the in-process representation of something that happened. Its fields
// populate {{event.*}} template variables. data carries type-specific detail
// (the entity's fields, or the FSM output) and is also addressable as
// {{event.data.<key>}}.
type event struct {
	Type   string                 // e.g. "entity.created", "fsm.output"
	Source string                 // finer-grained origin within the type, e.g. "fsm/<from>:<input>:<to>"; empty falls back to Type
	Entity string                 // entity name (entity.* events) or machine id as string (fsm.*)
	ID     interface{}            // native id: int64/int for entities and machines. Stringified ONLY at {{...}} template interpolation, never forced to string in structured (envelope/jsonplate) output, so it keeps its schema-true type on the wire.
	Data   map[string]interface{} // snapshot of the triggering state at trigger time
}

// webhookClient is the HTTP client used for webhook actions. Short timeout —
// Part 1 is single-attempt and we don't want a slow endpoint to wedge dispatch.
var webhookClient = &http.Client{Timeout: 10 * time.Second}

// dispatchEvent finds and runs every subscription matching ev for the tenant,
// asynchronously. Each action runs in its own goroutine; failures are logged to
// event_delivery_log, never surfaced to the caller (the originating operation
// has already committed). This is the entry point trigger sites call.
func (s *Server) dispatchEvent(tenantID tenant.TenantID, ev event) {
	store, ok := s.eventStore(tenantID)
	if !ok {
		return
	}
	subs, err := s.matchEventDefs(tenantID, ev.Type)
	if err != nil {
		s.logger.Warn().Err(err).Str("event", ev.Type).Msg("event dispatch: subscription lookup failed")
		return
	}
	for _, sub := range subs {
		sub := sub
		go func() {
			status, detail := s.runAction(tenantID, sub, ev, store)
			s.logDelivery(tenantID, sub.ID, ev.Type, status, detail)
		}()
	}
}

// dispatchEventSync runs the same logic synchronously and returns a per-
// subscription summary. Used by the /event/{id}/test endpoint so a test invocation
// can observe outcomes; not used by real triggers.
func (s *Server) dispatchEventSync(tenantID tenant.TenantID, ev event, onlySubID int64) []map[string]interface{} {
	store, ok := s.eventStore(tenantID)
	if !ok {
		return nil
	}
	subs, err := s.matchEventDefs(tenantID, ev.Type)
	if err != nil {
		return []map[string]interface{}{{"error": err.Error()}}
	}
	results := []map[string]interface{}{}
	for _, sub := range subs {
		if onlySubID != 0 && sub.ID != onlySubID {
			continue
		}
		status, detail := s.runAction(tenantID, sub, ev, store)
		s.logDelivery(tenantID, sub.ID, ev.Type, status, detail)
		results = append(results, map[string]interface{}{
			"subscription": sub.ID, "status": status, "detail": detail,
		})
	}
	return results
}

// fireEntityEvent dispatches an entity lifecycle event (entity.created /
// updated / deleted) for the request's tenant. Called post-commit from the CRUD
// handlers; dispatch is async and best-effort, so this never blocks or fails the
// originating request. The payload carries only what the handler already holds —
// no extra reads to enrich it (see S9_WORK_STRATEGY §7): created/updated carry
// the written data, deleted carries only the id.
func (s *Server) fireEntityEvent(r *http.Request, eventType, entity string, id int, data map[string]interface{}) {
	tenantID := getTenantIDNumeric(r.Context())
	if data == nil {
		data = map[string]interface{}{}
	}
	s.dispatchEvent(tenantID, event{
		Type:   eventType,
		Entity: entity,
		ID:     id,
		Data:   data,
	})
}

// fireFSMOutputEvents dispatches one fsm.output event per Mealy output produced
// by a walk, post-commit. Like the entity triggers, dispatch is async and best-
// effort. The event carries the machine id (as Entity and ID) and the output
// string under data.output, addressable as {{event.data.output}}.
func (s *Server) fireFSMOutputEvents(r *http.Request, machineID int64, input, previous, current string, outputs []string, definition interface{}) {
	if len(outputs) == 0 {
		return
	}
	tenantID := getTenantIDNumeric(r.Context())
	idStr := strconv.FormatInt(machineID, 10)
	source := fsmTransitionSource(previous, input, current)
	for _, out := range outputs {
		data := map[string]interface{}{"output": out, "machine_id": machineID}
		if definition != nil {
			data["definition"] = definition
		}
		s.dispatchEvent(tenantID, event{
			Type:   "fsm.output",
			Source: source,
			Entity: idStr,
			ID:     machineID,
			Data:   data,
		})
	}
}

// fsmTransitionSource composes the element-level event source for an FSM
// transition from its intrinsic coordinates (from-state, input, to-state). The
// transition has no author-assigned name in the current model, so these
// coordinates identify which transition fired. Element-level transition naming
// is recorded as future work in docs/EVENT_PENDING.md §6b.
func fsmTransitionSource(previous, input, current string) string {
	return "fsm/" + previous + ":" + input + ":" + current
}

// fireFSMStepEvent dispatches one fsm.step event per committed state transition,
// regardless of whether the transition produced any Mealy output. This is the
// latch a subscriber uses to react to a state change as such. Best-effort, fired
// post-commit, like fireFSMOutputEvents.
//
// The previous/current/terminal/vars facts are a free byproduct of the walk
// (which must read prior state to evaluate guards), so the step event carries
// the full state delta at no additional cost. When the step occurred inside a
// commit, the caller additionally attaches the affected-entity REF context via
// the affected argument (nil for a standalone walk).
//
// See docs/EVENT_MODEL.md §2.2 and §3.3.
func (s *Server) fireFSMStepEvent(r *http.Request, machineID int64, input, previous, current string, terminal bool, vars map[string]interface{}, affected []map[string]interface{}, definition interface{}) {
	tenantID := getTenantIDNumeric(r.Context())
	idStr := strconv.FormatInt(machineID, 10)
	data := map[string]interface{}{
		"machine_id": machineID,
		"previous":   previous,
		"current":    current,
		"terminal":   terminal,
	}
	if vars != nil {
		data["vars"] = vars
	}
	if len(affected) > 0 {
		data["affected"] = affected
	}
	if definition != nil {
		data["definition"] = definition
	}
	s.dispatchEvent(tenantID, event{
		Type:   "fsm.step",
		Source: fsmTransitionSource(previous, input, current),
		Entity: idStr,
		ID:     machineID,
		Data:   data,
	})
}

// fireCommitAppliedEvent dispatches one commit.applied event after a commit
// transaction has committed successfully. It carries the affected-entity REFs
// (with created/version outcome facts) and a copy of the committed request.
//
// Because this fires only after the atomic transaction succeeds, the request
// copy is an accurate record of what was committed, not merely what was
// requested — there is no partial-application state. See docs/EVENT_MODEL.md §3.
//
// affected is the list of {ref, created, version} maps for every entity the
// commit wrote; request is the marshalled commit request as applied.
func (s *Server) fireCommitAppliedEvent(r *http.Request, affected []map[string]interface{}, request interface{}) {
	tenantID := getTenantIDNumeric(r.Context())
	data := map[string]interface{}{
		"affected": affected,
		"request":  request,
	}
	// Entity/ID identify the primary updated entity when present; the full set
	// is in data.affected. Entity is the entity name (string); ID is the native
	// numeric id, stringified only if referenced via {{event.id}}.
	entityName := ""
	var idVal interface{}
	if len(affected) > 0 {
		if ref, ok := affected[0]["ref"].(map[string]interface{}); ok {
			if e, ok := ref["entity"].(string); ok {
				entityName = e
			}
			if idv, ok := ref["id"].(int64); ok {
				idVal = idv
			}
		}
	}
	s.dispatchEvent(tenantID, event{
		Type:   "commit.applied",
		Entity: entityName,
		ID:     idVal,
		Data:   data,
	})
}

func (s *Server) eventStore(tenantID tenant.TenantID) (storage.Store, bool) {
	store, err := s.storeForTenant(tenantID)
	if err != nil {
		return nil, false
	}
	if _, ok := store.(storage.WriterDBProvider); !ok {
		return nil, false
	}
	return store, true
}

// matchEventDefs returns all subscriptions for the tenant whose event_type
// matches ev.Type.
func (s *Server) matchEventDefs(tenantID tenant.TenantID, eventType string) ([]eventDef, error) {
	store, err := s.storeForTenant(tenantID)
	if err != nil {
		return nil, err
	}
	wdp, ok := store.(storage.WriterDBProvider)
	if !ok {
		return nil, fmt.Errorf("storage does not support v2 events")
	}
	rows, err := wdp.WriterDB().QueryContext(context.Background(), `
		SELECT id, event_type, action_type, config_json, execution, created_at
		FROM event_defs WHERE tenant_id = ? AND event_type = ? ORDER BY id`,
		tenantID, eventType)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var subs []eventDef
	for rows.Next() {
		var sub eventDef
		var cfg string
		if err := rows.Scan(&sub.ID, &sub.EventType, &sub.ActionType, &cfg, &sub.Execution, &sub.CreatedAt); err != nil {
			return nil, err
		}
		sub.Config = json.RawMessage(cfg)
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// runAction executes one subscription's action against the event and returns a
// (status, detail) pair for the delivery log. status is "delivered" or "failed".
func (s *Server) runAction(tenantID tenant.TenantID, sub eventDef, ev event, store storage.Store) (string, string) {
	cfg := map[string]interface{}{}
	if len(sub.Config) > 0 {
		_ = json.Unmarshal(sub.Config, &cfg)
	}
	switch sub.ActionType {
	case "webhook":
		return s.runWebhookAction(tenantID, sub.ID, cfg, ev)
	case "oql":
		return s.runOQLAction(tenantID, cfg, ev, store)
	default:
		return "failed", "unknown action_type: " + sub.ActionType
	}
}

// runWebhookAction POSTs the (template-substituted) body to the configured URL.
func (s *Server) runWebhookAction(tenantID tenant.TenantID, eventDefID int64, cfg map[string]interface{}, ev event) (string, string) {
	url, _ := cfg["url"].(string)
	if url == "" {
		return "failed", "webhook config missing url"
	}
	url = s.substituteTemplate(tenantID, url, ev)

	// Message: a structured "jsonplate" (resolved via path references against
	// ev.Data) takes priority; then a "body" string with {{...}} token
	// substitution; otherwise a default JSON event envelope. The jsonplate and
	// body-string paths coexist — a def uses whichever its config carries.
	// Whatever this produces becomes the "message" half of the delivered
	// payload; xolu always wraps it with an "origin" provenance block (below).
	var message interface{}
	switch {
	case cfg["jsonplate"] != nil:
		plateJSON, mErr := json.Marshal(cfg["jsonplate"])
		if mErr != nil {
			return "failed", "jsonplate: marshal template: " + mErr.Error()
		}
		rendered, rErr := jsonplate.Render(plateJSON, ev.Data)
		if rErr != nil {
			return "failed", "jsonplate: " + rErr.Error()
		}
		// Unmarshal so the rendered structure nests as JSON, not a string.
		if uErr := json.Unmarshal(rendered, &message); uErr != nil {
			return "failed", "jsonplate: rendered output not valid JSON: " + uErr.Error()
		}
	case func() bool { raw, ok := cfg["body"].(string); return ok && raw != "" }():
		raw, _ := cfg["body"].(string)
		substituted := s.substituteTemplate(tenantID, raw, ev)
		// A body string is intended as a literal JSON message; nest it as parsed
		// JSON when it parses, else carry it as a string.
		if uErr := json.Unmarshal([]byte(substituted), &message); uErr != nil {
			message = substituted
		}
	default:
		message = map[string]interface{}{
			"event": ev.Type, "entity": ev.Entity, "id": ev.ID, "data": ev.Data,
		}
	}

	// origin: the invariant provenance block stamped by xolu on every webhook
	// delivery, so the remote end always knows who fired and why, independent of
	// what the def's message contains. Authored message content cannot suppress
	// or alter it.
	latchSource := ev.Source
	if latchSource == "" {
		latchSource = ev.Type
	}
	payload := map[string]interface{}{
		"origin": map[string]interface{}{
			"agent":              "xolu",
			"agent_version":      version.Version,
			"event_def_id":       eventDefID,
			"event_latch_kind":   ev.Type,
			"event_latch_source": latchSource,
			// fired_at is stamped here, after the originating commit has been
			// applied and immediately before the notification is sent. It is the
			// dispatch time of this notification, intended for downstream
			// timeout, retry, and latency measurement.
			"fired_at": time.Now().UTC().Format(time.RFC3339Nano),
		},
		"message": message,
	}
	bodyBytes, mErr := json.Marshal(payload)
	if mErr != nil {
		return "failed", "marshal payload: " + mErr.Error()
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "failed", "build request: " + err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := webhookClient.Do(req)
	if err != nil {
		return "failed", "post: " + err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return "delivered", fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	return "failed", fmt.Sprintf("HTTP %d", resp.StatusCode)
}

// runOQLAction runs the configured OQL query (templates substituted) against the
// tenant store.
func (s *Server) runOQLAction(tenantID tenant.TenantID, cfg map[string]interface{}, ev event, store storage.Store) (string, string) {
	query, _ := cfg["query"].(string)
	if query == "" {
		return "failed", "oql config missing query"
	}
	query = s.substituteTemplate(tenantID, query, ev)
	if s.oqlJobs == nil {
		return "failed", "oql engine unavailable"
	}
	res, err := s.oqlJobs.ExecuteSyncWithStore(context.Background(), query, store)
	if err != nil {
		return "failed", "oql: " + err.Error()
	}
	rows := 0
	if res != nil {
		rows = len(res.Rows)
	}
	return "delivered", fmt.Sprintf("oql ok, %d rows", rows)
}

// substituteTemplate replaces {{event.*}} and {{gen:name}} tokens in s.
//
//	{{event.type}}        -> ev.Type
//	{{event.entity}}      -> ev.Entity
//	{{event.id}}          -> ev.ID
//	{{event.data.<key>}}  -> ev.Data[key]
//	{{gen:<name>}}        -> a value from the named generator
func (s *Server) substituteTemplate(tenantID tenant.TenantID, in string, ev event) string {
	if !strings.Contains(in, "{{") {
		return in
	}
	replace := func(token string) string {
		token = strings.TrimSpace(token)
		switch {
		case token == "event.type":
			return ev.Type
		case token == "event.entity":
			return ev.Entity
		case token == "event.id":
			// ev.ID is native-typed; stringify here, at the text-interpolation
			// site (the only place a string form is required).
			if ev.ID == nil {
				return ""
			}
			return fmt.Sprintf("%v", ev.ID)
		case strings.HasPrefix(token, "event.data."):
			key := strings.TrimPrefix(token, "event.data.")
			if v, ok := ev.Data[key]; ok {
				return fmt.Sprintf("%v", v)
			}
			return ""
		case strings.HasPrefix(token, "gen:"):
			name := strings.TrimPrefix(token, "gen:")
			if v, err := s.serverGenDispatcher()(tenantID, name); err == nil {
				return v
			}
			return ""
		default:
			return ""
		}
	}
	var out strings.Builder
	for {
		start := strings.Index(in, "{{")
		if start < 0 {
			out.WriteString(in)
			break
		}
		end := strings.Index(in[start:], "}}")
		if end < 0 {
			out.WriteString(in)
			break
		}
		end += start
		out.WriteString(in[:start])
		out.WriteString(replace(in[start+2 : end]))
		in = in[end+2:]
	}
	return out.String()
}

// logDelivery writes one row to event_delivery_log. Best-effort: a logging
// failure is itself only logged, never propagated (the action already ran).
func (s *Server) logDelivery(tenantID tenant.TenantID, subID int64, eventType, status, detail string) {
	store, err := s.storeForTenant(tenantID)
	if err != nil {
		return
	}
	wdp, ok := store.(storage.WriterDBProvider)
	if !ok {
		return
	}
	db := wdp.WriterDB()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	if err := tx.QueryRowContext(context.Background(), `
		INSERT INTO fsm_id_seq (tenant_id, kind, next_id) VALUES (?, 'event_log', 1)
		ON CONFLICT(tenant_id, kind) DO UPDATE SET next_id = next_id + 1
		RETURNING next_id`, tenantID).Scan(&id); err != nil {
		return
	}
	if _, err := tx.ExecContext(context.Background(), `
		INSERT INTO event_delivery_log (tenant_id, id, event_def_id, event_type, status, detail)
		VALUES (?, ?, ?, ?, ?, ?)`, tenantID, id, subID, eventType, status, detail); err != nil {
		return
	}
	_ = tx.Commit()
}
