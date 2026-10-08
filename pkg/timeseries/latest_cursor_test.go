// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

// latest_cursor_test.go
//
// XOT240: Latest returns at most N events (10,000 at most); a cursor pages past
// that. The pages together are exactly what one unpaged call returns.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestLatestCursorPagesThroughEveryEventOnce(t *testing.T) {
	ctx := context.Background()
	s := pebbleOf(t)
	if err := s.DefineTimeline(1, TimelineConfig{Dims: 2}); err != nil {
		t.Fatal(err)
	}
	base := time.Unix(3_000_000, 0).UTC()
	for v := uint64(1); v <= 25; v++ {
		if err := s.Append(ctx, Event{Timeline: 1, Dims: []uint64{7, v}, Time: base.Add(time.Duration(v) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	// Events of another entity share the timeline and must never appear.
	for v := uint64(1); v <= 5; v++ {
		if err := s.Append(ctx, Event{Timeline: 1, Dims: []uint64{8, v}, Time: base}); err != nil {
			t.Fatal(err)
		}
	}
	key := func(e Event) string { return fmt.Sprint(e.Dims) }

	all, err := s.Latest(ctx, LatestQuery{Timeline: 1, Dims: []uint64{7}, N: 100})
	if err != nil || len(all) != 25 {
		t.Fatalf("unpaged Latest: %d events, err=%v, want 25", len(all), err)
	}

	for _, pageSize := range []int{10, 5, 25, 7, 1} {
		var got []string
		var cursor []byte
		pages := 0
		for {
			var next []byte
			evs, err := s.Latest(ctx, LatestQuery{Timeline: 1, Dims: []uint64{7}, N: pageSize, Cursor: cursor, NextCursor: &next})
			if err != nil {
				t.Fatalf("page size %d, page %d: %v", pageSize, pages+1, err)
			}
			for _, e := range evs {
				got = append(got, key(e))
			}
			pages++
			if len(next) == 0 {
				break
			}
			if len(evs) != pageSize {
				t.Fatalf("page size %d: a page with a next cursor holds %d events", pageSize, len(evs))
			}
			cursor = next
			if pages > 30 {
				t.Fatal("paging did not terminate")
			}
		}
		var want []string
		for _, e := range all {
			want = append(want, key(e))
		}
		if strings.Join(got, ";") != strings.Join(want, ";") {
			t.Fatalf("page size %d: the pages are\n %v\nbut the unpaged call returns\n %v", pageSize, got, want)
		}
		wantPages := (25 + pageSize - 1) / pageSize
		if pages != wantPages {
			t.Fatalf("page size %d: %d pages, want %d", pageSize, pages, wantPages)
		}
	}

	// A page that ends exactly at the last event has no cursor.
	var next []byte
	if _, err := s.Latest(ctx, LatestQuery{Timeline: 1, Dims: []uint64{7}, N: 25, NextCursor: &next}); err != nil || next != nil {
		t.Fatalf("a page holding every event: cursor %v, err=%v, want none", next, err)
	}
}

func TestLatestCursorWithTimeBounds(t *testing.T) {
	ctx := context.Background()
	s := pebbleOf(t)
	if err := s.DefineTimeline(1, TimelineConfig{Dims: 2}); err != nil {
		t.Fatal(err)
	}
	base := time.Unix(4_000_000, 0).UTC()
	for v := uint64(1); v <= 20; v++ {
		if err := s.Append(ctx, Event{Timeline: 1, Dims: []uint64{7, v}, Time: base.Add(time.Duration(v) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	// Only the events at seconds 6 to 15 match: ten of them, paged by four.
	q := LatestQuery{Timeline: 1, Dims: []uint64{7}, From: base.Add(6 * time.Second), To: base.Add(15 * time.Second)}
	var got int
	var cursor []byte
	for i := 0; i < 10; i++ {
		var next []byte
		q.N, q.Cursor, q.NextCursor = 4, cursor, &next
		evs, err := s.Latest(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		got += len(evs)
		if len(next) == 0 {
			break
		}
		cursor = next
	}
	if got != 10 {
		t.Fatalf("paged events within the bounds = %d, want 10", got)
	}
}

func TestLatestCursorFromElsewhereIsRefused(t *testing.T) {
	ctx := context.Background()
	s := pebbleOf(t)
	for _, id := range []TimelineID{1, 2} {
		if err := s.DefineTimeline(id, TimelineConfig{Dims: 2}); err != nil {
			t.Fatal(err)
		}
		for v := uint64(1); v <= 6; v++ {
			if err := s.Append(ctx, Event{Timeline: id, Dims: []uint64{7, v}, Time: time.Unix(5_000_000, int64(v)).UTC()}); err != nil {
				t.Fatal(err)
			}
		}
	}
	var onTimeline1 []byte
	if _, err := s.Latest(ctx, LatestQuery{Timeline: 1, Dims: []uint64{7}, N: 3, NextCursor: &onTimeline1}); err != nil || onTimeline1 == nil {
		t.Fatalf("setup: cursor %v err %v", onTimeline1, err)
	}
	// The same cursor on another timeline, and a garbage one, are errors, not silent empties.
	for name, q := range map[string]LatestQuery{
		"another timeline": {Timeline: 2, Dims: []uint64{7}, N: 3, Cursor: onTimeline1},
		"another prefix":   {Timeline: 1, Dims: []uint64{9}, N: 3, Cursor: onTimeline1},
		"garbage":          {Timeline: 1, Dims: []uint64{7}, N: 3, Cursor: []byte("not a key")},
	} {
		if _, err := s.Latest(ctx, q); err == nil || !strings.Contains(err.Error(), "XOLU-TS030") {
			t.Fatalf("%s: err = %v, want a XOLU-TS030 refusal", name, err)
		}
	}
}
