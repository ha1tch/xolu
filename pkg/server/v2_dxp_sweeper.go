// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_dxp_sweeper.go — DxpSweeper, gc.Sweeper for dxp_txn instances
// stuck in 'active' past their own deadline (T-100), and for purging
// terminal instances past a configurable retention window (T-102,
// direct instruction 2026-07-31: "keep tombstones for a configurable
// period... defaults to 48 hours before they're gone").
//
// pkg/dxp's own package doc names the first half exactly: "the
// mount-time tombstone pass this package does not itself implement —
// that is the dxp coordinator's concern, item 21." dxp-coordinator-
// design.md §6 independently states the same expectation from the
// other direction: "the sweep worker that already exists (item 18,
// T-54) picks it up as ordinary expired — no new state, no new
// subsystem." Neither claim was true until this file: T-54's own
// sweep (pkg/dxp.Janitor) only trims lapsed claims from the in-memory
// reservation cache — hygiene, explicitly documented as "never
// correctness" — and nothing else was registered against dxp_txn rows
// at all. Confirmed directly by reading every s.gcWorkers registration
// in server.go before writing this: ts-retention, blob-gc, meta-gc.
// No dxp entry existed.
//
// Ordinary dispatch is synchronous (POST /dxp/txn drives Reserve
// through markDxpTxnTerminal within one request) and marks its own
// terminal state before the response is written. A row only remains
// 'active' past its own deadline_ns if that never happened — a
// process crash, or an unrecovered panic, between the initial
// snapshot insert (handleDxpTxnCreate) and dispatchDxpTxn's own
// markDxpTxnTerminal call. This sweeper is what finally notices.
//
// What the expiry half deliberately does NOT attempt: determining
// whether a swept instance's participants actually committed before
// whatever interrupted it. A crash between the collapsed path's one
// real Tx.Commit and dispatchDxpTxn's own separate markDxpTxnTerminal
// write is a real, narrow window this substrate's own accepted
// durability stance (dxp-coordinator-design.md §6-7) already
// tolerates — "the instance's own record never claims success it
// didn't achieve" — not something this sweeper resolves by querying
// participants directly. Marking such a row 'expired' with
// committed_through=0 is the honest default (never falsely claim
// committed), not a claim that nothing happened.
//
// Note on scope, not yet built: dxp-reservation-cache.md §11-13
// specifies a genuinely distinct 'abandoned' terminal state, written
// only by a mount-time startup pass, operationally meaningful because
// a spike of it (unlike ordinary 'expired') is an incident signature.
// This sweeper does not implement that distinction — direct
// instruction was to add configurable tombstone retention instead;
// the mount-time pass remains its own, separate, not-yet-scoped piece
// of work.

import (
	"context"
	"database/sql"
	"time"

	gcpkg "github.com/ha1tch/xolu/pkg/gc"
)

// DxpSweeper implements gc.Sweeper. Each sweep does two things:
//
//  1. Marks dxp_txn rows still 'active' past their own deadline_ns as
//     'expired', guarded by the same WHERE status = 'active' CAS
//     discipline markDxpTxnTerminal uses — a row a genuinely in-flight
//     dispatch is about to terminate itself races the sweep on the
//     deadline boundary only in the narrow, benign sense that
//     whichever UPDATE matches first wins; the other affects zero rows.
//  2. Purges terminal rows (committed/released/expired) older than
//     retentionSecs, measured from created_at. retentionSecs <= 0
//     disables purging entirely — tombstones kept forever — matching
//     how this codebase's other retention configs (BlobGCGracePeriodSecs)
//     already treat non-positive as "off," not "purge immediately."
type DxpSweeper struct {
	db            *sql.DB
	retentionSecs int
}

// NewDxpSweeper creates a DxpSweeper backed by the given database.
// retentionSecs is how long a terminal instance is kept before this
// same sweep purges it; see DxpTxnRetentionSecs's own config doc.
func NewDxpSweeper(db *sql.DB, retentionSecs int) *DxpSweeper {
	return &DxpSweeper{db: db, retentionSecs: retentionSecs}
}

// Sweep marks stuck-active instances expired, then purges terminal
// instances past retention — across every tenant in one pair of
// queries each: dxp_txn is a single global table with tenant_id as a
// column, not a per-tenant-prefixed one, matching MetaSweeper's own
// entity_meta sweep shape exactly.
func (d *DxpSweeper) Sweep(ctx context.Context) (gcpkg.Report, error) {
	now := time.Now().UTC()

	res, err := d.db.ExecContext(ctx,
		`UPDATE dxp_txn SET status = 'expired', committed_through = 0
		 WHERE status = 'active' AND deadline_ns < ?`, now.UnixNano())
	if err != nil {
		return gcpkg.Report{Errors: 1}, err
	}
	expiredN, _ := res.RowsAffected()
	report := gcpkg.Report{
		Examined:  int(expiredN), // examined = candidates found (no separate pre-scan; matches MetaSweeper)
		Collected: int(expiredN),
	}

	if d.retentionSecs <= 0 {
		return report, nil
	}
	cutoff := now.Add(-time.Duration(d.retentionSecs) * time.Second)
	res2, err := d.db.ExecContext(ctx,
		`DELETE FROM dxp_txn
		 WHERE status IN ('committed', 'released', 'expired')
		   AND created_at < ?`, cutoff)
	if err != nil {
		report.Errors++
		return report, err
	}
	purgedN, _ := res2.RowsAffected()
	report.Examined += int(purgedN)
	report.Collected += int(purgedN)
	return report, nil
}
