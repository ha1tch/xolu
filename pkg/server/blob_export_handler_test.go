// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/config"
)

func TestIntegration_BlobExport_FullAsyncFlow(t *testing.T) {
	ts := setupBlobTestServer(t, func(cfg *config.Config) {
		cfg.BalEnabled = true
	})
	defer ts.cleanup()

	// Seed a real entity so the export has real data to carry.
	entityResp := ts.blobDo("POST", "/api/v1/widget", []byte(`{"name":"probe"}`), map[string]string{
		"Content-Type": "application/json",
	})
	if entityResp.StatusCode != http.StatusCreated {
		t.Fatalf("seed entity: status %d", entityResp.StatusCode)
	}
	entityResp.Body.Close()

	// Start the export.
	startResp := ts.blobDo("POST", "/api/v1/blob/export", nil, nil)
	defer startResp.Body.Close()
	if startResp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(startResp.Body)
		t.Fatalf("start export: status %d, body=%s", startResp.StatusCode, body)
	}
	var startBody struct {
		Ticket string `json:"ticket"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(startResp.Body).Decode(&startBody); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if startBody.Ticket == "" {
		t.Fatal("empty ticket")
	}
	if startBody.Status != "running" {
		t.Errorf("initial status: got %q, want running", startBody.Status)
	}

	// A second export for the same (default) tenant while the first
	// may still be running should either be rejected (409) or, if the
	// first already finished by the time this fires, succeed -- this
	// test doesn't assert timing, only that a 409 (if it happens)
	// carries the right shape.
	secondResp := ts.blobDo("POST", "/api/v1/blob/export", nil, nil)
	secondBody, _ := io.ReadAll(secondResp.Body)
	secondResp.Body.Close()
	if secondResp.StatusCode == http.StatusConflict {
		var errBody map[string]interface{}
		if err := json.Unmarshal(secondBody, &errBody); err != nil {
			t.Errorf("409 response body is not valid JSON: %v (body=%s)", err, secondBody)
		}
	}

	// Poll status to completion.
	var finalStatus string
	var blobKey string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		statusResp := ts.blobDo("GET", "/api/v1/blob/export/"+startBody.Ticket, nil, nil)
		var statusBody struct {
			Ticket  string `json:"ticket"`
			Status  string `json:"status"`
			BlobKey string `json:"blob_key"`
			Error   string `json:"error"`
		}
		if err := json.NewDecoder(statusResp.Body).Decode(&statusBody); err != nil {
			statusResp.Body.Close()
			t.Fatalf("decode status response: %v", err)
		}
		statusResp.Body.Close()

		if statusBody.Status == "complete" {
			finalStatus = statusBody.Status
			blobKey = statusBody.BlobKey
			break
		}
		if statusBody.Status == "failed" {
			t.Fatalf("export failed: %s", statusBody.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if finalStatus != "complete" {
		t.Fatal("export did not complete within the deadline")
	}
	if blobKey == "" {
		t.Fatal("completed export has no blob_key")
	}

	// Retrieve the export through the EXISTING, already-tested BlobGet
	// mechanism (T-142) -- proving the ticket -> blob handoff is real,
	// not just that the job manager reports "complete".
	getResp := ts.blobDo("GET", "/api/v1/blob/"+blobKey, nil, nil)
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("GET the exported blob: status %d", getResp.StatusCode)
	}
	zipBytes, err := io.ReadAll(getResp.Body)
	if err != nil {
		t.Fatalf("reading exported blob: %v", err)
	}
	if len(zipBytes) == 0 {
		t.Fatal("exported blob is empty")
	}

	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("exported blob is not a valid zip: %v", err)
	}
	if len(zr.File) == 0 {
		t.Fatal("exported zip has no files at all")
	}

	// nodes.json specifically must contain the seeded widget entity.
	nodesFile, err := zr.Open("nodes.json")
	if err != nil {
		t.Fatalf("zip has no nodes.json: %v (files present: %v)", err, zipFileNames(zr))
	}
	nodesBytes, err := io.ReadAll(nodesFile)
	nodesFile.Close()
	if err != nil {
		t.Fatalf("reading nodes.json: %v", err)
	}
	if !bytes.Contains(nodesBytes, []byte("probe")) {
		t.Errorf("nodes.json does not contain the seeded entity's data: %s", nodesBytes)
	}
}

func zipFileNames(zr *zip.Reader) []string {
	names := make([]string, len(zr.File))
	for i, f := range zr.File {
		names[i] = f.Name
	}
	return names
}

func TestIntegration_BlobExport_TicketNotFound(t *testing.T) {
	ts := setupBlobTestServer(t)
	defer ts.cleanup()

	resp := ts.blobDo("GET", "/api/v1/blob/export/exp_doesnotexist", nil, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}

func TestIntegration_BlobExport_DisabledWhenBlobDisabled(t *testing.T) {
	// The standard (non-blob) test server never enables blob -- export
	// must fail the same way any other blob route does, not with some
	// different, export-specific error.
	tsServer := setupTestServer(t)
	defer tsServer.cleanup()

	resp, _ := tsServer.doRequest("POST", "/api/v1/blob/export", nil)
	assertStatus(t, resp, http.StatusNotImplemented)
}

// TestIntegration_BlobExportSweep_RealServerTicker boots a real server
// with the export sweep enabled at a short interval and a TTL of 0
// (immediately expired), performs a real export via the HTTP API, and
// waits for the ACTUAL background ticker (not a direct Sweep() call)
// to remove it -- proving the config fields
// (BlobExportSweepEnabled/IntervalSecs/TTLSecs) actually flow through
// server.go's own startup wiring into a running worker, not just that
// the underlying sweep logic is correct in isolation.
func TestIntegration_BlobExportSweep_RealServerTicker(t *testing.T) {
	ts := setupBlobTestServer(t, func(cfg *config.Config) {
		cfg.BalEnabled = true
		cfg.BlobExportSweepEnabled = true
		cfg.BlobExportSweepIntervalSecs = 1
		cfg.BlobExportTTLSecs = 0
	})
	defer ts.cleanup()

	startResp := ts.blobDo("POST", "/api/v1/blob/export", nil, nil)
	var startBody struct {
		Ticket string `json:"ticket"`
	}
	if err := json.NewDecoder(startResp.Body).Decode(&startBody); err != nil {
		startResp.Body.Close()
		t.Fatalf("decode start response: %v", err)
	}
	startResp.Body.Close()

	var blobKey string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		statusResp := ts.blobDo("GET", "/api/v1/blob/export/"+startBody.Ticket, nil, nil)
		var statusBody struct {
			Status  string `json:"status"`
			BlobKey string `json:"blob_key"`
		}
		json.NewDecoder(statusResp.Body).Decode(&statusBody)
		statusResp.Body.Close()
		if statusBody.Status == "complete" {
			blobKey = statusBody.BlobKey
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if blobKey == "" {
		t.Fatal("export did not complete")
	}

	// Confirm it exists right after completion.
	getResp := ts.blobDo("GET", "/api/v1/blob/"+blobKey, nil, nil)
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("export blob should exist immediately after completion: status %d", getResp.StatusCode)
	}

	// Wait for the real ticker (1s interval, TTL 0) to sweep it away --
	// generous margin over the 1s interval for scheduling variance.
	deadline = time.Now().Add(4 * time.Second)
	swept := false
	for time.Now().Before(deadline) {
		checkResp := ts.blobDo("GET", "/api/v1/blob/"+blobKey, nil, nil)
		checkResp.Body.Close()
		if checkResp.StatusCode == http.StatusNotFound {
			swept = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !swept {
		t.Fatal("export blob was not swept by the real background ticker within the deadline")
	}
}
