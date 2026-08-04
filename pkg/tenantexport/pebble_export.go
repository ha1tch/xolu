// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package tenantexport

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cockroachdb/pebble"
)

// PebbleKV is one key/value pair from a Pebble store, base64-encoded.
//
// Unconditional base64 for both fields, not a per-value guess at
// UTF-8-vs-binary: unlike the SQLite side (where this package knows
// specific TEXT columns hold JSON/decimal strings), a Pebble store's
// key and value encoding is internal to whichever primitive owns it
// (bal's rollup deltas, cal's occupancy bitmap, ts's own encoding) --
// this package has no reason to assume any of it is text, and
// guessing wrong per-key would make the same store's export
// inconsistently shaped from one key to the next.
type PebbleKV struct {
	Key   string `json:"key_b64"`
	Value string `json:"value_b64"`
}

// ExportPebbleStore iterates every key/value pair in the Pebble
// database at dir and writes them as a JSON array to
// outDir/<name>.json. dir is expected to already be scoped to one
// tenant (storelayout.TenantBalRollupDir, TenantCalDir, TenantTSDir,
// etc. each return a per-tenant directory) -- there is no tenant_id
// filtering step here because none is needed, unlike the SQLite side's
// shared tables.
//
// Opens its own read-only handle rather than requiring an existing
// *pebble.DB be passed in -- Pebble does not support opening the same
// directory read-write more than once, and this export is meant to run
// alongside the primitive's own live handle (already open read-write
// for normal request traffic), not compete with it for the same
// *pebble.DB instance.
func ExportPebbleStore(ctx context.Context, dir, name, outDir string) (int, error) {
	db, err := pebble.Open(dir, &pebble.Options{ReadOnly: true})
	if err != nil {
		return 0, fmt.Errorf("tenantexport: open pebble store %s: %w", dir, err)
	}
	defer db.Close()

	if err := os.MkdirAll(outDir, 0755); err != nil {
		return 0, fmt.Errorf("tenantexport: mkdir %s: %w", outDir, err)
	}
	outPath := filepath.Join(outDir, name+".json")
	f, err := os.Create(outPath)
	if err != nil {
		return 0, fmt.Errorf("tenantexport: create %s: %w", outPath, err)
	}
	defer f.Close()

	iter, err := db.NewIter(nil)
	if err != nil {
		return 0, fmt.Errorf("tenantexport: iterate %s: %w", dir, err)
	}
	defer iter.Close()

	enc := json.NewEncoder(f)
	if _, err := f.WriteString("[\n"); err != nil {
		return 0, fmt.Errorf("tenantexport: write %s: %w", outPath, err)
	}

	n := 0
	for valid := iter.First(); valid; valid = iter.Next() {
		if err := ctx.Err(); err != nil {
			return n, fmt.Errorf("tenantexport: %s export cancelled after %d keys: %w", name, n, err)
		}
		kv := PebbleKV{
			Key:   base64.StdEncoding.EncodeToString(iter.Key()),
			Value: base64.StdEncoding.EncodeToString(iter.Value()),
		}
		if n > 0 {
			if _, err := f.WriteString(",\n"); err != nil {
				return n, fmt.Errorf("tenantexport: write %s: %w", outPath, err)
			}
		}
		if err := enc.Encode(kv); err != nil {
			return n, fmt.Errorf("tenantexport: encode %s key %d: %w", name, n, err)
		}
		n++
	}
	if err := iter.Error(); err != nil {
		return n, fmt.Errorf("tenantexport: iterating %s: %w", dir, err)
	}

	if _, err := f.WriteString("]\n"); err != nil {
		return n, fmt.Errorf("tenantexport: write %s: %w", outPath, err)
	}
	return n, nil
}

// PebbleStoreSpec names one per-tenant Pebble store to export -- Dir is
// its own directory (already tenant-scoped, e.g.
// storelayout.TenantBalRollupDir(base, tenantID)), Name is the output
// file's own base name (e.g. "bal_rollup").
type PebbleStoreSpec struct {
	Dir  string
	Name string
}

// ExportPebbleStores runs ExportPebbleStore for every spec in order,
// stopping at the first error. A spec whose Dir does not exist (a
// tenant that has never used that primitive, e.g. no cal bookings ever
// made) is skipped, not an error -- an absent store is a legitimate
// "no data" case, not a failure, and pebble.Open on a missing
// directory would otherwise create an empty one as a side effect of
// merely checking.
func ExportPebbleStores(ctx context.Context, specs []PebbleStoreSpec, outDir string) (map[string]int, error) {
	counts := make(map[string]int, len(specs))
	for _, spec := range specs {
		if _, err := os.Stat(spec.Dir); os.IsNotExist(err) {
			continue
		}
		n, err := ExportPebbleStore(ctx, spec.Dir, spec.Name, outDir)
		if err != nil {
			return counts, err
		}
		counts[spec.Name] = n
	}
	return counts, nil
}
