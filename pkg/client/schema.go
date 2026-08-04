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
	"regexp"
)

// This file implements the Stage 2 schema-map endpoints — the read-only
// discovery surface a consumer needs to build a picture of xolu's current
// operational shape (entity types, FSM definitions, generators, event defs)
// — plus DefineEntitySchema (T-147, added 2026-08-04), the one write path
// among them: schema registration, which a consumer needs to create new
// entity types programmatically rather than only read existing ones.
//
// This header used to list two xolu-side gaps (no schema-list endpoint, no
// sequence-list endpoint) as still open. Both are resolved elsewhere in this
// same file (ListEntityTypes, ListSequences) -- the list itself had simply
// gone stale, never updated when those methods were added. Caught and
// corrected 2026-08-04 while adding DefineEntitySchema; if a real gap
// resurfaces, document it here again rather than trusting this comment
// without checking.

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

// entityNameRe mirrors the server's own IsValidStrictIdentifier rule
// (pkg/qs, shared with the storage schema validators, per
// validateEntityName's own comment in pkg/server/handlers.go): must
// start with a letter, then letters/digits/underscores only. Checked
// client-side so an invalid name fails fast locally rather than
// costing a round trip for a 400 -- this client's own established
// convention (see bal.go, blob.go). Not imported from pkg/qs directly:
// this client deliberately doesn't depend on xolu's internal packages,
// so the rule is reproduced, not shared -- if the server's own rule
// changes, this needs updating too.
var entityNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

func validateEntityTypeName(entityType string) error {
	if entityType == "" {
		return fmt.Errorf("xolu: entity type name cannot be empty")
	}
	if !entityNameRe.MatchString(entityType) {
		return fmt.Errorf("xolu: invalid entity type name %q: must start with a letter and contain only letters, numbers, and underscores", entityType)
	}
	return nil
}

// DefineEntitySchema registers or updates the schema for an entity
// type -- the write counterpart to GetEntitySchema. schema is the raw
// JSON Schema document (draft-07 shape: {"type":"object",
// "properties":{...},"required":[...]}), sent exactly as given; this
// client does not validate its contents beyond the entity type name
// itself, matching the server's own posture (schema shape validation
// happens server-side, at pkg/server/handlers.go's own
// validateSchemaFieldNames and the JSON Schema loader).
//
// Calling this for an entity type that already has a schema updates
// it -- the server's own response message says "created/updated"
// regardless, and this client makes no distinction between the two
// cases in its own return value either.
//
// Side effects, all server-side and outside this client's control:
// registering a schema creates or updates that entity type's adapted
// table (a column-per-field table optimised for direct SQL querying,
// replacing pure JSON-blob storage) and takes effect for validation
// immediately -- an existing entity of this type that doesn't conform
// to the new schema is not retroactively checked, but any subsequent
// write to it will be.
//
// Hits POST /api/v1/schema/{entity}. Returns *client.Error on non-2xx
// (400 for an invalid entity name or non-identifier field names in
// the schema itself -- D-009's own DDL-injection guard -- 500 if
// schema loading or adapted-table registration fails server-side).
func (c *Client) DefineEntitySchema(ctx context.Context, entityType string, schema map[string]interface{}) error {
	if err := validateEntityTypeName(entityType); err != nil {
		return err
	}
	path := "/schema/" + url.PathEscape(entityType)
	var result struct {
		Message string `json:"message"`
	}
	return c.do(ctx, http.MethodPost, path, schema, &result)
}

// EntityIndex describes one index on an adapted entity type's table.
type EntityIndex struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

// EntityGraphFootprint is one entity type's graph edge counts, present
// only when ListEntities was called with IncludeGraph.
type EntityGraphFootprint struct {
	OutEdges          int64    `json:"out_edges"`
	InEdges           int64    `json:"in_edges"`
	RelationshipTypes []string `json:"relationship_types"`
}

// EntityListEntry describes one entity type that has actual data for
// the current tenant -- whether or not it has a registered schema.
// This is the key difference from ListEntityTypes, which only sees
// entity types with a schema: a type written to without ever calling
// DefineEntitySchema for it appears here with HasSchema=false and
// nothing else missing except Columns/Indexes (which only exist for
// an adapted table).
type EntityListEntry struct {
	EntityType string `json:"entity_type"`
	Count      int64  `json:"count"`
	HasSchema  bool   `json:"has_schema"`
	Adapted    bool   `json:"adapted"`
	// Columns and Indexes are populated only when Adapted is true.
	Columns []string      `json:"columns,omitempty"`
	Indexes []EntityIndex `json:"indexes,omitempty"`
	// Graph is populated only when ListEntities was called with
	// includeGraph -- computing it costs one indexed pass over the
	// graph table per entity type, so it's opt-in server-side, not
	// computed by default.
	Graph *EntityGraphFootprint `json:"graph,omitempty"`
	// FirstSeen/LastUpdate are empty for an adapted entity type --
	// its own table carries no timestamp columns (its columns are
	// derived purely from the registered schema's fields), so there
	// is genuinely nothing to report, not a gap in this client.
	FirstSeen  string `json:"first_seen,omitempty"`
	LastUpdate string `json:"last_update,omitempty"`
}

// ListEntities enumerates every entity type that currently has data
// for this tenant, schemaless or not -- the fuller counterpart to
// ListEntityTypes, which only sees entity types with a registered
// schema. Pass includeGraph=true to also compute each entity type's
// graph edge counts and the relationship names touching it; leave it
// false for the common case, since that computation costs one indexed
// pass over the tenant's graph table per entity type and most callers
// don't need it.
//
// Hits GET /api/v1/entities (?include_graph=true when requested).
// Returns *client.Error on non-2xx (501 if the store isn't SQLite-
// backed).
func (c *Client) ListEntities(ctx context.Context, includeGraph bool) ([]EntityListEntry, error) {
	path := "/entities"
	if includeGraph {
		path += "?include_graph=true"
	}
	var envelope struct {
		Entities []EntityListEntry `json:"entities"`
		Count    int               `json:"count"`
	}
	if err := c.do(ctx, http.MethodGet, path, nil, &envelope); err != nil {
		return nil, err
	}
	if envelope.Entities == nil {
		envelope.Entities = []EntityListEntry{}
	}
	return envelope.Entities, nil
}

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

// CreateMachineDef registers a new FSM definition in the current
// tenant scope. spec is sent exactly as given -- server-side
// validation (determinism declared, states/transitions well-formed,
// exclusivity provable for a non-firstmatch machine) happens before
// anything is persisted; a spec that fails validation comes back as
// *client.Error, not a partially-created definition.
//
// Hits POST /api/v2/fsm/def. Returns *client.Error on non-2xx
// (422 with an XOLU-FSM* code for a spec that fails validation, per
// the same rules ValidateMachineDef checks without storing).
func (c *Client) CreateMachineDef(ctx context.Context, spec MachineSpec) (*MachineDefCreateResult, error) {
	var result MachineDefCreateResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/fsm/def"), spec, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ReplaceMachineDef overwrites an existing FSM definition's spec in
// place, re-validating it the same way CreateMachineDef does.
//
// Affects future machines only: a machine already created against the
// old spec keeps running against the version it was created with. This
// is not retroactive, and there is no way to migrate an in-flight
// machine to a replaced definition -- confirmed directly against the
// server's own route comment ("replace a definition (future machines
// only)"), not assumed.
//
// Hits PUT /api/v2/fsm/def/{id}. Returns *client.Error on non-2xx (404
// with XOLU-FSM001/002/012 if id doesn't exist, 422 for a spec that
// fails validation).
func (c *Client) ReplaceMachineDef(ctx context.Context, id int64, spec MachineSpec) (*MachineDefReplaceResult, error) {
	if id <= 0 {
		return nil, fmt.Errorf("xolu: id must be positive")
	}
	path := fmt.Sprintf("/fsm/def/%d", id)
	var result MachineDefReplaceResult
	if err := c.doURL(ctx, http.MethodPut, c.buildURLv2(path), spec, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DeleteMachineDef removes an FSM definition.
//
// Always permitted: confirmed directly against the server's own route
// comment ("delete a definition (always permitted)") and its handler
// (a plain DELETE with no check against existing machines) -- unlike
// ReplaceMachineDef's own future-machines-only framing might suggest by
// contrast, deleting a definition does NOT check whether any machine
// still references it. This client does not add a safety check of its
// own: no server-side query exists to count machines by definition ID
// to build one against, and adding that is a server-side feature
// request, not something to fake client-side.
//
// Hits DELETE /api/v2/fsm/def/{id}. Returns nil on 204, *client.Error
// on non-2xx (404 with XOLU-FSM001/002/012 if id doesn't exist).
func (c *Client) DeleteMachineDef(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("xolu: id must be positive")
	}
	path := fmt.Sprintf("/fsm/def/%d", id)
	return c.doURL(ctx, http.MethodDelete, c.buildURLv2(path), nil, nil)
}

// ValidateMachineDef checks spec the same way CreateMachineDef would,
// without storing anything.
//
// Always responds 200, valid or not -- validity is data in the
// response body (result.Valid), never encoded as an HTTP status.
// A non-nil error from this method means the request itself failed
// (transport, decode) — it is NOT how an invalid spec is reported.
// Check result.Valid and result.Errors for that; do not use
// errors.As/*client.Error to detect an invalid spec, since a
// correctly-rejected spec never produces one.
//
// Hits POST /api/v2/fsm/def/validate. Returns *client.Error only for a
// genuine transport-level non-2xx, which this endpoint is not expected
// to produce under normal use.
func (c *Client) ValidateMachineDef(ctx context.Context, spec MachineSpec) (*MachineDefValidation, error) {
	var result MachineDefValidation
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/fsm/def/validate"), spec, &result); err != nil {
		return nil, err
	}
	return &result, nil
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
// Use ListSequences to enumerate all sequences; this method retrieves a
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
