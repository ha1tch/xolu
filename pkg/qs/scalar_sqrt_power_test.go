// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package qs

import (
	"encoding/json"
	"testing"
)

// Found by FuzzScalarFunctions: SQRT and POWER could return NaN/+Inf
// (SQRT(-1), POWER(1e308, 2)), which encoding/json cannot marshal — the same
// failure mode as D-007's ROUND. They are not currently registered on the OQL
// surface, but they share the qs scalar registry (used by OQL and Sulpher), so
// they are coerced to nil (SQL NULL) for safety-by-default. These tests pin
// that behaviour.

func TestScalarSqrt_NonFinite_CoercedToNil(t *testing.T) {
	for _, in := range []float64{-1, -1e308} {
		v := ScalarSqrt([]interface{}{in})
		if v != nil {
			t.Errorf("SQRT(%v) should coerce non-finite result to nil, got %v", in, v)
		}
		if _, err := json.Marshal(map[string]interface{}{"r": v}); err != nil {
			t.Errorf("SQRT(%v) result not JSON-serialisable: %v", in, err)
		}
	}
	// A finite result is unaffected.
	if v := ScalarSqrt([]interface{}{16.0}); v != 4.0 {
		t.Errorf("SQRT(16) = %v, want 4", v)
	}
}

func TestScalarPower_NonFinite_CoercedToNil(t *testing.T) {
	cases := [][2]float64{
		{1e308, 2},  // +Inf
		{-1e308, 3}, // -Inf
	}
	for _, c := range cases {
		v := ScalarPower([]interface{}{c[0], c[1]})
		if v != nil {
			t.Errorf("POWER(%v, %v) should coerce non-finite result to nil, got %v", c[0], c[1], v)
		}
		if _, err := json.Marshal(map[string]interface{}{"r": v}); err != nil {
			t.Errorf("POWER(%v, %v) result not JSON-serialisable: %v", c[0], c[1], err)
		}
	}
	// A finite result is unaffected.
	if v := ScalarPower([]interface{}{2.0, 10.0}); v != 1024.0 {
		t.Errorf("POWER(2, 10) = %v, want 1024", v)
	}
}
