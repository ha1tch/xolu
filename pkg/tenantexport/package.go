// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package tenantexport

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/ha1tch/xolu/pkg/blob"
)

// PackageResult describes a completed, blob-stored export.
type PackageResult struct {
	// Key is the blob key the export was stored under.
	Key string
	// SHA256 is the stored content's hash, as returned by the blob
	// store itself.
	SHA256 string
	// Bytes is the packaged zip's own size.
	Bytes int64
}

// PackageAndStore zips every file directly inside srcDir (non-
// recursive -- the JSON files ExportSQLiteTables/ExportPebbleStores
// write are already flat, one file per table/store) and stores the
// result as a blob under key via bs.Put. srcDir itself is not removed;
// the caller owns cleanup (see this package's own doc comment on the
// temp-directory convention -- staged under the tenant's own
// directory, moved into blobs, then removed by the caller once this
// returns successfully).
func PackageAndStore(ctx context.Context, srcDir string, bs *blob.Store, key string) (*PackageResult, error) {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil, fmt.Errorf("tenantexport: read %s: %w", srcDir, err)
	}

	zipPath := filepath.Join(filepath.Dir(srcDir), filepath.Base(srcDir)+".zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		return nil, fmt.Errorf("tenantexport: create %s: %w", zipPath, err)
	}
	defer func() { _ = os.Remove(zipPath) }() // the zip is a staging artifact once it's in blob storage; removed regardless of outcome below

	zw := zip.NewWriter(zf)
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			_ = zw.Close()
			_ = zf.Close()
			return nil, fmt.Errorf("tenantexport: packaging cancelled: %w", err)
		}
		if entry.IsDir() {
			continue // srcDir is expected flat; a subdirectory would be a caller bug, not silently recursed into
		}
		if err := addFileToZip(zw, filepath.Join(srcDir, entry.Name()), entry.Name()); err != nil {
			_ = zw.Close()
			_ = zf.Close()
			return nil, fmt.Errorf("tenantexport: add %s to zip: %w", entry.Name(), err)
		}
	}
	if err := zw.Close(); err != nil {
		_ = zf.Close()
		return nil, fmt.Errorf("tenantexport: finalize zip: %w", err)
	}
	if err := zf.Close(); err != nil {
		return nil, fmt.Errorf("tenantexport: close zip file: %w", err)
	}

	f, err := os.Open(zipPath)
	if err != nil {
		return nil, fmt.Errorf("tenantexport: reopen %s for upload: %w", zipPath, err)
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("tenantexport: stat %s: %w", zipPath, err)
	}

	sha, _, _, err := bs.Put(key, f, "application/zip")
	if err != nil {
		return nil, fmt.Errorf("tenantexport: store blob %s: %w", key, err)
	}

	return &PackageResult{Key: key, SHA256: sha, Bytes: info.Size()}, nil
}

func addFileToZip(zw *zip.Writer, srcPath, nameInZip string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	w, err := zw.Create(nameInZip)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, src)
	return err
}
