// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// ts.go — xoluman's own "minimum slice" for a first, useful read-only
// ts rollup/event viewer (XM-2, XOT172): TSListTimelines, TSGetTimeline,
// TSQueryRange, TSRollupList, TSRollupGet. pkg/client had zero ts
// methods at all before this, despite pkg/server's own /ts route
// group already having a complete surface (timeline CRUD, event
// ingestion/query, aggregation, full rollup management) -- every
// other primitive touched this project (bal, cal, dxp, fsm) already
// had a client wrapper, ts did not.
//
// Write-side methods (TSDefineTimeline, TSAppend/TSBatchAppend,
// rollup def/run/delete, sync on/off) are a real, separate follow-up
// -- not covered here, matching xoluman's own stated priority: this
// slice unblocks a first, useful ts viewer; the fuller "manage ts"
// surface can come later without blocking on it.
//
// Errors arrive as the XOLU-TS00x family through the structured
// *Error type -- notably XOLU-TS004 (unknown/invalid timeline or
// rollup id), XOLU-TS005 (invalid timestamp), XOLU-TS007 (missing or
// invalid dims), XOLU-TS011 (query range too wide).

import (
	"context"
	"fmt"
	"net/http"
)

// TSListTimelines returns every timeline defined on the tenant.
//
// Hits GET /api/v1/.../ts/tl/list. Returns *client.Error on non-2xx.
func (c *Client) TSListTimelines(ctx context.Context) ([]TSTimeline, error) {
	var res []TSTimeline
	if err := c.doURL(ctx, http.MethodGet, c.buildURL("/ts/tl/list"), nil, &res); err != nil {
		return nil, err
	}
	if res == nil {
		res = []TSTimeline{}
	}
	return res, nil
}

// TSGetTimeline returns a single timeline's own definition.
//
// Hits GET /api/v1/.../ts/tl/{timelineID}. Returns *client.Error on
// non-2xx — notably XOLU-TS004 for an undefined timeline.
func (c *Client) TSGetTimeline(ctx context.Context, timelineID int64) (*TSTimeline, error) {
	if timelineID <= 0 {
		return nil, fmt.Errorf("timelineID must be positive")
	}
	var res TSTimeline
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d", timelineID))
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// TSQueryRange returns every event on req.Timeline within
// [req.From, req.To) across req.Dims. req.Dims is required and must
// be non-empty — the server rejects an empty Dims with XOLU-TS007
// before ever querying the store. req.Limit defaults server-side to
// 1000 (capped at the server's own configured maximum) when left
// zero; req.Order defaults to "asc".
//
// Hits POST /api/v1/.../ts/query/range (the POST form of range query
// — a structured JSON body rather than a query string, avoiding
// URL-length limits for wide Dims sets). Returns *client.Error on
// non-2xx — notably XOLU-TS011 if [From, To) exceeds the server's own
// configured maximum range.
func (c *Client) TSQueryRange(ctx context.Context, req TSQueryRangeRequest) (*TSQueryRangeResult, error) {
	if req.Timeline == 0 {
		return nil, fmt.Errorf("timeline is required")
	}
	if len(req.Dims) == 0 {
		return nil, fmt.Errorf("dims must be non-empty")
	}
	if req.From.IsZero() || req.To.IsZero() {
		return nil, fmt.Errorf("from and to are both required")
	}
	var res TSQueryRangeResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURL("/ts/query/range"), req, &res); err != nil {
		return nil, err
	}
	if res.Events == nil {
		res.Events = []TSEvent{}
	}
	return &res, nil
}

// TSRollupList returns every rollup defined with timelineID as its
// own source.
//
// Hits GET /api/v1/.../ts/tl/{timelineID}/rollup/list. Returns
// *client.Error on non-2xx — notably XOLU-TS004 for an undefined
// timeline.
func (c *Client) TSRollupList(ctx context.Context, timelineID int64) ([]TSRollup, error) {
	if timelineID <= 0 {
		return nil, fmt.Errorf("timelineID must be positive")
	}
	var res []TSRollup
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d/rollup/list", timelineID))
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	if res == nil {
		res = []TSRollup{}
	}
	return res, nil
}

// TSRollupGet returns a single rollup's own definition.
//
// Hits GET /api/v1/.../ts/tl/{timelineID}/rollup/{rollupID}. Returns
// *client.Error on non-2xx — notably XOLU-TS025 (ErrTSRollupNotFound)
// for an unknown rollup id.
func (c *Client) TSRollupGet(ctx context.Context, timelineID int64, rollupID string) (*TSRollup, error) {
	if timelineID <= 0 {
		return nil, fmt.Errorf("timelineID must be positive")
	}
	if rollupID == "" {
		return nil, fmt.Errorf("rollupID is required")
	}
	var res TSRollup
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d/rollup/%s", timelineID, rollupID))
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}
