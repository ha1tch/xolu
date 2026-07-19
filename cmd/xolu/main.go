// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/ha1tch/xolu/pkg/cache"
	"github.com/ha1tch/xolu/pkg/config"
	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/server"
	"github.com/ha1tch/xolu/pkg/storage"
	sl "github.com/ha1tch/xolu/pkg/storelayout"
	"github.com/ha1tch/xolu/pkg/tenant"
	"github.com/ha1tch/xolu/pkg/validation"
	"github.com/ha1tch/xolu/pkg/version"
	"github.com/rs/zerolog"
	zlog "github.com/rs/zerolog/log"
)

func main() {
	// ── Subcommand dispatch ───────────────────────────────────────────────────
	// Positional subcommands are checked before flag parsing so that
	// "xolu help", "xolu env", and "xolu version" work without flags.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-version":
			fmt.Println(version.Version)
			os.Exit(0)
		case "help", "--help", "-help", "-h":
			printUsage()
			os.Exit(0)
		case "env", "--env", "-env":
			printEnvVars()
			os.Exit(0)
		}
	}

	// ── Flag definitions ──────────────────────────────────────────────────────
	// Flags mirror the most commonly set env vars and take precedence over them.
	// All other configuration is via env vars (see: xolu env).
	//
	// Defaults are populated after LoadFromEnv so that env vars remain the
	// fallback and flags only override when explicitly supplied.
	//
	// We defer flag.Parse() until after LoadFromEnv so that flag defaults
	// reflect the env-derived config.

	// Load config from env first so flag defaults are env-aware.
	cfg := config.Default()
	config.LoadFromEnv(cfg)

	// Define flags with cfg values as defaults.
	flagPort := flag.Int("port", cfg.Port, "HTTP listen port")
	flagHost := flag.String("host", cfg.Host, "Bind address")
	flagBaseDir := flag.String("base-dir", cfg.BaseDir, "Base data directory")
	flagSchema := flag.String("schema", cfg.Schema, "Schema name")
	flagLogLevel := flag.String("log-level", cfg.LogLevel, "Log level: debug, info, warn, error")
	flagGraphMode := flag.String("graph-mode", cfg.GraphMode, "Graph mode: flat or disabled")
	flagNoAscii := flag.Bool("no-ascii", cfg.NoAscii, "Suppress ASCII art at startup")
	flagNoStartup := flag.Bool("no-startup-text", cfg.NoStartupText, "Suppress startup configuration summary")
	flagVerboseInit := flag.Bool("verbose-init", false, "Debug-level logging during initialisation only; reverts to --log-level once server is ready")

	flag.Usage = printUsage
	flag.Parse()

	// Apply flag values back onto cfg (flags win over env).
	cfg.Port = *flagPort
	cfg.Host = *flagHost
	cfg.BaseDir = *flagBaseDir
	cfg.Schema = *flagSchema
	cfg.SchemaDir = sl.SchemaDir(*flagBaseDir)
	if *flagLogLevel != "" {
		cfg.LogLevel = *flagLogLevel
	}
	if *flagGraphMode != "" {
		cfg.GraphMode = *flagGraphMode
		cfg.GraphEnabled = *flagGraphMode != "disabled"
	}
	cfg.NoAscii = *flagNoAscii
	cfg.NoStartupText = *flagNoStartup

	// Handle any remaining positional args after flags.
	if args := flag.Args(); len(args) > 0 {
		switch args[0] {
		case "version":
			fmt.Println(version.Version)
			os.Exit(0)
		case "help":
			printUsage()
			os.Exit(0)
		case "env":
			printEnvVars()
			os.Exit(0)
		case "layout-recon":
			os.Exit(reconLayout(cfg.BaseDir))
		default:
			fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", args[0])
			printUsage()
			os.Exit(1)
		}
	}

	// ── Logger setup ──────────────────────────────────────────────────────────
	logger := zerolog.New(os.Stdout).With().
		Timestamp().
		Logger().
		Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	// Apply log level from config. This sets the operational level used after
	// initialisation is complete.
	operationalLevel := zerolog.InfoLevel
	switch cfg.LogLevel {
	case "debug":
		operationalLevel = zerolog.DebugLevel
	case "warn":
		operationalLevel = zerolog.WarnLevel
	case "error":
		operationalLevel = zerolog.ErrorLevel
	}
	zerolog.SetGlobalLevel(operationalLevel)
	logger = logger.Level(operationalLevel)

	// --verbose-init: lower the global level to debug for the initialisation
	// phase only. The operational logger passed to server.New is created at
	// operationalLevel, so once srv.Start() is called all package-level debug
	// output is suppressed regardless of this flag.
	if *flagVerboseInit {
		zerolog.SetGlobalLevel(zerolog.DebugLevel)
		logger.Info().Msg("[verbose-init] debug logging enabled for initialisation phase")
	}

	// Route global zerolog calls (from pkg/oql, pkg/dynconfig, pkg/blob/gc,
	// pkg/timeseries/retention) through the same logger so all output shares
	// the same writer and format.
	zlog.Logger = logger

	// Validate configuration
	if errs, warnings := cfg.Validate(); len(errs) > 0 || len(warnings) > 0 {
		for _, w := range warnings {
			logger.Warn().Str("config", w).Msg("Configuration warning")
		}
		if len(errs) > 0 {
			for _, e := range errs {
				logger.Error().Str("config", e).Msg("Configuration error")
			}
			logger.Fatal().Int("errors", len(errs)).Msg("Invalid configuration; exiting")
		}
	}

	// Print banner
	printBanner(cfg)

	// Create directories
	if err := os.MkdirAll(cfg.BaseDir, 0755); err != nil {
		logger.Fatal().Err(err).Msg("Failed to create base directory")
	}

	// Migration safety: if the data root holds data written by a pre-normalization
	// xolu (a base store at the root, or backend-first sql/ or ts/ groupings),
	// refuse to start. Creating fresh stores at the new invariant paths would
	// silently leave the old data orphaned. Detection runs before any new
	// directory or store is created.
	if model, scanErr := scanLayout(cfg.BaseDir); scanErr == nil {
		if findings := sl.DetectLegacy(model); len(findings) > 0 {
			logger.Error().Msg("Refusing to start: the data root contains data from a previous xolu layout.")
			for _, f := range findings {
				logger.Error().Str("path", f.Path).Msg(f.Message)
			}
			logger.Error().Msg("Run 'xolu layout-recon' to inspect the directory. " +
				"Move or migrate the legacy data, or point --base-dir at a fresh directory.")
			os.Exit(1)
		}
	}

	if err := os.MkdirAll(cfg.SchemaDir, 0755); err != nil {
		logger.Fatal().Err(err).Msg("Failed to create schema directory")
	}

	// Resolve the base store path from the data root by invariant. In per-file
	// mode the base store is tenant 0 (<BaseDir>/t0000/store/xolu.db); in shared
	// mode it is the single shared store (<BaseDir>/shared/store/xolu.db). There
	// is no separately configurable database path — only --base-dir.
	var baseDBPath string
	if cfg.SQLitePerFileTenants {
		baseDBPath = sl.TenantStorePath(cfg.BaseDir, 0)
	} else {
		baseDBPath = sl.SharedStorePath(cfg.BaseDir)
	}
	if err := os.MkdirAll(filepath.Dir(baseDBPath), 0755); err != nil {
		logger.Fatal().Err(err).Str("path", baseDBPath).Msg("Failed to create store directory")
	}

	// Initialize storage
	store, err := storage.NewStoreFromConfig(storage.StoreConfig{
		Type:                      cfg.StorageType,
		BaseDir:                   cfg.BaseDir,
		DBPath:                    baseDBPath,
		FullTextEnabled:           cfg.FullTextEnabled,
		GraphEnabled:              cfg.GraphEnabled,
		SQLiteCacheSize:           cfg.SQLiteCacheSize,
		SQLiteBusyTimeout:         cfg.SQLiteBusyTimeout,
		SQLiteMaxOpenConns:        cfg.SQLiteMaxOpenConns,
		SQLiteMaxIdleConns:        cfg.SQLiteMaxIdleConns,
		SQLiteReadPoolSize:        cfg.SQLiteReadPoolSize,
		SQLiteContentionThreshold: cfg.SQLiteContentionThreshold,
		SQLitePerFileTenants:      cfg.SQLitePerFileTenants,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("Failed to initialize storage")
	}
	defer func() { _ = store.Close() }()
	// Attach the application logger to the store if it supports it.
	if sqlStore, ok := store.(interface {
		WithLogger(zerolog.Logger) *storage.SQLiteStore
	}); ok {
		sqlStore.WithLogger(logger)
	}

	// Log store info
	if infoProvider, ok := store.(storage.InfoProvider); ok {
		info := infoProvider.Info()
		logger.Info().
			Str("type", info.Type).
			Str("version", info.Version).
			Bool("supports_search", info.SupportsSearch).
			Bool("supports_batch", info.SupportsBatch).
			Bool("supports_transaction", info.SupportsTransaction).
			Msg("Storage initialized")
	}

	// Initialize cache
	var cacheInstance cache.Cache
	if cfg.CacheType == "redis" {
		redisCache, err := cache.NewRedisCache(
			cfg.RedisHost,
			cfg.RedisPort,
			time.Duration(cfg.CacheTTL)*time.Second,
			cfg.RedisPoolSize,
			cfg.RedisMinIdleConns,
		)
		if err != nil {
			logger.Warn().Err(err).Msg("Failed to connect to Redis, falling back to memory cache")
			cacheInstance = cache.NewShardedMemoryCache(cfg.CacheSize, time.Duration(cfg.CacheTTL)*time.Second, cfg.CacheShards)
		} else {
			cacheInstance = redisCache
			logger.Info().Msg("Using Redis cache")
		}
	} else {
		cacheInstance = cache.NewShardedMemoryCache(cfg.CacheSize, time.Duration(cfg.CacheTTL)*time.Second, cfg.CacheShards)
		logger.Info().Msg("Using in-memory cache")
	}
	defer func() { _ = cacheInstance.Close() }()

	// Initialize graph
	var graphInstance graph.Graph
	if cfg.GraphEnabled && cfg.GraphMode == "flat" {
		graphInstance = graph.NewFlatGraphWithCycleDetection(cfg.GraphCycleDetection)
		if cfg.GraphCycleCheckLimit > 0 {
			graphInstance.(*graph.FlatGraph).SetCycleCheckLimit(cfg.GraphCycleCheckLimit)
		}

		// The graph lives in the store's per-tenant edge tables (SQLite
		// GraphEdgeScanner). The in-memory graph is a cache rebuilt from those
		// tables on every startup; there is no separate graph file.
		if err := loadEntitiesIntoGraph(cfg, store, graphInstance, logger); err != nil {
			// Partial hydration is worse than no hydration: an undefined subset of
			// edges would be in the in-memory graph, silently producing wrong query
			// results. Clear the graph so that the server starts with a known-empty
			// state. Operators can call POST /api/v1/graph/rebuild to repopulate
			// after the underlying cause (timeout, disk issue) is resolved.
			logger.Error().Err(err).Msg("Failed to load entities into graph — clearing in-memory graph to avoid partial state")
			if clearErr := graphInstance.Clear(); clearErr != nil {
				logger.Error().Err(clearErr).Msg("Failed to clear graph after hydration failure")
			}
		}

		logger.Info().Msg("Graph initialized")
	} else {
		logger.Info().Msg("Graph disabled")
	}

	// Initialize validator
	validator := validation.NewJSONSchemaValidator(cfg.SchemaDir)
	if err := validator.LoadAllSchemas(); err != nil {
		logger.Warn().Err(err).Msg("Failed to load schemas")
	}

	// Sync adapted tables: for every loaded schema, ensure an adapted
	// table exists. This handles schemas added to the directory while
	// the server was down. RegisterAdaptedEntity is idempotent — it
	// skips tables whose schema hash hasn't changed.
	if sqlStore, ok := store.(*storage.SQLiteStore); ok {
		syncCtx, syncCancel := context.WithTimeout(context.Background(), 30*time.Second)
		for _, entity := range validator.LoadedEntities() {
			raw, err := validator.GetSchema(entity)
			if err != nil {
				continue
			}
			if err := sqlStore.RegisterAdaptedEntity(syncCtx, entity, raw); err != nil {
				logger.Warn().Err(err).Str("entity", entity).Msg("Failed to register adapted table at startup")
			}
		}
		syncCancel()
	}

	// Create server
	srv := server.New(cfg, store, cacheInstance, graphInstance, validator, logger)

	// Initialisation is complete. Restore the operational log level so that
	// request-handling debug output is not emitted unless --log-level debug
	// was also set. This is the boundary between init and operation.
	if *flagVerboseInit {
		zerolog.SetGlobalLevel(operationalLevel)
		logger.Info().Msg("[verbose-init] initialisation complete; reverting to operational log level")
	}

	// Setup graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.Info().Msg("Shutting down gracefully...")

		// Give in-flight requests up to 15 seconds to complete
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		// Shut down HTTP server first (stops accepting new requests,
		// waits for in-flight requests to finish)
		if err := srv.Shutdown(ctx); err != nil {
			logger.Error().Err(err).Msg("HTTP server shutdown error")
		}

		// Stop rate limiter cleanup
		srv.Stop()
	}()

	// Start server (blocks until Shutdown is called or a fatal error occurs)
	logger.Info().Msg("Server ready to accept requests")
	if err := srv.Start(); err != nil && err != http.ErrServerClosed {
		logger.Fatal().Err(err).Msg("Server failed")
	}

	// After Shutdown, main returns and defers execute (store.Close, cache.Close)
	logger.Info().Msg("Server stopped")
}

// rulerWidth is the width of horizontal separator lines in startup output.
// Matches the banner's 60-char width (v0.14.19+).
const rulerWidth = 60

// centredVersionLine builds a ruler line with the version string centred.
// Example: "/////////////////////////// xolu 0.9.9 ///////////////////////////"
func centredVersionLine(ver string) string {
	label := " xolu " + ver + " "
	inner := rulerWidth - 4
	pad := inner - len(label)
	if pad < 0 {
		pad = 0
	}
	left := pad / 2
	right := pad - left
	return "//" + repeatStr("/", left) + label + repeatStr("/", right) + "//"
}

func repeatStr(s string, n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	for i := range b {
		b[i] = s[0]
	}
	return string(b)
}

//go:embed banner.txt
var bannerAscii string

// printAscii prints the ASCII art box. Suppressed by XOLU_NO_ASCII=true.
// The banner content lives in banner.txt beside this file and is embedded
// at build time via //go:embed. Regenerated with NV Script figlet from
// v0.14.15.
func printAscii(cfg *config.Config) {
	if cfg.NoAscii {
		return
	}
	const lightBlue = "\033[1;36m"
	const reset = "\033[0m"
	fmt.Print(lightBlue)
	fmt.Print(bannerAscii)
	fmt.Print(reset)
	fmt.Println()
}

// printStartupText prints the configuration summary. Suppressed by XOLU_NO_STARTUP_TEXT=true.
func printStartupText(cfg *config.Config) {
	if cfg.NoStartupText {
		return
	}
	ruler := repeatStr("/", rulerWidth)
	sep := repeatStr("-", rulerWidth)

	fmt.Println(centredVersionLine(version.Version))
	fmt.Println(ruler)
	fmt.Println()

	// Network
	fmt.Println("Network:")
	host := cfg.Host
	if host == "" || host == "0.0.0.0" {
		host = "0.0.0.0 (all interfaces)"
	}
	fmt.Printf("  Listen:  %s:%d\n", host, cfg.Port)
	if cfg.MetricsPort > 0 {
		metricsHost := cfg.MetricsHost
		if metricsHost == "" {
			metricsHost = "0.0.0.0"
		}
		fmt.Printf("  Metrics: %s:%d (dedicated)\n", metricsHost, cfg.MetricsPort)
	}
	if len(cfg.CORSOrigins) > 0 {
		fmt.Printf("  CORS:    %v\n", cfg.CORSOrigins)
	}

	// Auth
	if cfg.AuthType != "" && cfg.AuthType != "none" {
		fmt.Println()
		fmt.Println("Auth:")
		fmt.Printf("  Type: %s\n", cfg.AuthType)
		if cfg.AuthType == "jwt" && cfg.JWTIssuer != "" {
			fmt.Printf("  JWT issuer: %s\n", cfg.JWTIssuer)
		}
		if cfg.RateLimitEnabled {
			fmt.Printf("  Rate limit: %d req / %ds\n", cfg.RateLimitRate, cfg.RateLimitWindow)
		}
	}

	// Storage
	fmt.Println()
	fmt.Println("Storage:")
	fmt.Printf("  Type: %s\n", cfg.StorageType)
	fmt.Printf("  Data root: %s\n", cfg.BaseDir)
	if cfg.StorageType == "sqlite" {
		if cfg.SQLitePerFileTenants {
			fmt.Printf("  Store: %s\n", sl.TenantStorePath(cfg.BaseDir, 0))
			fmt.Println("  Per-file tenants: enabled")
		} else {
			fmt.Printf("  Store: %s\n", sl.SharedStorePath(cfg.BaseDir))
		}
	}

	// Cache
	fmt.Println()
	fmt.Println("Cache:")
	if cfg.CacheType == "redis" {
		fmt.Printf("  Backend: Redis (%s:%d)\n", cfg.RedisHost, cfg.RedisPort)
	} else {
		fmt.Printf("  Backend: memory  shards=%d  capacity=%d\n", cfg.CacheShards, cfg.CacheSize)
	}
	fmt.Printf("  Entity TTL:            %ds\n", cfg.CacheTTL)
	if cfg.GraphQueryCacheTTL > 0 {
		fmt.Printf("  Graph query cache TTL: %ds\n", cfg.GraphQueryCacheTTL)
	} else {
		fmt.Println("  Graph query cache:     disabled")
	}
	if cfg.OQLQueryCacheTTL > 0 {
		fmt.Printf("  OQL query cache TTL:   %ds\n", cfg.OQLQueryCacheTTL)
	} else {
		fmt.Println("  OQL query cache:       disabled")
	}

	// Graph
	fmt.Println()
	fmt.Println("Graph:")
	if cfg.GraphEnabled {
		fmt.Printf("  Mode:               %s\n", cfg.GraphMode)
		fmt.Printf("  Cycle detection:    %s\n", cfg.GraphCycleDetection)
		fmt.Printf("  Max depth:          %d hops\n", cfg.MaxQueryDepth)
		if cfg.GraphMaxVisitedNodes > 0 {
			fmt.Printf("  Max visited nodes:  %d\n", cfg.GraphMaxVisitedNodes)
		}
		fmt.Printf("  Async job retention: %ds\n", cfg.AsyncJobRetentionTTL)
	} else {
		fmt.Println("  Disabled")
	}

	// Tenancy
	fmt.Println()
	fmt.Println("Tenancy:")
	fmt.Printf("  Mode: %s\n", cfg.TenantMode)
	if cfg.TenantAutoRegister {
		fmt.Println("  Auto-register: enabled")
	}

	// Non-default enabled subsystems
	type feature struct{ name, detail string }
	var features []feature
	if cfg.FullTextEnabled {
		features = append(features, feature{"full-text search", ""})
	}
	if cfg.TimeseriesEnabled {
		features = append(features, feature{"timeseries", ""})
	}
	if cfg.BlobEnabled {
		d := ""
		if cfg.S3Enabled {
			d = "+ S3 API"
		}
		features = append(features, feature{"blob storage", d})
	}
	if cfg.DynConfigEnabled {
		features = append(features, feature{"dynconfig", sl.DynConfigPath(cfg.BaseDir)})
	}
	if cfg.CascadingDelete {
		features = append(features, feature{"cascading delete", ""})
	}
	if len(features) > 0 {
		fmt.Println()
		fmt.Println("Features:")
		for _, f := range features {
			if f.detail != "" {
				fmt.Printf("  %s  (%s)\n", f.name, f.detail)
			} else {
				fmt.Printf("  %s\n", f.name)
			}
		}
	}

	// Behaviour — only non-default values
	fmt.Println()
	fmt.Println("Behaviour:")
	fmt.Printf("  Schema directory:  %s\n", cfg.SchemaDir)
	fmt.Printf("  REF embed depth:   %d\n", cfg.RefEmbedDepth)
	if cfg.PatchNullBehavior != "" && cfg.PatchNullBehavior != "store" {
		fmt.Printf("  Patch null:        %s\n", cfg.PatchNullBehavior)
	}
	if cfg.QueryTimeout > 0 {
		fmt.Printf("  Query timeout:     %ds\n", cfg.QueryTimeout)
	}
	if cfg.LogLevel != "" && cfg.LogLevel != "info" {
		fmt.Printf("  Log level:         %s\n", cfg.LogLevel)
	}

	fmt.Println()
	fmt.Println(sep)
	fmt.Println()
}

// printBanner is the combined entry point called from main.
// cfg.NoAscii and cfg.NoStartupText are honoured independently.
func printBanner(cfg *config.Config) {
	printAscii(cfg)
	printStartupText(cfg)
}

func loadEntitiesIntoGraph(
	cfg *config.Config,
	store storage.Store,
	g graph.Graph,
	logger zerolog.Logger,
) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Fast path: if the store can stream edges directly from its edge table,
	// use that instead of deserialising full entity JSON. This is O(edges)
	// rather than O(entities × JSON size).
	if scanner, ok := store.(storage.GraphEdgeScanner); ok {
		return loadEntitiesFromEdgeTable(ctx, scanner, g, logger)
	}

	// Slow path: deserialise all entities and extract REF fields in Go.
	// Used by any backend that does not implement
	// GraphEdgeScanner.
	return loadEntitiesFromStore(ctx, cfg, store, g, logger)
}

// loadEntitiesFromEdgeTable is the fast hydration path for stores that
// implement GraphEdgeScanner. It reads one row per edge — five narrow columns
// — and calls AddNode + AddEdge directly, never allocating entity JSON.
//
// If the store also implements TenantIDLister, all registered tenants
// (including tenant 0) are hydrated uniformly. Tenants with no edges are
// silently skipped.
func loadEntitiesFromEdgeTable(
	ctx context.Context,
	scanner storage.GraphEdgeScanner,
	g graph.Graph,
	logger zerolog.Logger,
) error {
	lister, ok := scanner.(storage.TenantIDLister)
	if !ok {
		// Store does not enumerate tenants; fall back to hydrating tenant 0 only.
		count, err := scanTenantEdges(ctx, scanner, g, 0, logger)
		if err != nil {
			return err
		}
		if count > 0 {
			logger.Info().Int("edges", count).Uint16("tenant", 0).Msg("Loaded tenant graph from edge table")
		}
		if n := g.VodeCount(); n > 0 {
			vodes := g.GetNodesByType(graph.NodeTypeVode)
			sample := vodes
			if len(sample) > 10 {
				sample = sample[:10]
			}
			wev := logger.Warn().Int("vode_count", n).Strs("vode_sample", sample)
			if len(vodes) > 10 {
				wev = wev.Int("vode_remaining", len(vodes)-10)
			}
			wev.Msg("Graph hydration complete but vode nodes remain — REF targets not yet written to store")
		}
		return nil
	}

	tenantIDs, err := lister.GraphTenantIDs(ctx)
	if err != nil {
		// Non-fatal: log and continue with an empty graph.
		logger.Warn().Err(err).Msg("loadEntitiesFromEdgeTable: could not enumerate tenants; graph will be empty")
		return nil
	}
	for _, tid := range tenantIDs {
		n, err := scanTenantEdges(ctx, scanner, g, tid, logger)
		if err != nil {
			logger.Warn().Err(err).Uint16("tenant", tid).Msg("loadEntitiesFromEdgeTable: tenant hydration failed; skipping")
			continue
		}
		if n > 0 {
			logger.Info().Int("edges", n).Uint16("tenant", tid).Msg("Loaded tenant graph from edge table")
		}
	}
	if n := g.VodeCount(); n > 0 {
		vodes := g.GetNodesByType(graph.NodeTypeVode)
		sample := vodes
		if len(sample) > 10 {
			sample = sample[:10]
		}
		wev := logger.Warn().Int("vode_count", n).Strs("vode_sample", sample)
		if len(vodes) > 10 {
			wev = wev.Int("vode_remaining", len(vodes)-10)
		}
		wev.Msg("Graph hydration complete but vode nodes remain — REF targets not yet written to store")
	}
	return nil
}

// scanTenantEdges streams all edges for one tenant into the graph.
// Returns the number of edge rows processed.
func scanTenantEdges(
	ctx context.Context,
	scanner storage.GraphEdgeScanner,
	g graph.Graph,
	tid uint16,
	logger zerolog.Logger,
) (int, error) {
	count := 0
	err := scanner.ScanGraphEdges(ctx, tid, func(e storage.GraphEdge) error {
		if err := g.AddNode(tenant.NodeID(tid, e.SourceEntity, e.SourceID), e.SourceEntity); err != nil {
			logger.Warn().Err(err).
				Str("source", e.SourceEntity).Int("id", e.SourceID).
				Msg("scanTenantEdges: AddNode source failed")
		}
		if err := g.AddNode(tenant.NodeID(tid, e.TargetEntity, e.TargetID), e.TargetEntity); err != nil {
			logger.Warn().Err(err).
				Str("target", e.TargetEntity).Int("id", e.TargetID).
				Msg("scanTenantEdges: AddNode target failed")
		}
		if err := g.AddEdge(
			tenant.NodeID(tid, e.SourceEntity, e.SourceID),
			tenant.NodeID(tid, e.TargetEntity, e.TargetID),
			e.Relationship,
		); err != nil {
			logger.Warn().Err(err).
				Str("source", e.SourceEntity).Str("target", e.TargetEntity).
				Str("rel", e.Relationship).
				Msg("scanTenantEdges: AddEdge failed")
		}
		count++
		return nil
	})
	return count, err
}

// loadEntitiesFromStore is the slow hydration path: deserialises all entities
// and calls UpdateFromEntityForTenant to extract REF fields. Used by the
// any backend that does not implement GraphEdgeScanner.
func loadEntitiesFromStore(
	ctx context.Context,
	cfg *config.Config,
	store storage.Store,
	g graph.Graph,
	logger zerolog.Logger,
) error {
	schemaPath := sl.SchemaDir(cfg.BaseDir)

	// Always hydrate tenant 0 (the default unscoped namespace).
	count := loadTenantEntitiesFromStore(ctx, cfg, store, g, 0, schemaPath, logger)
	logger.Info().Int("count", count).Uint16("tenant", 0).Msg("Loaded tenant-0 entities into graph")

	// Hydrate any non-zero tenants whose data directories exist on disk.
	// Tenant stores use subdirectories inside the data path
	// (e.g. schema/t0001/users/1.json). We enumerate those directories and
	// parse the tenant ID from the name so each tenant's graph nodes carry
	// the correct XXXX@ prefix.
	top, err := os.ReadDir(schemaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range top {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Match the "tXXXX" format produced by tenant.StorageDirSegment.
		if len(name) != 5 || name[0] != 't' {
			continue
		}
		parsed, err := strconv.ParseUint(name[1:], 16, 16)
		if err != nil || parsed == 0 {
			continue
		}
		tid := uint16(parsed)
		tenantSchemaPath := filepath.Join(schemaPath, name)
		n := loadTenantEntitiesFromStore(ctx, cfg, store, g, tid, tenantSchemaPath, logger)
		if n > 0 {
			logger.Info().Int("count", n).Uint16("tenant", tid).Msg("Loaded tenant entities into graph")
		}
	}

	return nil
}

// loadTenantEntitiesFromStore hydrates one tenant's graph nodes from the
// entity JSON files under schemaPath. It lists entity directories, reads
// every entity via the store, and calls UpdateFromEntityForTenant with the
// given tenant ID. Returns the number of entities successfully added.
func loadTenantEntitiesFromStore(
	ctx context.Context,
	cfg *config.Config,
	store storage.Store,
	g graph.Graph,
	tid uint16,
	schemaPath string,
	logger zerolog.Logger,
) int {
	entries, err := os.ReadDir(schemaPath)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn().Err(err).Str("path", schemaPath).Msg("loadTenantEntitiesFromStore: ReadDir failed")
		}
		return 0
	}

	// For non-zero tenants the store must be scoped to that tenant so that
	// List returns the right data directory. We construct a temporary scoped
	// store using the same base configuration.
	var scopedStore storage.Store
	if tid == 0 {
		scopedStore = store
	} else {
		baseCfg := store.Config()
		scopedStore, err = storage.NewStoreFromConfig(storage.StoreConfig{
			Type:                 baseCfg.Type,
			TenantID:             tid,
			SQLitePerFileTenants: baseCfg.SQLitePerFileTenants,
		})
		if err != nil {
			logger.Warn().Err(err).Uint16("tenant", tid).Msg("loadTenantEntitiesFromStore: could not create scoped store; skipping tenant")
			return 0
		}
		defer func() { _ = scopedStore.Close() }()
	}

	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		entityName := entry.Name()
		entities, err := scopedStore.List(ctx, entityName)
		if err != nil {
			logger.Warn().Err(err).Str("entity", entityName).Uint16("tenant", tid).Msg("Failed to list entities")
			continue
		}
		for _, data := range entities {
			id, ok := data["id"].(float64)
			if !ok {
				if idInt, ok := data["id"].(int); ok {
					id = float64(idInt)
				} else {
					continue
				}
			}
			if err := g.UpdateFromEntityForTenant(tid, entityName, int(id), data); err != nil {
				logger.Warn().Err(err).
					Str("entity", entityName).
					Int("id", int(id)).
					Uint16("tenant", tid).
					Msg("Failed to add entity to graph")
			} else {
				count++
			}
		}
	}
	return count
}

func printUsage() {
	ruler := repeatStr("/", rulerWidth)
	sep := repeatStr("-", rulerWidth)

	fmt.Println(centredVersionLine(version.Version))
	fmt.Println(ruler)
	fmt.Println()
	fmt.Println("xolu is a JSON document store with a graph layer, OQL query engine,")
	fmt.Println("tenant isolation, full-text search, timeseries, blob storage, and")
	fmt.Println("a Sulpher/Cypher-compatible graph query interface.")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  xolu [flags]             Start the server")
	fmt.Println("  xolu version             Print version and exit")
	fmt.Println("  xolu help                Show this message")
	fmt.Println("  xolu env                 List all environment variables")
	fmt.Println()
	fmt.Println("Flags:")
	fmt.Println("  --port <n>              HTTP listen port (default: 9090; env: XOLU_PORT)")
	fmt.Println("  --host <addr>           Bind address   (default: 0.0.0.0; env: XOLU_HOST)")
	fmt.Println("  --db <path>             Database path  (default: data/xolu.db; env: XOLU_DB_PATH)")
	fmt.Println("  --base-dir <path>       Base data directory (default: data; env: XOLU_BASE_DIR)")
	fmt.Println("  --schema <name>         Schema name    (default: schema; env: XOLU_SCHEMA_NAME)")
	fmt.Println("  --log-level <level>     debug | info | warn | error (env: XOLU_LOG_LEVEL)")
	fmt.Println("  --graph-mode <mode>     flat | disabled (env: XOLU_GRAPH_MODE)")
	fmt.Println("  --no-ascii              Suppress ASCII art at startup (env: XOLU_NO_ASCII)")
	fmt.Println("  --no-startup-text       Suppress configuration summary (env: XOLU_NO_STARTUP_TEXT)")
	fmt.Println("  --verbose-init          Debug-level logging during initialisation only")
	fmt.Println()
	fmt.Println("All other settings are configured via environment variables.")
	fmt.Println("Run 'xolu env' for the full list.")
	fmt.Println()
	fmt.Println(sep)
	fmt.Println()
}

func printEnvVars() {
	ruler := repeatStr("/", rulerWidth)
	sep := repeatStr("-", rulerWidth)

	fmt.Println(centredVersionLine(version.Version))
	fmt.Println(ruler)
	fmt.Println()
	fmt.Println("Environment variables  (flags in parentheses where a flag equivalent exists)")
	fmt.Println()
	fmt.Println(sep)
	fmt.Println()
	fmt.Println("  Network")
	fmt.Println("    XOLU_HOST                    Bind address (--host; default: 0.0.0.0)")
	fmt.Println("    XOLU_PORT                    HTTP port (--port; default: 9090)")
	fmt.Println("    XOLU_METRICS_PORT            Dedicated metrics port (0 = shared)")
	fmt.Println("    XOLU_CORS_ORIGINS            Comma-separated allowed CORS origins")
	fmt.Println()
	fmt.Println("  Storage")
	fmt.Println("    XOLU_STORAGE_TYPE            Storage backend: sqlite (default)")
	fmt.Println("    XOLU_DB_PATH                 SQLite database path (--db; default: data/xolu.db)")
	fmt.Println("    XOLU_BASE_DIR                Base data directory (--base-dir; default: data)")
	fmt.Println("    XOLU_SCHEMA_DIR              Schema directory (default: {base_dir}/schema)")
	fmt.Println("    XOLU_SCHEMA_NAME             Schema name (--schema; default: schema)")
	fmt.Println()
	fmt.Println("  Cache")
	fmt.Println("    XOLU_CACHE_TYPE              memory (default) or redis")
	fmt.Println("    XOLU_CACHE_TTL               Entity cache TTL in seconds (default: 300)")
	fmt.Println("    XOLU_CACHE_SIZE              In-memory cache capacity (default: 1024)")
	fmt.Println("    XOLU_CACHE_SHARDS            Shard count for memory cache (default: 16)")
	fmt.Println("    XOLU_REDIS_HOST              Redis host (default: localhost)")
	fmt.Println("    XOLU_REDIS_PORT              Redis port (default: 6379)")
	fmt.Println("    XOLU_GRAPH_QUERY_CACHE_TTL   Sulpher result cache TTL in seconds (default: 30)")
	fmt.Println("    XOLU_OQL_QUERY_CACHE_TTL     OQL result cache TTL in seconds (default: 30)")
	fmt.Println()
	fmt.Println("  Graph")
	fmt.Println("    XOLU_GRAPH_MODE              flat (--graph-mode; default) or disabled")
	fmt.Println("    XOLU_GRAPH_CYCLE_DETECTION   warn (default), error, or ignore")
	fmt.Println("    XOLU_GRAPH_MAX_VISITED_NODES BFS node budget per query (default: 10000)")
	fmt.Println("    XOLU_ASYNC_JOB_RETENTION_TTL Async job record lifetime seconds (default: 86400)")
	fmt.Println()
	fmt.Println("  Auth")
	fmt.Println("    XOLU_AUTH_TYPE               none (default), jwt, apikey, bearertoken")
	fmt.Println("    XOLU_JWT_SECRET              Secret for JWT validation")
	fmt.Println("    XOLU_API_KEYS                Comma-separated API keys for apikey auth")
	fmt.Println("    XOLU_INTERNAL_TOKEN          Shared secret for bearertoken auth")
	fmt.Println("    XOLU_RATE_LIMIT_ENABLED      Enable rate limiting (default: false)")
	fmt.Println("    XOLU_RATE_LIMIT_RATE         Requests per window (default: 100)")
	fmt.Println("    XOLU_RATE_LIMIT_WINDOW       Window in seconds (default: 60)")
	fmt.Println()
	fmt.Println("  Tenancy")
	fmt.Println("    XOLU_TENANT_MODE             path (default) or strict")
	fmt.Println("    XOLU_TENANT_AUTO_REGISTER    Auto-register unknown tenants (default: true)")
	fmt.Println()
	fmt.Println("  Subsystems (disabled by default)")
	fmt.Println("    XOLU_FULLTEXT_ENABLED        Enable full-text search")
	fmt.Println("    XOLU_TIMESERIES_ENABLED      Enable timeseries storage")
	fmt.Println("    XOLU_BLOB_ENABLED            Enable blob storage")
	fmt.Println("    XOLU_S3_ENABLED              Enable S3-compatible blob API")
	fmt.Println("    XOLU_DYNCONFIG_ENABLED       Enable dynamic config system")
	fmt.Println("    XOLU_CASCADING_DELETE        Enable cascading entity deletion")
	fmt.Println()
	fmt.Println("  Startup output")
	fmt.Println("    XOLU_NO_ASCII                Suppress ASCII art (--no-ascii)")
	fmt.Println("    XOLU_NO_STARTUP_TEXT         Suppress config summary (--no-startup-text)")
	fmt.Println("    (no env equivalent)         --verbose-init: debug logging during init only")
	fmt.Println()
	fmt.Println("  Logging")
	fmt.Println("    XOLU_LOG_LEVEL               debug, info (default), warn, error (--log-level)")
	fmt.Println()
	fmt.Println(sep)
	fmt.Println()
}
