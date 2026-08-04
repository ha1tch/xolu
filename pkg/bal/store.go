// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"modernc.org/sqlite"

	"github.com/ha1tch/xolu/pkg/chronicle"
	"github.com/ha1tch/xolu/pkg/dxp"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// sqliteConstraintUnique is SQLite's own stable numeric result code
// for a UNIQUE constraint violation (SQLITE_CONSTRAINT_UNIQUE = 2067,
// per SQLite's own public C API — not specific to this Go binding).
// Defined locally rather than imported: modernc.org/sqlite's own
// top-level package doesn't re-export this constant, only its
// internal /lib subpackage does, which isn't meant as this project's
// public surface to depend on. (Same constant, same reasoning, as
// pkg/loc/store.go's own copy — found and fixed there first, T-118's
// adversarial pass, then here once the identical gap was confirmed.)
const sqliteConstraintUnique = 2067

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
	db       *sql.DB
	tenantID tenant.TenantID // canonical tenant identity; prefix is DERIVED from this, never independently supplied
	prefix   string // tenant table prefix, e.g. "t0000_" — tenant.TablePrefix(tenantID)

	// claims, when set, gives Transfer visibility into live dxp
	// reservations against an account before its guarded UPDATE runs
	// (proposal §4: cache mutations/reads for a tenant only inside that
	// tenants write exclusion). nil is the default and preserves
	// exact pre-dxp behaviour: no lock taken, claimsSum is always 0.
	claims *dxp.MemCache

	// onRollupError, when set, is notified if the derived rollup plane
	// falls behind after an authoritative commit. Optional: the
	// authoritative planes never depend on it.
	onRollupError func(error)

	// rollup is bal's derived rollup-cascade plane (T-62), Pebble-backed.
	// Attached via SetRollupPebble — nil until then, matching claims'
	// optional-nil pattern. InitRollup/EmitDeltas/BalanceAsOf/
	// RebuildRollup/RollupOracle all require it non-nil; ordinary
	// Transfer does not touch it at all (rollup writes happen strictly
	// after commit, in EmitDeltas — see rollup_pebble.go doc).
	rollup *RollupPebble

	// sealer is bal's seal frontier (item 16 §7), attached via
	// SetSealer -- nil until then, matching claims/rollup's own
	// optional-nil pattern. nil means seal enforcement is not wired,
	// preserving exact pre-seal behaviour (no account is ever refused
	// for falling within a sealed period, because nothing is sealed).
	sealer *chronicle.Sealer
}

// OnRollupError registers a callback for derived-plane degradation.
func (s *Store) OnRollupError(fn func(error)) { s.onRollupError = fn }

// SetClaimsCache wires bal into the dxp reservation cache (T-54, item
// 19): once set, Transfer folds live pessimistic claims against each
// leg into its own guarded UPDATE before admitting, so an ordinary
// HTTP transfer cannot spend balance a dxp reservation is holding
// (proposal §4). nil (the default) is exact pre-dxp behaviour.
func (s *Store) SetClaimsCache(c *dxp.MemCache) { s.claims = c }

// balanceAndFloor reads accountID's current balance and floor,
// read-only — for dxp Reserve/Validate's admission checks, which must
// not write (reservations are memory-only, per T-54).
func (s *Store) balanceAndFloor(ctx context.Context, accountID string) (balance, floor int64, postable bool, err error) {
	var post int64
	err = s.db.QueryRowContext(ctx,
		`SELECT b.value, a.floor, a.postable FROM `+s.balancesTable()+` b
		 JOIN `+s.accountsTable()+` a ON a.account_key = b.account_key
		 WHERE a.account_id = ?`, accountID).Scan(&balance, &floor, &post)
	if err == sql.ErrNoRows {
		return 0, 0, false, &UnknownAccountError{AccountID: accountID}
	}
	return balance, floor, post == 1, err
}

// dxpResource is the cache resource key for one account under this
// tenant's bal participation — "acct:<external id>", matching the
// proposals own worked example.
func dxpResource(accountID string) string { return "acct:" + accountID }

// pessimisticClaimSum returns the sum of live PESSIMISTIC claims
// against resource, under the tenant lock the caller already holds
// (ClaimsForLocked, not ClaimsFor — see dxp.Cache doc). Optimistic
// claims are excluded by design (§7: guards ignore them entirely).
func pessimisticClaimSum(cache *dxp.MemCache, tenant, resource string) int64 {
	var sum int64
	for _, c := range cache.ClaimsForLocked(tenant, "bal", resource) {
		if c.Weight == dxp.Pessimistic {
			sum += c.Amount
		}
	}
	return sum
}

// NewStore binds bal to a database with a tenant table prefix.
// NewStore binds bal to a database for one tenant. tenantID is the
// canonical identity (pkg/tenant.IDString is the substrate-wide
// invariant every primitive's cross-primitive-comparable tenant key
// must derive from — see IDString's doc); prefix is derived from it,
// never accepted independently, so the two cannot drift the way an
// earlier version of this constructor allowed by taking a bare prefix
// string with no tenantID behind it at all.
func NewStore(db *sql.DB, tenantID tenant.TenantID) *Store {
	return &Store{db: db, tenantID: tenantID, prefix: tenantID.TablePrefix()}
}

// TenantID returns the tenant this Store is scoped to — the canonical
// identity prefix is derived from, exposed for callers (the dxp
// adapter) that need a cross-primitive-comparable tenant key rather
// than bal's own table-naming-specific prefix form.
func (s *Store) TenantID() tenant.TenantID { return s.tenantID }

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
		temporal_policy TEXT NOT NULL DEFAULT 'append_only', -- T-55 arrival-order contract
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
	CREATE TABLE IF NOT EXISTS ` + s.checkpointsTable() + ` (
		account_key INTEGER NOT NULL,
		at_unix     INTEGER NOT NULL,   -- sealed period boundary
		balance     INTEGER NOT NULL,   -- closing balance at that boundary
		entry_id    INTEGER NOT NULL,   -- journal position at close
		stale       INTEGER NOT NULL DEFAULT 0, -- legacy (T-51); inert since T-58 -- no writer sets 1 anymore, kept only so a pre-T-58 stale row is still correctly exempted by BalanceAsOf/VerifyCheckpoints
		PRIMARY KEY (account_key, at_unix)
	);
	CREATE INDEX IF NOT EXISTS idx_` + s.prefix + `bal_ckpt_account
		ON ` + s.checkpointsTable() + `(account_key, at_unix);
	`
	if _, err := s.db.ExecContext(ctx, ddl); err != nil {
		return err
	}
	// Legacy detection (house style): pre-T-55 databases lack the
	// policy column; the default gives every existing account
	// accounting semantics, which is the T-55 default by design.
	if err := s.ensureColumn(ctx, s.accountsTable(),
		"temporal_policy", "TEXT NOT NULL DEFAULT 'append_only'"); err != nil {
		return err
	}
	return s.ensureColumn(ctx, s.checkpointsTable(),
		"stale", "INTEGER NOT NULL DEFAULT 0")
}

// ensureColumn adds a column when absent — the ALTER path for
// databases created before the column joined the CREATE DDL.
func (s *Store) ensureColumn(ctx context.Context, table, column, decl string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`ALTER TABLE `+table+` ADD COLUMN `+column+` `+decl)
	return err
}

// DefineAccount creates an account and its zero balance row. The
// internal key is allocated densely (MAX+1) inside the transaction.
// DefineAccount creates an account plus its paired balances row
// (value 0, version 0) in one transaction. Write-first: the dense
// MAX(key)+1 allocation and the insert happen inside a single
// INSERT...SELECT...RETURNING statement, not a preceding SELECT — an
// earlier version read the next key as a separate statement first,
// the same WAL read-then-write-upgrade race T-115 found and fixed in
// loc.Move, confirmed present here too by /loc's own adversarial
// concurrency test (30 concurrent defines) applied against this
// function directly, not assumed safe by analogy alone. A duplicate
// account_id is now refused with a typed DuplicateAccountError
// (XOLU-BAL007, HTTP 409) rather than an unwrapped SQLite driver
// error falling through to a bare 500 — the same finding and fix as
// loc's own DuplicateLocationError.
func (s *Store) DefineAccount(ctx context.Context, def AccountDef) (AccountKey, error) {
	if err := def.Validate(); err != nil {
		return 0, fmt.Errorf("XOLU-BAL004: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var ceiling interface{}
	if def.Ceiling != nil {
		ceiling = *def.Ceiling
	}
	post := 0
	if def.Postable {
		post = 1
	}
	pol, err := chronicle.ParsePolicy(def.Policy)
	if err != nil {
		return 0, fmt.Errorf("XOLU-BAL004: %w", err)
	}

	var next int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO `+s.accountsTable()+`
			(account_key, account_id, unit, scale, floor, ceiling, postable, temporal_policy)
		SELECT COALESCE(MAX(account_key), 0) + 1, ?, ?, ?, ?, ?, ?, ?
		FROM `+s.accountsTable()+`
		RETURNING account_key`,
		def.ID, def.Unit, int64(def.Scale), def.Floor, ceiling, post, string(pol)).Scan(&next)
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqliteConstraintUnique {
			return 0, &DuplicateAccountError{AccountID: def.ID}
		}
		return 0, err
	}
	if next > int64(MaxAccountKey) {
		return 0, fmt.Errorf("account key space exhausted")
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

	// dxp claims visibility (T-54, proposal §4): the WHOLE guarded
	// sequence -- reading live claims, then the guarded UPDATEs below,
	// then commit -- runs under the tenants dxp lock when a cache is
	// wired, making the cache and this transaction one serialisation
	// domain. nil cache: zero cost, zero behaviour change (claimsSum
	// below is always 0, predicates reduce to their pre-dxp form).
	//
	// s.tenantID.String(), NOT s.prefix: the cache is keyed on the
	// canonical tenant identity (pkg/tenant.TenantID.String()), never
	// on bal's own table-naming prefix — using s.prefix here was
	// exactly the bug the tenantID field exists to make impossible
	// (found by TestOrdinaryTransfer_RespectsLiveDxpHold failing
	// after the adapter's own tenant key was corrected but this call
	// site wasn't: they'd have silently addressed two different
	// cache shards for the same tenant).
	tenantKey := s.tenantID.String()
	var srcClaimed, dstClaimed int64
	if s.claims != nil {
		s.claims.Lock(tenantKey)
		defer s.claims.Unlock(tenantKey)
		srcClaimed = pessimisticClaimSum(s.claims, tenantKey, dxpResource(from))
		dstClaimed = pessimisticClaimSum(s.claims, tenantKey, dxpResource(to))
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	srcKey, dstKey, err := s.transferInTx(ctx, tx, transferID, from, to, amount, memo, at, srcClaimed, dstClaimed)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	// Rollup plane (@B05): fold the two signed legs into the cascade
	// AFTER the authoritative commit. Derived-plane failure must never
	// fail an authoritative transfer that already committed, so this is
	// best-effort: a crash or error here leaves the rollup stale, which
	// the rollup oracle detects and RebuildRollup repairs from the
	// journal. No guard reads the rollup (@C04a), so staleness is a
	// performance/observability matter, never a correctness one.
	if emitErr := s.EmitDeltas(ctx, srcKey, dstKey, amount, at.UTC()); emitErr != nil {
		s.rollupDegraded(emitErr)
	}
	return nil
}

// transferInTx is Transfer's guarded core, extracted so a dxp
// coordinator (item 21) can drive it against an externally-supplied,
// shared transaction (proposal §11: every participant effect inside
// ONE SQL transaction on one tenant file) instead of the self-managed
// transaction Transfer opens for its own standalone callers. It does
// NOT commit -- the caller owns that -- and does NOT emit rollup
// deltas, since those must happen strictly after commit (see Transfer)
// and a shared multi-participant transaction has no single commit
// point this method can see. A coordinator driving transferInTx
// directly is responsible for its own post-commit rollup pass; the
// dxp.Participant contract has no post-commit verb today (item 21
// gap, flagged here rather than silently dropped -- filed in
// TRACKING.md under T-54's adapter work).
//
// srcClaimed and dstClaimed are the caller's already-computed live
// pessimistic-claim sums for from and to respectively (0 for a caller
// with no dxp cache wired, or for a resource with no live claims).
func (s *Store) transferInTx(ctx context.Context, tx *sql.Tx, transferID, from, to string,
	amount int64, memo string, at time.Time, srcClaimed, dstClaimed int64) (srcKey, dstKey int64, err error) {

	// Debit leg — the transaction's first statement, a write. The
	// subqueries bind account, postability, and floor into the one
	// predicate; RETURNING yields key + chain values.
	atUTC := at.UTC()
	// Item 16 §7: a sealed period refuses entries dated within it,
	// unconditionally -- independent of temporal_policy (see seal.go's
	// doc for the accepted race-window tradeoff of this early,
	// unlocked check versus a full Sealer.Guard wrap).
	if s.sealer != nil && s.sealer.Sealed(atUTC) {
		return 0, 0, &SealedPeriodError{At: atUTC, Frontier: s.sealer.Frontier()}
	}
	var srcNew, srcVer int64
	err = tx.QueryRowContext(ctx,
		`UPDATE `+s.balancesTable()+`
		 SET value = value - ?, version = version + 1
		 WHERE account_key = (SELECT account_key FROM `+s.accountsTable()+`
		                      WHERE account_id = ? AND postable = 1)
		   AND value - ? - ? >= (SELECT floor FROM `+s.accountsTable()+`
		                     WHERE account_id = ?)
		   AND ((SELECT temporal_policy FROM `+s.accountsTable()+`
		         WHERE account_id = ?) = 'backdated'
		        OR NOT EXISTS (SELECT 1 FROM `+s.journalTable()+` j
		                       WHERE j.account_key = (SELECT account_key FROM `+s.accountsTable()+`
		                                              WHERE account_id = ?)
		                         AND j.at > ?))
		 RETURNING account_key, value, version`,
		amount, from, amount, srcClaimed, from, from, from, atUTC).Scan(&srcKey, &srcNew, &srcVer)
	if err == sql.ErrNoRows {
		return 0, 0, s.diagnoseRefusal(ctx, tx, from, "floor", atUTC)
	}
	if err != nil {
		return 0, 0, err
	}

	// Credit leg: ceiling guard folded the same way (NULL ceiling
	// admits everything: `? <= COALESCE(ceiling, max)` with max the
	// int64 ceiling constant).
	var dstNew, dstVer int64
	err = tx.QueryRowContext(ctx,
		`UPDATE `+s.balancesTable()+`
		 SET value = value + ?, version = version + 1
		 WHERE account_key = (SELECT account_key FROM `+s.accountsTable()+`
		                      WHERE account_id = ? AND postable = 1)
		   AND value + ? + ? <= (SELECT COALESCE(ceiling, 9223372036854775807)
		                     FROM `+s.accountsTable()+` WHERE account_id = ?)
		   AND ((SELECT temporal_policy FROM `+s.accountsTable()+`
		         WHERE account_id = ?) = 'backdated'
		        OR NOT EXISTS (SELECT 1 FROM `+s.journalTable()+` j
		                       WHERE j.account_key = (SELECT account_key FROM `+s.accountsTable()+`
		                                              WHERE account_id = ?)
		                         AND j.at > ?))
		 RETURNING account_key, value, version`,
		amount, to, amount, dstClaimed, to, to, to, atUTC).Scan(&dstKey, &dstNew, &dstVer)
	if err == sql.ErrNoRows {
		return 0, 0, s.diagnoseRefusal(ctx, tx, to, "ceiling", atUTC)
	}
	if err != nil {
		return 0, 0, err
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO `+s.journalTable()+`
		 (transfer_id, account_key, amount, previous_balance, current_balance, version, memo, at)
		 VALUES (?,?,?,?,?,?,?,?), (?,?,?,?,?,?,?,?)`,
		transferID, srcKey, -amount, srcNew+amount, srcNew, srcVer, nullIfEmpty(memo), atUTC,
		transferID, dstKey, amount, dstNew-amount, dstNew, dstVer, nullIfEmpty(memo), atUTC,
	); err != nil {
		return 0, 0, err
	}
	// T-58: a checkpoint is a fold under SumInt64, exactly like a
	// rollup bucket — and folds under an associative, commutative
	// monoid absorb a correction directly, without needing the other
	// terms. This is why EmitDeltas/Append already handles backdated
	// entries correctly for buckets (see rollup.go): the same move
	// applied here to checkpoints. Two statements, not one IN (?, ?),
	// because the legs carry opposite signs. at_unix >= entry_time is
	// unchanged from the old staleness range: a checkpoint boundary
	// sums everything at-or-before it, so a checkpoint exactly at the
	// entry's instant already includes it and must be adjusted too.
	//
	// Unlike the recompute-from-journal approach this replaces, delta-
	// adjustment never reads the journal — it stays correct even after
	// item 16 (prefix-collapse retention) prunes old journal entries,
	// which is the whole reason this replaces rather than supplements
	// the old stale-flag write (docs/proposals/bal-checkpoint-delta-propagation.md).
	if _, err := tx.ExecContext(ctx,
		`UPDATE `+s.checkpointsTable()+`
		    SET balance = balance + ?
		  WHERE account_key = ? AND at_unix >= ?`,
		-amount, srcKey, atUTC.Unix()); err != nil {
		return 0, 0, err
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE `+s.checkpointsTable()+`
		    SET balance = balance + ?
		  WHERE account_key = ? AND at_unix >= ?`,
		amount, dstKey, atUTC.Unix()); err != nil {
		return 0, 0, err
	}
	return srcKey, dstKey, nil
}

// rollupDegraded records that the derived plane fell behind. Kept as a
// seam rather than a log call so the server can surface it; the
// authoritative planes are unaffected.
func (s *Store) rollupDegraded(err error) {
	if s.onRollupError != nil {
		s.onRollupError(err)
	}
}

// diagnoseRefusal names WHY a guarded UPDATE matched nothing — unknown
// account, summary account, or a genuine bounds refusal. Runs only on
// the failure path (the transaction rolls back regardless).
func (s *Store) diagnoseRefusal(ctx context.Context, tx *sql.Tx, accountID, side string, at time.Time) error {
	var post, backdated int64
	var policy string
	err := tx.QueryRowContext(ctx,
		`SELECT a.postable, a.temporal_policy,
		        EXISTS(SELECT 1 FROM `+s.journalTable()+` j
		               WHERE j.account_key = a.account_key AND j.at > ?)
		 FROM `+s.accountsTable()+` a WHERE a.account_id = ?`,
		at, accountID).Scan(&post, &policy, &backdated)
	if err == sql.ErrNoRows {
		return &UnknownAccountError{AccountID: accountID}
	}
	if err != nil {
		return err
	}
	if post == 0 {
		return &NotPostableError{AccountID: accountID}
	}
	if policy != string(chronicle.Backdated) && backdated == 1 {
		return &BackdatedError{AccountID: accountID, At: at}
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

// BackdatedError refuses a strictly-backdated entry on an append_only
// account (T-55; code XOLU-BAL006). Same-instant entries are admitted.
type BackdatedError struct {
	AccountID string
	At        time.Time
}

func (e *BackdatedError) Error() string {
	return fmt.Sprintf(
		"XOLU-BAL006: entry at %s predates the latest entry on append_only account %q",
		e.At.UTC().Format(time.RFC3339Nano), e.AccountID)
}

func (e *BackdatedError) Unwrap() error { return chronicle.ErrBackdatedRefused }

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

// DuplicateAccountError: XOLU-BAL007 — account_id already defined.
// HTTP 409. Found by /loc's own adversarial testing pass (T-118),
// which surfaced the identical gap here: a UNIQUE constraint
// violation on account_id previously had no typed error, falling
// through to a bare 500 for what is an ordinary client mistake
// (defining an id that already exists), not a server failure.
type DuplicateAccountError struct{ AccountID string }

func (e *DuplicateAccountError) Error() string {
	return fmt.Sprintf("XOLU-BAL007: account_id %q is already defined", e.AccountID)
}
