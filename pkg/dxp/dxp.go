// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package dxp implements the substrate-level reservation cache and
// participant contract for /dxp composed commitments (T-54, per
// docs/proposals/dxp-reservation-cache.md). It supersedes the
// persisted tentative-row medium of pkg/reserved for dxp's own use —
// reservations live in process memory for their TTL, never in SQL —
// while reusing pkg/reserved's weights, visibility taxonomy, and
// "guards never use accelerators" doctrine unchanged.
//
// # Why memory, not tables
//
// A reservation is inherently ephemeral. Persisting it forces the
// guard-plane adoption question onto every table any guard reads
// before any dxp consumer exists. Memory sidesteps that: crash means
// abandon, never resume (part 2 of the design; see the mount-time
// tombstone pass this package does not itself implement — that is the
// dxp coordinator's concern, item 21).
//
// # The serialisation rule (proposal §4)
//
// A participant's guard reads two places once dxp is in play: its SQL
// tables (inside its transaction) and this cache (outside any
// transaction). Left unmanaged, that splits a guard's read from its
// write — a claim could appear between the cache read and the SQL
// commit, admitting a write the claim should have blocked.
//
// The proposal's resolution is a property the substrate already has:
// per-tenant single-writer serialisation. This package makes that
// property a MECHANICAL guarantee rather than a timing coincidence of
// SQLite's own locking: [Cache.Lock] takes an explicit per-tenant Go
// mutex that every claims-touching write path — dxp's own Reserve and
// Execute, and any ordinary guarded write that must see live claims —
// acquires for the span "guard-evaluate over tables + live claims,
// then commit". Relying on SQLite's write-lock timing alone would make
// the cache's memory-safety contingent on driver-specific lock
// semantics (_txlock=immediate, busy_timeout tuning) holding
// everywhere, forever, silently — precisely the class of assumption
// the working agreement's verification obligation exists to catch
// before it ships as a race. The explicit mutex costs nothing extra
// (real concurrent SQL work against one tenant is already serialised
// by the file) and removes the contingency.
//
// ClaimsFor called WITHOUT holding [Cache.Lock] (tier 2, advisory
// ingestion per @D05c) takes its own brief read lock and tolerates
// staleness by design — see the Cache doc.
package dxp

import (
	"context"

	"github.com/ha1tch/xolu/pkg/reserved"
)

// Weight is pkg/reserved's admission-policy type, reused unchanged:
// Pessimistic claims fold into guard arithmetic as if committed;
// Optimistic claims coexist and the first confirmer wins.
type Weight = reserved.Weight

const (
	Pessimistic = reserved.Pessimistic
	Optimistic  = reserved.Optimistic
)

// Claim is one reserved resource, held by one dxp instance. Immutable
// once held — the cache never mutates a claim in place; a changed
// claim is a new Hold after a ReleaseTxn.
type Claim struct {
	Txn       string // owning dxp instance id
	Primitive string // participant registry key: "bal", "cal", "fsm", "blob", "entity"
	Tenant    string // tenant scope — claims never cross tenants (v1, @D08)
	Resource  string // primitive-scoped key: "acct:42", "cal:room7:2026-08-01", ...
	Weight    Weight
	Amount    int64 // magnitude for conserved resources (bal minor units); 1 for slot-shaped resources
	Deadline  int64 // unix nanoseconds, UTC. Authoritative — see Cache.ClaimsFor.

	// ParticipantID is the def's own participant id (the "id" field in
	// the def's participants array) — the coordinator hands it to
	// Reserve, an adapter carries it through unchanged in the Claim it
	// returns. Exists specifically to distinguish two participants of
	// the SAME primitive within one instance (T-109, found by direct
	// reproduction): Txn is shared by every participant in an
	// instance by design (the cache's own ClaimsByTxn/ConfirmTxn/
	// ReleaseTxn operate on every claim sharing one Txn, so Txn cannot
	// be made participant-specific without breaking that). Resource
	// alone is not always unique either — two bal legs debiting the
	// SAME source account (a completely ordinary pattern: one payer,
	// several line items) share the same Resource too. Before this
	// field existed, every adapter's own pending map was keyed by Txn
	// alone, so a second same-primitive participant's Reserve
	// silently overwrote the first's stashed params, and whichever
	// Execute goroutine ran first consumed the one surviving entry —
	// every other same-primitive participant's Execute then failed
	// outright ("no pending transfer for txn ..."), confirmed by
	// direct reproduction, not theorised.
	ParticipantID string
}

// Cache is the substrate-level reservation facility. One per xolu
// process, sharded by tenant internally (see MemCache).
//
// Hold, ClaimsByTxn, ConfirmTxn, and ReleaseTxn are mutation-adjacent
// and MUST be called only while holding the claim's tenant lock (see
// [Cache.Lock]) — they perform no locking themselves, by design,
// because the critical section they belong to spans the caller's SQL
// transaction too. ClaimsFor is safe to call either way: held-lock
// callers (tier 1, guard reads) get the fast unlocked path; unheld
// callers (tier 2, advisory) get a self-contained read lock.
type Cache interface {
	// Lock acquires tenant's write exclusion. Every subsequent Hold,
	// ClaimsByTxn, ConfirmTxn, or ReleaseTxn call for that tenant, plus
	// the caller's own SQL transaction, must occur before Unlock.
	Lock(tenant string)

	// Unlock releases tenant's write exclusion acquired by Lock.
	Unlock(tenant string)

	// Hold registers a claim. It performs NO conflict evaluation —
	// admission is the participant guard's job, already decided under
	// the same exclusion before Hold is called.
	Hold(c Claim) error

	// ClaimsFor returns the LIVE claims against a resource: claims
	// whose Deadline has lapsed are filtered here, unconditionally — no
	// caller ever sees a dead claim. Safe to call with or without
	// holding tenant's lock (see the interface doc).
	ClaimsFor(tenant, primitive, resource string) []Claim

	// ClaimsByTxn returns all live claims held by one dxp instance.
	// Requires tenant's lock held (tenant is recovered from the first
	// matching claim; an instance with zero live claims returns nil
	// with no error, which is valid — see ReleaseTxn).
	ClaimsByTxn(txn string) []Claim

	// ConfirmTxn removes an instance's claims as SATISFIED — the owning
	// transaction executed. Returns what was removed. Requires the
	// tenant's lock held by the caller (the coordinator knows the
	// tenant from the instance record).
	ConfirmTxn(tenant, txn string) []Claim

	// ReleaseTxn removes an instance's claims as ABANDONED: explicit
	// release, instance expiry, or invalidation-by-loss. The reason is
	// the coordinator's bookkeeping, not the cache's. Requires the
	// tenant's lock held by the caller.
	ReleaseTxn(tenant, txn string) []Claim
}

// OpParams is the per-operation payload a Reserve call carries into a
// participant.
//
// Decision recorded here (T-54's open question, resolved at first
// adapter contact per the proposal's §10): typed-per-primitive behind
// a one-method marker interface, not a JSON-native bag. Each primitive
// defines its own concrete OpParams type (e.g. bal's TransferParams).
// The def layer (item 20, not yet built) owns translating the wire
// JSON into the correct concrete type before calling Reserve — Reserve
// itself never parses JSON. Primitive() lets a coordinator wiring the
// wrong adapter to the wrong params fail at the call site via a type
// assertion the adapter performs, rather than silently.
type OpParams interface {
	// Primitive names the participant registry key this OpParams is
	// for, matching Claim.Primitive and the coordinator's registry key.
	Primitive() string
}

// Participant is what a primitive implements to be reserve-capable —
// the four verbs, with the cache threaded through (proposal §6).
//
// Locking is each implementation's OWN responsibility, taken and
// released within the call — not something a caller holds across it.
// A coordinator driving several participants for one instance calls
// Reserve for each in turn; each call is independently atomic with
// respect to its own tenant's cache, which is sufficient (different
// participants touch different resources) and avoids the reentrant-
// lock deadlock a caller-held lock spanning multiple participants'
// calls would invite the moment any two calls touch the same tenant.
type Participant interface {
	// Reserve evaluates the primitive's guard with live claims applied
	// (per the weight rules), and on consent Holds a claim, both inside
	// one tenant.Lock/Unlock critical section. A refusal carries the
	// primitive's own error (for XOLU-DXP002). participantID is the
	// def's own participant id — carried into the returned Claim
	// unchanged (Claim.ParticipantID's own doc explains why: Txn alone
	// cannot distinguish two same-primitive participants in one
	// instance, T-109).
	Reserve(ctx context.Context, tenant string, op OpParams,
		txn, participantID string, deadline int64, w Weight) (Claim, error)

	// Validate re-evaluates the guard for a held claim without
	// executing (3PS). Under lazy invalidation this is where a loser
	// discovers the resource is committed to a competitor
	// (XOLU-DXP007) versus a non-competitor drift (XOLU-DXP003).
	Validate(ctx context.Context, c Claim) error

	// Execute applies the effect through store, which the COORDINATOR
	// constructs and hands already-open (but not yet usable — Ready(),
	// called from inside Execute the moment it's actually about to
	// write, is what marks it usable and starts the coordinator's own
	// guard). Execute still takes its own tenant.Lock/Unlock around
	// its claims read and the store write, for the same reason
	// Reserve does — a coordinator calling several participants'
	// Execute in turn for one instance never holds two participants'
	// locks at once. store abstracts over engine: every guard-bearing
	// participant sharing one tenant SQLite file may collapse onto one
	// literal *sql.Tx (composability locality, @D06 — checked
	// directly against the doctrine's own single-tenant condition, not
	// engine type, see docs/proposals/dxp-coordinator-design.md §8);
	// a Pebble-backed participant is always genuinely independent,
	// never collapsed, since *pebble.Batch has no representation
	// inside *sql.Tx regardless of tenant scope. Result is opt-in —
	// nil is valid for a participant with nothing worth reporting.
	Execute(ctx context.Context, store ParticipantStore, c Claim) (Result, error)

	// Release abandons a claim's local consequences, if any. For most
	// primitives this is a no-op — the cache entry is removed by the
	// coordinator's ReleaseTxn — and exists for participants that stage
	// something alongside the claim.
	Release(ctx context.Context, c Claim) error

	// PostCommit fires strictly after the coordinator's own commit
	// succeeds for THIS instance — never before, and never at all for
	// an instance that ends "released" or "expired". Participants with
	// a derived/advisory plane fed only by committed writes (cal's H3
	// occupancy index is the first real one; bal's rollup plane is the
	// same shape, not wired here) use this to bring that plane up to
	// date, matching dxp-composed-commitment.md §5c's own tier-3 rule:
	// "Rollups... ingest at confirm only... consistent with the
	// standing law that no guard consults a rollup." Participants
	// without such a plane (bal today, fsm, entity, ts) are safe,
	// cheap no-ops — this verb exists so a future one doesn't need a
	// second interface change to get it.
	//
	// Best-effort by contract, not by accident: by the time this runs,
	// the instance is genuinely, durably, irreversibly committed —
	// there is nothing left to roll back, and a PostCommit failure
	// must never re-classify an already-committed instance. The
	// coordinator logs failures here; it does not fail the response
	// or retry synchronously. Named explicitly as missing in this
	// package's own prior doc, in T-83's own register entry, and in
	// cal's own dxp adapter comment before this — built directly
	// against all three, not improvised.
	PostCommit(ctx context.Context, c Claim) error
}
