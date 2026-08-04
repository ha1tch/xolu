// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package tenantexport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cockroachdb/pebble"
)

func seedPebbleDir(t *testing.T, dir string, kv map[string][]byte) {
	t.Helper()
	db, err := pebble.Open(dir, &pebble.Options{})
	if err != nil {
		t.Fatalf("open pebble for seeding: %v", err)
	}
	for k, v := range kv {
		if err := db.Set([]byte(k), v, pebble.Sync); err != nil {
			t.Fatalf("seed set %q: %v", k, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close after seeding: %v", err)
	}
}

func readPebbleJSONArray(t *testing.T, path string) []PebbleKV {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []PebbleKV
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal %s: %v (content: %s)", path, err, data)
	}
	return out
}

func TestExportPebbleStore_HappyPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bal_rollup")
	seedPebbleDir(t, dir, map[string][]byte{
		"account:1": []byte("balance-data-1"),
		"account:2": []byte("balance-data-2"),
		"account:3": {0x00, 0x01, 0xFF, 0xFE}, // genuinely binary, not text-decodable
	})

	outDir := t.TempDir()
	n, err := ExportPebbleStore(context.Background(), dir, "bal_rollup", outDir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if n != 3 {
		t.Fatalf("key count: got %d, want 3", n)
	}

	kvs := readPebbleJSONArray(t, filepath.Join(outDir, "bal_rollup.json"))
	if len(kvs) != 3 {
		t.Fatalf("got %d entries, want 3", len(kvs))
	}

	found := map[string]string{}
	for _, kv := range kvs {
		keyBytes, err := base64.StdEncoding.DecodeString(kv.Key)
		if err != nil {
			t.Fatalf("key %q is not valid base64: %v", kv.Key, err)
		}
		valBytes, err := base64.StdEncoding.DecodeString(kv.Value)
		if err != nil {
			t.Fatalf("value %q is not valid base64: %v", kv.Value, err)
		}
		found[string(keyBytes)] = string(valBytes)
	}
	if found["account:1"] != "balance-data-1" {
		t.Errorf("account:1: got %q", found["account:1"])
	}
	if found["account:2"] != "balance-data-2" {
		t.Errorf("account:2: got %q", found["account:2"])
	}
	// The genuinely binary value must round-trip byte-for-byte through
	// base64 -- this is exactly why base64 is unconditional here, not
	// a per-key text/binary guess.
	binVal, ok := found["account:3"]
	if !ok {
		t.Fatal("account:3 missing")
	}
	if binVal != string([]byte{0x00, 0x01, 0xFF, 0xFE}) {
		t.Errorf("binary value did not round-trip: got %x", []byte(binVal))
	}
}

func TestExportPebbleStore_Empty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty_store")
	seedPebbleDir(t, dir, map[string][]byte{}) // creates the store with zero keys

	outDir := t.TempDir()
	n, err := ExportPebbleStore(context.Background(), dir, "empty_store", outDir)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if n != 0 {
		t.Errorf("key count: got %d, want 0", n)
	}
	kvs := readPebbleJSONArray(t, filepath.Join(outDir, "empty_store.json"))
	if kvs == nil {
		t.Error("empty store must still produce a valid (empty) JSON array")
	}
}

func TestExportPebbleStores_MissingDirSkippedNotErrored(t *testing.T) {
	base := t.TempDir()
	realDir := filepath.Join(base, "real_store")
	seedPebbleDir(t, realDir, map[string][]byte{"k": []byte("v")})

	missingDir := filepath.Join(base, "never_used_by_this_tenant")
	// Deliberately never created.

	outDir := t.TempDir()
	specs := []PebbleStoreSpec{
		{Dir: realDir, Name: "real_store"},
		{Dir: missingDir, Name: "never_used"},
	}
	counts, err := ExportPebbleStores(context.Background(), specs, outDir)
	if err != nil {
		t.Fatalf("expected no error for a missing (never-used) store, got: %v", err)
	}
	if counts["real_store"] != 1 {
		t.Errorf("real_store count: got %d, want 1", counts["real_store"])
	}
	if _, ok := counts["never_used"]; ok {
		t.Error("never_used should not appear in counts at all -- it was skipped, not exported with zero rows")
	}
	if _, err := os.Stat(filepath.Join(outDir, "never_used.json")); !os.IsNotExist(err) {
		t.Error("no output file should exist for a skipped, never-used store")
	}
	if _, err := os.Stat(missingDir); !os.IsNotExist(err) {
		t.Error("checking a missing store's directory must not create it as a side effect")
	}
}
