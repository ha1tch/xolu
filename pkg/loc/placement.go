// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
)

// metresPerDegreeLat is the standard flat-Earth approximation constant
// (loc-00-design.md §4e: adequate precision at facility and city
// scale, not intended for continental scale). metresPerDegreeLon
// varies with latitude — computed per call, not a constant.
const metresPerDegreeLat = 111320.0

// safeLongitudeDelta converts a local east/west metre offset to a
// longitude delta, guarding the genuine near-pole singularity that an
// exact "!= 0" check on cos(lat) misses entirely: math.Cos(90 *
// math.Pi / 180) is 6.12e-17, not exactly 0.0, so an exact-zero guard
// lets the division through with an almost-zero denominator —
// confirmed directly (a 1km offset at lat=90 produced a "valid"
// float64 longitude delta of ~1.47e14 degrees before this fix, not a
// panic, silently corrupting whatever stored or compared value it
// fed into).
//
// Clamps the RESULT, not the denominator — an earlier version
// guarded with a fixed epsilon on metresPerDegreeLon (reject anything
// below 1.0 m/degree), which turned out not to bound the result at
// all: go test -fuzz found offset=1000, denom=4.22 (well above that
// threshold) still producing a delta of 236.8, past the +/-180 bound
// the whole point of this function is to guarantee. A fixed
// denominator epsilon can never bound the result for an arbitrary
// offset, since the actual violation condition is
// |offset| > 180*denom, which scales with offset, not a constant.
// Clamping the division's own output is what genuinely guarantees
// the bound for every combination, found by the fuzzer catching what
// hand-reasoning about "the pole case" alone had missed.
func safeLongitudeDelta(offsetMetres, metresPerDegreeLon float64) float64 {
	if offsetMetres == 0 {
		return 0
	}
	delta := offsetMetres / metresPerDegreeLon
	if delta > 180 {
		return 180
	}
	if delta < -180 {
		return -180
	}
	return delta
}

// AbsolutePosition is a location's fully-resolved real-world position:
// the placement chain composed from a location up to its nearest
// georeferenced ancestor, then converted from that ancestor's local
// frame into WGS84 lat/lon/alt plus an absolute heading.
type AbsolutePosition struct {
	Lat, Lon, Alt float64
	Heading       float64 // radians, absolute (TrueNorth + composed local rotation)
}

// Convention, stated once here rather than re-derived at each call
// site: a node's local (OffsetX, OffsetY) is expressed in its own
// parent's frame, where the parent's local +X axis is "east-ish" and
// +Y is "north-ish" before any TrueNorth correction, and Rotation is a
// standard counter-clockwise angle (radians) from that local +X axis.
// GeoAnchor.TrueNorth is the counter-clockwise angle from true
// geographic east to the anchored node's own local +X axis. This
// convention is internal to this package — nothing on the wire surface
// (Stage 6) needs to know it, only that composition is deterministic
// and invertible, which the Stage 1 test suite proves directly.
func composeLocalChain(chain []Placement) (x, y, z, rot float64) {
	for _, p := range chain {
		dx := p.OffsetX*math.Cos(rot) - p.OffsetY*math.Sin(rot)
		dy := p.OffsetX*math.Sin(rot) + p.OffsetY*math.Cos(rot)
		x += dx
		y += dy
		z += p.OffsetZ
		rot += p.Rotation
	}
	return x, y, z, rot
}

// ComposeAbsolutePosition resolves a location's absolute real-world
// position by walking ParentKey from locationID up to the nearest
// ancestor carrying a non-nil Anchor, composing every hop's Placement
// along the way (loc-00-design.md §3b), then converting the composed
// local offset into WGS84 via the flat-Earth approximation §4e already
// accepts at this scale.
//
// The walk is guaranteed to terminate at an anchor, never at a nil
// ParentKey with no anchor, because every root is required to carry
// one (XOLU-LOC010, enforced at Def/Patch time) — a malformed tree
// reaching a nil-parent node with no anchor is an invariant violation,
// reported as an error here rather than assumed impossible and left to
// panic.
func (s *Store) ComposeAbsolutePosition(ctx context.Context, locationID string) (AbsolutePosition, error) {
	loc, err := s.Get(ctx, locationID)
	if err != nil {
		return AbsolutePosition{}, err
	}

	// Walk root-ward, collecting each hop's Placement, nearest first.
	var chainReversed []Placement
	cur := loc
	for {
		chainReversed = append(chainReversed, cur.Placement)
		if cur.Anchor != nil {
			break
		}
		if cur.ParentKey == nil {
			return AbsolutePosition{}, fmt.Errorf(
				"invariant violation: location %q has no anchor and no parent — XOLU-LOC010 should have refused this at write time", cur.ID)
		}
		parent, err := s.getByKey(ctx, *cur.ParentKey)
		if err != nil {
			return AbsolutePosition{}, err
		}
		cur = parent
	}
	anchor := cur.Anchor

	// chainReversed is leaf-to-anchor; reverse to anchor-to-leaf for
	// top-down composition, dropping the anchor node's own Placement
	// (its OffsetX/Y/Z/Rotation are meaningless — GeoAnchor is where
	// the chain starts, not another hop within it).
	chain := make([]Placement, 0, len(chainReversed)-1)
	for i := len(chainReversed) - 2; i >= 0; i-- {
		chain = append(chain, chainReversed[i])
	}

	localX, localY, localZ, localRot := composeLocalChain(chain)

	// Rotate the composed local offset by TrueNorth to align with true
	// east/north, then convert metres to degrees.
	trueX := localX*math.Cos(anchor.TrueNorth) - localY*math.Sin(anchor.TrueNorth)
	trueY := localX*math.Sin(anchor.TrueNorth) + localY*math.Cos(anchor.TrueNorth)

	metresPerDegreeLon := metresPerDegreeLat * math.Cos(anchor.Lat*math.Pi/180.0)
	deltaLon := safeLongitudeDelta(trueX, metresPerDegreeLon)
	deltaLat := trueY / metresPerDegreeLat

	return AbsolutePosition{
		Lat:     anchor.Lat + deltaLat,
		Lon:     anchor.Lon + deltaLon,
		Alt:     anchor.Alt + localZ,
		Heading: anchor.TrueNorth + localRot,
	}, nil
}

// mixedAnchorDistanceThresholdMeters: T-132, loc-00-design.md §3b's
// documented BIM caution against mixing real-world references within
// one tree. A fixed distance, not derived from any physical bound —
// chosen conservatively against real facility-tree scale: 500km
// comfortably covers a single country's or region's own facilities
// (the ordinary case for this primitive's stated customer shape,
// per loc-00-design.md §1/§2), while still surfacing an advisory
// warning for a tree that spans further, prompting a caller to
// confirm intent rather than silently accepting a likely data-entry
// mistake. Never a hard refusal either way (§3b's own rule) — a
// genuinely multi-continent tenant just gets an advisory it can
// ignore, at a low false-positive cost precisely because nothing here
// blocks the write.
const mixedAnchorDistanceThresholdMeters = 500000.0

// nearestAncestorAnchor walks up from parentKey (the node whose own
// anchor is being checked is deliberately excluded — this looks at
// what came before it in the tree, not at itself) to the nearest
// ancestor carrying an Anchor. Returns nil, nil if none — the
// ordinary, expected shape for a tree with exactly one anchored root
// and nothing to warn about.
func (s *Store) nearestAncestorAnchor(ctx context.Context, parentKey *LocationKey) (*GeoAnchor, error) {
	for parentKey != nil {
		parent, err := s.getByKey(ctx, *parentKey)
		if err != nil {
			return nil, err
		}
		if parent.Anchor != nil {
			return parent.Anchor, nil
		}
		parentKey = parent.ParentKey
	}
	return nil, nil
}

// MixedAnchorWarning checks a location's own newly-set anchor against
// the nearest already-anchored ancestor, per loc-01-rest-api.md §1's
// own warnings field (T-132). Empty string, nil error means nothing
// to warn about — either no anchor was set, or no ancestor has one to
// compare against, or the distance is within the plausible-single-
// tree threshold.
func (s *Store) MixedAnchorWarning(ctx context.Context, parentKey *LocationKey, newAnchor *GeoAnchor) (string, error) {
	if newAnchor == nil {
		return "", nil
	}
	ancestorAnchor, err := s.nearestAncestorAnchor(ctx, parentKey)
	if err != nil {
		return "", err
	}
	if ancestorAnchor == nil {
		return "", nil
	}
	d := haversineMeters(newAnchor.Lat, newAnchor.Lon, ancestorAnchor.Lat, ancestorAnchor.Lon)
	if d <= mixedAnchorDistanceThresholdMeters {
		return "", nil
	}
	return fmt.Sprintf(
		"anchor is %.0fkm from the nearest already-anchored ancestor -- mixing real-world references within one tree is legal but worth confirming intentional (loc-00-design.md §3b)",
		d/1000), nil
}

// getByKey is Get's internal-key counterpart, used only by the
// placement-chain walk (which has a LocationKey from a parent
// reference, not an external id). Not exported: no caller outside
// this package should ever need to address a location by its internal
// key, per the two-identity split (loc-00-design.md §11a).
func (s *Store) getByKey(ctx context.Context, key LocationKey) (*Location, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+locationColumns+` FROM `+s.locationsTable()+` WHERE location_key = ?`, uint32(key))
	loc, err := scanLocation(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("invariant violation: parent location_key %d referenced but not found", key)
		}
		return nil, err
	}
	return loc, nil
}
