// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ha1tch/xolu/pkg/dxp"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// Compile-time check: Adapter must satisfy dxp.Participant. Added
// alongside the ParticipantStore migration specifically because
// nothing previously caught this — the interface and its four
// implementations can drift silently otherwise (found directly: this
// file's Execute still had the old *sql.Tx signature after
// dxp.Participant's own signature changed, and `go build ./...`
// caught nothing, since nothing yet requires this type to satisfy the
// interface at compile time).
var _ dxp.Participant = (*Adapter)(nil)

// TransferParams is bal's dxp.OpParams (T-54's typed-per-primitive
// decision, dxp.OpParams doc): a debit-hold against one account,
// captured later as a transfer's debit leg.
//
// v1 scope, deliberate: Reserve/Execute admission-check and hold only
// the DEBIT (From) side — matching the composed-commitment proposal's
// own hotel worked example (§5a), which reserves the paying account
// only. The credit (To) side is still guarded by the ordinary ceiling
// check inside transferInTx at Execute time; a ceiling refusal there
// surfaces as XOLU-DXP008 (execute failed, participant error carried)
// rather than being caught at Reserve. Reserving both legs — a second
// Claim per Transfer — is future work if a def author needs to fail
// fast on a bounded receiving account; nothing here forecloses it.
//
// Amount is deliberately json:"-" — excluded from generic
// json.Unmarshal entirely. Checked directly against bal's own real
// HTTP handler (handleBalTransfer, pkg/server/v2_bal_handlers.go)
// before choosing this: amounts cross any JSON boundary as decimal
// STRINGS only (@B04), never a bare JSON number, refused via
// UseNumber()-based decoding and a json.Number type check before
// bal.ParseAmount(s, scale) ever runs. A plain json:"amount" tag on
// this int64 field would silently accept a raw JSON number and
// bypass @B04 entirely for any caller reaching this type through the
// dxp coordinator's own params-decoding step. That step must
// replicate the same UseNumber/ParseAmount path bal's own HTTP
// handler already uses (per-primitive, not a generic Unmarshal),
// setting Amount explicitly rather than through this struct's own
// json tags.
type TransferParams struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Amount int64  `json:"-"`
	Memo   string `json:"memo,omitempty"`
}

// Primitive satisfies dxp.OpParams.
func (TransferParams) Primitive() string { return "bal" }

// Adapter is bal's dxp.Participant. One Adapter per Store; safe for
// concurrent use.
type Adapter struct {
	store *Store
	cache *dxp.MemCache

	mu sync.Mutex
	// pending stashes each Reserve's full OpParams by (txn,
	// participantID) — NOT txn alone (T-109's own finding, fixed
	// here): two bal legs debiting the SAME source account (one payer,
	// several line items — an entirely ordinary pattern, not a corner
	// case) share the same Resource too, so neither Txn nor Resource
	// alone can distinguish them. Before this fix, a second
	// same-primitive participant's Reserve silently overwrote the
	// first's stashed params under the shared Txn key, and whichever
	// Execute goroutine ran first consumed the one surviving entry —
	// every other same-primitive participant's Execute then failed
	// outright, confirmed by direct reproduction (T-109), not
	// theorised.
	//
	// Execute's signature (dxp.Participant, fixed by the proposal) takes
	// only the Claim, which carries admission bookkeeping (Resource,
	// Amount, Deadline) but not the full transfer (both accounts,
	// memo) — Claim is deliberately resource-shaped, not op-shaped.
	// Item 21's coordinator will own this durably via the instance
	// record (participants + bindings, persisted per instance); until
	// it exists, this map is the adapter's own process memory.
	//
	// This does NOT weaken T-54's crash-abandon guarantee: a process
	// restart loses this map, but it also loses every live claim in
	// the dxp cache (memory only, by design) — an instance whose
	// pending entry vanished at restart is exactly the instance whose
	// claims vanished too, and the mount-time pass (item 21, not yet
	// built) tombstones it either way. Nothing is recoverable here
	// that was going to be recoverable through this map alone.
	pending map[string]pendingTransfer
}

// pendingTransfer is what pending actually stashes: the Reserve-time
// OpParams plus a claim-sum snapshot for Execute. The snapshot exists
// because Execute MUST NOT acquire the tenant cache lock at all — it
// runs with the coordinator's *sql.Tx already open on the
// MaxOpenConns=1 writer pool, and any tenant-lock acquisition there
// closes an AB/BA cycle against Reserve/Validate/Transfer, which hold
// the tenant lock while waiting for that same pool (T-138, proven by
// a real goroutine dump from multi-core hardware — presented as ~60s
// full-tenant stalls rescued only by the request context's deadline).
// Sums are captured at Reserve time (before Hold inserts this claim,
// so self-exclusion falls out naturally) and refreshed at Validate
// time on the pessimistic path; Optimistic claims keep the
// Reserve-time snapshot, a staleness window of the same order as the
// Validate->Execute gap. The snapshot being slightly stale is
// acceptable by design: the authoritative admission is transferInTx's
// own guarded UPDATE, and any claim admitted after this snapshot was
// itself admitted by ITS Reserve arithmetic seeing THIS claim live in
// the cache.
type pendingTransfer struct {
	TransferParams
	srcClaimed int64 // live pessimistic claims against From, excluding this claim itself
	dstClaimed int64 // live pessimistic claims against To (never includes self: v1 reserves the debit side only)
}

// pendingKey composes the pending map's real key — txn alone is not
// enough (T-109), and Resource alone is not always unique either (a
// shared debit account across legs), so both txn and the def's own
// participant id are used together.
func pendingKey(txn, participantID string) string {
	return txn + "\x00" + participantID
}

// NewAdapter wires store into cache (store.SetClaimsCache) and returns
// an Adapter ready to register with a dxp coordinator under the
// primitive key "bal".
func NewAdapter(store *Store, cache *dxp.MemCache) *Adapter {
	store.SetClaimsCache(cache)
	return &Adapter{store: store, cache: cache, pending: make(map[string]pendingTransfer)}
}

// Reserve evaluates whether tp.Amount is available against tp.From —
// balance minus floor minus every live PESSIMISTIC claim already held
// against the account, regardless of THIS reservation's own weight
// (§7: pessimistic claims bind every guard everywhere; only whether
// the NEW claim itself counts toward others' admission depends on w).
// On consent it Holds a claim and stashes tp for Execute. The whole
// evaluate-then-hold sequence runs under one tenant.Lock/Unlock
// critical section (proposal §4).
func (a *Adapter) Reserve(ctx context.Context, tenant string, op dxp.OpParams,
	txn, participantID string, deadline int64, w dxp.Weight) (dxp.Claim, error) {

	tp, ok := op.(TransferParams)
	if !ok {
		return dxp.Claim{}, fmt.Errorf("bal participant: OpParams is %T, want bal.TransferParams", op)
	}
	if tp.Amount <= 0 {
		return dxp.Claim{}, &AmountScaleError{Detail: "transfer amount must be positive"}
	}
	if tp.From == tp.To {
		return dxp.Claim{}, &AmountScaleError{Detail: "transfer requires two distinct accounts"}
	}
	if got := a.store.TenantID().String(); got != tenant {
		return dxp.Claim{}, fmt.Errorf("bal participant: tenant key %q does not match store TenantID %d (want %q)", tenant, a.store.TenantID(), got)
	}

	a.cache.Lock(tenant)
	defer a.cache.Unlock(tenant)

	balance, floor, postable, err := a.store.balanceAndFloor(ctx, tp.From)
	if err != nil {
		return dxp.Claim{}, err
	}
	if !postable {
		return dxp.Claim{}, &NotPostableError{AccountID: tp.From}
	}

	claimed := pessimisticClaimSum(a.cache, tenant, dxpResource(tp.From))
	if balance-floor-claimed < tp.Amount {
		return dxp.Claim{}, &BoundsError{AccountID: tp.From, Side: "floor"}
	}
	// Snapshot the credit side too, under the same lock, BEFORE Hold
	// inserts this claim — both sums therefore exclude self without
	// any subtraction (see pendingTransfer's doc for why Execute needs
	// a snapshot at all: T-138).
	dstClaimed := pessimisticClaimSum(a.cache, tenant, dxpResource(tp.To))

	cl := dxp.Claim{
		Txn: txn, Primitive: "bal", Tenant: tenant, ParticipantID: participantID,
		Resource: dxpResource(tp.From), Weight: w, Amount: tp.Amount, Deadline: deadline,
		WriteTargets: []string{dxpResource(tp.From), dxpResource(tp.To)},
	}
	if err := a.cache.Hold(cl); err != nil {
		return dxp.Claim{}, err
	}

	a.mu.Lock()
	a.pending[pendingKey(txn, participantID)] = pendingTransfer{
		TransferParams: tp, srcClaimed: claimed, dstClaimed: dstClaimed,
	}
	a.mu.Unlock()
	return cl, nil
}

// Validate re-checks that the sum of every live pessimistic claim
// against c's account — c's own included — still fits within balance
// minus floor. That sum already includes c.Amount, so this is exactly
// the invariant Reserve established, re-evaluated against whatever the
// account's balance and floor are now (proposal §6: "guard inputs may
// change during the window"). The balance read and the claims read run
// under the SAME tenant.Lock, matching Reserve — a read split across
// two lock acquisitions would reopen the TOCTOU gap the lock exists to
// close.
//
// Optimistic claims are invisible to guard arithmetic everywhere (§7)
// and have nothing of their own for Validate to re-check here; the
// coordinator (item 21, not yet built) discovers invalidation-by-loss
// for them via ConfirmTxn's empty-return contract, not via this
// method — Validate cannot itself distinguish DXP007 (lost to a
// competitor) from DXP003 (drift) without coordinator-level context
// on who else held claims, so it returns a bal-native error and
// leaves that classification to the coordinator, consistent with
// Reserve's refusals.
func (a *Adapter) Validate(ctx context.Context, c dxp.Claim) error {
	if c.Weight == dxp.Optimistic {
		return nil
	}
	a.cache.Lock(c.Tenant)
	defer a.cache.Unlock(c.Tenant)

	accountID := accountFromResource(c.Resource)
	balance, floor, postable, err := a.store.balanceAndFloor(ctx, accountID)
	if err != nil {
		return err
	}
	if !postable {
		return &NotPostableError{AccountID: accountID}
	}
	claimedIncludingSelf := pessimisticClaimSum(a.cache, c.Tenant, c.Resource)
	if balance-floor-claimedIncludingSelf < 0 {
		return &BoundsError{AccountID: accountID, Side: "floor"}
	}

	// Refresh the Execute snapshot while the tenant lock is already
	// held here (T-138: Execute itself must never touch this lock).
	// Lock nesting is cache -> mu, the same order Reserve already
	// established. Only the pessimistic path reaches this point (the
	// optimistic early return above), so self-exclusion is always
	// claimedIncludingSelf minus this claim's own amount.
	a.mu.Lock()
	p, ok := a.pending[pendingKey(c.Txn, c.ParticipantID)]
	a.mu.Unlock()
	if ok {
		p.srcClaimed = claimedIncludingSelf - c.Amount
		p.dstClaimed = pessimisticClaimSum(a.cache, c.Tenant, dxpResource(p.To))
		a.mu.Lock()
		a.pending[pendingKey(c.Txn, c.ParticipantID)] = p
		a.mu.Unlock()
	}
	return nil
}

// Execute applies c's transfer via transferInTx against the
// coordinator-supplied tx (proposal §11: one SQL transaction for every
// participant; the coordinator opens and commits tx, never Execute).
// It does not emit rollup deltas: those must run strictly after
// commit (see Transfer), and a shared multi-participant transaction
// has no single commit point this method can observe. A coordinator
// driving Execute is responsible for its own post-commit rollup pass
// per participant; dxp.Participant has no post-commit verb today —
// flagged as an item 21 gap (recorded in TRACKING.md under T-54), not
// silently dropped.
//
// srcClaimed/dstClaimed come from the pendingTransfer snapshot
// (captured at Reserve, refreshed at Validate for pessimistic claims)
// rather than being read fresh here. Execute runs with the
// coordinator's transaction already open on the single-connection
// writer pool, so acquiring the tenant cache lock here — however
// briefly — closes the AB/BA cycle T-138 documents against
// Reserve/Validate/Transfer, which hold that lock while waiting for
// this pool. srcClaimed already excludes c itself (snapshot taken
// before Hold, or explicitly subtracted at Validate); dstClaimed never
// includes self, since v1 reserves the debit side only. A DIFFERENT
// instance's post-snapshot claim is still respected transitively: its
// own Reserve arithmetic saw this claim live in the cache, and the
// authoritative admission remains transferInTx's guarded UPDATE.
func (a *Adapter) Execute(ctx context.Context, store dxp.ParticipantStore, c dxp.Claim) (dxp.Result, error) {
	s, ok := store.(*dxp.SQLStore)
	if !ok {
		return nil, fmt.Errorf("bal participant: expected sql-backed store, got %s", store.Engine())
	}

	a.mu.Lock()
	tp, ok := a.pending[pendingKey(c.Txn, c.ParticipantID)]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("bal participant: no pending transfer for txn %s participant %s", c.Txn, c.ParticipantID)
	}

	srcClaimed, dstClaimed := tp.srcClaimed, tp.dstClaimed

	// Ready() called here, immediately before the actual write — the
	// moment this participant is genuinely about to touch the store,
	// not before (dxp-coordinator-design.md §2).
	if err := store.Ready(ctx); err != nil {
		return nil, err
	}

	_, _, err := a.store.transferInTx(ctx, s.Tx, c.Txn, tp.From, tp.To, tp.Amount, tp.Memo,
		executionInstant(), srcClaimed, dstClaimed)
	if err != nil {
		return nil, err
	}

	// pending is NOT cleared here: PostCommit needs tp.From/tp.To to
	// fold the rollup deltas into bal's own cascade after genuine
	// commit (T-62/T-83's own gap; the mechanism itself was built for
	// cal, T-108, and is wired here for bal onto the same interface).
	// Mirrors cal's own Execute/PostCommit lifecycle split exactly
	// (T-108's doc) -- cleaned up by whichever of PostCommit (success)
	// or Release (every other outcome) actually runs for this txn.
	return nil, nil
}

// Release drops txn's stashed params, if any. Idempotent and
// unconditional per the proposal's error taxonomy (§16): releasing an
// unknown or already-cleared txn is a no-op, never an error. The cache
// entry itself is removed by the coordinator's ReleaseTxn, not here.
func (a *Adapter) Release(ctx context.Context, c dxp.Claim) error {
	a.mu.Lock()
	delete(a.pending, pendingKey(c.Txn, c.ParticipantID))
	a.mu.Unlock()
	return nil
}

// PostCommit folds the transfer's two signed legs into bal's own
// rollup cascade (@B05) after the coordinator has confirmed genuine,
// durable commit -- the same post-commit rollup pass Transfer's own
// doc names as owed for any coordinator driving transferInTx directly
// (T-62/T-83). The mechanism itself (dxp.Participant.PostCommit) was
// built for cal (T-108), which named bal's rollup plane as the next
// real consumer; this wires bal onto that same mechanism.
//
// Re-reads fresh rather than trusting anything captured before commit,
// matching cal's own PostCommit discipline exactly: accountKeyOf looks
// up each leg's account_key fresh, and journalInstant reads the actual
// recorded `at` from the journal entry Execute already wrote -- not a
// freshly-taken "now", which could drift into the wrong rollup bucket
// if PostCommit runs some time after the transaction it follows.
func (a *Adapter) PostCommit(ctx context.Context, c dxp.Claim) error {
	a.mu.Lock()
	key := pendingKey(c.Txn, c.ParticipantID)
	tp, ok := a.pending[key]
	if ok {
		delete(a.pending, key)
	}
	a.mu.Unlock()
	if !ok {
		// Already cleaned up by Release for some other reason -- nothing
		// left to do, matching cal's own PostCommit contract exactly.
		return nil
	}

	srcKey, err := a.store.accountKeyOf(ctx, tp.From)
	if err != nil {
		return err
	}
	dstKey, err := a.store.accountKeyOf(ctx, tp.To)
	if err != nil {
		return err
	}
	at, err := a.store.journalInstant(ctx, c.Txn, srcKey)
	if err != nil {
		return err
	}

	// Rollup plane (@B05): best-effort, matching Transfer's own
	// precedent exactly -- a derived-plane failure must never surface
	// as though the (already-committed) transfer itself failed.
	// Staleness here is detected by the existing rollup oracle and
	// repaired by RebuildRollup; no guard reads the rollup (@C04a), so
	// this is a performance/observability matter, never a correctness
	// one.
	if emitErr := a.store.EmitDeltas(ctx, srcKey, dstKey, tp.Amount, at); emitErr != nil {
		a.store.rollupDegraded(emitErr)
	}
	return nil
}

// accountFromResource inverts dxpResource: "acct:<id>" -> "<id>".
func accountFromResource(resource string) string {
	return strings.TrimPrefix(resource, "acct:")
}

// executionInstant returns the canonical "now" for a journal entry
// written by Execute — the moment of execution, not the claim's
// deadline (a claim's Deadline is an expiry bound, never the entry's
// recorded time). xolutime.Now is the sanctioned source for any value
// that will be persisted, per its own doc.
func executionInstant() time.Time {
	return ot.Now().Time()
}
