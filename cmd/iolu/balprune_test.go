// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/bal"
	"github.com/ha1tch/xolu/pkg/chronicle"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// setupBalData seeds tenant 0's store with a sealed, prunable bal
// history directly via pkg/bal -- iolu itself has no "bal init"
// command (bal tables are created lazily by the server), so this is
// the same kind of direct-construction setup TestDBInit_PerFileLayout
// uses for graph tables.
func setupBalData(t *testing.T, base string) {
	t.Helper()
	db, err := sql.Open("sqlite", sl.TenantStorePath(base, 0))
	if err != nil {
		t.Fatalf("open tenant store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	ctx := context.Background()
	s := bal.NewStore(db, tenant.TenantID(0))
	if err := s.Init(ctx); err != nil {
		t.Fatalf("bal Init: %v", err)
	}
	if err := s.InitSeal(ctx); err != nil {
		t.Fatalf("bal InitSeal: %v", err)
	}
	sealer, err := chronicle.NewSealer(chronicle.MonthWindows)
	if err != nil {
		t.Fatal(err)
	}
	s.SetSealer(sealer)

	if _, err := s.DefineAccount(ctx, bal.AccountDef{ID: "~in", Unit: "u", Floor: -1 << 40, Postable: true}); err != nil {
		t.Fatalf("define ~in: %v", err)
	}
	if _, err := s.DefineAccount(ctx, bal.AccountDef{ID: "acct", Unit: "u", Postable: true}); err != nil {
		t.Fatalf("define acct: %v", err)
	}
	if err := s.Transfer(ctx, "seed", "~in", "acct", 100, "",
		time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed transfer: %v", err)
	}

	if _, err := s.SealPeriod(ctx, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("SealPeriod: %v", err)
	}
}

func journalRowCountAt(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM t0000_bal_journal`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBalPrune_WithoutYesDoesNotTouchTheDatabase(t *testing.T) {
	base := t.TempDir()
	cmdDBInit([]string{"--base-dir", base, "--mode", "per-file", "--tenant", "acme"})
	setupBalData(t, base)

	before := journalRowCountAt(t, sl.TenantStorePath(base, 0))
	cmdBalPrune([]string{"--base-dir", base, "--before", "2027-01-01T00:00:00Z"}) // no --yes
	after := journalRowCountAt(t, sl.TenantStorePath(base, 0))

	if after != before {
		t.Fatalf("row count changed without --yes: before %d, after %d", before, after)
	}
}

func TestBalPrune_WithYesActuallyPrunes(t *testing.T) {
	base := t.TempDir()
	cmdDBInit([]string{"--base-dir", base, "--mode", "per-file", "--tenant", "acme"})
	setupBalData(t, base)

	before := journalRowCountAt(t, sl.TenantStorePath(base, 0))
	if before == 0 {
		t.Fatal("test setup produced zero journal rows -- fixture is broken, not proving anything")
	}

	cmdBalPrune([]string{"--base-dir", base, "--before", "2027-01-01T00:00:00Z", "--yes"})

	after := journalRowCountAt(t, sl.TenantStorePath(base, 0))
	if after != 0 {
		t.Fatalf("expected the whole sealed history to be prunable: %d rows remain", after)
	}
}

func TestBalPrune_RetentionFloorIsRespectedThroughIolu(t *testing.T) {
	base := t.TempDir()
	cmdDBInit([]string{"--base-dir", base, "--mode", "per-file", "--tenant", "acme"})
	setupBalData(t, base)

	before := journalRowCountAt(t, sl.TenantStorePath(base, 0))

	// A retention floor before the sealed period: nothing should be
	// pruned even with --yes, proving --before reaches PruneJournal
	// correctly through the whole iolu wiring, not just when it's
	// permissive.
	cmdBalPrune([]string{"--base-dir", base, "--before", "2026-01-01T00:00:00Z", "--yes"})

	after := journalRowCountAt(t, sl.TenantStorePath(base, 0))
	if after != before {
		t.Fatalf("an early retention floor pruned rows anyway: before %d, after %d", before, after)
	}
}
