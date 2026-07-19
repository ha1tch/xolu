// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package authmw

import (
	"encoding/base64"
	"testing"
)

// FuzzParseAndValidateJWT fuzzes JWT parsing/validation. Two invariants:
//
//  1. No panic on ANY token string — arbitrary, malformed, truncated bytes in
//     any of the three dot-separated segments must be handled, never crash.
//  2. A token that is not validly signed by `secret` must NEVER be accepted
//     (ok == false). This guards the D-002 area: claim-type normalisation and
//     the mandatory-exp policy must not open an acceptance path for an
//     unsigned or wrongly-signed token.
//
// The fuzzer also builds a correctly-signed token from fuzzed header/payload
// bytes to exercise the post-signature claim path (exp/nbf/iss) with arbitrary
// claim shapes.
//
// Run actively with:
//
//	go test ./pkg/middleware -run x -fuzz FuzzParseAndValidateJWT -fuzztime 60s
func FuzzParseAndValidateJWT(f *testing.F) {
	seeds := []string{
		"",
		"a.b.c",
		"a.b",
		"....",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.sig",
		"not.base64.at.all",
	}
	for _, s := range seeds {
		f.Add(s, []byte(`{"sub":"x"}`))
	}

	const secret = "fuzz-secret"

	f.Fuzz(func(t *testing.T, rawToken string, payload []byte) {
		// 1. Arbitrary token string must not panic, and an arbitrary string is
		//    overwhelmingly not validly signed — if it is somehow accepted, that
		//    is a finding worth surfacing, but the hard contract is no panic.
		_, _ = parseAndValidateJWT(rawToken, secret, "")

		// 2. Build a CORRECTLY-signed token from the fuzzed payload and confirm
		//    the claim path never panics. Then flip one signature byte and
		//    confirm the tampered token is rejected.
		header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
		claims := base64.RawURLEncoding.EncodeToString(payload)
		signingInput := header + "." + claims
		sig := base64.RawURLEncoding.EncodeToString(computeHS256(signingInput, secret))

		signed := signingInput + "." + sig
		_, _ = parseAndValidateJWT(signed, secret, "") // must not panic

		// Tamper: a token signed with the WRONG secret must be rejected.
		badSig := base64.RawURLEncoding.EncodeToString(computeHS256(signingInput, secret+"x"))
		tampered := signingInput + "." + badSig
		if _, ok := parseAndValidateJWT(tampered, secret, ""); ok {
			t.Errorf("wrongly-signed token accepted (payload=%q)", string(payload))
		}
	})
}
