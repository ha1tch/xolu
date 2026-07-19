// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// setupMatchStore builds a store + source with the given calendars and bookings,
// rebuilds the index, and returns both. calPolicies maps calendarID -> policy.
func setupMatchStore(t *testing.T, calPolicies map[string]MatchConsiders, bookings []Booking) (*IndexStore, []Calendar) {
	t.Helper()
	s := openTestStore(t, 1)
	src := NewMemBookingSource(false)
	var cals []Calendar
	// deterministic calendar order
	ids := make([]string, 0, len(calPolicies))
	for id := range calPolicies {
		ids = append(ids, id)
	}
	// stable sort
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	for _, id := range ids {
		cc, err := src.CreateCalendar(Calendar{CalendarID: id, EntityRef: uint64(len(cals) + 1), MatchPolicy: calPolicies[id]})
		if err != nil {
			t.Fatal(err)
		}
		s.RegisterCalendar(cc)
		cals = append(cals, cc)
	}
	for _, b := range bookings {
		if err := src.PutBooking(b); err != nil {
			t.Fatalf("PutBooking %s: %v", b.BookingID, err)
		}
	}
	if err := s.RebuildFrom(src); err != nil {
		t.Fatal(err)
	}
	return s, cals
}

func bk(id, cal string, st State, startRFC, endRFC string, bearer uint64) Booking {
	return Booking{
		BookingID: id, CalendarID: cal, State: st,
		Span: Span{Start: ot.MustParse(startRFC), End: ot.MustParse(endRFC)},
		Mode: ModeExclusive, Bearer: bearer,
		CreatedAt: ot.Now(), UpdatedAt: ot.Now(),
	}
}

// TestMatchBasicIntersection: two calendars, each busy a different part of the
// day; match finds the gap they share.
func TestMatchBasicIntersection(t *testing.T) {
	// cal-a busy 09:00-12:00; cal-b busy 14:00-16:00. Both free 12:00-14:00 and
	// 16:00-end and 00:00-09:00.
	s, cals := setupMatchStore(t,
		map[string]MatchConsiders{"cal-a": ConsiderBinding, "cal-b": ConsiderBinding},
		[]Booking{
			bk("a1", "cal-a", StateBinding, "2026-07-08T09:00:00Z", "2026-07-08T12:00:00Z", 100),
			bk("b1", "cal-b", StateBinding, "2026-07-08T14:00:00Z", "2026-07-08T16:00:00Z", 101),
		})
	from := ot.MustParse("2026-07-08T08:00:00Z")
	to := ot.MustParse("2026-07-08T20:00:00Z")

	// 2h match: gaps are 12:00-14:00 (2h, fits) and 16:00-20:00 (4h, fits); the
	// 08:00-09:00 gap (1h) does not fit.
	res, err := s.Match(cals, from, to, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 2 {
		t.Fatalf("expected 2 matches, got %d: %+v", len(res.Matches), res.Matches)
	}
	if res.Matches[0].Start.UnixNano() != ot.MustParse("2026-07-08T12:00:00Z").UnixNano() {
		t.Fatalf("first match start = %v, want 12:00", res.Matches[0].Start)
	}
}

// TestMatchOptimisticIgnoresProposed: an optimistic (binding-only) calendar's
// proposals do NOT block a match.
func TestMatchOptimisticIgnoresProposed(t *testing.T) {
	s, cals := setupMatchStore(t,
		map[string]MatchConsiders{"opt": ConsiderBinding},
		[]Booking{
			// a proposed booking 10:00-14:00 — should NOT block under optimistic.
			bk("p1", "opt", StateProposed, "2026-07-08T10:00:00Z", "2026-07-08T14:00:00Z", 0),
		})
	from := ot.MustParse("2026-07-08T08:00:00Z")
	to := ot.MustParse("2026-07-08T18:00:00Z")
	res, err := s.Match(cals, from, to, 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// The whole window should be one big free run (proposals ignored).
	if len(res.Matches) != 1 {
		t.Fatalf("optimistic: expected 1 full-window match, got %d: %+v", len(res.Matches), res.Matches)
	}
	if res.Matches[0].Start.UnixNano() != from.UnixNano() || res.Matches[0].End.UnixNano() != to.UnixNano() {
		t.Fatalf("optimistic: match = %v..%v, want full window", res.Matches[0].Start, res.Matches[0].End)
	}
}

// TestMatchPessimisticBlocksOnProposed: a pessimistic calendar's proposal DOES
// block.
func TestMatchPessimisticBlocksOnProposed(t *testing.T) {
	s, cals := setupMatchStore(t,
		map[string]MatchConsiders{"pess": ConsiderBindingProposed},
		[]Booking{
			bk("p1", "pess", StateProposed, "2026-07-08T10:00:00Z", "2026-07-08T14:00:00Z", 0),
		})
	from := ot.MustParse("2026-07-08T08:00:00Z")
	to := ot.MustParse("2026-07-08T18:00:00Z")
	res, err := s.Match(cals, from, to, 3*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// Holes: 08:00-10:00 (2h, no fit), 14:00-18:00 (4h, fits). So exactly one.
	if len(res.Matches) != 1 {
		t.Fatalf("pessimistic: expected 1 match, got %d: %+v", len(res.Matches), res.Matches)
	}
	if res.Matches[0].Start.UnixNano() != ot.MustParse("2026-07-08T14:00:00Z").UnixNano() {
		t.Fatalf("pessimistic: match start = %v, want 14:00", res.Matches[0].Start)
	}
}

// TestMatchMixedPoliciesPessimismWins: an optimistic and a pessimistic calendar
// in the same match; the pessimistic one's proposal removes the slot (pessimism
// wins on clash, automatically).
func TestMatchMixedPoliciesPessimismWins(t *testing.T) {
	s, cals := setupMatchStore(t,
		map[string]MatchConsiders{
			"opt":  ConsiderBinding,
			"pess": ConsiderBindingProposed,
		},
		[]Booking{
			// opt has a proposal 10:00-12:00 (ignored, it's optimistic).
			bk("op1", "opt", StateProposed, "2026-07-08T10:00:00Z", "2026-07-08T12:00:00Z", 0),
			// pess has a proposal 13:00-15:00 (blocks, it's pessimistic).
			bk("pp1", "pess", StateProposed, "2026-07-08T13:00:00Z", "2026-07-08T15:00:00Z", 0),
		})
	from := ot.MustParse("2026-07-08T08:00:00Z")
	to := ot.MustParse("2026-07-08T18:00:00Z")
	res, err := s.Match(cals, from, to, 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// opt's proposal doesn't block; pess's does. So the only blocked region is
	// 13:00-15:00. Free runs: 08:00-13:00 (5h) and 15:00-18:00 (3h).
	if len(res.Matches) != 2 {
		t.Fatalf("mixed: expected 2 matches, got %d: %+v", len(res.Matches), res.Matches)
	}
	// verify the gap is exactly pess's proposal window
	if res.Matches[0].End.UnixNano() != ot.MustParse("2026-07-08T13:00:00Z").UnixNano() {
		t.Fatalf("mixed: first match should end at 13:00 (pess proposal start), got %v", res.Matches[0].End)
	}
	if res.Matches[1].Start.UnixNano() != ot.MustParse("2026-07-08T15:00:00Z").UnixNano() {
		t.Fatalf("mixed: second match should start at 15:00 (pess proposal end), got %v", res.Matches[1].Start)
	}
}

// TestMatchBlockingDiagnostic: when no coincidence exists, blocking names the
// calendars that contributed busy time.
func TestMatchNoCoincidenceBlocking(t *testing.T) {
	// cal-a busy all day; match can't find anything; cal-a is blocking.
	s, cals := setupMatchStore(t,
		map[string]MatchConsiders{"cal-a": ConsiderBinding, "cal-b": ConsiderBinding},
		[]Booking{
			bk("a1", "cal-a", StateBinding, "2026-07-08T00:00:00Z", "2026-07-09T00:00:00Z", 100),
			// cal-b entirely free this day.
		})
	from := ot.MustParse("2026-07-08T00:00:00Z")
	to := ot.MustParse("2026-07-09T00:00:00Z")
	res, err := s.Match(cals, from, to, 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 0 {
		t.Fatalf("expected no matches, got %d", len(res.Matches))
	}
	if len(res.Blocking) != 1 || res.Blocking[0] != "cal-a" {
		t.Fatalf("blocking = %v, want [cal-a]", res.Blocking)
	}
}

// --- Match vs independent N-way interval oracle ---

// oracleMatch computes, by direct interval arithmetic, the quanta in [from,to)
// that are free in ALL calendars per their policies, then the runs of >=
// duration. No bitmaps.
func oracleMatchFreeQuanta(cals []Calendar, bookingsByCal map[string][]Booking, from, to ot.Instant) map[int64]bool {
	freeQ := map[int64]bool{} // absolute quantum index -> commonly free
	fromN, toN := from.UnixNano(), to.UnixNano()
	startQ := fromN / NsPerQuantum
	endQ := (toN + NsPerQuantum - 1) / NsPerQuantum
	for aq := startQ; aq < endQ; aq++ {
		qStart := aq * NsPerQuantum
		qEnd := qStart + NsPerQuantum
		if qStart < fromN || qEnd > toN {
			continue
		}
		commonFree := true
		for _, c := range cals {
			for _, b := range bookingsByCal[c.CalendarID] {
				plane, occ := b.State.occupiesPlane()
				if !occ {
					continue
				}
				// policy: optimistic ignores proposed plane.
				if c.MatchPolicy != ConsiderBindingProposed && plane == PlaneProposed {
					continue
				}
				if b.Span.Start.UnixNano() < qEnd && b.Span.End.UnixNano() > qStart {
					commonFree = false
					break
				}
			}
			if !commonFree {
				break
			}
		}
		if commonFree {
			freeQ[aq] = true
		}
	}
	return freeQ
}

func TestMatchVsOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(40))
	base := ot.MustParse("2026-07-08T00:00:00Z")
	from := base
	to := base.Add(24 * time.Hour)

	for trial := 0; trial < 500; trial++ {
		nCal := rng.Intn(3) + 1
		policies := map[string]MatchConsiders{}
		for c := 0; c < nCal; c++ {
			pol := ConsiderBinding
			if rng.Intn(2) == 1 {
				pol = ConsiderBindingProposed
			}
			policies[fmt.Sprintf("c%d", c)] = pol
		}
		var bookings []Booking
		bookingsByCal := map[string][]Booking{}
		bi := 0
		for cid := range policies {
			for k := 0; k < rng.Intn(4); k++ {
				stMin := rng.Intn(24 * 12) // 5-min slots in a day
				durMin := (rng.Intn(24) + 1) * 5
				st := base.Add(time.Duration(stMin*5) * time.Minute)
				en := st.Add(time.Duration(durMin) * time.Minute)
				if en.UnixNano() > to.UnixNano() {
					en = to
				}
				if !st.Before(en) {
					continue
				}
				state := StateBinding
				bearer := uint64(bi + 100)
				if rng.Intn(2) == 0 {
					state = StateProposed
					bearer = 0
				}
				b := bk(fmt.Sprintf("b%d", bi), cid, state,
					st.String(), en.String(), bearer)
				// bk uses MustParse on RFC strings; st.String() is RFC3339-ish but
				// may lack form — build span directly instead.
				b.Span = Span{Start: st, End: en}
				bookings = append(bookings, b)
				bookingsByCal[cid] = append(bookingsByCal[cid], b)
				bi++
			}
		}

		s, cals := setupMatchStore(t, policies, bookings)

		dur := time.Duration((rng.Intn(6)+1)*5) * time.Minute
		res, err := s.Match(cals, from, to, dur)
		if err != nil {
			t.Fatalf("trial %d: Match: %v", trial, err)
		}

		// Oracle free quanta, then check each returned match span is entirely
		// within commonly-free quanta and meets duration; and that the match set
		// covers all maximal free runs >= duration.
		freeQ := oracleMatchFreeQuanta(cals, bookingsByCal, from, to)

		// every returned match must be wholly free in the oracle and >= dur.
		for _, m := range res.Matches {
			if m.End.Sub(m.Start) < dur {
				t.Fatalf("trial %d: match %v shorter than dur %v", trial, m, dur)
			}
			for aq := m.Start.UnixNano() / NsPerQuantum; aq < m.End.UnixNano()/NsPerQuantum; aq++ {
				if !freeQ[aq] {
					t.Fatalf("trial %d: match %v covers non-free quantum %d", trial, m, aq)
				}
			}
		}

		// completeness: build oracle runs and confirm count of fitting runs ==
		// returned count.
		oracleRuns := runsFromFreeQuanta(freeQ, from, to)
		fitting := 0
		for _, r := range oracleRuns {
			if r.End.Sub(r.Start) >= dur {
				fitting++
			}
		}
		if fitting != len(res.Matches) {
			t.Fatalf("trial %d: fitting oracle runs %d != matches %d", trial, fitting, len(res.Matches))
		}
		s.Close()
	}
}

// runsFromFreeQuanta turns a free-quantum set into maximal contiguous instant
// runs within [from,to).
func runsFromFreeQuanta(freeQ map[int64]bool, from, to ot.Instant) []Span {
	var runs []Span
	startQ := from.UnixNano() / NsPerQuantum
	endQ := to.UnixNano() / NsPerQuantum
	runStart := int64(-1)
	for aq := startQ; aq <= endQ; aq++ {
		free := aq < endQ && freeQ[aq]
		if free {
			if runStart < 0 {
				runStart = aq
			}
		} else {
			if runStart >= 0 {
				runs = append(runs, Span{
					Start: ot.FromUnixNano(runStart * NsPerQuantum),
					End:   ot.FromUnixNano(aq * NsPerQuantum),
				})
				runStart = -1
			}
		}
	}
	return runs
}

// --- check ---

func TestCheckFeasibleAndNot(t *testing.T) {
	s, cals := setupMatchStore(t,
		map[string]MatchConsiders{"room": ConsiderBinding},
		[]Booking{
			bk("b1", "room", StateBinding, "2026-07-08T09:00:00Z", "2026-07-08T12:00:00Z", 100),
		})
	room := cals[0]

	// Feasible: 13:00-14:00 is free.
	r1, err := s.Check(room, Span{Start: ot.MustParse("2026-07-08T13:00:00Z"), End: ot.MustParse("2026-07-08T14:00:00Z")}, ModeExclusive)
	if err != nil {
		t.Fatal(err)
	}
	if !r1.Feasible {
		t.Fatal("13:00-14:00 should be feasible")
	}

	// Infeasible: 10:00-11:00 clashes with the 09:00-12:00 booking.
	r2, err := s.Check(room, Span{Start: ot.MustParse("2026-07-08T10:00:00Z"), End: ot.MustParse("2026-07-08T11:00:00Z")}, ModeExclusive)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Feasible {
		t.Fatal("10:00-11:00 should be infeasible (clash)")
	}
	if len(r2.NearestOpenings) == 0 {
		t.Fatal("infeasible check should offer nearest openings")
	}
	// the nearest opening must be free of the booking (start at/after 12:00 or
	// before 09:00).
	for _, op := range r2.NearestOpenings {
		if op.Start.UnixNano() < ot.MustParse("2026-07-08T09:00:00Z").UnixNano() {
			continue // before the booking, fine
		}
		if op.Start.UnixNano() < ot.MustParse("2026-07-08T12:00:00Z").UnixNano() {
			t.Fatalf("nearest opening %v overlaps the booking", op)
		}
	}
}
