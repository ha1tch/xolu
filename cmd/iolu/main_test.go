// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Tests for the layout-aware iolu admin CLI. These cover the foundation layer
// (store-mode detection and path resolution) directly, and the command bodies
// end-to-end in-process — asserting that the on-disk layout iolu produces and
// reads matches the normalized layout the running server uses (pkg/storelayout).

package main

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// ---------------------------------------------------------------------------
// Foundation: store-mode detection and path resolution
// ---------------------------------------------------------------------------

func TestResolveStoreMode_Override(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		override string
		want     storeMode
		wantErr  bool
	}{
		{"per-file", modePerFile, false},
		{"shared", modeShared, false},
		{"bogus", modeUnknown, true},
	}
	for _, c := range cases {
		got, err := resolveStoreMode(base, c.override, modePerFile)
		if c.wantErr {
			if err == nil {
				t.Errorf("override %q: expected error, got none", c.override)
			}
			continue
		}
		if err != nil {
			t.Errorf("override %q: unexpected error: %v", c.override, err)
		}
		if got != c.want {
			t.Errorf("override %q: got %v, want %v", c.override, got, c.want)
		}
	}
}

func TestResolveStoreMode_DefaultWhenEmpty(t *testing.T) {
	base := t.TempDir() // empty: detection returns unknown
	got, err := resolveStoreMode(base, "", modeShared)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != modeShared {
		t.Errorf("empty base with no override: got %v, want default modeShared", got)
	}
}

func TestDetectStoreMode_PerFileAndShared(t *testing.T) {
	// Per-file: a tXXXX/store/xolu.db file present.
	perFile := t.TempDir()
	mustInit(t, perFile, modePerFile, nil)
	if m := detectStoreMode(perFile); m != modePerFile {
		t.Errorf("per-file base detected as %v, want per-file", m)
	}

	// Shared: a shared/store/xolu.db file present.
	shared := t.TempDir()
	mustInit(t, shared, modeShared, nil)
	if m := detectStoreMode(shared); m != modeShared {
		t.Errorf("shared base detected as %v, want shared", m)
	}

	// Empty base: unknown.
	if m := detectStoreMode(t.TempDir()); m != modeUnknown {
		t.Errorf("empty base detected as %v, want unknown", m)
	}
}

func TestStorePathFor(t *testing.T) {
	base := "/data"
	if got := storePathFor(base, 1, modePerFile); got != sl.TenantStorePath(base, 1) {
		t.Errorf("per-file tenant 1: got %q", got)
	}
	if got := storePathFor(base, 0, modePerFile); got != sl.TenantStorePath(base, 0) {
		t.Errorf("per-file tenant 0: got %q", got)
	}
	// Shared: every tenant maps to the single shared file.
	if got := storePathFor(base, 5, modeShared); got != sl.SharedStorePath(base) {
		t.Errorf("shared tenant 5: got %q, want shared store", got)
	}
}

// ---------------------------------------------------------------------------
// db init: produces the normalized layout in both modes
// ---------------------------------------------------------------------------

func TestDBInit_PerFileLayout(t *testing.T) {
	base := t.TempDir()
	cmdDBInit([]string{"--base-dir", base, "--mode", "per-file",
		"--tenant", "acme", "--tenant", "beta:5", "--graph"})

	// Each tenant (0, 1=acme, 5=beta) gets its own store file plus ts/blobs.
	for _, id := range []uint16{0, 1, 5} {
		mustExistFile(t, sl.TenantStorePath(base, id))
		mustExistDir(t, sl.TenantTSDir(base, id))
		mustExistDir(t, sl.TenantBlobDir(base, id))
	}
	// No shared store in per-file mode.
	if _, err := os.Stat(sl.SharedStorePath(base)); err == nil {
		t.Error("per-file init must not create a shared store")
	}
	// Each tenant's node table lives in its own store file.
	assertTableIn(t, sl.TenantStorePath(base, 1), tenant.NodesTableName(1), true)
	assertTableIn(t, sl.TenantStorePath(base, 5), tenant.NodesTableName(5), true)
	// And NOT in tenant 0's file.
	assertTableIn(t, sl.TenantStorePath(base, 0), tenant.NodesTableName(1), false)
}

func TestDBInit_SharedLayout(t *testing.T) {
	base := t.TempDir()
	cmdDBInit([]string{"--base-dir", base, "--mode", "shared",
		"--tenant", "acme", "--tenant", "gamma:7", "--graph"})

	// One shared store file; per-tenant ts/blobs dirs still present.
	mustExistFile(t, sl.SharedStorePath(base))
	for _, id := range []uint16{0, 1, 7} {
		mustExistDir(t, sl.TenantTSDir(base, id))
		mustExistDir(t, sl.TenantBlobDir(base, id))
		// No per-tenant store files in shared mode.
		if _, err := os.Stat(sl.TenantStorePath(base, id)); err == nil {
			t.Errorf("shared init must not create per-tenant store for %d", id)
		}
	}
	// All tenants' node tables coexist in the one shared store.
	for _, id := range []uint16{0, 1, 7} {
		assertTableIn(t, sl.SharedStorePath(base), tenant.NodesTableName(id), true)
	}
}

func TestDBInit_RefusesExistingRoot(t *testing.T) {
	base := t.TempDir()
	cmdDBInit([]string{"--base-dir", base, "--mode", "per-file"})
	// A second init against the same root must be refused. Run it as a
	// subprocess because the refusal calls os.Exit.
	if runIoluExpectingExit(t, base, "db", "init", "--base-dir", base, "--mode", "per-file") == 0 {
		t.Error("re-init of an existing root should exit non-zero")
	}
}

// ---------------------------------------------------------------------------
// Read commands: the per-file regressions (hang + wrong-store) must stay fixed
// ---------------------------------------------------------------------------

// tenantNodeCount must read a tenant's own store in per-file mode (regression
// for "no such table: tXXXX_nodes" against tenant 0's store), and must not
// require the tenant-0 connection to hold the table.
func TestTenantNodeCount_PerFileReadsOwnStore(t *testing.T) {
	base := t.TempDir()
	cmdDBInit([]string{"--base-dir", base, "--mode", "per-file", "--tenant", "acme"})

	store0, err := openTenantStore(base, 0, modePerFile, false)
	if err != nil {
		t.Fatalf("open tenant 0: %v", err)
	}
	defer func() { _ = store0.Close() }()

	// Count for tenant 1 must succeed (0 nodes), reading t0001's own store —
	// not error out because t0001_nodes is absent from tenant 0's store.
	if n := tenantNodeCount(base, 1, modePerFile, store0.DB()); n != 0 {
		t.Errorf("tenant 1 node count = %d, want 0", n)
	}
	// Tenant 0 count via the shared connection.
	if n := tenantNodeCount(base, 0, modePerFile, store0.DB()); n != 0 {
		t.Errorf("tenant 0 node count = %d, want 0", n)
	}
}

func TestTenantNodeCount_SharedReadsSharedStore(t *testing.T) {
	base := t.TempDir()
	cmdDBInit([]string{"--base-dir", base, "--mode", "shared", "--tenant", "acme:3"})

	store0, err := openTenantStore(base, 0, modeShared, false)
	if err != nil {
		t.Fatalf("open shared store: %v", err)
	}
	defer func() { _ = store0.Close() }()
	if n := tenantNodeCount(base, 3, modeShared, store0.DB()); n != 0 {
		t.Errorf("tenant 3 node count = %d, want 0", n)
	}
}

// tenant list must complete (not hang) in per-file mode with multiple tenants.
// The previous structure deadlocked the SQLite connection by issuing per-tenant
// count queries while the tenants cursor was open. Run as a subprocess with a
// hard deadline so a regression manifests as a timeout failure, not a hung test.
func TestTenantList_PerFileCompletes(t *testing.T) {
	base := t.TempDir()
	cmdDBInit([]string{"--base-dir", base, "--mode", "per-file",
		"--tenant", "acme", "--tenant", "beta:5"})

	out, code := runIoluWithDeadline(t, 30*time.Second, "tenant", "list", "--base-dir", base)
	if code != 0 {
		t.Fatalf("tenant list exited %d (possible hang regression). output:\n%s", code, out)
	}
	if !contains(out, "acme") || !contains(out, "beta") {
		t.Errorf("tenant list output missing tenants:\n%s", out)
	}
}

// ---------------------------------------------------------------------------
// tenant delete: mode-aware removal of on-disk state
// ---------------------------------------------------------------------------

func TestTenantDelete_PerFileRemovesTenantRoot(t *testing.T) {
	base := t.TempDir()
	cmdDBInit([]string{"--base-dir", base, "--mode", "per-file",
		"--tenant", "acme", "--tenant", "beta:5"})

	cmdTenantDelete([]string{"--base-dir", base, "--mode", "per-file", "--name", "beta"})

	// beta's entire tenant directory must be gone.
	if _, err := os.Stat(sl.TenantRoot(base, 5)); !os.IsNotExist(err) {
		t.Error("per-file delete should remove the whole tenant directory")
	}
	// acme (tenant 1) must remain.
	mustExistFile(t, sl.TenantStorePath(base, 1))

	// Registry must no longer list beta.
	store0, err := openTenantStore(base, 0, modePerFile, false)
	if err != nil {
		t.Fatalf("open tenant 0: %v", err)
	}
	defer func() { _ = store0.Close() }()
	var n int
	_ = store0.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM tenants WHERE name = 'beta'`).Scan(&n)
	if n != 0 {
		t.Errorf("beta still in registry after delete (count=%d)", n)
	}
}

func TestTenantDelete_SharedRemovesDirsAndTables(t *testing.T) {
	base := t.TempDir()
	cmdDBInit([]string{"--base-dir", base, "--mode", "shared",
		"--tenant", "acme", "--tenant", "gamma:7"})

	cmdTenantDelete([]string{"--base-dir", base, "--mode", "shared", "--name", "gamma"})

	// In shared mode the per-tenant dirs are removed...
	if _, err := os.Stat(sl.TenantTSDir(base, 7)); !os.IsNotExist(err) {
		t.Error("shared delete should remove the tenant ts dir")
	}
	if _, err := os.Stat(sl.TenantBlobDir(base, 7)); !os.IsNotExist(err) {
		t.Error("shared delete should remove the tenant blobs dir")
	}
	// ...and the tenant's tables dropped from the shared store.
	assertTableIn(t, sl.SharedStorePath(base), tenant.NodesTableName(7), false)
	// acme remains.
	assertTableIn(t, sl.SharedStorePath(base), tenant.NodesTableName(1), true)
}

// ---------------------------------------------------------------------------
// provision-ts / provisionTenantDirs: creates both ts and blobs per tenant
// ---------------------------------------------------------------------------

func TestProvisionTenantDirs_CreatesBoth(t *testing.T) {
	base := t.TempDir()
	if err := provisionTenantDirs(base, 4); err != nil {
		t.Fatalf("provisionTenantDirs: %v", err)
	}
	mustExistDir(t, sl.TenantTSDir(base, 4))
	mustExistDir(t, sl.TenantBlobDir(base, 4))
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// mustInit initialises a base dir in the given mode for detection tests.
func mustInit(t *testing.T, base string, mode storeMode, tenants []string) {
	t.Helper()
	args := []string{"--base-dir", base}
	switch mode {
	case modeShared:
		args = append(args, "--mode", "shared")
	default:
		args = append(args, "--mode", "per-file")
	}
	for _, tn := range tenants {
		args = append(args, "--tenant", tn)
	}
	cmdDBInit(args)
}

func mustExistFile(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Errorf("expected file %s: %v", path, err)
		return
	}
	if fi.IsDir() {
		t.Errorf("expected file but found directory: %s", path)
	}
}

func mustExistDir(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Errorf("expected directory %s: %v", path, err)
		return
	}
	if !fi.IsDir() {
		t.Errorf("expected directory but found file: %s", path)
	}
}

// assertTableIn opens a SQLite file directly and asserts table presence/absence.
func assertTableIn(t *testing.T, dbPath, table string, want bool) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open %s: %v", dbPath, err)
	}
	defer func() { _ = db.Close() }()
	var name string
	err = db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&name)
	got := err == nil
	if got != want {
		t.Errorf("table %s in %s: present=%v, want %v", table, dbPath, got, want)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
