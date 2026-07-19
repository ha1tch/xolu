// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package eval_test

import (
	"strings"
	"testing"

	"github.com/ha1tch/xolu/pkg/fsm/eval"
)

// ─── ParseGuard — syntax validation ──────────────────────────────────────────

func TestParseGuard_ValidExpressions(t *testing.T) {
	valid := []string{
		"@retries < 3",
		"payload.result = 'pass'",
		"payload.result = 'pass' AND payload.technician != ''",
		"@retries < 3 AND payload.status = 'ok'",
		"@count >= 0",
		"1 = 1",
	}
	for _, expr := range valid {
		_, err := eval.ParseGuard(expr)
		if err != nil {
			t.Errorf("ParseGuard(%q): unexpected error: %v", expr, err)
		}
	}
}

func TestParseGuard_InvalidExpressions(t *testing.T) {
	invalid := []string{
		"@retries <", // incomplete
		"= 'pass'",   // missing left operand
		"AND OR",     // no operands
	}
	for _, expr := range invalid {
		_, err := eval.ParseGuard(expr)
		if err == nil {
			t.Errorf("ParseGuard(%q): expected error, got nil", expr)
		}
	}
}

// ─── EvalGuard — AssetLifecycle spec examples ─────────────────────────────────

func TestEvalGuard_RetriesLessThan3_Pass(t *testing.T) {
	e := eval.New()
	ok, err := eval.EvalGuard(e, "@retries < 3",
		map[string]interface{}{"retries": int64(2)},
		nil)
	if err != nil {
		t.Fatalf("EvalGuard: %v", err)
	}
	if !ok {
		t.Error("want true (2 < 3), got false")
	}
}

func TestEvalGuard_RetriesLessThan3_Fail(t *testing.T) {
	e := eval.New()
	ok, err := eval.EvalGuard(e, "@retries < 3",
		map[string]interface{}{"retries": int64(3)},
		nil)
	if err != nil {
		t.Fatalf("EvalGuard: %v", err)
	}
	if ok {
		t.Error("want false (3 is not < 3), got true")
	}
}

func TestEvalGuard_PayloadResultPass(t *testing.T) {
	e := eval.New()
	ok, err := eval.EvalGuard(e, "payload.result = 'pass'",
		nil,
		map[string]interface{}{"result": "pass"})
	if err != nil {
		t.Fatalf("EvalGuard: %v", err)
	}
	if !ok {
		t.Error("want true, got false")
	}
}

func TestEvalGuard_PayloadResultFail(t *testing.T) {
	e := eval.New()
	ok, err := eval.EvalGuard(e, "payload.result = 'pass'",
		nil,
		map[string]interface{}{"result": "fail"})
	if err != nil {
		t.Fatalf("EvalGuard: %v", err)
	}
	if ok {
		t.Error("want false, got true")
	}
}

func TestEvalGuard_CompoundGuard_Pass(t *testing.T) {
	// Full AssetLifecycle spec guard:
	// payload.result = 'pass' AND payload.technician != ''
	e := eval.New()
	ok, err := eval.EvalGuard(e,
		"payload.result = 'pass' AND payload.technician != ''",
		nil,
		map[string]interface{}{"result": "pass", "technician": "alice"})
	if err != nil {
		t.Fatalf("EvalGuard: %v", err)
	}
	if !ok {
		t.Error("want true, got false")
	}
}

func TestEvalGuard_CompoundGuard_EmptyTechnician(t *testing.T) {
	e := eval.New()
	ok, err := eval.EvalGuard(e,
		"payload.result = 'pass' AND payload.technician != ''",
		nil,
		map[string]interface{}{"result": "pass", "technician": ""})
	if err != nil {
		t.Fatalf("EvalGuard: %v", err)
	}
	if ok {
		t.Error("want false (empty technician), got true")
	}
}

func TestEvalGuard_MixedVarsAndPayload(t *testing.T) {
	// @retries < 3 AND payload.result = 'pass'
	e := eval.New()
	ok, err := eval.EvalGuard(e,
		"@retries < 3 AND payload.result = 'pass'",
		map[string]interface{}{"retries": int64(1)},
		map[string]interface{}{"result": "pass"})
	if err != nil {
		t.Fatalf("EvalGuard: %v", err)
	}
	if !ok {
		t.Error("want true, got false")
	}
}

func TestEvalGuard_UndefinedVariable_ReturnsFalse(t *testing.T) {
	// Referencing an unbound variable returns NULL which coerces to false.
	e := eval.New()
	ok, err := eval.EvalGuard(e, "@undefined < 3", nil, nil)
	if err != nil {
		t.Fatalf("EvalGuard: %v", err)
	}
	if ok {
		t.Error("undefined variable: want false, got true")
	}
}

func TestEvalGuard_MissingPayloadField_ReturnsFalse(t *testing.T) {
	e := eval.New()
	ok, err := eval.EvalGuard(e, "payload.missing = 'x'",
		nil,
		map[string]interface{}{"other": "value"})
	if err != nil {
		t.Fatalf("EvalGuard: %v", err)
	}
	if ok {
		t.Error("missing payload field: want false, got true")
	}
}

// ─── EvalSet — arithmetic set clauses ────────────────────────────────────────

func TestEvalSet_Increment(t *testing.T) {
	// @retries + 1
	e := eval.New()
	val, err := eval.EvalSet(e, "@retries + 1",
		map[string]interface{}{"retries": int64(2)})
	if err != nil {
		t.Fatalf("EvalSet: %v", err)
	}
	n, ok := val.(int64)
	if !ok {
		t.Fatalf("result type: want int64, got %T (%v)", val, val)
	}
	if n != 3 {
		t.Errorf("@retries + 1: want 3, got %d", n)
	}
}

func TestEvalSet_Reset(t *testing.T) {
	// SET @retries = 0
	e := eval.New()
	val, err := eval.EvalSet(e, "0",
		map[string]interface{}{"retries": int64(5)})
	if err != nil {
		t.Fatalf("EvalSet: %v", err)
	}
	n, ok := val.(int64)
	if !ok {
		t.Fatalf("result type: want int64, got %T", val)
	}
	if n != 0 {
		t.Errorf("0: want 0, got %d", n)
	}
}

func TestEvalSet_StringExpression(t *testing.T) {
	// SET @status = 'active'
	e := eval.New()
	val, err := eval.EvalSet(e, "'active'", nil)
	if err != nil {
		t.Fatalf("EvalSet: %v", err)
	}
	s, ok := val.(string)
	if !ok {
		t.Fatalf("result type: want string, got %T", val)
	}
	if s != "active" {
		t.Errorf("'active': want 'active', got %q", s)
	}
}

func TestEvalSet_UpperFunction(t *testing.T) {
	e := eval.New()
	val, err := eval.EvalSet(e, "UPPER(@status)",
		map[string]interface{}{"status": "pending"})
	if err != nil {
		t.Fatalf("EvalSet: %v", err)
	}
	s, ok := val.(string)
	if !ok {
		t.Fatalf("result type: want string, got %T", val)
	}
	if s != "PENDING" {
		t.Errorf("UPPER(@status): want 'PENDING', got %q", s)
	}
}

// ─── Generator functions in set clauses ──────────────────────────────────────

func TestEvalSet_UUID_V4(t *testing.T) {
	e := eval.New()
	val, err := eval.EvalSet(e, "UUID_V4()", nil)
	if err != nil {
		t.Fatalf("EvalSet UUID_V4(): %v", err)
	}
	s, ok := val.(string)
	if !ok {
		t.Fatalf("UUID_V4() result type: want string, got %T", val)
	}
	if len(s) != 36 {
		t.Errorf("UUID_V4(): want 36-char UUID, got %q", s)
	}
}

func TestEvalSet_UUID_V7(t *testing.T) {
	e := eval.New()
	val, err := eval.EvalSet(e, "UUID_V7()", nil)
	if err != nil {
		t.Fatalf("EvalSet UUID_V7(): %v", err)
	}
	s, _ := val.(string)
	if len(s) != 36 {
		t.Errorf("UUID_V7(): want 36-char UUID, got %q", s)
	}
}

func TestEvalSet_CUID(t *testing.T) {
	e := eval.New()
	val, err := eval.EvalSet(e, "CUID()", nil)
	if err != nil {
		t.Fatalf("EvalSet CUID(): %v", err)
	}
	s, _ := val.(string)
	if !strings.HasPrefix(s, "c") {
		t.Errorf("CUID(): want prefix 'c', got %q", s)
	}
}

func TestEvalSet_ULID(t *testing.T) {
	e := eval.New()
	val, err := eval.EvalSet(e, "ULID()", nil)
	if err != nil {
		t.Fatalf("EvalSet ULID(): %v", err)
	}
	s, _ := val.(string)
	if len(s) != 26 {
		t.Errorf("ULID(): want 26 chars, got %q", s)
	}
}

// ─── Payload binding convention ───────────────────────────────────────────────

func TestPayloadBinding_FlattenedPrefix(t *testing.T) {
	// payload.field is accessible; nested objects are not.
	e := eval.New()
	ok, err := eval.EvalGuard(e, "payload.score > 50",
		nil,
		map[string]interface{}{
			"score":  75.0,
			"nested": map[string]interface{}{"x": 1}, // should be skipped
		})
	if err != nil {
		t.Fatalf("EvalGuard: %v", err)
	}
	if !ok {
		t.Error("payload.score > 50: want true, got false")
	}
}

// ─── RegisterFunc — custom function registration ──────────────────────────────

func TestRegisterFunc_Custom(t *testing.T) {
	e := eval.New()
	e.RegisterFunc("DOUBLE", func(args []eval.Value) (eval.Value, error) {
		if len(args) == 0 {
			return eval.Null(0), nil
		}
		return eval.NewBigInt(args[0].AsInt() * 2), nil
	})
	val, err := eval.EvalSet(e, "DOUBLE(@x)",
		map[string]interface{}{"x": int64(21)})
	if err != nil {
		t.Fatalf("EvalSet DOUBLE(): %v", err)
	}
	if val.(int64) != 42 {
		t.Errorf("DOUBLE(@x): want 42, got %v", val)
	}
}

// ─── Multiple evaluations with the same Evaluator ────────────────────────────

func TestEvalGuard_MultipleCallsIsolated(t *testing.T) {
	// Each EvalGuard/EvalSet call rebinds vars — confirm no state leak.
	e := eval.New()

	ok1, _ := eval.EvalGuard(e, "@retries < 3",
		map[string]interface{}{"retries": int64(1)}, nil)
	ok2, _ := eval.EvalGuard(e, "@retries < 3",
		map[string]interface{}{"retries": int64(5)}, nil)

	if !ok1 {
		t.Error("first call (retries=1): want true")
	}
	if ok2 {
		t.Error("second call (retries=5): want false")
	}
}
