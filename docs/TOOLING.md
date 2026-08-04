# xolu — Development Tooling

Updated: 2026-08-03

This document is the operating manual for xolu's code-management
tooling. It assumes **zero session context**: a developer — human, or a
Claude session with no memory of previous ones — should be able to work
the full development cycle from this page alone. Read it before
touching the tree.

The tools live in `scripts/`. Every one of them supports `--help`, and
every one follows two design invariants, adopted after repeated,
documented incidents:

1. **Exit codes are the only truth.** No tool requires piping its
   output to be readable, because piping is how guard exit codes get
   silently disarmed (it happened three recorded times before the
   tooling was rewritten). Full detail always goes to a log file;
   stdout carries a bounded summary; check `$?`, not vibes.
2. **Long work is journaled and resumable.** Session environments kill
   long processes without warning. Any tool that runs the test suite
   does it in shards, records each shard in a journal file, and
   resumes from the journal when re-invoked. A killed run costs the
   remaining shards, never the completed ones.

---

## 0. Session bootstrap (once per fresh environment)

The tree needs: Go (version per `go.mod`'s `go` directive; install the
latest stable from https://go.dev/dl/), the module cache
(`go mod download` from the repo root — the proxy is normally
reachable), and `golangci-lint` (install with
`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@<version pinned in .github/workflows/ci.yml>`).
The c04dcheck analyzer builds on demand from `tools/c04dcheck` — the
release tool does this itself.

## 1. Intake: is this tree what it claims to be?

**When:** at the start of any session that begins from a checkpoint
zip, before trusting the tree as a baseline for new work.

    python3 scripts/baseline.py --zip <checkpoint.zip> --dir <workdir>
    # or, for an already-unpacked tree:
    python3 scripts/baseline.py --tree <workdir>

What it does: verifies every file against the `MANIFEST.sha256`
embedded in checkpoints from v0.16.22 onward (older checkpoints get a
warning, not a failure); builds; runs the full suite sharded; compares
observed pass/skip/fail counts against the numbers `TESTING.md`
recorded at release. Exit 0 = the tree reproduces its own release
record. Exit 1 = it does not — **file the divergence in the register
(see §2) before doing anything else; a witnessed divergence is
evidence, not noise.** Interrupted? Re-invoke with the same arguments;
completed shards are skipped.

## 1a. Mid-session: is the tree still green after an edit?

**When:** after changing code, before the next release regenerates
`TESTING.md`'s own numbers. `baseline.py` (§1) cannot answer this — its
comparison is against `TESTING.md`'s *already-recorded* counts, so it
reports a false divergence for any legitimate test-count change (a new
test added) that hasn't shipped yet. `release.py` (§4) can answer it,
but only as one step inside the full guarded pipeline — reaching for
the whole release machinery just to check "did my edit break anything"
is disproportionate mid-investigation.

    python3 scripts/regrun.py                          # full suite
    python3 scripts/regrun.py --race                   # full suite, -race
    python3 scripts/regrun.py --race --tag t138-probe   # named journal
    python3 scripts/regrun.py --tag t138-probe           # re-invoke: resumes

Same sharding/resume discipline as `baseline.py` (§0's second design
invariant): a killed run costs only its incomplete shard, and
concurrent investigations should use distinct `--tag`s so their
journals cannot collide. Exit 0 = `run=N fail=0`. Journal and log
files (`.regrun-<tag>-*`, `regrun-<tag>.log`) sit outside every path
`release.py`'s own `ZIP_SOURCES` allowlist walks (§4), so they cannot
leak into a checkpoint by construction — no exclusion pattern needed,
same reason `.baseline-*` needs none. Clean them up between unrelated
investigations regardless; they're scratch, not committed state.

Originates from the T-138 lock-order deadlock investigation
(2026-08-02/03) — verifying a concurrency fix needed both a full-suite
regression and a `-race` rerun of the specific reproducer, repeatedly,
without re-litigating the checkpoint's own manifest each time.

## 1b. The canonical test runner (`run_tests.sh` / `make test`)

**One implementation, not three.** Before 0.24.2, this repo ran tests
three genuinely different ways: `make test`'s bare `go test -short
./...`, `run_tests.sh`'s own ~300-line bash implementation (its own
coverage parsing, its own category classification via grep), and this
repo's Python machinery (`testrun.py`, shared by `baseline.py`/
`release.py`/`regrun.py`). They could silently diverge in what they'd
catch — and did: T-139, an intermittent "too many open files" failure
from a real file-descriptor leak in `Server.Stop()`, reproduced
immediately under the bare/bash paths and was invisible under this
sandbox's own sharded Python runs. Not because sharding fixed
anything — because splitting the suite into smaller per-process
batches changes the odds of a leaked-resource bug crossing a
per-process ceiling, without touching the bug itself. Three
orchestration paths meant a test result on one didn't reliably mean
the same thing as a result on another.

`scripts/runtests.py` is now the single implementation. `run_tests.sh`
is a two-line shim (`exec python3 scripts/runtests.py "$@"`), and every
general-purpose Makefile test target (`test`, `test-v`, `test-race`,
`coverage`, `coverage-html`, `coverage-check`, `test-quick`,
`test-report`) goes through it. Package-specific dev-convenience
targets (`test-storage`, `test-oql`, etc.), stress/benchmark targets,
and the Redis-dependent targets remain direct `go test` invocations
deliberately — they're single-package iteration shortcuts or have
their own container-lifecycle concerns, not the "is the whole suite
green" question this consolidation is about.

    python3 scripts/runtests.py              # full report, unsharded
    python3 scripts/runtests.py --full        # include stress tests
    python3 scripts/runtests.py --race
    python3 scripts/runtests.py --threshold 75
    python3 scripts/runtests.py --html
    python3 scripts/runtests.py --shard-size 8 --tag ci   # constrained env

**Default is unsharded** — every package in one `go test` invocation,
matching this tool's own historical behaviour and preserving the
property that actually caught T-139. `--shard-size` (journaled,
resumable, same `.runtests-<tag>-*` pattern as `regrun.py`) exists for
environments with a genuine per-process resource ceiling — this
sandbox, not a general default. Reach for it there; don't reach for it
just because the full run is slow, since that's exactly the tradeoff
that let T-139 hide.

## 1c. Wave programme progress (`docs/SUBSTRATE_TRACKING.md §1`)

`scripts/wave_progress.py` regenerates that doc's own "Progress at a
glance" block from each wave's own per-item table in §2 — the bars
used to be hand-typed prose and drifted from the data they summarised
(Wave 1 was misreported 1/2 for a stretch; T-113 in `RESOLVED.md`).
Never hand-edit the fenced block; edit a wave's own table instead and
run:

    python3 scripts/wave_progress.py           # regenerate in place
    python3 scripts/wave_progress.py --check   # exit 1 if stale, don't write
    python3 scripts/wave_progress.py --show    # print to the terminal, touch nothing
    make waves                                 # same as --show

Wired into `release.py`'s generate step, so it's never stale for more
than one release regardless of whether anyone remembers to run it by
hand.

Each wave line carries two optional subtitles, both mechanically
derived, never guessed:

- **`debt:`** — open `docs/TRACKING.md` items whose theme is mapped to
  that wave via the script's own `WAVE_THEMES` dict (e.g. `bal` →
  wave 4). Excludes anything already counted as some wave's own item
  even if the theme matches a *different* wave — `T-81` is
  `iolu`-themed but is wave 8's own item 37, so it must not also show
  as wave 6 debt. `WAVE_THEMES` is hand-curated (new theme, new wave:
  update the dict) and deliberately doesn't cover every theme — items
  with no wave affiliation at all (the majority of the open register)
  correctly show no debt line anywhere, not a false attribution.
- **`blockers:`** — items in that wave's own scope (its items or its
  debt) whose `After: T-NN` field in `TRACKING.md` names a
  **currently open** prerequisite. `After: none`, references to
  already-shipped work, and unverifiable references (`T-52` says
  `After: T-51`, but `T-51` has never actually been filed in either
  `TRACKING.md` or `RESOLVED.md`) are all correctly excluded — an
  unverifiable reference is noise, not a real blocker. Blockers can be
  cross-wave (`T-81`, wave 8's own item, is blocked by `T-70`, a wave
  6 item) — that's the useful signal, not filtered out.

Both were verified genuinely discriminating, not just plausible-looking:
each was tested by flipping a real table row in a scratch copy and
confirming `--check` caught it, in both directions.

## 1d. Adding a new wave (`scripts/add_wave.py`)

Never hand-edit a new `### Wave N` section into `SUBSTRATE_TRACKING.md`
or `SUBSTRATE_DEVELOPMENT_PLAN.md` — picking the wave number and item
numbers by eye is exactly how a real collision happened once already:
`SUBSTRATE_TRACKING.md`'s own prose had already soft-reserved "wave
11" for unrelated future work (`/far`/`/dxp/mxn`) in a sentence, not a
formal heading, which a naive "highest heading + 1" scan would have
missed and silently overwritten.

    python3 scripts/add_wave.py \
        --name "pkg/client blob support" \
        --ideal-days 2 \
        --items-json '[{"summary": "...", "register_item": "T-142"}]' \
        --plan-note "Reopens the client's own documented v0.16.0 exclusion..." \
        [--wave-number N]   # override; still checked for collision, not trusted blindly
        [--dry-run]         # preview everything, touch nothing

One call does all of it: computes a wave number checked against both
formal headings and prose mentions anywhere in either document,
continues item numbering from the highest existing `| NN |` row
(global and sequential, never reused, matching wave 9b's own 51-56
continuing after wave 10's 45-50 despite sitting between them in the
roadmap), inserts the `SUBSTRATE_TRACKING.md` table section and the
`SUBSTRATE_DEVELOPMENT_PLAN.md` pointer paragraph, adds the wave's
entry to `wave_progress.py`'s own `SHORT_NAMES` dict, and regenerates
the bars — nothing left as a "remember to also do X" manual follow-up.
That last part was a real gap found by running the tool once, not a
hypothetical: the first live use left the bars reading "(unnamed wave
12)" until `SHORT_NAMES` insertion was added to the same run.

What it deliberately does NOT write: the `--plan-note` rationale
paragraph itself — why the wave exists, what it depends on, how it's
sequenced. That's a judgement call this project's own conventions
already treat as something written deliberately each time, not
generated; the tool places it correctly and consistently, it doesn't
compose it.

## 2. During work: the live register

`docs/TRACKING.md` holds open actionable items only; closed items live
in `docs/RESOLVED.md`. Never hand-execute the closure procedure — the
tool does it correctly every time:

    python3 scripts/register.py list                 # what is open
    python3 scripts/register.py show T-53            # one item's detail
    python3 scripts/register.py add --summary "..." --theme ts \
        --priority P2 --body "- **Trigger:** ...\n- **Scope:** ..."
    python3 scripts/register.py close T-53 --version 0.16.23
    python3 scripts/register.py check                # consistency

`add` files the status-table row and the detail section together (they
must never diverge — the release gate checks). `close` performs the
whole closure procedure as one operation: the item's full text moves
verbatim to the top of RESOLVED.md stamped with version and date, and
both the row and the detail section leave the register. After a close,
add the cross-reference in the CHANGELOG entry by hand — the changelog
says *what shipped*; RESOLVED.md says *what was wrong*; they reference,
never duplicate, each other. `--dry-run` on `add`/`close` prints diffs
without writing. **File issues the moment they are found**, not at
session end.

The release gate's register checks delegate to this tool's parser, so
the editor and the gate cannot disagree about what "consistent" means.

## 3. Dormant guards: verification that does not run by default

Stress tests, fuzz targets, and multi-core race harnesses do not run in
the default invocation — and **a shipped guard that never runs guards
nothing** (this repository learned that the hard way; see RESOLVED.md
on G-12). The registry lives in `docs/KNOWN_ISSUES.md` § Dormant
guards; the tool makes its execution records queryable:

    python3 scripts/guards.py list                   # ids + last exercised
    python3 scripts/guards.py stale                  # not run since last release
    python3 scripts/guards.py handoff G-01 G-04      # block to hand to the
                                                     # multi-core machine's owner
    python3 scripts/guards.py record G-01 --date 2026-07-25 --env m1

The flow for hardware this environment lacks: `handoff` emits
ready-to-run invocations; a human runs them on the multi-core box and
reports back; `record` updates the registry (previous record preserved).
Race-class guards **cannot** be closed from a single-CPU environment —
a sandbox pass of a concurrency test is not evidence.

**Writing a new dormant guard and registering it are one act** — add
the G-nn block in the same session the test is written.

## 4. Cutting a release

    python3 scripts/guards.py stale        # resolve/hand off/record skips first
    python3 scripts/release.py <version>   # after writing the CHANGELOG entry

Prerequisites the tool checks or needs: a `## [<version>]` CHANGELOG
entry (warned if absent); `Version:` headers in TRACKING.md and
KNOWN_ISSUES.md matching (the gate fails otherwise — bump them as part
of the release edit); golangci-lint on PATH.

The orchestrator runs, in order, every exit checked in-process:
validate → syncver → build → **tests** (sharded, journaled) → **lint**
→ **c04dcheck** (failed on ANY output — its checker exits 0 when
analysis is skipped, so silence is the only pass) → generators
(TESTING.md, badges) → consistency → **gate** → clean → zip (with
embedded MANIFEST.sha256, contamination scan, binary sniff, size
ceiling). The four bold steps are the four guards; nothing ships
around them.

If the run is killed: `python3 scripts/release.py <version> --resume`.
Journaled-green expensive steps are skipped; generators, the gate, and
the archive checks always re-run — no journal entry excuses a gate.
Full output is in `release-<version>.log`; step state in
`.release-state.json`.

After a green run, commit and tag:

    git commit -m "Release <version>: <headline>"
    git tag v<version>

## 5. Failure playbook

- **A tool exited nonzero:** read its named log file, fix, re-invoke
  (with `--resume` where supported). Do not work around a gate.
- **A run was killed mid-flight:** nothing is lost; the journal has the
  completed steps. Re-invoke.
- **The register or guards registry seems malformed to a tool:** the
  tool is the shared parser the gate uses — treat its complaint as a
  gate failure and fix the document, not the tool, unless the document
  is following TRACKING_PRACTICES.md and the parser is provably wrong.
- **Ad-hoc shell:** if you must compose shell around these tools,
  never pipe a guarded command's output before checking its exit, and
  use `bash` explicitly if you use bash idioms — `/bin/sh` aborts on
  them and silently kills whatever followed on the line.

## 6. File inventory

| Tool | Purpose |
|------|---------|
| `scripts/release.py` | Release orchestration (journal, shards, four guards, archive) |
| `scripts/baseline.py` | Checkpoint intake: manifest + build + suite vs TESTING.md |
| `scripts/register.py` | Live-register operations incl. the closure procedure |
| `scripts/guards.py` | Dormant-guard registry: currency, handoff, recording |
| `scripts/testrun.py` | Shared sharded-test machinery (imported, not a CLI) |
| `scripts/regrun.py` | Mid-session regression: is the tree green after an edit, ahead of the next release |
| `scripts/runtests.py` | The canonical test runner -- coverage, category classification, colour output, threshold gate, HTML/charts. `run_tests.sh` and `make test`/`test-v`/`test-race`/`coverage*` are thin wrappers around this, not separate implementations (T-139) |
| `scripts/ed.py` | Journaled handle-based editing: find/apply/sub/undo (`--help`, `selftest`) |
| `scripts/roles.py` | Syntactic-role auditor for occurrences (mass-substitution rule, mechanized) |
| `scripts/syncver.py` | VERSION ↔ version.go sync (imported by release.py; CLI too) |
| `scripts/release_gate.py` | Release-readiness checks (invoked by release.py) |
| `scripts/wave_progress.py` | Regenerates `docs/SUBSTRATE_TRACKING.md`'s progress-at-a-glance summary (bars + a per-wave debt subtitle from open register items sharing that wave's theme + a blockers subtitle when a wave's own items or debt are waiting on a currently-open prerequisite) from each wave's own per-item status table -- run standalone after editing a wave table, or automatically every release (wired into `release.py`'s generate step) |
| `scripts/add_wave.py` | Adds a new wave deterministically -- computes a safe wave number (checked against both formal headings AND prose reservations like "plausibly wave 11") and the next sequential item numbers, inserts sections into both `SUBSTRATE_TRACKING.md` and `SUBSTRATE_DEVELOPMENT_PLAN.md`, updates `wave_progress.py`'s own `SHORT_NAMES`, and regenerates the bars -- one call, nothing left as a manual follow-up |
| `release.sh`, `syncver.sh`, `run_tests.sh` | Thin shims to the above (`run_tests.sh` -> `scripts/runtests.py`), kept for muscle memory |
| `.repoman.json` | repoman configuration (id prefix, version-sync targets); the portable toolset at github.com/ha1tch/repoman operates on this tree via it — see T-56 for the planned script migration |

Generated artifacts (never hand-edit): `TESTING.md`, badge lines in
`README.md`/`MANUAL.md`. Journals and logs (`.release-state.json`,
`.baseline-state.json`, `release-*.log`) are excluded from checkpoints.
