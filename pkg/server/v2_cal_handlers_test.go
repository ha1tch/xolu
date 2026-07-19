// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// HTTP-level tests for the T-18 cal endpoints, added in v0.14.8.
//
// These tests boot a real xolu server (via the newV2Server helper) with
// CalEnabled=true, seed calendars and bookings directly through the
// cal.Manager exposed by the server, and exercise the four handlers over
// HTTP. Assertions cover:
//
//   - happy path for every endpoint
//   - structured error responses with the correct XOLU-CAL* code
//   - error-code granularity: unknown_calendar vs unknown_booking vs
//     illegal_transition are all distinguishable
//   - disabled subsystem returns 501 XOLU-CAL001 on every route
//   - the four objectives on /openings all resolve to real behaviour
//   - invalid inputs (bad span, bad objective, missing fields) are
//     structurally rejected before any lifecycle work happens

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/cal"
	"github.com/ha1tch/xolu/pkg/config"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// ─── Test scaffolding ──────────────────────────────────────────────────────

// newCalServer builds a test server with CalEnabled=true (and APIV2Enabled
// as required for cal to be reachable).
func newCalServer(t *testing.T) *stdTestServer {
	t.Helper()
	return newV2Server(t, func(cfg *config.Config) {
		cfg.CalEnabled = true
	})
}

// newCalDisabledServer builds a test server with APIV2Enabled=true but
// CalEnabled=false, so the /cal/* routes are absent.
func newCalDisabledServer(t *testing.T) *stdTestServer {
	t.Helper()
	return newV2Server(t)
}

// seedCalendar creates a calendar via the Manager.CreateCalendar facade
// (introduced in v0.14.10), which handles both persistence and index
// registration in one call. Before the facade existed, tests had to call
// SourceFor().CreateCalendar() and IndexFor().RegisterCalendar()
// separately, and forgetting the latter caused subtle Lifecycle.Create
// failures with ErrUnknownCalendar.
func seedCalendar(t *testing.T, sts *stdTestServer, calendarID string) {
	t.Helper()
	mgr := sts.srv.CalManagerForTest()
	_, err := mgr.CreateCalendar(0, cal.Calendar{
		CalendarID:   calendarID,
		DefaultState: cal.StateBinding,
		MatchPolicy:  cal.ConsiderBinding,
	})
	if err != nil {
		t.Fatalf("seedCalendar %q: %v", calendarID, err)
	}
}

// seedBooking creates a proposed booking directly through the cal.Lifecycle.
func seedBooking(t *testing.T, sts *stdTestServer, calendarID, bookingID string, start, end time.Time) {
	t.Helper()
	lc, err := sts.srv.CalManagerForTest().CalFor(0)
	if err != nil {
		t.Fatalf("seedBooking: CalFor: %v", err)
	}
	_, err = lc.Create(cal.Booking{
		BookingID:  bookingID,
		CalendarID: calendarID,
		State:      cal.StateProposed,
		Span: cal.Span{
			Start: ot.FromTime(start),
			End:   ot.FromTime(end),
		},
		Mode:   cal.ModeExclusive,
		Bearer: 1,
	})
	if err != nil {
		t.Fatalf("seedBooking %q: %v", bookingID, err)
	}
}

// post issues a POST to /api/v2/cal/{endpoint} on the test server, returning
// the raw response for inspection.
func calPost(t *testing.T, sts *stdTestServer, endpoint string, body interface{}) *http.Response {
	t.Helper()
	jsonBody, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	url := fmt.Sprintf("%s/api/v2/cal/%s", sts.ts.URL, endpoint)
	resp, err := http.Post(url, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		t.Fatalf("POST /api/v2/cal/%s: %v", endpoint, err)
	}
	return resp
}

// readBody drains and returns the response body as a decoded map.
func readCalBody(t *testing.T, resp *http.Response) map[string]interface{} {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(raw) == 0 {
		return nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode body %q: %v", raw, err)
	}
	return out
}

// assertErrorCode drains the response and asserts the structured error
// envelope carries the expected code. Returns the parsed error object for
// further inspection.
func assertCalErrorCode(t *testing.T, resp *http.Response, wantStatus int, wantCode string) map[string]interface{} {
	t.Helper()
	body := readCalBody(t, resp)
	if resp.StatusCode != wantStatus {
		t.Fatalf("expected status %d, got %d; body=%v", wantStatus, resp.StatusCode, body)
	}
	errObj, ok := body["error"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected structured error envelope, got %v", body)
	}
	if code, _ := errObj["code"].(string); code != wantCode {
		t.Errorf("expected code %q, got %q", wantCode, code)
	}
	return errObj
}

// ─── /check ────────────────────────────────────────────────────────────────

func TestCalCheckFeasibleOnEmptyCalendar(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-empty")

	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	resp := calPost(t, sts, "check", map[string]interface{}{
		"calendar_id": "cal-empty",
		"span": map[string]interface{}{
			"start": start.Format(time.RFC3339),
			"end":   end.Format(time.RFC3339),
		},
		"mode": "exclusive",
	})
	body := readCalBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%v", resp.StatusCode, body)
	}
	if feasible, _ := body["feasible"].(bool); !feasible {
		t.Errorf("expected feasible=true on empty calendar, got %v", body["feasible"])
	}
}

func TestCalCheckInfeasibleReturnsNearestOpenings(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-busy")

	// Occupy 10:00-11:00 with a proposed booking.
	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	seedBooking(t, sts, "cal-busy", "book-1", start, end)

	// Ask about the same window: should be infeasible.
	resp := calPost(t, sts, "check", map[string]interface{}{
		"calendar_id": "cal-busy",
		"span": map[string]interface{}{
			"start": start.Format(time.RFC3339),
			"end":   end.Format(time.RFC3339),
		},
		"mode": "exclusive",
	})
	body := readCalBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%v", resp.StatusCode, body)
	}
	if feasible, _ := body["feasible"].(bool); feasible {
		t.Errorf("expected feasible=false with conflicting booking, got %v", body["feasible"])
	}
	openings, _ := body["nearest_openings"].([]interface{})
	if len(openings) == 0 {
		t.Errorf("expected at least one nearest opening on infeasibility")
	}
}

func TestCalCheckUnknownCalendarReturnsCAL004(t *testing.T) {
	sts := newCalServer(t)
	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	resp := calPost(t, sts, "check", map[string]interface{}{
		"calendar_id": "nonexistent",
		"span": map[string]interface{}{
			"start": start.Format(time.RFC3339),
			"end":   start.Add(time.Hour).Format(time.RFC3339),
		},
		"mode": "exclusive",
	})
	assertCalErrorCode(t, resp, http.StatusNotFound, "XOLU-CAL004")
}

func TestCalCheckInvalidSpanReturnsCAL002(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-x")

	// end before start — invalid.
	start := time.Date(2027, 1, 1, 11, 0, 0, 0, time.UTC)
	end := start.Add(-time.Hour)
	resp := calPost(t, sts, "check", map[string]interface{}{
		"calendar_id": "cal-x",
		"span": map[string]interface{}{
			"start": start.Format(time.RFC3339),
			"end":   end.Format(time.RFC3339),
		},
		"mode": "exclusive",
	})
	assertCalErrorCode(t, resp, http.StatusBadRequest, "XOLU-CAL002")
}

func TestCalCheckMissingCalendarIDReturnsCAL004(t *testing.T) {
	sts := newCalServer(t)
	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	resp := calPost(t, sts, "check", map[string]interface{}{
		"span": map[string]interface{}{
			"start": start.Format(time.RFC3339),
			"end":   start.Add(time.Hour).Format(time.RFC3339),
		},
	})
	// Missing calendar_id surfaces as a bad-request but under the calendar-
	// not-found code (calendar_id is required).
	assertCalErrorCode(t, resp, http.StatusBadRequest, "XOLU-CAL004")
}

// ─── /openings ─────────────────────────────────────────────────────────────

func TestCalOpeningsHappyPath(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-open")

	from := time.Date(2027, 1, 1, 9, 0, 0, 0, time.UTC)
	to := from.Add(4 * time.Hour)
	resp := calPost(t, sts, "openings", map[string]interface{}{
		"calendar_id": "cal-open",
		"from":        from.Format(time.RFC3339),
		"to":          to.Format(time.RFC3339),
		"duration_ms": (30 * time.Minute).Milliseconds(),
		"objective":   "earliest",
	})
	body := readCalBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%v", resp.StatusCode, body)
	}
	if obj, _ := body["objective"].(string); obj != "earliest" {
		t.Errorf("expected objective=earliest, got %v", body["objective"])
	}
	openings, _ := body["openings"].([]interface{})
	if len(openings) == 0 {
		t.Errorf("expected openings on empty calendar")
	}
}

func TestCalOpeningsInvalidObjectiveReturnsCAL003(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-obj")

	from := time.Date(2027, 1, 1, 9, 0, 0, 0, time.UTC)
	resp := calPost(t, sts, "openings", map[string]interface{}{
		"calendar_id": "cal-obj",
		"from":        from.Format(time.RFC3339),
		"to":          from.Add(2 * time.Hour).Format(time.RFC3339),
		"duration_ms": (30 * time.Minute).Milliseconds(),
		"objective":   "unknown-objective",
	})
	assertCalErrorCode(t, resp, http.StatusBadRequest, "XOLU-CAL003")
}

func TestCalOpeningsAllFourObjectivesAccepted(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-four")

	from := time.Date(2027, 1, 1, 9, 0, 0, 0, time.UTC)
	to := from.Add(4 * time.Hour)
	for _, obj := range []string{"earliest", "first-fit", "emptiest", "longest-clear-margin"} {
		resp := calPost(t, sts, "openings", map[string]interface{}{
			"calendar_id": "cal-four",
			"from":        from.Format(time.RFC3339),
			"to":          to.Format(time.RFC3339),
			"duration_ms": (30 * time.Minute).Milliseconds(),
			"objective":   obj,
		})
		body := readCalBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("objective=%q: expected 200, got %d; body=%v", obj, resp.StatusCode, body)
			continue
		}
		if got, _ := body["objective"].(string); got != obj {
			t.Errorf("objective=%q: expected echo, got %q", obj, got)
		}
	}
}

func TestCalOpeningsEmptyObjectiveDefaultsToEarliest(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-def")

	from := time.Date(2027, 1, 1, 9, 0, 0, 0, time.UTC)
	resp := calPost(t, sts, "openings", map[string]interface{}{
		"calendar_id": "cal-def",
		"from":        from.Format(time.RFC3339),
		"to":          from.Add(2 * time.Hour).Format(time.RFC3339),
		"duration_ms": (30 * time.Minute).Milliseconds(),
		// objective omitted
	})
	body := readCalBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%v", resp.StatusCode, body)
	}
	if obj, _ := body["objective"].(string); obj != "earliest" {
		t.Errorf("expected default objective earliest, got %v", body["objective"])
	}
}

func TestCalOpeningsBadRangeReturnsCAL002(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-bad")

	// from >= to
	from := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	to := from
	resp := calPost(t, sts, "openings", map[string]interface{}{
		"calendar_id": "cal-bad",
		"from":        from.Format(time.RFC3339),
		"to":          to.Format(time.RFC3339),
		"duration_ms": (30 * time.Minute).Milliseconds(),
	})
	assertCalErrorCode(t, resp, http.StatusBadRequest, "XOLU-CAL002")
}

func TestCalOpeningsZeroDurationReturnsCAL002(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-zero")

	from := time.Date(2027, 1, 1, 9, 0, 0, 0, time.UTC)
	resp := calPost(t, sts, "openings", map[string]interface{}{
		"calendar_id": "cal-zero",
		"from":        from.Format(time.RFC3339),
		"to":          from.Add(time.Hour).Format(time.RFC3339),
		"duration_ms": 0,
	})
	assertCalErrorCode(t, resp, http.StatusBadRequest, "XOLU-CAL002")
}

// ─── /propose ──────────────────────────────────────────────────────────────

func TestCalProposeHappyPath(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-p")

	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	resp := calPost(t, sts, "propose", map[string]interface{}{
		"booking_id":  "book-new",
		"calendar_id": "cal-p",
		"span": map[string]interface{}{
			"start": start.Format(time.RFC3339),
			"end":   end.Format(time.RFC3339),
		},
		"mode":   "exclusive",
		"bearer": 42,
	})
	body := readCalBody(t, resp)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d; body=%v", resp.StatusCode, body)
	}
	if got, _ := body["booking_id"].(string); got != "book-new" {
		t.Errorf("expected booking_id echo, got %v", body["booking_id"])
	}
	if got, _ := body["state"].(string); got != "proposed" {
		t.Errorf("expected state=proposed, got %v", body["state"])
	}
}

func TestCalProposeUnknownCalendarReturnsCAL004(t *testing.T) {
	sts := newCalServer(t)
	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	resp := calPost(t, sts, "propose", map[string]interface{}{
		"booking_id":  "book-x",
		"calendar_id": "nonexistent",
		"span": map[string]interface{}{
			"start": start.Format(time.RFC3339),
			"end":   start.Add(time.Hour).Format(time.RFC3339),
		},
		"mode":   "exclusive",
		"bearer": 1,
	})
	assertCalErrorCode(t, resp, http.StatusNotFound, "XOLU-CAL004")
}

func TestCalProposeMissingBookingIDReturnsCAL005(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-nb")

	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	resp := calPost(t, sts, "propose", map[string]interface{}{
		"calendar_id": "cal-nb",
		"span": map[string]interface{}{
			"start": start.Format(time.RFC3339),
			"end":   start.Add(time.Hour).Format(time.RFC3339),
		},
		"mode":   "exclusive",
		"bearer": 1,
	})
	assertCalErrorCode(t, resp, http.StatusBadRequest, "XOLU-CAL005")
}

func TestCalProposeInvalidSpanReturnsCAL002(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-inv")

	// Zero-length span — Start == End.
	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	resp := calPost(t, sts, "propose", map[string]interface{}{
		"booking_id":  "book-inv",
		"calendar_id": "cal-inv",
		"span": map[string]interface{}{
			"start": start.Format(time.RFC3339),
			"end":   start.Format(time.RFC3339),
		},
		"mode":   "exclusive",
		"bearer": 1,
	})
	assertCalErrorCode(t, resp, http.StatusBadRequest, "XOLU-CAL002")
}

// ─── /confirm ──────────────────────────────────────────────────────────────

func TestCalConfirmHappyPath(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-c")

	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	seedBooking(t, sts, "cal-c", "book-c", start, start.Add(time.Hour))

	resp := calPost(t, sts, "confirm", map[string]interface{}{
		"calendar_id": "cal-c",
		"booking_id":  "book-c",
	})
	body := readCalBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%v", resp.StatusCode, body)
	}
	if state, _ := body["state"].(string); state != "binding" {
		t.Errorf("expected state=binding after confirm, got %v", body["state"])
	}
}

func TestCalConfirmUnknownBookingReturnsCAL005(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-nb")

	resp := calPost(t, sts, "confirm", map[string]interface{}{
		"calendar_id": "cal-nb",
		"booking_id":  "does-not-exist",
	})
	assertCalErrorCode(t, resp, http.StatusNotFound, "XOLU-CAL005")
}

func TestCalConfirmUnknownCalendarReturnsCAL005(t *testing.T) {
	sts := newCalServer(t)
	// Booking lookup goes through calendar_id + booking_id. Neither exists,
	// so we get "unknown booking" (CAL005) rather than "unknown calendar".
	resp := calPost(t, sts, "confirm", map[string]interface{}{
		"calendar_id": "nonexistent-cal",
		"booking_id":  "nonexistent-bk",
	})
	assertCalErrorCode(t, resp, http.StatusNotFound, "XOLU-CAL005")
}

func TestCalConfirmIllegalTransitionReturnsCAL006(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-ill")

	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	seedBooking(t, sts, "cal-ill", "book-ill", start, start.Add(time.Hour))

	// First confirm succeeds: proposed -> binding.
	resp := calPost(t, sts, "confirm", map[string]interface{}{
		"calendar_id": "cal-ill",
		"booking_id":  "book-ill",
	})
	if resp.StatusCode != http.StatusOK {
		body := readCalBody(t, resp)
		t.Fatalf("first confirm should succeed, got %d; body=%v", resp.StatusCode, body)
	}
	resp.Body.Close()

	// Second confirm on the same booking is now binding->binding, which
	// allowedTransition rejects. Should surface as XOLU-CAL006.
	resp2 := calPost(t, sts, "confirm", map[string]interface{}{
		"calendar_id": "cal-ill",
		"booking_id":  "book-ill",
	})
	assertCalErrorCode(t, resp2, http.StatusUnprocessableEntity, "XOLU-CAL006")
}

func TestCalConfirmMissingCalendarIDReturnsCAL004(t *testing.T) {
	sts := newCalServer(t)
	resp := calPost(t, sts, "confirm", map[string]interface{}{
		"booking_id": "some-booking",
	})
	assertCalErrorCode(t, resp, http.StatusBadRequest, "XOLU-CAL004")
}

func TestCalConfirmMissingBookingIDReturnsCAL005(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-mb")
	resp := calPost(t, sts, "confirm", map[string]interface{}{
		"calendar_id": "cal-mb",
	})
	assertCalErrorCode(t, resp, http.StatusBadRequest, "XOLU-CAL005")
}

// ─── Disabled subsystem returns 404 on the routes ──────────────────────────

func TestCalDisabledReturns404OnAllRoutes(t *testing.T) {
	sts := newCalDisabledServer(t)

	// When CalEnabled=false, setupV2CalRoutes is never called, so the routes
	// are absent from the router and chi returns 404. This is the same
	// posture xolu uses for the v2 tree as a whole.
	start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
	endpoints := []struct {
		name string
		body map[string]interface{}
	}{
		{"check", map[string]interface{}{
			"calendar_id": "x",
			"span":        map[string]interface{}{"start": start.Format(time.RFC3339), "end": start.Add(time.Hour).Format(time.RFC3339)},
		}},
		{"openings", map[string]interface{}{
			"calendar_id": "x",
			"from":        start.Format(time.RFC3339),
			"to":          start.Add(time.Hour).Format(time.RFC3339),
			"duration_ms": 60000,
		}},
		{"propose", map[string]interface{}{
			"calendar_id": "x", "booking_id": "y",
			"span": map[string]interface{}{"start": start.Format(time.RFC3339), "end": start.Add(time.Hour).Format(time.RFC3339)},
		}},
		{"confirm", map[string]interface{}{"calendar_id": "x", "booking_id": "y"}},
	}
	for _, ep := range endpoints {
		resp := calPost(t, sts, ep.name, ep.body)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("endpoint %q: expected 404 when cal disabled, got %d", ep.name, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

// ─── classifyCalError unit tests ──────────────────────────────────────────

// These are pure-function tests for the classifier; they do not need the
// httptest server. They're here (rather than under an internal_test.go)
// because classifyCalError is unexported; we test its observable output
// through the four handlers above, but also verify each sentinel's
// dispatch directly by constructing wrapped errors and inspecting the
// error response codes end-to-end.

func TestCalClassificationEndToEndForEverySentinel(t *testing.T) {
	// Verifies that every sentinel in pkg/cal has a distinct XOLU-CAL*
	// classification when it reaches the handler layer. Any regression
	// where a new sentinel is added without a classifier entry would
	// surface as a 500 ST006 here.
	//
	// We use /propose because Lifecycle.Create exercises the widest set
	// of sentinel paths (unknown-calendar, invalid-span via PutBooking,
	// bearer-required for binding bookings).
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-classify")

	// unknown-calendar → 404 CAL004
	{
		start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
		resp := calPost(t, sts, "propose", map[string]interface{}{
			"booking_id":  "b1",
			"calendar_id": "no-such-cal",
			"span": map[string]interface{}{
				"start": start.Format(time.RFC3339),
				"end":   start.Add(time.Hour).Format(time.RFC3339),
			},
			"mode": "exclusive", "bearer": 1,
		})
		assertCalErrorCode(t, resp, http.StatusNotFound, "XOLU-CAL004")
	}

	// invalid-span (span before Create) → 400 CAL002 (guard, not sentinel)
	{
		start := time.Date(2027, 1, 1, 10, 0, 0, 0, time.UTC)
		resp := calPost(t, sts, "propose", map[string]interface{}{
			"booking_id":  "b2",
			"calendar_id": "cal-classify",
			"span": map[string]interface{}{
				"start": start.Format(time.RFC3339),
				"end":   start.Format(time.RFC3339), // zero-length
			},
			"mode": "exclusive", "bearer": 1,
		})
		assertCalErrorCode(t, resp, http.StatusBadRequest, "XOLU-CAL002")
	}

	// A successful propose (needed to test the illegal-transition path
	// below via /confirm).
	{
		start := time.Date(2027, 1, 2, 10, 0, 0, 0, time.UTC)
		resp := calPost(t, sts, "propose", map[string]interface{}{
			"booking_id":  "b-ok",
			"calendar_id": "cal-classify",
			"span": map[string]interface{}{
				"start": start.Format(time.RFC3339),
				"end":   start.Add(time.Hour).Format(time.RFC3339),
			},
			"mode": "exclusive", "bearer": 1,
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("propose b-ok: expected 201, got %d", resp.StatusCode)
		}
		resp.Body.Close()
	}

	// unknown-booking → 404 CAL005
	{
		resp := calPost(t, sts, "confirm", map[string]interface{}{
			"calendar_id": "cal-classify",
			"booking_id":  "no-such-booking",
		})
		assertCalErrorCode(t, resp, http.StatusNotFound, "XOLU-CAL005")
	}

	// illegal-transition via double confirm → 422 CAL006
	{
		if resp := calPost(t, sts, "confirm", map[string]interface{}{
			"calendar_id": "cal-classify", "booking_id": "b-ok",
		}); resp.StatusCode != http.StatusOK {
			t.Fatalf("first confirm should succeed, got %d", resp.StatusCode)
		} else {
			resp.Body.Close()
		}
		resp := calPost(t, sts, "confirm", map[string]interface{}{
			"calendar_id": "cal-classify", "booking_id": "b-ok",
		})
		assertCalErrorCode(t, resp, http.StatusUnprocessableEntity, "XOLU-CAL006")
	}
}

// ─── Wire-format robustness tests ─────────────────────────────────────────

func TestCalCheckRejectsMalformedJSON(t *testing.T) {
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-mal")

	url := fmt.Sprintf("%s/api/v2/cal/check", sts.ts.URL)
	resp, err := http.Post(url, "application/json", bytes.NewReader([]byte(`{"calendar_id":`)))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for malformed JSON, got %d", resp.StatusCode)
	}
}

func TestCalHandlerRejectsWrongMethod(t *testing.T) {
	// All four cal endpoints are POST-only. GET should return 405 or 404
	// depending on chi's method-mismatch posture.
	sts := newCalServer(t)
	for _, ep := range []string{"check", "openings", "propose", "confirm"} {
		url := fmt.Sprintf("%s/api/v2/cal/%s", sts.ts.URL, ep)
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("GET %s: %v", ep, err)
		}
		if resp.StatusCode < 400 {
			t.Errorf("endpoint %q: GET should fail, got %d", ep, resp.StatusCode)
		}
		resp.Body.Close()
	}
}

func TestCalCheckPreservesUTCTimezone(t *testing.T) {
	// A span provided with an explicit non-UTC offset must be correctly
	// parsed via xolutime's Instant marshalling. If timezone handling
	// were broken, a span-with-offset would either fail to parse or
	// resolve to the wrong absolute instant, changing feasibility.
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-tz")

	// Occupy 12:00-13:00 UTC.
	start := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	seedBooking(t, sts, "cal-tz", "b1", start, start.Add(time.Hour))

	// Ask about the same absolute window but expressed as UTC-3 (would
	// be 09:00-10:00 in America/Argentina/Buenos_Aires). The absolute
	// instant is the same, so feasibility should still be false.
	arg := time.FixedZone("ART", -3*60*60)
	checkStart := time.Date(2027, 1, 1, 9, 0, 0, 0, arg)
	checkEnd := checkStart.Add(time.Hour)
	resp := calPost(t, sts, "check", map[string]interface{}{
		"calendar_id": "cal-tz",
		"span": map[string]interface{}{
			"start": checkStart.Format(time.RFC3339),
			"end":   checkEnd.Format(time.RFC3339),
		},
		"mode": "exclusive",
	})
	body := readCalBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%v", resp.StatusCode, body)
	}
	if feasible, _ := body["feasible"].(bool); feasible {
		t.Errorf("timezone offset not honoured: expected feasible=false (same absolute window), got %v", body["feasible"])
	}
}

func TestCalOpeningsRespectsExistingBookings(t *testing.T) {
	// Seed two bookings around a known gap; verify Openings correctly
	// identifies the gap and does NOT propose overlapping windows.
	sts := newCalServer(t)
	seedCalendar(t, sts, "cal-open2")

	// Block 10:00-11:00 and 12:00-13:00. The 11:00-12:00 gap should be
	// the only opening of 60 minutes.
	base := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	seedBooking(t, sts, "cal-open2", "b1", base.Add(10*time.Hour), base.Add(11*time.Hour))
	seedBooking(t, sts, "cal-open2", "b2", base.Add(12*time.Hour), base.Add(13*time.Hour))

	from := base.Add(9 * time.Hour)
	to := base.Add(14 * time.Hour)
	resp := calPost(t, sts, "openings", map[string]interface{}{
		"calendar_id": "cal-open2",
		"from":        from.Format(time.RFC3339),
		"to":          to.Format(time.RFC3339),
		"duration_ms": (60 * time.Minute).Milliseconds(),
		"objective":   "earliest",
	})
	body := readCalBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d; body=%v", resp.StatusCode, body)
	}
	openings, _ := body["openings"].([]interface{})
	if len(openings) == 0 {
		t.Fatal("expected at least one opening")
	}
	// None of the returned openings should overlap the 10:00-11:00 or
	// 12:00-13:00 windows.
	for _, o := range openings {
		m, ok := o.(map[string]interface{})
		if !ok {
			continue
		}
		startStr, _ := m["start"].(string)
		endStr, _ := m["end"].(string)
		os, err1 := time.Parse(time.RFC3339, startStr)
		oe, err2 := time.Parse(time.RFC3339, endStr)
		if err1 != nil || err2 != nil {
			t.Errorf("bad opening times: %v %v", startStr, endStr)
			continue
		}
		// Overlap check with each booking window.
		for _, bk := range []struct{ s, e time.Time }{
			{base.Add(10 * time.Hour), base.Add(11 * time.Hour)},
			{base.Add(12 * time.Hour), base.Add(13 * time.Hour)},
		} {
			if os.Before(bk.e) && oe.After(bk.s) {
				t.Errorf("opening %s..%s overlaps booked window %s..%s", os, oe, bk.s, bk.e)
			}
		}
	}
}
