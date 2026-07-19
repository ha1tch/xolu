// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package qs

import (
	"encoding/json"
	"math"
	"testing"
)

// FuzzScalarFunctions fuzzes the OQL-exposed scalar functions. The invariant is
// that every registered scalar, on ANY argument vector, must not panic and must
// return a JSON-serialisable value (no NaN/Inf, which encoding/json rejects).
// This is the broad net behind D-007: the committed regression pins SUBSTRING's
// negative-length panic and ROUND's NaN; this fuzzer searches every registered
// scalar for the next such case.
//
// The argument vector is derived from a fuzzed string, a fuzzed float, and a
// fuzzed int, covering the (string, number, index) shapes these functions take.
//
// Run actively with:
//
//	go test ./pkg/qs -run x -fuzz FuzzScalarFunctions -fuzztime 60s
func FuzzScalarFunctions(f *testing.F) {
	seeds := []struct {
		s string
		x float64
		n int
	}{
		{"hello", 2, -5},       // D-007 SUBSTRING negative length
		{"hello", 1.2345, 400}, // D-007 ROUND huge precision
		{"", 0, 0},
		{"abc", -1, -1},
		{"banana", 1e308, 2},
		{"x", math.MaxFloat64, 64},
		{"  spaced  ", 3.14, 10},
	}
	for _, s := range seeds {
		f.Add(s.s, s.x, s.n)
	}

	f.Fuzz(func(t *testing.T, s string, x float64, n int) {
		// Argument vectors covering the shapes scalars accept: (s), (s, n),
		// (x, n), (s, n, n).
		argVectors := [][]interface{}{
			{s},
			{s, float64(n)},
			{x, float64(n)},
			{s, float64(n), float64(n)},
			{x},
		}
		for name, fn := range ScalarFunctions {
			for _, args := range argVectors {
				result := fn(args) // must not panic
				// Result must be JSON-serialisable: no NaN/Inf leaks into a row.
				if _, err := json.Marshal(map[string]interface{}{"r": result}); err != nil {
					t.Errorf("scalar %s(%v) returned non-serialisable %v: %v", name, args, result, err)
				}
			}
		}
	})
}
