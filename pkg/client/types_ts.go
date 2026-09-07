// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// types_ts.go — wire types for the /api/v1/tenant/{tenant}/ts/* surface,
// both read and write. Shapes mirror pkg/server/ts_handlers.go and
// ts_rollup_handlers.go byte-for-byte per the same Stage 2 convention
// types_bal.go documents. Write-side coverage (TSAppend, TSBatchAppend)
// added XOT212 (wave 15) -- this file's own original header named that
// gap explicitly as "a real, separate follow-up, not covered here";
// this is that follow-up. Rollup definition/run methods remain out of
// scope, still a separate follow-up beyond this one.
//
// TimelineID and RollupID travel as plain uint32/string, not
// pkg/timeseries's own named types (timeseries.TimelineID,
// timeseries.RollupID) -- this client package has no dependency on
// xolu's own internal packages anywhere else (types_bal.go, etc. all
// use plain Go types), and there's no reason to start one here.

import "time"

// TSTimeline is the response shape for TSListTimelines/TSGetTimeline.
// FirstWriteAt is nil for a timeline that has never received an event.
type TSTimeline struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name,omitempty"`
	Dims          int        `json:"dims"`
	RetentionDays int        `json:"retention_days"`
	CreatedAt     time.Time  `json:"created_at"`
	FirstWriteAt  *time.Time `json:"first_write_at,omitempty"`
}

// TSQueryRangeRequest is TSQueryRange's own request. Dims is required
// and must be non-empty -- confirmed directly against the server's own
// handler (HandleTSQueryRangePost), which rejects an empty Dims with
// XOLU-TS007 before ever reaching the store. Limit and Order are
// optional; the server defaults Limit to 1000 (capped server-side at
// its own configured maximum) and Order to "asc" when left zero-value.
type TSQueryRangeRequest struct {
	Timeline uint64    `json:"timeline"`
	Dims     []uint64  `json:"dims"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	Limit    int       `json:"limit,omitempty"`
	Order    string    `json:"order,omitempty"` // "asc" (default) or "desc"
}

// TSEvent is one event as returned by TSQueryRange. Payload is
// whatever JSON value the event was appended with (or nil) -- the
// server carries it through opaquely, this client does the same.
type TSEvent struct {
	Timeline int64       `json:"timeline"`
	Dims     []uint64    `json:"dims"`
	Time     time.Time   `json:"time"`
	Nums     []float64   `json:"nums,omitempty"`
	Payload  interface{} `json:"payload,omitempty"`
}

// TSQueryRangeResult is TSQueryRange's own response.
type TSQueryRangeResult struct {
	Count  uint64    `json:"count"`
	Events []TSEvent `json:"events"`
}

// TSRollup is the response shape for TSRollupList/TSRollupGet.
// LateWindow is empty when the rollup was defined with no late-data
// grace window. BucketDuration/LateWindow travel as Go duration
// strings (e.g. "1h0m0s"), matching the server's own
// time.Duration.String() encoding exactly -- not re-parsed or
// reformatted by this client. SourceTID/DestTID are int64 on the
// wire specifically -- the server casts its own internal uint32
// TimelineID to int64 before serializing (tsRollupDefResponse), so
// this matches the actual JSON type, not the internal one.
type TSRollup struct {
	ID             string    `json:"id"`
	SourceTID      int64     `json:"source_tid"`
	DestTID        int64     `json:"dest_tid"`
	BucketDuration string    `json:"bucket_duration"`
	LateWindow     string    `json:"late_window,omitempty"`
	Running        bool      `json:"running"`
	CreatedAt      time.Time `json:"created_at"`
}

// TSBatchAppendResult is TSBatchAppend's own response. Checked
// directly against pkg/timeseries's own AppendBatch, not assumed from
// the response shape's own apparent implication of partial success:
// every failure path there returns (0, err) before any event is
// written, and the only success path returns (len(events), nil) --
// there is currently no code path producing Accepted < Total. This
// client does not assume that invariant holds forever (a future
// AppendBatch could genuinely support partial acceptance without this
// wire shape needing to change), so Failed/Accepted are still
// surfaced as returned rather than this client asserting Accepted ==
// Total itself.
type TSBatchAppendResult struct {
	Total    int `json:"total"`
	Accepted int `json:"accepted"`
	Failed   int `json:"failed"`
}

// types_ts.go, continued (XOT217, wave 16 Stage 3): timeline
// lifecycle and sync -- verified directly against
// pkg/server/ts_handlers.go's own request/response structs and
// handler bodies before being written here.

// TSProvisionResult is POST /provision's own response -- a one-time
// per-tenant setup call. Timeseries is always the literal string
// "enabled" on success.
type TSProvisionResult struct {
	TenantID   string `json:"tenant_id"`
	Timeseries string `json:"timeseries"`
}

// TSDefineTimelineRequest defines a new timeline. Dims is fixed for
// the timeline's own lifetime -- confirmed directly against
// HandleTSUpdateTimeline, which silently ignores this field if sent
// on an update (the store's own UpdateTimeline call never reads it),
// which is exactly why TSUpdateTimelineRequest below doesn't expose
// it at all rather than accept a value that would be quietly dropped.
type TSDefineTimelineRequest struct {
	ID            int64  `json:"id"`
	Name          string `json:"name,omitempty"`
	Dims          int    `json:"dims"`
	RetentionDays int    `json:"retention_days,omitempty"`
}

// TSUpdateTimelineRequest updates a timeline's own mutable fields --
// Name and RetentionDays only. Dims is deliberately not a field here
// at all (see TSDefineTimelineRequest's own doc comment for why).
type TSUpdateTimelineRequest struct {
	Name          string `json:"name,omitempty"`
	RetentionDays int    `json:"retention_days,omitempty"`
}

// TSSyncStatus is the shape every sync read/write returns. NoSync
// governs WAL fsync durability, not data replication or mirroring
// despite the name's own first impression: NoSync=false (the
// default, "sync on") waits for fsync before an append returns --
// crash-durable, slower. NoSync=true ("sync off") returns
// immediately without waiting -- faster, but a crash before the
// kernel's own dirty-page writeback (typically under a second) can
// lose the most recent writes.
type TSSyncStatus struct {
	TimelineID int64 `json:"timeline_id"`
	NoSync     bool  `json:"nosync"`
}

// TSLatestRequest is TSLatest's own request -- N defaults to 10
// server-side if 0. From/To are optional bounds, checked directly
// against the server's own "parse if present, ignore gracefully if
// absent" handling -- zero-value time.Time fields here are correctly
// treated as "no bound," not an error.
type TSLatestRequest struct {
	Timeline int64
	Dims     []uint64
	N        int
	From     time.Time
	To       time.Time
}

// types_ts.go, continued (XOT219, wave 16 Stage 5): rollup
// completeness -- define, parent, delete, run, status, and the
// tenant-wide tree. Verified directly against
// pkg/server/ts_rollup_handlers.go's own request/response structs
// before being written here.

// TSRollupDefineRequest defines a rollup on a source timeline.
// BucketDuration and LateWindow are Go duration strings (e.g. "5m"),
// not time.Duration -- matching the server's own wire shape exactly,
// which parses them with time.ParseDuration.
type TSRollupDefineRequest struct {
	DestTID        int64  `json:"dest_tid"`
	BucketDuration string `json:"bucket_duration"`
	LateWindow     string `json:"late_window,omitempty"`
}

// TSRollupTreeNode is one node in the tenant-wide rollup tree
// (GET /rollup/tree) -- Def is nil for a timeline with no rollup
// definition of its own; Children is nil/empty for a leaf.
type TSRollupTreeNode struct {
	TID      int64               `json:"tid"`
	Def      *TSRollup           `json:"def,omitempty"`
	Children []*TSRollupTreeNode `json:"children,omitempty"`
}

// TSRollupRunRequest manually triggers a rollup for a bounded range.
// From/To are optional -- zero-value time.Time means "no bound," the
// server's own default. Cascade, when true, also runs every
// descendant rollup definition for the corresponding time windows.
type TSRollupRunRequest struct {
	From    time.Time
	To      time.Time
	Cascade bool
}

// TSRollupStatusReport is a rollup worker's own operational status.
type TSRollupStatusReport struct {
	ID            string    `json:"id"`
	SourceTID     int64     `json:"source_tid"`
	DestTID       int64     `json:"dest_tid"`
	LastRunAt     time.Time `json:"last_run_at,omitempty"`
	LastBucketEnd time.Time `json:"last_bucket_end,omitempty"`
	EventsWritten int64     `json:"events_written"`
	LastError     string    `json:"last_error,omitempty"`
	Running       bool      `json:"running"`
}

// types_ts.go, continued (XOT220, wave 16 Stage 6): aggregates.
// Verified directly against pkg/server/ts_handlers.go's own request/
// response structs before being written here -- this stage was
// flagged in advance as carrying the most shape-uncertainty of the
// five ts stages, and that caution paid for itself: interval turned
// out to be a strict 9-value enum, not a general duration string, and
// RangeAggregate/FullAggregate compute a genuinely different thing
// than Aggregate (all seven numeric fields at once, no function
// selector) rather than being thin variants of it.

// TSAggregateRequest computes Function over NumField (0-6), optionally
// bucketed by Interval. Interval, when set, must be exactly one of
// "1m", "5m", "15m", "30m", "1h", "6h", "12h", "1d", "7d" -- checked
// directly against the server's own parseInterval, which recognizes
// only these nine literal strings, not an arbitrary Go duration.
// Leave Interval empty for a single scalar result over the whole
// range instead of buckets.
type TSAggregateRequest struct {
	Timeline int64     `json:"timeline"`
	Dims     []uint64  `json:"dims"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
	NumField int       `json:"num_field"`
	Function string    `json:"function"`
	Interval string    `json:"interval,omitempty"`
}

// TSBucket is one time bucket in a TSAggregateResult's own Buckets.
type TSBucket struct {
	Time  time.Time `json:"time"`
	Value float64   `json:"value"`
	Count uint64    `json:"count"`
}

// TSAggregateResult is exactly one of two shapes, determined by
// whether the request set Interval: Buckets is populated for a
// bucketed request (Value/Count/From/To all nil); Value/Count/From/To
// are populated for a scalar request (Buckets nil/empty). Never both.
type TSAggregateResult struct {
	Timeline int64      `json:"timeline"`
	NumField int        `json:"num_field"`
	Function string     `json:"function"`
	Interval string     `json:"interval,omitempty"`
	Buckets  []TSBucket `json:"buckets,omitempty"`
	Value    *float64   `json:"value,omitempty"`
	Count    *uint64    `json:"count,omitempty"`
	From     *time.Time `json:"from,omitempty"`
	To       *time.Time `json:"to,omitempty"`
}

// TSRangeAggregateRequest computes count/sum/avg/min/max for all
// seven numeric fields in one pass -- more efficient than several
// TSAggregate calls when multiple fields are needed. Does not support
// time bucketing; use TSAggregate for that.
type TSRangeAggregateRequest struct {
	Timeline int64     `json:"timeline"`
	Dims     []uint64  `json:"dims"`
	From     time.Time `json:"from"`
	To       time.Time `json:"to"`
}

// TSRangeAggregateResult is TSRangeAggregate's own response. Fields[i]
// is true iff numeric field i had at least one event in range; Sums/
// Avgs/Mins/Maxs[i] are meaningless (typically 0) when Fields[i] is
// false, matching the server's own convention exactly rather than
// this client inventing a different sentinel.
type TSRangeAggregateResult struct {
	Timeline int64      `json:"timeline"`
	Count    uint64     `json:"count"`
	Fields   [7]bool    `json:"fields"`
	Sums     [7]float64 `json:"sums"`
	Avgs     [7]float64 `json:"avgs"`
	Mins     [7]float64 `json:"mins"`
	Maxs     [7]float64 `json:"maxs"`
}

// TSFullAggregateRequest combines TSRangeAggregate's own exact
// statistics with approximate quantile estimates in one pass.
// QuantileFields selects which numeric fields (0-6) get quantile
// estimates -- nil means all seven. An empty/nil Quantiles is
// equivalent to plain TSRangeAggregate at no extra cost (no quantile
// sketch allocated server-side).
type TSFullAggregateRequest struct {
	Timeline       int64     `json:"timeline"`
	Dims           []uint64  `json:"dims"`
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
	Quantiles      []float64 `json:"quantiles,omitempty"`       // e.g. [0.5, 0.9, 0.99]
	QuantileFields []uint8   `json:"quantile_fields,omitempty"` // nil = all fields
}

// TSFullAggregateResult is TSFullAggregate's own response. Fields/
// Sums/Avgs/Mins/Maxs follow TSRangeAggregateResult's own seven-
// element convention exactly. Quantiles[i] is nil when field i had no
// events or wasn't in the request's own QuantileFields; otherwise the
// slice holds one estimate per entry in the request's own Quantiles,
// in the same order.
type TSFullAggregateResult struct {
	Timeline  int64        `json:"timeline"`
	Count     uint64       `json:"count"`
	Fields    [7]bool      `json:"fields"`
	Sums      [7]float64   `json:"sums"`
	Avgs      [7]float64   `json:"avgs"`
	Mins      [7]float64   `json:"mins"`
	Maxs      [7]float64   `json:"maxs"`
	Quantiles [7][]float64 `json:"quantiles"`
}

// types_ts.go, continued (XOT221, wave 16 Stage 7, final): retention,
// stats, and data lifecycle. Verified directly against
// pkg/server/ts_handlers.go's and ts_rollup_handlers.go's own
// request/response structs before being written here.

// TSTimelineRetention is one entry in TSRetentionResult's own
// Timelines.
type TSTimelineRetention struct {
	ID            int64  `json:"id"`
	Name          string `json:"name,omitempty"`
	RetentionDays int    `json:"retention_days"`
}

// TSRetentionResult is GET /retention's own response -- the tenant's
// own store-level default plus every timeline's own current setting.
// Per-timeline retention is set via TSUpdateTimeline, not here.
type TSRetentionResult struct {
	DefaultRetentionDays int                   `json:"default_retention_days"`
	Timelines            []TSTimelineRetention `json:"timelines"`
}

// TSPatchRetentionResult is PATCH /retention's own response.
type TSPatchRetentionResult struct {
	DefaultRetentionDays int    `json:"default_retention_days"`
	Status               string `json:"status"`
}

// TSStatsResult is GET /stats's own response -- store-level
// diagnostics for the whole tenant.
type TSStatsResult struct {
	TenantID  string `json:"tenant_id"`
	Timelines int    `json:"timelines"`
	DiskBytes int64  `json:"disk_bytes"`
}

// TSTimelineStatsResult is GET /stats/{timeline_id}'s own response.
// TotalEventsApproximate, when true, means TotalEvents is an
// estimate, not an exact count -- matching the server's own
// documented trade-off for a store that doesn't track exact counts
// cheaply. OldestEvent/NewestEvent are nil for a timeline with no
// events at all.
type TSTimelineStatsResult struct {
	TimelineID             int64      `json:"timeline_id"`
	Name                   string     `json:"name,omitempty"`
	TotalEvents            int64      `json:"total_events"`
	TotalEventsApproximate bool       `json:"total_events_approximate"`
	OldestEvent            *time.Time `json:"oldest_event,omitempty"`
	NewestEvent            *time.Time `json:"newest_event,omitempty"`
}
