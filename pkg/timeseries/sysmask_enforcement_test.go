// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

import (
	"errors"
	"strings"
	"testing"

	xoluerr "github.com/ha1tch/xolu/pkg/errors"
)

// TestSysmaskEnforcement_TwoPaths verifies the @S §8 partition:
//
//   - the user path (DefineTimeline) refuses ids in the system region;
//   - the system path (DefineSystemTimeline) refuses ids in the user
//     region;
//
// so the two paths cover the id space with no overlap. Exercised on a
// store with sysmask width 8 (top byte selects).
func TestSysmaskEnforcement_TwoPaths(t *testing.T) {
	dir := t.TempDir()
	s, err := NewPebbleStore(dir, StoreConfig{SysmaskWidth: 8}, testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close()

	userID := TimelineID(0x00000001)   // top byte zero → user
	systemID := TimelineID(0x01000001) // top byte 0x01 → system

	// User path accepts a user id.
	if err := s.DefineTimeline(userID, TimelineConfig{Name: "u", Dims: 1}); err != nil {
		t.Errorf("DefineTimeline(user id) should succeed, got: %v", err)
	}

	// User path REFUSES a system id.
	err = s.DefineTimeline(systemID, TimelineConfig{Name: "x", Dims: 1})
	if err == nil {
		t.Errorf("DefineTimeline(system id) must be refused, but succeeded")
	} else if !strings.Contains(err.Error(), string(xoluerr.ErrTSSystemScopeID)) {
		t.Errorf("expected ErrTSSystemScopeID, got: %v", err)
	}

	// System path accepts a system id.
	if err := s.DefineSystemTimeline(systemID, TimelineConfig{Name: "s", Dims: 1}); err != nil {
		t.Errorf("DefineSystemTimeline(system id) should succeed, got: %v", err)
	}

	// System path REFUSES a user id (the symmetric guard).
	err = s.DefineSystemTimeline(TimelineID(0x00000002), TimelineConfig{Name: "y", Dims: 1})
	if err == nil {
		t.Errorf("DefineSystemTimeline(user id) must be refused, but succeeded")
	} else if !strings.Contains(err.Error(), string(xoluerr.ErrTSSystemScopeID)) {
		t.Errorf("expected ErrTSSystemScopeID, got: %v", err)
	}
}

// TestSysmaskEnforcement_DefaultWidthNoGuard confirms that with the
// default width 0 (no reservation), the user path accepts every id —
// the guard is inert until an operator opts in, so pre-existing
// behaviour is unchanged.
func TestSysmaskEnforcement_DefaultWidthNoGuard(t *testing.T) {
	dir := t.TempDir()
	s, err := NewPebbleStore(dir, StoreConfig{}, testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close()

	// An id that WOULD be system under width 8 is a plain user id at
	// width 0.
	if err := s.DefineTimeline(TimelineID(0x01000001), TimelineConfig{Name: "t", Dims: 1}); err != nil {
		t.Errorf("width 0: every id is user; define should succeed, got: %v", err)
	}

	// And the system path refuses everything at width 0 (nothing is
	// system when no bits are reserved).
	err = s.DefineSystemTimeline(TimelineID(0xFFFFFFFF), TimelineConfig{Name: "z", Dims: 1})
	if err == nil {
		t.Errorf("width 0: no id is system; DefineSystemTimeline should refuse all, but succeeded")
	}
}

// TestSysmaskEnforcement_HTTPBoundaryError checks the error code is
// classifiable so the HTTP layer can map it to a 4xx rather than a 5xx.
func TestSysmaskEnforcement_HTTPBoundaryError(t *testing.T) {
	dir := t.TempDir()
	s, err := NewPebbleStore(dir, StoreConfig{SysmaskWidth: 8}, testPebbleConfig(), "", nil)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close()

	err = s.DefineTimeline(TimelineID(0x01000001), TimelineConfig{Name: "x", Dims: 1})
	if err == nil {
		t.Fatal("expected refusal")
	}
	// The error string must carry the code for classifyTSError to map.
	if !strings.Contains(err.Error(), "XOLU-TS027") {
		t.Errorf("error must carry XOLU-TS027 for HTTP classification, got: %v", err)
	}
	// Not a sentinel-wrapped error; the ts layer uses string-embedded
	// codes (matching ErrTSReservedID's pattern), so errors.Is is not
	// expected to match — this asserts the pattern, not a regression.
	if errors.Is(err, errors.New("XOLU-TS027")) {
		t.Errorf("unexpected sentinel match; ts uses embedded codes")
	}
}
