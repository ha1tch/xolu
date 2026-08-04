// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package tenant

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Table name functions — all 0% coverage
// ---------------------------------------------------------------------------

// TestTableNames verifies that every table-name function returns a non-empty
// string containing the expected tenant prefix, and that different tenant IDs
// produce different table names.
func TestTableNames(t *testing.T) {
	const tid0 TenantID = 0
	const tid1 TenantID = 1
	const tidMax TenantID = 0xFFFE

	cases := []struct {
		name string
		fn   func(TenantID) string
		want string // substring expected in result
	}{
		{"NodesTableName", TenantID.NodesTableName, "_nodes"},
		{"NodeSeqTableName", TenantID.NodeSeqTableName, "_nseq"},
		{"NodeFTSTableName", TenantID.NodeFTSTableName, "_nfts"},
		{"EdgePropsTableName", TenantID.EdgePropsTableName, "_edges"},
		{"EdgeSeqTableName", TenantID.EdgeSeqTableName, "_eseq"},
		{"NodeSchemaTableName", TenantID.NodeSchemaTableName, "_n_sch"},
		{"EdgeSchemaTableName", TenantID.EdgeSchemaTableName, "_e_sch"},
		{"EdgeFTSTableName", TenantID.EdgeFTSTableName, "_efts"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got0 := tc.fn(tid0)
			got1 := tc.fn(tid1)
			gotMax := tc.fn(tidMax)

			if got0 == "" {
				t.Errorf("%s(0) returned empty string", tc.name)
			}
			if !strings.Contains(got0, tc.want) {
				t.Errorf("%s(0) = %q, want substring %q", tc.name, got0, tc.want)
			}
			if got0 == got1 {
				t.Errorf("%s(0) == %s(1) = %q; different tenants must differ", tc.name, tc.name, got0)
			}
			if got1 == gotMax {
				t.Errorf("%s(1) == %s(0xFFFE); different tenants must differ", tc.name, tc.name)
			}
		})
	}
}

// TestAdaptedTableNames verifies AdaptedNodeTableName and AdaptedEdgeTableName.
func TestAdaptedTableNames(t *testing.T) {
	n := TenantID(0).AdaptedNodeTableName("person")
	if n == "" {
		t.Error("AdaptedNodeTableName returned empty string")
	}
	if !strings.Contains(n, "person") {
		t.Errorf("AdaptedNodeTableName(0,person) = %q, want it to contain entity name", n)
	}
	if TenantID(0).AdaptedNodeTableName("person") == TenantID(1).AdaptedNodeTableName("person") {
		t.Error("AdaptedNodeTableName: different tenant IDs must produce different names")
	}
	if TenantID(0).AdaptedNodeTableName("person") == TenantID(0).AdaptedNodeTableName("dept") {
		t.Error("AdaptedNodeTableName: different entity types must produce different names")
	}

	e := TenantID(0).AdaptedEdgeTableName("KNOWS")
	if e == "" {
		t.Error("AdaptedEdgeTableName returned empty string")
	}
	if TenantID(0).AdaptedEdgeTableName("KNOWS") == TenantID(1).AdaptedEdgeTableName("KNOWS") {
		t.Error("AdaptedEdgeTableName: different tenant IDs must produce different names")
	}
	if TenantID(0).AdaptedEdgeTableName("KNOWS") == TenantID(0).AdaptedEdgeTableName("LIKES") {
		t.Error("AdaptedEdgeTableName: different rel types must produce different names")
	}
}

// TestIndexNames verifies all index-name functions.
func TestIndexNames(t *testing.T) {
	indexCases := []struct {
		name string
		fn   func(TenantID) string
	}{
		{"NodesIndexEntityType", TenantID.NodesIndexEntityType},
		{"NodesIndexUpdatedAt", TenantID.NodesIndexUpdatedAt},
		{"NodeSeqIndexEntityType", TenantID.NodeSeqIndexEntityType},
		{"EdgeSeqIndexRelType", TenantID.EdgeSeqIndexRelType},
		{"GraphIndexSource", TenantID.GraphIndexSource},
		{"GraphIndexTarget", TenantID.GraphIndexTarget},
		{"GraphIndexRel", TenantID.GraphIndexRel},
	}
	for _, tc := range indexCases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.fn(0)
			if got == "" {
				t.Errorf("%s(0) returned empty string", tc.name)
			}
			if tc.fn(0) == tc.fn(1) {
				t.Errorf("%s: different tenant IDs must produce different index names", tc.name)
			}
		})
	}

	// Multi-argument index functions.
	if TenantID(0).AdaptedNodeIndexTenant("person") == "" {
		t.Error("AdaptedNodeIndexTenant returned empty string")
	}
	if TenantID(0).AdaptedNodeIndexTenant("person") == TenantID(1).AdaptedNodeIndexTenant("person") {
		t.Error("AdaptedNodeIndexTenant: different tenants must differ")
	}
	if TenantID(0).AdaptedNodeIndexField("person", "name") == "" {
		t.Error("AdaptedNodeIndexField returned empty string")
	}
	if TenantID(0).AdaptedNodeIndexField("person", "name") == TenantID(0).AdaptedNodeIndexField("person", "age") {
		t.Error("AdaptedNodeIndexField: different fields must produce different index names")
	}
	if TenantID(0).AdaptedEdgeIndexField("KNOWS", "since") == "" {
		t.Error("AdaptedEdgeIndexField returned empty string")
	}
	if TenantID(0).AdaptedEdgeIndexField("KNOWS", "since") == TenantID(0).AdaptedEdgeIndexField("LIKES", "since") {
		t.Error("AdaptedEdgeIndexField: different rel types must produce different index names")
	}
}

// TestNodeIDStripped verifies NodeIDStripped.
func TestNodeIDStripped(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// NodeID format: "0000@entity:id"
		{TenantID(0).NodeID("person", 1), "person:1"},
		{TenantID(1).NodeID("dept", 42), "dept:42"},
		// Already stripped (no prefix).
		{"person:1", "person:1"},
		// Empty.
		{"", ""},
	}
	for _, tc := range cases {
		got := NodeIDStripped(tc.input)
		if got != tc.want {
			t.Errorf("NodeIDStripped(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestElementKindString verifies ElementKind.String().
func TestElementKindString(t *testing.T) {
	cases := []struct {
		kind ElementKind
		want string
	}{
		{ElementNode, "node"},
		{ElementEdge, "edge"},
	}
	for _, tc := range cases {
		got := tc.kind.String()
		if got != tc.want {
			t.Errorf("ElementKind(%d).String() = %q, want %q", tc.kind, got, tc.want)
		}
	}
}
