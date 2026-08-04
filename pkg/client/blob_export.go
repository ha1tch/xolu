// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// blob_export.go — the async, tenant-scoped, blob-backed export client
// (T-145, redesigned 2026-08-03 -- see docs/RESOLVED.md and
// pkg/tenantexport's own doc comment for the full history). The
// requirement T-145 named never changed through that redesign: the
// client has to actually deliver a tenant's export data to the caller,
// streamed. Only the mechanism changed, from a single synchronous GET
// to an async ticket-based job the caller polls -- so the client
// surface has two layers:
//
//   - BlobExportStart / BlobExportStatus: the raw primitives, for a
//     caller that wants to see progress, poll on its own schedule, or
//     hand the ticket to something else entirely.
//   - Export: the convenience method most callers actually want --
//     runs the whole flow (start, poll to completion, download) and
//     streams the result to an io.Writer, the same one-call experience
//     the original synchronous design had.
//
// Hits POST/GET /api/v1/tenant/{tenant}/blob/export{,/​{ticket}} --
// tenant-scoped, unlike the old, shelved /api/v1/export (non-tenant-
// scoped, whole-database) this replaced. Uses the standard do()/
// buildURL() pipeline for Start/Status (small JSON round trips, no
// reason to bypass retry); Export's own final download reuses BlobGet,
// which already streams rather than buffers.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// BlobExportJobStatus is an export job's lifecycle state, mirroring
// pkg/tenantexport.JobStatus on the wire.
type BlobExportJobStatus string

const (
	BlobExportRunning  BlobExportJobStatus = "running"
	BlobExportComplete BlobExportJobStatus = "complete"
	BlobExportFailed   BlobExportJobStatus = "failed"
)

// BlobExportJob is the status of one export job.
type BlobExportJob struct {
	Ticket string              `json:"ticket"`
	Status BlobExportJobStatus `json:"status"`
	// BlobKey is set once Status == BlobExportComplete -- the key this
	// tenant's export is stored under, retrievable via BlobGet.
	BlobKey string `json:"blob_key,omitempty"`
	// Error is set once Status == BlobExportFailed.
	Error string `json:"error,omitempty"`
}

// BlobExportStart starts an async export job for this client's own
// tenant. Returns immediately with a ticket; the export itself runs in
// the background server-side (deliberately low-priority and throttled
// to one job in flight per tenant -- a second call while one is
// already running returns *client.Error with HTTPStatus 409, carrying
// the existing ticket in its message).
//
// Hits POST /api/v1/tenant/{tenant}/blob/export. Returns *client.Error
// on non-2xx.
func (c *Client) BlobExportStart(ctx context.Context) (*BlobExportJob, error) {
	var result BlobExportJob
	if err := c.do(ctx, http.MethodPost, "/blob/export", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// BlobExportStatus polls one export job's status by ticket.
//
// Hits GET /api/v1/tenant/{tenant}/blob/export/{ticket}. Returns
// *client.Error (HTTPStatus 404) if the ticket is unknown to this
// tenant -- including a ticket that belongs to a different tenant,
// which is treated identically to "not found" rather than confirming
// it exists elsewhere.
func (c *Client) BlobExportStatus(ctx context.Context, ticket string) (*BlobExportJob, error) {
	if ticket == "" {
		return nil, fmt.Errorf("xolu: BlobExportStatus requires a non-empty ticket")
	}
	path := "/blob/export/" + url.PathEscape(ticket)
	var result BlobExportJob
	if err := c.do(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ExportResult describes a completed, delivered export.
type ExportResult struct {
	// Ticket is the job that produced this export, in case the caller
	// wants to correlate it with server-side logs.
	Ticket string
	// BlobKey is the key the export is stored under -- the same key
	// BlobExportStatus reported, kept here so a caller using Export's
	// one-call form still has it without a separate status check.
	BlobKey string
	SHA256  string
	Size    int64
}

// blobExportPollInterval is Export's fixed polling cadence. A package-
// level var, not a local const, so tests can override it -- production
// behaviour is unaffected, the value is simply not exported for
// callers to tune (see Export's own doc comment on why: a caller
// needing a different cadence should use BlobExportStart/Status
// directly).
var blobExportPollInterval = 2 * time.Second

// Export runs a complete tenant export and streams the result to w:
// starts the job, polls until it completes or fails, then downloads
// the resulting blob (via BlobGet, which streams rather than buffers)
// directly into w. This is the one-call convenience form; a caller
// wanting to observe progress, poll on its own schedule, or start an
// export without immediately waiting on it should use
// BlobExportStart/BlobExportStatus directly instead.
//
// Polls every 2 seconds -- fixed, not configurable here; a caller
// needing a different cadence is exactly the caller who should be
// using the two primitives directly rather than this convenience
// wrapper. Respects ctx: cancelling or setting a deadline on ctx stops
// the poll loop and returns ctx.Err(), same as any other call on this
// client -- there is no separate timeout parameter, by design (Go's
// own idiom: the caller controls how long they're willing to wait via
// the context they pass in, e.g. context.WithTimeout(ctx, 10*time.Minute)
// for a large tenant).
//
// Returns a plain Go error (not *client.Error) if the job itself
// fails server-side -- there's no HTTP status to carry for that case,
// just the job's own recorded failure reason.
func (c *Client) Export(ctx context.Context, w io.Writer) (*ExportResult, error) {
	job, err := c.BlobExportStart(ctx)
	if err != nil {
		return nil, err
	}

	for job.Status == BlobExportRunning {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(blobExportPollInterval):
		}
		job, err = c.BlobExportStatus(ctx, job.Ticket)
		if err != nil {
			return nil, err
		}
	}

	if job.Status == BlobExportFailed {
		return nil, fmt.Errorf("xolu: export failed (ticket %s): %s", job.Ticket, job.Error)
	}

	blob, err := c.BlobGet(ctx, job.BlobKey)
	if err != nil {
		return nil, fmt.Errorf("xolu: export completed but downloading the result failed: %w", err)
	}
	defer func() { _ = blob.Body.Close() }()

	if _, err := io.Copy(w, blob.Body); err != nil {
		return nil, fmt.Errorf("xolu: export download interrupted: %w", err)
	}

	return &ExportResult{
		Ticket:  job.Ticket,
		BlobKey: job.BlobKey,
		SHA256:  blob.SHA256,
		Size:    blob.Size,
	}, nil
}
