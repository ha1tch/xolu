// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"testing"

	"github.com/google/uuid"
	"github.com/ha1tch/tsqlparser/ast"
)

// D-001 (OQL dispatch): NEWID() must be registered on the OQL scalar surface and
// dispatch through EvalScalarFunction to a real v4 UUID. This confirms the
// "keep NEWID() in OQL" binding, not just the underlying qs.ScalarNewID.

func TestOQL_NEWID_Registered(t *testing.T) {
	if _, ok := ScalarFunctions["NEWID"]; !ok {
		t.Fatal("NEWID is not registered in the OQL ScalarFunctions map")
	}
}

func TestOQL_NEWID_DispatchesToUUIDv4(t *testing.T) {
	fc := &ast.FunctionCall{
		Function:  &ast.Identifier{Value: "NEWID"},
		Arguments: []ast.Expression{},
	}
	result := EvalScalarFunction(fc, func(expr ast.Expression) interface{} { return nil })
	s, ok := result.(string)
	if !ok {
		t.Fatalf("NEWID() via EvalScalarFunction: want string, got %T (%v)", result, result)
	}
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("NEWID() via OQL = %q is not a parseable UUID: %v", s, err)
	}
	if id.Version() != 4 {
		t.Errorf("NEWID() via OQL = %q: want version 4, got %d", s, id.Version())
	}
}
