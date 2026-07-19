// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"fmt"
	"sort"
	"time"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// Stage 2: single-calendar availability reads over an in-memory occupancy
// window, before any persistence. This exercises the Stage 1 bit layer through
// the read surface (cal-rest-api.md §3) without storage or lifecycle.
//
// The binding/proposed split in the availability semantics IS the two-plane
// bitmap: q=free/busy range over commitments of any kind (binding OR proposed);
// q=capacity's ternary distinguishes them (binding => busy, proposed-only =>
// idk, none => free) and the scalar capacity ignores proposals. For a SINGLE
// calendar the proposed plane's role is fully specified by §3b and is built here
// in full; the GATE-1 ambiguity concerns cross-calendar match (Stage 4), not
// single-calendar availability.

// Occupancy is an in-memory occupancy window for one calendar: sparse per-day
// bitmaps on each plane, keyed by day-floored UnixNano. Only days with
// occupancy are present (the sparse-store model). This is the Stage-2 stand-in
// for the persisted index; Stage 3 replaces the maps with Pebble-backed storage
// behind the same read methods.
type Occupancy struct {
	binding  map[int64]*DayBitmap
	proposed map[int64]*DayBitmap
}

// NewOccupancy returns an empty occupancy window.
func NewOccupancy() *Occupancy {
	return &Occupancy{
		binding:  map[int64]*DayBitmap{},
		proposed: map[int64]*DayBitmap{},
	}
}

func (o *Occupancy) planeMap(p Plane) map[int64]*DayBitmap {
	if p == PlaneProposed {
		return o.proposed
	}
	return o.binding
}

// Add ORs a span's occupancy into the given plane. Invalid spans are rejected.
func (o *Occupancy) Add(p Plane, s Span) error {
	if !p.Valid() {
		return fmt.Errorf("cal: Add: invalid plane %v", p)
	}
	days, err := SpanDays(s)
	if err != nil {
		return err
	}
	m := o.planeMap(p)
	for _, d := range days {
		bm := m[d.DayNanos]
		if bm == nil {
			bm = &DayBitmap{}
			m[d.DayNanos] = bm
		}
		for i := range bm {
			bm[i] |= d.Bits[i]
		}
	}
	return nil
}

// dayOn returns the bitmap for a plane+day, or the zero bitmap if absent.
func (o *Occupancy) dayOn(p Plane, dayNanos int64) DayBitmap {
	if bm := o.planeMap(p)[dayNanos]; bm != nil {
		return *bm
	}
	return DayBitmap{}
}

// --- Period: a half-open [Start, End) instant range to read over ---

// Period is the resolved time range of an availability query. The REST layer
// parses human/ISO period strings ("2027/month/05") into a Period; the bit layer
// works in absolute instants only.
type Period struct {
	Start ot.Instant
	End   ot.Instant
}

// Valid reports a well-formed period.
func (p Period) Valid() bool { return p.Start.Before(p.End) }

// PeriodDay builds a one-UTC-day period for the day containing t.
func PeriodDay(t ot.Instant) Period {
	dayN := (t.UnixNano() / NsPerDay) * NsPerDay
	return Period{
		Start: ot.FromUnixNano(dayN),
		End:   ot.FromUnixNano(dayN + NsPerDay),
	}
}

// quantaInPeriod walks the period and yields, per day it touches, the day's
// floored UnixNano and the [loQ, hiQ) quantum sub-range within that day that the
// period covers (half-open). This is the read analogue of SpanDays.
func quantaInPeriod(p Period) []struct {
	DayNanos int64
	LoQ, HiQ int
} {
	var out []struct {
		DayNanos int64
		LoQ, HiQ int
	}
	startN := p.Start.UnixNano()
	endN := p.End.UnixNano()
	dayStart := (startN / NsPerDay) * NsPerDay
	for dayStart < endN {
		dayEnd := dayStart + NsPerDay
		loN, hiN := startN, endN
		if dayStart > loN {
			loN = dayStart
		}
		if dayEnd < hiN {
			hiN = dayEnd
		}
		loQ := int((loN - dayStart) / NsPerQuantum)
		hiQ := int((hiN - dayStart + NsPerQuantum - 1) / NsPerQuantum)
		if hiQ > QuantaPerDay {
			hiQ = QuantaPerDay
		}
		out = append(out, struct {
			DayNanos int64
			LoQ, HiQ int
		}{dayStart, loQ, hiQ})
		dayStart = dayEnd
	}
	return out
}

// --- Counts: the raw tallies §3b ships so callers derive their own measures ---

// Counts holds quantum tallies over a period. Binding = quanta with a binding
// commitment; Proposed = quanta proposed-but-not-binding (proposed set, binding
// clear); Free = quanta with no commitment of any kind. The three partition the
// period's quanta: Binding + Proposed + Free == total quanta in the period.
type Counts struct {
	Binding  int
	Proposed int
	Free     int
}

// Total is the number of quanta in the period.
func (c Counts) Total() int { return c.Binding + c.Proposed + c.Free }

// CountQuanta tallies binding/proposed/free quanta over the period. A quantum is
// Binding if its binding bit is set; else Proposed if its proposed bit is set;
// else Free. (Binding dominates: a quantum both binding and proposed counts as
// Binding — it is genuinely taken.)
func (o *Occupancy) CountQuanta(p Period) (Counts, error) {
	if !p.Valid() {
		return Counts{}, fmt.Errorf("cal: CountQuanta: invalid period")
	}
	var c Counts
	for _, seg := range quantaInPeriod(p) {
		b := o.dayOn(PlaneBinding, seg.DayNanos)
		pr := o.dayOn(PlaneProposed, seg.DayNanos)
		for q := seg.LoQ; q < seg.HiQ; q++ {
			switch {
			case b.Test(q):
				c.Binding++
			case pr.Test(q):
				c.Proposed++
			default:
				c.Free++
			}
		}
	}
	return c, nil
}

// --- The three availability reads (§3b) ---

// IsFree reports q=free: are there ZERO commitments of any kind in the period?
func (o *Occupancy) IsFree(p Period) (bool, error) {
	c, err := o.CountQuanta(p)
	if err != nil {
		return false, err
	}
	return c.Binding == 0 && c.Proposed == 0, nil
}

// IsBusy reports q=busy: is there ONE OR MORE commitment of any kind? The exact
// complement of IsFree.
func (o *Occupancy) IsBusy(p Period) (bool, error) {
	free, err := o.IsFree(p)
	if err != nil {
		return false, err
	}
	return !free, nil
}

// CapacityState is the q=capacity ternary (§3b).
type CapacityState string

const (
	StateFree CapacityState = "free" // no commitments at all
	StateIdk  CapacityState = "idk"  // proposed present, no binding (uncertain)
	StateBusy CapacityState = "busy" // binding present
)

// Capacity is the full q=capacity result: the ternary state, the scalar
// capacity = 100 − confirmed% (binding share; proposals ignored), and the raw
// counts.
type Capacity struct {
	State    CapacityState `json:"state"`
	Capacity int           `json:"capacity"` // 100 − binding%
	Counts   Counts        `json:"counts"`
}

// Capacity computes q=capacity over the period.
//
//   - state: binding present => busy; else proposed present => idk; else free.
//   - capacity: 100 − (binding quanta / total quanta · 100), rounded; proposals
//     do not reduce it.
func (o *Occupancy) Capacity(p Period) (Capacity, error) {
	c, err := o.CountQuanta(p)
	if err != nil {
		return Capacity{}, err
	}
	var state CapacityState
	switch {
	case c.Binding > 0:
		state = StateBusy
	case c.Proposed > 0:
		state = StateIdk
	default:
		state = StateFree
	}
	total := c.Total()
	capPct := 100
	if total > 0 {
		// 100 − confirmed%, integer-rounded.
		bindingPct := (c.Binding*100 + total/2) / total
		capPct = 100 - bindingPct
	}
	return Capacity{State: state, Capacity: capPct, Counts: c}, nil
}

// --- openings (§3a): where could a duration fit? ---
//
// Returns free spans wide enough for `duration` within [from, to). "Free" here
// means no commitment of any kind (binding or proposed) — a hole you could
// actually take. This is the placement question; it is deliberately distinct
// from availability (occupancy). objective is a FIXED enum, never a scoring
// function (A7 anti-solver); Stage 2 implements `earliest` (the spans in
// chronological order) and `first-fit`; `emptiest`/`longest-clear-margin` are
// margin-ranked variants layered on the same hole list.

// Objective is the fixed openings ordering enum.
type Objective string

const (
	ObjEarliest   Objective = "earliest"  // chronological
	ObjFirstFit   Objective = "first-fit" // first hole that fits (== earliest, one result)
	ObjEmptiest   Objective = "emptiest"  // most surrounding free margin first
	ObjLongestClr Objective = "longest-clear-margin"
)

// Opening is a free span wide enough for the requested duration.
type Opening struct {
	Start  ot.Instant
	End    ot.Instant
	Margin time.Duration // length of the containing free run
}

// freeRunsQuantized walks [from,to) at quantum resolution and returns maximal
// runs of free quanta as instant spans. A quantum is free iff neither plane has
// it set. Runs are clipped to the [from,to) window.
func (o *Occupancy) freeRuns(from, to ot.Instant) []Opening {
	var runs []Opening
	p := Period{Start: from, End: to}
	if !p.Valid() {
		return nil
	}

	runStartNanos := int64(-1)
	flush := func(endNanos int64) {
		if runStartNanos >= 0 {
			runs = append(runs, Opening{
				Start:  ot.FromUnixNano(runStartNanos),
				End:    ot.FromUnixNano(endNanos),
				Margin: time.Duration(endNanos-runStartNanos) * time.Nanosecond,
			})
			runStartNanos = -1
		}
	}

	for _, seg := range quantaInPeriod(p) {
		b := o.dayOn(PlaneBinding, seg.DayNanos)
		pr := o.dayOn(PlaneProposed, seg.DayNanos)
		for q := seg.LoQ; q < seg.HiQ; q++ {
			free := !b.Test(q) && !pr.Test(q)
			qStartNanos := seg.DayNanos + int64(q)*NsPerQuantum
			qEndNanos := qStartNanos + NsPerQuantum
			if free {
				if runStartNanos < 0 {
					runStartNanos = qStartNanos
				}
			} else {
				flush(qStartNanos)
			}
			_ = qEndNanos
		}
	}
	// Flush a trailing free run at the period end.
	flush(p.End.UnixNano())

	// Clip the first run's start and last run's end to the exact [from,to)
	// window (quantum granularity may overshoot the requested bounds).
	fromN, toN := from.UnixNano(), to.UnixNano()
	for i := range runs {
		if runs[i].Start.UnixNano() < fromN {
			runs[i].Start = ot.FromUnixNano(fromN)
		}
		if runs[i].End.UnixNano() > toN {
			runs[i].End = ot.FromUnixNano(toN)
		}
		runs[i].Margin = runs[i].End.Sub(runs[i].Start)
	}
	return runs
}

// Openings returns spans wide enough for duration within [from,to), ordered per
// objective. Cal implements exclusive-only occupancy: every booking takes the
// whole calendar for its span (see Mode godoc in booking.go). Buffer-after
// aftermath holds are honoured; capacity sub-units are not applicable in the
// exclusive-only model.
func (o *Occupancy) Openings(from, to ot.Instant, duration time.Duration, obj Objective) ([]Opening, error) {
	if !from.Before(to) {
		return nil, fmt.Errorf("cal: Openings: from not before to")
	}
	if duration <= 0 {
		return nil, fmt.Errorf("cal: Openings: duration must be positive")
	}

	runs := o.freeRuns(from, to)

	// Keep only runs that fit the duration. A run of length L admits a booking
	// of `duration` iff L >= duration; the opening is reported as the full run
	// (the caller picks a sub-span).
	var fit []Opening
	for _, r := range runs {
		if r.End.Sub(r.Start) >= duration {
			fit = append(fit, r)
		}
	}

	switch obj {
	case ObjEarliest, ObjFirstFit, "":
		sort.Slice(fit, func(i, j int) bool {
			return fit[i].Start.Before(fit[j].Start)
		})
		if obj == ObjFirstFit && len(fit) > 1 {
			fit = fit[:1]
		}
	case ObjEmptiest, ObjLongestClr:
		// Most free margin first; ties broken by earliest.
		sort.Slice(fit, func(i, j int) bool {
			mi, mj := fit[i].Margin, fit[j].Margin
			if mi != mj {
				return mi > mj
			}
			return fit[i].Start.Before(fit[j].Start)
		})
	default:
		return nil, fmt.Errorf("cal: Openings: unknown objective %q", obj)
	}
	return fit, nil
}
