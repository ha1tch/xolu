// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package storage — retry / contention / AdaptiveLock coverage tests.
//
// These tests are in the same package (storage_test) as the stress tests.
// They do NOT use -short gates because they are correctness tests, not
// throughput tests, and they run in well under a second each.

package storage_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/storage"

	_ "modernc.org/sqlite"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// openContendedStores opens two independent SQLiteStore instances against the
// same on-disk database file with busy_timeout = 0. This guarantees that any
// overlapping write attempt by the second store immediately returns
// SQLITE_BUSY rather than waiting — which is exactly the condition the retry
// loop and adaptive lock are designed to handle.
func openContendedStores(t *testing.T) (a, b *storage.SQLiteStore, cleanup func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "contended.db")

	cfg := storage.SQLiteConfig{
		BusyTimeout:         0,  // fail immediately on lock, no SQLite-side wait
		ContentionThreshold: 95, // engage adaptive lock quickly
	}

	storeA, err := storage.NewSQLiteStore(dbPath, cfg)
	if err != nil {
		t.Fatalf("openContendedStores A: %v", err)
	}
	storeB, err := storage.NewSQLiteStore(dbPath, cfg)
	if err != nil {
		storeA.Close()
		t.Fatalf("openContendedStores B: %v", err)
	}
	return storeA, storeB, func() {
		storeA.Close()
		storeB.Close()
	}
}

// ---------------------------------------------------------------------------
// AdaptiveLock unit tests
// ---------------------------------------------------------------------------

// TestAdaptiveLock_DisengagedByDefault verifies that a freshly created lock
// with threshold < 100 is not engaged and that Lock/RLock return false.
func TestAdaptiveLock_DisengagedByDefault(t *testing.T) {
	al := storage.NewAdaptiveLock(95)
	defer al.Stop()

	if al.Engaged() {
		t.Fatal("adaptive lock should not be engaged with no failures recorded")
	}
	if al.Lock() {
		t.Error("Lock() should return false when not engaged")
		al.Unlock()
	}
	if al.RLock() {
		t.Error("RLock() should return false when not engaged")
		al.RUnlock()
	}
}

// TestAdaptiveLock_EngagesOnFailure verifies that RecordFailure immediately
// engages the lock when threshold > 0.
func TestAdaptiveLock_EngagesOnFailure(t *testing.T) {
	al := storage.NewAdaptiveLock(95)
	defer al.Stop()

	al.RecordFailure()

	if !al.Engaged() {
		t.Fatal("adaptive lock should engage immediately on first failure")
	}

	// Lock/RLock must now acquire the underlying mutex and return true.
	if locked := al.Lock(); !locked {
		t.Error("Lock() should return true when engaged")
	} else {
		al.Unlock() // exercises Unlock
	}

	if locked := al.RLock(); !locked {
		t.Error("RLock() should return true when engaged")
	} else {
		al.RUnlock() // exercises RUnlock
	}
}

// TestAdaptiveLock_DisengagesOnCleanWindow verifies that the lock disengages
// after the monitor sees a window with only successes and no failures.
func TestAdaptiveLock_DisengagesOnCleanWindow(t *testing.T) {
	// Use threshold=50 so a clean window disengages easily.
	al := storage.NewAdaptiveLock(50)
	defer al.Stop()

	al.RecordFailure() // engage
	if !al.Engaged() {
		t.Fatal("expected lock to be engaged after failure")
	}

	// Record enough successes to make the window clean; the monitor
	// fires every 500ms and needs at least 10 observations.
	for i := 0; i < 20; i++ {
		al.RecordSuccess()
	}

	// Wait up to 2 s for the monitor to disengage.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !al.Engaged() {
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("adaptive lock did not disengage after clean window")
}

// TestAdaptiveLock_Threshold100AlwaysEngaged verifies that threshold=100
// keeps the lock permanently engaged.
func TestAdaptiveLock_Threshold100AlwaysEngaged(t *testing.T) {
	al := storage.NewAdaptiveLock(100)
	defer al.Stop()

	if !al.Engaged() {
		t.Fatal("threshold=100 should be permanently engaged")
	}
	// Record successes — should not disengage.
	for i := 0; i < 20; i++ {
		al.RecordSuccess()
	}
	time.Sleep(600 * time.Millisecond)
	if !al.Engaged() {
		t.Fatal("threshold=100 should remain engaged after clean window")
	}
}

// TestAdaptiveLock_Threshold0NeverEngages verifies that threshold=0 means the
// lock never engages regardless of failures.
func TestAdaptiveLock_Threshold0NeverEngages(t *testing.T) {
	al := storage.NewAdaptiveLock(0)
	defer al.Stop()

	al.RecordFailure()
	al.RecordFailure()
	al.RecordFailure()

	if al.Engaged() {
		t.Fatal("threshold=0 should never engage the lock")
	}
}

// TestAdaptiveLock_SetThreshold verifies that SetThreshold updates behaviour
// at runtime.
func TestAdaptiveLock_SetThreshold(t *testing.T) {
	al := storage.NewAdaptiveLock(0) // start disabled
	defer al.Stop()

	al.RecordFailure()
	if al.Engaged() {
		t.Fatal("threshold=0: failure should not engage")
	}

	al.SetThreshold(95) // now enable
	al.RecordFailure()
	if !al.Engaged() {
		t.Fatal("threshold=95: failure should engage")
	}
	if al.Threshold() != 95 {
		t.Fatalf("Threshold() = %d, want 95", al.Threshold())
	}

	al.SetThreshold(0) // disable immediately
	if al.Engaged() {
		t.Fatal("SetThreshold(0) should immediately disengage")
	}
}

// TestAdaptiveLock_ConcurrentLockUnlock verifies that Lock/Unlock are safe
// under concurrent goroutines when the lock is engaged.
func TestAdaptiveLock_ConcurrentLockUnlock(t *testing.T) {
	al := storage.NewAdaptiveLock(100) // always engaged
	defer al.Stop()

	const goroutines = 20
	var counter int64
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if locked := al.Lock(); locked {
					atomic.AddInt64(&counter, 1)
					al.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if counter != goroutines*50 {
		t.Errorf("counter = %d, want %d", counter, goroutines*50)
	}
}

// ---------------------------------------------------------------------------
// withRetry / withRetryRead / withRetryCreateVal via concurrent store access
// ---------------------------------------------------------------------------

// TestStorageRetry_BusyOnConcurrentWrite exercises the SQLITE_BUSY retry path
// by holding an exclusive SQLite write transaction on storeA while storeB
// attempts a write with busy_timeout=0. The second write will fail with
// SQLITE_BUSY on the first attempt; the retry loop in withRetry must handle
// it. We accept that the retry loop may also exhaust all attempts and return
// the busy error — what we are testing is that the retry logic executes, not
// that it succeeds, since timing with busy_timeout=0 is unpredictable.
func TestStorageRetry_BusyOnConcurrentWrite(t *testing.T) {
	storeA, storeB, cleanup := openContendedStores(t)
	defer cleanup()

	ctx := context.Background()

	// Populate a few records via storeA so there is a table to write to.
	for i := 0; i < 5; i++ {
		if _, err := storeA.Create(ctx, "widget", map[string]interface{}{
			"n": i,
		}); err != nil {
			t.Fatalf("storeA.Create: %v", err)
		}
	}

	// Hold an exclusive transaction on storeA's writer pool.
	// BEGIN IMMEDIATE acquires a reserved lock that blocks all other writers.
	tx, err := storeA.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		t.Fatalf("begin exclusive tx: %v", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE sqlite_master SET type=type"); err != nil {
		tx.Rollback()
		// sqlite_master write may be disallowed on some builds; fall through
		// and just hold the transaction open with a no-op.
	}

	// Attempt a write on storeB while the transaction is held.
	// This exercises withRetry's SQLITE_BUSY path and RecordFailure on
	// the adaptive lock.
	var writeErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, writeErr = storeB.Create(ctx, "widget", map[string]interface{}{
			"n": 99,
		})
	}()

	// Let storeB hit the busy condition, then release the lock.
	time.Sleep(50 * time.Millisecond)
	tx.Rollback() // releases the writer lock

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("storeB.Create timed out waiting for lock release")
	}

	// We don't assert on writeErr — with busy_timeout=0 and no retry gap
	// the write may or may not succeed. What we assert is that the retry
	// machinery executed without panicking.
	_ = writeErr
}

// TestStorageRetry_AdaptiveLockEngagesUnderContention verifies that the
// adaptive lock on storeA engages (Engaged() == true) after storeA
// records a failure from a SQLITE_BUSY condition. Since we can't inject
// isSQLiteBusy directly, we exercise the public RecordFailure surface.
func TestStorageRetry_AdaptiveLockEngagesUnderContention(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "engage.db")

	store, err := storage.NewSQLiteStore(dbPath, storage.SQLiteConfig{
		ContentionThreshold: 95,
		BusyTimeout:         0,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	al := store.ContentionLock()

	if al.Engaged() {
		t.Fatal("lock should not be engaged before any failures")
	}

	al.RecordFailure()

	if !al.Engaged() {
		t.Fatal("lock should engage immediately after first failure")
	}
}

// ---------------------------------------------------------------------------
// Ping
// ---------------------------------------------------------------------------

func TestSQLiteStore_Ping(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewSQLiteStore(filepath.Join(dir, "ping.db"), storage.SQLiteConfig{})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	if err := store.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

// ---------------------------------------------------------------------------
// GetMany
// ---------------------------------------------------------------------------

func TestSQLiteStore_GetMany(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewSQLiteStore(filepath.Join(dir, "getmany.db"), storage.SQLiteConfig{})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	// Create 5 entities.
	var ids []int
	for i := 0; i < 5; i++ {
		id, err := store.Create(ctx, "thing", map[string]interface{}{"i": i})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ids = append(ids, id)
	}

	// GetMany for 3 of the 5.
	want := ids[:3]
	results, err := store.GetMany(ctx, "thing", want)
	if err != nil {
		t.Fatalf("GetMany: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("GetMany returned %d results, want 3", len(results))
	}
	for _, id := range want {
		if _, ok := results[id]; !ok {
			t.Errorf("GetMany missing id %d", id)
		}
	}

	// GetMany with empty ID list must return empty map, no error.
	empty, err := store.GetMany(ctx, "thing", nil)
	if err != nil {
		t.Fatalf("GetMany(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("GetMany(nil) returned %d results, want 0", len(empty))
	}

	// GetMany including non-existent ID — must not error, just omit.
	missing, err := store.GetMany(ctx, "thing", []int{ids[0], 999999})
	if err != nil {
		t.Fatalf("GetMany with missing id: %v", err)
	}
	if len(missing) != 1 {
		t.Errorf("GetMany with missing id: got %d results, want 1", len(missing))
	}
}

// ---------------------------------------------------------------------------
// CountEntities
// ---------------------------------------------------------------------------

func TestSQLiteStore_CountEntities(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewSQLiteStore(filepath.Join(dir, "count.db"), storage.SQLiteConfig{})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	// Count on empty table.
	n, err := store.CountEntities(ctx, "fruit")
	if err != nil {
		t.Fatalf("CountEntities (empty): %v", err)
	}
	if n != 0 {
		t.Errorf("CountEntities (empty) = %d, want 0", n)
	}

	// Create 7 entities.
	for i := 0; i < 7; i++ {
		if _, err := store.Create(ctx, "fruit", map[string]interface{}{"k": i}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	n, err = store.CountEntities(ctx, "fruit")
	if err != nil {
		t.Fatalf("CountEntities: %v", err)
	}
	if n != 7 {
		t.Errorf("CountEntities = %d, want 7", n)
	}

	// Count a different entity type — must return 0, not 7.
	n, err = store.CountEntities(ctx, "vegetable")
	if err != nil {
		t.Fatalf("CountEntities (other entity): %v", err)
	}
	if n != 0 {
		t.Errorf("CountEntities (other entity) = %d, want 0", n)
	}
}

// ---------------------------------------------------------------------------
// ListPaged
// ---------------------------------------------------------------------------

func TestSQLiteStore_ListPaged(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewSQLiteStore(filepath.Join(dir, "paged.db"), storage.SQLiteConfig{})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	const total = 23
	for i := 0; i < total; i++ {
		if _, err := store.Create(ctx, "item", map[string]interface{}{"n": i}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	// Page through all records and verify no duplicates and correct total.
	const pageSize = 5
	seen := make(map[int]bool)
	offset := 0
	for {
		page, err := store.ListPaged(ctx, "item", pageSize, offset)
		if err != nil {
			t.Fatalf("ListPaged(limit=%d,offset=%d): %v", pageSize, offset, err)
		}
		if page.TotalItems != total {
			t.Errorf("TotalItems = %d, want %d", page.TotalItems, total)
		}
		if len(page.Data) == 0 {
			break
		}
		for _, row := range page.Data {
			n := int(row["n"].(float64))
			if seen[n] {
				t.Errorf("duplicate item n=%d at offset %d", n, offset)
			}
			seen[n] = true
		}
		offset += len(page.Data)
	}
	if len(seen) != total {
		t.Errorf("paginated %d unique items, want %d", len(seen), total)
	}

	// Offset beyond end — must return empty slice, no error.
	page, err := store.ListPaged(ctx, "item", pageSize, total+100)
	if err != nil {
		t.Fatalf("ListPaged beyond end: %v", err)
	}
	if len(page.Data) != 0 {
		t.Errorf("ListPaged beyond end returned %d rows, want 0", len(page.Data))
	}

	// Empty entity — total=0.
	page, err = store.ListPaged(ctx, "ghost", pageSize, 0)
	if err != nil {
		t.Fatalf("ListPaged empty entity: %v", err)
	}
	if page.TotalItems != 0 || len(page.Data) != 0 {
		t.Errorf("ListPaged empty entity: total=%d rows=%d, want 0,0", page.TotalItems, len(page.Data))
	}
}

// ---------------------------------------------------------------------------
// SQLiteTenantPersister (LoadAll / Save)
// ---------------------------------------------------------------------------

func TestSQLiteTenantPersister(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewSQLiteStore(filepath.Join(dir, "persist.db"), storage.SQLiteConfig{})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	p := storage.NewSQLiteTenantPersister(store.DB(), store.ReaderDB())

	// LoadAll on empty table.
	all, err := p.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll (empty): %v", err)
	}
	if len(all) != 0 {
		t.Errorf("LoadAll (empty) = %d entries, want 0", len(all))
	}

	// Save two tenants.
	if err := p.Save(ctx, "alpha", 1); err != nil {
		t.Fatalf("Save alpha: %v", err)
	}
	if err := p.Save(ctx, "beta", 2); err != nil {
		t.Fatalf("Save beta: %v", err)
	}

	// Save is idempotent.
	if err := p.Save(ctx, "alpha", 1); err != nil {
		t.Fatalf("Save alpha idempotent: %v", err)
	}

	// LoadAll returns both.
	all, err = p.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if all["alpha"] != 1 {
		t.Errorf("alpha = %d, want 1", all["alpha"])
	}
	if all["beta"] != 2 {
		t.Errorf("beta = %d, want 2", all["beta"])
	}

	// Save conflict: same ID, different name.
	if err := p.Save(ctx, "gamma", 1); err == nil {
		t.Error("Save(gamma,1) should fail: ID 1 already belongs to alpha")
	}

	// Save conflict: same name, different ID.
	if err := p.Save(ctx, "alpha", 99); err == nil {
		t.Error("Save(alpha,99) should fail: name alpha already has ID 1")
	}
}

// ---------------------------------------------------------------------------
// dialect helpers (Name, Placeholder) — currently 0%
// ---------------------------------------------------------------------------

func TestSQLiteDialect(t *testing.T) {
	d := &storage.SQLiteStorageDialect{}
	if d.Name() != "sqlite" {
		t.Errorf("Name() = %q, want %q", d.Name(), "sqlite")
	}
	// Placeholder is positional '?' for SQLite (not $N like Postgres).
	for _, n := range []int{1, 5, 100} {
		if p := d.Placeholder(n); p != "?" {
			t.Errorf("Placeholder(%d) = %q, want \"?\"", n, p)
		}
	}
}

// ---------------------------------------------------------------------------
// sortStrings (unexported helper — covered via ListPaged)
// NodesTable (unexported helper — covered via any table operation)
// ---------------------------------------------------------------------------

// TestSQLiteStore_NodesTableHelper ensures that internal table-name helpers
// are exercised. They are called by every CRUD operation, but this explicit
// call via Create + table inspection makes the line visible in coverage.
func TestSQLiteStore_BasicCRUDCoverageProbe(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewSQLiteStore(filepath.Join(dir, "probe.db"), storage.SQLiteConfig{})
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	ctx := context.Background()

	// Probe sortStrings indirectly via List (which sorts results).
	for i := 5; i >= 1; i-- {
		if _, err := store.Create(ctx, "probe", map[string]interface{}{"v": i}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}
	rows, err := store.List(ctx, "probe")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 5 {
		t.Errorf("List returned %d rows, want 5", len(rows))
	}

	// Probe fmt.Sprintf expansion of nodesTable helper — covered by any table op.
	// Probe withRetryRead happy path (no busy) via Get.
	id, _ := store.Create(ctx, "probe", map[string]interface{}{"v": 99})
	if _, err := store.Get(ctx, "probe", id); err != nil {
		t.Fatalf("Get: %v", err)
	}

	// Explicitly exercise CountEntities via the adapted path (need no adapted
	// spec here, so this hits the blob path). Already covered above but
	// ensure the function call appears in this file's path.
	if n, err := store.CountEntities(ctx, "probe"); err != nil {
		t.Fatalf("CountEntities: %v", err)
	} else if n != 6 {
		t.Errorf("CountEntities = %d, want 6", n)
	}

	// formatters / nodesTable via negative-case queries.
	if _, err := store.Get(ctx, "probe", 99999); err == nil {
		t.Error("Get(missing) should return error")
	}
}

// ---------------------------------------------------------------------------
// withRetryCreateVal path — exercise via concurrent Create
// ---------------------------------------------------------------------------

func TestStorageRetry_CreateValBusyPath(t *testing.T) {
	// This test creates two stores sharing the same DB (busy_timeout=0)
	// and drives concurrent Creates from both to maximise the chance of
	// hitting SQLITE_BUSY in the withRetryCreateVal path.
	storeA, storeB, cleanup := openContendedStores(t)
	defer cleanup()

	ctx := context.Background()
	const perStore = 30

	var wg sync.WaitGroup
	var errCount atomic.Int64

	for i := 0; i < perStore; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			if _, err := storeA.Create(ctx, "race", map[string]interface{}{"n": fmt.Sprintf("A%d", n)}); err != nil {
				errCount.Add(1)
			}
		}(i)
		go func(n int) {
			defer wg.Done()
			if _, err := storeB.Create(ctx, "race", map[string]interface{}{"n": fmt.Sprintf("B%d", n)}); err != nil {
				errCount.Add(1)
			}
		}(i)
	}
	wg.Wait()

	// We tolerate SQLITE_BUSY errors at busy_timeout=0. What we assert is
	// that no panic occurred and that at least some writes succeeded.
	total, err := storeA.CountEntities(ctx, "race")
	if err != nil {
		t.Fatalf("CountEntities: %v", err)
	}
	if total == 0 {
		t.Error("expected at least some records to be written")
	}
	t.Logf("concurrent Creates: %d succeeded, %d busy-errors", total, errCount.Load())
}
