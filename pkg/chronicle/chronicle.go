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

// Grain is one level of the time hierarchy: a bucket width. Instants
// truncate onto grain-aligned bucket starts in UTC.
type Grain struct {
	Name  string        // e.g. "5m", "hour", "day" — diagnostic only
	Width time.Duration // bucket width; must be > 0
}

// Truncate returns the bucket start containing t at this grain, in UTC.
func (g Grain) Truncate(t time.Time) time.Time {
	return t.UTC().Truncate(g.Width)
}

// Hierarchy is an ordered set of grains, finest first, each coarser
// grain an exact multiple of the previous. The multiple requirement is
// what makes the homomorphism exact: every coarse bucket is the fold of
// a whole number of fine buckets, so cascading combine loses nothing.
type Hierarchy struct {
	grains []Grain
}

// NewHierarchy validates and constructs a hierarchy. Grains must be
// ordered finest→coarsest, each width a positive exact multiple of the
// preceding width.
func NewHierarchy(grains ...Grain) (*Hierarchy, error) {
	if len(grains) == 0 {
		return nil, fmt.Errorf("chronicle: hierarchy needs at least one grain")
	}
	for i, g := range grains {
		if g.Width <= 0 {
			return nil, fmt.Errorf("chronicle: grain %q has non-positive width", g.Name)
		}
		if i > 0 {
			prev := grains[i-1].Width
			if g.Width <= prev || g.Width%prev != 0 {
				return nil, fmt.Errorf(
					"chronicle: grain %q (%v) must be a coarser exact multiple of %q (%v)",
					g.Name, g.Width, grains[i-1].Name, prev)
			}
		}
	}
	return &Hierarchy{grains: grains}, nil
}

// Levels returns the number of grains.
func (h *Hierarchy) Levels() int { return len(h.grains) }

// Grain returns the grain at level i (0 = finest).
func (h *Hierarchy) Grain(i int) Grain { return h.grains[i] }
