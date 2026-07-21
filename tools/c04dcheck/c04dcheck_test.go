// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestC04DCheck runs the analyzer over the violations fixture: five
// violations (checks 1, 2, and 3) must be reported at the annotated
// lines, and the legal patterns (int64 carry, 32-bit parse, tenant-id
// 16-bit parse, untyped constants, the explicit uint32() assertion
// idiom) must produce nothing.
func TestC04DCheck(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), Analyzer, "violations")
}
