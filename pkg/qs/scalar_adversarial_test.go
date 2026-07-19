// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package qs

import (
	"encoding/json"
	"testing"
)

// D-007: OQL-exposed scalar functions do not guard numeric/index edge cases.
//
// (a) ScalarSubstring panics on a negative length: end = start+length can be
//
//	< start, so s[start:end] is an invalid slice. Reachable via
//	SELECT SUBSTRING(field, n, -m). chi's Recoverer catches it (process
//	survives) but the request 500s instead of returning a value.
//
// Expected end state after the fix: a clamped/empty result, never a panic.
func TestScalarSubstring_NegativeLength_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ScalarSubstring panicked on negative length (should clamp): %v", r)
		}
	}()
	_ = ScalarSubstring([]interface{}{"hello", 2.0, -5.0})
}

func TestScalarSubstring_FuzzBounds_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ScalarSubstring panicked during bounds fuzz (should clamp): %v", r)
		}
	}()
	for start := -3; start <= 10; start++ {
		for length := -10; length <= 10; length++ {
			_ = ScalarSubstring([]interface{}{"abcdef", float64(start), float64(length)})
		}
	}
}

// (b) ScalarRound with a large precision yields NaN (math.Pow(10, p) overflows
//
//	to +Inf, then Round(f*Inf)/Inf = NaN). ROUND is OQL-exposed. NaN cannot
//	be JSON-encoded, so before the fix a query selecting ROUND(x, 400) made
//	the whole response fail to marshal.
//
// Expected end state after the fix: a non-finite result is coerced to nil, so
// it is JSON-serialisable (SQL NULL) and the response no longer fails.
func TestScalarRound_HugePrecision_ProducesNaN(t *testing.T) {
	v := ScalarRound([]interface{}{1.23456, 400.0})
	if v != nil {
		t.Errorf("ROUND(x, 400) should coerce a non-finite result to nil, got %v", v)
	}
	if _, err := json.Marshal(map[string]interface{}{"r": v}); err != nil {
		t.Errorf("ROUND(x, 400) = %v is not JSON-serialisable: %v", v, err)
	}
}
