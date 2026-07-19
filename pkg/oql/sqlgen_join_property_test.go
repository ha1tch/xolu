// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"strings"
	"testing"

	"github.com/ha1tch/tsqlparser"
	"github.com/ha1tch/tsqlparser/ast"

	"github.com/ha1tch/xolu/pkg/internal/advcorpus"
)

// Property (D-005 class): for ANY field identifier in an OQL JOIN — in the
// SELECT list, the WHERE clause, or the ON condition — GenerateJoinSQL either
// errors, or emits SQL in which no SQL metacharacter from that identifier
// survives into the json_extract path. This generalises the single-payload
// regression in sqlgen_injection_test.go across the whole join surface and all
// three syntactic positions the audit identified as routing through
// joinFieldRef.
//
// Each payload has three safe terminal states: the parser rejects it, the
// generator rejects it, or the generator emits metacharacter-free SQL. The only
// failure is generation success with a live metacharacter in the output.
func TestProperty_JoinSQL_NoMetacharacterEscape(t *testing.T) {
	d := &SQLiteDialect{}
	store := newMockJoinStore("post", false, "author", false)

	// Each template places {P} (a bracketed delimited identifier carrying the
	// payload) into a distinct syntactic position of a JOIN query.
	templates := map[string]string{
		"select": "SELECT a.[{P}], b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id",
		"where":  "SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.author_id = b.id WHERE a.[{P}] = 1",
		"on":     "SELECT a.title, b.name FROM post AS a INNER JOIN author AS b ON a.[{P}] = b.id",
	}

	for _, payload := range advcorpus.AllIdentifierPayloads() {
		payload := payload
		for pos, tmpl := range templates {
			pos, tmpl := pos, tmpl
			t.Run(pos+"/"+sanitiseSubtestName(payload), func(t *testing.T) {
				oqlStr := strings.Replace(tmpl, "{P}", payload, 1)

				prog, errs := tsqlparser.Parse(oqlStr)
				if len(errs) > 0 || len(prog.Statements) == 0 {
					// Parser rejection is a safe terminal state.
					return
				}
				sel, ok := prog.Statements[0].(*ast.SelectStatement)
				if !ok {
					return
				}
				js, jerr := extractJoinSpec(sel)
				if jerr != nil || js == nil {
					// No join spec extracted — also safe.
					return
				}
				plan := buildJoinPlan(js, false, false)

				result, err := GenerateJoinSQL(sel, plan, "", store, d)
				if err != nil {
					// Generation rejection is the intended safe outcome.
					return
				}

				// Accepted and generated: no metacharacter from the payload may
				// survive into the SQL.
				for _, meta := range advcorpus.SQLMetacharacters {
					if strings.Contains(payload, meta) && strings.Contains(result.SQL, meta) {
						t.Errorf("SQL injection on JOIN %s path via field %q: metacharacter %q reached SQL:\n%s",
							pos, payload, meta, result.SQL)
					}
				}
			})
		}
	}
}

// sanitiseSubtestName makes a corpus payload safe and readable as a subtest
// name (go test splits on '/', control bytes render badly).
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
