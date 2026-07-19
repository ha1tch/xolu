// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package cal is the xolu scheduling primitive: a temporal-occupancy index over
// bookings against calendars.
//
// # The two-layer model (H1/H3)
//
// A calendar's authoritative state is a relational booking record in SQLite (H1)
// — the source of truth. The structures in this package are a *derived,
// rebuildable bitmap index* over those bookings (H3): a per-(calendar, plane,
// day) occupancy raster that makes "is this span free?" and the N-way "when are
// all these free?" answerable by bitwise operations. The index is never
// authoritative; it can always be discarded and rebuilt from the SQLite records,
// and the invariant `index == rebuild` is the acceptance gate for every stateful
// operation built on top of this layer.
//
// # The conversion invariant (the load-bearing fact)
//
// Occupancy is stored at a single fine grain (5-minute quanta). Coarser views
// (the rollup pyramid) are *up-conversions* of the fine layer and are therefore
// lossy: a coarse bucket summarises many fine quanta. This asymmetry is the
// reason the rollup can only ever PRUNE a match (prove one impossible), never
// CONFIRM one (prove one exists) — confirmation always drops to the fine grain.
// Reconciliation between grains always moves toward the finer grain; the
// toward-coarser direction loses information.
//
// # This file (Stage 0)
//
// Settled design inputs pinned as constants, plus the core value types. No
// behaviour beyond construction lives here; the pure bit layer is in codec.go.
// The package deliberately knows nothing of time zones, recurrence, or DST — all
// timestamps are absolute UTC instants (see pkg/xolutime); calendar intentions
// live above this primitive.
package cal

import (
	"fmt"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// --- Settled quantum grid (cal-pebble-codec.md §3.3, §6 item 1) ---

const (
	// QBaseSeconds is the base quantum: 5 minutes, the evidenced finest grain.
	// Settled for v1; re-basing to a finer grain is an open question (codec §6.1)
	// but never coarser.
	QBaseSeconds = 300

	// QuantaPerDay is the number of 5-minute quanta in a UTC day: 86400/300 = 288.
	QuantaPerDay = 288

	// WordsPerDay is the uint64 count holding QuantaPerDay bits: ceil(288/64) = 5.
	// Bits 288..319 (the slack in the 5th word) are unused and always zero.
	WordsPerDay = 5

	// NsPerQuantum and NsPerDay are the nanosecond spans. NOTE: neither is a power
	// of two, so flooring to a day or quantum boundary is integer division
	// (div/mul), never a bitmask — a mask would floor to the nearest 2^k ns
	// (~19.5h for a "day"), which is wrong. See codec.go dayFloorNanos.
	NsPerQuantum int64 = QBaseSeconds * 1_000_000_000 // 300_000_000_000
	NsPerDay     int64 = 86_400 * 1_000_000_000       // 86_400_000_000_000
)

// --- Settled rollup pyramid (codec §4.1, §6 item 3) ---

const (
	// DaypartHours is the v1 rollup granularity: one 3-hour daypart.
	DaypartHours = 3

	// QuantaPerDaypart is 3h of 5-minute quanta: (3*3600)/300 = 36.
	QuantaPerDaypart = (DaypartHours * 3600) / QBaseSeconds // 36

	// DaypartsPerDay is the number of dayparts in a day: 288/36 = 8.
	// One rollup byte (8 bits) summarises a whole day at daypart granularity.
	DaypartsPerDay = QuantaPerDay / QuantaPerDaypart // 8
)

// --- Planes (codec §3.3 key layout) ---

// Plane selects the binding (confirmed) or proposed (tentative) occupancy plane.
// The two planes are stored under distinct keys; their semantics in matching are
// governed by GATE-1 (codec §6.5) and are not decided in the bit layer.
type Plane uint8

const (
	PlaneBinding  Plane = 0x00 // confirmed bookings
	PlaneProposed Plane = 0x01 // tentative (proposed-but-not-binding) bookings
)

func (p Plane) String() string {
	switch p {
	case PlaneBinding:
		return "binding"
	case PlaneProposed:
		return "proposed"
	default:
		return fmt.Sprintf("Plane(%#x)", uint8(p))
	}
}

// Valid reports whether p is a defined plane.
func (p Plane) Valid() bool { return p == PlaneBinding || p == PlaneProposed }

// --- Key layout (codec §3.2) ---
//
// [kind:1=0x01][cal_ordinal:4][plane:1][day_unixnano:8]  = 14 bytes, fixed.
// Big-endian throughout so lexicographic order == chronological scan order,
// matching the ts codec property.

const (
	keyKindOccupancy byte = 0x01 // occupancy bitmap day (matches codec 0x01 keyspace)

	// KeySize is the fixed occupancy-key width: 1+4+1+8.
	KeySize = 1 + 4 + 1 + 8 // 14
)

// CalOrdinal is the per-tenant dense calendar identifier (codec §3.2), assigned
// at cal/def time and recorded in the SQLite calendar record. uint32 (the one
// width the codec widens over ts's uint16 timeline id).
type CalOrdinal uint32

// --- Entity handle sentinels (codec §3.2) ---
//
// A booking's bearer/target entity is a uint64 handle into the entity graph.
// Real handles allocate upward from EntityNil; sentinels reserve the extremes.
// Validation is one range check: EntityNil < h <= EntityMaxValid.

const (
	EntityNil       uint64 = 0x0000000000000000 // floor: no binding by design
	EntityMaxValid  uint64 = 0xFFFFFFFFFFFFFF00 // allocator ceiling; top 256 reserved
	EntityTombstone uint64 = 0xFFFFFFFFFFFFFFFF // was bound, target entity deleted
	// Reserved sentinel pool is (EntityMaxValid, MaxUint64], allocated DESCENDING
	// from EntityTombstone. Boundary fixed from day one so a future sentinel can
	// never collide with an already-issued handle (carving it later is a migration).
)

// ValidEntity reports whether h is an assignable real handle (not a sentinel).
func ValidEntity(h uint64) bool {
	return h > EntityNil && h <= EntityMaxValid
}

// --- Span: an absolute UTC half-open interval [Start, End) ---

// Span is a concrete booking interval. Both endpoints are absolute UTC instants
// (xolutime.Instant); the span carries no zone or recurrence. It is half-open:
// [Start, End), so adjacent spans (one ending exactly where the next starts) do
// not overlap — matching the bitmap's quantum semantics.
type Span struct {
	Start ot.Instant
	End   ot.Instant
}

// Valid reports whether the span is well-formed: Start strictly before End.
// A zero-length or inverted span is invalid (a booking must occupy time).
func (s Span) Valid() bool {
	return s.Start.Before(s.End)
}

// DayBitmap is one day's occupancy raster on one plane: 288 bits in 5 uint64
// words. Bit q (0..287) set means quantum q of that day is occupied;
// q = seconds_into_day / 300. Bits 288..319 are unused and must stay zero.
type DayBitmap [WordsPerDay]uint64
