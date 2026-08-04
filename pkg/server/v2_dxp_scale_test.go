// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_dxp_scale_test.go — adversarial dxp tests at scale (T-109):
// many participants of the SAME primitive in one instance (several
// bal legs, several cal bookings, several fsm transitions, several
// entity creates), and repeated multi-substrate combinations mixing
// several of each across both engines. Everything before this file
// tested at most two or five distinct participants, each a different
// primitive — this tests N-of-one-primitive specifically, which
// stresses a different part of the coordinator: attendance/Reserve
// looping, concurrent Execute+Commit fan-out, and (adversarially)
// whether a single failure among many correctly aborts or tears the
// whole set, not just a pair.

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/cal"
	"github.com/ha1tch/xolu/pkg/storage"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// defineNBalAccounts creates one funding account (~in) and n
// independent destination accounts (acct1..acctN), returning their
// names. Independent destinations avoid any shared-resource admission
// interaction between legs — each leg's own claim is genuinely
// unrelated to the others', which is the point: N independent bal
// participants, not N contending ones.
func defineNBalAccounts(t *testing.T, env *stdTestServer, n int) []string {
	t.Helper()
	status, resp := doJSONRequest(t, "POST", balURL(env, "/def"),
		map[string]interface{}{"account_id": "~in", "unit": "unit", "scale": 0, "floor": "-1000000000"})
	if status != http.StatusCreated {
		t.Fatalf("define ~in: want 201, got %d %v", status, resp)
	}
	names := make([]string, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("acct%d", i)
		status, resp := doJSONRequest(t, "POST", balURL(env, "/def"),
			map[string]interface{}{"account_id": name, "unit": "unit", "scale": 0})
		if status != http.StatusCreated {
			t.Fatalf("define %s: want 201, got %d %v", name, status, resp)
		}
		names[i] = name
	}
	return names
}

// seedNCalendarsForDefaultTenant creates n independent calendars
// (room0..roomN-1), returning their ids. Independent calendars, same
// reasoning as defineNBalAccounts: N genuinely unrelated cal
// participants.
func seedNCalendarsForDefaultTenant(t *testing.T, env *stdTestServer, n int) []string {
	t.Helper()
	names := make([]string, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("scaleroom%d", i)
		seedCalendarForDefaultTenant(t, env, name)
		names[i] = name
	}
	return names
}

// createNHotelBookingMachines creates n independent fsm machines from
// hotelBookingFsmSpec, returning their ids. The def is created once;
// only the machine (the actual per-instance state) is created n times
// — a def registering the same spec twice is unnecessary and this
// mirrors how a real caller would use one definition for many bookings.
func createNHotelBookingMachines(t *testing.T, env *stdTestServer, n int) []int64 {
	t.Helper()
	status, defResp := doJSONRequest(t, "POST", fsmDefURL(env, ""), hotelBookingFsmSpec())
	if status != http.StatusCreated {
		t.Fatalf("create HotelBooking fsm def: want 201, got %d %v", status, defResp)
	}
	defID := int64(defResp["id"].(float64))
	ids := make([]int64, n)
	for i := 0; i < n; i++ {
		status, mResp := doJSONRequest(t, "POST", fsmMachineURL(env, ""), map[string]interface{}{"definition": defID})
		if status != http.StatusCreated {
			t.Fatalf("create HotelBooking fsm machine %d: want 201, got %d %v", i, status, mResp)
		}
		ids[i] = int64(mResp["id"].(float64))
	}
	return ids
}

// ─── Scale, happy path: many participants of one primitive ────────────────

// TestDxpTxnAPI_Scale_FiveBalLegs_AllCommit dispatches one instance
// with five independent bal transfer participants, all landing in the
// same collapsed transaction.
func TestDxpTxnAPI_Scale_FiveBalLegs_AllCommit(t *testing.T) {
	const n = 5
	env := newFullDxpServer(t)
	accounts := defineNBalAccounts(t, env, n)

	participants := make([]map[string]interface{}, n)
	for i, acct := range accounts {
		participants[i] = map[string]interface{}{
			"id": fmt.Sprintf("payment%d", i), "primitive": "bal", "op": "transfer",
			"params": map[string]interface{}{"from": "~in", "to": acct, "amount": fmt.Sprintf("%d", 10+i)},
		}
	}
	def := map[string]interface{}{
		"name": "scale_five_bal", "pattern": "3ps",
		"participants": participants,
		"phase_ttl":    map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{"def_id": defResp["id"]})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("expected committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}
	if ct, ok := resp["committed_through"].(float64); !ok || ct != n {
		t.Fatalf("expected committed_through %d, got %v", n, resp["committed_through"])
	}
	for i, acct := range accounts {
		status, balResp := doJSONRequest(t, "GET", balURL(env, "/balance?account="+acct), nil)
		want := fmt.Sprintf("%d", 10+i)
		if status != http.StatusOK || balResp["value"] != want {
			t.Errorf("account %s: want balance %s, got %d %v", acct, want, status, balResp)
		}
	}
}

// TestDxpTxnAPI_Scale_FourCalBookings_AllCommit books four independent
// calendars in one instance.
func TestDxpTxnAPI_Scale_FourCalBookings_AllCommit(t *testing.T) {
	const n = 4
	env := newFullDxpServer(t)
	rooms := seedNCalendarsForDefaultTenant(t, env, n)
	start := time.Date(2027, 7, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	participants := make([]map[string]interface{}, n)
	for i, room := range rooms {
		participants[i] = map[string]interface{}{
			"id": fmt.Sprintf("room%d", i), "primitive": "cal", "op": "book",
			"params": map[string]interface{}{
				"calendar": room, "booking_id": fmt.Sprintf("scale-%d", i),
				"span": map[string]interface{}{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
				"mode": "exclusive", "bearer": 1,
			},
		}
	}
	def := map[string]interface{}{
		"name": "scale_four_cal", "pattern": "3ps",
		"participants": participants,
		"phase_ttl":    map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{"def_id": defResp["id"]})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("expected committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}
	if ct, ok := resp["committed_through"].(float64); !ok || ct != n {
		t.Fatalf("expected committed_through %d, got %v", n, resp["committed_through"])
	}
	for _, room := range rooms {
		checkStatus, checkResp := doJSONRequest(t, "POST", fmt.Sprintf("%s/api/v2/tenant/default/cal/check", env.ts.URL), map[string]interface{}{
			"calendar_id": room,
			"span":        map[string]interface{}{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
			"mode":        "exclusive",
		})
		if checkStatus != http.StatusOK {
			t.Fatalf("/cal/check %s: want 200, got %d %v", room, checkStatus, checkResp)
		}
		if feasible, _ := checkResp["feasible"].(bool); feasible {
			t.Errorf("calendar %s: want occupied (feasible=false), got %v", room, checkResp)
		}
	}
}

// TestDxpTxnAPI_Scale_ThreeFsmTransitions_AllCommit transitions three
// independent fsm machines in one instance.
func TestDxpTxnAPI_Scale_ThreeFsmTransitions_AllCommit(t *testing.T) {
	const n = 3
	env := newFullDxpServer(t)
	machineIDs := createNHotelBookingMachines(t, env, n)

	participants := make([]map[string]interface{}, n)
	for i, id := range machineIDs {
		participants[i] = map[string]interface{}{
			"id": fmt.Sprintf("booking%d", i), "primitive": "fsm", "op": "transition",
			"params": map[string]interface{}{"machine_id": id, "input": "confirm"},
		}
	}
	def := map[string]interface{}{
		"name": "scale_three_fsm", "pattern": "3ps",
		"participants": participants,
		"phase_ttl":    map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{"def_id": defResp["id"]})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("expected committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}
	if ct, ok := resp["committed_through"].(float64); !ok || ct != n {
		t.Fatalf("expected committed_through %d, got %v", n, resp["committed_through"])
	}
	for _, id := range machineIDs {
		status, fsmResp := doJSONRequest(t, "GET", fmt.Sprintf("%s/api/v2/tenant/default/fsm/machine/%d", env.ts.URL, id), nil)
		if status != http.StatusOK || fsmResp["state"] != "confirmed" {
			t.Errorf("machine %d: want state confirmed, got %d %v", id, status, fsmResp)
		}
	}
}

// TestDxpTxnAPI_Scale_SixEntityCreates_AllCommit creates six
// independent entity rows in one instance — "create" needs no
// pre-existing id, unlike "update", so this needs no separate seeding
// step beyond the schema directories newV2Server already creates.
func TestDxpTxnAPI_Scale_SixEntityCreates_AllCommit(t *testing.T) {
	const n = 6
	env := newFullDxpServer(t)

	participants := make([]map[string]interface{}, n)
	for i := 0; i < n; i++ {
		participants[i] = map[string]interface{}{
			"id": fmt.Sprintf("guest%d", i), "primitive": "entity", "op": "create",
			"params": map[string]interface{}{
				"entity": "assets",
				"data":   map[string]interface{}{"name": fmt.Sprintf("guest-%d", i), "type": "sensor"},
			},
		}
	}
	def := map[string]interface{}{
		"name": "scale_six_entity", "pattern": "3ps",
		"participants": participants,
		"phase_ttl":    map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{"def_id": defResp["id"]})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("expected committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}
	if ct, ok := resp["committed_through"].(float64); !ok || ct != n {
		t.Fatalf("expected committed_through %d, got %v", n, resp["committed_through"])
	}
}

// ─── Kitchen sink: many of everything, repeated multi-substrate ───────────

// TestDxpTxnAPI_Scale_KitchenSink_ManyOfEverything_AllCommit is one
// instance with multiple participants of every primitive at once —
// three bal legs, two cal bookings, two fsm transitions, two entity
// creates, two ts appends — eleven participants total, spanning both
// substrates (ts forces dispatchPhased). Every earlier test in this
// file varied one axis at a time (many-of-one-primitive, or five
// distinct primitives once each); this is the first test to combine
// both: many AND multi-substrate AND repeated, together.
func TestDxpTxnAPI_Scale_KitchenSink_ManyOfEverything_AllCommit(t *testing.T) {
	env := newFullDxpServer(t)
	accounts := defineNBalAccounts(t, env, 3)
	rooms := seedNCalendarsForDefaultTenant(t, env, 2)
	machineIDs := createNHotelBookingMachines(t, env, 2)
	provisionTsAndDefineTimeline(t, env, 1, 1)

	start := time.Date(2027, 8, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	var participants []map[string]interface{}
	for i, acct := range accounts {
		participants = append(participants, map[string]interface{}{
			"id": fmt.Sprintf("bal%d", i), "primitive": "bal", "op": "transfer",
			"params": map[string]interface{}{"from": "~in", "to": acct, "amount": fmt.Sprintf("%d", 5+i)},
		})
	}
	for i, room := range rooms {
		participants = append(participants, map[string]interface{}{
			"id": fmt.Sprintf("cal%d", i), "primitive": "cal", "op": "book",
			"params": map[string]interface{}{
				"calendar": room, "booking_id": fmt.Sprintf("kitchen-%d", i),
				"span": map[string]interface{}{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
				"mode": "exclusive", "bearer": 1,
			},
		})
	}
	for i, mid := range machineIDs {
		participants = append(participants, map[string]interface{}{
			"id": fmt.Sprintf("fsm%d", i), "primitive": "fsm", "op": "transition",
			"params": map[string]interface{}{"machine_id": mid, "input": "confirm"},
		})
	}
	for i := 0; i < 2; i++ {
		participants = append(participants, map[string]interface{}{
			"id": fmt.Sprintf("entity%d", i), "primitive": "entity", "op": "create",
			"params": map[string]interface{}{
				"entity": "assets",
				"data":   map[string]interface{}{"name": fmt.Sprintf("kitchen-guest-%d", i), "type": "sensor"},
			},
		})
	}
	for i := 0; i < 2; i++ {
		participants = append(participants, map[string]interface{}{
			"id": fmt.Sprintf("ts%d", i), "primitive": "ts", "op": "append",
			"params": map[string]interface{}{
				"timeline": 1, "dims": []interface{}{1},
				"time_unix_ns": start.Add(time.Duration(i) * time.Minute).UnixNano(),
				"nums":         []interface{}{float64(i)},
			},
		})
	}

	def := map[string]interface{}{
		"name": "kitchen_sink", "pattern": "3ps",
		"participants": participants,
		"phase_ttl":    map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	analysis := defResp["analysis"].(map[string]interface{})
	if analysis["engine_homogeneous"] != false {
		t.Fatalf("expected engine_homogeneous: false (ts is pebble), got %v", analysis["engine_homogeneous"])
	}

	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{"def_id": defResp["id"]})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "committed" {
		t.Fatalf("expected committed, got %v (reason: %v)", resp["status"], resp["reason"])
	}
	const total = 11
	if ct, ok := resp["committed_through"].(float64); !ok || ct != total {
		t.Fatalf("expected committed_through %d, got %v", total, resp["committed_through"])
	}

	for i, acct := range accounts {
		status, balResp := doJSONRequest(t, "GET", balURL(env, "/balance?account="+acct), nil)
		want := fmt.Sprintf("%d", 5+i)
		if status != http.StatusOK || balResp["value"] != want {
			t.Errorf("bal account %s: want balance %s, got %d %v", acct, want, status, balResp)
		}
	}
	for _, room := range rooms {
		checkStatus, checkResp := doJSONRequest(t, "POST", fmt.Sprintf("%s/api/v2/tenant/default/cal/check", env.ts.URL), map[string]interface{}{
			"calendar_id": room,
			"span":        map[string]interface{}{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
			"mode":        "exclusive",
		})
		if checkStatus != http.StatusOK {
			t.Fatalf("/cal/check %s: want 200, got %d %v", room, checkStatus, checkResp)
		}
		if feasible, _ := checkResp["feasible"].(bool); feasible {
			t.Errorf("calendar %s: want occupied, got %v", room, checkResp)
		}
	}
	for _, mid := range machineIDs {
		status, fsmResp := doJSONRequest(t, "GET", fmt.Sprintf("%s/api/v2/tenant/default/fsm/machine/%d", env.ts.URL, mid), nil)
		if status != http.StatusOK || fsmResp["state"] != "confirmed" {
			t.Errorf("machine %d: want state confirmed, got %d %v", mid, status, fsmResp)
		}
	}
	status, evResp := doJSONRequest(t, "GET",
		tsURLFor(env, "/events?timeline=1&dims=1&from=2027-01-01T00:00:00Z&to=2027-12-31T00:00:00Z&limit=10"), nil)
	events, _ := evResp["events"].([]interface{})
	if status != http.StatusOK || len(events) != 2 {
		t.Errorf("ts: want exactly 2 events, got %d %v", status, evResp)
	}
}

// ─── Adversarial: one failure among many, correctly aborts everything ─────

// TestDxpTxnAPI_Adversarial_FiveBalLegs_OneInsufficientFunds_NoneCommit
// dispatches five bal legs; the fourth requests more than the source
// account can cover. Attendance must refuse the WHOLE instance —
// nothing commits, not even the four legs that would have succeeded
// on their own.
func TestDxpTxnAPI_Adversarial_FiveBalLegs_OneInsufficientFunds_NoneCommit(t *testing.T) {
	const n = 5
	env := newFullDxpServer(t)
	accounts := defineNBalAccounts(t, env, n)

	participants := make([]map[string]interface{}, n)
	for i, acct := range accounts {
		amount := "10"
		if i == 3 {
			amount = "999999999" // ~in has no floor defined by defineNBalAccounts beyond the default — refused on the funding side
		}
		participants[i] = map[string]interface{}{
			"id": fmt.Sprintf("payment%d", i), "primitive": "bal", "op": "transfer",
			"params": map[string]interface{}{"from": "~in", "to": acct, "amount": amount},
		}
	}
	def := map[string]interface{}{
		"name": "adversarial_five_bal", "pattern": "3ps",
		"participants": participants,
		"phase_ttl":    map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{"def_id": defResp["id"]})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "released" {
		t.Fatalf("expected released (refused at attendance), got %v (reason: %v)", resp["status"], resp["reason"])
	}
	for _, acct := range accounts {
		status, balResp := doJSONRequest(t, "GET", balURL(env, "/balance?account="+acct), nil)
		if status != http.StatusOK || balResp["value"] != "0" {
			t.Errorf("account %s: want balance 0 (nothing should have committed), got %d %v", acct, status, balResp)
		}
	}
}

// TestDxpTxnAPI_Adversarial_FourCalBookings_OneConflicts_NoneCommit books
// four calendars; the third's span conflicts with a real, pre-existing
// booking. Every one of the four calendars must remain unoccupied,
// including the three that would have booked cleanly.
func TestDxpTxnAPI_Adversarial_FourCalBookings_OneConflicts_NoneCommit(t *testing.T) {
	const n = 4
	env := newFullDxpServer(t)
	rooms := seedNCalendarsForDefaultTenant(t, env, n)
	start := time.Date(2027, 9, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	tid := defaultTenantID(t, env)
	lc, err := env.srv.CalManagerForTest().CalFor(tid)
	if err != nil {
		t.Fatalf("CalFor: %v", err)
	}
	if _, err := lc.Create(cal.Booking{
		BookingID: "existing", CalendarID: rooms[2], State: cal.StateBinding,
		Span:   cal.Span{Start: ot.FromTime(start), End: ot.FromTime(end)},
		Mode:   cal.ModeExclusive, Bearer: 1,
	}); err != nil {
		t.Fatalf("seed conflicting booking: %v", err)
	}

	participants := make([]map[string]interface{}, n)
	for i, room := range rooms {
		participants[i] = map[string]interface{}{
			"id": fmt.Sprintf("room%d", i), "primitive": "cal", "op": "book",
			"params": map[string]interface{}{
				"calendar": room, "booking_id": fmt.Sprintf("adversarial-%d", i),
				"span": map[string]interface{}{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
				"mode": "exclusive", "bearer": 1,
			},
		}
	}
	def := map[string]interface{}{
		"name": "adversarial_four_cal", "pattern": "3ps",
		"participants": participants,
		"phase_ttl":    map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{"def_id": defResp["id"]})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "released" {
		t.Fatalf("expected released (refused at attendance), got %v (reason: %v)", resp["status"], resp["reason"])
	}
	for i, room := range rooms {
		if i == 2 {
			continue // the pre-seeded booking legitimately occupies this one
		}
		checkStatus, checkResp := doJSONRequest(t, "POST", fmt.Sprintf("%s/api/v2/tenant/default/cal/check", env.ts.URL), map[string]interface{}{
			"calendar_id": room,
			"span":        map[string]interface{}{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
			"mode":        "exclusive",
		})
		if checkStatus != http.StatusOK {
			t.Fatalf("/cal/check %s: want 200, got %d %v", room, checkStatus, checkResp)
		}
		if feasible, _ := checkResp["feasible"].(bool); !feasible {
			t.Errorf("calendar %s: should have stayed free (nothing should have committed), got %v", room, checkResp)
		}
	}
}

// TestDxpTxnAPI_Adversarial_ThreeFsmTransitions_OneIllegal_NoneCommit
// transitions three machines; the second is sent an input the
// HotelBooking spec never defines from "reserved". All three machines
// must stay in "reserved", including the two whose own input was legal.
func TestDxpTxnAPI_Adversarial_ThreeFsmTransitions_OneIllegal_NoneCommit(t *testing.T) {
	const n = 3
	env := newFullDxpServer(t)
	machineIDs := createNHotelBookingMachines(t, env, n)

	participants := make([]map[string]interface{}, n)
	for i, id := range machineIDs {
		input := "confirm"
		if i == 1 {
			input = "cancel" // not a defined transition from "reserved" in hotelBookingFsmSpec
		}
		participants[i] = map[string]interface{}{
			"id": fmt.Sprintf("booking%d", i), "primitive": "fsm", "op": "transition",
			"params": map[string]interface{}{"machine_id": id, "input": input},
		}
	}
	def := map[string]interface{}{
		"name": "adversarial_three_fsm", "pattern": "3ps",
		"participants": participants,
		"phase_ttl":    map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{"def_id": defResp["id"]})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "released" {
		t.Fatalf("expected released (refused at attendance), got %v (reason: %v)", resp["status"], resp["reason"])
	}
	for _, id := range machineIDs {
		status, fsmResp := doJSONRequest(t, "GET", fmt.Sprintf("%s/api/v2/tenant/default/fsm/machine/%d", env.ts.URL, id), nil)
		if status != http.StatusOK || fsmResp["state"] != "reserved" {
			t.Errorf("machine %d: want state still reserved (nothing should have committed), got %d %v", id, status, fsmResp)
		}
	}
}

// TestDxpTxnAPI_Adversarial_SixEntityCreates_OneAlreadyExists_NoneCommit
// creates six entities; the fourth uses an explicit id that already
// exists. Refused at attendance — the other five, which would have
// created cleanly on their own, must not exist afterward either.
func TestDxpTxnAPI_Adversarial_SixEntityCreates_OneAlreadyExists_NoneCommit(t *testing.T) {
	const n = 6
	env := newFullDxpServer(t)
	existingID := createMetaEntity(t, env)

	participants := make([]map[string]interface{}, n)
	for i := 0; i < n; i++ {
		params := map[string]interface{}{
			"entity": "assets",
			"data":   map[string]interface{}{"name": fmt.Sprintf("adversarial-guest-%d", i), "type": "sensor"},
		}
		if i == 3 {
			params["id"] = existingID
		}
		participants[i] = map[string]interface{}{
			"id": fmt.Sprintf("guest%d", i), "primitive": "entity", "op": "create",
			"params": params,
		}
	}
	def := map[string]interface{}{
		"name": "adversarial_six_entity", "pattern": "3ps",
		"participants": participants,
		"phase_ttl":    map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	status, resp := doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{"def_id": defResp["id"]})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "released" {
		t.Fatalf("expected released (refused at attendance), got %v (reason: %v)", resp["status"], resp["reason"])
	}
}

// TestDxpTxnAPI_Adversarial_MultiSubstrateAtScale_OnePhaseParticipantFails_TornAccepted
// is the phased-path analog of the four tests above, deliberately
// different in kind: phased participants commit independently, so a
// late failure does NOT roll back siblings that already committed —
// it produces an honest, accepted torn commit (§6), not a full abort.
// Four participants total (two bal, one cal, one ts); one bal leg
// fails at EXECUTE time specifically (bal only checks the credit
// ceiling at Execute, never at Reserve — the same technique T-105's
// own torn test uses), after attendance already passed for all four.
// committed_through must read exactly 3, and the three legs that
// really did commit must be independently verifiable, not just
// trusted from the coordinator's own count.
func TestDxpTxnAPI_Adversarial_MultiSubstrateAtScale_OnePhaseParticipantFails_TornAccepted(t *testing.T) {
	env := newFullDxpServer(t)
	status, resp := doJSONRequest(t, "POST", balURL(env, "/def"),
		map[string]interface{}{"account_id": "~in", "unit": "unit", "scale": 0, "floor": "-1000000000"})
	if status != http.StatusCreated {
		t.Fatalf("define ~in: want 201, got %d %v", status, resp)
	}
	status, resp = doJSONRequest(t, "POST", balURL(env, "/def"),
		map[string]interface{}{"account_id": "goodacct", "unit": "unit", "scale": 0})
	if status != http.StatusCreated {
		t.Fatalf("define goodacct: want 201, got %d %v", status, resp)
	}
	status, resp = doJSONRequest(t, "POST", balURL(env, "/def"),
		map[string]interface{}{"account_id": "ceiling", "unit": "unit", "scale": 0, "ceiling": "10"})
	if status != http.StatusCreated {
		t.Fatalf("define ceiling: want 201, got %d %v", status, resp)
	}
	seedCalendarForDefaultTenant(t, env, "scaleroomX")
	provisionTsAndDefineTimeline(t, env, 1, 1)

	start := time.Date(2027, 10, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	def := map[string]interface{}{
		"name": "adversarial_multisub_scale", "pattern": "3ps",
		"participants": []map[string]interface{}{
			{"id": "bal_good", "primitive": "bal", "op": "transfer",
				"params": map[string]interface{}{"from": "~in", "to": "goodacct", "amount": "20"}},
			{"id": "bal_fails", "primitive": "bal", "op": "transfer",
				// exceeds "ceiling" account's own ceiling (10) -- refused
				// only at Execute time, never at Reserve (bal only ever
				// reserves the debit side, per TransferParams' own doc).
				"params": map[string]interface{}{"from": "~in", "to": "ceiling", "amount": "999"}},
			{"id": "room", "primitive": "cal", "op": "book",
				"params": map[string]interface{}{
					"calendar": "scaleroomX", "booking_id": "scale-torn-1",
					"span": map[string]interface{}{"start": start.Format(time.RFC3339), "end": end.Format(time.RFC3339)},
					"mode": "exclusive", "bearer": 1,
				}},
			{"id": "audit", "primitive": "ts", "op": "append",
				"params": map[string]interface{}{
					"timeline": 1, "dims": []interface{}{1},
					"time_unix_ns": start.UnixNano(), "nums": []interface{}{1.0},
				}},
		},
		"phase_ttl": map[string]interface{}{"reserve": "PT2M"},
	}
	status, defResp := doJSONRequest(t, "POST", dxpURL(env, "/def"), def)
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/def: want 201, got %d %v", status, defResp)
	}
	status, resp = doJSONRequest(t, "POST", dxpURL(env, "/txn"), map[string]interface{}{"def_id": defResp["id"]})
	if status != http.StatusCreated {
		t.Fatalf("POST /dxp/txn: want 201, got %d %v", status, resp)
	}
	if resp["status"] != "expired" {
		t.Fatalf("expected expired (torn, accepted per §6), got %v (reason: %v)", resp["status"], resp["reason"])
	}
	if ct, ok := resp["committed_through"].(float64); !ok || ct != 3 {
		t.Fatalf("expected committed_through 3 (bal_good + room + audit committed, bal_fails alone didn't), got %v", resp["committed_through"])
	}

	status, balResp := doJSONRequest(t, "GET", balURL(env, "/balance?account=goodacct"), nil)
	if status != http.StatusOK || balResp["value"] != "20" {
		t.Errorf("goodacct: want balance 20 (this leg genuinely committed), got %d %v", status, balResp)
	}
	status, balResp = doJSONRequest(t, "GET", balURL(env, "/balance?account=ceiling"), nil)
	if status != http.StatusOK || balResp["value"] != "0" {
		t.Errorf("ceiling: want balance 0 (this leg genuinely failed), got %d %v", status, balResp)
	}
	// cal leg verified against H1 (cal_bookings) directly, not
	// /cal/check -- same reasoning as T-107/T-108's own fix: H3 is only
	// ever brought up to date by PostCommit, which fires strictly on a
	// genuine FULL-instance commit (T-108's own contract) and never for
	// a torn/expired one. This instance is torn by design, so H3 never
	// hears about the cal leg even though it genuinely, durably
	// committed at H1 -- checking /cal/check here would be checking the
	// wrong plane for exactly the reason T-107 first found.
	wdp, ok := env.store.(storage.WriterDBProvider)
	if !ok {
		t.Fatalf("cal leg: test store does not implement storage.WriterDBProvider")
	}
	tid := defaultTenantID(t, env)
	var calState string
	err := wdp.WriterDB().QueryRowContext(context.Background(),
		`SELECT state FROM cal_bookings WHERE tenant_id = ? AND calendar_id = ? AND booking_id = ?`,
		tid, "scaleroomX", "scale-torn-1").Scan(&calState)
	if err != nil {
		t.Fatalf("cal leg: querying cal_bookings for scale-torn-1: %v", err)
	}
	if calState != string(cal.StateBinding) {
		t.Errorf("scaleroomX: want state %q (this leg genuinely committed), got %q", cal.StateBinding, calState)
	}
	status, evResp := doJSONRequest(t, "GET",
		tsURLFor(env, "/events?timeline=1&dims=1&from=2027-01-01T00:00:00Z&to=2027-12-31T00:00:00Z&limit=10"), nil)
	events, _ := evResp["events"].([]interface{})
	if status != http.StatusOK || len(events) != 1 {
		t.Errorf("ts: want exactly 1 event (this leg genuinely committed), got %d %v", status, evResp)
	}
}
