// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ha1tch/xolu/pkg/dxp"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// Compile-time check: Adapter must satisfy dxp.Participant — see
// bal's identical guard (pkg/bal/dxp_adapter.go) for why this was
// added now rather than assumed already covered.
var _ dxp.Participant = (*Adapter)(nil)

// CalTransitionParams is cal's dxp.OpParams (T-54's typed-per-primitive
// decision): places a booking on CalendarID at Span, admission-checked
// and held under one dxp reservation, executed as a real H1 write on
// commit. State defaults to the calendar's own DefaultState when unset
// (matching MatchCommit's own convention).
//
// v1 scope, deliberate, matching bal.TransferParams' own precedent of
// naming what's deferred rather than silently narrowing: this reserves
// CREATION of a new booking only (the proposed/binding placement a
// hotel-style dxp def actually needs, per dxp-composed-commitment.md's
// own worked example). Confirming an already-proposed booking (the
// ordinary CalConfirm path) is not part of this v1 — nothing here
// forecloses adding it as a second OpParams shape later.
type CalTransitionParams struct {
	CalendarID string `json:"calendar"`
	BookingID  string `json:"booking_id,omitempty"`
	Span       Span   `json:"span"`
	Mode       Mode   `json:"mode,omitempty"`
	Bearer     uint64 `json:"bearer,omitempty"`
	State      State  `json:"state,omitempty"` // target state; zero value defers to the calendar's DefaultState
}

// Primitive satisfies dxp.OpParams.
func (CalTransitionParams) Primitive() string { return "cal" }

// Adapter is cal's dxp.Participant. One Adapter per tenant assembly;
// safe for concurrent use.
type Adapter struct {
	lc    *Lifecycle
	src   *SQLiteBookingSource
	cache *dxp.MemCache

	mu sync.Mutex
	// pending stashes each Reserve's full OpParams by (txn,
	// participantID) — NOT txn alone (T-109's own finding, fixed
	// here): a def with two cal participants booking different
	// calendars in one instance would otherwise silently overwrite
	// each other's stashed reservation under the shared Txn key.
	// Claim is deliberately resource-shaped (proposal's own OpParams
	// doc), and for cal a single Claim only ever encodes ONE
	// day-bucket of a span that may touch several, so Execute and
	// Validate reconstruct the full reservation from here, never from
	// a single Claim's Resource alone. Same crash-abandon reasoning as
	// bal's own doc: a process restart loses this map, but it also
	// loses every live claim in the dxp cache (memory only, by
	// design) at the same moment.
	pending map[string]CalTransitionParams
}

// pendingKey composes the pending map's real key — see bal.pendingKey
// for the identical rationale (T-109): txn alone cannot distinguish
// two same-primitive participants in one instance.
func pendingKey(txn, participantID string) string {
	return txn + "\x00" + participantID
}

// NewAdapter wires lc (for spanConflicts and calendar lookup) and src
// (for the transactional H1 write Execute needs) into cache, and
// returns an Adapter ready to register with a dxp coordinator under
// the primitive key "cal". lc and src must be the same tenant
// assembly's Lifecycle/SQLiteBookingSource pair — mirroring
// cal.Manager.assemble's own construction, not a separate wiring path.
func NewAdapter(lc *Lifecycle, src *SQLiteBookingSource, cache *dxp.MemCache) *Adapter {
	return &Adapter{lc: lc, src: src, cache: cache, pending: make(map[string]CalTransitionParams)}
}

// dxpResource is the cache resource key for one day-bucket of one
// calendar — "cal:<calendarID>:<YYYY-MM-DD>", matching dxp.Claim.Resource's
// own doc comment example ("cal:room7:2026-08-01") precisely, not an
// independently-invented format. A booking spanning N days holds N
// claims under one txn (see Reserve) — coarser than cal's own
// 5-minute occupancy quantum, a documented approximation (T-54's own
// register note): two bookings on the same calendar the same day but
// at disjoint hours will contend for the SAME day-bucket resource even
// though their actual spans never overlap. Accepted deliberately:
// dxp reservations are rare, deliberate, human-paced events relative
// to ordinary booking traffic, and the alternative (a bespoke
// in-memory interval-overlap structure layered on the generic cache)
// is real, unbuilt future work, not a correctness requirement for v1.
func dxpResource(calendarID string, dayNanos int64) string {
	return "cal:" + calendarID + ":" + time.Unix(0, dayNanos).UTC().Format("2006-01-02")
}

// dxpDayBuckets returns every day-bucket resource key a span touches,
// via SpanDays — the same decomposition cal's own occupancy index
// already uses, not an independently-invented one.
func dxpDayBuckets(calendarID string, span Span) ([]string, error) {
	days, err := SpanDays(span)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(days))
	for i, d := range days {
		keys[i] = dxpResource(calendarID, d.DayNanos)
	}
	return keys, nil
}

// Reserve admission-checks tp against BOTH the ordinary booking path
// (spanConflicts against H1 — the cross-path guarantee: a live
// ordinary booking must refuse a new dxp reservation attempting the
// same span) and every live dxp claim on each day-bucket the span
// touches (the mixed-weight admission rule: a live pessimistic claim
// refuses any new reservation of either weight; a live optimistic
// claim only refuses a new pessimistic one — matching fsm's own
// Reserve exactly). On consent it Holds one claim per day-bucket,
// all under txn, and stashes tp for Execute/Validate. The whole
// evaluate-then-hold sequence runs under one tenant.Lock/Unlock
// critical section (proposal §4), matching bal/fsm.
func (a *Adapter) Reserve(ctx context.Context, tenantKey string, op dxp.OpParams,
	txn, participantID string, deadline int64, w dxp.Weight) (dxp.Claim, error) {

	tp, ok := op.(CalTransitionParams)
	if !ok {
		return dxp.Claim{}, fmt.Errorf("cal participant: OpParams is %T, want cal.CalTransitionParams", op)
	}
	if tp.CalendarID == "" || tp.BookingID == "" {
		return dxp.Claim{}, fmt.Errorf("cal participant: empty calendar_id or booking_id")
	}
	if !tp.Span.Valid() {
		return dxp.Claim{}, fmt.Errorf("cal participant: invalid span")
	}
	if got := a.src.TenantID().String(); got != tenantKey {
		return dxp.Claim{}, fmt.Errorf("cal participant: tenant key %q does not match store TenantID %d (want %q)", tenantKey, a.src.TenantID(), got)
	}

	calRow, ok := a.src.calendar(tp.CalendarID)
	if !ok {
		return dxp.Claim{}, fmt.Errorf("cal participant: %w: %q", ErrUnknownCalendar, tp.CalendarID)
	}
	state := tp.State
	if state == "" {
		state = calRow.DefaultState
	}
	tp.State = state // resolved before stashing: Execute must never read the calendar again (see its own doc)
	plane := planeForDefaultState(state)

	buckets, err := dxpDayBuckets(tp.CalendarID, tp.Span)
	if err != nil {
		return dxp.Claim{}, err
	}

	a.cache.Lock(tenantKey)
	defer a.cache.Unlock(tenantKey)

	// Cross-path guarantee, ordinary-path half: a live ordinary
	// booking must be visible to a NEW dxp reservation attempt.
	if conflicts, err := a.lc.spanConflicts(calRow, tp.Span, plane, ""); err != nil {
		return dxp.Claim{}, err
	} else if len(conflicts) > 0 {
		return dxp.Claim{}, &BookingConflictError{CalendarID: tp.CalendarID, Conflicts: conflicts}
	}

	// dxp-path half: mixed-weight admission, per day-bucket.
	for _, resource := range buckets {
		for _, c := range a.cache.ClaimsForLocked(tenantKey, "cal", resource) {
			if c.Weight == dxp.Pessimistic || w == dxp.Pessimistic {
				return dxp.Claim{}, &BookingConflictError{CalendarID: tp.CalendarID,
					Conflicts: []Conflict{{With: c.Txn, Reason: "live dxp reservation"}}}
			}
		}
	}

	var first dxp.Claim
	for i, resource := range buckets {
		cl := dxp.Claim{
			Txn: txn, Primitive: "cal", Tenant: tenantKey, ParticipantID: participantID,
			Resource: resource, Weight: w, Amount: 1, Deadline: deadline,
		}
		if err := a.cache.Hold(cl); err != nil {
			return dxp.Claim{}, err
		}
		if i == 0 {
			first = cl
		}
	}

	a.mu.Lock()
	a.pending[pendingKey(txn, participantID)] = tp
	a.mu.Unlock()
	return first, nil
}

// Validate re-runs Reserve's FULL admission check (both the ordinary
// and dxp-path halves, across every day-bucket the original span
// touches, not just the single bucket c.Resource happens to name) —
// c alone cannot carry enough information for a partial re-check the
// way bal's single-resource Validate can, so this reconstructs the
// full reservation from pending[c.Txn] first. Optimistic claims are
// invisible to guard arithmetic everywhere (bal's own §7 doc,
// generalised) and pass unconditionally, matching bal/fsm.
func (a *Adapter) Validate(ctx context.Context, c dxp.Claim) error {
	if c.Weight == dxp.Optimistic {
		return nil
	}
	a.mu.Lock()
	tp, ok := a.pending[pendingKey(c.Txn, c.ParticipantID)]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("cal participant: no pending reservation for txn %s participant %s", c.Txn, c.ParticipantID)
	}

	calRow, ok := a.src.calendar(tp.CalendarID)
	if !ok {
		return fmt.Errorf("cal participant: %w: %q", ErrUnknownCalendar, tp.CalendarID)
	}
	// tp.State is already resolved by Reserve (never empty in pending)
	// — no fallback needed here, unlike the old pre-fix shape.
	plane := planeForDefaultState(tp.State)

	buckets, err := dxpDayBuckets(tp.CalendarID, tp.Span)
	if err != nil {
		return err
	}

	a.cache.Lock(c.Tenant)
	defer a.cache.Unlock(c.Tenant)

	if conflicts, err := a.lc.spanConflicts(calRow, tp.Span, plane, tp.BookingID); err != nil {
		return err
	} else if len(conflicts) > 0 {
		return &BookingConflictError{CalendarID: tp.CalendarID, Conflicts: conflicts}
	}
	for _, resource := range buckets {
		for _, other := range a.cache.ClaimsForLocked(c.Tenant, "cal", resource) {
			if other.Txn == c.Txn {
				continue // this reservation's own claim, not a competitor
			}
			if other.Weight == dxp.Pessimistic || c.Weight == dxp.Pessimistic {
				return &BookingConflictError{CalendarID: tp.CalendarID,
					Conflicts: []Conflict{{With: other.Txn, Reason: "live dxp reservation"}}}
			}
		}
	}
	return nil
}

// Execute applies tp's booking via putBookingInTx against the
// coordinator-supplied tx (proposal §11: one SQL transaction for
// every participant; the coordinator opens and commits tx, never
// Execute). It does NOT touch the Pebble occupancy index (H3) —
// that plane is advisory (chronicle-substrate.md §4b/dxp-composed-
// commitment.md: "no guard ever consults it"), and updating it here,
// before the coordinator's own commit is final, would be actively
// wrong, not just premature: in the collapsed path Execute runs
// before the barrier T-99's own fix introduced, so the write it just
// made is not yet durable; in the phased path a sibling participant
// can still fail and tear the instance after this one's own commit
// succeeds. PostCommit (below) is where H3 actually gets updated, and
// only once the coordinator itself knows the instance is genuinely,
// irreversibly committed. pending is deliberately NOT cleared here
// any more (T-108) — PostCommit needs tp.BookingID to re-read the
// final booking from H1, and Release cleans it up on every path that
// isn't a genuine full-instance commit.
func (a *Adapter) Execute(ctx context.Context, store dxp.ParticipantStore, c dxp.Claim) (dxp.Result, error) {
	s, ok := store.(*dxp.SQLStore)
	if !ok {
		return nil, fmt.Errorf("cal participant: expected sql-backed store, got %s", store.Engine())
	}

	a.mu.Lock()
	tp, ok := a.pending[pendingKey(c.Txn, c.ParticipantID)]
	a.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("cal participant: no pending reservation for txn %s participant %s", c.Txn, c.ParticipantID)
	}

	// tp.State is already resolved (Reserve's own doc: never empty by
	// the time it reaches pending) -- Execute must NOT call
	// a.src.calendar() here. That method reads through s.db, a
	// SEPARATE, non-transactional connection from the caller's tx;
	// calling it while tx is still open deadlocks SQLite (found by
	// actually running this test, not by reasoning about it -- the
	// first version of this method did exactly that and hung).
	now := ot.Now()
	b := Booking{
		BookingID:  tp.BookingID,
		CalendarID: tp.CalendarID,
		State:      tp.State,
		Span:       tp.Span,
		Mode:       tp.Mode,
		Bearer:     tp.Bearer,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	// Ready() called here, immediately before the actual write.
	if err := store.Ready(ctx); err != nil {
		return nil, err
	}

	if err := a.src.putBookingInTx(ctx, s.Tx, b); err != nil {
		return nil, err
	}

	return nil, nil
}

// Release drops txn's stashed params, if any. Idempotent and
// unconditional, matching bal/fsm exactly. The cache entry itself is
// removed by the coordinator's ReleaseTxn, not here.
func (a *Adapter) Release(ctx context.Context, c dxp.Claim) error {
	a.mu.Lock()
	delete(a.pending, pendingKey(c.Txn, c.ParticipantID))
	a.mu.Unlock()
	return nil
}

// PostCommit brings H3 (the Pebble occupancy index) up to date for a
// booking the coordinator has now confirmed genuinely, durably
// committed — the mechanism T-83 named as missing, built directly
// against dxp.Participant's own new doc, not improvised.
//
// Re-reads the booking from H1 rather than trusting Execute's own
// stashed tp: by the time PostCommit runs, H1 is the one thing
// guaranteed final, and re-reading is the same discipline Execute's
// own doc already established for why it must NOT read through a.src
// while a transaction is open — here no transaction is open (the
// coordinator's own commit and its separate terminal-marking
// transaction have both already closed by construction, since
// PostCommit only ever fires after that), so the read is safe, and
// reading fresh rather than trusting a snapshot from before commit is
// the more honest source of truth regardless.
//
// addToPlane is documented safe to call more than once (an OR over a
// bitmap cannot corrupt shared bits by re-adding the same booking),
// so no additional idempotency guard is needed here beyond that.
func (a *Adapter) PostCommit(ctx context.Context, c dxp.Claim) error {
	a.mu.Lock()
	key := pendingKey(c.Txn, c.ParticipantID)
	tp, ok := a.pending[key]
	if ok {
		delete(a.pending, key)
	}
	a.mu.Unlock()
	if !ok {
		// Already cleaned up by Release for some other reason — nothing
		// left to do. Not an error, matching Release's own unconditional
		// no-op-on-missing-entry contract.
		return nil
	}

	b, found := a.src.booking(tp.CalendarID, tp.BookingID)
	if !found {
		return fmt.Errorf("cal participant: PostCommit: booking %q/%q not found in H1 after commit (unexpected)", tp.CalendarID, tp.BookingID)
	}
	if _, occ := b.State.occupiesPlane(); !occ {
		// Booked into a non-occupying state — nothing for H3 to add.
		return nil
	}
	return a.lc.index.addToPlane(b)
}

// BookingConflictError reports a dxp reservation refused because the
// requested span conflicts with either a live ordinary booking or a
// live competing dxp reservation.
type BookingConflictError struct {
	CalendarID string
	Conflicts  []Conflict
}

func (e *BookingConflictError) Error() string {
	return fmt.Sprintf("cal: reservation on %q refused: %d conflict(s)", e.CalendarID, len(e.Conflicts))
}
