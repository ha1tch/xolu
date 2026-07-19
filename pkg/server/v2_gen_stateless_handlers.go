// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_gen_stateless_handlers.go
//
// Generator logic for the config-bearing types — token, nanoid, random_int,
// and timestamp. Each value is a pure function of its config plus entropy
// (crypto/rand) or the clock. These functions are called by the typed configs
// in v2_gen_config.go and invoked via the named-generator surface
// (POST /gen/{type} + GET /gen/{type}/{name}/next) and @GEN('name').
//
// (The earlier bare per-type endpoints and bare OQL scalars TOKEN()/NANOID()/
// RANDOM_INT()/TIMESTAMP() were retired in favour of the @GEN named-definition
// surface; only the reusable generator logic remains here.)
//
// All random generation uses crypto/rand. timestamp uses Go's embedded IANA tz
// database (time/tzdata, imported for its side effect) so no OS timezone files
// are required.

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	_ "time/tzdata" // embed the IANA tz database for timestamp zone resolution
)

// ─── token ────────────────────────────────────────────────────────────────────
// A URL-safe, cryptographically random opaque string. Suitable for API keys,
// session tokens, reset links, and invite codes.

const tokenAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

const (
	tokenDefaultLength = 32
	tokenMinLength     = 1
	tokenMaxLength     = 512
)

// genToken returns a random token of n characters drawn from the URL-safe
// alphabet using crypto/rand. n is clamped to [tokenMinLength, tokenMaxLength].
func genToken(n int) string {
	if n < tokenMinLength {
		n = tokenDefaultLength
	}
	if n > tokenMaxLength {
		n = tokenMaxLength
	}
	return randomFromAlphabet(tokenAlphabet, n)
}

// ─── nanoid ───────────────────────────────────────────────────────────────────
// Compact, URL-safe unique identifier with a configurable alphabet and length.
// Shorter than uuid/ulid for the same collision resistance at typical sizes.

const nanoidDefaultAlphabet = "_-0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

const (
	nanoidDefaultLength = 21
	nanoidMinLength     = 1
	nanoidMaxLength     = 255
)

// genNanoID returns a nanoid of n characters from the given alphabet. An empty
// alphabet falls back to the default; n is clamped to [min, max].
func genNanoID(alphabet string, n int) string {
	if alphabet == "" {
		alphabet = nanoidDefaultAlphabet
	}
	if n < nanoidMinLength {
		n = nanoidDefaultLength
	}
	if n > nanoidMaxLength {
		n = nanoidMaxLength
	}
	return randomFromAlphabet(alphabet, n)
}

// ─── random_int ───────────────────────────────────────────────────────────────
// A uniformly random integer in [min, max] inclusive, using crypto/rand.

// genRandomInt returns a uniform random integer in [min, max] inclusive. If
// max < min the bounds are swapped; if equal, that value is returned.
func genRandomInt(min, max int64) int64 {
	if max < min {
		min, max = max, min
	}
	if min == max {
		return min
	}
	span := big.NewInt(max - min + 1)
	n, err := rand.Int(rand.Reader, span)
	if err != nil {
		return min // extremely unlikely; deterministic fallback
	}
	return min + n.Int64()
}

// ─── timestamp ────────────────────────────────────────────────────────────────
// The current time, formatted, in a requested timezone. The IANA tz database is
// embedded so any zone name resolves regardless of the host OS.

// genTimestamp returns the current time formatted by layout in the named zone.
// An empty zone means UTC; an empty layout means RFC3339. An unknown zone
// returns an error.
func genTimestamp(zone, layout string) (string, error) {
	loc := time.UTC
	if zone != "" {
		l, err := time.LoadLocation(zone)
		if err != nil {
			return "", fmt.Errorf("unknown timezone %q", zone)
		}
		loc = l
	}
	if layout == "" {
		layout = time.RFC3339
	}
	return time.Now().In(loc).Format(layout), nil
}

// ─── shared entropy helper ────────────────────────────────────────────────────

// randomFromAlphabet returns n characters drawn uniformly from alphabet using
// crypto/rand. Rejection sampling avoids modulo bias.
func randomFromAlphabet(alphabet string, n int) string {
	if alphabet == "" || n <= 0 {
		return ""
	}
	out := make([]byte, n)
	alphaLen := big.NewInt(int64(len(alphabet)))
	for i := 0; i < n; i++ {
		idx, err := rand.Int(rand.Reader, alphaLen)
		if err != nil {
			// Deterministic fallback on the (extremely unlikely) RNG failure.
			out[i] = alphabet[0]
			continue
		}
		out[i] = alphabet[idx.Int64()]
	}
	return string(out)
}
