// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package config

import (
	"github.com/ha1tch/xolu/pkg/authconfig"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
)

// Config holds application configuration
// APIKeyGrant binds a single API key to the tenants it may act on under
// TenantAuthMode "scoped". Exactly one of Tenants or Admin should be set.
//
// The definition moved to pkg/authconfig in the T-19 auth extraction;
// this alias preserves every existing reference.
type APIKeyGrant = authconfig.APIKeyGrant

// S3KeyGrant binds an S3 access key (and its secret) to the tenants it may act
// on. Used by the S3 gateway under TenantAuthMode "scoped": the access key
// identifies the caller, the secret authenticates it, and Tenants/Admin
// authorise which tenants it may reach. Without a matching grant, a scoped S3
// request is denied — the access-key string is no longer trusted as the tenant
// name. Exactly one of Tenants or Admin should be set.
type S3KeyGrant struct {
	AccessKey string   // the SigV4 access key ID
	Secret    string   // the secret used to authenticate the caller
	Tenants   []string // tenant names this key may act on
	Admin     bool     // true → authorised for any tenant
}

type Config struct {
	// Server configuration
	Host string
	Port int

	// Storage configuration
	StorageType string // "sqlite" (only supported backend since v0.9.9)
	BaseDir     string
	SchemaDir   string
	Schema      string

	// Cache configuration
	CacheType   string // "memory" or "redis"
	CacheTTL    int    // seconds
	RedisHost   string
	RedisPort   int
	CacheSize   int
	CacheShards int // Number of shards for in-memory cache (0 = default 16; must be power of 2)

	// API v2 configuration
	// APIV2Enabled gates the entire /api/v2 surface. False by default.
	// Meta subsystem (S3)
	// MetaMaxValueBytes is the maximum size of a single metadata value in bytes.
	MetaMaxValueBytes int // default 65536 (64 KB)
	// MetaGCEnabled enables the metadata TTL sweeper (default true when v2 enabled).
	MetaGCEnabled bool
	// MetaGCIntervalSecs is how often the metadata GC sweep runs (default 300).
	MetaGCIntervalSecs int
	// DxpGCEnabled enables the dxp_txn deadline sweeper (default true when
	// v2 enabled) — marks instances stuck 'active' past their own deadline
	// (a crash or unrecovered panic mid-dispatch, T-100) as 'expired'.
	DxpGCEnabled bool
	// DxpGCIntervalSecs is how often the dxp sweep runs (default 60 — dxp's
	// own phase_ttl deadlines run in seconds-to-minutes, shorter than
	// meta's TTLs, so a stuck instance is worth noticing sooner).
	DxpGCIntervalSecs int
	// DxpTxnRetentionSecs is how long a dxp_txn instance is kept after
	// reaching a terminal state (committed/released/expired) before the
	// same dxp-gc sweep purges it — direct instruction (2026-07-31):
	// "keep tombstones for a configurable period... defaults to 48
	// hours before they're gone." Measured from created_at: dispatch is
	// synchronous, so creation and termination are the same instant for
	// every ordinary instance; only T-100's own sweep-caught crash
	// residue terminates later than it was created, and retention from
	// created_at is still the honest, simpler choice there — no
	// separate terminal_at column exists, and this is a coarse cleanup
	// window, not a tight SLA. Default 172800 (48h).
	DxpTxnRetentionSecs int
	// Set XOLU_API_V2_ENABLED=true to enable experimental v2 functionality.
	APIV2Enabled bool

	// Graph configuration
	GraphEnabled         bool
	GraphMode            string // "flat" (default) or "disabled"
	AsyncJobRetentionTTL int    // seconds; how long completed async job records are kept (default 86400)
	OQLQueryCacheTTL     int    // seconds; HTTP-layer OQL result cache; 0 = disabled (default 30)
	GraphQueryCacheTTL   int    // seconds; HTTP-layer Sulpher result cache; 0 = disabled (default 30)
	GraphMaxVisitedNodes int    // Max nodes visited during traversal (0 = default 10000)
	GraphMaxResults      int    // Max result paths returned (0 = no limit)
	GraphCycleDetection  string // "warn", "error", "ignore"
	GraphCycleCheckLimit int    // BFS budget for cycle detection (0 = use default 512)

	// Full-text search
	FullTextEnabled bool

	// Commit endpoint behaviour.
	//
	// When StrictCommit is true (the default), POST /commit runs the same
	// schema validation and graph cycle prechecks as the normal write
	// endpoints (save, create, patch) before executing the storage
	// transaction. Set to false only when the caller is trusted
	// infrastructure that manages its own invariants and the extra
	// validation overhead is undesirable.
	//
	// 	// has been deprecated for production use; enforcing this at the HTTP
	// layer prevents silent correctness problems.
	StrictCommit bool

	// Query configuration
	MaxQueryDepth   int
	MaxEmbedDepth   int
	RefEmbedDepth   int
	DefaultPageSize int

	// Query guardrails — limits that prevent runaway queries from becoming
	// outages. All limits are enforced server-side; client timeouts alone
	// are not sufficient because they abandon work without freeing resources.
	QueryTimeout          int // Seconds; max execution time for OQL/Sulpher queries (0 = use default 30)
	QueryMaxRows          int // Max rows returned by a single query (0 = use default 10000)
	QueryMaxScanRows      int // Max rows scanned before aborting (0 = use default 100000)
	QueryMaxResponseBytes int // Max JSON response size in bytes (0 = use default 10MB)

	// Entity configuration
	PatchNullBehavior string // "store" or "delete"
	MaxEntitySize     int    // bytes

	// Cascade delete configuration
	CascadingDelete     bool
	MaxCascadeDeletions int
	MaxCascadeWork      int

	// Debug
	Debug      bool
	DebugLocks bool
	// LogLevel sets the minimum log level: "debug", "info", "warn", "error".
	// XOLU_LOG_LEVEL is the primary env var. XOLU_DEBUG=true is a legacy alias
	// that maps to LogLevel "debug". When both are set, XOLU_LOG_LEVEL wins.
	// Defaults to "info" when neither is set.
	LogLevel string

	// NoAscii suppresses the ASCII art box at startup (XOLU_NO_ASCII=true).
	// Useful in container environments or when stdout is a log pipeline.
	NoAscii bool

	// NoStartupText suppresses the startup configuration summary (XOLU_NO_STARTUP_TEXT=true).
	// NoAscii and NoStartupText are independent; either or both may be set.
	NoStartupText bool

	// Authentication
	AuthType  string   // "none", "jwt", "apikey", "bearertoken"
	JWTSecret string   // Secret for JWT validation
	JWTIssuer string   // Expected issuer claim
	APIKeys   []string // Valid API keys (comma-separated in env)
	// InternalToken is the shared secret for the "bearertoken" auth type.
	// The incoming Authorization: Bearer <token> value is compared against
	// this using subtle.ConstantTimeCompare. Typically a 32-byte hex string
	// generated with `openssl rand -hex 32`. Set via XOLU_INTERNAL_TOKEN.
	InternalToken    string
	AuthExcludePaths []string // Paths excluded from auth (e.g., /health)

	// Rate limiting
	RateLimitEnabled bool
	RateLimitRate    int // Requests per window
	RateLimitWindow  int // Window in seconds
	RateLimitByIP    bool

	// TrustedProxies is a comma-separated list of CIDR ranges whose
	// requests are permitted to override the observed peer IP via
	// X-Forwarded-For headers. When empty (default), header-based IP
	// spoofing is refused and the TCP peer is authoritative. See T-38.
	TrustedProxies string `json:"trusted_proxies"`
	RateLimitByKey   bool // Rate limit by API key or JWT subject

	// Metrics
	MetricsEnabled bool
	// MetricsPort, when > 0, starts a dedicated listener that serves only
	// /metrics on the given port. The main API port will no longer expose
	// /metrics, allowing Prometheus scrape traffic to be separated from
	// operational reads and writes. When 0 (the default), /metrics is served
	// on the main port as before. Controlled by XOLU_METRICS_PORT.
	MetricsPort int
	// MetricsHost sets the bind address for the dedicated metrics listener.
	// Only meaningful when MetricsPort > 0. When unset, the value is derived
	// from Host: if Host is a real address (not 0.0.0.0 or ::), it is
	// inherited; otherwise the metrics listener binds to 0.0.0.0. When
	// explicitly set, it always takes precedence. Controlled by XOLU_METRICS_HOST.
	MetricsHost string

	// Dynamic configuration — runtime-settable key/value store backed by a
	// JSON file. Settings take effect on the next reload interval without
	// restarting the server.

	// DynConfigEnabled enables the dynamic configuration system.
	// When false, DynConfigFile and DynConfigAPIEnabled are ignored.
	// Controlled by XOLU_DYNCONFIG_ENABLED.
	DynConfigEnabled bool
	// DynConfigFile is the path to the JSON file that backs the dynamic
	// configuration store. Defaults to {BaseDir}/dynconfig.json.
	// Controlled by XOLU_DYNCONFIG_FILE.
	DynConfigFile string
	// DynConfigReloadSecs is the interval between file reloads in seconds.
	// Must be > 0 when DynConfigEnabled is true. Default: 30.
	// Controlled by XOLU_DYNCONFIG_RELOAD_INTERVAL.
	DynConfigReloadSecs int
	// DynConfigAPIEnabled exposes the admin API endpoints for reading and
	// writing dynamic settings at runtime. When false, settings can only
	// be changed by editing the file directly.
	// Default: false. Controlled by XOLU_DYNCONFIG_API_ENABLED.
	DynConfigAPIEnabled bool

	// CORS
	// CORSOrigins lists allowed origins for cross-origin requests. Empty
	// disables CORS entirely. Use "*" for development only. When combined
	// with cookie-based auth, restrict to specific trusted domains — see
	// the security note on corsMiddleware in server.go.
	CORSOrigins []string

	// Performance tuning — SQLite
	SQLiteMaxOpenConns        int // Max open write connections (0 = backend default)
	SQLiteMaxIdleConns        int // Max idle write connections (0 = backend default)
	SQLiteReadPoolSize        int // Max open read connections (0 = backend default)
	SQLiteContentionThreshold int // Adaptive lock threshold 0-100 (0 = disabled, 95 = default)
	SQLiteBusyTimeout         int // Milliseconds to wait on locked database (0 = use default 5000)
	SQLiteCacheSize           int // Page cache size in KB (0 = use default 2000)

	// SQLitePerFileTenants controls whether each tenant gets its own SQLite
	// database file. When false (default), all tenants share one file and are
	// isolated by the tenant_id column. When true, each tenant gets its own
	// file. Paths are derived from BaseDir by the invariant layout (see
	// pkg/storelayout):
	//
	//   per-file:  tenant 0 -> <BaseDir>/t0000/store/xolu.db
	//              tenant N -> <BaseDir>/tXXXX/store/xolu.db
	//   shared:    all tenants -> <BaseDir>/shared/store/xolu.db
	//
	// Both modes use the same per-tenant table naming (t<XXXX>_* tables); the
	// flag governs only file placement. Choose at deployment time and do not
	// change while data exists — migration requires an explicit export/import.
	// Ignored when StorageType is not "sqlite".
	SQLitePerFileTenants bool

	// Performance tuning — query planner
	// PerformanceProfile selects hardware-specific thresholds for the
	// query planner's push-down decisions. Accepted values:
	//   "auto"      - Run a ~200ms startup micro-benchmark to calibrate (default)
	//   "edge"      - ARM SBCs, gateways (1-2 cores, 1-4 GB RAM)
	//   "vps"       - Small cloud instances (1-2 vCPU, 2-8 GB RAM)
	//   "dedicated" - Bare metal or large instances (4+ cores, 16+ GB)
	PerformanceProfile string

	// Performance tuning — Redis
	RedisPoolSize     int // Redis connection pool size (0 = use default 50)
	RedisMinIdleConns int // Redis minimum idle connections (0 = use default 10)

	// Performance tuning — HTTP server
	HTTPReadTimeout    int // Seconds; max duration for reading request (0 = no timeout)
	HTTPWriteTimeout   int // Seconds; max duration for writing response (0 = no timeout)
	HTTPIdleTimeout    int // Seconds; max duration for keep-alive idle (0 = no timeout)
	HTTPRequestTimeout int // Seconds; per-request middleware timeout (0 = use default 60)

	// Multi-tenancy
	// TenantMode controls tenant isolation behaviour:
	//   "path"   - Tenant routes available with auto-registration, non-tenant routes
	//              use tenant 0 (default)
	//   "strict" - All entity requests require tenant context; non-tenant routes
	//              return 403; tenants must be pre-registered
	TenantMode string

	// TenantAuthMode controls whether an authenticated caller's identity must be
	// authorised for the tenant it requests:
	//   "open"   - Default. Any authenticated caller may act on any tenant.
	//              Correct for single-tenant, trusted-gateway, and edge deployments.
	//   "scoped" - The caller's identity (JWT grant claim, API-key grant, or
	//              bearer=admin) must authorise the requested tenant; otherwise 403.
	//              Requires TenantMode "strict" (the unprefixed tenant-0 routes are
	//              disabled so there is no unauthorised default-tenant path).
	// See docs/proposals/tenant-access-control.md.
	TenantAuthMode string

	// APIKeyGrants maps individual API keys to the tenants they may act on, for
	// TenantAuthMode "scoped". Under "scoped", a key present only in the flat
	// APIKeys list (with no grant here) is rejected. Ignored under "open".
	APIKeyGrants []APIKeyGrant

	// TenantAutoRegister controls whether unknown tenant names are automatically
	// registered on first access. When true, any request to /api/v1/tenant/{name}/...
	// will create the tenant if it doesn't exist. When false (default), unknown
	// tenants return 404. Ignored when TenantMode is "strict".
	TenantAutoRegister bool

	// Timeseries storage (Pebble-backed, requires StorageType = "sqlite")
	TimeseriesEnabled        bool
	TSMemtableSize           int    // bytes, default 67108864 (64 MB)
	TSBlockSize              int    // bytes, default 32768 (32 KB)
	TSCompression            string // "snappy", "zstd", or "none"
	TSL0CompactionThreshold  int    // L0 files before compaction trigger, default 4
	TSMaxOpenFiles           int    // per-tenant Pebble file limit, default 500
	TSDefaultRetentionDays   int    // default 90
	TSCompactionIntervalSecs int    // retention sweep interval in seconds, default 3600
	TSRetentionEnabled       bool   // run background retention goroutine, default false

	// Timeseries query guardrails
	TSQueryTimeoutSecs    int // max execution time per query (0 = default 30s)
	TSMaxQueryEvents      int // max events returned by QueryRange/Latest (0 = default 10000)
	TSMaxScanEvents       int // max events scanned before aborting (0 = default 500000)
	TSMaxRangeDays        int // max From→To window in days (0 = default 366)
	TSMaxBatchSize        int // max events per batch append (0 = default 5000)
	TSMaxResponseBytes    int // max JSON response size in bytes (0 = default 10MB)
	TSMaxAggregateBuckets int // max buckets in a windowed aggregate (0 = default 10000)

	// Timeseries write coalescer tuning.
	// XOLU_TS_COAL_FLUSH_INTERVAL_MS controls how long the coalescer waits
	// before committing accumulated events (default 10ms). Lower values reduce
	// write latency jitter; higher values increase the number of events sharing
	// each fsync at the cost of additional latency. Only relevant when the
	// coalescer is enabled via dynconfig key ts.writecoal.
	TSCoalFlushIntervalMs int // default 10
	// XOLU_TS_COAL_MAX_EVENTS controls the maximum number of events the
	// coalescer accumulates before forcing an early flush (default 2000).
	// Prevents unbounded memory use if the arrival rate is very high.
	TSCoalMaxEvents int // default 2000

	// XOLU_TS_ROLLUP_CASCADE_DELETE controls whether deleting a rollup
	// definition automatically deletes all descendant definitions and stops
	// their workers. When true (default), a single DELETE on a parent removes
	// the entire subtree rooted at that definition. When false, deleting a
	// definition that has descendants returns an error; the caller must delete
	// bottom-up manually.
	TSRollupCascadeDelete bool // default true

	// Blob store — content-addressed object storage on the local filesystem.
	// Blobs never enter SQLite; only their SHA-256 reference is stored in
	// entity fields. Blobs are per-tenant, organised uniformly with the
	// timeseries plane:
	//   {BaseDir}/t{XXXX}/blobs/{xx}/{sha256hex}
	// where t{XXXX} is the tenant directory (tenant 0 included) and {xx} is the
	// first two hex characters of the SHA (git-style prefix). Key aliases live
	// in {BaseDir}/t{XXXX}/blobs/.keys.
	//
	// BlobEnabled must be true for either the JSON blob API (/api/v1/blob/)
	// or the S3-compatible API to function. When false, both are disabled
	// and any attempt to use them returns 501.
	BlobEnabled bool
	// BlobDir is retained for configuration compatibility but is no longer
	// consulted: blobs are placed per tenant under {BaseDir}/t{XXXX}/blobs by
	// the blob manager (see pkg/storelayout.TenantBlobDir). Deprecated.
	BlobDir string

	// CalEnabled controls whether the /api/v2/cal/* endpoints are wired.
	// When true, the server initialises a cal.Manager rooted at BaseDir
	// and registers the four calendar operations (check, openings,
	// propose, confirm) under the v2 tenant scope. When false, the
	// routes are omitted entirely and any attempt to reach them returns
	// the standard 404.
	//
	// Introduced with T-18 in v0.14.7. Default false, matching the v2
	// subsystem posture: opt-in until stable.
	CalEnabled bool

	// BalEnabled controls whether the /api/v2/bal/* endpoints are wired
	// (@B; opt-in until stable, like cal).
	BalEnabled bool
	// BlobMaxSize is the maximum blob size in bytes accepted by PUT/POST.
	// 0 = use default (67108864 = 64 MB). Applies to both the JSON and S3 APIs.
	BlobMaxSize int
	// BlobMaxTotalBytes is the per-tenant total storage cap in bytes.
	// A Put that would push a tenant's total bytes over this limit is rejected
	// with XOLU-BL006. Checked against the sampler cache — a soft cap, not a
	// hard guarantee. 0 means no limit (default).
	BlobMaxTotalBytes int64

	// S3-compatible API — exposes the blob store via an S3-like interface on
	// a dedicated listener so that existing S3 client libraries and tools
	// (rclone, boto3, the AWS CLI, etc.) work without modification.
	//
	// The S3 API is independent of BlobEnabled: setting S3Enabled = true
	// while BlobEnabled = false is a configuration error caught by Validate.
	//
	// Authentication: the S3 API accepts any AWS Signature V4 Authorization
	// header and extracts the access key ID as the tenant identifier. The
	// signature itself is not verified. This is intentional for single-operator
	// deployments; a future release may add full Sig V4 verification.
	// Configure S3 clients with any non-empty secret key value.
	//
	// Bucket semantics: each bucket maps to a tenant. Bucket names must match
	// existing tenant names; auto-registration is not performed on the S3
	// interface regardless of TenantAutoRegister.
	S3Enabled bool
	// S3Host is the bind address for the S3-compatible listener.
	// Default: inherits Host (0.0.0.0).
	// XOLU_S3_ADDR (host:port) and XOLU_S3_HOST / XOLU_S3_PORT are the
	// controlling env vars, following the same precedence as XOLU_METRICS_ADDR.
	S3Host string
	// S3Port is the port for the S3-compatible listener.
	// Default: 9091. Must differ from Port and MetricsPort when S3Enabled is true.
	// 0 means the S3 listener is not started even when S3Enabled is true
	// (useful for testing where the caller manages the listener directly).
	S3Port int
	// S3RequireAuth controls whether requests that arrive without an
	// Authorization header are rejected (true) or silently fall back to
	// treating the bucket name as the tenant (false, default).
	// When false a structured warning is still logged so operators can detect
	// misconfigured clients. Set XOLU_S3_REQUIRE_AUTH=true to enforce.
	S3RequireAuth bool

	// S3KeyGrants maps S3 access keys to their secret and the tenants they may
	// act on, for TenantAuthMode "scoped". Under "scoped" an S3 request must
	// present an access key with a matching grant whose secret authenticates it;
	// the access-key string is no longer trusted as the tenant name. Ignored
	// under "open".
	S3KeyGrants []S3KeyGrant

	// Blob GC — background garbage collection for unreferenced blobs.
	//
	// Delete removes only the key alias; the blob file remains until the GC
	// worker performs a mark-and-sweep. Unreferenced blobs are first moved to a
	// .gc-pending/ quarantine directory and hard-deleted after BlobGCGracePeriodSecs.
	// The grace period protects against the race where a Put has written the
	// blob file but has not yet written the key alias.

	// BlobGCEnabled enables the background GC worker.
	// Default: false (GC must be explicitly opted in).
	BlobGCEnabled bool
	// BlobGCIntervalSecs is the time between GC sweeps in seconds.
	// Default: 3600 (1 hour).
	BlobGCIntervalSecs int
	// BlobGCGracePeriodSecs is how long (in seconds) an unreferenced blob must
	// sit in .gc-pending/ before it is hard-deleted.
	// Default: 600 (10 minutes).
	BlobGCGracePeriodSecs int

	// BlobExportSweepEnabled enables the background sweep that deletes
	// expired async-export blobs (T-149, POST .../blob/export). Separate
	// from BlobGCEnabled: export blobs are ordinary blobs from GC's own
	// point of view (referenced by their key alias, so GC alone would
	// never reclaim them) -- this sweep exists specifically to enforce a
	// TTL on top of that, not to replace GC.
	// Default: false (must be explicitly opted in, matching BlobGCEnabled).
	BlobExportSweepEnabled bool
	// BlobExportSweepIntervalSecs is the time between export-sweep passes.
	// Default: 900 (15 minutes).
	BlobExportSweepIntervalSecs int
	// BlobExportTTLSecs is how long (in seconds) a completed export blob
	// is kept before the sweep deletes it -- Horacio's own framing when
	// this was designed (2026-08-03): "a TTL so that the export expires
	// in the next few hours".
	// Default: 14400 (4 hours).
	BlobExportTTLSecs int

	// BlobUsageSampleIntervalSecs is how often (in seconds) the background
	// usage sampler walks the blob store to update cached totals served by
	// the usage API and telemetry endpoint. 0 disables the sampler.
	// Default: 300 (5 minutes).
	BlobUsageSampleIntervalSecs int
}

// readSecret returns the value of the named secret, preferring the
// environment variable (upper-cased name) over a Docker-style secret file.
//
// Resolution order:
//  1. Environment variable `strings.ToUpper(name)` — returned as-is if non-empty.
//  2. File `/run/secrets/<name>` — trailing newlines stripped.
//  3. Empty string if neither is set.
//
// This allows secrets to be supplied either via environment variables
// (development, CI) or via Docker/Compose secret mounts (production).
func readSecret(name string) string {
	if v := os.Getenv(strings.ToUpper(name)); v != "" {
		return v
	}
	data, err := os.ReadFile("/run/secrets/" + name)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(data), "\n\r")
}

// Default returns the default configuration
func Default() *Config {
	return &Config{
		Host:                        "0.0.0.0",
		Port:                        9090,
		StorageType:                 "sqlite",
		BaseDir:                     "data",
		SchemaDir:                   "schema",
		Schema:                      "default",
		CacheType:                   "memory",
		CacheTTL:                    300,
		CacheSize:                   1024,
		CacheShards:                 16,
		RedisHost:                   "localhost",
		RedisPort:                   6379,
		GraphEnabled:                true,
		GraphMode:                   "flat",
		AsyncJobRetentionTTL:        86400,
		OQLQueryCacheTTL:            30,
		GraphQueryCacheTTL:          30,
		GraphMaxVisitedNodes:        10000,
		GraphMaxResults:             10000,
		GraphCycleDetection:         "warn",
		GraphCycleCheckLimit:        0, // 0 means use the graph layer default (512)
		FullTextEnabled:             false,
		StrictCommit:                true,
		MaxQueryDepth:               10,
		MaxEmbedDepth:               10,
		RefEmbedDepth:               3,
		DefaultPageSize:             10,
		QueryTimeout:                30,       // 30 seconds
		QueryMaxRows:                10000,    // 10k rows max returned
		QueryMaxScanRows:            100000,   // 100k rows max scanned
		QueryMaxResponseBytes:       10485760, // 10 MB
		PatchNullBehavior:           "store",
		MaxEntitySize:               1048576, // 1MB
		CascadingDelete:             false,
		MaxCascadeDeletions:         10000,
		MaxCascadeWork:              100000,
		Debug:                       false,
		DebugLocks:                  false,
		LogLevel:                    "info",
		AuthType:                    "none",
		JWTSecret:                   "",
		JWTIssuer:                   "",
		APIKeys:                     []string{},
		AuthExcludePaths:            []string{"/health", "/ready", "/version", "/metrics"},
		RateLimitEnabled:            false,
		RateLimitRate:               100,
		RateLimitWindow:             60,
		RateLimitByIP:               true,
		RateLimitByKey:              false,
		MetricsEnabled:              true,
		APIV2Enabled:                false,
		MetaMaxValueBytes:           65536,
		MetaGCEnabled:               true,
		MetaGCIntervalSecs:          300,
		DxpGCEnabled:                true,
		DxpGCIntervalSecs:           60,
		DxpTxnRetentionSecs:         172800, // 48h
		DynConfigEnabled:            false,
		DynConfigFile:               "", // resolved to {BaseDir}/dynconfig.json at startup
		DynConfigReloadSecs:         30,
		DynConfigAPIEnabled:         false,
		CORSOrigins:                 []string{},
		TenantMode:                  "path",
		TenantAuthMode:              "open",
		TimeseriesEnabled:           true,  // default on (mature; regression-guarded — see validateSubsystemParity)
		TSMemtableSize:              67108864, // 64 MB
		TSBlockSize:                 32768,    // 32 KB
		TSCompression:               "zstd",
		TSL0CompactionThreshold:     4,
		TSMaxOpenFiles:              500,
		TSDefaultRetentionDays:      90,
		TSCompactionIntervalSecs:    3600,
		TSRetentionEnabled:          false,
		TSQueryTimeoutSecs:          30,
		TSMaxQueryEvents:            10000,
		TSMaxScanEvents:             500000,
		TSMaxRangeDays:              366,
		TSMaxBatchSize:              5000,
		TSMaxResponseBytes:          10485760, // 10 MB
		TSMaxAggregateBuckets:       10000,
		TSCoalFlushIntervalMs:       10,   // 10ms flush window
		TSCoalMaxEvents:             2000, // early-flush threshold
		TSRollupCascadeDelete:       true, // cascade delete by default
		BlobEnabled:                 false,
		BlobDir:                     "",       // resolved to {BaseDir}/blobs at startup
		BlobMaxSize:                 67108864, // 64 MB
		BlobMaxTotalBytes:           0,        // no limit
		CalEnabled:                  false,    // T-18: opt-in until stable
		BalEnabled:                  false,    // @B: opt-in until stable
		S3Enabled:                   false,
		S3Host:                      "", // inherits Host at startup
		S3Port:                      9091,
		S3RequireAuth:               false,
		BlobGCEnabled:               false,
		BlobGCIntervalSecs:          3600,
		BlobGCGracePeriodSecs:       600,
		BlobExportSweepEnabled:      false,
		BlobExportSweepIntervalSecs: 900,
		BlobExportTTLSecs:           14400,
		BlobUsageSampleIntervalSecs: 300,
		SQLiteMaxOpenConns:          0, // 0 = backend default (1 for SQLite, higher for Postgres)
		SQLiteMaxIdleConns:          0, // 0 = backend default
		SQLiteReadPoolSize:          0, // 0 = backend default (NumCPU for SQLite)
		SQLiteContentionThreshold:   95,
		SQLiteBusyTimeout:           5000,
		SQLiteCacheSize:             2000,
		PerformanceProfile:          "auto",
		RedisPoolSize:               50,
		RedisMinIdleConns:           10,
		HTTPReadTimeout:             0,
		HTTPWriteTimeout:            0,
		HTTPIdleTimeout:             0,
		HTTPRequestTimeout:          60,
	}
}

// LoadFromEnv loads configuration from environment variables.
// All environment variables use the XOLU_ prefix.
func LoadFromEnv(cfg *Config) {
	// XOLU_ADDR is a convenience alias for setting host and port together
	// as a single "host:port" value. XOLU_HOST and XOLU_PORT take precedence
	// if set, so existing deployments are unaffected.
	if val := os.Getenv("XOLU_TRUSTED_PROXIES"); val != "" {
		cfg.TrustedProxies = val
	}
		if val := os.Getenv("XOLU_ADDR"); val != "" {
		if h, p, err := net.SplitHostPort(val); err == nil {
			cfg.Host = h
			if port, err := strconv.Atoi(p); err == nil {
				cfg.Port = port
			}
		}
	}
	if val := os.Getenv("XOLU_HOST"); val != "" {
		cfg.Host = val
	}
	if val := os.Getenv("XOLU_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			cfg.Port = port
		}
	}
	if val := os.Getenv("XOLU_STORAGE_TYPE"); val != "" {
		cfg.StorageType = val
	}
	if val := os.Getenv("XOLU_BASE_DIR"); val != "" {
		cfg.BaseDir = val
	}
	if val := os.Getenv("XOLU_SCHEMA_DIR"); val != "" {
		cfg.SchemaDir = val
	}
	if val := os.Getenv("XOLU_SCHEMA_NAME"); val != "" {
		cfg.Schema = val
	}
	if val := os.Getenv("XOLU_CACHE_TYPE"); val != "" {
		cfg.CacheType = val
	}
	if val := os.Getenv("XOLU_CACHE_TTL"); val != "" {
		if ttl, err := strconv.Atoi(val); err == nil {
			cfg.CacheTTL = ttl
		}
	}
	if val := os.Getenv("XOLU_CACHE_SHARDS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.CacheShards = n
		}
	}
	if val := os.Getenv("XOLU_REDIS_HOST"); val != "" {
		cfg.RedisHost = val
	}
	if val := os.Getenv("XOLU_REDIS_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			cfg.RedisPort = port
		}
	}
	if val := os.Getenv("XOLU_GRAPH_MODE"); val != "" {
		cfg.GraphMode = val
		cfg.GraphEnabled = val != "disabled"
	}
	if val := os.Getenv("XOLU_ASYNC_JOB_RETENTION_TTL"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			cfg.AsyncJobRetentionTTL = v
		}
	}
	if val := os.Getenv("XOLU_GRAPH_QUERY_CACHE_TTL"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			cfg.GraphQueryCacheTTL = v
		}
	}
	if val := os.Getenv("XOLU_OQL_QUERY_CACHE_TTL"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			cfg.OQLQueryCacheTTL = v
		}
	}
	if val := os.Getenv("XOLU_GRAPH_CYCLE_DETECTION"); val != "" {
		cfg.GraphCycleDetection = val
	}
	if val := os.Getenv("XOLU_GRAPH_CYCLE_CHECK_LIMIT"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			cfg.GraphCycleCheckLimit = v
		}
	}
	if val := os.Getenv("XOLU_GRAPH_MAX_VISITED_NODES"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			cfg.GraphMaxVisitedNodes = v
		}
	}
	if val := os.Getenv("XOLU_GRAPH_MAX_RESULTS"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			cfg.GraphMaxResults = v
		}
	}
	if val := os.Getenv("XOLU_FULLTEXT_ENABLED"); val != "" {
		cfg.FullTextEnabled = parseBool(val)
	}
	if val := os.Getenv("XOLU_STRICT_COMMIT"); val != "" {
		cfg.StrictCommit = parseBool(val)
	}
	if val := os.Getenv("XOLU_CASCADING_DELETE"); val != "" {
		cfg.CascadingDelete = parseBool(val)
	}
	if val := os.Getenv("XOLU_DEBUG"); val != "" {
		cfg.Debug = parseBool(val)
		// Legacy compat: XOLU_DEBUG=true maps to log level "debug".
		// XOLU_LOG_LEVEL (read below) takes precedence if set.
		if cfg.Debug {
			cfg.LogLevel = "debug"
		}
	}
	// XOLU_LOG_LEVEL accepts: debug, info, warn, error.
	// Takes precedence over the XOLU_DEBUG legacy alias.
	if val := strings.ToLower(strings.TrimSpace(os.Getenv("XOLU_LOG_LEVEL"))); val != "" {
		switch val {
		case "debug", "info", "warn", "error":
			cfg.LogLevel = val
		}
	}
	if val := os.Getenv("XOLU_DEBUG_LOCKS"); val != "" {
		cfg.DebugLocks = parseBool(val)
	}
	if val := os.Getenv("XOLU_NO_ASCII"); val != "" {
		cfg.NoAscii = parseBool(val)
	}
	if val := os.Getenv("XOLU_NO_STARTUP_TEXT"); val != "" {
		cfg.NoStartupText = parseBool(val)
	}
	if val := os.Getenv("XOLU_REF_EMBED_DEPTH"); val != "" {
		if depth, err := strconv.Atoi(val); err == nil {
			cfg.RefEmbedDepth = depth
		}
	}
	if val := os.Getenv("XOLU_MAX_ENTITY_SIZE"); val != "" {
		if size, err := strconv.Atoi(val); err == nil {
			cfg.MaxEntitySize = size
		}
	}
	if val := os.Getenv("XOLU_QUERY_TIMEOUT"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			cfg.QueryTimeout = v
		}
	}
	if val := os.Getenv("XOLU_QUERY_MAX_ROWS"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			cfg.QueryMaxRows = v
		}
	}
	if val := os.Getenv("XOLU_QUERY_MAX_SCAN_ROWS"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			cfg.QueryMaxScanRows = v
		}
	}
	if val := os.Getenv("XOLU_QUERY_MAX_RESPONSE_BYTES"); val != "" {
		if v, err := strconv.Atoi(val); err == nil {
			cfg.QueryMaxResponseBytes = v
		}
	}
	if val := os.Getenv("XOLU_PATCH_NULL"); val != "" {
		cfg.PatchNullBehavior = val
	}

	// Authentication settings
	if val := os.Getenv("XOLU_AUTH_TYPE"); val != "" {
		cfg.AuthType = val
	}
	if val := readSecret("xolu_jwt_secret"); val != "" {
		cfg.JWTSecret = val
	}
	if val := os.Getenv("XOLU_JWT_ISSUER"); val != "" {
		cfg.JWTIssuer = val
	}
	if val := os.Getenv("XOLU_API_KEYS"); val != "" {
		cfg.APIKeys = strings.Split(val, ",")
		for i := range cfg.APIKeys {
			cfg.APIKeys[i] = strings.TrimSpace(cfg.APIKeys[i])
		}
	}
	if val := readSecret("xolu_internal_token"); val != "" {
		cfg.InternalToken = val
	}

	// Rate limiting settings
	if val := os.Getenv("XOLU_RATE_LIMIT_ENABLED"); val != "" {
		cfg.RateLimitEnabled = parseBool(val)
	}
	if val := os.Getenv("XOLU_RATE_LIMIT_RATE"); val != "" {
		if rate, err := strconv.Atoi(val); err == nil {
			cfg.RateLimitRate = rate
		}
	}
	if val := os.Getenv("XOLU_RATE_LIMIT_WINDOW"); val != "" {
		if window, err := strconv.Atoi(val); err == nil {
			cfg.RateLimitWindow = window
		}
	}
	if val := os.Getenv("XOLU_RATE_LIMIT_BY_IP"); val != "" {
		cfg.RateLimitByIP = parseBool(val)
	}
	if val := os.Getenv("XOLU_RATE_LIMIT_BY_KEY"); val != "" {
		cfg.RateLimitByKey = parseBool(val)
	}
	if val := os.Getenv("XOLU_METRICS_ENABLED"); val != "" {
		cfg.MetricsEnabled = parseBool(val)
	}
	// XOLU_METRICS_ADDR is a convenience alias for XOLU_METRICS_HOST + XOLU_METRICS_PORT.
	// XOLU_METRICS_HOST and XOLU_METRICS_PORT take precedence if also set.
	if val := os.Getenv("XOLU_METRICS_ADDR"); val != "" {
		if h, p, err := net.SplitHostPort(val); err == nil {
			cfg.MetricsHost = h
			if port, err := strconv.Atoi(p); err == nil {
				cfg.MetricsPort = port
			}
		}
	}
	if val := os.Getenv("XOLU_METRICS_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			cfg.MetricsPort = port
		}
	}
	if val := os.Getenv("XOLU_METRICS_HOST"); val != "" {
		cfg.MetricsHost = val
	}
	if val := os.Getenv("XOLU_API_V2_ENABLED"); val != "" {
		cfg.APIV2Enabled = parseBool(val)
	}
	if val := os.Getenv("XOLU_META_MAX_VALUE_BYTES"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.MetaMaxValueBytes = n
		}
	}
	if val := os.Getenv("XOLU_META_GC_ENABLED"); val != "" {
		cfg.MetaGCEnabled = parseBool(val)
	}
	if val := os.Getenv("XOLU_META_GC_INTERVAL_SECS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.MetaGCIntervalSecs = n
		}
	}
	if val := os.Getenv("XOLU_DXP_GC_ENABLED"); val != "" {
		cfg.DxpGCEnabled = parseBool(val)
	}
	if val := os.Getenv("XOLU_DXP_GC_INTERVAL_SECS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.DxpGCIntervalSecs = n
		}
	}
	if val := os.Getenv("XOLU_DXP_TXN_RETENTION_SECS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.DxpTxnRetentionSecs = n
		}
	}
	if val := os.Getenv("XOLU_DYNCONFIG_ENABLED"); val != "" {
		cfg.DynConfigEnabled = parseBool(val)
	}
	if val := os.Getenv("XOLU_DYNCONFIG_FILE"); val != "" {
		cfg.DynConfigFile = val
	}
	if val := os.Getenv("XOLU_DYNCONFIG_RELOAD_INTERVAL"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.DynConfigReloadSecs = n
		}
	}
	if val := os.Getenv("XOLU_DYNCONFIG_API_ENABLED"); val != "" {
		cfg.DynConfigAPIEnabled = parseBool(val)
	}
	if val := os.Getenv("XOLU_CORS_ORIGINS"); val != "" {
		origins := strings.Split(val, ",")
		for i := range origins {
			origins[i] = strings.TrimSpace(origins[i])
		}
		cfg.CORSOrigins = origins
	}

	// Tenant mode
	if val := os.Getenv("XOLU_TENANT_MODE"); val != "" {
		cfg.TenantMode = val
	}
	if val := os.Getenv("XOLU_TENANT_AUTH_MODE"); val != "" {
		cfg.TenantAuthMode = val
	}
	if val := os.Getenv("XOLU_TENANT_AUTO_REGISTER"); val != "" {
		cfg.TenantAutoRegister = parseBool(val)
	}

	// Timeseries
	if val := os.Getenv("XOLU_TIMESERIES_ENABLED"); val != "" {
		cfg.TimeseriesEnabled = parseBool(val)
	}
	if val := os.Getenv("XOLU_TS_MEMTABLE_SIZE"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TSMemtableSize = n
		}
	}
	if val := os.Getenv("XOLU_TS_BLOCK_SIZE"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TSBlockSize = n
		}
	}
	if val := os.Getenv("XOLU_TS_COMPRESSION"); val != "" {
		cfg.TSCompression = val
	}
	if val := os.Getenv("XOLU_TS_L0_COMPACTION_THRESHOLD"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TSL0CompactionThreshold = n
		}
	}
	if val := os.Getenv("XOLU_TS_MAX_OPEN_FILES"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TSMaxOpenFiles = n
		}
	}
	if val := os.Getenv("XOLU_TS_DEFAULT_RETENTION_DAYS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TSDefaultRetentionDays = n
		}
	}
	if val := os.Getenv("XOLU_TS_COMPACTION_INTERVAL"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TSCompactionIntervalSecs = n
		}
	}
	if val := os.Getenv("XOLU_TS_RETENTION_ENABLED"); val != "" {
		cfg.TSRetentionEnabled = parseBool(val)
	}
	if val := os.Getenv("XOLU_TS_QUERY_TIMEOUT"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TSQueryTimeoutSecs = n
		}
	}
	if val := os.Getenv("XOLU_TS_MAX_QUERY_EVENTS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TSMaxQueryEvents = n
		}
	}
	if val := os.Getenv("XOLU_TS_MAX_SCAN_EVENTS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TSMaxScanEvents = n
		}
	}
	if val := os.Getenv("XOLU_TS_MAX_RANGE_DAYS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TSMaxRangeDays = n
		}
	}
	if val := os.Getenv("XOLU_TS_MAX_BATCH_SIZE"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TSMaxBatchSize = n
		}
	}
	if val := os.Getenv("XOLU_TS_MAX_RESPONSE_BYTES"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TSMaxResponseBytes = n
		}
	}
	if val := os.Getenv("XOLU_TS_MAX_AGGREGATE_BUCKETS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.TSMaxAggregateBuckets = n
		}
	}
	if val := os.Getenv("XOLU_TS_COAL_FLUSH_INTERVAL_MS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.TSCoalFlushIntervalMs = n
		}
	}
	if val := os.Getenv("XOLU_TS_COAL_MAX_EVENTS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			cfg.TSCoalMaxEvents = n
		}
	}
	if val := os.Getenv("XOLU_TS_ROLLUP_CASCADE_DELETE"); val != "" {
		cfg.TSRollupCascadeDelete = parseBool(val)
	}

	// Blob store
	if val := os.Getenv("XOLU_BLOB_ENABLED"); val != "" {
		cfg.BlobEnabled = parseBool(val)
	}
	if val := os.Getenv("XOLU_BLOB_DIR"); val != "" {
		cfg.BlobDir = val
	}

	// Cal subsystem (T-18): the /api/v2/cal/* endpoints. Off by default
	// until the surface is proven stable.
	if val := os.Getenv("XOLU_BAL_ENABLED"); val != "" {
		cfg.BalEnabled = parseBool(val)
	}
	if val := os.Getenv("XOLU_CAL_ENABLED"); val != "" {
		cfg.CalEnabled = parseBool(val)
	}
	if val := os.Getenv("XOLU_BLOB_MAX_SIZE"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.BlobMaxSize = n
		}
	}
	if val := os.Getenv("XOLU_BLOB_MAX_TOTAL_BYTES"); val != "" {
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			cfg.BlobMaxTotalBytes = n
		}
	}

	// S3-compatible API
	if val := os.Getenv("XOLU_S3_ENABLED"); val != "" {
		cfg.S3Enabled = parseBool(val)
	}
	// XOLU_S3_ADDR is a convenience alias for XOLU_S3_HOST + XOLU_S3_PORT.
	// XOLU_S3_HOST and XOLU_S3_PORT take precedence if also set.
	if val := os.Getenv("XOLU_S3_ADDR"); val != "" {
		if h, p, err := net.SplitHostPort(val); err == nil {
			cfg.S3Host = h
			if port, err := strconv.Atoi(p); err == nil {
				cfg.S3Port = port
			}
		}
	}
	if val := os.Getenv("XOLU_S3_HOST"); val != "" {
		cfg.S3Host = val
	}
	if val := os.Getenv("XOLU_S3_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			cfg.S3Port = port
		}
	}
	if val := os.Getenv("XOLU_S3_REQUIRE_AUTH"); val != "" {
		cfg.S3RequireAuth = parseBool(val)
	}
	if val := os.Getenv("XOLU_BLOB_GC_ENABLED"); val != "" {
		cfg.BlobGCEnabled = parseBool(val)
	}
	if val := os.Getenv("XOLU_BLOB_GC_INTERVAL"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.BlobGCIntervalSecs = n
		}
	}
	if val := os.Getenv("XOLU_BLOB_GC_GRACE_PERIOD"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.BlobGCGracePeriodSecs = n
		}
	}
	if val := os.Getenv("XOLU_BLOB_EXPORT_SWEEP_ENABLED"); val != "" {
		cfg.BlobExportSweepEnabled = parseBool(val)
	}
	if val := os.Getenv("XOLU_BLOB_EXPORT_SWEEP_INTERVAL"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.BlobExportSweepIntervalSecs = n
		}
	}
	if val := os.Getenv("XOLU_BLOB_EXPORT_TTL"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.BlobExportTTLSecs = n
		}
	}
	if val := os.Getenv("XOLU_BLOB_USAGE_INTERVAL"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.BlobUsageSampleIntervalSecs = n
		}
	}

	// Performance tuning — SQLite
	if val := os.Getenv("XOLU_SQLITE_MAX_OPEN_CONNS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.SQLiteMaxOpenConns = n
		}
	}
	if val := os.Getenv("XOLU_SQLITE_MAX_IDLE_CONNS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.SQLiteMaxIdleConns = n
		}
	}
	if val := os.Getenv("XOLU_SQLITE_READ_POOL_SIZE"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.SQLiteReadPoolSize = n
		}
	}
	if val := os.Getenv("XOLU_SQLITE_CONTENTION_THRESHOLD"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.SQLiteContentionThreshold = n
		}
	}
	if val := os.Getenv("XOLU_SQLITE_BUSY_TIMEOUT"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.SQLiteBusyTimeout = n
		}
	}
	if val := os.Getenv("XOLU_SQLITE_CACHE_SIZE"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.SQLiteCacheSize = n
		}
	}
	if val := os.Getenv("XOLU_SQLITE_PER_FILE_TENANTS"); val != "" {
		cfg.SQLitePerFileTenants = val == "true" || val == "1" || val == "yes"
	}

	// Performance tuning — query planner
	if val := os.Getenv("XOLU_PERFORMANCE_PROFILE"); val != "" {
		cfg.PerformanceProfile = val
	}

	// Performance tuning — Redis
	if val := os.Getenv("XOLU_REDIS_POOL_SIZE"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.RedisPoolSize = n
		}
	}
	if val := os.Getenv("XOLU_REDIS_MIN_IDLE_CONNS"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.RedisMinIdleConns = n
		}
	}

	// Performance tuning — HTTP server
	if val := os.Getenv("XOLU_HTTP_READ_TIMEOUT"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.HTTPReadTimeout = n
		}
	}
	if val := os.Getenv("XOLU_HTTP_WRITE_TIMEOUT"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.HTTPWriteTimeout = n
		}
	}
	if val := os.Getenv("XOLU_HTTP_IDLE_TIMEOUT"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.HTTPIdleTimeout = n
		}
	}
	if val := os.Getenv("XOLU_HTTP_REQUEST_TIMEOUT"); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			cfg.HTTPRequestTimeout = n
		}
	}
}
func parseBool(val string) bool {
	val = strings.ToLower(val)
	return val == "true" || val == "1" || val == "yes"
}

// Validate checks the configuration for invalid or inconsistent values.
// It returns hard errors (config is broken) and warnings (config is
// suspicious but functional) as separate slices.
func (c *Config) Validate() (errs []string, warnings []string) {

	check := func(field, value string, allowed []string) {
		for _, a := range allowed {
			if value == a {
				return
			}
		}
		errs = append(errs, fmt.Sprintf("%s: %q is not valid (must be one of: %s)",
			field, value, strings.Join(allowed, ", ")))
	}

	check("StorageType", c.StorageType, []string{"sqlite"})
	check("CacheType", c.CacheType, []string{"memory", "redis"})
	check("GraphMode", c.GraphMode, []string{"flat", "disabled"})
	check("GraphCycleDetection", c.GraphCycleDetection, []string{"warn", "error", "ignore"})
	check("PatchNullBehavior", c.PatchNullBehavior, []string{"store", "delete"})
	check("AuthType", c.AuthType, []string{"none", "jwt", "apikey", "bearertoken"})
	// Accept "none" as deprecated alias for "path"
	if c.TenantMode == "none" {
		c.TenantMode = "path"
	}
	check("TenantMode", c.TenantMode, []string{"path", "strict"})

	// Default empty TenantAuthMode to "open" (backward compatibility for configs
	// constructed without the field).
	if c.TenantAuthMode == "" {
		c.TenantAuthMode = "open"
	}
	check("TenantAuthMode", c.TenantAuthMode, []string{"open", "scoped"})
	// Matrix rule (tenant-access-control.md §4.1.1): scoped authorisation is
	// incoherent without strict routing, because path mode leaves the unprefixed
	// tenant-0 routes open and un-authorised. Reject the combination rather than
	// give a false sense of isolation.
	if c.TenantAuthMode == "scoped" && c.TenantMode != "strict" {
		errs = append(errs, `TenantAuthMode "scoped" requires TenantMode "strict" (otherwise the unprefixed tenant-0 routes bypass authorisation)`)
	}

	if c.MaxEntitySize <= 0 {
		errs = append(errs, "MaxEntitySize must be > 0")
	}
	if c.GraphCycleCheckLimit < 0 {
		errs = append(errs, "GraphCycleCheckLimit must be >= 0 (0 = use built-in default)")
	}
	if c.CacheTTL < 0 {
		errs = append(errs, "CacheTTL must be >= 0")
	}
	if c.Port < 0 || c.Port > 65535 {
		errs = append(errs, fmt.Sprintf("Port: %d is out of range (0-65535)", c.Port))
	}
	if c.MetricsPort < 0 || c.MetricsPort > 65535 {
		errs = append(errs, fmt.Sprintf("MetricsPort: %d is out of range (0-65535)", c.MetricsPort))
	}
	if c.MetricsPort > 0 && c.MetricsPort == c.Port {
		errs = append(errs, fmt.Sprintf("MetricsPort (%d) must differ from Port (%d)", c.MetricsPort, c.Port))
	}
	if c.MaxQueryDepth <= 0 {
		errs = append(errs, "MaxQueryDepth must be > 0")
	}
	if c.MaxEmbedDepth <= 0 {
		errs = append(errs, "MaxEmbedDepth must be > 0")
	}
	if c.RefEmbedDepth < 0 {
		errs = append(errs, "RefEmbedDepth must be >= 0")
	}
	if c.RateLimitEnabled {
		if c.RateLimitRate <= 0 {
			errs = append(errs, "RateLimitRate must be > 0 when rate limiting is enabled")
		}
		if c.RateLimitWindow <= 0 {
			errs = append(errs, "RateLimitWindow must be > 0 when rate limiting is enabled")
		}
	}
	if c.AuthType == "jwt" && c.JWTSecret == "" {
		errs = append(errs, "JWTSecret is required when AuthType is \"jwt\"")
	}
	if c.AuthType == "apikey" && len(c.APIKeys) == 0 {
		errs = append(errs, "APIKeys must not be empty when AuthType is \"apikey\"")
	}
	if c.AuthType == "bearertoken" && c.InternalToken == "" {
		errs = append(errs, "InternalToken (XOLU_INTERNAL_TOKEN) is required when AuthType is \"bearertoken\"")
	}
	if c.CacheType == "redis" && c.RedisHost == "" {
		errs = append(errs, "RedisHost is required when CacheType is \"redis\"")
	}

	// Performance tuning — range validation
	if c.SQLiteMaxOpenConns < 0 {
		errs = append(errs, "SQLiteMaxOpenConns must be >= 0")
	}
	if c.SQLiteMaxIdleConns < 0 {
		errs = append(errs, "SQLiteMaxIdleConns must be >= 0")
	}
	if c.SQLiteReadPoolSize < 0 {
		errs = append(errs, "SQLiteReadPoolSize must be >= 0")
	}
	if c.SQLiteContentionThreshold < 0 || c.SQLiteContentionThreshold > 100 {
		errs = append(errs, "SQLiteContentionThreshold must be 0-100")
	}
	if c.SQLiteBusyTimeout < 0 {
		errs = append(errs, "SQLiteBusyTimeout must be >= 0")
	}
	if c.SQLiteCacheSize < 0 {
		errs = append(errs, "SQLiteCacheSize must be >= 0")
	}
	switch strings.ToLower(c.PerformanceProfile) {
	case "auto", "edge", "vps", "dedicated", "":
		// valid
	default:
		errs = append(errs, fmt.Sprintf("PerformanceProfile %q is not valid; use auto, edge, vps, or dedicated", c.PerformanceProfile))
	}
	if c.RedisPoolSize < 0 {
		errs = append(errs, "RedisPoolSize must be >= 0")
	}
	if c.RedisMinIdleConns < 0 {
		errs = append(errs, "RedisMinIdleConns must be >= 0")
	}
	if c.HTTPReadTimeout < 0 {
		errs = append(errs, "HTTPReadTimeout must be >= 0")
	}
	if c.HTTPWriteTimeout < 0 {
		errs = append(errs, "HTTPWriteTimeout must be >= 0")
	}
	if c.HTTPIdleTimeout < 0 {
		errs = append(errs, "HTTPIdleTimeout must be >= 0")
	}
	if c.HTTPRequestTimeout < 0 {
		errs = append(errs, "HTTPRequestTimeout must be >= 0")
	}

	// Cross-field consistency warnings
	if c.SQLiteMaxIdleConns > 0 && c.SQLiteMaxOpenConns > 0 && c.SQLiteMaxIdleConns > c.SQLiteMaxOpenConns {
		warnings = append(warnings, fmt.Sprintf(
			"SQLiteMaxIdleConns (%d) > SQLiteMaxOpenConns (%d); idle will be clamped to max open",
			c.SQLiteMaxIdleConns, c.SQLiteMaxOpenConns))
	}
	if c.RedisMinIdleConns > 0 && c.RedisPoolSize > 0 && c.RedisMinIdleConns > c.RedisPoolSize {
		warnings = append(warnings, fmt.Sprintf(
			"RedisMinIdleConns (%d) > RedisPoolSize (%d); min idle will be clamped to pool size",
			c.RedisMinIdleConns, c.RedisPoolSize))
	}
	if c.StorageType != "sqlite" {
		hasSQLiteTuning := c.SQLiteMaxOpenConns != 0 || c.SQLiteMaxIdleConns != 0 || c.SQLiteReadPoolSize != 0 ||
			c.SQLiteContentionThreshold != 95 || c.SQLiteBusyTimeout != 5000 || c.SQLiteCacheSize != 2000
		if hasSQLiteTuning {
			warnings = append(warnings, "SQLite tuning parameters set but StorageType is not \"sqlite\"; they will be ignored")
		}
	}
	if c.CacheType != "redis" {
		hasRedisTuning := c.RedisPoolSize != 50 || c.RedisMinIdleConns != 10
		if hasRedisTuning {
			warnings = append(warnings, "Redis tuning parameters set but CacheType is not \"redis\"; they will be ignored")
		}
	}
	if c.HTTPWriteTimeout > 0 && c.HTTPRequestTimeout > 0 && c.HTTPRequestTimeout > c.HTTPWriteTimeout {
		warnings = append(warnings, fmt.Sprintf(
			"HTTPRequestTimeout (%ds) > HTTPWriteTimeout (%ds); the write timeout will close connections before the request timeout fires",
			c.HTTPRequestTimeout, c.HTTPWriteTimeout))
	}
	if c.RefEmbedDepth > 0 && c.MaxEmbedDepth > 0 && c.RefEmbedDepth > c.MaxEmbedDepth {
		warnings = append(warnings, fmt.Sprintf(
			"RefEmbedDepth (%d) > MaxEmbedDepth (%d); ref embedding will be capped at MaxEmbedDepth",
			c.RefEmbedDepth, c.MaxEmbedDepth))
	}
	if c.CacheType == "memory" && c.CacheSize > 0 && c.CacheSize < 16 {
		warnings = append(warnings, fmt.Sprintf(
			"CacheSize (%d) is very small for in-memory cache; this may cause excessive evictions",
			c.CacheSize))
	}
	if c.CacheShards < 0 {
		errs = append(errs, "CacheShards must be >= 0")
	}

	// Blob store validation
	if c.BlobMaxSize < 0 {
		errs = append(errs, "BlobMaxSize must be >= 0")
	}
	if c.BlobMaxTotalBytes < 0 {
		errs = append(errs, "BlobMaxTotalBytes must be >= 0")
	}
	if c.S3Enabled && !c.BlobEnabled {
		errs = append(errs, "S3Enabled requires BlobEnabled to be true")
	}
	if c.S3Port < 0 || c.S3Port > 65535 {
		errs = append(errs, fmt.Sprintf("S3Port: %d is out of range (0-65535)", c.S3Port))
	}
	if c.S3Enabled && c.S3Port > 0 {
		if c.S3Port == c.Port {
			errs = append(errs, fmt.Sprintf("S3Port (%d) must differ from Port (%d)", c.S3Port, c.Port))
		}
		if c.MetricsPort > 0 && c.S3Port == c.MetricsPort {
			errs = append(errs, fmt.Sprintf("S3Port (%d) must differ from MetricsPort (%d)", c.S3Port, c.MetricsPort))
		}
	}
	if !c.BlobEnabled {
		hasBlobTuning := c.BlobDir != "" || c.BlobMaxSize != 67108864
		if hasBlobTuning {
			warnings = append(warnings, "Blob tuning parameters set but BlobEnabled is false; they will be ignored")
		}
		if c.BlobGCEnabled {
			warnings = append(warnings, "BlobGCEnabled is set but BlobEnabled is false; GC will not start")
		}
		if c.BlobExportSweepEnabled {
			warnings = append(warnings, "BlobExportSweepEnabled is set but BlobEnabled is false; the export sweep will not start")
		}
	}
	if c.BlobEnabled && c.BlobGCEnabled {
		if c.BlobGCIntervalSecs <= 0 {
			errs = append(errs, "BlobGCIntervalSecs must be > 0")
		}
		if c.BlobGCGracePeriodSecs < 0 {
			errs = append(errs, "BlobGCGracePeriodSecs must be >= 0")
		}
	}
	if c.BlobEnabled && c.BlobExportSweepEnabled {
		if c.BlobExportSweepIntervalSecs <= 0 {
			errs = append(errs, "BlobExportSweepIntervalSecs must be > 0")
		}
		if c.BlobExportTTLSecs < 0 {
			errs = append(errs, "BlobExportTTLSecs must be >= 0")
		}
	}
	if c.BlobEnabled && c.BlobUsageSampleIntervalSecs < 0 {
		errs = append(errs, "BlobUsageSampleIntervalSecs must be >= 0")
	}
	if c.S3RequireAuth && !c.S3Enabled {
		warnings = append(warnings, "S3RequireAuth is set but S3Enabled is false; it will be ignored")
	}

	// Dynamic config validation
	if c.DynConfigEnabled {
		if c.DynConfigReloadSecs <= 0 {
			errs = append(errs, "DynConfigReloadSecs must be > 0")
		}
	}
	if c.DynConfigAPIEnabled && !c.DynConfigEnabled {
		warnings = append(warnings, "DynConfigAPIEnabled is set but DynConfigEnabled is false; API will not be registered")
	}

	// Timeseries validation
	if c.TimeseriesEnabled {
		if c.StorageType != "sqlite" {
			errs = append(errs, "TimeseriesEnabled requires StorageType \"sqlite\"")
		}
		check("TSCompression", c.TSCompression, []string{"snappy", "zstd", "none"})
		if c.TSMemtableSize <= 0 {
			errs = append(errs, "TSMemtableSize must be > 0")
		}
		if c.TSBlockSize <= 0 {
			errs = append(errs, "TSBlockSize must be > 0")
		}
		if c.TSL0CompactionThreshold <= 0 {
			errs = append(errs, "TSL0CompactionThreshold must be > 0")
		}
		if c.TSMaxOpenFiles <= 0 {
			errs = append(errs, "TSMaxOpenFiles must be > 0")
		}
		if c.TSDefaultRetentionDays <= 0 {
			errs = append(errs, "TSDefaultRetentionDays must be > 0")
		}
		if c.TSCompactionIntervalSecs <= 0 {
			errs = append(errs, "TSCompactionIntervalSecs must be > 0")
		}
	}
	if !c.TimeseriesEnabled {
		hasTSTuning := c.TSCompression != "zstd" || c.TSMemtableSize != 67108864 ||
			c.TSBlockSize != 32768 || c.TSL0CompactionThreshold != 4 ||
			c.TSMaxOpenFiles != 500 || c.TSDefaultRetentionDays != 90 ||
			c.TSCompactionIntervalSecs != 3600 || c.TSRetentionEnabled
		if hasTSTuning {
			warnings = append(warnings, "Timeseries tuning parameters set but TimeseriesEnabled is false; they will be ignored")
		}
	}

	return errs, warnings
}

// AuthConfig extracts the authentication subset of the full server
// configuration for pkg/middleware.AuthMiddleware (T-19). The server
// calls this once at startup; the two structures cannot drift because
// this is the only construction path xolu itself uses.
func (c *Config) AuthConfig() authconfig.Config {
	return authconfig.Config{
		AuthType:         c.AuthType,
		JWTSecret:        c.JWTSecret,
		JWTIssuer:        c.JWTIssuer,
		APIKeys:          c.APIKeys,
		APIKeyGrants:     c.APIKeyGrants,
		InternalToken:    c.InternalToken,
		AuthExcludePaths: c.AuthExcludePaths,
	}
}
