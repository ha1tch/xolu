// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package tenantexport

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/ha1tch/xolu/pkg/blob"
)

func TestPackageAndStore_HappyPath(t *testing.T) {
	srcDir := t.TempDir()
	files := map[string]string{
		"t0000_nodes.json":  `[{"id":1}]`,
		"cal_bookings.json": `[{"tenant_id":5,"booking_id":"b1"}]`,
		"bal_rollup.json":   `[{"key_b64":"YQ==","value_b64":"Yg=="}]`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	blobDir := t.TempDir()
	bs, err := blob.NewStore(blobDir, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	result, err := PackageAndStore(context.Background(), srcDir, bs, "export-test-1.zip")
	if err != nil {
		t.Fatalf("PackageAndStore: %v", err)
	}
	if result.Key != "export-test-1.zip" {
		t.Errorf("Key: got %q", result.Key)
	}
	if result.SHA256 == "" {
		t.Error("SHA256 is empty")
	}
	if result.Bytes == 0 {
		t.Error("Bytes is zero")
	}

	// The staging zip file must be gone -- only the blob store should
	// have a durable copy.
	stagingZip := filepath.Join(filepath.Dir(srcDir), filepath.Base(srcDir)+".zip")
	if _, err := os.Stat(stagingZip); !os.IsNotExist(err) {
		t.Error("staging zip file should have been removed after a successful store")
	}

	// Retrieve it back through the real blob store and confirm it's a
	// valid zip containing exactly the files written above, with the
	// correct content -- not just that Put returned success.
	rc, meta, err := bs.Get("export-test-1.zip")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	zipBytes, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading retrieved blob: %v", err)
	}
	if meta.ContentType != "application/zip" {
		t.Errorf("ContentType: got %q", meta.ContentType)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("retrieved blob is not a valid zip: %v", err)
	}
	if len(zr.File) != len(files) {
		t.Fatalf("zip has %d files, want %d", len(zr.File), len(files))
	}
	for _, zf := range zr.File {
		want, ok := files[zf.Name]
		if !ok {
			t.Errorf("unexpected file in zip: %s", zf.Name)
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatalf("opening %s in zip: %v", zf.Name, err)
		}
		got, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("reading %s in zip: %v", zf.Name, err)
		}
		if string(got) != want {
			t.Errorf("%s content: got %q, want %q", zf.Name, got, want)
		}
	}
}

func TestPackageAndStore_EmptySourceDir(t *testing.T) {
	srcDir := t.TempDir()
	blobDir := t.TempDir()
	bs, err := blob.NewStore(blobDir, 0)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	result, err := PackageAndStore(context.Background(), srcDir, bs, "empty-export.zip")
	if err != nil {
		t.Fatalf("PackageAndStore on an empty source dir: %v", err)
	}
	if result.Key != "empty-export.zip" {
		t.Errorf("Key: got %q", result.Key)
	}

	rc, _, err := bs.Get("empty-export.zip")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	zipBytes, _ := io.ReadAll(rc)
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("even an empty export must be a valid (empty) zip: %v", err)
	}
	if len(zr.File) != 0 {
		t.Errorf("expected zero files in an empty export, got %d", len(zr.File))
	}
}
