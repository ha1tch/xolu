// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package dxp

// store.go — ParticipantStore, its two concrete implementations, and
// Result. Full design rationale in docs/proposals/dxp-coordinator-
// design.md §§2-3, 10; implemented against that document directly,
// including one correction made to it in the same pass (§2's own
// TxnStores comment still referenced "canonical participant ordering"
// after that concept was dropped elsewhere in the same document —
// fixed there before writing this file, not carried forward here).

import (
	"context"

	"github.com/cockroachdb/pebble"
	"database/sql"
)

// ParticipantStore is the coordinator-supplied handle a participant's
// Execute writes through. Participants type-assert to their own
// engine-specific concrete type — the same pattern OpParams already
// uses (marker interface, concrete per adopter) — never see *sql.Tx
// or *pebble.Batch in the interface signature itself.
type ParticipantStore interface {
	Engine() string // diagnostic only — "sql", "pebble"

	// Ready is called BY the participant, from inside Execute, the
	// moment it is actually about to write — not before. This is what
	// starts the coordinator's guard for this participant; duration
	// is fixed coordinator-side at store construction, never visible
	// to or influenced by the participant. Only the participant knows
	// when its own internal work is actually done and it's about to
	// touch the store — only the coordinator owns how long it then
	// gets and what happens on timeout. Idempotent.
	Ready(ctx context.Context) error

	Commit(ctx context.Context) error
	Abort(ctx context.Context) error
}

// TxnStores holds one store per participant, indexed by the def's own
// participant array order — the order participants are listed in the
// def's JSON, nothing more (see this file's own package doc for the
// correction this reflects).
type TxnStores []ParticipantStore

// SQLStore wraps *sql.Tx, for bal/fsm/entity/cal-H1.
type SQLStore struct {
	Tx   *sql.Tx
	owns bool // false under collapse — multiple SQLStores may share one Tx; only the owner's Commit/Abort actually calls through

	ready bool
}

// NewSQLStore constructs a store that owns its own Tx's lifecycle —
// the non-collapsed case. Use NewSharedSQLStore for the collapsed
// case, where several participants' stores wrap the same underlying
// Tx and only one of them may actually call Commit/Rollback on it.
func NewSQLStore(tx *sql.Tx) *SQLStore {
	return &SQLStore{Tx: tx, owns: true}
}

// NewSharedSQLStore constructs a store wrapping a Tx it does not own —
// the collapsed-to-ACID case (§3). Commit/Abort become no-ops on a
// non-owning store; the coordinator commits the real, shared Tx itself,
// exactly once, never through any individual store's own Commit call.
func NewSharedSQLStore(tx *sql.Tx) *SQLStore {
	return &SQLStore{Tx: tx, owns: false}
}

func (s *SQLStore) Engine() string { return "sql" }

// Ready marks the store usable and starts the coordinator's guard for
// this participant. Idempotent — a second call is a no-op, not an
// error, matching Release's own idempotent contract elsewhere in this
// package.
func (s *SQLStore) Ready(ctx context.Context) error {
	s.ready = true
	return nil
}

func (s *SQLStore) Commit(ctx context.Context) error {
	if !s.owns {
		return nil
	}
	return s.Tx.Commit()
}

func (s *SQLStore) Abort(ctx context.Context) error {
	if !s.owns {
		return nil
	}
	return s.Tx.Rollback()
}

// PebbleStore wraps *pebble.Batch, for ts/cal-H3/any future Pebble
// participant. No collapse case exists for Pebble — not because @D06
// names engine type (it doesn't; its stated condition, checked
// directly against the framework's own source, is single-tenancy),
// but because *pebble.Batch has no representation inside *sql.Tx, so
// "collapse into one SQL transaction" mechanically cannot include it
// regardless of tenant scope. A Pebble store is always genuinely
// independent — there is no NewSharedPebbleStore, deliberately, to
// match.
type PebbleStore struct {
	Batch *pebble.Batch

	ready bool
}

// NewPebbleStore constructs a PebbleStore. Batch must already be
// open — native handles are passed already open, per this package's
// own design doc (§2): "open" and "usable" are deliberately two
// different states, and Ready() is what marks the transition to
// usable, not construction.
func NewPebbleStore(batch *pebble.Batch) *PebbleStore {
	return &PebbleStore{Batch: batch}
}

func (s *PebbleStore) Engine() string { return "pebble" }

func (s *PebbleStore) Ready(ctx context.Context) error {
	s.ready = true
	return nil
}

func (s *PebbleStore) Commit(ctx context.Context) error {
	return s.Batch.Commit(pebble.Sync)
}

func (s *PebbleStore) Abort(ctx context.Context) error {
	return s.Batch.Close()
}

// Result is the opaque JSON-shaped value a participant's Execute may
// return alongside success. Two independent consumers, never
// conflated (§10):
//   - Dependency-binding (§9, deferred, not built): only top-level
//     scalar fields, matching payload./query.'s own restriction in
//     pkg/fsm/eval exactly.
//   - Webhook/log delivery (unscoped, not built): the whole value,
//     structure intact, delivered only once the instance reaches a
//     terminal state.
// nil is valid — a participant with nothing worth reporting returns
// it; Result is opt-in per call, never mandatory.
type Result map[string]interface{}
