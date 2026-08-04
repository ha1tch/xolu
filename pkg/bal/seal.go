// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/ha1tch/xolu/pkg/chronicle"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// Item 16 §7 (bal-conservation-primitive.md): bal/close advances the
// account-set's seal frontier (chronicle's Sealer, lifted from cal in
// wave 3 anticipating exactly this consumer — its own doc comment: "bal's
// period close (item 16) is the first native consumer, sealing calendar
// months"). A closed period rejects entries dated within it (XOLU-BAL003),
// unconditionally -- independent of any account's own temporal_policy,
// which is a different, per-account axis (see store.go's guarded UPDATE).
// Sealing is tenant-wide (the account-SET's frontier, one value), not
// per-account.
//
// Race-window tradeoff, stated rather than hidden: chronicle.Sealer.Guard
// holds the seal lock across a caller-supplied closure, correctly
// serialising a mutation against a concurrent AdvanceTo for exactly this
// purpose. transferInTx does NOT use it -- wrapping the guarded UPDATE
// pair in a closure would mean rewriting already-tested, working
// admission logic for a marginal gain against a rare event (bal/close is
// a deliberate, human-triggered administrative action, not high-frequency
// contention). Instead, transferInTx does an early, unlocked
// Sealer.Sealed(at) check. This leaves a real but narrow race: a transfer
// already past this check when AdvanceTo runs can still commit into what
// becomes sealed a moment later. Accepted deliberately, not overlooked --
// revisit only if a real ordering violation is ever observed, not on
// general principle.

func (s *Store) sealTable() string { return s.prefix + "bal_seal" }

// InitSeal creates the seal-frontier persistence table. Idempotent,
// separate from Init and InitRollup for the same reason both of those
// are separate: the SQL plane and each derived/administrative plane can
// exist independently (the seal frontier, once persisted, is the
// durable record LoadSealer recovers from on restart -- chronicle.Sealer
// itself is memory-only by design, per its own doc comment: "recovery is
// the consumer's, because the consumer owns the durable record").
func (s *Store) InitSeal(ctx context.Context) error {
	ddl := `
	CREATE TABLE IF NOT EXISTS ` + s.sealTable() + ` (
		id            INTEGER PRIMARY KEY CHECK (id = 0),
		frontier_unix INTEGER NOT NULL
	);
	`
	_, err := s.db.ExecContext(ctx, ddl)
	return err
}

// LoadSealer constructs a chronicle.Sealer over bal's calendar-month
// tiling (chronicle.MonthWindows -- "bal's period shape", per its own
// doc) and restores any persisted frontier, so a restarted process
// recovers exactly where it left off rather than silently un-sealing
// the past. Returns a fresh, unsealed Sealer if no frontier was ever
// persisted (the common case: most tenants never call bal/close).
//
// The returned Sealer is long-lived (mirrors RollupPebble/dxp.MemCache):
// callers should construct it once per tenant and attach it to each
// request's freshly-built Store via SetSealer, not reconstruct it per
// request -- reconstructing per request would still recover the correct
// frontier from SQL, but would defeat the in-memory monotonicity
// AdvanceTo exists to provide between requests.
func LoadSealer(ctx context.Context, db *sql.DB, tenantID tenant.TenantID) (*chronicle.Sealer, error) {
	sealer, err := chronicle.NewSealer(chronicle.MonthWindows)
	if err != nil {
		return nil, err
	}
	prefix := tenantID.TablePrefix()
	var frontierUnix int64
	err = db.QueryRowContext(ctx,
		`SELECT frontier_unix FROM `+prefix+`bal_seal WHERE id = 0`).Scan(&frontierUnix)
	switch {
	case err == sql.ErrNoRows:
		return sealer, nil // never sealed: fresh, zero-frontier Sealer is correct
	case err != nil:
		// Table may not exist yet (InitSeal not yet called for this
		// tenant) -- same "derived plane can exist independently"
		// reasoning as InitRollup: absence is not an error, it means
		// nothing has ever been sealed.
		return sealer, nil
	}
	sealer.AdvanceTo(time.Unix(frontierUnix, 0).UTC())
	return sealer, nil
}

// SetSealer attaches an already-loaded seal frontier to this Store.
// Mirrors SetRollupPebble/SetClaimsCache exactly, and for the same
// reason: bal.Store is built fresh per request, but the Sealer must be
// the SAME long-lived instance across requests for AdvanceTo's
// monotonicity to mean anything.
func (s *Store) SetSealer(sealer *chronicle.Sealer) { s.sealer = sealer }

// SealPeriod advances the tenant's seal frontier to at and checkpoints
// every postable account as of that instant -- the two things item 16
// §7 says bal/close does together ("its closing checkpoints are
// written"). Returns the number of accounts checkpointed.
//
// Persisting AFTER AdvanceTo (not the caller's raw `at`) means a
// concurrent or repeated close can never regress the persisted value:
// AdvanceTo's own monotonicity decides what actually got sealed, and
// that -- not the request's input -- is what gets written durably.
func (s *Store) SealPeriod(ctx context.Context, at time.Time) (int, error) {
	if s.sealer == nil {
		return 0, fmt.Errorf("bal: SealPeriod: no sealer attached (call SetSealer first)")
	}
	s.sealer.AdvanceTo(at.UTC())
	frontier := s.sealer.Frontier()

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO `+s.sealTable()+` (id, frontier_unix) VALUES (0, ?)
		 ON CONFLICT(id) DO UPDATE SET frontier_unix = excluded.frontier_unix`,
		frontier.Unix()); err != nil {
		return 0, fmt.Errorf("bal: SealPeriod: persist frontier: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT account_id FROM `+s.accountsTable()+` WHERE postable = 1`)
	if err != nil {
		return 0, fmt.Errorf("bal: SealPeriod: list accounts: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := 0
	for _, id := range ids {
		if err := s.Checkpoint(ctx, id, frontier); err != nil {
			return n, fmt.Errorf("bal: SealPeriod: checkpoint %q: %w", id, err)
		}
		n++
	}
	return n, nil
}

// SealedPeriodError reports a transfer refused because its instant
// falls within the tenant's sealed (immutable) past -- XOLU-BAL003.
// Unlike BoundsError (a per-account admission refusal), this is a
// tenant-wide, policy-independent refusal: no account's temporal_policy
// setting can override a sealed period.
type SealedPeriodError struct {
	At       time.Time
	Frontier time.Time
}

func (e *SealedPeriodError) Error() string {
	return fmt.Sprintf("XOLU-BAL003: entry at %s falls within the sealed period (frontier %s)",
		e.At.Format(time.RFC3339), e.Frontier.Format(time.RFC3339))
}
