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
	// RangeLevel visits every existing bucket at the given level with
	// Start in the half-open window [from, to), in ascending Start
	// order. Returning false from fn stops the iteration. Required by
	// the prefix-fold read path (AsOf); implementations back it with an
	// ordered scan (SQL: ORDER BY start; Pebble: key iteration).
	RangeLevel(level int, from, to time.Time, fn func(k BucketKey, v T) bool)
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

// FoldRange folds the half-open window [from, to) using the coarsest
// buckets that fit entirely inside it, descending to finer grains only
// at the ragged edges — the classic prefix/segment walk. This is @C03's
// cumulative read ("the fold of a prefix: the same monoid, chained
// across sealed checkpoints"): bal's balance-as-of is FoldRange from
// the epoch (or the last sealed checkpoint) to the as-of instant.
//
// Correctness rests on the homomorphism: a coarse bucket equals the
// fold of its children, so substituting it for them changes nothing.
// The engine's tests assert FoldRange == the naive finest-grain fold.
//
// Cost: O(levels × buckets-touched); for a hierarchy like 5m/hour/day
// an as-of over a year touches ~365 day buckets + edge partials rather
// than ~105k five-minute buckets.
func (e *Engine[T]) FoldRange(from, to time.Time) T {
	from, to = from.UTC(), to.UTC()
	acc := e.m.Identity()
	if !from.Before(to) {
		return acc
	}
	e.foldRangeAt(e.h.Levels()-1, from, to, &acc)
	return acc
}

// foldRangeAt folds [from, to) at the given level: whole buckets of this
// grain are consumed directly; ragged leading/trailing partials recurse
// one level finer. At level 0 the finest buckets ARE the resolution —
// a partial finest bucket cannot occur because Append aligns everything
// to grain 0, so [from, to) is expected grain-0-aligned by callers that
// care about exactness (AsOf truncates accordingly).
func (e *Engine[T]) foldRangeAt(level int, from, to time.Time, acc *T) {
	g := e.h.Grain(level)
	if level == 0 {
		e.s.RangeLevel(0, from, to, func(_ BucketKey, v T) bool {
			*acc = e.m.Combine(*acc, v)
			return true
		})
		return
	}
	// First grain-aligned start at or after `from`; last aligned end at
	// or before `to`.
	alignedFrom := g.Truncate(from)
	if alignedFrom.Before(from) {
		alignedFrom = alignedFrom.Add(g.Width)
	}
	alignedTo := g.Truncate(to)

	if !alignedFrom.Before(alignedTo) {
		// No whole bucket of this grain fits; the entire window is
		// handled one level finer.
		e.foldRangeAt(level-1, from, to, acc)
		return
	}
	// Leading partial, whole buckets, trailing partial.
	if from.Before(alignedFrom) {
		e.foldRangeAt(level-1, from, alignedFrom, acc)
	}
	e.s.RangeLevel(level, alignedFrom, alignedTo, func(_ BucketKey, v T) bool {
		*acc = e.m.Combine(*acc, v)
		return true
	})
	if alignedTo.Before(to) {
		e.foldRangeAt(level-1, alignedTo, to, acc)
	}
}

// AsOf folds everything from `epoch` up to (excluding) the finest
// bucket containing t plus that bucket itself — i.e. the cumulative
// value as of the end of t's finest bucket. Callers wanting strict
// "before t" semantics pass to=t truncated to grain 0 via FoldRange
// directly; AsOf is the common inclusive read (bal: balance as of a
// posting instant includes the instant's bucket).
func (e *Engine[T]) AsOf(epoch, t time.Time) T {
	fineEnd := e.h.Grain(0).Truncate(t).Add(e.h.Grain(0).Width)
	return e.FoldRange(epoch, fineEnd)
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
