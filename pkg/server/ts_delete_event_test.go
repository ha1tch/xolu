// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// ts_delete_event_test.go
//
// XOT240 over HTTP: DELETE /ts/events removes one event by its exact key, and a
// POST /ts/tl/def that would change a timeline's settings is a conflict.

import (
	"net/http"
	"testing"
)

func TestTSE2E_DeleteOneEventByItsExactKey(t *testing.T) {
	env := setupTSServer(t, nil)
	tenant := "acme"
	env.registerTenant(tenant)
	env.provision(tenant)
	tl := env.defineTimeline(tenant, map[string]interface{}{"id": 7, "name": "idx", "dims": 2, "retention_days": -1})

	at := func(sec int) string { return time1970(sec) }
	for _, ev := range []struct {
		dims []uint64
		sec  int
	}{{[]uint64{1, 1}, 10}, {[]uint64{1, 2}, 20}} {
		env.appendEvent(tenant, map[string]interface{}{"timeline": tl, "dims": ev.dims, "time": at(ev.sec), "nums": []float64{1}})
	}
	count := func() int {
		_, body := env.do("GET", env.tsURL(tenant, "/events/latest")+"?timeline=7&dims=1&n=10", nil)
		evs, _ := body["events"].([]interface{})
		if evs == nil {
			evs, _ = body["data"].([]interface{})
		}
		return len(evs)
	}
	if got := count(); got != 2 {
		t.Fatalf("events before the delete = %d, want 2 (response shape changed?)", got)
	}

	del := func(dims []uint64, sec int) (int, map[string]interface{}) {
		return env.do("DELETE", env.tsURL(tenant, "/events"), map[string]interface{}{"timeline": tl, "dims": dims, "time": at(sec)})
	}
	if st, body := del([]uint64{1, 1}, 10); st != http.StatusOK || body["deleted"] != true {
		t.Fatalf("delete: %d %v", st, body)
	}
	if got := count(); got != 1 {
		t.Fatalf("events after deleting one = %d, want 1", got)
	}
	// The neighbour with the same first dimension is untouched; deleting again finds nothing.
	if st, body := del([]uint64{1, 1}, 10); st != http.StatusNotFound || tsCode(body) != "XOLU-TS029" {
		t.Fatalf("second delete: %d %v, want 404 XOLU-TS029", st, body)
	}
	if st, body := del([]uint64{1, 2}, 21); st != http.StatusNotFound || tsCode(body) != "XOLU-TS029" {
		t.Fatalf("delete at another time: %d %v, want 404 XOLU-TS029", st, body)
	}
	if st, _ := del([]uint64{1}, 20); st != http.StatusBadRequest {
		t.Fatalf("delete with a prefix of the dims: %d, want 400", st)
	}
	if got := count(); got != 1 {
		t.Fatalf("events after the refused deletes = %d, want 1", got)
	}

	// A re-definition that matches is fine; one that would change the retention is a conflict.
	post := func(retention int) (int, map[string]interface{}) {
		return env.do("POST", env.tsURL(tenant, "/tl/def"), map[string]interface{}{"id": 7, "name": "idx", "dims": 2, "retention_days": retention})
	}
	if st, body := post(-1); st != http.StatusCreated {
		t.Fatalf("matching re-definition: %d %v", st, body)
	}
	if st, body := post(30); st != http.StatusConflict || tsCode(body) != "XOLU-TS028" {
		t.Fatalf("re-definition with another retention: %d %v, want 409 XOLU-TS028", st, body)
	}
	// Any negative value means "no expiry", so -9 over -1 is the same definition.
	if st, body := post(-9); st != http.StatusCreated {
		t.Fatalf("re-definition spelling no-expiry as -9: %d %v", st, body)
	}
	// PATCH is the way to change it.
	if st, body := env.do("PATCH", env.tsURL(tenant, "/tl/7"), map[string]interface{}{"retention_days": 90}); st != http.StatusOK {
		t.Fatalf("PATCH retention: %d %v", st, body)
	}
}

func tsCode(body map[string]interface{}) interface{} {
	if e, ok := body["error"].(map[string]interface{}); ok {
		return e["code"]
	}
	return nil
}

func time1970(sec int) string {
	return "1970-01-01T00:00:" + twoDigits(sec) + "Z"
}

func twoDigits(n int) string {
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}
