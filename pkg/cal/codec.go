// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"encoding/binary"
	"fmt"
	"math/bits"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// This file is the pure bit layer: total, deterministic, allocation-light
// functions with no storage and no state. The conversion invariant (see the
// package doc) is the test oracle — every function here is verifiable against a
// brute-force interval-overlap reference. Nothing here depends on any open
// design decision.

// --- Day and quantum flooring (codec §3.3) ---
//
// Flooring is integer division, NOT a bitmask. NsPerDay and NsPerQuantum are not
// powers of two, so `& mask` would floor to the nearest 2^k ns (~19.5h for a
// "day"), which is not a day boundary. div/mul is two integer ops, as cheap as a
// mask and actually correct.

// dayFloorNanos returns the UnixNano of 00:00:00 UTC of the day containing the
// instant. This is the day_unixnano used in the occupancy key.
func dayFloorNanos(t ot.Instant) int64 {
	n := t.UnixNano()
	return (n / NsPerDay) * NsPerDay
}


// --- Key codec (codec §3.2) ---
//
// [kind:1=0x01][cal_ordinal:4][plane:1][day_unixnano:8] = 14 bytes, big-endian.

// EncodeKey builds the fixed-width occupancy key for a (calendar, plane, day).
// dayNanos must already be day-floored (use DayKey for the common path, which
// floors for you).
func EncodeKey(ord CalOrdinal, plane Plane, dayNanos int64) []byte {
	key := make([]byte, KeySize)
	key[0] = keyKindOccupancy
	binary.BigEndian.PutUint32(key[1:5], uint32(ord))
	key[5] = byte(plane)
	binary.BigEndian.PutUint64(key[6:14], uint64(dayNanos))
	return key
}

// DayKey is the convenience encoder for an arbitrary instant: it floors to the
// instant's UTC day and encodes. This is the function write/read paths use.
func DayKey(ord CalOrdinal, plane Plane, t ot.Instant) []byte {
	return EncodeKey(ord, plane, dayFloorNanos(t))
}

// DecodeKey reverses EncodeKey. It returns the calendar ordinal, plane, and the
// day_unixnano (day-floored UnixNano, UTC).
func DecodeKey(key []byte) (ord CalOrdinal, plane Plane, dayNanos int64, err error) {
	if len(key) != KeySize {
		return 0, 0, 0, fmt.Errorf("cal: DecodeKey: key len %d, expected %d", len(key), KeySize)
	}
	if key[0] != keyKindOccupancy {
		return 0, 0, 0, fmt.Errorf("cal: DecodeKey: kind %#x, expected %#x", key[0], keyKindOccupancy)
	}
	ord = CalOrdinal(binary.BigEndian.Uint32(key[1:5]))
	plane = Plane(key[5])
	dayNanos = int64(binary.BigEndian.Uint64(key[6:14]))
	return ord, plane, dayNanos, nil
}

// --- Bitmap primitives ---

// word/bit position of a quantum within a DayBitmap.
func wordBit(q int) (word int, bit uint) {
	return q / 64, uint(q % 64)
}

// Set marks quantum q (0..287) occupied. Out-of-range q is a no-op guard.
func (b *DayBitmap) Set(q int) {
	if q < 0 || q >= QuantaPerDay {
		return
	}
	w, bit := wordBit(q)
	b[w] |= 1 << bit
}

// Clear marks quantum q free.
func (b *DayBitmap) Clear(q int) {
	if q < 0 || q >= QuantaPerDay {
		return
	}
	w, bit := wordBit(q)
	b[w] &^= 1 << bit
}

// Test reports whether quantum q is occupied.
func (b DayBitmap) Test(q int) bool {
	if q < 0 || q >= QuantaPerDay {
		return false
	}
	w, bit := wordBit(q)
	return b[w]&(1<<bit) != 0
}

// SetRange marks quanta [lo, hi) occupied (half-open), clamped to [0, 288).
func (b *DayBitmap) SetRange(lo, hi int) {
	if lo < 0 {
		lo = 0
	}
	if hi > QuantaPerDay {
		hi = QuantaPerDay
	}
	for q := lo; q < hi; q++ {
		w, bit := wordBit(q)
		b[w] |= 1 << bit
	}
}

// PopCount returns the number of occupied quanta in the day.
func (b DayBitmap) PopCount() int {
	n := 0
	for _, w := range b {
		n += bits.OnesCount64(w)
	}
	return n
}

// IsZero reports whether the day has no occupancy (the sparse-store "no value"
// equivalent — an all-zero day need not be stored at all).
func (b DayBitmap) IsZero() bool {
	for _, w := range b {
		if w != 0 {
			return false
		}
	}
	return true
}

// And returns the bitwise intersection of two days (occupied in BOTH). This is
// the per-day kernel of the N-way match: a quantum free in the result of ANDing
// "busy" maps is... not what we want — match operates on FREE maps. See
// AndFree below, which is the matching primitive. And is the raw busy-AND,
// exposed for completeness and testing.
func (b DayBitmap) And(o DayBitmap) DayBitmap {
	var r DayBitmap
	for i := range r {
		r[i] = b[i] & o[i]
	}
	return r
}

// Or returns the bitwise union (occupied in EITHER) — e.g. combining binding and
// proposed planes when a caller asks for both (the GATE-1 binding+proposed case;
// the bit layer provides the operation, the policy decision lives above).
func (b DayBitmap) Or(o DayBitmap) DayBitmap {
	var r DayBitmap
	for i := range r {
		r[i] = b[i] | o[i]
	}
	return r
}

// freeMask returns a day whose set bits are the FREE quanta of b: the complement
// of occupancy over the 288 valid quanta, with the 32 slack bits forced zero so
// they never read as "free".
func (b DayBitmap) freeMask() DayBitmap {
	var r DayBitmap
	for i := range r {
		r[i] = ^b[i]
	}
	// Force the 32 unused high bits of the last word to zero: only quanta
	// 288..319 live there, and they are never free (they do not exist).
	// Valid bits in word 4 are 288-256=... wait: words 0..3 cover 0..255,
	// word 4 covers 256..319; valid quanta in word 4 are 256..287 = low 32 bits.
	const validBitsInLastWord = QuantaPerDay - 64*(WordsPerDay-1) // 288-256 = 32
	r[WordsPerDay-1] &= (1 << validBitsInLastWord) - 1
	return r
}

// AndFree returns the day's worth of commonly-FREE quanta across all the given
// busy-bitmaps: a bit is set in the result iff that quantum is free in EVERY
// input. This is the N-way match kernel (F16): "when are all these calendars
// simultaneously free?" Implemented as the AND of each input's free-mask.
//
// With no inputs the result is the all-free day (every valid quantum set),
// which is the correct identity for an empty intersection.
func AndFree(days ...DayBitmap) DayBitmap {
	var r DayBitmap
	// identity: all valid quanta free.
	for i := range r {
		r[i] = ^uint64(0)
	}
	const validBitsInLastWord = QuantaPerDay - 64*(WordsPerDay-1)
	r[WordsPerDay-1] &= (1 << validBitsInLastWord) - 1

	for _, d := range days {
		fm := d.freeMask()
		for i := range r {
			r[i] &= fm[i]
		}
	}
	return r
}

// --- Span → occupancy, including the midnight crossing (codec §3.3) ---
//
// A span may cross one or more UTC-midnight boundaries. The occupancy it
// produces is keyed per day, so a span sets bits across one or more adjacent
// day-values. SpanDays returns, in chronological order, each (dayNanos, bitmap)
// the span occupies. The caller (the store layer, a later stage) ORs each
// returned bitmap into that day's stored value.

// DayOccupancy is one day's contribution from a span: the day key's floored
// UnixNano and the bits that day should have set.
type DayOccupancy struct {
	DayNanos int64
	Bits     DayBitmap
}

// SpanDays expands a half-open [Start, End) span into per-day occupancy. Returns
// an error for an invalid span. A span ending exactly on a midnight contributes
// nothing to that midnight's day (half-open: the end quantum is exclusive).
func SpanDays(s Span) ([]DayOccupancy, error) {
	if !s.Valid() {
		return nil, fmt.Errorf("cal: SpanDays: invalid span (start not before end)")
	}
	startN := s.Start.UnixNano()
	endN := s.End.UnixNano() // exclusive

	var out []DayOccupancy
	dayStart := (startN / NsPerDay) * NsPerDay
	for dayStart < endN {
		dayEnd := dayStart + NsPerDay // exclusive end of this day

		// Overlap of [startN, endN) with [dayStart, dayEnd) in ns.
		loN := startN
		if dayStart > loN {
			loN = dayStart
		}
		hiN := endN
		if dayEnd < hiN {
			hiN = dayEnd
		}

		// Convert the ns overlap to quantum indices within this day.
		loQ := int((loN - dayStart) / NsPerQuantum)
		// hiN is exclusive; the last occupied quantum is the one containing
		// hiN-1ns. Use ceil-style: a span ending exactly on a quantum boundary
		// does not occupy the quantum starting there.
		hiQ := int((hiN - dayStart + NsPerQuantum - 1) / NsPerQuantum)
		if hiQ > QuantaPerDay {
			hiQ = QuantaPerDay
		}

		var bm DayBitmap
		bm.SetRange(loQ, hiQ)
		if !bm.IsZero() {
			out = append(out, DayOccupancy{DayNanos: dayStart, Bits: bm})
		}
		dayStart = dayEnd
	}
	return out, nil
}

// --- Daypart rollup (codec §4, §4.1, §4.3) ---
//
// One v1 level: a 3-hour daypart. 8 dayparts/day. The spec stores, per coarse
// unit, the bitmasked analogues of the ts rollup aggregates (§4.3):
//
//   - OR        — is ANY quantum in this daypart occupied?  (prunes `busy`,
//                 gates fine scans for availability)
//   - any-clear — is ANY quantum in this daypart free?      (prunes `match`)
//
// Both are up-conversions (lossy) and so can only PRUNE, never CONFIRM. The two
// aggregates prune different queries; conflating them is a bug (an OR bit being
// set does NOT mean the daypart has no free quantum). This package keeps them
// distinct.

// DaypartRollup is one day's rollup: two 8-bit masks, one bit per 3-hour
// daypart. Or bit d set => some quantum in daypart d is occupied. AnyClear bit d
// set => some quantum in daypart d is free.
type DaypartRollup struct {
	Or       uint8 // daypart has ≥1 occupied quantum
	AnyClear uint8 // daypart has ≥1 free quantum
}

// RollupDay computes both daypart aggregates of a fine day bitmap.
func RollupDay(b DayBitmap) DaypartRollup {
	var r DaypartRollup
	for d := 0; d < DaypartsPerDay; d++ {
		lo := d * QuantaPerDaypart
		hi := lo + QuantaPerDaypart
		anyOcc, anyFree := false, false
		for q := lo; q < hi; q++ {
			if b.Test(q) {
				anyOcc = true
			} else {
				anyFree = true
			}
			if anyOcc && anyFree {
				break
			}
		}
		if anyOcc {
			r.Or |= 1 << uint(d)
		}
		if anyFree {
			r.AnyClear |= 1 << uint(d)
		}
	}
	return r
}

// MatchCandidateDayparts returns the dayparts that survive match-pruning across
// the given calendars' rollups, as a bitmask (bit d set => daypart d must be
// confirmed at the fine grain).
//
// The ONLY sound prune for "is there a commonly-free fine quantum in this
// daypart" is: if ANY single calendar has the daypart entirely busy (no free
// quantum — its AnyClear bit is clear), then no common free slot can exist there,
// so the daypart is pruned. A daypart survives iff every calendar has at least
// one free quantum in it. Survival is necessary but NOT sufficient (the free
// quanta may not align) — hence prune-not-confirm: surviving dayparts MUST be
// verified against the fine layer.
func MatchCandidateDayparts(rollups ...DaypartRollup) uint8 {
	cand := uint8(0xFF) // all 8 dayparts candidate initially
	for _, r := range rollups {
		// eliminate any daypart this calendar has fully busy (AnyClear bit clear)
		cand &= r.AnyClear
	}
	return cand
}

// BusyDayparts returns the dayparts that are occupied in ANY of the given
// rollups (the OR aggregate unioned) — prunes availability?q=busy: a daypart with
// no occupancy in any calendar is definitely free at fine grain too.
func BusyDayparts(rollups ...DaypartRollup) uint8 {
	var any uint8
	for _, r := range rollups {
		any |= r.Or
	}
	return any
}

// CandidateDaypartIndices returns the indices (0..7) of dayparts surviving
// match-pruning. Convenience over MatchCandidateDayparts.
func CandidateDaypartIndices(rollups ...DaypartRollup) []int {
	cand := MatchCandidateDayparts(rollups...)
	var out []int
	for d := 0; d < DaypartsPerDay; d++ {
		if cand&(1<<uint(d)) != 0 {
			out = append(out, d)
		}
	}
	return out
}
