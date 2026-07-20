// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

import (
	"context"
	"fmt"
	"time"
)

// ErrDeleteNotSupported is returned by Store implementations that do not
// support event deletion. Callers must not assume the targeted event was
// removed when this error is received.
var ErrDeleteNotSupported = fmt.Errorf("ts: delete not supported by this backend")

// TimelineID is a uint32 identifier for a timeline within a tenant store.
// ID 0x00000000 is reserved; valid IDs are 0x00000001–0xFFFFFFFF.
// Widened from uint16 in the wave-1 per-primitive ID pass (@P wave 1)
// so per-tenant timeline counts scale past 65k — the ceiling that
// mid-market workloads reach long before a machine reaches its tenant
// count. On-disk Pebble key format widens from 2 to 4 bytes for the
// TimelineID prefix; done before production data exists so no migration
// is owed.
type TimelineID uint32

// MaxTimelineID is the highest valid timeline ID.
const MaxTimelineID TimelineID = 0xFFFFFFFF

// MinDims and MaxDims bound the number of dimensions a timeline may declare.
const (
	MinDims = 1
	MaxDims = 5
)

// TimelineConfig describes a timeline's declaration. Dims is immutable after
// the first write; Name and RetentionDays may be updated freely.
type TimelineConfig struct {
	Name          string // optional, human-readable label
	Dims          uint8  // 1–5, immutable after FirstWriteAt is set
	RetentionDays int    // 0 = use store-level default
	CreatedAt     time.Time
	FirstWriteAt  time.Time // zero until first event written; Dims locks here
}

// Event is a single timeseries record written to or read from a timeline.
type Event struct {
	Timeline TimelineID
	Dims     []uint64 // len must equal timeline's Dims
	Time     time.Time
	Nums     []float64 // optional, up to 7; nil means no numeric fields
	Payload  []byte    // optional, caller-defined opaque bytes
}

// RangeQuery retrieves events from a timeline over a time range.
// Dims is a leading prefix: 1 ≤ len(Dims) ≤ timeline.Dims.
type RangeQuery struct {
	Timeline      TimelineID
	Dims          []uint64
	From          time.Time
	To            time.Time
	Limit         int    // default 1000, max 10000
	Order         string // "asc" (default) or "desc"
	MaxScanEvents int    // 0 = use store/server default; aborts scan if exceeded
}

// LatestQuery retrieves the N most recent events matching a dimension prefix.
//
// Dims may be a leading prefix of the timeline's declared dimension count;
// all events matching that prefix are considered, across all remaining
// dimension values. This is intentional and useful for "latest across all
// sub-dimensions" queries.
//
// From and To are optional time bounds. When non-zero, only events within
// [From, To] are returned. This is applied as a Go-side filter, consistent
// with the partial-prefix time filter in QueryRange.
type LatestQuery struct {
	Timeline TimelineID
	Dims     []uint64
	N        int       // default 10, max 10000
	From     time.Time // optional lower bound (zero = unbounded)
	To       time.Time // optional upper bound (zero = unbounded)
}

// AggregateQuery computes an aggregate over a numeric field for all events
// matching the dimension prefix and time range.
type AggregateQuery struct {
	Timeline      TimelineID
	Dims          []uint64
	From          time.Time
	To            time.Time
	NumField      uint8         // index into Nums (0-based, max 6)
	Function      string        // "avg", "min", "max", "sum", "count"
	Interval      time.Duration // 0 = scalar result; > 0 = time-bucketed
	MaxScanEvents int           // 0 = no scan limit
	MaxBuckets    int           // 0 = no bucket limit; > 0 aborts when exceeded (XOLU-TS019)
}

// Bucket holds one time bucket of an aggregation result.
// RangeNumQuery is the query shape for single-field scalar range functions:
// RangeSum, RangeAvg, RangeMin, RangeMax, RangeCount.
// NumField is validated (0–6) and must correspond to a populated field.
type RangeNumQuery struct {
	Timeline      TimelineID
	Dims          []uint64
	From          time.Time
	To            time.Time
	NumField      uint8 // 0–6
	MaxScanEvents int   // 0 = no limit
}

// RangeAllQuery is the query shape for RangeAggregate, which computes
// statistics over all populated numeric fields in a single scan pass.
// No NumField — the result covers every field present in the matched events.
type RangeAllQuery struct {
	Timeline      TimelineID
	Dims          []uint64
	From          time.Time
	To            time.Time
	MaxScanEvents int // 0 = no limit
}

// RangeAggregateResult holds per-field statistics from a single scan pass.
// Fields[i] indicates whether num field i was present in at least one event;
// entries for absent fields carry zero values.
type RangeAggregateResult struct {
	Count  uint64
	Sums   [7]float64
	Avgs   [7]float64 // populated after scan: Sums[i]/Count; NaN if Count==0
	Mins   [7]float64
	Maxs   [7]float64
	Fields [7]bool // true if field i appeared in at least one event
}

// RangeFullQuery is the query shape for RangeFullAggregate, which computes
// sum, avg, min, max, count (via RangeAggregateResult) AND approximate
// quantiles for selected numeric fields — all in a single Pebble scan pass.
//
// Quantiles lists the desired quantile values, e.g. [0.5, 0.9, 0.99].
// Each value must be in [0, 1]; RangeFullAggregate returns an error otherwise.
//
// QuantileFields lists which numeric fields (0–6) should have quantiles
// computed. If nil, quantiles are computed for all seven fields, allocating
// ~16 KB of t-digest state per field. Callers should be explicit about which
// fields they need to avoid unnecessary allocation.
//
// If Quantiles is empty, RangeFullAggregate behaves identically to
// RangeAggregate (no digests allocated).
type RangeFullQuery struct {
	RangeAllQuery
	Quantiles      []float64 // quantile values to estimate, e.g. [0.5, 0.9, 0.99]
	QuantileFields []uint8   // num fields to estimate quantiles for (0–6); nil = all fields
}

// RangeFullResult holds the combined output of RangeFullAggregate.
// Aggregate contains the exact statistics (same as RangeAggregate).
// Quantiles[i][j] is the estimate for field i at Quantiles[j] from
// RangeFullQuery. A nil inner slice means field i was not requested or
// carried no events. The outer array is always length 7 (one slot per field).
type RangeFullResult struct {
	Aggregate RangeAggregateResult
	Quantiles [7][]float64
}

type Bucket struct {
	Time  time.Time
	Value float64
	Count uint64
}

// StoreStats holds aggregate diagnostics for the entire tenant store.
type StoreStats struct {
	Timelines int
	DiskBytes int64
}

// TimelineStats holds diagnostics for a single timeline.
// TotalEvents is derived from an in-memory counter that is persisted
// periodically to meta.json. After a crash without a clean Close, the
// counter may be stale; TotalEventsApproximate is always true for the
// current PebbleStore implementation.
type TimelineStats struct {
	TotalEvents            int64
	TotalEventsApproximate bool // always true; counter is eventually consistent
	OldestEvent            time.Time
	NewestEvent            time.Time
}

// StoreConfig holds configuration that is meaningful to any timeseries store
// backend. It is passed through the StoreFactory contract and must not contain
// engine-specific knobs.
type StoreConfig struct {
	DefaultRetentionDays int // store-level fallback; 0 = no expiry

	// SysmaskWidth is the immutable system/user partition width (@S),
	// applied ONLY at store creation. On reopen the persisted value in
	// meta.json wins and this field is ignored — the width can never
	// change for the life of a store. 0 (the default) means no system
	// reservation.
	SysmaskWidth SysmaskWidth

	// RollupCascadeDelete controls whether DeleteRollup automatically removes
	// all descendant definitions. When true (default), deleting a parent
	// removes its entire subtree. When false, deleting a definition that has
	// descendants returns an error; the caller must delete bottom-up.
	RollupCascadeDelete bool
}

// PebbleConfig holds LSM-tree tuning parameters specific to the Pebble
// storage engine. It is consumed only by NewPebbleStore / NewPebbleStoreFactory
// and has no meaning to other backends.
//
// Zero values are safe: NewPebbleStore applies sensible defaults for any
// field that is ≤ 0 or empty.
type PebbleConfig struct {
	MemtableSize          int    // bytes; default 67108864 (64 MB)
	BlockSize             int    // bytes; default 32768 (32 KB)
	Compression           string // "snappy", "zstd", or "none"; default "zstd"
	L0CompactionThreshold int    // L0 files before compaction; default 4
	MaxOpenFiles          int    // per-store file descriptor limit; default 500

	// Write coalescer tuning. Zero values fall back to package-level defaults
	// (10ms flush interval, 2000 max events). Only relevant when the coalescer
	// is enabled via dynconfig key ts.writecoal for the tenant or globally.
	CoalFlushIntervalMs int // flush window in milliseconds; default 10
	CoalMaxEvents       int // early-flush threshold in events; default 2000
}

// Store is the interface for a single tenant's timeseries backend.
// Implementations must be safe for concurrent use.
type Store interface {
	// Timeline management
	DefineTimeline(id TimelineID, cfg TimelineConfig) error
	// DefineSystemTimeline is the system-internal define path (@S §8):
	// it mints a system-region id and is not exposed on the tenant HTTP
	// surface. Refuses non-system ids (the symmetric guard).
	DefineSystemTimeline(id TimelineID, cfg TimelineConfig) error
	UpdateTimeline(id TimelineID, cfg TimelineConfig) error // name + RetentionDays only
	Timeline(id TimelineID) (TimelineConfig, bool)
	Timelines() []TimelineID

	// SysmaskWidth returns the store's immutable system/user partition
	// width (@S). 0 means no system reservation.
	SysmaskWidth() SysmaskWidth

	// Write
	Append(ctx context.Context, e Event) error
	AppendBatch(ctx context.Context, events []Event, maxBatch int) (int, error)

	// Delete removes the event identified by e from the store by computing its
	// encoded key from (Timeline, Dims, Time) and issuing a hard delete.
	// The event counter for the timeline is decremented on success.
	//
	// Implementors: returning nil from a backend that does not actually delete
	// the event is a silent correctness bug. The /commit endpoint calls Delete
	// and DeleteKeys as a rollback mechanism; a no-op implementation means
	// orphaned timeseries events will silently survive a SQLite failure and
	// become permanently inconsistent with entity state. Always return
	// ErrDeleteNotSupported if your backend cannot honour the deletion.
	Delete(ctx context.Context, e Event) error

	// DeleteKeys removes events by their pre-encoded keys. Keys must be produced
	// by EncodeKey; passing arbitrary byte slices produces undefined behaviour.
	// This is the preferred path when the caller already holds encoded keys
	// (e.g. during /commit rollback) and wants to avoid re-encoding overhead.
	// Because raw keys carry no Go-level timeline identity, implementations that
	// successfully delete via this method do NOT adjust event counters — the
	// counter is already documented as approximate (see storeMeta). Callers that
	// require an exact counter decrement should use Delete instead.
	//
	// Implementors: the same obligation as Delete applies. A silent no-op here
	// is not safe — it will cause /commit to silently succeed the rollback path
	// while leaving orphaned Pebble entries in place. Return ErrDeleteNotSupported
	// if your backend cannot delete by raw key; the /commit handler will log
	// XOLU-CM016 and alert operators rather than silently corrupting the store.
	DeleteKeys(ctx context.Context, keys [][]byte) error

	// Read
	QueryRange(ctx context.Context, q RangeQuery) ([]Event, error)
	Latest(ctx context.Context, q LatestQuery) ([]Event, error)

	// Aggregate — bucketed or scalar, single numeric field
	Aggregate(ctx context.Context, q AggregateQuery) ([]Bucket, error)

	// Single-field scalar range functions. Each performs one scan pass
	// over [From, To] for the given NumField. Kept alongside RangeAggregate
	// to allow direct performance comparison via benchmarks.
	RangeSum(ctx context.Context, q RangeNumQuery) (float64, error)
	RangeAvg(ctx context.Context, q RangeNumQuery) (float64, error)
	RangeMin(ctx context.Context, q RangeNumQuery) (float64, error)
	RangeMax(ctx context.Context, q RangeNumQuery) (float64, error)
	RangeCount(ctx context.Context, q RangeNumQuery) (uint64, error)

	// RangeAggregate computes Count, Sum, Avg, Min, Max for all seven
	// numeric fields simultaneously in a single scan pass.
	RangeAggregate(ctx context.Context, q RangeAllQuery) (*RangeAggregateResult, error)

	// RangeQuantile returns an approximate quantile estimate for a single
	// numeric field over [From, To] using a t-digest (compression=100).
	//
	// q must be in [0, 1]. Returns (0, nil) when no events carry NumField.
	//
	// Performance note: RangeQuantile performs its own full scan pass and
	// cannot be combined with RangeAggregate in a single pass. A caller
	// needing both sum/avg/min/max AND a quantile estimate for the same
	// range must issue two separate queries and pay for two scans.
	//
	// Future optimisation: if a single-pass combined result becomes
	// necessary, introduce a separate RangeFullQuery / RangeFullResult pair
	// rather than embedding *tdigest.TDigest into RangeAggregateResult.
	// Keeping the types separate preserves RangeAggregateResult as a plain
	// value type (no heap pointers, trivially copyable and serialisable) and
	// avoids surfacing the quantile estimator implementation as part of the
	// Store contract.
	RangeQuantile(ctx context.Context, q RangeNumQuery, quantile float64) (float64, error)

	// RangeMedian returns the approximate P50 for a single numeric field
	// over [From, To]. Syntax sugar over RangeQuantile(ctx, q, 0.5).
	// Carries the same two-scan limitation; see RangeQuantile.
	RangeMedian(ctx context.Context, q RangeNumQuery) (float64, error)

	// RangeFullAggregate computes exact sum/avg/min/max/count for all seven
	// numeric fields AND approximate quantiles for selected fields in a single
	// Pebble scan pass.
	//
	// This is the efficient alternative to calling RangeAggregate and
	// RangeQuantile separately when both are needed. RangeAggregateResult
	// is kept as a plain value type; digests are allocated during the scan
	// and discarded after quantile extraction, never stored in the result.
	//
	// If RangeFullQuery.Quantiles is empty the call is equivalent to
	// RangeAggregate with no additional cost.
	RangeFullAggregate(ctx context.Context, q RangeFullQuery) (*RangeFullResult, error)

	// Retention
	Purge(ctx context.Context) error

	// Retention configuration
	DefaultRetentionDays() int
	SetDefaultRetentionDays(days int) error

	// WriteConfig returns the write performance configuration for a timeline.
	// Returns the zero value (both flags false) if the timeline has no explicit
	// config set or if id is not defined.
	WriteConfig(id TimelineID) TimelineWriteConfig

	// SetWriteConfig updates the write performance configuration for a timeline.
	// The timeline must already exist. The config is persisted to disk so it
	// survives store restarts.
	SetWriteConfig(id TimelineID, cfg TimelineWriteConfig) error

	// Diagnostics
	Stats(ctx context.Context) (*StoreStats, error)
	TimelineStats(ctx context.Context, id TimelineID) (*TimelineStats, error)

	// Rollup management
	//
	// DefineRollup creates or updates a rollup definition on sourceTID.
	// Timeline 0 is rejected (XOLU-TS022). The destination must not already
	// be the target of another definition (XOLU-TS026). Adding a definition
	// that would create a cycle (XOLU-TS023) or exceed the depth limit
	// (XOLU-TS024) is rejected. Returns the assigned RollupID.
	DefineRollup(sourceTID TimelineID, def RollupDef) (RollupID, error)

	// GetRollup returns a specific rollup definition by its ID on sourceTID.
	GetRollup(sourceTID TimelineID, id RollupID) (RollupDef, error)

	// ListRollups returns all rollup definitions where sourceTID is the source.
	ListRollups(sourceTID TimelineID) ([]RollupDef, error)

	// DeleteRollup removes a rollup definition and stops its worker.
	// Data already written to the destination timeline is not affected.
	DeleteRollup(sourceTID TimelineID, id RollupID) error

	// RollupParent returns the rollup definition for which sourceTID is the
	// destination — i.e. the definition that feeds into this timeline.
	// Returns (RollupDef{}, false) if this timeline has no parent rollup.
	RollupParent(sourceTID TimelineID) (RollupDef, bool)

	// RollupTree returns the full rollup tree for this store, rooted at
	// timeline 0. Each node carries its definition and its children.
	RollupTree() *RollupTreeNode

	// RunRollup executes a rollup definition immediately for the given time
	// range, writing the results into the destination timeline. If from and
	// to are both zero, runs for the most recently closed bucket.
	// If cascade is true, after completing the specified bucket RunRollup
	// also runs all descendant definitions for the corresponding time windows,
	// walking down the tree in source→destination order. The worker goroutine
	// is started for this definition and all cascaded descendants if not
	// already running.
	RunRollup(ctx context.Context, sourceTID TimelineID, id RollupID, from, to time.Time, cascade bool) error

	// RollupStatus returns the operational status of a rollup definition.
	RollupStatus(sourceTID TimelineID, id RollupID) (RollupStatusReport, error)

	// Data deletion
	//
	// DeleteTimelineData removes all events from a timeline's Pebble key
	// range. The timeline definition is preserved. Timeline 0 is rejected.
	DeleteTimelineData(ctx context.Context, id TimelineID) error

	// DeleteTimeline removes a timeline's definition together with its event
	// data and its rollups. It is the inverse of DefineTimeline and is distinct
	// from DeleteTimelineData (which keeps the definition). Rollup cascade
	// follows RollupCascadeDelete: when off and the timeline still has rollups,
	// the call returns an error and changes nothing. Timeline 0 is rejected.
	DeleteTimeline(ctx context.Context, id TimelineID) error

	// PurgeTimelineRange removes events in [from, to] from a timeline.
	// Timeline 0 is rejected.
	PurgeTimelineRange(ctx context.Context, id TimelineID, from, to time.Time) error

	// Lifecycle
	Close() error
}

// RollupID uniquely identifies a rollup definition within a source timeline.
type RollupID string

// MaxRollupDepth is the maximum allowed depth of the rollup tree (not counting
// timeline 0 as the root). Controlled at runtime by dynconfig key
// ts.rollup_max_depth; this is the compiled-in default.
const MaxRollupDepth = 4

// RollupDef describes one rollup definition: read from SourceTID every
// BucketDuration, aggregate, and write into DestTID.
type RollupDef struct {
	ID             RollupID      `json:"id"`
	SourceTID      TimelineID    `json:"source_tid"`
	DestTID        TimelineID    `json:"dest_tid"`
	BucketDuration time.Duration `json:"bucket_duration"`
	// LateWindow is how long after a bucket closes to wait before computing
	// the rollup, to absorb late-arriving events. Default 0.
	LateWindow time.Duration `json:"late_window,omitempty"`
	// Running records whether the worker was active when last persisted.
	// Workers are not started automatically at DefineRollup time; they are
	// started explicitly via RunRollup (which starts the worker and cascades
	// to all descendants). On store reopen, only definitions with Running=true
	// restart their workers.
	Running   bool      `json:"running"`
	CreatedAt time.Time `json:"created_at"`
}

// RollupStatusReport carries the last-known operational state of a rollup worker.
type RollupStatusReport struct {
	ID            RollupID   `json:"id"`
	SourceTID     TimelineID `json:"source_tid"`
	DestTID       TimelineID `json:"dest_tid"`
	LastRunAt     time.Time  `json:"last_run_at,omitempty"`
	LastBucketEnd time.Time  `json:"last_bucket_end,omitempty"`
	EventsWritten int64      `json:"events_written"`
	LastError     string     `json:"last_error,omitempty"`
	Running       bool       `json:"running"`
}

// RollupTreeNode is one node in the tenant rollup tree as returned by RollupTree.
type RollupTreeNode struct {
	TID      TimelineID        `json:"tid"`
	Def      *RollupDef        `json:"def,omitempty"` // nil for the root (tid=0) and raw timelines
	Children []*RollupTreeNode `json:"children,omitempty"`
}

// TimelineWriteConfig holds the per-timeline performance configuration.
// The only remaining per-timeline flag is NoSync; write coalescing is
// controlled process-wide (or per-tenant) via dynconfig keys:
//
//	ts.writecoal           bool   — enable the write coalescer (default false)
//	ts.coal_flush_interval_ms  int — flush window in ms (default 10)
//	ts.coal_max_events     int    — early-flush threshold (default 2000)
//
// These keys are read from the tenant's dynconfig namespace first
// ("tenant.{name}"), falling back to "global" if absent.
type TimelineWriteConfig struct {
	// NoSync — when true, AppendBatch commits this timeline's events with
	// pebble.NoSync instead of pebble.Sync. The OS page cache is not flushed
	// to durable storage before the call returns. Data loss is possible if
	// the process crashes before the OS writes the WAL to disk.
	NoSync bool `json:"nosync"`
}

// StoreFactory creates a Store for a given data directory and tenant name.
// The tenantName is used to scope dynconfig lookups to the tenant's namespace.
type StoreFactory func(dir string, cfg StoreConfig, tenantName string) (Store, error)

// Manager manages per-tenant Store lifecycle.
type Manager interface {
	// Provision creates a timeseries store for a tenant.
	// tenantName is used to scope dynconfig lookups.
	Provision(ctx context.Context, tenantID uint16, tenantName string) error

	// StoreFor returns the Store for a tenant, or an error if not provisioned.
	StoreFor(tenantID uint16) (Store, error)

	// IsProvisioned reports whether a tenant has timeseries storage.
	IsProvisioned(tenantID uint16) bool

	// Close shuts down all stores.
	Close() error
}
