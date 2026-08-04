// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"

	"github.com/ha1tch/xolu/pkg/cal"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// cmdTenantProvisionCal creates a tenant's cal occupancy index (H3,
// Pebble) and rebuilds it from the authoritative SQL booking records
// (H1 — cal_calendars/cal_bookings). This is item 24 (wave 6), the
// plan's own "only no-workaround gap": every other wave-6 command has
// some manual path available today, but cal's Pebble index has none —
// it is otherwise only ever created lazily, on a running server's
// first request touching that tenant (cal.Manager.assemble, which
// this command mirrors standalone, not re-implements independently).
// Useful for pre-provisioning ahead of first traffic, and for
// restoring the index after a backup/restore cycle that only carried
// the SQL side.
//
// Checked directly rather than assumed: iolu db init never calls
// InitV2Schema at all — V2 tables (bal/cal/fsm) are created only by
// the SERVER's own lazy per-tenant init on first touch. A tenant that
// has never been hit by a running server therefore has no
// cal_calendars/cal_bookings tables yet, and this command would fail
// against them without calling InitV2Schema itself first — so it
// does, unconditionally and idempotently (CREATE TABLE IF NOT EXISTS
// throughout), covering both the "server already ran" and "cold,
// never-touched tenant" cases with the same code path.
//
// Idempotent and safe to re-run: RebuildFrom clears and repopulates
// the index from the SQL records every time, matching what the server
// itself does on every fresh assembly (store/store.go's own comment:
// "index == rebuild... a fresh process / first-touch correct without
// a separate warmup").
func cmdTenantProvisionCal(args []string) {
	fs := flag.NewFlagSet("iolu tenant provision-cal", flag.ExitOnError)
	baseDir := fs.String("base-dir", "", "xolu data root (required)")
	mode := fs.String("mode", "", "store organisation override: per-file or shared (default: auto-detect)")
	name := fs.String("name", "", "Tenant name (required)")
	_ = fs.Parse(args)

	if *baseDir == "" || *name == "" {
		fs.Usage()
		os.Exit(1)
	}
	storeMode, err := resolveStoreMode(*baseDir, *mode, modePerFile)
	if err != nil {
		fatal("%v", err)
	}

	store, err := openTenantStore(*baseDir, 0, storeMode, false)
	if err != nil {
		fatal("open database: %v", err)
	}
	defer func() { _ = store.Close() }()
	db := store.DB()
	ctx := context.Background()

	var id int
	err = db.QueryRowContext(ctx, `SELECT id FROM tenants WHERE name = ?`, *name).Scan(&id)
	if err == sql.ErrNoRows {
		fatal("tenant %q not found", *name)
	} else if err != nil {
		fatal("query tenant: %v", err)
	}
	tid := tenant.TenantID(id)

	// The store holding THIS tenant's V2 tables: its own file in
	// per-file mode, tenant 0's (already open) shared store otherwise.
	target := store
	if storeMode == modePerFile && tid != 0 {
		tstore, err := openTenantStore(*baseDir, tid, storeMode, false)
		if err != nil {
			fatal("open tenant %d store: %v", tid, err)
		}
		defer func() { _ = tstore.Close() }()
		target = tstore
	}
	if err := target.InitV2Schema(ctx); err != nil {
		fatal("init V2 schema (cal_calendars/cal_bookings): %v", err)
	}

	idxDir := sl.TenantCalDir(*baseDir, tid)
	idx, err := cal.OpenIndexStore(idxDir)
	if err != nil {
		fatal("open cal index store: %v", err)
	}
	defer func() { _ = idx.Close() }()

	src := cal.NewSQLiteBookingSource(target.DB(), tid, false) // false: retire ordinals, matching cal.Manager's own default
	if err := idx.RebuildFrom(src); err != nil {
		fatal("rebuild cal index: %v", err)
	}

	fmt.Printf("provisioned cal occupancy index for tenant %q (ID %d) at %s\n", *name, id, idxDir)
}
