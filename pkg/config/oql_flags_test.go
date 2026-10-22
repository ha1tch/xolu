// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package config

import (
	"strings"
	"testing"
)

// XOT241 and XOT246: the OQL flags default to today's behaviour and read their
// environment variables.
func TestOQLFlagsDefaults(t *testing.T) {
	cfg := Default()
	if cfg.OQLUnknownEntity != "error" {
		t.Errorf("OQLUnknownEntity = %q, want %q (QL004 stays the default)", cfg.OQLUnknownEntity, "error")
	}
	if cfg.OQLBlobThreshold != 0 || cfg.OQLNonCoveringThreshold != 0 ||
		cfg.OQLTempBTree1Threshold != 0 || cfg.OQLTempBTree2Threshold != 0 {
		t.Errorf("thresholds must default to 0 (keep the profile's value), got %d %d %d %d",
			cfg.OQLBlobThreshold, cfg.OQLNonCoveringThreshold, cfg.OQLTempBTree1Threshold, cfg.OQLTempBTree2Threshold)
	}
}

func TestOQLFlagsFromEnv(t *testing.T) {
	t.Setenv("XOLU_OQL_UNKNOWN_ENTITY", " EMPTY ")
	t.Setenv("XOLU_OQL_BLOB_THRESHOLD", "75")
	t.Setenv("XOLU_OQL_NONCOVERING_THRESHOLD", "200")
	t.Setenv("XOLU_OQL_TEMPBTREE1_THRESHOLD", "300")
	t.Setenv("XOLU_OQL_TEMPBTREE2_THRESHOLD", "400")
	cfg := Default()
	LoadFromEnv(cfg)
	if cfg.OQLUnknownEntity != "empty" {
		t.Errorf("OQLUnknownEntity = %q, want %q (trimmed and lower-cased)", cfg.OQLUnknownEntity, "empty")
	}
	if cfg.OQLBlobThreshold != 75 || cfg.OQLNonCoveringThreshold != 200 ||
		cfg.OQLTempBTree1Threshold != 300 || cfg.OQLTempBTree2Threshold != 400 {
		t.Errorf("thresholds = %d %d %d %d, want 75 200 300 400",
			cfg.OQLBlobThreshold, cfg.OQLNonCoveringThreshold, cfg.OQLTempBTree1Threshold, cfg.OQLTempBTree2Threshold)
	}
}

func TestOQLFlagsValidation(t *testing.T) {
	cfg := Default()
	errs, _ := cfg.Validate()
	if strings.Contains(strings.Join(errs, "\n"), "OQL") {
		t.Errorf("the defaults must not produce an OQL validation error: %v", errs)
	}

	cfg = Default()
	cfg.OQLUnknownEntity = "sometimes"
	cfg.OQLBlobThreshold = -1
	errs, _ = cfg.Validate()
	joined := strings.Join(errs, "\n")
	for _, want := range []string{"OQLUnknownEntity", "OQLBlobThreshold"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected a validation error naming %s, got: %v", want, errs)
		}
	}
}
