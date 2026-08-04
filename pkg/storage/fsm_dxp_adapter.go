// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"fmt"
	"sync"

	"github.com/ha1tch/xolu/pkg/dxp"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// Compile-time check: FsmAdapter must satisfy dxp.Participant — see
// bal's identical guard (pkg/bal/dxp_adapter.go) for why this was
// added now rather than assumed already covered.
var _ dxp.Participant = (*FsmAdapter)(nil)

// FsmTransitionParams is fsm's dxp.OpParams (T-54's typed-per-primitive
// decision): everything FsmWalkInTx needs to resolve and apply one
// transition, minus the tentative-write step itself.
//
// TenantID is carried explicitly (fsm's storage layer is keyed by
// tenant.TenantID, not a string prefix like bal's) rather than derived
// from the tenant string Reserve/Execute receive — Reserve cross-checks
// the two agree (TenantID.String() == tenant) so a caller passing a
// mismatched pair fails loudly instead of silently splitting the same
// logical tenant across two cache shards.
type FsmTransitionParams struct {
	TenantID      tenant.TenantID        `json:"-"` // never trusted from participant params — the coordinator always sets this from the instance's own actual tenant
	MachineID     int64                  `json:"machine_id"`
	Input         string                 `json:"input"`
	Payload       map[string]interface{} `json:"payload,omitempty"`
	QueryBindings map[string]interface{} `json:"query_bindings,omitempty"`
}

// Primitive satisfies dxp.OpParams.
func (FsmTransitionParams) Primitive() string { return "fsm" }

// FsmAdapter is fsm's dxp.Participant. One per SQLiteStore; safe for
// concurrent use.
type FsmAdapter struct {
	store *SQLiteStore
	cache *dxp.MemCache

	mu      sync.Mutex
	pending map[string]FsmTransitionParams // keyed by (txn, participantID) — see bal.Adapter's pendingKey doc for the rationale (T-109)
}

// pendingKey composes the pending map's real key for both FsmAdapter
// and EntityAdapter (same package, shared here) — see bal.pendingKey
// for the identical rationale (T-109): txn alone cannot distinguish
// two same-primitive participants in one instance.
func pendingKey(txn, participantID string) string {
	return txn + "\x00" + participantID
}

// NewFsmAdapter wires store into cache (SetDxpClaims) and returns an
// FsmAdapter ready to register with a dxp coordinator under the
// primitive key "fsm".
func NewFsmAdapter(store *SQLiteStore, cache *dxp.MemCache) *FsmAdapter {
	store.SetDxpClaims(cache)
	return &FsmAdapter{store: store, cache: cache, pending: make(map[string]FsmTransitionParams)}
}

// Reserve resolves tp's transition read-only (fsmResolveInTx — no
// write, per T-54's memory-only-reservation rule) and, on a legal
// transition, Holds a claim.
//
// Admission rule (proposal §5c's fsm resolution, made precise for
// mixed weights on one machine, which the design left unstated): a
// live PESSIMISTIC claim always refuses a new reservation, of either
// weight — pessimistic means exclusive, "the machine is locked
// mid-step". A live OPTIMISTIC claim only refuses a new PESSIMISTIC
// reservation (pessimistic wants exclusivity even against optimistic
// siblings); optimistic reservations coexist with each other freely,
// per "competing reserved transitions from one state coexist."
func (a *FsmAdapter) Reserve(ctx context.Context, tenant string, op dxp.OpParams,
	txn, participantID string, deadline int64, w dxp.Weight) (dxp.Claim, error) {

	tp, ok := op.(FsmTransitionParams)
	if !ok {
		return dxp.Claim{}, fmt.Errorf("fsm participant: OpParams is %T, want storage.FsmTransitionParams", op)
	}
	if got := tp.TenantID.String(); got != tenant {
		return dxp.Claim{}, fmt.Errorf("fsm participant: tenant key %q does not match TenantID %d (want %q)", tenant, tp.TenantID, got)
	}

	a.cache.Lock(tenant)
	defer a.cache.Unlock(tenant)

	resource := dxpFsmResource(tp.MachineID)
	for _, c := range a.cache.ClaimsForLocked(tenant, "fsm", resource) {
		if c.Weight == dxp.Pessimistic || w == dxp.Pessimistic {
			return dxp.Claim{}, &FsmWalkError{Code: "XOLU-FSM004",
				Message: "machine already reserved by a pending dxp transaction"}
		}
	}

	// Resolve read-only on a throwaway transaction: fsmResolveInTx wants
	// a *sql.Tx, but Reserve must not write (T-54). A read-only tx
	// against the reader pool (query_only=ON, see SQLiteStore doc) is
	// rolled back unconditionally — never committed, so it can carry no
	// write regardless of what fsmResolveInTx does internally.
	rtx, err := a.store.readDB.BeginTx(ctx, nil) // readDB carries query_only(ON) at the connection level (see SQLiteStore doc) — that pragma is the actual enforcement; sql.TxOptions.ReadOnly support varies by driver and isn't relied on here
	if err != nil {
		return dxp.Claim{}, err
	}
	defer func() { _ = rtx.Rollback() }()

	if _, err := a.store.fsmResolveInTx(ctx, rtx, tp.TenantID, tp.MachineID, tp.Input, tp.Payload, tp.QueryBindings); err != nil {
		return dxp.Claim{}, err
	}

	cl := dxp.Claim{
		Txn: txn, Primitive: "fsm", Tenant: tenant, ParticipantID: participantID,
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

// Validate re-resolves the same transition read-only. Any change since
// Reserve — the machine moved, the guard now fails, the machine went
// terminal — surfaces as fsmResolveInTx's own error; Validate does not
// itself classify DXP007 (lost to a competitor) versus DXP003 (drift),
// for the same reason bal's Validate doesn't: that needs coordinator-
// level context this adapter doesn't have. See bal.Adapter.Validate.
func (a *FsmAdapter) Validate(ctx context.Context, c dxp.Claim) error {
	a.mu.Lock()
	tp, ok := a.pending[pendingKey(c.Txn, c.ParticipantID)]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("fsm participant: no pending transition for txn %s participant %s", c.Txn, c.ParticipantID)
	}

	rtx, err := a.store.readDB.BeginTx(ctx, nil) // readDB carries query_only(ON) at the connection level (see SQLiteStore doc) — that pragma is the actual enforcement; sql.TxOptions.ReadOnly support varies by driver and isn't relied on here
	if err != nil {
		return err
	}
	defer func() { _ = rtx.Rollback() }()

	_, err = a.store.fsmResolveInTx(ctx, rtx, tp.TenantID, tp.MachineID, tp.Input, tp.Payload, tp.QueryBindings)
	return err
}

// Execute re-resolves tp's transition against tx (the coordinator's
// shared transaction, freshly — not the Reserve-time snapshot, so a
// change since Reserve is caught here rather than trusted from stale
// data) and applies it via fsmApplyTransitionInTx, whose CAS on the
// freshly-observed state is the mechanism that actually enforces
// "nothing moved this machine out from under us." Bypasses
// FsmWalkInTx's own claims gate deliberately — Execute IS the
// dxp-authorised path; gating it on its own claim would self-block.
func (a *FsmAdapter) Execute(ctx context.Context, store dxp.ParticipantStore, c dxp.Claim) (dxp.Result, error) {
	s, ok := store.(*dxp.SQLStore)
	if !ok {
		return nil, fmt.Errorf("fsm participant: expected sql-backed store, got %s", store.Engine())
	}

	a.mu.Lock()
	tp, ok := a.pending[pendingKey(c.Txn, c.ParticipantID)]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("fsm participant: no pending transition for txn %s participant %s", c.Txn, c.ParticipantID)
	}

	// Ready() called here, immediately before the first actual write.
	if err := store.Ready(ctx); err != nil {
		return nil, err
	}

	resolution, err := a.store.fsmResolveInTx(ctx, s.Tx, tp.TenantID, tp.MachineID, tp.Input, tp.Payload, tp.QueryBindings)
	if err != nil {
		return nil, err
	}
	if _, err := a.store.fsmApplyTransitionInTx(ctx, s.Tx, tp.TenantID, tp.MachineID, resolution, tp.Input, tp.Payload, tp.QueryBindings); err != nil {
		return nil, err
	}

	a.mu.Lock()
	delete(a.pending, pendingKey(c.Txn, c.ParticipantID))
	a.mu.Unlock()
	return nil, nil
}

// Release drops txn's stashed params, if any. Idempotent and
// unconditional, matching bal.Adapter.Release.
func (a *FsmAdapter) Release(ctx context.Context, c dxp.Claim) error {
	a.mu.Lock()
	delete(a.pending, pendingKey(c.Txn, c.ParticipantID))
	a.mu.Unlock()
	return nil
}

// PostCommit is a no-op — fsm has no derived/advisory plane a commit
// signal would need to update. Implemented so FsmAdapter satisfies
// dxp.Participant without a second interface-change ripple later.
func (a *FsmAdapter) PostCommit(ctx context.Context, c dxp.Claim) error {
	return nil
}
