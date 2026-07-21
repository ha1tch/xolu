// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"reflect"
	"testing"

	"github.com/ha1tch/xolu/pkg/models"
)

// These tests pin the REF compose/decompose invariants (hardened
// 2026-07-21). The canonical pair is models.IsReference (decompose) and
// models.NewReference/ToMap (compose); PartitionData and ReassembleData
// must route through them, and must agree with ExtractEntityEdges about
// what counts as a REF. Drift between the column pipeline and the edge
// pipeline is exactly the class of bug that made the RI restrict check
// unreliable for adapted entities.

func refSpecForTest(t *testing.T) *AdaptedTableSpec {
	t.Helper()
	schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{"type": "string"},
			"author": map[string]interface{}{
				"type": "object", "format": "ref",
			},
			"editor": map[string]interface{}{
				"type": "object", "format": "ref",
			},
		},
	}
	spec, err := DeriveAdaptedTableSpec("book", schema, &SQLiteStorageDialect{}, 0)
	if err != nil {
		t.Fatalf("DeriveAdaptedTableSpec: %v", err)
	}
	return spec
}

// TestREFRoundTrip_Identity: PartitionData ∘ ReassembleData is identity on
// well-formed REF fields.
func TestREFRoundTrip_Identity(t *testing.T) {
	spec := refSpecForTest(t)
	data := map[string]interface{}{
		"name":   "dune",
		"author": models.NewReference("person", 42).ToMap(),
		"editor": models.NewReference("person", 7).ToMap(),
	}

	cols, extra := PartitionData(spec, data)
	out := ReassembleData(spec, cols, extra, 1, 1)

	for _, field := range []string{"author", "editor"} {
		got, gok := models.IsReference(out[field])
		want, wok := models.IsReference(data[field])
		if !gok || !wok {
			t.Fatalf("%s: round-trip lost REF shape (in ok=%v, out ok=%v)", field, wok, gok)
		}
		if got.Entity != want.Entity || got.ID != want.ID {
			t.Errorf("%s: round-trip changed ref: got %s:%d want %s:%d",
				field, got.Entity, got.ID, want.Entity, want.ID)
		}
	}
}

// TestREFDecompose_RejectsMalformed: a value that models.IsReference
// rejects must decompose to nil halves — never to half a reference. Before
// hardening, {"entity":"x","id":1} without "type":"REF" decomposed its
// entity half while ExtractEntityEdges ignored it: the two pipelines
// disagreed. Both must now reject it.
func TestREFDecompose_RejectsMalformed(t *testing.T) {
	spec := refSpecForTest(t)
	malformed := map[string]interface{}{
		"name":   "x",
		"author": map[string]interface{}{"entity": "person", "id": 1}, // no "type":"REF"
	}

	// Edge pipeline: no edges.
	edges, err := models.ExtractEntityEdges(malformed)
	if err != nil {
		t.Fatalf("ExtractEntityEdges: %v", err)
	}
	if len(edges) != 0 {
		t.Fatalf("edge pipeline accepted malformed ref: %+v", edges)
	}

	// Column pipeline: all REF columns nil.
	cols, _ := PartitionData(spec, malformed)
	for i, col := range spec.Columns {
		if col.IsREF && col.JSONField == "author" && cols[i] != nil {
			t.Errorf("column pipeline decomposed malformed ref: column %s = %v", col.Name, cols[i])
		}
	}
}

// TestREFPipelines_Agree: for the same data, the set of (entity, id)
// targets the edge pipeline extracts equals the set the column pipeline
// decomposes. This is the agreement invariant the RI referrer check
// depends on: a referrer visible to one pipeline but not the other is a
// silent enforcement hole.
func TestREFPipelines_Agree(t *testing.T) {
	spec := refSpecForTest(t)
	cases := []map[string]interface{}{
		{ // both refs present
			"name":   "a",
			"author": models.NewReference("person", 3).ToMap(),
			"editor": models.NewReference("person", 9).ToMap(),
		},
		{ // one ref, one absent
			"name":   "b",
			"author": models.NewReference("org", 5).ToMap(),
		},
		{ // one well-formed, one malformed — both pipelines must keep
			// exactly the well-formed one
			"name":   "c",
			"author": models.NewReference("person", 11).ToMap(),
			"editor": map[string]interface{}{"entity": "person"}, // malformed
		},
		{ // no refs at all
			"name": "d",
		},
	}

	for ci, data := range cases {
		edges, err := models.ExtractEntityEdges(data)
		if err != nil {
			t.Fatalf("case %d: ExtractEntityEdges: %v", ci, err)
		}
		edgeSet := map[[2]interface{}]bool{}
		for _, e := range edges {
			edgeSet[[2]interface{}{e.TargetEntity, e.TargetID}] = true
		}

		cols, _ := PartitionData(spec, data)
		colSet := map[[2]interface{}]bool{}
		// Pair up the entity/id halves per JSON field.
		half := map[string][2]interface{}{}
		for i, col := range spec.Columns {
			if !col.IsREF || cols[i] == nil {
				continue
			}
			h := half[col.JSONField]
			if col.IsREFEntity {
				h[0] = cols[i]
			} else if col.IsREFID {
				h[1] = cols[i]
			}
			half[col.JSONField] = h
		}
		for _, h := range half {
			if h[0] != nil && h[1] != nil {
				colSet[[2]interface{}{h[0], h[1]}] = true
			}
		}

		if !reflect.DeepEqual(edgeSet, colSet) {
			t.Errorf("case %d: pipelines disagree.\n  edge pipeline:   %v\n  column pipeline: %v",
				ci, edgeSet, colSet)
		}
	}
}
