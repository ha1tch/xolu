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

// PruneJournal implements the finiteness law (chronicle-substrate.md
// §4b): "entries older than a sealed checkpoint are derivationally
// redundant... policy may archive-then-prune the pre-checkpoint
// journal while conservation survives through the checkpoint chain."
//
// For each postable account, PruneJournal finds that account's latest
// checkpoint which is BOTH itself sealed (Sealer.Sealed) AND at or
// before `before`, and deletes every journal entry at or before that
// checkpoint's instant. `before` is a caller-supplied retention floor,
// never a ceiling the seal alone doesn't already impose: an account is
// never pruned past what's actually sealed, regardless of `before` --
// "no seam to tamper with" (§4b) requires the boundary to be a real
// seal, not an arbitrary caller-chosen instant. `before` only lets a
// caller retain MORE than the seal strictly requires (a longer
// compliance retention window, for instance), never less. An account
// with no sealed, in-range checkpoint is left untouched -- pruning
// nothing is always a correct, safe outcome.
//
// Archival (§4b: "optional everywhere, and never a precondition for
// correctness") is deliberately not implemented here -- this is prune
// only. A caller wanting cold-storage archival before calling this
// owns that step itself (blob storage, per §4b's own guidance on
// where it would target).
//
// Go-only by design, not yet exposed over HTTP -- see
// docs/KNOWN_ISSUES.md's "bal design — recorded decisions" for why,
// and cmd/iolu's `bal prune` command for the operator-facing path.
func (s *Store) PruneJournal(ctx context.Context, before time.Time) (int, error) {
	if s.sealer == nil {
		return 0, fmt.Errorf("bal: PruneJournal: no sealer attached (call SetSealer first)")
	}
	beforeUTC := before.UTC()

	rows, err := s.db.QueryContext(ctx,
		`SELECT account_id FROM `+s.accountsTable()+` WHERE postable = 1`)
	if err != nil {
		return 0, fmt.Errorf("bal: PruneJournal: list accounts: %w", err)
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

	total := 0
	for _, id := range ids {
		n, err := s.pruneAccountJournal(ctx, id, beforeUTC)
		if err != nil {
			return total, fmt.Errorf("bal: PruneJournal: account %q: %w", id, err)
		}
		total += n
	}
	return total, nil
}

// pruneAccountJournal prunes one account's journal, returning the
// number of rows removed (0 if nothing was safe to prune).
func (s *Store) pruneAccountJournal(ctx context.Context, accountID string, before time.Time) (int, error) {
	key, err := s.accountKeyOf(ctx, accountID)
	if err != nil {
		return 0, err
	}

	// Latest checkpoint, newest first: the first one that is both at
	// or before the seal frontier and within the retention floor is
	// the prune cutoff. A checkpoint instant marks a PERIOD BOUNDARY
	// (e.g. "start of July" closing June), not a point inside a
	// period -- Sealed(t) asks a different question (is the whole
	// window CONTAINING t sealed), which is wrong here: Sealed on a
	// month-boundary checkpoint answers for the NEXT month, not the
	// one just closed. Comparing directly against Frontier() is the
	// correct, boundary-safe check. stale = 0 excludes legacy
	// (pre-T-58) checkpoints whose correctness isn't guaranteed --
	// same filter VerifyCheckpoints already applies.
	frontier := s.sealer.Frontier()
	rows, err := s.db.QueryContext(ctx,
		`SELECT at_unix FROM `+s.checkpointsTable()+`
		 WHERE account_key = ? AND stale = 0 ORDER BY at_unix DESC`, key)
	if err != nil {
		return 0, err
	}
	var cutoff sql.NullInt64
	for rows.Next() {
		var atUnix int64
		if err := rows.Scan(&atUnix); err != nil {
			_ = rows.Close()
			return 0, err
		}
		at := time.Unix(atUnix, 0).UTC()
		if !at.After(before) && !at.After(frontier) {
			cutoff = sql.NullInt64{Int64: atUnix, Valid: true}
			break
		}
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if !cutoff.Valid {
		return 0, nil // nothing safe to prune for this account
	}

	// <= matches Checkpoint's own convention (SUM(amount) WHERE at <=
	// checkpoint_at): the checkpoint balance already includes entries
	// exactly at its own instant, so those are redundant too.
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM `+s.journalTable()+` WHERE account_key = ? AND at <= ?`,
		key, time.Unix(cutoff.Int64, 0).UTC())
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
