// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// dxp.go — item 23 (wave 5): the six dxp/def and dxp/txn methods
// against the /api/v2/dxp/* surface (item 20/21). Investigated the
// existing convention before writing anything, not assumed: bal.go and
// cal.go are the closest analogs (define once, act many times against
// a named resource). All six endpoints decode structured errors into
// *client.Error, keyed on the XOLU-DXP001-008 family -- notably
// XOLU-DXP006 for a def registration rejected by static analysis, and
// XOLU-DXP001 for bindings that fail a def's own bindings_schema at
// instantiation.
//
// A def-as-tool surface for molu (the item's own other named half) is
// deliberately NOT built here: molu's own tool-registration convention
// (how a def's participants/bindings_schema become a callable tool
// description) is molu's concern, not this client's -- DxpDefGet
// already returns everything a molu-side adapter would need (spec,
// analysis, bindings_schema) to build one.

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// DxpDefCreate registers a new dxp definition. The server computes and
// returns DxpAnalysis (CollapseEligible, EngineHomogeneous) at
// registration time; a caller does not supply it.
//
// Hits POST /api/v2/.../dxp/def. Returns *client.Error on non-2xx --
// notably XOLU-DXP006 when static analysis refuses the definition
// (unknown primitive, invalid pattern, malformed participant params).
func (c *Client) DxpDefCreate(ctx context.Context, req DxpDefCreateRequest) (*DxpDef, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if req.Pattern == "" {
		return nil, fmt.Errorf("pattern is required")
	}
	if len(req.Participants) == 0 {
		return nil, fmt.Errorf("at least one participant is required")
	}
	if req.PhaseTTL.Reserve == "" {
		return nil, fmt.Errorf("PhaseTTL.Reserve is required")
	}
	var d DxpDef
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/dxp/def"), req, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// DxpDefList returns every definition registered for the tenant,
// oldest first. Each entry is a DxpDefSummary, not a full DxpDef --
// use DxpDefGet for one definition's spec and analysis.
//
// Hits GET /api/v2/.../dxp/def. Returns *client.Error on non-2xx.
func (c *Client) DxpDefList(ctx context.Context) (*DxpDefListResult, error) {
	var res DxpDefListResult
	if err := c.doURL(ctx, http.MethodGet, c.buildURLv2("/dxp/def"), nil, &res); err != nil {
		return nil, err
	}
	if res.Definitions == nil {
		res.Definitions = []DxpDefSummary{}
	}
	return &res, nil
}

// DxpDefGet retrieves one definition by id, including its full spec,
// analysis, and bindings_schema -- everything a caller (or a molu-side
// tool adapter) needs to construct a valid DxpTxnCreateRequest against
// it without having to remember what was originally registered.
//
// Hits GET /api/v2/.../dxp/def/{id}. Returns *client.Error on non-2xx
// -- notably XOLU-DXP006 (this def's own reserved code family) with
// HTTP 404 when id does not exist.
func (c *Client) DxpDefGet(ctx context.Context, id int64) (*DxpDef, error) {
	if id <= 0 {
		return nil, fmt.Errorf("id must be a positive integer")
	}
	u := c.buildURLv2(fmt.Sprintf("/dxp/def/%d", id))
	var d DxpDef
	if err := c.doURL(ctx, http.MethodGet, u, nil, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// DxpTxnCreate instantiates req.DefID and dispatches it -- one
// complete, synchronous call: by the time this returns, the instance
// has already reached a terminal status (committed, released, or
// expired). There is no separate "start" step and nothing left
// in-flight to poll for on a normal path; DxpTxnGet exists for
// after-the-fact observability (the sweep worker's own terminal
// states, or re-reading an instance created by another caller), not
// for waiting on this one to finish.
//
// Hits POST /api/v2/.../dxp/txn. Returns *client.Error on non-2xx --
// notably XOLU-DXP001 when Bindings fails DefID's own bindings_schema,
// and HTTP 404 when DefID does not exist. A non-committed outcome
// (released or expired) is NOT an error -- it is a normal response
// with Status set accordingly and Reason naming why; check
// resp.Status, do not assume a nil error means committed.
func (c *Client) DxpTxnCreate(ctx context.Context, req DxpTxnCreateRequest) (*DxpTxn, error) {
	if req.DefID <= 0 {
		return nil, fmt.Errorf("DefID must be a positive integer")
	}
	var txn DxpTxn
	if err := c.doURL(ctx, http.MethodPost, c.buildURLv2("/dxp/txn"), req, &txn); err != nil {
		return nil, err
	}
	return &txn, nil
}

// DxpTxnList returns every transaction instance for the tenant, oldest
// first. status, if non-empty, filters to exactly one of "active",
// "committed", "released", or "expired" -- server-validated, not
// client-validated, since the set is small and stable enough that
// duplicating it here would only be one more place for it to drift.
// An empty status returns every instance regardless of outcome.
//
// Hits GET /api/v2/.../dxp/txn[?status=]. Returns *client.Error on
// non-2xx.
func (c *Client) DxpTxnList(ctx context.Context, status string) (*DxpTxnListResult, error) {
	u := c.buildURLv2("/dxp/txn")
	if status != "" {
		q := url.Values{}
		q.Set("status", status)
		u += "?" + q.Encode()
	}
	var res DxpTxnListResult
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	if res.Instances == nil {
		res.Instances = []DxpTxnSummary{}
	}
	return &res, nil
}

// DxpTxnGet retrieves one transaction instance by id, including its
// full resolved snapshot -- the after-the-fact observability surface
// item 20's own remaining scope named explicitly as missing until it
// was built: a caller (or the sweep worker's own operator-facing
// tooling) can now see a swept, expired, or torn instance's full
// participant list and outcome, not just that something happened.
//
// Hits GET /api/v2/.../dxp/txn/{id}. Returns *client.Error on non-2xx
// -- HTTP 404 when id does not exist.
func (c *Client) DxpTxnGet(ctx context.Context, id int64) (*DxpTxn, error) {
	if id <= 0 {
		return nil, fmt.Errorf("id must be a positive integer")
	}
	u := c.buildURLv2(fmt.Sprintf("/dxp/txn/%d", id))
	var txn DxpTxn
	if err := c.doURL(ctx, http.MethodGet, u, nil, &txn); err != nil {
		return nil, err
	}
	return &txn, nil
}
