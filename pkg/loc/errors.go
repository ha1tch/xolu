// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import "fmt"

// Typed errors (XOLU-LOC family), matching bal's own convention
// (pkg/bal/store.go's "Typed errors" section) — CapacityError and
// InvariantError already exist (admission.go, Stage 2); this file
// adds the rest, retrofitted from Stage 1/3's plain fmt.Errorf calls
// now that Stage 6's HTTP layer needs errors.As-based status mapping,
// not string-prefix parsing.

// UnknownLocationError: XOLU-LOC003. HTTP 404.
type UnknownLocationError struct{ LocationID string }

func (e *UnknownLocationError) Error() string {
	return fmt.Sprintf("XOLU-LOC003: unknown location_id %q", e.LocationID)
}

// UnknownFenceError: XOLU-LOC004. HTTP 404.
type UnknownFenceError struct{ FenceID string }

func (e *UnknownFenceError) Error() string {
	return fmt.Sprintf("XOLU-LOC004: unknown fence %q", e.FenceID)
}

// UnknownSubjectError: XOLU-LOC005 — a fence's subject reference does
// not resolve, T-127. In practice this means the (kind, key) shape
// itself is invalid (unknown kind, malformed key, or a subject that's
// neither the "kind:key" shorthand nor a REF object) — not a live
// existence check against an entity row, since nothing in this
// codebase's meta-subject addressing does that (pkg/storage/
// meta_subject.go is engine-inert by design; /meta's own handlers
// validate shape only, never existence). HTTP 404.
type UnknownSubjectError struct{ Detail string }

func (e *UnknownSubjectError) Error() string {
	return fmt.Sprintf("XOLU-LOC005: %s", e.Detail)
}

// RootAnchorError: XOLU-LOC010 — a root location was defined or
// patched without a placement anchor. HTTP 400.
type RootAnchorError struct{ LocationID string }

func (e *RootAnchorError) Error() string {
	return fmt.Sprintf("XOLU-LOC010: root location %q must carry a placement anchor", e.LocationID)
}

// CapacityOnNonPostableError: XOLU-LOC011. HTTP 400.
type CapacityOnNonPostableError struct{ LocationID string }

func (e *CapacityOnNonPostableError) Error() string {
	return fmt.Sprintf("XOLU-LOC011: capacity set on a non-postable node %q", e.LocationID)
}

// OccupiedError: XOLU-LOC012 — delete refused, the node (or a
// descendant) currently holds an assigned subject, unconditionally,
// regardless of force (loc-01-rest-api.md §1's own "no flag overrides
// it" rule). HTTP 409.
type OccupiedError struct{ LocationID string }

func (e *OccupiedError) Error() string {
	return fmt.Sprintf("XOLU-LOC012: location %q (or a descendant) currently holds an assigned subject", e.LocationID)
}

// HasChildrenError: XOLU-LOC013 — delete refused, children present
// and force was not set. HTTP 409.
type HasChildrenError struct{ LocationID string }

func (e *HasChildrenError) Error() string {
	return fmt.Sprintf("XOLU-LOC013: location %q has children, force not set", e.LocationID)
}

// SelfIntersectingPolygonError: XOLU-LOC020. HTTP 400.
type SelfIntersectingPolygonError struct{ FenceID string }

func (e *SelfIntersectingPolygonError) Error() string {
	return fmt.Sprintf("XOLU-LOC020: fence %q geometry is a self-intersecting polygon", e.FenceID)
}

// DuplicateLocationError: XOLU-LOC014 — location_id already defined.
// HTTP 409. Found by adversarial testing, not written to spec: no
// XOLU-LOC code in loc-01-rest-api.md's own table covers this case,
// and bal's own DefineAccount has the identical gap (a UNIQUE
// constraint violation surfaces as a raw driver error, mapped to 500
// by the default case) — checked directly, not assumed unique to
// this package. Fixed here, flagged as a likely systemic gap rather
// than silently fixed in isolation.
type DuplicateLocationError struct{ LocationID string }

func (e *DuplicateLocationError) Error() string {
	return fmt.Sprintf("XOLU-LOC014: location_id %q is already defined", e.LocationID)
}

// DuplicateFenceError: XOLU-LOC015 — fence_id already defined. HTTP 409.
type DuplicateFenceError struct{ FenceID string }

func (e *DuplicateFenceError) Error() string {
	return fmt.Sprintf("XOLU-LOC015: fence_id %q is already defined", e.FenceID)
}

// ValidationError is a generic 400 for malformed input that has no
// XOLU-LOC code reserved for it in loc-01-rest-api.md's own table
// (empty required fields, malformed GeoJSON, an unsupported geometry
// type) — a real gap in the table, not glossed over: these refusals
// exist and are correctly 400s, they just don't have a numbered code
// of their own yet.
type ValidationError struct{ Detail string }

func (e *ValidationError) Error() string { return "XOLU-LOC: " + e.Detail }

// PatternCapacityConflictError: XOLU-LOC022 — a def/attach set both
// inline capacity and a pattern reference, T-131. Mirrors
// obj-01-rest-api.md's XOLU-OBJ013 shape exactly. HTTP 400.
type PatternCapacityConflictError struct{}

func (e *PatternCapacityConflictError) Error() string {
	return "XOLU-LOC022: at most one of capacity or pattern may be set"
}

// DuplicatePatternError: XOLU-LOC023 — pattern_id already defined,
// T-131. Same systemic gap T-125 found and fixed for location_id/
// fence_id/account_id, closed here proactively rather than
// reproduced a third time. HTTP 409.
type DuplicatePatternError struct{ PatternID string }

func (e *DuplicatePatternError) Error() string {
	return fmt.Sprintf("XOLU-LOC023: pattern_id %q is already defined", e.PatternID)
}

// UnknownPatternError: XOLU-LOC024 — pattern_id does not resolve,
// T-131 (GET/patterns/{id}, or a def/attach referencing a
// nonexistent pattern). HTTP 404.
type UnknownPatternError struct{ PatternID string }

func (e *UnknownPatternError) Error() string {
	return fmt.Sprintf("XOLU-LOC024: unknown pattern_id %q", e.PatternID)
}
