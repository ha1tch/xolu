// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ha1tch/xolu/pkg/bal"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// cmdBalPrune runs bal.Store.PruneJournal across every tenant that has
// bal tables (item 16, prefix-collapse retention —
// chronicle-substrate.md §4b). PruneJournal is Go-only by design, with
// no HTTP surface (docs/KNOWN_ISSUES.md's "bal design" section records
// why); this command is the operator-facing path for triggering it,
// wired directly to the existing Go function rather than duplicating
// its cutoff-selection logic — the same reason there is no separate
// "dry run" mode here: a true dry run would need PruneJournal itself
// to accept an external, rollback-only transaction, which it does not
// today. --yes is the safety gate instead: without it, the command
// reports which tenants have anything eligible and exits without
// touching the database.
//
// --before is a REQUIRED, explicit retention floor, not a default of
// "prune everything sealed" — an irreversible, tenant-wide deletion
// command should never have a silent default that deletes the maximum
// possible amount.
func cmdBalPrune(args []string) {
	fs := flag.NewFlagSet("iolu bal prune", flag.ExitOnError)
	baseDir := fs.String("base-dir", "", "xolu data root (required)")
	mode := fs.String("mode", "", "store organisation override: per-file or shared (default: auto-detect)")
	before := fs.String("before", "", "RFC3339 retention floor: never prune anything at or after this instant (required)")
	yes := fs.Bool("yes", false, "actually prune (without this, reports what has bal tables and exits)")
	_ = fs.Parse(args)

	if *baseDir == "" || *before == "" {
		fs.Usage()
		os.Exit(1)
	}
	beforeAt, err := time.Parse(time.RFC3339, *before)
	if err != nil {
		fatal("--before: %v", err)
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

	type target struct {
		tid tenant.TenantID
	}
	var targets []target
	for _, tid := range tenants {
		store, err := openTenantStore(*baseDir, tid, storeMode, false)
		if err != nil {
			fatal("open tenant %d: %v", tid, err)
		}
		var balExists int
		balTable := tid.TablePrefix() + "bal_journal"
		err = store.DB().QueryRowContext(ctx,
			`SELECT 1 FROM sqlite_master WHERE type='table' AND name=?`, balTable).Scan(&balExists)
		_ = store.Close()
		if err != nil {
			continue // no bal tables for this tenant
		}
		targets = append(targets, target{tid: tid})
	}

	if !*yes {
		if len(targets) == 0 {
			fmt.Println("no tenants have bal tables — nothing to prune")
			return
		}
		fmt.Printf("would attempt prune (before %s) on %d tenant(s):\n", beforeAt.UTC().Format(time.RFC3339), len(targets))
		for _, tg := range targets {
			fmt.Printf("  tenant %d\n", tg.tid)
		}
		fmt.Println("pass --yes to actually prune")
		return
	}

	totalPruned := 0
	for _, tg := range targets {
		store, err := openTenantStore(*baseDir, tg.tid, storeMode, false)
		if err != nil {
			fatal("open tenant %d: %v", tg.tid, err)
		}
		bs := bal.NewStore(store.DB(), tg.tid)
		sealer, err := bal.LoadSealer(ctx, store.DB(), tg.tid)
		if err != nil {
			_ = store.Close()
			fatal("tenant %d: load sealer: %v", tg.tid, err)
		}
		bs.SetSealer(sealer)

		n, err := bs.PruneJournal(ctx, beforeAt)
		if err != nil {
			_ = store.Close()
			fatal("tenant %d: prune: %v", tg.tid, err)
		}
		fmt.Printf("tenant %d: pruned %d row(s)\n", tg.tid, n)
		totalPruned += n
		_ = store.Close()
	}
	fmt.Printf("prune: %d row(s) pruned across %d tenant(s)\n", totalPruned, len(targets))
}
