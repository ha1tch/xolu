// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// dxp_adapter.go — T-121 (wave 10): obj's own dxp.Participant, built
// specifically to make promote/demote's own atomic, multi-primitive
// composition possible (obj-00-design.md §9). Deliberately narrower
// than T-123's own eventual full scope (that item still owns graph
// mirroring, events, and folding those into PostCommit) — this file
// exists to unblock T-121, not to front-run T-123's own remaining
// work. Two operations only: attach-and-contain (promote's own obj
// leg) and unassign-and-detach (demote's own obj leg). Ordinary,
// non-dxp Attach/Detach/Move/MoveToContainer (T-119/T-120) are
// entirely unaffected — this adapter is a second entry point into the
// same guarded store logic, not a replacement for the first.

package obj

import (
	"context"
	"fmt"
	"sync"

	"github.com/ha1tch/xolu/pkg/dxp"
)

// Compile-time check: Adapter must satisfy dxp.Participant — see
// loc's identical guard (pkg/loc/dxp_adapter.go) for why this was
// added directly rather than assumed already covered.
var _ dxp.Participant = (*Adapter)(nil)

// DxpAttachAndContainParams is obj's dxp.OpParams for promote's own
// obj leg (obj-01-rest-api.md §5): attach obj capability to
// SubjectRef and immediately position it as contained by
// ContainerRef, atomically. SubjectRef is always caller-composed from
// a PRE-ALLOCATED entity id (storage.SQLiteStore.AllocateNodeID) —
// promote's own handler resolves this before building the dxp
// transaction (dxp has no mechanism for one leg's execution result to
// feed another leg's params, confirmed directly against
// EntityAdapter.Execute before this shape was settled on).
type DxpAttachAndContainParams struct {
	SubjectRef   string   `json:"subject_ref"`
	ContainerRef string   `json:"container_ref"`
	Capacity     Capacity `json:"capacity,omitempty"`
}

// Primitive satisfies dxp.OpParams.
func (DxpAttachAndContainParams) Primitive() string { return "obj" }

// DxpDetachParams is obj's dxp.OpParams for demote's own obj leg:
// unassign SubjectRef's current position (relinquishing its
// container's own count, if any) and remove obj capability entirely,
// atomically. XOLU-OBJ011 if SubjectRef still contains anything.
type DxpDetachParams struct {
	SubjectRef string `json:"subject_ref"`
}

// Primitive satisfies dxp.OpParams.
func (DxpDetachParams) Primitive() string { return "obj" }

// Adapter is obj's dxp.Participant. One Adapter per Store; safe for
// concurrent use.
type Adapter struct {
	store *Store
	cache *dxp.MemCache

	mu sync.Mutex
	// pending holds either DxpAttachAndContainParams or
	// DxpDetachParams, keyed by (txn, participantID) — not txn alone,
	// matching loc/bal/cal/entity's own identical reasoning (T-109):
	// two obj participants in one instance (T-123's own named
	// adversarial risk) would otherwise silently overwrite each
	// other's stashed params under a shared Txn key.
	pending map[string]dxp.OpParams
}

func pendingKey(txn, participantID string) string {
	return txn + "\x00" + participantID
}

// NewAdapter wires store into cache and returns an Adapter ready to
// register with a dxp coordinator under the primitive key "obj".
func NewAdapter(store *Store, cache *dxp.MemCache) *Adapter {
	return &Adapter{store: store, cache: cache, pending: make(map[string]dxp.OpParams)}
}

// containerResource is the cache resource key for one container's own
// count capacity — "obj:container:<subject_ref>", matching
// dxp.Claim.Resource's own doc-comment convention. DxpDetachParams has
// no resource of its own to guard (detaching only ever relinquishes
// capacity, never consumes it) — its own claims use a txn-scoped,
// collision-free key purely for the coordinator's uniform bookkeeping,
// the identical shape EntityAppendParams's auto-id path already
// established for the same reason.
func containerResource(containerRef string) string {
	return "obj:container:" + containerRef
}

func pessimisticClaimSum(cache *dxp.MemCache, tenant, resource string) int64 {
	var sum int64
	for _, c := range cache.ClaimsForLocked(tenant, "obj", resource) {
		if c.Weight == dxp.Pessimistic {
			sum += c.Amount
		}
	}
	return sum
}

// Reserve evaluates admission for whichever op type it's given. For
// attach-and-contain: the destination container's current count plus
// every live pessimistic claim against it (this reservation's own
// included) against its ceiling — the identical "count + claims <=
// ceiling" arithmetic loc's own Reserve applies to a leaf, applied
// here to a container. For detach: no capacity to guard, so this
// always consents at Reserve time — the real checks (still attached,
// contains nothing) run for real in Execute, against transaction-
// scoped state, not a pre-transaction snapshot.
func (a *Adapter) Reserve(ctx context.Context, tenantKey string, op dxp.OpParams,
	txn, participantID string, deadline int64, w dxp.Weight) (dxp.Claim, error) {

	if got := a.store.TenantID().String(); got != tenantKey {
		return dxp.Claim{}, fmt.Errorf("obj participant: tenant key %q does not match store TenantID %d (want %q)", tenantKey, a.store.TenantID(), got)
	}

	switch tp := op.(type) {
	case DxpAttachAndContainParams:
		if tp.SubjectRef == "" || tp.ContainerRef == "" {
			return dxp.Claim{}, fmt.Errorf("obj participant: empty subject_ref or container_ref")
		}
		if tp.SubjectRef == tp.ContainerRef {
			return dxp.Claim{}, &ContainmentCycleError{SubjectRef: tp.SubjectRef, ContainerRef: tp.ContainerRef}
		}
		resource := containerResource(tp.ContainerRef)

		a.cache.Lock(tenantKey)
		defer a.cache.Unlock(tenantKey)

		container, err := a.store.Get(ctx, tp.ContainerRef)
		if err != nil {
			return dxp.Claim{}, err
		}
		claimed := pessimisticClaimSum(a.cache, tenantKey, resource)
		if container.Capacity.MaxCount != nil && container.Capacity.CurCount+claimed+1 > *container.Capacity.MaxCount {
			return dxp.Claim{}, &CapacityError{SubjectRef: tp.ContainerRef, Dimension: "count"}
		}

		cl := dxp.Claim{
			Txn: txn, Primitive: "obj", Tenant: tenantKey, ParticipantID: participantID,
			Resource: resource, Weight: w, Amount: 1, Deadline: deadline,
		}
		if err := a.cache.Hold(cl); err != nil {
			return dxp.Claim{}, err
		}
		a.mu.Lock()
		a.pending[pendingKey(txn, participantID)] = tp
		a.mu.Unlock()
		return cl, nil

	case DxpDetachParams:
		if tp.SubjectRef == "" {
			return dxp.Claim{}, fmt.Errorf("obj participant: empty subject_ref")
		}
		// No capacity to guard — a txn-scoped, collision-free resource
		// key purely for uniform bookkeeping (this file's own doc
		// comment on containerResource explains why).
		resource := "obj:detach:" + txn + ":" + participantID
		cl := dxp.Claim{
			Txn: txn, Primitive: "obj", Tenant: tenantKey, ParticipantID: participantID,
			Resource: resource, Weight: w, Amount: 1, Deadline: deadline,
		}
		a.cache.Lock(tenantKey)
		err := a.cache.Hold(cl)
		a.cache.Unlock(tenantKey)
		if err != nil {
			return dxp.Claim{}, err
		}
		a.mu.Lock()
		a.pending[pendingKey(txn, participantID)] = tp
		a.mu.Unlock()
		return cl, nil

	default:
		return dxp.Claim{}, fmt.Errorf("obj participant: OpParams is %T, want obj.DxpAttachAndContainParams or obj.DxpDetachParams", op)
	}
}

// Validate re-checks admission for a held claim, matching Reserve's
// own logic exactly, against whatever the container's count and
// ceiling are now. Optimistic claims pass unconditionally (bal's §7
// doc, generalised, matching loc's own Validate).
func (a *Adapter) Validate(ctx context.Context, c dxp.Claim) error {
	if c.Weight == dxp.Optimistic {
		return nil
	}
	a.mu.Lock()
	op, ok := a.pending[pendingKey(c.Txn, c.ParticipantID)]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("obj participant: no pending operation for txn %s participant %s", c.Txn, c.ParticipantID)
	}

	tp, ok := op.(DxpAttachAndContainParams)
	if !ok {
		return nil // DxpDetachParams has nothing to re-validate: no capacity guard.
	}

	a.cache.Lock(c.Tenant)
	defer a.cache.Unlock(c.Tenant)

	container, err := a.store.Get(ctx, tp.ContainerRef)
	if err != nil {
		return err
	}
	claimedIncludingSelf := pessimisticClaimSum(a.cache, c.Tenant, containerResource(tp.ContainerRef))
	if container.Capacity.MaxCount != nil && container.Capacity.CurCount+claimedIncludingSelf > *container.Capacity.MaxCount {
		return &CapacityError{SubjectRef: tp.ContainerRef, Dimension: "count"}
	}
	return nil
}

// Execute applies the pending operation via the coordinator-supplied
// tx (dxp-coordinator-design.md §11: one SQL transaction per
// participant; the coordinator opens and commits tx, never Execute).
func (a *Adapter) Execute(ctx context.Context, store dxp.ParticipantStore, c dxp.Claim) (dxp.Result, error) {
	s, ok := store.(*dxp.SQLStore)
	if !ok {
		return nil, fmt.Errorf("obj participant: expected sql-backed store, got %s", store.Engine())
	}

	a.mu.Lock()
	op, ok := a.pending[pendingKey(c.Txn, c.ParticipantID)]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("obj participant: no pending operation for txn %s participant %s", c.Txn, c.ParticipantID)
	}

	// Ready() called here, immediately before the actual write.
	if err := store.Ready(ctx); err != nil {
		return nil, err
	}

	switch tp := op.(type) {
	case DxpAttachAndContainParams:
		if err := attachAndContainInTx(ctx, s.Tx, tp.SubjectRef, tp.ContainerRef, tp.Capacity); err != nil {
			return nil, err
		}
	case DxpDetachParams:
		if err := unassignAndDetachInTx(ctx, s.Tx, tp.SubjectRef); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("obj participant: pending OpParams is %T, unexpected", op)
	}
	return nil, nil
}

// Release drops txn's stashed params, if any. Idempotent and
// unconditional, matching loc/bal/cal/entity exactly.
func (a *Adapter) Release(ctx context.Context, c dxp.Claim) error {
	a.mu.Lock()
	delete(a.pending, pendingKey(c.Txn, c.ParticipantID))
	a.mu.Unlock()
	return nil
}

// PostCommit is a safe, cheap no-op for now — T-123's own scope
// eventually folds the graph mirror in here (obj-00-design.md §10),
// the identical shape bal.Adapter.PostCommit already established.
// Exists now so T-123 doesn't need a second interface change,
// matching dxp.Participant's own doc comment for this verb.
// PostCommit best-effort mirrors the committed containment change
// into the live graph (T-123, obj-00-design.md §10) — the identical
// commit-first-authoritative/mirror-second-best-effort shape
// bal.Adapter.PostCommit already established, applied to a genuinely
// new mirror target (no other primitive's own PostCommit touches the
// graph). Re-reads the pending op's own params rather than trusting
// any state from Execute — the same discipline bal's own PostCommit
// doc names explicitly, for the identical reason: PostCommit runs
// separately, potentially some time after Execute.
func (a *Adapter) PostCommit(ctx context.Context, c dxp.Claim) error {
	a.mu.Lock()
	key := pendingKey(c.Txn, c.ParticipantID)
	op, ok := a.pending[key]
	if ok {
		delete(a.pending, key)
	}
	a.mu.Unlock()
	if !ok {
		// Already cleaned up by Release for some other reason —
		// nothing left to do, matching bal's own PostCommit contract.
		return nil
	}

	switch tp := op.(type) {
	case DxpAttachAndContainParams:
		a.store.mirrorContainmentAdd(tp.SubjectRef, tp.ContainerRef)
	case DxpDetachParams:
		// The subject's own row is gone by now (store.go's own
		// deletion semantics) — the journal (T-122) is the only place
		// its last container is still recorded.
		prevContainer, hadPrev, err := a.store.lastKnownContainerFromJournal(ctx, tp.SubjectRef)
		if err != nil {
			a.store.mirrorDegraded(err)
			return nil
		}
		if hadPrev {
			a.store.mirrorContainmentRemove(tp.SubjectRef, prevContainer)
		}
	}
	return nil
}
