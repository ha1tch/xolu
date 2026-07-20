// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

import "testing"

// TestSysmask_IsSystem exercises the predicate across the full width
// range and the boundary ids, per @S §2–§3. The two extremes (0 and 32)
// are the ones most likely to be mis-implemented, so they are covered
// explicitly.
func TestSysmask_IsSystem(t *testing.T) {
	cases := []struct {
		width    SysmaskWidth
		id       TimelineID
		isSystem bool
	}{
		// width 0: nothing is ever system, not even the top of the space.
		{0, 0x00000000, false},
		{0, 0x00000001, false},
		{0, 0x7FFFFFFF, false},
		{0, 0xFFFFFFFF, false},

		// width 8: top byte selects. 0x00 prefix = user.
		{8, 0x00000001, false}, // user timeline 1
		{8, 0x00000002, false}, // user timeline 2
		{8, 0x00FFFFFF, false}, // top of user-space at width 8
		{8, 0x01000000, true},  // first system id
		{8, 0x01000001, true},  // system timeline (top byte 0x01)
		{8, 0x02000001, true},  // system timeline (top byte 0x02)
		{8, 0xFF000001, true},  // system timeline (top byte 0xFF)

		// width 1: only the single top bit selects.
		{1, 0x7FFFFFFF, false}, // top bit clear → user
		{1, 0x80000000, true},  // top bit set → system

		// width 16: top halfword selects.
		{16, 0x0000FFFF, false},
		{16, 0x00010000, true},

		// width 24: only the low byte is user-space.
		{24, 0x000000FF, false},
		{24, 0x00000100, true},

		// width 32: every bit selects; system iff id != 0.
		{32, 0x00000000, false}, // id 0 reserved, classifies as "not system"
		{32, 0x00000001, true},
		{32, 0xFFFFFFFF, true},
	}

	for _, c := range cases {
		got := c.width.IsSystem(c.id)
		if got != c.isSystem {
			t.Errorf("width=%d IsSystem(0x%08X) = %v, want %v",
				uint8(c.width), uint32(c.id), got, c.isSystem)
		}
		// IsUser must always be the exact complement.
		if c.width.IsUser(c.id) == got {
			t.Errorf("width=%d IsUser(0x%08X) not complement of IsSystem",
				uint8(c.width), uint32(c.id))
		}
	}
}

// TestSysmask_MaxUserID checks the user-space ceiling used by the
// allocator's exhaustion guard.
func TestSysmask_MaxUserID(t *testing.T) {
	cases := []struct {
		width SysmaskWidth
		want  TimelineID
	}{
		{0, 0xFFFFFFFF},  // whole space is user
		{1, 0x7FFFFFFF},  // top bit reserved
		{8, 0x00FFFFFF},  // top byte reserved
		{16, 0x0000FFFF}, // top halfword reserved
		{24, 0x000000FF}, // top 24 bits reserved
		{32, 0x00000000}, // system-only; no user ids beyond reserved 0
	}
	for _, c := range cases {
		if got := c.width.MaxUserID(); got != c.want {
			t.Errorf("width=%d MaxUserID() = 0x%08X, want 0x%08X",
				uint8(c.width), uint32(got), uint32(c.want))
		}
		// Consistency: MaxUserID itself must be user (except width 32,
		// where it is id 0, which is "not system" by the reserved rule).
		if c.width.IsSystem(c.want) {
			t.Errorf("width=%d MaxUserID 0x%08X classifies as system",
				uint8(c.width), uint32(c.want))
		}
		// And one past it (if representable) must be system, except width 0.
		if c.width != 0 && c.width != MaxSysmaskWidth {
			if !c.width.IsSystem(c.want + 1) {
				t.Errorf("width=%d id after MaxUserID (0x%08X) should be system",
					uint8(c.width), uint32(c.want+1))
			}
		}
	}
}

// TestSysmask_Valid covers the legal-range check.
func TestSysmask_Valid(t *testing.T) {
	for w := 0; w <= 32; w++ {
		if !SysmaskWidth(w).Valid() {
			t.Errorf("width %d should be valid", w)
		}
	}
	for _, w := range []int{33, 40, 255} {
		if SysmaskWidth(w).Valid() {
			t.Errorf("width %d should be invalid", w)
		}
	}
}
