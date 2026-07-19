// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package eval

// eval_adversarial_test.go — adversarial battery for the FSM guard/set engine.
//
// These target the least-explored corners of T-SQL expression evaluation as it
// is used in FSM guards and set clauses: NULL three-valued logic, operator
// precedence around the @var-equality reconstruction, type coercion at
// comparison boundaries, and arithmetic edge cases. Some assert fixed bugs;
// others lock in behaviour that is correct-but-surprising so it cannot silently
// drift. Where a result is a deliberate T-SQL semantic (not a bug), the test
// name and comment say so.

import "testing"

func adv(t *testing.T, expr string, vars, pl map[string]interface{}) bool {
	t.Helper()
	e := New()
	got, err := EvalGuard(e, expr, vars, pl)
	if err != nil {
		t.Fatalf("EvalGuard(%q): unexpected error %v", expr, err)
	}
	return got
}

func advSet(t *testing.T, expr string, vars map[string]interface{}) (interface{}, error) {
	t.Helper()
	e := New()
	return EvalSet(e, expr, vars)
}

// ─── Precedence around @var-equality reconstruction (regression: patch010 fix) ─
//
// `@var = X` is reconstructed from a T-SQL assignment misparse. The fix must
// keep AND/OR (which bind looser than `=`) above the equality, not swallowed
// into its right operand. Before the precedence-aware rebalance,
// `@n = 5 OR @a = 1` evaluated as `@n = (5 OR @a = 1)` and a NULL left made the
// whole thing false even when the OR should be true.

func TestAdversarial_VarEqualityOrPrecedence(t *testing.T) {
	cases := []struct {
		expr string
		vars map[string]interface{}
		want bool
	}{
		{"@n = 5 OR @a = 1", map[string]interface{}{"a": 1}, true}, // NULL OR true
		{"@a = 1 OR @n = 5", map[string]interface{}{"a": 1}, true}, // true OR NULL
		{"@a = 5 OR @b = 1", map[string]interface{}{"a": 9, "b": 1}, true},
		{"@a = 5 OR @b = 1", map[string]interface{}{"a": 9, "b": 9}, false},
		{"@a = 1 OR @b = 2 OR @c = 3", map[string]interface{}{"a": 9, "b": 9, "c": 3}, true},
	}
	for _, c := range cases {
		if got := adv(t, c.expr, c.vars, nil); got != c.want {
			t.Errorf("%q vars=%v: want %v, got %v", c.expr, c.vars, c.want, got)
		}
	}
}

func TestAdversarial_VarEqualityAndPrecedence(t *testing.T) {
	cases := []struct {
		expr string
		vars map[string]interface{}
		want bool
	}{
		{"@a = 1 AND @b = 2", map[string]interface{}{"a": 1, "b": 2}, true},
		{"@a = 1 AND @b = 2", map[string]interface{}{"a": 1, "b": 9}, false},
		{"@a = 1 AND @b = 2 AND @c = 3", map[string]interface{}{"a": 1, "b": 2, "c": 3}, true},
		// Mixed: equality with presence check, the packet-validator shape.
		{"@a IS NOT NULL AND @a = 1", map[string]interface{}{"a": 1}, true},
		{"@a IS NOT NULL AND @a = 1", map[string]interface{}{}, false},
	}
	for _, c := range cases {
		if got := adv(t, c.expr, c.vars, nil); got != c.want {
			t.Errorf("%q vars=%v: want %v, got %v", c.expr, c.vars, c.want, got)
		}
	}
}

// ─── NULL three-valued logic ──────────────────────────────────────────────────

func TestAdversarial_NullThreeValuedLogic(t *testing.T) {
	cases := []struct {
		name string
		expr string
		vars map[string]interface{}
		want bool
	}{
		// NULL = NULL is NULL (not true), coerced to false in boolean context.
		{"null_eq_null", "@a = @b", map[string]interface{}{}, false},
		// NULL AND FALSE = FALSE.
		{"null_and_false", "@missing = 1 AND @a = 99", map[string]interface{}{"a": 1}, false},
		// NULL AND TRUE = NULL -> false.
		{"null_and_true", "@missing = 1 AND @a = 1", map[string]interface{}{"a": 1}, false},
		// NULL OR TRUE = TRUE.
		{"null_or_true", "@missing = 1 OR @a = 1", map[string]interface{}{"a": 1}, true},
		// NULL OR FALSE = NULL -> false.
		{"null_or_false", "@missing = 1 OR @a = 2", map[string]interface{}{"a": 1}, false},
		// NOT NULL = NULL -> false (NOT of an unknown is unknown).
		{"not_null", "NOT (@missing = 1)", map[string]interface{}{}, false},
		// IS NULL / IS NOT NULL are the reliable presence tests.
		{"is_null_present", "@a IS NULL", map[string]interface{}{"a": 1}, false},
		{"is_null_absent", "@a IS NULL", map[string]interface{}{}, true},
		{"is_not_null_present", "@a IS NOT NULL", map[string]interface{}{"a": 1}, true},
		{"is_not_null_absent", "@a IS NOT NULL", map[string]interface{}{}, false},
		{"both_null", "@a IS NULL AND @b IS NULL", map[string]interface{}{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := adv(t, c.expr, c.vars, nil); got != c.want {
				t.Errorf("%q vars=%v: want %v, got %v", c.expr, c.vars, c.want, got)
			}
		})
	}
}

// ─── Type coercion at comparison boundaries ───────────────────────────────────
//
// JSON numbers arrive as float64 and compare numerically with int literals and
// int variables — the common, safe case. A genuinely string-typed value
// compared with a number is compared LEXICALLY, not numerically: this is a
// T-SQL type-mismatch behaviour, not a bug, but it is a trap for validator
// authors and is locked in here so it is documented-by-test. The defensive
// pattern is to compare like types.

func TestAdversarial_NumericTypesCompareNumerically(t *testing.T) {
	cases := []struct {
		expr string
		vars map[string]interface{}
		want bool
	}{
		{"@a = 10", map[string]interface{}{"a": float64(10)}, true},
		{"@a > 5", map[string]interface{}{"a": float64(10)}, true},
		{"@a = 10", map[string]interface{}{"a": int64(10)}, true},
		{"@a > 5", map[string]interface{}{"a": 10}, true},
		{"@a < 5", map[string]interface{}{"a": float64(2.5)}, true},
	}
	for _, c := range cases {
		if got := adv(t, c.expr, c.vars, nil); got != c.want {
			t.Errorf("%q vars=%v: want %v, got %v", c.expr, c.vars, c.want, got)
		}
	}
}

func TestAdversarial_StringVsNumberIsLexical(t *testing.T) {
	// DOCUMENTED BEHAVIOUR, NOT A BUG: a string-typed value vs a number is
	// compared as strings. "10" > 5 is false because string "10" coerces and
	// compares lexically. Validator authors must compare like types; this test
	// pins the behaviour so a future change is a conscious decision.
	if adv(t, "payload.n > 5", nil, map[string]interface{}{"n": "10"}) {
		t.Errorf(`payload.n("10") > 5 returned true; expected false (lexical string comparison)`)
	}
	// Equality of a numeric string with an int literal does coerce true here.
	if !adv(t, "payload.n = 10", nil, map[string]interface{}{"n": "10"}) {
		t.Errorf(`payload.n("10") = 10 returned false; expected true (string/int equality coercion)`)
	}
}

// ─── Operator forms and precedence ────────────────────────────────────────────

func TestAdversarial_OperatorForms(t *testing.T) {
	cases := []struct {
		expr string
		vars map[string]interface{}
		want bool
	}{
		{"@a <> @b", map[string]interface{}{"a": 1, "b": 2}, true},  // <> inequality
		{"@a != @b", map[string]interface{}{"a": 1, "b": 2}, true},  // != inequality
		{"@a <> @b", map[string]interface{}{"a": 2, "b": 2}, false}, // <> equal
		{"@a >= @b AND @a <= @c", map[string]interface{}{"a": 5, "b": 1, "c": 9}, true},
		// Precedence: AND binds tighter than OR.
		{"@a = 1 OR @b = 1 AND @c = 1", map[string]interface{}{"a": 1, "b": 9, "c": 9}, true},
		// false OR (true AND false) = false  (c=8 makes @c=9 false)
		{"@a = 9 OR @b = 1 AND @c = 9", map[string]interface{}{"a": 8, "b": 1, "c": 8}, false},
		// false OR (true AND true) = true
		{"@a = 9 OR @b = 1 AND @c = 9", map[string]interface{}{"a": 8, "b": 1, "c": 9}, true},
	}
	for _, c := range cases {
		if got := adv(t, c.expr, c.vars, nil); got != c.want {
			t.Errorf("%q vars=%v: want %v, got %v", c.expr, c.vars, c.want, got)
		}
	}
}

// ─── Arithmetic edge cases in set clauses ─────────────────────────────────────
//
// These pin the current behaviour of set-clause arithmetic so a regression is
// visible. Division by zero and overflow currently produce a value rather than
// an error; these are documented behaviours, captured here, and are candidates
// for stricter handling in a future change.

func TestAdversarial_SetArithmeticEdges(t *testing.T) {
	// Division by zero currently yields null rather than erroring.
	v, err := advSet(t, "@a / @b", map[string]interface{}{"a": 10, "b": 0})
	if err != nil {
		t.Logf("div-by-zero errored (acceptable): %v", err)
	} else if v != nil {
		t.Errorf("div-by-zero: expected nil or error, got %v", v)
	}

	// Normal integer arithmetic.
	v2, err := advSet(t, "@a + @b * @c", map[string]interface{}{"a": 2, "b": 3, "c": 4})
	if err != nil {
		t.Fatalf("arithmetic precedence: %v", err)
	}
	if iv, _ := v2.(int64); iv != 14 { // 2 + (3*4)
		t.Errorf("arithmetic precedence 2+3*4: want 14, got %v (%T)", v2, v2)
	}

	// Subtraction into negative.
	v3, _ := advSet(t, "@a - @b", map[string]interface{}{"a": 3, "b": 10})
	if iv, _ := v3.(int64); iv != -7 {
		t.Errorf("3 - 10: want -7, got %v", v3)
	}
}

// ─── Guard evaluation never panics on hostile input ───────────────────────────
//
// Whatever the inputs, a guard must return (bool, error), never panic. These
// throw deliberately awkward expressions and value types at the evaluator and
// assert only that it does not crash.

func TestAdversarial_NoPanicOnHostileInput(t *testing.T) {
	exprs := []string{
		"@a = @b = @c",   // chained equality
		"((((@a = 1))))", // deep nesting
		"@a = 1 AND @a = 1 AND @a = 1 AND @a = 1 AND @a = 1", // repetition
		"@a + @b - @c * @d / @e = 0",                         // arithmetic in a comparison
		"@a IS NOT NULL AND @b IS NULL OR @c = 1",            // mixed null/bool
		"NOT NOT @a = 1",                                     // double negation
	}
	vars := map[string]interface{}{"a": 1, "b": 2, "c": 0, "d": 1, "e": 1}
	for _, expr := range exprs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("guard %q panicked: %v", expr, r)
				}
			}()
			e := New()
			_, _ = EvalGuard(e, expr, vars, nil) // result unimportant; must not panic
		}()
	}
}

// ─── Guard error propagation ──────────────────────────────────────────────────

func TestAdversarial_MalformedGuardErrors(t *testing.T) {
	malformed := []string{
		"@a = = 1",  // double operator
		"@a AND",    // dangling operator
		"= 1",       // leading operator
		"((@a = 1)", // unbalanced parens
	}
	for _, expr := range malformed {
		e := New()
		_, err := EvalGuard(e, expr, map[string]interface{}{"a": 1}, nil)
		if err == nil {
			t.Errorf("malformed guard %q: expected an error, got nil", expr)
		}
	}
}
