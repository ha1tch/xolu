// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package eval

// eval_seq_test.go — S8: NEXT VALUE FOR substitution in FSM set clauses.
//
// These exercise the AST-substitution path directly (bare node, arithmetic
// nesting, detection, multiple distinct sequences, and the missing-incrementor
// error) rather than only through the FSM walk integration test, so a
// regression in the substitution logic is caught at the unit level.

import "testing"

func TestEvalSeq_BareNextValueFor(t *testing.T) {
	e := New()
	e.SetSeqIncrementor(func(name string) (int64, error) {
		if name != "order_seq" {
			t.Fatalf("unexpected sequence name: %s", name)
		}
		return 41, nil
	})
	v, err := EvalSetWithSeq(e, "NEXT VALUE FOR order_seq", nil)
	if err != nil {
		t.Fatalf("bare NEXT VALUE FOR: %v", err)
	}
	if iv, _ := v.(int64); iv != 41 {
		t.Errorf("bare: want 41, got %v (%T)", v, v)
	}
}

func TestEvalSeq_NestedInArithmetic(t *testing.T) {
	e := New()
	e.SetSeqIncrementor(func(string) (int64, error) { return 41, nil })
	v, err := EvalSetWithSeq(e, "NEXT VALUE FOR s + 1", nil)
	if err != nil {
		t.Fatalf("nested NEXT VALUE FOR: %v", err)
	}
	if iv, _ := v.(int64); iv != 42 {
		t.Errorf("nested: want 42, got %v (%T)", v, v)
	}
}

func TestEvalSeq_IncrementorCalledOncePerReference(t *testing.T) {
	e := New()
	calls := 0
	e.SetSeqIncrementor(func(string) (int64, error) { calls++; return 10, nil })
	if _, err := EvalSetWithSeq(e, "NEXT VALUE FOR s + NEXT VALUE FOR s", nil); err != nil {
		t.Fatalf("two references: %v", err)
	}
	if calls != 2 {
		t.Errorf("incrementor calls for two references: want 2, got %d", calls)
	}
}

func TestEvalSeq_MultipleDistinctSequences(t *testing.T) {
	e := New()
	seen := map[string]int{}
	e.SetSeqIncrementor(func(name string) (int64, error) {
		seen[name]++
		if name == "a" {
			return 100, nil
		}
		return 200, nil
	})
	v, err := EvalSetWithSeq(e, "NEXT VALUE FOR a + NEXT VALUE FOR b", nil)
	if err != nil {
		t.Fatalf("two sequences: %v", err)
	}
	if iv, _ := v.(int64); iv != 300 {
		t.Errorf("a+b: want 300, got %v", v)
	}
	if seen["a"] != 1 || seen["b"] != 1 {
		t.Errorf("each sequence incremented once: got %v", seen)
	}
}

func TestEvalSeq_NoIncrementorIsError(t *testing.T) {
	e := New() // no incrementor installed
	if _, err := EvalSetWithSeq(e, "NEXT VALUE FOR s", nil); err == nil {
		t.Error("NEXT VALUE FOR with no incrementor: want error, got nil")
	}
}

func TestEvalSeq_IncrementorErrorPropagates(t *testing.T) {
	e := New()
	e.SetSeqIncrementor(func(string) (int64, error) {
		return 0, errSeqExhausted
	})
	if _, err := EvalSetWithSeq(e, "NEXT VALUE FOR s", nil); err == nil {
		t.Error("incrementor error: want propagated error, got nil")
	}
}

func TestEvalSeq_PlainSetClauseUnaffected(t *testing.T) {
	e := New()
	// A set clause with no NEXT VALUE FOR must evaluate normally even when an
	// incrementor is installed.
	e.SetSeqIncrementor(func(string) (int64, error) {
		t.Fatal("incrementor must not be called for a plain set clause")
		return 0, nil
	})
	v, err := EvalSetWithSeq(e, "@retries + 1", map[string]interface{}{"retries": 4})
	if err != nil {
		t.Fatalf("plain set: %v", err)
	}
	if iv, _ := v.(int64); iv != 5 {
		t.Errorf("plain set @retries+1 at 4: want 5, got %v (%T)", v, v)
	}
}

func TestEvalSeq_ContainsNextValueForDetection(t *testing.T) {
	has, err := ContainsNextValueFor("NEXT VALUE FOR s + 1")
	if err != nil || !has {
		t.Errorf("detect present: want true/nil, got %v/%v", has, err)
	}
	has, err = ContainsNextValueFor("@retries + 1")
	if err != nil || has {
		t.Errorf("detect absent: want false/nil, got %v/%v", has, err)
	}
}

// errSeqExhausted is a sentinel for the incrementor-error test.
var errSeqExhausted = &seqTestError{"sequence exhausted"}

type seqTestError struct{ msg string }

func (e *seqTestError) Error() string { return e.msg }
