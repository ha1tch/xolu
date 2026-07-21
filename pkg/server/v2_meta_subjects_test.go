// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_meta_subjects_test.go
//
// Item 7 part 2: namespaced subject kinds on /api/v2/meta (@C04c), the
// gated-kind refusals, and the ts.timeline cascade sweep.

package server_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/ha1tch/xolu/pkg/config"
)

// ─── Namespaced kinds ────────────────────────────────────────────────────────

func TestMetaSubjects_TimelineKindCRUD(t *testing.T) {
	env := newMetaServer(t)

	// Key ABOVE uint16 (@C04d: the width bug's exact territory): the full
	// round trip must preserve it.
	const key = "70000"

	status, resp := doJSONRequest(t, "PUT",
		metaURL(env, "/ts.timeline/"+key+"/owner"),
		map[string]interface{}{"value": "warehouse-A"})
	if status != http.StatusOK {
		t.Fatalf("PUT ts.timeline: %d %v", status, resp)
	}
	sub, _ := resp["subject"].(map[string]interface{})
	if sub["kind"] != "ts.timeline" || sub["key"] != key {
		t.Fatalf("subject echo wrong: %v", resp["subject"])
	}
	if _, hasID := resp["id"]; hasID {
		t.Fatal("namespaced kinds must not carry the legacy integer id field")
	}

	status, resp = doJSONRequest(t, "GET", metaURL(env, "/ts.timeline/"+key+"/owner"), nil)
	if status != http.StatusOK || resp["value"] != "warehouse-A" {
		t.Fatalf("GET: %d %v", status, resp)
	}

	status, _ = doJSONRequest(t, "DELETE", metaURL(env, "/ts.timeline/"+key+"/owner"), nil)
	if status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("DELETE: %d", status)
	}
	status, _ = doJSONRequest(t, "GET", metaURL(env, "/ts.timeline/"+key+"/owner"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("GET after delete: %d, want 404", status)
	}
}

func TestMetaSubjects_CalendarKindOpaqueKey(t *testing.T) {
	env := newMetaServer(t)
	status, resp := doJSONRequest(t, "PUT",
		metaURL(env, "/cal.calendar/warehouse-A/timezone"),
		map[string]interface{}{"value": "America/Montevideo"})
	if status != http.StatusOK {
		t.Fatalf("PUT cal.calendar: %d %v", status, resp)
	}
	status, resp = doJSONRequest(t, "GET", metaURL(env, "/cal.calendar/warehouse-A"), nil)
	if status != http.StatusOK {
		t.Fatalf("LIST: %d", status)
	}
	if entries, ok := resp["entries"].([]interface{}); !ok || len(entries) != 1 {
		t.Fatalf("LIST entries: %v", resp["entries"])
	}
}

func TestMetaSubjects_Refusals(t *testing.T) {
	env := newMetaServer(t)
	cases := []struct {
		path string
		want int
	}{
		{"/bal.account/warehouse:A/code", http.StatusBadRequest},  // gated kind
		{"/nope.kind/x/k", http.StatusBadRequest},                 // unknown kind
		{"/ts.timeline/4294967296/k", http.StatusBadRequest},      // one past uint32 (@C04d)
		{"/ts.timeline/-1/k", http.StatusBadRequest},              // negative
		{"/assets/0/k", http.StatusBadRequest},                    // entity id must be positive
	}
	for _, c := range cases {
		status, _ := doJSONRequest(t, "PUT", metaURL(env, c.path),
			map[string]interface{}{"value": "x"})
		if status != c.want {
			t.Fatalf("PUT %s: %d, want %d", c.path, status, c.want)
		}
	}
}

func TestMetaSubjects_EntityBehaviourUnchanged(t *testing.T) {
	env := newMetaServer(t)
	id := createMetaEntity(t, env)

	status, resp := doJSONRequest(t, "PUT",
		metaURL(env, fmt.Sprintf("/assets/%d/note", id)),
		map[string]interface{}{"value": "kept"})
	if status != http.StatusOK {
		t.Fatalf("entity PUT: %d %v", status, resp)
	}
	// Back-compat: entity responses keep entity + integer id, and gain subject.
	if resp["entity"] != "assets" {
		t.Fatalf("entity field lost: %v", resp)
	}
	if _, ok := resp["id"].(float64); !ok {
		t.Fatalf("integer id field lost: %v", resp["id"])
	}
	if _, ok := resp["subject"].(map[string]interface{}); !ok {
		t.Fatalf("subject field missing: %v", resp)
	}
	// Missing entity still 404s (existence check unchanged).
	status, _ = doJSONRequest(t, "PUT", metaURL(env, "/assets/999999/note"),
		map[string]interface{}{"value": "x"})
	if status != http.StatusNotFound {
		t.Fatalf("PUT on missing entity: %d, want 404", status)
	}
}

// ─── ts cascade sweep ────────────────────────────────────────────────────────

func TestMetaSubjects_TimelineDeleteSweeps(t *testing.T) {
	env := newMetaServer(t, func(cfg *config.Config) {
		cfg.TimeseriesEnabled = true
	})

	// A real timeline (v1 ts surface: provision, then define), annotated,
	// then deleted: annotations must go.
	status, _ := doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/ts/provision", env.ts.URL), nil)
	if status != http.StatusCreated && status != http.StatusOK && status != http.StatusConflict {
		t.Skipf("ts provisioning not available in this configuration: %d", status)
	}
	status, _ = doJSONRequest(t, "POST",
		fmt.Sprintf("%s/api/v1/tenant/default/ts/tl/def", env.ts.URL),
		map[string]interface{}{"id": 70001, "dims": 1, "name": "sweeptest"})
	if status != http.StatusCreated {
		t.Skipf("ts define not available in this configuration: %d", status)
	}
	status, _ = doJSONRequest(t, "PUT",
		metaURL(env, "/ts.timeline/70001/owner"),
		map[string]interface{}{"value": "ops"})
	if status != http.StatusOK {
		t.Fatalf("annotate timeline: %d", status)
	}

	status, _ = doJSONRequest(t, "DELETE",
		fmt.Sprintf("%s/api/v1/tenant/default/ts/tl/70001", env.ts.URL), nil)
	if status != http.StatusNoContent && status != http.StatusOK {
		t.Fatalf("delete timeline: %d", status)
	}

	status, _ = doJSONRequest(t, "GET", metaURL(env, "/ts.timeline/70001/owner"), nil)
	if status != http.StatusNotFound {
		t.Fatalf("annotation survived the timeline delete: %d, want 404", status)
	}
}
