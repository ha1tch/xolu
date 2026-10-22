// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ha1tch/tsqlparser"
)

// Regression tests for the Validator's entity cache under concurrent use.
//
// Engine.Execute and Engine.ExecuteWithStore validate under the engine's
// shared (read) lock, so any number of requests are inside Validator at the
// same time. EntityExists refreshes the cache on a miss, and that refresh
// used to reassign and write the cache map in place with no
// synchronization of its own. Two overlapping misses therefore wrote the
// same map at once, and the Go runtime terminates the whole process for
// that ("fatal error: concurrent map writes") -- chi's Recoverer cannot
// catch it. A cache hit never wrote anything, which is why a server would
// run for minutes and then die on the first burst of misses.
//
// ExecuteWithStore reaches the validator through forStore, which returns the
// validator itself when the request's store is the default store, and a
// copy otherwise. Both shapes are covered below.
//
// The race detector catches this deterministically (make test-race); a
// plain run usually dies with the fatal error itself.

// countingChecker is an EntityChecker that takes a fixed time per call and
// counts calls. The delay widens the window in which concurrent refreshes
// overlap, which is what exposes the race.
type countingChecker struct {
	mu    sync.Mutex
	names []string
	delay time.Duration
	calls atomic.Int64
}

func (c *countingChecker) ListEntities(_ context.Context) ([]string, error) {
	c.calls.Add(1)
	time.Sleep(c.delay)
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.names...), nil
}

func (c *countingChecker) add(name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.names = append(c.names, name)
}

// tenantChecker stands in for a tenant's own store: a different
// EntityChecker from the validator's default one.
type tenantChecker struct{ names []string }

func (c *tenantChecker) ListEntities(_ context.Context) ([]string, error) {
	return append([]string(nil), c.names...), nil
}

func entityNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("ent%03d", i)
	}
	return out
}

// TestValidator_ConcurrentCacheMissesAreRaceFree drives many goroutines
// through the refresh-on-miss path at once, interleaved with cache hits.
func TestValidator_ConcurrentCacheMissesAreRaceFree(t *testing.T) {
	checker := &countingChecker{names: entityNames(300), delay: 5 * time.Millisecond}
	v := NewValidatorWithStore(t.TempDir(), checker)

	const workers, rounds = 48, 20
	start := make(chan struct{})
	var wg sync.WaitGroup
	failures := make(chan string, workers*rounds*2)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for r := 0; r < rounds; r++ {
				if name := fmt.Sprintf("missing%dx%d", w, r); v.EntityExists(name) {
					failures <- name + " reported as existing"
				}
				if !v.EntityExists("ent007") {
					failures <- "ent007 reported as missing"
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Error(f)
	}
}

// TestValidator_ConcurrentValidateOnUnknownEntityIsRaceFree goes through the
// same call path as the crash: parse, then Validate a SELECT naming an entity
// the cache does not know. Each goroutine parses its own statement, as
// Engine.parse does per request.
func TestValidator_ConcurrentValidateOnUnknownEntityIsRaceFree(t *testing.T) {
	checker := &countingChecker{names: entityNames(300), delay: 5 * time.Millisecond}
	v := NewValidatorWithStore(t.TempDir(), checker)

	const workers = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	failures := make(chan string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			program, errs := tsqlparser.Parse("SELECT * FROM nosuchentity")
			if len(errs) > 0 || len(program.Statements) != 1 {
				failures <- fmt.Sprintf("parse failed: %v", errs)
				return
			}
			if err := v.Validate(program.Statements[0]); err == nil {
				failures <- "Validate accepted a SELECT on an unknown entity"
			}
		}()
	}
	close(start)
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Error(f)
	}
}

// TestValidator_ConcurrentForStoreIsRaceFree mixes both shapes forStore can
// return: the validator itself (the request's store is the default store,
// which is every request that carries no tenant) and a per-request copy (a
// tenant store). Misses on both refresh the cache; lookups of a type only
// the tenant's store has must still be answered from that store.
func TestValidator_ConcurrentForStoreIsRaceFree(t *testing.T) {
	checker := &countingChecker{names: entityNames(300), delay: 5 * time.Millisecond}
	v := NewValidatorWithStore(t.TempDir(), checker)
	tenant := &tenantChecker{names: []string{"tenantonly"}}

	const workers, rounds = 48, 12
	start := make(chan struct{})
	var wg sync.WaitGroup
	failures := make(chan string, workers*rounds*4)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			<-start
			for r := 0; r < rounds; r++ {
				missing := fmt.Sprintf("missing%dx%d", w, r)
				if v.forStore(checker).EntityExists(missing) { // returns v itself
					failures <- missing + " reported as existing via the default store"
				}
				if v.forStore(tenant).EntityExists(missing) { // a copy
					failures <- missing + " reported as existing via a tenant store"
				}
				if !v.forStore(tenant).EntityExists("tenantonly") {
					failures <- "tenantonly not found through the tenant's own store"
				}
				if !v.EntityExists("ent007") {
					failures <- "ent007 reported as missing"
				}
			}
		}(w)
	}
	close(start)
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Error(f)
	}
}

// TestValidator_SimultaneousMissesShareRefreshes checks that a burst of
// misses does not become a burst of store listings. A refresh that began
// after a caller's miss and has completed already answers that caller's
// question, so N simultaneous misses cost one or two listings, not N.
func TestValidator_SimultaneousMissesShareRefreshes(t *testing.T) {
	checker := &countingChecker{names: entityNames(10), delay: 200 * time.Millisecond}
	v := NewValidatorWithStore(t.TempDir(), checker) // one listing for its own initial load
	before := checker.calls.Load()

	const workers = 32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if v.EntityExists("nosuchentity") {
				t.Error("nosuchentity reported as existing")
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := checker.calls.Load() - before; got < 1 || got > 3 {
		t.Errorf("%d simultaneous misses caused %d store listings; want 1 to 3 (one per caller is the stampede this guards against)", workers, got)
	}
}

// TestValidator_MissPicksUpNewlyCreatedEntity pins the documented behavior
// that an entity type created after the last refresh is recognized on the
// next miss, without a manual refresh -- and that coalescing refreshes does
// not let a stale refresh answer for a later miss.
func TestValidator_MissPicksUpNewlyCreatedEntity(t *testing.T) {
	checker := &countingChecker{names: []string{"alpha"}}
	v := NewValidatorWithStore(t.TempDir(), checker)

	if !v.EntityExists("alpha") {
		t.Fatal("alpha should exist from the initial load")
	}
	if v.EntityExists("beta") {
		t.Fatal("beta must not exist yet")
	}
	checker.add("beta")
	if !v.EntityExists("beta") {
		t.Error("an entity created after the last refresh must be recognized on the next miss")
	}
	if !v.EntityExists("alpha") {
		t.Error("a previously cached entity must survive a refresh")
	}
}

// TestValidator_ForStoreCopySharesCache pins that the per-request copy
// forStore returns shares the validator's entity cache: a refresh triggered
// through a copy is visible to the original without listing the store again.
// Before the cache sat behind a pointer, every copy refreshed a private map
// and threw it away, so each tenant request for a not-yet-cached type paid a
// full store listing and taught the shared cache nothing.
func TestValidator_ForStoreCopySharesCache(t *testing.T) {
	checker := &countingChecker{names: []string{"alpha"}}
	v := NewValidatorWithStore(t.TempDir(), checker)
	tenant := &tenantChecker{names: []string{"zeta"}}

	checker.add("beta")
	if !v.forStore(tenant).EntityExists("beta") {
		t.Fatal("beta, created after the initial load, should be found by the copy's refresh")
	}
	before := checker.calls.Load()
	if !v.EntityExists("beta") {
		t.Fatal("beta should now be known to the original validator")
	}
	if got := checker.calls.Load() - before; got != 0 {
		t.Errorf("the original re-listed the store %d time(s): the copy's refresh was not shared", got)
	}
}
