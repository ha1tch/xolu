// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// errors.go — T-119 (wave 10): typed errors, matching loc's own
// convention (pkg/loc/errors.go) for errors.As-based HTTP status
// mapping rather than string-prefix parsing.

package obj

import "fmt"

// UnknownSubjectError: XOLU-OBJ001 — subject does not resolve to a
// valid (kind, key) shape. Format-only, per this package's own doc
// comment in model.go — not a live existence check against an entity
// row, consistent with every other primitive's meta-subject
// addressing in this codebase. HTTP 404.
type UnknownSubjectError struct{ Detail string }

func (e *UnknownSubjectError) Error() string { return fmt.Sprintf("XOLU-OBJ001: %s", e.Detail) }

// AlreadyAttachedError: XOLU-OBJ006 — subject already has obj
// capability composed. HTTP 409.
type AlreadyAttachedError struct{ SubjectRef string }

func (e *AlreadyAttachedError) Error() string {
	return fmt.Sprintf("XOLU-OBJ006: subject %q already has obj capability attached", e.SubjectRef)
}

// NotAttachedError: subject has no obj capability — the target of a
// GET/detach/move/report against a subject that was never attached,
// or was already detached. Mapped to XOLU-OBJ001 at the HTTP layer
// (obj-01-rest-api.md names no separate code for this specific case;
// "does not resolve" covers it — an obj capability that was never
// attached is exactly a subject the obj surface doesn't recognise).
type NotAttachedError struct{ SubjectRef string }

func (e *NotAttachedError) Error() string {
	return fmt.Sprintf("XOLU-OBJ001: subject %q has no obj capability attached", e.SubjectRef)
}

// DetachRefusedError: XOLU-OBJ007 — detach refused because the
// subject currently contains something or is positioned anywhere
// other than unassigned (obj-01-rest-api.md §1). HTTP 409.
type DetachRefusedError struct {
	SubjectRef string
	Reason     string // "positioned" | "occupied" -- which half of the OR fired
}

func (e *DetachRefusedError) Error() string {
	return fmt.Sprintf("XOLU-OBJ007: detach refused for %q: %s", e.SubjectRef, e.Reason)
}

// ValidationError is a generic 400 for malformed input with no
// XOLU-OBJ code of its own yet — mirrors loc's identical convention
// and identical reasoning (pkg/loc/errors.go's own doc comment).
type ValidationError struct{ Detail string }

func (e *ValidationError) Error() string { return "XOLU-OBJ: " + e.Detail }

// CapacityError: XOLU-OBJ003 — destination obj subject at capacity.
// T-120's own scope only ever sets Dimension "count" — max_weight_kg/
// max_volume_m3 enforcement is a deliberately deferred, separate item
// (see model.go's own Capacity doc comment for why: no field exists
// yet for what a contained subject itself contributes, and the unit
// question — kg vs lb, m3 vs L vs ft3 — is genuinely unresolved).
// HTTP 409.
type CapacityError struct {
	SubjectRef string
	Dimension  string
}

func (e *CapacityError) Error() string {
	return fmt.Sprintf("XOLU-OBJ003: subject %q is at capacity on dimension %q", e.SubjectRef, e.Dimension)
}

// ContainmentCycleError: XOLU-OBJ004 — the move would create a
// containment cycle. HTTP 409.
type ContainmentCycleError struct {
	SubjectRef   string
	ContainerRef string
}

func (e *ContainmentCycleError) Error() string {
	return fmt.Sprintf("XOLU-OBJ004: moving %q into %q would create a containment cycle", e.SubjectRef, e.ContainerRef)
}

// ContainerNotAttachedError: XOLU-OBJ005 — the target obj subject
// (the intended container) is not itself obj-attached. HTTP 409,
// matching obj-01-rest-api.md §2's own refusal list exactly (not 404
// — the subject may well exist as an entity, it simply hasn't been
// given obj capability, a distinct fact from "does not resolve").
type ContainerNotAttachedError struct{ ContainerRef string }

func (e *ContainerNotAttachedError) Error() string {
	return fmt.Sprintf("XOLU-OBJ005: container %q is not obj-attached", e.ContainerRef)
}

// DemoteRefusedError: XOLU-OBJ011 — demote refused because the
// subject currently contains something of its own (obj-01-rest-
// api.md §5: "dissolve contents first"). HTTP 409.
type DemoteRefusedError struct{ SubjectRef string }

func (e *DemoteRefusedError) Error() string {
	return fmt.Sprintf("XOLU-OBJ011: demote refused for %q: still contains something", e.SubjectRef)
}

// CapacityInvalidError: XOLU-OBJ008 — a capacity update leaves every
// dimension unconstrained (obj-01-rest-api.md §4: "at least one
// dimension must be set, or the subject cannot hold anything" —
// matching /loc's own XOLU-LOC011 refusal shape). HTTP 400.
type CapacityInvalidError struct{ SubjectRef string }

func (e *CapacityInvalidError) Error() string {
	return fmt.Sprintf("XOLU-OBJ008: capacity update for %q leaves every dimension unset", e.SubjectRef)
}

// RetireRefusedError: XOLU-OBJ012 — retire refused because the
// subject currently contains something (obj-01-rest-api.md §6).
// HTTP 409.
type RetireRefusedError struct{ SubjectRef string }

func (e *RetireRefusedError) Error() string {
	return fmt.Sprintf("XOLU-OBJ012: retire refused for %q: still contains something", e.SubjectRef)
}

// AlreadyRetiredError: retiring a subject that is already retired.
// Not itself a named XOLU-OBJ code in obj-01-rest-api.md's own §6 —
// retire's own spec covers only the "still contains something"
// refusal explicitly — mapped to the same XOLU-OBJ012 bucket at the
// HTTP layer since both are "retire cannot proceed" refusals of the
// same shape, not a distinct wire-visible case worth its own code.
type AlreadyRetiredError struct{ SubjectRef string }

func (e *AlreadyRetiredError) Error() string {
	return fmt.Sprintf("XOLU-OBJ012: %q is already retired -- irreversible, no un-retire", e.SubjectRef)
}
