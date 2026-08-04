// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// anchor_journal_test.go — T-128 (wave 9b): PATCH changing a
// location's anchor appends one loc_journal entry (kind='anchor'),
// closing loc-00-design.md §5b's residual. Mirrors §8a's no-op-
// writes-nothing discipline exactly: no real anchor change, no entry.

package loc

import (
	"context"
	"testing"
)

// anchorJournalCount returns how many loc_journal rows exist for the
// given location_id with kind='anchor' — the direct, store-level
// proof this item's exit criteria call for.
func anchorJournalCount(t *testing.T, s *Store, locationID string) int {
	t.Helper()
	var n int
	if err := s.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM loc_journal WHERE subject_ref = ? AND kind = 'anchor'`,
		locationID).Scan(&n); err != nil {
		t.Fatalf("count anchor journal entries: %v", err)
	}
	return n
}

// TestPatch_AnchorChange_AppendsOneJournalEntry is the direct proof:
// changing anchor_lat/lon/alt/true_north via PATCH produces exactly
// one loc_journal row, recoverable with the new anchor's position.
func TestPatch_AnchorChange_AppendsOneJournalEntry(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	mustDef(t, s, LocationDef{
		ID: "site-a", ParentID: nil, Name: "Site A", Postable: false,
		Placement: Placement{Anchor: &GeoAnchor{Lat: -34.90, Lon: -56.16, Alt: 10, TrueNorth: 0}},
	})

	if anchorJournalCount(t, s, "site-a") != 0 {
		t.Fatalf("before any patch: want 0 anchor journal entries")
	}

	newAnchor := Placement{Anchor: &GeoAnchor{Lat: -34.95, Lon: -56.20, Alt: 12, TrueNorth: 0}}
	if err := s.Patch(ctx, "site-a", PatchParams{Placement: &newAnchor}); err != nil {
		t.Fatalf("Patch (anchor change): %v", err)
	}

	if got := anchorJournalCount(t, s, "site-a"); got != 1 {
		t.Fatalf("after one real anchor change: want 1 journal entry, got %d", got)
	}

	// The entry must carry the NEW anchor's position, not the old one
	// or nothing at all — the historically useful part named in §5b.
	var lat, lon, alt float64
	if err := s.DB().QueryRowContext(ctx,
		`SELECT report_lat, report_lon, report_alt FROM loc_journal
		 WHERE subject_ref = ? AND kind = 'anchor'`, "site-a").Scan(&lat, &lon, &alt); err != nil {
		t.Fatalf("read anchor journal entry: %v", err)
	}
	if !approxEqual(lat, -34.95) || !approxEqual(lon, -56.20) || !approxEqual(alt, 12) {
		t.Errorf("journal entry does not carry the new anchor: got lat=%v lon=%v alt=%v", lat, lon, alt)
	}

	// A location fetched after the patch reflects the new anchor too —
	// the journal entry is additive, not a substitute for the live row.
	got, err := s.Get(ctx, "site-a")
	if err != nil {
		t.Fatalf("Get after anchor patch: %v", err)
	}
	if got.Anchor == nil || !approxEqual(got.Anchor.Lat, -34.95) {
		t.Errorf("live location row not updated: %+v", got.Placement)
	}
}

// TestPatch_AnchorUnchanged_WritesNothing mirrors §8a directly: PATCH
// re-sending the identical anchor values must not produce a journal
// entry — a no-op stays a no-op, exactly as a repeated identical
// report already does for subject movement.
func TestPatch_AnchorUnchanged_WritesNothing(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	anchor := GeoAnchor{Lat: -34.90, Lon: -56.16, Alt: 10, TrueNorth: 0}
	mustDef(t, s, LocationDef{
		ID: "site-b", ParentID: nil, Name: "Site B", Postable: false,
		Placement: Placement{Anchor: &anchor},
	})

	// Re-send the identical placement (same offsets, same anchor).
	same := Placement{Anchor: &GeoAnchor{Lat: -34.90, Lon: -56.16, Alt: 10, TrueNorth: 0}}
	if err := s.Patch(ctx, "site-b", PatchParams{Placement: &same}); err != nil {
		t.Fatalf("Patch (identical anchor): %v", err)
	}

	if got := anchorJournalCount(t, s, "site-b"); got != 0 {
		t.Fatalf("identical anchor re-sent: want 0 journal entries (no-op), got %d", got)
	}
}

// TestPatch_OffsetOnlyChange_NeverTouchesAnchorJournal proves the
// other half of the same rule: a placement PATCH that changes only
// the relative offset/rotation, while re-sending the SAME anchor,
// must not be mistaken for an anchor change — offset and anchor are
// different facts (relative-transform vs. real-world georeference),
// and only the second is what §5b's journal entry is about.
func TestPatch_OffsetOnlyChange_NeverTouchesAnchorJournal(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	anchor := GeoAnchor{Lat: -34.90, Lon: -56.16, Alt: 10, TrueNorth: 0}
	mustDef(t, s, LocationDef{
		ID: "site-c", ParentID: nil, Name: "Site C", Postable: false,
		Placement: Placement{OffsetX: 0, OffsetY: 0, Anchor: &anchor},
	})

	offsetMoved := Placement{
		OffsetX: 100, OffsetY: 200, // changed
		Anchor: &GeoAnchor{Lat: -34.90, Lon: -56.16, Alt: 10, TrueNorth: 0}, // unchanged
	}
	if err := s.Patch(ctx, "site-c", PatchParams{Placement: &offsetMoved}); err != nil {
		t.Fatalf("Patch (offset-only change): %v", err)
	}

	if got := anchorJournalCount(t, s, "site-c"); got != 0 {
		t.Fatalf("offset-only change with anchor re-sent unchanged: want 0 anchor journal entries, got %d", got)
	}

	got, err := s.Get(ctx, "site-c")
	if err != nil {
		t.Fatalf("Get after offset patch: %v", err)
	}
	if !approxEqual(got.OffsetX, 100) || !approxEqual(got.OffsetY, 200) {
		t.Errorf("offset not actually updated: %+v", got.Placement)
	}
}

// TestPatch_NamePatchAlone_NeverTouchesAnchorJournal covers the
// simplest case explicitly: PatchParams.Placement == nil (a name-only
// patch, exactly TestRoundTrip's own shape) must never reach the
// anchor-comparison logic at all, let alone write a journal entry.
func TestPatch_NamePatchAlone_NeverTouchesAnchorJournal(t *testing.T) {
	ctx := context.Background()
	s := testStore(t)

	mustDef(t, s, LocationDef{
		ID: "site-d", ParentID: nil, Name: "Site D", Postable: false,
		Placement: rootAnchor(),
	})

	newName := "Site D Renamed"
	if err := s.Patch(ctx, "site-d", PatchParams{Name: &newName}); err != nil {
		t.Fatalf("Patch (name only): %v", err)
	}

	if got := anchorJournalCount(t, s, "site-d"); got != 0 {
		t.Fatalf("name-only patch: want 0 anchor journal entries, got %d", got)
	}
}
