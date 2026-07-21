// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package chronicle

import (
	"sort"
	"time"
)

// MemStore is the in-memory BucketStore: the test vehicle and the
// small-consumer default. Not safe for concurrent use — the engine's
// single-writer discipline is the consumer's to enforce (@C04a).
type MemStore[T any] struct {
	m map[BucketKey]T
}

// NewMemStore constructs an empty in-memory store.
func NewMemStore[T any]() *MemStore[T] { return &MemStore[T]{m: map[BucketKey]T{}} }

func (s *MemStore[T]) Get(k BucketKey) (T, bool) { v, ok := s.m[k]; return v, ok }
func (s *MemStore[T]) Put(k BucketKey, v T)      { s.m[k] = v }
func (s *MemStore[T]) Delete(k BucketKey)        { delete(s.m, k) }

// RangeLevel visits existing buckets at level with Start in [from, to),
// ascending. The map is unordered, so keys are collected and sorted —
// fine for the test/small-consumer role; durable stores use an ordered
// scan natively.
func (s *MemStore[T]) RangeLevel(level int, from, to time.Time, fn func(k BucketKey, v T) bool) {
	var keys []BucketKey
	for k := range s.m {
		if k.Level == level && !k.Start.Before(from) && k.Start.Before(to) {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Start.Before(keys[j].Start) })
	for _, k := range keys {
		if !fn(k, s.m[k]) {
			return
		}
	}
}

// Len reports the number of stored buckets (test support).
func (s *MemStore[T]) Len() int { return len(s.m) }

// ─── Incumbent monoid instantiations (@C03) ─────────────────────────────────
//
// These are the extraction's correctness bar: the folds ts, cal, and bal
// already perform, expressed as engine parameters. Incumbents are NOT
// re-plumbed onto the engine (@C §5 — cal migrates opportunistically or
// never); these instantiations exist so the property tests prove the
// engine subsumes them, and so bal (wave 4) rides natively.

// SumFloat64 is ts's additive fold: (float64, +, 0).
type SumFloat64 struct{}

func (SumFloat64) Identity() float64           { return 0 }
func (SumFloat64) Combine(a, b float64) float64 { return a + b }

// MinFloat64 is ts's minimum fold, with +Inf-free identity handling via
// a validity flag: the identity is "no value yet".
type MinValue struct {
	Valid bool
	V     float64
}

type MinFloat64 struct{}

func (MinFloat64) Identity() MinValue { return MinValue{} }
func (MinFloat64) Combine(a, b MinValue) MinValue {
	switch {
	case !a.Valid:
		return b
	case !b.Valid:
		return a
	case b.V < a.V:
		return b
	default:
		return a
	}
}

// MaxFloat64 is ts's maximum fold, same identity treatment.
type MaxFloat64 struct{}

func (MaxFloat64) Identity() MinValue { return MinValue{} }
func (MaxFloat64) Combine(a, b MinValue) MinValue {
	switch {
	case !a.Valid:
		return b
	case !b.Valid:
		return a
	case b.V > a.V:
		return b
	default:
		return a
	}
}

// BitsetOR is cal's daypart occupancy fold: (uint8 bitset, OR, 0).
// One byte summarises a day at 3-hour-daypart granularity (@cal codec §4).
type BitsetOR struct{}

func (BitsetOR) Identity() uint8            { return 0 }
func (BitsetOR) Combine(a, b uint8) uint8   { return a | b }

// SumInt64 is bal's conservation fold: (int64, +, 0). Balance-as-of is
// this monoid chained across sealed checkpoints (@C03).
type SumInt64 struct{}

func (SumInt64) Identity() int64          { return 0 }
func (SumInt64) Combine(a, b int64) int64 { return a + b }
