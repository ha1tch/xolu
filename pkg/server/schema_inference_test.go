// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

import (
	"strconv"
	"testing"
)

func fieldProp(t *testing.T, s *SchemaSuggestion, name string) map[string]interface{} {
	t.Helper()
	props, ok := s.SuggestedSchema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("properties missing or wrong type: %v", s.SuggestedSchema["properties"])
	}
	prop, ok := props[name].(map[string]interface{})
	if !ok {
		return nil
	}
	return prop
}

func fieldAnalysis(s *SchemaSuggestion, name string) *FieldAnalysis {
	for i := range s.FieldAnalysis {
		if s.FieldAnalysis[i].Field == name {
			return &s.FieldAnalysis[i]
		}
	}
	return nil
}

func requiredList(s *SchemaSuggestion) []string {
	req, _ := s.SuggestedSchema["required"].([]string)
	return req
}

func TestInferSchema_ConsistentStringField(t *testing.T) {
	blobs := []string{
		`{"id":1,"name":"alpha"}`,
		`{"id":2,"name":"beta"}`,
		`{"id":3,"name":"gamma"}`,
	}
	s, err := inferSchema("widgets", 3, blobs)
	if err != nil {
		t.Fatalf("inferSchema: %v", err)
	}
	prop := fieldProp(t, s, "name")
	if prop == nil {
		t.Fatal("name field missing from suggested schema")
	}
	if prop["type"] != "string" {
		t.Errorf("name type: got %v", prop["type"])
	}
	fa := fieldAnalysis(s, "name")
	if fa == nil || fa.Confidence != "high" || fa.Coverage != 1.0 {
		t.Errorf("name analysis: got %+v", fa)
	}
	found := false
	for _, r := range requiredList(s) {
		if r == "name" {
			found = true
		}
	}
	if !found {
		t.Error("name should be required (100% coverage)")
	}
}

func TestInferSchema_OptionalField(t *testing.T) {
	blobs := []string{
		`{"id":1,"name":"a","nickname":"x"}`,
		`{"id":2,"name":"b"}`,
		`{"id":3,"name":"c"}`,
		`{"id":4,"name":"d"}`,
	}
	s, err := inferSchema("widgets", 4, blobs)
	if err != nil {
		t.Fatalf("inferSchema: %v", err)
	}
	fa := fieldAnalysis(s, "nickname")
	if fa == nil {
		t.Fatal("nickname missing from analysis")
	}
	if fa.Coverage != 0.25 {
		t.Errorf("nickname coverage: got %v, want 0.25", fa.Coverage)
	}
	for _, r := range requiredList(s) {
		if r == "nickname" {
			t.Error("nickname should NOT be required (25% coverage)")
		}
	}
	if fieldProp(t, s, "nickname") == nil {
		t.Error("nickname should still appear in properties despite being optional")
	}
}

func TestInferSchema_MixedTypesExcluded(t *testing.T) {
	blobs := []string{
		`{"id":1,"legacy_id":"abc"}`,
		`{"id":2,"legacy_id":"def"}`,
		`{"id":3,"legacy_id":123}`,
	}
	s, err := inferSchema("widgets", 3, blobs)
	if err != nil {
		t.Fatalf("inferSchema: %v", err)
	}
	if fieldProp(t, s, "legacy_id") != nil {
		t.Error("legacy_id has inconsistent types (string+number) and must be excluded from the suggested schema")
	}
	fa := fieldAnalysis(s, "legacy_id")
	if fa == nil {
		t.Fatal("legacy_id should still appear in FieldAnalysis even though excluded from the schema")
	}
	if fa.Confidence != "low" {
		t.Errorf("legacy_id confidence: got %q, want low", fa.Confidence)
	}
	if fa.Note == "" {
		t.Error("legacy_id should carry a note explaining the exclusion")
	}
}

func TestInferSchema_RefFieldDetected(t *testing.T) {
	blobs := []string{
		`{"id":1,"owner":{"type":"REF","entity":"users","id":1}}`,
		`{"id":2,"owner":{"type":"REF","entity":"users","id":2}}`,
		`{"id":3,"owner":{"type":"REF","entity":"users","id":1}}`,
	}
	s, err := inferSchema("widgets", 3, blobs)
	if err != nil {
		t.Fatalf("inferSchema: %v", err)
	}
	prop := fieldProp(t, s, "owner")
	if prop == nil {
		t.Fatal("owner missing from suggested schema")
	}
	if prop["type"] != "object" || prop["format"] != "ref" {
		t.Errorf("owner prop: got %v, want type=object format=ref", prop)
	}
	fa := fieldAnalysis(s, "owner")
	if fa == nil || fa.InferredType != "ref" || fa.Confidence != "high" {
		t.Errorf("owner analysis: got %+v", fa)
	}
}

func TestInferSchema_NonRefObjectNotMisdetected(t *testing.T) {
	blobs := []string{
		`{"id":1,"address":{"type":"home","city":"Austin"}}`,
		`{"id":2,"address":{"type":"work","city":"Denver"}}`,
	}
	s, err := inferSchema("widgets", 2, blobs)
	if err != nil {
		t.Fatalf("inferSchema: %v", err)
	}
	fa := fieldAnalysis(s, "address")
	if fa == nil {
		t.Fatal("address missing from analysis")
	}
	if fa.InferredType == "ref" {
		t.Error("address should not be misdetected as a ref field -- it lacks the 'entity' and numeric 'id' keys REF requires")
	}
}

func TestInferSchema_DecimalPatternDetected(t *testing.T) {
	blobs := []string{
		`{"id":1,"price":"12.50"}`,
		`{"id":2,"price":"9.99"}`,
		`{"id":3,"price":"100.00"}`,
	}
	s, err := inferSchema("products", 3, blobs)
	if err != nil {
		t.Fatalf("inferSchema: %v", err)
	}
	prop := fieldProp(t, s, "price")
	if prop == nil {
		t.Fatal("price missing from suggested schema")
	}
	if prop["format"] != "decimal" {
		t.Errorf("price format: got %v, want decimal", prop["format"])
	}
	fa := fieldAnalysis(s, "price")
	if fa == nil || fa.Confidence != "medium" {
		t.Errorf("price confidence should be medium (pattern-based, not guaranteed): got %+v", fa)
	}
	if fa.Note == "" {
		t.Error("price should carry a note about the pattern-match caveat")
	}
}

func TestInferSchema_NonDecimalStringNotFlagged(t *testing.T) {
	blobs := []string{
		`{"id":1,"name":"widget-a"}`,
		`{"id":2,"name":"widget-b"}`,
	}
	s, err := inferSchema("widgets", 2, blobs)
	if err != nil {
		t.Fatalf("inferSchema: %v", err)
	}
	prop := fieldProp(t, s, "name")
	if prop["format"] == "decimal" {
		t.Error("plain string field should not be flagged as decimal")
	}
}

func TestInferSchema_EnumDetected(t *testing.T) {
	blobs := make([]string, 0, 30)
	statuses := []string{"open", "closed", "pending"}
	for i := 0; i < 30; i++ {
		blobs = append(blobs, `{"id":`+strconv.Itoa(i)+`,"status":"`+statuses[i%3]+`"}`)
	}
	s, err := inferSchema("tasks", 30, blobs)
	if err != nil {
		t.Fatalf("inferSchema: %v", err)
	}
	fa := fieldAnalysis(s, "status")
	if fa == nil {
		t.Fatal("status missing from analysis")
	}
	if len(fa.SuggestedEnum) != 3 {
		t.Fatalf("SuggestedEnum: got %v, want 3 values", fa.SuggestedEnum)
	}
	prop := fieldProp(t, s, "status")
	if prop["enum"] == nil {
		t.Error("status property should carry an enum")
	}
}

func TestInferSchema_HighCardinalityNotFlaggedAsEnum(t *testing.T) {
	blobs := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		blobs = append(blobs, `{"id":`+strconv.Itoa(i)+`,"email":"user`+strconv.Itoa(i)+`@example.com"}`)
	}
	s, err := inferSchema("users", 20, blobs)
	if err != nil {
		t.Fatalf("inferSchema: %v", err)
	}
	fa := fieldAnalysis(s, "email")
	if fa == nil {
		t.Fatal("email missing from analysis")
	}
	if len(fa.SuggestedEnum) > 0 {
		t.Errorf("email has 20 distinct values across 20 rows -- must not be suggested as an enum, got %v", fa.SuggestedEnum)
	}
}

func TestInferSchema_SparseEnumNotFlagged(t *testing.T) {
	blobs := []string{
		`{"id":1,"rare_field":"a"}`,
		`{"id":2,"rare_field":"b"}`,
	}
	s, err := inferSchema("widgets", 2, blobs)
	if err != nil {
		t.Fatalf("inferSchema: %v", err)
	}
	fa := fieldAnalysis(s, "rare_field")
	if fa != nil && len(fa.SuggestedEnum) > 0 {
		t.Errorf("2 observations is too few to confidently suggest an enum, got %v", fa.SuggestedEnum)
	}
}

func TestInferSchema_MalformedRowSkippedNotFatal(t *testing.T) {
	blobs := []string{
		`{"id":1,"name":"a"}`,
		`not valid json at all`,
		`{"id":3,"name":"c"}`,
	}
	s, err := inferSchema("widgets", 3, blobs)
	if err != nil {
		t.Fatalf("inferSchema should not fail outright on one malformed row: %v", err)
	}
	if s.SampledRows != 2 {
		t.Errorf("SampledRows: got %d, want 2 (the malformed row should be skipped, not counted)", s.SampledRows)
	}
	prop := fieldProp(t, s, "name")
	if prop == nil {
		t.Error("name should still be inferred from the 2 valid rows")
	}
}

func TestInferSchema_IDFieldExcluded(t *testing.T) {
	blobs := []string{`{"id":1,"name":"a"}`, `{"id":2,"name":"b"}`}
	s, err := inferSchema("widgets", 2, blobs)
	if err != nil {
		t.Fatalf("inferSchema: %v", err)
	}
	if fieldProp(t, s, "id") != nil {
		t.Error("'id' is a system field and must not appear in the suggested schema")
	}
}

func TestInferSchema_EmptyInput(t *testing.T) {
	s, err := inferSchema("widgets", 0, nil)
	if err != nil {
		t.Fatalf("inferSchema on empty input: %v", err)
	}
	if s.SampledRows != 0 {
		t.Errorf("SampledRows: got %d, want 0", s.SampledRows)
	}
	props, _ := s.SuggestedSchema["properties"].(map[string]interface{})
	if len(props) != 0 {
		t.Errorf("expected no properties for empty input, got %v", props)
	}
}

func TestInferSchema_AdditionalPropertiesLeftPermissive(t *testing.T) {
	blobs := []string{`{"id":1,"name":"a"}`}
	s, err := inferSchema("widgets", 1, blobs)
	if err != nil {
		t.Fatalf("inferSchema: %v", err)
	}
	if _, set := s.SuggestedSchema["additionalProperties"]; set {
		t.Error("additionalProperties should be left unset (permissive) by default -- inference runs on a sample, not necessarily all data")
	}
}
