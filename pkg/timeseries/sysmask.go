// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

import "fmt"

// SysmaskWidth is the immutable count of top bits of a 32-bit TimelineID
// that form the system/user region selector. See
// docs/proposals/system-bookkeeping-scope.md (@S).
//
// It is a *width*, not a mask value and not a prefix: the number of
// high bits reserved as the selector. An id is system-scope iff those
// selector bits are non-zero.
//
//   - width 0  → no bits reserved; the entire space is user-space (the
//     default: a store that never opts in behaves as if the feature is
//     off).
//   - width 32 → every bit is a selector; the only all-zero value is
//     id 0 (already reserved), so the store is effectively system-only.
//   - 0 < width < 32 → a split: low ids (zero selector) are user, high
//     ids (non-zero selector) are system.
//
// The width is frozen at store initialisation and never changes for the
// life of the store. Changing a store's effective width is done offline
// by transvasing into a new store (@S §6), never by mutation.
type SysmaskWidth uint8

// MaxSysmaskWidth is the widest legal sysmask (the full 32-bit id).
const MaxSysmaskWidth SysmaskWidth = 32

// Valid reports whether the width is in the legal range 0–32.
func (w SysmaskWidth) Valid() bool {
	return w <= MaxSysmaskWidth
}

// IsSystem reports whether id falls in the system region under this
// width. One shift and one compare, with both extremes handled:
//
//   - width 0:  no selector bits, so nothing is system. Special-cased
//     because `id >> 32` is not well-defined for a 32-bit value.
//   - width 32: every bit selects, so system iff id != 0.
func (w SysmaskWidth) IsSystem(id TimelineID) bool {
	if w == 0 {
		return false
	}
	if w >= MaxSysmaskWidth {
		return id != 0
	}
	return (uint32(id) >> (32 - uint32(w))) != 0
}

// IsUser is the complement of IsSystem.
func (w SysmaskWidth) IsUser(id TimelineID) bool {
	return !w.IsSystem(id)
}

// MaxUserID returns the highest id that is still user-space under this
// width — i.e. the id whose selector bits are all zero and whose
// remaining bits are all one. For width 0 this is MaxTimelineID (the
// whole space); for width 32 it is 0 (no user ids exist beyond the
// reserved id 0). Used by the user allocator to detect user-space
// exhaustion without crossing the partition.
func (w SysmaskWidth) MaxUserID() TimelineID {
	if w == 0 {
		return MaxTimelineID
	}
	if w >= MaxSysmaskWidth {
		return 0
	}
	// Low (32-w) bits all set, selector bits zero.
	return TimelineID((uint32(1) << (32 - uint32(w))) - 1)
}

// String renders the width for display, e.g. in `iolu db status`.
func (w SysmaskWidth) String() string {
	switch {
	case w == 0:
		return "0 (no system reservation; all user-space)"
	case w >= MaxSysmaskWidth:
		return "32 (system-only)"
	default:
		return fmt.Sprintf("%d (user ≤ 0x%08X, system above)", uint8(w), uint32(w.MaxUserID()))
	}
}
