// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

// dxp_adapter.go — ts's dxp.Participant (T-86, item 40, wave 5).
// First genuinely Pebble-backed dxp participant — bal/cal(H1)/fsm/
// entity are all SQL-backed, so this is what makes EngineHomogeneous
// ever actually false, and what the phased (non-collapsed) execution
// path (dispatchDxpTxnCore, still refused explicitly) will need to
// exist for at all.
//
// Investigated directly before writing this, not assumed: validateEvent
// is pure/stateless — timeline existence is the one real admission
// check (mirroring cal's calendar-existence check and bal's
// account-existence check, an already-proven pattern, not a novel
// one). No conflict/admission concept exists beyond existence — ts
// events are independent, immutable, append-only points; there is
// nothing analogous to cal's interval-overlap or bal's
// balance-sufficiency to design, so Reserve/Validate are genuinely
// near-trivial here, not cut corners.
//
// The one real complication: Append's own production path has a
// write-coalescer branch (coalEnabled/startCoalescer) that hands off
// to an async goroutine — incompatible with a dxp-driven write needing
// to land synchronously inside a coordinator-supplied *pebble.Batch.
// Execute below bypasses Append/the coalescer entirely, writing
// directly via EncodeKey/EncodeValue + the batch's own Set — exactly
// the encode-then-set pair Append itself uses on its own s.db.Set
// path, just targeting the coordinator's batch instead. Confirmed
// safe to bypass, not just convenient: ts events have no shared
// mutable state to get out of sync (unlike bal's balance), so
// bypassing the coalescer for a dxp write while ordinary writes keep
// using it is functionally equivalent to coalescing with a batch size
// of one.
//
// Deliberately NOT wired into dxpPrimitiveOps/dxpEngineOf/
// dxpParticipantRegistry (pkg/server/v2_dxp_def_handlers.go) by this
// item — doing so would let a def register successfully with ts as a
// participant while every dispatch against it refuses outright
// ("phased execution is not yet implemented"), an accept-then-always-
// refuse state that's honest but confusing. That wiring is deferred
// to whichever item actually builds the phased path, so registering a
// ts-touching def and dispatching it become possible in the same
// change, not two separate, confusing steps.
//
// No tenant-identity cross-check here unlike bal's Adapter (which
// checks tenant against store.TenantID()): PebbleStore carries
// tenantName only for dynconfig scoping, not an exported tenant
// identity accessor, and each PebbleStore instance is already
// constructed per-tenant by the manager — the coordinator's own
// per-tenant registry construction is what actually enforces scoping,
// same structural guarantee bal's check is redundant with in
// practice, just without a clean accessor to make it explicit here too.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/ha1tch/xolu/pkg/dxp"
)

// Compile-time check: Adapter must satisfy dxp.Participant.
var _ dxp.Participant = (*Adapter)(nil)

// AppendParams is ts's dxp.OpParams — one event to append.
type AppendParams struct {
	Timeline   TimelineID `json:"timeline"`
	Dims       []uint64   `json:"dims,omitempty"`
	TimeUnixNs int64      `json:"time_unix_ns"`
	Nums       []float64  `json:"nums,omitempty"`
	Payload    []byte     `json:"payload,omitempty"`
}

// Primitive satisfies dxp.OpParams.
func (AppendParams) Primitive() string { return "ts" }

// Adapter is ts's dxp.Participant. One Adapter per PebbleStore; safe
// for concurrent use.
type Adapter struct {
	store *PebbleStore
	cache *dxp.MemCache

	mu sync.Mutex
	// pending stashes each Reserve's full AppendParams by txn id,
	// mirroring bal.Adapter's own pending map exactly — Claim carries
	// admission bookkeeping (Resource, Amount, Deadline), not the
	// event's own payload, so Execute needs somewhere to recover it
	// from. Same non-durability reasoning as bal's own doc: a crash
	// loses this map, but also loses every live claim in the dxp
	// cache (memory only, by design) at the same instant, so nothing
	// recoverable here is lost that durable claims wouldn't also lose.
	pending map[string]AppendParams
}

// pendingKey composes the pending map's real key — see bal.pendingKey
// for the identical rationale (T-109): txn alone cannot distinguish
// two same-primitive participants in one instance.
func pendingKey(txn, participantID string) string {
	return txn + "\x00" + participantID
}

// NewAdapter returns an Adapter ready to register with a dxp
// coordinator under the primitive key "ts".
func NewAdapter(store *PebbleStore, cache *dxp.MemCache) *Adapter {
	return &Adapter{store: store, cache: cache, pending: make(map[string]AppendParams)}
}

// dxpResource names the claim resource for a timeline — namespaced,
// matching bal's own "acct:<id>"/cal's "cal:<id>:..." convention.
func dxpResource(id TimelineID) string {
	return fmt.Sprintf("ts:%d", id)
}

// Reserve validates the event (validateEvent, pure) and checks the
// timeline is defined (the one real admission check — see this file's
// own doc). On consent it Holds a trivial claim (Amount: 1 — a
// slot-shaped resource per Claim's own doc, not a conserved one like
// bal's) and stashes the params for Execute.
func (a *Adapter) Reserve(ctx context.Context, tenant string, op dxp.OpParams,
	txn, participantID string, deadline int64, w dxp.Weight) (dxp.Claim, error) {

	ap, ok := op.(AppendParams)
	if !ok {
		return dxp.Claim{}, fmt.Errorf("ts participant: OpParams is %T, want timeseries.AppendParams", op)
	}
	if err := a.store.validateEvent(eventFromParams(ap)); err != nil {
		return dxp.Claim{}, err
	}
	cfg, found := a.store.reg.get(ap.Timeline)
	if !found {
		return dxp.Claim{}, fmt.Errorf("ts: timeline %d not defined (XOLU-TS004)", ap.Timeline)
	}
	if len(ap.Dims) != int(cfg.Dims) {
		return dxp.Claim{}, fmt.Errorf("ts: timeline %d expects %d dims, got %d (XOLU-TS007)", ap.Timeline, cfg.Dims, len(ap.Dims))
	}

	a.cache.Lock(tenant)
	cl := dxp.Claim{
		Txn: txn, Primitive: "ts", Tenant: tenant, ParticipantID: participantID,
		Resource: dxpResource(ap.Timeline), Weight: w, Amount: 1, Deadline: deadline,
	}
	holdErr := a.cache.Hold(cl)
	a.cache.Unlock(tenant)
	if holdErr != nil {
		return dxp.Claim{}, holdErr
	}

	a.mu.Lock()
	a.pending[pendingKey(txn, participantID)] = ap
	a.mu.Unlock()
	return cl, nil
}

// Validate re-checks the timeline still exists — the only fact
// Reserve established that could have changed (a delete between
// Reserve and Validate). No balance/occupancy-shaped invariant exists
// here to re-verify, unlike bal/cal — append is independent of every
// other event on the timeline, live or not.
func (a *Adapter) Validate(ctx context.Context, c dxp.Claim) error {
	if c.Weight == dxp.Optimistic {
		return nil
	}
	a.mu.Lock()
	ap, ok := a.pending[pendingKey(c.Txn, c.ParticipantID)]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("ts participant: no pending append for txn %s participant %s", c.Txn, c.ParticipantID)
	}
	if _, found := a.store.reg.get(ap.Timeline); !found {
		return fmt.Errorf("ts: timeline %d no longer defined (XOLU-TS004)", ap.Timeline)
	}
	return nil
}

// Execute writes the event directly into the coordinator-supplied
// batch — EncodeKey/EncodeValue + Set, the same pair Append's own
// non-coalesced path uses on s.db, just targeting store.Batch
// instead. Deliberately bypasses Append/the coalescer entirely (see
// this file's own doc for why that's safe here).
func (a *Adapter) Execute(ctx context.Context, store dxp.ParticipantStore, c dxp.Claim) (dxp.Result, error) {
	ps, ok := store.(*dxp.PebbleStore)
	if !ok {
		return nil, fmt.Errorf("ts participant: expected pebble-backed store, got %s", store.Engine())
	}

	a.mu.Lock()
	ap, ok := a.pending[pendingKey(c.Txn, c.ParticipantID)]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("ts participant: no pending append for txn %s participant %s", c.Txn, c.ParticipantID)
	}

	cfg, found := a.store.reg.get(ap.Timeline)
	if !found {
		return nil, fmt.Errorf("ts: timeline %d no longer defined (XOLU-TS004)", ap.Timeline)
	}

	key, err := EncodeKey(ap.Timeline, cfg.Dims, ap.Dims, timeFromUnixNs(ap.TimeUnixNs))
	if err != nil {
		return nil, err
	}
	val, err := EncodeValue(ap.Nums, ap.Payload)
	if err != nil {
		return nil, err
	}

	// Ready() called here, immediately before the actual write — the
	// moment this participant is genuinely about to touch the store,
	// not before (dxp-coordinator-design.md §2), same convention every
	// other adapter follows.
	if err := store.Ready(ctx); err != nil {
		return nil, err
	}

	if err := ps.Batch.Set(key, val, pebble.Sync); err != nil {
		return nil, fmt.Errorf("ts: pebble batch set: %w", err)
	}

	a.store.counter(ap.Timeline).Add(1)
	_ = a.store.reg.recordFirstWrite(ap.Timeline)

	a.mu.Lock()
	delete(a.pending, pendingKey(c.Txn, c.ParticipantID))
	a.mu.Unlock()
	return nil, nil
}

// Release drops txn's stashed params, if any. Idempotent and
// unconditional, matching every other adapter's own error taxonomy
// (dxp-reservation-cache.md §16): releasing an unknown or
// already-cleared txn is a no-op, never an error.
func (a *Adapter) Release(ctx context.Context, c dxp.Claim) error {
	a.mu.Lock()
	delete(a.pending, pendingKey(c.Txn, c.ParticipantID))
	a.mu.Unlock()
	return nil
}

// PostCommit is a no-op — ts's own event write IS the primary store,
// not a guard-plane write with a separate derived/advisory index fed
// afterward (unlike cal's H1/H3 split). Implemented so Adapter
// satisfies dxp.Participant without a second interface-change ripple
// later.
func (a *Adapter) PostCommit(ctx context.Context, c dxp.Claim) error {
	return nil
}

// timeFromUnixNs converts AppendParams' wire-friendly int64 (avoids
// time.Time's own JSON marshalling/timezone surface crossing the dxp
// binding boundary, same reasoning bal's @B04 applies to amounts,
// though nanosecond-since-epoch has no smuggling risk of its own —
// it's just a plain integer either way) back to a time.Time for
// validateEvent/EncodeKey, which both take the real type.
func timeFromUnixNs(ns int64) time.Time {
	return time.Unix(0, ns).UTC()
}

// eventFromParams builds the Event validateEvent expects from
// AppendParams' own wire shape — Dims is intentionally omitted here:
// dims-count validation (against the timeline's own configured width)
// happens separately in Reserve, since validateEvent itself never
// checks Dims (only Append's own caller does, per the real code, not
// assumed).
func eventFromParams(ap AppendParams) Event {
	return Event{
		Timeline: ap.Timeline,
		Time:     timeFromUnixNs(ap.TimeUnixNs),
		Nums:     ap.Nums,
	}
}
