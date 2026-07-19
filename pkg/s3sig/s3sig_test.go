// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package s3sig

import "testing"

func sampleComponents() Components {
	return Components{
		AccessKey:   "AKIAALPHA",
		Date:        "20260101",
		Region:      "us-east-1",
		Service:     "s3",
		Method:      "GET",
		CanonURI:    "/alpha/object.txt",
		CanonQuery:  "",
		Headers:     map[string]string{"host": "xolu.example", "x-amz-content-sha256": "UNSIGNED-PAYLOAD", "x-amz-date": "20260101T000000Z"},
		PayloadHash: "UNSIGNED-PAYLOAD",
		AmzDate:     "20260101T000000Z",
	}
}

// Sign then Verify must agree (the central contract between server and tests).
func TestSignVerify_RoundTrip(t *testing.T) {
	const secret = "test-secret-roundtrip"
	c := sampleComponents()
	signed := []string{"host", "x-amz-content-sha256", "x-amz-date"}

	authHeader := Sign(secret, c, signed)

	parsed, err := ParseAuthorization(authHeader)
	if err != nil {
		t.Fatalf("ParseAuthorization: %v", err)
	}
	// Caller fills request facts back in (the server does this from *http.Request).
	parsed.Method = c.Method
	parsed.CanonURI = c.CanonURI
	parsed.CanonQuery = c.CanonQuery
	parsed.Headers = c.Headers
	parsed.PayloadHash = c.PayloadHash
	parsed.AmzDate = c.AmzDate

	if err := parsed.Verify(secret); err != nil {
		t.Errorf("Verify after Sign failed: %v", err)
	}
}

// A wrong secret must fail verification.
func TestVerify_WrongSecret(t *testing.T) {
	c := sampleComponents()
	signed := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	authHeader := Sign("the-real-secret", c, signed)

	parsed, _ := ParseAuthorization(authHeader)
	parsed.Method, parsed.CanonURI, parsed.Headers = c.Method, c.CanonURI, c.Headers
	parsed.PayloadHash, parsed.AmzDate = c.PayloadHash, c.AmzDate

	if err := parsed.Verify("the-wrong-secret"); err == nil {
		t.Error("verification passed with wrong secret")
	}
}

// Tampering with a signed value (the URI) must fail verification.
func TestVerify_TamperedRequest(t *testing.T) {
	const secret = "tamper-secret"
	c := sampleComponents()
	signed := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	authHeader := Sign(secret, c, signed)

	parsed, _ := ParseAuthorization(authHeader)
	parsed.Method = c.Method
	parsed.CanonURI = "/beta/object.txt" // attacker changed the bucket/path
	parsed.Headers = c.Headers
	parsed.PayloadHash, parsed.AmzDate = c.PayloadHash, c.AmzDate

	if err := parsed.Verify(secret); err == nil {
		t.Error("verification passed despite tampered URI")
	}
}

// Malformed Authorization headers are rejected by the parser.
func TestParseAuthorization_Malformed(t *testing.T) {
	bad := []string{
		"",
		"Basic abc",
		"AWS4-HMAC-SHA256 Credential=ak/20260101/us-east-1/s3",                  // missing terminator
		"AWS4-HMAC-SHA256 SignedHeaders=host, Signature=x",                      // no credential
		"AWS4-HMAC-SHA256 Credential=ak/d/r/s/aws4_request, SignedHeaders=host", // no signature
	}
	for _, h := range bad {
		if _, err := ParseAuthorization(h); err == nil {
			t.Errorf("expected error for %q", h)
		}
	}
}

// Known-answer test against the AWS SigV4 documented example
// ("GET / " on examplebucket). This proves the canonical-request and signing-key
// derivation match the real AWS algorithm, not merely our own Sign.
// Reference values are from the AWS "Signature Calculations for S3" example.
func TestKnownAnswer_AWSExample(t *testing.T) {
	// AWS documented example for GET an object from examplebucket.
	const (
		secret    = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
		accessKey = "AKIAIOSFODNN7EXAMPLE"
		date      = "20130524"
		region    = "us-east-1"
		service   = "s3"
		amzDate   = "20130524T000000Z"
		// SHA256("") payload for the GET example.
		payload       = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
		wantSignature = "f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41"
	)
	c := Components{
		AccessKey:  accessKey,
		Date:       date,
		Region:     region,
		Service:    service,
		Method:     "GET",
		CanonURI:   "/test.txt",
		CanonQuery: "",
		Headers: map[string]string{
			"host":                 "examplebucket.s3.amazonaws.com",
			"range":                "bytes=0-9",
			"x-amz-content-sha256": payload,
			"x-amz-date":           amzDate,
		},
		PayloadHash:   payload,
		AmzDate:       amzDate,
		SignedHeaders: []string{"host", "range", "x-amz-content-sha256", "x-amz-date"},
	}
	got := c.Compute(secret)
	if got != wantSignature {
		t.Errorf("AWS known-answer mismatch:\n got  %s\n want %s", got, wantSignature)
	}
}
