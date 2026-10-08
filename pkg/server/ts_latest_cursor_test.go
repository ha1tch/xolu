// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"fmt"
	"net/http"
	"testing"
)

// XOT240 over HTTP: /events/latest answers next_cursor when more events match
// than n, and ?cursor= continues from it.
func TestTSE2E_LatestCursor(t *testing.T) {
	env := setupTSServer(t, nil)
	tenant := "acme"
	env.registerTenant(tenant)
	env.provision(tenant)
	tl := env.defineTimeline(tenant, map[string]interface{}{"id": 7, "name": "idx", "dims": 2, "retention_days": -1})
	for v := 1; v <= 25; v++ {
		env.appendEvent(tenant, map[string]interface{}{"timeline": tl, "dims": []uint64{1, uint64(v)}, "time": fmt.Sprintf("1970-01-01T00:00:%02dZ", v), "nums": []float64{1}})
	}
	page := func(cursor string) (int, map[string]interface{}) {
		url := env.tsURL(tenant, "/events/latest") + "?timeline=7&dims=1&n=10"
		if cursor != "" {
			url += "&cursor=" + cursor
		}
		return env.do("GET", url, nil)
	}
	total, cursor, pages := 0, "", 0
	for {
		st, body := page(cursor)
		if st != http.StatusOK {
			t.Fatalf("page %d: %d %v", pages+1, st, body)
		}
		evs, _ := body["events"].([]interface{})
		total += len(evs)
		pages++
		next, _ := body["next_cursor"].(string)
		if next == "" {
			break
		}
		if len(evs) != 10 {
			t.Fatalf("a page with a next_cursor holds %d events, want 10", len(evs))
		}
		cursor = next
		if pages > 5 {
			t.Fatal("paging did not terminate")
		}
	}
	if total != 25 || pages != 3 {
		t.Fatalf("paged %d events in %d pages, want 25 in 3", total, pages)
	}
	// "!!!!" is a valid query string but not base64 (a bare "%%%" would be dropped by URL parsing).
	if st, body := page("!!!!"); st != http.StatusBadRequest {
		t.Fatalf("a malformed cursor: %d %v, want 400", st, body)
	}
	// A well-formed cursor that this timeline did not issue is refused too.
	if st, body := page("AAAA"); st != http.StatusBadRequest || tsCode(body) != "XOLU-TS030" {
		t.Fatalf("a foreign cursor: %d %v, want 400 XOLU-TS030", st, body)
	}
}
