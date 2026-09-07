//go:build integration

// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// adversarial_integration_test.go — intense adversarial testing
// against wave 15/16's own client implementation (obj, loc, ts),
// requested unsolicited after that work shipped, then implemented for
// real. Each test either confirms existing behavior is correct under
// a hostile input/timing, or surfaces a genuine finding, not assumed
// safe by inspection alone.

package client_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/client"
)

// TestAdversarial_FenceLookupPathColonCollision tested a real,
// predicted-in-advance bug in fenceLookupPath's own earlier design (a
// single fenceID string, split on ":" to distinguish "kind:key"
// shorthand from a plain tree-aligned location id) -- confirmed real
// against a location literally named "root:special", then fixed by
// replacing that design with LocFenceRef (Kind and Key as two
// explicit fields, never inferred from a string's own shape,
// mirroring the server's own /loc/fences/{kind}/{key} path directly).
// This test now verifies the fix -- the same colon-containing id that
// used to defeat the guess is addressed correctly through
// LocFenceRefByLocation, no ambiguity possible since there's nothing
// left to guess.
func TestAdversarial_FenceLookupPathColonCollision(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	postable := true
	weirdID := "root:special"
	if _, err := env.c.LocDefine(ctx, client.LocDefineRequest{
		LocationID: weirdID, Name: "special", Postable: &postable,
		Placement: client.LocPlacement{Anchor: &client.LocAnchor{Lat: -34.9, Lon: -56.16}},
	}); err != nil {
		t.Fatalf("LocDefine (id containing a colon): %v", err)
	}

	square := [][][2]float64{{
		{-56.18, -34.88}, {-56.14, -34.88}, {-56.14, -34.92}, {-56.18, -34.92}, {-56.18, -34.88},
	}}
	if _, err := env.c.LocFenceAttach(ctx, client.LocFenceAttachRequest{
		AlignedTo: weirdID,
		Geometry:  client.LocFenceGeometry{Type: "Polygon", Coordinates: square},
	}); err != nil {
		t.Fatalf("LocFenceAttach (tree-aligned to a colon-containing id): %v", err)
	}

	got, err := env.c.LocFenceGet(ctx, client.LocFenceRefByLocation(weirdID))
	if err != nil {
		t.Fatalf("LocFenceGet(LocFenceRefByLocation(%q)): want success -- Kind/Key are "+
			"explicit fields now, nothing left to guess from the id's own shape -- got: %v",
			weirdID, err)
	}
	if got.FenceID != weirdID {
		t.Fatalf("LocFenceGet: want fence_id=%q, got %q", weirdID, got.FenceID)
	}

	if err := env.c.LocFenceDelete(ctx, client.LocFenceRefByLocation(weirdID)); err != nil {
		t.Fatalf("LocFenceDelete(LocFenceRefByLocation(%q)): %v", weirdID, err)
	}
	if _, err := env.c.LocFenceGet(ctx, client.LocFenceRefByLocation(weirdID)); err == nil {
		t.Errorf("LocFenceGet after delete: want an error, got a live record")
	}

	t.Logf("confirmed fixed: a location id containing a colon (%q) is now addressed "+
		"correctly through LocFenceRefByLocation, get/delete both round-tripped cleanly.", weirdID)
}

// TestAdversarial_TSBatchAppendPartialFailureAtEnd tests
// TSBatchAppendResult's own doc comment claim more thoroughly than
// the existing TestIntegration_TSWriteFlow does: that test proves a
// FULLY invalid batch fails outright. This test proves the harder
// case -- four valid events followed by one invalid event at the very
// END of the batch -- and confirms the failure is genuinely
// all-or-nothing (0 land, not 4 of 5), matching what was checked
// directly against pkg/timeseries's own AppendBatch (validates every
// event before writing any of them) before that claim was written.
func TestAdversarial_TSBatchAppendPartialFailureAtEnd(t *testing.T) {
	env := bootServerWithTS(t)
	ctx := context.Background()

	if _, err := env.c.TSProvision(ctx); err != nil {
		t.Fatalf("TSProvision: %v", err)
	}
	if _, err := env.c.TSDefineTimeline(ctx, client.TSDefineTimelineRequest{
		ID: 1, Name: "readings", Dims: 1,
	}); err != nil {
		t.Fatalf("TSDefineTimeline: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	mixedBatch := []client.TSEvent{
		{Timeline: 1, Dims: []uint64{7}, Time: now.Add(-4 * time.Hour), Nums: []float64{1}},
		{Timeline: 1, Dims: []uint64{7}, Time: now.Add(-3 * time.Hour), Nums: []float64{2}},
		{Timeline: 1, Dims: []uint64{7}, Time: now.Add(-2 * time.Hour), Nums: []float64{3}},
		{Timeline: 1, Dims: []uint64{7}, Time: now.Add(-1 * time.Hour), Nums: []float64{4}},
		// The one bad event, deliberately last: dims count (2) doesn't
		// match the timeline's own definition (1).
		{Timeline: 1, Dims: []uint64{7, 8}, Time: now, Nums: []float64{5}},
	}
	if _, err := env.c.TSBatchAppend(ctx, mixedBatch); err == nil {
		t.Fatalf("TSBatchAppend (4 valid + 1 invalid at the end): want a refusal, got success")
	}

	sliced, err := env.c.TSQueryRange(ctx, client.TSQueryRangeRequest{
		Timeline: 1, Dims: []uint64{7}, From: now.Add(-5 * time.Hour), To: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("TSQueryRange (after the refused batch): %v", err)
	}
	if len(sliced.Events) != 0 {
		t.Errorf("CONFIRMED FINDING (or the opposite -- record whichever this is): "+
			"want 0 events landed after a batch refused for its own last event's dims "+
			"mismatch, got %d: %+v -- if non-zero, TSBatchAppendResult's own doc comment "+
			"claim of no-partial-acceptance does not hold for a failure at the END of "+
			"an otherwise-valid batch, only for an entirely-invalid one.",
			len(sliced.Events), sliced.Events)
	} else {
		t.Logf("confirmed: 0 of 5 events landed -- the 4 valid events preceding the " +
			"one invalid one at the end were correctly not partially admitted.")
	}
}

// TestAdversarial_TSUpdateTimelineDimsBypass tests the server itself,
// not just this client's own guardrail. TSUpdateTimelineRequest was
// deliberately designed to structurally omit Dims so a caller can't
// send a conflicting value -- which means the existing test only
// proves this client can't construct that request, not that the
// SERVER discards a conflicting Dims if one arrives some other way
// (a different client, a hand-crafted request, a future client bug).
// This bypasses the typed client entirely with a raw PATCH carrying a
// genuinely different dims value.
func TestAdversarial_TSUpdateTimelineDimsBypass(t *testing.T) {
	env := bootServerWithTS(t)
	ctx := context.Background()

	if _, err := env.c.TSProvision(ctx); err != nil {
		t.Fatalf("TSProvision: %v", err)
	}
	if _, err := env.c.TSDefineTimeline(ctx, client.TSDefineTimelineRequest{
		ID: 1, Name: "readings", Dims: 1,
	}); err != nil {
		t.Fatalf("TSDefineTimeline (dims=1): %v", err)
	}

	body := strings.NewReader(`{"name":"readings-renamed","dims":4}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch,
		env.ts.URL+"/api/v1/tenant/default/ts/tl/1", body)
	if err != nil {
		t.Fatalf("build raw PATCH request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("raw PATCH: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("raw PATCH with dims=4: want 200 (the field should be silently ignored, "+
			"not refused), got %d", resp.StatusCode)
	}

	afterRaw, err := env.c.TSGetTimeline(ctx, 1)
	if err != nil {
		t.Fatalf("TSGetTimeline (after the raw PATCH): %v", err)
	}
	if afterRaw.Name != "readings-renamed" {
		t.Errorf("raw PATCH: want name updated to readings-renamed, got %q", afterRaw.Name)
	}
	if afterRaw.Dims != 1 {
		t.Errorf("CONFIRMED FINDING (or the opposite): a raw PATCH carrying dims=4 "+
			"was sent directly to the server, bypassing this client's own guardrail "+
			"entirely -- want Dims to remain 1 (immutable, silently ignored server-side "+
			"as documented), got %d. If 4, the server's own immutability claim is false, "+
			"not just unenforced by this client.", afterRaw.Dims)
	} else {
		t.Logf("confirmed at the server level, not just the client's own guardrail: " +
			"a raw request carrying a conflicting dims value was silently ignored, " +
			"Dims genuinely remained 1.")
	}
}

// TestAdversarial_TSRollupCycleRejected tests XOLU-TS-rollup-cycle,
// referenced directly in TSRollupDefine's own doc comment but never
// actually triggered while implementing it -- reading that the check
// exists in pkg/timeseries's own rollup.go is not the same as proving
// it fires correctly against a real cycle.
func TestAdversarial_TSRollupCycleRejected(t *testing.T) {
	env := bootServerWithTS(t)
	ctx := context.Background()

	if _, err := env.c.TSProvision(ctx); err != nil {
		t.Fatalf("TSProvision: %v", err)
	}
	for _, id := range []int64{1, 2} {
		if _, err := env.c.TSDefineTimeline(ctx, client.TSDefineTimelineRequest{
			ID: id, Name: "t", Dims: 1,
		}); err != nil {
			t.Fatalf("TSDefineTimeline (%d): %v", id, err)
		}
	}

	if _, err := env.c.TSRollupDefine(ctx, 1, client.TSRollupDefineRequest{
		DestTID: 2, BucketDuration: "5m",
	}); err != nil {
		t.Fatalf("TSRollupDefine (1 -> 2): %v", err)
	}

	_, err := env.c.TSRollupDefine(ctx, 2, client.TSRollupDefineRequest{
		DestTID: 1, BucketDuration: "5m",
	})
	if err == nil {
		t.Fatalf("CONFIRMED FINDING: TSRollupDefine(2 -> 1), which would close a real " +
			"cycle with the already-existing 1 -> 2 rollup, succeeded -- want a refusal " +
			"(XOLU-TS-rollup-cycle).")
	}
	t.Logf("rollup cycle correctly refused: %v", err)
}

// TestAdversarial_TSRollupDepthRejected tests XOLU-TS-rollup-depth the
// same way -- referenced in a doc comment, never actually triggered.
// pkg/timeseries.MaxRollupDepth is 4, and rollupRegistry.add's own
// walk-upward accounting means a chain of 5 sequential rollup
// definitions (6 timelines) has its 5th definition rejected -- worked
// out directly from reading that function's own depth arithmetic
// before writing this test, not guessed.
func TestAdversarial_TSRollupDepthRejected(t *testing.T) {
	env := bootServerWithTS(t)
	ctx := context.Background()

	if _, err := env.c.TSProvision(ctx); err != nil {
		t.Fatalf("TSProvision: %v", err)
	}
	for id := int64(1); id <= 6; id++ {
		if _, err := env.c.TSDefineTimeline(ctx, client.TSDefineTimelineRequest{
			ID: id, Name: "t", Dims: 1,
		}); err != nil {
			t.Fatalf("TSDefineTimeline (%d): %v", id, err)
		}
	}

	// 1->2, 2->3, 3->4, 4->5: each individually within depth 4.
	for src := int64(1); src <= 4; src++ {
		if _, err := env.c.TSRollupDefine(ctx, src, client.TSRollupDefineRequest{
			DestTID: src + 1, BucketDuration: "5m",
		}); err != nil {
			t.Fatalf("TSRollupDefine (%d -> %d), want success within depth 4: %v", src, src+1, err)
		}
	}

	// 5->6: the fifth link in the chain, exceeding MaxRollupDepth=4.
	_, err := env.c.TSRollupDefine(ctx, 5, client.TSRollupDefineRequest{
		DestTID: 6, BucketDuration: "5m",
	})
	if err == nil {
		t.Fatalf("CONFIRMED FINDING: TSRollupDefine(5 -> 6), the fifth link in a " +
			"1->2->3->4->5->6 chain, succeeded -- want a refusal (XOLU-TS-rollup-depth), " +
			"MaxRollupDepth is 4.")
	}
	t.Logf("rollup depth limit correctly enforced: %v", err)
}

// TestAdversarial_FenceGeometryBoundaries tests three geometry
// boundary cases no existing test (this wave's own, or the earlier
// TestIntegration_LocFencesAndGeospatial) ever sent: a self-
// intersecting polygon, a self-anchored circle, and a negative-radius
// circle -- each read directly in loc.geometry.go /
// v2_loc_handlers.go before this test was written, not assumed
// refused by inspection alone. Also checks radius=0 specifically,
// since the real check is RadiusMeters < 0, not <= 0 -- zero was
// never actually confirmed either way before this test.
func TestAdversarial_FenceGeometryBoundaries(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	postable := true
	defineYard := func(id string) {
		t.Helper()
		if _, err := env.c.LocDefine(ctx, client.LocDefineRequest{
			LocationID: id, Name: id, Postable: &postable,
			Placement: client.LocPlacement{Anchor: &client.LocAnchor{Lat: -34.9, Lon: -56.16}},
		}); err != nil {
			t.Fatalf("LocDefine %s: %v", id, err)
		}
	}

	// Each sub-case gets its own location -- deliberately, after an
	// earlier draft of this test reused one "yard" location across all
	// three and produced a false "correctly refused" for the negative-
	// radius case that was actually XOLU-LOC015 (fence_id already
	// defined) left behind by an earlier sub-case, not a genuine
	// radius rejection. Caught by reading the actual error code
	// returned, not just checking err != nil.

	defineYard("yard-bowtie")
	// A genuine bowtie/self-intersecting quadrilateral: (0,0)-(2,2)-
	// (2,0)-(0,2)-(0,0) -- the two diagonal edges cross.
	bowtie := [][][2]float64{{
		{-56.16, -34.90}, {-56.14, -34.88}, {-56.14, -34.90}, {-56.16, -34.88}, {-56.16, -34.90},
	}}
	_, err := env.c.LocFenceAttach(ctx, client.LocFenceAttachRequest{
		AlignedTo: "yard-bowtie", Geometry: client.LocFenceGeometry{Type: "Polygon", Coordinates: bowtie},
	})
	if err == nil {
		t.Errorf("CONFIRMED FINDING: a self-intersecting (bowtie) polygon was accepted -- want a refusal.")
	} else {
		t.Logf("self-intersecting polygon correctly refused: %v", err)
	}

	defineYard("yard-self-anchor")
	_, err = env.c.LocFenceAttach(ctx, client.LocFenceAttachRequest{
		AlignedTo: "yard-self-anchor",
		Geometry:  client.LocFenceGeometry{Type: "circle", Center: &client.LocFenceCenter{Self: true}, RadiusM: 100},
	})
	if err == nil {
		t.Errorf("CONFIRMED FINDING: a self-anchored circle (center.self=true) was accepted -- want a refusal.")
	} else {
		t.Logf("self-anchored circle correctly refused: %v -- NOTE: the server's own refusal "+
			"message cites \"wave 10, not built yet\" as the reason; obj (wave 10) has been "+
			"built for a long time, including this same session's own further work on it -- "+
			"the refusal itself is correct, but the stated reason is now stale.", err)
	}

	defineYard("yard-neg-radius")
	_, err = env.c.LocFenceAttach(ctx, client.LocFenceAttachRequest{
		AlignedTo: "yard-neg-radius",
		Geometry:  client.LocFenceGeometry{Type: "circle", Center: &client.LocFenceCenter{Lat: -34.9, Lon: -56.16}, RadiusM: -5},
	})
	if err == nil {
		t.Errorf("CONFIRMED FINDING: a negative-radius circle (-5m) was accepted -- want a refusal.")
	} else if xerr, ok := err.(*client.Error); ok && xerr.Code == "XOLU-LOC015" {
		t.Fatalf("test setup bug, not a finding: got XOLU-LOC015 (fence_id already defined) "+
			"instead of a genuine radius-validation error -- %v", err)
	} else {
		t.Logf("negative-radius circle correctly refused: %v", err)
	}

	defineYard("yard-zero")
	zeroFence, err := env.c.LocFenceAttach(ctx, client.LocFenceAttachRequest{
		AlignedTo: "yard-zero",
		Geometry:  client.LocFenceGeometry{Type: "circle", Center: &client.LocFenceCenter{Lat: -34.9, Lon: -56.16}, RadiusM: 0},
	})
	if err != nil {
		t.Logf("radius=0 circle refused: %v -- the code's own check is RadiusMeters < 0, "+
			"not <= 0, so this refusal comes from somewhere other than that specific check.", err)
	} else {
		t.Logf("radius=0 (degenerate point) circle accepted, fence_id=%v -- matches the "+
			"real check being RadiusMeters < 0, not <= 0.", zeroFence.FenceID)
	}
}

// TestAdversarial_LocPatchTriStateEdges tests two edges of
// LocPatchRequest's own tri-state Capacity design that the existing
// TestIntegration_LocCoreFlow never reached -- that test proved "set
// a new ceiling" and "clear it entirely" both work, but never sent a
// negative capacity (a grep across pkg/server and pkg/loc found no
// explicit guard against one before this test was written -- worth
// checking directly rather than assuming either outcome) and never
// distinguished "name omitted" from "name explicitly set to empty
// string" -- omitempty on a *string field omits only a nil pointer,
// not a non-nil pointer to an empty string, so Name: strPtr("") is a
// real, distinct wire case LocPatchRequest can construct but no
// existing test ever sent.
func TestAdversarial_LocPatchTriStateEdges(t *testing.T) {
	env := bootServer(t)
	ctx := context.Background()

	postable := true
	if _, err := env.c.LocDefine(ctx, client.LocDefineRequest{
		LocationID: "bay", Name: "Bay", Postable: &postable, Capacity: int64Ptr(10),
		Placement: client.LocPlacement{Anchor: &client.LocAnchor{Lat: -34.9, Lon: -56.16}},
	}); err != nil {
		t.Fatalf("LocDefine bay: %v", err)
	}

	negCap := int64(-5)
	negCapPtr := &negCap
	patched, err := env.c.LocPatch(ctx, "bay", client.LocPatchRequest{Capacity: &negCapPtr})
	if err != nil {
		t.Logf("negative capacity (-5) correctly refused: %v", err)
	} else {
		t.Errorf("CONFIRMED FINDING: LocPatch accepted a negative capacity (-5) -- got %+v. "+
			"No explicit guard against this was found in pkg/server or pkg/loc before this "+
			"test was written; a negative item-count ceiling has no physical meaning.", patched)
	}

	emptyName := ""
	patchedEmpty, err := env.c.LocPatch(ctx, "bay", client.LocPatchRequest{Name: &emptyName})
	if err != nil {
		t.Logf("explicit empty-string name correctly refused: %v", err)
	} else if patchedEmpty.Name == "" {
		t.Logf("NOTED, NOT FIXED (genuinely ambiguous, unlike the negative-capacity finding "+
			"above): LocPatch accepted an explicit empty-string name -- the location's own "+
			"name is now genuinely empty, not merely unchanged. Name: strPtr(\"\") is distinct "+
			"from a nil Name (omitted) at the wire level (omitempty only omits a nil pointer). "+
			"Left as-is: an empty display name is unusual but not obviously wrong the way a "+
			"negative capacity ceiling is -- a location could plausibly want one.")
	} else {
		t.Logf("explicit empty-string name silently left the name unchanged (%q) rather "+
			"than either setting it empty or returning an error -- a third behavior, "+
			"worth knowing about even though it's not obviously wrong.", patchedEmpty.Name)
	}
}
