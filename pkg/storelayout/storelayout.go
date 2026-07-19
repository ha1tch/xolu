// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package storelayout is the single authority for xolu's on-disk directory
// layout. Every database file, timeseries directory, and blob directory is
// derived from one configurable input — the base directory — by fixed
// invariants, in the same spirit as the per-tenant table-naming invariants one
// level up. No other package composes data paths by hand.
//
// The layout is tenant-first: everything belonging to one tenant lives under
// that tenant's own directory, subdivided by role.
//
//	<base>/
//	  t0000/                  tenant 0 (unscoped / single-tenant mode)
//	    store/xolu.db          primary entity store (SQLite today)
//	    ts/                   timeseries store (Pebble today)
//	    blobs/                blob store
//	  t0001/                  first registered tenant
//	    store/xolu.db
//	    ts/
//	    blobs/
//	  ...
//	  shared/                 shared-tenancy mode (all tenants in one store file)
//	    store/xolu.db
//	    ts/
//	    blobs/
//	  dynconfig.json          server-level config (not tenant data)
//	  schema/                 server-level entity schemas
//
// Design rules:
//   - The base directory is the ONLY configurable path. Everything below it is
//     derived; there are no per-file or per-role path overrides.
//   - Role directory names ("store", "ts", "blobs") describe the role, not the
//     engine, so a future backend change does not change the layout contract.
//   - Tenant 0 is a tenant like any other: it gets the directory "t0000". It is
//     never special-cased to the base root.
//   - Shared-tenancy mode is not a tenant; it gets its own segment, "shared",
//     with the same internal shape as a tenant directory.
package storelayout

import (
	"fmt"
	"path/filepath"
	"strconv"
)

// Role names — the subdirectories within a tenant (or shared) directory.
// These describe the role of the data, not the storage engine.
const (
	roleStore = "store" // primary entity store (SQLite today)
	roleTS    = "ts"    // timeseries store (Pebble today)
	roleBlobs = "blobs" // blob store (content-addressed, tenant-first layout)
	roleCal   = "cal"   // scheduling occupancy index (Pebble today; SQLite record lives in store)
)

// Fixed filenames within a role directory.
const (
	// StoreFile is the SQLite database filename within a store/ directory.
	// It is the same in every tenant and in shared mode; tenants are
	// distinguished by their directory, not by the filename.
	StoreFile = "xolu.db"

	// DynConfigFileName is the server-level dynamic-config file, at the base root.
	DynConfigFileName = "dynconfig.json"

	// SchemaDirName is the server-level schema directory, at the base root.
	SchemaDirName = "schema"

	// BlobsDirName is the role-directory name for blobs. Blobs are a per-tenant
	// role (see TenantBlobDir: <base>/tXXXX/blobs); this constant names that
	// role directory. It is the same string as roleBlobs and retained for any
	// external reference to the blob directory name.
	BlobsDirName = "blobs"

	// sharedSegment is the directory segment for shared-tenancy mode.
	sharedSegment = "shared"
)

// TenantSegment returns the directory-name segment for a tenant.
//
// Unlike the older tenant.StorageDirSegment, tenant 0 is NOT special-cased to an
// empty segment: it returns "t0000", so tenant 0 gets its own directory like any
// other tenant. The format is "tXXXX" with uppercase hex, four digits, matching
// the historical format so that directory scans that parse the segment back
// (see ParseTenantSegment) continue to work.
func TenantSegment(tenantID uint16) string {
	return fmt.Sprintf("t%04X", tenantID)
}

// ParseTenantSegment parses a directory name of the form "tXXXX" (uppercase hex,
// four digits) back into a tenant id. It returns ok=false for any name that is
// not a well-formed tenant segment. "shared" is not a tenant segment and returns
// ok=false. Note that "t0000" parses to tenant 0 with ok=true — callers that
// scan tenant directories must decide for themselves whether to include tenant 0.
func ParseTenantSegment(name string) (tenantID uint16, ok bool) {
	if len(name) != 5 || name[0] != 't' {
		return 0, false
	}
	parsed, err := strconv.ParseUint(name[1:], 16, 16)
	if err != nil {
		return 0, false
	}
	return uint16(parsed), true
}

// TenantRoot returns the directory holding all of one tenant's data:
// <base>/tXXXX. Every role directory (store, ts, blobs) lives beneath it.
func TenantRoot(base string, tenantID uint16) string {
	return filepath.Join(base, TenantSegment(tenantID))
}

// SharedRoot returns the directory holding the shared-tenancy store:
// <base>/shared. It has the same internal shape as a tenant root.
func SharedRoot(base string) string {
	return filepath.Join(base, sharedSegment)
}

// TenantStoreDir returns the per-tenant primary-store directory:
// <base>/tXXXX/store.
func TenantStoreDir(base string, tenantID uint16) string {
	return filepath.Join(TenantRoot(base, tenantID), roleStore)
}

// TenantStorePath returns the per-tenant SQLite database file:
// <base>/tXXXX/store/xolu.db.
func TenantStorePath(base string, tenantID uint16) string {
	return filepath.Join(TenantStoreDir(base, tenantID), StoreFile)
}

// TenantTSDir returns the per-tenant timeseries directory:
// <base>/tXXXX/ts.
func TenantTSDir(base string, tenantID uint16) string {
	return filepath.Join(TenantRoot(base, tenantID), roleTS)
}

// TenantBlobDir returns the per-tenant blob directory: <base>/tXXXX/blobs.
// This is the tenant-first blob layout, analogous to TenantTSDir: one blob
// directory per tenant, keyed by ID, tenant 0 included.
func TenantBlobDir(base string, tenantID uint16) string {
	return filepath.Join(TenantRoot(base, tenantID), roleBlobs)
}

// TenantCalDir returns the per-tenant scheduling (cal) directory:
// <base>/tXXXX/cal. This is the cal occupancy index (the derived Pebble bitmap,
// H3); the authoritative booking record (H1) lives in the tenant's primary
// store (TenantStorePath). Analogous to TenantTSDir.
func TenantCalDir(base string, tenantID uint16) string {
	return filepath.Join(TenantRoot(base, tenantID), roleCal)
}

// SharedStoreDir returns the shared-mode primary-store directory:
// <base>/shared/store.
func SharedStoreDir(base string) string {
	return filepath.Join(SharedRoot(base), roleStore)
}

// SharedStorePath returns the shared-mode SQLite database file:
// <base>/shared/store/xolu.db.
func SharedStorePath(base string) string {
	return filepath.Join(SharedStoreDir(base), StoreFile)
}

// SharedTSDir returns the shared-mode timeseries directory:
// <base>/shared/ts.
func SharedTSDir(base string) string {
	return filepath.Join(SharedRoot(base), roleTS)
}

// SharedCalDir returns the shared-mode scheduling (cal) directory:
// <base>/shared/cal.
func SharedCalDir(base string) string {
	return filepath.Join(SharedRoot(base), roleCal)
}

// SharedBlobDir returns the shared-segment blob directory: <base>/shared/blobs.
// Defined for symmetry with SharedStoreDir/SharedTSDir; the blob plane, like
// the timeseries plane, uses per-tenant directories (TenantBlobDir) and does
// not consume this.
func SharedBlobDir(base string) string {
	return filepath.Join(SharedRoot(base), roleBlobs)
}

// BlobsDir returns the legacy server-level blob directory: <base>/blobs.
//
// Deprecated: blobs are now a per-tenant role. Use TenantBlobDir(base, id).
// Retained only until the server construction path is migrated off it.
func BlobsDir(base string) string {
	return filepath.Join(base, BlobsDirName)
}

// DynConfigPath returns the server-level dynamic-config file:
// <base>/dynconfig.json. This is server data, not tenant data, so it lives at
// the base root, a sibling of the tenant directories.
func DynConfigPath(base string) string {
	return filepath.Join(base, DynConfigFileName)
}

// SchemaDir returns the server-level schema directory: <base>/schema.
// This is server data, not tenant data, so it lives at the base root.
func SchemaDir(base string) string {
	return filepath.Join(base, SchemaDirName)
}
