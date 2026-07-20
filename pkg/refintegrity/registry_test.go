// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package refintegrity

import "testing"

func xref(entity string, policy OnDelete) map[string]interface{} {
	return map[string]interface{}{"entity": entity, "on_delete": string(policy)}
}

func TestCollectXRefs_SortedAndParsed(t *testing.T) {
	fields := []FieldXRefMeta{
		{Field: "editor_id", Raw: xref("users", OnDeleteNullify)},
		{Field: "author_id", Raw: xref("users", OnDeleteRestrict)},
		{Field: "title", Raw: nil}, // no annotation → skipped
	}
	got, err := CollectXRefs(fields)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d xrefs, want 2", len(got))
	}
	// Sorted by field name: author_id before editor_id.
	if got[0].Field != "author_id" || got[1].Field != "editor_id" {
		t.Errorf("not sorted by field: %s, %s", got[0].Field, got[1].Field)
	}
	if got[0].OnDelete != OnDeleteRestrict || got[1].OnDelete != OnDeleteNullify {
		t.Errorf("policies wrong: %s, %s", got[0].OnDelete, got[1].OnDelete)
	}
}

func TestCollectXRefs_MalformedAborts(t *testing.T) {
	fields := []FieldXRefMeta{
		{Field: "good", Raw: xref("users", OnDeleteRestrict)},
		{Field: "bad", Raw: map[string]interface{}{"on_delete": "restrict"}}, // no entity
	}
	if _, err := CollectXRefs(fields); err == nil {
		t.Error("a malformed x-ref must abort the whole collection, not be skipped")
	}
}

func TestRegistry_ReferrersOf(t *testing.T) {
	reg := NewRegistry()
	// posts.author_id → users (restrict); comments.user_id → users (cascade);
	// posts.category_id → categories (restrict).
	if err := reg.AddEntitySchema("posts", []FieldXRefMeta{
		{Field: "author_id", Raw: xref("users", OnDeleteRestrict)},
		{Field: "category_id", Raw: xref("categories", OnDeleteRestrict)},
	}); err != nil {
		t.Fatal(err)
	}
	if err := reg.AddEntitySchema("comments", []FieldXRefMeta{
		{Field: "user_id", Raw: xref("users", OnDeleteCascade)},
	}); err != nil {
		t.Fatal(err)
	}

	users := reg.ReferrersOf("users")
	if len(users) != 2 {
		t.Fatalf("users has %d referrers, want 2", len(users))
	}
	// Both posts and comments reference users.
	seen := map[string]OnDelete{}
	for _, p := range users {
		seen[p.ReferringEntity] = p.OnDelete
	}
	if seen["posts"] != OnDeleteRestrict {
		t.Errorf("posts→users should be restrict, got %q", seen["posts"])
	}
	if seen["comments"] != OnDeleteCascade {
		t.Errorf("comments→users should be cascade, got %q", seen["comments"])
	}

	if got := reg.ReferrersOf("categories"); len(got) != 1 {
		t.Errorf("categories has %d referrers, want 1", len(got))
	}
	if got := reg.ReferrersOf("nonexistent"); got != nil {
		t.Errorf("unreferenced entity should return nil, got %v", got)
	}
}

func TestRegistry_HasRestrictReferrers(t *testing.T) {
	reg := NewRegistry()
	_ = reg.AddEntitySchema("posts", []FieldXRefMeta{
		{Field: "author_id", Raw: xref("users", OnDeleteRestrict)},
	})
	_ = reg.AddEntitySchema("logs", []FieldXRefMeta{
		{Field: "actor_id", Raw: xref("accounts", OnDeleteCascade)},
	})

	if !reg.HasRestrictReferrers("users") {
		t.Error("users has a restrict referrer (posts.author_id)")
	}
	if reg.HasRestrictReferrers("accounts") {
		t.Error("accounts has only a cascade referrer, not restrict")
	}
	if reg.HasRestrictReferrers("nothing") {
		t.Error("unreferenced entity has no restrict referrers")
	}
}

func TestRegistry_NilSafe(t *testing.T) {
	var reg *Registry
	if reg.ReferrersOf("x") != nil {
		t.Error("nil registry ReferrersOf should be nil")
	}
	if reg.HasRestrictReferrers("x") {
		t.Error("nil registry HasRestrictReferrers should be false")
	}
}
