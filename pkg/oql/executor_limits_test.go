// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import "testing"

// TestSetLimits_ZeroBecomesDefault is the regression test for D-014.
//
// The executor enforces scan/result bounds with `if limit > 0` checks, so a zero
// limit silently disables the bound and permits an unbounded in-memory scan. The
// config documents "0 = use default" for QueryMaxRows/QueryMaxScanRows but never
// enforced it (no Validate() check, no use-time fallback), and the env loader
// accepts XOLU_QUERY_MAX_SCAN_ROWS=0 verbatim. SetLimits now applies the
// documented defaults to any non-positive value so the executor can never hold a
// limit-disabling zero.
func TestSetLimits_ZeroBecomesDefault(t *testing.T) {
	store := newMockStore()
	engine := NewEngine(store, t.TempDir())
	e := engine.executor

	cases := []struct {
		name     string
		in       QueryLimits
		wantRows int
		wantScan int
	}{
		{"both zero", QueryLimits{0, 0}, defaultMaxRows, defaultMaxScanRows},
		{"both negative", QueryLimits{-1, -100}, defaultMaxRows, defaultMaxScanRows},
		{"scan zero only", QueryLimits{500, 0}, 500, defaultMaxScanRows},
		{"rows zero only", QueryLimits{0, 9999}, defaultMaxRows, 9999},
		{"both positive preserved", QueryLimits{123, 456}, 123, 456},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e.SetLimits(tc.in)
			if e.limits.MaxRows != tc.wantRows {
				t.Errorf("MaxRows: got %d, want %d", e.limits.MaxRows, tc.wantRows)
			}
			if e.limits.MaxScanRows != tc.wantScan {
				t.Errorf("MaxScanRows: got %d, want %d", e.limits.MaxScanRows, tc.wantScan)
			}
		})
	}
}
