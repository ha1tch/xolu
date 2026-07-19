// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_gen_named_test.go — S10 named stateful generators: define, /next,
// @GEN('name') OQL dispatch, and define-time validation.

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func namedGenURL(env *stdTestServer, path string) string {
	return fmt.Sprintf("%s/api/v2/tenant/default/gen%s", env.ts.URL, path)
}

// defineGen is a helper: POST /gen/{type} with a name and config.
func defineGen(t *testing.T, env *stdTestServer, gtype, name string, config map[string]interface{}) (int, map[string]interface{}) {
	t.Helper()
	body := map[string]interface{}{"name": name}
	if config != nil {
		body["config"] = config
	}
	return doJSONRequest(t, "POST", namedGenURL(env, "/"+gtype), body)
}

// ─── define + next round-trip ─────────────────────────────────────────────────

func TestNamedGen_DefineAndNextToken(t *testing.T) {
	env := newV2Server(t)
	st, resp := defineGen(t, env, "token", "api_key", map[string]interface{}{"length": 48})
	if st != http.StatusCreated {
		t.Fatalf("define token api_key: want 201, got %d: %v", st, resp)
	}
	st, nResp := doJSONRequest(t, "GET", namedGenURL(env, "/token/api_key/next"), nil)
	if st != http.StatusOK {
		t.Fatalf("token next: want 200, got %d: %v", st, nResp)
	}
	val, _ := nResp["value"].(string)
	if len(val) != 48 {
		t.Errorf("api_key configured length 48, got %d (%q)", len(val), val)
	}
}

func TestNamedGen_DefineAndNextRandomInt(t *testing.T) {
	env := newV2Server(t)
	st, resp := defineGen(t, env, "random_int", "dice", map[string]interface{}{"min": 1, "max": 6})
	if st != http.StatusCreated {
		t.Fatalf("define random_int dice: want 201, got %d: %v", st, resp)
	}
	for i := 0; i < 50; i++ {
		_, nResp := doJSONRequest(t, "GET", namedGenURL(env, "/random_int/dice/next"), nil)
		v, _ := nResp["value"].(string)
		n, _ := strconv.ParseInt(v, 10, 64)
		if n < 1 || n > 6 {
			t.Fatalf("dice out of [1,6]: %d", n)
		}
	}
}

// ─── @GEN('name') OQL dispatch — the real integration ─────────────────────────

func TestNamedGen_GenOQLDispatch(t *testing.T) {
	env := newV2Server(t)
	seedAsset(t, env, "g", "active")
	// Define a token generator with a distinctive length so we can detect it.
	if st, r := defineGen(t, env, "token", "session_tok", map[string]interface{}{"length": 17}); st != http.StatusCreated {
		t.Fatalf("define session_tok: %d %v", st, r)
	}
	oqlURL := fmt.Sprintf("%s/api/v1/tenant/default/oql/query", env.ts.URL)
	st, resp := doJSONRequest(t, "POST", oqlURL,
		map[string]interface{}{"query": "SELECT @GEN('session_tok') AS v FROM assets"})
	if st != http.StatusOK {
		t.Fatalf("@GEN OQL: want 200, got %d: %v", st, resp)
	}
	v := scalarFromOQL(t, resp)
	if len(v) != 17 {
		t.Errorf("@GEN('session_tok') should honour configured length 17, got %d (%q)", len(v), v)
	}
}

func TestNamedGen_GenOQLUnknownNameIsNull(t *testing.T) {
	// @GEN of an undefined name resolves to NULL (consistent with @SEQ), not a
	// query failure.
	env := newV2Server(t)
	seedAsset(t, env, "g", "active")
	oqlURL := fmt.Sprintf("%s/api/v1/tenant/default/oql/query", env.ts.URL)
	st, resp := doJSONRequest(t, "POST", oqlURL,
		map[string]interface{}{"query": "SELECT @GEN('does_not_exist') AS v FROM assets"})
	if st != http.StatusOK {
		t.Fatalf("@GEN unknown name should still 200 with NULL, got %d: %v", st, resp)
	}
	data, _ := resp["data"].([]interface{})
	if len(data) == 0 {
		t.Fatal("expected a row")
	}
	row, _ := data[0].(map[string]interface{})
	if row["v"] != nil {
		t.Errorf("@GEN('does_not_exist') should be NULL, got %v", row["v"])
	}
}

// ─── define-time validation ───────────────────────────────────────────────────

func TestNamedGen_RejectsBadConfigAtDefine(t *testing.T) {
	env := newV2Server(t)
	cases := []struct {
		gtype  string
		config map[string]interface{}
		reason string
	}{
		{"token", map[string]interface{}{"length": 99999}, "token length out of range"},
		{"nanoid", map[string]interface{}{"alphabet": "x"}, "single-char alphabet"},
		{"random_int", map[string]interface{}{"min": 10, "max": 5}, "max < min"},
		{"random_int", map[string]interface{}{"min": 1}, "missing max"},
		{"timestamp", map[string]interface{}{"zone": "Not/AZone"}, "bad timezone"},
	}
	for _, c := range cases {
		st, resp := defineGen(t, env, c.gtype, "bad_"+strings.ReplaceAll(c.reason, " ", "_"), c.config)
		if st != http.StatusBadRequest || errCode(resp) != "XOLU-GEN004" {
			t.Errorf("%s (%s): want 400/XOLU-GEN004, got %d/%v", c.gtype, c.reason, st, resp["error"])
		}
	}
}

func TestNamedGen_RejectsUnknownConfigField(t *testing.T) {
	// A misspelled config key must fail the definition, not be silently dropped.
	env := newV2Server(t)
	st, resp := defineGen(t, env, "token", "typo", map[string]interface{}{"lenght": 32})
	if st != http.StatusBadRequest || errCode(resp) != "XOLU-GEN004" {
		t.Errorf("unknown config field: want 400/XOLU-GEN004, got %d/%v", st, resp["error"])
	}
}

// ─── collisions, not-found, seq-shadow guard, delete ──────────────────────────

func TestNamedGen_NameCollision(t *testing.T) {
	env := newV2Server(t)
	if st, _ := defineGen(t, env, "token", "dup", nil); st != http.StatusCreated {
		t.Fatal("first define should succeed")
	}
	st, resp := defineGen(t, env, "nanoid", "dup", nil) // same name, different type
	if st != http.StatusUnprocessableEntity || errCode(resp) != "XOLU-GEN002" {
		t.Errorf("name collision: want 422/XOLU-GEN002, got %d/%v", st, resp["error"])
	}
}

func TestNamedGen_NextNotFound(t *testing.T) {
	env := newV2Server(t)
	st, resp := doJSONRequest(t, "GET", namedGenURL(env, "/token/ghost/next"), nil)
	if st != http.StatusNotFound || errCode(resp) != "XOLU-GEN003" {
		t.Errorf("missing generator next: want 404/XOLU-GEN003, got %d/%v", st, resp["error"])
	}
}

func TestNamedGen_SequenceTypeRejected(t *testing.T) {
	// /gen/sequence and /gen/seq must not be addressable via the generic route.
	env := newV2Server(t)
	for _, typ := range []string{"sequence", "seq"} {
		st, resp := doJSONRequest(t, "POST", namedGenURL(env, "/"+typ),
			map[string]interface{}{"name": "x"})
		// 'seq' is a static route (define handler expects different body); both
		// must NOT create a generic generator of sequence type. We assert the
		// generic handler refuses 'sequence' explicitly.
		if typ == "sequence" && st != http.StatusNotFound {
			t.Errorf("/gen/sequence should be rejected (404), got %d: %v", st, resp)
		}
	}
}

func TestNamedGen_UnknownTypeRejected(t *testing.T) {
	env := newV2Server(t)
	st, resp := defineGen(t, env, "wormhole", "x", nil)
	if st != http.StatusBadRequest || errCode(resp) != "XOLU-GEN004" {
		t.Errorf("unknown type: want 400/XOLU-GEN004, got %d/%v", st, resp["error"])
	}
}

func TestNamedGen_Delete(t *testing.T) {
	env := newV2Server(t)
	defineGen(t, env, "nanoid", "tmp", nil)
	st, _ := doJSONRequest(t, "DELETE", namedGenURL(env, "/nanoid/tmp"), nil)
	if st != http.StatusOK {
		t.Fatalf("delete: want 200, got %d", st)
	}
	// Now /next must 404.
	st, _ = doJSONRequest(t, "GET", namedGenURL(env, "/nanoid/tmp/next"), nil)
	if st != http.StatusNotFound {
		t.Errorf("deleted generator next should 404, got %d", st)
	}
}

func TestNamedGen_ListAndGet(t *testing.T) {
	env := newV2Server(t)
	defineGen(t, env, "token", "a", nil)
	defineGen(t, env, "token", "b", map[string]interface{}{"length": 10})

	_, listResp := doJSONRequest(t, "GET", namedGenURL(env, "/token"), nil)
	gens, _ := listResp["generators"].([]interface{})
	if len(gens) != 2 {
		t.Errorf("list token: want 2 generators, got %d", len(gens))
	}

	st, getResp := doJSONRequest(t, "GET", namedGenURL(env, "/token/b"), nil)
	if st != http.StatusOK {
		t.Fatalf("get token/b: %d", st)
	}
	cfg, _ := getResp["config"].(map[string]interface{})
	if cfg["length"].(float64) != 10 {
		t.Errorf("token/b config length should be 10, got %v", cfg["length"])
	}
}
