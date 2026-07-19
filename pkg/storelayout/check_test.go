// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storelayout

import (
	"strings"
	"testing"
)

// conformingModel is a layout that exactly matches the invariant.
func conformingModel() Model {
	return Model{
		BaseDir: "/data",
		Tenants: []TenantDir{
			{
				Segment: "t0000",
				Roles: []RoleDir{
					{Name: "store", Entries: []string{"xolu.db", "xolu.db-wal", "xolu.db-shm"}},
					{Name: "ts", Entries: []string{"db"}},
					{Name: "blobs", Entries: []string{"ab", ".keys"}},
				},
			},
			{
				Segment: "t0001",
				Roles: []RoleDir{
					{Name: "store", Entries: []string{"xolu.db"}},
				},
			},
			{
				Segment: "shared",
				Roles: []RoleDir{
					{Name: "store", Entries: []string{"xolu.db"}},
				},
			},
		},
		RootFiles: []string{"dynconfig.json"},
		RootDirs:  []string{"schema"},
	}
}

func TestCheck_Conforming(t *testing.T) {
	if issues := Check(conformingModel()); len(issues) != 0 {
		t.Fatalf("conforming model reported %d issues: %v", len(issues), issues)
	}
}

func TestCheck_DatabaseAtRoot(t *testing.T) {
	m := conformingModel()
	m.RootFiles = append(m.RootFiles, "xolu.db") // the original bug: a store file loose at the root
	issues := Check(m)
	// Caught generically as a stray root file — storelayout does not name the
	// backend's files; any non-dynconfig file at the root is a violation.
	if !hasIssueContaining(issues, "xolu.db", "unexpected file at the base root") {
		t.Fatalf("expected a stray-root-file violation for the misplaced store file, got: %v", issues)
	}
}

func TestCheck_StrayRootFileAndDir(t *testing.T) {
	m := conformingModel()
	m.RootFiles = append(m.RootFiles, "notes.txt")
	m.RootDirs = append(m.RootDirs, "sql") // old backend-first dir is now a violation
	issues := Check(m)
	if !hasIssueContaining(issues, "notes.txt", "unexpected file") {
		t.Errorf("expected stray-file violation, got: %v", issues)
	}
	if !hasIssueContaining(issues, "sql", "unexpected directory") {
		t.Errorf("expected stray-dir violation for sql/, got: %v", issues)
	}
}

func TestCheck_BadTenantName(t *testing.T) {
	m := conformingModel()
	m.Tenants = append(m.Tenants, TenantDir{
		Segment: "tenant1", // not tXXXX, not shared
		Roles:   []RoleDir{{Name: "store", Entries: []string{"xolu.db"}}},
	})
	if !hasIssueContaining(Check(m), "tenant1", "neither a valid tenant segment") {
		t.Fatalf("expected bad-tenant-name violation, got: %v", Check(m))
	}
}

func TestCheck_BadRole(t *testing.T) {
	m := conformingModel()
	m.Tenants = append(m.Tenants, TenantDir{
		Segment: "t0002",
		Roles: []RoleDir{
			{Name: "graph", Entries: []string{"x"}}, // graph is no longer a role
		},
		Extra: []string{"loose.txt"},
	})
	issues := Check(m)
	if !hasIssueContaining(issues, "t0002/graph", "unexpected role") {
		t.Errorf("expected bad-role violation, got: %v", issues)
	}
	if !hasIssueContaining(issues, "t0002/loose.txt", "unexpected entry") {
		t.Errorf("expected loose-entry violation, got: %v", issues)
	}
}

// TestCheck_RoleContentsNotJudged is the boundary guard: pass 1 validates the
// directory structure down to the role directory and renders no verdict on what
// lies inside it. A store/ directory that is empty, that lacks the backend's
// anchor file, or that contains arbitrary extra files is NOT a structural
// violation — that is the backend's domain (pass 2).
func TestCheck_RoleContentsNotJudged(t *testing.T) {
	m := conformingModel()
	// An empty store dir, a store dir full of unexpected files, and a backend's
	// own nested subdirectory must all pass the structural check.
	m.Tenants = append(m.Tenants,
		TenantDir{Segment: "t0002", Roles: []RoleDir{{Name: "store", Entries: nil}}},
		TenantDir{Segment: "t0003", Roles: []RoleDir{{Name: "store", Entries: []string{"anything.db", "random.txt", "wal"}}}},
		TenantDir{Segment: "t0004", Roles: []RoleDir{{Name: "ts", Entries: []string{"db", "CURRENT", "MANIFEST-000001"}}}},
	)
	for _, iss := range Check(m) {
		// None of the new tenants' role-dir contents should be flagged.
		for _, seg := range []string{"t0002/store/", "t0003/store/", "t0004/ts/"} {
			if strings.HasPrefix(iss.Path, seg) {
				t.Errorf("pass 1 judged role-dir contents (%v) — that is the backend's domain", iss)
			}
		}
	}
}

func hasIssueContaining(issues []Issue, pathPart, msgPart string) bool {
	for _, i := range issues {
		if strings.Contains(i.Path, pathPart) && strings.Contains(i.Message, msgPart) {
			return true
		}
	}
	return false
}
