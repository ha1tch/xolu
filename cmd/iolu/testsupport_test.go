// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Subprocess helpers for exercising iolu command paths that call os.Exit
// (validation failures, the re-init refusal) and for asserting that a command
// completes within a deadline (the per-file list hang regression). The iolu
// binary is built once for the whole package via TestMain and removed when the
// test run finishes, so no build artifact is left behind.

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	// Register the modernc "sqlite" driver for direct sql.Open in assertions.
	_ "modernc.org/sqlite"
)

var ioluBinPath string

// TestMain builds the iolu binary into a temp dir used by the subprocess
// helpers, runs the package tests, then removes the temp dir.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "iolu-bin-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: mkdir temp: %v\n", err)
		os.Exit(2)
	}
	ioluBinPath = filepath.Join(dir, "iolu")

	build := exec.Command("go", "build", "-o", ioluBinPath, ".")
	build.Env = os.Environ()
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "TestMain: build iolu: %v\n%s\n", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(2)
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// runIoluExpectingExit runs iolu with the given args and returns its exit code.
// The first parameter documents the data root under test; args are verbatim.
func runIoluExpectingExit(t *testing.T, _ string, args ...string) int {
	t.Helper()
	_, code := runIoluWithDeadline(t, 30*time.Second, args...)
	return code
}

// runIoluWithDeadline runs the prebuilt iolu binary with a hard deadline and
// returns its combined output and exit code. A deadline overrun is reported as
// a failure so a hang regression cannot stall the suite.
func runIoluWithDeadline(t *testing.T, d time.Duration, args ...string) (string, int) {
	t.Helper()
	if ioluBinPath == "" {
		t.Fatal("iolu binary not built (TestMain did not run?)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()

	cmd := exec.CommandContext(ctx, ioluBinPath, args...)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()

	if ctx.Err() == context.DeadlineExceeded {
		t.Fatalf("iolu %v exceeded deadline %s (hang?). output:\n%s", args, d, out)
	}
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run iolu %v: %v", args, err)
		}
	}
	return string(out), code
}
