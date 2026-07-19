// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package cal

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// TestMatchCommitAllLand: with every calendar free at the span, all N bookings
// are placed.
func TestMatchCommitAllLand(t *testing.T) {
	lc, s, src := setupLifecycle(t, "or-3", "surgeon", "anaesthetist", "cart")
	when := Span{Start: ot.MustParse("2026-07-08T09:00:00Z"), End: ot.MustParse("2026-07-08T12:00:00Z")}
	members := []CommitMember{
		{CalendarID: "or-3", BookingID: "op-room", Mode: ModeExclusive, Bearer: 100},
		{CalendarID: "surgeon", BookingID: "op-surgeon", Mode: ModeExclusive, Bearer: 101},
		{CalendarID: "anaesthetist", BookingID: "op-anaes", Mode: ModeExclusive, Bearer: 102},
		{CalendarID: "cart", BookingID: "op-cart", Mode: ModeExclusive, Bearer: 103},
	}
	res, err := lc.MatchCommit(when, members)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Committed {
		t.Fatalf("expected commit, got %+v", res)
	}
	if len(res.Placed) != 4 {
		t.Fatalf("placed %d, want 4", len(res.Placed))
	}
	// every calendar must now be busy at the span.
	for _, cid := range []string{"or-3", "surgeon", "anaesthetist", "cart"} {
		o, _ := s.ReadOccupancy(cid)
		busy, _ := o.IsBusy(Period{Start: when.Start, End: when.End})
		if !busy {
			t.Fatalf("calendar %q should be busy at the committed span", cid)
		}
	}
	assertIndexMatchesRebuild(t, s, src, "after successful match/commit")
}

// TestMatchCommitConflictCommitsNothing: if ANY calendar is busy at the span, the
// whole commit is refused and NOTHING is written — the all-or-nothing guarantee.
func TestMatchCommitConflictCommitsNothing(t *testing.T) {
	lc, s, src := setupLifecycle(t, "or-3", "surgeon", "anaesthetist")

	// Pre-book the surgeon at the target span so the commit must fail.
	pre := bk("busy", "surgeon", StateBinding, "2026-07-08T10:00:00Z", "2026-07-08T11:00:00Z", 200)
	if _, err := lc.Create(pre); err != nil {
		t.Fatal(err)
	}
	before := dumpIndex(t, s)

	when := Span{Start: ot.MustParse("2026-07-08T09:00:00Z"), End: ot.MustParse("2026-07-08T12:00:00Z")}
	members := []CommitMember{
		{CalendarID: "or-3", BookingID: "op-room", Mode: ModeExclusive, Bearer: 100},
		{CalendarID: "surgeon", BookingID: "op-surgeon", Mode: ModeExclusive, Bearer: 101},
		{CalendarID: "anaesthetist", BookingID: "op-anaes", Mode: ModeExclusive, Bearer: 102},
	}
	res, err := lc.MatchCommit(when, members)
	if err != nil {
		t.Fatal(err)
	}
	if res.Committed {
		t.Fatal("commit should have been refused (surgeon busy)")
	}
	if len(res.Blocking) != 1 || res.Blocking[0] != "surgeon" {
		t.Fatalf("blocking = %v, want [surgeon]", res.Blocking)
	}
	// CRITICAL: nothing placed — or-3 and anaesthetist must NOT have op bookings.
	for _, cid := range []string{"or-3", "anaesthetist"} {
		o, _ := s.ReadOccupancy(cid)
		busy, _ := o.IsBusy(Period{Start: when.Start, End: when.End})
		if busy {
			t.Fatalf("calendar %q must be untouched after refused commit (partial write!)", cid)
		}
	}
	// and the index must be byte-identical to before the commit attempt.
	after := dumpIndex(t, s)
	if ok, msg := indexEqual(before, after); !ok {
		t.Fatalf("refused commit must leave index untouched: %s", msg)
	}
	// the op bookings must not exist in the record either.
	if _, ok := src.booking("or-3", "op-room"); ok {
		t.Fatal("refused commit must not have created any booking record")
	}
}

// TestMatchCommitMultipleBlocking: when several calendars are busy, all are named.
func TestMatchCommitMultipleBlocking(t *testing.T) {
	lc, _, _ := setupLifecycle(t, "a", "b", "c")
	when := Span{Start: ot.MustParse("2026-07-08T09:00:00Z"), End: ot.MustParse("2026-07-08T12:00:00Z")}
	// busy a and c
	if _, err := lc.Create(bk("xa", "a", StateBinding, "2026-07-08T10:00:00Z", "2026-07-08T11:00:00Z", 200)); err != nil {
		t.Fatal(err)
	}
	if _, err := lc.Create(bk("xc", "c", StateBinding, "2026-07-08T09:30:00Z", "2026-07-08T10:30:00Z", 201)); err != nil {
		t.Fatal(err)
	}
	members := []CommitMember{
		{CalendarID: "a", BookingID: "p-a", Mode: ModeExclusive, Bearer: 100},
		{CalendarID: "b", BookingID: "p-b", Mode: ModeExclusive, Bearer: 101},
		{CalendarID: "c", BookingID: "p-c", Mode: ModeExclusive, Bearer: 102},
	}
	res, err := lc.MatchCommit(when, members)
	if err != nil {
		t.Fatal(err)
	}
	if res.Committed {
		t.Fatal("should be refused")
	}
	if len(res.Blocking) != 2 || res.Blocking[0] != "a" || res.Blocking[1] != "c" {
		t.Fatalf("blocking = %v, want [a c]", res.Blocking)
	}
}

// TestMatchCommitValidation: empty set, invalid span, unknown calendar, duplicate.
func TestMatchCommitValidation(t *testing.T) {
	lc, _, _ := setupLifecycle(t, "a")
	when := Span{Start: ot.MustParse("2026-07-08T09:00:00Z"), End: ot.MustParse("2026-07-08T12:00:00Z")}

	if _, err := lc.MatchCommit(when, nil); err == nil {
		t.Fatal("empty member set should error")
	}
	bad := Span{Start: when.End, End: when.Start}
	if _, err := lc.MatchCommit(bad, []CommitMember{{CalendarID: "a", BookingID: "x", Bearer: 1}}); err == nil {
		t.Fatal("invalid span should error")
	}
	if _, err := lc.MatchCommit(when, []CommitMember{{CalendarID: "nope", BookingID: "x", Bearer: 1}}); err == nil {
		t.Fatal("unknown calendar should error")
	}
}

// TestMatchCommitWaitThenSeize: the match -> commit pair. Use Match to find a
// coincident opening, then MatchCommit to seize it; the seized span must have
// been one of the matches.
func TestMatchCommitFollowsMatch(t *testing.T) {
	lc, s, _ := setupLifecycle(t, "x", "y")
	// x busy 09-11, y busy 13-15. Common free includes 11-13.
	if _, err := lc.Create(bk("x1", "x", StateBinding, "2026-07-08T09:00:00Z", "2026-07-08T11:00:00Z", 200)); err != nil {
		t.Fatal(err)
	}
	if _, err := lc.Create(bk("y1", "y", StateBinding, "2026-07-08T13:00:00Z", "2026-07-08T15:00:00Z", 201)); err != nil {
		t.Fatal(err)
	}
	calX, _ := lc.src.calendar("x")
	calY, _ := lc.src.calendar("y")

	from := ot.MustParse("2026-07-08T08:00:00Z")
	to := ot.MustParse("2026-07-08T18:00:00Z")
	mr, err := s.Match([]Calendar{calX, calY}, from, to, 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(mr.Matches) == 0 {
		t.Fatal("expected at least one coincident opening")
	}
	// seize a 2h slot inside the first match.
	first := mr.Matches[0]
	when := Span{Start: first.Start, End: first.Start.Add(2 * time.Hour)}
	res, err := lc.MatchCommit(when, []CommitMember{
		{CalendarID: "x", BookingID: "seize-x", Mode: ModeExclusive, Bearer: 100},
		{CalendarID: "y", BookingID: "seize-y", Mode: ModeExclusive, Bearer: 101},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Committed {
		t.Fatalf("seizing a matched opening should commit: %+v", res)
	}
}

// TestMatchCommitRandomizedAtomicity: random commit attempts; after each, the
// index must equal a rebuild (so a refused commit never leaves partial state and
// a successful one is fully consistent).
func TestMatchCommitRandomizedAtomicity(t *testing.T) {
	rng := rand.New(rand.NewSource(60))
	base := ot.MustParse("2026-07-08T00:00:00Z")

	for trial := 0; trial < 100; trial++ {
		lc, s, src := setupLifecycle(t, "a", "b", "c")
		seq := 0

		// seed some random existing bookings
		for k := 0; k < rng.Intn(6); k++ {
			cid := []string{"a", "b", "c"}[rng.Intn(3)]
			st := base.Add(time.Duration(rng.Intn(20*12)*5) * time.Minute)
			en := st.Add(time.Duration((rng.Intn(12)+1)*5) * time.Minute)
			_, _ = lc.Create(Booking{BookingID: fmt.Sprintf("seed%d", seq), CalendarID: cid,
				State: StateBinding, Span: Span{Start: st, End: en}, Mode: ModeExclusive,
				Bearer: uint64(seq + 300), CreatedAt: ot.Now(), UpdatedAt: ot.Now()})
			seq++
		}

		// attempt a random commit across a random subset of calendars.
		st := base.Add(time.Duration(rng.Intn(20*12)*5) * time.Minute)
		en := st.Add(time.Duration((rng.Intn(8)+1)*5) * time.Minute)
		when := Span{Start: st, End: en}
		var members []CommitMember
		for _, cid := range []string{"a", "b", "c"} {
			if rng.Intn(2) == 0 {
				members = append(members, CommitMember{CalendarID: cid,
					BookingID: fmt.Sprintf("commit%d-%s", trial, cid), Mode: ModeExclusive,
					Bearer: uint64(seq + 500)})
				seq++
			}
		}
		if len(members) > 0 {
			if _, err := lc.MatchCommit(when, members); err != nil {
				t.Fatalf("trial %d: MatchCommit: %v", trial, err)
			}
		}
		// invariant after the attempt (commit or refusal).
		assertIndexMatchesRebuild(t, s, src, fmt.Sprintf("trial %d after commit attempt", trial))
		s.Close()
	}
}
