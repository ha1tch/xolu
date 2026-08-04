// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/ha1tch/xolu/pkg/dxp"
)

// Compile-time check: EntityAdapter must satisfy dxp.Participant —
// see bal's identical guard (pkg/bal/dxp_adapter.go) for why this was
// added now rather than assumed already covered.
var _ dxp.Participant = (*EntityAdapter)(nil)

// EntityUpdateParams is entity's dxp.OpParams (T-54's typed-per-
// primitive decision): a version-guarded update to one existing
// entity row.
//
// Covers the UPDATE path — the entity named by (Entity, ID) must
// already exist. See EntityAppendParams, below, for CREATE (T-84,
// 2026-07-29) — a distinct admission shape (existence must be false,
// not true; auto-generated ids have no existence to check at all
// before Execute) handled by its own type, dispatched by this
// adapter's own Reserve/Validate/Execute via a type switch on
// dxp.OpParams's concrete type, not a separate Participant.
//
// ExpectVersion mirrors CommitUpdate.Version: nil means unconditional
// (last-writer-wins on Execute, matching Save's own default), non-nil
// makes both Reserve's admission check and Execute's write CAS on the
// stored _version.
type EntityUpdateParams struct {
	Entity        string                 `json:"entity"`
	ID            int                    `json:"id"`
	Data          map[string]interface{} `json:"data"`
	ExpectVersion *int                   `json:"expect_version,omitempty"`
}

// Primitive satisfies dxp.OpParams.
func (EntityUpdateParams) Primitive() string { return "entity" }

// EntityAppendParams is entity's dxp.OpParams for the CREATE path —
// entity's own vocabulary calls this "Append" (matching CommitAppend,
// the non-dxp /commit path's identical type), not "Create"; matched
// here rather than inventing a parallel term.
//
// Two admission shapes, matching CommitAppend's own documented
// contract exactly:
//   - ID != nil: a caller-chosen id. Reserve refuses if that id
//     already exists (ErrAlreadyExists) — the same conflict an
//     UPDATE on the same id would hit, deliberately sharing
//     dxpEntityResource's resource-key namespace with
//     EntityUpdateParams so the two correctly contend for the same
//     row rather than silently coexisting.
//   - ID == nil: server-allocated via the entity type's own sequence
//     (nodeSeqTable, an atomic `next_id = next_id + 1 RETURNING`).
//     There is no id to check for conflict before Execute actually
//     allocates one — two concurrent auto-id creates get two
//     different ids by the sequence's own construction, not by
//     anything this adapter arranges. Reserve still Holds a claim,
//     under a txn-scoped resource key that cannot collide with
//     anything else, purely so the coordinator's own bookkeeping
//     (attendance, Release) stays uniform across every participant
//     regardless of whether that participant has a real resource to
//     guard.
type EntityAppendParams struct {
	Entity string                 `json:"entity"`
	ID     *int                   `json:"id,omitempty"`
	Data   map[string]interface{} `json:"data"`
}

// Primitive satisfies dxp.OpParams.
func (EntityAppendParams) Primitive() string { return "entity" }

// EntityAdapter is entity's dxp.Participant. One per SQLiteStore; safe
// for concurrent use.
type EntityAdapter struct {
	store *SQLiteStore
	cache *dxp.MemCache

	mu      sync.Mutex
	pending map[string]dxp.OpParams // keyed by (txn, participantID) — holds either EntityUpdateParams or EntityAppendParams; see bal.pendingKey for the rationale (T-109)
}

// NewEntityAdapter wires store into cache (SetDxpClaims — shared with
// fsm's adapter if both are registered against the same store) and
// returns an EntityAdapter ready to register with a dxp coordinator
// under the primitive key "entity".
func NewEntityAdapter(store *SQLiteStore, cache *dxp.MemCache) *EntityAdapter {
	store.SetDxpClaims(cache)
	return &EntityAdapter{store: store, cache: cache, pending: make(map[string]dxp.OpParams)}
}

// Reserve admits an update or an append, dispatching on op's concrete
// type (both share the primitive key "entity", so the coordinator
// routes both through this same adapter — not two separate
// Participants). Read-only, per T-54's memory-only-reservation rule.
//
// Admission rule, matching fsm.Adapter's: a live PESSIMISTIC claim on
// a resource refuses any new reservation of either weight (exclusive
// — the row is locked mid-update); a live OPTIMISTIC claim only
// refuses a new PESSIMISTIC one; OPTIMISTIC siblings coexist.
func (a *EntityAdapter) Reserve(ctx context.Context, tenant string, op dxp.OpParams,
	txn, participantID string, deadline int64, w dxp.Weight) (dxp.Claim, error) {

	if got := a.store.config.TenantID.String(); got != tenant {
		return dxp.Claim{}, fmt.Errorf("entity participant: tenant key %q does not match store TenantID %d (want %q)", tenant, a.store.config.TenantID, got)
	}

	switch tp := op.(type) {
	case EntityUpdateParams:
		return a.reserveUpdate(ctx, tenant, tp, txn, participantID, deadline, w)
	case EntityAppendParams:
		return a.reserveAppend(ctx, tenant, tp, txn, participantID, deadline, w)
	default:
		return dxp.Claim{}, fmt.Errorf("entity participant: OpParams is %T, want storage.EntityUpdateParams or storage.EntityAppendParams", op)
	}
}

func (a *EntityAdapter) reserveUpdate(ctx context.Context, tenant string, tp EntityUpdateParams,
	txn, participantID string, deadline int64, w dxp.Weight) (dxp.Claim, error) {

	a.cache.Lock(tenant)
	defer a.cache.Unlock(tenant)

	resource := dxpEntityResource(tp.Entity, tp.ID)
	for _, c := range a.cache.ClaimsForLocked(tenant, "entity", resource) {
		if c.Weight == dxp.Pessimistic || w == dxp.Pessimistic {
			return dxp.Claim{}, fmt.Errorf("entity participant: %s already reserved by a pending dxp transaction", resource)
		}
	}

	version, exists, err := a.store.entityVersion(ctx, tp.Entity, tp.ID)
	if err != nil {
		return dxp.Claim{}, err
	}
	if !exists {
		return dxp.Claim{}, fmt.Errorf("entity participant: %s does not exist (update requires an existing row)", resource)
	}
	if tp.ExpectVersion != nil && *tp.ExpectVersion != version {
		return dxp.Claim{}, ErrConflict
	}

	cl := dxp.Claim{
		Txn: txn, Primitive: "entity", Tenant: tenant, ParticipantID: participantID,
		Resource: resource, Weight: w, Amount: 1, Deadline: deadline,
	}
	if err := a.cache.Hold(cl); err != nil {
		return dxp.Claim{}, err
	}

	a.mu.Lock()
	a.pending[pendingKey(txn, participantID)] = tp
	a.mu.Unlock()
	return cl, nil
}

// reserveAppend admits an EntityAppendParams. Two shapes:
//
// Explicit id: refuses if that id already exists (ErrAlreadyExists,
// matching CommitAppend's own documented contract for the non-dxp
// path exactly) and reserves under the SAME resource-key namespace
// dxpEntityResource gives updates — an update and a create racing for
// the same explicit id correctly contend for one claim, not two
// independent ones that would let both proceed.
//
// Auto-generated id (ID == nil): no id exists to check yet — the
// sequence (nodeSeqTable) allocates one atomically inside Execute's
// own createInTx call, so two concurrent auto-id creates can never
// collide by construction, independent of anything this adapter
// arranges. A claim is still held, under a txn-scoped resource key
// guaranteed not to collide with any real entity resource, purely so
// the coordinator's attendance/Release bookkeeping stays uniform
// across every participant.
func (a *EntityAdapter) reserveAppend(ctx context.Context, tenant string, tp EntityAppendParams,
	txn, participantID string, deadline int64, w dxp.Weight) (dxp.Claim, error) {

	a.cache.Lock(tenant)
	defer a.cache.Unlock(tenant)

	var resource string
	if tp.ID != nil {
		resource = dxpEntityResource(tp.Entity, *tp.ID)
		for _, c := range a.cache.ClaimsForLocked(tenant, "entity", resource) {
			if c.Weight == dxp.Pessimistic || w == dxp.Pessimistic {
				return dxp.Claim{}, fmt.Errorf("entity participant: %s already reserved by a pending dxp transaction", resource)
			}
		}
		_, exists, err := a.store.entityVersion(ctx, tp.Entity, *tp.ID)
		if err != nil {
			return dxp.Claim{}, err
		}
		if exists {
			return dxp.Claim{}, fmt.Errorf("entity participant: %s: %w", resource, ErrAlreadyExists)
		}
	} else {
		// No real resource to guard — (txn, participantID)-scoped key,
		// cannot collide, including with a SIBLING auto-id append in the
		// same instance (txn alone was not enough here either — T-109).
		resource = "entity:" + tp.Entity + ":~append:" + txn + ":" + participantID
	}

	cl := dxp.Claim{
		Txn: txn, Primitive: "entity", Tenant: tenant, ParticipantID: participantID,
		Resource: resource, Weight: w, Amount: 1, Deadline: deadline,
	}
	if err := a.cache.Hold(cl); err != nil {
		return dxp.Claim{}, err
	}

	a.mu.Lock()
	a.pending[pendingKey(txn, participantID)] = tp
	a.mu.Unlock()
	return cl, nil
}

// Validate re-checks admission against current data, dispatching on
// the pending op's concrete type. A conflict here is what a
// competitor's commit looks like from this adapter's vantage; as with
// bal and fsm, classifying it as DXP007 (lost) versus DXP003 (drift)
// needs coordinator-level context this adapter doesn't have, so it
// returns ErrConflict / ErrAlreadyExists / a plain error and leaves
// that classification up the stack.
func (a *EntityAdapter) Validate(ctx context.Context, c dxp.Claim) error {
	a.mu.Lock()
	op, ok := a.pending[pendingKey(c.Txn, c.ParticipantID)]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("entity participant: no pending operation for txn %s participant %s", c.Txn, c.ParticipantID)
	}

	switch tp := op.(type) {
	case EntityUpdateParams:
		version, exists, err := a.store.entityVersion(ctx, tp.Entity, tp.ID)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("entity participant: %s no longer exists", dxpEntityResource(tp.Entity, tp.ID))
		}
		if tp.ExpectVersion != nil && *tp.ExpectVersion != version {
			return ErrConflict
		}
		return nil
	case EntityAppendParams:
		if tp.ID == nil {
			return nil // nothing to re-check: no real resource, see reserveAppend's own doc
		}
		_, exists, err := a.store.entityVersion(ctx, tp.Entity, *tp.ID)
		if err != nil {
			return err
		}
		if exists {
			// A competitor created this id after Reserve consented —
			// the create-side equivalent of an update's version drift.
			return fmt.Errorf("entity participant: %s: %w", dxpEntityResource(tp.Entity, *tp.ID), ErrAlreadyExists)
		}
		return nil
	default:
		return fmt.Errorf("entity participant: pending OpParams is %T, unexpected", op)
	}
}

// Execute applies the pending op via the store's existing saveInTx or
// createInTx — no new write path needed for either, unlike bal and
// cal, because both were already externally-transactable (built for
// the /commit path) before this session's dxp work. createInTx was
// checked directly, not assumed, to be genuinely tx-scoped throughout
// — every helper it calls (adaptedCreate, syncGraphEdges,
// indexForFTS) takes tx as a parameter and never falls back to a
// non-tx read, the same class of bug found and fixed in cal's own
// adapter (T-82). createInTx's returned allocated id is discarded
// here — Execute's current signature has no Result to carry it in
// (see dxp-coordinator-design.md §10); this is a concrete example of
// exactly what that would be for, once built.
func (a *EntityAdapter) Execute(ctx context.Context, store dxp.ParticipantStore, c dxp.Claim) (dxp.Result, error) {
	s, ok := store.(*dxp.SQLStore)
	if !ok {
		return nil, fmt.Errorf("entity participant: expected sql-backed store, got %s", store.Engine())
	}

	a.mu.Lock()
	op, ok := a.pending[pendingKey(c.Txn, c.ParticipantID)]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("entity participant: no pending operation for txn %s participant %s", c.Txn, c.ParticipantID)
	}

	// Ready() called here, immediately before the actual write.
	if err := store.Ready(ctx); err != nil {
		return nil, err
	}

	switch tp := op.(type) {
	case EntityUpdateParams:
		if _, err := a.store.saveInTx(ctx, s.Tx, CommitUpdate{
			Entity: tp.Entity, ID: tp.ID, Version: tp.ExpectVersion, Data: tp.Data,
		}); err != nil {
			return nil, err
		}
	case EntityAppendParams:
		if _, err := a.store.createInTx(ctx, s.Tx, CommitAppend(tp)); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("entity participant: pending OpParams is %T, unexpected", op)
	}

	a.mu.Lock()
	delete(a.pending, pendingKey(c.Txn, c.ParticipantID))
	a.mu.Unlock()
	return nil, nil
}

// Release drops txn's stashed params, if any. Idempotent and
// unconditional, matching bal.Adapter.Release and FsmAdapter.Release.
func (a *EntityAdapter) Release(ctx context.Context, c dxp.Claim) error {
	a.mu.Lock()
	delete(a.pending, pendingKey(c.Txn, c.ParticipantID))
	a.mu.Unlock()
	return nil
}

// PostCommit is a no-op — entity has no derived/advisory plane a
// commit signal would need to update. Implemented so EntityAdapter
// satisfies dxp.Participant without a second interface-change ripple
// later.
func (a *EntityAdapter) PostCommit(ctx context.Context, c dxp.Claim) error {
	return nil
}

// entityVersion reads an entity's stored _version, read-only, for
// Reserve/Validate's admission checks (which must not write —
// reservations are memory-only, T-54). Uses the reader pool
// (query_only(ON) at the connection level; see SQLiteStore doc)
// rather than a throwaway tx, since a single scalar read needs no
// transaction at all.
func (s *SQLiteStore) entityVersion(ctx context.Context, entity string, id int) (version int, exists bool, err error) {
	spec := s.adapted.Get(entity)
	if spec != nil {
		err = s.readDB.QueryRowContext(ctx, fmt.Sprintf(
			"SELECT _version FROM %s WHERE id = ?", spec.TableName()), id).Scan(&version)
	} else {
		err = s.readDB.QueryRowContext(ctx,
			`SELECT _version FROM `+s.nodesTable()+` WHERE entity_type = ? AND id = ?`,
			entity, id).Scan(&version)
	}
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	return version, err == nil, err
}
