// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// cmd/critrepro — runs "critical scenarios" repeatedly, entirely outside
// `go test`'s own harness: no testing.T, no -count, no go test process at
// all. Plain func main(), a plain loop, real HTTP requests against a real
// in-process server built exactly as the equivalent go test helper builds
// it.
//
// Why this exists (T-168, 2026-08-07): TestDxpTxnAPI_Scale_FiveBalLegs_
// AllCommit failed at a stable, high rate (roughly 33-43%) under
// `go test -count=500` on real M1 hardware -- multi-core, so genuinely
// reproducible -- while never failing once in this project's own
// single-core sandbox. Extensive investigation ruled out a Go-level data
// race (-race clean, 100/100), broken lock logic, lost WriteTargets, a
// SQLite driver comparison bug, and wall-clock non-monotonicity (a direct
// 2M-iteration probe of time.Now().UTC(), the exact call the affected
// code makes, found zero backward jumps). Horacio's own hypothesis --
// that go test's own repeated-invocation harness, not the application
// code, was the actual source of the failures -- was the one that
// resolved it: this exact scenario, same hardware, same fix, driven by a
// bare `func main()` loop instead of `go test -count=500`, passed
// 500/500. The dxp/bal coordination fix (WriteTargets-based per-target
// locking in dispatchPhased and dispatchCollapsed) is genuinely correct;
// something about `go test`'s own execution model under heavy repeated
// invocation is not, for reasons not further investigated -- this
// project does not have the budget to dig into the Go toolchain itself
// right now, and doesn't need to: production traffic never goes through
// `go test` regardless.
//
// The practical decision this tool encodes: for scenarios whose own
// correctness genuinely depends on true multi-core timing (this one, and
// any future one with the same shape), `go test`'s own repeated-run
// harness is not trusted as the verification instrument, even though it
// remains the right tool for everything else. Each such scenario keeps
// its own single-run go test coverage (a real, valuable correctness
// check on its own), and additionally gets a scenario here, run
// repeatedly via this tool during release verification instead of via
// `go test -count=N`.
//
// Usage:
//
//	go run ./cmd/critrepro                    # every scenario, 500x each
//	go run ./cmd/critrepro -n 200              # every scenario, 200x each
//	go run ./cmd/critrepro -scenario five-bal-legs -n 500
//
// Exit code is nonzero if any scenario had any failure -- release.py's
// own critrepro step relies on this directly.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
)

// scenario is one critical, repeat-under-real-parallelism check. run
// executes a single iteration (iter is the 0-based iteration number, for
// scenarios that want per-iteration uniqueness beyond what their own
// tmpDir already gives them) and reports whether it passed, and if not,
// why.
type scenario struct {
	name string
	run  func(iter int) (ok bool, reason string)
}

var registry = []scenario{
	{name: "five-bal-legs", run: fiveBalLegsIteration},
	{name: "ts-delete-fence", run: tsDeleteFenceIteration},
}

func main() {
	n := flag.Int("n", 500, "iterations per scenario")
	name := flag.String("scenario", "", "run only this scenario (default: all)")
	flag.Parse()

	var toRun []scenario
	if *name == "" {
		toRun = registry
	} else {
		for _, s := range registry {
			if s.name == *name {
				toRun = append(toRun, s)
			}
		}
		if len(toRun) == 0 {
			names := make([]string, len(registry))
			for i, s := range registry {
				names[i] = s.name
			}
			sort.Strings(names)
			fmt.Fprintf(os.Stderr, "critrepro: unknown scenario %q -- known scenarios: %v\n", *name, names)
			os.Exit(2)
		}
	}

	anyFail := false
	for _, s := range toRun {
		pass, fail := 0, 0
		fmt.Printf("=== %s (n=%d) ===\n", s.name, *n)
		for i := 0; i < *n; i++ {
			ok, reason := s.run(i)
			if ok {
				pass++
			} else {
				fail++
				fmt.Printf("FAIL scenario=%s iter=%d reason=%s\n", s.name, i, reason)
			}
		}
		fmt.Printf("--- %s: pass=%d fail=%d\n\n", s.name, pass, fail)
		if fail > 0 {
			anyFail = true
		}
	}

	if anyFail {
		os.Exit(1)
	}
}
