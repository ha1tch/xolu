// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Stage 4 tests — retry policy, telemetry, per-call timeouts.
//
// Tests inject a deterministic sleep function into the retry policy so the
// suite runs in milliseconds rather than seconds.

// ─── RetryPolicy: mechanics ─────────────────────────────────────────────────

func TestRetryPolicyDefaultIsNoRetry(t *testing.T) {
	// The zero-value client (no WithRetryPolicy) must not retry, even on
	// 5xx responses to idempotent methods.
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	c := New(server.URL)
	_, err := c.GetMachine(context.Background(), 42)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("expected 1 attempt with default policy, got %d", got)
	}
}

func TestRetryPolicyRetriesOn5xx(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"id":42,"definition":1,"definition_name":"x","definition_deleted":false,"state":"ok","vars":{},"created_at":"t"}`))
	}))
	defer server.Close()

	c := New(server.URL, WithRetryPolicy(RetryPolicy{
		MaxAttempts:       3,
		InitialBackoff:    time.Millisecond,
		MaxBackoff:        time.Millisecond,
		BackoffMultiplier: 2.0,
		sleep:             func(time.Duration) {}, // deterministic no-op
	}))
	m, err := c.GetMachine(context.Background(), 42)
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if m.ID != 42 {
		t.Errorf("expected id=42, got %d", m.ID)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("expected 3 attempts, got %d", got)
	}
}

func TestRetryPolicyDoesNotRetry4xx(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":{"code":"XOLU-FSM003","message":"not found","status":404}}`))
	}))
	defer server.Close()

	c := New(server.URL, WithRetryPolicy(RetryPolicy{
		MaxAttempts:       5,
		InitialBackoff:    time.Millisecond,
		BackoffMultiplier: 2.0,
		sleep:             func(time.Duration) {},
	}))
	_, err := c.GetMachine(context.Background(), 42)
	if err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("expected 1 attempt for 4xx, got %d", got)
	}
}

func TestRetryPolicyDoesNotRetryPOST(t *testing.T) {
	// POST is not idempotent per RFC 9110 §9.2.2. Even under an
	// aggressive retry policy, the client must not silently retry.
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	c := New(server.URL, WithRetryPolicy(RetryPolicy{
		MaxAttempts:       5,
		InitialBackoff:    time.Millisecond,
		BackoffMultiplier: 2.0,
		sleep:             func(time.Duration) {},
	}))
	// WalkMachine is a POST.
	_, err := c.WalkMachine(context.Background(), 42, WalkRequest{Input: "submit"})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("expected 1 attempt for POST, got %d", got)
	}
}

func TestRetryPolicyDoesNotRetryPATCH(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	c := New(server.URL, WithRetryPolicy(RetryPolicy{
		MaxAttempts:       5,
		InitialBackoff:    time.Millisecond,
		BackoffMultiplier: 2.0,
		sleep:             func(time.Duration) {},
	}))
	_, err := c.PatchMachine(context.Background(), 42, PatchMachineRequest{})
	if err == nil {
		t.Fatal("expected error")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("expected 1 attempt for PATCH, got %d", got)
	}
}

func TestRetryPolicyRetriesPUTandDELETE(t *testing.T) {
	// PUT and DELETE are idempotent and must participate in the retry
	// policy. Since our client doesn't have a PUT method exposed by
	// default, we exercise DELETE via DeleteMachine.
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c := New(server.URL, WithRetryPolicy(RetryPolicy{
		MaxAttempts:       3,
		InitialBackoff:    time.Millisecond,
		BackoffMultiplier: 2.0,
		sleep:             func(time.Duration) {},
	}))
	if err := c.DeleteMachine(context.Background(), 42); err != nil {
		t.Fatalf("expected success after retry, got %v", err)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Errorf("expected 2 attempts for DELETE, got %d", got)
	}
}

func TestRetryPolicyMaxAttemptsRespected(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	c := New(server.URL, WithRetryPolicy(RetryPolicy{
		MaxAttempts:       4,
		InitialBackoff:    time.Millisecond,
		BackoffMultiplier: 2.0,
		sleep:             func(time.Duration) {},
	}))
	_, err := c.GetMachine(context.Background(), 42)
	if err == nil {
		t.Fatal("expected error after exhausted retries")
	}
	if got := atomic.LoadInt32(&attempts); got != 4 {
		t.Errorf("expected 4 attempts, got %d", got)
	}
}

func TestBackoffForExponentialGrowth(t *testing.T) {
	p := RetryPolicy{
		InitialBackoff:    100 * time.Millisecond,
		BackoffMultiplier: 2.0,
		MaxBackoff:        10 * time.Second,
	}
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, 100 * time.Millisecond},
		{2, 200 * time.Millisecond},
		{3, 400 * time.Millisecond},
		{4, 800 * time.Millisecond},
	}
	for _, c := range cases {
		if got := p.backoffFor(c.attempt); got != c.want {
			t.Errorf("backoffFor(%d): expected %s, got %s", c.attempt, c.want, got)
		}
	}
}

func TestBackoffForCappedByMaxBackoff(t *testing.T) {
	p := RetryPolicy{
		InitialBackoff:    100 * time.Millisecond,
		BackoffMultiplier: 2.0,
		MaxBackoff:        500 * time.Millisecond,
	}
	// attempt=5 would be 1600ms without cap, capped at 500ms
	if got := p.backoffFor(5); got != 500*time.Millisecond {
		t.Errorf("expected 500ms (cap), got %s", got)
	}
}

func TestDefaultRetryOnTransportErrors(t *testing.T) {
	if !DefaultRetryOn(nil, errors.New("connection reset")) {
		t.Errorf("expected transport error to be retryable")
	}
}

func TestDefaultRetryOnContextErrorsAreNotRetryable(t *testing.T) {
	if DefaultRetryOn(nil, context.Canceled) {
		t.Errorf("context.Canceled must not be retryable")
	}
	if DefaultRetryOn(nil, context.DeadlineExceeded) {
		t.Errorf("context.DeadlineExceeded must not be retryable")
	}
}

func TestDefaultRetryOn5xxRetryable4xxNot(t *testing.T) {
	for _, s := range []int{500, 502, 503, 504} {
		if !DefaultRetryOn(&http.Response{StatusCode: s}, nil) {
			t.Errorf("status %d should be retryable", s)
		}
	}
	for _, s := range []int{400, 401, 403, 404, 409, 422} {
		if DefaultRetryOn(&http.Response{StatusCode: s}, nil) {
			t.Errorf("status %d should NOT be retryable", s)
		}
	}
	for _, s := range []int{200, 201, 204} {
		if DefaultRetryOn(&http.Response{StatusCode: s}, nil) {
			t.Errorf("status %d should NOT be retryable", s)
		}
	}
}

// ─── Retry: context cancellation during backoff ─────────────────────────────

func TestRetrySleepReturnsContextError(t *testing.T) {
	// Use a real sleep (no injected sleep) with a very small backoff
	// and a context that cancels immediately.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	c := New(server.URL, WithRetryPolicy(RetryPolicy{
		MaxAttempts:       3,
		InitialBackoff:    100 * time.Millisecond, // real sleep
		BackoffMultiplier: 2.0,
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the request even starts
	_, err := c.GetMachine(ctx, 42)
	if err == nil {
		t.Fatal("expected context error")
	}
	// The first attempt itself will fail with context.Canceled; the
	// retry loop should honour it without further attempts.
	if !errors.Is(err, context.Canceled) {
		// Accept either the wrapped context error or a "request failed"
		// wrap around it — both are correct.
		if !strings.Contains(err.Error(), "context canceled") {
			t.Errorf("expected context canceled, got %v", err)
		}
	}
}

// ─── Telemetry: WithLogger ──────────────────────────────────────────────────

func TestWithLoggerNilIsNoOp(t *testing.T) {
	// Passing nil to WithLogger must not panic and must leave the
	// default (discardLogger) in place.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"0.14.5","enabled":true,"as_of":"2026-07-17T00:00:00Z"}`))
	}))
	defer server.Close()
	c := New(server.URL, WithLogger(nil))
	if _, err := c.V2Availability(context.Background()); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoggerEmitsDebugForSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"0.14.5","enabled":true,"as_of":"2026-07-17T00:00:00Z"}`))
	}))
	defer server.Close()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := New(server.URL, WithLogger(logger))
	if _, err := c.V2Availability(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"level":"DEBUG"`) {
		t.Errorf("expected DEBUG level, got: %s", out)
	}
	if !strings.Contains(out, `"method":"GET"`) {
		t.Errorf("expected method=GET in log, got: %s", out)
	}
	if !strings.Contains(out, `"path":"/api/v2/"`) {
		t.Errorf("expected path=/api/v2/ in log, got: %s", out)
	}
	if !strings.Contains(out, `"status":200`) {
		t.Errorf("expected status=200 in log, got: %s", out)
	}
}

func TestLoggerEmitsInfoFor401(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := New(server.URL, WithLogger(logger))
	_, _ = c.GetMachine(context.Background(), 42)
	out := buf.String()
	if !strings.Contains(out, `"level":"INFO"`) {
		t.Errorf("expected INFO level for 401, got: %s", out)
	}
	if !strings.Contains(out, `"status":401`) {
		t.Errorf("expected status=401, got: %s", out)
	}
}

func TestLoggerEmitsWarnForRetry(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"id":42,"definition":1,"definition_name":"x","definition_deleted":false,"state":"ok","vars":{},"created_at":"t"}`))
	}))
	defer server.Close()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := New(server.URL,
		WithLogger(logger),
		WithRetryPolicy(RetryPolicy{
			MaxAttempts:       3,
			InitialBackoff:    time.Millisecond,
			BackoffMultiplier: 2.0,
			sleep:             func(time.Duration) {},
		}))
	_, err := c.GetMachine(context.Background(), 42)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"level":"WARN"`) {
		t.Errorf("expected WARN level for retry, got: %s", out)
	}
	if !strings.Contains(out, "retrying") {
		t.Errorf("expected 'retrying' in log, got: %s", out)
	}
}

func TestLoggerNeverLeaksAuthHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version":"0.14.5","enabled":true,"as_of":"2026-07-17T00:00:00Z"}`))
	}))
	defer server.Close()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := New(server.URL,
		WithLogger(logger),
		WithBearerToken("super-secret-token-12345"))
	if _, err := c.V2Availability(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "super-secret-token-12345") {
		t.Errorf("SECURITY: token leaked into log output: %s", out)
	}
	if strings.Contains(strings.ToLower(out), "bearer") {
		t.Errorf("SECURITY: 'bearer' leaked into log output: %s", out)
	}
	if strings.Contains(strings.ToLower(out), "authorization") {
		t.Errorf("SECURITY: 'authorization' leaked into log output: %s", out)
	}
}

func TestLoggerNeverLeaksRequestBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"previous":"a","current":"b","terminal":false,"outputs":[],"vars":{},"history_id":1}`))
	}))
	defer server.Close()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := New(server.URL, WithLogger(logger))
	_, err := c.WalkMachine(context.Background(), 42, WalkRequest{
		Input:   "submit",
		Payload: map[string]interface{}{"sensitive_id": "PII-DO-NOT-LEAK"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "PII-DO-NOT-LEAK") {
		t.Errorf("SECURITY: request body content leaked into log: %s", out)
	}
	if strings.Contains(out, "sensitive_id") {
		t.Errorf("SECURITY: request body field name leaked into log: %s", out)
	}
}

func TestLoggerNeverLeaksResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"sensitive_response":"CUSTOMER-CONFIDENTIAL","previous":"a","current":"b","terminal":false,"outputs":[],"vars":{},"history_id":1}`))
	}))
	defer server.Close()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := New(server.URL, WithLogger(logger))
	_, _ = c.WalkMachine(context.Background(), 42, WalkRequest{Input: "submit"})
	out := buf.String()
	if strings.Contains(out, "CUSTOMER-CONFIDENTIAL") {
		t.Errorf("SECURITY: response body content leaked into log: %s", out)
	}
	if strings.Contains(out, "sensitive_response") {
		t.Errorf("SECURITY: response body field name leaked into log: %s", out)
	}
}

func TestLoggerPathNeverIncludesQueryString(t *testing.T) {
	// The path field must be just the URL path, not the URL with query
	// string, so tenant IDs or filter values don't end up in logs.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"machines":[]}`))
	}))
	defer server.Close()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	c := New(server.URL, WithLogger(logger))
	_, err := c.ListMachines(context.Background(), &MachineFilter{
		Ref: "sensitive-customer-ref-12345",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "sensitive-customer-ref-12345") {
		t.Errorf("SECURITY: query string content leaked into log: %s", out)
	}
}

// ─── Per-call timeout ───────────────────────────────────────────────────────

func TestWithCallTimeoutApplied(t *testing.T) {
	// Server hangs for longer than the call timeout. The client should
	// return before the server responds.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := New(server.URL, WithCallTimeout(50*time.Millisecond))
	start := time.Now()
	_, err := c.V2Availability(context.Background())
	dur := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if dur > 400*time.Millisecond {
		t.Errorf("call took %s, expected <400ms (timeout not applied)", dur)
	}
}

func TestClientWithTimeoutIsNonMutating(t *testing.T) {
	parent := New("http://example", WithCallTimeout(10*time.Second))
	child := parent.WithTimeout(1 * time.Second)
	if parent.callTimeout != 10*time.Second {
		t.Errorf("parent timeout was mutated to %s", parent.callTimeout)
	}
	if child.callTimeout != 1*time.Second {
		t.Errorf("expected child timeout 1s, got %s", child.callTimeout)
	}
	if parent == child {
		t.Errorf("expected distinct client instances")
	}
}

func TestCallerContextDeadlineWinsWhenTighter(t *testing.T) {
	// Client has a 5s call timeout. Caller provides a 50ms deadline.
	// The caller's deadline should win.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	c := New(server.URL, WithCallTimeout(5*time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.V2Availability(ctx)
	dur := time.Since(start)
	if err == nil {
		t.Fatal("expected deadline error")
	}
	if dur > 400*time.Millisecond {
		t.Errorf("call took %s, expected <400ms (caller deadline not honoured)", dur)
	}
}

// ─── WithTenantContext preserves Stage 4 fields ─────────────────────────────

func TestWithTenantContextPreservesRetryLoggerTimeout(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	parent := New("http://example",
		WithRetryPolicy(RetryPolicy{MaxAttempts: 3}),
		WithLogger(logger),
		WithCallTimeout(2*time.Second),
	)
	child := parent.WithTenantContext("0007")
	if child.retry.MaxAttempts != 3 {
		t.Errorf("retry not preserved: %+v", child.retry)
	}
	if child.logger != logger {
		t.Errorf("logger not preserved")
	}
	if child.callTimeout != 2*time.Second {
		t.Errorf("callTimeout not preserved: %s", child.callTimeout)
	}
}

// ─── Retry + telemetry interaction ──────────────────────────────────────────

func TestRetryBackoffSequenceMatchesPolicy(t *testing.T) {
	// Verify the actual sleep durations delivered to the sleep function
	// follow the exponential-backoff schedule.
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	var mu sync.Mutex
	var sleeps []time.Duration
	c := New(server.URL, WithRetryPolicy(RetryPolicy{
		MaxAttempts:       4,
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        10 * time.Second,
		BackoffMultiplier: 2.0,
		sleep: func(d time.Duration) {
			mu.Lock()
			sleeps = append(sleeps, d)
			mu.Unlock()
		},
	}))
	_, _ = c.GetMachine(context.Background(), 42)

	// 4 attempts means 3 backoffs: 100ms, 200ms, 400ms.
	mu.Lock()
	defer mu.Unlock()
	want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond}
	if len(sleeps) != len(want) {
		t.Fatalf("expected %d sleeps, got %d: %v", len(want), len(sleeps), sleeps)
	}
	for i, w := range want {
		if sleeps[i] != w {
			t.Errorf("sleep[%d]: expected %s, got %s", i, w, sleeps[i])
		}
	}
}
