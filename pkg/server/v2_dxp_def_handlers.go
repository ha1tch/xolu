// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_dxp_def_handlers.go — dxp/def: registration, validation, and the
// ID allocator for definitions and transaction instances (item 20,
// wave 5). Structurally mirrors pkg/server/v2_fsm_def_handlers.go and
// v2_fsm_common.go throughout — checked directly against fsm's real
// schema and validation shape before writing any of this, not
// designed from the doctrine's own worked examples alone. See
// docs/proposals/dxp-coordinator-design.md for the full design
// rationale, including two corrections made while designing this
// (fsm has no version-number field at all, and canonical participant
// ordering does not apply to this coordinator).

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ha1tch/queryfy"
	"github.com/ha1tch/queryfy/builders"
	"github.com/ha1tch/queryfy/builders/jsonschema"
	"github.com/ha1tch/xolu/pkg/bal"
	"github.com/ha1tch/xolu/pkg/cal"
	"github.com/ha1tch/xolu/pkg/dxp"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/jsonplate"
	"github.com/ha1tch/xolu/pkg/loc"
	"github.com/ha1tch/xolu/pkg/obj"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
	"github.com/ha1tch/xolu/pkg/timeseries"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// dxpParticipantSpec is one participant in a dxp/def, matching the
// doctrine's own worked examples' JSON shape exactly (checked
// directly against dxp-composed-commitment.md §3 and §5a).
type dxpParticipantSpec struct {
	ID        string                 `json:"id"`
	Primitive string                 `json:"primitive"`
	Op        string                 `json:"op"`
	Params    map[string]interface{} `json:"params,omitempty"`
}

// dxpPhaseTTLSpec is dxp/def's own phase_ttl block. Both of the
// doctrine's worked examples carry exactly one key, "reserve" — the
// only phase a def author configures. Validate/Execute timing is
// coordinator-owned (Ready()'s own guard, dxp-coordinator-design.md
// §4/§5), never def-configurable — there is deliberately no "execute"
// or "validate" key here to mirror.
type dxpPhaseTTLSpec struct {
	Reserve string `json:"reserve"`
}

// dxpDefSpec is dxp/def's own definition body — the JSON a def author
// POSTs to register.
type dxpDefSpec struct {
	Name           string                 `json:"name"`
	Pattern        string                 `json:"pattern"`
	Participants   []dxpParticipantSpec   `json:"participants"`
	PhaseTTL       dxpPhaseTTLSpec        `json:"phase_ttl"`
	BindingsSchema map[string]interface{} `json:"bindings_schema,omitempty"`
}

// dxpAnalysis is dxp/def's own static-analysis result — mirrors
// fsmAnalysis's role exactly: computed once at registration, marshaled
// into analysis_json, never recomputed (definitions are insert-only
// and immutable, so there is nothing for a persisted result to drift
// from — the same reasoning that makes fsm_definitions.analysis_json
// safe to persist applies here unchanged).
type dxpAnalysis struct {
	// CollapseEligible reflects @D06's own, actually-stated condition
	// (checked directly against dxp-composed-commitment.md §6, not
	// assumed): the participant set is single-tenant. Trivially true
	// for v1 — cross-tenant dxp does not exist yet (T-54's parked
	// item) — so this field is currently always true. It exists now,
	// ahead of that item landing, so nothing about dxp_defs' own
	// schema needs to change when it does; only this computation
	// gains a real branch.
	CollapseEligible bool `json:"collapse_eligible"`
	// EngineHomogeneous is a SEPARATE fact from CollapseEligible,
	// deliberately not conflated with it (this document corrected
	// exactly this conflation once already — see
	// dxp-coordinator-design.md §8). @D06 says nothing about engine
	// type; this field is this validator's own, explicit, named
	// inference: does the participant set include any non-SQL
	// (Pebble-backed) primitive. A `*pebble.Batch` cannot join "one
	// SQL transaction" regardless of tenant scope, so a def with any
	// non-SQL participant cannot use the collapsed path even when
	// CollapseEligible is true — the coordinator must consult BOTH
	// fields, not just @D06's own.
	EngineHomogeneous bool     `json:"engine_homogeneous"`
	Warnings          []string `json:"warnings,omitempty"`
}

// dxpValidationError mirrors fsmValidationError's shape and role
// exactly — a structured rejection at registration, always
// XOLU-DXP006 (dxp's own reserved code for exactly this, confirmed to
// already exist in pkg/errors before this was written), with the
// specific reason carried in Message rather than a dedicated
// sub-code — the same "one code, many messages" convention fsm's own
// XOLU-FSM006 already uses for every distinct structural failure.
type dxpValidationError struct {
	Code    string
	Message string
}

func (e *dxpValidationError) Error() string { return e.Code + ": " + e.Message }

// dxpPrimitiveOps names every currently-registered dxp primitive and
// the op names its own adapter actually supports — checked directly
// against each adapter's concrete OpParams type and Primitive()
// method (pkg/bal/dxp_adapter.go, pkg/cal/dxp_adapter.go,
// pkg/storage/{entity,fsm}_dxp_adapter.go), not assumed from the
// doctrine's worked examples alone. entity's two ops map to two
// different concrete Go types (EntityAppendParams for "create",
// EntityUpdateParams for "update") — "create" here deliberately
// matches the doctrine's own worked-example op name rather than
// entity's internal Go type name ("Append"): the type name matches
// entity's own CommitAppend vocabulary for internal consistency, but
// the def-facing op string is a different, external-API audience,
// and "create" is the more legible choice for anyone writing a def.
//
// dxpPrimitiveOps is the registry of primitive/op pairs a dxp def may
// name. ts's own "append" landed alongside its adapter (T-86) and the
// phased execution path (T-105) — the two pieces that make it
// genuinely dispatchable, not just registrable.
var dxpPrimitiveOps = map[string][]string{
	"bal":    {"transfer"},
	"cal":    {"book"},
	"entity": {"create", "update"},
	"fsm":    {"transition"},
	"loc":    {"move"},
	"obj":    {"attach_and_contain", "detach"},
	"ts":     {"append"},
}

// dxpEngineOf names which storage engine backs each primitive's
// guard-bearing write, for dxpAnalysis.EngineHomogeneous's own
// computation. cal is "sql" here specifically because only H1 (the
// guard-bearing booking record) is a dxp participant — H3 (the Pebble
// occupancy index) is deliberately outside dxp's reach entirely
// (T-83, cal's own adapter doc). ts is "pebble" — the first and only
// non-SQL entry, which is what makes EngineHomogeneous ever actually
// false and the phased execution path (T-105) real rather than dead
// code.
var dxpEngineOf = map[string]string{
	"bal":    "sql",
	"cal":    "sql",
	"entity": "sql",
	"fsm":    "sql",
	"loc":    "sql-loc", // deliberately distinct from "sql": loc has its own dedicated per-tenant SQLite file (Stage 0), not the shared tenant primary store bal/cal/fsm/entity all share. EngineHomogeneous checks for the literal string "sql" (see validateDxpDef), so this tag alone forces the phased path whenever loc participates — the identical mechanism ts's own "pebble" tag already established, applied a second time for a second genuinely separate engine.
	"obj":    "sql-obj", // identical reasoning to "sql-loc": obj has its own dedicated per-tenant SQLite file (storelayout.TenantObjDir, T-119), a third genuinely separate SQL engine, not the shared tenant primary store and not loc's own file either.
	"ts":     "pebble",
}

// validateDxpDef checks spec against the full registration-time
// checklist (docs/proposals/dxp-coordinator-design.md's own item-20
// scope), returning a dxpAnalysis on success. Canonical participant
// ordering is deliberately NOT part of this checklist — worked
// through directly and confirmed unnecessary (dxp-coordinator-design.md
// §12): Reserve never blocks, so no circular wait is possible
// regardless of ordering, and there is nothing for an ordering rule
// to protect against in this design.
func validateDxpDef(spec *dxpDefSpec) (*dxpAnalysis, error) {
	if spec.Pattern == "" {
		return nil, &dxpValidationError{Code: "XOLU-DXP006",
			Message: "pattern is required: declare pattern as \"3ps\""}
	}
	if spec.Pattern != "3ps" {
		return nil, &dxpValidationError{Code: "XOLU-DXP006",
			Message: fmt.Sprintf("unsupported pattern %q: only \"3ps\" is implemented (2ps and the wider phase-spectrum are deferred, see SUBSTRATE_DEVELOPMENT_PLAN.md §6 Deferred)", spec.Pattern)}
	}
	if len(spec.Participants) == 0 {
		return nil, &dxpValidationError{Code: "XOLU-DXP006", Message: "definition declares no participants"}
	}

	seenIDs := make(map[string]bool, len(spec.Participants))
	analysis := &dxpAnalysis{CollapseEligible: true, EngineHomogeneous: true}
	for _, p := range spec.Participants {
		if p.ID == "" {
			return nil, &dxpValidationError{Code: "XOLU-DXP006", Message: "a participant has an empty id"}
		}
		if seenIDs[p.ID] {
			return nil, &dxpValidationError{Code: "XOLU-DXP006",
				Message: fmt.Sprintf("participant id %q is declared more than once", p.ID)}
		}
		seenIDs[p.ID] = true

		ops, known := dxpPrimitiveOps[p.Primitive]
		if !known {
			return nil, &dxpValidationError{Code: "XOLU-DXP006",
				Message: fmt.Sprintf("participant %q: primitive %q is not a registered dxp primitive", p.ID, p.Primitive)}
		}
		if !stringInSlice(p.Op, ops) {
			return nil, &dxpValidationError{Code: "XOLU-DXP006",
				Message: fmt.Sprintf("participant %q: primitive %q does not support op %q", p.ID, p.Primitive, p.Op)}
		}

		if dxpEngineOf[p.Primitive] != "sql" {
			analysis.EngineHomogeneous = false
		}

		// Each participant's params is itself a jsonplate template —
		// {"$ref": "path"} references into the eventual bindings
		// payload, resolved at instance-creation time. Validated here
		// (structural only: valid JSON, every $ref carries a string
		// path) so a malformed template is refused at registration,
		// never discovered later at instantiation.
		if len(p.Params) > 0 {
			paramsJSON, err := json.Marshal(p.Params)
			if err != nil {
				return nil, &dxpValidationError{Code: "XOLU-DXP006",
					Message: fmt.Sprintf("participant %q: params: %v", p.ID, err)}
			}
			if err := jsonplate.Validate(paramsJSON); err != nil {
				return nil, &dxpValidationError{Code: "XOLU-DXP006",
					Message: fmt.Sprintf("participant %q: params: %v", p.ID, err)}
			}
		}
	}

	if spec.PhaseTTL.Reserve == "" {
		return nil, &dxpValidationError{Code: "XOLU-DXP006", Message: "phase_ttl.reserve is required"}
	}
	if _, err := parsePhaseTTL(spec.PhaseTTL.Reserve); err != nil {
		return nil, &dxpValidationError{Code: "XOLU-DXP006",
			Message: fmt.Sprintf("phase_ttl.reserve %q: %v", spec.PhaseTTL.Reserve, err)}
	}

	// bindings_schema, if present, must itself compile as a valid
	// JSON Schema — the same compilation pkg/validation's
	// JSONSchemaValidator runs for entity type schemas (checked
	// directly against that file before writing this), reused rather
	// than duplicated. An empty/absent schema is valid — a def with
	// no bindings requirement at all.
	if len(spec.BindingsSchema) > 0 {
		raw, err := json.Marshal(spec.BindingsSchema)
		if err != nil {
			return nil, &dxpValidationError{Code: "XOLU-DXP006", Message: fmt.Sprintf("bindings_schema: %v", err)}
		}
		compiled, convErrs := jsonschema.FromJSON(raw, &jsonschema.Options{StoreUnknown: true})
		for _, e := range convErrs {
			if !e.IsWarning {
				return nil, &dxpValidationError{Code: "XOLU-DXP006",
					Message: fmt.Sprintf("bindings_schema: compilation error at %s: %s", e.Path, e.Message)}
			}
		}
		if _, ok := compiled.(*builders.ObjectSchema); !ok {
			return nil, &dxpValidationError{Code: "XOLU-DXP006",
				Message: fmt.Sprintf("bindings_schema: must compile to an object schema, got %T", compiled)}
		}
	}

	// @D06's own condition (checked directly, not assumed — see this
	// type's own doc comment): single-tenant. Trivially true for v1.
	analysis.CollapseEligible = true

	return analysis, nil
}

func stringInSlice(s string, list []string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// parsePhaseTTL parses the narrow subset of ISO 8601 durations dxp's
// own phase_ttl values actually use — "PT" followed by one or more
// <number><unit> pairs (S/M/H only; no years/months/weeks/days, since
// a phase TTL is always a short, sub-day timeout). Deliberately not a
// general ISO 8601 duration parser — checked directly, no such parser
// exists anywhere in this codebase already, and the doctrine's own
// worked examples ("PT2M", "PT90S") never need anything broader than
// this.
func parsePhaseTTL(s string) (int64, error) {
	if !strings.HasPrefix(s, "PT") {
		return 0, fmt.Errorf("must start with \"PT\" (ISO 8601 time-duration prefix)")
	}
	rest := s[2:]
	if rest == "" {
		return 0, fmt.Errorf("no duration value after \"PT\"")
	}

	var totalNs int64
	for len(rest) > 0 {
		i := 0
		for i < len(rest) && rest[i] >= '0' && rest[i] <= '9' {
			i++
		}
		if i == 0 {
			return 0, fmt.Errorf("expected a number, got %q", rest)
		}
		n, err := strconv.ParseInt(rest[:i], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number %q: %w", rest[:i], err)
		}
		if i >= len(rest) {
			return 0, fmt.Errorf("number %d has no unit (S, M, or H)", n)
		}
		switch rest[i] {
		case 'S':
			totalNs += n * 1_000_000_000
		case 'M':
			totalNs += n * 60 * 1_000_000_000
		case 'H':
			totalNs += n * 3600 * 1_000_000_000
		default:
			return 0, fmt.Errorf("unsupported unit %q (only S, M, H)", string(rest[i]))
		}
		rest = rest[i+1:]
	}
	if totalNs <= 0 {
		return 0, fmt.Errorf("duration must be positive")
	}
	return totalNs, nil
}

// allocDXPID allocates the next id for kind ("def" or "txn") under
// tenantID, via dxp_id_seq — the exact same atomic
// INSERT ... ON CONFLICT DO UPDATE SET next_id = next_id + 1 RETURNING
// pattern as allocFSMID, checked directly against that function before
// writing this one, not approximated from memory.
func allocDXPID(ctx context.Context, tx *sql.Tx, tenantID tenant.TenantID, kind string) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO dxp_id_seq (tenant_id, kind, next_id)
		VALUES (?, ?, 1)
		ON CONFLICT(tenant_id, kind) DO UPDATE SET next_id = next_id + 1
		RETURNING next_id`, tenantID, kind).Scan(&id)
	return id, err
}

// writeDxpValidationError mirrors writeFSMValidationError exactly.
// Every validateDxpDef refusal is XOLU-DXP006 (dxp's own reserved
// code for exactly this, confirmed to already exist in pkg/errors
// before any of this was written) — always HTTP 422, matching
// XOLU-FSM006's own bucket in fsmErrorStatus, since a registration-
// time structural rejection is the same kind of failure fsm's own
// definition validation already reports the same way.
func (s *Server) writeDxpValidationError(w http.ResponseWriter, ve *dxpValidationError) {
	s.writeError(w, http.StatusUnprocessableEntity, xoluerr.Code(ve.Code), ve.Message)
}

// ─── POST /dxp/def — create ─────────────────────────────────────────────────

// handleDxpDefCreate mirrors handleFSMDefCreate's exact structure —
// decode, validate, allocate id, insert, respond — checked directly
// against that handler before writing this one, not approximated.
// Reuses s.fsmDB(r) directly rather than duplicating it: despite the
// name, it does nothing fsm-specific — it resolves the tenant's
// WriterDB and numeric tenant id generically, for any primitive.
func (s *Server) handleDxpDefCreate(w http.ResponseWriter, r *http.Request) {
	var spec dxpDefSpec
	if !s.decodeJSON(w, r, &spec) {
		return
	}

	analysis, verr := validateDxpDef(&spec)
	if verr != nil {
		if ve, ok := verr.(*dxpValidationError); ok {
			s.writeDxpValidationError(w, ve)
			return
		}
		s.writeError(w, http.StatusUnprocessableEntity, xoluerr.ErrDXPDefinitionInvalid, verr.Error())
		return
	}

	db, tenantID := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"storage does not support v2 dxp")
		return
	}

	specJSON, err := json.Marshal(&spec)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	analysisJSON, err := json.Marshal(analysis)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	bindingsSchema := spec.BindingsSchema
	if bindingsSchema == nil {
		bindingsSchema = map[string]interface{}{}
	}
	bindingsSchemaJSON, err := json.Marshal(bindingsSchema)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	defer func() { _ = tx.Rollback() }()

	id, err := allocDXPID(r.Context(), tx, tenantID, "def")
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	var createdAt string
	err = tx.QueryRowContext(r.Context(), `
		INSERT INTO dxp_defs (tenant_id, id, name, spec_json, analysis_json, bindings_schema_json)
		VALUES (?, ?, ?, ?, ?, ?)
		RETURNING created_at`,
		tenantID, id, spec.Name, string(specJSON), string(analysisJSON), string(bindingsSchemaJSON)).Scan(&createdAt)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":         id,
		"name":       spec.Name,
		"created_at": createdAt,
		"analysis":   analysis,
	})
}

// ─── POST /dxp/txn — instantiate ────────────────────────────────────────────

// dxpTxnCreateRequest is what POST /dxp/txn accepts — one complete,
// self-contained invocation (dxp-coordinator-design.md's own recorded
// correction: closer to calling a stored procedure than opening a SQL
// transaction; each call independently validated, nothing assembled
// across multiple requests).
type dxpTxnCreateRequest struct {
	DefID    int64                  `json:"def_id"`
	Bindings map[string]interface{} `json:"bindings,omitempty"`
}

// dxpTxnSnapshot is the fully-resolved def cloned into dxp_txn.snapshot_json
// at creation — mirrors fsmMachineSnapshot's own "clone the whole spec,
// not just part of it" principle (checked directly against
// handleFSMMachineCreate before choosing this shape). Participants here
// carry RESOLVED params — every {"$ref": ...} already replaced with its
// bound value — never templates by the time this is stored.
type dxpTxnSnapshot struct {
	Pattern      string               `json:"pattern"`
	Participants []dxpParticipantSpec `json:"participants"`
	PhaseTTL     dxpPhaseTTLSpec      `json:"phase_ttl"`
}

// dxpTxnCreateHTTPError carries the two non-500 outcomes
// createAndDispatchDxpTxn can produce — everything else defaults to
// 500 XOLU-DXP-storage-failed in the caller, matching the original
// inline handler's own behaviour exactly (checked line-by-line before
// this extraction, not approximated).
type dxpTxnCreateHTTPError struct {
	Status int
	Code   xoluerr.Code
	Msg    string
}

func (e *dxpTxnCreateHTTPError) Error() string { return e.Msg }

// dxpTxnCreateResult is createAndDispatchDxpTxn's own return shape —
// enough for handleDxpTxnCreate's own JSON response and for
// promote/demote's own callers (T-121) to build their own.
type dxpTxnCreateResult struct {
	TxnID     int64
	DefID     int64
	CreatedAt string
	Snapshot  dxpTxnSnapshot
	Dispatch  dxpDispatchResult
}

// createAndDispatchDxpTxn is handleDxpTxnCreate's own core, extracted
// so promote/demote (T-121) can reuse it directly with
// programmatically-constructed bindings, rather than duplicating this
// logic. Behaviour-identical to the inline version it replaced —
// look up defID, validate bindings against its schema, render every
// participant's params via jsonplate, allocate a txn id, insert the
// row, commit, dispatch, all in one call, matching dxp/txn's own "one
// complete, stateless invocation" contract (this file's own recorded
// correction).
func (s *Server) createAndDispatchDxpTxn(ctx context.Context, r *http.Request, tenantID tenant.TenantID, defID int64, bindings map[string]interface{}) (dxpTxnCreateResult, error) {
	db, _ := s.fsmDB(r)
	if db == nil {
		return dxpTxnCreateResult{}, fmt.Errorf("storage does not support v2 dxp")
	}

	var specJSON, bindingsSchemaJSON, analysisJSON string
	err := db.QueryRowContext(ctx, `
		SELECT spec_json, bindings_schema_json, analysis_json FROM dxp_defs
		WHERE tenant_id = ? AND id = ?`, tenantID, defID).Scan(&specJSON, &bindingsSchemaJSON, &analysisJSON)
	if err == sql.ErrNoRows {
		return dxpTxnCreateResult{}, &dxpTxnCreateHTTPError{Status: http.StatusNotFound, Code: xoluerr.ErrDXPDefinitionInvalid,
			Msg: fmt.Sprintf("dxp/def %d not found", defID)}
	}
	if err != nil {
		return dxpTxnCreateResult{}, err
	}

	var analysis dxpAnalysis
	if err := json.Unmarshal([]byte(analysisJSON), &analysis); err != nil {
		return dxpTxnCreateResult{}, fmt.Errorf("stored def %d has corrupt analysis_json: %w", defID, err)
	}

	var spec dxpDefSpec
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		return dxpTxnCreateResult{}, fmt.Errorf("stored def %d has corrupt spec_json: %w", defID, err)
	}

	// Validate bindings against the def's own schema — an empty/absent
	// schema means validation passes (matching JSONSchemaValidator's
	// own "no schema means validation passes" behavior, checked
	// directly against pkg/validation before mirroring it here).
	var bindingsSchema map[string]interface{}
	if err := json.Unmarshal([]byte(bindingsSchemaJSON), &bindingsSchema); err != nil {
		return dxpTxnCreateResult{}, fmt.Errorf("stored def %d has corrupt bindings_schema_json: %w", defID, err)
	}
	if len(bindingsSchema) > 0 {
		raw, err := json.Marshal(bindingsSchema)
		if err != nil {
			return dxpTxnCreateResult{}, err
		}
		compiled, convErrs := jsonschema.FromJSON(raw, &jsonschema.Options{StoreUnknown: true})
		for _, e := range convErrs {
			if !e.IsWarning {
				return dxpTxnCreateResult{}, fmt.Errorf("stored bindings_schema for def %d: %s", defID, e.Message)
			}
		}
		objSchema, ok := compiled.(*builders.ObjectSchema)
		if !ok {
			return dxpTxnCreateResult{}, fmt.Errorf("stored bindings_schema for def %d did not compile to an object schema", defID)
		}
		vctx := queryfy.NewValidationContext(queryfy.Strict)
		_ = objSchema.Validate(bindings, vctx)
		if vctx.HasErrors() {
			fieldErrors := vctx.Errors()
			msgs := make([]string, 0, len(fieldErrors))
			for _, fe := range fieldErrors {
				if fe.Path == "" {
					msgs = append(msgs, fe.Message)
				} else {
					msgs = append(msgs, fmt.Sprintf("%s: %s", fe.Path, fe.Message))
				}
			}
			return dxpTxnCreateResult{}, &dxpTxnCreateHTTPError{Status: http.StatusUnprocessableEntity, Code: xoluerr.ErrDXPBindings,
				Msg: strings.Join(msgs, "; ")}
		}
	}

	// Resolve each participant's params via jsonplate — every {"$ref": ...}
	// replaced with its bound value from bindings. A malformed
	// template can't reach this point (validateDxpDef already refused
	// it at registration); an absent path resolves to JSON null rather
	// than erroring (jsonplate's own documented behavior), which
	// Reserve's own admission check will then refuse on, same as any
	// other invalid params.
	resolvedParticipants := make([]dxpParticipantSpec, len(spec.Participants))
	for i, p := range spec.Participants {
		resolved := p
		if len(p.Params) > 0 {
			paramsJSON, err := json.Marshal(p.Params)
			if err != nil {
				return dxpTxnCreateResult{}, err
			}
			renderedJSON, err := jsonplate.Render(paramsJSON, bindings)
			if err != nil {
				return dxpTxnCreateResult{}, fmt.Errorf("participant %q: %w", p.ID, err)
			}
			var rendered map[string]interface{}
			if err := json.Unmarshal(renderedJSON, &rendered); err != nil {
				return dxpTxnCreateResult{}, err
			}
			resolved.Params = rendered
		}
		resolvedParticipants[i] = resolved
	}

	snapshot := dxpTxnSnapshot{
		Pattern:      spec.Pattern,
		Participants: resolvedParticipants,
		PhaseTTL:     spec.PhaseTTL,
	}
	snapshotJSON, err := json.Marshal(&snapshot)
	if err != nil {
		return dxpTxnCreateResult{}, err
	}

	reserveTTL, err := parsePhaseTTL(spec.PhaseTTL.Reserve)
	if err != nil {
		// Already validated at registration; a failure here means the
		// stored def itself is corrupt, not a caller error.
		return dxpTxnCreateResult{}, fmt.Errorf("stored def %d has invalid phase_ttl.reserve: %w", defID, err)
	}
	deadlineNs := ot.Now().UnixNano() + reserveTTL

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return dxpTxnCreateResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	txnID, err := allocDXPID(ctx, tx, tenantID, "txn")
	if err != nil {
		return dxpTxnCreateResult{}, err
	}

	var createdAt string
	err = tx.QueryRowContext(ctx, `
		INSERT INTO dxp_txn (tenant_id, id, dxp_def_id, dxp_def_name, snapshot_json, bindings_schema_json, status, committed_through, deadline_ns)
		VALUES (?, ?, ?, ?, ?, ?, 'active', 0, ?)
		RETURNING created_at`,
		tenantID, txnID, defID, spec.Name, string(snapshotJSON), bindingsSchemaJSON, deadlineNs).Scan(&createdAt)
	if err != nil {
		return dxpTxnCreateResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return dxpTxnCreateResult{}, err
	}

	// Dispatch happens now, in the same call — POST /dxp/txn is one
	// complete, stateless invocation (this file's own recorded
	// correction: closer to a stored procedure call than opening a
	// database transaction), not creation followed by a separate start
	// step.
	result, err := s.dispatchDxpTxn(ctx, r, tenantID, txnID, snapshot, analysis, deadlineNs)
	if err != nil {
		return dxpTxnCreateResult{}, err
	}

	return dxpTxnCreateResult{TxnID: txnID, DefID: defID, CreatedAt: createdAt, Snapshot: snapshot, Dispatch: result}, nil
}

// handleDxpTxnCreate mirrors handleDxpDefCreate's own overall shape —
// decode, look up, validate, resolve, allocate id, insert, respond —
// now a thin wrapper around createAndDispatchDxpTxn (T-121's own
// extraction), translating its returned error into the correct HTTP
// status/code.
func (s *Server) handleDxpTxnCreate(w http.ResponseWriter, r *http.Request) {
	var req dxpTxnCreateRequest
	if !s.decodeJSON(w, r, &req) {
		return
	}

	_, tenantID := s.fsmDB(r)

	result, err := s.createAndDispatchDxpTxn(r.Context(), r, tenantID, req.DefID, req.Bindings)
	if err != nil {
		var httpErr *dxpTxnCreateHTTPError
		if errors.As(err, &httpErr) {
			s.writeError(w, httpErr.Status, httpErr.Code, httpErr.Msg)
			return
		}
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"id":                result.TxnID,
		"def_id":            result.DefID,
		"status":            result.Dispatch.Status,
		"committed_through": result.Dispatch.CommittedThrough,
		"reason":            result.Dispatch.Reason,
		"created_at":        result.CreatedAt,
		"snapshot":          result.Snapshot,
	})
}

// ─── dxp_txn status transitions — the T-34 CAS pattern ─────────────────────

// markDxpTxnTerminal guards dxp_txn's own phase transition — the T-34
// CAS pattern (dxp-coordinator-design.md's own recorded requirement,
// §4a's guard locality: the read and the write must commit together,
// in the same table dxp already owns, never split across a different
// primitive's own machinery — the reasoning behind refusing to host
// this on pkg/fsm, worked through directly earlier in this design).
// Mirrors fsm_walk.go's identical UPDATE-then-RowsAffected shape,
// checked directly against that file before writing this, not
// approximated.
//
// newStatus must be one of the three real terminal states —
// "committed", "released", or "expired" (dxp-composed-commitment.md
// §4's own outcome-uniqueness guard, checked directly — a correction
// to this schema's own original comment, which wrongly listed only
// two). committedThrough is written atomically alongside the
// transition, in the same guarded query — the final, observable value
// at the moment the instance actually reaches its terminal state, not
// tracked incrementally per-participant-commit (dxp-coordinator-
// design.md's own decision: committed_through only needs to be
// durable for the sweep worker's post-mortem observability at expiry,
// never for mid-flight resume, since there is none, per §7's
// no-durability decision).
//
// Returns (false, nil) — not an error — if the instance was not
// 'active' at the moment of the attempt: a competitor already
// transitioned it first. This is the same "moved before this
// transition committed" shape fsm's own CAS reports, an outcome of
// the guard working correctly, not a failure of this call.
func markDxpTxnTerminal(ctx context.Context, tx *sql.Tx, tenantID tenant.TenantID, txnID int64, newStatus string, committedThrough int) (bool, error) {
	if newStatus != "committed" && newStatus != "released" && newStatus != "expired" {
		return false, fmt.Errorf("markDxpTxnTerminal: %q is not a real terminal status (committed, released, expired)", newStatus)
	}
	res, err := tx.ExecContext(ctx, `
		UPDATE dxp_txn SET status = ?, committed_through = ?
		WHERE tenant_id = ? AND id = ? AND status = 'active'`,
		newStatus, committedThrough, tenantID, txnID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

// ─── Per-participant params decoding ────────────────────────────────────────

// dxpMemCache returns the one, long-lived dxp.MemCache shared by every
// participant adapter for tenantID — constructed once, lazily, and
// reused on every subsequent call. Mirrors balRollup/balSealer's own
// pattern exactly (checked directly against balStore before writing
// this, not approximated): a fresh Store gets built per request, but
// a genuinely long-lived resource — here, the in-memory reservation
// cache every adapter must share to actually detect cross-primitive
// resource conflicts — lives once per tenant on the Server itself.
// Without this, each dxp adapter constructed fresh per request would
// get its own, independent MemCache, and no two participants would
// ever see each other's claims at all.
func (s *Server) dxpMemCache(tenantID tenant.TenantID) *dxp.MemCache {
	c, _ := s.dxpCache.LoadOrStore(tenantID, dxp.NewMemCache())
	return c.(*dxp.MemCache)
}

// dxpParticipantRegistry constructs one dxp.Participant adapter per
// currently-registered primitive (bal/cal/fsm/entity/ts — matching
// dxpPrimitiveOps exactly), all sharing the one, tenant-scoped,
// long-lived dxp.MemCache from dxpMemCache. Without a shared cache,
// adapters constructed independently would each get their own,
// isolated MemCache, and no two participants would ever see each
// other's claims — the entire admission-conflict mechanism this
// package exists for would silently do nothing.
//
// Only constructs the primitives named in needed — a real bug found
// by the first end-to-end test that ever actually dispatched a
// transaction: constructing all four (now five) unconditionally meant
// a bal-only def failed outright whenever cal happened to be disabled
// on the server, even though that specific instance never touched
// cal at all. Fixed here rather than by enabling cal everywhere.
//
// bal uses balStore's own existing per-request-store/long-lived-
// resource pattern directly. cal uses calMgr's own Manager, which
// keeps a single Lifecycle+SourceFor per tenant rather than building
// fresh per request — a different, equally real pattern, checked
// directly against v2_cal_handlers.go before using it, not assumed
// to match bal's. fsm and entity share the tenant's own
// *storage.SQLiteStore directly, type-asserted from getStore the
// same way fsmDB does internally, but returning the full store (both
// adapters need its own fsmResolveInTx/saveInTx/createInTx methods,
// not just its *sql.DB). ts uses s.tsManager.StoreFor, type-asserted
// to *timeseries.PebbleStore — timeseries.NewAdapter needs the
// concrete type, not the Store interface, matching how fsm/entity
// need the concrete *storage.SQLiteStore above for the same reason.
func (s *Server) dxpParticipantRegistry(r *http.Request, needed map[string]bool) (map[string]dxp.Participant, tenant.TenantID, error) {
	tenantID := getTenantIDNumeric(r.Context())
	cache := s.dxpMemCache(tenantID)
	reg := make(map[string]dxp.Participant, len(needed))

	if needed["bal"] {
		balSt, err := s.balStore(r)
		if err != nil {
			return nil, tenantID, fmt.Errorf("bal store: %w", err)
		}
		reg["bal"] = bal.NewAdapter(balSt, cache)
	}

	if needed["loc"] {
		locSt, err := s.locStore(r)
		if err != nil {
			return nil, tenantID, fmt.Errorf("loc store: %w", err)
		}
		reg["loc"] = loc.NewAdapter(locSt, cache)
	}

	if needed["obj"] {
		objSt, err := s.objStore(r)
		if err != nil {
			return nil, tenantID, fmt.Errorf("obj store: %w", err)
		}
		reg["obj"] = obj.NewAdapter(objSt, cache)
	}

	if needed["cal"] {
		if s.calMgr == nil {
			return nil, tenantID, fmt.Errorf("cal is not enabled on this server")
		}
		lc, err := s.calMgr.CalFor(tenantID)
		if err != nil {
			return nil, tenantID, fmt.Errorf("cal lifecycle: %w", err)
		}
		calSrc := s.calMgr.SourceFor(tenantID)
		reg["cal"] = cal.NewAdapter(lc, calSrc, cache)
	}

	if needed["fsm"] || needed["entity"] {
		sqliteStore, ok := s.getStore(r.Context()).(*storage.SQLiteStore)
		if !ok {
			return nil, tenantID, fmt.Errorf("storage does not support v2 fsm/entity")
		}
		if needed["fsm"] {
			reg["fsm"] = storage.NewFsmAdapter(sqliteStore, cache)
		}
		if needed["entity"] {
			reg["entity"] = storage.NewEntityAdapter(sqliteStore, cache)
		}
	}

	if needed["ts"] {
		if s.tsManager == nil {
			return nil, tenantID, fmt.Errorf("timeseries is not enabled on this server")
		}
		if !s.tsManager.IsProvisioned(tenantID) {
			return nil, tenantID, fmt.Errorf("timeseries is not provisioned for this tenant")
		}
		tsStoreIface, err := s.tsManager.StoreFor(tenantID)
		if err != nil {
			return nil, tenantID, fmt.Errorf("ts store: %w", err)
		}
		tsStore, ok := tsStoreIface.(*timeseries.PebbleStore)
		if !ok {
			return nil, tenantID, fmt.Errorf("ts store does not support dxp participation (want *timeseries.PebbleStore, got %T)", tsStoreIface)
		}
		reg["ts"] = timeseries.NewAdapter(tsStore, cache)
	}

	return reg, tenantID, nil
}

// decodeDxpParticipantParams constructs the correct concrete
// dxp.OpParams type for one participant, from its already-resolved
// params JSON (every {"$ref": ...} template already substituted by
// jsonplate.Render at instantiation time — this function never sees a
// template, only concrete, bound values).
//
// bal specifically replicates handleBalTransfer's own @B04-aware
// decode path exactly (UseNumber, refuse a bare json.Number for
// "amount", then bal.ParseAmount) — checked directly against that
// handler before writing this, not approximated. A generic
// json.Unmarshal for bal's own params would silently accept a raw
// JSON number and bypass @B04 entirely, since Go's default JSON
// number handling turns a number into float64 with no way to tell
// afterward whether the original source was a string or a number —
// exactly the ambiguity @B04 exists to close off.
//
// tenantID is set explicitly for fsm — never trusted from the
// participant's own params JSON, even though FsmTransitionParams'
// own TenantID field already carries json:"-" (belt and suspenders:
// the tag prevents unmarshal from setting it at all; this is the one
// place it's actually assigned, from the instance's own real tenant).
func decodeDxpParticipantParams(primitive, op string, paramsJSON []byte, tenantID tenant.TenantID) (dxp.OpParams, error) {
	switch primitive {
	case "bal":
		dec := json.NewDecoder(bytes.NewReader(paramsJSON))
		dec.UseNumber()
		var raw map[string]interface{}
		if err := dec.Decode(&raw); err != nil {
			return nil, fmt.Errorf("bal participant params: %w", err)
		}
		if _, isNum := raw["amount"].(json.Number); isNum {
			return nil, fmt.Errorf("bal participant params: amount must be a decimal string, not a JSON number (@B04)")
		}
		amountStr, _ := raw["amount"].(string)
		var scale uint8
		if s, ok := raw["scale"].(json.Number); ok {
			n, err := s.Int64()
			if err != nil || n < 0 || n > 255 {
				return nil, fmt.Errorf("bal participant params: scale %v is not a valid uint8", s)
			}
			scale = uint8(n)
		}
		amount, err := bal.ParseAmount(amountStr, scale)
		if err != nil {
			return nil, fmt.Errorf("bal participant params: %w", err)
		}
		var tp bal.TransferParams
		if err := json.Unmarshal(paramsJSON, &tp); err != nil {
			return nil, fmt.Errorf("bal participant params: %w", err)
		}
		tp.Amount = amount
		return tp, nil

	case "cal":
		var tp cal.CalTransitionParams
		if err := json.Unmarshal(paramsJSON, &tp); err != nil {
			return nil, fmt.Errorf("cal participant params: %w", err)
		}
		return tp, nil

	case "loc":
		// Direct typed decode, no raw-map intermediate — loc's own
		// Stage 0 pinned discipline (pkg/loc/doc.go), the same
		// obligation @B04 forces bal into a bespoke decode path for:
		// DxpMoveParams has no field needing special parsing, so a
		// plain json.Unmarshal already satisfies it, matching cal's
		// own shape exactly rather than bal's.
		var tp loc.DxpMoveParams
		if err := json.Unmarshal(paramsJSON, &tp); err != nil {
			return nil, fmt.Errorf("loc participant params: %w", err)
		}
		return tp, nil

	case "obj":
		// Op-dependent, matching entity's own dual-shape decode
		// (EntityUpdateParams vs EntityAppendParams) — the primitive
		// alone doesn't determine the concrete type, the op does.
		switch op {
		case "attach_and_contain":
			var tp obj.DxpAttachAndContainParams
			if err := json.Unmarshal(paramsJSON, &tp); err != nil {
				return nil, fmt.Errorf("obj participant params: %w", err)
			}
			return tp, nil
		case "detach":
			var tp obj.DxpDetachParams
			if err := json.Unmarshal(paramsJSON, &tp); err != nil {
				return nil, fmt.Errorf("obj participant params: %w", err)
			}
			return tp, nil
		default:
			return nil, fmt.Errorf("obj participant params: unknown op %q", op)
		}

	case "fsm":
		var tp storage.FsmTransitionParams
		if err := json.Unmarshal(paramsJSON, &tp); err != nil {
			return nil, fmt.Errorf("fsm participant params: %w", err)
		}
		tp.TenantID = tenantID
		return tp, nil

	case "ts":
		var tp timeseries.AppendParams
		if err := json.Unmarshal(paramsJSON, &tp); err != nil {
			return nil, fmt.Errorf("ts participant params: %w", err)
		}
		return tp, nil

	case "entity":
		switch op {
		case "update":
			var tp storage.EntityUpdateParams
			if err := json.Unmarshal(paramsJSON, &tp); err != nil {
				return nil, fmt.Errorf("entity participant params: %w", err)
			}
			return tp, nil
		case "create":
			var tp storage.EntityAppendParams
			if err := json.Unmarshal(paramsJSON, &tp); err != nil {
				return nil, fmt.Errorf("entity participant params: %w", err)
			}
			return tp, nil
		default:
			return nil, fmt.Errorf("entity participant params: unknown op %q", op)
		}

	default:
		return nil, fmt.Errorf("no params decoder registered for primitive %q", primitive)
	}
}
