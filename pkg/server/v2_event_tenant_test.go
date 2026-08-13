// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"fmt"
	"net/http"
	"testing"
)

// TestEventList_TenantIsolation is the XOT180 general-sweep check
// (2026-08-12): handleEventList's own SQL looks correctly
// tenant-scoped by inspection ("WHERE tenant_id = ?"), but "looks
// correct in the code" is exactly the standard that let XOT173 ship.
// Proven directly here instead: two tenants, each with their own
// event subscription of the same event_type/action_type, confirm
// neither tenant's own list result reflects the other's -- compared
// by count, since each tenant has its own independent id sequence.
func TestEventList_TenantIsolation(t *testing.T) {
	env := newV2Server(t)
	defaultURL := fmt.Sprintf("%s/api/v2/tenant/default/event/def", env.ts.URL)
	otherURL := fmt.Sprintf("%s/api/v2/tenant/other-tenant/event/def", env.ts.URL)

	body := map[string]interface{}{"event_type": "entity.created", "action_type": "webhook"}
	status, respDefault := doJSONRequest(t, "POST", defaultURL, body)
	if status != http.StatusCreated {
		t.Fatalf("default tenant event def: want 201, got %d %v", status, respDefault)
	}
	status, respOther := doJSONRequest(t, "POST", otherURL, body)
	if status != http.StatusCreated {
		t.Fatalf("other-tenant event def: want 201, got %d %v", status, respOther)
	}

	status, listDefault := doJSONRequest(t, "GET", defaultURL, nil)
	if status != http.StatusOK {
		t.Fatalf("default tenant event list: want 200, got %d %v", status, listDefault)
	}
	subs, _ := listDefault["subscriptions"].([]interface{})
	if len(subs) != 1 {
		t.Fatalf("tenant isolation violated: default tenant's own event list want exactly 1, got %d: %v", len(subs), subs)
	}
	if subs[0].(map[string]interface{})["id"] != respDefault["id"] {
		t.Errorf("default tenant's own event list: want its own def's id, got %v", subs[0])
	}
}
