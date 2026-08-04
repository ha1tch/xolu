// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"context"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/cal"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// seedCalBooking writes one calendar and one binding booking directly
// via pkg/cal's SQL source, over the tenant's own store file --
// mirroring setupBalData's approach in balprune_test.go for the same
// reason: iolu itself has no "create a booking" command (booking
// creation is deliberately not on the wire, per the client
// integration test's own seeding note), so direct construction is the
// only way to get real data into the fixture.
func seedCalBooking(t *testing.T, base string, tid tenant.TenantID) cal.Calendar {
	t.Helper()
	dbPath := sl.TenantStorePath(base, tid)
	st, err := storage.NewSQLiteStore(dbPath, storage.SQLiteConfig{DBPath: dbPath, TenantID: tid})
	if err != nil {
		t.Fatalf("open tenant store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.InitV2Schema(context.Background()); err != nil {
		t.Fatalf("InitV2Schema: %v", err)
	}

	src := cal.NewSQLiteBookingSource(st.DB(), tid, false)
	c, err := src.CreateCalendar(cal.Calendar{
		CalendarID:   "room-a",
		DefaultState: cal.StateBinding,
		MatchPolicy:  cal.ConsiderBinding,
	})
	if err != nil {
		t.Fatalf("create calendar: %v", err)
	}

	start := ot.FromTime(time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC))
	end := ot.FromTime(time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC))
	if err := src.PutBooking(cal.Booking{
		BookingID:  "b1",
		CalendarID: "room-a",
		State:      cal.StateBinding,
		Span:       cal.Span{Start: start, End: end},
		Mode:       cal.ModeExclusive,
		Bearer:     1,
		CreatedAt:  ot.Now(),
		UpdatedAt:  ot.Now(),
	}); err != nil {
		t.Fatalf("put booking: %v", err)
	}
	return c
}

func TestTenantProvisionCal_RebuildsFromRealBookingData(t *testing.T) {
	base := t.TempDir()
	cmdDBInit([]string{"--base-dir", base, "--mode", "per-file", "--tenant", "acme:5", "--graph=false"})

	tid := tenant.TenantID(5)
	seeded := seedCalBooking(t, base, tid)

	cmdTenantProvisionCal([]string{"--base-dir", base, "--name", "acme"})

	// Open the index the command just built and confirm it actually
	// reflects the booking that was already in SQL -- not just that a
	// directory now exists, which an empty rebuild would also produce.
	// The ordinal map itself is in-memory only (store.go: "rebuildable")
	// and is never restored by Open alone -- the real server never
	// relies on that either, it calls RebuildFrom on every assembly
	// (manager.go's own doc: "a fresh process / first-touch correct
	// without a separate warmup"). RegisterCalendar here restores just
	// the ordinal mapping this fresh handle needs to read what
	// cmdTenantProvisionCal already wrote, without re-deriving it a
	// second time -- keeping the assertion about THAT write, not about
	// RebuildFrom's own correctness (already pkg/cal's own concern).
	idxDir := sl.TenantCalDir(base, tid)
	idx, err := cal.OpenIndexStore(idxDir)
	if err != nil {
		t.Fatalf("open provisioned index: %v", err)
	}
	defer func() { _ = idx.Close() }()
	idx.RegisterCalendar(seeded)

	occ, err := idx.ReadOccupancy("room-a")
	if err != nil {
		t.Fatalf("ReadOccupancy: %v", err)
	}
	if occ == nil {
		t.Fatal("expected non-nil occupancy for room-a after rebuild -- the seeded booking should be reflected")
	}
}
