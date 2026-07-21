// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// ri_strategy.go — three switchable strategies for closing the
// referential-integrity restrict race (G-12), selected by
// config.RIStrategy so CI can arbitrate them on real multi-core silicon.
//
// The anomaly: a delete of a target races a create that references it.
// The handler's in-memory pre-check (restrictReferrers, reading the
// FlatGraph) can return "not referenced" because the concurrent create's
// post-commit updateGraph has not landed yet; the delete then proceeds.
// The store-level in-transaction check is the real backstop, but under
// WAL snapshot isolation with the adaptive throughput lock disengaged,
// the delete's referrer read and the create's target read can each
// observe a snapshot taken before the other committed — classic
// write-skew, which snapshot isolation does not prevent. Result: a post
// pointing at a deleted user, ~1/8 on multi-core, never on single-core.
//
// Strategies:
//
//   "serialize"      — RI-relevant writes take one process mutex (RILock)
//                      so the delete and the referencing create cannot
//                      run concurrently. The in-tx check remains the
//                      authority; serialisation removes the interleaving
//                      that let two stale snapshots both win.
//
//   "intx-only"      — the in-memory pre-check becomes advisory only: it
//                      may report a violation early (fast refusal) but is
//                      NEVER trusted to permit a delete. Every delete of
//                      a restrict-referenced entity goes through the
//                      store's in-transaction check, which sees only
//                      committed rows. Relies on SQL commit ordering plus
//                      busy-retry; no process lock.
//
//   "serialize-intx" — both (default). Serialise AND never trust the
//                      pre-check to permit. Belt and braces.
//
// All three keep the fast-refusal path: if the pre-check positively finds
// a referrer, the delete is refused immediately in every strategy — that
// direction is safe (a committed referrer cannot un-commit).

package server

import (
	"context"
	"net/http"

	"github.com/ha1tch/xolu/pkg/models"
)

// riSerializer is the store capability for the serialise strategies.
type riSerializer interface {
	RILock()
	RIUnlock()
}

// riNoLockDeleter / riNoLockCreator are the lock-free variants a caller
// holding RILock must use to avoid re-locking the same mutex.
type riNoLockDeleter interface {
	DeleteWithRestrictNoLock(ctx context.Context, entity string, id int, restrictedBy []string) error
}
type riLockedDeleter interface {
	DeleteWithRestrict(ctx context.Context, entity string, id int, restrictedBy []string) error
}
type riNoLockCreator interface {
	CreateNoLock(ctx context.Context, entity string, data map[string]interface{}) (int, error)
}

func (s *Server) riSerialize() bool {
	return s.config.RIStrategy == "serialize" || s.config.RIStrategy == "serialize-intx"
}

// deleteWithRIStrategy performs a restrict-aware delete under the active
// strategy. It assumes the caller has NOT already refused via a positive
// pre-check. restrictedBy is the set of entities that restrict-reference
// `entity` (empty ⇒ no restrict policy ⇒ a plain delete is safe).
func (s *Server) deleteWithRIStrategy(ctx context.Context, store storeDeleter, entity string, id int, restrictedBy []string) error {
	// No restrict policy: nothing to serialise, plain delete.
	if len(restrictedBy) == 0 {
		return store.Delete(ctx, entity, id)
	}

	serialize := s.riSerialize()
	if serialize {
		if ser, ok := store.(riSerializer); ok {
			ser.RILock()
			defer ser.RIUnlock()
			if nd, ok := store.(riNoLockDeleter); ok {
				return nd.DeleteWithRestrictNoLock(ctx, entity, id, restrictedBy)
			}
		}
	}
	// Non-serialised (or store without the capability): the locked
	// in-tx delete is the authority.
	if ld, ok := store.(riLockedDeleter); ok {
		return ld.DeleteWithRestrict(ctx, entity, id, restrictedBy)
	}
	// Store has no in-tx restrict support at all: fall back to plain
	// delete (RI cannot be enforced at the store; the pre-check was the
	// only line and it did not block).
	return store.Delete(ctx, entity, id)
}

// storeDeleter is the minimal delete surface used above.
type storeDeleter interface {
	Delete(ctx context.Context, entity string, id int) error
}

// createWithRIStrategy performs a create under the active strategy. When
// serialising and the payload carries REF edges, it holds RILock around a
// lock-free create so the create cannot interleave with a concurrent
// delete of a target. Otherwise it is an ordinary create.
func (s *Server) createWithRIStrategy(ctx context.Context, store storeCreator, entity string, data map[string]interface{}, hasRefs bool) (int, error) {
	if hasRefs && s.riSerialize() {
		if ser, ok := store.(riSerializer); ok {
			if nc, ok := store.(riNoLockCreator); ok {
				ser.RILock()
				defer ser.RIUnlock()
				return nc.CreateNoLock(ctx, entity, data)
			}
		}
	}
	return store.Create(ctx, entity, data)
}

type storeCreator interface {
	Create(ctx context.Context, entity string, data map[string]interface{}) (int, error)
}

// serverPayloadHasRefs reports whether a create/update payload carries
// any REF field — the cheap gate deciding RI serialisation for writes.
func serverPayloadHasRefs(data map[string]interface{}) bool {
	edges, err := models.ExtractEntityEdges(data)
	return err == nil && len(edges) > 0
}

// httpStatusForRIRefusal is a small shared helper so the delete handler
// and any future RI path map refusals identically.
func httpStatusForRIRefusal() int { return http.StatusConflict }
