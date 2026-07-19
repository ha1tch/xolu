// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package eval

import (
	"testing"
)

// FuzzEvalGuard fuzzes FSM guard-expression evaluation — the reachable path by
// which a registered function (~180 of them, only 5 covered by the D-008
// regression suite) is invoked at transition time. The invariant is that
// evaluating ANY guard expression must not panic: a function may return a value
// or a typed error, but never crash the evaluator.
//
// This is the broad net behind D-008: the committed regression pins the five
// known offenders (SUBSTRING/STUFF panics, REPLICATE/SPACE/STR allocation);
// this fuzzer searches the rest of the registry for the next one. Allocation
// (OOM) is bounded by the maxFunctionOutputBytes guard added in D-008, so seeds
// here exercise structure and indices rather than attack-scale counts.
//
// Run actively with:
//
//	go test ./pkg/fsm/eval -run x -fuzz FuzzEvalGuard -fuzztime 60s
//
// Under a normal `go test` (no -fuzz) it replays the seed corpus only.
func FuzzEvalGuard(f *testing.F) {
	seeds := []string{
		// D-008 known cases
		"SUBSTRING('hello', 1, -5) = ''",
		"STUFF('hello', 1, -9, 'X') = ''",
		"REPLICATE('x', 100) = ''",
		"SPACE(100) = ''",
		"STR(1.0, 100) = ''",
		// generators
		"NEWID() = ''",
		"UUID_V4() = ''",
		// common string/number functions across the registry
		"LEN('abc') = 3",
		"UPPER('abc') = 'ABC'",
		"LEFT('abc', 2) = 'ab'",
		"RIGHT('abc', -1) = ''",
		"CHARINDEX('a', 'banana') = 2",
		"ROUND(1.2345, 2) = 1.23",
		"ABS(-1) = 1",
		"REPLACE('aaa', 'a', 'b') = 'bbb'",
		// structural hostility
		"",
		"(((((((((((",
		"1 = ",
		"FOO(",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, expr string) {
		// A fresh evaluator per call; no shared state to corrupt across inputs.
		e := New()
		// The contract: evaluation must not panic. A parse/type error returned
		// as a normal error is fine; an unrecovered panic fails the fuzz case.
		_, _ = EvalGuard(e, expr, map[string]interface{}{}, nil)
		_, _ = EvalSet(e, expr, map[string]interface{}{})
	})
}
