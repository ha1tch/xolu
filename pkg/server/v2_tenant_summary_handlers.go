// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_tenant_summary_handlers.go — a single-round-trip tenant data-
// presence summary (XOT213, wave 15), requested by the xoluman team
// so its own tenant-emptiness safety check stops making several
// separate round trips for one yes/no question. Deliberately
// synchronous (unlike blob_export_handlers.go's own async ticket
// flow next to it) -- this reads COUNT(*) across a fixed, small set
// of tables/stores and counts keys in three Pebble stores, not a full
// export; real production tenants are not expected to make this slow
// enough to need the async pattern blob export uses for genuinely
// large payloads.
//
//	GET /api/v1/tenant/{tenant_id}/tenant-summary -> TenantDataSummary + "empty" bool

import (
	"net/http"

	"github.com/ha1tch/xolu/pkg/blob"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenantexport"
)

func (s *Server) handleTenantSummary(w http.ResponseWriter, r *http.Request) {
	tid := getTenantIDNumeric(r.Context())

	store := s.getStore(r.Context())
	sqlStore, ok := store.(*storage.SQLiteStore)
	if !ok {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrStorageFailed,
			"Tenant summary is only supported against a SQLite-backed store")
		return
	}

	// Blob is disabled on plenty of real deployments -- that's a
	// normal, expected condition, treated the same as any other
	// primitive this tenant has never used (nil bs -> Blob: 0, not an
	// error). If blob IS enabled but opening the store genuinely
	// fails, that's a different thing: a real anomaly in a component
	// this endpoint would otherwise silently report as empty --
	// XOT216 exists precisely because a wrong "empty" answer from
	// this safety-check feature is a real cost, so this failure mode
	// is NOT swallowed into a quiet zero the way a merely-disabled
	// primitive is.
	var bs *blob.Store
	if s.blobMgr != nil {
		var err error
		bs, err = s.blobMgr.StoreFor(tid)
		if err != nil {
			s.logger.Error().Err(err).Uint16("tenant", uint16(tid)).Msg("tenant summary: open blob store failed")
			s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
				"Failed to open blob store for tenant summary")
			return
		}
	}

	summary, err := tenantexport.Summarize(r.Context(), sqlStore.DB(), s.config.BaseDir, tid, bs)
	if err != nil {
		s.logger.Error().Err(err).Uint16("tenant", uint16(tid)).Msg("tenant summary: Summarize failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"Failed to compute tenant summary")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"primary":    summary.Primary,
		"loc":        summary.Loc,
		"obj":        summary.Obj,
		"ts":         summary.TS,
		"cal_index":  summary.CalIndex,
		"bal_rollup": summary.BalRollup,
		"blob":       summary.Blob,
		"empty":      summary.Empty(),
	})
}
