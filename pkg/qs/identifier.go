// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package qs

import (
	"fmt"
	"strings"
)

// Identifier validation — the single source of truth for what counts as a safe
// SQL identifier across OQL, Sulpher, storage, and the server handlers.
//
// WHY AN ALLOWLIST, NOT A DENYLIST.
// These helpers define the *safe* character set and reject everything else,
// rather than enumerating "dangerous" characters to strip. A denylist
// (strip quotes, semicolons, etc.) fails open: every injection this project has
// found used characters a quote-focused denylist would miss — `)`, `(`, `;`,
// `--`, `,`, spaces, `*`, and bare keywords like UNION/SELECT. The safe set, by
// contrast, is small, closed, and known, so anything outside it can be refused
// without having to anticipate the attacker's character choice.
//
// WHY REJECT, NOT STRIP.
// For an identifier there is no "correct" value to preserve by stripping —
// silently rewriting `user) DROP--` to `userDROP` turns an attack into a
// plausible-but-wrong identifier. Identifiers are rejected loudly. (Free-text
// search terms are the one place stripping is appropriate, because there is no
// identifier to preserve; that lives separately in the FTS sanitiser.)
//
// In xolu, entity and field names are bare identifiers by construction, so this
// allowlist costs nothing against legitimate input while closing the injection
// class for identifiers that reach SQL unparameterised (table names, column
// names, aliases, ORDER BY/GROUP BY terms).

// IsBareIdentRune reports whether r may appear *within* a bare SQL identifier:
// an ASCII letter, an ASCII digit, or underscore. It does not enforce the
// leading-character rule (see IsValidIdentifier).
//
// ASCII-only is deliberate. Permitting Unicode letters would open a
// homoglyph/confusable attack surface (e.g. a Cyrillic "а" U+0430 is visually
// identical to Latin "a" U+0061), letting one tenant register an entity that is
// indistinguishable from another's in logs, tooling, and policy. xolu has no use
// case for non-ASCII identifiers — they would also be hard to type in queries —
// so the safe, friction-free policy is ASCII only. (Free-text search content is
// a separate concern handled by the FTS sanitiser, not this function.)
//
// This is the rune-level predicate used by the parse-time AST gates to detect
// delimiter-smuggled identifiers: any identifier value containing a rune for
// which this returns false could only have entered through a delimited form
// ([..], "..", or `..`) and is therefore rejected before SQL generation.
func IsBareIdentRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') || r == '_'
}

// isBareIdentStart reports whether r may *begin* a bare identifier: an ASCII
// letter or underscore (not a digit).
func isBareIdentStart(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_'
}

// IsValidIdentifier reports whether s is a valid bare SQL identifier: it must be
// non-empty, start with a letter or underscore, and otherwise contain only
// letters, digits, and underscores. This is the canonical rule previously
// duplicated as adaptedFieldNameRe, identifierRe, isSimpleIdent, etc.
func IsValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !isBareIdentStart(r) {
				return false
			}
			continue
		}
		if !IsBareIdentRune(r) {
			return false
		}
	}
	return true
}

// ValidateIdentifier returns a descriptive error if s is not a valid bare SQL
// identifier, or nil if it is. Use this where the caller wants to surface the
// reason (registration, schema derivation, the load trust boundary).
func ValidateIdentifier(s string) error {
	if s == "" {
		return fmt.Errorf("identifier cannot be empty")
	}
	if !IsValidIdentifier(s) {
		return fmt.Errorf("invalid identifier %q: must start with a letter or underscore and contain only letters, digits, and underscores", s)
	}
	return nil
}

// IsValidFieldPath reports whether s is a dot-separated path of valid bare
// identifiers (e.g. "title" or "a.title"). Each segment must independently
// satisfy IsValidIdentifier; empty segments (leading, trailing, or doubled
// dots) are rejected. This is the qualified-field variant used by the OQL field
// resolver, which legitimately needs the dot separator that a plain identifier
// disallows.
func IsValidFieldPath(s string) bool {
	if s == "" {
		return false
	}
	for _, seg := range strings.Split(s, ".") {
		if !IsValidIdentifier(seg) {
			return false
		}
	}
	return true
}

// ValidateFieldPath returns a descriptive error if s is not a valid dotted field
// path, or nil if it is.
func ValidateFieldPath(s string) error {
	if s == "" {
		return fmt.Errorf("field path cannot be empty")
	}
	if !IsValidFieldPath(s) {
		return fmt.Errorf("invalid field path %q: each segment must start with a letter or underscore and contain only letters, digits, and underscores", s)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Strict variant: leading letter required (no leading underscore)
// ---------------------------------------------------------------------------
//
// Entity names and persisted schema field/index names use a stricter rule than
// query field paths: they must start with a *letter*, not an underscore. This
// reserves the leading-underscore namespace for system columns (_extra,
// _version, etc.) so a user-supplied name can never collide with one. The
// character set is otherwise identical to the non-strict variant (ASCII only).

// IsValidStrictIdentifier reports whether s is a non-empty bare identifier that
// starts with an ASCII letter (not underscore) and otherwise contains only
// letters, digits, and underscores.
func IsValidStrictIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
				return false
			}
			continue
		}
		if !IsBareIdentRune(r) {
			return false
		}
	}
	return true
}

// ValidateStrictIdentifier returns a descriptive error if s is not a valid
// leading-letter bare identifier, or nil if it is.
func ValidateStrictIdentifier(s string) error {
	if s == "" {
		return fmt.Errorf("identifier cannot be empty")
	}
	if !IsValidStrictIdentifier(s) {
		return fmt.Errorf("invalid identifier %q: must start with a letter and contain only letters, digits, and underscores", s)
	}
	return nil
}

// IsValidStrictFieldPath reports whether s is a dot-separated path of valid
// leading-letter bare identifiers.
func IsValidStrictFieldPath(s string) bool {
	if s == "" {
		return false
	}
	for _, seg := range strings.Split(s, ".") {
		if !IsValidStrictIdentifier(seg) {
			return false
		}
	}
	return true
}

// ValidateStrictFieldPath returns a descriptive error if s is not a valid dotted
// path of leading-letter identifiers, or nil if it is.
func ValidateStrictFieldPath(s string) error {
	if s == "" {
		return fmt.Errorf("field path cannot be empty")
	}
	if !IsValidStrictFieldPath(s) {
		return fmt.Errorf("invalid field path %q: each segment must start with a letter and contain only letters, digits, and underscores", s)
	}
	return nil
}
