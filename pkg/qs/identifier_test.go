// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package qs

import "testing"

func TestIsValidIdentifier(t *testing.T) {
	valid := []string{"a", "_x", "title", "author_id", "Name1", "MEMBER_OF"}
	for _, s := range valid {
		if !IsValidIdentifier(s) {
			t.Errorf("IsValidIdentifier(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "1abc", "a b", "a)", "x;y", "a--b", "drop table", `a"b`, "a'b", "a*b", "a.b", "a,b", "a)UNION", "café_name", "ünïcode", "usеr"}
	for _, s := range invalid {
		if IsValidIdentifier(s) {
			t.Errorf("IsValidIdentifier(%q) = true, want false", s)
		}
	}
}

func TestIsValidFieldPath(t *testing.T) {
	valid := []string{"title", "a.title", "x.y.z", "_a._b"}
	for _, s := range valid {
		if !IsValidFieldPath(s) {
			t.Errorf("IsValidFieldPath(%q) = false, want true", s)
		}
	}
	invalid := []string{"", ".", "a.", ".a", "a..b", "a.b)c", "a.1b", "a) UNION SELECT--"}
	for _, s := range invalid {
		if IsValidFieldPath(s) {
			t.Errorf("IsValidFieldPath(%q) = true, want false", s)
		}
	}
}

func TestIsBareIdentRune(t *testing.T) {
	for _, r := range []rune{'a', 'Z', '0', '_'} {
		if !IsBareIdentRune(r) {
			t.Errorf("IsBareIdentRune(%q) = false, want true", r)
		}
	}
	for _, r := range []rune{' ', ')', ';', '-', '"', '\'', '*', '.', ',', '(', 'é', '中'} {
		if IsBareIdentRune(r) {
			t.Errorf("IsBareIdentRune(%q) = true, want false", r)
		}
	}
}
