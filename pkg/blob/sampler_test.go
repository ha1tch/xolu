// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package blob

import (
	"strings"
	"testing"
	"time"
)

// A Store is single-tenant: the sampler caches exactly one SampledUsage for
// the store's tenant, exposed via Current(). Cross-tenant aggregation is the
// responsibility of the blob manager (see pkg/server), not the sampler.

// ---------------------------------------------------------------------------
// ForceResample: SampledAt starts zero, becomes non-zero after first walk
// ---------------------------------------------------------------------------

func TestSampler_SampledAtZeroBeforeFirstWalk(t *testing.T) {
	s := newTestStore(t)
	sampler := NewUsageSampler(s, time.Hour) // long interval — never fires
	// Don't call Start or ForceResample.

	if !sampler.Current().SampledAt.IsZero() {
		t.Error("Current().SampledAt should be zero before any walk")
	}
}

func TestSampler_ForceResample_SampledAtBecomesNonZero(t *testing.T) {
	s := newTestStore(t)
	sampler := NewUsageSampler(s, time.Hour)

	sampler.ForceResample()

	if sampler.Current().SampledAt.IsZero() {
		t.Error("Current().SampledAt should be non-zero after ForceResample")
	}
}

// ---------------------------------------------------------------------------
// ForceResample: correct counts
// ---------------------------------------------------------------------------

func TestSampler_ForceResample_EmptyStore(t *testing.T) {
	s := newTestStore(t)
	sampler := NewUsageSampler(s, time.Hour)
	sampler.ForceResample()

	c := sampler.Current()
	if c.BlobCount != 0 {
		t.Errorf("BlobCount: want 0, got %d", c.BlobCount)
	}
	if c.KeyCount != 0 {
		t.Errorf("KeyCount: want 0, got %d", c.KeyCount)
	}
}

func TestSampler_ForceResample_Counts(t *testing.T) {
	s := newTestStore(t)

	// Put two distinct blobs and one duplicate. Content-deduplication means
	// 3 keys but 2 distinct blobs.
	content1 := []byte("sampler-test-content-1")
	content2 := []byte("sampler-test-content-2")
	if _, _, _, err := s.Put("key1", strings.NewReader(string(content1)), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Put("key2", strings.NewReader(string(content2)), "text/plain"); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Put("key3", strings.NewReader(string(content1)), "text/plain"); err != nil {
		t.Fatal(err)
	}

	sampler := NewUsageSampler(s, time.Hour)
	sampler.ForceResample()

	c := sampler.Current()
	if c.SampledAt.IsZero() {
		t.Fatal("Current SampledAt is zero after ForceResample")
	}
	if c.KeyCount != 3 {
		t.Errorf("KeyCount: want 3, got %d", c.KeyCount)
	}
	if c.BlobCount != 2 {
		t.Errorf("BlobCount: want 2 (deduplicated), got %d", c.BlobCount)
	}
}

// ---------------------------------------------------------------------------
// Start / Stop lifecycle
// ---------------------------------------------------------------------------

func TestSampler_StartStop_DoesNotDeadlock(t *testing.T) {
	s := newTestStore(t)
	sampler := NewUsageSampler(s, time.Hour) // long interval — never fires during test

	sampler.Start()

	done := make(chan struct{})
	go func() {
		sampler.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop() deadlocked")
	}
}

func TestSampler_Start_InitialWalkBeforeFirstTick(t *testing.T) {
	s := newTestStore(t)
	if _, _, _, err := s.Put("k", strings.NewReader("data"), ""); err != nil {
		t.Fatal(err)
	}

	sampler := NewUsageSampler(s, time.Hour)
	sampler.Start()
	defer sampler.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !sampler.Current().SampledAt.IsZero() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("initial walk did not complete within 2s of Start()")
}
