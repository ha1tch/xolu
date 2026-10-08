// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// commit_body_limit_test.go
//
// XOT231: an oversize /commit body answers 413 XOLU-ST007, and a malformed one
// still answers 400.

import (
	"net/http"
	"strings"
	"testing"
)

func TestCommitE2E_OversizeBodyAnswers413(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()

	req := map[string]interface{}{
		"update": map[string]interface{}{
			"entity": "assets",
			"id":     8102,
			"data":   map[string]interface{}{"pad": strings.Repeat("x", 2<<20)},
		},
	}
	status, body := env.do("POST", commitURL(""), req)
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d, want 413: %.200s", status, body)
	}
	if !strings.Contains(string(body), "XOLU-ST007") {
		t.Errorf("body does not carry XOLU-ST007: %.200s", body)
	}
}

// A malformed body is still a 400.
func TestCommitE2E_MalformedBodyStillAnswers400(t *testing.T) {
	env := newCommitEnv(t)
	defer env.cleanup()
	status, _ := env.do("POST", commitURL(""), "not an object")
	if status != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", status)
	}
}
