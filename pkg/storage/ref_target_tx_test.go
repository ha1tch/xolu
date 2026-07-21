// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"errors"
	"testing"
)

// The G-12 create-side closure: a write whose REF names a missing target
// must be refused INSIDE the write's own transaction (@R02.3 shipped
// early). These are the deterministic semantics; the probabilistic
// interleaving guard is pkg/server's TestRIRestrict_Race on multi-core
// CI, which is what falsified the delete-side-only closure (2026-07-21,
// GitHub runners, 1/8 dangling).

func refTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, cleanup := newRestrictTestStore(t) // sibling helper: graph-enabled temp store
	t.Cleanup(cleanup)
	return s
}

func TestCreate_RefTargetMissing_Refused(t *testing.T) {
	s := refTestStore(t)
	ctx := context.Background()

	_, err := s.Create(ctx, "posts", map[string]interface{}{
		"title":     "orphan",
		"author_id": map[string]interface{}{"type": "REF", "entity": "users", "id": 999},
	})
	var rtm *RefTargetMissingError
	if !errors.As(err, &rtm) {
		t.Fatalf("create with missing target: got %v, want RefTargetMissingError", err)
	}
	if rtm.TargetEntity != "users" || rtm.TargetID != 999 {
		t.Fatalf("error names wrong target: %+v", rtm)
	}
	// The refused create must leave nothing behind: no row, no edges.
	if s.Exists(ctx, "posts", 1) {
		t.Fatal("refused create left a row behind")
	}
}

func TestCreate_RefTargetPresent_Proceeds(t *testing.T) {
	s := refTestStore(t)
	ctx := context.Background()

	uid, err := s.Create(ctx, "users", map[string]interface{}{"name": "ada"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, "posts", map[string]interface{}{
		"title":     "ok",
		"author_id": map[string]interface{}{"type": "REF", "entity": "users", "id": uid},
	}); err != nil {
		t.Fatalf("create with live target refused: %v", err)
	}
}

func TestUpdate_RefTargetMissing_Refused(t *testing.T) {
	s := refTestStore(t)
	ctx := context.Background()

	uid, _ := s.Create(ctx, "users", map[string]interface{}{"name": "ada"})
	pid, err := s.Create(ctx, "posts", map[string]interface{}{
		"title":     "ok",
		"author_id": map[string]interface{}{"type": "REF", "entity": "users", "id": uid},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Redirect the REF at a missing user: refused, original intact.
	err = s.Update(ctx, "posts", pid, map[string]interface{}{
		"title":     "still ok",
		"author_id": map[string]interface{}{"type": "REF", "entity": "users", "id": 999},
	})
	var rtm *RefTargetMissingError
	if !errors.As(err, &rtm) {
		t.Fatalf("update to missing target: got %v, want RefTargetMissingError", err)
	}
	got, err := s.Get(ctx, "posts", pid)
	if err != nil {
		t.Fatal(err)
	}
	if got["title"] != "ok" {
		t.Fatalf("refused update mutated the row: %v", got["title"])
	}
}

func TestCreate_SelfReference_Allowed(t *testing.T) {
	// An entity referencing itself in the same create: the row is
	// inserted before syncGraphEdges runs, so the in-tx SELECT sees it.
	s := refTestStore(t)
	ctx := context.Background()

	// First create to learn the next id deterministically.
	id1, err := s.Create(ctx, "nodes", map[string]interface{}{"name": "root"})
	if err != nil {
		t.Fatal(err)
	}
	id2Expected := id1 + 1
	if _, err := s.Create(ctx, "nodes", map[string]interface{}{
		"name":   "selfie",
		"parent": map[string]interface{}{"type": "REF", "entity": "nodes", "id": id2Expected},
	}); err != nil {
		t.Fatalf("self-reference in same create refused: %v", err)
	}
}
