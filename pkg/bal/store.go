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
// The db handle must carry the house SQLite contract: WAL +
// busy_timeout + _txlock=immediate. Immediate matters specifically for
// bal: Transfer reads account rows before its first UPDATE, and a
// deferred transaction upgrading read→write under WAL fails
// SQLITE_BUSY without consulting the busy handler (snapshot
// invalidation). Taking the write lock at BEGIN makes contending
// transfers queue under busy_timeout — the serialised-writer
// behaviour the admission guard's correctness argument assumes.
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

// accountRow is the guard-relevant account state, read in-transaction.
type accountRow struct {
	key      int64
	scale    uint8
	floor    int64
	ceiling  sql.NullInt64
	postable bool
}

func (s *Store) accountForUpdate(ctx context.Context, tx *sql.Tx, accountID string) (*accountRow, error) {
	var a accountRow
	var scale int64
	var post int64
	err := tx.QueryRowContext(ctx,
		`SELECT account_key, scale, floor, ceiling, postable FROM `+s.accountsTable()+` WHERE account_id = ?`,
		accountID).Scan(&a.key, &scale, &a.floor, &a.ceiling, &post)
	if err == sql.ErrNoRows {
		return nil, &UnknownAccountError{AccountID: accountID}
	}
	if err != nil {
		return nil, err
	}
	a.scale = uint8(scale)
	a.postable = post != 0
	return &a, nil
}

// Transfer moves amount minor units from `from` to `to` as two signed
// journal entries (−a, +a) in ONE transaction (@B03). Admission is the
// house CAS discipline (@B06, T-34): the decision lives inside each
// UPDATE's predicate, rows-affected is the verdict — never
// read-decide-write. The chain triple is captured from the same
// guarded statement via RETURNING.
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

	src, err := s.accountForUpdate(ctx, tx, from)
	if err != nil {
		return err
	}
	dst, err := s.accountForUpdate(ctx, tx, to)
	if err != nil {
		return err
	}
	if !src.postable {
		return &NotPostableError{AccountID: from}
	}
	if !dst.postable {
		return &NotPostableError{AccountID: to}
	}

	// Debit leg: value-amount must stay >= floor. One statement decides
	// and reports (RETURNING new value + version).
	var srcNew, srcVer int64
	err = tx.QueryRowContext(ctx,
		`UPDATE `+s.balancesTable()+`
		 SET value = value - ?, version = version + 1
		 WHERE account_key = ? AND value - ? >= ?
		 RETURNING value, version`,
		amount, src.key, amount, src.floor).Scan(&srcNew, &srcVer)
	if err == sql.ErrNoRows {
		return &BoundsError{AccountID: from, Side: "floor"}
	}
	if err != nil {
		return err
	}

	// Credit leg: value+amount must stay <= ceiling (when one exists).
	var dstNew, dstVer int64
	if dst.ceiling.Valid {
		err = tx.QueryRowContext(ctx,
			`UPDATE `+s.balancesTable()+`
			 SET value = value + ?, version = version + 1
			 WHERE account_key = ? AND value + ? <= ?
			 RETURNING value, version`,
			amount, dst.key, amount, dst.ceiling.Int64).Scan(&dstNew, &dstVer)
	} else {
		err = tx.QueryRowContext(ctx,
			`UPDATE `+s.balancesTable()+`
			 SET value = value + ?, version = version + 1
			 WHERE account_key = ?
			 RETURNING value, version`,
			amount, dst.key).Scan(&dstNew, &dstVer)
	}
	if err == sql.ErrNoRows {
		return &BoundsError{AccountID: to, Side: "ceiling"}
	}
	if err != nil {
		return err
	}

	// The two entries. previous_balance derives from RETURNING values —
	// same statement, same transaction, no separate read (@C04a).
	atUTC := at.UTC()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+s.journalTable()+`
		 (transfer_id, account_key, amount, previous_balance, current_balance, version, memo, at)
		 VALUES (?,?,?,?,?,?,?,?), (?,?,?,?,?,?,?,?)`,
		transferID, src.key, -amount, srcNew+amount, srcNew, srcVer, nullIfEmpty(memo), atUTC,
		transferID, dst.key, amount, dstNew-amount, dstNew, dstVer, nullIfEmpty(memo), atUTC,
	); err != nil {
		return err
	}
	return tx.Commit()
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
