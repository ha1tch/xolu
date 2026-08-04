// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_dxp_hotel_test.go — the full doctrine worked example (T-107):
// cal+bal+fsm+ts+entity, five participants, two substrates, dispatched
// through the real HTTP API for the first time ever. Everything before
// this file either tested a subset of participants or hand-wired the
// phases directly (pkg/dxp/integration) — this is the actual wave-5
// exit gate the whole multi-session dxp effort has been working
// toward, proven end to end, not just designed.
//
// Also: negative interval-overlap tests through the dxp path
// specifically, not cal's own native admission tests (which already
// exist elsewhere) — the point here is proving dxp's OWN attendance
// mechanism correctly releases every other participant's claim (bal's
// included) when cal refuses a genuine domain-level conflict, in each
// of the real overlap shapes: start, middle, end, and complete.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/cal"
	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/tenant"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// defaultTenantID resolves "default"'s real, auto-registered numeric
// tenant ID. Found the hard way (T-108): tenant ID 0 is reserved by
// pkg/tenant's own registry (the first auto-assigned id is 1, skipping
// 0 explicitly), but seedCalendar/seedBooking (v2_cal_handlers_test.go)
// both hardcode tenant 0 directly against cal.Manager, bypassing the
// registry entirely — correct for cal's own tests, whose URLs
// (calPost) carry no /tenant/{name}/ segment at all and so never
// trigger auto-registration, but wrong the moment "default" is also
// touched via a URL that DOES carry a tenant segment (every dxp/bal/ts
// URL in this test file does) and auto-registers to 1, not 0. This
// resolves the real id explicitly rather than assuming either number.
func defaultTenantID(t *testing.T, env *stdTestServer) tenant.TenantID {
	t.Helper()
	id, err := env.srv.TenantRegistry().GetOrRegister(context.Background(), "default")
	if err != nil {
		t.Fatalf("resolve default tenant id: %v", err)
	}
	return id
}

// seedCalendarForDefaultTenant is seedCalendar, but against "default"'s
// real registered tenant id rather than a hardcoded 0 — see
// defaultTenantID's own doc for why the distinction matters here.
func seedCalendarForDefaultTenant(t *testing.T, env *stdTestServer, calendarID string) {
	t.Helper()
	tid := defaultTenantID(t, env)
	mgr := env.srv.CalManagerForTest()
	_, err := mgr.CreateCalendar(tid, cal.Calendar{
		CalendarID:   calendarID,
		DefaultState: cal.StateBinding,
		MatchPolicy:  cal.ConsiderBinding,
	})
	if err != nil {
		t.Fatalf("seedCalendarForDefaultTenant %q: %v", calendarID, err)
	}
}

// seedBookingForDefaultTenant is seedBooking, against "default"'s real
// registered tenant id — same reasoning as seedCalendarForDefaultTenant.
func seedBookingForDefaultTenant(t *testing.T, env *stdTestServer, calendarID, bookingID string, start, end time.Time) {
	t.Helper()
	tid := defaultTenantID(t, env)
	lc, err := env.srv.CalManagerForTest().CalFor(tid)
	if err != nil {
		t.Fatalf("seedBookingForDefaultTenant: CalFor: %v", err)
	}
	// StateBinding, not the shared seedBooking helper's StateProposed --
	// found the hard way (T-108): spanConflicts checks LiveBookingsOn
	// filtered by PLANE, and a dxp reservation with no explicit State
	// resolves to the calendar's DefaultState (Binding here), which
	// checks the Binding plane (StateBinding/StateHonoured bookings)
	// only -- a Proposed-state booking sits in a different plane
	// entirely and is invisible to a Binding-plane conflict check, by
	// design (proposed/tentative holds don't block other admission
	// attempts the same way a binding one does). The overlap tests in
	// this file need the seeded booking to actually be checked against.
	_, err = lc.Create(cal.Booking{
		BookingID:  bookingID,
		CalendarID: calendarID,
		State:      cal.StateBinding,
		Span: cal.Span{
			Start: ot.FromTime(start),
			End:   ot.FromTime(end),
		},
		Mode:   cal.ModeExclusive,
		Bearer: 1,
	})
	if err != nil {
		t.Fatalf("seedBookingForDefaultTenant %q: %v", bookingID, err)
	}
}

// newFullDxpServer enables every subsystem a dxp def can name a
// participant from: bal, cal, ts (fsm/entity need only APIV2Enabled,
// already on via newMetaServer).
func newFullDxpServer(t *testing.T) *stdTestServer {
	return newMetaServer(t, func(cfg *config.Config) {
		cfg.BalEnabled = true
		cfg.CalEnabled = true
		cfg.TimeseriesEnabled = true
		cfg.TSMemtableSize = 4 * 1024 * 1024
		cfg.TSBlockSize = 4096
		cfg.TSCompression = "snappy"
		cfg.TSL0CompactionThreshold = 4
		cfg.TSMaxOpenFiles = 50
		cfg.TSDefaultRetentionDays = 90
		cfg.TSCompactionIntervalSecs = 3600
	})
}

func hotelBookingFsmSpec() map[string]interface{} {
	return map[string]interface{}{
		"name":        "HotelBooking",
		"initial":     "reserved",
		"determinism": "strict",
		"states": map[string]interface{}{
			"reserved":  map[string]interface{}{"terminal": false},
			"confirmed": map[string]interface{}{"terminal": true},
		},
		"transitions": []map[string]interface{}{
			{"from": "reserved", "input": "confirm", "to": "confirmed"},
		},
	}
}

func createHotelBookingMachine(t *testing.T, env *stdTestServer) int64 {
	t.Helper()
	status, defResp := doJSONRequest(t, "POST", fsmDefURL(env, ""), hotelBookingFsmSpec())
	if status != http.StatusCreated {
		t.Fatalf("create HotelBooking fsm def: want 201, got %d %v", status, defResp)
	}
	defID := int64(defResp["id"].(float64))
	status, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
	if status != http.StatusCreated {
		t.Fatalf("create HotelBooking fsm machine: want 201, got %d %v", status, mResp)
	}
	return int64(mResp["id"].(float64))
}

// TestDxpTxnAPI_Create_FullHotelExample_FiveParticipants_AllCommit is
// the headline proof: cal (room booking) + bal (payment) + fsm
// (booking confirmation) + ts (audit event) + entity (guest record
// update), five participants spanning both substrates, dispatched
// together through dispatchPhased and checked against all five real
// side effects independently — not the coordinator's own say-so for
// any of them.
func TestDxpTxnAPI_Create_FullHotelExample_FiveParticipants_AllCommit(t *testing.T) {
	env := newFullDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	seedCalendarForDefaultTenant(t, env, "room7")
	machineID := createHotelBookingMachine(t, env)
	entityID := createMetaEntity(t, env)
	provisionTsAndDefineTimeline(t, env, 1, 1)

	start := time.Date(2027, 3, 10, 14, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)

	def := map[string]interface{}{
		"name":    "hotel_reserve_full",
		"pattern": "3ps",
		"bindings_schema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"amount": map[string]interface{}{"type": "string"},
			},
			"required": []interface{}{"amount"},
		},
		"participants": []map[string]interface{}{
			{"id": "room", "primitive": "cal", "op": "book",
				"params": map[string]interface{}{
					"calendar":   "room7",
					"booking_id": "hotel-full-1",
					"span":       map[string]interface{}{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
					"mode":       "exclusive",
					"bearer":     1,
				}},
			{"id": "payment", "primitive": "bal", "op": "transfer",
				"params": map[string]interface{}{
					"from": "~in", "to": "acct",
					"amount": map[string]interface{}{"$ref": "amount"},
				}},
			{"id": "booking", "primitive": "fsm", "op": "transition",
				"params": map[string]interface{}{
					"machine_id": machineID,
					"input":      "confirm",
				}},
			{"id": "audit", "primitive": "ts", "op": "append",
				"params": map[string]interface{}{
					"timeline":     1,
					"dims":         []interface{}{1},
					"time_unix_ns": start.UnixNano(),
					"nums":         []interface{}{1.0},
				}},
			{"id": "guest", "primitive": "entity", "op": "update",
				"params": map[string]interface{}{
					"entity": "assets",
					"id":     entityID,
					"data":   map[string]interface{}{"name": "confirmed-guest", "type": "sensor"},
				}},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT2M"},
	}

	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	analysis := defResp["analysis"].(map[string]interface{})
	if analysis["engine_homogeneous"] != false {
		t.Errorf("expected engine_homogeneous: false (ts is pebble), got %v", analysis["engine_homogeneous"])
	}

	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{
		"def_id":   defResp["id"],
		"bindings": map[string]interface{}{"amount": "150"},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("expected committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}
	if ct, ok := resp["committed_through"].(float64); !ok || ct != 5 {
		t.Fatalf("expected committed_through 5, got %v", resp["committed_through"])
	}

	// All five real side effects, each checked independently through
	// its own primitive's own real API.
	status, balResp := doJSONRequest(t, "GET", balURL(env, "/balance?account=acct"), nil)
	if status != http.StatusOK || balResp["value"] != "150" {
		t.Errorf("bal leg: want balance 150, got %d %v", status, balResp)
	}

	// cal leg: verified against cal_bookings (H1, guard-bearing) directly
	// via raw SQL, NOT /cal/check -- checked directly, not assumed:
	// handleCalCheck queries s.calMgr.IndexFor(tenantID), the H3 Pebble
	// occupancy index, and dxp's own cal adapter only ever writes H1
	// (dxpEngineOf["cal"] = "sql" covers H1 alone, by design -- H3 is
	// deliberately outside dxp's reach, T-83, still open). /cal/check
	// against a dxp-committed booking genuinely returns feasible=true
	// here -- confirmed empirically, not a test bug -- because H3 was
	// never told about the commit. This is T-83's own already-registered
	// gap made concrete, not a new one; H1 is the actual source of
	// truth and what a dxp-driven booking is supposed to (and does)
	// update, so that is what this test checks instead.
	wdp, ok := env.store.(storage.WriterDBProvider)
	if !ok {
		t.Fatalf("cal leg: test store does not implement storage.WriterDBProvider")
	}
	tid := defaultTenantID(t, env)
	var calState string
	err := wdp.WriterDB().QueryRowContext(context.Background(),
		`SELECT state FROM cal_bookings WHERE tenant_id = ? AND calendar_id = ? AND booking_id = ?`,
		tid, "room7", "hotel-full-1").Scan(&calState)
	if err != nil {
		t.Fatalf("cal leg: querying cal_bookings for hotel-full-1: %v", err)
	}
	if calState != string(cal.StateBinding) {
		t.Errorf("cal leg: want state %q, got %q", cal.StateBinding, calState)
	}

	status, fsmResp := doJSONRequest(t, "GET", fmt.Sprintf("%s/api/v2/tenant/default/fsm/machine/%d", env.ts.URL, machineID), nil)
	if status != http.StatusOK || fsmResp["state"] != "confirmed" {
		t.Errorf("fsm leg: want state confirmed, got %d %v", status, fsmResp)
	}

	status, evResp := doJSONRequest(t, "GET",
		tsURLFor(env, "/events?timeline=1&dims=1&from=2027-01-01T00:00:00Z&to=2027-12-31T00:00:00Z&limit=10"), nil)
	events, _ := evResp["events"].([]interface{})
	if status != http.StatusOK || len(events) != 1 {
		t.Errorf("ts leg: want exactly 1 event, got %d %v", status, evResp)
	}

	status, entResp := doJSONRequest(t, "GET", fmt.Sprintf("%s/api/v1/tenant/default/assets/%d", env.ts.URL, entityID), nil)
	if status != http.StatusOK || entResp["name"] != "confirmed-guest" {
		t.Errorf("entity leg: want name confirmed-guest, got %d %v", status, entResp)
	}
}

// ─── Negative: interval-overlap admission through the dxp path ────────────

// overlapTwoParticipantDef pairs a bal payment with a cal room
// reservation for the given span -- bal's own Reserve always succeeds
// against a funded account; cal's is the one expected to refuse when
// the span conflicts with an existing real booking. Proves dxp's own
// attendance mechanism releases bal's already-held claim too, not
// just that cal refuses on its own.
func overlapTwoParticipantDef(start, end time.Time) map[string]interface{} {
	return map[string]interface{}{
		"name":    "overlap_probe",
		"pattern": "3ps",
		"bindings_schema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"amount": map[string]interface{}{"type": "string"}},
			"required":   []interface{}{"amount"},
		},
		"participants": []map[string]interface{}{
			{"id": "payment", "primitive": "bal", "op": "transfer",
				"params": map[string]interface{}{
					"from": "~in", "to": "acct",
					"amount": map[string]interface{}{"$ref": "amount"},
				}},
			{"id": "room", "primitive": "cal", "op": "book",
				"params": map[string]interface{}{
					"calendar":   "room7",
					"booking_id": "probe-1",
					"span":       map[string]interface{}{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
					"mode":       "exclusive",
					"bearer":     1,
				}},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT2M"},
	}
}

// dispatchOverlapProbe registers overlapTwoParticipantDef and
// dispatches it once, returning the /dxp/txn response.
func dispatchOverlapProbe(t *testing.T, env *stdTestServer, start, end time.Time) map[string]interface{} {
	t.Helper()
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), overlapTwoParticipantDef(start, end))
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{
		"def_id":   defResp["id"],
		"bindings": map[string]interface{}{"amount": "50"},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	return resp
}

// assertOverlapRefused checks the instance was released (refused at
// attendance, before any Execute) and that bal's own already-held
// claim was genuinely released too -- balance stays 0, not partially
// applied.
func assertOverlapRefused(t *testing.T, env *stdTestServer, resp map[string]interface{}, label string) {
	t.Helper()
	if resp["status"] != "released" {
		t.Fatalf("%s: expected released (refused at attendance), got %v (reason: %v)", label, resp["status"], resp["reason"])
	}
	status, balResp := doJSONRequest(t, "GET", balURL(env, "/balance?account=acct"), nil)
	if status != http.StatusOK || balResp["value"] != "0" {
		t.Errorf("%s: bal leg should never have committed, want balance 0, got %d %v", label, status, balResp)
	}
}

// setupOverlapCalendar seeds room7 and a real, ordinary (non-dxp)
// exclusive booking from 14:00 to 18:00 on a fixed day -- the fixed
// point every overlap variant below is measured against.
func setupOverlapCalendar(t *testing.T, env *stdTestServer) (existingStart, existingEnd time.Time) {
	t.Helper()
	seedCalendarForDefaultTenant(t, env, "room7")
	existingStart = time.Date(2027, 5, 1, 14, 0, 0, 0, time.UTC)
	existingEnd = time.Date(2027, 5, 1, 18, 0, 0, 0, time.UTC)
	seedBookingForDefaultTenant(t, env, "room7", "existing-1", existingStart, existingEnd)
	return existingStart, existingEnd
}

// TestDxpTxnAPI_Overlap_StartOfExisting -- the new span starts before
// and ends inside the existing 14:00-18:00 booking (12:00-15:00).
func TestDxpTxnAPI_Overlap_StartOfExisting(t *testing.T) {
	env := newFullDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	existingStart, _ := setupOverlapCalendar(t, env)

	resp := dispatchOverlapProbe(t, env, existingStart.Add(-2*time.Hour), existingStart.Add(1*time.Hour))
	assertOverlapRefused(t, env, resp, "start-overlap")
}

// TestDxpTxnAPI_Overlap_MiddleOfExisting -- the new span is entirely
// inside the existing booking (15:00-16:00, inside 14:00-18:00).
func TestDxpTxnAPI_Overlap_MiddleOfExisting(t *testing.T) {
	env := newFullDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	existingStart, _ := setupOverlapCalendar(t, env)

	resp := dispatchOverlapProbe(t, env, existingStart.Add(1*time.Hour), existingStart.Add(2*time.Hour))
	assertOverlapRefused(t, env, resp, "middle-overlap")
}

// TestDxpTxnAPI_Overlap_EndOfExisting -- the new span starts inside
// and ends after the existing booking (17:00-20:00, existing ends 18:00).
func TestDxpTxnAPI_Overlap_EndOfExisting(t *testing.T) {
	env := newFullDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	_, existingEnd := setupOverlapCalendar(t, env)

	resp := dispatchOverlapProbe(t, env, existingEnd.Add(-1*time.Hour), existingEnd.Add(2*time.Hour))
	assertOverlapRefused(t, env, resp, "end-overlap")
}

// TestDxpTxnAPI_Overlap_CompletelyContainsExisting -- the new span
// fully subsumes the existing booking on both sides (10:00-22:00,
// existing is 14:00-18:00).
func TestDxpTxnAPI_Overlap_CompletelyContainsExisting(t *testing.T) {
	env := newFullDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	existingStart, existingEnd := setupOverlapCalendar(t, env)

	resp := dispatchOverlapProbe(t, env, existingStart.Add(-4*time.Hour), existingEnd.Add(4*time.Hour))
	assertOverlapRefused(t, env, resp, "complete-overlap")
}

// TestDxpTxnAPI_Overlap_ExistingCompletelyContainsNew -- the inverse
// of the previous case: the new span sits entirely inside the
// existing one with margin on both sides (15:30-16:30, inside the
// existing 14:00-18:00), a distinct overlap shape from "middle" only
// in how tight the margins are -- included for completeness since it
// is a geometrically different case from touching either boundary.
func TestDxpTxnAPI_Overlap_ExistingCompletelyContainsNew(t *testing.T) {
	env := newFullDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	existingStart, _ := setupOverlapCalendar(t, env)

	resp := dispatchOverlapProbe(t, env, existingStart.Add(90*time.Minute), existingStart.Add(150*time.Minute))
	assertOverlapRefused(t, env, resp, "existing-contains-new")
}

// TestDxpTxnAPI_Overlap_AdjacentNonOverlapping_Commits is the
// contrast case: a span immediately adjacent to the existing booking
// (18:00-19:00, existing ends exactly at 18:00) does NOT conflict --
// half-open interval semantics, checked as real behaviour through the
// dxp path, not assumed. Proves the refusals above are about genuine
// overlap, not merely "any span near an existing booking".
func TestDxpTxnAPI_Overlap_AdjacentNonOverlapping_Commits(t *testing.T) {
	env := newFullDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	_, existingEnd := setupOverlapCalendar(t, env)

	resp := dispatchOverlapProbe(t, env, existingEnd, existingEnd.Add(1*time.Hour))
	if resp["status"] != "committed" {
		t.Fatalf("adjacent, non-overlapping span: expected committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}
	if ct, ok := resp["committed_through"].(float64); !ok || ct != 2 {
		t.Errorf("expected committed_through 2, got %v", resp["committed_through"])
	}
}

// ─── PostCommit (T-108): the actual proof, not just the wiring ────────────

// TestDxpTxnAPI_PostCommit_CalOccupancyIndexReflectsCommittedBooking is
// the test T-108 exists for: direct instruction was "/cal/check and
// /cal/openings MUST become correct for dxp-driven bookings" — this
// dispatches a real dxp cal booking (bal+cal, all-SQL, so this goes
// through dispatchCollapsed specifically — PostCommit must fire from
// its own success path, not just phased's, for this test to mean
// anything) and checks BOTH read paths afterward. Before PostCommit
// existed, /cal/check returned feasible:true for this exact span —
// T-107 found that empirically, not assumed from the design docs.
// This test is what must never let that regress.
func TestDxpTxnAPI_PostCommit_CalOccupancyIndexReflectsCommittedBooking(t *testing.T) {
	env := newFullDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	seedCalendarForDefaultTenant(t, env, "room9")

	start := time.Date(2027, 6, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)

	def := map[string]interface{}{
		"name":    "postcommit_probe",
		"pattern": "3ps",
		"bindings_schema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"amount": map[string]interface{}{"type": "string"}},
			"required":   []interface{}{"amount"},
		},
		"participants": []map[string]interface{}{
			{"id": "payment", "primitive": "bal", "op": "transfer",
				"params": map[string]interface{}{
					"from": "~in", "to": "acct",
					"amount": map[string]interface{}{"$ref": "amount"},
				}},
			{"id": "room", "primitive": "cal", "op": "book",
				"params": map[string]interface{}{
					"calendar":   "room9",
					"booking_id": "postcommit-1",
					"span":       map[string]interface{}{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
					"mode":       "exclusive",
					"bearer":     1,
				}},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	// bal+cal are both "sql" -- this def must be collapse-eligible, so
	// dispatchCollapsed's own PostCommit call is what this test
	// actually exercises, not dispatchPhased's.
	analysis := defResp["analysis"].(map[string]interface{})
	if analysis["engine_homogeneous"] != true {
		t.Fatalf("expected engine_homogeneous: true (bal+cal are both sql), got %v", analysis["engine_homogeneous"])
	}

	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{
		"def_id":   defResp["id"],
		"bindings": map[string]interface{}{"amount": "60"},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("expected committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}

	// The actual proof, path 1: /cal/check reads H3 directly. This is
	// the exact call T-107 found returning feasible:true for a
	// genuinely committed dxp booking, before PostCommit existed.
	checkStatus, checkResp := doJSONRequest(t, "POST", fmt.Sprintf("%s/api/v2/tenant/default/cal/check", env.ts.URL), map[string]interface{}{
		"calendar_id": "room9",
		"span":        map[string]interface{}{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
		"mode":        "exclusive",
	})
	if checkStatus != http.StatusOK {
		t.Fatalf("/cal/check: want 200, got %d %v", checkStatus, checkResp)
	}
	if feasible, _ := checkResp["feasible"].(bool); feasible {
		t.Fatalf("/cal/check: want feasible=false (H3 must reflect the committed dxp booking), got %v", checkResp)
	}

	// The actual proof, path 2: /cal/openings, a DIFFERENT read of the
	// same underlying occupancy (checked separately, not assumed to
	// agree with /check -- T-29's own regression guard exists
	// precisely because that agreement is a property to prove, not a
	// given). Request openings for exactly the booked window at
	// exactly the booked duration: if H3 correctly shows the booking,
	// there must be zero openings that fit, since the whole window is
	// occupied by the one booking.
	openStatus, openResp := doJSONRequest(t, "POST", fmt.Sprintf("%s/api/v2/tenant/default/cal/openings", env.ts.URL), map[string]interface{}{
		"calendar_id": "room9",
		"from":        start.Format(time.RFC3339),
		"to":          end.Format(time.RFC3339),
		"duration_ms": end.Sub(start).Milliseconds(),
		"objective":   "earliest",
	})
	if openStatus != http.StatusOK {
		t.Fatalf("/cal/openings: want 200, got %d %v", openStatus, openResp)
	}
	openings, _ := openResp["openings"].([]interface{})
	if len(openings) != 0 {
		t.Errorf("/cal/openings: want zero openings inside the fully-booked window, got %v", openResp["openings"])
	}
}

// TestDxpTxnAPI_PostCommit_CalOccupancyIndex_PhasedPath is the same
// proof as above, but forced through dispatchPhased specifically (cal
// paired with ts, engine_homogeneous:false) rather than
// dispatchCollapsed — a genuinely different code path with its own
// separate postCommitAll call. Both paths need their own proof; one
// passing was never evidence for the other.
func TestDxpTxnAPI_PostCommit_CalOccupancyIndex_PhasedPath(t *testing.T) {
	env := newFullDxpServer(t)
	seedCalendarForDefaultTenant(t, env, "room10")
	provisionTsAndDefineTimeline(t, env, 1, 1)

	start := time.Date(2027, 6, 2, 9, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)

	def := map[string]interface{}{
		"name":    "postcommit_probe_phased",
		"pattern": "3ps",
		"participants": []map[string]interface{}{
			{"id": "room", "primitive": "cal", "op": "book",
				"params": map[string]interface{}{
					"calendar":   "room10",
					"booking_id": "postcommit-2",
					"span":       map[string]interface{}{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
					"mode":       "exclusive",
					"bearer":     1,
				}},
			{"id": "audit", "primitive": "ts", "op": "append",
				"params": map[string]interface{}{
					"timeline":     1,
					"dims":         []interface{}{1},
					"time_unix_ns": start.UnixNano(),
					"nums":         []interface{}{1.0},
				}},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	analysis := defResp["analysis"].(map[string]interface{})
	if analysis["engine_homogeneous"] != false {
		t.Fatalf("expected engine_homogeneous: false (ts is pebble) so this exercises dispatchPhased, got %v", analysis["engine_homogeneous"])
	}

	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{
		"def_id": defResp["id"],
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("expected committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}

	checkStatus, checkResp := doJSONRequest(t, "POST", fmt.Sprintf("%s/api/v2/tenant/default/cal/check", env.ts.URL), map[string]interface{}{
		"calendar_id": "room10",
		"span":        map[string]interface{}{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
		"mode":        "exclusive",
	})
	if checkStatus != http.StatusOK {
		t.Fatalf("/cal/check: want 200, got %d %v", checkStatus, checkResp)
	}
	if feasible, _ := checkResp["feasible"].(bool); feasible {
		t.Fatalf("/cal/check: want feasible=false after a phased-path commit, got %v", checkResp)
	}
}

// TestDxpTxnAPI_PostCommit_BalRollupReflectsCommittedTransfer proves
// T-83's other named consumer of the PostCommit mechanism (T-108 built
// the mechanism and wired cal; this wires bal onto it): after a
// dxp-driven transfer commits, /bal/asof -- which reads the derived
// rollup plane exclusively (@B05), never the authoritative journal --
// must reflect it. Forces dispatchCollapsed (bal+cal are both sql).
func TestDxpTxnAPI_PostCommit_BalRollupReflectsCommittedTransfer(t *testing.T) {
	env := newFullDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	seedCalendarForDefaultTenant(t, env, "room11")

	start := time.Date(2027, 6, 3, 9, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)

	def := map[string]interface{}{
		"name":    "postcommit_bal_probe",
		"pattern": "3ps",
		"bindings_schema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"amount": map[string]interface{}{"type": "string"}},
			"required":   []interface{}{"amount"},
		},
		"participants": []map[string]interface{}{
			{"id": "payment", "primitive": "bal", "op": "transfer",
				"params": map[string]interface{}{
					"from": "~in", "to": "acct",
					"amount": map[string]interface{}{"$ref": "amount"},
				}},
			{"id": "room", "primitive": "cal", "op": "book",
				"params": map[string]interface{}{
					"calendar":   "room11",
					"booking_id": "postcommit-bal-1",
					"span":       map[string]interface{}{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
					"mode":       "exclusive",
					"bearer":     1,
				}},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	analysis := defResp["analysis"].(map[string]interface{})
	if analysis["engine_homogeneous"] != true {
		t.Fatalf("expected engine_homogeneous: true (bal+cal are both sql), got %v", analysis["engine_homogeneous"])
	}

	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{
		"def_id":   defResp["id"],
		"bindings": map[string]interface{}{"amount": "75"},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("expected committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}

	asOfStatus, asOfResp := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v2/tenant/default/bal/asof?account=acct&at=%s",
			env.ts.URL, time.Now().Add(time.Hour).Format(time.RFC3339)), nil)
	if asOfStatus != http.StatusOK {
		t.Fatalf("/bal/asof: want 200, got %d %v", asOfStatus, asOfResp)
	}
	if minor, _ := asOfResp["minor"].(float64); minor != 75 {
		t.Fatalf("/bal/asof account=acct: want minor=75 (rollup must reflect the committed dxp transfer), got %v", asOfResp)
	}
}

// TestDxpTxnAPI_PostCommit_BalRollup_PhasedPath is the same proof as
// above, forced through dispatchPhased specifically (bal+ts,
// engine_homogeneous:false) rather than dispatchCollapsed -- a
// genuinely different code path with its own separate postCommitAll
// call. Matches cal's own two-path precedent: one path passing was
// never evidence for the other.
func TestDxpTxnAPI_PostCommit_BalRollup_PhasedPath(t *testing.T) {
	env := newFullDxpServer(t)
	defineSimplePaymentAccounts(t, env)
	provisionTsAndDefineTimeline(t, env, 2, 1)

	start := time.Date(2027, 6, 4, 9, 0, 0, 0, time.UTC)

	def := map[string]interface{}{
		"name":    "postcommit_bal_probe_phased",
		"pattern": "3ps",
		"bindings_schema": map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{"amount": map[string]interface{}{"type": "string"}},
			"required":   []interface{}{"amount"},
		},
		"participants": []map[string]interface{}{
			{"id": "payment", "primitive": "bal", "op": "transfer",
				"params": map[string]interface{}{
					"from": "~in", "to": "acct",
					"amount": map[string]interface{}{"$ref": "amount"},
				}},
			{"id": "audit", "primitive": "ts", "op": "append",
				"params": map[string]interface{}{
					"timeline":     2,
					"dims":         []interface{}{1},
					"time_unix_ns": start.UnixNano(),
					"nums":         []interface{}{1.0},
				}},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	analysis := defResp["analysis"].(map[string]interface{})
	if analysis["engine_homogeneous"] != false {
		t.Fatalf("expected engine_homogeneous: false (ts is pebble) so this exercises dispatchPhased, got %v", analysis["engine_homogeneous"])
	}

	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{
		"def_id":   defResp["id"],
		"bindings": map[string]interface{}{"amount": "40"},
	})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("expected committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}

	asOfStatus, asOfResp := doJSONRequest(t, "GET",
		fmt.Sprintf("%s/api/v2/tenant/default/bal/asof?account=acct&at=%s",
			env.ts.URL, time.Now().Add(time.Hour).Format(time.RFC3339)), nil)
	if asOfStatus != http.StatusOK {
		t.Fatalf("/bal/asof: want 200, got %d %v", asOfStatus, asOfResp)
	}
	if minor, _ := asOfResp["minor"].(float64); minor != 40 {
		t.Fatalf("/bal/asof account=acct: want minor=40 after a phased-path commit (rollup must reflect it), got %v", asOfResp)
	}
}
