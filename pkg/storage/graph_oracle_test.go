// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"testing"
)

func TestGraphEdgesOracle_CleanStoreAgrees(t *testing.T) {
	s := refTestStore(t)
	ctx := context.Background()

	uid, err := s.Create(ctx, "users", map[string]interface{}{"name": "ada"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.Create(ctx, "posts", map[string]interface{}{
			"title":     "p",
			"author_id": map[string]interface{}{"type": "REF", "entity": "users", "id": uid},
		}); err != nil {
			t.Fatal(err)
		}
	}

	res, err := s.GraphEdgesOracle().Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Equal {
		t.Fatalf("clean store diverged: %s", res.FirstDivergence)
	}
	if res.Derived == "" {
		t.Fatal("fingerprint empty — oracle exercised nothing")
	}
}

func TestGraphEdgesOracle_DetectsMissingEdge(t *testing.T) {
	s := refTestStore(t)
	ctx := context.Background()

	uid, _ := s.Create(ctx, "users", map[string]interface{}{"name": "ada"})
	if _, err := s.Create(ctx, "posts", map[string]interface{}{
		"title":     "p",
		"author_id": map[string]interface{}{"type": "REF", "entity": "users", "id": uid},
	}); err != nil {
		t.Fatal(err)
	}

	// Drift class 1: derived plane lost a row (edge deleted behind the store).
	if _, err := s.DB().ExecContext(ctx,
		`DELETE FROM t0000_graph WHERE source_entity = 'posts'`); err != nil {
		t.Fatal(err)
	}
	res, err := s.GraphEdgesOracle().Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Equal {
		t.Fatal("oracle missed a deleted edge")
	}
}

func TestGraphEdgesOracle_DetectsPhantomEdge(t *testing.T) {
	s := refTestStore(t)
	ctx := context.Background()

	if _, err := s.Create(ctx, "users", map[string]interface{}{"name": "ada"}); err != nil {
		t.Fatal(err)
	}

	// Drift class 2: derived plane grew a row no document implies.
	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO t0000_graph (source_entity, source_id, target_entity, target_id, relationship_name)
		 VALUES ('posts', 99, 'users', 1, 'author_id')`); err != nil {
		t.Fatal(err)
	}
	res, err := s.GraphEdgesOracle().Check(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Equal {
		t.Fatal("oracle missed a phantom edge")
	}
}
