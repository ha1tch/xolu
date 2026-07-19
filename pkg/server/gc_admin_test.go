// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// gc_admin_test.go
//
// Tests for S2: admin GC endpoints and gc.Worker migration.
// Covers GET /api/v1/admin/gc and POST /api/v1/admin/gc/{name}/run.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
)

// gcAdminURL returns the URL for a GC admin path on a stdTestServer.
func gcAdminURL(sts *stdTestServer, path string) string {
	return fmt.Sprintf("%s/api/v1/admin/gc%s", sts.ts.URL, path)
}

// ─── GET /api/v1/admin/gc ────────────────────────────────────────────────────

func TestGCAdmin_ListEmpty(t *testing.T) {
	// A server with no GC workers enabled returns an empty workers array.
	env := newV1OnlyServer(t)
	status, resp := doJSONRequest(t, "GET", gcAdminURL(env, ""), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /admin/gc: want 200, got %d: %v", status, resp)
	}
	workers, ok := resp["workers"].([]interface{})
	if !ok {
		t.Fatalf("workers: want array, got %T: %v", resp["workers"], resp)
	}
	if len(workers) != 0 {
		t.Errorf("no GC workers enabled: want empty array, got %d", len(workers))
	}
}

func TestGCAdmin_ListWithBlobGC(t *testing.T) {
	env := setupBlobTestServer(t, func(cfg *config.Config) {
		cfg.BlobGCEnabled = true
		cfg.BlobGCIntervalSecs = 3600
		cfg.BlobGCGracePeriodSecs = 1
	})
	t.Cleanup(env.cleanup)

	status, resp := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v1/admin/gc", env.ts.URL), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /admin/gc: want 200, got %d: %v", status, resp)
	}
	workers, _ := resp["workers"].([]interface{})
	if len(workers) == 0 {
		t.Fatalf("blob GC enabled: want >= 1 worker, got 0")
	}
	found := false
	for _, w := range workers {
		wm := w.(map[string]interface{})
		if wm["name"] == "blob-gc" {
			found = true
		}
	}
	if !found {
		t.Errorf("blob-gc worker not in list: %v", workers)
	}
}

func TestGCAdmin_ListWithTSRetention(t *testing.T) {
	env := setupTSServer(t, func(cfg *config.Config) {
		cfg.TSRetentionEnabled = true
		cfg.TSCompactionIntervalSecs = 3600
	})
	// Construct URL directly since tsURL adds tenant prefix
	status, resp := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v1/admin/gc", env.ts.URL), nil)
	if status != http.StatusOK {
		t.Fatalf("GET /admin/gc: want 200, got %d: %v", status, resp)
	}
	workers, _ := resp["workers"].([]interface{})
	found := false
	for _, w := range workers {
		wm := w.(map[string]interface{})
		if wm["name"] == "ts-retention" {
			found = true
		}
	}
	if !found {
		t.Errorf("ts-retention worker not in list: %v", workers)
	}
}

func TestGCAdmin_ListWorkerShape(t *testing.T) {
	env := setupBlobTestServer(t, func(cfg *config.Config) {
		cfg.BlobGCEnabled = true
		cfg.BlobGCIntervalSecs = 3600
	})
	t.Cleanup(env.cleanup)

	_, resp := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v1/admin/gc", env.ts.URL), nil)
	workers, _ := resp["workers"].([]interface{})
	if len(workers) == 0 {
		t.Skip("no workers in list")
	}
	w := workers[0].(map[string]interface{})
	if w["name"] == nil {
		t.Errorf("worker missing 'name' field: %v", w)
	}
	// last_report and last_swept_at are absent before first sweep — that's correct.
}

// ─── POST /api/v1/admin/gc/{name}/run ────────────────────────────────────────

func TestGCAdmin_RunNotFound(t *testing.T) {
	env := newV1OnlyServer(t)
	status, _ := doJSONRequest(t, "POST",
		gcAdminURL(env, "/nonexistent-worker/run"), nil)
	if status != http.StatusNotFound {
		t.Errorf("run unknown worker: want 404, got %d", status)
	}
}

func TestGCAdmin_RunBlobGC(t *testing.T) {
	env := setupBlobTestServer(t, func(cfg *config.Config) {
		cfg.BlobGCEnabled = true
		cfg.BlobGCIntervalSecs = 3600
	})
	t.Cleanup(env.cleanup)

	status, resp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/admin/gc/blob-gc/run", env.ts.URL), nil)
	if status != http.StatusOK {
		t.Fatalf("run blob-gc: want 200, got %d: %v", status, resp)
	}
	if resp["worker"] != "blob-gc" {
		t.Errorf("worker: want 'blob-gc', got %v", resp["worker"])
	}
	report, ok := resp["report"].(map[string]interface{})
	if !ok {
		t.Fatalf("report: want object, got %T: %v", resp["report"], resp)
	}
	for _, field := range []string{"examined", "collected", "errors", "duration_ms"} {
		if report[field] == nil {
			t.Errorf("report missing field %q: %v", field, report)
		}
	}
}

func TestGCAdmin_RunTSRetention(t *testing.T) {
	env := setupTSServer(t, func(cfg *config.Config) {
		cfg.TSRetentionEnabled = true
		cfg.TSCompactionIntervalSecs = 3600
	})
	env.registerTenant("acme")
	env.provision("acme")

	status, resp := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/admin/gc/ts-retention/run", env.ts.URL), nil)
	if status != http.StatusOK {
		t.Fatalf("run ts-retention: want 200, got %d: %v", status, resp)
	}
	if resp["worker"] != "ts-retention" {
		t.Errorf("worker: want 'ts-retention', got %v", resp["worker"])
	}
	report, ok := resp["report"].(map[string]interface{})
	if !ok {
		t.Fatalf("report: want object, got %T", resp["report"])
	}
	if report["duration_ms"] == nil {
		t.Errorf("report missing duration_ms: %v", report)
	}
}

func TestGCAdmin_RunUpdatesLastReport(t *testing.T) {
	env := setupBlobTestServer(t, func(cfg *config.Config) {
		cfg.BlobGCEnabled = true
		cfg.BlobGCIntervalSecs = 3600
	})
	t.Cleanup(env.cleanup)

	// Before run: last_report should be absent.
	_, before := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v1/admin/gc", env.ts.URL), nil)
	workers, _ := before["workers"].([]interface{})
	for _, w := range workers {
		wm := w.(map[string]interface{})
		if wm["name"] == "blob-gc" && wm["last_report"] != nil {
			t.Error("last_report should be absent before first run")
		}
	}

	// Trigger run.
	doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/admin/gc/blob-gc/run", env.ts.URL), nil)

	// After run: last_report and last_swept_at should be present.
	_, after := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v1/admin/gc", env.ts.URL), nil)
	workers2, _ := after["workers"].([]interface{})
	found := false
	for _, w := range workers2 {
		wm := w.(map[string]interface{})
		if wm["name"] == "blob-gc" {
			found = true
			if wm["last_report"] == nil {
				t.Error("last_report should be present after run")
			}
			if wm["last_swept_at"] == nil {
				t.Error("last_swept_at should be present after run")
			}
		}
	}
	if !found {
		t.Error("blob-gc worker not found in list after run")
	}
}

// ─── Migration smoke test — Stop() still works via gc.Worker ──────────────────

func TestGCAdmin_WorkersStopCleanly(t *testing.T) {
	// Both tsRetention and blobGC are now *gc.Worker; Stop() must not panic.
	env := setupBlobTestServer(t, func(cfg *config.Config) {
		cfg.BlobGCEnabled = true
		cfg.BlobGCIntervalSecs = 3600
		cfg.TimeseriesEnabled = true
		cfg.TSRetentionEnabled = true
		cfg.TSCompactionIntervalSecs = 3600
	})
	t.Cleanup(env.cleanup)
	env.provisionTS(t)
	env.srv.Stop()
	// Second Stop via t.Cleanup — must be idempotent.
}
