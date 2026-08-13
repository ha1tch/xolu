// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// types_ts.go — wire types for the /api/v1/tenant/{tenant}/ts/* surface's
// own read paths (XOT172, xoluman's own XM-2 report). Shapes mirror
// pkg/server/ts_handlers.go and ts_rollup_handlers.go byte-for-byte per
// the same Stage 2 convention types_bal.go documents. This is
// xoluman's own "minimum slice" for a first, useful read-only ts
// viewer -- TSListTimelines, TSGetTimeline, TSQueryRange, TSRollupList,
// TSRollupGet -- not the full read+write surface their report also
// laid out; write-side methods (TSDefineTimeline, TSAppend, rollup
// def/run, etc.) are a real, separate follow-up, not covered here.
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
