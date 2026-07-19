// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package qs

import (
	"testing"
)

// --- CompareValues ---

func TestCompareValues_Numeric(t *testing.T) {
	cases := []struct {
		a, b interface{}
		want int
	}{
		{1, 2, -1},
		{2, 1, 1},
		{1, 1, 0},
		{1.5, 2.5, -1},
		{int64(10), float64(10), 0},
		{nil, nil, 0},
		{nil, 1, -1},
		{1, nil, 1},
	}
	for _, c := range cases {
		got := CompareValues(c.a, c.b)
		if got != c.want {
			t.Errorf("CompareValues(%v, %v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCompareValues_Strings(t *testing.T) {
	if CompareValues("apple", "banana") >= 0 {
		t.Error("expected apple < banana")
	}
	if CompareValues("zebra", "ant") <= 0 {
		t.Error("expected zebra > ant")
	}
	if CompareValues("same", "same") != 0 {
		t.Error("expected same == same")
	}
}

// --- ToFloatSafe ---

func TestToFloatSafe(t *testing.T) {
	cases := []struct {
		v    interface{}
		want float64
		ok   bool
	}{
		{42, 42, true},
		{int64(7), 7, true},
		{float32(3.14), float64(float32(3.14)), true},
		{float64(2.71), 2.71, true},
		{true, 1, true},
		{false, 0, true},
		{"not a number", 0, false},
		{nil, 0, false},
	}
	for _, c := range cases {
		got, ok := ToFloatSafe(c.v)
		if ok != c.ok {
			t.Errorf("ToFloatSafe(%v): ok=%v want %v", c.v, ok, c.ok)
		}
		if ok && got != c.want {
			t.Errorf("ToFloatSafe(%v): got %v want %v", c.v, got, c.want)
		}
	}
}

// --- GetNestedValue ---

func TestGetNestedValue(t *testing.T) {
	m := map[string]interface{}{
		"name": "Alice",
		"address": map[string]interface{}{
			"city": "Montevideo",
		},
	}
	if v := GetNestedValue(m, "name"); v != "Alice" {
		t.Errorf("expected Alice, got %v", v)
	}
	if v := GetNestedValue(m, "address.city"); v != "Montevideo" {
		t.Errorf("expected Montevideo, got %v", v)
	}
	if v := GetNestedValue(m, "missing"); v != nil {
		t.Errorf("expected nil, got %v", v)
	}
	if v := GetNestedValue(m, "address.missing"); v != nil {
		t.Errorf("expected nil, got %v", v)
	}
}

// --- ApplyDistinct ---

func TestApplyDistinct(t *testing.T) {
	rows := []map[string]interface{}{
		{"name": "Alice"},
		{"name": "Bob"},
		{"name": "Alice"},
	}
	got := ApplyDistinct(rows)
	if len(got) != 2 {
		t.Errorf("expected 2 distinct rows, got %d", len(got))
	}
}

func TestApplyDistinct_Empty(t *testing.T) {
	if got := ApplyDistinct(nil); got != nil {
		t.Error("expected nil for nil input")
	}
	if got := ApplyDistinct([]map[string]interface{}{}); len(got) != 0 {
		t.Error("expected empty for empty input")
	}
}

// --- Scalar functions ---

func TestScalarUpper(t *testing.T) {
	if got := ScalarUpper([]interface{}{"hello"}); got != "HELLO" {
		t.Errorf("got %v", got)
	}
	if got := ScalarUpper([]interface{}{nil}); got != nil {
		t.Errorf("expected nil for nil input, got %v", got)
	}
}

func TestScalarLower(t *testing.T) {
	if got := ScalarLower([]interface{}{"WORLD"}); got != "world" {
		t.Errorf("got %v", got)
	}
}

func TestScalarTrim(t *testing.T) {
	if got := ScalarTrim([]interface{}{"  hi  "}); got != "hi" {
		t.Errorf("got %q", got)
	}
}

func TestScalarLen(t *testing.T) {
	if got := ScalarLen([]interface{}{"hello"}); got != 5 {
		t.Errorf("got %v", got)
	}
}

func TestScalarCoalesce(t *testing.T) {
	if got := ScalarCoalesce([]interface{}{nil, nil, "found"}); got != "found" {
		t.Errorf("got %v", got)
	}
	if got := ScalarCoalesce([]interface{}{nil, nil}); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestScalarRound(t *testing.T) {
	if got := ScalarRound([]interface{}{3.456, 2}); got != 3.46 {
		t.Errorf("got %v", got)
	}
	if got := ScalarRound([]interface{}{3.5}); got != 4.0 {
		t.Errorf("got %v", got)
	}
}

func TestScalarSubstring(t *testing.T) {
	if got := ScalarSubstring([]interface{}{"hello world", 7, 5}); got != "world" {
		t.Errorf("got %v", got)
	}
}

func TestScalarLeft(t *testing.T) {
	if got := ScalarLeft([]interface{}{"hello", 3}); got != "hel" {
		t.Errorf("got %v", got)
	}
}

func TestScalarRight(t *testing.T) {
	if got := ScalarRight([]interface{}{"hello", 3}); got != "llo" {
		t.Errorf("got %v", got)
	}
}

func TestScalarReplace(t *testing.T) {
	if got := ScalarReplace([]interface{}{"hello world", "world", "there"}); got != "hello there" {
		t.Errorf("got %v", got)
	}
}

func TestScalarSize(t *testing.T) {
	if got := ScalarSize([]interface{}{"hello"}); got != 5 {
		t.Errorf("got %v", got)
	}
	if got := ScalarSize([]interface{}{[]interface{}{1, 2, 3}}); got != 3 {
		t.Errorf("got %v", got)
	}
}

func TestScalarToInt(t *testing.T) {
	if got := ScalarToInt([]interface{}{"42"}); got != int64(42) {
		t.Errorf("got %v", got)
	}
	if got := ScalarToInt([]interface{}{3.9}); got != int64(3) {
		t.Errorf("got %v", got)
	}
}

func TestScalarToFloat(t *testing.T) {
	if got := ScalarToFloat([]interface{}{"3.14"}); got != 3.14 {
		t.Errorf("got %v", got)
	}
}

func TestScalarAbs(t *testing.T) {
	if got := ScalarAbs([]interface{}{-5.0}); got != 5.0 {
		t.Errorf("got %v", got)
	}
}

// --- Aggregate functions ---

func TestAggCount(t *testing.T) {
	if got := AggCount([]interface{}{1, nil, 2, nil, 3}); got != 3 {
		t.Errorf("got %v", got)
	}
}

func TestAggSum(t *testing.T) {
	if got := AggSum([]interface{}{1, 2, 3}); got != 6.0 {
		t.Errorf("got %v", got)
	}
	if got := AggSum([]interface{}{nil, nil}); got != nil {
		t.Errorf("expected nil for all-nil input, got %v", got)
	}
	if got := AggSum([]interface{}{}); got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
}

func TestAggAvg(t *testing.T) {
	if got := AggAvg([]interface{}{1, 2, 3}); got != 2.0 {
		t.Errorf("got %v", got)
	}
	if got := AggAvg([]interface{}{nil}); got != nil {
		t.Errorf("expected nil for all-nil input, got %v", got)
	}
}

func TestAggMin(t *testing.T) {
	if got := AggMin([]interface{}{3, 1, 2}); got != 1 {
		t.Errorf("got %v", got)
	}
}

func TestAggMax(t *testing.T) {
	if got := AggMax([]interface{}{3, 1, 2}); got != 3 {
		t.Errorf("got %v", got)
	}
}

func TestAggCollect(t *testing.T) {
	got := AggCollect([]interface{}{1, nil, 2, nil, 3})
	if len(got.([]interface{})) != 3 {
		t.Errorf("expected 3 collected values, got %v", got)
	}
}
