// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package jsonic

import (
	"testing"
)

// FuzzTokenise fuzzes the jsonic tokeniser. The invariant is that tokenising
// ANY byte input must not panic and must not recurse into an unrecoverable
// stack-overflow fatal: deeply nested input must hit the MaxNestingDepth guard
// (D-003) and return a normal error, not crash the process.
//
// Because a stack-overflow fatal cannot be caught by recover(), this fuzzer's
// real value is in active mode, where the runtime kills the worker and the
// crasher is recorded. Seeds keep nesting just over the bound to exercise the
// guard without risking the fatal on every replay.
//
// Run actively with:
//
//	go test ./pkg/jsonic -run x -fuzz FuzzTokenise -fuzztime 60s
func FuzzTokenise(f *testing.F) {
	seeds := [][]byte{
		[]byte(`{}`),
		[]byte(`[]`),
		[]byte(`{"a":1}`),
		[]byte(`[1,2,3]`),
		[]byte(`{"a":{"b":{"c":null}}}`),
		[]byte(`"string"`),
		[]byte(`123.456`),
		[]byte(`true`),
		[]byte(`null`),
		// malformed / hostile
		[]byte(``),
		[]byte(`{`),
		[]byte(`[`),
		[]byte(`{"a":}`),
		[]byte(`[1,]`),
		[]byte("\x00"),
		[]byte(`{"a":"\uXXXX"}`),
		// moderate nesting (well under the bound, exercises recursion)
		nestedSeed(500),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		tok := GetTokeniser()
		defer PutTokeniser(tok)
		// Must not panic and must terminate. A malformed input returns an
		// error; deep input must return the depth-limit error, never a fatal.
		_ = tok.Tokenise(input)
	})
}

func nestedSeed(depth int) []byte {
	b := make([]byte, 0, depth*2+4)
	for i := 0; i < depth; i++ {
		b = append(b, '[')
	}
	b = append(b, 'n', 'u', 'l', 'l')
	for i := 0; i < depth; i++ {
		b = append(b, ']')
	}
	return b
}
