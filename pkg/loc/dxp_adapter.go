// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/ha1tch/xolu/pkg/dxp"
)

// Compile-time check: Adapter must satisfy dxp.Participant — see
// bal's identical guard (pkg/bal/dxp_adapter.go) for why this was
// added directly rather than assumed already covered.
var _ dxp.Participant = (*Adapter)(nil)

// DxpMoveParams is loc's dxp.OpParams (T-118, wave 9): reassigns a
// subject's tree-leaf position via dxp. Deliberately narrower than
// admission.go's own MoveParams: no caller-supplied fence keys. A
// real caller manually specifying fence deltas was only ever Stage
// 2's own test hook — Stage 3 (T-116) built real fence-membership
// resolution from geometry, and a dxp wire surface should not
// reintroduce the thing that superseded.
//
// Tree-aligned fence membership (loc-01-rest-api.md §0: "follows the
// free tree walk automatically") is now handled automatically —
// Move itself auto-derives entered/exited from tree alignment
// whenever the caller doesn't explicitly supply fence keys (Stage 6),
// and Execute's call into moveInTx never does, so a dxp-triggered
// move gets real tree-aligned fence capacity guards for free, not
// just leaf capacity. What's still not covered: a self-anchored or
// standalone fence's membership, which only report's exact geometric
// test resolves — a dxp move was never going to touch those anyway,
// since move never resolves a coordinate (§0's own two-write-path
// distinction).
type DxpMoveParams struct {
	SubjectRef   string `json:"subject_ref"`
	ToLocationID string `json:"to_location_id"`
}

// Primitive satisfies dxp.OpParams.
func (DxpMoveParams) Primitive() string { return "loc" }

// Adapter is loc's dxp.Participant. One Adapter per Store; safe for
// concurrent use.
type Adapter struct {
	store *Store
	cache *dxp.MemCache

	mu sync.Mutex
	// pending stashes each Reserve's full OpParams by (txn,
	// participantID) — not txn alone (T-109's own finding, the same
	// reasoning bal/cal's own pending maps document): two loc
	// participants moving different subjects in one instance would
	// otherwise silently overwrite each other's stashed params under
	// a shared Txn key.
	pending map[string]DxpMoveParams
}

// pendingKey composes the pending map's real key — see bal.pendingKey/
// cal.pendingKey for the identical rationale (T-109).
func pendingKey(txn, participantID string) string {
	return txn + "\x00" + participantID
}

// NewAdapter wires store into cache and returns an Adapter ready to
// register with a dxp coordinator under the primitive key "loc".
func NewAdapter(store *Store, cache *dxp.MemCache) *Adapter {
	return &Adapter{store: store, cache: cache, pending: make(map[string]DxpMoveParams)}
}

// leafResource is the cache resource key for one location's capacity
// — "loc:leaf:<location_key>", matching dxp.Claim.Resource's own doc
// comment convention ("acct:42", "cal:room7:2026-08-01").
func leafResource(locationKey int64) string {
	return fmt.Sprintf("loc:leaf:%d", locationKey)
}

// pessimisticClaimSum mirrors bal.pessimisticClaimSum and
// cal's own equivalent exactly — each package defines its own copy
// rather than sharing one across pkg/dxp, matching the established
// precedent (the helper is a few lines, and the alternative is a
// shared dependency for something this small). Every loc Claim's
// Amount is always 1 (a leaf holds subjects, not a summed quantity),
// so this sum is genuinely a COUNT of live pessimistic claims against
// the resource — the same "claims count toward the limit" arithmetic
// bal applies to a balance, applied here to a capacity ceiling.
func pessimisticClaimSum(cache *dxp.MemCache, tenant, resource string) int64 {
	var sum int64
	for _, c := range cache.ClaimsForLocked(tenant, "loc", resource) {
		if c.Weight == dxp.Pessimistic {
			sum += c.Amount
		}
	}
	return sum
}

// Reserve evaluates whether the destination leaf has room — current
// count plus every live PESSIMISTIC claim against it (this
// reservation's own included, once held) against its ceiling — the
// same "count + claims <= ceiling" arithmetic Stage 2's ordinary CAS
// applies at commit time, evaluated early here against live
// reservations rather than committed rows alone. On consent it Holds
// one claim and stashes tp for Execute. The whole evaluate-then-hold
// sequence runs under one tenant.Lock/Unlock critical section
// (proposal §4), matching bal/cal.
func (a *Adapter) Reserve(ctx context.Context, tenantKey string, op dxp.OpParams,
	txn, participantID string, deadline int64, w dxp.Weight) (dxp.Claim, error) {

	tp, ok := op.(DxpMoveParams)
	if !ok {
		return dxp.Claim{}, fmt.Errorf("loc participant: OpParams is %T, want loc.DxpMoveParams", op)
	}
	if tp.SubjectRef == "" || tp.ToLocationID == "" {
		return dxp.Claim{}, fmt.Errorf("loc participant: empty subject_ref or to_location_id")
	}
	if got := a.store.TenantID().String(); got != tenantKey {
		return dxp.Claim{}, fmt.Errorf("loc participant: tenant key %q does not match store TenantID %d (want %q)", tenantKey, a.store.TenantID(), got)
	}

	locationKey, ceiling, count, err := a.store.leafCapacitySnapshot(ctx, tp.ToLocationID)
	if err != nil {
		return dxp.Claim{}, err
	}
	resource := leafResource(locationKey)

	a.cache.Lock(tenantKey)
	defer a.cache.Unlock(tenantKey)

	claimed := pessimisticClaimSum(a.cache, tenantKey, resource)
	if ceiling.Valid && count+claimed+1 > ceiling.Int64 {
		return dxp.Claim{}, &CapacityError{Kind: "leaf", Key: uint32(locationKey)}
	}

	cl := dxp.Claim{
		Txn: txn, Primitive: "loc", Tenant: tenantKey, ParticipantID: participantID,
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

// Validate re-checks that the sum of every live pessimistic claim
// against c's leaf — c's own included — still fits its ceiling. That
// sum already includes c.Amount (1), so this is exactly the invariant
// Reserve established, re-evaluated against whatever the leaf's count
// and ceiling are now. The count read and the claims read run under
// the SAME tenant.Lock, matching bal/cal — a read split across two
// lock acquisitions would reopen the TOCTOU gap the lock exists to
// close. Optimistic claims are invisible to guard arithmetic
// everywhere (bal's §7 doc, generalised) and pass unconditionally.
func (a *Adapter) Validate(ctx context.Context, c dxp.Claim) error {
	if c.Weight == dxp.Optimistic {
		return nil
	}
	a.mu.Lock()
	tp, ok := a.pending[pendingKey(c.Txn, c.ParticipantID)]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("loc participant: no pending move for txn %s participant %s", c.Txn, c.ParticipantID)
	}

	a.cache.Lock(c.Tenant)
	defer a.cache.Unlock(c.Tenant)

	locationKey, ceiling, count, err := a.store.leafCapacitySnapshot(ctx, tp.ToLocationID)
	if err != nil {
		return err
	}
	claimedIncludingSelf := pessimisticClaimSum(a.cache, c.Tenant, leafResource(locationKey))
	if ceiling.Valid && count+claimedIncludingSelf > ceiling.Int64 {
		return &CapacityError{Kind: "leaf", Key: uint32(locationKey)}
	}
	return nil
}

// Execute applies tp's move via moveInTx against the coordinator-
// supplied tx (proposal §11: one SQL transaction for every
// participant; the coordinator opens and commits tx, never Execute).
func (a *Adapter) Execute(ctx context.Context, store dxp.ParticipantStore, c dxp.Claim) (dxp.Result, error) {
	s, ok := store.(*dxp.SQLStore)
	if !ok {
		return nil, fmt.Errorf("loc participant: expected sql-backed store, got %s", store.Engine())
	}

	a.mu.Lock()
	tp, ok := a.pending[pendingKey(c.Txn, c.ParticipantID)]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("loc participant: no pending move for txn %s participant %s", c.Txn, c.ParticipantID)
	}

	// Ready() called here, immediately before the actual write — the
	// moment this participant is genuinely about to touch the store,
	// not before (dxp-coordinator-design.md §2).
	if err := store.Ready(ctx); err != nil {
		return nil, err
	}

	if _, err := a.store.moveInTx(ctx, s.Tx, MoveParams{SubjectRef: tp.SubjectRef, ToLocationID: tp.ToLocationID}); err != nil {
		return nil, err
	}
	return nil, nil
}

// Release drops txn's stashed params, if any. Idempotent and
// unconditional, matching bal/cal exactly. The cache entry itself is
// removed by the coordinator's ReleaseTxn, not here.
func (a *Adapter) Release(ctx context.Context, c dxp.Claim) error {
	a.mu.Lock()
	delete(a.pending, pendingKey(c.Txn, c.ParticipantID))
	a.mu.Unlock()
	return nil
}

// PostCommit is a safe, cheap no-op — loc has no derived/advisory
// plane fed only by committed writes (no rollup, no occupancy index
// analogous to cal's H3). Exists so a future one doesn't need a
// second interface change, matching dxp.Participant's own doc comment
// for this verb exactly (fsm/entity/ts today are the same shape).
func (a *Adapter) PostCommit(ctx context.Context, c dxp.Claim) error {
	a.mu.Lock()
	delete(a.pending, pendingKey(c.Txn, c.ParticipantID))
	a.mu.Unlock()
	return nil
}

// leafCapacitySnapshot resolves a location_id to its internal key
// plus a live read of its current ceiling/count — shared by Reserve
// and Validate so the same query shape and error handling exist in
// exactly one place.
func (s *Store) leafCapacitySnapshot(ctx context.Context, locationID string) (locationKey int64, ceiling sql.NullInt64, count int64, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT l.location_key, c.ceiling, c.count FROM `+s.locationsTable()+` l
		 JOIN loc_capacity c ON c.location_key = l.location_key
		 WHERE l.location_id = ?`, locationID).Scan(&locationKey, &ceiling, &count)
	if err == sql.ErrNoRows {
		return 0, sql.NullInt64{}, 0, &UnknownLocationError{LocationID: locationID}
	}
	return locationKey, ceiling, count, err
}
