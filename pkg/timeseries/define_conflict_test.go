// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

// define_conflict_test.go
//
// XOT240: a re-definition of a timeline is idempotent when it matches and a
// conflict when it would change the name or the retention; RetentionForever is
// an explicit "never expire"; one event can be tested for by its exact key.

import (
	"context"
	"strings"
	"testing"
	"time"
)

func pebbleOf(t *testing.T) *PebbleStore {
	t.Helper()
	p, ok := newTestStore(t).(*PebbleStore)
	if !ok {
		t.Skip("not a PebbleStore")
	}
	return p
}

func TestDefineTimeline_RedefinitionIsIdempotentOrConflicts(t *testing.T) {
	s := pebbleOf(t)
	id := TimelineID(1)
	if err := s.DefineTimeline(id, TimelineConfig{Dims: 2, Name: "idx", RetentionDays: RetentionForever}); err != nil {
		t.Fatal(err)
	}
	// The same definition again is a no-op.
	if err := s.DefineTimeline(id, TimelineConfig{Dims: 2, Name: "idx", RetentionDays: RetentionForever}); err != nil {
		t.Fatalf("an identical re-definition: %v", err)
	}
	// An empty name means "no opinion".
	if err := s.DefineTimeline(id, TimelineConfig{Dims: 2, RetentionDays: RetentionForever}); err != nil {
		t.Fatalf("a re-definition with no name: %v", err)
	}
	for name, cfg := range map[string]TimelineConfig{
		"a finite retention":           {Dims: 2, Name: "idx", RetentionDays: 30},
		"retention left out (default)": {Dims: 2, Name: "idx"},
		"another name":                 {Dims: 2, Name: "other", RetentionDays: RetentionForever},
	} {
		err := s.DefineTimeline(id, cfg)
		if err == nil || !strings.Contains(err.Error(), "XOLU-TS028") {
			t.Fatalf("%s: err = %v, want a XOLU-TS028 conflict", name, err)
		}
		if got, _ := s.Timeline(id); got.RetentionDays != RetentionForever || got.Name != "idx" {
			t.Fatalf("%s: the timeline changed to %+v although the definition was refused", name, got)
		}
	}
	// The same refusals hold once the timeline has data.
	if err := s.Append(context.Background(), Event{Timeline: id, Dims: []uint64{1, 1}, Time: time.Unix(1_000_000, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := s.DefineTimeline(id, TimelineConfig{Dims: 2, Name: "idx", RetentionDays: 7}); err == nil || !strings.Contains(err.Error(), "XOLU-TS028") {
		t.Fatalf("after the first write: err = %v, want a XOLU-TS028 conflict", err)
	}
	// Changing them is what UpdateTimeline (PATCH /ts/tl/{id}) is for.
	if err := s.UpdateTimeline(id, TimelineConfig{Name: "idx", RetentionDays: 90}); err != nil {
		t.Fatalf("UpdateTimeline: %v", err)
	}
	if got, _ := s.Timeline(id); got.RetentionDays != 90 {
		t.Fatalf("retention after UpdateTimeline = %d, want 90", got.RetentionDays)
	}
	// Every negative value has always meant "no expiry" (docs/API_REFERENCE.md), so
	// -5 is accepted, and it is the same definition as RetentionForever.
	if err := s.DefineTimeline(2, TimelineConfig{Dims: 1, RetentionDays: -5}); err != nil {
		t.Fatalf("DefineTimeline refused retention_days -5: %v", err)
	}
	if err := s.DefineTimeline(2, TimelineConfig{Dims: 1, RetentionDays: RetentionForever}); err != nil {
		t.Fatalf("-1 over -5 is the same definition, but: %v", err)
	}
}

// With a store-level default retention, a timeline at 0 follows it and one at
// RetentionForever does not: the purge removes the old event from the first only.
func TestRetentionForeverIsNeverPurged(t *testing.T) {
	ctx := context.Background()
	s := pebbleOf(t)
	if err := s.SetDefaultRetentionDays(30); err != nil {
		t.Fatal(err)
	}
	forever, followsDefault := TimelineID(1), TimelineID(2)
	if err := s.DefineTimeline(forever, TimelineConfig{Dims: 1, RetentionDays: RetentionForever}); err != nil {
		t.Fatal(err)
	}
	if err := s.DefineTimeline(followsDefault, TimelineConfig{Dims: 1}); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-90 * 24 * time.Hour)
	for _, id := range []TimelineID{forever, followsDefault} {
		if err := s.Append(ctx, Event{Timeline: id, Dims: []uint64{1}, Time: old}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Purge(ctx); err != nil {
		t.Fatalf("Purge: %v", err)
	}
	count := func(id TimelineID) int {
		evs, err := s.QueryRange(ctx, RangeQuery{Timeline: id, Dims: []uint64{1}, From: old.Add(-time.Hour), To: time.Now().UTC()})
		if err != nil {
			t.Fatal(err)
		}
		return len(evs)
	}
	if got := count(forever); got != 1 {
		t.Fatalf("the RetentionForever timeline holds %d events after a purge, want 1", got)
	}
	if got := count(followsDefault); got != 0 {
		t.Fatalf("the timeline that follows the 30-day default holds %d 90-day-old events after a purge, want 0", got)
	}
}

func TestHasFindsAnEventByItsExactKey(t *testing.T) {
	ctx := context.Background()
	s := pebbleOf(t)
	if err := s.DefineTimeline(1, TimelineConfig{Dims: 2}); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(2_000_000, 123).UTC()
	if err := s.Append(ctx, Event{Timeline: 1, Dims: []uint64{5, 6}, Time: at}); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		e    Event
		want bool
	}{
		"the same key":       {Event{Timeline: 1, Dims: []uint64{5, 6}, Time: at}, true},
		"another time":       {Event{Timeline: 1, Dims: []uint64{5, 6}, Time: at.Add(time.Nanosecond)}, false},
		"another second dim": {Event{Timeline: 1, Dims: []uint64{5, 7}, Time: at}, false},
		"another first dim":  {Event{Timeline: 1, Dims: []uint64{4, 6}, Time: at}, false},
	} {
		got, err := s.Has(ctx, tc.e)
		if err != nil || got != tc.want {
			t.Fatalf("%s: Has = %v, %v; want %v", name, got, err, tc.want)
		}
	}
	if _, err := s.Has(ctx, Event{Timeline: 1, Dims: []uint64{5}, Time: at}); err == nil {
		t.Fatal("Has accepted a key with the wrong number of dims")
	}
	if _, err := s.Has(ctx, Event{Timeline: 9, Dims: []uint64{5, 6}, Time: at}); err == nil {
		t.Fatal("Has accepted an undefined timeline")
	}
}
