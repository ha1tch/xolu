// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package eval

import (
	"strings"
	"testing"
)

// TestConvert_BoundsDeclaredLength is the regression test for D-017, the D-008
// allocation class on the CAST/CONVERT fixed-width path.
//
// A declared length such as CAST(x AS CHAR(n)) flows from ParseDataType (which
// applies no upper bound) through Convert -> convertToChar -> NewChar ->
// strings.Repeat, so a guard expression like CAST('x' AS CHAR(2000000000))
// could request a multi-gigabyte allocation. Convert now rejects a declared
// length above maxFunctionOutputBytes, matching the REPLICATE/SPACE guard.
func TestConvert_BoundsDeclaredLength(t *testing.T) {
	// Oversized declared length must be rejected, not allocated.
	if _, err := Cast(NewVarChar("x", -1), TypeChar, 0, 0, 2_000_000_000); err == nil {
		t.Fatalf("Cast to CHAR(2000000000) was accepted; expected length-limit rejection")
	}
	// Same for binary and nchar paths.
	if _, err := Cast(NewVarChar("x", -1), TypeBinary, 0, 0, 1_000_000_000); err == nil {
		t.Errorf("Cast to BINARY(1000000000) was accepted; expected rejection")
	}
	if _, err := Cast(NewVarChar("x", -1), TypeNChar, 0, 0, 1_000_000_000); err == nil {
		t.Errorf("Cast to NCHAR(1000000000) was accepted; expected rejection")
	}

	// Legitimate small fixed-width casts must still work and pad correctly.
	v, err := Cast(NewVarChar("ab", -1), TypeChar, 0, 0, 5)
	if err != nil {
		t.Fatalf("legitimate CHAR(5) cast failed: %v", err)
	}
	if got := v.AsString(); got != "ab"+strings.Repeat(" ", 3) {
		t.Errorf("CHAR(5) padding wrong: got %q", got)
	}

	// MAX (maxLen == -1) must remain allowed (no fixed-width padding).
	if _, err := Cast(NewVarChar("x", -1), TypeVarChar, 0, 0, -1); err != nil {
		t.Errorf("VARCHAR(MAX) cast rejected: %v", err)
	}
}
