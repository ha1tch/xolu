# `tests/` — testing control plane

This directory is the single discoverable home for xolu's test orchestration,
seed corpora, and durable test artifacts. It is **not** where the test functions
live: Go requires `func Test*`, `func Benchmark*`, and `func Fuzz*` to reside in
the package they exercise, so those stay under `pkg/…`. What lives here is
everything *around* them — launchers, corpora, crashers, baselines — so there is
one place to look.

## Layout

```
tests/
  README.md            this file
  fuzz/
    run.sh             launcher: replay seeds (default) or actively fuzz
    targets.txt        registry: fuzz target name -> package it lives in
    corpus/            curated seed corpora (human-authored, version-controlled)
    crashers/          triaged reproducers promoted from fuzz findings
  adversarial/
    corpus/            notes on the shared hostile-input corpus
  interop/
    run.sh             launcher: boots a scoped S3 harness, runs real clients
    boto_check.py      boto3 round-trip + negative checks
    server/            the harness: a scoped xolu S3 listener with a known grant
```

The shared adversarial corpus that the property tests and fuzz seeds draw from
is a Go package, `pkg/internal/advcorpus` (it has to be importable by tests
across packages). `tests/adversarial/corpus/` documents it and is the place for
any non-Go corpus data.

## Fuzzing

Six native Go fuzz targets, one per security-relevant boundary:

| Target | Package | Guards |
|--------|---------|--------|
| `FuzzEvalGuard` | `pkg/fsm/eval` | FSM function registry panics / allocation (D-008) |
| `FuzzTokenise` | `pkg/jsonic` | tokeniser depth / stack overflow (D-003) |
| `FuzzBlobSHA` | `pkg/blob` | SHA digest validation panic (D-004) |
| `FuzzScalarFunctions` | `pkg/qs` | scalar panics / non-serialisable output (D-007) |
| `FuzzValidateFieldName` | `pkg/oql` | identifier allowlist property (D-005) |
| `FuzzParseAndValidateJWT` | `pkg/middleware` | JWT parse panics / signature bypass (D-002) |

**Replay seeds (fast, deterministic — safe in CI):**

```bash
./tests/fuzz/run.sh
```

This runs the targets as ordinary tests with no `-fuzz` flag; it only replays
the checked-in seed corpus. The release pipeline and `run_tests.sh` use this
mode, so they stay deterministic and never write files.

**Active fuzzing (unbounded — run locally or in a nightly job):**

```bash
./tests/fuzz/run.sh --active 60            # 60s per target
./tests/fuzz/run.sh --active 60 FuzzEvalGuard
```

Active mode writes new interesting inputs and any crasher into the package's
`testdata/fuzz/<Target>/` tree (Go's convention). A crasher there becomes a
permanent regression seed. When triaging a finding, copy the minimised
reproducer into `tests/fuzz/crashers/` with a note on which defect it maps to.

## Property tests

The injection-class guarantees live next to the code they test, but are
conceptually part of this suite:

- `pkg/storage/adapted_ddl_property_test.go` — no metacharacter from any schema
  field name reaches derived DDL (D-009 class).
- `pkg/oql/sqlgen_join_property_test.go` — no metacharacter reaches JOIN SQL in
  the SELECT, WHERE, or ON position (D-005 class).
- `pkg/oql/sqlgen_parity_property_test.go` — the JOIN field path is never weaker
  than the single-table path (D-005 root cause).

All three draw from `pkg/internal/advcorpus` and run under a normal `go test`.

## Not yet here (planned consolidation pass)

The following are intentionally **not** migrated yet, to keep the test-infra
reorganisation separate from the security work that created this directory:

- `bench/` — a launcher wrapping both `go test -bench ./pkg/...` and the
  standalone `go run ./cmd/tsbench` benchmark tool, plus committed baselines.
  The benchmark classification logic currently lives inline in `run_tests.sh`.
- `coverage/` — the coverage + threshold-gate logic currently inline in
  `run_tests.sh`, lifted into a launcher here.

Until that pass, `run_tests.sh` and `release.sh` at the repo root remain the
entry points for unit/coverage runs, and `cmd/tsbench` remains the benchmark
tool. The root scripts are unchanged by the introduction of this directory.
