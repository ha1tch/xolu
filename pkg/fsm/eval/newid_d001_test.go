// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package eval_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ha1tch/xolu/pkg/fsm/eval"
)

// D-001: NEWID() previously synthesised a UUID-shaped string from a single
// nanosecond timestamp — its final segment was a constant (an undefined 64-bit
// shift), it set no version/variant bits, and it collided under rapid
// generation. It is now bound to the same real generator as UUID_V4()
// (uuid.NewRandom), so it returns a proper, unique, random v4 UUID.

// NEWID() must produce a parseable version-4 UUID with no constant tail.
func TestNewID_ProducesValidUUIDv4(t *testing.T) {
	e := eval.New()
	val, err := eval.EvalSet(e, "NEWID()", nil)
	if err != nil {
		t.Fatalf("EvalSet(NEWID()): %v", err)
	}
	s, ok := val.(string)
	if !ok {
		t.Fatalf("NEWID() result type: want string, got %T (%v)", val, val)
	}
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("NEWID() = %q is not a parseable UUID: %v", s, err)
	}
	if id.Version() != 4 {
		t.Errorf("NEWID() = %q: want UUID version 4, got version %d", s, id.Version())
	}
	// The old implementation's last 12 hex chars were always zero.
	if s[24:] == "000000000000" {
		t.Errorf("NEWID() = %q has the constant all-zero final segment of the old impl", s)
	}
}

// Successive NEWID() calls must differ — uniqueness was the core property the
// old timestamp-derived implementation failed to guarantee.
func TestNewID_IsUniqueAcrossCalls(t *testing.T) {
	e := eval.New()
	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		val, err := eval.EvalSet(e, "NEWID()", nil)
		if err != nil {
			t.Fatalf("EvalSet(NEWID()) iteration %d: %v", i, err)
		}
		s := val.(string)
		if _, dup := seen[s]; dup {
			t.Fatalf("NEWID() produced a duplicate value %q within %d calls", s, n)
		}
		seen[s] = struct{}{}
	}
}

// NEWID() and UUID_V4() share the same generator; both must yield valid v4
// UUIDs, confirming the rebind rather than a divergent implementation.
func TestNewID_MatchesUUIDv4Shape(t *testing.T) {
	e := eval.New()
	for _, expr := range []string{"NEWID()", "UUID_V4()"} {
		val, err := eval.EvalSet(e, expr, nil)
		if err != nil {
			t.Fatalf("EvalSet(%s): %v", expr, err)
		}
		id, err := uuid.Parse(val.(string))
		if err != nil {
			t.Fatalf("%s = %q not a UUID: %v", expr, val, err)
		}
		if id.Version() != 4 {
			t.Errorf("%s: want version 4, got %d", expr, id.Version())
		}
	}
}
