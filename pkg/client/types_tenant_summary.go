// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// types_tenant_summary.go — wire type for TenantSummary (XOT213,
// wave 15). Field shapes verified directly against
// pkg/server/v2_tenant_summary_handlers.go's own writeJSON call
// before being written here.

// TenantSummary is GET /tenant-summary's own response -- a
// single-round-trip data-presence check across every store a
// tenant's own data can live in, derived server-side from the same
// authoritative table lists pkg/tenantexport's own backup path uses
// (not a second, independently-maintained notion of what counts).
// Primary/Loc/Obj are keyed by the underlying table's own name; TS/
// CalIndex/BalRollup are key counts in the three per-tenant Pebble
// stores. Empty is computed server-side once and included directly,
// rather than left for every caller to re-derive the same "are all of
// these maps and ints zero" check independently.
type TenantSummary struct {
	Primary   map[string]int `json:"primary"`
	Loc       map[string]int `json:"loc"`
	Obj       map[string]int `json:"obj"`
	TS        int            `json:"ts"`
	CalIndex  int            `json:"cal_index"`
	BalRollup int            `json:"bal_rollup"`
	// Blob is the tenant's own uploaded blob key count (XOT216) --
	// always 0 when blob is disabled server-wide, matching every
	// other per-store field's own "never used this primitive"
	// treatment, not a sentinel meaning "unknown."
	Blob  int  `json:"blob"`
	Empty bool `json:"empty"`
}
