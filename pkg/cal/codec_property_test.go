// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"math/rand"
	"testing"
	"time"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// The bit layer is an optimisation of "do these intervals overlap". The slow,
// obviously-correct interval version is the oracle; the bitmaps must agree with
// it across a large random corpus. These tests are the acceptance gate for
// Stage 1.

// --- Oracle: occupancy as a set of occupied quantum indices, computed without
// any bitmap, directly from interval arithmetic. ---

// oracleQuanta returns the set of (dayNanos, quantum) pairs a span occupies,
// computed by walking quanta and testing half-open interval membership.
func oracleQuanta(s Span) map[[2]int64]bool {
	out := map[[2]int64]bool{}
	startN := s.Start.UnixNano()
	endN := s.End.UnixNano()
	// Walk every quantum boundary the span could touch.
	firstDay := (startN / NsPerDay) * NsPerDay
	for dayStart := firstDay; dayStart < endN; dayStart += NsPerDay {
		for q := 0; q < QuantaPerDay; q++ {
			qStart := dayStart + int64(q)*NsPerQuantum
			qEnd := qStart + NsPerQuantum
			// Quantum is occupied iff it overlaps [startN, endN) (half-open):
			// qStart < endN && qEnd > startN.
			if qStart < endN && qEnd > startN {
				out[[2]int64{dayStart, int64(q)}] = true
			}
		}
	}
	return out
}

func randInstant(rng *rand.Rand) ot.Instant {
	// Random instant within ~10 years after epoch, at second resolution so the
	// oracle's quantum walk stays cheap but boundaries are still exercised.
	maxSec := int64(10 * 365 * 24 * 3600)
	return ot.FromTime(time.Unix(rng.Int63n(maxSec), 0).UTC())
}

func randSpan(rng *rand.Rand) Span {
	start := randInstant(rng)
	// duration 1s .. ~3 days, to exercise single-day and multi-midnight spans.
	durSec := rng.Int63n(3*24*3600) + 1
	end := start.Add(time.Duration(durSec) * time.Second)
	return Span{Start: start, End: end}
}

// TestSpanDaysMatchesOracle: the bits SpanDays produces equal the oracle's
// occupied-quantum set, across random spans including midnight crossings.
func TestSpanDaysMatchesOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 5000; iter++ {
		s := randSpan(rng)
		want := oracleQuanta(s)

		days, err := SpanDays(s)
		if err != nil {
			t.Fatalf("SpanDays(%v): %v", s, err)
		}
		got := map[[2]int64]bool{}
		for _, d := range days {
			for q := 0; q < QuantaPerDay; q++ {
				if d.Bits.Test(q) {
					got[[2]int64{d.DayNanos, int64(q)}] = true
				}
			}
		}

		if len(got) != len(want) {
			t.Fatalf("iter %d span=%v: got %d quanta, want %d", iter, s, len(got), len(want))
		}
		for k := range want {
			if !got[k] {
				t.Fatalf("iter %d span=%v: missing quantum %v", iter, s, k)
			}
		}
	}
}

// TestAndFreeMatchesOracle: AndFree over N calendars' busy-days equals the set
// of quanta free in ALL of them, computed directly.
func TestAndFreeMatchesOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for iter := 0; iter < 3000; iter++ {
		n := rng.Intn(4) + 1 // 1..4 calendars
		var maps []DayBitmap
		// oracle: a quantum is free-in-all iff occupied in none.
		occupiedInAny := [QuantaPerDay]bool{}
		for c := 0; c < n; c++ {
			var bm DayBitmap
			// random occupancy: a handful of random sub-ranges within one day.
			for r := 0; r < rng.Intn(5); r++ {
				lo := rng.Intn(QuantaPerDay)
				hi := lo + rng.Intn(QuantaPerDay-lo) + 1
				bm.SetRange(lo, hi)
				for q := lo; q < hi; q++ {
					occupiedInAny[q] = true
				}
			}
			maps = append(maps, bm)
		}
		free := AndFree(maps...)
		for q := 0; q < QuantaPerDay; q++ {
			wantFree := !occupiedInAny[q]
			if free.Test(q) != wantFree {
				t.Fatalf("iter %d q=%d: AndFree free=%v want %v", iter, q, free.Test(q), wantFree)
			}
		}
		// slack bits must never be free
		for q := QuantaPerDay; q < WordsPerDay*64; q++ {
			w, bit := q/64, uint(q%64)
			if free[w]&(1<<bit) != 0 {
				t.Fatalf("iter %d: slack bit %d set in AndFree result", iter, q)
			}
		}
	}
}

// TestRollupPruneIsSound: the match rollup may only prune. For any fine quantum
// that is free-in-all calendars, its daypart MUST survive pruning — otherwise the
// rollup eliminated a real match (a false prune, forbidden). This is the core
// soundness guarantee of the prune-not-confirm rule.
func TestRollupPruneIsSound(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for iter := 0; iter < 5000; iter++ {
		n := rng.Intn(3) + 2 // 2..4 calendars
		var fine []DayBitmap
		var rolls []DaypartRollup
		for c := 0; c < n; c++ {
			var bm DayBitmap
			for r := 0; r < rng.Intn(6); r++ {
				lo := rng.Intn(QuantaPerDay)
				hi := lo + rng.Intn(QuantaPerDay-lo) + 1
				bm.SetRange(lo, hi)
			}
			fine = append(fine, bm)
			rolls = append(rolls, RollupDay(bm))
		}

		fineFree := AndFree(fine...)
		cand := MatchCandidateDayparts(rolls...)

		for q := 0; q < QuantaPerDay; q++ {
			if fineFree.Test(q) {
				d := q / QuantaPerDaypart
				if cand&(1<<uint(d)) == 0 {
					t.Fatalf("iter %d: fine-free quantum %d in daypart %d was pruned (false prune)",
						iter, q, d)
				}
			}
		}
	}
}

// TestRollupCannotConfirm: a surviving daypart does NOT imply a fine match.
// Construct a daypart where every calendar has free quanta (so it survives the
// any-clear prune) but the free quanta never align (so there is no commonly-free
// fine quantum). This proves the rollup genuinely cannot confirm — survival is
// necessary, not sufficient — which is why the fine layer must verify.
func TestRollupCannotConfirm(t *testing.T) {
	// Daypart 0 = quanta 0..35. Calendar A is busy on the odd quanta, B on the
	// even quanta. Each has free quanta in daypart 0 (so AnyClear bit 0 is set in
	// both -> daypart survives), but no quantum is free in BOTH.
	var a, b DayBitmap
	for q := 0; q < QuantaPerDaypart; q++ {
		if q%2 == 0 {
			a.Set(q) // A busy on evens -> A free on odds
		} else {
			b.Set(q) // B busy on odds -> B free on evens
		}
	}
	ra, rb := RollupDay(a), RollupDay(b)

	cand := MatchCandidateDayparts(ra, rb)
	if cand&(1<<0) == 0 {
		t.Fatal("daypart 0 should survive pruning (both have free quanta in it)")
	}

	// But the fine layer finds NO commonly-free quantum in daypart 0.
	fineFree := AndFree(a, b)
	for q := 0; q < QuantaPerDaypart; q++ {
		if fineFree.Test(q) {
			t.Fatalf("expected no common free quantum in daypart 0, but quantum %d is free in both", q)
		}
	}
	// This is the confirm-gap: rollup said "candidate", fine layer said "no".
	// The rollup correctly did not (and cannot) confirm.
}

// TestRollupAggregatesDistinct: OR and AnyClear are different aggregates and must
// not be conflated. A partially-busy daypart has BOTH set.
func TestRollupAggregatesDistinct(t *testing.T) {
	var b DayBitmap
	b.SetRange(0, 10) // daypart 0 partially busy (10 of 36 quanta)
	r := RollupDay(b)
	if r.Or&1 == 0 {
		t.Fatal("daypart 0 has occupancy; Or bit must be set")
	}
	if r.AnyClear&1 == 0 {
		t.Fatal("daypart 0 has free quanta; AnyClear bit must be set")
	}
	// daypart 1 (quanta 36..71) fully free: Or clear, AnyClear set.
	if r.Or&(1<<1) != 0 {
		t.Fatal("daypart 1 is free; Or bit must be clear")
	}
	if r.AnyClear&(1<<1) == 0 {
		t.Fatal("daypart 1 is free; AnyClear bit must be set")
	}
	// fully-busy daypart: Or set, AnyClear clear.
	var full DayBitmap
	full.SetRange(0, QuantaPerDaypart)
	rf := RollupDay(full)
	if rf.Or&1 == 0 || rf.AnyClear&1 != 0 {
		t.Fatalf("fully-busy daypart: Or=%08b AnyClear=%08b (want Or bit set, AnyClear clear)", rf.Or, rf.AnyClear)
	}
}

// TestKeyRoundTrip: EncodeKey/DecodeKey and DayKey flooring.
func TestKeyRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for i := 0; i < 2000; i++ {
		ord := CalOrdinal(rng.Uint32())
		plane := PlaneBinding
		if rng.Intn(2) == 1 {
			plane = PlaneProposed
		}
		inst := randInstant(rng)
		key := DayKey(ord, plane, inst)
		if len(key) != KeySize {
			t.Fatalf("key size %d != %d", len(key), KeySize)
		}
		gotOrd, gotPlane, gotDay, err := DecodeKey(key)
		if err != nil {
			t.Fatalf("DecodeKey: %v", err)
		}
		if gotOrd != ord || gotPlane != plane {
			t.Fatalf("roundtrip ord/plane: got %d/%v want %d/%v", gotOrd, gotPlane, ord, plane)
		}
		// decoded day must be the UTC-midnight floor of inst.
		wantDay := (inst.UnixNano() / NsPerDay) * NsPerDay
		if gotDay != wantDay {
			t.Fatalf("day floor: got %d want %d", gotDay, wantDay)
		}
		if gotDay%NsPerDay != 0 {
			t.Fatalf("decoded day %d is not midnight-aligned", gotDay)
		}
	}
}

// TestKeyLexicographicOrder: big-endian day encoding means later days sort after
// earlier days for the same (ord, plane) — the scan-order property ts relies on.
func TestKeyLexicographicOrder(t *testing.T) {
	ord := CalOrdinal(42)
	day1 := ot.MustParse("2026-07-08T12:00:00Z")
	day2 := ot.MustParse("2026-07-09T01:00:00Z")
	k1 := DayKey(ord, PlaneBinding, day1)
	k2 := DayKey(ord, PlaneBinding, day2)
	if !(string(k1) < string(k2)) {
		t.Fatalf("expected earlier day key to sort first: %x vs %x", k1, k2)
	}
}

// TestPopCountAndCapacity: occupancy count matches set bits; capacity sanity.
func TestPopCount(t *testing.T) {
	var b DayBitmap
	if b.PopCount() != 0 {
		t.Fatal("empty popcount != 0")
	}
	b.SetRange(0, 36) // one full daypart
	if b.PopCount() != 36 {
		t.Fatalf("popcount = %d, want 36", b.PopCount())
	}
	if !b.Test(0) || !b.Test(35) || b.Test(36) {
		t.Fatal("SetRange boundary wrong (half-open)")
	}
}

// TestSpanEndOnQuantumBoundaryIsExclusive: a span ending exactly on a quantum
// boundary does not occupy the quantum that starts there (half-open).
func TestSpanEndOnQuantumBoundaryIsExclusive(t *testing.T) {
	// 00:00:00 .. 00:05:00 occupies exactly quantum 0, not quantum 1.
	s := Span{
		Start: ot.MustParse("2026-07-08T00:00:00Z"),
		End:   ot.MustParse("2026-07-08T00:05:00Z"),
	}
	days, err := SpanDays(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 {
		t.Fatalf("expected 1 day, got %d", len(days))
	}
	if !days[0].Bits.Test(0) || days[0].Bits.Test(1) {
		t.Fatal("expected only quantum 0 occupied")
	}
	if days[0].Bits.PopCount() != 1 {
		t.Fatalf("expected popcount 1, got %d", days[0].Bits.PopCount())
	}
}

// TestMidnightCrossing: a span straddling UTC midnight occupies the tail of day
// N and the head of day N+1.
func TestMidnightCrossing(t *testing.T) {
	s := Span{
		Start: ot.MustParse("2026-07-08T23:50:00Z"),
		End:   ot.MustParse("2026-07-09T00:10:00Z"),
	}
	days, err := SpanDays(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 {
		t.Fatalf("expected 2 days, got %d", len(days))
	}
	// day 1: 23:50..24:00 = quanta 286,287 (last two of 288).
	d0 := days[0]
	if !d0.Bits.Test(286) || !d0.Bits.Test(287) || d0.Bits.PopCount() != 2 {
		t.Fatalf("day0 wrong: popcount=%d", d0.Bits.PopCount())
	}
	// day 2: 00:00..00:10 = quanta 0,1.
	d1 := days[1]
	if !d1.Bits.Test(0) || !d1.Bits.Test(1) || d1.Bits.PopCount() != 2 {
		t.Fatalf("day1 wrong: popcount=%d", d1.Bits.PopCount())
	}
}

// TestInvalidSpan: zero-length and inverted spans are rejected.
func TestInvalidSpan(t *testing.T) {
	zero := Span{
		Start: ot.MustParse("2026-07-08T00:00:00Z"),
		End:   ot.MustParse("2026-07-08T00:00:00Z"),
	}
	if _, err := SpanDays(zero); err == nil {
		t.Fatal("zero-length span should be invalid")
	}
	inv := Span{
		Start: ot.MustParse("2026-07-08T01:00:00Z"),
		End:   ot.MustParse("2026-07-08T00:00:00Z"),
	}
	if _, err := SpanDays(inv); err == nil {
		t.Fatal("inverted span should be invalid")
	}
}

// TestEntityHandleValidation: sentinel range check and the reserved top-256 pool.
func TestEntityHandleValidation(t *testing.T) {
	if ValidEntity(EntityNil) {
		t.Fatal("EntityNil must be invalid")
	}
	if ValidEntity(EntityTombstone) {
		t.Fatal("EntityTombstone must be invalid")
	}
	if !ValidEntity(1) || !ValidEntity(EntityMaxValid) {
		t.Fatal("real handles (1 .. EntityMaxValid) must be valid")
	}
	// The whole reserved pool (EntityMaxValid, MaxUint64] must be invalid, so a
	// future sentinel carved descending from the top can never read as a live
	// handle.
	if ValidEntity(EntityMaxValid + 1) {
		t.Fatal("first reserved-pool value must be invalid")
	}
	if EntityMaxValid != 0xFFFFFFFFFFFFFF00 {
		t.Fatalf("EntityMaxValid = %#x, want 0xFFFFFFFFFFFFFF00 (top 256 reserved)", EntityMaxValid)
	}
	// A handful of pool values, all invalid.
	for _, h := range []uint64{0xFFFFFFFFFFFFFF01, 0xFFFFFFFFFFFFFFFE, EntityTombstone} {
		if ValidEntity(h) {
			t.Fatalf("reserved-pool value %#x must be invalid", h)
		}
	}
}
