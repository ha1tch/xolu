// Package jsonplate renders structured JSON payload templates ("jsonplates")
// by resolving embedded path references against a data context.
//
// A jsonplate is ordinary JSON in which any object of the form
//
//	{"$ref": "path.into.data"}
//
// is a reference: at render time it is replaced by the value found at that path
// in the supplied data context. Every other value is a literal and is copied
// through unchanged. Path resolution is delegated to queryfy's query language
// (field access, nested access, array indexing: "a.b", "a.b[0].c"), so this
// package owns only the structural substitution walk — queryfy does the
// traversal.
//
// Validation happens at definition time (Validate), resolution at render time
// (Render). A reference whose path is absent from the data resolves to JSON
// null rather than erroring, so a partially-populated context degrades
// gracefully instead of failing a whole render.
//
// Example:
//
//	plate := []byte(`{"asset":{"$ref":"current"},"out":{"$ref":"affected[0].ref.id"}}`)
//	data  := map[string]interface{}{
//	    "current":  "InService",
//	    "affected": []interface{}{map[string]interface{}{"ref": map[string]interface{}{"id": 123}}},
//	}
//	out, _ := jsonplate.Render(plate, data)
//	// out == {"asset":"InService","out":123}
package jsonplate

import (
	"encoding/json"
	"fmt"

	"github.com/ha1tch/queryfy"
)

// refKey is the object key that marks an object as a reference rather than a
// literal. An object is treated as a reference if and only if it has exactly
// one key, refKey, whose value is a string.
const refKey = "$ref"

// Render resolves a jsonplate against the given data context and returns the
// rendered JSON. plate must be a valid JSON document. data is the context that
// reference paths are resolved against (typically an event's data map).
//
// A reference to a path that does not exist in data resolves to JSON null.
func Render(plate []byte, data interface{}) ([]byte, error) {
	var root interface{}
	if err := json.Unmarshal(plate, &root); err != nil {
		return nil, fmt.Errorf("jsonplate: invalid template JSON: %w", err)
	}
	resolved := resolve(root, data)
	out, err := json.Marshal(resolved)
	if err != nil {
		return nil, fmt.Errorf("jsonplate: marshal rendered output: %w", err)
	}
	return out, nil
}

// Validate checks that plate is well-formed: valid JSON, and every reference
// object ({"$ref": ...}) carries a string path. It does not check that the
// referenced paths exist (that is only knowable at render time against real
// data); it checks the template's own structure. Intended to be called when an
// event def is created, so a malformed jsonplate is rejected up front rather
// than at firing time.
func Validate(plate []byte) error {
	var root interface{}
	if err := json.Unmarshal(plate, &root); err != nil {
		return fmt.Errorf("jsonplate: invalid template JSON: %w", err)
	}
	return validate(root)
}

// asRef reports whether v is a reference object and, if so, returns its path.
// A reference object is a map with exactly one entry whose key is refKey and
// whose value is a string.
func asRef(v interface{}) (string, bool) {
	m, ok := v.(map[string]interface{})
	if !ok || len(m) != 1 {
		return "", false
	}
	raw, ok := m[refKey]
	if !ok {
		return "", false
	}
	path, ok := raw.(string)
	if !ok {
		return "", false
	}
	return path, true
}

// resolve walks the template structure, replacing reference objects with the
// value at their path in data and copying every other value through. A path
// that does not resolve yields nil (JSON null).
func resolve(node, data interface{}) interface{} {
	if path, ok := asRef(node); ok {
		val, err := queryfy.Query(data, path)
		if err != nil {
			// Absent or unresolvable path degrades to null rather than failing
			// the whole render.
			return nil
		}
		return val
	}

	switch n := node.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(n))
		for k, v := range n {
			out[k] = resolve(v, data)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(n))
		for i, v := range n {
			out[i] = resolve(v, data)
		}
		return out
	default:
		// Scalar literal: copied through unchanged.
		return n
	}
}

// validate walks the template structure and checks every reference object is
// well-formed. A map with a "$ref" key must have exactly one key and a string
// value; a map that happens to contain "$ref" among other keys is a malformed
// reference and is rejected (to avoid silently treating it as a literal).
func validate(node interface{}) error {
	switch n := node.(type) {
	case map[string]interface{}:
		if _, hasRef := n[refKey]; hasRef {
			if _, ok := asRef(n); !ok {
				return fmt.Errorf("jsonplate: malformed reference: %q object must have exactly one key with a string value", refKey)
			}
			return nil
		}
		for _, v := range n {
			if err := validate(v); err != nil {
				return err
			}
		}
		return nil
	case []interface{}:
		for _, v := range n {
			if err := validate(v); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}
