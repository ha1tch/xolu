// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// schema_promotion_handlers.go — T-151: promoting a schemaless entity
// type to schemaful, guided by heuristic inference over its actual
// data (schema_inference.go).
//
// Three endpoints:
//
//	GET  /api/v1/entity/{type}/schema-suggestion   -- read-only preview
//	POST /api/v1/entities/promote/flex/{type}      -- fast, no data migration
//	POST /api/v1/entities/promote/strict/{type}    -- async: validates every
//	                                                   existing row first,
//	                                                   migrates only if all pass
//	GET  /api/v1/entities/promote/status/{ticket}  -- poll a strict job
//
// flex vs strict, and why both exist: flex registers the schema and
// creates the adapted table immediately, exactly like
// DefineEntitySchema -- fast, but pre-existing rows stay in blob
// storage, individually reachable by ID but invisible to LIST/count
// (verified directly; see this session's own record). strict is the
// real fix for that gap: it validates EVERY existing row against the
// candidate schema before touching anything, and only if all of them
// pass does it register the schema and migrate every row into the
// adapted table as one transaction. If any row fails, NOTHING changes
// -- the entity type is exactly as schemaless as before, with the
// specific failing rows and reasons reported so the caller can fix
// the data or the schema and retry. Async because validating and
// migrating every row of a large entity population is not a
// sub-second operation the way flex's registration step is.
//
// Route shape (the team's own design, not this implementation's
// invention): /entities/promote/{flex,strict}/{type}, not
// /entity/{type}/promote/{flex,strict} -- the action is a stable
// prefix independent of entity type specifically so a future RBAC
// layer can grant or deny "who may run strict promotions" as a single
// pattern match on the path, without needing per-entity-type rules.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/storage"
)

func (s *Server) handleSchemaSuggestion(w http.ResponseWriter, r *http.Request) {
	entityType := chi.URLParam(r, "entity")
	if err := validateEntityName(entityType); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidEntity, err.Error())
		return
	}

	sqlStore, ok := s.getStore(r.Context()).(*storage.SQLiteStore)
	if !ok {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrStorageFailed,
			"Schema suggestion is only supported against a SQLite-backed store")
		return
	}

	suggestion, err := suggestSchemaFor(r.Context(), sqlStore, entityType)
	if err != nil {
		if err == errEntityTypeNotFound {
			s.writeError(w, http.StatusNotFound, xoluerr.ErrInvalidEntity,
				fmt.Sprintf("Entity type %q has no data", entityType))
			return
		}
		s.logger.Error().Err(err).Str("entity", entityType).Msg("schema suggestion: inference failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, "Failed to infer schema")
		return
	}

	s.writeJSON(w, http.StatusOK, suggestion)
}

// resolveSchemaForPromotion determines the schema to apply: an
// explicit, non-nil body if the caller sent one (a literal JSON "null"
// payload decodes successfully to a nil map and does NOT count as
// explicit -- a real bug this guarded against, caught before it
// shipped), or the auto-inferred suggestion otherwise. Shared by both
// flex and strict so their "what schema am I even applying" logic
// can't drift apart.
func (s *Server) resolveSchemaForPromotion(r *http.Request, w http.ResponseWriter, sqlStore *storage.SQLiteStore, entityType string) (schema map[string]interface{}, autoInferred bool, ok bool) {
	if r.ContentLength != 0 {
		var explicit map[string]interface{}
		if !s.decodeJSON(w, r, &explicit) {
			return nil, false, false
		}
		if explicit != nil {
			return explicit, false, true
		}
	}
	suggestion, err := suggestSchemaFor(r.Context(), sqlStore, entityType)
	if err != nil {
		if err == errEntityTypeNotFound {
			s.writeError(w, http.StatusNotFound, xoluerr.ErrInvalidEntity,
				fmt.Sprintf("Entity type %q has no data to infer a schema from", entityType))
			return nil, false, false
		}
		s.logger.Error().Err(err).Str("entity", entityType).Msg("promote: inference failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, "Failed to infer schema")
		return nil, false, false
	}
	return suggestion.SuggestedSchema, true, true
}

// applySchemaAndRegister is the shared "commit to this schema" step:
// load it into the validator, persist it to disk, rebuild the RI
// registry, and register the adapted table. Used by flex directly,
// and by strict only after every existing row has already validated
// successfully. Takes ctx directly, not an *http.Request -- strict's
// own call happens from a background job goroutine with no real
// request to hand it.
func (s *Server) applySchemaAndRegister(ctx context.Context, sqlStore *storage.SQLiteStore, entityType string, schema map[string]interface{}) error {
	if err := validateSchemaFieldNames(schema); err != nil {
		return err
	}
	if err := s.validator.LoadSchema(entityType, schema); err != nil {
		return fmt.Errorf("load schema: %w", err)
	}
	s.rebuildRIRegistry()
	if err := s.validator.SaveSchema(entityType, schema); err != nil {
		s.logger.Warn().Err(err).Str("entity", entityType).Msg("promote: failed to persist schema to disk")
	}
	if err := sqlStore.RegisterAdaptedEntity(ctx, entityType, schema); err != nil {
		return fmt.Errorf("register adapted table: %w", err)
	}
	return nil
}

func (s *Server) handlePromoteFlex(w http.ResponseWriter, r *http.Request) {
	entityType := chi.URLParam(r, "entity")
	if err := validateEntityName(entityType); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidEntity, err.Error())
		return
	}
	sqlStore, ok := s.getStore(r.Context()).(*storage.SQLiteStore)
	if !ok {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrStorageFailed,
			"Promote is only supported against a SQLite-backed store")
		return
	}

	schema, autoInferred, ok := s.resolveSchemaForPromotion(r, w, sqlStore, entityType)
	if !ok {
		return
	}
	if err := s.applySchemaAndRegister(r.Context(), sqlStore, entityType, schema); err != nil {
		s.logger.Error().Err(err).Str("entity", entityType).Msg("promote/flex failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, err.Error())
		return
	}

	// flex does not migrate existing rows -- a real, non-obvious
	// consequence (verified directly: a pre-existing row stays
	// reachable by GET /{entity}/{id} but disappears from GET
	// /{entity} LIST and from GET /entities' own count) worth
	// surfacing explicitly rather than leaving the caller to discover
	// it. strict exists specifically to avoid this.
	preExistingRows := 0
	if err := sqlStore.DB().QueryRowContext(r.Context(),
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE entity_type = ?", sqlStore.NodesTable()),
		entityType).Scan(&preExistingRows); err != nil {
		s.logger.Warn().Err(err).Str("entity", entityType).Msg("promote/flex: could not check for pre-existing blob rows")
	}

	s.logger.Info().Str("entity", entityType).Bool("auto_inferred", autoInferred).
		Int("pre_existing_rows_not_migrated", preExistingRows).Msg("Promoted entity type to schemaful (flex)")

	response := map[string]interface{}{
		"message":       fmt.Sprintf("Entity type %s promoted to schemaful (flex)", entityType),
		"auto_inferred": autoInferred,
		"schema":        schema,
	}
	if preExistingRows > 0 {
		response["warning"] = fmt.Sprintf(
			"%d pre-existing row(s) were NOT migrated into the new adapted table (flex mode). "+
				"They remain reachable via GET /%s/{id} but will not appear in GET /%s (list) or in GET "+
				"/entities' own count. Use POST /entities/promote/strict/%s instead to validate and migrate "+
				"existing data as part of promotion.",
			preExistingRows, entityType, entityType, entityType)
	}
	s.writeJSON(w, http.StatusCreated, response)
}

func (s *Server) handlePromoteStrict(w http.ResponseWriter, r *http.Request) {
	entityType := chi.URLParam(r, "entity")
	if err := validateEntityName(entityType); err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidEntity, err.Error())
		return
	}
	if s.promoteJobs == nil {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrStorageFailed, "Promote job manager not initialised")
		return
	}
	sqlStore, ok := s.getStore(r.Context()).(*storage.SQLiteStore)
	if !ok {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrStorageFailed,
			"Promote is only supported against a SQLite-backed store")
		return
	}

	schema, autoInferred, ok := s.resolveSchemaForPromotion(r, w, sqlStore, entityType)
	if !ok {
		return
	}

	compiled, err := compileStandalone(schema)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrInvalidEntity, "Schema does not compile: "+err.Error())
		return
	}

	tid := sqlStore.Config().TenantID
	ticket, err := s.promoteJobs.Submit(tid, entityType, func() (*PromoteResult, []RowValidationFailure, error) {
		// context.Background(), not r.Context(): this closure runs in
		// a background goroutine that outlives the triggering request
		// -- the same real bug (and the same fix) as T-149's own
		// export job, caught there by an integration test before it
		// shipped; applied here from the start rather than
		// rediscovered.
		ctx := context.Background()
		validCount, failures, err := validateAllRows(ctx, sqlStore, entityType, compiled)
		if err != nil {
			return nil, nil, fmt.Errorf("validating existing rows: %w", err)
		}
		if len(failures) > 0 {
			return nil, failures, nil
		}
		if err := s.applySchemaAndRegister(ctx, sqlStore, entityType, schema); err != nil {
			return nil, nil, fmt.Errorf("registering schema: %w", err)
		}
		migrated, err := sqlStore.MigrateBlobEntitiesToAdapted(ctx, entityType)
		if err != nil {
			return nil, nil, fmt.Errorf("migrating rows after successful validation of %d rows: %w", validCount, err)
		}
		return &PromoteResult{MigratedRows: migrated, AutoInferred: autoInferred}, nil, nil
	})
	if err != nil {
		if inFlight, ok := err.(*ErrPromoteInFlight); ok {
			s.writeError(w, http.StatusConflict, xoluerr.ErrPromoteInFlight,
				fmt.Sprintf("A strict promotion is already running for this entity type (ticket %s)", inFlight.ExistingTicket))
			return
		}
		s.logger.Error().Err(err).Str("entity", entityType).Msg("promote/strict: submit failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, "Failed to start strict promotion")
		return
	}

	s.writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"ticket": ticket,
		"status": string(PromoteJobRunning),
	})
}

func (s *Server) handlePromoteStatus(w http.ResponseWriter, r *http.Request) {
	if s.promoteJobs == nil {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrStorageFailed, "Promote job manager not initialised")
		return
	}
	ticket := chi.URLParam(r, "ticket")
	if ticket == "" {
		s.writeError(w, http.StatusBadRequest, xoluerr.ErrMissingParam, "ticket required")
		return
	}
	job, ok := s.promoteJobs.Status(ticket)
	if !ok {
		s.writeError(w, http.StatusNotFound, xoluerr.ErrPromoteTicketNotFound, "Promotion ticket not found")
		return
	}

	response := map[string]interface{}{
		"ticket":      job.Ticket,
		"entity_type": job.EntityType,
		"status":      string(job.Status),
	}
	switch job.Status {
	case PromoteJobComplete:
		response["result"] = job.Result
	case PromoteJobRejected:
		response["failures"] = job.Failures
	case PromoteJobFailed:
		response["error"] = job.Error
	}
	s.writeJSON(w, http.StatusOK, response)
}

var errEntityTypeNotFound = fmt.Errorf("entity type has no data")

// suggestSchemaFor samples up to defaultSampleSize rows of entityType's
// stored data and runs inference over them.
//
// Deliberately reads directly from the nodes table, not through
// listTenantEntities/the adapted-table path: promotion only makes
// sense for a genuinely schemaless entity type (one still stored as
// blob JSON) -- an already-adapted entity type has a real schema by
// definition and has nothing to promote.
func suggestSchemaFor(ctx context.Context, sqlStore *storage.SQLiteStore, entityType string) (*SchemaSuggestion, error) {
	tid := sqlStore.Config().TenantID
	db := sqlStore.DB()
	nodesTable := tid.NodesTableName()

	var totalRows int
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE entity_type = ?", nodesTable),
		entityType).Scan(&totalRows); err != nil {
		return nil, fmt.Errorf("count %s: %w", nodesTable, err)
	}
	if totalRows == 0 {
		return nil, errEntityTypeNotFound
	}

	rows, err := db.QueryContext(ctx,
		fmt.Sprintf("SELECT data FROM %s WHERE entity_type = ? LIMIT ?", nodesTable),
		entityType, defaultSampleSize)
	if err != nil {
		return nil, fmt.Errorf("sample %s: %w", nodesTable, err)
	}
	defer func() { _ = rows.Close() }()

	var blobs []string
	for rows.Next() {
		var blob sql.NullString
		if err := rows.Scan(&blob); err != nil {
			return nil, fmt.Errorf("scan %s row: %w", nodesTable, err)
		}
		if blob.Valid {
			blobs = append(blobs, blob.String)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterating %s: %w", nodesTable, err)
	}

	return inferSchema(entityType, totalRows, blobs)
}
