// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"net/http"
	"sort"
	"testing"

	"github.com/ha1tch/xolu/pkg/server"
	"github.com/ha1tch/xolu/pkg/version"
)

// GET /version answers the version and the capabilities list, and the list is
// the one the server package defines.
func TestVersionEndpointAdvertisesCapabilities(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()

	status, body := env.doJSON("GET", "/version", nil)
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if body["version"] != version.Version {
		t.Errorf("version = %v, want %s", body["version"], version.Version)
	}
	raw, ok := body["capabilities"].([]interface{})
	if !ok {
		t.Fatalf("no capabilities list in %v", body)
	}
	got := make([]string, 0, len(raw))
	for _, c := range raw {
		got = append(got, c.(string))
	}
	want := append([]string(nil), server.Capabilities...)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("capabilities = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("capabilities = %v, want %v", got, want)
		}
	}
	for _, must := range []string{"oql.adapted-select", "oql.tenant-entities", "commit.cas-missing-conflict", "commit.oversize-413", "commit.delete", "commit.templates", "schema.unique-index"} {
		found := false
		for _, g := range got {
			found = found || g == must
		}
		if !found {
			t.Errorf("capability %q is missing", must)
		}
	}
}
