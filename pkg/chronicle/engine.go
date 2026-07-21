// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package chronicle

import (
	"fmt"
	"time"
)

// BucketKey addresses one bucket: a hierarchy level and its grain-aligned
// start instant (UTC).
type BucketKey struct {
	Level int
	Start time.Time
}

// BucketStore is the storage seam. The engine is storage-agnostic: an
// in-memory store ships here for tests and small consumers; bal brings
// a SQL-plane store (wave 4, guard-locality obliged — @C04a), and any
// Pebble-plane store arrives with its consumer.
//
// Get returns the bucket's folded value and whether it exists. Put
// overwrites. Delete removes (used by invalidation). Implementations
// must be safe for the engine's single-writer discipline; concurrent
// writers are the consumer's concern (bal serialises under its own
// transaction; @C04a).
type BucketStore[T any] interface {
	Get(k BucketKey) (T, bool)
	Put(k BucketKey, v T)
	Delete(k BucketKey)
}

// Engine is the monoid-parameterised cascade over one hierarchy and one
// store. Append folds a value into the finest bucket and cascades the
// combine upward through every coarser grain. Invalidate removes every
// bucket covering an instant so a later Recompute (or the consumer's
// re-fold) rebuilds them — corrections must not silently keep stale
// coarse folds (the ts correction rule, generalised).
type Engine[T any] struct {
	m Monoid[T]
	h *Hierarchy
	s BucketStore[T]
}

// NewEngine constructs an engine. All three parameters are required.
func NewEngine[T any](m Monoid[T], h *Hierarchy, s BucketStore[T]) (*Engine[T], error) {
	if m == nil || h == nil || s == nil {
		return nil, fmt.Errorf("chronicle: NewEngine requires monoid, hierarchy, and store")
	}
	return &Engine[T]{m: m, h: h, s: s}, nil
}

// Append folds v into the bucket containing t at every level of the
// hierarchy — the upward cascade. Because Combine is associative and
// every coarse bucket is an exact multiple of the fine grain, combining
// the increment directly into each level equals re-folding that level
// from its children: the homomorphism property, asserted by the
// engine's tests rather than trusted.
func (e *Engine[T]) Append(v T, t time.Time) {
	for level := 0; level < e.h.Levels(); level++ {
		k := BucketKey{Level: level, Start: e.h.Grain(level).Truncate(t)}
		cur, ok := e.s.Get(k)
		if !ok {
			cur = e.m.Identity()
		}
		e.s.Put(k, e.m.Combine(cur, v))
	}
}

// Bucket returns the folded value for the bucket containing t at the
// given level, or the identity if the bucket does not exist.
func (e *Engine[T]) Bucket(level int, t time.Time) T {
	k := BucketKey{Level: level, Start: e.h.Grain(level).Truncate(t)}
	if v, ok := e.s.Get(k); ok {
		return v
	}
	return e.m.Identity()
}

// Invalidate removes every bucket, at every level, that covers t. A
// correction to underlying data makes every covering fold stale; the
// safe response is absence (forcing recompute), never a silently wrong
// value. Recompute rebuilds from a replay callback.
func (e *Engine[T]) Invalidate(t time.Time) {
	for level := 0; level < e.h.Levels(); level++ {
		e.s.Delete(BucketKey{Level: level, Start: e.h.Grain(level).Truncate(t)})
	}
}

// Recompute rebuilds every bucket, at every level, inside the coarsest
// bucket containing t, by re-folding source values supplied by replay.
// replay must yield every (value, instant) pair within the half-open
// window [from, to) — the consumer owns the authoritative record (the
// journal, the event store) and therefore owns replay; the engine owns
// only the fold.
//
// The whole coarsest window is cleared, not just the chain covering t:
// replay refills every fine bucket in the window via Append, so any
// bucket left standing would double-count its replayed values.
func (e *Engine[T]) Recompute(t time.Time, replay func(from, to time.Time, emit func(v T, at time.Time))) {
	coarsest := e.h.Grain(e.h.Levels() - 1)
	from := coarsest.Truncate(t)
	to := from.Add(coarsest.Width)

	// Clear every bucket at every level within [from, to).
	for level := 0; level < e.h.Levels(); level++ {
		g := e.h.Grain(level)
		for start := g.Truncate(from); start.Before(to); start = start.Add(g.Width) {
			e.s.Delete(BucketKey{Level: level, Start: start})
		}
	}

	replay(from, to, func(v T, at time.Time) {
		if !at.Before(from) && at.Before(to) {
			e.Append(v, at)
		}
	})
}
