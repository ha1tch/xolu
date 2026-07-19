// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package qs provides query-system utilities shared by the OQL and Sulpher
// subsystems. Functions here must remain free of subsystem-specific
// dependencies — no tsqlparser AST types, no sulpher AST types, no storage
// interfaces. Both subsystems import this package; optimising or changing
// any function here affects both.
package qs

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// CompareValues compares two interface{} values and returns -1, 0, or 1.
// Numeric types are compared numerically. All other types are compared as
// their fmt.Sprintf("%v") string representation. Nil sorts before any
// non-nil value.
func CompareValues(a, b interface{}) int {
	if a == nil && b == nil {
		return 0
	}
	if a == nil {
		return -1
	}
	if b == nil {
		return 1
	}

	aFloat, aOk := ToFloatSafe(a)
	bFloat, bOk := ToFloatSafe(b)
	if aOk && bOk {
		if math.IsNaN(aFloat) && math.IsNaN(bFloat) {
			return 0
		}
		if aFloat < bFloat {
			return -1
		}
		if aFloat > bFloat {
			return 1
		}
		return 0
	}

	aStr := fmt.Sprintf("%v", a)
	bStr := fmt.Sprintf("%v", b)
	return strings.Compare(aStr, bStr)
}

// ToFloat converts a value to float64, returning 0 for unrecognised types.
// Use ToFloatSafe when you need to distinguish a genuine zero from a
// conversion failure.
func ToFloat(v interface{}) float64 {
	f, _ := ToFloatSafe(v)
	return f
}

// ToFloatSafe converts a value to float64.
// Returns (value, true) on success and (0, false) when the type cannot be
// converted.
func ToFloatSafe(v interface{}) (float64, bool) {
	switch val := v.(type) {
	case int:
		return float64(val), true
	case int8:
		return float64(val), true
	case int16:
		return float64(val), true
	case int32:
		return float64(val), true
	case int64:
		return float64(val), true
	case uint:
		return float64(val), true
	case uint8:
		return float64(val), true
	case uint16:
		return float64(val), true
	case uint32:
		return float64(val), true
	case uint64:
		return float64(val), true
	case float32:
		f := float64(val)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	case float64:
		if math.IsNaN(val) || math.IsInf(val, 0) {
			return 0, false
		}
		return val, true
	case bool:
		if val {
			return 1, true
		}
		return 0, true
	case string:
		// DECIMAL fields and some JSON values are stored as strings.
		// Parse them so that toInteger/toFloat and arithmetic work correctly.
		if f, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
			if math.IsNaN(f) || math.IsInf(f, 0) {
				return 0, false
			}
			return f, true
		}
		return 0, false
	}
	return 0, false
}
