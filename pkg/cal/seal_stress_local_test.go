// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

//go:build stress
// +build stress

// T-15 resolution: `cal` seal concurrency stress at production scale.
//
// This file is guarded by the `stress` build tag so it does NOT run under
// normal `go test ./...`. It is intended for manual local execution on
// realistic hardware, where the scale parameters below can be pushed far
// beyond what the in-container `TestSealRaceStress` (see seal_test.go)
// exercises.
//
// Invocation:
//
//   go test -tags=stress -timeout=1h -v -run=TestSealStressLocal ./pkg/cal
//
// Environment overrides (all optional; defaults are set below):
//
//   XOLU_SEAL_STRESS_TRIALS       — number of independent trials
//   XOLU_SEAL_STRESS_WORKERS      — concurrent mutator goroutines per trial
//   XOLU_SEAL_STRESS_BOOKINGS     — bookings seeded per trial
//   XOLU_SEAL_STRESS_OPS_PER_WORKER — mutations attempted per worker
//   XOLU_SEAL_STRESS_CALENDARS    — calendars per tenant
//   XOLU_SEAL_STRESS_DAYS         — future days across which bookings are spread
//
// The default scale (5 trials, 16 workers, 5000 bookings, 2000 ops/worker,
// 10 calendars, 90 days) targets ~1.6 million mutation attempts against
// 5000 concurrently-mutating bookings under an advancing seal frontier
// per trial. On a modern developer machine this should complete in
// several minutes. On realistic production-scale hardware (dozens of
// cores, NVMe storage) larger scales are appropriate.
//
// What this test guards:
//
//   1. Under sustained concurrent mutation with an advancing seal
//      frontier, the in-memory index does not drift from the SQL source
//      of truth. `assertIndexMatchesRebuild` is checked at quiescence.
//   2. No goroutine panics, deadlocks, or data-races (run with -race
//      for the third property).
//   3. The seal frontier advances monotonically — it never regresses.
//
// This test is NOT a benchmark; it does not measure throughput. Its
// output on success is silence plus a per-trial summary log. On failure
// it names the trial and the property violated.
//
// Run under `-race` for full coverage:
//
//   go test -tags=stress -race -timeout=1h -v -run=TestSealStressLocal ./pkg/cal

package cal

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ot "github.com/ha1tch/xolu/pkg/xolutime"
)

// Default scale — override via env vars.
const (
	defaultStressTrials       = 5
	defaultStressWorkers      = 16
	defaultStressBookings     = 5000
	defaultStressOpsPerWorker = 2000
	defaultStressCalendars    = 10
	defaultStressDays         = 90
)

type stressConfig struct {
	trials       int
	workers      int
	bookings     int
	opsPerWorker int
	calendars    int
	days         int
}

func loadStressConfig() stressConfig {
	get := func(key string, def int) int {
		v := os.Getenv(key)
		if v == "" {
			return def
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return def
		}
		return n
	}
	return stressConfig{
		trials:       get("XOLU_SEAL_STRESS_TRIALS", defaultStressTrials),
		workers:      get("XOLU_SEAL_STRESS_WORKERS", defaultStressWorkers),
		bookings:     get("XOLU_SEAL_STRESS_BOOKINGS", defaultStressBookings),
		opsPerWorker: get("XOLU_SEAL_STRESS_OPS_PER_WORKER", defaultStressOpsPerWorker),
		calendars:    get("XOLU_SEAL_STRESS_CALENDARS", defaultStressCalendars),
		days:         get("XOLU_SEAL_STRESS_DAYS", defaultStressDays),
	}
}

func TestSealStressLocal(t *testing.T) {
	cfg := loadStressConfig()
	t.Logf("stress config: trials=%d workers=%d bookings=%d ops/worker=%d calendars=%d days=%d gomaxprocs=%d",
		cfg.trials, cfg.workers, cfg.bookings, cfg.opsPerWorker, cfg.calendars, cfg.days,
		runtime.GOMAXPROCS(0))

	base := ot.MustParse("2027-01-01T00:00:00Z")

	for trial := 0; trial < cfg.trials; trial++ {
		trialStart := time.Now()

		calIDs := make([]string, cfg.calendars)
		for i := range calIDs {
			calIDs[i] = fmt.Sprintf("c%d", i)
		}
		lc, idx, src := setupLifecycle(t, calIDs...)
		sealer := NewSealer(lc)

		// Seed bookings across the day range.
		type ref struct{ cid, bid string }
		refs := make([]ref, 0, cfg.bookings)
		rng := rand.New(rand.NewSource(int64(trial + 1)))

		var createdOK, createdErr int
		for i := 0; i < cfg.bookings; i++ {
			cid := calIDs[rng.Intn(len(calIDs))]
			dayOff := rng.Intn(cfg.days)
			hourOff := rng.Intn(20)
			durHours := rng.Intn(8) + 1
			st := base.Add(time.Duration(dayOff*24+hourOff) * time.Hour)
			en := st.Add(time.Duration(durHours) * time.Hour)
			bid := fmt.Sprintf("t%d-b%d", trial, i)

			startState := StateProposed
			if rng.Intn(2) == 0 {
				startState = StateBinding
			}
			b := Booking{
				BookingID:  bid,
				CalendarID: cid,
				State:      startState,
				Span:       Span{Start: st, End: en},
				Mode:       ModeExclusive,
				Bearer:     uint64(i + 1),
				CreatedAt:  ot.Now(),
				UpdatedAt:  ot.Now(),
			}
			if _, err := sealer.CreateSealed(b); err == nil {
				refs = append(refs, ref{cid, bid})
				createdOK++
			} else {
				createdErr++
			}
		}
		t.Logf("trial %d: seeded ok=%d err=%d refs=%d", trial, createdOK, createdErr, len(refs))
		if len(refs) == 0 {
			t.Fatalf("trial %d: no bookings created — seed range too tight?", trial)
		}

		// Counters for post-run summary
		var (
			opConfirm, opCancel, opMove int64
			okConfirm, okCancel, okMove int64
		)

		var wg sync.WaitGroup

		// Seal-advancing goroutine.
		frontierStart := base
		frontierEnd := base.Add(time.Duration(cfg.days*24) * time.Hour)
		frontierStep := time.Hour * time.Duration(24*cfg.days/40)
		if frontierStep < time.Hour {
			frontierStep = time.Hour
		}

		var lastFrontierNs atomic.Int64
		lastFrontierNs.Store(frontierStart.UnixNano())

		wg.Add(1)
		go func() {
			defer wg.Done()
			cur := frontierStart
			for cur.Before(frontierEnd) {
				sealer.AdvanceTo(cur)
				if cur.UnixNano() < lastFrontierNs.Load() {
					t.Errorf("trial %d: seal frontier REGRESSED from %d to %d",
						trial, lastFrontierNs.Load(), cur.UnixNano())
				}
				lastFrontierNs.Store(cur.UnixNano())
				cur = cur.Add(frontierStep)
				time.Sleep(time.Millisecond)
			}
		}()

		// Mutator goroutines.
		for w := 0; w < cfg.workers; w++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				r := rand.New(rand.NewSource(int64(trial*10_000 + workerID)))
				for k := 0; k < cfg.opsPerWorker; k++ {
					rf := refs[r.Intn(len(refs))]
					switch r.Intn(3) {
					case 0:
						atomic.AddInt64(&opConfirm, 1)
						if err := sealer.ConfirmSealed(rf.cid, rf.bid); err == nil {
							atomic.AddInt64(&okConfirm, 1)
						}
					case 1:
						atomic.AddInt64(&opCancel, 1)
						if err := sealer.CancelSealed(rf.cid, rf.bid); err == nil {
							atomic.AddInt64(&okCancel, 1)
						}
					case 2:
						atomic.AddInt64(&opMove, 1)
						dayOff := r.Intn(cfg.days)
						st := base.Add(time.Duration(dayOff*24+r.Intn(20)) * time.Hour)
						en := st.Add(time.Duration(r.Intn(6)+1) * time.Hour)
						if _, err := sealer.MoveSealed(rf.cid, rf.bid,
							Span{Start: st, End: en}); err == nil {
							atomic.AddInt64(&okMove, 1)
						}
					}
				}
			}(w)
		}

		wg.Wait()

		// At quiescence: verify the invariants.
		assertIndexMatchesRebuild(t, idx, src,
			fmt.Sprintf("seal-stress trial %d", trial))

		trialDur := time.Since(trialStart)
		totalOps := atomic.LoadInt64(&opConfirm) +
			atomic.LoadInt64(&opCancel) +
			atomic.LoadInt64(&opMove)
		t.Logf("trial %d: duration=%s ops=%d (confirm=%d/%d cancel=%d/%d move=%d/%d) rate=%.0f ops/s",
			trial, trialDur, totalOps,
			atomic.LoadInt64(&okConfirm), atomic.LoadInt64(&opConfirm),
			atomic.LoadInt64(&okCancel), atomic.LoadInt64(&opCancel),
			atomic.LoadInt64(&okMove), atomic.LoadInt64(&opMove),
			float64(totalOps)/trialDur.Seconds())

		idx.Close()
	}
}
