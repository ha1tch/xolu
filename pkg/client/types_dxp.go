// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// types_dxp.go — wire types for the /api/v2/dxp/def and /api/v2/dxp/txn
// surface (item 23, wave 5). Shapes mirror pkg/server/v2_dxp_def_handlers.go
// and v2_dxp_read_handlers.go byte-for-byte per the Stage 2 convention,
// checked directly against both files rather than assumed from the
// doctrine's own worked examples alone.

// DxpParticipant is one participant in a dxp/def, matching the
// doctrine's own worked-example JSON shape exactly: an id local to
// this def, the primitive it targets ("bal", "cal", "fsm", "entity",
// "ts"), the op that primitive exposes, and its params -- which may
// contain {"$ref": "<binding name>"} templates resolved against a
// dxp/txn's own bindings at instantiation time (jsonplate).
type DxpParticipant struct {
	ID        string                 `json:"id"`
	Primitive string                 `json:"primitive"`
	Op        string                 `json:"op"`
	Params    map[string]interface{} `json:"params,omitempty"`
}

// DxpPhaseTTL is a dxp/def's own phase_ttl block. Reserve is the only
// def-configurable phase timeout today -- Validate/Execute timing is
// coordinator-owned, never def-configurable.
type DxpPhaseTTL struct {
	Reserve string `json:"reserve"`
}

// DxpAnalysis is the static-analysis result computed once at
// registration and returned with every def response thereafter.
// CollapseEligible and EngineHomogeneous are separate facts: a
// participant set can be tenant-scoped (collapse-eligible per @D06)
// while still including a non-SQL primitive, which forces the phased
// dispatch path regardless.
type DxpAnalysis struct {
	CollapseEligible  bool     `json:"collapse_eligible"`
	EngineHomogeneous bool     `json:"engine_homogeneous"`
	Warnings          []string `json:"warnings,omitempty"`
}

// DxpDefCreateRequest is the body POST /dxp/def accepts. BindingsSchema
// is an optional JSON Schema object validated against a dxp/txn's own
// bindings at instantiation time -- omit it to skip bindings validation
// entirely (matches the server's own "no schema means validation
// passes" convention).
type DxpDefCreateRequest struct {
	Name           string                 `json:"name"`
	Pattern        string                 `json:"pattern"`
	Participants   []DxpParticipant       `json:"participants"`
	PhaseTTL       DxpPhaseTTL            `json:"phase_ttl"`
	BindingsSchema map[string]interface{} `json:"bindings_schema,omitempty"`
}

// DxpDef is a registered definition, as returned by DxpDefCreate and
// DxpDefGet. Spec and BindingsSchema are populated by DxpDefGet only
// (DxpDefCreate's own response echoes just what a caller needs to
// proceed to DxpTxnCreate: the id and the computed analysis) -- both
// are the zero value on a DxpDefCreate response, not an error.
type DxpDef struct {
	ID             int64                  `json:"id"`
	Name           string                 `json:"name"`
	CreatedAt      string                 `json:"created_at"`
	Analysis       DxpAnalysis            `json:"analysis"`
	Spec           *DxpDefCreateRequest   `json:"spec,omitempty"`
	BindingsSchema map[string]interface{} `json:"bindings_schema,omitempty"`
}

// DxpDefSummary is one entry in DxpDefList's response -- deliberately
// narrower than DxpDef (no spec, no analysis): matches what GET
// /dxp/def actually returns per definition, not what GET /dxp/def/{id}
// returns for one.
type DxpDefSummary struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

// DxpDefListResult is the response of DxpDefList.
type DxpDefListResult struct {
	Definitions []DxpDefSummary `json:"definitions"`
}

// DxpTxnCreateRequest is the body POST /dxp/txn accepts -- one
// complete, self-contained invocation (dxp-coordinator-design.md's own
// recorded correction: closer to calling a stored procedure than
// opening a transaction). Bindings must satisfy DefID's own
// bindings_schema, if it has one.
type DxpTxnCreateRequest struct {
	DefID    int64                  `json:"def_id"`
	Bindings map[string]interface{} `json:"bindings,omitempty"`
}

// DxpTxnSnapshot is the fully-resolved def cloned into a dxp/txn at
// creation -- every participant's {"$ref": ...} already replaced with
// its bound value, never a template by the time a caller sees this.
type DxpTxnSnapshot struct {
	Pattern      string           `json:"pattern"`
	Participants []DxpParticipant `json:"participants"`
	PhaseTTL     DxpPhaseTTL      `json:"phase_ttl"`
}

// DxpTxn is a transaction instance, as returned by DxpTxnCreate and
// DxpTxnGet. Status is one of "active" (should not appear here --
// POST /dxp/txn dispatches synchronously in the same request, so a
// freshly-created instance is already terminal by the time a caller
// sees it), "committed", "released", or "expired". Reason is set only
// on a non-committed outcome; DefName and DeadlineNs are populated by
// DxpTxnGet only, matching what GET /dxp/txn/{id} returns that POST
// /dxp/txn's own response does not.
type DxpTxn struct {
	ID               int64          `json:"id"`
	DefID            int64          `json:"def_id"`
	DefName          string         `json:"def_name,omitempty"`
	Status           string         `json:"status"`
	CommittedThrough int            `json:"committed_through"`
	Reason           string         `json:"reason,omitempty"`
	DeadlineNs       int64          `json:"deadline_ns,omitempty"`
	CreatedAt        string         `json:"created_at"`
	Snapshot         DxpTxnSnapshot `json:"snapshot"`
}

// DxpTxnSummary is one entry in DxpTxnList's response -- deliberately
// narrower than DxpTxn (no snapshot, no deadline): matches what GET
// /dxp/txn actually returns per instance.
type DxpTxnSummary struct {
	ID               int64  `json:"id"`
	DefID            int64  `json:"def_id"`
	DefName          string `json:"def_name"`
	Status           string `json:"status"`
	CommittedThrough int    `json:"committed_through"`
	CreatedAt        string `json:"created_at"`
}

// DxpTxnListResult is the response of DxpTxnList.
type DxpTxnListResult struct {
	Instances []DxpTxnSummary `json:"instances"`
}
