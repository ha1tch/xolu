// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package dynconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newTmp(t *testing.T) (*DynConfig, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "dynconfig.json")
	dc, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return dc, path
}

func mustRaw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// ---------------------------------------------------------------------------
// New
// ---------------------------------------------------------------------------

func TestNew_NoFile(t *testing.T) {
	// File does not exist — should start empty without error.
	dc, _ := newTmp(t)
	if got := dc.Dump(); len(got) != 0 {
		t.Errorf("expected empty store, got %v", got)
	}
}

func TestNew_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dynconfig.json")

	initial := `{"global":{"blob.max_bytes":1048576}}`
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	dc, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	v, ok := dc.GetInt64("global", "blob.max_bytes")
	if !ok || v != 1048576 {
		t.Errorf("expected 1048576, got %d (ok=%v)", v, ok)
	}
}

func TestNew_MalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dynconfig.json")
	if err := os.WriteFile(path, []byte("{not valid json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := New(path)
	if err == nil {
		t.Fatal("expected error for malformed file, got nil")
	}
}

// ---------------------------------------------------------------------------
// Set / Get round-trips
// ---------------------------------------------------------------------------

func TestSet_GetInt64(t *testing.T) {
	dc, _ := newTmp(t)
	if err := dc.Set("global", "blob.max_bytes", mustRaw(int64(4096))); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, ok := dc.GetInt64("global", "blob.max_bytes")
	if !ok || v != 4096 {
		t.Errorf("GetInt64: got %d ok=%v, want 4096 true", v, ok)
	}
}

func TestSet_GetFloat64(t *testing.T) {
	dc, _ := newTmp(t)
	if err := dc.Set("ns", "rate", mustRaw(3.14)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, ok := dc.GetFloat64("ns", "rate")
	if !ok || v != 3.14 {
		t.Errorf("GetFloat64: got %f ok=%v, want 3.14 true", v, ok)
	}
}

func TestSet_GetString(t *testing.T) {
	dc, _ := newTmp(t)
	if err := dc.Set("ns", "mode", mustRaw("strict")); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, ok := dc.GetString("ns", "mode")
	if !ok || v != "strict" {
		t.Errorf("GetString: got %q ok=%v, want strict true", v, ok)
	}
}

func TestSet_GetBool(t *testing.T) {
	dc, _ := newTmp(t)
	if err := dc.Set("ns", "enabled", mustRaw(true)); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, ok := dc.GetBool("ns", "enabled")
	if !ok || !v {
		t.Errorf("GetBool: got %v ok=%v, want true true", v, ok)
	}
}

func TestGet_AbsentKeyReturnsNil(t *testing.T) {
	dc, _ := newTmp(t)
	if got := dc.Get("nonexistent", "key"); got != nil {
		t.Errorf("expected nil for absent key, got %v", got)
	}
}

func TestGetInt64_AbsentReturnsFalse(t *testing.T) {
	dc, _ := newTmp(t)
	_, ok := dc.GetInt64("ns", "missing")
	if ok {
		t.Error("expected ok=false for absent key")
	}
}

func TestGetInt64_WrongType(t *testing.T) {
	dc, _ := newTmp(t)
	_ = dc.Set("ns", "k", mustRaw("not-a-number"))
	_, ok := dc.GetInt64("ns", "k")
	if ok {
		t.Error("expected ok=false for non-integer value")
	}
}

func TestGetString_WrongType(t *testing.T) {
	dc, _ := newTmp(t)
	_ = dc.Set("ns", "k", mustRaw(42))
	_, ok := dc.GetString("ns", "k")
	if ok {
		t.Error("expected ok=false for non-string value")
	}
}

func TestGetBool_WrongType(t *testing.T) {
	dc, _ := newTmp(t)
	_ = dc.Set("ns", "k", mustRaw("yes"))
	_, ok := dc.GetBool("ns", "k")
	if ok {
		t.Error("expected ok=false for non-bool value")
	}
}

// ---------------------------------------------------------------------------
// Set validation
// ---------------------------------------------------------------------------

func TestSet_InvalidNamespace(t *testing.T) {
	dc, _ := newTmp(t)
	if err := dc.Set("bad namespace", "key", mustRaw(1)); err == nil {
		t.Error("expected error for namespace with space")
	}
	if err := dc.Set("", "key", mustRaw(1)); err == nil {
		t.Error("expected error for empty namespace")
	}
	if err := dc.Set("bad/slash", "key", mustRaw(1)); err == nil {
		t.Error("expected error for namespace with slash")
	}
}

func TestSet_InvalidKey(t *testing.T) {
	dc, _ := newTmp(t)
	if err := dc.Set("ns", "", mustRaw(1)); err == nil {
		t.Error("expected error for empty key")
	}
	if err := dc.Set("ns", "bad key", mustRaw(1)); err == nil {
		t.Error("expected error for key with space")
	}
}

func TestSet_InvalidJSON(t *testing.T) {
	dc, _ := newTmp(t)
	if err := dc.Set("ns", "k", json.RawMessage("{not json")); err == nil {
		t.Error("expected error for invalid JSON value")
	}
	if err := dc.Set("ns", "k", json.RawMessage("")); err == nil {
		t.Error("expected error for empty JSON value")
	}
}

func TestSet_ValidSpecialCharsInName(t *testing.T) {
	dc, _ := newTmp(t)
	// Dots, hyphens, underscores are all permitted.
	if err := dc.Set("tenant.acme-corp_v2", "blob.max_bytes", mustRaw(1024)); err != nil {
		t.Errorf("unexpected error for valid name: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Set atomicity: failed flush must not corrupt in-memory store
// ---------------------------------------------------------------------------

func TestSet_FailedFlushDoesNotCorruptStore(t *testing.T) {
	dir := t.TempDir()
	// Point the config file at a path whose parent directory does not exist.
	// os.CreateTemp (used by flushLocked) will fail because it cannot create
	// a temp file in a non-existent directory. This is portable and does not
	// rely on chmod — which is ineffective when running as root.
	path := filepath.Join(dir, "nosuchdir", "dynconfig.json")

	// Manually construct a DynConfig whose filePath points at the bad path
	// but whose in-memory store has an initial value seeded without flushing.
	// We do this by creating it with a valid path first, seeding via Set,
	// then swapping the filePath to the bad one.
	goodPath := filepath.Join(dir, "dynconfig.json")
	dc, err := New(goodPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := dc.Set("global", "k", mustRaw("original")); err != nil {
		t.Fatal(err)
	}

	// Redirect to the bad path — next flush will fail.
	dc.filePath = path

	// This Set should fail (can't create temp file in missing directory).
	setErr := dc.Set("global", "k", mustRaw("corrupted"))
	if setErr == nil {
		t.Skip("flush did not fail (possibly running as a user with elevated privileges); skipping atomicity check")
	}

	// The in-memory value must still be "original".
	v, ok := dc.GetString("global", "k")
	if !ok || v != "original" {
		t.Errorf("store corrupted after failed flush: got %q ok=%v", v, ok)
	}
}

// ---------------------------------------------------------------------------
// Delete
// ---------------------------------------------------------------------------

func TestDelete_RemovesKey(t *testing.T) {
	dc, _ := newTmp(t)
	_ = dc.Set("ns", "k", mustRaw(1))
	if err := dc.Delete("ns", "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := dc.Get("ns", "k"); got != nil {
		t.Errorf("expected nil after delete, got %v", got)
	}
}

func TestDelete_EmptyNamespaceRemoved(t *testing.T) {
	dc, _ := newTmp(t)
	_ = dc.Set("ns", "only", mustRaw(1))
	_ = dc.Delete("ns", "only")
	// Namespace itself should be gone.
	if ns := dc.Namespace("ns"); ns != nil {
		t.Errorf("expected namespace to be removed, got %v", ns)
	}
}

func TestDelete_NonexistentKeyIsNoOp(t *testing.T) {
	dc, _ := newTmp(t)
	if err := dc.Delete("ns", "missing"); err != nil {
		t.Errorf("Delete of nonexistent key should return nil, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Namespace / Dump
// ---------------------------------------------------------------------------

func TestNamespace_ReturnsCopy(t *testing.T) {
	dc, _ := newTmp(t)
	_ = dc.Set("global", "a", mustRaw(1))
	_ = dc.Set("global", "b", mustRaw(2))

	ns := dc.Namespace("global")
	if len(ns) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(ns))
	}
	// Mutating the returned map must not affect the store.
	delete(ns, "a")
	if dc.Get("global", "a") == nil {
		t.Error("store modified through Namespace copy")
	}
}

func TestNamespace_Nonexistent(t *testing.T) {
	dc, _ := newTmp(t)
	if ns := dc.Namespace("missing"); ns != nil {
		t.Errorf("expected nil for missing namespace, got %v", ns)
	}
}

func TestDump_DeepCopy(t *testing.T) {
	dc, _ := newTmp(t)
	_ = dc.Set("global", "x", mustRaw(99))

	d := dc.Dump()
	if len(d) != 1 {
		t.Fatalf("expected 1 namespace, got %d", len(d))
	}
	// Mutating the dump must not affect the store.
	delete(d, "global")
	if dc.Get("global", "x") == nil {
		t.Error("store modified through Dump copy")
	}
}

// ---------------------------------------------------------------------------
// Persistence: values survive reload
// ---------------------------------------------------------------------------

func TestPersistence_SurvivesReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dynconfig.json")

	dc1, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = dc1.Set("global", "blob.max_bytes", mustRaw(int64(8192)))

	// Open a second instance pointing at the same file.
	dc2, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := dc2.GetInt64("global", "blob.max_bytes")
	if !ok || v != 8192 {
		t.Errorf("value not persisted: got %d ok=%v", v, ok)
	}
}

// ---------------------------------------------------------------------------
// Reload: malformed file leaves existing store intact
// ---------------------------------------------------------------------------

func TestReload_MalformedFilePreservesStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dynconfig.json")

	dc, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = dc.Set("global", "k", mustRaw("safe"))

	// Overwrite file with garbage directly (bypassing Set).
	if err := os.WriteFile(path, []byte("{invalid json!!!"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := dc.Reload(); err == nil {
		t.Error("expected error from Reload on malformed file")
	}

	// In-memory value must still be intact.
	v, ok := dc.GetString("global", "k")
	if !ok || v != "safe" {
		t.Errorf("store corrupted after failed reload: got %q ok=%v", v, ok)
	}
}

func TestReload_InvalidNameInFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dynconfig.json")

	dc, err := New(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = dc.Set("global", "k", mustRaw("original"))

	// Manually write a file with an invalid namespace name.
	bad := `{"bad namespace":{"k":1}}`
	if err := os.WriteFile(path, []byte(bad), 0644); err != nil {
		t.Fatal(err)
	}
	if err := dc.Reload(); err == nil {
		t.Error("expected error for invalid namespace name in file")
	}
	// Original value must survive.
	if v, ok := dc.GetString("global", "k"); !ok || v != "original" {
		t.Errorf("store corrupted after rejected reload: %q ok=%v", v, ok)
	}
}

// ---------------------------------------------------------------------------
// Concurrent access
// ---------------------------------------------------------------------------

func TestConcurrentSetGet(t *testing.T) {
	dc, _ := newTmp(t)
	const goroutines = 20
	const iters = 50

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			ns := "ns"
			key := "counter"
			for j := 0; j < iters; j++ {
				_ = dc.Set(ns, key, mustRaw(int64(id*iters+j)))
				dc.Get(ns, key)
				dc.Dump()
			}
		}(i)
	}
	wg.Wait()
	// If we get here without a race detector hit, the test passes.
}

// ---------------------------------------------------------------------------
// TenantNamespace helper
// ---------------------------------------------------------------------------

func TestTenantNamespace(t *testing.T) {
	got := TenantNamespace("acme")
	if got != "tenant.acme" {
		t.Errorf("TenantNamespace(acme) = %q, want tenant.acme", got)
	}
}

// ---------------------------------------------------------------------------
// Watcher: Start / Stop lifecycle
// ---------------------------------------------------------------------------

func TestWatcher_StartStop(t *testing.T) {
	dc, path := newTmp(t)
	_ = dc.Set("global", "k", mustRaw("v1"))

	w := NewWatcher(dc, 50*time.Millisecond)
	w.Start()

	// Write a new value directly to the file so the watcher picks it up.
	newContent := `{"global":{"k":"v2"}}`
	if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Give the watcher time to fire at least once.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if v, ok := dc.GetString("global", "k"); ok && v == "v2" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	w.Stop() // Must not deadlock.

	v, ok := dc.GetString("global", "k")
	if !ok || v != "v2" {
		t.Errorf("watcher did not reload: got %q ok=%v", v, ok)
	}
}

func TestWatcher_StopIsIdempotentViaSingleCall(t *testing.T) {
	dc, _ := newTmp(t)
	w := NewWatcher(dc, time.Hour) // Long interval — won't fire.
	w.Start()
	// Stop must return; if it blocks the test will time out.
	done := make(chan struct{})
	go func() {
		w.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() deadlocked")
	}
}
