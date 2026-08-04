// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestBlobExportStartHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/blob/export" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("method: got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{"ticket":"exp_abc123","status":"running"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	job, err := c.BlobExportStart(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.Ticket != "exp_abc123" {
		t.Errorf("Ticket: got %q", job.Ticket)
	}
	if job.Status != BlobExportRunning {
		t.Errorf("Status: got %q", job.Status)
	}
}

func TestBlobExportStartConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":{"code":"XOLU-BL008","message":"An export is already running for this tenant (ticket exp_existing)","status":409}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.BlobExportStart(context.Background())
	xoluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusConflict {
		t.Errorf("HTTPStatus: got %d", xoluErr.HTTPStatus)
	}
	if !strings.Contains(xoluErr.Message, "exp_existing") {
		t.Errorf("expected the existing ticket in the error message, got %q", xoluErr.Message)
	}
}

func TestBlobExportStatusHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/blob/export/exp_abc123" {
			t.Errorf("path: got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ticket":"exp_abc123","status":"complete","blob_key":"export-5.zip"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	job, err := c.BlobExportStatus(context.Background(), "exp_abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.Status != BlobExportComplete {
		t.Errorf("Status: got %q", job.Status)
	}
	if job.BlobKey != "export-5.zip" {
		t.Errorf("BlobKey: got %q", job.BlobKey)
	}
}

func TestBlobExportStatusNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-BL007","message":"Export ticket not found","status":404}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.BlobExportStatus(context.Background(), "exp_doesnotexist")
	xoluErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if xoluErr.HTTPStatus != http.StatusNotFound {
		t.Errorf("HTTPStatus: got %d", xoluErr.HTTPStatus)
	}
}

func TestBlobExportStatusEmptyTicketRejectedClientSide(t *testing.T) {
	c := New("http://example.com")
	_, err := c.BlobExportStatus(context.Background(), "")
	if err == nil {
		t.Fatal("expected an error for an empty ticket")
	}
}

// TestExport_FullFlow proves the convenience wrapper actually drives
// the whole sequence: start, poll through a "running" response, then
// on "complete" download via BlobGet and stream it into the caller's
// writer -- not just that each underlying call works in isolation.
func TestExport_FullFlow(t *testing.T) {
	originalInterval := blobExportPollInterval
	blobExportPollInterval = 10 * time.Millisecond
	defer func() { blobExportPollInterval = originalInterval }()

	var statusCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/blob/export":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"ticket":"exp_flow","status":"running"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/blob/export/exp_flow":
			n := atomic.AddInt32(&statusCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			if n < 2 {
				w.Write([]byte(`{"ticket":"exp_flow","status":"running"}`))
			} else {
				w.Write([]byte(`{"ticket":"exp_flow","status":"complete","blob_key":"export-9.zip"}`))
			}
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/blob/export-9.zip":
			w.Header().Set("Content-Type", "application/zip")
			w.Header().Set("X-Blob-SHA256", "deadbeef")
			w.Header().Set("X-Blob-Size", "9")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("zip-bytes"))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := New(server.URL)
	var buf bytes.Buffer

	// This test can't wait through the real 2s poll interval, so it
	// runs Export in a goroutine and just confirms it completes
	// correctly within a generous deadline -- the fixed interval
	// itself isn't under test here, the sequencing is.
	done := make(chan struct{})
	var result *ExportResult
	var exportErr error
	go func() {
		result, exportErr = c.Export(context.Background(), &buf)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Export did not complete within the test deadline")
	}

	if exportErr != nil {
		t.Fatalf("unexpected error: %v", exportErr)
	}
	if buf.String() != "zip-bytes" {
		t.Errorf("streamed content: got %q", buf.String())
	}
	if result.BlobKey != "export-9.zip" {
		t.Errorf("BlobKey: got %q", result.BlobKey)
	}
	if result.SHA256 != "deadbeef" {
		t.Errorf("SHA256: got %q", result.SHA256)
	}
	if atomic.LoadInt32(&statusCalls) < 2 {
		t.Errorf("expected at least 2 status polls (one running, one complete), got %d", statusCalls)
	}
}

func TestExport_JobFailure(t *testing.T) {
	originalInterval := blobExportPollInterval
	blobExportPollInterval = 10 * time.Millisecond
	defer func() { blobExportPollInterval = originalInterval }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"ticket":"exp_fail","status":"running"}`))
		case http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ticket":"exp_fail","status":"failed","error":"disk full"}`))
		}
	}))
	defer server.Close()

	c := New(server.URL)
	var buf bytes.Buffer

	done := make(chan struct{})
	var exportErr error
	go func() {
		_, exportErr = c.Export(context.Background(), &buf)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Export did not complete within the test deadline")
	}

	if exportErr == nil {
		t.Fatal("expected an error for a failed job")
	}
	if !strings.Contains(exportErr.Error(), "disk full") {
		t.Errorf("expected the job's own failure reason in the error, got: %v", exportErr)
	}
	if buf.Len() != 0 {
		t.Errorf("buffer should be untouched on a failed job, got %d bytes", buf.Len())
	}
}

func TestExport_ContextCancelledDuringPoll(t *testing.T) {
	// Explicit, not inherited from another test's cleanup: this test's
	// own correctness depends on ctx's deadline firing BEFORE the poll
	// interval, so it sets an interval long enough to guarantee that
	// regardless of what any other test left behind.
	originalInterval := blobExportPollInterval
	blobExportPollInterval = 5 * time.Second
	defer func() { blobExportPollInterval = originalInterval }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"ticket":"exp_slow","status":"running"}`))
		case http.MethodGet:
			// Always "running" -- this job never completes, forcing
			// Export into its poll loop until the context cancels it.
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"ticket":"exp_slow","status":"running"}`))
		}
	}))
	defer server.Close()

	c := New(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	var buf bytes.Buffer
	_, err := c.Export(ctx, &buf)
	if err == nil {
		t.Fatal("expected a context deadline error")
	}
}
