// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storelayout

import (
	"testing"

	"github.com/ha1tch/xolu/pkg/tenant"
)

func TestTenantSegment(t *testing.T) {
	cases := map[tenant.TenantID]string{
		0:    "t0000", // tenant 0 gets a real segment, not ""
		1:    "t0001",
		255:  "t00FF",
		4096: "t1000",
	}
	for tid, want := range cases {
		if got := TenantSegment(tid); got != want {
			t.Errorf("TenantSegment(%d) = %q, want %q", tid, got, want)
		}
	}
}

func TestParseTenantSegment_RoundTrip(t *testing.T) {
	for _, tid := range []tenant.TenantID{0, 1, 255, 4096, 65535} {
		seg := TenantSegment(tid)
		got, ok := ParseTenantSegment(seg)
		if !ok {
			t.Errorf("ParseTenantSegment(%q) ok=false, want true", seg)
			continue
		}
		if got != tid {
			t.Errorf("round trip %d -> %q -> %d", tid, seg, got)
		}
	}
}

func TestParseTenantSegment_Rejects(t *testing.T) {
	for _, name := range []string{"", "shared", "t001", "t00012", "x0001", "tGGGG", "store", "ts"} {
		if _, ok := ParseTenantSegment(name); ok {
			t.Errorf("ParseTenantSegment(%q) ok=true, want false", name)
		}
	}
}

func TestTenantPaths_Layout(t *testing.T) {
	base := "/data"

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"tenant root", TenantRoot(base, 1), "/data/t0001"},
		{"tenant store dir", TenantStoreDir(base, 1), "/data/t0001/store"},
		{"tenant store file", TenantStorePath(base, 1), "/data/t0001/store/xolu.db"},
		{"tenant ts dir", TenantTSDir(base, 1), "/data/t0001/ts"},
		{"tenant blob dir", TenantBlobDir(base, 1), "/data/t0001/blobs"},
		{"tenant cal dir", TenantCalDir(base, 1), "/data/t0001/cal"},

		// tenant 0 is a tenant like any other — t0000, never the bare root.
		{"tenant 0 store file", TenantStorePath(base, 0), "/data/t0000/store/xolu.db"},
		{"tenant 0 ts dir", TenantTSDir(base, 0), "/data/t0000/ts"},

		// shared mode: its own segment, same internal shape.
		{"shared root", SharedRoot(base), "/data/shared"},
		{"shared store file", SharedStorePath(base), "/data/shared/store/xolu.db"},
		{"shared ts dir", SharedTSDir(base), "/data/shared/ts"},
		{"shared cal dir", SharedCalDir(base), "/data/shared/cal"},
		{"shared blob dir", SharedBlobDir(base), "/data/shared/blobs"},

		// server-level data: base root, sibling of tenant dirs.
		{"dynconfig", DynConfigPath(base), "/data/dynconfig.json"},
		{"schema dir", SchemaDir(base), "/data/schema"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestNoDatabaseAtRoot is the guard for the original bug: no store file should
// ever resolve directly under the base root — every store lives in a named
// subdirectory (tXXXX or shared), never loose at <base>/xolu.db.
func TestNoDatabaseAtRoot(t *testing.T) {
	base := "/data"
	rootDB := base + "/" + StoreFile // "/data/xolu.db" — the forbidden location
	for _, p := range []string{
		TenantStorePath(base, 0),
		TenantStorePath(base, 1),
		SharedStorePath(base),
	} {
		if p == rootDB {
			t.Errorf("store path %q resolves to the base root — must be in a named subdirectory", p)
		}
	}
}
