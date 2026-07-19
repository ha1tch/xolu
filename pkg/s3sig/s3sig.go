// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package s3sig implements the subset of AWS Signature Version 4 needed to
// authenticate S3-style requests against xolu's S3 gateway. It provides both
// Sign (used by tests and tooling) and Verify (used by the server), built on a
// single shared canonical-request derivation so the two cannot drift.
//
// Scope: this covers header-based SigV4 (Authorization header), the form real
// S3 clients (aws-cli, boto3, minio-go) produce. It does not implement
// presigned-URL (query-parameter) signing or chunked/streaming signed payloads.
// The payload hash is taken from the x-amz-content-sha256 header as the client
// provides it (including the literal "UNSIGNED-PAYLOAD"), matching standard S3
// client behaviour.
package s3sig

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
)

const (
	algorithm   = "AWS4-HMAC-SHA256"
	terminator  = "aws4_request"
	emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
)

// Components is the parsed content of a SigV4 Authorization header plus the
// request facts needed to recompute the signature.
type Components struct {
	AccessKey     string   // from Credential
	Date          string   // yyyymmdd, from Credential scope
	Region        string   // from Credential scope
	Service       string   // from Credential scope (normally "s3")
	SignedHeaders []string // lower-case header names, in signed order
	Signature     string   // hex signature presented by the client

	Method      string            // HTTP method
	CanonURI    string            // canonical (path) URI
	CanonQuery  string            // canonical query string
	Headers     map[string]string // request headers (canonical-cased keys ok; looked up case-insensitively)
	PayloadHash string            // value of x-amz-content-sha256
	AmzDate     string            // value of x-amz-date (full timestamp)
}

var (
	// ErrMalformed indicates the Authorization header could not be parsed.
	ErrMalformed = errors.New("s3sig: malformed Authorization header")
	// ErrMismatch indicates the recomputed signature did not match.
	ErrMismatch = errors.New("s3sig: signature mismatch")
)

// ParseAuthorization parses a SigV4 "Authorization" header value into its
// Credential / SignedHeaders / Signature parts. Request facts (method, URI,
// etc.) must be filled in by the caller before Verify.
func ParseAuthorization(auth string) (Components, error) {
	var c Components
	if !strings.HasPrefix(auth, algorithm) {
		return c, ErrMalformed
	}
	rest := strings.TrimSpace(auth[len(algorithm):])
	// rest is "Credential=..., SignedHeaders=..., Signature=..."
	for _, part := range strings.Split(rest, ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "Credential="):
			scope := strings.SplitN(part[len("Credential="):], "/", 5)
			if len(scope) != 5 || scope[4] != terminator {
				return c, ErrMalformed
			}
			c.AccessKey, c.Date, c.Region, c.Service = scope[0], scope[1], scope[2], scope[3]
		case strings.HasPrefix(part, "SignedHeaders="):
			c.SignedHeaders = strings.Split(part[len("SignedHeaders="):], ";")
		case strings.HasPrefix(part, "Signature="):
			c.Signature = part[len("Signature="):]
		}
	}
	if c.AccessKey == "" || len(c.SignedHeaders) == 0 || c.Signature == "" {
		return c, ErrMalformed
	}
	return c, nil
}

// canonicalRequest builds the SigV4 canonical request string.
func (c Components) canonicalRequest() string {
	var ch strings.Builder
	for _, h := range c.SignedHeaders {
		ch.WriteString(strings.ToLower(h))
		ch.WriteString(":")
		ch.WriteString(strings.TrimSpace(headerLookup(c.Headers, h)))
		ch.WriteString("\n")
	}
	payload := c.PayloadHash
	if payload == "" {
		payload = emptySHA256
	}
	return strings.Join([]string{
		c.Method,
		c.CanonURI,
		c.CanonQuery,
		ch.String(),
		strings.Join(lowerAll(c.SignedHeaders), ";"),
		payload,
	}, "\n")
}

// stringToSign builds the SigV4 string-to-sign.
func (c Components) stringToSign(canonReqHash string) string {
	scope := strings.Join([]string{c.Date, c.Region, c.Service, terminator}, "/")
	amzDate := c.AmzDate
	if amzDate == "" {
		amzDate = headerLookup(c.Headers, "x-amz-date")
	}
	return strings.Join([]string{algorithm, amzDate, scope, canonReqHash}, "\n")
}

// signingKey derives the SigV4 signing key from the secret via the HMAC chain.
func signingKey(secret, date, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, terminator)
}

// Compute returns the hex signature for the components using the given secret.
func (c Components) Compute(secret string) string {
	canonHash := sha256Hex(c.canonicalRequest())
	sts := c.stringToSign(canonHash)
	key := signingKey(secret, c.Date, c.Region, c.Service)
	return hex.EncodeToString(hmacSHA256(key, sts))
}

// Verify recomputes the signature with the secret and constant-time compares it
// to the presented one.
func (c Components) Verify(secret string) error {
	want := c.Compute(secret)
	if subtle.ConstantTimeCompare([]byte(want), []byte(c.Signature)) != 1 {
		return ErrMismatch
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// headerLookup finds a header value case-insensitively.
func headerLookup(headers map[string]string, name string) string {
	if v, ok := headers[name]; ok {
		return v
	}
	lname := strings.ToLower(name)
	for k, v := range headers {
		if strings.ToLower(k) == lname {
			return v
		}
	}
	return ""
}

func lowerAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToLower(s)
	}
	return out
}

// SortedSignedHeaders returns the signed header names sorted, as SigV4 requires.
func SortedSignedHeaders(headers []string) []string {
	out := lowerAll(headers)
	sort.Strings(out)
	return out
}

// Sign builds a complete SigV4 Authorization header value for the given request
// facts and secret. It is the inverse of Verify and shares the same canonical
// derivation, so a header produced by Sign always verifies with the same
// secret. Intended for tests and tooling (the server only verifies).
//
// signedHeaders must name the headers present in the headers map that should be
// signed; they are lower-cased and sorted internally.
func Sign(secret string, c Components, signedHeaders []string) string {
	c.SignedHeaders = SortedSignedHeaders(signedHeaders)
	sig := c.Compute(secret)
	scope := strings.Join([]string{c.Date, c.Region, c.Service, terminator}, "/")
	return algorithm +
		" Credential=" + c.AccessKey + "/" + scope +
		", SignedHeaders=" + strings.Join(c.SignedHeaders, ";") +
		", Signature=" + sig
}
