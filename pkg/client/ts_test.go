// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// ts_test.go — mock-based tests per the Stage 2 convention: happy
// path, structured error, and client-side validation per method. The
// real-server round trip lives in integration_test.go's
// TestIntegration_TSReadSlice, which is what catches wire-shape drift
// these mocks structurally cannot -- every field type here was
// checked against the real server handlers directly before writing
// (int64 IDs, uint64 request timeline, etc.), not assumed from
// xoluman's own proposed signatures.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var tsT0 = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// ─── TSListTimelines ────────────────────────────────────────────────────────

func TestTSListTimelinesHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ts/tl/list" {
			t.Errorf("expected /api/v1/ts/tl/list, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"id":1,"name":"cpu","dims":1,"retention_days":30,"created_at":"2026-06-15T12:00:00Z"}]`))
	}))
	defer server.Close()

	c := New(server.URL)
	tls, err := c.TSListTimelines(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tls) != 1 || tls[0].ID != 1 || tls[0].Name != "cpu" {
		t.Errorf("unexpected timelines: %+v", tls)
	}
}

func TestTSListTimelinesEmptyResultNotNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[]`))
	}))
	defer server.Close()

	c := New(server.URL)
	tls, err := c.TSListTimelines(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tls == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(tls) != 0 {
		t.Errorf("expected empty, got %+v", tls)
	}
}

// ─── TSGetTimeline ──────────────────────────────────────────────────────────

func TestTSGetTimelineHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ts/tl/1" {
			t.Errorf("expected /api/v1/ts/tl/1, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":1,"name":"cpu","dims":1,"retention_days":30,"created_at":"2026-06-15T12:00:00Z"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	tl, err := c.TSGetTimeline(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tl.ID != 1 || tl.Name != "cpu" {
		t.Errorf("unexpected timeline: %+v", tl)
	}
}

func TestTSGetTimelineNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-TS004","message":"timeline 99 not defined"}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.TSGetTimeline(context.Background(), 99)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestTSGetTimelineRequiresPositiveID(t *testing.T) {
	c := New("http://unused")
	if _, err := c.TSGetTimeline(context.Background(), 0); err == nil {
		t.Error("expected error for timelineID=0")
	}
	if _, err := c.TSGetTimeline(context.Background(), -1); err == nil {
		t.Error("expected error for negative timelineID")
	}
}

// ─── TSQueryRange ───────────────────────────────────────────────────────────

func TestTSQueryRangeHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ts/query/range" {
			t.Errorf("expected /api/v1/ts/query/range, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"count":1,"events":[{"timeline":1,"dims":[7],"time":"2026-06-15T12:00:00Z","nums":[1.5]}]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.TSQueryRange(context.Background(), TSQueryRangeRequest{
		Timeline: 1, Dims: []uint64{7}, From: tsT0.Add(-time.Hour), To: tsT0,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Count != 1 || len(res.Events) != 1 || res.Events[0].Nums[0] != 1.5 {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestTSQueryRangeRangeTooWide(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"code":"XOLU-TS011","message":"query range exceeds 90 days"}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.TSQueryRange(context.Background(), TSQueryRangeRequest{
		Timeline: 1, Dims: []uint64{7}, From: tsT0.AddDate(-1, 0, 0), To: tsT0,
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestTSQueryRangeRequiresFields(t *testing.T) {
	c := New("http://unused")
	ctx := context.Background()

	if _, err := c.TSQueryRange(ctx, TSQueryRangeRequest{Dims: []uint64{7}, From: tsT0.Add(-time.Hour), To: tsT0}); err == nil {
		t.Error("expected error for missing Timeline")
	}
	if _, err := c.TSQueryRange(ctx, TSQueryRangeRequest{Timeline: 1, From: tsT0.Add(-time.Hour), To: tsT0}); err == nil {
		t.Error("expected error for empty Dims")
	}
	if _, err := c.TSQueryRange(ctx, TSQueryRangeRequest{Timeline: 1, Dims: []uint64{7}, To: tsT0}); err == nil {
		t.Error("expected error for zero-value From")
	}
	if _, err := c.TSQueryRange(ctx, TSQueryRangeRequest{Timeline: 1, Dims: []uint64{7}, From: tsT0}); err == nil {
		t.Error("expected error for zero-value To")
	}
}

// ─── TSRollupList ───────────────────────────────────────────────────────────

func TestTSRollupListHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ts/tl/1/rollup/list" {
			t.Errorf("expected /api/v1/ts/tl/1/rollup/list, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`[{"id":"r1","source_tid":1,"dest_tid":2,"bucket_duration":"1h0m0s","running":true,"created_at":"2026-06-15T12:00:00Z"}]`))
	}))
	defer server.Close()

	c := New(server.URL)
	rollups, err := c.TSRollupList(context.Background(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rollups) != 1 || rollups[0].ID != "r1" || rollups[0].DestTID != 2 {
		t.Errorf("unexpected rollups: %+v", rollups)
	}
}

func TestTSRollupListRequiresPositiveID(t *testing.T) {
	c := New("http://unused")
	if _, err := c.TSRollupList(context.Background(), 0); err == nil {
		t.Error("expected error for timelineID=0")
	}
}

// ─── TSRollupGet ────────────────────────────────────────────────────────────

func TestTSRollupGetHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ts/tl/1/rollup/r1" {
			t.Errorf("expected /api/v1/ts/tl/1/rollup/r1, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"id":"r1","source_tid":1,"dest_tid":2,"bucket_duration":"1h0m0s","running":true,"created_at":"2026-06-15T12:00:00Z"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	rollup, err := c.TSRollupGet(context.Background(), 1, "r1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rollup.ID != "r1" || rollup.SourceTID != 1 {
		t.Errorf("unexpected rollup: %+v", rollup)
	}
}

func TestTSRollupGetNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-TS025","message":"rollup not found"}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.TSRollupGet(context.Background(), 1, "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestTSRollupGetRequiresFields(t *testing.T) {
	c := New("http://unused")
	if _, err := c.TSRollupGet(context.Background(), 0, "r1"); err == nil {
		t.Error("expected error for timelineID=0")
	}
	if _, err := c.TSRollupGet(context.Background(), 1, ""); err == nil {
		t.Error("expected error for empty rollupID")
	}
}
