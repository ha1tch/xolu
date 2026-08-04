// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// blob_export_handlers.go — async, tenant-scoped, blob-backed export
// (design settled directly with the team, 2026-08-03, replacing the
// earlier synchronous, non-tenant-scoped GET /api/v1/export -- see
// T-149 and pkg/tenantexport's own doc comment for the full history).
//
// Two routes, matching the same shape as OQL's own async query flow
// (handleOQLQueryAsync/Status/Result) rather than inventing a new
// pattern:
//
//	POST /api/v1/tenant/{tenant_id}/blob/export         -> {ticket, status}
//	GET  /api/v1/tenant/{tenant_id}/blob/export/{ticket} -> {ticket, status, blob_key?, error?}
//
// There is no separate "claim" endpoint. Once status is "complete",
// blob_key names a real key in that same tenant's blob store,
// retrievable through the existing BlobGet/BlobHead machinery
// (T-142) -- the blob IS the claim; a caller with the tenant's own
// credentials can fetch it the same way as any other blob.

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenantexport"
)

// handleBlobExportStart starts an async export job for the request's
// own tenant.
func (s *Server) handleBlobExportStart(w http.ResponseWriter, r *http.Request) {
	if s.tenantExportJobs == nil {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrStorageFailed,
			"Export job manager not initialised")
		return
	}

	bs, _, tid, ok := s.blobStoreFor(w, r)
	if !ok {
		return
	}

	store := s.getStore(r.Context())
	sqlStore, ok := store.(*storage.SQLiteStore)
	if !ok {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrStorageFailed,
			"Export is only supported against a SQLite-backed store")
		return
	}
	primaryDB := sqlStore.DB()

	exportKey := fmt.Sprintf("%s%d.zip", tenantexport.ExportKeyPrefix, tid)
	ticket, err := s.tenantExportJobs.Submit(tid, func() (*tenantexport.PackageResult, error) {
		// context.Background(), NOT r.Context(): this closure runs in
		// a background goroutine that outlives the triggering request
		// by design -- r.Context() is cancelled by Go's own HTTP
		// server machinery the moment this handler returns (after
		// writing the 202 below), which would cancel the export
		// almost immediately. Confirmed as a real bug by the
		// integration test before this fix existed, not assumed --
		// matches oql.JobManager's own established pattern
		// (context.Background() as the base for async work, per
		// pkg/oql/oql.go's own Submit).
		return tenantexport.ExportTenant(context.Background(), primaryDB, s.config.BaseDir, tid, bs, exportKey)
	})
	if err != nil {
		if inFlight, ok := err.(*tenantexport.ErrTenantExportInFlight); ok {
			s.writeError(w, http.StatusConflict, xoluerr.ErrBlobExportInFlight,
				fmt.Sprintf("An export is already running for this tenant (ticket %s)", inFlight.ExistingTicket))
			return
		}
		s.logger.Error().Err(err).Uint16("tenant", uint16(tid)).Msg("blob export: submit failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed,
			"Failed to start export")
		return
	}

	s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"ticket": ticket,
		"status": string(tenantexport.JobRunning),
	})
}

// handleBlobExportStatus polls an export job's own status.
//
// Deliberately does NOT check that the ticket belongs to the
// requesting tenant beyond what the job manager itself tracks
// (Job.TenantID) -- a ticket is an unguessable random value (128 bits
// from crypto/rand, pkg/tenantexport's own newTicket), so knowledge of
// one already implies having received it from this same tenant's own
// earlier POST. Cross-tenant enumeration is not a realistic concern at
// that entropy; still, the tenant recorded on the job is checked and
// mismatches are treated identically to "not found" rather than
// leaking whether a ticket exists for a DIFFERENT tenant.
func (s *Server) handleBlobExportStatus(w http.ResponseWriter, r *http.Request) {
	if s.tenantExportJobs == nil {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrStorageFailed,
			"Export job manager not initialised")
		return
	}

	_, _, tid, ok := s.blobStoreFor(w, r)
	if !ok {
		return
	}

	ticket := chi.URLParam(r, "ticket")
	if ticket == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "ticket required")
		return
	}

	job, ok := s.tenantExportJobs.Status(ticket)
	if !ok || job.TenantID != tid {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrBlobExportNotFound, "Export ticket not found")
		return
	}

	response := map[string]interface{}{
		"ticket": job.Ticket,
		"status": string(job.Status),
	}
	switch job.Status {
	case tenantexport.JobComplete:
		response["blob_key"] = job.BlobKey
	case tenantexport.JobFailed:
		response["error"] = job.Error
	}
	s.writeJSON(w, http.StatusOK, response)
}
