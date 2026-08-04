// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ha1tch/xolu/pkg/chronicle"
)

// Two verifiers of different strengths (@B08), both rebuild oracles.

// GlobalFoldOracle: SELECT SUM per account from the journal, compared
// row-for-row against balances — derive(journal) == current, exactly.
func (s *Store) GlobalFoldOracle() chronicle.RebuildOracle {
	return chronicle.RebuildOracle{
		Name: "bal.fold[" + strings.TrimSuffix(s.prefix, "_") + "]",
		Derive: func(ctx context.Context) (string, error) {
			// Re-scoped (chronicle-substrate.md §4b, "rebuild oracle
			// re-scopes to the earliest retained checkpoint"): baseline
			// from each account's latest non-stale checkpoint (0 if
			// none exists — identical to the old from-epoch sum in
			// that case) plus the journal strictly after it. Gives an
			// IDENTICAL result to the old unconditional
			// SUM(amount) GROUP BY account_key when nothing has ever
			// been pruned, and stays correct once PruneJournal has run.
			ckRows, err := s.db.QueryContext(ctx,
				`SELECT c.account_key, c.balance, c.at_unix FROM `+s.checkpointsTable()+` c
				 INNER JOIN (
				   SELECT account_key, MAX(at_unix) AS at_unix FROM `+s.checkpointsTable()+`
				   WHERE stale = 0 GROUP BY account_key
				 ) latest ON c.account_key = latest.account_key AND c.at_unix = latest.at_unix
				 WHERE c.stale = 0`)
			if err != nil {
				return "", err
			}
			baseline := map[int64]struct {
				balance int64
				atUnix  int64
			}{}
			for ckRows.Next() {
				var key, bal, at int64
				if err := ckRows.Scan(&key, &bal, &at); err != nil {
					_ = ckRows.Close()
					return "", err
				}
				baseline[key] = struct {
					balance int64
					atUnix  int64
				}{bal, at}
			}
			_ = ckRows.Close()
			if err := ckRows.Err(); err != nil {
				return "", err
			}

			rows, err := s.db.QueryContext(ctx,
				`SELECT account_key, COALESCE(SUM(amount),0) FROM `+s.journalTable()+`
				 GROUP BY account_key`)
			if err != nil {
				return "", err
			}
			defer func() { _ = rows.Close() }()
			sums := map[int64]int64{}
			for rows.Next() {
				var key, sum int64
				if err := rows.Scan(&key, &sum); err != nil {
					return "", err
				}
				sums[key] = sum
			}
			if err := rows.Err(); err != nil {
				return "", err
			}
			// A pruned account's journal-after-checkpoint sum needs
			// its own query -- the GROUP BY above summed the WHOLE
			// remaining journal per account, which is only correct
			// for accounts with no checkpoint. For accounts with one,
			// re-derive summing only entries after it.
			for key, b := range baseline {
				var afterSum int64
				if err := s.db.QueryRowContext(ctx,
					`SELECT COALESCE(SUM(amount),0) FROM `+s.journalTable()+`
					 WHERE account_key = ? AND at > ?`,
					key, time.Unix(b.atUnix, 0).UTC()).Scan(&afterSum); err != nil {
					return "", err
				}
				sums[key] = b.balance + afterSum
			}

			var lines []string
			for key, sum := range sums {
				lines = append(lines, fmt.Sprintf("%d %d", key, sum))
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n"), nil
		},
		Current: func(ctx context.Context) (string, error) {
			// Accounts with no entries hold value 0 — the fold omits
			// them, so the current side omits zero rows symmetrically.
			rows, err := s.db.QueryContext(ctx,
				`SELECT account_key, value FROM `+s.balancesTable()+` WHERE version > 0`)
			if err != nil {
				return "", err
			}
			defer func() { _ = rows.Close() }()
			var lines []string
			for rows.Next() {
				var key, v int64
				if err := rows.Scan(&key, &v); err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%d %d", key, v))
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n"), rows.Err()
		},
	}
}

// ChainBreak localises a violation of the per-account arithmetic chain.
type ChainBreak struct {
	AccountKey int64
	EntryID    int64
	Detail     string
}

// VerifyChains is the local verifier (@B08): per account, every entry
// satisfies previous+amount=current, entryₙ.previous = entryₙ₋₁.current,
// and versions are contiguous. A lost, duplicated, or altered entry is
// not merely detected but LOCALISED to the exact break.
// VerifyChains proves the per-account arithmetic and linkage chain.
// The first entry it sees for an account is normally required to carry
// version 1 (the account's true first-ever entry) -- but once
// PruneJournal has removed an account's earlier entries, the first
// entry it SEES is no longer the first entry that ever existed, and
// asserting version == 1 would be a false positive on every pruned
// account, every run. Re-scoped (chronicle-substrate.md §4b): the
// version == 1 assertion is skipped only when a checkpoint precedes
// the first retained entry -- that's the signal an account was
// legitimately pruned, not evidence of a real gap. The arithmetic and
// linkage checks (prev+amount==cur, and consecutive-entry linkage
// among what IS retained) are unaffected either way; only the single
// "was this truly entry #1" assertion needed re-scoping.
func (s *Store) VerifyChains(ctx context.Context) ([]ChainBreak, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT entry_id, account_key, amount, previous_balance, current_balance, version
		 FROM `+s.journalTable()+` ORDER BY account_key, entry_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var breaks []ChainBreak
	type tail struct {
		current int64
		version int64
	}
	tails := map[int64]tail{}
	for rows.Next() {
		var id, key, amount, prev, cur, ver int64
		if err := rows.Scan(&id, &key, &amount, &prev, &cur, &ver); err != nil {
			return nil, err
		}
		if prev+amount != cur {
			breaks = append(breaks, ChainBreak{key, id, fmt.Sprintf(
				"arithmetic: previous %d + amount %d != current %d", prev, amount, cur)})
		}
		if t, seen := tails[key]; seen {
			if prev != t.current {
				breaks = append(breaks, ChainBreak{key, id, fmt.Sprintf(
					"linkage: previous %d != prior entry's current %d", prev, t.current)})
			}
			if ver != t.version+1 {
				breaks = append(breaks, ChainBreak{key, id, fmt.Sprintf(
					"version gap: %d after %d", ver, t.version)})
			}
		} else if ver != 1 {
			pruned, err := s.hasPrecedingCheckpoint(ctx, key, prev)
			if err != nil {
				return nil, err
			}
			if !pruned {
				breaks = append(breaks, ChainBreak{key, id, fmt.Sprintf(
					"first entry has version %d, want 1", ver)})
			}
		}
		tails[key] = tail{current: cur, version: ver}
	}
	return breaks, rows.Err()
}

// hasPrecedingCheckpoint reports whether accountKey has a non-stale
// checkpoint whose balance equals prevBalance -- the signal that the
// first RETAINED journal entry's previous_balance is legitimately
// explained by a checkpoint (PruneJournal ran), not a genuine gap.
// Matching on balance, not just existence, is deliberate: a checkpoint
// that doesn't match this entry's own previous_balance would be
// evidence of a DIFFERENT problem, not grounds to skip the assertion.
func (s *Store) hasPrecedingCheckpoint(ctx context.Context, accountKey, prevBalance int64) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM `+s.checkpointsTable()+`
		 WHERE account_key = ? AND stale = 0 AND balance = ?`,
		accountKey, prevBalance).Scan(&count)
	return count > 0, err
}

// ChainOracle wraps VerifyChains as a rebuild oracle for iolu db check.
func (s *Store) ChainOracle() chronicle.RebuildOracle {
	return chronicle.RebuildOracle{
		Name: "bal.chain[" + strings.TrimSuffix(s.prefix, "_") + "]",
		Derive: func(ctx context.Context) (string, error) {
			return "chain intact", nil
		},
		Current: func(ctx context.Context) (string, error) {
			breaks, err := s.VerifyChains(ctx)
			if err != nil {
				return "", err
			}
			if len(breaks) == 0 {
				return "chain intact", nil
			}
			var lines []string
			for _, b := range breaks {
				lines = append(lines, fmt.Sprintf("account %d entry %d: %s", b.AccountKey, b.EntryID, b.Detail))
			}
			return strings.Join(lines, "\n"), nil
		},
	}
}
