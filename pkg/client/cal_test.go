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

// ─── CalCreateCalendar ──────────────────────────────────────────────────────

func TestCalCreateCalendarHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/cal/calendars" {
			t.Errorf("expected /api/v2/cal/calendars, got %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		var body map[string]interface{}
		json.NewDecoder(r.Body).Decode(&body)
		if body["calendar_id"] != "room-a" {
			t.Errorf("calendar_id: got %v", body["calendar_id"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"calendar_id":"room-a","entity_ref":0,"default_state":"binding","match_policy":"binding"}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.CalCreateCalendar(context.Background(), CalCreateCalendarRequest{CalendarID: "room-a"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.CalendarID != "room-a" || res.DefaultState != "binding" {
		t.Errorf("unexpected calendar: %+v", res)
	}
}

func TestCalCreateCalendarAlreadyExists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		w.Write([]byte(`{"error":{"code":"XOLU-CAL008","message":"calendar already exists: room-a","status":409}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.CalCreateCalendar(context.Background(), CalCreateCalendarRequest{CalendarID: "room-a"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCalCreateCalendarRequiresCalendarID(t *testing.T) {
	c := New("http://unused")
	if _, err := c.CalCreateCalendar(context.Background(), CalCreateCalendarRequest{}); err == nil {
		t.Error("expected error for empty CalendarID")
	}
}

// ─── CalListCalendars ───────────────────────────────────────────────────────

func TestCalListCalendarsHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/cal/calendars" {
			t.Errorf("expected /api/v2/cal/calendars, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"calendars":[
			{"calendar_id":"room-a","entity_ref":1,"default_state":"binding","match_policy":"consider_binding"},
			{"calendar_id":"room-b","entity_ref":2,"default_state":"proposed","match_policy":"binding+proposed"}]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.CalListCalendars(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Calendars) != 2 {
		t.Fatalf("want 2 calendars, got %d: %+v", len(res.Calendars), res.Calendars)
	}
	if res.Calendars[0].CalendarID != "room-a" || res.Calendars[0].DefaultState != "binding" {
		t.Errorf("unexpected room-a: %+v", res.Calendars[0])
	}
}

func TestCalListCalendarsEmptyResultNotNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"calendars":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.CalListCalendars(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Calendars == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
}

func TestCalListCalendarsDisabledSubsystem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		w.Write([]byte(`{"error":{"code":"XOLU-CAL099","message":"cal subsystem is not enabled","status":501}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.CalListCalendars(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ─── CalListBookings ────────────────────────────────────────────────────────

func TestCalListBookingsHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/cal/bookings" {
			t.Errorf("expected /api/v2/cal/bookings, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		q := r.URL.Query()
		if q.Get("calendar_id") != "room-a" {
			t.Errorf("calendar_id: got %v", q.Get("calendar_id"))
		}
		if q.Get("from") == "" || q.Get("to") == "" {
			t.Errorf("from/to: want both set, got from=%q to=%q", q.Get("from"), q.Get("to"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"bookings":[
			{"booking_id":"b1","calendar_id":"room-a","state":"binding",
			 "span":{"start":"2026-08-01T09:00:00Z","end":"2026-08-01T10:00:00Z"},
			 "mode":"exclusive","bearer":1}]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.CalListBookings(context.Background(), "room-a", calT0, calT0.Add(8*time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Bookings) != 1 || res.Bookings[0].BookingID != "b1" || res.Bookings[0].State != "binding" {
		t.Errorf("unexpected bookings: %+v", res.Bookings)
	}
}

func TestCalListBookingsEmptyResultNotNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"bookings":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.CalListBookings(context.Background(), "room-a", calT0, calT0.Add(time.Hour))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Bookings == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
}

func TestCalListBookingsUnknownCalendar(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-CAL002","message":"calendar not found: room-z"}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.CalListBookings(context.Background(), "room-z", calT0, calT0.Add(time.Hour))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestCalListBookingsClientSideValidation(t *testing.T) {
	c := New("http://unused")
	ctx := context.Background()

	if _, err := c.CalListBookings(ctx, "", calT0, calT0.Add(time.Hour)); err == nil {
		t.Error("expected error for empty calendarID")
	}
	if _, err := c.CalListBookings(ctx, "room-a", calT0, calT0); err == nil {
		t.Error("expected error for from == to")
	}
	if _, err := c.CalListBookings(ctx, "room-a", calT0.Add(time.Hour), calT0); err == nil {
		t.Error("expected error for from after to")
	}
}

// ─── CalListBookingsForBearer ───────────────────────────────────────────────

func TestCalListBookingsForBearerHappyPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/cal/bookings/by-bearer" {
			t.Errorf("expected /api/v2/cal/bookings/by-bearer, got %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		if got := r.URL.Query().Get("bearer"); got != "100" {
			t.Errorf("bearer query param: got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"bookings":[
			{"booking_id":"b1","calendar_id":"room-a","state":"binding",
			 "span":{"start":"2026-08-01T09:00:00Z","end":"2026-08-01T10:00:00Z"},
			 "mode":"exclusive","bearer":100},
			{"booking_id":"b2","calendar_id":"room-b","state":"proposed",
			 "span":{"start":"2026-08-02T09:00:00Z","end":"2026-08-02T10:00:00Z"},
			 "mode":"exclusive","bearer":100}]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.CalListBookingsForBearer(context.Background(), 100)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.Bookings) != 2 {
		t.Fatalf("want 2 bookings, got %d: %+v", len(res.Bookings), res.Bookings)
	}
	if res.Bookings[0].CalendarID != "room-a" || res.Bookings[1].CalendarID != "room-b" {
		t.Errorf("expected bookings from two different calendars, got %+v", res.Bookings)
	}
}

func TestCalListBookingsForBearerEmptyResultNotNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"bookings":[]}`))
	}))
	defer server.Close()

	c := New(server.URL)
	res, err := c.CalListBookingsForBearer(context.Background(), 999)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Bookings == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
}

func TestCalListBookingsForBearerRequiresNonZero(t *testing.T) {
	c := New("http://unused")
	if _, err := c.CalListBookingsForBearer(context.Background(), 0); err == nil {
		t.Error("expected client-side validation error for bearer=0")
	}
}

func TestCalListBookingsForBearerServerRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":{"code":"XOLU-CAL002","message":"invalid bearer","status":400}}`))
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.CalListBookingsForBearer(context.Background(), 42)
	if err == nil {
		t.Fatal("expected error, got nil")
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
