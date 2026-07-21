// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package bal

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ha1tch/xolu/pkg/chronicle"
)

// Two verifiers of different strengths (@B08), both rebuild oracles.

// GlobalFoldOracle: SELECT SUM per account from the journal, compared
// row-for-row against balances — derive(journal) == current, exactly.
func (s *Store) GlobalFoldOracle() chronicle.RebuildOracle {
	return chronicle.RebuildOracle{
		Name: "bal.fold[" + strings.TrimSuffix(s.prefix, "_") + "]",
		Derive: func(ctx context.Context) (string, error) {
			rows, err := s.db.QueryContext(ctx,
				`SELECT account_key, COALESCE(SUM(amount),0) FROM `+s.journalTable()+`
				 GROUP BY account_key`)
			if err != nil {
				return "", err
			}
			defer func() { _ = rows.Close() }()
			var lines []string
			for rows.Next() {
				var key, sum int64
				if err := rows.Scan(&key, &sum); err != nil {
					return "", err
				}
				lines = append(lines, fmt.Sprintf("%d %d", key, sum))
			}
			sort.Strings(lines)
			return strings.Join(lines, "\n"), rows.Err()
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
			breaks = append(breaks, ChainBreak{key, id, fmt.Sprintf(
				"first entry has version %d, want 1", ver)})
		}
		tails[key] = tail{current: cur, version: ver}
	}
	return breaks, rows.Err()
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
