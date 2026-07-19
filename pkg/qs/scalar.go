// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package qs

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// ScalarFunc is a function that takes a list of evaluated arguments and
// returns a single value. Both OQL and Sulpher use this type; dispatch
// is handled by each subsystem's own function registry.
type ScalarFunc func(args []interface{}) interface{}

// finiteResult wraps a ScalarFunc so that non-finite float results (NaN, +Inf,
// -Inf) are normalised to nil (SQL NULL). Any float32/float64 that leaks a
// non-finite value would otherwise corrupt downstream JSON serialisation and
// panic shopspring/decimal conversions. The invariant is enforced once at the
// dispatcher rather than in every individual scalar.
func finiteResult(fn ScalarFunc) ScalarFunc {
	return func(args []interface{}) interface{} {
		r := fn(args)
		switch v := r.(type) {
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return nil
			}
		case float32:
			f := float64(v)
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return nil
			}
		}
		return r
	}
}

// ScalarFunctions is the canonical map of function name (upper-case) to
// implementation. OQL and Sulpher build their own registries from this map,
// adding subsystem-specific aliases or overrides as needed. All entries are
// wrapped in finiteResult so callers can rely on the no-NaN-no-Inf invariant.
var ScalarFunctions = wrapFinite(rawScalarFunctions)

func wrapFinite(raw map[string]ScalarFunc) map[string]ScalarFunc {
	wrapped := make(map[string]ScalarFunc, len(raw))
	for k, v := range raw {
		wrapped[k] = finiteResult(v)
	}
	return wrapped
}

var rawScalarFunctions = map[string]ScalarFunc{
	// String
	"UPPER":     ScalarUpper,
	"LOWER":     ScalarLower,
	"LEN":       ScalarLen,
	"TRIM":      ScalarTrim,
	"LTRIM":     ScalarLTrim,
	"RTRIM":     ScalarRTrim,
	"CONCAT":    ScalarConcat,
	"SUBSTRING": ScalarSubstring,
	"LEFT":      ScalarLeft,
	"RIGHT":     ScalarRight,
	"REPLACE":   ScalarReplace,
	"CHARINDEX": ScalarCharIndex,
	"REVERSE":   ScalarReverse,
	// Type conversion
	"CAST":      ScalarCast,
	"TOSTRING":  ScalarCast,    // Cypher alias
	"TOINTEGER": ScalarToInt,   // Cypher alias
	"TOFLOAT":   ScalarToFloat, // Cypher alias
	// Null handling
	"COALESCE": ScalarCoalesce,
	"ISNULL":   ScalarCoalesce, // T-SQL alias
	// Numeric
	"ABS":     ScalarAbs,
	"ROUND":   ScalarRound,
	"FLOOR":   ScalarFloor,
	"CEILING": ScalarCeiling,
	"SIGN":    ScalarSign,
	"SQRT":    ScalarSqrt,
	"POWER":   ScalarPower,
	// Date/time
	"GETDATE":    ScalarGetDate,
	"GETUTCDATE": ScalarGetUTCDate,
	"YEAR":       ScalarYear,
	"MONTH":      ScalarMonth,
	"DAY":        ScalarDay,
	"DATEPART":   ScalarDatePart,
	"DATEDIFF":   ScalarDateDiff,
	"DATE_TRUNC": ScalarDateTrunc,
	// Collection (Cypher-oriented)
	"SIZE":   ScalarSize,
	"TYPE":   ScalarType,
	"LABELS": ScalarLabels,
}

// --- Time parsing ---

var timeFormats = []string{
	time.RFC3339,
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05Z",
	"2006-01-02",
}

func ParseTime(v interface{}) (time.Time, bool) {
	return parseTime(v)
}

func parseTime(v interface{}) (time.Time, bool) {
	// Pass through an already-parsed time.Time
	if t, ok := v.(time.Time); ok {
		return t, true
	}
	s, ok := v.(string)
	if !ok {
		return time.Time{}, false
	}
	for _, f := range timeFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func toString(v interface{}) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// --- String functions ---

func ScalarUpper(args []interface{}) interface{} {
	if len(args) < 1 || args[0] == nil {
		return nil
	}
	return strings.ToUpper(toString(args[0]))
}

func ScalarLower(args []interface{}) interface{} {
	if len(args) < 1 || args[0] == nil {
		return nil
	}
	return strings.ToLower(toString(args[0]))
}

func ScalarLen(args []interface{}) interface{} {
	if len(args) < 1 || args[0] == nil {
		return nil
	}
	return len(toString(args[0]))
}

func ScalarTrim(args []interface{}) interface{} {
	if len(args) < 1 || args[0] == nil {
		return nil
	}
	return strings.TrimSpace(toString(args[0]))
}

func ScalarLTrim(args []interface{}) interface{} {
	if len(args) < 1 || args[0] == nil {
		return nil
	}
	return strings.TrimLeftFunc(toString(args[0]), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
}

func ScalarRTrim(args []interface{}) interface{} {
	if len(args) < 1 || args[0] == nil {
		return nil
	}
	return strings.TrimRightFunc(toString(args[0]), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
}

func ScalarConcat(args []interface{}) interface{} {
	var sb strings.Builder
	for _, a := range args {
		if a != nil {
			sb.WriteString(toString(a))
		}
	}
	return sb.String()
}

func ScalarSubstring(args []interface{}) interface{} {
	if len(args) < 2 {
		return nil
	}
	s := toString(args[0])
	start := int(ToFloat(args[1])) - 1
	if start < 0 {
		start = 0
	}
	if start >= len(s) {
		return ""
	}
	if len(args) >= 3 {
		length := int(ToFloat(args[2]))
		end := start + length
		if end > len(s) {
			end = len(s)
		}
		// D-007: a negative length makes end < start, so s[start:end] panics.
		// Clamp to an empty selection rather than slicing backwards.
		if end < start {
			end = start
		}
		return s[start:end]
	}
	return s[start:]
}

func ScalarLeft(args []interface{}) interface{} {
	if len(args) < 2 {
		return nil
	}
	s := toString(args[0])
	n := int(ToFloat(args[1]))
	if n >= len(s) {
		return s
	}
	if n < 0 {
		return ""
	}
	return s[:n]
}

func ScalarRight(args []interface{}) interface{} {
	if len(args) < 2 {
		return nil
	}
	s := toString(args[0])
	n := int(ToFloat(args[1]))
	if n >= len(s) {
		return s
	}
	if n < 0 {
		return ""
	}
	return s[len(s)-n:]
}

func ScalarReplace(args []interface{}) interface{} {
	if len(args) < 3 {
		return nil
	}
	return strings.ReplaceAll(toString(args[0]), toString(args[1]), toString(args[2]))
}

func ScalarCharIndex(args []interface{}) interface{} {
	if len(args) < 2 || args[0] == nil || args[1] == nil {
		return 0
	}
	needle := toString(args[0])
	haystack := toString(args[1])
	idx := strings.Index(haystack, needle)
	if idx < 0 {
		return 0
	}
	return idx + 1
}

func ScalarReverse(args []interface{}) interface{} {
	if len(args) < 1 || args[0] == nil {
		return nil
	}
	s := toString(args[0])
	r := []rune(s)
	for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
		r[i], r[j] = r[j], r[i]
	}
	return string(r)
}

// --- Type conversion ---

func ScalarCast(args []interface{}) interface{} {
	if len(args) < 1 || args[0] == nil {
		return nil
	}
	return toString(args[0])
}

func ScalarToInt(args []interface{}) interface{} {
	if len(args) < 1 || args[0] == nil {
		return nil
	}
	if f, ok := ToFloatSafe(args[0]); ok {
		return int64(f)
	}
	if s, ok := args[0].(string); ok {
		if i, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			return i
		}
	}
	return nil
}

func ScalarToFloat(args []interface{}) interface{} {
	if len(args) < 1 || args[0] == nil {
		return nil
	}
	if f, ok := ToFloatSafe(args[0]); ok {
		return f
	}
	if s, ok := args[0].(string); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return f
		}
	}
	return nil
}

// --- Null handling ---

func ScalarCoalesce(args []interface{}) interface{} {
	for _, a := range args {
		if a != nil {
			return a
		}
	}
	return nil
}

// --- Numeric ---

func ScalarAbs(args []interface{}) interface{} {
	if len(args) < 1 {
		return nil
	}
	f, ok := ToFloatSafe(args[0])
	if !ok {
		return nil
	}
	return math.Abs(f)
}

func ScalarRound(args []interface{}) interface{} {
	if len(args) < 1 {
		return nil
	}
	f, ok := ToFloatSafe(args[0])
	if !ok {
		return nil
	}
	precision := 0
	if len(args) >= 2 {
		if p, ok2 := ToFloatSafe(args[1]); ok2 {
			precision = int(p)
		}
	}
	shift := math.Pow(10, float64(precision))
	r := math.Round(f*shift) / shift
	// D-007: a large precision overflows shift to +Inf, making r NaN/Inf, which
	// encoding/json cannot marshal (the whole query response would fail). Coerce
	// a non-finite result to nil so it serialises cleanly as SQL NULL.
	if math.IsNaN(r) || math.IsInf(r, 0) {
		return nil
	}
	return r
}

func ScalarFloor(args []interface{}) interface{} {
	if len(args) < 1 {
		return nil
	}
	f, ok := ToFloatSafe(args[0])
	if !ok {
		return nil
	}
	return math.Floor(f)
}

func ScalarCeiling(args []interface{}) interface{} {
	if len(args) < 1 {
		return nil
	}
	f, ok := ToFloatSafe(args[0])
	if !ok {
		return nil
	}
	return math.Ceil(f)
}

func ScalarSign(args []interface{}) interface{} {
	if len(args) < 1 {
		return nil
	}
	f, ok := ToFloatSafe(args[0])
	if !ok {
		return nil
	}
	if f < 0 {
		return -1
	}
	if f > 0 {
		return 1
	}
	return 0
}

func ScalarSqrt(args []interface{}) interface{} {
	if len(args) < 1 {
		return nil
	}
	f, ok := ToFloatSafe(args[0])
	if !ok {
		return nil
	}
	r := math.Sqrt(f)
	// Non-finite results (e.g. SQRT(-1) = NaN) cannot be JSON-encoded and would
	// break a query response; coerce to nil (SQL NULL). Mirrors the D-007 ROUND
	// fix and makes this function safe if registered on the OQL surface.
	if math.IsNaN(r) || math.IsInf(r, 0) {
		return nil
	}
	return r
}

func ScalarPower(args []interface{}) interface{} {
	if len(args) < 2 {
		return nil
	}
	base, ok1 := ToFloatSafe(args[0])
	exp, ok2 := ToFloatSafe(args[1])
	if !ok1 || !ok2 {
		return nil
	}
	r := math.Pow(base, exp)
	// Non-finite results (e.g. POWER(1e308, 2) = +Inf) cannot be JSON-encoded;
	// coerce to nil (SQL NULL), mirroring the D-007 ROUND fix.
	if math.IsNaN(r) || math.IsInf(r, 0) {
		return nil
	}
	return r
}

// --- Date/time ---

func ScalarGetDate(args []interface{}) interface{} {
	// GETDATE() returns LOCAL server time by T-SQL contract — deliberately not
	// routed through xolutime (which has no local-now). ScalarGetUTCDate is the
	// UTC counterpart.
	return time.Now().Format(time.RFC3339)
}

func ScalarGetUTCDate(args []interface{}) interface{} {
	return ot.Now().Format(time.RFC3339, nil)
}

// ScalarNewID implements T-SQL NEWID(): it returns a random (version 4) UUID
// string. It takes no arguments. This is the OQL-surface counterpart of the
// FSM eval NEWID()/UUID_V4(); both produce a real, unique, unpredictable UUID
// via the same generator (uuid.NewRandom). On the rare generator error it
// returns nil (SQL NULL) rather than a malformed value.
func ScalarNewID(args []interface{}) interface{} {
	id, err := uuid.NewRandom()
	if err != nil {
		return nil
	}
	return id.String()
}

func ScalarYear(args []interface{}) interface{} {
	if len(args) < 1 {
		return nil
	}
	t, ok := parseTime(args[0])
	if !ok {
		return nil
	}
	return t.Year()
}

func ScalarMonth(args []interface{}) interface{} {
	if len(args) < 1 {
		return nil
	}
	t, ok := parseTime(args[0])
	if !ok {
		return nil
	}
	return int(t.Month())
}

func ScalarDay(args []interface{}) interface{} {
	if len(args) < 1 {
		return nil
	}
	t, ok := parseTime(args[0])
	if !ok {
		return nil
	}
	return t.Day()
}

func ScalarDatePart(args []interface{}) interface{} {
	if len(args) < 2 {
		return nil
	}
	part := strings.ToLower(toString(args[0]))
	t, ok := parseTime(args[1])
	if !ok {
		return nil
	}
	switch part {
	case "year", "yy", "yyyy":
		return t.Year()
	case "month", "mm", "m":
		return int(t.Month())
	case "day", "dd", "d":
		return t.Day()
	case "hour", "hh":
		return t.Hour()
	case "minute", "mi", "n":
		return t.Minute()
	case "second", "ss", "s":
		return t.Second()
	case "weekday", "dw", "dayofweek", "dow":
		return int(t.Weekday())
	case "week", "wk", "ww":
		_, w := t.ISOWeek()
		return w
	case "dayofyear", "dy", "y":
		return t.YearDay()
	case "quarter", "qq", "q":
		return (int(t.Month()) + 2) / 3
	}
	return nil
}

func ScalarDateDiff(args []interface{}) interface{} {
	if len(args) < 3 {
		return nil
	}
	part := strings.ToLower(toString(args[0]))
	t1, ok1 := parseTime(args[1])
	t2, ok2 := parseTime(args[2])
	if !ok1 || !ok2 {
		return nil
	}
	dur := t2.Sub(t1)
	switch part {
	case "year", "yy", "yyyy":
		return t2.Year() - t1.Year()
	case "month", "mm", "m":
		return (t2.Year()-t1.Year())*12 + int(t2.Month()) - int(t1.Month())
	case "day", "dd", "d":
		return int(dur.Hours() / 24)
	case "hour", "hh":
		return int(dur.Hours())
	case "minute", "mi", "n":
		return int(dur.Minutes())
	case "second", "ss", "s":
		return int(dur.Seconds())
	}
	return nil
}

func ScalarDateTrunc(args []interface{}) interface{} {
	if len(args) < 2 {
		return nil
	}
	part := strings.ToLower(toString(args[0]))
	t, ok := parseTime(args[1])
	if !ok {
		return nil
	}
	var result time.Time
	switch part {
	case "year":
		result = time.Date(t.Year(), 1, 1, 0, 0, 0, 0, t.Location())
	case "month":
		result = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location())
	case "day":
		result = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
	case "hour":
		result = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, t.Location())
	case "minute":
		result = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, t.Location())
	case "second":
		result = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, t.Location())
	default:
		return nil
	}
	return result.Format(time.RFC3339)
}

// --- Collection / graph helpers (Cypher-oriented) ---

// ScalarSize returns the length of a string or the number of elements in a
// slice. Returns nil for other types.
func ScalarSize(args []interface{}) interface{} {
	if len(args) < 1 || args[0] == nil {
		return nil
	}
	switch v := args[0].(type) {
	case string:
		return len(v)
	case []interface{}:
		return len(v)
	}
	return nil
}

// ScalarType returns the relationship type string from a map that has a
// "relationship" or "type" key — matching xolu's GraphEdge representation.
// Returns nil when the argument is not a recognised edge map.
func ScalarType(args []interface{}) interface{} {
	if len(args) < 1 || args[0] == nil {
		return nil
	}
	m, ok := args[0].(map[string]interface{})
	if !ok {
		return nil
	}
	if rel, ok := m["relationship"]; ok {
		return rel
	}
	if t, ok := m["type"]; ok {
		return t
	}
	return nil
}

// ScalarLabels returns the label slice from a node map that has a "labels"
// key, or a single-element slice containing the "type" key value.
// Returns nil when the argument is not a recognised node map.
func ScalarLabels(args []interface{}) interface{} {
	if len(args) < 1 || args[0] == nil {
		return nil
	}
	m, ok := args[0].(map[string]interface{})
	if !ok {
		return nil
	}
	if labels, ok := m["labels"]; ok {
		return labels
	}
	if t, ok := m["type"]; ok {
		return []interface{}{t}
	}
	return nil
}
