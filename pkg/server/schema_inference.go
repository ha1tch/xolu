// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// schema_inference.go — the heuristic engine behind schema promotion
// (T-151): given a schemaless entity type's actual stored data, infer
// a reasonable JSON Schema for it, with per-field reasoning so a
// caller can judge how much to trust the guess rather than being
// handed a black box.
//
// Design principle: a heuristic guess is not a fact. Every field in
// FieldAnalysis carries its own coverage and confidence, and the
// suggested schema itself defaults to permissive
// (additionalProperties left unset, not forced false) -- inference
// runs over a bounded SAMPLE, not necessarily every row, so a strict
// schema built from a sample risks rejecting a legitimate field this
// sample just didn't happen to see.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

const (
	// defaultSampleSize bounds how many rows a single inference pass
	// reads -- scanning every row of a large entity population would
	// be expensive for a "give me a quick guess" operation. Large
	// enough for enum/type patterns to show up reliably, small enough
	// to stay cheap.
	defaultSampleSize = 500

	// enumMaxDistinct: a field is only suggested as an enum when the
	// number of distinct values seen is at or below this AND well
	// below the number of rows that had the field at all -- a field
	// with 3 distinct values across 500 rows looks like a real enum;
	// a field with 3 distinct values across 4 rows is just a small
	// sample, not evidence of a closed value set.
	enumMaxDistinct = 12
	// enumMinRowsPerValue: require at least this many observations on
	// average per distinct value before calling it an enum, so a
	// sparse field with a handful of once-seen values doesn't produce
	// a false-confidence enum suggestion.
	enumMinRowsPerValue = 3

	// distinctValueTrackingCap stops tracking new distinct values past
	// this count -- once a field has shown this many different values,
	// it's clearly not enum-shaped, and continuing to track every
	// unique string in a large sample would waste memory for no
	// benefit.
	distinctValueTrackingCap = 200
)

var decimalLikeRe = regexp.MustCompile(`^-?\d+\.\d+$`)

// FieldAnalysis is one field's inferred shape and the evidence behind
// it, for a single entity type's schema-suggestion response.
type FieldAnalysis struct {
	Field string `json:"field"`
	// InferredType is a JSON Schema type ("string", "number",
	// "boolean", "object", "array") or "ref" for a field whose values
	// consistently match xolu's own REF wire shape
	// ({"type":"REF","entity":...,"id":...}) -- "ref" is not a JSON
	// Schema type, it's shorthand in this response for
	// {"type":"object","format":"ref"}, which is what actually lands
	// in SuggestedSchema for such a field.
	InferredType string `json:"inferred_type"`
	// Coverage is the fraction of sampled rows where this field was
	// present (0.0-1.0). 1.0 means every sampled row had it -- the
	// basis for suggesting it as required.
	Coverage float64 `json:"coverage"`
	// Confidence is this package's own qualitative read on how much
	// to trust InferredType: "high" for a field with a single
	// consistent type across every observation, "medium" for a
	// pattern-based guess (decimal-like strings), "low" for anything
	// sparse or inconsistent.
	Confidence string `json:"confidence"`
	// SuggestedEnum is set when the field looks closed-set (few
	// distinct values relative to how often it was observed) --
	// present alongside InferredType, not instead of it.
	SuggestedEnum []string `json:"suggested_enum,omitempty"`
	// Note explains anything a caller should know before trusting
	// this field's inference blindly -- e.g. why it was excluded from
	// SuggestedSchema, or why confidence is low.
	Note string `json:"note,omitempty"`
}

// SchemaSuggestion is the full response for one entity type.
type SchemaSuggestion struct {
	EntityType string `json:"entity_type"`
	// SampledRows/TotalRows: inference always runs over a bounded
	// sample (defaultSampleSize), never necessarily every row --
	// both counts are reported so a caller can judge how
	// representative the sample likely was.
	SampledRows int `json:"sampled_rows"`
	TotalRows   int `json:"total_rows"`
	// SuggestedSchema is ready to hand to DefineEntitySchema/Promote
	// as-is, or to edit first. additionalProperties is deliberately
	// left unset (permissive), not forced false -- see this file's own
	// header comment.
	SuggestedSchema map[string]interface{} `json:"suggested_schema"`
	// FieldAnalysis explains the reasoning behind SuggestedSchema,
	// field by field, including fields that were EXCLUDED from it
	// (inconsistent types) so nothing is silently dropped without
	// explanation.
	FieldAnalysis []FieldAnalysis `json:"field_analysis"`
}

// fieldStats accumulates raw observations for one field name across a
// sample -- the intermediate state inferSchema builds before turning
// it into the caller-facing FieldAnalysis/SuggestedSchema shapes.
type fieldStats struct {
	presentCount int
	nullCount    int
	typeCounts   map[string]int // JSON Schema type name -> observation count
	refShaped    int            // observations matching xolu's REF wire shape
	decimalLike  int            // observations that were strings matching a decimal pattern
	distinct     map[string]int // string representation -> count, capped at distinctValueTrackingCap
	distinctCap  bool           // true once tracking was stopped because the cap was hit
}

func newFieldStats() *fieldStats {
	return &fieldStats{
		typeCounts: make(map[string]int),
		distinct:   make(map[string]int),
	}
}

// observe records one occurrence of this field's value.
func (fs *fieldStats) observe(v interface{}) {
	fs.presentCount++
	if v == nil {
		fs.nullCount++
		fs.typeCounts["null"]++
		return
	}

	switch val := v.(type) {
	case string:
		fs.typeCounts["string"]++
		if decimalLikeRe.MatchString(val) {
			fs.decimalLike++
		}
		fs.recordDistinct(val)
	case bool:
		fs.typeCounts["boolean"]++
		fs.recordDistinct(fmt.Sprintf("%v", val))
	case float64:
		fs.typeCounts["number"]++
		fs.recordDistinct(fmt.Sprintf("%v", val))
	case map[string]interface{}:
		fs.typeCounts["object"]++
		if isRefShape(val) {
			fs.refShaped++
		}
	case []interface{}:
		fs.typeCounts["array"]++
	default:
		fs.typeCounts["unknown"]++
	}
}

func (fs *fieldStats) recordDistinct(s string) {
	if fs.distinctCap {
		return
	}
	if _, ok := fs.distinct[s]; !ok && len(fs.distinct) >= distinctValueTrackingCap {
		fs.distinctCap = true
		return
	}
	fs.distinct[s]++
}

// isRefShape reports whether v matches xolu's own REF wire format
// exactly: {"type":"REF","entity":<string>,"id":<number>} (per
// docs/JSON_SCHEMA.md's own documented shape) -- checked structurally,
// not just "has a type field", so an unrelated object that happens to
// have its own "type" key isn't misidentified.
func isRefShape(v map[string]interface{}) bool {
	t, ok := v["type"].(string)
	if !ok || t != "REF" {
		return false
	}
	if _, ok := v["entity"].(string); !ok {
		return false
	}
	if _, ok := v["id"].(float64); !ok {
		return false
	}
	return true
}

// dominantType returns the JSON type with the most observations, and
// whether the field's type was consistent enough across the sample to
// trust with high confidence (a single type accounting for
// (almost) every non-null observation).
func (fs *fieldStats) dominantType() (typ string, consistent bool) {
	nonNull := fs.presentCount - fs.nullCount
	if nonNull == 0 {
		return "null", false
	}
	best, bestCount := "", 0
	for t, c := range fs.typeCounts {
		if t == "null" {
			continue
		}
		if c > bestCount {
			best, bestCount = t, c
		}
	}
	// Consistent means the dominant type accounts for every non-null
	// observation -- any second type at all means real inconsistency,
	// not sampling noise, since JSON's own type system doesn't produce
	// spurious type variation on its own.
	return best, bestCount == nonNull
}

// analyzeField turns one field's raw stats into its caller-facing
// FieldAnalysis, and, when the field is clean enough to trust, its own
// JSON Schema property fragment for SuggestedSchema.
func analyzeField(name string, fs *fieldStats, totalSampled int) (FieldAnalysis, map[string]interface{}, bool) {
	coverage := float64(fs.presentCount) / float64(totalSampled)
	dominant, consistent := fs.dominantType()

	fa := FieldAnalysis{Field: name, Coverage: coverage}

	if !consistent {
		fa.InferredType = "mixed"
		fa.Confidence = "low"
		fa.Note = fmt.Sprintf("inconsistent types across the sample (%s); excluded from the suggested schema, needs manual review", describeTypeCounts(fs.typeCounts))
		return fa, nil, false
	}

	nonNullCount := fs.presentCount - fs.nullCount
	prop := map[string]interface{}{}

	switch dominant {
	case "object":
		if fs.refShaped == nonNullCount {
			fa.InferredType = "ref"
			fa.Confidence = "high"
			prop["type"] = "object"
			prop["format"] = "ref"
		} else {
			fa.InferredType = "object"
			fa.Confidence = "low"
			fa.Note = "nested object field; this engine does not infer nested schemas, review manually"
			prop["type"] = "object"
		}
	case "string":
		fa.InferredType = "string"
		prop["type"] = "string"
		if fs.decimalLike == nonNullCount && nonNullCount > 0 {
			fa.Confidence = "medium"
			fa.Note = "every sampled value looks like a decimal string (e.g. \"12.50\"); format:\"decimal\" suggested, but this is a pattern match, not a type guarantee -- a version string like \"1.0\" would match too, review before trusting it"
			prop["format"] = "decimal"
		} else {
			fa.Confidence = "high"
		}
	case "number", "boolean", "array":
		fa.InferredType = dominant
		fa.Confidence = "high"
		prop["type"] = dominant
	default:
		fa.InferredType = dominant
		fa.Confidence = "low"
		fa.Note = "unrecognized value shape; excluded from the suggested schema"
		return fa, nil, false
	}

	if !fs.distinctCap && len(fs.distinct) > 0 && len(fs.distinct) <= enumMaxDistinct &&
		nonNullCount >= len(fs.distinct)*enumMinRowsPerValue {
		values := make([]string, 0, len(fs.distinct))
		for v := range fs.distinct {
			values = append(values, v)
		}
		sort.Strings(values)
		fa.SuggestedEnum = values
		enumVals := make([]interface{}, len(values))
		for i, v := range values {
			enumVals[i] = v
		}
		if dominant == "string" {
			prop["enum"] = enumVals
		}
	}

	return fa, prop, true
}

func describeTypeCounts(counts map[string]int) string {
	parts := make([]string, 0, len(counts))
	for t, c := range counts {
		parts = append(parts, fmt.Sprintf("%s in %d", t, c))
	}
	sort.Strings(parts)
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// inferSchema runs the full analysis over a set of raw JSON blobs
// (each one row's stored data, "id" not yet stripped -- callers pass
// the exact bytes from the nodes table's own data column) and produces
// a SchemaSuggestion.
func inferSchema(entityType string, totalRows int, rawBlobs []string) (*SchemaSuggestion, error) {
	fields := make(map[string]*fieldStats)
	var fieldOrder []string
	sampled := 0

	for _, blob := range rawBlobs {
		var doc map[string]interface{}
		if err := json.Unmarshal([]byte(blob), &doc); err != nil {
			// A single malformed row must not abort inference for
			// every other row -- skip it, don't fail the whole
			// suggestion.
			continue
		}
		sampled++
		for k, v := range doc {
			if k == "id" {
				continue // system field, not part of the schema
			}
			fs, ok := fields[k]
			if !ok {
				fs = newFieldStats()
				fields[k] = fs
				fieldOrder = append(fieldOrder, k)
			}
			fs.observe(v)
		}
	}
	sort.Strings(fieldOrder)

	properties := map[string]interface{}{}
	var required []string
	var analysis []FieldAnalysis

	for _, name := range fieldOrder {
		fa, prop, include := analyzeField(name, fields[name], sampled)
		analysis = append(analysis, fa)
		if include {
			properties[name] = prop
			if fa.Coverage == 1.0 {
				required = append(required, name)
			}
		}
	}
	sort.Strings(required)

	schema := map[string]interface{}{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}

	return &SchemaSuggestion{
		EntityType:      entityType,
		SampledRows:     sampled,
		TotalRows:       totalRows,
		SuggestedSchema: schema,
		FieldAnalysis:   analysis,
	}, nil
}
