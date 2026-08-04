// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package reserved

import (
	"context"
	"database/sql"
	"fmt"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// Querier is the subset of database/sql shared by *sql.DB and *sql.Tx.
// Every transition in this file takes a Querier so that participants can
// run it inside their own transactions — the guard-locality law (@C04a):
// a guarded transition's read and write commit together.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

// ConventionColumns returns the three column definitions of the
// tentative-row convention, for embedding in a participant table's
// CREATE TABLE. The state default is 'committed' so that every row
// written outside the reserve path is real by construction — adopting
// the convention changes nothing for existing writers.
func ConventionColumns() string {
	return `txn_id           TEXT     NULL,
	state            TEXT     NOT NULL DEFAULT 'committed',
	reserve_deadline INTEGER  NULL`
}

// ConventionIndexes returns CREATE INDEX statements for a participant
// table: a partial index over reserved rows by txn_id (the CAS
// transitions' access path) and one by deadline (the sweeper's).
// prefix follows the house table-prefix pattern; table is the full
// table name.
func ConventionIndexes(prefix, table string) string {
	return fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_%[1]sreserved_txn
		ON %[2]s(txn_id) WHERE state = 'reserved';
	CREATE INDEX IF NOT EXISTS idx_%[1]sreserved_deadline
		ON %[2]s(reserve_deadline) WHERE state = 'reserved';`, prefix, table)
}

// GuardPredicate returns the SQL fragment a guard-plane read appends to
// its WHERE clause, per the participant's declared weight. The caller
// binds nowNano (unix nanoseconds) once for the pessimistic form; the
// optimistic form takes no bind.
//
// Pessimistic: committed rows plus reserved rows still inside their
// window — a live reservation is honoured like a commit. The deadline
// comparison is inline because the deadline is authoritative: a lapsed
// reservation stops counting here, immediately, sweeper or no sweeper.
//
// Optimistic: committed rows only — reservations are invisible to
// admission, and conflicts resolve at confirmation (first confirmer
// wins).
func GuardPredicate(w Weight) (fragment string, binds int) {
	switch w {
	case Pessimistic:
		return `(state = 'committed' OR (state = 'reserved' AND reserve_deadline > ?))`, 1
	default:
		return `state = 'committed'`, 0
	}
}

// ApplicationPredicate returns the SQL fragment for application (non-
// guard) reads: committed rows only, unconditionally. Application reads
// never see reservations regardless of weight (@D05b visibility rule).
func ApplicationPredicate() string {
	return `state = 'committed'`
}

// PredicateFor returns the read predicate for a visibility tier
// (@D05c). TierGuard defers to the weight; TierAdvisory with a
// pessimistic weight may ingest live reservations (and thereby joins
// the sweeper's cleanup obligations); TierAnalytic is commit-fed,
// strictly.
func PredicateFor(tier VisibilityTier, w Weight) (fragment string, binds int) {
	switch tier {
	case TierGuard:
		return GuardPredicate(w)
	case TierAdvisory:
		if w == Pessimistic {
			return GuardPredicate(Pessimistic)
		}
		return ApplicationPredicate(), 0
	default:
		return ApplicationPredicate(), 0
	}
}

// ReserveValues returns the column values a participant's reserve-path
// INSERT binds for the convention columns: (txn_id, state,
// reserve_deadline). The INSERT itself belongs to the participant — the
// facility does not know its table shape — but the tentative marking is
// uniform.
func ReserveValues(txnID string, deadlineNano int64) (string, State, int64) {
	return txnID, StateReserved, deadlineNano
}

// Confirm CAS-flips every live reserved row owned by txnID in table to
// committed:
//
//	UPDATE <table> SET state='committed', reserve_deadline=NULL
//	 WHERE txn_id=? AND state='reserved' AND reserve_deadline > <now>
//
// txn_id is retained on the committed rows, which is what makes retries
// classifiable (OutcomeAlreadyConfirmed) and gives the audit trail its
// thread. Rows-affected is checked; when the CAS does not fire, the
// outcome is classified — never guessed:
//
//   - rows exist, reserved, deadline lapsed  → OutcomeExpired
//   - rows exist, committed under this txn   → OutcomeAlreadyConfirmed
//   - no rows carry this txn_id              → OutcomeGone
//
// The deadline comparison makes expiry authoritative at the moment of
// confirmation: a reservation past its window cannot be confirmed even
// if the sweeper has not run.
func Confirm(ctx context.Context, q Querier, table, txnID string, now ot.Instant) (Outcome, int64, error) {
	res, err := q.ExecContext(ctx,
		`UPDATE `+table+` SET state = 'committed', reserve_deadline = NULL
		  WHERE txn_id = ? AND state = 'reserved' AND reserve_deadline > ?`,
		txnID, now.UnixNano())
	if err != nil {
		return OutcomeGone, 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return OutcomeGone, 0, err
	}
	if n > 0 {
		return OutcomeConfirmed, n, nil
	}
	return classify(ctx, q, table, txnID, now)
}

// classify explains a Confirm whose CAS affected zero rows.
func classify(ctx context.Context, q Querier, table, txnID string, now ot.Instant) (Outcome, int64, error) {
	var reserved, lapsed, committed int64
	err := q.QueryRowContext(ctx,
		`SELECT
		    COUNT(*) FILTER (WHERE state = 'reserved'),
		    COUNT(*) FILTER (WHERE state = 'reserved' AND reserve_deadline <= ?),
		    COUNT(*) FILTER (WHERE state = 'committed')
		   FROM `+table+` WHERE txn_id = ?`,
		now.UnixNano(), txnID).Scan(&reserved, &lapsed, &committed)
	if err != nil {
		return OutcomeGone, 0, err
	}
	switch {
	case lapsed > 0:
		return OutcomeExpired, 0, nil
	case committed > 0 && reserved == 0:
		return OutcomeAlreadyConfirmed, committed, nil
	default:
		return OutcomeGone, 0, nil
	}
}

// Release deletes every reserved row owned by txnID in table — the
// explicit return of a reservation. Committed rows are untouched:
// release after a successful confirm is a no-op, not a rollback.
// Returns the number of rows released.
func Release(ctx context.Context, q Querier, table, txnID string) (int64, error) {
	res, err := q.ExecContext(ctx,
		`DELETE FROM `+table+` WHERE txn_id = ? AND state = 'reserved'`, txnID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
