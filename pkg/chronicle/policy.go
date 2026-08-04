// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package chronicle

import (
	"errors"
	"fmt"
	"time"
)

// TemporalPolicy is a chronicle-based timeline's declared arrival-order
// contract (T-55). One vocabulary across every primitive that keeps a
// chronicle timeline (/bal accounts, /ts timelines, /cal calendars);
// each primitive stores the value in its OWN guard plane — a column on
// the timeline's authoritative row, read inside the transaction of the
// write it governs — never in /meta (@C04c: guards do not read meta).
type TemporalPolicy string

const (
	// AppendOnly is the default: accounting-style. An entry dated
	// strictly before the timeline's latest recorded entry is refused
	// by the primitive's guard with the primitive's own error.
	// Same-instant entries are admitted — batches and multi-leg writes
	// legitimately share a timestamp, and refusing them would make the
	// policy unusable at second granularity. (Deviation from T-55's
	// filed "at-or-before" wording, recorded there.)
	AppendOnly TemporalPolicy = "append_only"

	// Backdated admits entries in any arrival order — museum records,
	// wikipedia-style timelines, facts of the past arriving as found.
	// Under this policy the primitive's checkpoint-invalidation
	// machinery is active: a write dated at-or-before an existing
	// checkpoint marks that checkpoint and every later one stale
	// (lazy; recomputed on the next Checkpoint call, skipped by
	// as-of reads meanwhile).
	Backdated TemporalPolicy = "backdated"
)

// ErrBackdatedRefused is the sentinel a guard's refusal wraps when an
// append_only timeline receives a strictly-backdated entry. Primitives
// wrap it in their own error types and codes (bal: XOLU-BAL006).
var ErrBackdatedRefused = errors.New(
	"entry predates the timeline's latest entry (policy append_only)")

// ParsePolicy validates a stored or supplied policy string. The empty
// string is the default (AppendOnly) so that pre-policy rows and
// zero-value definitions get accounting semantics without migration.
func ParsePolicy(s string) (TemporalPolicy, error) {
	switch TemporalPolicy(s) {
	case "", AppendOnly:
		return AppendOnly, nil
	case Backdated:
		return Backdated, nil
	default:
		return "", fmt.Errorf("unknown temporal policy %q", s)
	}
}

// CheckAdmission applies the policy to a candidate entry instant given
// the timeline's latest recorded instant (zero time when the timeline
// is empty). Returns nil to admit, ErrBackdatedRefused (wrapped with
// both instants) to refuse. Pure function: primitives that fold the
// predicate into SQL for write-first transactions must implement the
// SAME rule — strictly-earlier refused, same-instant admitted — and
// their conformance tests should assert agreement with this function.
func (p TemporalPolicy) CheckAdmission(at, latest time.Time) error {
	if p == Backdated || latest.IsZero() || !at.Before(latest) {
		return nil
	}
	return fmt.Errorf("%w: entry %s < latest %s",
		ErrBackdatedRefused, at.UTC().Format(time.RFC3339Nano),
		latest.UTC().Format(time.RFC3339Nano))
}

// CanTransition reports whether a timeline's policy may change from
// one value to another. Widening (append_only → backdated) is always
// legal: it only admits more. Narrowing is refused in v1 — it asserts
// the recorded past is monotonic, which is a verification-bearing
// operation deferred by T-55's scope.
func CanTransition(from, to TemporalPolicy) bool {
	return from == to || (from == AppendOnly && to == Backdated)
}
