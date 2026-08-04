// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package dxp

import (
	"context"

	"github.com/ha1tch/xolu/pkg/gc"
)

// Janitor trims lapsed claims from a MemCache on a timer. It
// implements gc.Sweeper and plugs into the existing GC worker
// abstraction, the in-memory descendant of pkg/reserved's Sweeper
// (proposal §3).
//
// The janitor is hygiene, not enforcement: ClaimsFor filters lapsed
// claims at read time unconditionally, so a lapsed reservation has
// already stopped counting everywhere before the janitor ever runs.
// What it reclaims is memory — bounding shard growth for tenants with
// many short-TTL reservations — never correctness.
type Janitor struct {
	cache *MemCache
}

// NewJanitor creates a Janitor over cache.
func NewJanitor(cache *MemCache) *Janitor {
	return &Janitor{cache: cache}
}

// Sweep trims lapsed claims from every known tenant shard. Examined
// counts claims inspected; Collected counts claims dropped. A tenant
// with no lapsed claims contributes zero to both, which is valid.
func (j *Janitor) Sweep(_ context.Context) (gc.Report, error) {
	var r gc.Report
	for _, tenant := range j.cache.tenants() {
		examined, dropped := j.cache.trimLapsed(tenant)
		r.Examined += examined
		r.Collected += dropped
	}
	return r, nil
}
