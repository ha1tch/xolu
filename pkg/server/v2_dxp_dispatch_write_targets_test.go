// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_dxp_dispatch_write_targets_test.go — direct, logic-level tests
// for writeTargets/sortedUniqueTargets (T-168), the per-write-target
// locking dispatchPhased uses to serialize participants that write to
// the same underlying account/resource. Internal package (not
// server_test) since both helpers are unexported.

import (
	"reflect"
	"testing"

	"github.com/ha1tch/xolu/pkg/dxp"
)

func TestWriteTargets_UsesExplicitFieldWhenSet(t *testing.T) {
	c := dxp.Claim{Resource: "acct:from", WriteTargets: []string{"acct:from", "acct:to"}}
	got := writeTargets(c)
	want := []string{"acct:from", "acct:to"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWriteTargets_FallsBackToResourceWhenUnset(t *testing.T) {
	// The case every adapter except bal's own Reserve is in as of this
	// writing -- cal, fsm, entity, ts never populate WriteTargets.
	c := dxp.Claim{Resource: "cal:room7:2026-08-01"}
	got := writeTargets(c)
	want := []string{"cal:room7:2026-08-01"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestWriteTargets_FallsBackWhenExplicitlyEmpty(t *testing.T) {
	c := dxp.Claim{Resource: "acct:x", WriteTargets: []string{}}
	got := writeTargets(c)
	want := []string{"acct:x"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortedUniqueTargets_SortsAndDedupes(t *testing.T) {
	got := sortedUniqueTargets([]string{"z", "a", "m", "a", "z"})
	want := []string{"a", "m", "z"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortedUniqueTargets_SingleEntry(t *testing.T) {
	got := sortedUniqueTargets([]string{"only"})
	want := []string{"only"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSortedUniqueTargets_Empty(t *testing.T) {
	got := sortedUniqueTargets(nil)
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

// TestSortedUniqueTargets_ConsistentGlobalOrderAcrossOverlappingSets is
// the direct correctness proof for the deadlock-avoidance property the
// lock-acquisition order depends on: two participants whose target
// sets overlap but aren't identical must always agree on which of the
// shared targets to lock first.
func TestSortedUniqueTargets_ConsistentGlobalOrderAcrossOverlappingSets(t *testing.T) {
	a := sortedUniqueTargets([]string{"y", "x"}) // participant A: writes x, y
	b := sortedUniqueTargets([]string{"y", "z"}) // participant B: writes y, z
	if a[0] != "x" || a[1] != "y" {
		t.Fatalf("A: got %v", a)
	}
	if b[0] != "y" || b[1] != "z" {
		t.Fatalf("B: got %v", b)
	}
	// Both agree "y" sorts after "x" and before "z" -- neither
	// participant would ever try to acquire "y" before a target that
	// sorts earlier in the OTHER participant's own set, which is what
	// prevents a circular wait between them.
}
