// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package eval

import (
	"strings"
	"testing"
)

func TestForceTop1_InjectsIntoPlainSelect(t *testing.T) {
	out, err := ForceTop1("SELECT status FROM assets WHERE name = 'acme'")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.ToUpper(out), "TOP 1") {
		t.Errorf("expected TOP 1 in output, got: %s", out)
	}
}

func TestForceTop1_OverridesExistingTop(t *testing.T) {
	// An author-supplied TOP 100 must be forced down to TOP 1.
	out, err := ForceTop1("SELECT TOP 100 status FROM assets")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	up := strings.ToUpper(out)
	if !strings.Contains(up, "TOP 1") {
		t.Errorf("expected TOP 1, got: %s", out)
	}
	if strings.Contains(up, "TOP 100") {
		t.Errorf("TOP 100 should have been overridden, got: %s", out)
	}
}

func TestForceTop1_PreservesOrderBy(t *testing.T) {
	// ORDER BY decides WHICH row TOP 1 returns; it must survive.
	out, err := ForceTop1("SELECT status FROM assets ORDER BY updated_at DESC")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	up := strings.ToUpper(out)
	if !strings.Contains(up, "ORDER BY") {
		t.Errorf("ORDER BY should be preserved, got: %s", out)
	}
	if !strings.Contains(up, "TOP 1") {
		t.Errorf("expected TOP 1, got: %s", out)
	}
}

func TestForceTop1_PreservesWhere(t *testing.T) {
	out, err := ForceTop1("SELECT a, b FROM t WHERE x = 5 AND y = 6")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(strings.ToUpper(out), "WHERE") {
		t.Errorf("WHERE should be preserved, got: %s", out)
	}
}

func TestForceTop1_RejectsNonSelect(t *testing.T) {
	for _, q := range []string{
		"UPDATE assets SET status = 'x'",
		"DELETE FROM assets",
		"INSERT INTO assets (name) VALUES ('x')",
	} {
		if _, err := ForceTop1(q); err == nil {
			t.Errorf("non-SELECT %q should be rejected", q)
		}
	}
}

func TestForceTop1_RejectsGarbage(t *testing.T) {
	for _, q := range []string{"NOT A QUERY", "SELECT FROM", ""} {
		if _, err := ForceTop1(q); err == nil {
			t.Errorf("garbage %q should be rejected", q)
		}
	}
}
