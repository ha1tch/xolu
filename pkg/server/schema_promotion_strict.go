// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// schema_promotion_strict.go — the validate-then-commit machinery
// behind strict promotion: compile a candidate schema WITHOUT
// registering it, validate every existing row of the entity type
// against that standalone compilation, and only touch anything (load
// the schema, register the adapted table, migrate rows) if every row
// passes. A row that fails means nothing changes at all -- the entity
// type is exactly as schemaless afterward as before, with the specific
// failures reported so the caller can fix the data or adjust the
// schema and retry.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	ot "github.com/ha1tch/xolu/pkg/xolutime"

	"github.com/ha1tch/queryfy"
	"github.com/ha1tch/queryfy/builders"
	"github.com/ha1tch/queryfy/builders/jsonschema"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// RowValidationFailure is one row that failed validation against a
// candidate schema during strict promotion.
type RowValidationFailure struct {
	ID     int      `json:"id"`
	Errors []string `json:"errors"`
}

// compileStandalone compiles schema into a queryfy ObjectSchema
// WITHOUT registering it anywhere -- the same compilation
// JSONSchemaValidator.LoadSchemaWithWarnings itself does internally
// (pkg/validation/validation.go), reused here directly rather than
// duplicated, so a candidate schema can be validated against real data
// before any commitment is made to it.
func compileStandalone(schema map[string]interface{}) (*builders.ObjectSchema, error) {
	raw, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("marshal candidate schema: %w", err)
	}
	compiled, convErrs := jsonschema.FromJSON(raw, &jsonschema.Options{StoreUnknown: true})
	for _, e := range convErrs {
		if !e.IsWarning {
			return nil, fmt.Errorf("schema compilation error at %s: %s", e.Path, e.Message)
		}
	}
	obj, ok := compiled.(*builders.ObjectSchema)
	if !ok {
		return nil, fmt.Errorf("schema did not compile to an ObjectSchema (got %T)", compiled)
	}
	return obj, nil
}

// validateAllRows reads EVERY row of entityType (not a sample --
// strict mode's whole point is exhaustiveness) and validates each
// against compiled. Returns every failure found; an empty failures
// slice with a nil error means every row passed.
func validateAllRows(ctx context.Context, sqlStore *storage.SQLiteStore, entityType string, compiled *builders.ObjectSchema) (validCount int, failures []RowValidationFailure, err error) {
	db := sqlStore.DB()
	nodesTable := sqlStore.NodesTable()

	rows, err := db.QueryContext(ctx,
		"SELECT id, data FROM "+nodesTable+" WHERE entity_type = ?", entityType)
	if err != nil {
		return 0, nil, fmt.Errorf("query %s: %w", nodesTable, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return validCount, failures, err
		}
		var id int
		var dataStr string
		if err := rows.Scan(&id, &dataStr); err != nil {
			return validCount, failures, fmt.Errorf("scan row: %w", err)
		}
		var data map[string]interface{}
		if err := json.Unmarshal([]byte(dataStr), &data); err != nil {
			failures = append(failures, RowValidationFailure{ID: id, Errors: []string{"stored data is not valid JSON: " + err.Error()}})
			continue
		}
		// "id" is a system field, present in every stored row but
		// deliberately never part of an inferred (or hand-written)
		// schema's own properties -- inferSchema already excludes it
		// from analysis for the same reason. queryfy.Strict mode
		// rejects any field not explicitly listed in properties
		// regardless of additionalProperties, so validating the raw
		// row as-is would reject "id" on every single row. Caught
		// directly: an all-conforming dataset still failed strict
		// promotion entirely, on "id: unexpected field", before this
		// fix existed.
		delete(data, "id")

		vctx := queryfy.NewValidationContext(queryfy.Strict)
		_ = compiled.Validate(data, vctx)
		if !vctx.HasErrors() {
			validCount++
			continue
		}
		var errs []string
		for _, fe := range vctx.Errors() {
			if fe.Path == "" {
				errs = append(errs, fe.Message)
			} else {
				errs = append(errs, fmt.Sprintf("%s: %s", fe.Path, fe.Message))
			}
		}
		failures = append(failures, RowValidationFailure{ID: id, Errors: errs})
	}
	if err := rows.Err(); err != nil {
		return validCount, failures, fmt.Errorf("iterating %s: %w", nodesTable, err)
	}
	return validCount, failures, nil
}

// ─── Async job manager for strict promotion ────────────────────────────────
//
// Modeled closely on pkg/tenantexport.JobManager's own proven design
// (ticket generation, throttle-by-key, bounded concurrency semaphore)
// but NOT reused directly: tenantexport's throttle key is tenant ID
// alone (export is inherently whole-tenant, no sub-resource), while
// strict promotion needs (tenant, entity type) -- promoting "invoices"
// and "customers" for the same tenant concurrently is fine and should
// not be throttled against each other the way two overlapping exports
// of the SAME tenant should be. The result type differs too (row
// counts/failures, not a blob key). Small enough, and different
// enough in both key shape and result shape, that a second focused
// implementation was clearer than forcing tenantexport.JobManager to
// generalize over both.

type PromoteJobStatus string

const (
	PromoteJobRunning  PromoteJobStatus = "running"
	PromoteJobComplete PromoteJobStatus = "complete"
	PromoteJobFailed   PromoteJobStatus = "failed"
	// PromoteJobRejected is distinct from Failed: Failed means
	// something went wrong (a storage error, a bug); Rejected means
	// strict promotion worked exactly as designed and correctly
	// declined to promote because not every row validated -- the
	// caller's data or schema needs attention, not this code.
	PromoteJobRejected PromoteJobStatus = "rejected"
)

type PromoteResult struct {
	MigratedRows int  `json:"migrated_rows"`
	AutoInferred bool `json:"auto_inferred"`
}

type PromoteJob struct {
	Ticket     string
	TenantID   tenant.TenantID
	EntityType string
	Status     PromoteJobStatus
	Result     *PromoteResult
	Failures   []RowValidationFailure
	Error      string
	CreatedAt  time.Time
	FinishedAt time.Time
}

type promoteJobKey struct {
	tenantID   tenant.TenantID
	entityType string
}

// PromoteJobManager tracks strict-promotion jobs in memory, throttled
// per (tenant, entity type) and bounded in total concurrency -- the
// same "low priority, never competes unbounded with normal traffic"
// posture as the export job manager.
type PromoteJobManager struct {
	mu   sync.Mutex
	jobs map[string]*PromoteJob

	runningByKey map[promoteJobKey]string

	sem chan struct{}
}

func NewPromoteJobManager(maxConcurrent int) *PromoteJobManager {
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &PromoteJobManager{
		jobs:         make(map[string]*PromoteJob),
		runningByKey: make(map[promoteJobKey]string),
		sem:          make(chan struct{}, maxConcurrent),
	}
}

type ErrPromoteInFlight struct {
	ExistingTicket string
}

func (e *ErrPromoteInFlight) Error() string {
	return fmt.Sprintf("xolu: a strict promotion is already running for this entity type (ticket %s)", e.ExistingTicket)
}

// Submit starts a strict-promotion job. work is called with no
// arguments; the caller closes over whatever DB handle/schema it
// needs, keeping this manager itself free of any dependency on the
// promotion logic's own signature.
func (m *PromoteJobManager) Submit(tenantID tenant.TenantID, entityType string, work func() (*PromoteResult, []RowValidationFailure, error)) (string, error) {
	key := promoteJobKey{tenantID: tenantID, entityType: entityType}

	m.mu.Lock()
	if existing, running := m.runningByKey[key]; running {
		m.mu.Unlock()
		return "", &ErrPromoteInFlight{ExistingTicket: existing}
	}
	ticket, err := newPromoteTicket()
	if err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("xolu: generate ticket: %w", err)
	}
	job := &PromoteJob{Ticket: ticket, TenantID: tenantID, EntityType: entityType, Status: PromoteJobRunning, CreatedAt: ot.Now().Time()}
	m.jobs[ticket] = job
	m.runningByKey[key] = ticket
	m.mu.Unlock()

	go func() {
		m.sem <- struct{}{}
		defer func() { <-m.sem }()

		result, failures, err := work()

		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.runningByKey, key)
		job.FinishedAt = ot.Now().Time()
		switch {
		case err != nil:
			job.Status = PromoteJobFailed
			job.Error = err.Error()
		case len(failures) > 0:
			job.Status = PromoteJobRejected
			job.Failures = failures
		default:
			job.Status = PromoteJobComplete
			job.Result = result
		}
	}()

	return ticket, nil
}

func (m *PromoteJobManager) Status(ticket string) (*PromoteJob, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[ticket]
	if !ok {
		return nil, false
	}
	cp := *job
	return &cp, true
}

func newPromoteTicket() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "prm_" + hex.EncodeToString(b), nil
}
