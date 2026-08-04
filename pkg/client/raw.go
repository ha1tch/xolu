// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// raw.go — Raw, a minimal escape hatch exposing this client's existing
// auth machinery for callers that need to issue a request this client
// has no typed method for. Requested for xoluman's own ad-hoc REST
// query console -- OQL and Sulpher already have typed methods
// (Client.OQL, Client.Sulpher); Raw exists for everything else an
// operator might want to try against a connected instance without
// reimplementing auth.
//
// Deliberately different from every other method in this package in
// two ways, both load-bearing to what "raw" means here, not
// oversights:
//
//   - No implicit tenant-prefixing. Every other method builds its URL
//     via buildURL/buildURLv2, which silently inserts
//     /tenant/{id}/... when the client is tenant-configured. Raw takes
//     a full path (e.g. "/api/v1/bal/def" or
//     "/api/v1/tenant/t0000/bal/def") appended directly to the
//     client's base URL, with no rewriting -- an operator issuing an
//     arbitrary request needs to see and control exactly what goes
//     over the wire, not have a prefix silently added underneath them.
//   - No structured-error decoding. Every other method decodes a
//     non-2xx response into *client.Error. Raw returns the response
//     exactly as received -- status, headers, body -- for any status
//     code, reserving a non-nil error return for transport failures
//     only (connection refused, timeout, request construction
//     failure). Interpreting the status code is the caller's job here;
//     that is the entire point of an escape hatch for requests this
//     client doesn't otherwise understand.
//
// Single attempt, not retried, for the same reason as blob.go's
// Put/Get and export.go's Export: the request body (an io.Reader) is
// not guaranteed re-readable, and Raw's own callers are, by
// definition, doing something this client has no specific knowledge
// of -- silently retrying an operation of unknown idempotency is not
// a safe default.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// RawResult is the unfiltered response to a Raw request.
type RawResult struct {
	// StatusCode is the HTTP status exactly as received -- 2xx, 4xx,
	// 5xx, whatever the server sent. Not treated as an error condition
	// by Raw itself; inspect it directly.
	StatusCode int
	// Body is the full response body, read to completion. Not
	// interpreted or decoded in any way.
	Body []byte
	// Header is the response's own HTTP headers.
	Header http.Header
}

// Raw issues an arbitrary HTTP request against this client's connected
// instance, applying the client's configured auth and nothing else --
// no tenant-path rewriting, no error-shape decoding, no retry.
//
// path is appended directly to the client's base URL and must start
// with "/" (e.g. "/api/v1/bal/def", "/api/v1/tenant/t0000/bal/def",
// "/health"); it is not rewritten or tenant-prefixed. body may be nil
// for methods that don't send one. contentType is sent as the
// Content-Type header when body is non-nil; pass "" to default to
// "application/json", matching every other method in this package
// (Raw is most often used for the same JSON API surface, just for a
// path this client has no typed wrapper for).
//
// A non-nil error means the request never completed -- a transport
// failure, not an HTTP error status. Any response the server actually
// sent, including 4xx/5xx, comes back as a non-nil *RawResult with a
// nil error; check result.StatusCode.
func (c *Client) Raw(ctx context.Context, method, path string, contentType string, body io.Reader) (*RawResult, error) {
	if path == "" || path[0] != '/' {
		return nil, fmt.Errorf("xolu: Raw path must start with \"/\", got %q", path)
	}
	requestURL := c.baseURL + path

	if c.callTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.callTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, method, requestURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if body != nil {
		if contentType == "" {
			contentType = "application/json"
		}
		req.Header.Set("Content-Type", contentType)
	}
	if h := c.authHeader(); h != "" {
		req.Header.Set("Authorization", h)
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logRequest(ctx, method, path, 0, time.Since(start), 1, err)
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	c.logRequest(ctx, method, path, resp.StatusCode, time.Since(start), 1, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return &RawResult{
		StatusCode: resp.StatusCode,
		Body:       respBody,
		Header:     resp.Header,
	}, nil
}
