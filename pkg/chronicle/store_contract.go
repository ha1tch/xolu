// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package chronicle

import (
	"testing"
	"time"
)

// RunBucketStoreContract exercises any BucketStore[int64] implementation
// against the behaviour the engine depends on. New stores (bal's
// SQL-plane store at wave 4; any Pebble-plane store) must pass this
// harness before the engine is trusted on them — the same
// contract-harness discipline as pkg/graph's store contract.
//
// The int64 carrier is deliberate: the contract is about storage
// behaviour (presence, overwrite, deletion, ordered ranging, level
// isolation), not the monoid, and a comparable carrier keeps the
// assertions exact.
func RunBucketStoreContract(t *testing.T, newStore func() BucketStore[int64]) {
	t.Helper()
	base := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	k := func(level, slot int) BucketKey {
		return BucketKey{Level: level, Start: base.Add(time.Duration(slot) * 5 * time.Minute)}
	}

	t.Run("get_missing", func(t *testing.T) {
		s := newStore()
		if _, ok := s.Get(k(0, 0)); ok {
			t.Fatal("Get on empty store must report absence")
		}
	})

	t.Run("put_get_overwrite", func(t *testing.T) {
		s := newStore()
		s.Put(k(0, 0), 7)
		if v, ok := s.Get(k(0, 0)); !ok || v != 7 {
			t.Fatalf("Get after Put: got (%v,%v)", v, ok)
		}
		s.Put(k(0, 0), 9)
		if v, _ := s.Get(k(0, 0)); v != 9 {
			t.Fatalf("Put must overwrite: got %v", v)
		}
	})

	t.Run("delete_idempotent", func(t *testing.T) {
		s := newStore()
		s.Put(k(0, 1), 3)
		s.Delete(k(0, 1))
		if _, ok := s.Get(k(0, 1)); ok {
			t.Fatal("Get after Delete must report absence")
		}
		s.Delete(k(0, 1)) // deleting absent key must not panic
	})

	t.Run("level_isolation", func(t *testing.T) {
		s := newStore()
		s.Put(k(0, 0), 1)
		s.Put(k(1, 0), 2) // same Start, different level
		if v, _ := s.Get(k(0, 0)); v != 1 {
			t.Fatal("level 0 clobbered by level 1 write at same Start")
		}
		if v, _ := s.Get(k(1, 0)); v != 2 {
			t.Fatal("level 1 value wrong")
		}
	})

	t.Run("range_ordered_halfopen", func(t *testing.T) {
		s := newStore()
		// Insert out of order; expect ascending visits within [from, to).
		for _, slot := range []int{4, 0, 2, 6, 1} {
			s.Put(k(0, slot), int64(slot))
		}
		s.Put(k(1, 3), 99) // other level: must not appear

		from := k(0, 1).Start
		to := k(0, 6).Start // half-open: slot 6 excluded
		var got []int64
		s.RangeLevel(0, from, to, func(_ BucketKey, v int64) bool {
			got = append(got, v)
			return true
		})
		want := []int64{1, 2, 4}
		if len(got) != len(want) {
			t.Fatalf("range visited %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("range order/content: got %v, want %v", got, want)
			}
		}
	})

	t.Run("range_early_stop", func(t *testing.T) {
		s := newStore()
		for slot := 0; slot < 5; slot++ {
			s.Put(k(0, slot), int64(slot))
		}
		count := 0
		s.RangeLevel(0, base, base.Add(time.Hour), func(_ BucketKey, _ int64) bool {
			count++
			return count < 2
		})
		if count != 2 {
			t.Fatalf("early stop: visited %d, want 2", count)
		}
	})
}
