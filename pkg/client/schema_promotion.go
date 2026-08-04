// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// schema_promotion.go — T-151: promoting a schemaless entity type to
// schemaful, guided by heuristic inference over its actual data.
//
// Mirrors the server's own three-endpoint design exactly (see
// pkg/server/schema_promotion_handlers.go's own header comment for the
// full rationale, verified there with real tests before this client
// was written against it):
//
//   - GetSchemaSuggestion: read-only preview of what the engine would
//     guess, with per-field reasoning -- nothing is applied.
//   - PromoteFlex: fast, registers the schema immediately. Does NOT
//     migrate pre-existing rows into the new adapted table -- they
//     stay reachable by ID but drop out of LIST/count until someone
//     re-writes them. Fine for an empty or throwaway entity type;
//     surprising otherwise, which is why PromoteFlexResult surfaces a
//     Warning field when the server detected pre-existing rows.
//   - PromoteStrictStart/PromoteStrictStatus: the real fix for that
//     gap -- validates every existing row before touching anything,
//     migrates all of them atomically only if every row passes, and
//     leaves the entity type completely untouched otherwise (reporting
//     exactly which rows failed and why). Async, because validating
//     and migrating a large entity population isn't a sub-second
//     operation the way flex's registration step is.
//   - PromoteStrict: the convenience wrapper most callers actually
//     want -- runs Start, polls Status to completion, returns the
//     final job. Same shape as this package's own Export() convenience
//     method over BlobExportStart/BlobExportStatus.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// ─── Schema suggestion ──────────────────────────────────────────────────────

// FieldAnalysis is one field's inferred shape and the evidence behind
// it -- mirrors the server's own type field-for-field.
type FieldAnalysis struct {
	Field         string   `json:"field"`
	InferredType  string   `json:"inferred_type"`
	Coverage      float64  `json:"coverage"`
	Confidence    string   `json:"confidence"`
	SuggestedEnum []string `json:"suggested_enum,omitempty"`
	Note          string   `json:"note,omitempty"`
}

// SchemaSuggestion is the full response from GetSchemaSuggestion.
type SchemaSuggestion struct {
	EntityType      string                 `json:"entity_type"`
	SampledRows     int                    `json:"sampled_rows"`
	TotalRows       int                    `json:"total_rows"`
	SuggestedSchema map[string]interface{} `json:"suggested_schema"`
	FieldAnalysis   []FieldAnalysis        `json:"field_analysis"`
}

// GetSchemaSuggestion previews what schema-promotion's heuristic
// engine would infer for entityType, without applying anything -- safe
// to call at any time, purely read-only.
//
// Hits GET /api/v1/entity/{type}/schema-suggestion. Returns
// *client.Error on non-2xx (404 if the entity type has no data to
// infer from).
func (c *Client) GetSchemaSuggestion(ctx context.Context, entityType string) (*SchemaSuggestion, error) {
	if err := validateEntityTypeName(entityType); err != nil {
		return nil, err
	}
	path := "/entity/" + url.PathEscape(entityType) + "/schema-suggestion"
	var suggestion SchemaSuggestion
	if err := c.do(ctx, http.MethodGet, path, nil, &suggestion); err != nil {
		return nil, err
	}
	return &suggestion, nil
}

// ─── flex ───────────────────────────────────────────────────────────────────

// PromoteFlexResult is the response from PromoteFlex.
type PromoteFlexResult struct {
	Message      string                 `json:"message"`
	AutoInferred bool                   `json:"auto_inferred"`
	Schema       map[string]interface{} `json:"schema"`
	// Warning is set when the server detected pre-existing rows that
	// were NOT migrated into the new adapted table -- see this file's
	// own header comment. Empty when there was nothing pre-existing.
	Warning string `json:"warning,omitempty"`
}

// PromoteFlex promotes entityType to schemaful immediately: registers
// schema (or, if schema is nil, an auto-inferred one) and creates the
// adapted table. Does not migrate pre-existing rows -- check
// result.Warning; a non-empty value means some exist and are now
// split across storage (reachable by ID, invisible to LIST/count).
// Use PromoteStrict instead if that split is not acceptable for this
// entity type.
//
// Hits POST /api/v1/entities/promote/flex/{type}. Returns
// *client.Error on non-2xx (404 if schema is nil and the entity type
// has no data to infer from).
func (c *Client) PromoteFlex(ctx context.Context, entityType string, schema map[string]interface{}) (*PromoteFlexResult, error) {
	if err := validateEntityTypeName(entityType); err != nil {
		return nil, err
	}
	path := "/entities/promote/flex/" + url.PathEscape(entityType)
	var result PromoteFlexResult
	if err := c.do(ctx, http.MethodPost, path, schema, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ─── strict ─────────────────────────────────────────────────────────────────

// PromoteJobStatus is a strict-promotion job's lifecycle state.
type PromoteJobStatus string

const (
	PromoteJobRunning  PromoteJobStatus = "running"
	PromoteJobComplete PromoteJobStatus = "complete"
	PromoteJobFailed   PromoteJobStatus = "failed"
	// PromoteJobRejected means strict promotion worked exactly as
	// designed and correctly declined to promote because not every
	// row validated against the schema -- distinct from Failed, which
	// means something actually went wrong (a storage error). Check
	// Failures for exactly which rows and why.
	PromoteJobRejected PromoteJobStatus = "rejected"
)

// PromoteResult is the outcome of a successfully completed strict
// promotion.
type PromoteResult struct {
	MigratedRows int  `json:"migrated_rows"`
	AutoInferred bool `json:"auto_inferred"`
}

// RowValidationFailure is one row that failed validation during
// strict promotion.
type RowValidationFailure struct {
	ID     int      `json:"id"`
	Errors []string `json:"errors"`
}

// PromoteJob is a strict-promotion job's current status.
type PromoteJob struct {
	Ticket     string                 `json:"ticket"`
	EntityType string                 `json:"entity_type"`
	Status     PromoteJobStatus       `json:"status"`
	Result     *PromoteResult         `json:"result,omitempty"`
	Failures   []RowValidationFailure `json:"failures,omitempty"`
	Error      string                 `json:"error,omitempty"`
}

// PromoteStrictStart starts an async strict promotion for entityType:
// validates every existing row against schema (or, if schema is nil,
// an auto-inferred one) and, only if every row passes, migrates all of
// them into a newly-registered adapted table as one atomic operation.
// Returns immediately with a ticket to poll via PromoteStrictStatus.
//
// Throttled per (tenant, entity type), not per tenant -- promoting two
// different entity types for the same tenant concurrently is fine. A
// second call for an entity type already being promoted returns
// *client.Error with HTTPStatus 409, carrying the existing ticket in
// its message.
//
// Hits POST /api/v1/entities/promote/strict/{type}. Returns
// *client.Error on non-2xx.
func (c *Client) PromoteStrictStart(ctx context.Context, entityType string, schema map[string]interface{}) (*PromoteJob, error) {
	if err := validateEntityTypeName(entityType); err != nil {
		return nil, err
	}
	path := "/entities/promote/strict/" + url.PathEscape(entityType)
	var job PromoteJob
	if err := c.do(ctx, http.MethodPost, path, schema, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// PromoteStrictStatus polls one strict-promotion job's status by
// ticket.
//
// Hits GET /api/v1/entities/promote/status/{ticket}. Returns
// *client.Error (HTTPStatus 404) if the ticket is unknown.
func (c *Client) PromoteStrictStatus(ctx context.Context, ticket string) (*PromoteJob, error) {
	if ticket == "" {
		return nil, fmt.Errorf("xolu: PromoteStrictStatus requires a non-empty ticket")
	}
	path := "/entities/promote/status/" + url.PathEscape(ticket)
	var job PromoteJob
	if err := c.do(ctx, http.MethodGet, path, nil, &job); err != nil {
		return nil, err
	}
	return &job, nil
}

// promoteStrictPollInterval is PromoteStrict's fixed polling cadence
// -- a package-level var, not a local const, so tests can override it.
// See Export's own identical pattern (blob_export.go) for why this
// isn't a caller-configurable parameter.
var promoteStrictPollInterval = 2 * time.Second

// PromoteStrict runs a complete strict promotion and waits for the
// result: starts the job, polls until it completes, is rejected, or
// fails, and returns the final PromoteJob. This is the one-call
// convenience form; a caller wanting to observe progress or start a
// promotion without immediately waiting on it should use
// PromoteStrictStart/PromoteStrictStatus directly instead.
//
// Respects ctx: cancelling or setting a deadline on ctx stops the poll
// loop and returns ctx.Err(). Does NOT return an error for a rejected
// promotion -- rejection is a normal, successful outcome of strict
// promotion's own design (it means the check worked); inspect
// job.Status and job.Failures rather than relying on the error return
// to distinguish rejection from success.
func (c *Client) PromoteStrict(ctx context.Context, entityType string, schema map[string]interface{}) (*PromoteJob, error) {
	job, err := c.PromoteStrictStart(ctx, entityType, schema)
	if err != nil {
		return nil, err
	}

	for job.Status == PromoteJobRunning {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(promoteStrictPollInterval):
		}
		job, err = c.PromoteStrictStatus(ctx, job.Ticket)
		if err != nil {
			return nil, err
		}
	}

	if job.Status == PromoteJobFailed {
		return job, fmt.Errorf("xolu: strict promotion failed (ticket %s): %s", job.Ticket, job.Error)
	}
	return job, nil
}
