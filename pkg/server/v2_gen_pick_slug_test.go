// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server_test

// v2_gen_pick_slug_test.go — S10 stateful generators: pick (random,
// round_robin, weighted-rejected) and slug.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// ─── pick: random mode ────────────────────────────────────────────────────────

func TestPick_RandomStaysInSet(t *testing.T) {
	env := newV2Server(t)
	set := []string{"alice", "bob", "carol", "dave"}
	st, resp := defineGen(t, env, "pick", "oncall",
		map[string]interface{}{"set": set, "mode": "random"})
	if st != http.StatusCreated {
		t.Fatalf("define pick random: %d %v", st, resp)
	}
	allowed := map[string]bool{"alice": true, "bob": true, "carol": true, "dave": true}
	for i := 0; i < 40; i++ {
		_, n := doJSONRequest(t, "GET", namedGenURL(env, "/pick/oncall/next"), nil)
		v, _ := n["value"].(string)
		if !allowed[v] {
			t.Fatalf("random pick returned %q, not in set", v)
		}
	}
}

// ─── pick: round-robin mode (the stateful one) ────────────────────────────────

func TestPick_RoundRobinSequentialAndWraps(t *testing.T) {
	env := newV2Server(t)
	set := []string{"a", "b", "c"}
	if st, r := defineGen(t, env, "pick", "rr",
		map[string]interface{}{"set": set, "mode": "round_robin"}); st != http.StatusCreated {
		t.Fatalf("define pick round_robin: %d %v", st, r)
	}
	// Two full cycles must produce a, b, c, a, b, c in order.
	want := []string{"a", "b", "c", "a", "b", "c"}
	for i, exp := range want {
		_, n := doJSONRequest(t, "GET", namedGenURL(env, "/pick/rr/next"), nil)
		got, _ := n["value"].(string)
		if got != exp {
			t.Errorf("round-robin step %d: want %q, got %q", i, exp, got)
		}
	}
}

func TestPick_RoundRobinIndependentCursors(t *testing.T) {
	// Two named round-robin generators must not share a cursor.
	env := newV2Server(t)
	defineGen(t, env, "pick", "rr1", map[string]interface{}{"set": []string{"x", "y"}, "mode": "round_robin"})
	defineGen(t, env, "pick", "rr2", map[string]interface{}{"set": []string{"p", "q"}, "mode": "round_robin"})

	_, a1 := doJSONRequest(t, "GET", namedGenURL(env, "/pick/rr1/next"), nil)
	_, b1 := doJSONRequest(t, "GET", namedGenURL(env, "/pick/rr2/next"), nil)
	_, a2 := doJSONRequest(t, "GET", namedGenURL(env, "/pick/rr1/next"), nil)
	if a1["value"] != "x" || b1["value"] != "p" || a2["value"] != "y" {
		t.Errorf("cursors not independent: rr1=%v,%v rr2=%v", a1["value"], a2["value"], b1["value"])
	}
}

func TestPick_WeightedRejectedAtDefine(t *testing.T) {
	env := newV2Server(t)
	st, resp := defineGen(t, env, "pick", "weighted_gen",
		map[string]interface{}{"set": []string{"a", "b"}, "mode": "weighted", "weights": []int{3, 1}})
	if st != http.StatusBadRequest || errCode(resp) != "XOLU-GEN004" {
		t.Errorf("weighted mode should be rejected at define (Part 2): want 400/XOLU-GEN004, got %d/%v", st, resp["error"])
	}
}

func TestPick_EmptySetRejected(t *testing.T) {
	env := newV2Server(t)
	st, resp := defineGen(t, env, "pick", "empty", map[string]interface{}{"set": []string{}})
	if st != http.StatusBadRequest || errCode(resp) != "XOLU-GEN004" {
		t.Errorf("empty set should be rejected: want 400/XOLU-GEN004, got %d/%v", st, resp["error"])
	}
}

func TestPick_RoundRobinViaGenOQL(t *testing.T) {
	// @GEN must advance the same cursor as HTTP /next.
	env := newV2Server(t)
	seedAsset(t, env, "g", "active")
	defineGen(t, env, "pick", "oql_rr", map[string]interface{}{"set": []string{"one", "two"}, "mode": "round_robin"})
	oqlURL := fmt.Sprintf("%s/api/v1/tenant/default/oql/query", env.ts.URL)
	q := map[string]interface{}{"query": "SELECT @GEN('oql_rr') AS v FROM assets"}

	_, r1 := doJSONRequest(t, "POST", oqlURL, q)
	_, r2 := doJSONRequest(t, "POST", oqlURL, q)
	v1 := scalarFromOQL(t, r1)
	v2 := scalarFromOQL(t, r2)
	if v1 != "one" || v2 != "two" {
		t.Errorf("@GEN round-robin should advance: want one,two; got %q,%q", v1, v2)
	}
}

// ─── slug ─────────────────────────────────────────────────────────────────────

func TestSlug_AdjectiveNounShape(t *testing.T) {
	env := newV2Server(t)
	if st, r := defineGen(t, env, "slug", "rid", nil); st != http.StatusCreated {
		t.Fatalf("define slug default: %d %v", st, r)
	}
	_, n := doJSONRequest(t, "GET", namedGenURL(env, "/slug/rid/next"), nil)
	v, _ := n["value"].(string)
	parts := strings.Split(v, "-")
	if len(parts) != 2 {
		t.Errorf("default slug should be adjective-noun (2 parts), got %q", v)
	}
}

func TestSlug_AdjAdjNounAndSeparator(t *testing.T) {
	env := newV2Server(t)
	defineGen(t, env, "slug", "rid3", map[string]interface{}{
		"vocabulary": "adjective-adjective-noun", "separator": "_"})
	_, n := doJSONRequest(t, "GET", namedGenURL(env, "/slug/rid3/next"), nil)
	v, _ := n["value"].(string)
	parts := strings.Split(v, "_")
	if len(parts) != 3 {
		t.Errorf("adjective-adjective-noun with '_' separator should be 3 parts, got %q", v)
	}
}

func TestSlug_WordCount(t *testing.T) {
	env := newV2Server(t)
	defineGen(t, env, "slug", "words4", map[string]interface{}{"vocabulary": "word", "words": 4})
	_, n := doJSONRequest(t, "GET", namedGenURL(env, "/slug/words4/next"), nil)
	v, _ := n["value"].(string)
	if parts := strings.Split(v, "-"); len(parts) != 4 {
		t.Errorf("word vocabulary words=4 should yield 4 parts, got %q (%d)", v, len(parts))
	}
}

func TestSlug_BadVocabularyRejected(t *testing.T) {
	env := newV2Server(t)
	st, resp := defineGen(t, env, "slug", "badvocab", map[string]interface{}{"vocabulary": "haiku"})
	if st != http.StatusBadRequest || errCode(resp) != "XOLU-GEN004" {
		t.Errorf("bad vocabulary should be rejected: want 400/XOLU-GEN004, got %d/%v", st, resp["error"])
	}
}

func TestSlug_TooManyWordsRejected(t *testing.T) {
	env := newV2Server(t)
	st, resp := defineGen(t, env, "slug", "toomany", map[string]interface{}{"vocabulary": "word", "words": 99})
	if st != http.StatusBadRequest || errCode(resp) != "XOLU-GEN004" {
		t.Errorf("words=99 should be rejected: want 400/XOLU-GEN004, got %d/%v", st, resp["error"])
	}
}
