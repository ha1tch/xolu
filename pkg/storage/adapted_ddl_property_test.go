// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"strings"
	"testing"

	"github.com/ha1tch/xolu/pkg/internal/advcorpus"
)

// Property (D-009 class): for ANY schema field name, DeriveAdaptedTableSpec
// either rejects it, or every column name, index name, and emitted DDL string
// derived from it is free of SQL metacharacters originating in that name. This
// generalises the single-payload regression in adapted_injection_test.go to the
// whole identifier-trust class, and crucially covers the REF_<field>_entity /
// REF_<field>_id column synthesis and index-name derivation paths that the
// point-fix guards transitively but did not touch directly.
//
// A failure here means a field name reached emitted DDL with a live
// metacharacter — the D-009 vulnerability, on some path.
func TestProperty_AdaptedDDL_NoMetacharacterEscape(t *testing.T) {
	dialect := &SQLiteStorageDialect{}

	for _, field := range advcorpus.AllIdentifierPayloads() {
		field := field
		t.Run(sanitiseSubtestName(field), func(t *testing.T) {
			schema := map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"safe": map[string]interface{}{"type": "string"},
					field:  map[string]interface{}{"type": "string"},
				},
			}

			spec, err := DeriveAdaptedTableSpec("widget", schema, dialect, 0)
			if err != nil {
				// Rejection at derivation is always a safe outcome.
				return
			}

			// Accepted. Now NOTHING derived from the field may carry a
			// metacharacter into an identifier position or emitted DDL.
			assertNoMeta := func(t *testing.T, where, s string) {
				t.Helper()
				for _, meta := range advcorpus.SQLMetacharacters {
					// Only flag a metacharacter that actually came from the
					// field: it must be absent from the field to be a false
					// positive, so we only assert when the field contains it.
					if strings.Contains(field, meta) && strings.Contains(s, meta) {
						t.Errorf("DDL injection via field %q: metacharacter %q reached %s:\n%s",
							field, meta, where, s)
					}
				}
			}

			for _, col := range spec.Columns {
				assertNoMeta(t, "column name", col.Name)
			}
			for _, idx := range spec.Indexes {
				assertNoMeta(t, "index name", idx.Name)
				for _, c := range idx.Columns {
					assertNoMeta(t, "index column", c)
				}
			}
			assertNoMeta(t, "CREATE TABLE", GenerateCreateTableSQL(spec, dialect))
			for _, stmt := range GenerateIndexSQL(spec, dialect) {
				assertNoMeta(t, "CREATE INDEX", stmt)
			}
		})
	}
}

// Property: every well-formed identifier MUST be accepted (no over-rejection).
func TestProperty_AdaptedDDL_ValidIdentifiersAccepted(t *testing.T) {
	dialect := &SQLiteStorageDialect{}
	for _, field := range advcorpus.ValidIdentifiers {
		field := field
		t.Run(field, func(t *testing.T) {
			schema := map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					field: map[string]interface{}{"type": "string"},
				},
			}
			if _, err := DeriveAdaptedTableSpec("widget", schema, dialect, 0); err != nil {
				t.Errorf("valid field name %q was rejected: %v", field, err)
			}
		})
	}
}

// sanitiseSubtestName makes a corpus payload safe and readable as a subtest
// name (go test splits on '/', and control bytes render badly).
func sanitiseSubtestName(s string) string {
	if s == "" {
		return "empty"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '/' || r == ' ' || r < 0x20 || r == 0x7f:
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 48 {
		out = out[:48]
	}
	return out
}
