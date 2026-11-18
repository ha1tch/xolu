// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRawWithHeadersSetsReplacesAndRemoves(t *testing.T) {
	var got http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	c := New(server.URL, WithAPIKey("test-key"))
	header := http.Header{
		"X-Blob-Key":    {"docs/a.txt"},  // one of the caller's own
		"Authorization": {"Bearer mine"}, // replaces the one the client builds
		"Content-Type":  nil,             // named with no value: not sent at all
	}
	res, err := c.RawWithHeaders(context.Background(), http.MethodPost, "/api/v1/blob", "", header, strings.NewReader("x"))
	if err != nil || res.StatusCode != http.StatusCreated {
		t.Fatalf("got %v, %v", res, err)
	}
	if got.Get("X-Blob-Key") != "docs/a.txt" || got.Get("Authorization") != "Bearer mine" {
		t.Errorf("headers: %v", got)
	}
	if _, sent := got["Content-Type"]; sent {
		t.Errorf("Content-Type was sent: %q", got.Get("Content-Type"))
	}

	// nil headers: exactly Raw
	if _, err := c.RawWithHeaders(context.Background(), http.MethodPost, "/x", "", nil, strings.NewReader("{}")); err != nil {
		t.Fatal(err)
	}
	if got.Get("Content-Type") != "application/json" || got.Get("Authorization") != "ApiKey test-key" {
		t.Errorf("with no headers of its own: %v", got)
	}
}
