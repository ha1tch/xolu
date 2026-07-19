// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// This file defines the client's retry policy — the automatic retry-with-
// backoff behaviour that Stage 4 introduces.
//
// The default policy is to NOT retry (MaxAttempts=1), matching pre-Stage-4
// behaviour. Retries are enabled by passing WithRetryPolicy(...) at
// construction, or by setting *Client.retry directly.
//
// Retry participation is decided by HTTP method, not by caller opt-in:
//
//   Idempotent per RFC 9110 §9.2.2 → participates in the retry policy
//     GET, HEAD, PUT, DELETE, OPTIONS
//   Not idempotent → NEVER retried, regardless of policy
//     POST, PATCH
//
// This rule is universal across every method the client exposes. There is
// deliberately no per-method opt-in or opt-out: if callers want to retry a
// POST (e.g. a WalkMachine that they know is safe to replay), they wrap the
// call themselves. The client will not retry a POST silently under any
// configuration.

// RetryPolicy configures automatic retries for idempotent requests. The zero
// value corresponds to "no retries" — the same behaviour as pre-Stage-4
// clients.
//
// A caller who wants retries builds the policy explicitly:
//
//	c := client.New(baseURL,
//	    client.WithRetryPolicy(client.RetryPolicy{
//	        MaxAttempts:       3,
//	        InitialBackoff:    200 * time.Millisecond,
//	        MaxBackoff:        5 * time.Second,
//	        BackoffMultiplier: 2.0,
//	    }))
//
// A retry policy applies to a call only when:
//
//   - the HTTP method is idempotent (GET, HEAD, PUT, DELETE, OPTIONS), and
//   - the failure classifies as retryable via RetryOn (default: transport
//     errors and 5xx responses; not 4xx; not context cancellation).
//
// Retries are silent to the caller: telemetry emitted via WithLogger reports
// each retry at warn level, but the returned error and result reflect only
// the final attempt.
type RetryPolicy struct {
	// MaxAttempts is the total number of attempts including the first.
	// MaxAttempts=1 means "no retry", MaxAttempts=3 means "up to two
	// retries after the first attempt". Values less than 1 are treated
	// as 1.
	MaxAttempts int

	// InitialBackoff is the wait before the second attempt. Subsequent
	// attempts wait InitialBackoff * BackoffMultiplier^(attempt-1),
	// capped at MaxBackoff.
	InitialBackoff time.Duration

	// MaxBackoff is the ceiling on backoff between attempts. Zero means
	// "no ceiling", which is rarely what a caller wants; the default
	// policy from DefaultRetryPolicy sets a sensible ceiling.
	MaxBackoff time.Duration

	// BackoffMultiplier is the factor by which backoff grows between
	// attempts. Values less than 1 (including zero) are treated as 1,
	// producing constant backoff.
	BackoffMultiplier float64

	// RetryOn decides whether a given attempt outcome is retryable.
	// If nil, DefaultRetryOn is used, which retries transport errors
	// and 5xx responses.
	//
	// The predicate MUST NOT return true for context cancellation or
	// deadline exceeded — the retry loop honours those errors as final
	// regardless of RetryOn's answer, but the predicate is called first
	// and should return false on them for clarity.
	RetryOn func(resp *http.Response, err error) bool

	// sleep is the sleep function used between attempts. Left nil in
	// public use; tests substitute a deterministic replacement.
	sleep func(time.Duration)
}

// DefaultRetryPolicy is a sensible starting point: three attempts, 200 ms
// initial backoff, 5 s ceiling, doubling. Callers who want retries can pass
// this via WithRetryPolicy directly, or copy and modify it.
//
// This is NOT the client's default when WithRetryPolicy is not supplied —
// that default is "no retries" (MaxAttempts=1) for backwards compatibility
// with pre-Stage-4 client versions.
var DefaultRetryPolicy = RetryPolicy{
	MaxAttempts:       3,
	InitialBackoff:    200 * time.Millisecond,
	MaxBackoff:        5 * time.Second,
	BackoffMultiplier: 2.0,
	// RetryOn nil → DefaultRetryOn used at dispatch time.
}

// DefaultRetryOn is the default retry-decision predicate: it returns true
// for transport-level errors (err != nil and not caused by context) and for
// HTTP 5xx responses.
//
// It returns false on:
//   - success responses (2xx)
//   - client errors (4xx) including 401, 403, 404, 409, 422 — these are
//     not transient and retrying will not fix them
//   - context cancellation or deadline exceeded — the caller has decided
//     the call should stop
func DefaultRetryOn(resp *http.Response, err error) bool {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
		return true
	}
	if resp == nil {
		return false
	}
	return resp.StatusCode >= 500 && resp.StatusCode < 600
}

// isRetryableMethod reports whether an HTTP method is idempotent per
// RFC 9110 §9.2.2 and therefore safe to retry automatically.
func isRetryableMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete, http.MethodOptions:
		return true
	}
	return false
}

// backoffFor computes the wait before the (attempt+1)-th attempt, given
// InitialBackoff, BackoffMultiplier, and MaxBackoff. attempt is 1-indexed:
// attempt=1 means "computing the wait before the second attempt".
//
// The multiplier is applied (attempt-1) times, so:
//
//	attempt=1 → InitialBackoff * multiplier^0 = InitialBackoff
//	attempt=2 → InitialBackoff * multiplier^1
//	attempt=3 → InitialBackoff * multiplier^2
//
// If MaxBackoff > 0, the result is capped at MaxBackoff.
func (p RetryPolicy) backoffFor(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	mult := p.BackoffMultiplier
	if mult < 1 {
		mult = 1
	}
	wait := float64(p.InitialBackoff)
	for i := 1; i < attempt; i++ {
		wait *= mult
	}
	d := time.Duration(wait)
	if p.MaxBackoff > 0 && d > p.MaxBackoff {
		d = p.MaxBackoff
	}
	return d
}

// shouldRetry combines the method-level idempotency check, the attempt
// counter, and the RetryOn predicate. It returns true iff a further attempt
// should be made.
//
// method: the HTTP method of the request
// attempt: the number of attempts already made (1 after the first attempt)
// resp: the response of the just-completed attempt, or nil if err != nil
// err: the error of the just-completed attempt, or nil
func (p RetryPolicy) shouldRetry(method string, attempt int, resp *http.Response, err error) bool {
	if p.MaxAttempts <= 1 {
		return false
	}
	if attempt >= p.MaxAttempts {
		return false
	}
	if !isRetryableMethod(method) {
		return false
	}
	// Context errors are terminal regardless of the predicate.
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
	}
	retryOn := p.RetryOn
	if retryOn == nil {
		retryOn = DefaultRetryOn
	}
	return retryOn(resp, err)
}
