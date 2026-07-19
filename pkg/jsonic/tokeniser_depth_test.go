// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package jsonic

import (
	"strings"
	"testing"
)

// D-003: the recursive-descent tokeniser (parseValue -> parseObject/parseArray
// -> parseValue) tracked no nesting depth and relied solely on the Go runtime's
// stack-growth limit. Deep enough input produced `fatal error: stack overflow`,
// which is NOT a panic and cannot be caught by recover() — it kills the process.
//
// Expected end state after the fix: a configurable maximum nesting depth; input
// beyond it returns a normal error rather than recursing into a fatal. Input
// within the limit tokenises normally.

// nestedArray builds depth levels of "[" ... "]" wrapping a null.
func nestedArray(depth int) []byte {
	var b strings.Builder
	b.Grow(depth*2 + 4)
	for i := 0; i < depth; i++ {
		b.WriteByte('[')
	}
	b.WriteString("null")
	for i := 0; i < depth; i++ {
		b.WriteByte(']')
	}
	return []byte(b.String())
}

// nestedObject builds depth levels of {"a": ... } wrapping a null.
func nestedObject(depth int) []byte {
	var b strings.Builder
	for i := 0; i < depth; i++ {
		b.WriteString(`{"a":`)
	}
	b.WriteString("null")
	for i := 0; i < depth; i++ {
		b.WriteByte('}')
	}
	return []byte(b.String())
}

// Input nested beyond the limit must return a clean error, never a fatal.
func TestTokenise_ExcessiveArrayDepth_Error(t *testing.T) {
	input := nestedArray(MaxNestingDepth + 50)
	tok := GetTokeniser()
	defer PutTokeniser(tok)
	if err := tok.Tokenise(input); err == nil {
		t.Errorf("array nested to %d levels: expected a depth-limit error, got nil", MaxNestingDepth+50)
	}
}

func TestTokenise_ExcessiveObjectDepth_Error(t *testing.T) {
	input := nestedObject(MaxNestingDepth + 50)
	tok := GetTokeniser()
	defer PutTokeniser(tok)
	if err := tok.Tokenise(input); err == nil {
		t.Errorf("object nested to %d levels: expected a depth-limit error, got nil", MaxNestingDepth+50)
	}
}

// Input within the limit must tokenise without error.
func TestTokenise_DepthWithinLimit_OK(t *testing.T) {
	// Comfortably under the limit so a legitimately deep document still parses.
	input := nestedArray(MaxNestingDepth / 2)
	tok := GetTokeniser()
	defer PutTokeniser(tok)
	if err := tok.Tokenise(input); err != nil {
		t.Errorf("array nested to %d levels (within limit): unexpected error: %v", MaxNestingDepth/2, err)
	}
}

// A pooled tokeniser must not carry depth state between uses: a deep input that
// errors, followed by a shallow input, must succeed.
func TestTokenise_DepthResetBetweenUses(t *testing.T) {
	tok := GetTokeniser()
	defer PutTokeniser(tok)
	_ = tok.Tokenise(nestedArray(MaxNestingDepth + 50)) // errors, leaves depth state
	if err := tok.Tokenise([]byte(`[1,2,3]`)); err != nil {
		t.Errorf("shallow input after a deep one: depth state leaked across uses: %v", err)
	}
}
