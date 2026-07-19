// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Command interop-server boots a xolu S3 listener in scoped mode with a known
// S3KeyGrant, so a real third-party S3 client (mc, boto3, s3cmd, aws-cli) can be
// exercised against the SigV4 verification path. It is test orchestration, not
// part of the shipped server; see tests/interop/run.sh for the launcher.
//
// Fixed test fixture:
//
//	bucket / tenant : acme
//	access key      : AKIAINTEROP
//	secret          : interop-secret-key
//
// Flags:
//
//	-addr   listen address (default :19091)
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ha1tch/xolu/pkg/cache"
	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/server"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/ha1tch/xolu/pkg/validation"

	"github.com/rs/zerolog"
)

// Fixture values shared with run.sh and any client harness.
const (
	FixtureBucket    = "acme"
	FixtureAccessKey = "AKIAINTEROP"
	FixtureSecret    = "interop-secret-key"
)

func main() {
	addr := flag.String("addr", ":19091", "listen address")
	flag.Parse()

	tmp, err := os.MkdirTemp("", "xolu-interop-*")
	if err != nil {
		panic(err)
	}
	blobDir := filepath.Join(tmp, "blobs")
	if err := os.MkdirAll(blobDir, 0o755); err != nil {
		panic(err)
	}

	cfg := config.Default()
	cfg.StorageType = "sqlite"
	cfg.BaseDir = tmp
	cfg.GraphEnabled = false
	cfg.MaxEntitySize = 1 << 20
	cfg.DefaultPageSize = 100

	// Scoped S3 with a known key/secret grant for the fixture bucket.
	cfg.TenantMode = "strict"
	cfg.TenantAuthMode = "scoped"
	cfg.BlobEnabled = true
	cfg.BlobDir = blobDir
	cfg.BlobMaxSize = 1 << 20
	cfg.S3Enabled = true
	cfg.S3KeyGrants = []config.S3KeyGrant{
		{AccessKey: FixtureAccessKey, Secret: FixtureSecret, Tenants: []string{FixtureBucket}},
	}

	dbPath := filepath.Join(tmp, "interop.db")
	store, err := storage.NewStore("sqlite", map[string]interface{}{"db_path": dbPath})
	if err != nil {
		panic(err)
	}
	memCache := cache.NewMemoryCache(1000, 300*time.Second)
	g := graph.NewFlatGraph()
	validator := validation.NewJSONSchemaValidator(filepath.Join(tmp, "schema"))
	logger := zerolog.New(os.Stdout).Level(zerolog.WarnLevel)

	srv := server.New(cfg, store, memCache, g, validator, logger)
	if err := srv.TenantRegistry().Register(context.Background(), FixtureBucket, 1); err != nil {
		panic(err)
	}

	fmt.Printf("xolu S3 interop server on %s (bucket=%s, ak=%s)\n", *addr, FixtureBucket, FixtureAccessKey)
	if err := http.ListenAndServe(*addr, srv.S3Handler()); err != nil {
		panic(err)
	}
}
