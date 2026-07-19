// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"fmt"
	"reflect"
	"unicode"

	"github.com/ha1tch/tsqlparser/ast"
)

// ---------------------------------------------------------------------------
// Parse-time identifier-smuggling gate
// ---------------------------------------------------------------------------
//
// Root cause of the alias/ORDER-BY injection class (the OQL JOIN alias, the
// aggregate ORDER BY else-branch, and the Sulpher RETURN alias on the Cypher
// side): the tsqlparser lexer accepts T-SQL *delimited* identifiers — `[..]` and
// `".."` — and stores the raw inner text with the delimiters stripped, tokenised
// as a plain IDENT. By the time SQL generation runs, an *.ast.Identifier whose
// Value is `x) UNION SELECT ...--` is structurally indistinguishable from a
// legitimate bare identifier, and any sink that interpolates that Value as a SQL
// identifier (alias, ORDER BY term, column name) is injectable.
//
// The defence is that a *bare* identifier — one the lexer produced via
// readIdentifier rather than readBracketedIdentifier/readQuotedIdentifier — can
// only contain letters, digits, '_', '#', and '$'. Any identifier Value
// containing a character outside that set therefore *must* have entered through a
// delimited form. In xolu that never happens legitimately: entity and field
// names are constrained to ^[a-zA-Z][a-zA-Z0-9_]*$ at the server, OQL, and
// storage layers, so a delimited identifier can never resolve to a real column
// or table. Rejecting it at parse time closes the entire class — including any
// generation sink not individually hardened — at no cost to legitimate queries.
//
// This is a defence-in-depth gate. The per-sink validators (validateFieldName on
// aliases and ORDER BY terms, isSimpleIdent on Sulpher aliases) remain in place.

// isBareIdentRune reports whether r is legal in a bare (non-delimited) SQL
// identifier as produced by the tsqlparser lexer's readIdentifier:
// unicode letters, digits, and the three extra symbols '_', '#', '$'.
func isBareIdentRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '#' || r == '$'
}

// identifierIsSmuggled reports whether an identifier Value contains any character
// impossible in a bare identifier — the signature of a delimiter-smuggled
// identifier ([..] or ".."). An empty value is not flagged here (empty/forms are
// handled by the per-sink validators); the qualified-identifier separator '.'
// is handled by walking each part, so a lone '.' never reaches this check.
func identifierIsSmuggled(value string) bool {
	// The star wildcard (SELECT *, COUNT(*)) is parsed as an Identifier whose
	// Value is "*". It is the one legitimate non-bare-alphabet identifier value
	// and is never interpolated as a smuggling vector (it is matched structurally,
	// e.g. by isCountStar). Exempt it explicitly.
	if value == "*" {
		return false
	}
	for _, r := range value {
		if !isBareIdentRune(r) {
			return true
		}
	}
	return false
}

// checkASTForSmuggledIdentifiers walks an AST node recursively and returns an
// error if any *ast.Identifier carries a delimiter-smuggled value. The walk is
// reflection-based so it covers every identifier-bearing position — aliases,
// column references, table names, function arguments, ORDER BY / GROUP BY terms —
// without enumerating each container struct, and stays correct if the AST grows
// new node types.
func checkASTForSmuggledIdentifiers(root ast.Node) error {
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
				// Guard against cycles / repeated shared nodes.
				ptr := v.Pointer()
				if visited[ptr] {
					return
				}
				visited[ptr] = true
				// Identify *ast.Identifier directly so we check its Value.
				if id, ok := v.Interface().(*ast.Identifier); ok {
					if identifierIsSmuggled(id.Value) {
						firstErr = fmt.Errorf("rejected query: identifier %q contains characters only possible via a delimited identifier ([..] or \"..\"); such identifiers are not permitted (possible SQL injection)", id.Value)
						return
					}
				}
			}
			walk(v.Elem())

		case reflect.Struct:
			for i := 0; i < v.NumField(); i++ {
				f := v.Field(i)
				// Only traverse exported fields (unexported can't be Interface()'d).
				if !v.Type().Field(i).IsExported() {
					continue
				}
				walk(f)
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
