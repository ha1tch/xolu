// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// m1_list_test.go — M1 (molu readiness): ListEntityTypes (T-24) and
// ListSequences (T-25). Same convention as the Stage 2 tests: happy path,
// structured error, and shape verification per method.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ─── ListEntityTypes ────────────────────────────────────────────────────────

func TestListEntityTypesHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/schemas" {
			t.Errorf("expected /api/v1/schemas, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"schemas":[{"name":"asset"},{"name":"widget"}],"count":2}`))
	}))
	defer server.Close()

	c := New(server.URL)
	types, err := c.ListEntityTypes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 2 {
		t.Fatalf("expected 2 entity types, got %d", len(types))
	}
	if types[0].Name != "asset" || types[1].Name != "widget" {
		t.Errorf("expected [asset widget], got [%s %s]", types[0].Name, types[1].Name)
	}
}

func TestListEntityTypesEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"schemas":[],"count":0}`))
	}))
	defer server.Close()

	c := New(server.URL)
	types, err := c.ListEntityTypes(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if types == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(types) != 0 {
		t.Errorf("expected empty, got %v", types)
	}
}

func TestListEntityTypesStructuredError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"code":"XOLU-ST001","message":"storage failed","status":500}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.ListEntityTypes(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	ce, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if ce.Code != "XOLU-ST001" {
		t.Errorf("expected code XOLU-ST001, got %s", ce.Code)
	}
	if ce.HTTPStatus != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", ce.HTTPStatus)
	}
}

// ─── ListSequences ──────────────────────────────────────────────────────────

func TestListSequencesHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/gen/seq" {
			t.Errorf("expected /api/v2/gen/seq, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"sequences":[
			{"name":"invoices","current":10,"increment_by":2,"cycle":false},
			{"name":"orders","current":42,"increment_by":1,"cycle":true}
		],"count":2}`))
	}))
	defer server.Close()

	c := New(server.URL)
	seqs, err := c.ListSequences(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seqs) != 2 {
		t.Fatalf("expected 2 sequences, got %d", len(seqs))
	}
	if seqs[0].Name != "invoices" || seqs[0].Current != 10 ||
		seqs[0].IncrementBy != 2 || seqs[0].Cycle {
		t.Errorf("invoices: unexpected shape %+v", seqs[0])
	}
	if seqs[1].Name != "orders" || seqs[1].Current != 42 ||
		seqs[1].IncrementBy != 1 || !seqs[1].Cycle {
		t.Errorf("orders: unexpected shape %+v", seqs[1])
	}
}

func TestListSequencesEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"sequences":[],"count":0}`))
	}))
	defer server.Close()

	c := New(server.URL)
	seqs, err := c.ListSequences(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if seqs == nil {
		t.Fatal("expected non-nil empty slice, got nil")
	}
	if len(seqs) != 0 {
		t.Errorf("expected empty, got %v", seqs)
	}
}

func TestListSequencesStructuredError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":{"code":"XOLU-ST001","message":"storage failed","status":500}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.ListSequences(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	ce, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if ce.Code != "XOLU-ST001" {
		t.Errorf("expected code XOLU-ST001, got %s", ce.Code)
	}
}
