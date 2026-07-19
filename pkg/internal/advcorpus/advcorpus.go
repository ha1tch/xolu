// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package advcorpus is a shared adversarial-input corpus for xolu's property
// tests and fuzz seed corpora. It is an internal package: importable by any
// xolu package's tests, not part of the public API.
//
// The corpus centralises hostile identifier strings so that every boundary that
// trusts an identifier — OQL field names (D-005), adapted-table schema field
// names (D-009), and any future identifier sink — is exercised against the same
// vocabulary. A single definition means a payload added here strengthens every
// consumer at once.
//
// The corpus is organised by intent. SQLMetacharacters and Identifiers are the
// load-bearing sets for the injection property tests; the remainder broaden
// fuzz coverage.
package advcorpus

// SQLMetacharacters are the substrings that must never survive from an
// untrusted identifier into emitted SQL/DDL. A property test asserts that for
// any corpus identifier, either the boundary rejects it, or none of these
// appears in the generated SQL attributable to that identifier.
var SQLMetacharacters = []string{
	"'",  // string-literal breakout
	"\"", // quoted-identifier breakout
	"`",  // backtick (MySQL-style) identifier
	")",  // close a CREATE/json_extract paren
	"(",  // open a call/subquery
	";",  // statement separator (chained DDL/DML)
	"--", // line comment (truncate the rest of the statement)
	"/*", // block comment open
	"*/", // block comment close
}

// InjectionIdentifiers are crafted field/column-name payloads that attempt to
// break out of a SQL/DDL context. Each is something a caller could supply as an
// OQL JOIN field name (via a T-SQL delimited identifier) or a JSON-schema
// property key. A correct boundary rejects every one of these.
var InjectionIdentifiers = []string{
	// OQL JOIN / json_extract path breakouts (D-005)
	`x') UNION SELECT data,_version FROM t0000_nodes--`,
	`x') UNION SELECT 1--`,
	`x' OR '1'='1`,
	`x') OR (1=1`,
	`a'||(SELECT secret FROM t0000_nodes)||'`,

	// Adapted-table DDL breakouts (D-009)
	`evil TEXT); DROP TABLE t0000_nodes;--`,
	`c TEXT); ALTER TABLE t0000_nodes ADD COLUMN pwned TEXT;--`,
	`c TEXT); ATTACH DATABASE 'x.db' AS x;--`,
	`c TEXT); UPDATE t0000_nodes SET data='';--`,
	`name TEXT, evil TEXT`, // sneak a second column past the comma

	// Comment / terminator games
	`field--comment`,
	`field/*inline*/name`,
	`field;DROP`,
	`field)`,
	`(field`,

	// Quote balancing tricks
	`it''s`,
	`a"b`,
	"back`tick",
}

// PathTraversalDigests are blob-SHA payloads (D-004): values that are not a
// well-formed 64-char lowercase-hex digest and must be rejected before reaching
// the filesystem, including ones carrying path components.
var PathTraversalDigests = []string{
	"",                     // empty — panics in hexSHA[:2]
	"a",                    // too short — panics
	"ab",                   // exactly the slice width, still wrong length
	"xyz",                  // non-hex, short
	"../../../etc/passwd",  // path traversal
	"..",                   // parent dir
	"./.",                  // current/relative
	"foo/bar",              // separator in the middle
	"AABBCCDD" + zeros(56), // uppercase hex (digests are lowercase)
	"zz" + zeros(62),       // right length (64), non-hex lead
	zeros(63),              // hex but one short
	zeros(65),              // hex but one long
	zeros(64) + "/x",       // valid prefix then a separator
}

// Identifiers is a general set of edge-case identifier shapes (not necessarily
// malicious) that any identifier validator should handle deterministically:
// boundary conditions around length, casing, leading characters, Unicode, and
// embedded control bytes.
var Identifiers = []string{
	"",              // empty
	" ",             // single space
	"\t",            // tab
	"\n",            // newline
	"a b",           // internal space
	"_leading",      // leading underscore
	"1leading",      // leading digit
	".dotstart",     // leading dot
	"trailing.",     // trailing dot
	"a..b",          // double dot
	"a.b.c",         // multi-segment (valid for OQL field paths)
	"ALLCAPS",       // uppercase
	"MiXeD",         // mixed case
	"name\x00null",  // embedded NUL
	"name\x7f",      // DEL control
	"café",          // non-ASCII letter
	"ＮＡＭＥ",          // full-width Unicode lookalikes
	"a\u200bb",      // zero-width space
	"select",        // reserved word (lowercase)
	"SELECT",        // reserved word (uppercase)
	"id",            // collides with the system id column
	"_version",      // collides with a system column
	"_extra",        // collides with the overflow column
	"created_at",    // collides with a system timestamp column
	longName(64),    // at a plausible identifier length
	longName(1024),  // overlong
	longName(65536), // pathologically long
}

// ValidIdentifiers are well-formed names that every identifier validator MUST
// accept. Property tests use these as the negative control: the boundary must
// not over-reject legitimate input.
var ValidIdentifiers = []string{
	"name",
	"unit_cost",
	"sku_code",
	"field1",
	"a",
	"customer_id",
	"line_total",
}

// AllIdentifierPayloads concatenates the identifier-shaped corpora (everything
// except blob digests) for callers that want a single slice to iterate.
func AllIdentifierPayloads() []string {
	out := make([]string, 0, len(InjectionIdentifiers)+len(Identifiers)+len(ValidIdentifiers))
	out = append(out, InjectionIdentifiers...)
	out = append(out, Identifiers...)
	out = append(out, ValidIdentifiers...)
	return out
}

func zeros(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = '0'
	}
	return string(b)
}

func longName(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
