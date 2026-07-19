// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"encoding/json"
)

// This file defines the typed structures returned by the FSM machine
// operation endpoints (Stage 3 of the client roadmap).
//
// Shapes verified against xolu v0.14.4 pkg/server/v2_fsm_machine_handlers.go
// and pkg/server/v2_fsm_walk.go handlers. Where xolu emits an envelope key
// (e.g. `{"machines": [...]}`), the client's method returns the inner slice
// directly and the envelope is handled by the method implementation.

// ─── Machine identity ───────────────────────────────────────────────────────

// MachineSummary is one entry in the list returned by Client.ListMachines.
// The list envelope key is "machines" per xolu's handleFSMMachineList.
type MachineSummary struct {
	// ID is the machine's numeric identifier, tenant-scoped.
	ID int64 `json:"id"`
	// Definition is the fsm_def_id the machine was instantiated from.
	Definition int64 `json:"definition"`
	// DefinitionName is a cached copy of the definition's name at
	// creation time. Persists even if the definition is later renamed or
	// deleted.
	DefinitionName string `json:"definition_name"`
	// State is the machine's current state name.
	State string `json:"state"`
	// Ref is the optional external identifier bound at creation time,
	// e.g. an entity URI. Nil when the machine was created without a ref.
	Ref *string `json:"ref"`
	// CreatedAt is the timestamp of machine creation.
	CreatedAt string `json:"created_at"`
}

// Machine is the full record returned by Client.CreateMachine,
// Client.GetMachine, and Client.PatchMachine. It carries identity, current
// state, and live variable values.
//
// Response shape identical for create/get/patch (verified from source).
type Machine struct {
	// ID is the machine's numeric identifier, tenant-scoped.
	ID int64 `json:"id"`
	// Definition is the fsm_def_id the machine was instantiated from.
	Definition int64 `json:"definition"`
	// DefinitionName is the cached copy of the definition's name.
	DefinitionName string `json:"definition_name"`
	// DefinitionDeleted is true when the source definition has been
	// deleted since machine creation. The machine continues to operate
	// on its self-contained snapshot; this flag is informational.
	DefinitionDeleted bool `json:"definition_deleted"`
	// State is the machine's current state name.
	State string `json:"state"`
	// Vars is the current live variable map. Keys are variable names,
	// values are the raw JSON values as the FSM evaluator stores them.
	Vars map[string]interface{} `json:"vars"`
	// Ref is the optional external identifier bound at creation time,
	// echoed only when present.
	Ref string `json:"ref,omitempty"`
	// CreatedAt is the timestamp of machine creation.
	CreatedAt string `json:"created_at"`
}

// ─── Machine filters ────────────────────────────────────────────────────────

// MachineFilter is an optional set of query parameters for Client.ListMachines.
// A nil filter or a filter with all fields empty returns every machine in the
// current tenant scope.
//
// The three filters map directly to the "definition", "state", and "ref"
// query parameters accepted by GET /api/v2/fsm/machine.
type MachineFilter struct {
	// Definition filters by fsm_def_id. Zero means no filter.
	Definition int64
	// State filters by current state name. Empty string means no filter.
	State string
	// Ref filters by the external identifier bound at creation. Empty
	// string means no filter.
	Ref string
}

// ─── Machine creation ──────────────────────────────────────────────────────

// CreateMachineRequest is the body of Client.CreateMachine. The definition ID
// is required; Ref is optional and binds an external identifier to the
// machine (an entity URI, a business key, a slug — xolu does not interpret it
// beyond using it for lookup and filtering).
//
// Overrides let the caller narrow variable defaults or guard expressions at
// instantiation time without editing the definition. The override map is
// applied to a snapshot copy of the definition and then re-validated as a
// whole; failure returns XOLU-FSM* validation errors.
//
// Note that inline entity creation via the `entity` field is deferred in xolu
// v0.14.4 preview and is not exposed by the client. Bind with `ref` instead.
type CreateMachineRequest struct {
	// Definition is the fsm_def_id to instantiate.
	Definition int64 `json:"definition"`
	// Ref is an optional external identifier bound to the machine.
	Ref string `json:"ref,omitempty"`
	// Overrides narrow variable defaults or guard expressions on a
	// per-machine basis. Nil means no overrides.
	Overrides *MachineOverrides `json:"overrides,omitempty"`
}

// MachineOverrides is the shape of the `overrides` block on
// CreateMachineRequest and PatchMachineRequest. Mirrors xolu's internal
// fsmOverrides in pkg/server/v2_fsm_common.go.
type MachineOverrides struct {
	// Variables overrides variable declarations. Keyed by variable name.
	Variables map[string]VariableDef `json:"variables,omitempty"`
	// Transitions overrides transition guards. Keyed by transition input.
	Transitions map[string]TransitionOverride `json:"transitions,omitempty"`
}

// TransitionOverride carries the overridable fields of a transition. In
// xolu v0.14.4 only Guard is overridable.
type TransitionOverride struct {
	// Guard is the new T-SQL guard expression. Nil means "leave the
	// existing guard in place"; the empty string means "clear the guard".
	Guard *string `json:"guard,omitempty"`
}

// PatchMachineRequest is the body of Client.PatchMachine.
//
// A patch applies overrides to the machine's spec snapshot; live state, live
// variable values, and history are preserved unchanged. Re-validation of the
// resulting snapshot occurs as a whole; failure returns XOLU-FSM* errors and
// no persistent change is made.
type PatchMachineRequest struct {
	Overrides *MachineOverrides `json:"overrides,omitempty"`
}

// ─── Walk ──────────────────────────────────────────────────────────────────

// WalkRequest is the body of Client.WalkMachine.
type WalkRequest struct {
	// Input is the input symbol driving the transition. Required.
	Input string `json:"input"`
	// Payload provides arbitrary key/value data available to guards and
	// set-clauses under the "payload." prefix during evaluation. Nil is
	// permitted; the FSM will use only variables and (optionally) the
	// pre-walk query result.
	Payload map[string]interface{} `json:"payload,omitempty"`
}

// WalkResult is the response of Client.WalkMachine, returned on a successful
// transition.
//
// A rejected walk (no matching transition, guard failed, terminal state, etc.)
// returns *client.Error carrying an XOLU-FSM* code — not a WalkResult with a
// diagnostic. Callers dispatch on the error code.
type WalkResult struct {
	// Previous is the state the machine was in before the walk.
	Previous string `json:"previous"`
	// Current is the state the machine is in after the walk.
	Current string `json:"current"`
	// Terminal is true when Current is a terminal state.
	Terminal bool `json:"terminal"`
	// Outputs is the Mealy output emitted by the transition. Multi-valued
	// because a machine's output alphabet may name several outputs;
	// individual transitions typically emit at most one.
	Outputs []string `json:"outputs"`
	// Vars is the live variable map after set-clauses have applied.
	Vars map[string]interface{} `json:"vars"`
	// HistoryID is the id of the history row recording this walk.
	HistoryID int64 `json:"history_id"`
}

// ─── State ─────────────────────────────────────────────────────────────────

// MachineState is the response of Client.GetMachineState — a lightweight
// current-state snapshot without the full variable map. Use GetMachine for
// the richer view.
type MachineState struct {
	State    string `json:"state"`
	Terminal bool   `json:"terminal"`
}

// ─── Result ────────────────────────────────────────────────────────────────

// MachineResult is the response of Client.GetMachineResult — a
// convenience over state + vars + final transition output.
//
// For a machine that has not yet reached a terminal state, Terminal is false
// and FinalOutput is nil; State and Vars still reflect the current
// non-final values, so a caller can poll this endpoint and act once
// Terminal becomes true.
type MachineResult struct {
	// Machine is the machine's numeric identifier.
	Machine int64 `json:"machine"`
	// State is the current state name.
	State string `json:"state"`
	// Terminal is true when State is a terminal state.
	Terminal bool `json:"terminal"`
	// Vars is the live variable map.
	Vars map[string]interface{} `json:"vars"`
	// FinalOutput is the output emitted by the transition that reached
	// the current state, but only meaningful once Terminal is true.
	// Preserved as raw JSON because xolu emits either a JSON array of
	// output names or an empty array; consumers can decode as needed.
	FinalOutput json.RawMessage `json:"final_output,omitempty"`
}

// ─── Vars ──────────────────────────────────────────────────────────────────

// VariableSnapshot is one entry in the map returned by Client.GetMachineVars.
// Each variable's live value is carried alongside its declared type and the
// default value from the machine's snapshot spec — a caller can distinguish
// "value equal to default" from "value has diverged" without re-fetching the
// definition.
type VariableSnapshot struct {
	Value   interface{} `json:"value"`
	Type    string      `json:"type,omitempty"`
	Default interface{} `json:"default,omitempty"`
}

// ─── Transitions ───────────────────────────────────────────────────────────

// AvailableTransitions is the response of Client.GetMachineTransitions — the
// input symbols for which a transition from the current state exists.
//
// Guards are not pre-evaluated at this endpoint; an input appears here if
// any transition names it from the current state, whether or not that
// transition's guard would currently permit the walk.
type AvailableTransitions struct {
	State  string   `json:"state"`
	Inputs []string `json:"inputs"`
}

// ─── History ───────────────────────────────────────────────────────────────

// HistoryEntry is one row of a machine's history, as returned by
// Client.GetMachineHistory. The list is ordered by history id (ascending).
//
// The very first entry has From=nil, Input=nil, and Note="machine created";
// subsequent entries record each walk with the emitting transition's input
// and payload.
type HistoryEntry struct {
	// ID is the numeric identifier of this history row.
	ID int64 `json:"id"`
	// From is the state the machine was in before this step. Nil for the
	// initial "machine created" entry.
	From *string `json:"from"`
	// To is the state the machine was in after this step. Always set,
	// including on the initial entry (equal to the initial state).
	To string `json:"to"`
	// Input is the input symbol that drove the transition. Nil for the
	// initial entry.
	Input *string `json:"input"`
	// Payload is the raw payload supplied by the walk caller. Preserved
	// as raw JSON because xolu emits arbitrary caller-supplied shapes.
	Payload json.RawMessage `json:"payload,omitempty"`
	// Vars is the live variable map after this step, preserved as raw
	// JSON for the same reason.
	Vars json.RawMessage `json:"vars"`
	// Outputs is the transition's Mealy output, preserved as raw JSON.
	// Omitted when the transition emitted nothing.
	Outputs json.RawMessage `json:"outputs,omitempty"`
	// Note is a human-readable annotation. Present on the initial
	// "machine created" entry; typically nil on walk entries.
	Note string `json:"note,omitempty"`
	// At is the timestamp of this step.
	At string `json:"at"`
}
