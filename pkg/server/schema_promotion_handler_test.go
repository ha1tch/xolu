// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/server"
	"github.com/ha1tch/xolu/pkg/tenant"
)

func pollPromoteStatus(t *testing.T, ts *TestServer, ticket string, timeout time.Duration) map[string]interface{} {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, body := ts.doRequest("GET", "/api/v1/entities/promote/status/"+ticket, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("poll status: %d: %s", resp.StatusCode, body)
		}
		var out map[string]interface{}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("unmarshal status: %v (body=%s)", err, body)
		}
		if out["status"] != "running" {
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("ticket %s did not leave 'running' within %v", ticket, timeout)
	return nil
}

// ─── flex ───────────────────────────────────────────────────────────────────

func TestPromoteFlex_AutoInferred(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	ts.doRequest("POST", "/api/v1/gadgets", map[string]interface{}{"name": "widget-1"})
	ts.doRequest("POST", "/api/v1/gadgets", map[string]interface{}{"name": "widget-2"})

	resp, body := ts.doRequest("POST", "/api/v1/entities/promote/flex/gadgets", nil)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var out map[string]interface{}
	json.Unmarshal(body, &out)
	if out["auto_inferred"] != true {
		t.Errorf("auto_inferred: got %v, want true", out["auto_inferred"])
	}
	if out["warning"] == nil {
		t.Error("expected a warning about un-migrated pre-existing rows")
	}

	entResp, entBody := ts.doRequest("GET", "/api/v1/entities", nil)
	if entResp.StatusCode != http.StatusOK {
		t.Fatalf("entities: %d: %s", entResp.StatusCode, entBody)
	}
	var entOut entityListResponse
	json.Unmarshal(entBody, &entOut)
	g := findEntity(&entOut, "gadgets")
	if g == nil || !g.Adapted || !g.HasSchema {
		t.Errorf("gadgets should be adapted+has_schema after flex: got %+v", g)
	}
	if g.Count != 0 {
		t.Errorf("flex must NOT migrate existing rows -- adapted table count should be 0, got %d", g.Count)
	}
}

func TestPromoteFlex_ExplicitSchema(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"sku": map[string]interface{}{"type": "string"}},
		"required":   []string{"sku"},
	}
	schemaBytes, _ := json.Marshal(schema)
	req, _ := http.NewRequest("POST", ts.ts.URL+"/api/v1/entities/promote/flex/parts", bytes.NewReader(schemaBytes))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	if out["auto_inferred"] != false {
		t.Errorf("auto_inferred: got %v, want false (explicit schema given)", out["auto_inferred"])
	}
	if out["warning"] != nil {
		t.Errorf("no pre-existing rows -- expected no warning, got %v", out["warning"])
	}
}

func TestPromoteFlex_NoDataNotFound(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	resp, body := ts.doRequest("POST", "/api/v1/entities/promote/flex/nonexistent", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404: %s", resp.StatusCode, body)
	}
}

func TestPromoteFlex_InvalidEntityName(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	resp, _ := ts.doRequest("POST", "/api/v1/entities/promote/flex/123bad", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", resp.StatusCode)
	}
}

// ─── strict: success path ──────────────────────────────────────────────────

func TestPromoteStrict_AllValid_MigratesAndListsCorrectly(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	for i := 0; i < 5; i++ {
		ts.doRequest("POST", "/api/v1/invoices", map[string]interface{}{"amount": "10.00", "paid": false})
	}

	resp, body := ts.doRequest("POST", "/api/v1/entities/promote/strict/invoices", nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("submit status %d: %s", resp.StatusCode, body)
	}
	var submitOut map[string]interface{}
	json.Unmarshal(body, &submitOut)
	ticket, _ := submitOut["ticket"].(string)
	if ticket == "" {
		t.Fatal("empty ticket")
	}
	if submitOut["status"] != "running" {
		t.Errorf("initial status: got %v", submitOut["status"])
	}

	final := pollPromoteStatus(t, ts, ticket, 5*time.Second)
	if final["status"] != "complete" {
		t.Fatalf("final status: got %v (full: %+v)", final["status"], final)
	}
	result, _ := final["result"].(map[string]interface{})
	if result == nil {
		t.Fatal("expected a result object on complete")
	}
	if migrated, _ := result["migrated_rows"].(float64); migrated != 5 {
		t.Errorf("migrated_rows: got %v, want 5", result["migrated_rows"])
	}

	listResp, listBody := ts.doRequest("GET", "/api/v1/invoices", nil)
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d: %s", listResp.StatusCode, listBody)
	}
	var listOut struct {
		Pagination struct {
			TotalItems int `json:"total_items"`
		} `json:"pagination"`
	}
	json.Unmarshal(listBody, &listOut)
	if listOut.Pagination.TotalItems != 5 {
		t.Errorf("LIST total_items: got %d, want 5 (strict must not split data across storage)", listOut.Pagination.TotalItems)
	}

	entResp, entBody := ts.doRequest("GET", "/api/v1/entities", nil)
	if entResp.StatusCode != http.StatusOK {
		t.Fatalf("entities status %d: %s", entResp.StatusCode, entBody)
	}
	var entOut entityListResponse
	json.Unmarshal(entBody, &entOut)
	inv := findEntity(&entOut, "invoices")
	if inv == nil || inv.Count != 5 || !inv.Adapted || !inv.HasSchema {
		t.Errorf("invoices after strict promotion: got %+v, want count=5 adapted=true has_schema=true", inv)
	}
}

func TestPromoteStrict_ExplicitSchema(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	ts.doRequest("POST", "/api/v1/parts", map[string]interface{}{"sku": "P-1"})

	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"sku": map[string]interface{}{"type": "string"}},
		"required":   []string{"sku"},
	}
	schemaBytes, _ := json.Marshal(schema)
	req, _ := http.NewRequest("POST", ts.ts.URL+"/api/v1/entities/promote/strict/parts", bytes.NewReader(schemaBytes))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&out)
	ticket, _ := out["ticket"].(string)

	final := pollPromoteStatus(t, ts, ticket, 5*time.Second)
	if final["status"] != "complete" {
		t.Fatalf("final status: got %v", final["status"])
	}
	result, _ := final["result"].(map[string]interface{})
	if result["auto_inferred"] != false {
		t.Errorf("auto_inferred: got %v, want false (explicit schema given)", result["auto_inferred"])
	}
}

// ─── strict: rejection path -- nothing must mutate ─────────────────────────

func TestPromoteStrict_SomeRowsInvalid_RejectsAndMutatesNothing(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	ts.doRequest("POST", "/api/v1/messy", map[string]interface{}{"val": "text"})
	ts.doRequest("POST", "/api/v1/messy", map[string]interface{}{"val": 42})

	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"val": map[string]interface{}{"type": "string"}},
		"required":   []string{"val"},
	}
	schemaBytes, _ := json.Marshal(schema)
	req, _ := http.NewRequest("POST", ts.ts.URL+"/api/v1/entities/promote/strict/messy", bytes.NewReader(schemaBytes))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("submit status: %d", resp.StatusCode)
	}
	var submitOut map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&submitOut)
	ticket, _ := submitOut["ticket"].(string)

	final := pollPromoteStatus(t, ts, ticket, 5*time.Second)
	if final["status"] != "rejected" {
		t.Fatalf("final status: got %v, want rejected (full: %+v)", final["status"], final)
	}
	failures, _ := final["failures"].([]interface{})
	if len(failures) != 1 {
		t.Fatalf("expected exactly 1 failure (the val=42 row), got %d: %v", len(failures), failures)
	}
	firstFailure, _ := failures[0].(map[string]interface{})
	if int(firstFailure["id"].(float64)) != 2 {
		t.Errorf("failing row id: got %v, want 2", firstFailure["id"])
	}

	entResp, entBody := ts.doRequest("GET", "/api/v1/entities", nil)
	if entResp.StatusCode != http.StatusOK {
		t.Fatalf("entities status %d: %s", entResp.StatusCode, entBody)
	}
	var entOut entityListResponse
	json.Unmarshal(entBody, &entOut)
	messy := findEntity(&entOut, "messy")
	if messy == nil || messy.Adapted || messy.HasSchema || messy.Count != 2 {
		t.Errorf("messy after a rejected strict promotion: got %+v, want unchanged (count=2, adapted=false, has_schema=false)", messy)
	}
}

func TestPromoteStrict_IDFieldNotFalselyRejected(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	for i := 0; i < 3; i++ {
		ts.doRequest("POST", "/api/v1/clean", map[string]interface{}{"name": "ok"})
	}
	resp, body := ts.doRequest("POST", "/api/v1/entities/promote/strict/clean", nil)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("submit status %d: %s", resp.StatusCode, body)
	}
	var out map[string]interface{}
	json.Unmarshal(body, &out)
	ticket, _ := out["ticket"].(string)

	final := pollPromoteStatus(t, ts, ticket, 5*time.Second)
	if final["status"] != "complete" {
		t.Fatalf("expected complete for an all-conforming dataset, got %v (full: %+v)", final["status"], final)
	}
}

// ─── strict: throttling ─────────────────────────────────────────────────────

func TestPromoteStrict_ThrottlesSameEntityType(t *testing.T) {
	// Deliberately NOT an HTTP-level test: an HTTP round trip racing a
	// real (fast) migration job is exactly the kind of test that looks
	// reasonable and is actually flaky -- caught directly: with only a
	// handful of rows, the first job can finish migrating before the
	// second HTTP request even lands, so the second submit correctly
	// 404s ("no data left to promote") instead of hitting the 409 this
	// test means to prove. Testing PromoteJobManager.Submit directly
	// with a manually-controlled blocking work function is
	// deterministic -- the same pattern already proven for
	// tenantexport.JobManager's own per-tenant throttle test.
	m := server.NewPromoteJobManager(4)
	release := make(chan struct{})
	firstTicket, err := m.Submit(tenant.TenantID(1), "bulky", func() (*server.PromoteResult, []server.RowValidationFailure, error) {
		<-release
		return &server.PromoteResult{MigratedRows: 50}, nil, nil
	})
	if err != nil {
		t.Fatalf("first Submit: %v", err)
	}

	_, err = m.Submit(tenant.TenantID(1), "bulky", func() (*server.PromoteResult, []server.RowValidationFailure, error) {
		return nil, nil, nil
	})
	if err == nil {
		t.Fatal("expected the second Submit for the SAME (tenant, entity type) to be rejected while the first runs")
	}
	inFlightErr, ok := err.(*server.ErrPromoteInFlight)
	if !ok {
		t.Fatalf("expected *server.ErrPromoteInFlight, got %T: %v", err, err)
	}
	if inFlightErr.ExistingTicket != firstTicket {
		t.Errorf("ExistingTicket: got %q, want %q", inFlightErr.ExistingTicket, firstTicket)
	}

	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, ok := m.Status(firstTicket)
		if ok && job.Status != server.PromoteJobRunning {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("first job did not finish after release")
}

func TestPromoteStrict_DoesNotThrottleDifferentEntityTypes(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	ts.doRequest("POST", "/api/v1/typea", map[string]interface{}{"x": 1})
	ts.doRequest("POST", "/api/v1/typeb", map[string]interface{}{"y": 1})

	resp1, body1 := ts.doRequest("POST", "/api/v1/entities/promote/strict/typea", nil)
	if resp1.StatusCode != http.StatusAccepted {
		t.Fatalf("typea submit: %d: %s", resp1.StatusCode, body1)
	}
	resp2, body2 := ts.doRequest("POST", "/api/v1/entities/promote/strict/typeb", nil)
	if resp2.StatusCode != http.StatusAccepted {
		t.Errorf("typeb submit for a DIFFERENT entity type should not be throttled: got %d, want 202: %s", resp2.StatusCode, body2)
	}

	var out1, out2 map[string]interface{}
	json.Unmarshal(body1, &out1)
	json.Unmarshal(body2, &out2)
	pollPromoteStatus(t, ts, out1["ticket"].(string), 5*time.Second)
	pollPromoteStatus(t, ts, out2["ticket"].(string), 5*time.Second)
}

// ─── status polling edge cases ──────────────────────────────────────────────

func TestPromoteStatus_UnknownTicket(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	resp, _ := ts.doRequest("GET", "/api/v1/entities/promote/status/prm_doesnotexist", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", resp.StatusCode)
	}
}
