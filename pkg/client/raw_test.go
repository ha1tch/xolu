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
	"testing"
)

func TestRawHappyPath_WithBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/bal/def" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method: got %s", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type default: got %q", got)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"account_id":"acct"}` {
			t.Errorf("body: got %q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"account_id":"acct","postable":true}`))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.Raw(context.Background(), http.MethodPost, "/api/v1/bal/def", "",
		strings.NewReader(`{"account_id":"acct"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StatusCode != http.StatusCreated {
		t.Errorf("StatusCode: got %d", result.StatusCode)
	}
	if !strings.Contains(string(result.Body), `"acct"`) {
		t.Errorf("Body: got %q", result.Body)
	}
}

func TestRawGET_NoBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method: got %s", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "" {
			t.Errorf("Content-Type: got %q, want empty when there's no body", got)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	c := New(server.URL)
	result, err := c.Raw(context.Background(), http.MethodGet, "/health", "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StatusCode != http.StatusOK {
		t.Errorf("StatusCode: got %d", result.StatusCode)
	}
}

func TestRawCustomContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "text/csv" {
			t.Errorf("Content-Type: got %q, want text/csv", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := New(server.URL)
	if _, err := c.Raw(context.Background(), http.MethodPost, "/whatever", "text/csv", strings.NewReader("a,b,c")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRawErrorStatusPassedThroughUnfiltered(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"code":"XOLU-ST001","message":"bad request","status":400}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	// A 4xx must NOT come back as a Go error -- that's the entire
	// point of Raw. The caller inspects StatusCode themselves.
	result, err := c.Raw(context.Background(), http.MethodPost, "/api/v1/whatever", "", strings.NewReader("{}"))
	if err != nil {
		t.Fatalf("expected nil error for a 4xx response (Raw does not decode errors), got: %v", err)
	}
	if result.StatusCode != http.StatusBadRequest {
		t.Errorf("StatusCode: got %d", result.StatusCode)
	}
	if !strings.Contains(string(result.Body), "XOLU-ST001") {
		t.Errorf("Body should contain the raw, undecoded error JSON: got %q", result.Body)
	}
}

func TestRawIgnoresConfiguredTenant(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/whatever" {
			t.Errorf("path: got %s, want the exact path passed in, no tenant prefix inserted", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := New(server.URL, WithTenant("some-tenant"))
	if _, err := c.Raw(context.Background(), http.MethodGet, "/api/v1/whatever", "", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRawRejectsPathWithoutLeadingSlash(t *testing.T) {
	c := New("http://example.com")
	_, err := c.Raw(context.Background(), http.MethodGet, "api/v1/whatever", "", nil)
	if err == nil {
		t.Fatal("expected an error for a path not starting with /")
	}
}

func TestRawAppliesAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization: got %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := New(server.URL, WithAPIKey("test-key"))
	if _, err := c.Raw(context.Background(), http.MethodGet, "/api/v1/whatever", "", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
