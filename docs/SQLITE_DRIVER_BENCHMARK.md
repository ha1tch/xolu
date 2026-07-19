# SQLite Driver Benchmark — `modernc.org/sqlite` vs `mattn/go-sqlite3`

**Author:** Nadine Ostrovski  
**Date:** 2026-06-15  
**Environment:** Linux/amd64, Intel Xeon @ 2.10 GHz, Go 1.26.4  
**Drivers:** `modernc.org/sqlite v1.52.0` (pure Go), `mattn/go-sqlite3 v1.14.45` (CGo)

---

## Background

Xolu currently uses `modernc.org/sqlite`, a pure-Go SQLite driver that
compiles SQLite's C source to Go via a transpiler. It requires no C
compiler, produces a single self-contained binary, and has zero CGo
dependency. These properties were the original motivation for choosing it
over `mattn/go-sqlite3`, which is a thin CGo binding to the SQLite C
library.

The question this document answers: how much does the pure-Go driver cost
in raw throughput terms relative to the CGo driver, measured against the
operations that matter most for xolu's write path?

Both drivers use identical SQL and identical pragma settings — WAL mode,
`synchronous=NORMAL`, `foreign_keys=ON`, 2 MB page cache, 5 s busy
timeout — matching xolu's production configuration.

---

## Benchmark design

Six benchmark families, each run with `-benchtime=3s -count=3` for stable
medians. All timings are per-operation (ns/op).

**`BenchmarkInsert`** — single INSERT inside its own `BEGIN`/`COMMIT`.
Mirrors xolu's `Create` write path without the sequence upsert or graph sync.

**`BenchmarkUpdate`** — single UPDATE inside its own `BEGIN`/`COMMIT`.
Mirrors xolu's `Update` write path.

**`BenchmarkSelect`** — single SELECT by primary key. Mirrors xolu's `Get`
read path without JSON unmarshalling.

**`BenchmarkBatch10` / `BenchmarkBatch100`** — N INSERTs inside one
`BEGIN`/`COMMIT` with a pre-prepared statement. Mirrors xolu's `/commit`
endpoint behaviour.

**`BenchmarkCreateInner`** — two statements inside one transaction: a
sequence upsert (`INSERT ... ON CONFLICT DO UPDATE ... RETURNING next_id`)
followed by an entity INSERT. This is the closest approximation to xolu's
actual `createInner` function, minus graph edge sync and FTS indexing.

---

## Results

### Raw benchmark output (median of 3 runs)

```
BenchmarkInsert_modernc         41,474 ns/op    576 B/op   15 allocs/op
BenchmarkInsert_modernc         44,360 ns/op    576 B/op   15 allocs/op
BenchmarkInsert_modernc         47,108 ns/op    576 B/op   15 allocs/op

BenchmarkInsert_mattn           37,478 ns/op    712 B/op   21 allocs/op
BenchmarkInsert_mattn           41,177 ns/op    712 B/op   21 allocs/op
BenchmarkInsert_mattn           40,233 ns/op    712 B/op   21 allocs/op

BenchmarkUpdate_modernc         27,453 ns/op    528 B/op   14 allocs/op
BenchmarkUpdate_modernc         29,527 ns/op    528 B/op   14 allocs/op
BenchmarkUpdate_modernc         27,463 ns/op    528 B/op   14 allocs/op

BenchmarkUpdate_mattn           25,420 ns/op    680 B/op   21 allocs/op
BenchmarkUpdate_mattn           24,646 ns/op    680 B/op   21 allocs/op
BenchmarkUpdate_mattn           26,016 ns/op    680 B/op   21 allocs/op

BenchmarkSelect_modernc          8,970 ns/op    800 B/op   27 allocs/op
BenchmarkSelect_modernc          9,012 ns/op    800 B/op   27 allocs/op
BenchmarkSelect_modernc          9,033 ns/op    800 B/op   27 allocs/op

BenchmarkSelect_mattn            7,391 ns/op    872 B/op   31 allocs/op
BenchmarkSelect_mattn            7,605 ns/op    872 B/op   31 allocs/op
BenchmarkSelect_mattn            7,572 ns/op    872 B/op   31 allocs/op

BenchmarkBatch10_modernc        75,982 ns/op   2,392 B/op   82 allocs/op
BenchmarkBatch10_modernc        70,121 ns/op   2,394 B/op   82 allocs/op
BenchmarkBatch10_modernc        68,332 ns/op   2,392 B/op   82 allocs/op

BenchmarkBatch10_mattn          61,076 ns/op   2,384 B/op   70 allocs/op
BenchmarkBatch10_mattn          58,029 ns/op   2,384 B/op   70 allocs/op
BenchmarkBatch10_mattn          58,976 ns/op   2,384 B/op   70 allocs/op

BenchmarkBatch100_modernc      308,136 ns/op  18,232 B/op  712 allocs/op
BenchmarkBatch100_modernc      312,325 ns/op  18,232 B/op  712 allocs/op
BenchmarkBatch100_modernc      309,141 ns/op  18,232 B/op  712 allocs/op

BenchmarkBatch100_mattn        237,600 ns/op  16,784 B/op  520 allocs/op
BenchmarkBatch100_mattn        228,805 ns/op  16,784 B/op  520 allocs/op
BenchmarkBatch100_mattn        236,610 ns/op  16,784 B/op  520 allocs/op

BenchmarkCreateInner_modernc   103,414 ns/op   1,304 B/op   38 allocs/op
BenchmarkCreateInner_modernc   103,948 ns/op   1,304 B/op   38 allocs/op
BenchmarkCreateInner_modernc   107,086 ns/op   1,304 B/op   38 allocs/op

BenchmarkCreateInner_mattn      79,520 ns/op   1,480 B/op   45 allocs/op
BenchmarkCreateInner_mattn      79,148 ns/op   1,480 B/op   45 allocs/op
BenchmarkCreateInner_mattn      82,523 ns/op   1,480 B/op   45 allocs/op
```

### Summary table (median ns/op)

| Benchmark | modernc (pure Go) | mattn (CGo) | mattn faster by |
|-----------|------------------:|------------:|----------------:|
| INSERT (single tx) | 44,360 | 40,233 | **9.3%** |
| UPDATE (single tx) | 27,453 | 25,420 | **7.4%** |
| SELECT (by PK) | 9,012 | 7,572 | **16.0%** |
| Batch INSERT ×10 | 70,121 | 58,976 | **15.9%** |
| Batch INSERT ×100 | 309,141 | 236,610 | **23.5%** |
| CreateInner (seq+insert) | 103,948 | 79,148 | **23.8%** |

### Throughput comparison (ops/sec, single operation)

| Operation | modernc | mattn | Δ |
|-----------|--------:|------:|--:|
| INSERT | 22,500 | 24,900 | +10% |
| UPDATE | 36,400 | 39,300 | +8% |
| SELECT | 111,000 | 132,000 | +19% |
| CreateInner | 9,600 | 12,600 | +31% |

---

## Analysis

### The gap is real but bounded

The CGo driver (`mattn`) is consistently faster than the pure-Go driver
(`modernc`) across every benchmark. The margin ranges from **7–10%** on
simple single-statement transactions to **24%** on the
`CreateInner` benchmark that most closely mirrors xolu's actual write path.

This is not surprising. `mattn/go-sqlite3` is a thin C binding — the SQLite
C library executes natively, and only the Go↔C boundary crossing adds
overhead. `modernc.org/sqlite` transpiles the same C source to Go and runs
it through the Go runtime, which adds interpreter overhead and triggers
more GC pressure on the Go side.

### Where the gap is largest

The `CreateInner` and `Batch100` benchmarks show the largest gaps (~24%).
Both involve multiple statements per transaction. The overhead compounds
per-statement: `modernc` pays more per `Exec` and `QueryRow` call because
each call goes through more Go code to reach the transpiled C layer.
`mattn` pays a smaller but fixed CGo boundary crossing per call. At 2–4
statements per transaction, the compounding is visible.

The `SELECT` benchmark shows a 16% gap despite being a single-statement
read. This is significant because reads are the most frequent operation in
most deployments. The per-call overhead of `modernc` is measurable even
for a simple primary key lookup.

### Allocation profile

Counterintuitively, `mattn` uses *more* memory per operation (712 B/op vs
576 B/op on INSERT; 1,480 B/op vs 1,304 B/op on CreateInner) and *more*
allocations per operation (21 vs 15; 45 vs 38). The CGo boundary requires
Go-side memory for C string marshalling and result buffers that the pure-Go
driver handles differently. Despite allocating more, `mattn` is faster
because the execution of the SQL itself is quicker in native C.

---

## Translating to xolu's observed latency

The benchmark measures the raw driver cost. Xolu's observed `Create` latency
of ~688 µs (from the olubench report) is much higher than the 104 µs
`CreateInner` benchmark. The gap is explained by:

- **JSON marshalling**: ~50–80 µs per entity depending on payload size
- **`syncGraphEdges`**: field scan + optional edge insert
- **`indexForFTS`**: flag check (near-zero when disabled)
- **`database/sql` connection acquisition overhead**: ~10–20 µs per
  `withRetryCreateVal` call before the transaction even opens
- **OS-level contention**: the benchmark runs on an isolated temp database;
  production databases have WAL readers, checkpoint activity, and page
  cache misses

With the CGo driver, `CreateInner` runs in ~79 µs instead of ~104 µs —
a saving of ~25 µs. Applied to the full xolu `Create` path, this would
improve the median single-insert latency from ~688 µs to approximately
660–665 µs. The percentage improvement (~4%) is far smaller than the
driver-level benchmark suggests because the driver cost is not the dominant
term in xolu's total write latency.

For reads, the 16% improvement at the driver level translates to a more
meaningful improvement in xolu's `Get` path because the driver cost is a
larger fraction of total read latency (~9 µs out of ~200 µs ≈ 4.5% of the
total; a 16% driver improvement ≈ 1.4 µs saving ≈ ~0.7% of total read
latency). Also not the dominant term.

---

## Decision

**The CGo driver is not worth adopting at this time.**

The performance gain on the full xolu write path is approximately 3–5%,
which does not move the needle on the 9–10× gap between xolu and PostgreSQL
on write operations. That gap is structural — single writer, per-transaction
overhead, `database/sql` call chain — and the driver is a small component
of it.

The cost of adopting `mattn/go-sqlite3` is significant:

- Requires a C compiler at build time (`gcc` or `clang`)
- Breaks pure-Go cross-compilation (`GOOS=windows GOARCH=amd64` from Linux
  requires a cross-compiler)
- CGo introduces a separate class of build failures and debugging surface
- The binary is no longer self-contained in the same sense — it links
  against the system libc

The point at which this trade-off would reverse is if xolu's write latency
ceiling mattered more than the pure-Go deployment model — for instance, if
the SQLite backend were expected to serve 5,000+ writes/sec sustained on
a single tenant. At that scale, the SQLite ceiling is the binding
constraint regardless of driver, and the correct response is PostgreSQL
graduation, not a 5% driver improvement.

**The recommendation stands: keep `modernc.org/sqlite` as the SQLite driver.
Invest in PostgreSQL backend readiness instead.**

---

## Reproducing these results

```bash
mkdir sqlite_bench && cd sqlite_bench
cat > go.mod << 'EOF'
module sqlite_bench
go 1.25
require (
    github.com/mattn/go-sqlite3 v1.14.45
    modernc.org/sqlite v1.52.0
)
EOF
# copy bench_test.go from this repository's benchmark source
go mod tidy
CGO_ENABLED=1 go test -bench=. -benchmem -benchtime=3s -count=3
```

Requires `gcc` or equivalent C compiler for `mattn/go-sqlite3`.
Results will vary by CPU, OS scheduler behaviour, and disk I/O
characteristics. Run on a quiet machine with no concurrent heavy workloads
for reproducible numbers.
