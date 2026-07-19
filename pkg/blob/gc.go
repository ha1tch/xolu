// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package blob

// GC performs periodic garbage collection of unreferenced blobs.
//
// Algorithm — two phases, filesystem-native, no database required:
//
//  1. Mark: walk .keys/ for each tenant, read every alias file, accumulate
//     the set of live SHAs. Cost is O(key count), not O(blob count).
//
//  2. Sweep: walk the {xx}/ prefix directories, check each blob filename
//     against the live set. Unreferenced blobs are not deleted immediately —
//     they are moved to .gc-pending/{sha} with the current timestamp appended
//     as .{unix}. Blobs that have been in quarantine for longer than
//     GracePeriod are hard-deleted.
//
// The quarantine step protects against the race where a Put has written the
// blob file but has not yet written the key alias when the sweep runs. A
// grace period of ten minutes is safe for any realistic workload.
//
// External SHA references (e.g. blobs referenced by the timeseries history
// store but having no key alias) are handled via the SHARefSource interface.
// The GC calls CollectLiveSHAs before sweeping; any SHA returned there is
// treated as live. Pass nil when no external reference source exists.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gcpkg "github.com/ha1tch/xolu/pkg/gc"
	"github.com/rs/zerolog/log"
)

// SHARefSource is implemented by subsystems that hold SHA references outside
// the key-alias index (e.g. the timeseries history store). The GC calls
// CollectLiveSHAs once per sweep cycle and treats every returned SHA as live.
type SHARefSource interface {
	CollectLiveSHAs() (map[string]struct{}, error)
}

// GCConfig holds tunable parameters for the GC worker.
type GCConfig struct {
	// Interval between GC sweeps. Default: 1 hour.
	Interval time.Duration
	// GracePeriod is how long a blob must sit in .gc-pending/ before it is
	// hard-deleted. Default: 10 minutes.
	GracePeriod time.Duration
}

// DefaultGCConfig returns a GCConfig with production-safe defaults.
func DefaultGCConfig() GCConfig {
	return GCConfig{
		Interval:    time.Hour,
		GracePeriod: 10 * time.Minute,
	}
}

// GCWorker runs periodic mark-and-sweep GC against a Store.
// It is safe to create only one GCWorker per Store.
type GCWorker struct {
	store   *Store
	cfg     GCConfig
	extRefs SHARefSource // may be nil
	stop    chan struct{}
	done    chan struct{}
}

// NewGCWorker creates a GCWorker. extRefs may be nil when there are no
// external SHA reference sources.
func NewGCWorker(s *Store, cfg GCConfig, extRefs SHARefSource) *GCWorker {
	return &GCWorker{
		store:   s,
		cfg:     cfg,
		extRefs: extRefs,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Start launches the GC goroutine.
func (w *GCWorker) Start() {
	go w.run()
}

// Stop signals the GC to stop and blocks until it has exited.
func (w *GCWorker) Stop() {
	close(w.stop)
	<-w.done
}

// RunOnce executes a single mark-and-sweep cycle synchronously. Useful for
// tests and for a one-shot manual trigger from an admin endpoint.
func (w *GCWorker) RunOnce() GCReport {
	return w.sweep()
}

func (w *GCWorker) run() {
	defer close(w.done)
	ticker := time.NewTicker(w.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			r := w.sweep()
			log.Info().
				Int("quarantined", r.Quarantined).
				Int("deleted", r.Deleted).
				Int("errors", r.Errors).
				Int("tenants", r.TenantsScanned).
				Msg("blob GC sweep complete")
		}
	}
}

// ---------------------------------------------------------------------------
// GCReport is returned by RunOnce for inspection in tests and admin handlers.
// ---------------------------------------------------------------------------

// GCReport summarises the result of a single GC sweep.
type GCReport struct {
	TenantsScanned int
	Quarantined    int // blobs moved to .gc-pending this cycle
	Deleted        int // blobs hard-deleted from .gc-pending this cycle
	Errors         int
}

// ---------------------------------------------------------------------------
// Core sweep
// ---------------------------------------------------------------------------

func (w *GCWorker) sweep() GCReport {
	root := w.store.Root()
	var report GCReport

	// Build the external live SHA set first (one call, not per-tenant).
	extLive := map[string]struct{}{}
	if w.extRefs != nil {
		if shas, err := w.extRefs.CollectLiveSHAs(); err != nil {
			log.Warn().Err(err).Msg("blob GC: external ref source error")
			report.Errors++
		} else {
			extLive = shas
		}
	}

	// A Store is single-tenant: its root is the tenant's blobs directory,
	// holding .keys/ and the {xx}/ shard directories directly. Sweep it.
	r := w.sweepTenant(root, extLive)
	report.TenantsScanned = 1
	report.Quarantined += r.Quarantined
	report.Deleted += r.Deleted
	report.Errors += r.Errors
	return report
}

func (w *GCWorker) sweepTenant(tenantDir string, extLive map[string]struct{}) GCReport {
	var report GCReport

	// ------------------------------------------------------------------
	// Mark: collect live SHAs from the key-alias index.
	// ------------------------------------------------------------------
	liveSHAs := map[string]struct{}{}
	for sha := range extLive {
		liveSHAs[sha] = struct{}{}
	}

	keysDir := filepath.Join(tenantDir, ".keys")
	keyEntries, err := os.ReadDir(keysDir)
	if err != nil && !os.IsNotExist(err) {
		log.Warn().Err(err).Str("dir", keysDir).Msg("blob GC: cannot read .keys dir")
		report.Errors++
		// Don't abort: sweep phase with an incomplete live set is worse than
		// not sweeping. But we *do* abort if we can't trust the live set at all.
		return report
	}
	for _, ke := range keyEntries {
		if ke.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(keysDir, ke.Name()))
		if err != nil {
			// Race: key deleted between ReadDir and ReadFile. Not an error.
			continue
		}
		sha := strings.TrimSpace(string(data))
		if sha != "" {
			liveSHAs[sha] = struct{}{}
		}
	}

	// ------------------------------------------------------------------
	// Sweep: walk the {xx}/ prefix shard directories.
	// ------------------------------------------------------------------
	pendingDir := filepath.Join(tenantDir, ".gc-pending")

	prefixEntries, err := os.ReadDir(tenantDir)
	if err != nil {
		log.Warn().Err(err).Str("dir", tenantDir).Msg("blob GC: cannot read tenant dir")
		report.Errors++
		return report
	}

	now := time.Now()

	for _, pe := range prefixEntries {
		name := pe.Name()
		// Skip internal directories and anything that isn't a two-hex prefix shard.
		if !pe.IsDir() || name == ".keys" || name == ".gc-pending" || len(name) != 2 {
			continue
		}
		if !isHexPrefix(name) {
			continue
		}

		shardDir := filepath.Join(tenantDir, name)
		blobs, err := os.ReadDir(shardDir)
		if err != nil {
			log.Warn().Err(err).Str("dir", shardDir).Msg("blob GC: cannot read shard dir")
			report.Errors++
			continue
		}

		for _, be := range blobs {
			bName := be.Name()
			// Skip sidecar files (.ct, .md5 suffixes) and temp files.
			if strings.HasSuffix(bName, ".ct") || strings.HasSuffix(bName, ".md5") || strings.HasPrefix(bName, ".") {
				continue
			}
			sha := bName
			if _, live := liveSHAs[sha]; live {
				continue
			}
			// Unreferenced blob — quarantine it.
			blobPath := filepath.Join(shardDir, bName)
			if err := w.quarantine(blobPath, pendingDir, now); err != nil {
				log.Warn().Err(err).Str("blob", blobPath).Msg("blob GC: quarantine failed")
				report.Errors++
			} else {
				report.Quarantined++
			}
		}
	}

	// ------------------------------------------------------------------
	// Hard-delete blobs that have been in quarantine long enough.
	// Pass the current live set so purgePending can skip blobs that became
	// live again during the grace period (e.g. a key alias was written after
	// the blob was quarantined but before the grace period expired).
	// ------------------------------------------------------------------
	deleted, errs := w.purgePending(pendingDir, tenantDir, now, liveSHAs)
	report.Deleted += deleted
	report.Errors += errs

	return report
}

// quarantine moves blobPath (and its .ct sidecar if present) into pendingDir,
// naming them {sha}.{unix_nano} so the sweep knows when they were quarantined.
func (w *GCWorker) quarantine(blobPath, pendingDir string, now time.Time) error {
	if err := os.MkdirAll(pendingDir, 0755); err != nil {
		return fmt.Errorf("mkdir pending: %w", err)
	}
	sha := filepath.Base(blobPath)
	stamp := strconv.FormatInt(now.UnixNano(), 10)
	dest := filepath.Join(pendingDir, sha+"."+stamp)

	if err := os.Rename(blobPath, dest); err != nil {
		return err
	}
	// Best-effort: move the .ct and .md5 sidecars too so they don't litter the
	// shard.
	ctSrc := blobPath + ".ct"
	if _, err := os.Stat(ctSrc); err == nil {
		_ = os.Rename(ctSrc, dest+".ct")
	}
	md5Src := blobPath + ".md5"
	if _, err := os.Stat(md5Src); err == nil {
		_ = os.Rename(md5Src, dest+".md5")
	}
	return nil
}

// purgePending hard-deletes anything in pendingDir whose timestamp is older
// than GracePeriod AND whose SHA is still not in liveSHAs. The live-set
// re-check prevents deleting a blob that gained a key alias (or an external
// reference) during the grace period after being quarantined.
func (w *GCWorker) purgePending(pendingDir, tenantDir string, now time.Time, liveSHAs map[string]struct{}) (deleted, errs int) {
	entries, err := os.ReadDir(pendingDir)
	if os.IsNotExist(err) {
		return 0, 0
	}
	if err != nil {
		return 0, 1
	}

	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".ct") || strings.HasSuffix(name, ".md5") {
			continue // cleaned up alongside its parent below
		}

		// Name format: {sha}.{unix_nano}
		dot := strings.LastIndexByte(name, '.')
		if dot < 0 {
			continue // unexpected file; leave it alone
		}
		nanoStr := name[dot+1:]
		nanos, err := strconv.ParseInt(nanoStr, 10, 64)
		if err != nil {
			continue
		}
		quarantinedAt := time.Unix(0, nanos).UTC()
		if now.Sub(quarantinedAt) < w.cfg.GracePeriod {
			continue // still within grace period
		}

		sha := name[:dot]
		if _, live := liveSHAs[sha]; live {
			// Blob became live again during grace period — restore it to its
			// shard directory so it is accessible for reads.
			pendingPath := filepath.Join(pendingDir, name)
			blobPath := filepath.Join(tenantDir, sha[:2], sha)
			if mkErr := os.MkdirAll(filepath.Dir(blobPath), 0755); mkErr == nil {
				if renErr := os.Rename(pendingPath, blobPath); renErr == nil {
					_ = os.Rename(pendingPath+".ct", blobPath+".ct")
					_ = os.Rename(pendingPath+".md5", blobPath+".md5")
				}
			}
			continue
		}

		path := filepath.Join(pendingDir, name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs++
		} else {
			deleted++
		}
		// Remove .ct and .md5 sidecars if they exist.
		_ = os.Remove(path + ".ct")
		_ = os.Remove(path + ".md5")
	}
	return deleted, errs
}

// ---------------------------------------------------------------------------
// gc.Sweeper adapter
// ---------------------------------------------------------------------------

// Sweep implements gc.Sweeper. It runs one full mark-and-sweep cycle and
// maps the blob-specific GCReport to the shared gc.Report type.
// The sweep logic is unchanged; this method is the adapter layer only.
func (w *GCWorker) Sweep(_ context.Context) (gcpkg.Report, error) {
	r := w.sweep()
	return gcpkg.Report{
		Examined:    r.TenantsScanned,
		Collected:   r.Deleted,
		Quarantined: r.Quarantined,
		Errors:      r.Errors,
	}, nil
}
