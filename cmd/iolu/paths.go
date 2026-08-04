// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Layout-aware path resolution for iolu.
//
// iolu speaks the same normalized on-disk layout the running server writes,
// derived entirely through pkg/storelayout. A data root (--base-dir) contains:
//
//	<base>/t<XXXX>/store/xolu.db   per-file mode: one SQLite file per tenant
//	<base>/t<XXXX>/ts/            per-tenant timeseries (Pebble)
//	<base>/t<XXXX>/blobs/         per-tenant blobs (content-addressed)
//	<base>/shared/store/xolu.db    shared mode: all tenants in one SQLite file
//	<base>/shared/ts/, /blobs/    shared-mode siblings (unused by the blob/ts
//	                              planes, which are always per-tenant)
//	<base>/schema/, dynconfig.json
//
// The store organisation (per-file vs shared) is a SQLite concern only; the
// timeseries and blob planes are always per-tenant directories. iolu detects
// the store mode from disk, with an explicit override available.

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ha1tch/xolu/pkg/storage"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// storeMode describes how the SQLite primary store is organised.
type storeMode int

const (
	modeUnknown storeMode = iota
	modePerFile           // <base>/tXXXX/store/xolu.db, one file per tenant
	modeShared            // <base>/shared/store/xolu.db, one file for all tenants
)

func (m storeMode) String() string {
	switch m {
	case modePerFile:
		return "per-file"
	case modeShared:
		return "shared"
	default:
		return "unknown"
	}
}

// detectStoreMode inspects a base directory and reports whether the primary
// store is shared or per-file. Detection is structural:
//   - a <base>/shared/store/xolu.db file implies shared mode;
//   - any <base>/tXXXX/store/xolu.db file implies per-file mode.
//
// It returns modeUnknown when neither is present (e.g. an empty or not-yet
// initialised base dir), so callers can fall back to an explicit choice.
func detectStoreMode(base string) storeMode {
	if fi, err := os.Stat(sl.SharedStorePath(base)); err == nil && !fi.IsDir() {
		return modeShared
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return modeUnknown
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id, ok := sl.ParseTenantSegment(e.Name())
		if !ok {
			continue
		}
		if fi, err := os.Stat(sl.TenantStorePath(base, id)); err == nil && !fi.IsDir() {
			return modePerFile
		}
	}
	return modeUnknown
}

// resolveStoreMode combines an explicit override flag with on-disk detection.
// The override (if non-empty) wins; otherwise detection is used; otherwise the
// supplied default applies. It returns an error only for an invalid override.
func resolveStoreMode(base, override string, dflt storeMode) (storeMode, error) {
	switch override {
	case "per-file":
		return modePerFile, nil
	case "shared":
		return modeShared, nil
	case "":
		if m := detectStoreMode(base); m != modeUnknown {
			return m, nil
		}
		return dflt, nil
	default:
		return modeUnknown, fmt.Errorf("invalid store mode %q (want per-file or shared)", override)
	}
}

// storePathFor returns the SQLite database file path for a tenant under the
// given mode. In shared mode every tenant maps to the single shared file.
func storePathFor(base string, tid tenant.TenantID, mode storeMode) string {
	if mode == modeShared {
		return sl.SharedStorePath(base)
	}
	return sl.TenantStorePath(base, tid)
}

// openTenantStore opens (or creates) the primary store for a tenant via the
// same pkg/storage path the running server uses, with the correct
// PerFileTenants setting so DDL and table naming match xolu exactly. It creates
// the parent directory first (the SQLite store does not create parents).
func openTenantStore(base string, tid tenant.TenantID, mode storeMode, graph bool) (*storage.SQLiteStore, error) {
	dbPath := storePathFor(base, tid, mode)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("create store directory: %w", err)
	}
	return storage.NewSQLiteStore(dbPath, storage.SQLiteConfig{
		DBPath:            dbPath,
		EnableWAL:         true,
		EnableForeignKeys: true,
		CacheSize:         2000,
		BusyTimeout:       5000,
		GraphEnabled:      graph,
		TenantID:          tid,
		PerFileTenants:    mode == modePerFile,
	})
}
