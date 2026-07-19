// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_gen_stateless_test.go — generator-logic correctness for the config-bearing
// types, exercised through the named-generator surface (define -> /next).
//
// These tests cover the value-level properties NOT already verified by the
// define/validation tests in v2_gen_named_test.go: random_int range coverage,
// timestamp valid-zone output, nanoid custom-alphabet output, and token
// uniqueness. The earlier bare per-type endpoints (/gen/token etc.) and bare
// OQL scalars (TOKEN(), NANOID(), ...) were retired in favour of the
// @GEN('name') named-definition surface; these tests target that surface.

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// scalarFromOQL extracts the first row's "v" column from an OQL query response
// as a string. Shared by the named-generator and pick/slug OQL dispatch tests.
func scalarFromOQL(t *testing.T, resp map[string]interface{}) string {
	t.Helper()
	data, ok := resp["data"].([]interface{})
	if !ok || len(data) == 0 {
		t.Fatalf("no data rows in OQL response: %v", resp)
	}
	row, _ := data[0].(map[string]interface{})
	switch x := row["v"].(type) {
	case string:
		return x
	case float64:
		return strconv.FormatInt(int64(x), 10)
	default:
		return fmt.Sprintf("%v", row["v"])
	}
}

// token: distinct values across many draws (no collisions).
func TestGenToken_Uniqueness(t *testing.T) {
	env := newV2Server(t)
	if st, r := defineGen(t, env, "token", "tok", map[string]interface{}{"length": 32}); st != http.StatusCreated {
		t.Fatalf("define token: %d %v", st, r)
	}
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		_, n := doJSONRequest(t, "GET", namedGenURL(env, "/token/tok/next"), nil)
		v, _ := n["value"].(string)
		if len(v) != 32 {
			t.Fatalf("token length: want 32, got %d", len(v))
		}
		if seen[v] {
			t.Fatalf("token collision at iteration %d: %q", i, v)
		}
		seen[v] = true
	}
}

// nanoid: a custom alphabet is honoured in the produced value.
func TestGenNanoID_CustomAlphabetHonoured(t *testing.T) {
	env := newV2Server(t)
	defineGen(t, env, "nanoid", "nid", map[string]interface{}{"alphabet": "abc", "length": 40})
	_, n := doJSONRequest(t, "GET", namedGenURL(env, "/nanoid/nid/next"), nil)
	v, _ := n["value"].(string)
	if len(v) != 40 {
		t.Fatalf("nanoid length: want 40, got %d", len(v))
	}
	for _, c := range v {
		if !strings.ContainsRune("abc", c) {
			t.Errorf("nanoid char %q not in custom alphabet 'abc'", c)
		}
	}
}

// random_int: over many draws on a tiny range every endpoint value appears,
// guarding against an off-by-one that excludes a bound.
func TestGenRandomInt_CoversInclusiveRange(t *testing.T) {
	env := newV2Server(t)
	defineGen(t, env, "random_int", "r02", map[string]interface{}{"min": 0, "max": 2})
	seen := map[int64]bool{}
	for i := 0; i < 200; i++ {
		_, n := doJSONRequest(t, "GET", namedGenURL(env, "/random_int/r02/next"), nil)
		v, _ := n["value"].(string)
		x, _ := strconv.ParseInt(v, 10, 64)
		seen[x] = true
	}
	for _, want := range []int64{0, 1, 2} {
		if !seen[want] {
			t.Errorf("random_int [0,2] never produced %d over 200 draws (endpoint excluded?)", want)
		}
	}
}

// timestamp: a valid named zone (from the embedded tz database) produces a
// parseable RFC3339 value.
func TestGenTimestamp_ValidZoneProducesRFC3339(t *testing.T) {
	env := newV2Server(t)
	defineGen(t, env, "timestamp", "ts_tokyo", map[string]interface{}{"zone": "Asia/Tokyo"})
	st, n := doJSONRequest(t, "GET", namedGenURL(env, "/timestamp/ts_tokyo/next"), nil)
	if st != http.StatusOK {
		t.Fatalf("timestamp next: want 200, got %d: %v", st, n)
	}
	v, _ := n["value"].(string)
	if _, err := time.Parse(time.RFC3339, v); err != nil {
		t.Errorf("timestamp value not RFC3339: %q (%v)", v, err)
	}
}
