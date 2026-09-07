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
	"net/url"
	"strconv"
	"strings"
	"time"
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

// TSAppend appends a single event to a timeline. e.Time is marshaled
// as RFC3339Nano (Go's own time.Time JSON encoding), matching what
// the server's own parseTSTime accepts.
//
// Hits POST /api/v1/.../ts/events. Returns *client.Error on non-2xx --
// notably XOLU-TS004 for an undefined timeline, XOLU-TS007 for a
// dims-count mismatch against the timeline's own definition.
func (c *Client) TSAppend(ctx context.Context, e TSEvent) error {
	if e.Timeline <= 0 {
		return fmt.Errorf("timeline must be positive")
	}
	if e.Time.IsZero() {
		return fmt.Errorf("time is required")
	}
	return c.doURL(ctx, http.MethodPost, c.buildURL("/ts/events"), e, nil)
}

// TSBatchAppend appends events atomically -- checked directly against
// pkg/timeseries's own AppendBatch before writing this: the current
// implementation validates every event first and returns an outright
// error if any one is invalid, before any event is written, so a
// response is only ever returned when every event in this call
// succeeded together. See TSBatchAppendResult's own doc comment for
// why Accepted/Failed are still surfaced as returned rather than this
// client asserting Accepted == Total itself.
//
// Hits POST /api/v1/.../ts/events/batch. Returns *client.Error on
// non-2xx -- notably XOLU-TS006 if len(events) exceeds the server's
// own configured maximum batch size.
func (c *Client) TSBatchAppend(ctx context.Context, events []TSEvent) (*TSBatchAppendResult, error) {
	if len(events) == 0 {
		return nil, fmt.Errorf("events must be non-empty")
	}
	body := map[string]interface{}{"events": events}
	var res TSBatchAppendResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURL("/ts/events/batch"), body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ts.go, continued (XOT217, wave 16 Stage 3): timeline lifecycle and
// sync -- the foundational ts stage, staged before rollup/aggregate
// coverage since those only make sense against timelines the client
// can actually create/update/retire, not just append to.

// TSProvision is a one-time per-tenant setup call, required before
// any other ts operation succeeds against a fresh tenant.
//
// Hits POST /api/v1/.../ts/provision. Returns *client.Error on
// non-2xx.
func (c *Client) TSProvision(ctx context.Context) (*TSProvisionResult, error) {
	var res TSProvisionResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURL("/ts/provision"), nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// TSDefineTimeline defines a new timeline. req.Dims is fixed for the
// timeline's own lifetime once set here.
//
// Hits POST /api/v1/.../ts/tl/def. Returns *client.Error on non-2xx.
func (c *Client) TSDefineTimeline(ctx context.Context, req TSDefineTimelineRequest) (*TSTimeline, error) {
	if req.Dims < 1 {
		return nil, fmt.Errorf("dims is required")
	}
	var tl TSTimeline
	if err := c.doURL(ctx, http.MethodPost, c.buildURL("/ts/tl/def"), req, &tl); err != nil {
		return nil, err
	}
	return &tl, nil
}

// TSUpdateTimeline updates timelineID's own mutable fields (name,
// retention). See TSUpdateTimelineRequest's own doc comment for why
// dims cannot be changed here.
//
// Hits PATCH /api/v1/.../ts/tl/{timeline_id}. Returns *client.Error
// on non-2xx -- notably 404 for an undefined timeline.
func (c *Client) TSUpdateTimeline(ctx context.Context, timelineID int64, req TSUpdateTimelineRequest) (*TSTimeline, error) {
	if timelineID <= 0 {
		return nil, fmt.Errorf("timelineID must be positive")
	}
	var tl TSTimeline
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d", timelineID))
	if err := c.doURL(ctx, http.MethodPatch, u, req, &tl); err != nil {
		return nil, err
	}
	return &tl, nil
}

// TSDeleteTimeline removes timelineID's own definition together with
// its event data and rollups -- the inverse of TSDefineTimeline.
// Distinct from a future data-only purge (Wave 16 Stage 7), which
// keeps the definition.
//
// Hits DELETE /api/v1/.../ts/tl/{timeline_id}. Returns *client.Error
// on non-2xx -- notably 409 if the timeline still has rollups and
// cascade deletion is disabled server-side (remove the rollups
// first), 404 for an undefined timeline.
func (c *Client) TSDeleteTimeline(ctx context.Context, timelineID int64) error {
	if timelineID <= 0 {
		return fmt.Errorf("timelineID must be positive")
	}
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d", timelineID))
	return c.doURL(ctx, http.MethodDelete, u, nil, nil)
}

// TSSyncGet returns timelineID's own current WAL-fsync durability
// mode. See TSSyncStatus's own doc comment for what NoSync actually
// governs.
//
// Hits GET /api/v1/.../ts/tl/{timeline_id}/sync. Returns
// *client.Error on non-2xx.
func (c *Client) TSSyncGet(ctx context.Context, timelineID int64) (*TSSyncStatus, error) {
	if timelineID <= 0 {
		return nil, fmt.Errorf("timelineID must be positive")
	}
	var res TSSyncStatus
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d/sync", timelineID))
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// TSSyncOn restores synchronous (crash-durable) write mode for
// timelineID -- the default mode; an append waits for WAL fsync
// before returning.
//
// Hits POST /api/v1/.../ts/tl/{timeline_id}/sync/on. Returns
// *client.Error on non-2xx.
func (c *Client) TSSyncOn(ctx context.Context, timelineID int64) (*TSSyncStatus, error) {
	if timelineID <= 0 {
		return nil, fmt.Errorf("timelineID must be positive")
	}
	var res TSSyncStatus
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d/sync/on", timelineID))
	if err := c.doURL(ctx, http.MethodPost, u, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// TSSyncOff enables nosync mode for timelineID -- an append returns
// immediately without waiting for WAL fsync. Faster, but a crash
// before the kernel's own dirty-page writeback (typically under a
// second) can lose the most recent writes to this timeline.
//
// Hits POST /api/v1/.../ts/tl/{timeline_id}/sync/off. Returns
// *client.Error on non-2xx.
func (c *Client) TSSyncOff(ctx context.Context, timelineID int64) (*TSSyncStatus, error) {
	if timelineID <= 0 {
		return nil, fmt.Errorf("timelineID must be positive")
	}
	var res TSSyncStatus
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d/sync/off", timelineID))
	if err := c.doURL(ctx, http.MethodPost, u, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ts.go, continued (XOT218, wave 16 Stage 4): query completeness --
// the GET-based query-string alternative to the already-covered
// TSQueryRange (which hits POST /query/range), and TSLatest.

// TSQueryRangeGet is the query-string-based alternative to
// TSQueryRange -- same semantics, same result shape (reused directly,
// no separate type), different transport: query parameters instead
// of a JSON body. The server's own doc comment on the POST variant
// notes it exists specifically to avoid URL-length limits for complex
// queries -- this GET form is the simpler, curl-friendly counterpart
// for everything else.
//
// Hits GET /api/v1/.../ts/events?timeline=...&dims=...&from=...&to=....
// Returns *client.Error on non-2xx.
func (c *Client) TSQueryRangeGet(ctx context.Context, req TSQueryRangeRequest) (*TSQueryRangeResult, error) {
	if len(req.Dims) == 0 {
		return nil, fmt.Errorf("dims must be non-empty")
	}
	q := url.Values{}
	q.Set("timeline", strconv.FormatUint(req.Timeline, 10))
	dimStrs := make([]string, len(req.Dims))
	for i, d := range req.Dims {
		dimStrs[i] = strconv.FormatUint(d, 10)
	}
	q.Set("dims", strings.Join(dimStrs, ","))
	q.Set("from", req.From.Format(time.RFC3339Nano))
	q.Set("to", req.To.Format(time.RFC3339Nano))
	if req.Limit > 0 {
		q.Set("limit", strconv.Itoa(req.Limit))
	}
	if req.Order != "" {
		q.Set("order", req.Order)
	}
	var res TSQueryRangeResult
	u := c.buildURL("/ts/events") + "?" + q.Encode()
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// TSLatest returns the req.N most recent events for a timeline
// (defaulting to 10 if req.N is 0), optionally bounded by From/To.
//
// Hits GET /api/v1/.../ts/events/latest?timeline=...&dims=...&n=....
// Returns *client.Error on non-2xx.
func (c *Client) TSLatest(ctx context.Context, req TSLatestRequest) (*TSQueryRangeResult, error) {
	if len(req.Dims) == 0 {
		return nil, fmt.Errorf("dims must be non-empty")
	}
	q := url.Values{}
	q.Set("timeline", strconv.FormatUint(uint64(req.Timeline), 10))
	dimStrs := make([]string, len(req.Dims))
	for i, d := range req.Dims {
		dimStrs[i] = strconv.FormatUint(d, 10)
	}
	q.Set("dims", strings.Join(dimStrs, ","))
	if req.N > 0 {
		q.Set("n", strconv.Itoa(req.N))
	}
	if !req.From.IsZero() {
		q.Set("from", req.From.Format(time.RFC3339Nano))
	}
	if !req.To.IsZero() {
		q.Set("to", req.To.Format(time.RFC3339Nano))
	}
	var res TSQueryRangeResult
	u := c.buildURL("/ts/events/latest") + "?" + q.Encode()
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ts.go, continued (XOT219, wave 16 Stage 6): rollup completeness --
// building on TSRollupList/TSRollupGet, already covered.

// TSRollupDefine defines a rollup on timelineID, aggregating into
// req.DestTID at req.BucketDuration intervals.
//
// Hits POST /api/v1/.../ts/tl/{timeline_id}/rollup/def. Returns
// *client.Error on non-2xx -- notably XOLU-TS (rollup-cycle or
// rollup-depth) errors if this definition would create a cycle or
// exceed the server's own max rollup chain depth.
func (c *Client) TSRollupDefine(ctx context.Context, timelineID int64, req TSRollupDefineRequest) (*TSRollup, error) {
	if timelineID <= 0 {
		return nil, fmt.Errorf("timelineID must be positive")
	}
	if req.BucketDuration == "" {
		return nil, fmt.Errorf("bucketDuration is required")
	}
	var rollup TSRollup
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d/rollup/def", timelineID))
	if err := c.doURL(ctx, http.MethodPost, u, req, &rollup); err != nil {
		return nil, err
	}
	return &rollup, nil
}

// TSRollupParent returns the rollup definition for which timelineID
// is the destination -- its own parent in the rollup tree.
//
// Hits GET /api/v1/.../ts/tl/{timeline_id}/rollup/parent. Returns
// *client.Error on non-2xx -- notably 404 if timelineID has no
// rollup parent.
func (c *Client) TSRollupParent(ctx context.Context, timelineID int64) (*TSRollup, error) {
	if timelineID <= 0 {
		return nil, fmt.Errorf("timelineID must be positive")
	}
	var rollup TSRollup
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d/rollup/parent", timelineID))
	if err := c.doURL(ctx, http.MethodGet, u, nil, &rollup); err != nil {
		return nil, err
	}
	return &rollup, nil
}

// TSRollupDelete removes rollupID and stops its worker.
//
// Hits DELETE /api/v1/.../ts/tl/{timeline_id}/rollup/{rollup_id}.
// Returns *client.Error on non-2xx.
func (c *Client) TSRollupDelete(ctx context.Context, timelineID int64, rollupID string) error {
	if timelineID <= 0 {
		return fmt.Errorf("timelineID must be positive")
	}
	if rollupID == "" {
		return fmt.Errorf("rollupID is required")
	}
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d/rollup/%s", timelineID, url.PathEscape(rollupID)))
	return c.doURL(ctx, http.MethodDelete, u, nil, nil)
}

// TSRollupRun manually triggers rollupID for req's own bounded range.
// The request body's own From/To are formatted explicitly as
// time.RFC3339 (not relying on time.Time's default JSON marshaling,
// which produces RFC3339Nano) -- confirmed directly against the
// server's own strict time.Parse(time.RFC3339, ...) call before
// choosing this, not assumed lenient.
//
// Hits POST /api/v1/.../ts/tl/{timeline_id}/rollup/{rollup_id}/run.
// Returns *client.Error on non-2xx -- notably 404 for an unknown
// rollup.
func (c *Client) TSRollupRun(ctx context.Context, timelineID int64, rollupID string, req TSRollupRunRequest) error {
	if timelineID <= 0 {
		return fmt.Errorf("timelineID must be positive")
	}
	if rollupID == "" {
		return fmt.Errorf("rollupID is required")
	}
	body := map[string]interface{}{"cascade": req.Cascade}
	if !req.From.IsZero() {
		body["from"] = req.From.Format(time.RFC3339)
	}
	if !req.To.IsZero() {
		body["to"] = req.To.Format(time.RFC3339)
	}
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d/rollup/%s/run", timelineID, url.PathEscape(rollupID)))
	return c.doURL(ctx, http.MethodPost, u, body, nil)
}

// TSRollupStatus returns rollupID's own operational status.
//
// Hits GET /api/v1/.../ts/tl/{timeline_id}/rollup/{rollup_id}/status.
// Returns *client.Error on non-2xx -- notably 404 for an unknown
// rollup.
func (c *Client) TSRollupStatus(ctx context.Context, timelineID int64, rollupID string) (*TSRollupStatusReport, error) {
	if timelineID <= 0 {
		return nil, fmt.Errorf("timelineID must be positive")
	}
	if rollupID == "" {
		return nil, fmt.Errorf("rollupID is required")
	}
	var res TSRollupStatusReport
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d/rollup/%s/status", timelineID, url.PathEscape(rollupID)))
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// TSRollupTree returns the full rollup tree for the tenant. Returns
// nil (not an error) for a tenant with no timelines at all -- the
// server's own response body is a bare JSON null in that case,
// confirmed directly against treeNodeToResponse's own nil handling.
//
// Hits GET /api/v1/.../ts/rollup/tree. Returns *client.Error on
// non-2xx.
func (c *Client) TSRollupTree(ctx context.Context) (*TSRollupTreeNode, error) {
	var res *TSRollupTreeNode
	if err := c.doURL(ctx, http.MethodGet, c.buildURL("/ts/rollup/tree"), nil, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// ts.go, continued (XOT220, wave 16 Stage 6): aggregates -- the three
// routes flagged in advance as carrying the most shape-uncertainty of
// the five ts stages. That caution paid for itself: RangeAggregate
// and FullAggregate turned out to compute a genuinely different
// thing than Aggregate (all seven fields at once, no function
// selector, no bucketing), not thin variants of it -- see this file's
// own types_ts.go header comment for the full account.

// TSAggregate computes req.Function over req.NumField, optionally
// bucketed by req.Interval. See TSAggregateRequest's own doc comment
// for the exact 9-value Interval enum, and TSAggregateResult's own
// doc comment for the bucketed-vs-scalar union shape the response
// takes.
//
// Hits POST /api/v1/.../ts/aggregate. Returns *client.Error on
// non-2xx -- notably XOLU-TS (invalid-agg-field) if NumField is out
// of the 0-6 range.
func (c *Client) TSAggregate(ctx context.Context, req TSAggregateRequest) (*TSAggregateResult, error) {
	if req.NumField < 0 || req.NumField > 6 {
		return nil, fmt.Errorf("numField must be 0-6, got %d", req.NumField)
	}
	if req.Function == "" {
		return nil, fmt.Errorf("function is required")
	}
	var res TSAggregateResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURL("/ts/aggregate"), req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// TSRangeAggregate computes count/sum/avg/min/max for all seven
// numeric fields in one pass -- more efficient than several
// TSAggregate calls when multiple fields are needed. Does not support
// time bucketing; use TSAggregate for that.
//
// Hits POST /api/v1/.../ts/range_aggregate. Returns *client.Error on
// non-2xx.
func (c *Client) TSRangeAggregate(ctx context.Context, req TSRangeAggregateRequest) (*TSRangeAggregateResult, error) {
	var res TSRangeAggregateResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURL("/ts/range_aggregate"), req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// TSFullAggregate combines TSRangeAggregate's own exact statistics
// with approximate quantile estimates in one pass. An empty/nil
// req.Quantiles is equivalent to plain TSRangeAggregate at no extra
// cost server-side (no quantile sketch allocated).
//
// Hits POST /api/v1/.../ts/full_aggregate. Returns *client.Error on
// non-2xx.
func (c *Client) TSFullAggregate(ctx context.Context, req TSFullAggregateRequest) (*TSFullAggregateResult, error) {
	var res TSFullAggregateResult
	if err := c.doURL(ctx, http.MethodPost, c.buildURL("/ts/full_aggregate"), req, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ts.go, continued (XOT221, wave 16 Stage 7, final): retention,
// stats, and data lifecycle.

// TSGetRetention returns the tenant's own store-level default
// retention plus every timeline's own current setting.
//
// Hits GET /api/v1/.../ts/retention. Returns *client.Error on
// non-2xx.
func (c *Client) TSGetRetention(ctx context.Context) (*TSRetentionResult, error) {
	var res TSRetentionResult
	if err := c.doURL(ctx, http.MethodGet, c.buildURL("/ts/retention"), nil, &res); err != nil {
		return nil, err
	}
	if res.Timelines == nil {
		res.Timelines = []TSTimelineRetention{}
	}
	return &res, nil
}

// TSPatchRetention updates the tenant's own store-level default
// retention. Per-timeline retention is set via TSUpdateTimeline, not
// here.
//
// Hits PATCH /api/v1/.../ts/retention. Returns *client.Error on
// non-2xx.
func (c *Client) TSPatchRetention(ctx context.Context, defaultRetentionDays int) (*TSPatchRetentionResult, error) {
	if defaultRetentionDays < 0 {
		return nil, fmt.Errorf("defaultRetentionDays must be non-negative")
	}
	body := map[string]interface{}{"default_retention_days": defaultRetentionDays}
	var res TSPatchRetentionResult
	if err := c.doURL(ctx, http.MethodPatch, c.buildURL("/ts/retention"), body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// TSStats returns store-level diagnostics for the whole tenant.
//
// Hits GET /api/v1/.../ts/stats. Returns *client.Error on non-2xx.
func (c *Client) TSStats(ctx context.Context) (*TSStatsResult, error) {
	var res TSStatsResult
	if err := c.doURL(ctx, http.MethodGet, c.buildURL("/ts/stats"), nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// TSTimelineStats returns per-timeline diagnostics for timelineID.
//
// Hits GET /api/v1/.../ts/stats/{timeline_id}. Returns *client.Error
// on non-2xx -- notably 404 for an undefined timeline.
func (c *Client) TSTimelineStats(ctx context.Context, timelineID int64) (*TSTimelineStatsResult, error) {
	if timelineID <= 0 {
		return nil, fmt.Errorf("timelineID must be positive")
	}
	var res TSTimelineStatsResult
	u := c.buildURL(fmt.Sprintf("/ts/stats/%d", timelineID))
	if err := c.doURL(ctx, http.MethodGet, u, nil, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// TSDeleteTimelineData removes every event from timelineID, keeping
// the timeline's own definition -- distinct from TSDeleteTimeline
// (Wave 16 Stage 3), which removes both together.
//
// Hits DELETE /api/v1/.../ts/tl/{timeline_id}/data. Returns
// *client.Error on non-2xx.
func (c *Client) TSDeleteTimelineData(ctx context.Context, timelineID int64) error {
	if timelineID <= 0 {
		return fmt.Errorf("timelineID must be positive")
	}
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d/data", timelineID))
	return c.doURL(ctx, http.MethodDelete, u, nil, nil)
}

// TSPurgeTimelineRange removes events in [from, to) from timelineID,
// keeping everything outside that range. from/to are formatted
// explicitly as time.RFC3339 (not relying on time.Time's default JSON
// marshaling, which produces RFC3339Nano) -- confirmed directly
// against the server's own strict time.Parse(time.RFC3339, ...) call
// before choosing this, the same discipline TSRollupRun already
// follows for the identical reason.
//
// Hits POST /api/v1/.../ts/tl/{timeline_id}/data/purge. Returns
// *client.Error on non-2xx -- notably a refusal if to is not after
// from.
func (c *Client) TSPurgeTimelineRange(ctx context.Context, timelineID int64, from, to time.Time) error {
	if timelineID <= 0 {
		return fmt.Errorf("timelineID must be positive")
	}
	if !to.After(from) {
		return fmt.Errorf("to must be after from")
	}
	body := map[string]interface{}{
		"from": from.Format(time.RFC3339),
		"to":   to.Format(time.RFC3339),
	}
	u := c.buildURL(fmt.Sprintf("/ts/tl/%d/data/purge", timelineID))
	return c.doURL(ctx, http.MethodPost, u, body, nil)
}
