// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

import (
	"testing"
	"time"
)

// TestCodec_TimelineIDBeyondUint16 is the regression witness for the
// wave-1 per-primitive ID widening (@P wave 1).
//
// Before the widening, TimelineID was uint16 and the codec packed it
// into a 2-byte big-endian prefix. Any TimelineID above 0xFFFF passed
// to EncodeKey would have been silently truncated by the uint16(tid)
// cast — a latent data-corruption bug that no test caught because no
// test used a value above 0xFFFF.
//
// This test walks a set of TimelineIDs deliberately chosen to expose
// truncation failures: values whose low 16 bits are the same but whose
// high 16 bits differ. If the codec accidentally reverts to the
// 2-byte layout, these round-trips produce collisions or misdecodes.
func TestCodec_TimelineIDBeyondUint16(t *testing.T) {
	// Values chosen so that low-16 collisions are catastrophic if the
	// codec is narrow: 0x00010001, 0x00020001, 0x00030001 all have
	// low 16 bits = 0x0001.
	testIDs := []TimelineID{
		0x00000001, // classic in-range
		0x0000FFFF, // old MaxTimelineID
		0x00010000, // first value beyond the old cap — the CI marker
		0x00010001, // low-16 collides with 0x00000001
		0x00020001, // low-16 collides with 0x00010001
		0x0FFFFFFF, // 256M
		0xFFFFFFFF, // MaxTimelineID
	}

	ts := time.Unix(1_000_000, 0).UTC()
	dv := []uint64{42, 100}

	for _, tid := range testIDs {
		t.Run("", func(t *testing.T) {
			key, err := EncodeKey(tid, 2, dv, ts)
			if err != nil {
				t.Fatalf("EncodeKey(%#x): %v", tid, err)
			}
			// Key layout: [4B tid][8B d0][8B d1][8B ts] = 28 bytes
			if got := len(key); got != 28 {
				t.Fatalf("EncodeKey(%#x): key len %d, want 28 (per @P wave 1 layout)", tid, got)
			}
			got, gotDV, gotTS, err := DecodeKey(key, 2)
			if err != nil {
				t.Fatalf("DecodeKey(%#x): %v", tid, err)
			}
			if got != tid {
				t.Fatalf("round-trip: got tid=%#x, want %#x — codec truncated?", got, tid)
			}
			if gotDV[0] != dv[0] || gotDV[1] != dv[1] {
				t.Errorf("dimension corruption: got %v, want %v", gotDV, dv)
			}
			if !gotTS.Equal(ts) {
				t.Errorf("timestamp corruption: got %v, want %v", gotTS, ts)
			}
		})
	}
}

// TestCodec_PrefixKeyBeyondUint16 covers the same property for
// EncodePrefixKey (used by range scans), which had its own PutUint16
// call before the widening.
func TestCodec_PrefixKeyBeyondUint16(t *testing.T) {
	tid := TimelineID(0x00010000)
	prefix := EncodePrefixKey(tid, []uint64{7})
	// Layout: [4B tid][8B d0] = 12 bytes
	if got := len(prefix); got != 12 {
		t.Fatalf("EncodePrefixKey(%#x): len %d, want 12", tid, got)
	}
	// The 4-byte tid should be exactly 0x00, 0x01, 0x00, 0x00 big-endian.
	want := [4]byte{0x00, 0x01, 0x00, 0x00}
	for i, b := range want {
		if prefix[i] != b {
			t.Errorf("prefix[%d] = 0x%02x, want 0x%02x — pre-widening 2-byte codec?", i, prefix[i], b)
		}
	}
}
