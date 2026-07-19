// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package xolutime secures xolu's single time invariant: every persisted moment is
// an absolute UTC instant, and nothing else.
//
// The package is deliberately thin. It enforces a discipline; it does not own a
// policy. It has no concept of recurrence, no concept of a stored time zone, and
// no display formatting beyond a helper that *requires* the caller to name the
// zone explicitly. Time zones, daylight-saving arithmetic, recurrence expansion,
// and "wall-clock intention" all live above the storage primitives, never here.
//
// The rule, stated once for all of xolu:
//
//   - A persisted timestamp is an absolute instant, stored as UTC.
//   - The only sanctioned source of "now" for a persisted value is xolutime.Now.
//   - Wall-clock-plus-zone, if a layer needs it, is a separate explicit field
//     that layer owns. The storage primitive sees the instant only.
//   - Human-facing formatting always takes an explicit zone. There is no
//     implicit session/server zone anywhere in xolu.
//
// xolutime does NOT govern duration measurement. A `start := time.Now()` used only
// to compute elapsed time via time.Since is correct as written and must keep Go's
// monotonic clock reading — calling .UTC() on it would strip the monotonic
// component and corrupt the measurement. Use xolutime for values you store or
// serialize; use the standard library directly for stopwatches.
//
// Conventionally imported with the alias `ot`:
//
//	import ot "github.com/ha1tch/xolu/pkg/xolutime"
package xolutime

import (
	"fmt"
	"time"
)

// Instant is an absolute point in time, guaranteed to carry the UTC location.
//
// It is a thin newtype over time.Time rather than an opaque struct, so existing
// code and the standard library interoperate without friction: an Instant is a
// time.Time and can be passed anywhere one is accepted. The type exists to make
// the UTC guarantee visible in signatures and to give the constructors a single
// place to enforce it. Every value produced by this package is in UTC; the
// constructors are the only sanctioned way to mint one.
type Instant struct {
	t time.Time
}

// Now returns the current moment as a UTC Instant.
//
// This is the only sanctioned source of "now" for any value that will be
// persisted or serialized. It deliberately drops the monotonic clock reading
// (via .UTC()); do not use it to measure elapsed time — use time.Now and
// time.Since directly for that.
func Now() Instant {
	return Instant{t: time.Now().UTC()}
}

// FromTime converts an arbitrary time.Time into a UTC Instant.
//
// The wall-clock instant is preserved exactly; only the location is normalized
// to UTC. FromTime(t).Time().Equal(t) holds for every t. Use this at the
// boundary where a time.Time of unknown provenance enters xolu storage.
func FromTime(t time.Time) Instant {
	return Instant{t: t.UTC()}
}

// FromUnixNano reconstructs a UTC Instant from a nanosecond count since the Unix
// epoch. This mirrors the timeseries codec's decode path
// (time.Unix(0, ns).UTC()) so that ts keys and xolutime agree byte-for-byte on
// what an instant is.
func FromUnixNano(ns int64) Instant {
	return Instant{t: time.Unix(0, ns).UTC()}
}

// Zero returns the zero Instant (the Unix epoch in UTC), useful as an explicit
// "unset" sentinel. Note this is the epoch, not Go's time.Time zero value; xolu
// stores instants as epoch-relative nanoseconds and has no representation for
// year-1 timestamps.
func Zero() Instant {
	return Instant{t: time.Unix(0, 0).UTC()}
}

// Time returns the underlying time.Time, always in UTC. Use this to hand the
// instant to code that expects a standard library time.Time.
func (i Instant) Time() time.Time { return i.t }

// UnixNano returns the instant as nanoseconds since the Unix epoch — the exact
// integer the timeseries codec stores as a big-endian uint64 key suffix.
func (i Instant) UnixNano() int64 { return i.t.UnixNano() }

// IsZero reports whether the instant is the Unix epoch (xolutime's "unset"
// sentinel), or Go's time.Time zero value. Either counts as unset.
func (i Instant) IsZero() bool {
	return i.t.IsZero() || i.t.Equal(time.Unix(0, 0).UTC())
}

// Before, After, Equal mirror time.Time. Because both operands are UTC, these
// compare absolute instants with no zone ambiguity.
func (i Instant) Before(o Instant) bool { return i.t.Before(o.t) }
func (i Instant) After(o Instant) bool  { return i.t.After(o.t) }
func (i Instant) Equal(o Instant) bool  { return i.t.Equal(o.t) }

// Add returns the instant shifted by d, still in UTC. This is plain instant
// arithmetic (d is an absolute duration); it is NOT calendar arithmetic and
// knows nothing of daylight saving. "Add one day" across a DST boundary is a
// calendar operation that belongs above xolutime, not here.
func (i Instant) Add(d time.Duration) Instant {
	return Instant{t: i.t.Add(d).UTC()}
}

// Sub returns the duration i - o. Both are UTC, so the result is the true
// elapsed absolute duration between the two instants.
func (i Instant) Sub(o Instant) time.Duration { return i.t.Sub(o.t) }

// String renders the instant in RFC 3339 with nanoseconds, always with the Z
// (UTC) designator. This is for logs and debugging, where UTC is the right and
// unambiguous choice. For human-facing display in a particular zone, use Format
// with an explicit location.
func (i Instant) String() string { return i.t.Format(time.RFC3339Nano) }

// Format renders the instant for human display in an explicitly named zone.
//
// There is intentionally no zone-less Format: xolu never formats a stored instant
// against an implicit session or server zone (the PostgreSQL timestamptz
// footgun). The caller must say which zone the human is in. The instant itself
// is unchanged; only its presentation is localized.
func (i Instant) Format(layout string, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	return i.t.In(loc).Format(layout)
}

// MarshalJSON emits the instant as an RFC 3339 / ISO 8601 string with an
// explicit Z, so a persisted or wire-transmitted instant is unambiguous and
// round-trips through UnmarshalJSON.
func (i Instant) MarshalJSON() ([]byte, error) {
	return []byte(`"` + i.t.Format(time.RFC3339Nano) + `"`), nil
}

// UnmarshalJSON parses an RFC 3339 string into a UTC Instant, applying the same
// no-naive-input rule as Parse: a string lacking an explicit offset or Z is
// rejected, because a zone-naive timestamp is not an absolute instant.
func (i *Instant) UnmarshalJSON(b []byte) error {
	if len(b) < 2 || b[0] != '"' || b[len(b)-1] != '"' {
		return fmt.Errorf("xolutime: Instant must be a JSON string, got %q", string(b))
	}
	inst, err := Parse(string(b[1 : len(b)-1]))
	if err != nil {
		return err
	}
	*i = inst
	return nil
}
