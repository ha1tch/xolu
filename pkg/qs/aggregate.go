// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package qs

import "strconv"

// toNumeric attempts to extract a float64 from any value, including
// string-encoded numerics (e.g. SQLite decimal columns returned as text).
func toNumeric(v interface{}) (float64, bool) {
	if f, ok := ToFloatSafe(v); ok {
		return f, true
	}
	if s, ok := v.(string); ok {
		// strconv.ParseFloat, not fmt.Sscanf: Sscanf's "%f" verb matches
		// only a leading numeric prefix and reports success even with
		// unconsumed trailing characters -- a string like "2026-08-03..."
		// would silently parse as the bare number 2026. ParseFloat
		// requires the whole string to be a valid float, which is what
		// this function's own "string-encoded numerics" comment already
		// assumes.
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// AggFunc is a function that reduces a slice of values to a single value.
// Both OQL and Sulpher use this type; dispatch is handled by each
// subsystem's own aggregator.
type AggFunc func(values []interface{}) interface{}

// AggregateFunctions is the canonical map of aggregate function name
// (upper-case) to implementation.
var AggregateFunctions = map[string]AggFunc{
	"COUNT": AggCount,
	"SUM":   AggSum,
	"AVG":   AggAvg,
	"MIN":   AggMin,
	"MAX":   AggMax,
}

// AggCount counts non-nil values. When all values are nil (COUNT(*) semantics
// where the caller passes a slice of non-nil sentinels), it counts all of them.
func AggCount(values []interface{}) interface{} {
	count := 0
	for _, v := range values {
		if v != nil {
			count++
		}
	}
	return count
}

// AggSum sums all numeric non-nil values.
// Returns nil when no numeric values are present (SQL standard: SUM of empty
// set is NULL). Handles string-encoded numerics (e.g. SQLite decimal columns
// returned as text).
func AggSum(values []interface{}) interface{} {
	var sum float64
	hasValue := false
	for _, v := range values {
		if v == nil {
			continue
		}
		if f, ok := toNumeric(v); ok {
			sum += f
			hasValue = true
		}
	}
	if !hasValue {
		return nil
	}
	return sum
}

// AggAvg returns the mean of all numeric non-nil values.
// Returns nil when no numeric values are present.
func AggAvg(values []interface{}) interface{} {
	var sum float64
	count := 0
	for _, v := range values {
		if v == nil {
			continue
		}
		if f, ok := toNumeric(v); ok {
			sum += f
			count++
		}
	}
	if count == 0 {
		return nil
	}
	return sum / float64(count)
}

// AggMin returns the minimum value. Numeric types (including string-encoded
// numerics) are compared numerically; other types use CompareValues ordering.
// Returns nil when values is empty or all nil.
func AggMin(values []interface{}) interface{} {
	var min interface{}
	for _, v := range values {
		if v == nil {
			continue
		}
		if min == nil {
			min = v
			continue
		}
		// Use numeric comparison when both sides parse as numbers
		vf, vOk := toNumeric(v)
		mf, mOk := toNumeric(min)
		if vOk && mOk {
			if vf < mf {
				min = v
			}
		} else if CompareValues(v, min) < 0 {
			min = v
		}
	}
	return min
}

// AggMax returns the maximum value. Numeric types (including string-encoded
// numerics) are compared numerically; other types use CompareValues ordering.
// Returns nil when values is empty or all nil.
func AggMax(values []interface{}) interface{} {
	var max interface{}
	for _, v := range values {
		if v == nil {
			continue
		}
		if max == nil {
			max = v
			continue
		}
		vf, vOk := toNumeric(v)
		mf, mOk := toNumeric(max)
		if vOk && mOk {
			if vf > mf {
				max = v
			}
		} else if CompareValues(v, max) > 0 {
			max = v
		}
	}
	return max
}

// AggCollect accumulates all non-nil values into a []interface{} slice.
// This is the Cypher collect() aggregate; it has no T-SQL equivalent.
func AggCollect(values []interface{}) interface{} {
	out := make([]interface{}, 0, len(values))
	for _, v := range values {
		if v != nil {
			out = append(out, v)
		}
	}
	return out
}
