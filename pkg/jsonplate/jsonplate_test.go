package jsonplate

import (
	"encoding/json"
	"reflect"
	"testing"
)

// renderToMap renders a plate and unmarshals the result for structural compare.
func renderToMap(t *testing.T, plate string, data interface{}) interface{} {
	t.Helper()
	out, err := Render([]byte(plate), data)
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	var got interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("rendered output is not valid JSON: %v (%s)", err, out)
	}
	return got
}

func TestRender_ScalarRef(t *testing.T) {
	data := map[string]interface{}{"current": "InService"}
	got := renderToMap(t, `{"state":{"$ref":"current"}}`, data)
	want := map[string]interface{}{"state": "InService"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRender_NestedPath(t *testing.T) {
	data := map[string]interface{}{
		"vars": map[string]interface{}{"retries": float64(2)},
	}
	got := renderToMap(t, `{"r":{"$ref":"vars.retries"}}`, data)
	want := map[string]interface{}{"r": float64(2)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRender_ArrayIndexPath(t *testing.T) {
	data := map[string]interface{}{
		"affected": []interface{}{
			map[string]interface{}{"ref": map[string]interface{}{"id": float64(123)}},
		},
	}
	got := renderToMap(t, `{"id":{"$ref":"affected[0].ref.id"}}`, data)
	want := map[string]interface{}{"id": float64(123)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRender_RefInsideArray(t *testing.T) {
	data := map[string]interface{}{"a": "x", "b": "y"}
	got := renderToMap(t, `{"items":[{"$ref":"a"},{"$ref":"b"},"literal"]}`, data)
	want := map[string]interface{}{
		"items": []interface{}{"x", "y", "literal"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRender_RefResolvingToStructure(t *testing.T) {
	// A reference whose resolved value is itself an object/array is substituted
	// whole.
	data := map[string]interface{}{
		"affected": []interface{}{
			map[string]interface{}{"entity": "asset", "id": float64(1)},
		},
	}
	got := renderToMap(t, `{"all":{"$ref":"affected"}}`, data)
	want := map[string]interface{}{
		"all": []interface{}{
			map[string]interface{}{"entity": "asset", "id": float64(1)},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRender_MissingPathYieldsNull(t *testing.T) {
	data := map[string]interface{}{"present": "yes"}
	got := renderToMap(t, `{"a":{"$ref":"absent"},"b":{"$ref":"present"}}`, data)
	want := map[string]interface{}{"a": nil, "b": "yes"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRender_LiteralsPreserved(t *testing.T) {
	data := map[string]interface{}{"x": "resolved"}
	got := renderToMap(t,
		`{"lit_str":"hello","lit_num":42,"lit_bool":true,"lit_null":null,"ref":{"$ref":"x"}}`,
		data)
	want := map[string]interface{}{
		"lit_str":  "hello",
		"lit_num":  float64(42),
		"lit_bool": true,
		"lit_null": nil,
		"ref":      "resolved",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRender_DeeplyNestedLiteralStructure(t *testing.T) {
	data := map[string]interface{}{"v": "deep"}
	got := renderToMap(t,
		`{"a":{"b":{"c":[{"d":{"$ref":"v"}}]}}}`,
		data)
	want := map[string]interface{}{
		"a": map[string]interface{}{
			"b": map[string]interface{}{
				"c": []interface{}{
					map[string]interface{}{"d": "deep"},
				},
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRender_InvalidTemplateJSON(t *testing.T) {
	if _, err := Render([]byte(`{not json`), nil); err == nil {
		t.Fatal("expected error for invalid template JSON, got nil")
	}
}

func TestValidate_WellFormed(t *testing.T) {
	plates := []string{
		`{"a":{"$ref":"x"}}`,
		`{"items":[{"$ref":"a"},"lit"]}`,
		`{"plain":"literal","n":1}`,
		`{"nested":{"deep":{"$ref":"a.b.c"}}}`,
	}
	for _, p := range plates {
		if err := Validate([]byte(p)); err != nil {
			t.Errorf("Validate(%s) unexpected error: %v", p, err)
		}
	}
}

func TestValidate_MalformedRef(t *testing.T) {
	bad := []string{
		`{"a":{"$ref":123}}`,                // non-string path
		`{"a":{"$ref":"x","extra":"oops"}}`, // $ref mixed with other keys
	}
	for _, p := range bad {
		if err := Validate([]byte(p)); err == nil {
			t.Errorf("Validate(%s) expected error, got nil", p)
		}
	}
}

func TestValidate_InvalidJSON(t *testing.T) {
	if err := Validate([]byte(`{nope`)); err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}
