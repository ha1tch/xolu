// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

// fsm_walk.go — S8: FSM walk executed inside a transaction.
//
// FsmWalkInTx runs a single FSM transition on a caller-supplied *sql.Tx,
// mirroring saveInTx / createInTx: plain SQL on the commit transaction plus
// guard/set expression evaluation via pkg/fsm/eval. Because it runs on the
// same tx as the entity write, a walk inside /commit is atomic with the
// document update — if the walk fails, the whole commit rolls back, and vice
// versa.
//
// The standalone POST /fsm/machine/{id}/walk handler calls this on a tx it
// opens itself, so both the standalone and commit-embedded walk share one
// implementation.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/ha1tch/xolu/pkg/dxp"
	"github.com/ha1tch/xolu/pkg/fsm/eval"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// SeqIncrementTx performs an atomic named-sequence increment on a caller's
// transaction and returns the new current value, without committing. It is
// the tx-scoped core shared by the standalone /seq/{name}/next path and by
// FSM set clauses that contain NEXT VALUE FOR. On an exhausted non-cyclic
// sequence, or a missing sequence, it returns sql.ErrNoRows.
func SeqIncrementTx(ctx context.Context, tx *sql.Tx, tenantID tenant.TenantID, name string) (int64, error) {
	var current, inc int64
	var minVal, maxVal sql.NullInt64
	var cycle int
	err := tx.QueryRowContext(ctx,
		`SELECT current_val, increment_by, min_val, max_val, cycle
		 FROM sequences WHERE tenant_id=? AND name=?`,
		tenantID, name).Scan(&current, &inc, &minVal, &maxVal, &cycle)
	if err != nil {
		return 0, err // sql.ErrNoRows propagates
	}

	next := current + inc
	if maxVal.Valid && next > maxVal.Int64 {
		if cycle == 1 {
			start := int64(1)
			if minVal.Valid {
				start = minVal.Int64
			}
			next = start
		} else {
			return 0, sql.ErrNoRows
		}
	}
	if minVal.Valid && next < minVal.Int64 {
		if cycle == 1 {
			next = maxVal.Int64
		} else {
			return 0, sql.ErrNoRows
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE sequences SET current_val=? WHERE tenant_id=? AND name=?`,
		next, tenantID, name); err != nil {
		return 0, err
	}
	return next, nil
}

// FsmWalkError is a typed error carrying an XOLU-FSM code so the server layer
// can map it to the correct HTTP status. Walk failures inside a commit are
// surfaced by the server as XOLU-FSM008.
type FsmWalkError struct {
	Code    string
	Message string
}

func (e *FsmWalkError) Error() string { return e.Message }

// FsmWalkResult is the outcome of a successful walk.
type FsmWalkResult struct {
	Previous  string
	Current   string
	Terminal  bool
	Outputs   []string
	Vars      map[string]interface{}
	HistoryID int64

	// Definition is the machine's definition spec as a map[string]interface{},
	// exposed read-only to event consumers (the "definition" namespace in FSM
	// event data) so jsonplates can reference definition facts.
	//
	// It is a map (not the fsmDefinitionSpec struct) because queryfy's path
	// engine traverses maps, not structs. It is decoded fresh from the snapshot
	// JSON, so it is an independent copy with no aliasing back to the running
	// machine's spec — no separate deep copy is needed for safety, and the
	// running machine cannot be mutated through it. See docs/EVENT_PENDING.md §6b.
	Definition interface{}
}

// fsm wire-format types needed to read a machine snapshot. These mirror the
// server-layer definition spec; only the fields the walk reads are declared.
type fsmStateSpec struct {
	Terminal bool `json:"terminal"`
}

type fsmVariableSpec struct {
	Type    string      `json:"type"`
	Default interface{} `json:"default"`
}

type fsmTransitionSpec struct {
	From   json.RawMessage   `json:"from"`
	Input  string            `json:"input"`
	To     string            `json:"to"`
	Guard  string            `json:"guard,omitempty"`
	Output string            `json:"output,omitempty"`
	Set    map[string]string `json:"set,omitempty"`
}

func (t *fsmTransitionSpec) fromStates() ([]string, error) {
	if len(t.From) == 0 {
		return nil, fmt.Errorf("transition has no 'from' state")
	}
	var single string
	if err := json.Unmarshal(t.From, &single); err == nil {
		return []string{single}, nil
	}
	var list []string
	if err := json.Unmarshal(t.From, &list); err == nil && len(list) > 0 {
		return list, nil
	}
	return nil, fmt.Errorf("transition 'from' must be a state name or list")
}

type fsmDefinitionSpec struct {
	Name        string                     `json:"name"`
	Initial     string                     `json:"initial"`
	States      map[string]fsmStateSpec    `json:"states"`
	Variables   map[string]fsmVariableSpec `json:"variables,omitempty"`
	Transitions []fsmTransitionSpec        `json:"transitions"`
}

type fsmMachineSnapshot struct {
	Spec fsmDefinitionSpec `json:"spec"`
}

// fsmResolution is what fsmResolveInTx determines without writing
// anything: the transition a walk WOULD take, and the state it
// observed while deciding. observedState is the CAS token
// fsmApplyTransitionInTx uses to detect whether anything moved the
// machine between resolve and apply.
type fsmResolution struct {
	snap          fsmMachineSnapshot
	vars          map[string]interface{}
	observedState string
	transition    *fsmTransitionSpec
}

// fsmResolveInTx performs the read-only half of a walk: snapshot load,
// terminal check (XOLU-FSM005), transition lookup (XOLU-FSM003), and
// guard evaluation (XOLU-FSM004) — selecting which transition would
// fire, without writing state, vars, or history. Split out of the
// former monolithic FsmWalkInTx (T-54, item 19s fsm half) so a dxp
// Reserve can evaluate admission without the memory-only-reservation
// rule (T-54) being violated by an early write.
//
// Guards never call NEXT VALUE FOR or otherwise touch sequences —
// only Set clauses do (see fsmApplyTransitionInTx) — so this half is
// naturally free of side effects; nothing here needs a sequence
// incrementor bound.
func (s *SQLiteStore) fsmResolveInTx(ctx context.Context, tx *sql.Tx, tenantID tenant.TenantID,
	machineID int64, input string, payload map[string]interface{}, queryBindings map[string]interface{}) (*fsmResolution, error) {

	if input == "" {
		return nil, &FsmWalkError{Code: "XOLU-FSM006", Message: "walk requires an input"}
	}

	var snapJSON, varsJSON, state string
	err := tx.QueryRowContext(ctx, `
		SELECT snapshot_json, state, vars_json FROM fsm_machines
		WHERE tenant_id = ? AND id = ?`, tenantID, machineID).
		Scan(&snapJSON, &state, &varsJSON)
	if err == sql.ErrNoRows {
		return nil, &FsmWalkError{Code: "XOLU-FSM002", Message: "machine not found"}
	}
	if err != nil {
		return nil, &FsmWalkError{Code: "XOLU-FSM006", Message: err.Error()}
	}

	var snap fsmMachineSnapshot
	if err := json.Unmarshal([]byte(snapJSON), &snap); err != nil {
		return nil, &FsmWalkError{Code: "XOLU-FSM006", Message: err.Error()}
	}
	vars := map[string]interface{}{}
	if err := json.Unmarshal([]byte(varsJSON), &vars); err != nil {
		return nil, &FsmWalkError{Code: "XOLU-FSM006", Message: err.Error()}
	}

	// Terminal check.
	if ss, ok := snap.Spec.States[state]; ok && ss.Terminal {
		return nil, &FsmWalkError{Code: "XOLU-FSM005", Message: "machine is in terminal state " + state}
	}

	// Candidate transitions for (state, input), in definition order. With
	// multiple edges on the same (state, input) disambiguated by guards, the
	// walk fires the first whose guard passes. This supports validator-style
	// machines (accept / reject-invalid / reject-missing edges on one input).
	candidates := findWalkTransitions(&snap.Spec, state, input)
	if len(candidates) == 0 {
		return nil, &FsmWalkError{
			Code:    "XOLU-FSM003",
			Message: "no transition for input " + strconv.Quote(input) + " from state " + strconv.Quote(state),
		}
	}

	// Select the first candidate whose guard passes. An empty guard always
	// passes. If every candidate's guard is false, the transition is rejected
	// (XOLU-FSM004) — this preserves the single-edge behaviour as the special
	// case of one candidate.
	var transition *fsmTransitionSpec
	var lastRejectedGuard string
	for _, cand := range candidates {
		if cand.Guard == "" {
			transition = cand
			break
		}
		ev := eval.New()
		pass, gerr := eval.EvalGuardWithQuery(ev, cand.Guard, vars, payload, queryBindings)
		if gerr != nil {
			return nil, &FsmWalkError{Code: "XOLU-FSM011", Message: "guard evaluation failed: " + gerr.Error()}
		}
		if pass {
			transition = cand
			break
		}
		lastRejectedGuard = cand.Guard
	}
	if transition == nil {
		var msg string
		if len(candidates) == 1 {
			msg = "transition guard rejected: " + lastRejectedGuard
		} else {
			msg = "no transition guard matched for input " + strconv.Quote(input) +
				" from state " + strconv.Quote(state)
		}
		return nil, &FsmWalkError{Code: "XOLU-FSM004", Message: msg}
	}

	return &fsmResolution{snap: snap, vars: vars, observedState: state, transition: transition}, nil
}

// fsmApplyTransitionInTx performs the write half of a walk: set-clause
// evaluation (including NEXT VALUE FOR on this tx, XOLU-FSM011), a
// CAS state advance, and history append. The UPDATE is compare-and-
// swap on r.observedState — the state fsmResolveInTx saw — so a
// machine moved by anything else between resolve and apply (a
// competing dxp Execute under optimistic weight, most plausibly)
// fails the CAS rather than silently overwriting a decision made
// against stale data. This is fsm's analogue of the T-34 CAS
// discipline; fsm never had its own before this split.
func (s *SQLiteStore) fsmApplyTransitionInTx(ctx context.Context, tx *sql.Tx, tenantID tenant.TenantID,
	machineID int64, r *fsmResolution, input string, payload map[string]interface{},
	queryBindings map[string]interface{}) (*FsmWalkResult, error) {

	transition := r.transition
	newState := transition.To

	// Set clauses (NEXT VALUE FOR runs on this same tx).
	newVars := make(map[string]interface{}, len(r.vars))
	for k, v := range r.vars {
		newVars[k] = v
	}
	if len(transition.Set) > 0 {
		ev := eval.New()
		// Set clauses may read walk payload fields (e.g. "@expected =
		// payload.len"), the same as guards. Bind payload before evaluating.
		ev.BindPayload(payload)
		// Set clauses may also read transition pre-query columns (query.<col>).
		if queryBindings != nil {
			ev.BindQuery(queryBindings)
		}
		// Bind a tx-scoped sequence incrementor so NEXT VALUE FOR runs inside
		// this walk transaction.
		ev.SetSeqIncrementor(func(name string) (int64, error) {
			return SeqIncrementTx(ctx, tx, tenantID, name)
		})
		for varName, expr := range transition.Set {
			val, serr := eval.EvalSetWithSeq(ev, expr, newVars)
			if serr != nil {
				return nil, &FsmWalkError{Code: "XOLU-FSM011",
					Message: "set clause for " + varName + " failed: " + serr.Error()}
			}
			newVars[stripWalkAt(varName)] = val
			newVars[varName] = val
		}
	}

	// Canonical @-prefixed stored form.
	storedVars := map[string]interface{}{}
	for k, v := range newVars {
		if len(k) > 0 && k[0] == '@' {
			storedVars[k] = v
		}
	}
	for name := range r.snap.Spec.Variables {
		if _, ok := storedVars[name]; !ok {
			if v, ok2 := newVars[stripWalkAt(name)]; ok2 {
				storedVars[name] = v
			}
		}
	}
	storedVarsJSON, _ := json.Marshal(storedVars)

	res, err := tx.ExecContext(ctx, `
		UPDATE fsm_machines SET state = ?, vars_json = ?
		WHERE tenant_id = ? AND id = ? AND state = ?`,
		newState, string(storedVarsJSON), tenantID, machineID, r.observedState)
	if err != nil {
		return nil, &FsmWalkError{Code: "XOLU-FSM006", Message: err.Error()}
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, &FsmWalkError{Code: "XOLU-FSM004",
			Message: "machine moved from " + strconv.Quote(r.observedState) + " before this transition committed"}
	}

	// History.
	var outputs []string
	if transition.Output != "" {
		outputs = []string{transition.Output}
	}
	var outputJSON interface{}
	if len(outputs) > 0 {
		b, _ := json.Marshal(outputs)
		outputJSON = string(b)
	}
	var payloadJSON interface{}
	if len(payload) > 0 {
		b, _ := json.Marshal(payload)
		payloadJSON = string(b)
	}
	histID, err := s.allocFsmIDTx(ctx, tx, tenantID, "history")
	if err != nil {
		return nil, &FsmWalkError{Code: "XOLU-FSM006", Message: err.Error()}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO fsm_history
			(tenant_id, id, machine_id, from_state, to_state, input, payload_json, vars_json, output_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tenantID, histID, machineID, r.observedState, newState, input, payloadJSON, string(storedVarsJSON), outputJSON); err != nil {
		return nil, &FsmWalkError{Code: "XOLU-FSM006", Message: err.Error()}
	}

	terminal := false
	if ss, ok := r.snap.Spec.States[newState]; ok {
		terminal = ss.Terminal
	}

	// definitionMap is the machine's definition spec as a map[string]interface{},
	// exposed read-only to event consumers as the "definition" namespace in FSM
	// event data so jsonplates can reference definition facts (e.g.
	// definition.states.X.terminal).
	//
	// It MUST be a map, not the fsmDefinitionSpec struct: queryfy's path engine
	// traverses maps but not arbitrary structs (a struct path resolves to "field
	// not found"). Decoded independently from r.snap so it carries no aliasing.
	var definitionMap interface{}
	{
		snapJSON, _ := json.Marshal(r.snap)
		var snapForDef struct {
			Spec map[string]interface{} `json:"spec"`
		}
		if json.Unmarshal(snapJSON, &snapForDef) == nil {
			definitionMap = snapForDef.Spec
		}
	}

	return &FsmWalkResult{
		Previous:   r.observedState,
		Current:    newState,
		Terminal:   terminal,
		Outputs:    outputs,
		Vars:       storedVars,
		HistoryID:  histID,
		Definition: definitionMap,
	}, nil
}

// dxpFsmResource is the cache resource key for one machine under a
// tenant's fsm participation — "machine:<id>".
func dxpFsmResource(machineID int64) string {
	return "machine:" + strconv.FormatInt(machineID, 10)
}

// FsmWalkInTx executes one transition for the machine on the given tx.
// It performs: snapshot load, terminal check (XOLU-FSM005), transition
// lookup (XOLU-FSM003), guard evaluation (XOLU-FSM004), state advance,
// set-clause evaluation including NEXT VALUE FOR on this tx
// (XOLU-FSM011), and history append. It does not commit — the caller
// owns the transaction.
//
// When a dxp cache is wired (SetDxpClaims), a live PESSIMISTIC dxp
// claim on this machine refuses the walk before it resolves anything
// (XOLU-FSM004: "machine is locked mid-step" — proposal §5c's fsm
// resolution) — the substrate-wide rule that every write path, not
// only the coordinator, must see dxp's holds. Optimistic claims are
// invisible here by design (§7); a plain walk may race one, and its
// own dxp Validate/Execute discovers the drift via the CAS in
// fsmApplyTransitionInTx.
func (s *SQLiteStore) FsmWalkInTx(ctx context.Context, tx *sql.Tx, tenantID tenant.TenantID,
	machineID int64, input string, payload map[string]interface{}, queryBindings map[string]interface{}) (*FsmWalkResult, error) {

	if claims := s.dxpClaims.Load(); claims != nil {
		tenantKey := tenantID.String()
		claims.Lock(tenantKey)
		defer claims.Unlock(tenantKey)
		for _, c := range claims.ClaimsForLocked(tenantKey, "fsm", dxpFsmResource(machineID)) {
			if c.Weight == dxp.Pessimistic {
				return nil, &FsmWalkError{Code: "XOLU-FSM004",
					Message: "machine is locked by a pending dxp transaction"}
			}
		}
	}

	resolution, err := s.fsmResolveInTx(ctx, tx, tenantID, machineID, input, payload, queryBindings)
	if err != nil {
		return nil, err
	}
	return s.fsmApplyTransitionInTx(ctx, tx, tenantID, machineID, resolution, input, payload, queryBindings)
}

// allocFsmIDTx allocates the next monotonic FSM id for the given kind on tx.
func (s *SQLiteStore) allocFsmIDTx(ctx context.Context, tx *sql.Tx, tenantID tenant.TenantID, kind string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO fsm_id_seq (tenant_id, kind, next_id)
		VALUES (?, ?, 1)
		ON CONFLICT(tenant_id, kind) DO UPDATE SET next_id = next_id + 1
		RETURNING next_id`, tenantID, kind).Scan(&id)
	return id, err
}

// findWalkTransitions returns every transition matching (state, input) in
// definition order. Multiple matches are the guard-disambiguated case: the
// walk evaluates their guards in order and fires the first that passes.
func findWalkTransitions(spec *fsmDefinitionSpec, state, input string) []*fsmTransitionSpec {
	var out []*fsmTransitionSpec
	for i := range spec.Transitions {
		ts := &spec.Transitions[i]
		froms, err := ts.fromStates()
		if err != nil {
			continue
		}
		for _, f := range froms {
			if f == state && ts.Input == input {
				out = append(out, ts)
				break
			}
		}
	}
	return out
}

func stripWalkAt(name string) string {
	if len(name) > 0 && name[0] == '@' {
		return name[1:]
	}
	return name
}
