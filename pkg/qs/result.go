// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package qs

import (
	"encoding/json"
	"strings"
)

// GetNestedValue retrieves a value from a map using dot notation.
// "name" returns m["name"]; "address.city" returns m["address"].(map)["city"].
// Returns nil when any segment is absent or the intermediate value is not a map.
func GetNestedValue(m map[string]interface{}, path string) interface{} {
	if v, ok := m[path]; ok {
		return v
	}
	parts := strings.SplitN(path, ".", 2)
	if len(parts) == 2 {
		if nested, ok := m[parts[0]].(map[string]interface{}); ok {
			return GetNestedValue(nested, parts[1])
		}
	}
	return nil
}

// ApplyDistinct removes duplicate rows from a result set.
// Equality is determined by JSON serialisation of the full row map.
// Rows that cannot be serialised are included without deduplication.
func ApplyDistinct(results []map[string]interface{}) []map[string]interface{} {
	if len(results) == 0 {
		return results
	}
	seen := make(map[string]bool, len(results))
	out := make([]map[string]interface{}, 0, len(results))
	for _, row := range results {
		b, err := json.Marshal(row)
		if err != nil {
			out = append(out, row)
			continue
		}
		key := string(b)
		if !seen[key] {
			seen[key] = true
			out = append(out, row)
		}
	}
	return out
}
