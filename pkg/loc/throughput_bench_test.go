// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// throughput_bench_test.go — T-129 (wave 9b): loc-02-implementation.md
// Stage 2 named a benchmark exit criterion ("a write-path throughput
// number recorded, however rough, before Stage 2 is called done")
// that was never met before v0.21.0 shipped. This closes it.
//
// admission_test.go's own BenchmarkMove already covers the
// single-leaf, no-fence shape (found on inspection here, not
// duplicated) — its own comment names it as this exact exit
// criterion, written but never run/recorded. What was genuinely
// missing, and what this file adds, is the other half
// loc-02-implementation.md's own text names: "single-leaf AND
// single-fence CAS" — the multi-target atomicity path, Stage 2's own
// highest-risk mechanism, via the EnteredFenceKeys/ExitedFenceKeys
// test hook admission_test.go's correctness tests already use for the
// identical reason — no geometry, no tree-alignment lookup.
//
// This is a throughput measurement, not the concurrency-race guard
// (G-14, docs/KNOWN_ISSUES.md, dormant, multi-core-gated) — sequential,
// single-goroutine, unlimited capacity throughout (never refused, so
// every call measures the guarded-write path itself, not refusal
// handling). Run both together with:
//   go test ./pkg/loc/ -bench BenchmarkMove -benchtime=1s -run '^$'

package loc

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "modernc.org/sqlite"
)

// benchStore mirrors testStore(t) exactly, using *testing.B's own
// TempDir/Cleanup instead of *testing.T's — testStore itself can't be
// reused directly since it's typed against *testing.T.
func benchStore(b *testing.B) *Store {
	b.Helper()
	tmp, err := os.MkdirTemp("", "loc-bench")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(tmp) })
	db, err := sql.Open("sqlite",
		tmp+"/loc.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	s := NewStore(db, 0)
	if err := s.Init(context.Background()); err != nil {
		b.Fatal(err)
	}
	return s
}

// BenchmarkMove_LeafAndFence isolates Stage 2's own multi-target CAS
// guard directly: every move (after the first, which has nothing yet
// to exit) both reassigns the leaf and crosses one capacity-bearing
// fence, alternating between two leaves and two fences so every call
// after the first does a real enter-one/exit-one pair — no geometry,
// no tree-alignment lookup, exactly the shape loc-02-implementation.md's
// own Stage 2 names.
func BenchmarkMove_LeafAndFence(b *testing.B) {
	ctx := context.Background()
	s := benchStore(b)

	rootID := "bench-root2"
	if _, err := s.Def(ctx, LocationDef{
		ID: rootID, Name: "root", Postable: false,
		Placement: Placement{Anchor: &GeoAnchor{Lat: 0, Lon: 0}},
	}); err != nil {
		b.Fatalf("Def(root): %v", err)
	}
	leafA, leafB := "bench-root2/a", "bench-root2/b"
	for _, id := range []string{leafA, leafB} {
		if _, err := s.Def(ctx, LocationDef{
			ID: id, ParentID: &rootID, Name: id, Postable: true,
		}); err != nil {
			b.Fatalf("Def(%s): %v", id, err)
		}
	}
	fenceAKey, err := s.DefFence(ctx, "bench-fence-a", nil)
	if err != nil {
		b.Fatalf("DefFence(a): %v", err)
	}
	fenceBKey, err := s.DefFence(ctx, "bench-fence-b", nil)
	if err != nil {
		b.Fatalf("DefFence(b): %v", err)
	}

	const subjectRef = "bench:leaf-fence-subject"
	var currentFence FenceKey
	var hasFence bool
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		to, enterFence := leafA, fenceAKey
		if i%2 == 1 {
			to, enterFence = leafB, fenceBKey
		}
		var exitedKeys []FenceKey
		if hasFence {
			exitedKeys = []FenceKey{currentFence}
		}
		if err := s.Move(ctx, MoveParams{
			SubjectRef:       subjectRef,
			ToLocationID:     to,
			EnteredFenceKeys: []FenceKey{enterFence},
			ExitedFenceKeys:  exitedKeys,
		}); err != nil {
			b.Fatalf("Move: %v", err)
		}
		currentFence, hasFence = enterFence, true
	}
}
