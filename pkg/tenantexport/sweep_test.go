// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package tenantexport

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/blob"
)

func TestSweepExpiredExports_DeletesOnlyExpired(t *testing.T) {
	dir := t.TempDir()
	bs, err := blob.NewStore(dir, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// A fresh export -- must survive the sweep.
	if _, _, _, err := bs.Put("export-0.zip", strings.NewReader("fresh"), "application/zip"); err != nil {
		t.Fatalf("put fresh export: %v", err)
	}
	// A non-export blob -- must never be touched by this sweep at all,
	// regardless of age.
	if _, _, _, err := bs.Put("not-an-export-key", strings.NewReader("unrelated"), "text/plain"); err != nil {
		t.Fatalf("put unrelated blob: %v", err)
	}

	report, err := SweepExpiredExports(context.Background(), bs, time.Hour)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if report.Examined != 1 {
		t.Errorf("Examined: got %d, want 1 (only the export- prefixed blob)", report.Examined)
	}
	if report.Collected != 0 {
		t.Errorf("Collected: got %d, want 0 (nothing is expired yet)", report.Collected)
	}

	if rc, _, err := bs.Get("export-0.zip"); err != nil {
		t.Errorf("fresh export should still exist: %v", err)
	} else {
		rc.Close()
	}
	if rc, _, err := bs.Get("not-an-export-key"); err != nil {
		t.Errorf("unrelated blob should be completely untouched: %v", err)
	} else {
		rc.Close()
	}
}

func TestSweepExpiredExports_TTLZeroDeletesEverythingMatchingPrefix(t *testing.T) {
	dir := t.TempDir()
	bs, err := blob.NewStore(dir, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, _, _, err := bs.Put("export-5.zip", strings.NewReader("data"), "application/zip"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, _, _, err := bs.Put("not-an-export", strings.NewReader("keep me"), "text/plain"); err != nil {
		t.Fatalf("put unrelated: %v", err)
	}

	// A zero (or negative) TTL means "everything already written counts
	// as expired" -- a real, useful configuration (e.g. an operator
	// wanting to force-clear all export blobs), not a degenerate case
	// to guard against.
	report, err := SweepExpiredExports(context.Background(), bs, 0)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if report.Collected != 1 {
		t.Errorf("Collected: got %d, want 1", report.Collected)
	}

	if _, _, err := bs.Get("export-5.zip"); err == nil {
		t.Error("expired export should have been deleted")
	}
	if rc, _, err := bs.Get("not-an-export"); err != nil {
		t.Errorf("unrelated blob must survive a TTL-0 export sweep: %v", err)
	} else {
		rc.Close()
	}
}

func TestSweepExpiredExports_EmptyStore(t *testing.T) {
	dir := t.TempDir()
	bs, err := blob.NewStore(dir, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	report, err := SweepExpiredExports(context.Background(), bs, time.Hour)
	if err != nil {
		t.Fatalf("sweep on an empty store: %v", err)
	}
	if report.Examined != 0 || report.Collected != 0 {
		t.Errorf("expected an all-zero report for an empty store, got %+v", report)
	}
}
