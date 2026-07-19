// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"encoding/json"
	"fmt"
	"time"
)

// This file defines the typed structures returned by the schema-map endpoints
// (Stage 2 of the client roadmap). The shapes mirror xolu's actual wire format
// as verified against pkg/server/v2_*.go handlers at xolu v0.14.3.
//
// These types are the substrate on which a consumer builds its semantic map;
// see molu Part 2 §4 for one such consumer's use of them.
//
// Structures with subordinate JSON that xolu itself does not fully type
// (generator configurations, event action configurations, entity JSON schemas)
// carry a json.RawMessage rather than a Go struct. The client returns the raw
// truth and lets callers decode it as they see fit; this matches how xolu
// stores those payloads internally.

// ─── Entity schemas ─────────────────────────────────────────────────────────

// EntitySchema describes a registered entity type — its declared fields, its
// JSON Schema, and the subset of fields that carry REF references to other
// entities.
//
// Returned by Client.GetEntitySchema.
//
// The `Schema` field is the raw JSON Schema xolu holds for the entity type.
// Field-level extraction (`Fields`, `Refs`) is done client-side by walking
// the schema; consumers that need more sophisticated inspection can decode
// `Schema` themselves.
type EntitySchema struct {
	// Name is the entity type name (e.g. "users", "orders").
	Name string `json:"name"`
	// Schema is the raw JSON Schema document for the entity, exactly as
	// returned by xolu.
	Schema json.RawMessage `json:"schema"`
	// Fields lists the entity's declared fields with their JSON Schema types.
	Fields []FieldDef `json:"fields,omitempty"`
	// Refs lists the subset of Fields that carry `"format":"ref"` — the
	// reference edges that populate xolu's graph layer.
	Refs []RefFieldDef `json:"refs,omitempty"`
}

// FieldDef is a single declared field on an entity.
type FieldDef struct {
	// Name is the field name as it appears in JSON documents.
	Name string `json:"name"`
	// Type is the JSON Schema type ("string", "integer", "number",
	// "boolean", "object", "array") or a xolu-specific format tag
	// ("decimal", "timestamp", "ref").
	Type string `json:"type"`
	// Required is true when the field is listed under the schema's
	// "required" array.
	Required bool `json:"required,omitempty"`
	// Format is the JSON Schema "format" declaration when present
	// (e.g. "email", "uuid", "date-time"). Empty when absent.
	Format string `json:"format,omitempty"`
}

// RefFieldDef is a field whose value is a reference to another entity.
type RefFieldDef struct {
	// Name is the field name (e.g. "author_id").
	Name string `json:"name"`
	// Target is the entity type the reference points to
	// (e.g. "users"). Extracted from the schema's `"target"` extension
	// when present; empty when the target is polymorphic.
	Target string `json:"target,omitempty"`
}

// ─── FSM definitions and machines ───────────────────────────────────────────

// MachineDefSummary is one entry in the list returned by
// Client.ListMachineDefs. It carries only identity fields; the full
// definition body must be fetched with Client.GetMachineDef.
//
// Wire shape verified against pkg/server/v2_fsm_def_handlers.go handleFSMDefList.
type MachineDefSummary struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// MachineDef is the full body of an FSM definition, as returned by
// Client.GetMachineDef.
//
// Wire shape verified against pkg/server/v2_fsm_def_handlers.go handleFSMDefGet
// and the internal fsmDefinitionSpec / fsmStateSpec / fsmVariableSpec /
// fsmTransitionSpec types in pkg/server/v2_fsm_common.go.
//
// The `Analysis` field is xolu's internal analysis output (reachability,
// determinism, and other structural properties). It is preserved as
// json.RawMessage because its shape is xolu-server-internal and may evolve.
type MachineDef struct {
	// ID is the definition's numeric identifier, tenant-scoped.
	ID int64 `json:"id"`
	// CreatedAt is the timestamp of definition creation.
	CreatedAt string `json:"created_at"`
	// Spec is the definition body — states, transitions, variables.
	Spec MachineSpec `json:"spec"`
	// Analysis is xolu's structural-analysis output for the definition.
	// Kept opaque as json.RawMessage because the shape is server-internal.
	Analysis json.RawMessage `json:"analysis,omitempty"`
}

// MachineSpec is the wire-format definition body. It matches xolu's internal
// fsmDefinitionSpec byte-for-byte.
type MachineSpec struct {
	Name           string                    `json:"name"`
	Description    string                    `json:"description,omitempty"`
	Initial        string                    `json:"initial"`
	Determinism    string                    `json:"determinism"`
	States         map[string]StateDef       `json:"states"`
	Variables      map[string]VariableDef    `json:"variables,omitempty"`
	Transitions    []TransitionDef           `json:"transitions"`
	OutputAlphabet []string                  `json:"output_alphabet,omitempty"`
	LinkedStates   map[string]int64          `json:"linked_states,omitempty"`
	GC             *GCPolicy                 `json:"gc,omitempty"`
	// InputQueries associates an OQL SELECT with an input symbol. See the
	// xolu documentation for the exact semantics of the query-before-walk
	// evaluation pattern.
	InputQueries map[string]string `json:"input_queries,omitempty"`
}

// StateDef is a single state declaration.
type StateDef struct {
	Terminal bool `json:"terminal"`
}

// VariableDef is a single machine-variable declaration.
type VariableDef struct {
	Type    string      `json:"type"`
	Default interface{} `json:"default"`
}

// TransitionDef is a single transition. `From` is a json.RawMessage because
// xolu accepts either a single state name (JSON string) or a list of state
// names (JSON array) on the wire. Use FromStates() to normalise.
//
// `Guard` and `Set` values are T-SQL expression fragments, evaluated at walk
// time by the FSM evaluator. They are returned verbatim so callers can
// display them, log them, or reason about them without re-fetching.
type TransitionDef struct {
	From   json.RawMessage   `json:"from"`
	Input  string            `json:"input"`
	To     string            `json:"to"`
	Guard  string            `json:"guard,omitempty"`
	Output string            `json:"output,omitempty"`
	Set    map[string]string `json:"set,omitempty"`
}

// FromStates normalises the From field into a slice of state names,
// accepting either a JSON string or a JSON array of strings.
func (t *TransitionDef) FromStates() ([]string, error) {
	if len(t.From) == 0 {
		return nil, nil
	}
	var single string
	if err := json.Unmarshal(t.From, &single); err == nil {
		return []string{single}, nil
	}
	var list []string
	if err := json.Unmarshal(t.From, &list); err == nil {
		return list, nil
	}
	return nil, fmt.Errorf("transition 'from' is neither a state name nor a list of state names: %s", t.From)
}

// GCPolicy is the optional GC policy block on a definition.
type GCPolicy struct {
	StalledAfter string `json:"stalled_after,omitempty"`
	DeadAfter    string `json:"dead_after,omitempty"`
	OnGCCollect  string `json:"on_gc_collect,omitempty"`
}

// ─── Generators ─────────────────────────────────────────────────────────────

// GeneratorKind identifies one of xolu's stateless generator kinds. Named
// generators are per-kind lists queryable with Client.ListGenerators.
//
// The four values below match the routes registered in
// pkg/server/v2_handlers.go under /api/v2/gen/{type}.
type GeneratorKind string

const (
	GeneratorUUIDv4 GeneratorKind = "uuid_v4"
	GeneratorUUIDv7 GeneratorKind = "uuid_v7"
	GeneratorCUID   GeneratorKind = "cuid"
	GeneratorULID   GeneratorKind = "ulid"
)

// AllGeneratorKinds enumerates the generator kinds a consumer can query. To
// build a complete generator listing, iterate this slice and call
// Client.ListGenerators for each kind.
var AllGeneratorKinds = []GeneratorKind{
	GeneratorUUIDv4, GeneratorUUIDv7, GeneratorCUID, GeneratorULID,
}

// GeneratorDef is one named generator instance under a given kind, as
// returned by Client.ListGenerators.
//
// The `Config` field is the raw generator configuration; its shape depends
// on the kind and is documented in the xolu generator subsystem.
type GeneratorDef struct {
	Name   string          `json:"name"`
	Config json.RawMessage `json:"config,omitempty"`
}

// Sequence is a single named monotonic sequence, as returned by
// Client.GetSequence.
//
// Note that xolu v0.14.3 does not expose a "list all sequences" endpoint;
// consumers that need to enumerate sequences must know their names out of
// band. See the xolu debt tracker for the planned list endpoint.
// Sequence matches handleSeqGet's wire format. T-32: the earlier shape
// declared `step` and `created_at`, which the server never sends — Step
// was silently zero from Stage 2 through v0.15.3. Breaking change in
// v0.16.0: Step → IncrementBy, CreatedAt dropped, Cycle and the optional
// min/max bounds added.
type Sequence struct {
	Name        string `json:"name"`
	Start       int64  `json:"start"`
	Current     int64  `json:"current"`
	IncrementBy int64  `json:"increment_by"`
	Cycle       bool   `json:"cycle"`
	MinVal      *int64 `json:"min_val,omitempty"`
	MaxVal      *int64 `json:"max_val,omitempty"`
}

// EntityTypeSummary is one entry of GET /api/v1/schemas (T-24): an entity
// type with a registered schema. Name-only by design — the server tracks
// no registration timestamps.
type EntityTypeSummary struct {
	Name string `json:"name"`
}

// SequenceSummary is one entry of GET /api/v2/.../gen/seq (T-25). Field
// names match the wire exactly; note this differs from the Sequence type
// used by GetSequence, whose "step"/"created_at" tags do not match what
// the server actually sends (filed as register item T-32).
type SequenceSummary struct {
	Name        string `json:"name"`
	Current     int64  `json:"current"`
	IncrementBy int64  `json:"increment_by"`
	Cycle       bool   `json:"cycle"`
}

// ─── Event definitions ──────────────────────────────────────────────────────

// EventDef is one registered event subscription, as returned by
// Client.ListEventDefs and Client.GetEventDef.
//
// Wire shape verified against pkg/server/v2_event_handlers.go eventDef.
//
// `Config` is the raw action configuration (webhook URL and headers, OQL
// query, etc.). Its shape depends on ActionType.
type EventDef struct {
	// ID is the subscription's numeric identifier, tenant-scoped.
	ID int64 `json:"id"`
	// EventType is the event type this subscription reacts to
	// (e.g. "entity.updated", "fsm.step", "commit.applied").
	EventType string `json:"event_type"`
	// ActionType names the delivery mechanism ("webhook", "oql").
	ActionType string `json:"action_type"`
	// Config is the raw action configuration.
	Config json.RawMessage `json:"config,omitempty"`
	// Execution declares the delivery mode ("async" today; "sync" is
	// accepted but silently downgraded — see the xolu changelog).
	Execution string `json:"execution"`
	// CreatedAt is the timestamp of subscription creation.
	CreatedAt string `json:"created_at,omitempty"`
}

// ─── v2 availability ────────────────────────────────────────────────────────

// V2Availability is the response of GET /api/v2/ — the map of v2 subsystems
// and their current status. Useful for a consumer to check whether v2 is
// enabled on this server before attempting v2 calls.
//
// The `Subsystems` map is left as json.RawMessage because the per-subsystem
// entries have evolved as new subsystems land; keeping it raw avoids version
// coupling.
type V2Availability struct {
	Version    string          `json:"version"`
	Enabled    bool            `json:"enabled"`
	AsOf       time.Time       `json:"as_of"`
	Warning    string          `json:"warning,omitempty"`
	Subsystems json.RawMessage `json:"subsystems,omitempty"`
}
