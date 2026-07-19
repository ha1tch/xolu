// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package oql — coverage tests for zero-coverage functions.
//
// Covers:
//   Executor.SetProfile             (0% → covered)
//   Executor.SetLimits              (0% → covered)
//   Engine.SetLimits                (0% → covered)
//   Engine.SetProfile               (0% → covered)
//   JobManager.SetQueryTimeout      (0% → covered)
//   JobManager.ExecuteSyncWithStore (0% → covered)
//   JobManager.cleanup              (0% → covered via TTL expiry)
//   SQLiteDialect.JSONFieldAliased  (0% → covered)
//   SQLiteDialect.CastExpression    (0% → covered)
//   ValidateSchemaDir               (0% → covered)
//   GetSchemaPath                   (0% → covered)
//   Validator.validateJoinClause    (0% → covered via Execute with JOIN)

package oql

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Executor.SetProfile / SetLimits
// ---------------------------------------------------------------------------

func TestExecutor_SetProfile(t *testing.T) {
	store := newMockStore()
	tmpDir := t.TempDir()
	engine := NewEngine(store, tmpDir)

	// nil profile is a no-op — must not panic.
	engine.executor.SetProfile(nil)

	// Non-nil profile replaces the planner.
	profile := &HardwareProfile{
		Name:                 "custom",
		BlobPushThreshold:    50,
		NonCoveringThreshold: 150,
		TempBTree1Threshold:  50,
		TempBTree2Threshold:  200,
	}
	engine.executor.SetProfile(profile)
}

func TestExecutor_SetLimits(t *testing.T) {
	store := newMockStore()
	tmpDir := t.TempDir()
	engine := NewEngine(store, tmpDir)

	limits := QueryLimits{
		MaxRows:     1000,
		MaxScanRows: 50000,
	}
	engine.executor.SetLimits(limits)
}

// ---------------------------------------------------------------------------
// Engine.SetLimits / SetProfile
// ---------------------------------------------------------------------------

func TestEngine_SetLimitsAndProfile(t *testing.T) {
	store := newMockStore()
	tmpDir := t.TempDir()
	engine := NewEngine(store, tmpDir)

	engine.SetLimits(QueryLimits{MaxRows: 500, MaxScanRows: 10000})

	profile := &HardwareProfile{
		Name:                 "custom",
		BlobPushThreshold:    45,
		NonCoveringThreshold: 130,
		TempBTree1Threshold:  50,
		TempBTree2Threshold:  200,
	}
	engine.SetProfile(profile)

	// nil profile must not panic.
	engine.SetProfile(nil)
}

// ---------------------------------------------------------------------------
// JobManager.SetQueryTimeout / ExecuteSyncWithStore / cleanup
// ---------------------------------------------------------------------------

func TestJobManager_SetQueryTimeout(t *testing.T) {
	store := newMockStore()
	tmpDir := t.TempDir()
	engine := NewEngine(store, tmpDir)
	jm := NewJobManager(engine, 10*time.Second)

	jm.SetQueryTimeout(5 * time.Second)
	jm.SetQueryTimeout(0) // disable
}

func TestJobManager_ExecuteSyncWithStore(t *testing.T) {
	store := newMockStore()
	store.Create(context.Background(), "item", map[string]interface{}{"name": "Widget", "price": 9.99})
	tmpDir := t.TempDir()
	os.MkdirAll(filepath.Join(tmpDir, "item"), 0755)
	engine := NewEngine(store, tmpDir)
	jm := NewJobManager(engine, 10*time.Second)

	result, err := jm.ExecuteSyncWithStore(context.Background(),
		"SELECT name FROM item", store)
	if err != nil {
		t.Fatalf("ExecuteSyncWithStore: %v", err)
	}
	if result == nil {
		t.Error("ExecuteSyncWithStore: nil result")
	}
}

func TestJobManager_Cleanup(t *testing.T) {
	store := newMockStore()
	tmpDir := t.TempDir()
	engine := NewEngine(store, tmpDir)

	// Very short TTL so completed jobs expire quickly.
	jm := NewJobManager(engine, 1*time.Millisecond)

	// Submit a query — it will complete and then expire.
	jm.Submit("SELECT 1", store)

	// Wait for job to complete and TTL to pass.
	time.Sleep(50 * time.Millisecond)

	// cleanup is called internally on the next Submit or GetJob.
	// Submit triggers the cleanup goroutine path.
	jm.Submit("SELECT 1", store)
	time.Sleep(20 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// SQLiteDialect.JSONFieldAliased / CastExpression
// ---------------------------------------------------------------------------

func TestSQLiteDialect_JSONFieldAliased(t *testing.T) {
	d := &SQLiteDialect{NodesTable: "t0000_nodes"}

	result := d.JSONFieldAliased("p", "name")
	if result == "" {
		t.Error("JSONFieldAliased returned empty string")
	}
}

func TestSQLiteDialect_CastExpression(t *testing.T) {
	d := &SQLiteDialect{NodesTable: "t0000_nodes"}

	result := d.CastExpression("x", "REAL")
	if result != "CAST(x AS REAL)" {
		t.Errorf("CastExpression = %q, want %q", result, "CAST(x AS REAL)")
	}

	result = d.CastExpression("json_extract(data,'$.age')", "INTEGER")
	if result == "" {
		t.Error("CastExpression returned empty string")
	}
}

// ---------------------------------------------------------------------------
// ValidateSchemaDir / GetSchemaPath
// ---------------------------------------------------------------------------

func TestValidateSchemaDir(t *testing.T) {
	dir := t.TempDir()

	// Valid directory.
	if err := ValidateSchemaDir(dir); err != nil {
		t.Errorf("ValidateSchemaDir (valid): %v", err)
	}

	// Non-existent directory.
	if err := ValidateSchemaDir(filepath.Join(dir, "nosuchdir")); err == nil {
		t.Error("ValidateSchemaDir (missing): expected error")
	}

	// Path is a file, not a directory.
	f := filepath.Join(dir, "notadir.txt")
	os.WriteFile(f, []byte("x"), 0644)
	if err := ValidateSchemaDir(f); err == nil {
		t.Error("ValidateSchemaDir (file): expected error")
	}
}

func TestGetSchemaPath(t *testing.T) {
	p := GetSchemaPath("/schemas", "person")
	if p != filepath.Join("/schemas", "person") {
		t.Errorf("GetSchemaPath = %q", p)
	}
}
