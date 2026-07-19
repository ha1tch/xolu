// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package timeseries

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	xoluerr "github.com/ha1tch/xolu/pkg/errors"
)

// ─── rollup registry ─────────────────────────────────────────────────────────

const rollupDefsFile = "rollup_defs.json"

// rollupRegistry persists and manages rollup definitions for a store.
// All methods that mutate state persist to disk before returning.
type rollupRegistry struct {
	mu   sync.RWMutex
	dir  string
	defs map[RollupID]*RollupDef // keyed by RollupID
}

type rollupDisk struct {
	Defs []RollupDef `json:"defs"`
}

func loadRollupRegistry(dir string) (*rollupRegistry, error) {
	rr := &rollupRegistry{
		dir:  dir,
		defs: make(map[RollupID]*RollupDef),
	}
	data, err := os.ReadFile(filepath.Join(dir, rollupDefsFile))
	if os.IsNotExist(err) {
		return rr, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ts: rollup defs: read: %w", err)
	}
	var disk rollupDisk
	if err := json.Unmarshal(data, &disk); err != nil {
		return nil, fmt.Errorf("ts: rollup defs: parse: %w", err)
	}
	for i := range disk.Defs {
		d := disk.Defs[i]
		rr.defs[d.ID] = &d
	}
	return rr, nil
}

func (rr *rollupRegistry) save() error {
	disk := rollupDisk{}
	for _, d := range rr.defs {
		disk.Defs = append(disk.Defs, *d)
	}
	data, err := json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return fmt.Errorf("ts: rollup defs: marshal: %w", err)
	}
	tmp := filepath.Join(rr.dir, rollupDefsFile+".tmp")
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("ts: rollup defs: write: %w", err)
	}
	return os.Rename(tmp, filepath.Join(rr.dir, rollupDefsFile))
}

// add validates and stores a new rollup definition. Caller must hold rr.mu
// write lock. Performs single-parent and cycle checks before inserting.
func (rr *rollupRegistry) add(def *RollupDef, maxDepth int) error {
	// Single-parent: dest must not already be the target of another def.
	for _, d := range rr.defs {
		if d.DestTID == def.DestTID {
			return fmt.Errorf("ts: timeline %d is already the destination of rollup %s (%s)",
				def.DestTID, d.ID, xoluerr.ErrTSRollupDestInUse)
		}
	}

	// Cycle check and depth check: walk upward from SourceTID.
	// The parent of a timeline is the def whose DestTID == that timeline.
	// Walking upward means following "who is the source of the def that
	// writes into me" until we hit a timeline with no parent def.
	depth := 0
	cur := def.SourceTID
	for cur != 0 {
		if cur == def.DestTID {
			return fmt.Errorf("ts: rollup from %d to %d would create a cycle (%s)",
				def.SourceTID, def.DestTID, xoluerr.ErrTSRollupCycle)
		}
		parentDef := rr.parentOf(cur)
		if parentDef == nil {
			break
		}
		cur = parentDef.SourceTID
		depth++
		if depth > maxDepth {
			return fmt.Errorf("ts: rollup would exceed maximum depth %d (%s)",
				maxDepth, xoluerr.ErrTSRollupDepth)
		}
	}
	// Also count downward from DestTID to find existing children depth.
	childDepth := rr.maxChildDepth(def.DestTID)
	if depth+1+childDepth > maxDepth {
		return fmt.Errorf("ts: rollup would exceed maximum depth %d (%s)",
			maxDepth, xoluerr.ErrTSRollupDepth)
	}

	rr.defs[def.ID] = def
	return rr.save()
}

// parentOf returns the definition for which tid is the destination, or nil.
// Caller must hold rr.mu (at least read lock).
func (rr *rollupRegistry) parentOf(tid TimelineID) *RollupDef {
	for _, d := range rr.defs {
		if d.DestTID == tid {
			return d
		}
	}
	return nil
}

// maxChildDepth returns the maximum chain length reachable downward from tid.
// Caller must hold rr.mu (at least read lock).
func (rr *rollupRegistry) maxChildDepth(tid TimelineID) int {
	max := 0
	for _, d := range rr.defs {
		if d.SourceTID == tid {
			child := 1 + rr.maxChildDepth(d.DestTID)
			if child > max {
				max = child
			}
		}
	}
	return max
}

// descendants returns all rollup definitions reachable downward from tid,
// in breadth-first order (parent before children). Does not include tid itself.
// Caller must hold rr.mu (at least read lock).
func (rr *rollupRegistry) descendants(tid TimelineID) []*RollupDef {
	var result []*RollupDef
	queue := []TimelineID{tid}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range rr.defs {
			if d.SourceTID == cur {
				result = append(result, d)
				queue = append(queue, d.DestTID)
			}
		}
	}
	return result
}

// forSource returns all definitions where SourceTID == tid.
func (rr *rollupRegistry) forSource(tid TimelineID) []RollupDef {
	rr.mu.RLock()
	defer rr.mu.RUnlock()
	var out []RollupDef
	for _, d := range rr.defs {
		if d.SourceTID == tid {
			out = append(out, *d)
		}
	}
	return out
}

// get returns a definition by ID, or false.
func (rr *rollupRegistry) get(id RollupID) (RollupDef, bool) {
	rr.mu.RLock()
	defer rr.mu.RUnlock()
	d, ok := rr.defs[id]
	if !ok {
		return RollupDef{}, false
	}
	return *d, true
}

// remove deletes a definition by ID. Returns false if not found.
func (rr *rollupRegistry) remove(id RollupID) (bool, error) {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	if _, ok := rr.defs[id]; !ok {
		return false, nil
	}
	delete(rr.defs, id)
	return true, rr.save()
}

// ─── rollup worker ────────────────────────────────────────────────────────────

// rollupWorker runs a background goroutine that fires at each bucket boundary
// for one rollup definition, computing the aggregate and writing it to the
// destination timeline.
type rollupWorker struct {
	def    RollupDef
	store  *PebbleStore
	stopCh chan struct{}
	doneCh chan struct{}

	mu            sync.Mutex
	lastRunAt     time.Time
	lastBucketEnd time.Time
	eventsWritten int64
	lastError     string
}

func newRollupWorker(def RollupDef, store *PebbleStore) *rollupWorker {
	return &rollupWorker{
		def:    def,
		store:  store,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

func (w *rollupWorker) start() {
	go w.loop()
}

func (w *rollupWorker) stop() {
	close(w.stopCh)
	<-w.doneCh
}

func (w *rollupWorker) loop() {
	defer close(w.doneCh)

	// Align the first tick to the next bucket boundary.
	now := time.Now().UTC()
	dur := w.def.BucketDuration
	next := now.Truncate(dur).Add(dur).Add(w.def.LateWindow)
	if next.Before(now) {
		next = next.Add(dur)
	}

	timer := time.NewTimer(next.Sub(now))
	defer timer.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case t := <-timer.C:
			bucketEnd := t.UTC().Truncate(dur)
			bucketStart := bucketEnd.Add(-dur)
			ctx := context.Background()
			if err := w.runBucket(ctx, bucketStart, bucketEnd); err != nil {
				w.mu.Lock()
				w.lastError = err.Error()
				w.mu.Unlock()
			}
			// Schedule next tick.
			next = bucketEnd.Add(dur).Add(w.def.LateWindow)
			timer.Reset(next.Sub(time.Now().UTC()))
		}
	}
}

// runBucket aggregates [from, to) from the source timeline and writes one
// rollup event into the destination timeline.
func (w *rollupWorker) runBucket(ctx context.Context, from, to time.Time) error {
	srcCfg, ok := w.store.reg.get(w.def.SourceTID)
	if !ok {
		return fmt.Errorf("ts rollup: source timeline %d not defined", w.def.SourceTID)
	}
	_, ok = w.store.reg.get(w.def.DestTID)
	if !ok {
		return fmt.Errorf("ts rollup: dest timeline %d not defined", w.def.DestTID)
	}

	// Query all events in [from, to) across all dim combinations.
	// NOTE: current implementation collapses all series into a single aggregate
	// event regardless of dim values. Per-series bucketing (one event per unique
	// dim combination) requires DistinctDims support and is not yet implemented.
	res, err := w.store.RangeAggregate(ctx, RangeAllQuery{
		Timeline: w.def.SourceTID,
		Dims:     make([]uint64, srcCfg.Dims),
		From:     from,
		To:       to,
	})
	if err != nil {
		return fmt.Errorf("ts rollup: aggregate: %w", err)
	}
	if res.Count == 0 {
		return nil
	}

	// Pack aggregate stats into the seven float fields:
	// val0=mean[0], val1=min[0], val2=max[0], val3=sum[0],
	// val4=count (as float64), val5=mean[1] (if present), val6=mean[2]
	nums := make([]float64, 7)
	nums[0] = res.Avgs[0]
	nums[1] = res.Mins[0]
	nums[2] = res.Maxs[0]
	nums[3] = res.Sums[0]
	nums[4] = float64(res.Count)
	nums[5] = res.Avgs[1]
	nums[6] = res.Avgs[2]

	event := Event{
		Timeline: w.def.DestTID,
		Dims:     make([]uint64, srcCfg.Dims),
		Time:     to, // rollup event timestamp = bucket close
		Nums:     nums,
	}
	if err := w.store.Append(ctx, event); err != nil {
		return fmt.Errorf("ts rollup: append: %w", err)
	}

	w.mu.Lock()
	w.lastRunAt = time.Now().UTC()
	w.lastBucketEnd = to
	w.eventsWritten++
	w.lastError = ""
	w.mu.Unlock()
	return nil
}

func (w *rollupWorker) status() RollupStatusReport {
	w.mu.Lock()
	defer w.mu.Unlock()
	return RollupStatusReport{
		ID:            w.def.ID,
		SourceTID:     w.def.SourceTID,
		DestTID:       w.def.DestTID,
		LastRunAt:     w.lastRunAt,
		LastBucketEnd: w.lastBucketEnd,
		EventsWritten: w.eventsWritten,
		LastError:     w.lastError,
		Running:       true,
	}
}

// ─── PebbleStore rollup methods ───────────────────────────────────────────────

func (s *PebbleStore) rollupMaxDepth() int {
	if s.dc != nil {
		ns := "tenant." + s.tenantName
		if n, ok := s.dc.GetInt64(ns, "ts.rollup_max_depth"); ok && n > 0 {
			return int(n)
		}
		if n, ok := s.dc.GetInt64("global", "ts.rollup_max_depth"); ok && n > 0 {
			return int(n)
		}
	}
	return MaxRollupDepth
}

func (s *PebbleStore) guardRootTimeline(id TimelineID) error {
	if id == 0 {
		return fmt.Errorf("ts: timeline 0 is the structural root and cannot be used with rollup operations (%s)",
			xoluerr.ErrTSRootTimeline)
	}
	return nil
}

func (s *PebbleStore) DefineRollup(sourceTID TimelineID, def RollupDef) (RollupID, error) {
	if err := s.guardRootTimeline(sourceTID); err != nil {
		return "", err
	}
	if _, ok := s.reg.get(sourceTID); !ok {
		return "", fmt.Errorf("ts: timeline %d not defined (XOLU-TS004)", sourceTID)
	}
	if _, ok := s.reg.get(def.DestTID); !ok {
		return "", fmt.Errorf("ts: dest timeline %d not defined (XOLU-TS004)", def.DestTID)
	}
	if def.DestTID == 0 {
		return "", fmt.Errorf("ts: timeline 0 cannot be a rollup destination (%s)", xoluerr.ErrTSRootTimeline)
	}
	if def.BucketDuration <= 0 {
		return "", fmt.Errorf("ts: rollup bucket_duration must be positive")
	}
	if def.SourceTID == def.DestTID {
		return "", fmt.Errorf("ts: rollup source and destination must differ (%s)", xoluerr.ErrTSRollupCycle)
	}

	def.SourceTID = sourceTID
	def.CreatedAt = time.Now().UTC()
	if def.ID == "" {
		def.ID = RollupID(fmt.Sprintf("%d-%d-%d", sourceTID, def.DestTID, def.CreatedAt.UnixNano()))
	}

	s.rollupReg.mu.Lock()
	err := s.rollupReg.add(&def, s.rollupMaxDepth())
	s.rollupReg.mu.Unlock()
	if err != nil {
		return "", err
	}

	// Workers are NOT started at definition time. The caller must explicitly
	// trigger via RunRollup (with cascade=true to start descendants too).
	// This avoids workers firing on independent timers before data exists.

	return def.ID, nil
}

func (s *PebbleStore) GetRollup(sourceTID TimelineID, id RollupID) (RollupDef, error) {
	if err := s.guardRootTimeline(sourceTID); err != nil {
		return RollupDef{}, err
	}
	def, ok := s.rollupReg.get(id)
	if !ok || def.SourceTID != sourceTID {
		return RollupDef{}, fmt.Errorf("ts: rollup %s not found on timeline %d (%s)",
			id, sourceTID, xoluerr.ErrTSRollupNotFound)
	}
	return def, nil
}

func (s *PebbleStore) ListRollups(sourceTID TimelineID) ([]RollupDef, error) {
	if err := s.guardRootTimeline(sourceTID); err != nil {
		return nil, err
	}
	return s.rollupReg.forSource(sourceTID), nil
}

// setRunning updates the Running flag on a definition and persists it.
func (rr *rollupRegistry) setRunning(id RollupID, running bool) error {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	d, ok := rr.defs[id]
	if !ok {
		return nil // already gone, not an error
	}
	d.Running = running
	return rr.save()
}

func (s *PebbleStore) DeleteRollup(sourceTID TimelineID, id RollupID) error {
	if err := s.guardRootTimeline(sourceTID); err != nil {
		return err
	}
	def, ok := s.rollupReg.get(id)
	if !ok || def.SourceTID != sourceTID {
		return fmt.Errorf("ts: rollup %s not found on timeline %d (%s)",
			id, sourceTID, xoluerr.ErrTSRollupNotFound)
	}

	// Collect descendants before any deletions so the tree is intact.
	s.rollupReg.mu.RLock()
	descendants := s.rollupReg.descendants(def.DestTID)
	s.rollupReg.mu.RUnlock()

	if !s.cfg.RollupCascadeDelete && len(descendants) > 0 {
		return fmt.Errorf("ts: rollup %s has %d descendant(s); set XOLU_TS_ROLLUP_CASCADE_DELETE=true to delete recursively, or delete bottom-up manually (%s)",
			id, len(descendants), xoluerr.ErrTSRollupDestInUse)
	}

	// Stop and delete descendants first (leaves before parents — reverse order).
	for i := len(descendants) - 1; i >= 0; i-- {
		d := descendants[i]
		s.rollupWorkersMu.Lock()
		if w, ok := s.rollupWorkers[d.ID]; ok {
			w.stop()
			delete(s.rollupWorkers, d.ID)
		}
		s.rollupWorkersMu.Unlock()
		if _, err := s.rollupReg.remove(d.ID); err != nil {
			return fmt.Errorf("ts: delete descendant rollup %s: %w", d.ID, err)
		}
	}

	// Stop and delete the requested definition itself.
	s.rollupWorkersMu.Lock()
	if w, ok := s.rollupWorkers[id]; ok {
		w.stop()
		delete(s.rollupWorkers, id)
	}
	s.rollupWorkersMu.Unlock()

	removed, err := s.rollupReg.remove(id)
	if err != nil {
		return err
	}
	if !removed {
		return fmt.Errorf("ts: rollup %s not found (%s)", id, xoluerr.ErrTSRollupNotFound)
	}
	return nil
}

// ensureWorkerRunning starts the worker for id if not already running,
// persists Running=true, and returns the worker.
func (s *PebbleStore) ensureWorkerRunning(def RollupDef) *rollupWorker {
	s.rollupWorkersMu.Lock()
	defer s.rollupWorkersMu.Unlock()
	if w, ok := s.rollupWorkers[def.ID]; ok {
		return w
	}
	w := newRollupWorker(def, s)
	s.rollupWorkers[def.ID] = w
	w.start()
	_ = s.rollupReg.setRunning(def.ID, true)
	return w
}

func (s *PebbleStore) RunRollup(ctx context.Context, sourceTID TimelineID, id RollupID, from, to time.Time, cascade bool) error {
	if err := s.guardRootTimeline(sourceTID); err != nil {
		return err
	}
	def, ok := s.rollupReg.get(id)
	if !ok || def.SourceTID != sourceTID {
		return fmt.Errorf("ts: rollup %s not found on timeline %d (%s)",
			id, sourceTID, xoluerr.ErrTSRollupNotFound)
	}

	// If no range given, compute the most recently closed bucket.
	if from.IsZero() && to.IsZero() {
		now := time.Now().UTC()
		to = now.Truncate(def.BucketDuration)
		from = to.Add(-def.BucketDuration)
	}

	// Ensure this definition's worker is running and use it for status tracking.
	w := s.ensureWorkerRunning(def)

	// Run all buckets in [from, to). If from→to spans multiple bucket
	// durations, iterate them in order.
	bucketFrom := from.Truncate(def.BucketDuration)
	bucketTo := to.Truncate(def.BucketDuration)
	if bucketTo.Before(to) {
		bucketTo = bucketTo.Add(def.BucketDuration)
	}
	for t := bucketFrom; t.Before(bucketTo); t = t.Add(def.BucketDuration) {
		if err := w.runBucket(ctx, t, t.Add(def.BucketDuration)); err != nil {
			return err
		}
	}

	if !cascade {
		return nil
	}

	// Cascade: run all descendants in breadth-first order so each level has
	// data from its source before it runs. descendants() already returns nodes
	// in breadth-first order starting from the immediate children of def.DestTID
	// — that is the correct execution order. Do not append immediateChildren
	// separately; they are already the first entries in descendants.
	s.rollupReg.mu.RLock()
	allToRun := s.rollupReg.descendants(def.DestTID)
	s.rollupReg.mu.RUnlock()

	for _, d := range allToRun {
		childDef := *d
		cw := s.ensureWorkerRunning(childDef)

		// Align the child's window to cover the same calendar span as the
		// parent's window, expressed in the child's own bucket duration.
		childFrom := from.Truncate(childDef.BucketDuration)
		childTo := to.Truncate(childDef.BucketDuration)
		if childTo.Before(to) {
			childTo = childTo.Add(childDef.BucketDuration)
		}

		for t := childFrom; t.Before(childTo); t = t.Add(childDef.BucketDuration) {
			if err := cw.runBucket(ctx, t, t.Add(childDef.BucketDuration)); err != nil {
				return fmt.Errorf("ts: cascade rollup %s bucket %v: %w", childDef.ID, t, err)
			}
		}
	}

	return nil
}

func (s *PebbleStore) RollupParent(tid TimelineID) (RollupDef, bool) {
	s.rollupReg.mu.RLock()
	defer s.rollupReg.mu.RUnlock()
	d := s.rollupReg.parentOf(tid)
	if d == nil {
		return RollupDef{}, false
	}
	return *d, true
}

func (s *PebbleStore) RollupTree() *RollupTreeNode {
	s.rollupReg.mu.RLock()
	defer s.rollupReg.mu.RUnlock()

	root := &RollupTreeNode{TID: 0}

	// Raw timelines are those that appear as SourceTID in at least one
	// definition but never as DestTID — they are the implicit children of
	// the structural root (timeline 0).
	sources := map[TimelineID]bool{}
	dests := map[TimelineID]bool{}
	for _, d := range s.rollupReg.defs {
		sources[d.SourceTID] = true
		dests[d.DestTID] = true
	}
	for tid := range sources {
		if !dests[tid] {
			// tid is a raw (root-level) timeline.
			node := &RollupTreeNode{TID: tid}
			node.Children = s.rollupChildren(tid)
			root.Children = append(root.Children, node)
		}
	}
	return root
}

func (s *PebbleStore) rollupChildren(tid TimelineID) []*RollupTreeNode {
	var children []*RollupTreeNode
	for _, d := range s.rollupReg.defs {
		if d.SourceTID == tid {
			def := *d
			node := &RollupTreeNode{TID: d.DestTID, Def: &def}
			node.Children = s.rollupChildren(d.DestTID)
			children = append(children, node)
		}
	}
	return children
}

func (s *PebbleStore) RollupStatus(sourceTID TimelineID, id RollupID) (RollupStatusReport, error) {
	if err := s.guardRootTimeline(sourceTID); err != nil {
		return RollupStatusReport{}, err
	}
	def, ok := s.rollupReg.get(id)
	if !ok || def.SourceTID != sourceTID {
		return RollupStatusReport{}, fmt.Errorf("ts: rollup %s not found on timeline %d (%s)",
			id, sourceTID, xoluerr.ErrTSRollupNotFound)
	}

	s.rollupWorkersMu.RLock()
	w, running := s.rollupWorkers[id]
	s.rollupWorkersMu.RUnlock()

	if running {
		return w.status(), nil
	}
	return RollupStatusReport{
		ID:        id,
		SourceTID: sourceTID,
		DestTID:   def.DestTID,
		Running:   false,
	}, nil
}

// ─── Data deletion ────────────────────────────────────────────────────────────

func (s *PebbleStore) DeleteTimelineData(ctx context.Context, id TimelineID) error {
	if id == 0 {
		return fmt.Errorf("ts: timeline 0 is the structural root and cannot be cleared (%s)",
			xoluerr.ErrTSRootTimeline)
	}
	cfg, ok := s.reg.get(id)
	if !ok {
		return fmt.Errorf("ts: timeline %d not defined (XOLU-TS004)", id)
	}
	return s.deleteDataRange(id, cfg)
}

// deleteDataRange removes all event data for a timeline given its config. It is
// the shared core of DeleteTimelineData (which looks cfg up via the public,
// marker-aware get) and DeleteTimeline (which holds a cfg read past the
// deleting marker). It does no existence check — the caller has already
// resolved cfg.
func (s *PebbleStore) deleteDataRange(id TimelineID, cfg TimelineConfig) error {
	startKey := EncodePrefixKey(id, make([]uint64, cfg.Dims))
	endKey := incrementKey(EncodePrefixKey(id, fillDims(cfg.Dims, math.MaxUint64)))

	if err := s.db.DeleteRange(startKey, endKey, pebble.Sync); err != nil {
		return fmt.Errorf("ts: delete timeline data: %w", err)
	}
	// Reset counter to zero — the data is gone.
	s.counter(id).Store(0)
	return nil
}

// DeleteTimeline removes a timeline's definition, its event data, and its
// rollups. It is the inverse of DefineTimeline and is distinct from
// DeleteTimelineData (which clears events but keeps the definition).
//
// Cascade follows the same RollupCascadeDelete policy that DeleteRollup uses:
//   - cascade on (default): the timeline's rollups are removed first, then its
//     data, then its definition.
//   - cascade off: if the timeline still has rollups, the call returns an error
//     and changes nothing; the caller must delete the rollups first.
//
// Concurrency: before tearing anything down, the timeline is marked deleting in
// the registry, after which get reports it as not-found. Concurrent readers and
// writers therefore fail fast with a clean not-found instead of racing the data
// teardown and observing a defined-but-empty timeline. If the cascade-off check
// rejects the delete — or any teardown step fails — the marker is cleared and
// the timeline remains fully usable (the operation is all-or-nothing as seen by
// callers). The internal sequence still runs rollups → data → definition so no
// step leaves a rollup pointing at a source whose data or definition is gone.
// Timeline 0 is the structural root and cannot be deleted.
func (s *PebbleStore) DeleteTimeline(ctx context.Context, id TimelineID) error {
	if id == 0 {
		return fmt.Errorf("ts: timeline 0 is the structural root and cannot be deleted (%s)",
			xoluerr.ErrTSRootTimeline)
	}

	// Mark deleting first: from here on get() hides the timeline, so concurrent
	// reads/writes get a clean not-found rather than racing the teardown.
	if err := s.reg.markDeleting(id); err != nil {
		return err // undefined, already-deleting, or reserved — nothing changed
	}

	// cfg must be read past the marker we just set.
	cfg, ok := s.reg.getForDelete(id)
	if !ok {
		// Should not happen (markDeleting verified existence under the lock),
		// but stay safe and roll the marker back.
		s.reg.unmarkDeleting(id)
		return fmt.Errorf("ts: timeline %d not defined (XOLU-TS004)", id)
	}

	rollups, err := s.ListRollups(id)
	if err != nil {
		s.reg.unmarkDeleting(id)
		return fmt.Errorf("ts: delete timeline: list rollups: %w", err)
	}
	if len(rollups) > 0 && !s.cfg.RollupCascadeDelete {
		s.reg.unmarkDeleting(id)
		return fmt.Errorf("ts: timeline %d has %d rollup(s); set XOLU_TS_ROLLUP_CASCADE_DELETE=true to delete recursively, or delete them first (%s)",
			id, len(rollups), xoluerr.ErrTSRollupDestInUse)
	}
	for _, rd := range rollups {
		if err := s.DeleteRollup(id, rd.ID); err != nil {
			s.reg.unmarkDeleting(id)
			return fmt.Errorf("ts: delete timeline: delete rollup %s: %w", rd.ID, err)
		}
	}

	if err := s.deleteDataRange(id, cfg); err != nil {
		s.reg.unmarkDeleting(id)
		return fmt.Errorf("ts: delete timeline: clear data: %w", err)
	}

	if err := s.reg.delete(id); err != nil {
		s.reg.unmarkDeleting(id)
		return fmt.Errorf("ts: delete timeline: remove definition: %w", err)
	}
	return nil
}

func (s *PebbleStore) PurgeTimelineRange(ctx context.Context, id TimelineID, from, to time.Time) error {
	if id == 0 {
		return fmt.Errorf("ts: timeline 0 is the structural root and cannot be purged (%s)",
			xoluerr.ErrTSRootTimeline)
	}
	cfg, ok := s.reg.get(id)
	if !ok {
		return fmt.Errorf("ts: timeline %d not defined (XOLU-TS004)", id)
	}
	if !to.After(from) {
		return fmt.Errorf("ts: purge range: to must be after from")
	}

	// Scan and batch-delete events in the time range.
	startKey := EncodePrefixKey(id, make([]uint64, cfg.Dims))
	endKey := incrementKey(EncodePrefixKey(id, fillDims(cfg.Dims, math.MaxUint64)))

	iter, err := s.db.NewIter(&pebble.IterOptions{
		LowerBound: startKey,
		UpperBound: endKey,
	})
	if err != nil {
		return fmt.Errorf("ts: purge range iter: %w", err)
	}
	defer func() { _ = iter.Close() }()

	batch := s.db.NewBatch()
	defer func() { _ = batch.Close() }()
	var deleted int64

	for iter.First(); iter.Valid(); iter.Next() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		ts, err := DecodeTimestamp(iter.Key(), cfg.Dims)
		if err != nil {
			continue
		}
		if ts.Before(from) {
			continue
		}
		if !ts.Before(to) {
			break
		}
		key := make([]byte, len(iter.Key()))
		copy(key, iter.Key())
		if err := batch.Delete(key, nil); err != nil {
			return fmt.Errorf("ts: purge range delete: %w", err)
		}
		deleted++
		if deleted%int64(purgeBatchSize) == 0 {
			if err := batch.Commit(pebble.Sync); err != nil {
				return fmt.Errorf("ts: purge range commit: %w", err)
			}
			_ = batch.Close()
			batch = s.db.NewBatch()
		}
	}
	if err := iter.Error(); err != nil {
		return fmt.Errorf("ts: purge range scan: %w", err)
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return fmt.Errorf("ts: purge range final commit: %w", err)
	}
	s.counter(id).Add(-deleted)
	return nil
}
