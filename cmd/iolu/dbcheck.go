// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/ha1tch/xolu/pkg/bal"
	"github.com/ha1tch/xolu/pkg/chronicle"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// cmdDBCheck runs the rebuild oracles — derive(record) == current derived
// state — across every tenant (operations roadmap item 5; the harness is
// pkg/chronicle's, @C §4 extraction #3). Exit 0: every oracle agrees.
// Exit 1: at least one divergence (the store has drifted from its
// authoritative record) or an execution error.
//
// v1 runs the graph-edge oracle (blob documents vs the edge table).
// Further oracles register here as their primitives grow deriveFns
// (ts rollups, cal index, bal balances at wave 4+).
func cmdDBCheck(args []string) {
	fs := flag.NewFlagSet("iolu db check", flag.ExitOnError)
	baseDir := fs.String("base-dir", "", "xolu data root (required)")
	mode := fs.String("mode", "", "store organisation override: per-file or shared (default: auto-detect)")
	verbose := fs.Bool("v", false, "print fingerprints on divergence")
	_ = fs.Parse(args)

	if *baseDir == "" {
		fs.Usage()
		os.Exit(1)
	}
	storeMode, err := resolveStoreMode(*baseDir, *mode, modePerFile)
	if err != nil {
		fatal("%v", err)
	}
	ctx := context.Background()

	tenants, err := listTenantIDs(*baseDir, storeMode)
	if err != nil {
		fatal("list tenants: %v", err)
	}

	diverged := 0
	checked := 0
	for _, tid := range tenants {
		store, err := openTenantStore(*baseDir, tid, storeMode, false)
		if err != nil {
			fatal("open tenant %d: %v", tid, err)
		}

		// Skip tenants without a graph table (graph never enabled): the
		// oracle's derived plane does not exist, so there is nothing to
		// have drifted.
		var exists int
		table := tid.GraphTableName()
		err = store.DB().QueryRowContext(ctx,
			`SELECT 1 FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists)
		if err != nil {
			fmt.Printf("  %s  graph.edges   SKIP (no graph table)\n", tenant.TenantID(tid).DirName())
			_ = store.Close()
			continue
		}

		oracles := []chronicle.RebuildOracle{store.GraphEdgesOracle()}
		// bal oracles join when the tenant has bal tables (@B08).
		var balExists int
		balTable := tid.TablePrefix() + "bal_journal"
		var rp *bal.RollupPebble
		if err := store.DB().QueryRowContext(ctx,
			`SELECT 1 FROM sqlite_master WHERE type='table' AND name=?`, balTable).Scan(&balExists); err == nil {
			bs := bal.NewStore(store.DB(), tid)
			oracles = append(oracles, bs.GlobalFoldOracle(), bs.ChainOracle())
			// Rollup oracle only where the derived plane exists: it
			// proves derive(journal) == cascade THROUGH the grains, so a
			// lost carry upward is caught, not just a leaf mismatch.
			// T-62: the plane is Pebble now, so "exists" means the
			// rollup directory is there, not a SQL table.
			rollupDir := sl.TenantBalRollupDir(*baseDir, tid)
			if _, statErr := os.Stat(rollupDir); statErr == nil {
				var openErr error
				rp, openErr = bal.OpenRollupPebble(rollupDir)
				if openErr != nil {
					fatal("tenant %d: open rollup pebble: %v", tid, openErr)
				}
				bs.SetRollupPebble(rp)
				oracles = append(oracles, bs.RollupOracle())
			}
		}
		results, err := chronicle.CheckAll(ctx, oracles)
		if err != nil {
			if rp != nil {
				_ = rp.Close()
			}
			_ = store.Close()
			fatal("tenant %d: %v", tid, err)
		}
		for _, r := range results {
			checked++
			if r.Equal {
				fmt.Printf("  %-22s PASS\n", r.Name)
				continue
			}
			diverged++
			fmt.Printf("  %-22s DIVERGED  %s\n", r.Name, r.FirstDivergence)
			if *verbose {
				fmt.Printf("    -- derived --\n%s\n    -- current --\n%s\n", indent(r.Derived), indent(r.Current))
			}
		}
		if rp != nil {
			_ = rp.Close()
		}
		_ = store.Close()
	}

	fmt.Printf("db check: %d oracle(s) run, %d diverged\n", checked, diverged)
	if diverged > 0 {
		os.Exit(1)
	}
}

// listTenantIDs enumerates tenants for db check. Per-file mode: every
// tXXXX directory holding a store file. Shared mode: tenant 0 plus every
// row in the shared store's tenants registry (best-effort: a missing
// registry yields just tenant 0).
func listTenantIDs(baseDir string, mode storeMode) ([]tenant.TenantID, error) {
	if mode == modePerFile {
		entries, err := os.ReadDir(baseDir)
		if err != nil {
			return nil, err
		}
		var ids []tenant.TenantID
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			id, ok := sl.ParseTenantSegment(e.Name())
			if !ok {
				continue
			}
			if _, err := os.Stat(storePathFor(baseDir, id, mode)); err == nil {
				ids = append(ids, id)
			}
		}
		return ids, nil
	}
	// Shared mode: one file; tenants come from the registry.
	store, err := openTenantStore(baseDir, 0, mode, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()
	ids := []tenant.TenantID{0}
	rows, err := store.DB().Query(`SELECT id FROM tenants ORDER BY id`)
	if err != nil {
		return ids, nil // no registry: tenant 0 only
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return ids, err
		}
		if id != 0 {
			ids = append(ids, tenant.TenantID(id))
		}
	}
	return ids, rows.Err()
}

func indent(s string) string {
	if s == "" {
		return "    (empty)"
	}
	out := ""
	for _, line := range splitLines(s) {
		out += "    " + line + "\n"
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	return append(lines, s[start:])
}
