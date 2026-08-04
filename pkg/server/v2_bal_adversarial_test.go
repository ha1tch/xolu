// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"net/http"
	"testing"
)

// TestBalAPI_DuplicateDefine_Returns409NotFiveHundred is bal's own
// version of loc's TestLocAPI_DuplicateDefine_Returns409NotFiveHundred
// — the same UNIQUE-constraint-falls-through-to-500 gap, found in
// loc first (T-118's adversarial pass) then confirmed and fixed in
// bal too.
func TestBalAPI_DuplicateDefine_Returns409NotFiveHundred(t *testing.T) {
	env := newDxpServer(t)
	body := map[string]interface{}{"account_id": "dup-http-acct", "unit": "EUR", "scale": 2.0}
	status, resp := doJSONRequest(t, "POST", balURL(env, "/def"), body)
	if status != http.StatusCreated {
		t.Fatalf("first define: want 201, got %d %v", status, resp)
	}
	status, resp = doJSONRequest(t, "POST", balURL(env, "/def"), body)
	if status != http.StatusConflict {
		t.Fatalf("duplicate account_id: want 409, got %d %v", status, resp)
	}
	errObj, ok := resp["error"].(map[string]interface{})
	if !ok || errObj["code"] != "XOLU-BAL007" {
		t.Errorf("want XOLU-BAL007, got %v", resp)
	}
}
