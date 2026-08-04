// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package chronicle is the extracted cascade engine of the chronicle
// substrate (docs/proposals/chronicle-substrate.md, @C03/@C04): a
// generic rollup cascade parameterised by a monoid, over a time-grain
// hierarchy.
//
// The theorem (@C03): the rollup cascade in ts, cal, and bal is one
// construction — a monoid homomorphism over a grain hierarchy. ts folds
// (number, +, 0) and (min/max with identities); cal's dayparts fold
// (bitset, OR, ∅); bal folds (int64, +, 0). This package implements the
// construction once, parameterised: an associative combine with an
// identity, cascading upward on append, invalidating on correction.
//
// Sequencing (@C §5): incumbents do not migrate onto this engine merely
// because it exists — cal's rollups are stress-verified on real hardware
// and migrate opportunistically or never; ts likewise when next touched
// for its own reasons. New consumers (bal, wave 4) ride the engine
// natively. The instantiation tests in this package prove the incumbent
// monoids are expressible, which is the extraction's correctness bar.
package chronicle

import (
	"fmt"
	"time"
)

// Monoid is the algebraic parameter of the cascade: an associative
// Combine with an Identity element. Associativity and identity are laws
// the implementation must satisfy — they are property-tested per
// instantiation in this package, not assumed.
type Monoid[T any] interface {
	// Identity returns the neutral element: Combine(Identity(), x) == x.
	Identity() T
	// Combine folds two values. Must be associative:
	// Combine(a, Combine(b, c)) == Combine(Combine(a, b), c).
	Combine(a, b T) T
}

// Grain is one level of the time hierarchy: a tiling of the timeline
// into buckets. Instants truncate onto grain-aligned bucket starts in
// UTC.
//
// A grain is defined by two operations rather than a width, because
// calendar periods (months, quarters, years) have no fixed duration yet
// tile time perfectly:
//
//   - truncate(t): the start of the bucket containing t
//   - next(s):     the start of the bucket following the one at s
//
// Fixed-width grains are built with FixedGrain; calendar grains with
// MonthGrain / MonthsGrain. This mirrors seal.go's WindowFn, which
// already carried both shapes.
type Grain struct {
	Name string // e.g. "hour", "day", "month" — diagnostic only

	// Width is the fixed bucket width, or 0 for calendar grains. Kept
	// for diagnostics and for the fixed-width fast paths; never assume
	// it is non-zero.
	Width time.Duration

	truncate func(time.Time) time.Time
	next     func(time.Time) time.Time
}

// FixedGrain builds a fixed-width grain (hour, day, week, 5m…).
func FixedGrain(name string, width time.Duration) Grain {
	return Grain{
		Name:     name,
		Width:    width,
		truncate: func(t time.Time) time.Time { return t.UTC().Truncate(width) },
		next:     func(s time.Time) time.Time { return s.Add(width) },
	}
}

// MonthsGrain builds a calendar grain spanning n whole months, aligned
// to the start of the year: n=1 months, n=3 quarters, n=6 halves,
// n=12 years. Alignment to January keeps quarters at Jan/Apr/Jul/Oct
// and makes every coarser multiple nest exactly.
func MonthsGrain(name string, n int) Grain {
	return Grain{
		Name: name,
		truncate: func(t time.Time) time.Time {
			t = t.UTC()
			m := int(t.Month()) - 1 // 0-based
			m -= m % n
			return time.Date(t.Year(), time.Month(m+1), 1, 0, 0, 0, 0, time.UTC)
		},
		next: func(s time.Time) time.Time { return s.UTC().AddDate(0, n, 0) },
	}
}

// MonthGrain is MonthsGrain(name, 1).
func MonthGrain(name string) Grain { return MonthsGrain(name, 1) }

// Truncate returns the bucket start containing t at this grain, in UTC.
func (g Grain) Truncate(t time.Time) time.Time { return g.truncate(t.UTC()) }

// Next returns the start of the bucket after the one starting at s.
func (g Grain) Next(s time.Time) time.Time { return g.next(s.UTC()) }

// Hierarchy is a single-parent TREE of grains, finest at the leaves.
// Each grain has at most one parent, and a grain may have several
// children — the shape ts rollups have always had (one source may feed
// several destinations; a destination has exactly one source). A linear
// chain is the degenerate case of one child per node.
//
// Nesting requirement, which is what makes the homomorphism exact:
// every parent bucket must begin on a child bucket boundary and span a
// whole number of child buckets, so a parent equals the fold of its
// children and cascading combine loses nothing. This is checked
// structurally (by probing boundaries) rather than by duration modulus,
// so calendar grains qualify: months nest in quarters 3:1, quarters in
// years 4:1, days in months (28–31):1.
type Hierarchy struct {
	grains []Grain
	// parent[i] is the index of i's parent — the next FINER grain that i
	// coarsens. The ROOT is the finest grain (hour), and the tree fans
	// OUT toward coarser grains: day is hour's child; week and month are
	// both day's children. Single-parent means each grain coarsens
	// exactly one finer grain; fan-out means one grain may be coarsened
	// by several (week and month both coarsen day).
	parent []int
}

// NewHierarchy builds a LINEAR hierarchy: grains ordered finest→coarsest,
// each the parent of the previous. Retained as the common case and for
// back-compatibility; NewTreeHierarchy expresses fan-out.
func NewHierarchy(grains ...Grain) (*Hierarchy, error) {
	if len(grains) == 0 {
		return nil, fmt.Errorf("chronicle: hierarchy needs at least one grain")
	}
	// Grains are given finest-first; the finest is the ROOT and each
	// subsequent (coarser) grain is a child of the one before it.
	parent := make([]int, len(grains))
	for i := range grains {
		if i == 0 {
			parent[i] = -1 // finest is the root
		} else {
			parent[i] = i - 1
		}
	}
	return newHierarchy(grains, parent)
}

// TreeSpec declares one grain and the name of its parent ("" for the
// root). Order is free; the constructor resolves names.
type TreeSpec struct {
	Grain  Grain
	Parent string // parent grain's Name, or "" if this is the root
}

// NewTreeHierarchy builds a single-parent tree of grains — the shape ts
// rollups use. Exactly one grain may be the root; every other grain
// names its parent. Fan-out is permitted (day may parent BOTH week and
// month); fan-in is not (a grain has one parent), and cycles are
// rejected.
//
// Grains are stored finest-first by tree depth so that level indices
// remain stable and BucketKey needs no change.
func NewTreeHierarchy(specs ...TreeSpec) (*Hierarchy, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("chronicle: hierarchy needs at least one grain")
	}
	byName := map[string]int{}
	for i, s := range specs {
		if s.Grain.Name == "" {
			return nil, fmt.Errorf("chronicle: grain at position %d has no name", i)
		}
		if _, dup := byName[s.Grain.Name]; dup {
			return nil, fmt.Errorf("chronicle: duplicate grain name %q", s.Grain.Name)
		}
		byName[s.Grain.Name] = i
	}

	grains := make([]Grain, len(specs))
	parent := make([]int, len(specs))
	roots := 0
	for i, s := range specs {
		grains[i] = s.Grain
		if s.Parent == "" {
			parent[i] = -1
			roots++
			continue
		}
		p, ok := byName[s.Parent]
		if !ok {
			return nil, fmt.Errorf("chronicle: grain %q names unknown parent %q", s.Grain.Name, s.Parent)
		}
		if p == i {
			return nil, fmt.Errorf("chronicle: grain %q is its own parent", s.Grain.Name)
		}
		parent[i] = p
	}
	if roots != 1 {
		return nil, fmt.Errorf("chronicle: hierarchy needs exactly one root grain, found %d", roots)
	}

	// Cycle check: walking upward from every grain must terminate.
	for i := range grains {
		seen := map[int]bool{i: true}
		for cur := parent[i]; cur != -1; cur = parent[cur] {
			if seen[cur] {
				return nil, fmt.Errorf("chronicle: cycle in hierarchy at grain %q", grains[i].Name)
			}
			seen[cur] = true
		}
	}
	return newHierarchy(grains, parent)
}

// newHierarchy validates nesting structurally and constructs.
func newHierarchy(grains []Grain, parent []int) (*Hierarchy, error) {
	for i, g := range grains {
		if g.truncate == nil || g.next == nil {
			return nil, fmt.Errorf(
				"chronicle: grain %q is not constructed — use FixedGrain or MonthsGrain", g.Name)
		}
		if p := parent[i]; p != -1 {
			// grains[i] coarsens its parent grains[p]: the finer grain
			// (the parent) must tile the coarser one (i) exactly.
			if err := checkNesting(grains[p], grains[i]); err != nil {
				return nil, err
			}
		}
	}
	return &Hierarchy{grains: grains, parent: parent}, nil
}

// nestingProbes are the instants at which nesting is verified. They span
// leap years, month-length variation, and a year boundary — the cases
// where calendar nesting could fail if grains were mismatched.
var nestingProbes = []time.Time{
	time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),   // leap year start
	time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC), // leap day
	time.Date(2025, 3, 15, 7, 30, 0, 0, time.UTC),
	time.Date(2025, 12, 31, 23, 0, 0, 0, time.UTC), // year boundary
	time.Date(2026, 7, 21, 9, 15, 0, 0, time.UTC),
}

// checkNesting verifies that child buckets tile parent buckets exactly:
// a parent bucket must start on a child boundary, and stepping the child
// forward must land exactly on the parent's end. Structural, so it holds
// for fixed and calendar grains alike.
func checkNesting(child, par Grain) error {
	for _, probe := range nestingProbes {
		pStart := par.Truncate(probe)
		pEnd := par.Next(pStart)

		// The parent's start must itself be a child boundary.
		if !child.Truncate(pStart).Equal(pStart) {
			return fmt.Errorf(
				"chronicle: grain %q does not nest in %q — parent bucket start %s is not a %q boundary",
				child.Name, par.Name, pStart.Format(time.RFC3339), child.Name)
		}
		// Stepping child buckets from the parent's start must land
		// exactly on the parent's end (a whole number of children).
		cur := pStart
		steps := 0
		for cur.Before(pEnd) {
			cur = child.Next(cur)
			steps++
			if steps > 100000 {
				return fmt.Errorf(
					"chronicle: grain %q does not nest in %q — child too fine to tile the parent",
					child.Name, par.Name)
			}
		}
		if !cur.Equal(pEnd) {
			return fmt.Errorf(
				"chronicle: grain %q does not nest in %q — %d child buckets overshoot parent end %s",
				child.Name, par.Name, steps, pEnd.Format(time.RFC3339))
		}
		if steps < 1 {
			return fmt.Errorf(
				"chronicle: grain %q must be strictly finer than %q", child.Name, par.Name)
		}
	}
	return nil
}

// Levels returns the number of grains.
func (h *Hierarchy) Levels() int { return len(h.grains) }

// Grain returns the grain at level i.
func (h *Hierarchy) Grain(i int) Grain { return h.grains[i] }

// Parent returns the level of i's parent, or -1 if i is the root.
func (h *Hierarchy) Parent(i int) int { return h.parent[i] }

// Root returns the level of the FINEST grain — the tree's root, from
// which coarser grains fan out.
func (h *Hierarchy) Root() int {
	for i, p := range h.parent {
		if p == -1 {
			return i
		}
	}
	return len(h.grains) - 1 // unreachable for a validated hierarchy
}

// Children returns the levels whose parent is i, ascending.
func (h *Hierarchy) Children(i int) []int {
	var out []int
	for j, p := range h.parent {
		if p == i {
			out = append(out, j)
		}
	}
	return out
}

// Leaves returns the levels with no children — the COARSEST grain on
// each branch (year on the month branch, week on the week branch). A
// fold starts at whichever leaf best tiles the requested window and
// descends toward the root.
func (h *Hierarchy) Leaves() []int {
	var out []int
	for i := range h.grains {
		if len(h.Children(i)) == 0 {
			out = append(out, i)
		}
	}
	return out
}

// CoarsestLeafFor returns the leaf whose buckets are coarsest among
// those that still fit inside [from, to) — the best starting point for
// a fold. Falls back to the root when no leaf bucket fits.
func (h *Hierarchy) CoarsestLeafFor(from, to time.Time) int {
	best := h.Root()
	var bestSpan time.Duration
	for _, l := range h.Leaves() {
		g := h.grains[l]
		s := g.Truncate(from)
		if s.Before(from) {
			s = g.Next(s)
		}
		if !g.Next(s).After(to) { // at least one whole bucket fits
			span := g.Next(s).Sub(s)
			if span > bestSpan {
				best, bestSpan = l, span
			}
		}
	}
	return best
}
