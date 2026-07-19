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

// Stage 4: multi-calendar match (cal-rest-api.md §8) and single-calendar check
// (§5). match answers "when are all these calendars simultaneously free for
// `duration`?" — the F16 operation the primitive most exists for.
//
// GATE-1 realisation: each calendar contributes its own busy-map to the N-way
// AndFree, built per its match_considers policy:
//   - ConsiderBinding         -> the binding plane only (optimistic)
//   - ConsiderBindingProposed -> binding ∪ proposed     (pessimistic)
// AndFree ANDs the free-masks, so the result is busy wherever ANY calendar's
// contribution is busy. Pessimism therefore wins on clash automatically — a
// pessimistic calendar's proposed bits remove a slot; an optimistic calendar's
// proposals do not. No agreement logic.

// calBusyDay returns one calendar's busy-map for a given day on the planes its
// policy selects.
func (s *IndexStore) calBusyDay(c Calendar, dayNanos int64) (DayBitmap, error) {
	bind, err := s.dayValue(c.Ordinal, PlaneBinding, dayNanos)
	if err != nil {
		return DayBitmap{}, err
	}
	if c.MatchPolicy != ConsiderBindingProposed {
		return bind, nil // optimistic: binding only
	}
	prop, err := s.dayValue(c.Ordinal, PlaneProposed, dayNanos)
	if err != nil {
		return DayBitmap{}, err
	}
	return bind.Or(prop), nil // pessimistic: binding ∪ proposed
}

// MatchResult is the outcome of a match: coincident free spans, or the calendars
// that blocked any coincidence.
type MatchResult struct {
	Matches  []Span
	Checked  []string
	Blocking []string // calendars with zero free coincidence (only set when Matches empty)
}

// Match returns spans within [from,to) where every named calendar is
// simultaneously free for at least `duration`, honouring each calendar's
// match_considers policy. cals must be resolved Calendar records (the caller
// looks them up; Match needs their ordinals and policies).
//
// The day-level rollup prunes days that cannot contain a coincidence before the
// fine AND runs (prune-not-confirm: a pruned day definitely has none; a surviving
// day is confirmed at fine grain). When the result is empty, Blocking names the
// calendars responsible.
func (s *IndexStore) Match(cals []Calendar, from, to ot.Instant, duration time.Duration) (MatchResult, error) {
	if len(cals) == 0 {
		return MatchResult{}, fmt.Errorf("cal: Match: no calendars")
	}
	if !from.Before(to) {
		return MatchResult{}, fmt.Errorf("cal: Match: from not before to")
	}
	if duration <= 0 {
		return MatchResult{}, fmt.Errorf("cal: Match: duration must be positive")
	}

	checked := make([]string, len(cals))
	for i, c := range cals {
		checked[i] = c.CalendarID
	}

	// Build a combined free-occupancy across all calendars into an in-memory
	// Occupancy (single synthetic calendar whose "busy" is the union of every
	// calendar's policy-selected busy). Then the Stage-2 openings logic finds
	// runs of commonly-free time. This reuses verified code rather than
	// re-deriving run-finding.
	combined := NewOccupancy()

	// Walk each day in [from,to); for days that survive rollup pruning, OR every
	// calendar's busy contribution into the combined map on the binding plane
	// (the combined map's "binding" is the union-busy; proposed is unused here).
	startDay := (from.UnixNano() / NsPerDay) * NsPerDay
	endN := to.UnixNano()

	// Track, per calendar, whether it ever contributed busy bits that eliminated
	// otherwise-free time — used to populate Blocking.
	contributedBusy := make([]bool, len(cals))

	for dayNanos := startDay; dayNanos < endN; dayNanos += NsPerDay {
		// Rollup prune: if any calendar has every daypart in this day fully busy
		// on its selected planes, the day cannot contain a coincidence. (A light
		// prune at v1's single daypart level; see §4 — prunes fully-saturated
		// dayparts only.) We compute each calendar's day rollup and the candidate
		// dayparts; if no daypart survives, skip the fine AND for this day.
		var rollups []DaypartRollup
		dayBusy := make([]DayBitmap, len(cals))
		for i, c := range cals {
			bm, err := s.calBusyDay(c, dayNanos)
			if err != nil {
				return MatchResult{}, err
			}
			dayBusy[i] = bm
			rollups = append(rollups, RollupDay(bm))
		}
		candidate := MatchCandidateDayparts(rollups...)
		if candidate == 0 {
			// Every daypart pruned: no common free quantum exists this day, so
			// the whole day is busy in the combined map. Recording it as fully
			// busy is essential — an ABSENT day reads as fully FREE in Openings,
			// which would invert the result.
			full := allBusyDay()
			combined.binding[dayNanos] = &full
			for i := range cals {
				contributedBusy[i] = contributedBusy[i] || !isAllFree(dayBusy[i])
			}
			continue
		}

		// Fine AND: commonly-free quanta this day. The combined map's busy is the
		// complement of free over the day. complementDay handles the free.IsZero
		// case (whole day busy) and the all-free case (no busy entry) correctly,
		// so there is no early continue here — an absent day would read as free.
		free := AndFree(dayBusy...)
		busy := complementDay(free)
		if !busy.IsZero() {
			bm := &DayBitmap{}
			*bm = busy
			combined.binding[dayNanos] = bm
		}
		// (a day with free==all-free contributes no busy entry, i.e. fully free)
		for i := range cals {
			contributedBusy[i] = contributedBusy[i] || !isAllFree(dayBusy[i])
		}
	}

	// Find free runs of >= duration in the combined map across [from,to).
	openings, err := combined.Openings(from, to, duration, ObjEarliest)
	if err != nil {
		return MatchResult{}, err
	}

	res := MatchResult{Checked: checked}
	for _, op := range openings {
		res.Matches = append(res.Matches, Span{Start: op.Start, End: op.End})
	}

	// If no coincidence, name the blocking calendars: those that contributed any
	// busy time within the window. (A calendar that was entirely free over the
	// window never blocks; one that had commitments did.)
	if len(res.Matches) == 0 {
		for i, c := range cals {
			if contributedBusy[i] {
				res.Blocking = append(res.Blocking, c.CalendarID)
			}
		}
		sort.Strings(res.Blocking)
	}
	return res, nil
}

// isAllFree reports whether a busy-map has no occupied valid quanta.
func isAllFree(b DayBitmap) bool {
	return b.freeMask() == allFreeDay()
}

// allFreeDay returns a day with every valid quantum free (set), slack zeroed.
func allFreeDay() DayBitmap {
	var r DayBitmap
	for i := range r {
		r[i] = ^uint64(0)
	}
	const validBitsInLastWord = QuantaPerDay - 64*(WordsPerDay-1)
	r[WordsPerDay-1] &= (1 << validBitsInLastWord) - 1
	return r
}

// allBusyDay returns a day with every valid quantum occupied (set in a busy-map),
// slack zeroed. Same bit pattern as allFreeDay but used as a busy-map.
func allBusyDay() DayBitmap {
	return allFreeDay()
}

// complementDay returns the busy-map whose set bits are the FREE bits of the
// input's complement — i.e. given a free-map, return the corresponding busy-map
// over valid quanta (slack zeroed).
func complementDay(free DayBitmap) DayBitmap {
	all := allFreeDay()
	var busy DayBitmap
	for i := range busy {
		busy[i] = all[i] &^ free[i]
	}
	return busy
}

// --- check (§5): single-calendar feasibility dry-run ---

// CheckResult is the outcome of a feasibility check: feasible, or the conflicts
// and nearest openings.
type CheckResult struct {
	Feasible        bool
	NearestOpenings []Span // when infeasible: where "yes" lives nearby
}

// Check reports whether a booking of the given span/mode would be feasible on a
// calendar right now, writing nothing. Same occupancy engine as a real create,
// so check and create cannot drift. For an exclusive booking, feasible means the
// span is entirely free of any commitment (binding or proposed) on the calendar.
//
// When infeasible, NearestOpenings offers free spans of the same duration within
// a search window around the requested time (the "here's where yes lives" hint).
func (s *IndexStore) Check(c Calendar, span Span, mode Mode) (CheckResult, error) {
	if !span.Valid() {
		return CheckResult{}, fmt.Errorf("cal: Check: invalid span")
	}
	o, err := s.ReadOccupancy(c.CalendarID)
	if err != nil {
		return CheckResult{}, err
	}

	// Feasibility: the requested span must be free of any commitment.
	period := Period(span)
	counts, err := o.CountQuanta(period)
	if err != nil {
		return CheckResult{}, err
	}
	feasible := counts.Binding == 0 && counts.Proposed == 0

	res := CheckResult{Feasible: feasible}
	if feasible {
		return res, nil
	}

	// Infeasible: find nearest openings of the same duration in a window that
	// extends a day either side of the requested span.
	dur := span.End.Sub(span.Start)
	from := span.Start.Add(-24 * time.Hour)
	to := span.End.Add(24 * time.Hour)
	ops, err := o.Openings(from, to, dur, ObjEarliest)
	if err != nil {
		return res, err
	}
	for _, op := range ops {
		res.NearestOpenings = append(res.NearestOpenings, Span{Start: op.Start, End: op.End})
		if len(res.NearestOpenings) >= 3 {
			break // a few suggestions, not an exhaustive list
		}
	}
	return res, nil
}
