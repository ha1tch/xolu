// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ─── GetSchemaSuggestion ────────────────────────────────────────────────────

func TestGetSchemaSuggestionHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/entity/widgets/schema-suggestion" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("method: got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"entity_type":"widgets","sampled_rows":10,"total_rows":10,
			"suggested_schema":{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]},
			"field_analysis":[{"field":"name","inferred_type":"string","coverage":1,"confidence":"high"}]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	s, err := c.GetSchemaSuggestion(context.Background(), "widgets")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.EntityType != "widgets" || s.SampledRows != 10 || s.TotalRows != 10 {
		t.Errorf("got %+v", s)
	}
	if len(s.FieldAnalysis) != 1 || s.FieldAnalysis[0].Field != "name" {
		t.Errorf("FieldAnalysis: got %+v", s.FieldAnalysis)
	}
}

func TestGetSchemaSuggestionInvalidName(t *testing.T) {
	c := New("http://example.com")
	_, err := c.GetSchemaSuggestion(context.Background(), "123bad")
	if err == nil {
		t.Fatal("expected a validation error for an invalid entity name")
	}
}

func TestGetSchemaSuggestionNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-ST003","message":"no data","status":404}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.GetSchemaSuggestion(context.Background(), "nothing")
	xoluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusNotFound {
		t.Errorf("HTTPStatus: got %d", xoluErr.HTTPStatus)
	}
}

// ─── PromoteFlex ─────────────────────────────────────────────────────────────

func TestPromoteFlexAutoInferred(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/entities/promote/flex/widgets" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "null" {
			t.Errorf("expected a literal null body for a nil schema (Go's typed-nil-in-interface, marshaled), got %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"message":"ok","auto_inferred":true,"schema":{"type":"object"}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.PromoteFlex(context.Background(), "widgets", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.AutoInferred {
		t.Error("AutoInferred: got false, want true")
	}
}

func TestPromoteFlexExplicitSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "sku") {
			t.Errorf("expected the explicit schema in the body, got %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"message":"ok","auto_inferred":false,"schema":{"type":"object"}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"sku": map[string]interface{}{"type": "string"}},
	}
	result, err := c.PromoteFlex(context.Background(), "parts", schema)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.AutoInferred {
		t.Error("AutoInferred: got true, want false")
	}
}

func TestPromoteFlexWarningSurfaced(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"message":"ok","auto_inferred":true,"schema":{"type":"object"},"warning":"3 pre-existing row(s) were NOT migrated"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.PromoteFlex(context.Background(), "widgets", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Warning == "" {
		t.Error("expected Warning to be populated")
	}
}

// ─── PromoteStrictStart / PromoteStrictStatus ──────────────────────────────

func TestPromoteStrictStartHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/entities/promote/strict/widgets" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"ticket":"prm_abc","status":"running"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	job, err := c.PromoteStrictStart(context.Background(), "widgets", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.Ticket != "prm_abc" || job.Status != PromoteJobRunning {
		t.Errorf("got %+v", job)
	}
}

func TestPromoteStrictStartConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":{"code":"XOLU-ST010","message":"A strict promotion is already running for this entity type (ticket prm_existing)","status":409}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.PromoteStrictStart(context.Background(), "widgets", nil)
	xoluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusConflict {
		t.Errorf("HTTPStatus: got %d", xoluErr.HTTPStatus)
	}
	if !strings.Contains(xoluErr.Message, "prm_existing") {
		t.Errorf("expected the existing ticket in the error message, got %q", xoluErr.Message)
	}
}

func TestPromoteStrictStatusComplete(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/entities/promote/status/prm_abc" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ticket":"prm_abc","entity_type":"widgets","status":"complete","result":{"migrated_rows":42,"auto_inferred":true}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	job, err := c.PromoteStrictStatus(context.Background(), "prm_abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.Status != PromoteJobComplete {
		t.Errorf("Status: got %q", job.Status)
	}
	if job.Result == nil || job.Result.MigratedRows != 42 {
		t.Errorf("Result: got %+v", job.Result)
	}
}

func TestPromoteStrictStatusRejected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ticket":"prm_abc","entity_type":"messy","status":"rejected",
			"failures":[{"id":2,"errors":["val: expected string, got float64"]}]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	job, err := c.PromoteStrictStatus(context.Background(), "prm_abc")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.Status != PromoteJobRejected {
		t.Errorf("Status: got %q", job.Status)
	}
	if len(job.Failures) != 1 || job.Failures[0].ID != 2 {
		t.Errorf("Failures: got %+v", job.Failures)
	}
}

func TestPromoteStrictStatusEmptyTicketRejectedClientSide(t *testing.T) {
	c := New("http://example.com")
	_, err := c.PromoteStrictStatus(context.Background(), "")
	if err == nil {
		t.Fatal("expected an error for an empty ticket")
	}
}

// ─── PromoteStrict convenience wrapper ──────────────────────────────────────

func TestPromoteStrict_FullFlow_Complete(t *testing.T) {
	originalInterval := promoteStrictPollInterval
	promoteStrictPollInterval = 10 * time.Millisecond
	defer func() { promoteStrictPollInterval = originalInterval }()

	var statusCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"ticket":"prm_flow","status":"running"}`))
		case r.Method == http.MethodGet:
			n := atomic.AddInt32(&statusCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if n < 2 {
				w.Write([]byte(`{"ticket":"prm_flow","status":"running"}`))
			} else {
				w.Write([]byte(`{"ticket":"prm_flow","status":"complete","result":{"migrated_rows":7,"auto_inferred":true}}`))
			}
		}
	}))
	defer server.Close()

	c := New(server.URL)
	job, err := c.PromoteStrict(context.Background(), "widgets", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.Status != PromoteJobComplete {
		t.Errorf("Status: got %q", job.Status)
	}
	if job.Result.MigratedRows != 7 {
		t.Errorf("MigratedRows: got %d", job.Result.MigratedRows)
	}
	if atomic.LoadInt32(&statusCalls) < 2 {
		t.Errorf("expected at least 2 status polls, got %d", statusCalls)
	}
}

func TestPromoteStrict_FullFlow_Rejected_NoError(t *testing.T) {
	originalInterval := promoteStrictPollInterval
	promoteStrictPollInterval = 10 * time.Millisecond
	defer func() { promoteStrictPollInterval = originalInterval }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"ticket":"prm_rej","status":"running"}`))
		case r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ticket":"prm_rej","status":"rejected","failures":[{"id":1,"errors":["bad"]}]}`))
		}
	}))
	defer server.Close()

	c := New(server.URL)
	job, err := c.PromoteStrict(context.Background(), "messy", nil)
	// Rejection is a normal, successful outcome of the check -- must
	// NOT be surfaced as a Go error.
	if err != nil {
		t.Fatalf("rejection should not be a Go error, got: %v", err)
	}
	if job.Status != PromoteJobRejected {
		t.Errorf("Status: got %q", job.Status)
	}
	if len(job.Failures) != 1 {
		t.Errorf("Failures: got %+v", job.Failures)
	}
}

func TestPromoteStrict_FullFlow_Failed_IsError(t *testing.T) {
	originalInterval := promoteStrictPollInterval
	promoteStrictPollInterval = 10 * time.Millisecond
	defer func() { promoteStrictPollInterval = originalInterval }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"ticket":"prm_fail","status":"running"}`))
		case r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ticket":"prm_fail","status":"failed","error":"disk full"}`))
		}
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.PromoteStrict(context.Background(), "widgets", nil)
	if err == nil {
		t.Fatal("expected an error for a genuinely failed job")
	}
	if !strings.Contains(err.Error(), "disk full") {
		t.Errorf("expected the job's own failure reason in the error, got: %v", err)
	}
}

func TestPromoteStrict_ContextCancelledDuringPoll(t *testing.T) {
	originalInterval := promoteStrictPollInterval
	promoteStrictPollInterval = 5 * time.Second
	defer func() { promoteStrictPollInterval = originalInterval }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"ticket":"prm_slow","status":"running"}`))
		case r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ticket":"prm_slow","status":"running"}`))
		}
	}))
	defer server.Close()

	c := New(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	_, err := c.PromoteStrict(ctx, "widgets", nil)
	if err == nil {
		t.Fatal("expected a context deadline error")
	}
}
