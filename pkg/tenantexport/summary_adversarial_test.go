// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// summary_adversarial_test.go — intense adversarial testing against
// Summarize (XOT213, wave 15/16), requested unsolicited after that
// work shipped, then implemented for real. Each test either confirms
// existing behavior is correct under a hostile input/timing, or
// surfaces a genuine finding, not assumed safe by inspection alone.

package tenantexport

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/ha1tch/xolu/pkg/blob"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// TestSummarize_ConcurrentWriteRace tests a concern never actually
// exercised while building Summarize: it runs roughly ten separate,
// non-transactional queries across SQL tables, dedicated files,
// Pebble stores, and blob storage -- nothing holds a consistent
// snapshot across all of them. This test makes that concrete and
// deterministic, using testHookAfterPrimaryRead (a minimal, test-only
// synchronization point, nil on every real path) rather than a
// many-iterations probabilistic attempt at the same timing.
//
// Scenario: a genuinely empty tenant. Summarize reads Primary (finds
// 0 rows, correctly, at that instant) and pauses at the hook. While
// paused, a real write lands directly in the primary table -- the
// tenant is now genuinely non-empty. Summarize resumes, reads
// everything else (still legitimately empty), and returns.
func TestSummarize_ConcurrentWriteRace(t *testing.T) {
	base := t.TempDir()
	tenantID := tenant.TenantID(13)

	primaryDB, err := sql.Open("sqlite", base+"/primary.db")
	if err != nil {
		t.Fatalf("open primary db: %v", err)
	}
	defer primaryDB.Close()

	orig := PrimaryStoreTables
	origLoc := LocStoreTables
	origObj := ObjStoreTables
	PrimaryStoreTables = []SQLiteTableSpec{{Name: "nodes", TenantFiltered: false}}
	LocStoreTables = []SQLiteTableSpec{}
	ObjStoreTables = []SQLiteTableSpec{}
	defer func() { PrimaryStoreTables = orig; LocStoreTables = origLoc; ObjStoreTables = origObj }()
	if _, err := primaryDB.Exec(`CREATE TABLE nodes (id INTEGER)`); err != nil {
		t.Fatalf("create nodes: %v", err)
	}

	primaryRead := make(chan struct{})
	proceed := make(chan struct{})
	testHookAfterPrimaryRead = func() {
		close(primaryRead)
		<-proceed
	}
	defer func() { testHookAfterPrimaryRead = nil }()

	type result struct {
		summary *TenantDataSummary
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		s, err := Summarize(context.Background(), primaryDB, base, tenantID, nil)
		resultCh <- result{s, err}
	}()

	<-primaryRead // Summarize has read Primary (0 rows) and is now paused.

	if _, err := primaryDB.Exec(`INSERT INTO nodes VALUES (1)`); err != nil {
		t.Fatalf("concurrent write while Summarize is paused: %v", err)
	}

	close(proceed) // let Summarize finish reading everything else.
	r := <-resultCh
	if r.err != nil {
		t.Fatalf("Summarize: %v", r.err)
	}

	// Ground truth, checked directly and independently of Summarize's
	// own machinery: the tenant is NOT empty right now.
	var realCount int
	if err := primaryDB.QueryRow(`SELECT COUNT(*) FROM nodes`).Scan(&realCount); err != nil {
		t.Fatalf("ground-truth count: %v", err)
	}
	if realCount != 1 {
		t.Fatalf("test setup itself is wrong: want 1 real row, got %d", realCount)
	}

	if r.summary.Primary["nodes"] != 0 {
		t.Errorf("Summarize's own Primary count: want the stale value 0 (read before the concurrent write), got %d -- "+
			"either the race didn't land as timed, or Summarize re-reads Primary somewhere it shouldn't",
			r.summary.Primary["nodes"])
	}
	if !r.summary.Empty() {
		t.Fatalf("did not reproduce the TOCTOU gap this test exists to demonstrate: Summarize's own Empty() "+
			"was expected to (wrongly) report true. Got false -- summary: %+v", r.summary)
	}

	t.Logf("CONFIRMED FINDING: Summarize reported Empty()=true for tenant %d immediately after it returned, "+
		"while the tenant genuinely held 1 real row (confirmed by an independent, direct COUNT query) that "+
		"landed during the call itself, in a store Summarize had already read by the time the write happened. "+
		"Summarize's own snapshot is not atomic across the several stores it queries -- inherent to querying "+
		"multiple independent storage engines (SQL, two dedicated SQLite files, three Pebble stores, blob "+
		"storage) with no shared transaction possible across all of them, not a bug introduced by any one "+
		"query's own logic. A caller relying on Empty() as a live guarantee rather than a point-in-time "+
		"snapshot could act on a stale answer.", tenantID)
}

// TestSummarize_BlobEnabledButBroken tests the specific claim made
// directly in handleTenantSummary's own comment: that a genuinely
// FAILING blob store (as opposed to blob merely being disabled,
// which correctly summarizes to zero) is treated as a hard error
// rather than a quiet zero -- designed for, and asserted in a
// comment, but never actually exercised against a real failure until
// now. Every existing test only covered "blob works" or "blob is
// nil" (disabled).
//
// Corruption: blob.Store.List() treats a MISSING .keys directory as
// "zero blobs, not an error" (confirmed directly by reading its own
// source before choosing this corruption -- deleting .keys would not
// exercise the failure path at all, it would silently look identical
// to "no blobs uploaded yet"). Replacing .keys with a plain file
// instead -- something os.ReadDir genuinely cannot read as a
// directory -- is what actually reaches the error path.
func TestSummarize_BlobEnabledButBroken(t *testing.T) {
	base := t.TempDir()
	tenantID := tenant.TenantID(17)

	primaryDB, err := sql.Open("sqlite", base+"/primary.db")
	if err != nil {
		t.Fatalf("open primary db: %v", err)
	}
	defer primaryDB.Close()

	orig := PrimaryStoreTables
	origLoc := LocStoreTables
	origObj := ObjStoreTables
	PrimaryStoreTables = []SQLiteTableSpec{}
	LocStoreTables = []SQLiteTableSpec{}
	ObjStoreTables = []SQLiteTableSpec{}
	defer func() { PrimaryStoreTables = orig; LocStoreTables = origLoc; ObjStoreTables = origObj }()

	blobDir := t.TempDir()
	bs, err := blob.NewStore(blobDir, 0)
	if err != nil {
		t.Fatalf("blob.NewStore: %v", err)
	}
	// Real use first, so .keys genuinely exists as a real directory
	// before it gets corrupted -- proving this isn't just "never
	// initialized."
	if _, _, _, err := bs.Put("seed.txt", strings.NewReader("x"), "text/plain"); err != nil {
		t.Fatalf("blob Put (before corruption): %v", err)
	}

	keysDir := blobDir + "/.keys"
	if err := os.RemoveAll(keysDir); err != nil {
		t.Fatalf("remove .keys to replace it: %v", err)
	}
	if err := os.WriteFile(keysDir, []byte("not a directory"), 0644); err != nil {
		t.Fatalf("replace .keys with a plain file: %v", err)
	}

	_, err = Summarize(context.Background(), primaryDB, base, tenantID, bs)
	if err == nil {
		t.Fatalf("CONFIRMED FINDING: Summarize succeeded despite a genuinely broken " +
			"blob store (.keys replaced with a plain file, not merely absent) -- " +
			"want a hard error, matching the design reasoning stated directly in " +
			"handleTenantSummary's own comment: a real blob failure should not be " +
			"swallowed into a quiet zero the way a merely-disabled primitive is.")
	}
	t.Logf("Summarize correctly failed loudly on a genuinely broken blob store: %v", err)
}
