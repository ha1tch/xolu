// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// blob.go — the six methods against the native /api/v1/blob* surface
// (pkg/server/blob_handlers.go). The S3-compatible surface is
// deliberately not wrapped — see types_blob.go's own header comment.
//
// Put and Get bypass the standard do()/doURL() pipeline entirely and
// do not participate in the client's retry policy, for the same
// reason: that pipeline hardcodes Content-Type: application/json and
// buffers the full response body via io.ReadAll before returning,
// both wrong for arbitrary, possibly-large blob content. A retry on a
// partially-sent PUT body or a partially-streamed GET response is not
// a safe, well-defined operation without an independently seekable
// source, which callers are not required to provide (io.Reader has no
// such guarantee) -- so neither method retries. Head/Delete/List/Usage
// have no such constraint and use the client's normal pipeline,
// including its normal retry behaviour.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// validateBlobKey checks a non-empty key against xolu's own rules
// (pkg/blob/store.go's validateKey) before any request is sent. An
// empty key is NOT rejected here -- BlobPut treats "" as a valid,
// meaningful request (content-addressed storage), while BlobGet/
// BlobHead/BlobDelete reject an empty key themselves before calling
// this, since "" can never be a real key to look up or remove.
func validateBlobKey(key string) error {
	if key == "" {
		return nil
	}
	if strings.ContainsAny(key, "/\\") {
		return fmt.Errorf("xolu: invalid blob key %q: keys are flat, '/' and '\\\\' are not allowed", key)
	}
	if key == "." || key == ".." {
		return fmt.Errorf("xolu: invalid blob key %q: \".\" and \"..\" are reserved", key)
	}
	if strings.HasPrefix(key, ".") {
		return fmt.Errorf("xolu: invalid blob key %q: a leading \".\" is reserved for internal use", key)
	}
	return nil
}

// BlobPut uploads content under key. If key is empty, the server
// stores the content addressed by its own SHA256 and returns that hash
// as the effective key (BlobPutResult.Key) -- no separate alias is
// written. contentType is sent as-is; pass "" to let the server default
// to application/octet-stream.
//
// Keys are FLAT, not hierarchical -- there is no folder/prefix
// structure at the storage layer. A non-empty key must not contain '/'
// or '\', must not be "." or "..", and must not start with "."
// (reserved for internal use) -- validated client-side against xolu's
// own rules (pkg/blob/store.go's validateKey) before any request is
// sent, matching this client's established convention (see bal.go) of
// catching an obviously-invalid call before spending a round trip on
// it. BlobList's prefix filter still works normally on flat keys (a
// plain string prefix match, e.g. prefix "log-" matches "log-2026-01"
// and "log-2026-02") -- "flat" means no '/' delimiter is meaningful,
// not that prefix filtering is unavailable.
//
// Hits POST /api/v1/blob with X-Blob-Key set when key is non-empty.
// Single attempt, not retried -- see this file's own header comment.
// Returns *client.Error on non-2xx.
func (c *Client) BlobPut(ctx context.Context, key, contentType string, body io.Reader) (*BlobPutResult, error) {
	if err := validateBlobKey(key); err != nil {
		return nil, err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	requestURL := c.buildURL("/blob")

	if c.callTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.callTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	if key != "" {
		req.Header.Set("X-Blob-Key", key)
	}
	if h := c.authHeader(); h != "" {
		req.Header.Set("Authorization", h)
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logRequest(ctx, http.MethodPost, "/blob", 0, time.Since(start), 1, err)
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	c.logRequest(ctx, http.MethodPost, "/blob", resp.StatusCode, time.Since(start), 1, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	var result BlobPutResult
	if err := c.decodeResponse(resp.StatusCode, respBody, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// BlobGetResult carries a streamed blob's content alongside its
// metadata. Body MUST be closed by the caller (defer result.Body.Close())
// -- closing it also releases the request's own context timeout, if
// one was configured; the connection is not fully released until then.
type BlobGetResult struct {
	Body        io.ReadCloser
	ContentType string
	SHA256      string
	MD5         string
	Size        int64
	ETag        string
}

// BlobGet retrieves a blob's content as a stream -- the response body
// is never buffered into memory, so this is safe to use for blobs of
// any size. The caller is responsible for reading and closing
// result.Body.
//
// Hits GET /api/v1/blob/{key}. Single attempt, not retried -- see this
// file's own header comment. Returns *client.Error on non-2xx (Body is
// nil in that case; the error response itself is small and safely
// buffered internally before being decoded).
func (c *Client) BlobGet(ctx context.Context, key string) (*BlobGetResult, error) {
	if key == "" {
		return nil, fmt.Errorf("xolu: BlobGet requires a non-empty key")
	}
	if err := validateBlobKey(key); err != nil {
		return nil, err
	}
	requestURL := c.buildURL("/blob/" + url.PathEscape(key))

	var cancel context.CancelFunc
	if c.callTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, c.callTimeout)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Accept", "*/*")
	if h := c.authHeader(); h != "" {
		req.Header.Set("Authorization", h)
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if cancel != nil {
			cancel()
		}
		c.logRequest(ctx, http.MethodGet, "/blob/"+key, 0, time.Since(start), 1, err)
		return nil, fmt.Errorf("request failed: %w", err)
	}
	c.logRequest(ctx, http.MethodGet, "/blob/"+key, resp.StatusCode, time.Since(start), 1, nil)

	if resp.StatusCode >= 400 {
		defer func() { _ = resp.Body.Close() }()
		if cancel != nil {
			defer cancel()
		}
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("failed to read response body: %w", readErr)
		}
		return nil, c.decodeResponse(resp.StatusCode, respBody, nil)
	}

	body := resp.Body
	if cancel != nil {
		body = &readCloserWithCancel{ReadCloser: resp.Body, cancel: cancel}
	}

	return &BlobGetResult{
		Body:        body,
		ContentType: resp.Header.Get("Content-Type"),
		SHA256:      resp.Header.Get("X-Blob-SHA256"),
		MD5:         resp.Header.Get("X-Blob-MD5"),
		Size:        parseContentLength(resp.Header.Get("X-Blob-Size")),
		ETag:        resp.Header.Get("ETag"),
	}, nil
}

// readCloserWithCancel closes an underlying ReadCloser and then calls
// an associated context.CancelFunc, releasing a call-timeout context's
// own timer -- needed because BlobGet cannot defer that cancel itself
// (the caller streams the body after BlobGet has already returned).
type readCloserWithCancel struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (r *readCloserWithCancel) Close() error {
	err := r.ReadCloser.Close()
	r.cancel()
	return err
}

// BlobHead retrieves a blob's metadata without its content -- cheaper
// than BlobGet when only size/hash/existence is needed.
//
// Hits HEAD /api/v1/blob/{key}. Single attempt, not retried -- see this
// file's own header comment. Returns *client.Error on non-2xx; the
// server's own HEAD handler returns bare status codes with no JSON
// body on error, so the resulting *client.Error carries an HTTP status
// but no Code/Message.
func (c *Client) BlobHead(ctx context.Context, key string) (*BlobHeadResult, error) {
	if key == "" {
		return nil, fmt.Errorf("xolu: BlobHead requires a non-empty key")
	}
	if err := validateBlobKey(key); err != nil {
		return nil, err
	}
	requestURL := c.buildURL("/blob/" + url.PathEscape(key))

	if c.callTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.callTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	if h := c.authHeader(); h != "" {
		req.Header.Set("Authorization", h)
	}

	start := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logRequest(ctx, http.MethodHead, "/blob/"+key, 0, time.Since(start), 1, err)
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	c.logRequest(ctx, http.MethodHead, "/blob/"+key, resp.StatusCode, time.Since(start), 1, nil)

	if resp.StatusCode >= 400 {
		return nil, &Error{HTTPStatus: resp.StatusCode, StatusCode: resp.StatusCode}
	}

	var storedAt time.Time
	if v := resp.Header.Get("X-Blob-Stored-At"); v != "" {
		storedAt, _ = time.Parse(time.RFC3339, v)
	}

	return &BlobHeadResult{
		Key:         key,
		ContentType: resp.Header.Get("Content-Type"),
		Size:        parseContentLength(resp.Header.Get("Content-Length")),
		SHA256:      resp.Header.Get("X-Blob-SHA256"),
		MD5:         resp.Header.Get("X-Blob-MD5"),
		ETag:        resp.Header.Get("ETag"),
		StoredAt:    storedAt,
	}, nil
}

// parseContentLength parses a decimal size header, returning 0 for an
// empty or malformed value rather than erroring -- a missing size
// header is not itself a request failure the caller needs to handle.
func parseContentLength(s string) int64 {
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

// BlobDelete removes a key alias. The underlying content-addressed
// blob is not immediately removed -- xolu's own GC handles unreferenced
// blobs separately.
//
// Hits DELETE /api/v1/blob/{key}. Returns *client.Error on non-2xx
// (XOLU-BLOB-not-found family maps to 404).
func (c *Client) BlobDelete(ctx context.Context, key string) (*BlobDeleteResult, error) {
	if key == "" {
		return nil, fmt.Errorf("xolu: BlobDelete requires a non-empty key")
	}
	if err := validateBlobKey(key); err != nil {
		return nil, err
	}
	var result BlobDeleteResult
	path := "/blob/" + url.PathEscape(key)
	if err := c.do(ctx, http.MethodDelete, path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// BlobList lists stored blobs, optionally filtered by key prefix,
// sorted by key ascending (the server's own sort order).
//
// Hits GET /api/v1/blob. Returns *client.Error on non-2xx.
func (c *Client) BlobList(ctx context.Context, prefix string) (*BlobListResult, error) {
	path := "/blob"
	if prefix != "" {
		path += "?prefix=" + url.QueryEscape(prefix)
	}
	var result BlobListResult
	if err := c.do(ctx, http.MethodGet, path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// BlobUsage returns the most recently sampled disk usage for the
// tenant's blob namespace. Served from an in-memory cache the server's
// own background sampler maintains -- cheap, but SampledAt may be nil
// (and all counts zero) if the sampler has not yet completed its first
// walk. That is a valid response, not an error.
//
// Hits GET /api/v1/blob/usage. Returns *client.Error on non-2xx
// (503 if the blob subsystem is not enabled server-side at all).
func (c *Client) BlobUsage(ctx context.Context) (*BlobUsageResult, error) {
	var result BlobUsageResult
	if err := c.do(ctx, http.MethodGet, "/blob/usage", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
