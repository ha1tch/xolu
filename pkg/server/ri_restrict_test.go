// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// TestRIRestrict_BlocksDeleteWithLiveReferrer is the wave-2 stage-2
// acceptance test (@R02.2): a users entity referenced by a posts entity
// via an author_id ref with on_delete=restrict cannot be deleted while
// the post exists. This is the SQL ON DELETE RESTRICT behaviour the
// incoming SQL-instinct dev teams expect.
func TestRIRestrict_BlocksDeleteWithLiveReferrer(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	// users: a plain entity.
	usersSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
		},
	}
	resp, body := ts.doRequest(http.MethodPost, "/api/v1/schema/users", usersSchema)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("create users schema: %d %s", resp.StatusCode, body)
	}

	// posts: author_id references users with restrict.
	postsSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"title": map[string]interface{}{"type": "string"},
			"author_id": map[string]interface{}{
				"type":   "object",
				"format": "ref",
				"x-ref": map[string]interface{}{
					"entity":    "users",
					"on_delete": "restrict",
				},
			},
		},
	}
	resp, body = ts.doRequest(http.MethodPost, "/api/v1/schema/posts", postsSchema)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		t.Fatalf("create posts schema: %d %s", resp.StatusCode, body)
	}

	// Create a user.
	resp, body = ts.doRequest(http.MethodPost, "/api/v1/users", map[string]interface{}{"name": "alice"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create user: %d %s", resp.StatusCode, body)
	}
	var userResp struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(body, &userResp); err != nil {
		t.Fatalf("decode user: %v", err)
	}

	// Create a post referencing the user.
	post := map[string]interface{}{
		"title": "hello",
		"author_id": map[string]interface{}{
			"type": "REF", "entity": "users", "id": userResp.ID,
		},
	}
	resp, body = ts.doRequest(http.MethodPost, "/api/v1/posts", post)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create post: %d %s", resp.StatusCode, body)
	}

	// Attempt to delete the user — must be refused with 409 + XOLU-RI001.
	resp, body = ts.doRequest(http.MethodDelete, fmt.Sprintf("/api/v1/users/%d", userResp.ID), nil)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("delete referenced user: expected 409, got %d %s", resp.StatusCode, body)
	}
	if !containsStr(string(body), "XOLU-RI001") {
		t.Errorf("expected XOLU-RI001 in body, got: %s", body)
	}

	// The user must still exist (delete was refused, not partially applied).
	resp, _ = ts.doRequest(http.MethodGet, fmt.Sprintf("/api/v1/users/%d", userResp.ID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("user should still exist after refused delete, GET returned %d", resp.StatusCode)
	}
}

// TestRIRestrict_AllowsDeleteWithoutReferrer confirms restrict does not
// block a delete when nothing references the target — the guard is
// precise, not blanket.
func TestRIRestrict_AllowsDeleteWithoutReferrer(t *testing.T) {
	ts := setupTestServer(t)
	defer ts.cleanup()

	usersSchema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
	}
	ts.doRequest(http.MethodPost, "/api/v1/schema/users", usersSchema)
	postsSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"author_id": map[string]interface{}{
				"type":   "object",
				"format": "ref",
				"x-ref":  map[string]interface{}{"entity": "users", "on_delete": "restrict"},
			},
		},
	}
	ts.doRequest(http.MethodPost, "/api/v1/schema/posts", postsSchema)

	// Create a user with NO referrer.
	_, body := ts.doRequest(http.MethodPost, "/api/v1/users", map[string]interface{}{"name": "bob"})
	var u struct {
		ID int `json:"id"`
	}
	json.Unmarshal(body, &u)

	// Delete should succeed — nothing references this user.
	resp, delBody := ts.doRequest(http.MethodDelete, fmt.Sprintf("/api/v1/users/%d", u.ID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("delete unreferenced user should succeed, got %d %s", resp.StatusCode, delBody)
	}
}

