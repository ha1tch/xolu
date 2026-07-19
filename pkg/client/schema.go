// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// This file implements the Stage 2 schema-map endpoints — the read-only
// discovery surface a consumer needs to build a picture of xolu's current
// operational shape (entity types, FSM definitions, generators, event defs).
//
// Two xolu-side gaps are called out where they apply:
//
//   1. No `GET /api/v1/schemas` list endpoint exists. Consumers that need to
//      enumerate entity types must know the type names out of band; this
//      client will grow a ListEntityTypes method once xolu ships the list
//      endpoint.
//
//   2. No `GET /api/v2/gen/seq` list endpoint exists. Consumers can retrieve
//      a specific named sequence via GetSequence(name) but cannot enumerate
//      them.
//
// Both gaps are tracked in xolu's technical-debt list. See molu Part 2 §4 for
// how a consumer works around them today.

// ─── Entity schemas ─────────────────────────────────────────────────────────

// GetEntitySchema fetches the schema declaration for a single entity type.
//
// Hits GET /api/v1/schema/{entity}. Returns *client.Error on non-2xx.
//
// The returned EntitySchema carries the raw JSON Schema in `Schema` plus a
// field breakdown (`Fields`, `Refs`) extracted client-side by walking the
// schema. Consumers that need finer control over field extraction can decode
// `Schema` themselves — it is preserved verbatim.
func (c *Client) GetEntitySchema(ctx context.Context, entityType string) (*EntitySchema, error) {
	if entityType == "" {
		return nil, fmt.Errorf("entityType is required")
	}

	// This endpoint is on v1 and uses the existing v1 pipeline.
	path := "/schema/" + url.PathEscape(entityType)

	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return nil, err
	}

	es := &EntitySchema{
		Name:   entityType,
		Schema: raw,
	}
	if err := extractFieldsFromSchema(raw, es); err != nil {
		// Extraction failure is not fatal: the raw schema is still returned
		// and callers who wanted the field breakdown can decode `Schema`
		// themselves. Log as a warning would go here if telemetry were
		// wired; Stage 4 adds it.
		es.Fields = nil
		es.Refs = nil
	}
	return es, nil
}

// extractFieldsFromSchema walks a JSON Schema and pulls out top-level
// property definitions. The schema shape follows draft-07: {"type":"object",
// "properties":{...}, "required":[...]}. Nested schemas, oneOf/allOf/anyOf
// composition, and $ref pointers are not decomposed — those consumers can
// decode `Schema` themselves.
func extractFieldsFromSchema(raw json.RawMessage, es *EntitySchema) error {
	if len(raw) == 0 {
		return nil
	}
	var doc struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	requiredSet := make(map[string]bool, len(doc.Required))
	for _, r := range doc.Required {
		requiredSet[r] = true
	}

	for name, propRaw := range doc.Properties {
		var prop struct {
			Type   string `json:"type"`
			Format string `json:"format"`
			Target string `json:"target"` // xolu ref extension
		}
		if err := json.Unmarshal(propRaw, &prop); err != nil {
			continue // skip unparseable properties
		}
		es.Fields = append(es.Fields, FieldDef{
			Name:     name,
			Type:     prop.Type,
			Required: requiredSet[name],
			Format:   prop.Format,
		})
		if prop.Format == "ref" {
			es.Refs = append(es.Refs, RefFieldDef{
				Name:   name,
				Target: prop.Target,
			})
		}
	}
	return nil
}

// ─── FSM definitions ────────────────────────────────────────────────────────

// ListMachineDefs returns summaries of every FSM definition registered in the
// current tenant scope. Each summary carries id, name, and creation timestamp
// only; use GetMachineDef to fetch the full spec.
//
// Hits GET /api/v2/fsm/def. Requires xolu's v2 API to be enabled
// (XOLU_API_V2_ENABLED=true). Returns *client.Error on non-2xx.
// ListEntityTypes enumerates the entity types that currently have a
// registered schema, sorted by name.
//
// Hits GET /api/v1/schemas (T-24). Returns *client.Error on non-2xx.
func (c *Client) ListEntityTypes(ctx context.Context) ([]EntityTypeSummary, error) {
	var envelope struct {
		Schemas []EntityTypeSummary `json:"schemas"`
		Count   int                 `json:"count"`
	}
	if err := c.do(ctx, http.MethodGet, "/schemas", nil, &envelope); err != nil {
		return nil, err
	}
	if envelope.Schemas == nil {
		envelope.Schemas = []EntityTypeSummary{}
	}
	return envelope.Schemas, nil
}

// ListMachineDefs enumerates every registered FSM definition in the
// current tenant scope.
//
// Hits GET /api/v2/.../fsm/def. Returns *client.Error on non-2xx.
func (c *Client) ListMachineDefs(ctx context.Context) ([]MachineDefSummary, error) {
	var wrapper struct {
		Definitions []MachineDefSummary `json:"definitions"`
	}
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2("/fsm/def"), nil, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Definitions, nil
}

// GetMachineDef fetches the full definition body for a single FSM
// definition by ID.
//
// Hits GET /api/v2/fsm/def/{id}. Returns *client.Error on non-2xx (including
// XOLU-FSM* codes when the definition is not found or is malformed).
func (c *Client) GetMachineDef(ctx context.Context, id int64) (*MachineDef, error) {
	path := fmt.Sprintf("/fsm/def/%d", id)
	var def MachineDef
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2(path), nil, &def); err != nil {
		return nil, err
	}
	return &def, nil
}

// ─── Generators ─────────────────────────────────────────────────────────────

// ListGenerators returns the named generators registered under a single
// generator kind in the current tenant scope. Since xolu keeps a separate
// list per kind, a consumer building a full generator inventory must call
// this method for each of AllGeneratorKinds.
//
// Hits GET /api/v2/gen/{kind}. Returns *client.Error on non-2xx.
//
// Note: named sequences do NOT appear in this listing. Sequences live at a
// separate route (/api/v2/gen/seq) which xolu v0.14.3 exposes only per-name,
// not as a list. See the xolu technical-debt tracker.
func (c *Client) ListGenerators(ctx context.Context, kind GeneratorKind) ([]GeneratorDef, error) {
	if kind == "" {
		return nil, fmt.Errorf("kind is required")
	}
	path := "/gen/" + string(kind)
	var wrapper struct {
		Type       string         `json:"type"`
		Generators []GeneratorDef `json:"generators"`
	}
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2(path), nil, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Generators, nil
}

// GetSequence fetches a single named sequence's current metadata (start,
// step, current value, creation timestamp).
//
// Hits GET /api/v2/gen/seq/{name} (equivalent to /api/v2/seq/{name}, xolu's
// permanent alias). Returns *client.Error on non-2xx.
//
// Consumers cannot enumerate sequences today — this method retrieves a
// specific sequence when the name is known out of band.
func (c *Client) GetSequence(ctx context.Context, name string) (*Sequence, error) {
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	path := "/gen/seq/" + url.PathEscape(name)
	var seq Sequence
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2(path), nil, &seq); err != nil {
		return nil, err
	}
	// The server does not echo the name in the body; ensure the returned
	// value carries the name the caller asked for so callers can distinguish
	// results in a batch.
	if seq.Name == "" {
		seq.Name = name
	}
	return &seq, nil
}

// ListSequences enumerates the tenant's named sequences, sorted by name.
//
// Hits GET /api/v2/.../gen/seq (T-25). Returns *client.Error on non-2xx.
// Returns SequenceSummary values (which match the list wire format), not
// the Sequence type used by GetSequence — see T-32 for the divergence.
func (c *Client) ListSequences(ctx context.Context) ([]SequenceSummary, error) {
	var envelope struct {
		Sequences []SequenceSummary `json:"sequences"`
		Count     int               `json:"count"`
	}
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2("/gen/seq"), nil, &envelope); err != nil {
		return nil, err
	}
	if envelope.Sequences == nil {
		envelope.Sequences = []SequenceSummary{}
	}
	return envelope.Sequences, nil
}

// ─── Event definitions ──────────────────────────────────────────────────────

// ListEventDefs returns every registered event subscription in the current
// tenant scope.
//
// Hits GET /api/v2/event/def. Returns *client.Error on non-2xx.
//
// The wire envelope key is "subscriptions" (not "events" or "definitions");
// see xolu's pkg/server/v2_event_handlers.go handleEventList.
func (c *Client) ListEventDefs(ctx context.Context) ([]EventDef, error) {
	var wrapper struct {
		Subscriptions []EventDef `json:"subscriptions"`
	}
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2("/event/def"), nil, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Subscriptions, nil
}

// GetEventDef fetches a single event subscription by ID.
//
// Hits GET /api/v2/event/def/{id}. Returns *client.Error on non-2xx.
func (c *Client) GetEventDef(ctx context.Context, id int64) (*EventDef, error) {
	path := fmt.Sprintf("/event/def/%d", id)
	var def EventDef
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2(path), nil, &def); err != nil {
		return nil, err
	}
	return &def, nil
}

// ─── v2 availability ────────────────────────────────────────────────────────

// V2Availability fetches the v2 subsystem availability map — a consumer can
// use this to check whether xolu's v2 API is enabled on the target server
// and which subsystems are currently live.
//
// Hits GET /api/v2/. This endpoint always exists when v2 is enabled and
// returns 404 when v2 is disabled. Does not require auth.
func (c *Client) V2Availability(ctx context.Context) (*V2Availability, error) {
	var av V2Availability
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2Root("/"), nil, &av); err != nil {
		return nil, err
	}
	return &av, nil
}
