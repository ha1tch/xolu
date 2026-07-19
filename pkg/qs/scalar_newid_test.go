// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package qs

import (
	"testing"

	"github.com/google/uuid"
)

// D-001 (OQL surface): NEWID() is exposed in OQL via ScalarNewID, bound to the
// real v4 generator (uuid.NewRandom). It must return a parseable, unique,
// version-4 UUID.

func TestScalarNewID_ProducesValidUUIDv4(t *testing.T) {
	v := ScalarNewID(nil)
	s, ok := v.(string)
	if !ok {
		t.Fatalf("ScalarNewID result type: want string, got %T (%v)", v, v)
	}
	id, err := uuid.Parse(s)
	if err != nil {
		t.Fatalf("ScalarNewID() = %q is not a parseable UUID: %v", s, err)
	}
	if id.Version() != 4 {
		t.Errorf("ScalarNewID() = %q: want version 4, got %d", s, id.Version())
	}
}

func TestScalarNewID_IsUniqueAcrossCalls(t *testing.T) {
	const n = 1000
	seen := make(map[string]struct{}, n)
	for i := 0; i < n; i++ {
		s := ScalarNewID(nil).(string)
		if _, dup := seen[s]; dup {
			t.Fatalf("ScalarNewID produced a duplicate value %q within %d calls", s, n)
		}
		seen[s] = struct{}{}
	}
}
