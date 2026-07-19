// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"strings"
	"testing"
)

// FuzzValidateFieldName fuzzes the OQL field-name validator — the single gate
// both the single-table and JOIN paths rely on (D-005). The invariant is the
// security property itself: any field name validateFieldName ACCEPTS must be
// free of SQL metacharacters. An accepted name flows unescaped into a
// json_extract path, so an accepted metacharacter is an injection.
//
// Run actively with:
//
//	go test ./pkg/oql -run x -fuzz FuzzValidateFieldName -fuzztime 60s
func FuzzValidateFieldName(f *testing.F) {
	seeds := []string{
		"name",
		"a.b.c",
		"x') UNION SELECT 1--",
		"evil); DROP TABLE t--",
		"a'b",
		"a\"b",
		"a;b",
		"a(b)",
		"a/*b*/c",
		"",
		" ",
		"a b",
		"_x",
		"1x",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	dangerous := []string{"'", "\"", "`", ")", "(", ";", "--", "/*", "*/"}

	f.Fuzz(func(t *testing.T, name string) {
		if validateFieldName(name) != nil {
			return // rejected — safe
		}
		// Accepted: it must contain no SQL metacharacter.
		for _, meta := range dangerous {
			if strings.Contains(name, meta) {
				t.Errorf("validateFieldName accepted %q containing metacharacter %q", name, meta)
			}
		}
	})
}
