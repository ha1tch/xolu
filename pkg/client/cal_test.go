// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// cal_test.go — Stage 5 (plan M3) tests. Per the Stage 2 convention:
// happy path, structured error, and shape verification per method, plus
// the Openings→Check→Propose sequence exercised at the wire level (the
// server-side T-29 property guards the semantics; this guards the wire).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

var calT0 = time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)

// ─── CalCheck ───────────────────────────────────────────────────────────────

func TestCalCheckHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/cal/check" {
			t.Errorf("expected /api/v2/cal/check, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		if req["calendar_id"] != "room-a" {
			t.Errorf("calendar_id: got %v", req["calendar_id"])
		}
		span := req["span"].(map[string]interface{})
		if _, err := time.Parse(time.RFC3339, span["start"].(string)); err != nil {
			t.Errorf("span.start not RFC3339 with zone: %v", span["start"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"feasible":false,"nearest_openings":[
			{"start":"2026-08-01T11:00:00Z","end":"2026-08-01T12:00:00Z"}]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.CalCheck(context.Background(), "room-a",
		CalSpan{Start: calT0, End: calT0.Add(time.Hour)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Feasible {
		t.Error("expected feasible=false")
	}
	if len(res.NearestOpenings) != 1 || res.NearestOpenings[0].Start.Hour() != 11 {
		t.Errorf("nearest openings: unexpected %+v", res.NearestOpenings)
	}
}

func TestCalCheckClientSideValidation(t *testing.T) {
	c := New("http://unused")
	if _, err := c.CalCheck(context.Background(), "", CalSpan{Start: calT0, End: calT0.Add(time.Hour)}); err == nil {
		t.Error("empty calendarID: expected error")
	}
	if _, err := c.CalCheck(context.Background(), "room-a", CalSpan{Start: calT0, End: calT0}); err == nil {
		t.Error("degenerate span: expected error")
	}
}

func TestCalCheckDisabledSubsystem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		w.Write([]byte(`{"error":{"code":"XOLU-CAL001","message":"cal subsystem is not enabled on this server (XOLU_CAL_ENABLED=false)","status":501}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.CalCheck(context.Background(), "room-a",
		CalSpan{Start: calT0, End: calT0.Add(time.Hour)})
	ce, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *client.Error, got %T: %v", err, err)
	}
	if ce.Code != "XOLU-CAL001" || ce.HTTPStatus != http.StatusNotImplemented {
		t.Errorf("expected XOLU-CAL001/501, got %s/%d", ce.Code, ce.HTTPStatus)
	}
}

// ─── CalOpenings ────────────────────────────────────────────────────────────

func TestCalOpeningsHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/cal/openings" {
			t.Errorf("expected /api/v2/cal/openings, got %s", r.URL.Path)
		}
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		if req["duration_ms"] != float64(30*60*1000) {
			t.Errorf("duration_ms: got %v", req["duration_ms"])
		}
		if req["objective"] != "emptiest" {
			t.Errorf("objective: got %v", req["objective"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"objective":"emptiest","openings":[
			{"start":"2026-08-01T09:00:00Z","end":"2026-08-01T09:30:00Z","margin_ms":900000}]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.CalOpenings(context.Background(), "room-a",
		calT0, calT0.Add(8*time.Hour), 30*time.Minute, ObjectiveEmptiest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Objective != ObjectiveEmptiest {
		t.Errorf("objective echo: got %s", res.Objective)
	}
	if len(res.Openings) != 1 || res.Openings[0].MarginMs != 900000 {
		t.Errorf("openings: unexpected %+v", res.Openings)
	}
}

func TestCalOpeningsObjectiveValidation(t *testing.T) {
	c := New("http://unused")
	_, err := c.CalOpenings(context.Background(), "room-a",
		calT0, calT0.Add(time.Hour), 30*time.Minute, Objective("soonest"))
	if err == nil {
		t.Fatal("invalid objective: expected client-side error before any request")
	}
	if _, ok := err.(*Error); ok {
		t.Error("expected a plain validation error, not *client.Error (no request should be sent)")
	}
}

func TestCalOpeningsEmptyObjectiveDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"objective":"earliest","openings":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.CalOpenings(context.Background(), "room-a",
		calT0, calT0.Add(time.Hour), 30*time.Minute, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Objective != ObjectiveEarliest {
		t.Errorf("expected server-defaulted earliest echo, got %s", res.Objective)
	}
	if res.Openings == nil {
		t.Error("expected non-nil empty slice")
	}
}

// ─── CalPropose / CalConfirm ────────────────────────────────────────────────

func TestCalProposeHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/cal/propose" {
			t.Errorf("expected /api/v2/cal/propose, got %s", r.URL.Path)
		}
		var req map[string]interface{}
		json.NewDecoder(r.Body).Decode(&req)
		if req["booking_id"] != "01J0EXAMPLEULID" {
			t.Errorf("booking_id: got %v", req["booking_id"])
		}
		if _, present := req["buffer_after"]; present {
			t.Error("nil BufferAfter must be omitted from the wire")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"booking_id":"01J0EXAMPLEULID","calendar_id":"room-a",
			"state":"proposed","span":{"start":"2026-08-01T09:00:00Z","end":"2026-08-01T10:00:00Z"},
			"mode":"exclusive","bearer":7,"created_at":"2026-07-18T12:00:00Z",
			"updated_at":"2026-07-18T12:00:00Z"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	b, err := c.CalPropose(context.Background(), CalProposeRequest{
		BookingID:  "01J0EXAMPLEULID",
		CalendarID: "room-a",
		Span:       CalSpan{Start: calT0, End: calT0.Add(time.Hour)},
		Bearer:     7,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.State != "proposed" || b.Mode != "exclusive" || b.Bearer != 7 {
		t.Errorf("booking shape: %+v", b)
	}
	if b.CreatedAt.IsZero() {
		t.Error("created_at should have parsed")
	}
}

func TestCalProposeConflictError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":{"code":"XOLU-CAL004","message":"span conflicts with existing occupancy","status":422}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.CalPropose(context.Background(), CalProposeRequest{
		BookingID: "b1", CalendarID: "room-a",
		Span: CalSpan{Start: calT0, End: calT0.Add(time.Hour)},
	})
	ce, ok := err.(*Error)
	if !ok || ce.Code != "XOLU-CAL004" {
		t.Fatalf("expected XOLU-CAL004 *client.Error, got %T: %v", err, err)
	}
}

func TestCalConfirmHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/cal/confirm" {
			t.Errorf("expected /api/v2/cal/confirm, got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"booking_id":"b1","calendar_id":"room-a","state":"binding",
			"span":{"start":"2026-08-01T09:00:00Z","end":"2026-08-01T10:00:00Z"},
			"mode":"exclusive","bearer":7}`))
	}))
	defer server.Close()

	c := New(server.URL)
	b, err := c.CalConfirm(context.Background(), "room-a", "b1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.State != "binding" {
		t.Errorf("expected binding, got %s", b.State)
	}
}

func TestCalConfirmIllegalTransition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":{"code":"XOLU-CAL003","message":"illegal transition: binding -> binding","status":422}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.CalConfirm(context.Background(), "room-a", "b1")
	ce, ok := err.(*Error)
	if !ok || ce.Code != "XOLU-CAL003" {
		t.Fatalf("expected XOLU-CAL003 *client.Error, got %T: %v", err, err)
	}
}

// ─── Openings → Check → Propose sequence ────────────────────────────────────

// The molu tools will drive exactly this sequence. The scripted server
// asserts each request's wire shape and hands the opening from step 1
// through the chain; a drift in any of the three wire formats breaks it.
func TestCalSequenceOpeningsCheckPropose(t *testing.T) {
	step := 0
	openStart, openEnd := "2026-08-01T14:00:00Z", "2026-08-01T15:00:00Z"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		step++
		switch step {
		case 1:
			if r.URL.Path != "/api/v2/cal/openings" {
				t.Errorf("step 1: got %s", r.URL.Path)
			}
			w.Write([]byte(`{"objective":"earliest","openings":[
				{"start":"` + openStart + `","end":"` + openEnd + `","margin_ms":600000}]}`))
		case 2:
			if r.URL.Path != "/api/v2/cal/check" {
				t.Errorf("step 2: got %s", r.URL.Path)
			}
			var req map[string]interface{}
			json.NewDecoder(r.Body).Decode(&req)
			span := req["span"].(map[string]interface{})
			got, _ := time.Parse(time.RFC3339, span["start"].(string))
			want, _ := time.Parse(time.RFC3339, openStart)
			if !got.Equal(want) {
				t.Errorf("step 2: span.start %v does not chain from step 1 opening %v", got, want)
			}
			w.Write([]byte(`{"feasible":true,"nearest_openings":[]}`))
		case 3:
			if r.URL.Path != "/api/v2/cal/propose" {
				t.Errorf("step 3: got %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"booking_id":"seq-b1","calendar_id":"room-a","state":"proposed",
				"span":{"start":"` + openStart + `","end":"` + openEnd + `"},"mode":"exclusive","bearer":1}`))
		}
	}))
	defer server.Close()

	c := New(server.URL)
	ctx := context.Background()

	res, err := c.CalOpenings(ctx, "room-a", calT0, calT0.Add(12*time.Hour), time.Hour, "")
	if err != nil || len(res.Openings) != 1 {
		t.Fatalf("openings: %v / %+v", err, res)
	}
	o := res.Openings[0]

	chk, err := c.CalCheck(ctx, "room-a", CalSpan{Start: o.Start, End: o.End})
	if err != nil || !chk.Feasible {
		t.Fatalf("check: %v / %+v", err, chk)
	}

	b, err := c.CalPropose(ctx, CalProposeRequest{
		BookingID: "seq-b1", CalendarID: "room-a",
		Span: CalSpan{Start: o.Start, End: o.End}, Bearer: 1,
	})
	if err != nil || b.State != "proposed" {
		t.Fatalf("propose: %v / %+v", err, b)
	}
	if step != 3 {
		t.Fatalf("expected 3 chained requests, got %d", step)
	}
}
