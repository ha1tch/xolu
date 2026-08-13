// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/rs/zerolog"

	"github.com/ha1tch/xolu/pkg/cache"
	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/server"
	"github.com/ha1tch/xolu/pkg/storage"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/validation"
)

func doJSON(method, url string, body interface{}) (int, map[string]interface{}) {
	var buf []byte
	if body != nil {
		buf, _ = json.Marshal(body)
	}
	req, err := http.NewRequest(method, url, bytes.NewReader(buf))
	if err != nil {
		panic(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var result map[string]interface{}
	_ = json.NewDecoder(resp.Body).Decode(&result)
	return resp.StatusCode, result
}

// fiveBalLegsIteration mirrors pkg/server's own
// TestDxpTxnAPI_Scale_FiveBalLegs_AllCommit exactly -- same server
// construction (matching newFullDxpServer -> newMetaServer -> newV2Server
// -> newTestServerFromConfig field-for-field), same request sequence.
// The only structural difference from the go test version is the one
// this tool exists to test for: no testing.T, no -count, no go test
// process at all.
func fiveBalLegsIteration(iter int) (bool, string) {
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("critrepro_fivebal_%d_", iter))
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	for _, entity := range []string{
		"assets", "events", "asset_types", "sensors", "sensor_bindings",
		"users", "locations", "audit_log",
	} {
		_ = os.MkdirAll(filepath.Join(tmpDir, "test_schema", entity), 0755)
	}

	cfg := &config.Config{
		Host: "localhost", Port: 0,
		StorageType:          "sqlite",
		BaseDir:              tmpDir,
		Schema:               "test_schema",
		SchemaDir:            filepath.Join(tmpDir, "test_schema"),
		CacheType:            "memory",
		CacheTTL:             300,
		MaxEntitySize:        1048576,
		GraphEnabled:         true,
		TenantMode:           "path",
		TenantAutoRegister:   true,
		APIV2Enabled:         true,
		MetaMaxValueBytes:    65536,
		MetaGCEnabled:        true,
		MetaGCIntervalSecs:   3600,
		MaxQueryDepth:        10,
		AsyncJobRetentionTTL: 86400,
		BalEnabled:           true,
	}

	dbPath := sl.SharedStorePath(cfg.BaseDir)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		panic(err)
	}
	store, err := storage.NewStore("sqlite", map[string]interface{}{"db_path": dbPath})
	if err != nil {
		panic(err)
	}
	defer func() { _ = store.Close() }()

	memCache := cache.NewMemoryCache(1000, 300*time.Second)
	g := graph.NewFlatGraph()
	schemaDir := filepath.Join(cfg.BaseDir, cfg.Schema, "_schemas")
	validator := validation.NewJSONSchemaValidator(schemaDir)
	logger := zerolog.New(os.Stdout).Level(zerolog.Disabled)

	srv := server.New(cfg, store, memCache, g, validator, logger)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer srv.Stop()

	base := ts.URL + "/api/v2/tenant/default"

	accounts := []string{"acct0", "acct1", "acct2", "acct3", "acct4"}
	for _, acct := range accounts {
		st, resp := doJSON("POST", base+"/bal/def", map[string]interface{}{
			"account_id": acct, "unit": "unit", "scale": 0,
		})
		if st != http.StatusCreated {
			panic(fmt.Sprintf("define %s: want 201, got %d %v", acct, st, resp))
		}
	}
	st, resp := doJSON("POST", base+"/bal/def", map[string]interface{}{
		"account_id": "~in", "unit": "unit", "scale": 0, "floor": "-1000000000",
	})
	if st != http.StatusCreated {
		panic(fmt.Sprintf("define ~in: want 201, got %d %v", st, resp))
	}

	participants := make([]map[string]interface{}, len(accounts))
	for i, acct := range accounts {
		participants[i] = map[string]interface{}{
			"id": fmt.Sprintf("payment%d", i), "primitive": "bal", "op": "transfer",
			"params": map[string]interface{}{"from": "~in", "to": acct, "amount": fmt.Sprintf("%d", 10+i)},
		}
	}
	def := map[string]interface{}{
		"name": "scale_five_bal_critrepro", "pattern": "3ps",
		"participants": participants,
		"phase_ttl":    map[string]interface{}{"reserve": "PT2M"},
	}
	st, defResp := doJSON("POST", base+"/dxp/def", def)
	if st != http.StatusCreated {
		panic(fmt.Sprintf("POST /dxp/def: want 201, got %d %v", st, defResp))
	}
	st, txnResp := doJSON("POST", base+"/dxp/txn", map[string]interface{}{"def_id": defResp["id"]})
	if st != http.StatusCreated {
		panic(fmt.Sprintf("POST /dxp/txn: want 201, got %d %v", st, txnResp))
	}
	if txnResp["status"] != "committed" {
		return false, fmt.Sprintf("%v", txnResp["reason"])
	}
	return true, ""
}
