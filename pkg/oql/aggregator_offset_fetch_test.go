// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"testing"

	"github.com/ha1tch/tsqlparser/ast"
)

func recs(ids ...int) []map[string]interface{} {
	out := make([]map[string]interface{}, len(ids))
	for i, id := range ids {
		out[i] = map[string]interface{}{"id": id}
	}
	return out
}

func idsOf(records []map[string]interface{}) []int {
	out := make([]int, len(records))
	for i, r := range records {
		out[i] = r["id"].(int)
	}
	return out
}

// TestApplyOffsetFetch is the XM-5 fix (xoluman's own report,
// 2026-08-12): OFFSET N ROWS FETCH NEXT M ROWS ONLY parsed correctly
// but was never applied anywhere -- stmt.Offset/stmt.Fetch were
// simply never read by the executor. Reproduces xoluman's own exact
// case (OFFSET 2 ROWS FETCH NEXT 3 ROWS ONLY against 35 rows) plus
// every boundary shape: offset alone, fetch alone, offset past the
// end of the result set, both absent.
func TestApplyOffsetFetch(t *testing.T) {
	// xoluman's own exact reproduction: 35 rows, ids 1..35 for
	// readability, OFFSET 2 ROWS FETCH NEXT 3 ROWS ONLY.
	full35 := recs(rangeInts(1, 35)...)

	tests := []struct {
		name       string
		records    []map[string]interface{}
		offset     ast.Expression
		fetch      ast.Expression
		wantIDs    []int
		wantLength int
	}{
		{
			name:       "xoluman's own reproduction: OFFSET 2 ROWS FETCH NEXT 3 ROWS ONLY",
			records:    full35,
			offset:     intLit(2),
			fetch:      intLit(3),
			wantIDs:    []int{3, 4, 5},
			wantLength: 3,
		},
		{
			name:       "offset alone, no fetch cap",
			records:    recs(1, 2, 3, 4, 5),
			offset:     intLit(2),
			fetch:      nil,
			wantIDs:    []int{3, 4, 5},
			wantLength: 3,
		},
		{
			name:       "fetch alone, no offset",
			records:    recs(1, 2, 3, 4, 5),
			offset:     nil,
			fetch:      intLit(2),
			wantIDs:    []int{1, 2},
			wantLength: 2,
		},
		{
			name:       "offset past the end of the result set returns empty, not an error",
			records:    recs(1, 2, 3),
			offset:     intLit(10),
			fetch:      intLit(5),
			wantIDs:    []int{},
			wantLength: 0,
		},
		{
			name:       "fetch larger than remaining records after offset returns what's left",
			records:    recs(1, 2, 3, 4, 5),
			offset:     intLit(3),
			fetch:      intLit(100),
			wantIDs:    []int{4, 5},
			wantLength: 2,
		},
		{
			name:       "both absent is a no-op",
			records:    recs(1, 2, 3),
			offset:     nil,
			fetch:      nil,
			wantIDs:    []int{1, 2, 3},
			wantLength: 3,
		},
		{
			name:       "offset exactly equal to record count returns empty",
			records:    recs(1, 2, 3),
			offset:     intLit(3),
			fetch:      nil,
			wantIDs:    []int{},
			wantLength: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyOffsetFetch(tt.records, tt.offset, tt.fetch)
			if len(got) != tt.wantLength {
				t.Fatalf("want %d records, got %d: %v", tt.wantLength, len(got), got)
			}
			if tt.wantLength > 0 {
				gotIDs := idsOf(got)
				for i, id := range tt.wantIDs {
					if gotIDs[i] != id {
						t.Errorf("position %d: want id=%d, got id=%d (full: %v, want: %v)", i, id, gotIDs[i], gotIDs, tt.wantIDs)
					}
				}
			}
		})
	}
}

func rangeInts(from, to int) []int {
	out := make([]int, 0, to-from+1)
	for i := from; i <= to; i++ {
		out = append(out, i)
	}
	return out
}
