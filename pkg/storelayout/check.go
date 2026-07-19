// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storelayout

import (
	"fmt"
	"sort"
)

// The model below is an in-memory description of a scanned base directory. It
// is deliberately I/O-free: a caller walks the real filesystem, fills in a
// Model, and hands it to Check. This keeps storelayout a pure authority over
// the layout — it both defines the invariant (the path functions) and verifies
// it (Check) without ever touching disk itself.

// RoleDir describes one role subdirectory (store or ts) found within a
// tenant or shared directory.
type RoleDir struct {
	Name    string   // the role directory name as found on disk
	Entries []string // immediate child entry names (files and dirs)
}

// TenantDir describes one tenant (or shared) directory found under the base.
type TenantDir struct {
	Segment string    // directory name as found on disk, e.g. "t0001", "shared", or something invalid
	Roles   []RoleDir // role subdirectories found within
	Extra   []string  // non-directory or unexpected immediate entries directly under the segment
}

// Model is the complete in-memory description of a scanned base directory.
type Model struct {
	BaseDir string

	// Tenants holds every immediate subdirectory of BaseDir that looks like it
	// is meant to be a tenant or shared directory (i.e. is itself a directory),
	// whether or not its name is valid. Check decides validity.
	Tenants []TenantDir

	// RootFiles holds immediate non-directory entries directly under BaseDir
	// (e.g. "dynconfig.json", or an out-of-place "xolu.db").
	RootFiles []string

	// RootDirs holds immediate subdirectory names directly under BaseDir that
	// are NOT tenant/shared directories captured in Tenants — e.g. "schema",
	// or an out-of-place "sql"/"ts". (schema is valid; others are violations.)
	RootDirs []string
}

// Severity is the classification of an Issue. The current policy is strict and
// binary: every problem is a Violation. The type exists so that callers can
// branch on severity without assuming there is only one kind.
type Severity int

const (
	// Violation means the layout does not conform to the invariant.
	Violation Severity = iota
)

func (s Severity) String() string {
	switch s {
	case Violation:
		return "violation"
	default:
		return "unknown"
	}
}

// Issue is a single conformance problem found by Check.
type Issue struct {
	Severity Severity
	Path     string // the offending path, relative to BaseDir where meaningful
	Message  string
}

func (i Issue) String() string {
	return fmt.Sprintf("[%s] %s: %s", i.Severity, i.Path, i.Message)
}

// validRoleName reports whether name is one of the permitted role
// directories within a tenant or shared directory.
func validRoleName(name string) bool {
	switch name {
	case roleStore, roleTS, roleBlobs, roleCal:
		return true
	default:
		return false
	}
}

// Check validates a scanned Model against the invariant directory structure and
// returns every conformance problem found. The policy is strict: anything that
// is not exactly what the invariant permits is a Violation. An empty result
// means the structure conforms.
//
// Check is "pass 1": it validates the directory structure that storelayout owns,
// which is universal across all storage backends — the tree from the base down
// to and including each role directory. It deliberately renders NO verdict on
// the contents of a role directory. Everything beneath store/ and ts/ —
// files, nested subdirectories, and the backend's anchor file — is owned by the
// storage backend that occupies that role, not by storelayout. File-level
// conformance is "pass 2", delegated to the backend (which, holding its own
// configuration, is the only component that can say what files it expects).
//
// The structural rules enforced:
//   - Immediate children of the base may only be: tenant directories ("tXXXX"),
//     the shared directory ("shared"), the schema directory, and the dynconfig
//     file. Anything else (a stray file — including a misplaced store file — or a
//     stray directory such as a leftover "sql"/"ts") is a violation.
//   - Within a tenant or shared directory, only the role directories store, ts,
//     blobs may appear, and only as directories. Any other entry is a
//     violation.
//   - The contents of a role directory are out of scope here (see pass 2).
func Check(m Model) []Issue {
	var issues []Issue

	add := func(path, msg string) {
		issues = append(issues, Issue{Severity: Violation, Path: path, Message: msg})
	}

	// Root-level files: only the dynconfig file is permitted. Any other file at
	// the base root is a violation — including a misplaced store file, which is
	// caught here generically without storelayout needing to know any backend's
	// filenames.
	for _, f := range m.RootFiles {
		if f == DynConfigFileName {
			continue
		}
		add(f, "unexpected file at the base root")
	}

	// Root-level non-tenant directories: only the schema directory is permitted.
	// Blobs are now a per-tenant role (tXXXX/blobs), not a server-level directory.
	for _, d := range m.RootDirs {
		if d == SchemaDirName {
			continue
		}
		add(d, "unexpected directory at the base root")
	}

	// Tenant / shared directories.
	for _, td := range m.Tenants {
		isShared := td.Segment == sharedSegment
		_, isTenant := ParseTenantSegment(td.Segment)
		if !isShared && !isTenant {
			add(td.Segment, "directory name is neither a valid tenant segment (tXXXX) nor the shared directory")
			// Still inspect roles below so the report is complete.
		}

		for _, extra := range td.Extra {
			add(td.Segment+"/"+extra, "unexpected entry directly under a tenant/shared directory; only role directories (store, ts) are permitted")
		}

		for _, role := range td.Roles {
			if !validRoleName(role.Name) {
				add(td.Segment+"/"+role.Name, "unexpected role directory; only store and ts are permitted")
			}
			// The contents of a valid role directory — files and any nested
			// subdirectories, including the backend's anchor file — are owned by
			// the storage backend, not by storelayout. Pass 1 validates the
			// directory structure down to and including the role directory and
			// renders no verdict on what lies within. File-level conformance is
			// pass 2, delegated to the backend that owns the role.
		}
	}

	sort.Slice(issues, func(a, b int) bool {
		if issues[a].Path != issues[b].Path {
			return issues[a].Path < issues[b].Path
		}
		return issues[a].Message < issues[b].Message
	})
	return issues
}
