// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Store is bal's SQL plane (@B05): the append-only journal and the
// balances table, maintained in the same transaction as each entry.
// The bounds guard's input commits-or-aborts with the entry it guards
// (@C04a); no rollup is ever consulted by a guard.
//
// The db handle must carry WAL + busy_timeout (the house defaults).
// Transfer is deliberately WRITE-FIRST: its opening statement is the
// guarded UPDATE itself (accounts resolved by subquery), so the
// transaction is a writer from its first statement and contending
// transfers queue under busy_timeout even on plain deferred
// transactions. A read-first shape would hit WAL's snapshot
// invalidation (SQLITE_BUSY past the busy handler) on read→write
// upgrade — the G-13 harness caught exactly that in an earlier form.
type Store struct {
	db     *sql.DB
	prefix string // tenant table prefix, e.g. "t0000_"
}

// NewStore binds bal to a database with a tenant table prefix.
func NewStore(db *sql.DB, tablePrefix string) *Store {
	return &Store{db: db, prefix: tablePrefix}
}

func (s *Store) accountsTable() string { return s.prefix + "bal_accounts" }
func (s *Store) journalTable() string  { return s.prefix + "bal_journal" }
func (s *Store) balancesTable() string { return s.prefix + "bal_balances" }

// Init creates the bal tables. Idempotent. The journal's `state` column
// (default 'committed') leaves room for holds (@B10) without migration.
func (s *Store) Init(ctx context.Context) error {
	ddl := `
	CREATE TABLE IF NOT EXISTS ` + s.accountsTable() + ` (
		account_key INTEGER PRIMARY KEY,          -- internal uint32, dense
		account_id  TEXT    NOT NULL UNIQUE,      -- external namespaced string
		unit        TEXT    NOT NULL,
		scale       INTEGER NOT NULL,
		floor       INTEGER NOT NULL DEFAULT 0,
		ceiling     INTEGER NULL,
		postable    INTEGER NOT NULL DEFAULT 1,
		created_at  TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS ` + s.balancesTable() + ` (
		account_key INTEGER PRIMARY KEY REFERENCES ` + s.accountsTable() + `(account_key),
		value       INTEGER NOT NULL DEFAULT 0,
		version     INTEGER NOT NULL DEFAULT 0    -- monotonic, one per entry (@B03 chain)
	);
	CREATE TABLE IF NOT EXISTS ` + s.journalTable() + ` (
		entry_id         INTEGER PRIMARY KEY AUTOINCREMENT,
		transfer_id      TEXT    NOT NULL,        -- pairs the two legs
		account_key      INTEGER NOT NULL REFERENCES ` + s.accountsTable() + `(account_key),
		amount           INTEGER NOT NULL,        -- signed minor units
		previous_balance INTEGER NOT NULL,        -- chain triple (@B03)
		current_balance  INTEGER NOT NULL,
		version          INTEGER NOT NULL,        -- balances.version after this entry
		memo             TEXT    NULL,            -- immutable with the record (@C04c corollary)
		at               TIMESTAMP NOT NULL,
		state            TEXT    NOT NULL DEFAULT 'committed'
	);
	CREATE INDEX IF NOT EXISTS idx_` + s.prefix + `bal_journal_account
		ON ` + s.journalTable() + `(account_key, entry_id);
	CREATE INDEX IF NOT EXISTS idx_` + s.prefix + `bal_journal_transfer
		ON ` + s.journalTable() + `(transfer_id);
	`
	_, err := s.db.ExecContext(ctx, ddl)
	return err
}

// DefineAccount creates an account and its zero balance row. The
// internal key is allocated densely (MAX+1) inside the transaction.
func (s *Store) DefineAccount(ctx context.Context, def AccountDef) (AccountKey, error) {
	if err := def.Validate(); err != nil {
		return 0, fmt.Errorf("XOLU-BAL004: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var next int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(account_key), 0) + 1 FROM `+s.accountsTable()).Scan(&next); err != nil {
		return 0, err
	}
	if next > int64(MaxAccountKey) {
		return 0, fmt.Errorf("account key space exhausted")
	}
	var ceiling interface{}
	if def.Ceiling != nil {
		ceiling = *def.Ceiling
	}
	post := 0
	if def.Postable {
		post = 1
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+s.accountsTable()+` (account_key, account_id, unit, scale, floor, ceiling, postable)
		 VALUES (?,?,?,?,?,?,?)`,
		next, def.ID, def.Unit, int64(def.Scale), def.Floor, ceiling, post); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+s.balancesTable()+` (account_key, value, version) VALUES (?, 0, 0)`,
		next); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return AccountKey(uint32(next)), nil
}

// Transfer moves amount minor units from `from` to `to` as two signed
// journal entries (−a, +a) in ONE transaction (@B03). Admission is the
// house CAS discipline (@B06, T-34): the decision lives inside each
// UPDATE's predicate, rows-affected is the verdict — never
// read-decide-write. The guarded UPDATE is the transaction's FIRST
// statement (write-first; see Store docs), resolving the account and
// its floor by subquery and returning the chain triple in the same
// statement. Error discrimination (unknown vs not-postable vs bounds)
// happens on the failure path only, where the transaction is read-only
// and about to roll back.
func (s *Store) Transfer(ctx context.Context, transferID, from, to string, amount int64, memo string, at time.Time) error {
	if amount <= 0 {
		return &AmountScaleError{Detail: "transfer amount must be positive"}
	}
	if from == to {
		return &AmountScaleError{Detail: "transfer requires two distinct accounts"}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Debit leg — the transaction's first statement, a write. The
	// subqueries bind account, postability, and floor into the one
	// predicate; RETURNING yields key + chain values.
	var srcKey, srcNew, srcVer int64
	err = tx.QueryRowContext(ctx,
		`UPDATE `+s.balancesTable()+`
		 SET value = value - ?, version = version + 1
		 WHERE account_key = (SELECT account_key FROM `+s.accountsTable()+`
		                      WHERE account_id = ? AND postable = 1)
		   AND value - ? >= (SELECT floor FROM `+s.accountsTable()+`
		                     WHERE account_id = ?)
		 RETURNING account_key, value, version`,
		amount, from, amount, from).Scan(&srcKey, &srcNew, &srcVer)
	if err == sql.ErrNoRows {
		return s.diagnoseRefusal(ctx, tx, from, "floor")
	}
	if err != nil {
		return err
	}

	// Credit leg: ceiling guard folded the same way (NULL ceiling
	// admits everything: `? <= COALESCE(ceiling, max)` with max the
	// int64 ceiling constant).
	var dstKey, dstNew, dstVer int64
	err = tx.QueryRowContext(ctx,
		`UPDATE `+s.balancesTable()+`
		 SET value = value + ?, version = version + 1
		 WHERE account_key = (SELECT account_key FROM `+s.accountsTable()+`
		                      WHERE account_id = ? AND postable = 1)
		   AND value + ? <= (SELECT COALESCE(ceiling, 9223372036854775807)
		                     FROM `+s.accountsTable()+` WHERE account_id = ?)
		 RETURNING account_key, value, version`,
		amount, to, amount, to).Scan(&dstKey, &dstNew, &dstVer)
	if err == sql.ErrNoRows {
		return s.diagnoseRefusal(ctx, tx, to, "ceiling")
	}
	if err != nil {
		return err
	}

	atUTC := at.UTC()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+s.journalTable()+`
		 (transfer_id, account_key, amount, previous_balance, current_balance, version, memo, at)
		 VALUES (?,?,?,?,?,?,?,?), (?,?,?,?,?,?,?,?)`,
		transferID, srcKey, -amount, srcNew+amount, srcNew, srcVer, nullIfEmpty(memo), atUTC,
		transferID, dstKey, amount, dstNew-amount, dstNew, dstVer, nullIfEmpty(memo), atUTC,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// diagnoseRefusal names WHY a guarded UPDATE matched nothing — unknown
// account, summary account, or a genuine bounds refusal. Runs only on
// the failure path (the transaction rolls back regardless).
func (s *Store) diagnoseRefusal(ctx context.Context, tx *sql.Tx, accountID, side string) error {
	var post int64
	err := tx.QueryRowContext(ctx,
		`SELECT postable FROM `+s.accountsTable()+` WHERE account_id = ?`,
		accountID).Scan(&post)
	if err == sql.ErrNoRows {
		return &UnknownAccountError{AccountID: accountID}
	}
	if err != nil {
		return err
	}
	if post == 0 {
		return &NotPostableError{AccountID: accountID}
	}
	return &BoundsError{AccountID: accountID, Side: side}
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// Balance returns the current balance and version for an account.
func (s *Store) Balance(ctx context.Context, accountID string) (value int64, version int64, err error) {
	err = s.db.QueryRowContext(ctx,
		`SELECT b.value, b.version FROM `+s.balancesTable()+` b
		 JOIN `+s.accountsTable()+` a ON a.account_key = b.account_key
		 WHERE a.account_id = ?`, accountID).Scan(&value, &version)
	if err == sql.ErrNoRows {
		return 0, 0, &UnknownAccountError{AccountID: accountID}
	}
	return value, version, err
}

// Entry is one journal row on the API surface: external account id,
// signed amount, the chain triple, memo, instant. Internal keys never
// appear (@B09a).
type Entry struct {
	EntryID         int64     `json:"entry_id"`
	TransferID      string    `json:"transfer_id"`
	AccountID       string    `json:"account_id"`
	Amount          int64     `json:"-"` // rendered as string by the handler (@B04)
	PreviousBalance int64     `json:"-"`
	CurrentBalance  int64     `json:"-"`
	Version         int64     `json:"version"`
	Memo            string    `json:"memo,omitempty"`
	At              time.Time `json:"at"`
}

// Entries returns up to limit journal rows for an account, oldest
// first, starting after afterEntryID (0 = from the beginning).
func (s *Store) Entries(ctx context.Context, accountID string, afterEntryID int64, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT j.entry_id, j.transfer_id, a.account_id, j.amount,
		        j.previous_balance, j.current_balance, j.version,
		        COALESCE(j.memo,''), j.at
		 FROM `+s.journalTable()+` j
		 JOIN `+s.accountsTable()+` a ON a.account_key = j.account_key
		 WHERE a.account_id = ? AND j.entry_id > ?
		 ORDER BY j.entry_id LIMIT ?`,
		accountID, afterEntryID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.EntryID, &e.TransferID, &e.AccountID, &e.Amount,
			&e.PreviousBalance, &e.CurrentBalance, &e.Version, &e.Memo, &e.At); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	if out == nil {
		// Distinguish empty journal from unknown account.
		var one int
		if err := s.db.QueryRowContext(ctx,
			`SELECT 1 FROM `+s.accountsTable()+` WHERE account_id = ?`, accountID).Scan(&one); err == sql.ErrNoRows {
			return nil, &UnknownAccountError{AccountID: accountID}
		}
	}
	return out, rows.Err()
}

// AccountScale returns an account's scale (render support).
func (s *Store) AccountScale(ctx context.Context, accountID string) (uint8, error) {
	var scale int64
	err := s.db.QueryRowContext(ctx,
		`SELECT scale FROM `+s.accountsTable()+` WHERE account_id = ?`, accountID).Scan(&scale)
	if err == sql.ErrNoRows {
		return 0, &UnknownAccountError{AccountID: accountID}
	}
	return uint8(scale), err
}

// ─── Typed errors (XOLU-BAL family, @B09) ───────────────────────────────────

type BoundsError struct {
	AccountID string
	Side      string // "floor" or "ceiling"
}

func (e *BoundsError) Error() string {
	return fmt.Sprintf("XOLU-BAL001: transfer refused: %s bound on %s", e.Side, e.AccountID)
}

type UnknownAccountError struct{ AccountID string }

func (e *UnknownAccountError) Error() string {
	return fmt.Sprintf("XOLU-BAL002: unknown account %s", e.AccountID)
}

type AmountScaleError struct{ Detail string }

func (e *AmountScaleError) Error() string {
	return fmt.Sprintf("XOLU-BAL004: %s", e.Detail)
}

type NotPostableError struct{ AccountID string }

func (e *NotPostableError) Error() string {
	return fmt.Sprintf("XOLU-BAL005: %s is a summary account (not postable)", e.AccountID)
}
