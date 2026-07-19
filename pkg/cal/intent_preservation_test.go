// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"testing"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// TestSpanIntentPreservedOffGrid pins the intent-preservation invariant:
// a booking's stored span keeps the caller's exact instants (H1, SQLite
// record), while quanta derivation rounds conservatively outward for the
// bitmap index (H2) — floored start, ceiled end. A user who enters 9:57
// is never shown 9:55; the 5-minute grid is an indexing artefact, not a
// recording constraint.
//
// Historical note: a design-stage "3-bit minute modifier" existed to
// recover the true start minute in a bitmap-centric model. The H1/H2
// split superseded it — the exact instant is the record, and the offset
// from the quantum floor is recoverable by arithmetic at full precision
// for zero stored bits. See the recorded decision in
// docs/KNOWN_ISSUES.md.
func TestSpanIntentPreservedOffGrid(t *testing.T) {
	s := Span{
		Start: ot.MustParse("2026-07-20T09:57:00Z"),
		End:   ot.MustParse("2026-07-20T10:15:00Z"),
	}

	days, err := SpanDays(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 {
		t.Fatalf("expected 1 day, got %d", len(days))
	}

	// Occupancy: exactly quanta 119–122 (09:55, 10:00, 10:05, 10:10).
	var got []int
	for q := 0; q < QuantaPerDay; q++ {
		if days[0].Bits.Test(q) {
			got = append(got, q)
		}
	}
	want := []int{119, 120, 121, 122}
	if len(got) != len(want) {
		t.Fatalf("occupied quanta: want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("occupied quanta: want %v, got %v", want, got)
		}
	}

	// Intent: the span itself is untouched by derivation.
	if s.Start != ot.MustParse("2026-07-20T09:57:00Z") ||
		s.End != ot.MustParse("2026-07-20T10:15:00Z") {
		t.Fatalf("span mutated by derivation: %v .. %v", s.Start, s.End)
	}
}
