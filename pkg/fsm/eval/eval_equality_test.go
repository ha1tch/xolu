// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package eval

// eval_equality_test.go — regression guard for the @var equality parse bug.
//
// T-SQL parses `@var = expr` in a SELECT column as variable assignment, which
// dropped the comparison and left only the right-hand side. In an FSM guard
// that made every `@var = ...` guard evaluate to the truthiness of the RHS
// (so `@retries = 3` was always "true"). parseExpression now reconstructs the
// equality. These tests lock that behaviour in: equality on a left-hand
// variable must compare, not assign.

import "testing"

func evalGuardBool(t *testing.T, expr string, vars map[string]interface{}) bool {
	t.Helper()
	e := New()
	got, err := EvalGuard(e, expr, vars, nil)
	if err != nil {
		t.Fatalf("EvalGuard(%q): %v", expr, err)
	}
	return got
}

func TestEquality_VarEqualsLiteral(t *testing.T) {
	if evalGuardBool(t, "@a = 3", map[string]interface{}{"a": 1}) {
		t.Error("@a(1) = 3 must be false")
	}
	if !evalGuardBool(t, "@a = 3", map[string]interface{}{"a": 3}) {
		t.Error("@a(3) = 3 must be true")
	}
}

func TestEquality_VarEqualsVar(t *testing.T) {
	if evalGuardBool(t, "@received = @expected", map[string]interface{}{"received": 10, "expected": 30}) {
		t.Error("@received(10) = @expected(30) must be false")
	}
	if !evalGuardBool(t, "@received = @expected", map[string]interface{}{"received": 30, "expected": 30}) {
		t.Error("@received(30) = @expected(30) must be true")
	}
}

func TestEquality_VarNotEquals(t *testing.T) {
	if !evalGuardBool(t, "@a != @b", map[string]interface{}{"a": 1, "b": 2}) {
		t.Error("@a(1) != @b(2) must be true")
	}
	if evalGuardBool(t, "@a != @b", map[string]interface{}{"a": 2, "b": 2}) {
		t.Error("@a(2) != @b(2) must be false")
	}
}

func TestEquality_InequalitiesUnaffected(t *testing.T) {
	// These never went through the assignment misparse, but lock them in too.
	cases := []struct {
		expr string
		vars map[string]interface{}
		want bool
	}{
		{"@a < 3", map[string]interface{}{"a": 1}, true},
		{"@a > 3", map[string]interface{}{"a": 1}, false},
		{"@a >= 3", map[string]interface{}{"a": 3}, true},
		{"@a <= 3", map[string]interface{}{"a": 3}, true},
	}
	for _, c := range cases {
		if got := evalGuardBool(t, c.expr, c.vars); got != c.want {
			t.Errorf("%q vars=%v: want %v, got %v", c.expr, c.vars, c.want, got)
		}
	}
}

func TestEquality_CompoundGuardWithVarEquality(t *testing.T) {
	// The packet-validator shape: presence check AND equality.
	expr := "payload.crc IS NOT NULL AND payload.crc = @received"
	e := New()
	got, err := EvalGuard(e, expr, map[string]interface{}{"received": 42}, map[string]interface{}{"crc": 42})
	if err != nil {
		t.Fatalf("compound guard: %v", err)
	}
	if !got {
		t.Error("payload.crc(42) = @received(42) with presence check must be true")
	}
	got2, _ := EvalGuard(New(), expr, map[string]interface{}{"received": 42}, map[string]interface{}{"crc": 99})
	if got2 {
		t.Error("payload.crc(99) = @received(42) must be false")
	}
}
