// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"testing"

	"github.com/ha1tch/xolu/pkg/internal/advcorpus"
)

// Property (D-005 root cause): the JOIN field path must never be weaker than the
// single-table field path. D-005 existed precisely because the two diverged —
// the single-table generator validated field names via validateFieldName while
// the JOIN generator did not. This test pins the invariant directly: for ANY
// identifier, whatever the single-table gate (validateFieldName) rejects, the
// JOIN gate (joinFieldRef, blob branch) must also reject.
//
// The JOIN path may be equally strict or stricter; it may never be more
// permissive. A failure here means the paths have diverged again and the JOIN
// surface is the weaker one — the D-005 condition.
func TestProperty_JoinFieldRef_NoWeakerThanSingleTable(t *testing.T) {
	d := &SQLiteDialect{}
	store := newMockJoinStore("post", false, "author", false)

	// A minimal joinSpec routing a left-alias field down the blob (non-adapted)
	// branch — the branch that interpolates into json_extract and was the D-005
	// sink.
	js := &joinSpec{
		LeftEntity:  "post",
		LeftAlias:   "a",
		RightEntity: "author",
		RightAlias:  "b",
		JoinType:    "INNER",
	}
	plan := buildJoinPlan(js, false, false)

	for _, payload := range advcorpus.AllIdentifierPayloads() {
		payload := payload
		t.Run(sanitiseSubtestName(payload), func(t *testing.T) {
			singleTableRejects := validateFieldName(payload) != nil

			_, joinErr := joinFieldRef("a", payload, js, plan, store, d)
			joinRejects := joinErr != nil

			if singleTableRejects && !joinRejects {
				t.Errorf("path divergence (D-005 regression): single-table path rejects field %q "+
					"but the JOIN path accepts it — the JOIN surface is weaker", payload)
			}
		})
	}
}

// Companion: the two gates should in fact agree exactly on the corpus (the JOIN
// path now calls the same validateFieldName). This is a tighter assertion than
// "no weaker"; it documents that the paths are unified, not merely ordered. If a
// future change makes the JOIN path legitimately stricter, relax this to the
// no-weaker property above and keep that one as the security invariant.
func TestProperty_JoinFieldRef_AgreesWithSingleTable(t *testing.T) {
	d := &SQLiteDialect{}
	store := newMockJoinStore("post", false, "author", false)
	js := &joinSpec{
		LeftEntity: "post", LeftAlias: "a",
		RightEntity: "author", RightAlias: "b",
		JoinType: "INNER",
	}
	plan := buildJoinPlan(js, false, false)

	for _, payload := range advcorpus.AllIdentifierPayloads() {
		payload := payload
		t.Run(sanitiseSubtestName(payload), func(t *testing.T) {
			singleTableRejects := validateFieldName(payload) != nil
			_, joinErr := joinFieldRef("a", payload, js, plan, store, d)
			joinRejects := joinErr != nil

			if singleTableRejects != joinRejects {
				t.Errorf("gates disagree on field %q: single-table rejects=%v, JOIN rejects=%v",
					payload, singleTableRejects, joinRejects)
			}
		})
	}
}
