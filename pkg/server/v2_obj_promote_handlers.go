// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_obj_promote_handlers.go — T-121 (wave 10), Stage 3: promote/
// demote (obj-00-design.md §9, obj-01-rest-api.md §5). Two real
// wire-contract corrections against the original proposal, both
// confirmed directly with the team before building, not assumed:
//
//  1. bal has no single-sided decrement/increment — only two-sided
//     Transfer(from, to, amount). The original proposal's own example
//     showed only "bal_account" with no counterparty. This surface
//     adds an explicit "to_account" (promote) / "from_account"
//     (demote) field the caller supplies — deliberately, not a fixed
//     system sink, since the caller is in the best position to know
//     where a promoted/demoted unit's count should land.
//  2. "amount" is a decimal STRING (@B04's own established discipline,
//     enforced identically everywhere else bal appears in this
//     codebase's own dxp surface — decodeDxpParticipantParams's own
//     bal case refuses a bare JSON number outright). The original
//     proposal's own example showed a bare number; corrected here for
//     consistency, not a new rule invented for this endpoint.
//
// Scope, deliberately narrowed: position.kind is always "obj"
// (containment) — the case obj-00-design.md §9's own worked example
// describes (a case pulled off a pallet's bulk count becomes a child
// of something), and the only kind requiring genuine dxp-participant
// integration on obj's own side. loc_leaf/null positioning via
// promote is not built here.
//
// Two parametrized system defs bootstrap lazily, once per tenant, not
// one per call (the team's own insight: dxp/def is already a reusable
// template mechanism via $ref bindings — reuse it as designed rather
// than registering a fresh def on every promote). A third handles
// demote. entity.create vs entity.existing_key need genuinely
// different participant sets (a create leg or not), so promote uses
// two separate defs, chosen by which the caller supplied.

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// ─── System def bootstrap ───────────────────────────────────────────

const (
	objPromoteCreateDefName = "obj.promote.create"
	objPromoteReuseDefName  = "obj.promote.reuse"
	objDemoteDefName        = "obj.demote"
)

func refParam(path string) map[string]interface{} {
	return map[string]interface{}{"$ref": path}
}

func objPromoteCreateDefSpec() dxpDefSpec {
	return dxpDefSpec{
		Name:    objPromoteCreateDefName,
		Pattern: "3ps",
		Participants: []dxpParticipantSpec{
			{ID: "decrement", Primitive: "bal", Op: "transfer", Params: map[string]interface{}{
				"from": refParam("bal_account"), "to": refParam("to_account"),
				"amount": refParam("amount"), "scale": refParam("scale"), "memo": refParam("memo"),
			}},
			{ID: "create_entity", Primitive: "entity", Op: "create", Params: map[string]interface{}{
				"entity": refParam("entity_kind"), "id": refParam("entity_id"), "data": refParam("entity_data"),
			}},
			{ID: "attach_contain", Primitive: "obj", Op: "attach_and_contain", Params: map[string]interface{}{
				"subject_ref": refParam("subject_ref"), "container_ref": refParam("container_ref"),
			}},
		},
		PhaseTTL: dxpPhaseTTLSpec{Reserve: "PT30S"},
	}
}

func objPromoteReuseDefSpec() dxpDefSpec {
	return dxpDefSpec{
		Name:    objPromoteReuseDefName,
		Pattern: "3ps",
		Participants: []dxpParticipantSpec{
			{ID: "decrement", Primitive: "bal", Op: "transfer", Params: map[string]interface{}{
				"from": refParam("bal_account"), "to": refParam("to_account"),
				"amount": refParam("amount"), "scale": refParam("scale"), "memo": refParam("memo"),
			}},
			{ID: "attach_contain", Primitive: "obj", Op: "attach_and_contain", Params: map[string]interface{}{
				"subject_ref": refParam("subject_ref"), "container_ref": refParam("container_ref"),
			}},
		},
		PhaseTTL: dxpPhaseTTLSpec{Reserve: "PT30S"},
	}
}

func objDemoteDefSpec() dxpDefSpec {
	return dxpDefSpec{
		Name:    objDemoteDefName,
		Pattern: "3ps",
		Participants: []dxpParticipantSpec{
			{ID: "detach", Primitive: "obj", Op: "detach", Params: map[string]interface{}{
				"subject_ref": refParam("subject_ref"),
			}},
			{ID: "increment", Primitive: "bal", Op: "transfer", Params: map[string]interface{}{
				"from": refParam("from_account"), "to": refParam("bal_account"),
				"amount": refParam("amount"), "scale": refParam("scale"), "memo": refParam("memo"),
			}},
		},
		PhaseTTL: dxpPhaseTTLSpec{Reserve: "PT30S"},
	}
}

// ensureSystemDxpDef finds an existing def by name for this tenant,
// or registers spec fresh if none exists — lazy, once per tenant, not
// once per promote/demote call (the team's own insight: dxp/def is
// already a reusable, parametrized template; reuse it as designed).
// A small, accepted race on true first-use concurrency: dxp_defs.name
// carries no unique constraint, so two simultaneous first calls could
// each insert their own copy. Rare (this is a one-time-per-tenant
// bootstrap, not a hot path) and harmless (both rows are identical in
// shape; either is equally usable) — not worth a schema migration to
// close.
func (s *Server) ensureSystemDxpDef(ctx context.Context, db *sql.DB, tenantID tenant.TenantID, spec dxpDefSpec) (int64, error) {
	var existingID int64
	err := db.QueryRowContext(ctx, `SELECT id FROM dxp_defs WHERE tenant_id = ? AND name = ? ORDER BY id LIMIT 1`,
		tenantID, spec.Name).Scan(&existingID)
	if err == nil {
		return existingID, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}

	analysis, verr := validateDxpDef(&spec)
	if verr != nil {
		return 0, fmt.Errorf("system def %q failed its own validation: %w", spec.Name, verr)
	}
	specJSON, err := json.Marshal(&spec)
	if err != nil {
		return 0, err
	}
	analysisJSON, err := json.Marshal(analysis)
	if err != nil {
		return 0, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	id, err := allocDXPID(ctx, tx, tenantID, "def")
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO dxp_defs (tenant_id, id, name, spec_json, analysis_json, bindings_schema_json)
		VALUES (?, ?, ?, ?, ?, '{}')`,
		tenantID, id, spec.Name, string(specJSON), string(analysisJSON)); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// ─── Wire shapes (obj-01-rest-api.md §5) ────────────────────────────

type objPromoteEntityReq struct {
	Kind        string                 `json:"kind"`
	ExistingKey *int                   `json:"existing_key,omitempty"`
	Create      map[string]interface{} `json:"create,omitempty"`
}

type objPromotePositionReq struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
}

type objPromoteReq struct {
	BalAccount string                `json:"bal_account"`
	ToAccount  string                `json:"to_account"`
	Amount     string                `json:"amount"` // decimal string, @B04
	Scale      *uint8                `json:"scale,omitempty"`
	Memo       string                `json:"memo,omitempty"`
	Entity     objPromoteEntityReq   `json:"entity"`
	Position   objPromotePositionReq `json:"position"`
}

type objDemoteReq struct {
	Subject     string `json:"subject"`
	BalAccount  string `json:"bal_account"`
	FromAccount string `json:"from_account"`
	Amount      string `json:"amount"` // decimal string, @B04
	Scale       *uint8 `json:"scale,omitempty"`
	Memo        string `json:"memo,omitempty"`
}

func scaleOrZero(s *uint8) uint8 {
	if s == nil {
		return 0
	}
	return *s
}

// ─── Promote ─────────────────────────────────────────────────────────

func (s *Server) handleObjPromote(w http.ResponseWriter, r *http.Request) {
	var req objPromoteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	if req.BalAccount == "" || req.ToAccount == "" || req.Amount == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, "bal_account, to_account, and amount are required")
		return
	}
	if (req.Entity.ExistingKey == nil) == (req.Entity.Create == nil) {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrObjEntitySelectionInvalid,
			"exactly one of entity.existing_key or entity.create must be set")
		return
	}
	if req.Position.Kind != "obj" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON,
			fmt.Sprintf("position.kind must be \"obj\" in this release, got %q", req.Position.Kind))
		return
	}
	pk, pkey, found := cutSubject(req.Position.Subject)
	if !found {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON,
			fmt.Sprintf("position.subject must be \"kind:key\": %q", req.Position.Subject))
		return
	}
	containerRef, err := composedObjSubjectRef(pk, pkey)
	if err != nil {
		s.writeObjError(w, err)
		return
	}

	ctx := r.Context()
	_, tenantID := s.fsmDB(r)

	var subjectRef string
	var defSpec dxpDefSpec
	bindings := map[string]interface{}{
		"bal_account": req.BalAccount, "to_account": req.ToAccount,
		"amount": req.Amount, "scale": scaleOrZero(req.Scale), "memo": req.Memo,
		"container_ref": containerRef,
	}

	if req.Entity.ExistingKey != nil {
		subjectRef = fmt.Sprintf("%s:%d", req.Entity.Kind, *req.Entity.ExistingKey)
		defSpec = objPromoteReuseDefSpec()
	} else {
		sqliteStore, ok := s.getStore(ctx).(*storage.SQLiteStore)
		if !ok {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, "storage does not support v2 entity")
			return
		}
		entityID, err := sqliteStore.AllocateNodeID(ctx, req.Entity.Kind)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
			return
		}
		subjectRef = fmt.Sprintf("%s:%d", req.Entity.Kind, entityID)
		bindings["entity_kind"] = req.Entity.Kind
		bindings["entity_id"] = entityID
		bindings["entity_data"] = req.Entity.Create
		defSpec = objPromoteCreateDefSpec()
	}
	bindings["subject_ref"] = subjectRef

	db, _ := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, "storage does not support v2 dxp")
		return
	}
	defID, err := s.ensureSystemDxpDef(ctx, db, tenantID, defSpec)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	result, err := s.createAndDispatchDxpTxn(ctx, r, tenantID, defID, bindings)
	if err != nil {
		var httpErr *dxpTxnCreateHTTPError
		if errors.As(err, &httpErr) {
			s.writeError(w, httpErr.Status, httpErr.Code, httpErr.Msg)
			return
		}
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	if result.Dispatch.Status == "committed" {
		s.fireObjEvent(r, "obj.promote", subjectRef, map[string]interface{}{
			"bal_account": req.BalAccount, "container_ref": containerRef, "txn_id": result.TxnID,
		})
	}
	s.writeJSON(w, http.StatusCreated, map[string]interface{}{
		"subject":           subjectRef,
		"txn_id":            result.TxnID,
		"status":            result.Dispatch.Status,
		"committed_through": result.Dispatch.CommittedThrough,
		"reason":            result.Dispatch.Reason,
	})
}

// ─── Demote ──────────────────────────────────────────────────────────

func (s *Server) handleObjDemote(w http.ResponseWriter, r *http.Request) {
	var req objDemoteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON, err.Error())
		return
	}
	if req.Subject == "" || req.BalAccount == "" || req.FromAccount == "" || req.Amount == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON,
			"subject, bal_account, from_account, and amount are required")
		return
	}
	sk, skey, found := cutSubject(req.Subject)
	if !found {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidJSON,
			fmt.Sprintf("subject must be \"kind:key\": %q", req.Subject))
		return
	}
	subjectRef, err := composedObjSubjectRef(sk, skey)
	if err != nil {
		s.writeObjError(w, err)
		return
	}

	ctx := r.Context()
	_, tenantID := s.fsmDB(r)
	db, _ := s.fsmDB(r)
	if db == nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, "storage does not support v2 dxp")
		return
	}
	defID, err := s.ensureSystemDxpDef(ctx, db, tenantID, objDemoteDefSpec())
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	bindings := map[string]interface{}{
		"subject_ref": subjectRef, "bal_account": req.BalAccount, "from_account": req.FromAccount,
		"amount": req.Amount, "scale": scaleOrZero(req.Scale), "memo": req.Memo,
	}
	result, err := s.createAndDispatchDxpTxn(ctx, r, tenantID, defID, bindings)
	if err != nil {
		var httpErr *dxpTxnCreateHTTPError
		if errors.As(err, &httpErr) {
			s.writeError(w, httpErr.Status, httpErr.Code, httpErr.Msg)
			return
		}
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	if result.Dispatch.Status == "committed" {
		s.fireObjEvent(r, "obj.demote", subjectRef, map[string]interface{}{
			"bal_account": req.BalAccount, "txn_id": result.TxnID,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"subject":           subjectRef,
		"txn_id":            result.TxnID,
		"status":            result.Dispatch.Status,
		"committed_through": result.Dispatch.CommittedThrough,
		"reason":            result.Dispatch.Reason,
	})
}

// cutSubject splits a "kind:key" shorthand subject string — the
// identical parsing v2_obj_handlers.go's own handleObjMove already
// does inline for the "obj" move-target case (strings.Cut), reused
// here since promote/demote both need it too.
func cutSubject(s string) (kind, key string, found bool) {
	return strings.Cut(s, ":")
}
