// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// qs_coverage_test.go — coverage tests for previously untested qs functions.
//
// Covers: ScalarLTrim, ScalarRTrim, ScalarConcat, ScalarCharIndex, ScalarReverse,
// ScalarCast, ScalarFloor, ScalarCeiling, ScalarSign, ScalarSqrt, ScalarPower,
// ScalarGetDate, ScalarGetUTCDate, ScalarYear, ScalarMonth, ScalarDay,
// ScalarDatePart, ScalarDateDiff, ScalarDateTrunc, ScalarType, ScalarLabels,
// ParseTime/parseTime, plus nil/edge-case paths in existing partial-coverage
// functions (ToFloatSafe, ScalarToFloat, ScalarToInt, ScalarAbs, etc.).

package qs

import (
	"math"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// String scalars — LTrim, RTrim, Concat, CharIndex, Reverse, Cast
// ---------------------------------------------------------------------------

func TestScalarLTrim(t *testing.T) {
	cases := []struct {
		args []interface{}
		want string
	}{
		{[]interface{}{"  hello  "}, "hello  "},
		{[]interface{}{"\t\n hello"}, "hello"},
		{[]interface{}{"no-leading"}, "no-leading"},
		{[]interface{}{""}, ""},
		{[]interface{}{nil}, ""}, // nil → toString("") → ltrim → ""
	}
	for _, tc := range cases {
		got := ScalarLTrim(tc.args)
		if tc.args[0] == nil {
			if got != nil {
				t.Errorf("LTrim nil: want nil, got %v", got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("LTrim(%q): want %q, got %v", tc.args[0], tc.want, got)
		}
	}
	// Empty args → nil.
	if got := ScalarLTrim(nil); got != nil {
		t.Errorf("LTrim(nil args): want nil, got %v", got)
	}
}

func TestScalarRTrim(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"  hello  ", "  hello"},
		{"hello\t\n", "hello"},
		{"no-trailing", "no-trailing"},
		{"", ""},
	}
	for _, tc := range cases {
		got := ScalarRTrim([]interface{}{tc.in})
		if got != tc.want {
			t.Errorf("RTrim(%q): want %q, got %v", tc.in, tc.want, got)
		}
	}
	if got := ScalarRTrim([]interface{}{nil}); got != nil {
		t.Errorf("RTrim nil: want nil, got %v", got)
	}
	if got := ScalarRTrim(nil); got != nil {
		t.Errorf("RTrim empty args: want nil, got %v", got)
	}
}

func TestScalarConcat(t *testing.T) {
	// Basic concatenation.
	got := ScalarConcat([]interface{}{"hello", " ", "world"})
	if got != "hello world" {
		t.Errorf("Concat: want %q, got %v", "hello world", got)
	}
	// Nil elements are skipped.
	got = ScalarConcat([]interface{}{"a", nil, "b"})
	if got != "ab" {
		t.Errorf("Concat with nil: want %q, got %v", "ab", got)
	}
	// Empty args → empty string.
	got = ScalarConcat(nil)
	if got != "" {
		t.Errorf("Concat empty: want \"\", got %v", got)
	}
	// Numeric args are coerced to string.
	got = ScalarConcat([]interface{}{float64(42), " items"})
	s, ok := got.(string)
	if !ok || !strings.Contains(s, "42") {
		t.Errorf("Concat numeric: want string containing 42, got %v", got)
	}
}

func TestScalarCharIndex(t *testing.T) {
	cases := []struct {
		needle, haystack interface{}
		want             int
	}{
		{"lo", "hello world", 4}, // 1-indexed: 'l' at 3, 'lo' at 4
		{"world", "hello world", 7},
		{"missing", "hello", 0},
		{"", "hello", 1}, // empty needle: found at 1
	}
	for _, tc := range cases {
		got := ScalarCharIndex([]interface{}{tc.needle, tc.haystack})
		if got != tc.want {
			t.Errorf("CharIndex(%q in %q): want %d, got %v", tc.needle, tc.haystack, tc.want, got)
		}
	}
	// nil args → 0.
	if got := ScalarCharIndex([]interface{}{nil, "hello"}); got != 0 {
		t.Errorf("CharIndex nil needle: want 0, got %v", got)
	}
	if got := ScalarCharIndex([]interface{}{"x", nil}); got != 0 {
		t.Errorf("CharIndex nil haystack: want 0, got %v", got)
	}
	if got := ScalarCharIndex([]interface{}{"x"}); got != 0 {
		t.Errorf("CharIndex one arg: want 0, got %v", got)
	}
}

func TestScalarReverse(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", "olleh"},
		{"", ""},
		{"a", "a"},
		{"αβγ", "γβα"}, // unicode-safe
	}
	for _, tc := range cases {
		got := ScalarReverse([]interface{}{tc.in})
		if got != tc.want {
			t.Errorf("Reverse(%q): want %q, got %v", tc.in, tc.want, got)
		}
	}
	if got := ScalarReverse([]interface{}{nil}); got != nil {
		t.Errorf("Reverse nil: want nil, got %v", got)
	}
	if got := ScalarReverse(nil); got != nil {
		t.Errorf("Reverse empty args: want nil, got %v", got)
	}
}

func TestScalarCast(t *testing.T) {
	// Cast just calls toString.
	if got := ScalarCast([]interface{}{float64(42)}); got != "42" {
		t.Errorf("Cast(42): want \"42\", got %v", got)
	}
	if got := ScalarCast([]interface{}{"already"}); got != "already" {
		t.Errorf("Cast(string): want \"already\", got %v", got)
	}
	if got := ScalarCast([]interface{}{nil}); got != nil {
		t.Errorf("Cast nil: want nil, got %v", got)
	}
	if got := ScalarCast(nil); got != nil {
		t.Errorf("Cast empty args: want nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Math scalars — Floor, Ceiling, Sign, Sqrt, Power
// ---------------------------------------------------------------------------

func TestScalarFloor(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{2.9, 2},
		{-2.1, -3},
		{3.0, 3},
	}
	for _, tc := range cases {
		got := ScalarFloor([]interface{}{tc.in})
		if got != tc.want {
			t.Errorf("Floor(%v): want %v, got %v", tc.in, tc.want, got)
		}
	}
	if got := ScalarFloor(nil); got != nil {
		t.Errorf("Floor empty args: want nil, got %v", got)
	}
	if got := ScalarFloor([]interface{}{"notanumber"}); got != nil {
		t.Errorf("Floor non-numeric: want nil, got %v", got)
	}
}

func TestScalarCeiling(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{
		{2.1, 3},
		{-2.9, -2},
		{3.0, 3},
	}
	for _, tc := range cases {
		got := ScalarCeiling([]interface{}{tc.in})
		if got != tc.want {
			t.Errorf("Ceiling(%v): want %v, got %v", tc.in, tc.want, got)
		}
	}
	if got := ScalarCeiling(nil); got != nil {
		t.Errorf("Ceiling empty args: want nil, got %v", got)
	}
}

func TestScalarSign(t *testing.T) {
	cases := []struct {
		in   float64
		want int
	}{
		{5.0, 1},
		{-3.0, -1},
		{0.0, 0},
	}
	for _, tc := range cases {
		got := ScalarSign([]interface{}{tc.in})
		if got != tc.want {
			t.Errorf("Sign(%v): want %v, got %v", tc.in, tc.want, got)
		}
	}
	if got := ScalarSign(nil); got != nil {
		t.Errorf("Sign empty args: want nil, got %v", got)
	}
	if got := ScalarSign([]interface{}{"bad"}); got != nil {
		t.Errorf("Sign non-numeric: want nil, got %v", got)
	}
}

func TestScalarSqrt(t *testing.T) {
	got := ScalarSqrt([]interface{}{float64(9)})
	if got != 3.0 {
		t.Errorf("Sqrt(9): want 3.0, got %v", got)
	}
	if got := ScalarSqrt([]interface{}{float64(2)}); got == nil {
		t.Errorf("Sqrt(2): want non-nil")
	} else if v, ok := got.(float64); !ok || math.Abs(v-math.Sqrt(2)) > 1e-10 {
		t.Errorf("Sqrt(2): inaccurate: %v", got)
	}
	if got := ScalarSqrt(nil); got != nil {
		t.Errorf("Sqrt empty args: want nil, got %v", got)
	}
	if got := ScalarSqrt([]interface{}{"x"}); got != nil {
		t.Errorf("Sqrt non-numeric: want nil, got %v", got)
	}
}

func TestScalarPower(t *testing.T) {
	cases := []struct {
		base, exp, want float64
	}{
		{2, 10, 1024},
		{3, 3, 27},
		{4, 0.5, 2},
	}
	for _, tc := range cases {
		got := ScalarPower([]interface{}{tc.base, tc.exp})
		if got != tc.want {
			t.Errorf("Power(%v,%v): want %v, got %v", tc.base, tc.exp, tc.want, got)
		}
	}
	if got := ScalarPower([]interface{}{float64(2)}); got != nil {
		t.Errorf("Power one arg: want nil, got %v", got)
	}
	if got := ScalarPower(nil); got != nil {
		t.Errorf("Power empty args: want nil, got %v", got)
	}
	if got := ScalarPower([]interface{}{"x", float64(2)}); got != nil {
		t.Errorf("Power non-numeric base: want nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// Date/time scalars — ParseTime, GetDate, GetUTCDate, Year, Month, Day,
// DatePart, DateDiff, DateTrunc
// ---------------------------------------------------------------------------

func TestParseTime(t *testing.T) {
	// Already a time.Time.
	now := time.Now()
	if got, ok := ParseTime(now); !ok || !got.Equal(now) {
		t.Errorf("ParseTime(time.Time): want passthrough, got %v, ok=%v", got, ok)
	}
	// RFC3339 string.
	ts := "2026-03-15T14:30:00Z"
	if got, ok := ParseTime(ts); !ok {
		t.Errorf("ParseTime RFC3339: parse failed")
	} else if got.Year() != 2026 || got.Month() != 3 || got.Day() != 15 {
		t.Errorf("ParseTime RFC3339: unexpected result %v", got)
	}
	// Date-only string.
	if got, ok := ParseTime("2026-06-01"); !ok {
		t.Errorf("ParseTime date-only: parse failed")
	} else if got.Year() != 2026 || got.Month() != 6 {
		t.Errorf("ParseTime date-only: %v", got)
	}
	// Unrecognised format → false.
	if _, ok := ParseTime("not-a-date"); ok {
		t.Errorf("ParseTime garbage: want false")
	}
	// Non-string, non-time → false.
	if _, ok := ParseTime(float64(42)); ok {
		t.Errorf("ParseTime float: want false")
	}
}

func TestScalarGetDate(t *testing.T) {
	before := time.Now().Add(-time.Second)
	got := ScalarGetDate(nil)
	after := time.Now().Add(time.Second)
	s, ok := got.(string)
	if !ok {
		t.Fatalf("GetDate: want string, got %T", got)
	}
	parsed, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("GetDate: unparseable result %q: %v", s, err)
	}
	if parsed.Before(before) || parsed.After(after) {
		t.Errorf("GetDate: %v not within expected range", parsed)
	}
}

func TestScalarGetUTCDate(t *testing.T) {
	got := ScalarGetUTCDate(nil)
	s, ok := got.(string)
	if !ok {
		t.Fatalf("GetUTCDate: want string, got %T", got)
	}
	if _, err := time.Parse(time.RFC3339, s); err != nil {
		t.Errorf("GetUTCDate: unparseable result %q", s)
	}
}

const refDate = "2026-03-15T14:30:45Z"

func TestScalarYear(t *testing.T) {
	if got := ScalarYear([]interface{}{refDate}); got != 2026 {
		t.Errorf("Year: want 2026, got %v", got)
	}
	if got := ScalarYear(nil); got != nil {
		t.Errorf("Year nil args: want nil, got %v", got)
	}
	if got := ScalarYear([]interface{}{"bad"}); got != nil {
		t.Errorf("Year bad input: want nil, got %v", got)
	}
}

func TestScalarMonth(t *testing.T) {
	if got := ScalarMonth([]interface{}{refDate}); got != 3 {
		t.Errorf("Month: want 3, got %v", got)
	}
	if got := ScalarMonth(nil); got != nil {
		t.Errorf("Month nil args: want nil")
	}
}

func TestScalarDay(t *testing.T) {
	if got := ScalarDay([]interface{}{refDate}); got != 15 {
		t.Errorf("Day: want 15, got %v", got)
	}
	if got := ScalarDay(nil); got != nil {
		t.Errorf("Day nil args: want nil")
	}
}

func TestScalarDatePart(t *testing.T) {
	cases := []struct {
		part string
		want int
	}{
		{"year", 2026}, {"yy", 2026}, {"yyyy", 2026},
		{"month", 3}, {"mm", 3}, {"m", 3},
		{"day", 15}, {"dd", 15}, {"d", 15},
		{"hour", 14}, {"hh", 14},
		{"minute", 30}, {"mi", 30}, {"n", 30},
		{"second", 45}, {"ss", 45}, {"s", 45},
	}
	for _, tc := range cases {
		got := ScalarDatePart([]interface{}{tc.part, refDate})
		if got != tc.want {
			t.Errorf("DatePart(%q): want %d, got %v", tc.part, tc.want, got)
		}
	}
	// Unknown part → nil.
	if got := ScalarDatePart([]interface{}{"century", refDate}); got != nil {
		t.Errorf("DatePart unknown: want nil, got %v", got)
	}
	// week and dayofyear parts.
	if got := ScalarDatePart([]interface{}{"week", refDate}); got == nil {
		t.Errorf("DatePart week: want non-nil")
	}
	if got := ScalarDatePart([]interface{}{"dayofyear", refDate}); got == nil {
		t.Errorf("DatePart dayofyear: want non-nil")
	}
	if got := ScalarDatePart([]interface{}{"quarter", refDate}); got != 1 {
		t.Errorf("DatePart quarter: want 1, got %v", got)
	}
	if got := ScalarDatePart([]interface{}{"weekday", refDate}); got == nil {
		t.Errorf("DatePart weekday: want non-nil")
	}
	// Too few args → nil.
	if got := ScalarDatePart(nil); got != nil {
		t.Errorf("DatePart nil args: want nil, got %v", got)
	}
	// Bad date → nil.
	if got := ScalarDatePart([]interface{}{"year", "not-a-date"}); got != nil {
		t.Errorf("DatePart bad date: want nil, got %v", got)
	}
}

func TestScalarDateDiff(t *testing.T) {
	start := "2026-01-01T00:00:00Z"
	end := "2026-03-15T06:00:00Z"

	cases := []struct {
		part string
		want int
	}{
		{"year", 0},
		{"month", 2},
		{"day", 73},
		{"hour", 73*24 + 6},
	}
	for _, tc := range cases {
		got := ScalarDateDiff([]interface{}{tc.part, start, end})
		if got != tc.want {
			t.Errorf("DateDiff(%q): want %d, got %v", tc.part, tc.want, got)
		}
	}
	// Unknown part → nil.
	if got := ScalarDateDiff([]interface{}{"week", start, end}); got != nil {
		t.Errorf("DateDiff unknown part: want nil, got %v", got)
	}
	// minute and second parts exist.
	if got := ScalarDateDiff([]interface{}{"minute", start, end}); got == nil {
		t.Errorf("DateDiff minute: want non-nil")
	}
	if got := ScalarDateDiff([]interface{}{"second", start, end}); got == nil {
		t.Errorf("DateDiff second: want non-nil")
	}
	// Too few args → nil.
	if got := ScalarDateDiff(nil); got != nil {
		t.Errorf("DateDiff nil args: want nil, got %v", got)
	}
	// Bad date → nil.
	if got := ScalarDateDiff([]interface{}{"day", "bad", end}); got != nil {
		t.Errorf("DateDiff bad start: want nil, got %v", got)
	}
}

func TestScalarDateTrunc(t *testing.T) {
	input := "2026-03-15T14:30:45Z"
	cases := []struct{ part, want string }{
		{"year", "2026-01-01T00:00:00Z"},
		{"month", "2026-03-01T00:00:00Z"},
		{"day", "2026-03-15T00:00:00Z"},
		{"hour", "2026-03-15T14:00:00Z"},
		{"minute", "2026-03-15T14:30:00Z"},
		{"second", "2026-03-15T14:30:45Z"},
	}
	for _, tc := range cases {
		got := ScalarDateTrunc([]interface{}{tc.part, input})
		if got != tc.want {
			t.Errorf("DateTrunc(%q): want %q, got %v", tc.part, tc.want, got)
		}
	}
	// Unknown part → nil.
	if got := ScalarDateTrunc([]interface{}{"week", input}); got != nil {
		t.Errorf("DateTrunc unknown part: want nil, got %v", got)
	}
	// Bad date → nil.
	if got := ScalarDateTrunc([]interface{}{"day", "not-a-date"}); got != nil {
		t.Errorf("DateTrunc bad date: want nil, got %v", got)
	}
	// Too few args → nil.
	if got := ScalarDateTrunc(nil); got != nil {
		t.Errorf("DateTrunc nil args: want nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// ScalarType and ScalarLabels
// ---------------------------------------------------------------------------

func TestScalarType(t *testing.T) {
	// Edge map with "relationship" key.
	got := ScalarType([]interface{}{map[string]interface{}{"relationship": "OWNS", "other": "x"}})
	if got != "OWNS" {
		t.Errorf("ScalarType relationship: want OWNS, got %v", got)
	}
	// Fallback to "type" key.
	got = ScalarType([]interface{}{map[string]interface{}{"type": "REF"}})
	if got != "REF" {
		t.Errorf("ScalarType type: want REF, got %v", got)
	}
	// No matching key → nil.
	if got := ScalarType([]interface{}{map[string]interface{}{"foo": "bar"}}); got != nil {
		t.Errorf("ScalarType no key: want nil, got %v", got)
	}
	// Non-map → nil.
	if got := ScalarType([]interface{}{"string"}); got != nil {
		t.Errorf("ScalarType string: want nil, got %v", got)
	}
	// nil arg → nil.
	if got := ScalarType([]interface{}{nil}); got != nil {
		t.Errorf("ScalarType nil: want nil, got %v", got)
	}
	// empty args → nil.
	if got := ScalarType(nil); got != nil {
		t.Errorf("ScalarType no args: want nil, got %v", got)
	}
}

func TestScalarLabels(t *testing.T) {
	// Map with "labels" key.
	got := ScalarLabels([]interface{}{map[string]interface{}{"labels": []interface{}{"Asset", "Node"}}})
	labels, ok := got.([]interface{})
	if !ok || len(labels) != 2 {
		t.Errorf("ScalarLabels labels key: want slice of 2, got %v", got)
	}
	// Fallback: "type" key → single-element slice.
	got = ScalarLabels([]interface{}{map[string]interface{}{"type": "Asset"}})
	labels, ok = got.([]interface{})
	if !ok || len(labels) != 1 || labels[0] != "Asset" {
		t.Errorf("ScalarLabels type fallback: want [Asset], got %v", got)
	}
	// No matching key → nil.
	if got := ScalarLabels([]interface{}{map[string]interface{}{"foo": "bar"}}); got != nil {
		t.Errorf("ScalarLabels no key: want nil, got %v", got)
	}
	// Non-map → nil.
	if got := ScalarLabels([]interface{}{"string"}); got != nil {
		t.Errorf("ScalarLabels string: want nil, got %v", got)
	}
	// nil arg → nil.
	if got := ScalarLabels([]interface{}{nil}); got != nil {
		t.Errorf("ScalarLabels nil: want nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// ToFloatSafe — partial-coverage edge cases
// ---------------------------------------------------------------------------

func TestToFloatSafe_EdgeCases(t *testing.T) {
	// bool true/false.
	if v, ok := ToFloatSafe(true); !ok || v != 1.0 {
		t.Errorf("ToFloatSafe(true): want 1.0, got %v %v", v, ok)
	}
	if v, ok := ToFloatSafe(false); !ok || v != 0.0 {
		t.Errorf("ToFloatSafe(false): want 0.0, got %v %v", v, ok)
	}
	// int varieties.
	for _, iv := range []interface{}{int(7), int32(7), int64(7), uint(7), uint64(7)} {
		v, ok := ToFloatSafe(iv)
		if !ok || v != 7.0 {
			t.Errorf("ToFloatSafe(%T(7)): want 7.0 ok=true, got %v %v", iv, v, ok)
		}
	}
	// Unparseable string → false.
	if _, ok := ToFloatSafe("abc"); ok {
		t.Errorf("ToFloatSafe(\"abc\"): want false")
	}
	// nil → false.
	if _, ok := ToFloatSafe(nil); ok {
		t.Errorf("ToFloatSafe(nil): want false")
	}
}

// ---------------------------------------------------------------------------
// ScalarToInt / ScalarToFloat — uncovered branches
// ---------------------------------------------------------------------------

func TestScalarToInt_StringInput(t *testing.T) {
	got := ScalarToInt([]interface{}{"  42  "})
	if got != int64(42) {
		t.Errorf("ToInt string: want 42, got %v", got)
	}
	// Non-numeric string → nil.
	if got := ScalarToInt([]interface{}{"abc"}); got != nil {
		t.Errorf("ToInt bad string: want nil, got %v", got)
	}
}

func TestScalarToFloat_StringInput(t *testing.T) {
	got := ScalarToFloat([]interface{}{"  3.14  "})
	f, ok := got.(float64)
	if !ok || math.Abs(f-3.14) > 1e-9 {
		t.Errorf("ToFloat string: want 3.14, got %v", got)
	}
	if got := ScalarToFloat([]interface{}{"bad"}); got != nil {
		t.Errorf("ToFloat bad string: want nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// AggMin / AggMax — uncovered branches (nil/non-numeric elements)
// ---------------------------------------------------------------------------

func TestAggMin_WithNilAndNonNumeric(t *testing.T) {
	values := []interface{}{nil, "notanumber", float64(5), float64(2)}
	got := AggMin(values)
	if got != float64(2) {
		t.Errorf("AggMin with nil/non-numeric: want 2.0, got %v", got)
	}
	// All nil → nil.
	if got := AggMin([]interface{}{nil, nil}); got != nil {
		t.Errorf("AggMin all nil: want nil, got %v", got)
	}
	// Empty → nil.
	if got := AggMin(nil); got != nil {
		t.Errorf("AggMin empty: want nil, got %v", got)
	}
}

func TestAggMax_WithNilAndNonNumeric(t *testing.T) {
	// Nil values must be skipped; numeric winner must be selected.
	values := []interface{}{nil, float64(3), float64(10), nil, float64(7)}
	got := AggMax(values)
	if got != float64(10) {
		t.Errorf("AggMax with nils: want 10.0, got %v", got)
	}
	if got := AggMax([]interface{}{nil}); got != nil {
		t.Errorf("AggMax all nil: want nil, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// ScalarAbs — nil/non-numeric branch
// ---------------------------------------------------------------------------

func TestScalarAbs_NonNumeric(t *testing.T) {
	if got := ScalarAbs([]interface{}{"text"}); got != nil {
		t.Errorf("Abs non-numeric: want nil, got %v", got)
	}
}
