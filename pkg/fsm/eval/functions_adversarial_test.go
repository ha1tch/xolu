// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package eval

import "testing"

// The existing eval adversarial suite covers operator/structural hostility but
// never exercises FSM *function arguments*. These tests cover that gap (D-008):
// string functions with out-of-range indices panic, and REPLICATE/SPACE/STR
// allocate proportional to an unbounded user integer.
//
// Guards are evaluated at transition time (definition validation is parse-only),
// so a malicious guard fires when an event drives the machine.

// SUBSTRING / STUFF with a negative length panic via an invalid slice.
func TestFunctions_StringSlice_NoPanic(t *testing.T) {
	guards := []string{
		"SUBSTRING('hello', 1, -5) = ''",
		"SUBSTRING('hello', 2, -100) = ''",
		"STUFF('hello', 1, -9, 'X') = ''",
	}
	vars := map[string]interface{}{}
	for _, g := range guards {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("guard %q panicked (should return value or clean error): %v", g, r)
				}
			}()
			e := New()
			_, _ = EvalGuard(e, g, vars, nil)
		}()
	}
}

// REPLICATE / SPACE / STR allocate ~ the user-controlled count. Before the
// D-008 fix this was unbounded: at attack scale (~1e12) an unrecoverable fatal
// OOM that recover() cannot catch. After the fix, an attack-scale count must
// return a clean error (not allocate, not panic), while a legitimate small
// count still succeeds.
func TestFunctions_UnboundedAllocation(t *testing.T) {
	// Attack-scale counts must be rejected with a clean error.
	attack := []struct{ name, expr string }{
		{"REPLICATE", "LEN(REPLICATE('xy', 1000000000000)) > 0"},
		{"SPACE", "LEN(SPACE(1000000000000)) > 0"},
		{"STR", "LEN(STR(1.0, 1000000000000)) > 0"},
	}
	for _, g := range attack {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s at attack scale panicked (should return clean error): %v", g.name, r)
				}
			}()
			e := New()
			_, err := EvalGuard(e, g.expr, map[string]interface{}{}, nil)
			if err == nil {
				t.Errorf("%s at attack scale: expected a bounded-output error, got nil (no upper bound on the count argument)", g.name)
			}
		}()
	}

	// Legitimate small counts must still succeed.
	ok := []struct{ name, expr string }{
		{"REPLICATE", "REPLICATE('ab', 3) = 'ababab'"},
		{"SPACE", "LEN(SPACE(4)) = 4"},
		{"STR", "LEN(STR(1.0, 10)) > 0"},
	}
	for _, g := range ok {
		e := New()
		if _, err := EvalGuard(e, g.expr, map[string]interface{}{}, nil); err != nil {
			t.Errorf("%s with a small count returned an error (should succeed): %v", g.name, err)
		}
	}
}
