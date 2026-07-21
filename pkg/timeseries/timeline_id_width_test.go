// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

import (
	"math"
	"testing"
)

// TestTimelineIDFromJSON_FullRange is the regression guard for the 32-bit
// build/truncation bug class (fixed 2026-07-20). A TimelineID is uint32;
// the CI break was `int(MaxTimelineID)` overflowing int on 32-bit targets,
// and the latent sibling was uint32 ids held in platform-dependent int or
// truncating uint16 fields at the JSON boundary. TimelineIDFromJSON is the
// single sanctioned crossing; this test pins that it accepts the entire
// uint32 range — including values above 2^31 that a 32-bit int cannot
// hold — and rejects out-of-range input.
func TestTimelineIDFromJSON_FullRange(t *testing.T) {
	valid := []struct {
		name string
		in   int64
		want TimelineID
	}{
		{"one", 1, 1},
		{"small", 42, 42},
		{"uint16 max", 0xFFFF, 0xFFFF},
		{"just above uint16", 0x10000, 0x10000},          // the id class the old uint16 fields truncated
		{"above int32 max", math.MaxInt32 + 1, 0x80000000}, // the id class a 32-bit int cannot hold
		{"near ceiling", 0xFFFFFFFE, 0xFFFFFFFE},
		{"ceiling (MaxTimelineID)", 0xFFFFFFFF, MaxTimelineID},
	}
	for _, c := range valid {
		got, err := TimelineIDFromJSON(c.in)
		if err != nil {
			t.Errorf("%s: TimelineIDFromJSON(%d) unexpected error: %v", c.name, c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: TimelineIDFromJSON(%d) = 0x%08X, want 0x%08X — truncation?",
				c.name, c.in, uint32(got), uint32(c.want))
		}
	}

	invalid := []struct {
		name string
		in   int64
	}{
		{"zero (reserved)", 0},
		{"negative", -1},
		{"one past ceiling", 0x100000000},          // MaxTimelineID + 1
		{"far above ceiling", math.MaxInt64},        // a huge JSON number
	}
	for _, c := range invalid {
		if _, err := TimelineIDFromJSON(c.in); err == nil {
			t.Errorf("%s: TimelineIDFromJSON(%d) should error, but succeeded", c.name, c.in)
		}
	}
}

// TestMaxTimelineID_FitsUint32 pins that the ceiling constant is exactly
// the uint32 maximum. The CI break came from converting this to int; the
// constant itself must remain a full uint32.
func TestMaxTimelineID_FitsUint32(t *testing.T) {
	if uint32(MaxTimelineID) != math.MaxUint32 {
		t.Errorf("MaxTimelineID = 0x%08X, want 0xFFFFFFFF", uint32(MaxTimelineID))
	}
	// Round-trip through int64 (the JSON carrier width) must be lossless —
	// this is the conversion path every handler now uses.
	if got := int64(MaxTimelineID); got != 0xFFFFFFFF {
		t.Errorf("int64(MaxTimelineID) = %d, want 4294967295", got)
	}
	// And TimelineID(int64(max)) must survive the round trip.
	if TimelineID(int64(MaxTimelineID)) != MaxTimelineID {
		t.Error("MaxTimelineID does not survive int64 round-trip")
	}
}
