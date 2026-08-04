// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package reserved implements the reserved-commit facility (@D05b): the
// engine-level lifecycle that cal proposals, bal holds, blob leases, and
// entity staged writes are all instances of. Participants supply only a
// conflict predicate and a weight policy; the facility owns the
// tentative-row convention, the state walk, and the sweep.
//
// # The convention
//
// A guard-bearing table adopts three columns (see sql.go for the DDL
// fragment):
//
//	txn_id           TEXT     NULL      -- owning transaction; set at reserve
//	state            TEXT     NOT NULL DEFAULT 'committed'
//	reserve_deadline INTEGER  NULL      -- unix nanoseconds; set while reserved
//
// Only two states are ever persisted: 'committed' (the default — every
// ordinary row) and 'reserved' (a tentative row inside its TTL window).
// The four terminal outcomes of §D05b's walk
//
//	reserved → confirmed | released | expired | invalidated
//
// are transitions, not resting states: Confirm CAS-flips the row to
// 'committed'; Release deletes it; the sweeper deletes it after its
// deadline (expired); and under optimistic weight a competing
// transaction's confirmation is what makes the loser's own confirmation
// fail (invalidated) — the loser learns its fate at its next CAS, and
// its rows are released by its coordinator or collected at TTL. bal's
// journal state column (@B10) is this convention's first schema
// instance, promoted here to substrate-wide practice.
//
// # The two weights
//
// Each participant declares how its reserved rows count in admission:
//
//   - Pessimistic (2PS semantics): reserved rows are honoured like real
//     commits by every guard — a reserved debit reduces available
//     balance, a reserved slot refuses competitors — until confirmation
//     or expiry.
//   - Optimistic (3PS semantics): reserved rows are invisible to
//     admission; conflicting reservations coexist, and the first
//     confirmer wins.
//
// # Deadline authority
//
// The deadline is authoritative; the sweeper is only hygiene. Guard
// predicates compare reserve_deadline inline (see GuardPredicate), so a
// lapsed reservation stops counting the instant it expires and cannot
// be confirmed even while unswept. This is what makes coordinator death
// survivable: an abandoned reservation self-releases by clock, not by
// cleanup.
//
// # Doctrine: guards never use accelerators
//
// Enforcement reads go to the guard plane — the SQL tables carrying
// this convention — always. In-memory accelerators are commit-fed and
// serve application traversal only; a commit-fed accelerator answering
// a tentative-aware question would answer wrongly (@D05c tier 1, and
// the G-12 lesson written down as law).
//
// # Visibility taxonomy (@D05c)
//
//	tier 1  guard plane      tentative-aware, mandatory (per weight)
//	tier 2  advisory planes  weight-optional, sweep-cleaned
//	tier 3  analytic planes  commit-fed, strictly
//
// See VisibilityTier and PredicateFor.
package reserved

import (
	"time"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// State is the persisted lifecycle state of a row under the convention.
// Only StateCommitted and StateReserved are ever stored; the terminal
// outcomes of the reserved walk are transitions (see Outcome).
type State string

const (
	// StateCommitted marks an ordinary, fully real row — the column
	// default, and the only state application reads ever see.
	StateCommitted State = "committed"

	// StateReserved marks a tentative row inside its TTL window, owned
	// by the transaction named in txn_id.
	StateReserved State = "reserved"
)

// Weight is a participant's declared admission policy for its reserved
// rows (@D05b "the two weights").
type Weight int

const (
	// Pessimistic gives 2PS semantics: guards honour reserved rows like
	// commits until confirmation or expiry.
	Pessimistic Weight = iota

	// Optimistic gives 3PS semantics: guards ignore reserved rows;
	// conflicting reservations coexist and the first confirmer wins.
	Optimistic
)

// String returns the weight's name for logs and errors.
func (w Weight) String() string {
	switch w {
	case Pessimistic:
		return "pessimistic"
	case Optimistic:
		return "optimistic"
	default:
		return "unknown"
	}
}

// VisibilityTier names the three planes of the @D05c taxonomy. It
// exists so that read paths can declare which tier they serve and take
// their predicate from PredicateFor rather than hand-rolling one.
type VisibilityTier int

const (
	// TierGuard is the guard plane: tentative-aware, mandatory. Sees
	// committed rows plus live reserved rows per the declared weight.
	TierGuard VisibilityTier = iota

	// TierAdvisory covers derived availability-type structures (cal's
	// occupancy index). May ingest reservations when the participant's
	// weight is pessimistic; correctness never depends on this tier.
	TierAdvisory

	// TierAnalytic covers rollups, FTS, events, caches, and the
	// in-memory graph: commit-fed, strictly. A reserved row is
	// invisible here; it becomes visible at confirm.
	TierAnalytic
)

// Outcome classifies the result of a Confirm attempt. Exactly one
// outcome is returned per attempt; OutcomeConfirmed is the only success.
type Outcome int

const (
	// OutcomeConfirmed: the CAS fired — the reservation's rows are now
	// committed.
	OutcomeConfirmed Outcome = iota

	// OutcomeAlreadyConfirmed: the rows are already committed under
	// this txn_id — an idempotent retry of a confirmation that won.
	OutcomeAlreadyConfirmed

	// OutcomeExpired: the reservation's deadline lapsed before the CAS.
	// The deadline is authoritative: this outcome is returned even if
	// the sweeper has not yet collected the rows.
	OutcomeExpired

	// OutcomeGone: no rows carry this txn_id — the reservation was
	// released, invalidated and swept, or never existed here.
	OutcomeGone
)

// String returns the outcome's name for logs and errors.
func (o Outcome) String() string {
	switch o {
	case OutcomeConfirmed:
		return "confirmed"
	case OutcomeAlreadyConfirmed:
		return "already-confirmed"
	case OutcomeExpired:
		return "expired"
	case OutcomeGone:
		return "gone"
	default:
		return "unknown"
	}
}

// Deadline computes a reservation deadline as now + ttl, in the
// canonical persisted representation (unix nanoseconds, UTC). A
// non-positive ttl yields a deadline already in the past, which the
// guard plane treats as lapsed immediately — reserving with one is a
// caller error that the clock, not a validation branch, punishes.
func Deadline(now ot.Instant, ttl time.Duration) int64 {
	return now.Time().Add(ttl).UnixNano()
}
