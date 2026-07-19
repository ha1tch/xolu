// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"context"
	"log/slog"
	"time"
)

// This file defines the client's telemetry emission — the log/slog output
// the client produces when WithLogger is set at construction.
//
// Content discipline (non-negotiable):
//
//   - Never any header value except HTTP status.
//   - Never any request body content.
//   - Never any response body content.
//   - Never any token, secret, key, or credential.
//   - Only structural facts: HTTP method, URL path, status, duration,
//     attempt number, backoff duration.
//
// A caller who wants payload logging can wrap the client themselves. The
// client emits enough for operational visibility (which endpoint, how long,
// what status, whether retries fired) without leaking data. This is the
// same reasoning that keeps xolu's server-side logging payload-free.
//
// Level choices:
//
//   debug — every request outcome (success and failure). Debug level
//           because per-request logs are high-volume and callers usually
//           don't want them by default.
//   info  — auth failures (401, 403). Notable but not alarming; operators
//           want to see these but shouldn't need to filter them out of
//           debug output.
//   warn  — retry firings. Each retry is a warn because it means an
//           attempt failed in a retryable way; a burst of these is a
//           signal.
//
// Deliberately no error-level output: errors are returned to the caller
// and the caller decides whether to log them. Logging errors here would
// double every failure in the caller's log stream.


// logRequest emits the debug-level record for a single completed attempt.
// method is the HTTP method; urlPath is the URL's path (never the full URL
// with querystring); status is the HTTP status (0 if the request never
// reached the server); dur is the wall-clock duration of the attempt;
// attempt is 1 for the first attempt, 2 for the first retry, and so on;
// transportErr is the transport error if the request never got a response.
func (c *Client) logRequest(ctx context.Context, method, urlPath string, status int, dur time.Duration, attempt int, transportErr error) {
	if c.logger == nil {
		return
	}
	// Route auth failures to info level so operators see them without
	// enabling debug output.
	level := slog.LevelDebug
	if status == 401 || status == 403 {
		level = slog.LevelInfo
	}
	attrs := []slog.Attr{
		slog.String("method", method),
		slog.String("path", urlPath),
		slog.Duration("duration", dur),
		slog.Int("attempt", attempt),
	}
	if status > 0 {
		attrs = append(attrs, slog.Int("status", status))
	}
	if transportErr != nil {
		// The error's string is included as a structural fact — its
		// content describes what went wrong at the transport level, not
		// any payload. This is safe.
		attrs = append(attrs, slog.String("error", transportErr.Error()))
	}
	c.logger.LogAttrs(ctx, level, "xolu client: request", attrs...)
}

// logRetry emits the warn-level record for a retry decision. attempt is the
// attempt number that just failed (so the NEXT attempt is attempt+1). wait
// is the backoff before the next attempt.
func (c *Client) logRetry(ctx context.Context, method, urlPath string, attempt int, wait time.Duration, cause string) {
	if c.logger == nil {
		return
	}
	c.logger.LogAttrs(ctx, slog.LevelWarn, "xolu client: retrying",
		slog.String("method", method),
		slog.String("path", urlPath),
		slog.Int("attempt", attempt),
		slog.Int("next_attempt", attempt+1),
		slog.Duration("wait", wait),
		slog.String("cause", cause),
	)
}
