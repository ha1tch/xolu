// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_dxp_dispatch.go — the coordinator's actual orchestration (item
// 21): Reserve every participant, gate on full attendance, Validate,
// then Execute+Commit, ending in exactly one terminal state. Every
// piece this calls (ParticipantStore, markDxpTxnTerminal,
// decodeDxpParticipantParams, dxpParticipantRegistry) was built and
// tested independently first (T-88, T-93, T-95, T-96); this file is
// what finally calls them together. Full design rationale in
// docs/proposals/dxp-coordinator-design.md.
//
// dispatchDxpTxnCore vs. dispatchDxpTxn (T-103): the actual
// orchestration logic takes db/pebbleDB/cache/registry as explicit
// parameters rather than reaching into *Server/*http.Request —
// dispatchDxpTxn is now a thin wrapper that constructs the real ones
// and calls it. This is what makes deterministic adversarial testing
// possible at all. T-99's own race was found and fixed via a
// standalone reproduction entirely OUTSIDE this function, because
// there was no way to inject a controllable participant here — named
// explicitly as a remaining gap in T-99's own register entry. T-103
// closed it.
//
// Phased execution (T-105): the second, comparably-sized half of what
// "multi-substrate dxp" actually requires, alongside T-86's ts
// adapter. Collapsed and phased are genuinely different execution
// strategies, not one function with a flag — see dispatchCollapsed
// and dispatchPhased below, and each one's own doc for why they
// cannot share a barrier/commit strategy.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"

	"github.com/cockroachdb/pebble"
	"github.com/ha1tch/xolu/pkg/dxp"
	"github.com/ha1tch/xolu/pkg/tenant"
	"github.com/ha1tch/xolu/pkg/timeseries"
	"github.com/rs/zerolog"
)

// dxpDispatchResult is the final, terminal outcome of driving one
// dxp_txn instance through its phases — dxp-composed-commitment.md
// §4's own outcome-uniqueness guard, exactly one of the three real
// terminal states, never anything else.
type dxpDispatchResult struct {
	Status           string `json:"status"` // "committed", "released", or "expired"
	CommittedThrough int    `json:"committed_through"`
	Reason           string `json:"reason,omitempty"` // set when Status != "committed"
}

// dispatchDxpTxn is the thin, *Server/*http.Request-coupled wrapper:
// constructs the real registry/cache/db/pebbleDB and hands off to
// dispatchDxpTxnCore, which does the actual work. Kept as a separate,
// tiny function specifically so the HTTP call site
// (handleDxpTxnCreate) doesn't change at all — every existing caller
// and test keeps working unmodified.
//
// pebbleDB is resolved here only if "ts" is actually one of the
// snapshot's participants (mirroring dxpParticipantRegistry's own
// "only construct what's needed" principle) — a second call to
// s.tsManager.StoreFor beyond the one dxpParticipantRegistry already
// makes to build the ts adapter itself; a small, accepted duplication
// rather than entangling adapter construction with store-handle
// resolution in one function.
func (s *Server) dispatchDxpTxn(ctx context.Context, r *http.Request, tenantID tenant.TenantID,
	txnID int64, snapshot dxpTxnSnapshot, analysis dxpAnalysis, deadlineNs int64) (dxpDispatchResult, error) {

	needed := make(map[string]bool, len(snapshot.Participants))
	for _, ps := range snapshot.Participants {
		needed[ps.Primitive] = true
	}
	registry, _, err := s.dxpParticipantRegistry(r, needed)
	if err != nil {
		return dxpDispatchResult{}, err
	}
	cache := s.dxpMemCache(tenantID)
	db, _ := s.fsmDB(r)
	if db == nil {
		return dxpDispatchResult{}, fmt.Errorf("storage does not support v2 dxp")
	}

	var pebbleDB *pebble.DB
	if needed["ts"] {
		if s.tsManager == nil || !s.tsManager.IsProvisioned(tenantID) {
			return dxpDispatchResult{}, fmt.Errorf("timeseries is not available for this tenant")
		}
		tsStoreIface, err := s.tsManager.StoreFor(tenantID)
		if err != nil {
			return dxpDispatchResult{}, fmt.Errorf("ts store: %w", err)
		}
		pdp, ok := tsStoreIface.(timeseries.PebbleDBProvider)
		if !ok {
			return dxpDispatchResult{}, fmt.Errorf("ts store does not expose a pebble handle")
		}
		pebbleDB = pdp.PebbleDB()
	}

	var locDB *sql.DB
	if needed["loc"] {
		locSt, err := s.locStore(r)
		if err != nil {
			return dxpDispatchResult{}, fmt.Errorf("loc store: %w", err)
		}
		locDB = locSt.DB()
	}

	var objDB *sql.DB
	if needed["obj"] {
		objSt, err := s.objStore(r)
		if err != nil {
			return dxpDispatchResult{}, fmt.Errorf("obj store: %w", err)
		}
		objDB = objSt.DB()
	}

	return dispatchDxpTxnCore(ctx, db, pebbleDB, locDB, objDB, cache, registry, tenantID, txnID, snapshot, analysis, deadlineNs, s.logger)
}

// dispatchDxpTxnCore drives one dxp_txn instance through its phases
// against whatever registry it's handed — the real five adapters in
// production (via dispatchDxpTxn above), anything satisfying
// dxp.Participant in a test. Called synchronously, once, from within
// the same request that created the instance — dispatch is not a
// separate step (dxp-coordinator-design.md's own recorded correction:
// POST /dxp/txn is one complete, stateless invocation, closer to a
// stored procedure call than opening a database transaction, worked
// through directly after an initial wrong reading assumed otherwise).
//
// Attendance (§4): every participant must Reserve successfully before
// any Validate runs, and every Validate must succeed before any
// Execute runs — unanimous, matching plain 3PS's own formal
// definition exactly, checked directly against the canonical
// framework's dxp-11-proof-3ps.md before this was designed. Canonical
// participant ordering does not apply (§12 — Reserve never blocks, so
// sequential dispatch here is correct, not merely simple): each
// participant is Reserved in the def's own listed order, but nothing
// about correctness depends on that order.
//
// Collapse (@D06): the def's own analysis.CollapseEligible reflects
// @D06's actual, verbatim condition (single-tenant, trivially true
// for v1) — analysis.EngineHomogeneous is this codebase's own,
// separate inference (whether every participant is SQL-backed), kept
// distinct deliberately (§13's own recorded correction — conflating
// these two was a real mistake made once already). ts (T-86) is the
// first non-SQL participant, so EngineHomogeneous is now genuinely
// sometimes false — the phased path below is real, not dead code.
func dispatchDxpTxnCore(ctx context.Context, db *sql.DB, pebbleDB *pebble.DB, locDB *sql.DB, objDB *sql.DB, cache *dxp.MemCache,
	registry map[string]dxp.Participant, tenantID tenant.TenantID,
	txnID int64, snapshot dxpTxnSnapshot, analysis dxpAnalysis, deadlineNs int64,
	logger zerolog.Logger) (dxpDispatchResult, error) {

	txn := strconv.FormatInt(txnID, 10)
	tenantKey := tenantID.String()

	var reserved []dxpReservedParticipant

	// terminal releases every claim held so far, records the outcome
	// via markDxpTxnTerminal, and returns. Used for both pre-
	// attendance refusals (Reserve/Validate failures — nothing was
	// ever Executed, so committedThrough is always 0 here) and is NOT
	// used post-Execute; the collapsed/phased paths below handle their
	// own termination directly, since they need a committedThrough
	// value this closure has no way to know.
	terminal := func(status, reason string) (dxpDispatchResult, error) {
		for _, d := range reserved {
			_ = d.P.Release(ctx, d.Claim)
		}
		cache.Lock(tenantKey)
		cache.ReleaseTxn(tenantKey, txn)
		cache.Unlock(tenantKey)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return dxpDispatchResult{}, err
		}
		if _, err := markDxpTxnTerminal(ctx, tx, tenantID, txnID, status, 0); err != nil {
			_ = tx.Rollback()
			return dxpDispatchResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return dxpDispatchResult{}, err
		}
		return dxpDispatchResult{Status: status, Reason: reason}, nil
	}

	// Phase 1: Reserve every participant, in the def's own listed
	// order (not a canonical one — see this function's own doc).
	for _, ps := range snapshot.Participants {
		p, ok := registry[ps.Primitive]
		if !ok {
			return terminal("released", fmt.Sprintf("no participant registered for primitive %q", ps.Primitive))
		}
		paramsJSON, err := json.Marshal(ps.Params)
		if err != nil {
			return terminal("released", err.Error())
		}
		op, err := decodeDxpParticipantParams(ps.Primitive, ps.Op, paramsJSON, tenantID)
		if err != nil {
			return terminal("released", err.Error())
		}
		claim, err := p.Reserve(ctx, tenantKey, op, txn, ps.ID, deadlineNs, dxp.Pessimistic)
		if err != nil {
			return terminal("released", fmt.Sprintf("participant %q: %v", ps.ID, err))
		}
		reserved = append(reserved, dxpReservedParticipant{Spec: ps, P: p, Claim: claim})
	}

	// Attendance established — all N Reserved. Phase 2: Validate all.
	for _, d := range reserved {
		if err := d.P.Validate(ctx, d.Claim); err != nil {
			return terminal("released", fmt.Sprintf("participant %q: %v", d.Spec.ID, err))
		}
	}

	// Phase 3: Execute + Commit. Collapse eligibility and engine
	// homogeneity are checked SEPARATELY and deliberately kept
	// distinct (§13's own recorded correction) — collapse requires
	// both.
	if analysis.CollapseEligible && analysis.EngineHomogeneous {
		return dispatchCollapsed(ctx, db, cache, tenantKey, txn, tenantID, txnID, reserved, logger)
	}
	if pebbleDB == nil {
		for _, d := range reserved {
			if dxpEngineOf[d.Spec.Primitive] == "pebble" {
				return terminal("released", fmt.Sprintf(
					"participant %q needs a pebble-backed store but none was supplied", d.Spec.ID))
			}
		}
	}
	if locDB == nil {
		for _, d := range reserved {
			if dxpEngineOf[d.Spec.Primitive] == "sql-loc" {
				return terminal("released", fmt.Sprintf(
					"participant %q needs loc's own store but none was supplied", d.Spec.ID))
			}
		}
	}
	if objDB == nil {
		for _, d := range reserved {
			if dxpEngineOf[d.Spec.Primitive] == "sql-obj" {
				return terminal("released", fmt.Sprintf(
					"participant %q needs obj's own store but none was supplied", d.Spec.ID))
			}
		}
	}
	return dispatchPhased(ctx, db, pebbleDB, locDB, objDB, cache, tenantKey, txn, tenantID, txnID, reserved, logger)
}

// dxpReservedParticipant is the shape both dispatchCollapsed and
// dispatchPhased operate on — pulled out of dispatchDxpTxnCore's own
// local "dispatched" type so both functions can share one signature
// without a package-level type collision with that unexported local.
type dxpReservedParticipant struct {
	Spec  dxpParticipantSpec
	P     dxp.Participant
	Claim dxp.Claim
}

// releaseAll calls Release on every reserved participant — used on
// every path that does NOT end with the instance genuinely committed,
// including participants whose own Execute already succeeded in the
// phased case (their SQL/Pebble row is real and stays, per §6's own
// accepted-torn-commit stance; Release only abandons LOCAL bookkeeping
// like cal's pending map, never the already-landed write). Best-effort
// and logged, never fails the response — by the time this runs the
// instance's own terminal status is the authoritative outcome.
func releaseAll(ctx context.Context, reserved []dxpReservedParticipant, logger zerolog.Logger) {
	for _, d := range reserved {
		if err := d.P.Release(ctx, d.Claim); err != nil {
			logger.Warn().Err(err).Str("participant", d.Spec.ID).Str("primitive", d.Spec.Primitive).
				Msg("dxp: Release failed (best-effort, instance outcome unaffected)")
		}
	}
}

// postCommitAll calls PostCommit on every reserved participant — only
// ever reached after the instance is confirmed genuinely, durably
// committed (T-108: the signal dxp.Participant's own doc, T-83, and
// cal's adapter comment all named as missing). Best-effort and
// logged, never fails the response: a PostCommit failure means a
// derived/advisory plane (cal's H3 today) is stale, not that the
// commit itself was wrong — there is nothing left to roll back by the
// time this runs.
func postCommitAll(ctx context.Context, reserved []dxpReservedParticipant, logger zerolog.Logger) {
	for _, d := range reserved {
		if err := d.P.PostCommit(ctx, d.Claim); err != nil {
			logger.Warn().Err(err).Str("participant", d.Spec.ID).Str("primitive", d.Spec.Primitive).
				Msg("dxp: PostCommit failed (best-effort, instance stays committed; a derived/advisory plane may be stale)")
		}
	}
}

// dispatchCollapsed executes every reserved participant against one
// shared *sql.Tx — the single-tenant, all-SQL case (@D06). Every
// participant Executes concurrently against the shared Tx, but no
// goroutine calls Commit or Abort on its own — T-99's own finding,
// fixed there and preserved here exactly: the previous shape let the
// owning (i==0) store's real Commit() fire the moment ITS OWN
// goroutine finished Execute, with no barrier waiting for siblings
// still mid-Execute — confirmed by direct reproduction against
// modernc.org/sqlite to actually tear the shared Tx. Concurrent
// Exec/Query calls against one *sql.Tx serialise safely on their own
// (database/sql's own documented guarantee for concurrent Tx use); it
// was only Commit racing a sibling's still-in-flight Exec that broke
// atomicity, so Commit is sequenced strictly after a wg.Wait barrier,
// never racing any Execute.
func dispatchCollapsed(ctx context.Context, db *sql.DB, cache *dxp.MemCache,
	tenantKey, txn string, tenantID tenant.TenantID, txnID int64,
	reserved []dxpReservedParticipant, logger zerolog.Logger) (dxpDispatchResult, error) {

	sharedTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return dxpDispatchResult{}, err
	}
	txCommitted := false
	defer func() {
		if !txCommitted {
			_ = sharedTx.Rollback()
		}
	}()

	type executed struct {
		spec dxpParticipantSpec
		err  error
	}
	results := make([]executed, len(reserved))
	var wg sync.WaitGroup
	var ownerStore *dxp.SQLStore // the one store whose Commit touches the real Tx — i==0, captured for the barrier below

	// Per-write-target locks (T-168, corrected 2026-08-05 after the
	// first attempt fixed dispatchPhased only -- this test's own def
	// (5 bal legs, single tenant, all-SQL) is CollapseEligible &&
	// EngineHomogeneous, so it never went through dispatchPhased at
	// all; confirmed directly by tracing the actual routing condition
	// in dispatchDxpTxnCore before writing this, not assumed from the
	// def's own "pattern" field, which does not determine collapse
	// eligibility). All five participants here share ONE *sql.Tx, not
	// five independent ones -- but the same race applies: each
	// goroutine still calls Execute (and therefore bal's own
	// executionInstant) independently, and *sql.Tx's own concurrency
	// safety guarantees nothing about the ORDER concurrent statements
	// against the same transaction are actually processed in. See
	// dxp.Claim.WriteTargets's own doc comment for the full mechanism.
	targetLocks := map[string]*sync.Mutex{}
	for _, d := range reserved {
		for _, target := range writeTargets(d.Claim) {
			if _, ok := targetLocks[target]; !ok {
				targetLocks[target] = &sync.Mutex{}
			}
		}
	}

	// Writing to results[i] here needs no mutex: each goroutine
	// touches only its own index, and wg.Wait() below establishes
	// happens-before for every read of results and ownerStore that
	// follows it.
	for i, d := range reserved {
		wg.Add(1)
		go func(i int, d dxpReservedParticipant) {
			defer wg.Done()

			// Locked from immediately before Execute (where a
			// participant's own write-ordering-sensitive timestamp
			// gets generated) through Execute's own return -- not
			// through a per-goroutine commit, since this path has
			// none; the one real commit happens once, later, after
			// every goroutine here has already finished. Sorted for
			// the same deadlock-avoidance reason as dispatchPhased.
			targets := sortedUniqueTargets(writeTargets(d.Claim))
			for _, t := range targets {
				targetLocks[t].Lock()
			}

			var store *dxp.SQLStore
			if i == 0 {
				store = dxp.NewSQLStore(sharedTx)
				ownerStore = store
			} else {
				store = dxp.NewSharedSQLStore(sharedTx)
			}
			if _, execErr := d.P.Execute(ctx, store, d.Claim); execErr != nil {
				results[i] = executed{spec: d.Spec, err: execErr}
			}

			for _, t := range targets {
				targetLocks[t].Unlock()
			}
		}(i, d)
	}
	wg.Wait()

	// The barrier. Every participant has now either executed
	// successfully or failed — nothing is still in flight — so it is
	// now safe to decide Commit vs. Rollback for the one real Tx. A
	// consequence worth naming: for the collapsed path,
	// committedThrough is always exactly 0 (any participant failed,
	// nothing was ever committed) or len(reserved) (all participants
	// executed, the one real commit succeeded) — never a partial
	// count. That is a strictly stronger guarantee than before T-99's
	// fix, not merely a side effect of it.
	for _, res := range results {
		if res.err != nil {
			// Nothing has been committed — the barrier above guarantees
			// every Execute finished before we ever got here, and no
			// goroutine calls Commit. sharedTx must be explicitly closed
			// HERE, before opening tx2 below — T-104's own finding, fixed:
			// the writer pool is MaxOpenConns=1 (pkg/storage/sqlite.go's
			// own documented single-writer WAL discipline), and sharedTx
			// is still genuinely open at this point. Deferring the
			// rollback to this function's own exit would try to open tx2
			// on a pool with zero free connections and deadlock forever;
			// confirmed directly, not theorised.
			_ = sharedTx.Rollback()
			txCommitted = true // the deferred rollback above is now a safe no-op
			cache.Lock(tenantKey)
			cache.ReleaseTxn(tenantKey, txn)
			cache.Unlock(tenantKey)
			releaseAll(ctx, reserved, logger)
			tx2, err2 := db.BeginTx(ctx, nil)
			if err2 != nil {
				return dxpDispatchResult{}, err2
			}
			if _, err2 := markDxpTxnTerminal(ctx, tx2, tenantID, txnID, "expired", 0); err2 != nil {
				_ = tx2.Rollback()
				return dxpDispatchResult{}, err2
			}
			if err2 := tx2.Commit(); err2 != nil {
				return dxpDispatchResult{}, err2
			}
			return dxpDispatchResult{Status: "expired", CommittedThrough: 0,
				Reason: fmt.Sprintf("participant %q: %v", res.spec.ID, res.err)}, nil
		}
	}

	// Every participant executed successfully and nothing is still in
	// flight — safe to commit the one real Tx now, sequenced after the
	// barrier. Every other participant's store.Commit is a documented
	// no-op (owns == false); only ownerStore's touches sharedTx.
	if commitErr := ownerStore.Commit(ctx); commitErr != nil {
		// Same defensive close as the branch above (T-104): Tx.Commit
		// failing typically already releases the pool connection on its
		// own, but relying on that precisely is fragile.
		_ = sharedTx.Rollback()
		txCommitted = true
		cache.Lock(tenantKey)
		cache.ReleaseTxn(tenantKey, txn)
		cache.Unlock(tenantKey)
		releaseAll(ctx, reserved, logger)
		tx2, err2 := db.BeginTx(ctx, nil)
		if err2 != nil {
			return dxpDispatchResult{}, err2
		}
		if _, err2 := markDxpTxnTerminal(ctx, tx2, tenantID, txnID, "expired", 0); err2 != nil {
			_ = tx2.Rollback()
			return dxpDispatchResult{}, err2
		}
		if err2 := tx2.Commit(); err2 != nil {
			return dxpDispatchResult{}, err2
		}
		return dxpDispatchResult{Status: "expired", CommittedThrough: 0,
			Reason: fmt.Sprintf("commit failed: %v", commitErr)}, nil
	}
	txCommitted = true
	committedThrough := len(reserved)
	cache.Lock(tenantKey)
	cache.ConfirmTxn(tenantKey, txn)
	cache.Unlock(tenantKey)

	tx3, err := db.BeginTx(ctx, nil)
	if err != nil {
		return dxpDispatchResult{}, err
	}
	ok, err := markDxpTxnTerminal(ctx, tx3, tenantID, txnID, "committed", committedThrough)
	if err != nil {
		_ = tx3.Rollback()
		return dxpDispatchResult{}, err
	}
	if !ok {
		_ = tx3.Rollback()
		return dxpDispatchResult{}, fmt.Errorf("instance %d was not active at commit time (unexpected)", txnID)
	}
	if err := tx3.Commit(); err != nil {
		return dxpDispatchResult{}, err
	}

	// Instance is now genuinely, durably committed — the one place
	// PostCommit is allowed to fire (T-108).
	postCommitAll(ctx, reserved, logger)

	return dxpDispatchResult{Status: "committed", CommittedThrough: committedThrough}, nil
}

// dispatchPhased executes every reserved participant against its OWN,
// genuinely independent store — dxp-coordinator-design.md §2-3: an
// SQLStore per SQL participant (always owns:true — no sharing, unlike
// the collapsed case, since there is no single Tx to share), a
// PebbleStore per Pebble participant (only "ts" today), each backed
// by its own fresh *sql.Tx or *pebble.Batch.
//
// Unlike dispatchCollapsed, concurrent Execute+Commit PER PARTICIPANT
// (no barrier between them) is safe here BY CONSTRUCTION, not despite
// the lesson T-99/T-104 taught: there is no shared resource for one
// participant's real commit to race against a sibling's still-in-
// flight Execute, because every store is independently owned. Each
// goroutine below does Execute-then-Commit as one atomic step from
// the coordinator's own point of view, exactly the shape T-99 proved
// unsafe for the collapsed case specifically because that case's
// stores were NOT independent.
//
// A torn commit (participant A's commit succeeds, durably;
// participant B's then fails) is a genuine, ACCEPTED risk here, not a
// bug — dxp-coordinator-design.md §6: "tombstone the instances that
// don't pan out, collect garbage periodically. Not prevented —
// detected, marked, cleaned up." committed_through is therefore a
// REAL partial count for this path, unlike the collapsed path's
// always-0-or-N guarantee — and T-100's own sweep is exactly the
// cleanup mechanism this acceptance already relies on.
func dispatchPhased(ctx context.Context, db *sql.DB, pebbleDB *pebble.DB, locDB *sql.DB, objDB *sql.DB, cache *dxp.MemCache,
	tenantKey, txn string, tenantID tenant.TenantID, txnID int64,
	reserved []dxpReservedParticipant, logger zerolog.Logger) (dxpDispatchResult, error) {

	type executed struct {
		spec      dxpParticipantSpec
		committed bool
		err       error
	}
	results := make([]executed, len(reserved))
	var wg sync.WaitGroup

	// Per-write-target locks (T-168): two participants that write to
	// the same underlying account/resource must be serialized against
	// each other, not merely reserved-conflict-checked. Built
	// single-threaded, entirely before any goroutine is spawned below
	// -- no map-protecting mutex needed, since nothing else touches
	// targetLocks until every entry already exists.
	targetLocks := map[string]*sync.Mutex{}
	for _, d := range reserved {
		for _, target := range writeTargets(d.Claim) {
			if _, ok := targetLocks[target]; !ok {
				targetLocks[target] = &sync.Mutex{}
			}
		}
	}

	for i, d := range reserved {
		wg.Add(1)
		go func(i int, d dxpReservedParticipant) {
			defer wg.Done()

			// Locked from here -- immediately before Execute, where a
			// participant's own write-ordering-sensitive timestamp (if
			// it has one; bal's own executionInstant is the case this
			// exists for) gets generated -- through Commit/Abort below.
			// Sorted so two participants whose target sets overlap but
			// aren't identical (e.g. A writes {x,y}, B writes {y,z})
			// always acquire in the same global order, never a
			// circular wait. Established before any commit happens,
			// not a retry after a rejected one.
			targets := sortedUniqueTargets(writeTargets(d.Claim))
			for _, t := range targets {
				targetLocks[t].Lock()
			}
			defer func() {
				for _, t := range targets {
					targetLocks[t].Unlock()
				}
			}()

			var store dxp.ParticipantStore
			switch dxpEngineOf[d.Spec.Primitive] {
			case "sql":
				tx, err := db.BeginTx(ctx, nil)
				if err != nil {
					results[i] = executed{spec: d.Spec, err: err}
					return
				}
				store = dxp.NewSQLStore(tx)
			case "sql-loc":
				tx, err := locDB.BeginTx(ctx, nil)
				if err != nil {
					results[i] = executed{spec: d.Spec, err: err}
					return
				}
				store = dxp.NewSQLStore(tx)
			case "sql-obj":
				tx, err := objDB.BeginTx(ctx, nil)
				if err != nil {
					results[i] = executed{spec: d.Spec, err: err}
					return
				}
				store = dxp.NewSQLStore(tx)
			case "pebble":
				store = dxp.NewPebbleStore(pebbleDB.NewBatch())
			default:
				results[i] = executed{spec: d.Spec, err: fmt.Errorf("no engine registered for primitive %q", d.Spec.Primitive)}
				return
			}

			if _, execErr := d.P.Execute(ctx, store, d.Claim); execErr != nil {
				_ = store.Abort(ctx)
				results[i] = executed{spec: d.Spec, err: execErr}
				return
			}
			if commitErr := store.Commit(ctx); commitErr != nil {
				results[i] = executed{spec: d.Spec, err: commitErr}
				return
			}
			results[i] = executed{spec: d.Spec, committed: true}
		}(i, d)
	}
	wg.Wait()

	committedThrough := 0
	var firstFailure *executed
	for i := range results {
		if results[i].committed {
			committedThrough++
		} else if firstFailure == nil {
			firstFailure = &results[i]
		}
	}

	if firstFailure != nil {
		// Torn or refused — an accepted risk for the phased path (§6),
		// not a bug: committedThrough here is a genuine partial count,
		// reflecting exactly how many participants' independent commits
		// actually succeeded before the instance is marked expired.
		cache.Lock(tenantKey)
		cache.ReleaseTxn(tenantKey, txn)
		cache.Unlock(tenantKey)
		releaseAll(ctx, reserved, logger)
		tx2, err2 := db.BeginTx(ctx, nil)
		if err2 != nil {
			return dxpDispatchResult{}, err2
		}
		if _, err2 := markDxpTxnTerminal(ctx, tx2, tenantID, txnID, "expired", committedThrough); err2 != nil {
			_ = tx2.Rollback()
			return dxpDispatchResult{}, err2
		}
		if err2 := tx2.Commit(); err2 != nil {
			return dxpDispatchResult{}, err2
		}
		return dxpDispatchResult{Status: "expired", CommittedThrough: committedThrough,
			Reason: fmt.Sprintf("participant %q: %v", firstFailure.spec.ID, firstFailure.err)}, nil
	}

	cache.Lock(tenantKey)
	cache.ConfirmTxn(tenantKey, txn)
	cache.Unlock(tenantKey)
	tx3, err := db.BeginTx(ctx, nil)
	if err != nil {
		return dxpDispatchResult{}, err
	}
	ok, err := markDxpTxnTerminal(ctx, tx3, tenantID, txnID, "committed", committedThrough)
	if err != nil {
		_ = tx3.Rollback()
		return dxpDispatchResult{}, err
	}
	if !ok {
		_ = tx3.Rollback()
		return dxpDispatchResult{}, fmt.Errorf("instance %d was not active at commit time (unexpected)", txnID)
	}
	if err := tx3.Commit(); err != nil {
		return dxpDispatchResult{}, err
	}

	// Instance is now genuinely, durably committed — the one place
	// PostCommit is allowed to fire (T-108).
	postCommitAll(ctx, reserved, logger)

	return dxpDispatchResult{Status: "committed", CommittedThrough: committedThrough}, nil
}

// writeTargets returns the set of identifiers a participant's Execute
// will actually write to -- c.WriteTargets if the adapter populated
// it, falling back to []string{c.Resource} otherwise. See
// dxp.Claim.WriteTargets's own doc comment for the full story (T-168).
func writeTargets(c dxp.Claim) []string {
	if len(c.WriteTargets) > 0 {
		return c.WriteTargets
	}
	return []string{c.Resource}
}

// sortedUniqueTargets sorts and deduplicates targets -- sorted so every
// goroutine locking an overlapping-but-not-identical target set always
// acquires in the same global order (never a circular wait); deduped
// so a participant whose own WriteTargets happens to repeat an entry
// never double-locks (and later double-unlocks) the same mutex.
func sortedUniqueTargets(targets []string) []string {
	sorted := append([]string(nil), targets...)
	sort.Strings(sorted)
	out := sorted[:0]
	var prev string
	for i, t := range sorted {
		if i == 0 || t != prev {
			out = append(out, t)
			prev = t
		}
	}
	return out
}
