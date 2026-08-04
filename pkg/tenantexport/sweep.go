// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package tenantexport

import (
	"context"
	"fmt"
	"time"

	"github.com/ha1tch/xolu/pkg/blob"
	"github.com/ha1tch/xolu/pkg/gc"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// ExportKeyPrefix is the fixed prefix every export blob is stored
// under (see handleBlobExportStart's own exportKey construction,
// "export-%d.zip") -- SweepExpiredExports lists exactly this prefix,
// never touching a caller's own blobs that happen to start
// differently.
const ExportKeyPrefix = "export-"

// SweepExpiredExports deletes every blob under ExportKeyPrefix in bs
// whose StoredAt is older than ttl. Implements the same one-store,
// one-sweep shape as blob.GCWorker.Sweep -- a caller enumerating
// multiple tenants' stores (see pkg/server's blobManager.Sweep for the
// established iteration pattern this mirrors) calls this once per
// store and aggregates the returned gc.Report itself, the same way
// blobManager already aggregates blob.GCWorker's own reports.
//
// A per-key deletion failure is counted in Report.Errors and does not
// stop the sweep -- one export blob that fails to delete (a
// permissions issue, a concurrent delete already in flight) should not
// prevent every other expired export in the same store from being
// cleaned up.
func SweepExpiredExports(ctx context.Context, bs *blob.Store, ttl time.Duration) (gc.Report, error) {
	start := time.Now()
	report := gc.Report{}

	metas, err := bs.List(ExportKeyPrefix)
	if err != nil {
		report.Duration = time.Since(start)
		return report, fmt.Errorf("tenantexport: list %s* for sweep: %w", ExportKeyPrefix, err)
	}

	cutoff := ot.Now().Time().Add(-ttl)
	for _, m := range metas {
		if err := ctx.Err(); err != nil {
			report.Duration = time.Since(start)
			return report, err
		}
		report.Examined++
		if m.StoredAt.After(cutoff) {
			continue // not expired yet
		}
		if err := bs.Delete(m.Key); err != nil {
			report.Errors++
			continue
		}
		report.Collected++
	}

	report.Duration = time.Since(start)
	return report, nil
}
