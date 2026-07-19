// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

// This file implements the Stage 3 FSM machine operation endpoints — the
// mutating and read surface a consumer needs to instantiate machines, drive
// them through their state graph, and query their state and history.
//
// All methods hit /api/v2/fsm/machine[...] and require xolu's v2 API to be
// enabled on the target server (XOLU_API_V2_ENABLED=true). All are
// tenant-scoped via buildURLv2, so a client configured with WithTenant()
// operates on that tenant.

// ─── Create ────────────────────────────────────────────────────────────────

// CreateMachine instantiates a new FSM machine from a definition. The
// definition must exist in the current tenant scope.
//
// Hits POST /api/v2/fsm/machine. Returns *Machine on 201, *client.Error on
// non-2xx.
//
// The response carries the machine's identity, initial state, and initial
// variable values (from the definition's declared defaults, further modified
// by any overrides supplied in the request).
func (c *Client) CreateMachine(ctx context.Context, req CreateMachineRequest) (*Machine, error) {
	if req.Definition <= 0 {
		return nil, fmt.Errorf("definition is required")
	}
	var m Machine
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/fsm/machine"), req, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ─── List ──────────────────────────────────────────────────────────────────

// ListMachines returns every machine matching an optional filter. A nil
// filter (or a filter with all fields zero) returns every machine in the
// current tenant scope.
//
// Hits GET /api/v2/fsm/machine?[filter query params].
//
// The wire envelope key is "machines" per xolu's handleFSMMachineList.
func (c *Client) ListMachines(ctx context.Context, filter *MachineFilter) ([]MachineSummary, error) {
	u := c.buildURLv2("/fsm/machine")
	if filter != nil {
		q := url.Values{}
		if filter.Definition > 0 {
			q.Set("definition", strconv.FormatInt(filter.Definition, 10))
		}
		if filter.State != "" {
			q.Set("state", filter.State)
		}
		if filter.Ref != "" {
			q.Set("ref", filter.Ref)
		}
		if enc := q.Encode(); enc != "" {
			u = u + "?" + enc
		}
	}

	var wrapper struct {
		Machines []MachineSummary `json:"machines"`
	}
	if err := c.doURL(ctx, http.MethodGet, u, nil, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Machines, nil
}

// ─── Get ───────────────────────────────────────────────────────────────────

// GetMachine fetches a machine's full record — identity, current state, live
// variables.
//
// Hits GET /api/v2/fsm/machine/{id}. Returns *client.Error with code
// XOLU-FSM* on non-2xx (including 404 when the machine does not exist).
func (c *Client) GetMachine(ctx context.Context, id int64) (*Machine, error) {
	if id <= 0 {
		return nil, fmt.Errorf("id must be positive")
	}
	path := fmt.Sprintf("/fsm/machine/%d", id)
	var m Machine
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2(path), nil, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ─── Patch ─────────────────────────────────────────────────────────────────

// PatchMachine applies overrides to a machine's spec snapshot. Live state,
// live variable values, and history are preserved unchanged.
//
// Hits PATCH /api/v2/fsm/machine/{id}. Returns *client.Error carrying an
// XOLU-FSM* validation code on non-2xx.
//
// The returned *Machine reflects the patched machine (identity, current
// state, live vars) — the underlying snapshot spec is not echoed back;
// callers who need the post-patch spec should re-fetch the machine's
// definition or read the snapshot from xolu's storage tier directly.
func (c *Client) PatchMachine(ctx context.Context, id int64, req PatchMachineRequest) (*Machine, error) {
	if id <= 0 {
		return nil, fmt.Errorf("id must be positive")
	}
	path := fmt.Sprintf("/fsm/machine/%d", id)
	var m Machine
	if err := c.doURL(ctx, http.MethodPatch, c.buildURLv2(path), req, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ─── Delete ────────────────────────────────────────────────────────────────

// DeleteMachine removes a machine and all its history from the current
// tenant scope.
//
// Hits DELETE /api/v2/fsm/machine/{id}. Returns nil on 204, *client.Error
// on non-2xx (404 with XOLU-FSM003 when the machine does not exist).
func (c *Client) DeleteMachine(ctx context.Context, id int64) error {
	if id <= 0 {
		return fmt.Errorf("id must be positive")
	}
	path := fmt.Sprintf("/fsm/machine/%d", id)
	return c.doURL(ctx, http.MethodDelete, c.buildURLv2(path), nil, nil)
}

// ─── Walk ──────────────────────────────────────────────────────────────────

// WalkMachine drives a machine through one transition. The input symbol is
// required; the payload is available to guards and set-clauses under the
// "payload." prefix during evaluation.
//
// Hits POST /api/v2/fsm/machine/{id}/walk.
//
// On success returns *WalkResult with the previous and current states, the
// terminal flag, any Mealy outputs emitted, the post-walk variable map, and
// the history row id.
//
// On rejection returns *client.Error with an XOLU-FSM* code. Common
// rejection codes:
//
//   XOLU-FSM003  machine not found
//   XOLU-FSM004  no transition for (state, input)
//   XOLU-FSM005  guard rejected the transition
//   XOLU-FSM006  machine is in a terminal state (no further walks)
//   XOLU-FSM007  input query failed
//   XOLU-FSM008  storage error inside the walk transaction
//
// Callers can dispatch on Error.Code to distinguish these.
func (c *Client) WalkMachine(ctx context.Context, id int64, req WalkRequest) (*WalkResult, error) {
	if id <= 0 {
		return nil, fmt.Errorf("id must be positive")
	}
	if req.Input == "" {
		return nil, fmt.Errorf("input is required")
	}
	path := fmt.Sprintf("/fsm/machine/%d/walk", id)
	var res WalkResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2(path), req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ─── State ─────────────────────────────────────────────────────────────────

// GetMachineState returns just the current state and terminal flag for a
// machine — a lightweight probe compared to GetMachine.
//
// Hits GET /api/v2/fsm/machine/{id}/state.
func (c *Client) GetMachineState(ctx context.Context, id int64) (*MachineState, error) {
	if id <= 0 {
		return nil, fmt.Errorf("id must be positive")
	}
	path := fmt.Sprintf("/fsm/machine/%d/state", id)
	var s MachineState
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2(path), nil, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ─── Result ────────────────────────────────────────────────────────────────

// GetMachineResult returns a convenience summary: current state, terminal
// flag, live vars, and (when terminal) the final transition's output.
//
// Hits GET /api/v2/fsm/machine/{id}/result.
//
// The FinalOutput field is nil until Terminal becomes true. A caller can
// poll this endpoint and act once Terminal flips.
func (c *Client) GetMachineResult(ctx context.Context, id int64) (*MachineResult, error) {
	if id <= 0 {
		return nil, fmt.Errorf("id must be positive")
	}
	path := fmt.Sprintf("/fsm/machine/%d/result", id)
	var r MachineResult
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2(path), nil, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ─── Vars ──────────────────────────────────────────────────────────────────

// GetMachineVars returns the machine's live variable snapshot: each
// variable's current value alongside its declared type and default.
//
// Hits GET /api/v2/fsm/machine/{id}/vars. The response is a flat map
// (no envelope key) — the returned map is keyed by variable name.
func (c *Client) GetMachineVars(ctx context.Context, id int64) (map[string]VariableSnapshot, error) {
	if id <= 0 {
		return nil, fmt.Errorf("id must be positive")
	}
	path := fmt.Sprintf("/fsm/machine/%d/vars", id)
	out := map[string]VariableSnapshot{}
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2(path), nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ─── Transitions ───────────────────────────────────────────────────────────

// GetMachineTransitions returns the input symbols for which a transition
// from the machine's current state exists.
//
// Hits GET /api/v2/fsm/machine/{id}/transitions.
//
// Guards are not pre-evaluated: an input appears here if any transition
// names it from the current state, whether or not that transition's guard
// would currently permit the walk. Callers wanting a proven-permissible
// input must attempt the walk and handle the XOLU-FSM005 rejection.
func (c *Client) GetMachineTransitions(ctx context.Context, id int64) (*AvailableTransitions, error) {
	if id <= 0 {
		return nil, fmt.Errorf("id must be positive")
	}
	path := fmt.Sprintf("/fsm/machine/%d/transitions", id)
	var t AvailableTransitions
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2(path), nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// ─── History ───────────────────────────────────────────────────────────────

// GetMachineHistory returns the ordered walk history for a machine, oldest
// first. The first entry records the machine's creation; each subsequent
// entry records one walk.
//
// Hits GET /api/v2/fsm/machine/{id}/history. The wire envelope is
// {"machine": id, "entries": [...]} — this method returns the inner slice.
//
// xolu v0.14.4 does not support pagination on this endpoint; the full
// history is returned in one response. Consumers of large history sets
// should design their access patterns around this.
func (c *Client) GetMachineHistory(ctx context.Context, id int64) ([]HistoryEntry, error) {
	if id <= 0 {
		return nil, fmt.Errorf("id must be positive")
	}
	path := fmt.Sprintf("/fsm/machine/%d/history", id)
	var wrapper struct {
		Machine int64          `json:"machine"`
		Entries []HistoryEntry `json:"entries"`
	}
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2(path), nil, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Entries, nil
}
