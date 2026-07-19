// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

import (
	"fmt"
	"reflect"
	"unicode"

	sulpherast "github.com/ha1tch/sulpher/ast"
)

// ---------------------------------------------------------------------------
// Parse-time identifier-smuggling gate (Cypher side)
// ---------------------------------------------------------------------------
//
// This is the Sulpher counterpart to the OQL pkg/oql ast gate. Same root cause:
// the Cypher lexer accepts backtick-escaped identifiers (`...`) that may carry
// any character except a backtick, including the double-quote that delimits the
// generated SQL alias. A backtick identifier therefore lets an attacker smuggle
// SQL metacharacters into an *ast.Identifier whose Value reaches push-down SQL
// generation (the RETURN alias breakout).
//
// Two independent signals mark a smuggled identifier:
//
//  1. The sulpher AST records Backtick=true on identifiers that were
//     backtick-escaped in source. No legitimate xolu field, label, or alias
//     needs backticking — entity and field names are constrained to
//     ^[a-zA-Z][a-zA-Z0-9_]*$ at the server, OQL, and storage layers — so a
//     backtick identifier is inherently illegitimate here.
//  2. Independently, any identifier Value containing a character outside the
//     bare-identifier alphabet (letters, digits, '_') could only have come from
//     a delimited form.
//
// Either signal rejects the query at the parser boundary, before any executor or
// SQL generation runs. The per-sink isSimpleIdent guard on the RETURN alias
// remains as defence in depth.

// isBareCypherIdentRune reports whether r is legal in a bare (non-backtick)
// Cypher identifier as produced by the sulpher lexer's readIdentifier
// (isIdentPart): unicode letters, digits, and '_'.
func isBareCypherIdentRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// cypherIdentifierIsSmuggled reports whether an identifier value contains a
// character impossible in a bare Cypher identifier. The '*' wildcard
// (RETURN *, count(*)) is exempt — it is matched structurally, never
// interpolated as an identifier.
func cypherIdentifierIsSmuggled(value string) bool {
	if value == "*" {
		return false
	}
	for _, r := range value {
		if !isBareCypherIdentRune(r) {
			return true
		}
	}
	return false
}

// checkCypherASTForSmuggledIdentifiers walks a Sulpher AST and rejects any
// *sulpherast.Identifier that was backtick-escaped or whose value escapes the
// bare-identifier alphabet. The reflection walk covers every identifier-bearing
// position (labels, property keys, variables, RETURN aliases, ORDER BY terms)
// without enumerating each container type.
func checkCypherASTForSmuggledIdentifiers(root interface{}) error {
	var firstErr error
	visited := make(map[uintptr]bool)

	var walk func(v reflect.Value)
	walk = func(v reflect.Value) {
		if firstErr != nil || !v.IsValid() {
			return
		}

		switch v.Kind() {
		case reflect.Pointer, reflect.Interface:
			if v.IsNil() {
				return
			}
			if v.Kind() == reflect.Pointer {
				ptr := v.Pointer()
				if visited[ptr] {
					return
				}
				visited[ptr] = true
				if id, ok := v.Interface().(*sulpherast.Identifier); ok {
					if id.Backtick {
						firstErr = fmt.Errorf("rejected query: backtick-escaped identifier %q is not permitted (possible SQL injection); xolu identifiers must be bare names", id.Value)
						return
					}
					if cypherIdentifierIsSmuggled(id.Value) {
						firstErr = fmt.Errorf("rejected query: identifier %q contains characters only possible via a delimited identifier; such identifiers are not permitted (possible SQL injection)", id.Value)
						return
					}
				}
			}
			walk(v.Elem())

		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				if !v.Type().Field(i).IsExported() {
					continue
				}
				walk(v.Field(i))
			}

		case reflect.Slice, reflect.Array:
			for i := 0; i < v.Len(); i++ {
				walk(v.Index(i))
			}

		case reflect.Map:
			for _, k := range v.MapKeys() {
				walk(v.MapIndex(k))
			}
		}
	}

	walk(reflect.ValueOf(root))
	return firstErr
}
