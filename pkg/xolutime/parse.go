// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package xolutime

import (
	"fmt"
	"strings"
	"time"
)

// Parse converts an RFC 3339 timestamp string into a UTC Instant, rejecting any
// input that does not carry an explicit time zone.
//
// This is the input-boundary guard for the whole system. A string like
// "2026-07-08T09:00:00" names a wall-clock reading with no reference point — it
// is not an absolute instant, and there is no honest way to store it without
// silently assuming a zone (the PostgreSQL "timestamp without time zone"
// footgun, where the value's meaning shifts with whatever zone happens to be
// configured). xolu refuses to guess: callers must state the offset (e.g.
// "2026-07-08T09:00:00-03:00") or "Z" for UTC.
//
// The wall-clock intention behind a naive string — "9am, in the user's zone,
// recurring" — is real and legitimate, but it belongs to the layer that owns the
// user's zone, which resolves it to an absolute instant before handing it to
// storage. That resolution is not xolutime's job; refusing the naive input is.
func Parse(s string) (Instant, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Instant{}, fmt.Errorf("xolutime: empty timestamp")
	}
	if !hasExplicitZone(s) {
		return Instant{}, fmt.Errorf(
			"xolutime: %q lacks an explicit time zone; "+
				"supply an offset (e.g. -03:00) or Z — xolu never assumes a zone", s)
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		// Fall back to the second-resolution layout for a clearer error on
		// otherwise-valid input; time.RFC3339Nano accepts both, so an error here
		// is a genuine format problem.
		if _, err2 := time.Parse(time.RFC3339, s); err2 != nil {
			return Instant{}, fmt.Errorf("xolutime: parse %q: %w", s, err)
		}
		// Unreachable in practice (RFC3339Nano is a superset), kept for safety.
		return Instant{}, fmt.Errorf("xolutime: parse %q: %w", s, err)
	}
	return Instant{t: t.UTC()}, nil
}

// MustParse is Parse that panics on error. Use only for compile-time-constant
// literals in tests and initialization, never on external input.
func MustParse(s string) Instant {
	i, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return i
}

// hasExplicitZone reports whether an RFC 3339 string carries a zone designator:
// a trailing Z, or a +hh:mm / -hh:mm offset on the time portion.
//
// The offset check looks only after the 'T' so that the date's own hyphens
// ("2026-07-08") are never mistaken for a negative offset.
func hasExplicitZone(s string) bool {
	if strings.HasSuffix(s, "Z") || strings.HasSuffix(s, "z") {
		return true
	}
	tIdx := strings.IndexAny(s, "Tt")
	if tIdx < 0 {
		// No time portion at all (a bare date): definitionally zone-naive.
		return false
	}
	timePart := s[tIdx+1:]
	// A zone offset is a + or - in the time portion. Guard against an empty
	// time part.
	return strings.ContainsAny(timePart, "+-")
}
