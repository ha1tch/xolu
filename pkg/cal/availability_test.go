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

func inst(s string) ot.Instant { return ot.MustParse(s) }

func mustAdd(o *Occupancy, p Plane, startRFC, endRFC string, t *testing.T) {
	t.Helper()
	if err := o.Add(p, Span{Start: inst(startRFC), End: inst(endRFC)}); err != nil {
		t.Fatalf("Add: %v", err)
	}
}

// --- Ternary precedence and capacity scalar ---

func TestCapacityTernaryPrecedence(t *testing.T) {
	day := PeriodDay(inst("2026-07-08T12:00:00Z"))

	// Empty calendar -> free.
	o := NewOccupancy()
	cap0, _ := o.Capacity(day)
	if cap0.State != StateFree || cap0.Capacity != 100 {
		t.Fatalf("empty: state=%v cap=%d, want free/100", cap0.State, cap0.Capacity)
	}

	// Proposed only -> idk, capacity unaffected (proposals don't reduce it).
	o = NewOccupancy()
	mustAdd(o, PlaneProposed, "2026-07-08T09:00:00Z", "2026-07-08T12:00:00Z", t)
	capP, _ := o.Capacity(day)
	if capP.State != StateIdk {
		t.Fatalf("proposed-only: state=%v, want idk", capP.State)
	}
	if capP.Capacity != 100 {
		t.Fatalf("proposed-only: capacity=%d, want 100 (proposals ignored)", capP.Capacity)
	}
	if capP.Counts.Proposed == 0 {
		t.Fatal("proposed-only: expected proposed count > 0")
	}

	// Binding present -> busy, capacity reduced.
	o = NewOccupancy()
	mustAdd(o, PlaneBinding, "2026-07-08T00:00:00Z", "2026-07-08T06:00:00Z", t) // 6h of 24h = 25%
	capB, _ := o.Capacity(day)
	if capB.State != StateBusy {
		t.Fatalf("binding: state=%v, want busy", capB.State)
	}
	if capB.Capacity != 75 {
		t.Fatalf("binding 6h/24h: capacity=%d, want 75", capB.Capacity)
	}

	// Binding dominates proposed in the same period -> busy.
	o = NewOccupancy()
	mustAdd(o, PlaneBinding, "2026-07-08T00:00:00Z", "2026-07-08T01:00:00Z", t)
	mustAdd(o, PlaneProposed, "2026-07-08T10:00:00Z", "2026-07-08T20:00:00Z", t)
	capM, _ := o.Capacity(day)
	if capM.State != StateBusy {
		t.Fatalf("binding+proposed: state=%v, want busy", capM.State)
	}
}

// TestFreeBusyComplement: free and busy are exact complements over any period.
func TestFreeBusyComplement(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 1000; i++ {
		o := NewOccupancy()
		base := inst("2026-07-08T00:00:00Z")
		// random commitments in the day
		for k := 0; k < rng.Intn(4); k++ {
			st := base.Add(time.Duration(rng.Intn(20)) * time.Hour)
			en := st.Add(time.Duration(rng.Intn(4)+1) * time.Hour)
			pl := PlaneBinding
			if rng.Intn(2) == 1 {
				pl = PlaneProposed
			}
			_ = o.Add(pl, Span{Start: st, End: en})
		}
		day := PeriodDay(base)
		free, _ := o.IsFree(day)
		busy, _ := o.IsBusy(day)
		if free == busy {
			t.Fatalf("iter %d: free==busy==%v (must be complements)", i, free)
		}
	}
}

// --- Counts vs an independent interval oracle ---

// oracleCounts tallies binding/proposed/free quanta over a single-day period by
// direct interval membership, no bitmaps. Binding dominates.
func oracleCounts(bindingSpans, proposedSpans []Span, p Period) Counts {
	var c Counts
	startN := p.Start.UnixNano()
	dayN := (startN / NsPerDay) * NsPerDay
	// assume single-day period for the oracle (tests use day periods)
	for q := 0; q < QuantaPerDay; q++ {
		qStart := dayN + int64(q)*NsPerQuantum
		qEnd := qStart + NsPerQuantum
		if qStart < p.Start.UnixNano() || qEnd > p.End.UnixNano() {
			continue // outside the period window
		}
		occBind := anyOverlap(bindingSpans, qStart, qEnd)
		occProp := anyOverlap(proposedSpans, qStart, qEnd)
		switch {
		case occBind:
			c.Binding++
		case occProp:
			c.Proposed++
		default:
			c.Free++
		}
	}
	return c
}

func anyOverlap(spans []Span, qStart, qEnd int64) bool {
	for _, s := range spans {
		if s.Start.UnixNano() < qEnd && s.End.UnixNano() > qStart {
			return true
		}
	}
	return false
}

func TestCountsMatchOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(12))
	base := inst("2026-07-08T00:00:00Z")
	day := PeriodDay(base)
	for iter := 0; iter < 2000; iter++ {
		o := NewOccupancy()
		var bSpans, pSpans []Span
		for k := 0; k < rng.Intn(5); k++ {
			st := base.Add(time.Duration(rng.Intn(1440)) * time.Minute)
			en := st.Add(time.Duration(rng.Intn(180)+5) * time.Minute)
			if en.UnixNano() > day.End.UnixNano() {
				en = day.End
			}
			if !st.Before(en) {
				continue
			}
			s := Span{Start: st, End: en}
			if rng.Intn(2) == 0 {
				bSpans = append(bSpans, s)
				_ = o.Add(PlaneBinding, s)
			} else {
				pSpans = append(pSpans, s)
				_ = o.Add(PlaneProposed, s)
			}
		}
		got, _ := o.CountQuanta(day)
		want := oracleCounts(bSpans, pSpans, day)
		if got != want {
			t.Fatalf("iter %d: counts got %+v want %+v", iter, got, want)
		}
		if got.Total() != QuantaPerDay {
			t.Fatalf("iter %d: total %d != %d", iter, got.Total(), QuantaPerDay)
		}
	}
}

// --- openings ---

func TestOpeningsBasic(t *testing.T) {
	o := NewOccupancy()
	// Book 09:00-12:00 binding; the day otherwise free.
	mustAdd(o, PlaneBinding, "2026-07-08T09:00:00Z", "2026-07-08T12:00:00Z", t)
	from := inst("2026-07-08T08:00:00Z")
	to := inst("2026-07-08T18:00:00Z")

	// A 4h slot: only the afternoon hole (12:00-18:00 = 6h) fits; the morning
	// hole (08:00-09:00 = 1h) does not.
	ops, err := o.Openings(from, to, 4*time.Hour, ObjEarliest)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 fitting opening, got %d: %+v", len(ops), ops)
	}
	if ops[0].Start.UnixNano() != inst("2026-07-08T12:00:00Z").UnixNano() {
		t.Fatalf("opening start = %v, want 12:00", ops[0].Start)
	}
	if ops[0].End.UnixNano() != to.UnixNano() {
		t.Fatalf("opening end = %v, want 18:00", ops[0].End)
	}

	// A 30m slot: both holes fit; earliest first => morning hole leads.
	ops2, _ := o.Openings(from, to, 30*time.Minute, ObjEarliest)
	if len(ops2) != 2 {
		t.Fatalf("expected 2 openings, got %d", len(ops2))
	}
	if !ops2[0].Start.Before(ops2[1].Start) {
		t.Fatal("earliest objective: not chronologically ordered")
	}
	if ops2[0].Start.UnixNano() != from.UnixNano() {
		t.Fatalf("first opening start = %v, want 08:00", ops2[0].Start)
	}
}

func TestOpeningsProposedBlocks(t *testing.T) {
	// A proposed commitment also blocks an opening (a hole means free of ANY
	// commitment — you cannot place into a tentatively-held slot).
	o := NewOccupancy()
	mustAdd(o, PlaneProposed, "2026-07-08T10:00:00Z", "2026-07-08T14:00:00Z", t)
	from := inst("2026-07-08T08:00:00Z")
	to := inst("2026-07-08T18:00:00Z")
	ops, _ := o.Openings(from, to, 3*time.Hour, ObjEarliest)
	// Holes: 08:00-10:00 (2h, no fit) and 14:00-18:00 (4h, fits).
	if len(ops) != 1 {
		t.Fatalf("expected 1 opening, got %d: %+v", len(ops), ops)
	}
	if ops[0].Start.UnixNano() != inst("2026-07-08T14:00:00Z").UnixNano() {
		t.Fatalf("opening start = %v, want 14:00", ops[0].Start)
	}
}

func TestOpeningsEmptiestObjective(t *testing.T) {
	o := NewOccupancy()
	// Two holes: a 2h morning and a 5h afternoon, split by a booking.
	mustAdd(o, PlaneBinding, "2026-07-08T10:00:00Z", "2026-07-08T11:00:00Z", t) // 10-11 busy
	mustAdd(o, PlaneBinding, "2026-07-08T16:00:00Z", "2026-07-08T16:30:00Z", t) // 16-16:30 busy
	from := inst("2026-07-08T08:00:00Z")
	to := inst("2026-07-08T21:00:00Z")
	// Holes: 08-10 (2h), 11-16 (5h), 16:30-21 (4.5h).
	ops, _ := o.Openings(from, to, 1*time.Hour, ObjEmptiest)
	if len(ops) < 2 {
		t.Fatalf("expected multiple openings, got %d", len(ops))
	}
	// Emptiest => largest margin first; 11-16 (5h) should lead.
	if ops[0].Margin < ops[1].Margin {
		t.Fatalf("emptiest: first margin %v < second %v (not descending)", ops[0].Margin, ops[1].Margin)
	}
	if ops[0].Start.UnixNano() != inst("2026-07-08T11:00:00Z").UnixNano() {
		t.Fatalf("emptiest first opening start = %v, want 11:00 (the 5h hole)", ops[0].Start)
	}
}

func TestOpeningsValidation(t *testing.T) {
	o := NewOccupancy()
	from := inst("2026-07-08T08:00:00Z")
	to := inst("2026-07-08T18:00:00Z")
	if _, err := o.Openings(to, from, time.Hour, ObjEarliest); err == nil {
		t.Fatal("reversed from/to should error")
	}
	if _, err := o.Openings(from, to, 0, ObjEarliest); err == nil {
		t.Fatal("zero duration should error")
	}
	if _, err := o.Openings(from, to, time.Hour, Objective("bogus")); err == nil {
		t.Fatal("unknown objective should error")
	}
}

// TestMultiDayAvailability: a period spanning several days counts correctly.
func TestMultiDayAvailability(t *testing.T) {
	o := NewOccupancy()
	// Binding 23:00 day1 .. 01:00 day2 (crosses midnight, 2h).
	mustAdd(o, PlaneBinding, "2026-07-08T23:00:00Z", "2026-07-09T01:00:00Z", t)
	period := Period{
		Start: inst("2026-07-08T00:00:00Z"),
		End:   inst("2026-07-10T00:00:00Z"), // 2 full days
	}
	c, err := o.CountQuanta(period)
	if err != nil {
		t.Fatal(err)
	}
	// 2h binding = 24 quanta; total = 2*288 = 576.
	if c.Binding != 24 {
		t.Fatalf("binding quanta = %d, want 24", c.Binding)
	}
	if c.Total() != 2*QuantaPerDay {
		t.Fatalf("total = %d, want %d", c.Total(), 2*QuantaPerDay)
	}
}
