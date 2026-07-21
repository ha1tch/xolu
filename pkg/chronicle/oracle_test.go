// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package chronicle

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// engineFingerprint serialises every bucket of an engine's store into
// the canonical line-oriented form: "level start value", sorted.
func engineFingerprint(h *Hierarchy, s *MemStore[int64]) string {
	var lines []string
	for lvl := 0; lvl < h.Levels(); lvl++ {
		s.RangeLevel(lvl, time.Time{}, time.Unix(1<<62, 0), func(k BucketKey, v int64) bool {
			lines = append(lines, fmt.Sprintf("%d %s %d", k.Level, k.Start.Format(time.RFC3339), v))
			return true
		})
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

type oracleEvent struct {
	v  int64
	at time.Time
}

// engineOracle builds the engine-backed instantiation: Derive replays
// the journal through a FRESH engine; Current fingerprints the live one.
func engineOracle(h *Hierarchy, live *MemStore[int64], journal *[]oracleEvent) RebuildOracle {
	return RebuildOracle{
		Name: "chronicle.engine",
		Derive: func(ctx context.Context) (string, error) {
			fresh := NewMemStore[int64]()
			eng, err := NewEngine[int64](SumInt64{}, h, fresh)
			if err != nil {
				return "", err
			}
			for _, e := range *journal {
				eng.Append(e.v, e.at)
			}
			return engineFingerprint(h, fresh), nil
		},
		Current: func(ctx context.Context) (string, error) {
			return engineFingerprint(h, live), nil
		},
	}
}

func TestRebuildOracle_EngineCleanState(t *testing.T) {
	h := fiveMinHierarchy(t)
	live := NewMemStore[int64]()
	eng, _ := NewEngine[int64](SumInt64{}, h, live)

	base := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	var journal []oracleEvent
	for i := 0; i < 500; i++ {
		e := oracleEvent{v: int64(i % 37), at: base.Add(time.Duration(i*7) * time.Minute)}
		journal = append(journal, e)
		eng.Append(e.v, e.at)
	}

	AssertRebuildOracle(t, context.Background(), engineOracle(h, live, &journal))
}

func TestRebuildOracle_DetectsCorruption(t *testing.T) {
	h := fiveMinHierarchy(t)
	live := NewMemStore[int64]()
	eng, _ := NewEngine[int64](SumInt64{}, h, live)

	base := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	var journal []oracleEvent
	for i := 0; i < 100; i++ {
		e := oracleEvent{v: 10, at: base.Add(time.Duration(i) * 5 * time.Minute)}
		journal = append(journal, e)
		eng.Append(e.v, e.at)
	}

	// Corrupt one bucket behind the engine's back.
	k := BucketKey{Level: 1, Start: base}
	v, _ := live.Get(k)
	live.Put(k, v+1)

	res, err := engineOracle(h, live, &journal).Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Equal {
		t.Fatal("oracle failed to detect a corrupted bucket")
	}
	if res.FirstDivergence == "" || !strings.Contains(res.FirstDivergence, "line") {
		t.Fatalf("divergence not localised: %q", res.FirstDivergence)
	}
}

func TestRebuildOracle_HarnessMechanics(t *testing.T) {
	ctx := context.Background()

	// Missing functions rejected.
	if _, err := (RebuildOracle{Name: "x"}).Check(ctx); err == nil {
		t.Fatal("oracle without Derive/Current must be rejected")
	}

	// CheckAll: divergences are results, not errors.
	oracles := []RebuildOracle{
		{Name: "ok", Derive: constFP("a\nb"), Current: constFP("a\nb")},
		{Name: "diverged", Derive: constFP("a\nb"), Current: constFP("a\nc")},
		{Name: "length", Derive: constFP("a"), Current: constFP("a\nb")},
	}
	results, err := CheckAll(ctx, oracles)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || !results[0].Equal || results[1].Equal || results[2].Equal {
		t.Fatalf("unexpected results: %+v", results)
	}
	if !strings.Contains(results[1].FirstDivergence, "line 2") {
		t.Fatalf("divergence should name line 2: %q", results[1].FirstDivergence)
	}
	if !strings.Contains(results[2].FirstDivergence, "length") {
		t.Fatalf("length divergence should be named: %q", results[2].FirstDivergence)
	}
}

func constFP(s string) func(context.Context) (string, error) {
	return func(context.Context) (string, error) { return s, nil }
}
