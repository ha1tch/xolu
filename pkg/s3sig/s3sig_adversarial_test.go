// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Adversarial tests for SigV4 verification. The round-trip, wrong-secret,
// tampered-URI, malformed-header and AWS known-answer cases live in
// s3sig_test.go; this file targets signature-forgery and request-manipulation
// vectors: tampering with signed headers, payload hash, credential scope, and
// the signature itself, plus no-panic on hostile Verify inputs.

package s3sig

import (
	"strings"
	"testing"
)

const advSec = "adversarial-s3-secret-000000"

// baseComp returns a signed, verifiable request as a starting point; tests then
// mutate one field and assert verification fails.
func baseComp() (Components, string) {
	c := Components{
		AccessKey:   "AKIATEST",
		Date:        "20260101",
		Region:      "us-east-1",
		Service:     "s3",
		Method:      "GET",
		CanonURI:    "/acme/object.txt",
		CanonQuery:  "",
		Headers:     map[string]string{"host": "xolu.example", "x-amz-date": "20260101T000000Z", "x-amz-content-sha256": "UNSIGNED-PAYLOAD"},
		PayloadHash: "UNSIGNED-PAYLOAD",
		AmzDate:     "20260101T000000Z",
	}
	auth := Sign(advSec, c, []string{"host", "x-amz-date", "x-amz-content-sha256"})
	return c, auth
}

// reparse rebuilds a Components from the auth header plus the (possibly mutated)
// request facts, exactly as the server does.
func reparse(t *testing.T, auth string, c Components) Components {
	t.Helper()
	p, err := ParseAuthorization(auth)
	if err != nil {
		t.Fatalf("ParseAuthorization: %v", err)
	}
	p.Method = c.Method
	p.CanonURI = c.CanonURI
	p.CanonQuery = c.CanonQuery
	p.Headers = c.Headers
	p.PayloadHash = c.PayloadHash
	p.AmzDate = c.AmzDate
	return p
}

// Tampering with any signed request fact must break verification.
func TestAdvSig_TamperedFacts(t *testing.T) {
	mutators := map[string]func(*Components){
		"method":       func(c *Components) { c.Method = "DELETE" },
		"uri":          func(c *Components) { c.CanonURI = "/other/object.txt" },
		"query":        func(c *Components) { c.CanonQuery = "list-type=2" },
		"payload hash": func(c *Components) { c.PayloadHash = strings.Repeat("a", 64) },
		"host header":  func(c *Components) { c.Headers = cloneWith(c.Headers, "host", "evil.example") },
		"amz-date": func(c *Components) {
			c.AmzDate = "20260202T000000Z"
			c.Headers = cloneWith(c.Headers, "x-amz-date", "20260202T000000Z")
		},
	}
	for name, mutate := range mutators {
		t.Run(name, func(t *testing.T) {
			c, auth := baseComp()
			mutate(&c)
			p := reparse(t, auth, c)
			if err := p.Verify(advSec); err == nil {
				t.Errorf("verification passed after tampering with %s", name)
			}
		})
	}
}

// Tampering with the signature string itself must fail.
func TestAdvSig_TamperedSignature(t *testing.T) {
	c, auth := baseComp()

	cases := map[string]string{
		"flipped last chars": auth[:len(auth)-2] + "00",
		"empty signature":    replaceSig(auth, ""),
		"truncated":          replaceSig(auth, "abc123"),
		"uppercased hex":     replaceSig(auth, strings.ToUpper(extractSig(auth))),
		"all zeros":          replaceSig(auth, strings.Repeat("0", 64)),
	}
	for name, badAuth := range cases {
		t.Run(name, func(t *testing.T) {
			p, err := ParseAuthorization(badAuth)
			if err != nil {
				return // malformed-at-parse is an acceptable rejection
			}
			p.Method, p.CanonURI, p.Headers = c.Method, c.CanonURI, c.Headers
			p.PayloadHash, p.AmzDate = c.PayloadHash, c.AmzDate
			if err := p.Verify(advSec); err == nil {
				t.Errorf("verification passed with %s signature", name)
			}
		})
	}
}

// Claiming to have signed a header but presenting a different value for it must
// fail (the signed value is part of the canonical request).
func TestAdvSig_SignedHeaderManipulation(t *testing.T) {
	c, auth := baseComp()
	// The signature covered host=xolu.example; present a different host at verify.
	p := reparse(t, auth, c)
	p.Headers = cloneWith(p.Headers, "host", "attacker.example")
	if err := p.Verify(advSec); err == nil {
		t.Error("verification passed with a swapped signed host header")
	}
}

// Credential-scope mismatch (date/region/service differ from what was signed)
// must fail, since the scope feeds the signing key derivation.
func TestAdvSig_ScopeMismatch(t *testing.T) {
	scopeMutators := map[string]func(*Components){
		"date":    func(c *Components) { c.Date = "20260202" },
		"region":  func(c *Components) { c.Region = "eu-west-1" },
		"service": func(c *Components) { c.Service = "iam" },
	}
	for name, mutate := range scopeMutators {
		t.Run(name, func(t *testing.T) {
			c, auth := baseComp()
			p, err := ParseAuthorization(auth)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			// Apply request facts, then corrupt the parsed scope.
			p.Method, p.CanonURI, p.Headers = c.Method, c.CanonURI, c.Headers
			p.PayloadHash, p.AmzDate = c.PayloadHash, c.AmzDate
			mutate(&p)
			if err := p.Verify(advSec); err == nil {
				t.Errorf("verification passed with mismatched %s in scope", name)
			}
		})
	}
}

// Verify must not panic on hostile / empty Components.
func TestAdvSig_NoPanicOnHostileInput(t *testing.T) {
	inputs := []Components{
		{},
		{Signature: "x"},
		{Headers: nil, SignedHeaders: []string{"host"}},
		{SignedHeaders: nil, PayloadHash: ""},
		{Headers: map[string]string{}, SignedHeaders: []string{"host", "x-amz-date"}},
	}
	for i, c := range inputs {
		// Must return an error (mismatch), never panic.
		if err := c.Verify(advSec); err == nil {
			t.Errorf("input %d: empty/hostile components verified successfully", i)
		}
	}
}

// ParseAuthorization must not panic on adversarial header strings.
func TestAdvSig_ParseNoPanic(t *testing.T) {
	hostile := []string{
		"AWS4-HMAC-SHA256",
		"AWS4-HMAC-SHA256 ",
		"AWS4-HMAC-SHA256 Credential=",
		"AWS4-HMAC-SHA256 Credential=/////",
		"AWS4-HMAC-SHA256 Credential=a/b/c/d/e/f/g, SignedHeaders=, Signature=",
		"AWS4-HMAC-SHA256 " + strings.Repeat("Credential=x/y/z/s/aws4_request, ", 1000),
		"AWS4-HMAC-SHA256 Signature=\x00\x00",
		"AWS4-HMAC-SHA256 SignedHeaders=;;;;;",
	}
	for _, h := range hostile {
		_, _ = ParseAuthorization(h) // only asserting no panic
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func cloneWith(m map[string]string, k, v string) map[string]string {
	out := make(map[string]string, len(m))
	for kk, vv := range m {
		out[kk] = vv
	}
	out[k] = v
	return out
}

func extractSig(auth string) string {
	const p = "Signature="
	i := strings.Index(auth, p)
	if i < 0 {
		return ""
	}
	return auth[i+len(p):]
}

func replaceSig(auth, newSig string) string {
	const p = "Signature="
	i := strings.Index(auth, p)
	if i < 0 {
		return auth
	}
	return auth[:i+len(p)] + newSig
}
