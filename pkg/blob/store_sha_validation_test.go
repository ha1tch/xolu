// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package blob

import (
	"bytes"
	"strings"
	"testing"
)

// D-004: SHA-addressed reads/writes pass the digest straight to blobPath, which
// slices hexSHA[:2] for git-style prefix sharding with no length or hex
// validation (unlike the caller-key path, which is guarded by validateKey).
//
//  1. A digest shorter than two characters ("" or "a") panics in hexSHA[:2].
//  2. A non-hex digest can carry path components (".", "/") into filepath.Join.
//
// Expected end state after the fix: an invalid digest is rejected with a clean
// error (no panic, no filesystem access from malformed input) before any
// slicing or path join.

// GetBySHA / PutBySHA must not panic on a short or empty digest.
func TestGetBySHA_ShortDigest_NoPanic(t *testing.T) {
	s := newTestStore(t)
	for _, bad := range []string{"", "a", "ab", "xyz"} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("GetBySHA(%q) panicked (should return a clean error): %v", bad, r)
				}
			}()
			_, _, err := s.GetBySHA("k", bad)
			if err == nil {
				t.Errorf("GetBySHA(%q): expected an error for an invalid digest, got nil", bad)
			}
		}()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("PutBySHA(%q) panicked (should return a clean error): %v", bad, r)
				}
			}()
			_, _, err := s.PutBySHA("k", bad, "")
			if err == nil {
				t.Errorf("PutBySHA(%q): expected an error for an invalid digest, got nil", bad)
			}
		}()
	}
}

// A non-hex digest carrying path separators must be rejected, not joined into a
// filesystem path.
func TestGetBySHA_NonHexDigest_Rejected(t *testing.T) {
	s := newTestStore(t)
	for _, bad := range []string{
		"../../../etc/passwd",
		"..",
		"zz" + strings.Repeat("0", 62), // right length, non-hex
		strings.Repeat("0", 63),        // hex but wrong length (63 not 64)
		strings.Repeat("0", 65),        // hex but wrong length (65 not 64)
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("GetBySHA(%q) panicked (should return a clean error): %v", bad, r)
				}
			}()
			_, _, err := s.GetBySHA("k", bad)
			if err == nil {
				t.Errorf("GetBySHA(%q): expected rejection of a non-hex/wrong-length digest, got nil", bad)
			}
		}()
	}
}

// Control: a well-formed 64-char lowercase-hex digest is accepted by the
// validator (it then resolves to ErrNotFound for absent content, but must not
// be rejected as malformed). A round-trip via Put confirms a real digest works.
func TestGetBySHA_ValidDigest_RoundTrip(t *testing.T) {
	s := newTestStore(t)
	content := []byte("d004 content")
	sha, _, _, err := s.Put("orig", bytes.NewReader(content), "text/plain")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	rc, _, err := s.GetBySHA("alias", sha)
	if err != nil {
		t.Fatalf("GetBySHA with valid digest: %v", err)
	}
	rc.Close()
}
