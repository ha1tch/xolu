// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package blob

import (
	"testing"
)

// FuzzBlobSHA fuzzes the SHA-addressed blob operations. The invariant is that
// GetBySHA and PutBySHA must not panic on ANY digest string: a malformed digest
// (short, empty, non-hex, path-bearing) must be rejected with a clean error
// before reaching blobPath's hexSHA[:2] slice or filepath.Join (D-004).
//
// Run actively with:
//
//	go test ./pkg/blob -run x -fuzz FuzzBlobSHA -fuzztime 60s
func FuzzBlobSHA(f *testing.F) {
	seeds := []string{
		"",
		"a",
		"ab",
		"xyz",
		"../../../etc/passwd",
		"..",
		"foo/bar",
		"zz" + zeros64(62),
		zeros64(63),
		zeros64(64),
		zeros64(65),
		"AABBCC" + zeros64(58), // uppercase
	}
	for _, s := range seeds {
		f.Add(s)
	}

	dir := f.TempDir()
	store, err := NewStore(dir, 0)
	if err != nil {
		f.Fatalf("NewStore: %v", err)
	}

	f.Fuzz(func(t *testing.T, hexSHA string) {
		// Neither call may panic on any digest. A bad digest returns an error
		// (ErrSHAInvalid / ErrNotFound); it must never slice or join blindly.
		if rc, _, err := store.GetBySHA("k", hexSHA); err == nil && rc != nil {
			rc.Close()
		}
		_, _, _ = store.PutBySHA("k", hexSHA, "")
	})
}

func zeros64(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '0'
	}
	return string(b)
}
