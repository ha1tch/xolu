// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// model.go — T-119 (wave 10), Stage 0: pkg/obj's core types.
//
// Package obj implements /obj, the manipulable-object primitive
// (obj-00-design.md): every tracked thing is an entity /obj attaches
// position and containment capability to, never an independent
// identity of its own (§4). Subjects are addressed via
// pkg/storage/meta_subject.go's (kind, key) convention, the same
// reuse loc's own standalone fences settled on (T-127) — format-only
// validation, consistent with every other primitive's meta-subject
// addressing in this codebase (none does a live existence check
// against an entity row; @C04c's engine-inert law extends to the
// convention even where, as here, the primitive built on top of it is
// itself guard-bearing).
package obj

import "time"

// PositionKind names which of §6's three termination cases a
// subject's position currently resolves to at the *first* hop — not
// the final resolved answer, which may require walking further
// through other obj subjects (kind == PositionKindObj).
type PositionKind string

const (
	// PositionKindLocLeaf: anchored at a /loc tree leaf. Stage 1.
	PositionKindLocLeaf PositionKind = "loc_leaf"
	// PositionKindObj: contained by another /obj subject — this IS
	// containment (obj-00-design.md §5), not a separate fact. Stage 2.
	PositionKindObj PositionKind = "obj"
	// PositionKindUnassigned: explicitly off-site/unknown, a
	// first-class ordinary state (obj-00-design.md §12), not an error.
	// Stored as an empty PositionKind, never a Go nil special-case —
	// the zero value already means this.
	PositionKindUnassigned PositionKind = ""
)

// Capacity is a subject's own optional multi-dimensional ceiling
// (obj-00-design.md §7) — weight, volume, and count are each
// independently optional; a subject with none set can be positioned
// and can itself be contained, but cannot hold anything (obj-01-rest-
// api.md §1). Current* fields are the guard-bearing running totals,
// mirroring loc_capacity's own count column, generalised to three
// independent dimensions instead of one.
type Capacity struct {
	MaxWeightKg *float64
	MaxVolumeM3 *float64
	MaxCount    *int64

	CurWeightKg float64
	CurVolumeM3 float64
	CurCount    int64
}

// Subject is one /obj-tracked entity's full row: identity, capacity,
// and current position — the same "everything about one thing in one
// struct" shape Location/Fence use in pkg/loc.
type Subject struct {
	Ref       string // canonical "kind:key" form, meta_subject.go's own convention
	Capacity  Capacity
	Position  Position
	RetiredAt *time.Time // T-122: obj-00-design.md §12's terminal state — nil unless retired, once set never cleared
	CreatedAt time.Time
}

// Position is a subject's own recorded position — obj's own canonical
// record for all three of §6's termination kinds, not a derived read
// through /loc's tables. This is deliberate, not incidental: /loc has
// no representation at all for "unassigned" (obj-01-rest-api.md §0's
// own named gap this design closes) or for "contained by another
// entity," so obj cannot lean on /loc's own loc_assignment as its
// single source of truth even for the loc_leaf case — a subject
// moved to a loc_leaf is authoritative in *both* places: /loc's own
// guard-bearing leaf-capacity CAS decides whether the move is
// admitted at all, and obj's own Position row is /obj's independent
// record of which of its three termination kinds currently applies.
// See store.go's own MoveToLocLeaf doc comment for the real
// consequence of this split: two separate SQLite files, two separate
// commits, named plainly rather than assumed atomic.
type Position struct {
	Kind        PositionKind
	LocLeafID   string // set iff Kind == PositionKindLocLeaf
	ContainedBy string // set iff Kind == PositionKindObj (Stage 2) — the containing subject's own Ref
	UpdatedAt   time.Time
}
