// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

//go:build ristrategybench

// ri_strategy_bench_test.go — throughput benchmark for the three G-12 RI
// strategies, used to break performance ties among strategies that are
// all correct on multi-core.
//
// Dormant guard (build tag `ristrategybench`): meaningful only on real
// multi-core silicon, like the correctness guard G-12. The strategy is
// selected by XOLU_RI_STRATEGY; CI runs this once per strategy and
// compares ns/op.
//
// Invocation (real silicon, per strategy):
//
//	XOLU_RI_STRATEGY=<s> GOMAXPROCS=<cores> \
//	  go test -tags ristrategybench ./pkg/server/ \
//	  -run '^$' -bench BenchmarkRIStrategy -benchmem -benchtime=3s
//
// The workload is RI-RELEVANT by construction: each iteration creates a
// user, then concurrently creates a post referencing that user and
// deletes a DIFFERENT existing user — so both the create-with-refs path
// (serialise + target check) and the delete-with-restrictors path
// (serialise + in-tx referrer check) are exercised under contention. A
// workload without REF edges would leave all RI machinery dormant and
// report false parity.

package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
)

func BenchmarkRIStrategy(b *testing.B) {
	strategy := os.Getenv("XOLU_RI_STRATEGY")
	if strategy == "" {
		strategy = "serialize-intx"
	}
	ts := setupTestServerWithRIStrategy(b, strategy)
	defer ts.cleanup()

	// Schemas: users, posts.author_id →restrict→ users.
	ts.doRequest(http.MethodPost, "/api/v1/schema/users", map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}},
	})
	ts.doRequest(http.MethodPost, "/api/v1/schema/posts", map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"author_id": map[string]interface{}{
				"type":   "object",
				"format": "ref",
				"x-ref":  map[string]interface{}{"entity": "users", "on_delete": "restrict"},
			},
		},
	})

	// Seed a pool of users to delete from, so the delete path always has
	// a live, unreferenced target (delete of a referenced user is
	// correctly refused and would not exercise the successful path).
	const pool = 4096
	userIDs := make([]int, 0, pool)
	for i := 0; i < pool; i++ {
		_, body := ts.doRequest(http.MethodPost, "/api/v1/users",
			map[string]interface{}{"name": fmt.Sprintf("seed%d", i)})
		var u struct {
			ID int `json:"id"`
		}
		_ = json.Unmarshal(body, &u)
		userIDs = append(userIDs, u.ID)
	}

	b.ReportAllocs()
	b.ResetTimer()

	var mu sync.Mutex
	next := 0
	take := func() int {
		mu.Lock()
		defer mu.Unlock()
		if next >= len(userIDs) {
			return -1
		}
		id := userIDs[next]
		next++
		return id
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			// A fresh user to reference (so the create-with-ref target
			// exists), and a pooled user to delete concurrently.
			_, body := ts.doRequest(http.MethodPost, "/api/v1/users",
				map[string]interface{}{"name": "ref"})
			var ru struct {
				ID int `json:"id"`
			}
			_ = json.Unmarshal(body, &ru)

			victim := take()

			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				ts.doRequest(http.MethodPost, "/api/v1/posts", map[string]interface{}{
					"author_id": map[string]interface{}{"type": "REF", "entity": "users", "id": ru.ID},
				})
			}()
			go func() {
				defer wg.Done()
				if victim >= 0 {
					ts.doRequest(http.MethodDelete, fmt.Sprintf("/api/v1/users/%d", victim), nil)
				}
			}()
			wg.Wait()
		}
	})

	b.StopTimer()
	b.Logf("RI strategy benchmark complete [%s]", strategy)
}
