// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package chronicle

import (
	"math/rand"
	"testing"
	"time"
)

// ─── Monoid laws, per instantiation ─────────────────────────────────────────
// Associativity and identity are the engine's load-bearing assumptions;
// each incumbent instantiation proves them by property test, not trust.

func testMonoidLaws[T comparable](t *testing.T, name string, m Monoid[T], gen func(r *rand.Rand) T) {
	t.Helper()
	r := rand.New(rand.NewSource(42))
	for i := 0; i < 500; i++ {
		a, b, c := gen(r), gen(r), gen(r)
		if m.Combine(a, m.Combine(b, c)) != m.Combine(m.Combine(a, b), c) {
			t.Fatalf("%s: associativity violated at a=%v b=%v c=%v", name, a, b, c)
		}
		if m.Combine(m.Identity(), a) != a || m.Combine(a, m.Identity()) != a {
			t.Fatalf("%s: identity violated at a=%v", name, a)
		}
	}
}

func TestMonoidLaws_AllInstantiations(t *testing.T) {
	// ts sum: floats are only approximately associative; use ints-as-floats
	// so the law holds exactly, which is also ts's real usage envelope for
	// counts. (True float folds accept the usual FP caveat; the engine's
	// correctness claim is algebraic, tested on an exact carrier.)
	testMonoidLaws[float64](t, "SumFloat64", SumFloat64{}, func(r *rand.Rand) float64 {
		return float64(r.Intn(1000))
	})
	genMV := func(r *rand.Rand) MinValue {
		if r.Intn(5) == 0 {
			return MinValue{}
		}
		return MinValue{Valid: true, V: float64(r.Intn(1000))}
	}
	testMonoidLaws[MinValue](t, "MinFloat64", MinFloat64{}, genMV)
	testMonoidLaws[MinValue](t, "MaxFloat64", MaxFloat64{}, genMV)
	testMonoidLaws[uint8](t, "BitsetOR (cal dayparts)", BitsetOR{}, func(r *rand.Rand) uint8 {
		return uint8(r.Intn(256))
	})
	testMonoidLaws[int64](t, "SumInt64 (bal)", SumInt64{}, func(r *rand.Rand) int64 {
		return int64(r.Intn(2000) - 1000) // signed: bal's two-sided entries
	})
}

// ─── The homomorphism property ──────────────────────────────────────────────
// After any sequence of appends, every coarse bucket must equal the fold
// of its child fine buckets. This is the theorem the engine implements;
// if it fails, cascading combine and re-folding disagree.

func fiveMinHierarchy(t *testing.T) *Hierarchy {
	t.Helper()
	h, err := NewHierarchy(
		Grain{Name: "5m", Width: 5 * time.Minute},
		Grain{Name: "hour", Width: time.Hour},
		Grain{Name: "day", Width: 24 * time.Hour},
	)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHomomorphism_CoarseEqualsFoldOfFine(t *testing.T) {
	h := fiveMinHierarchy(t)
	store := NewMemStore[int64]()
	eng, err := NewEngine[int64](SumInt64{}, h, store)
	if err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 2000; i++ {
		at := base.Add(time.Duration(r.Intn(24*60)) * time.Minute)
		eng.Append(int64(r.Intn(100)), at)
	}

	// Every hour bucket == fold of its twelve 5m children; the day == fold
	// of its 24 hours.
	m := SumInt64{}
	dayTotal := m.Identity()
	for hr := 0; hr < 24; hr++ {
		hourStart := base.Add(time.Duration(hr) * time.Hour)
		fromFine := m.Identity()
		for i := 0; i < 12; i++ {
			fromFine = m.Combine(fromFine, eng.Bucket(0, hourStart.Add(time.Duration(i)*5*time.Minute)))
		}
		got := eng.Bucket(1, hourStart)
		if got != fromFine {
			t.Fatalf("hour %d: coarse bucket %d != fold of fine %d — homomorphism broken", hr, got, fromFine)
		}
		dayTotal = m.Combine(dayTotal, got)
	}
	if got := eng.Bucket(2, base); got != dayTotal {
		t.Fatalf("day bucket %d != fold of hours %d", got, dayTotal)
	}
}

func TestHomomorphism_CalDaypartShape(t *testing.T) {
	// cal's shape: 5-minute occupancy quanta OR-folded into 3h dayparts
	// and a day byte (@cal codec §4) — instantiated on the engine.
	h, err := NewHierarchy(
		Grain{Name: "quantum", Width: 5 * time.Minute},
		Grain{Name: "daypart", Width: 3 * time.Hour},
		Grain{Name: "day", Width: 24 * time.Hour},
	)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := NewEngine[uint8](BitsetOR{}, h, NewMemStore[uint8]())
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	// Occupy one quantum in daypart 0 and one in daypart 5.
	eng.Append(1, base.Add(10*time.Minute))
	eng.Append(1, base.Add(16*time.Hour))

	if eng.Bucket(1, base) != 1 {
		t.Fatal("daypart 0 should be occupied")
	}
	if eng.Bucket(1, base.Add(6*time.Hour)) != 0 {
		t.Fatal("daypart 2 should be empty")
	}
	if eng.Bucket(2, base) != 1 {
		t.Fatal("day byte should show occupancy (OR)")
	}
}

// ─── Invalidation and recompute ─────────────────────────────────────────────

func TestInvalidate_RemovesCoveringChain(t *testing.T) {
	h := fiveMinHierarchy(t)
	store := NewMemStore[int64]()
	eng, _ := NewEngine[int64](SumInt64{}, h, store)
	at := time.Date(2026, 7, 21, 10, 7, 0, 0, time.UTC)
	eng.Append(5, at)
	if store.Len() != 3 {
		t.Fatalf("expected 3 buckets (one per level), got %d", store.Len())
	}
	eng.Invalidate(at)
	if store.Len() != 0 {
		t.Fatalf("invalidate left %d buckets", store.Len())
	}
	if eng.Bucket(0, at) != 0 {
		t.Fatal("invalidated bucket should read as identity")
	}
}

func TestRecompute_EqualsFreshFold(t *testing.T) {
	h := fiveMinHierarchy(t)
	eng, _ := NewEngine[int64](SumInt64{}, h, NewMemStore[int64]())

	// The authoritative record (the consumer's journal).
	type ev struct {
		v  int64
		at time.Time
	}
	base := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	r := rand.New(rand.NewSource(11))
	var journal []ev
	for i := 0; i < 300; i++ {
		e := ev{v: int64(r.Intn(50)), at: base.Add(time.Duration(r.Intn(24*60)) * time.Minute)}
		journal = append(journal, e)
		eng.Append(e.v, e.at)
	}

	// Correct one event: change journal[0]'s value, then recompute the
	// window containing it via replay from the corrected journal.
	journal[0].v += 1000
	eng.Recompute(journal[0].at, func(from, to time.Time, emit func(v int64, at time.Time)) {
		for _, e := range journal {
			emit(e.v, e.at)
		}
	})

	// A freshly-folded engine over the corrected journal must agree at
	// every level and bucket touched.
	fresh, _ := NewEngine[int64](SumInt64{}, h, NewMemStore[int64]())
	for _, e := range journal {
		fresh.Append(e.v, e.at)
	}
	for lvl := 0; lvl < h.Levels(); lvl++ {
		g := h.Grain(lvl)
		for start := base; start.Before(base.Add(24 * time.Hour)); start = start.Add(g.Width) {
			if got, want := eng.Bucket(lvl, start), fresh.Bucket(lvl, start); got != want {
				t.Fatalf("level %d bucket %v: recomputed %d != fresh fold %d", lvl, start, got, want)
			}
		}
	}
}

// ─── Hierarchy validation ───────────────────────────────────────────────────

func TestHierarchy_RejectsNonMultiples(t *testing.T) {
	if _, err := NewHierarchy(
		Grain{Name: "5m", Width: 5 * time.Minute},
		Grain{Name: "7m", Width: 7 * time.Minute},
	); err == nil {
		t.Fatal("non-multiple grain must be rejected — the homomorphism requires exact nesting")
	}
	if _, err := NewHierarchy(); err == nil {
		t.Fatal("empty hierarchy must be rejected")
	}
}
