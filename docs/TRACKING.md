# xolu — Tracking (live register)

Version: 0.30.43-pre1_fixes01
Last reviewed: 2026-10-06

Open actionable items only — debt, defects, gaps, hardening, tooling, and
features filed as prerequisites. A closed item still present here is itself
a defect. Practices: `docs/TRACKING_PRACTICES.md`.

Related documents:
- `docs/RESOLVED.md` — closed items, append-only (T-01, T-15, T-17, T-18,
  T-29, T-30, T-31, D-001–D-009, and the retired TD-nnn namespace live there).
- `docs/KNOWN_ISSUES.md` — intentional limits, invariant boundaries, and
  recorded decisions; not actionable work.
- `docs/API_V2_TRACKING.md` — API v2 stage delivery. Demarcation: the S-table
  owns stage detail; where a register item overlaps a stage (T-07/T-09/T-10 vs
  S13–S17), the item links to the stage rather than duplicating it.
- `docs/SULPHER_OPTIMISATION_ROADMAP.md` — Sulpher performance opportunities,
  deliberately kept as a benchmark-gated roadmap rather than register items.
- `CHANGELOG.md` — what shipped, when.

Status legend: ✓ done · ◐ partial · ☐ not started · ✗ dropped

## Status table

The table is derived from the `Theme / Priority / Status / Blocks` field
lines carried by every detail section below; the two must not diverge
(consistency is a T-22 release-gate check). Priority is importance only
(P1 highest); themes group the detail sections; provenance lives in each
item's Trigger line.

| ID | Summary | Theme | Pri | Status | Blocks / after |
|----|---------|-------|-----|--------|----------------|
| T-07 | Durable dispatch, at-least-once delivery | events | P3 | ☐ | Blocks: agentic reliance on events as source of truth (molu included). Overlaps: S16 (d… |
| T-08 | Wire deferred event types | events | P3 | ☐ | Overlaps: S15 (additional trigger sources). Companion: T-20 could ship together. |
| T-09 | Wire deferred event actions (`sulpher`, `fsm.walk`) | events | P3 | ☐ | Overlaps: S14 (`fsm.walk` action); `sulpher` action in the S13–S17 family. |
| T-10 | True synchronous event execution | events | P3 | ☐ | Overlaps: S13 (sync event actions). |
| T-11 | Delivery ordering guarantees | events | P3 | ☐ | — |
| T-27 | Guard-evaluated `GetMachineTransitions` | fsm-read | P4 | ☐ | Pairs: T-28 (single FSM read-surface polish release). Not required by molu Part 2 today. |
| T-28 | Paginate FSM machine history | fsm-read | P4 | ☐ | Pairs: T-27 (single FSM read-surface polish release). Not required by molu Part 2 today. |
| T-20 | Emit schema-change events | events | P4 | ☐ | Non-blocking (molu can poll). Companion: T-08; and T-12 if new types adopt the dotted-s… |
| T-03 | Unified function registry across OQL / Sulpher / FSM eval | query-engines | P4 | ☐ | After: S14 (`@FSM` OQL function — see `API_V2_TRACKING.md`); registry taxonomy is final… |
| T-05 | Remove inert config fields `BlobDir` / `DynConfigFile` | storage-config | P4 | ☐ | Standalone since T-01 shipped. |
| T-23 | `syncver.sh` / `release.sh` for tsqlparser | tooling | P4 | ☐ | Lands in the tsqlparser repo, not xolu. |
| T-12 | Federation-consistent subjects and references | events | P5 | ☐ | After: 1.0 (naming settled; matching reshape is post-1.0). Companion: T-20 if new types… |
| T-13 | Author-named transitions in `event_latch_source` | events | P5 | ☐ | — |
| T-06 | Schema modes (b) strict and (c) clone | storage-config | P5 | ☐ | No timeline committed; design in `SCHEMA_MODES_DESIGN.md`. |
| T-33 | Partial `StoreConfig` in `loadTenantEntitiesFromStore` scoped store | storage-config | P5 | ☐ | Inert today (read-only path); fragile against future writes. |
| T-16 | `cal` daypart rollup-prune performance validation | cal | P5 | ☐ | After: realistic occupancy distributions are available to measure against. |
| T-42 | Dockerfile audit and refresh before wave-programme close | ops | P4 | ☐ | End-of-programme item so shipped images carry release-quality xolu, not interim wave states. |
| T-14 | SSA-based dataflow analysis for wall-clock usage | tooling | P5 | ☐ | — |
| T-46 | Retrofit /ts to the two-identity model (@C04d): external string timeline id at the wire, internal uint32 as codec key — the design cal and bal already use. Breaking API change; eliminates ts's numeric-id boundary surface entirely | ts | P4 | ☐ | After: a v2 ts API window (breaking). Correct long-term direction; ts's int64+helper form holds the line until then. |
| T-47 | Deflake `TestDeleteTimeline_DeletingMarker` concurrent-reader subtest | ts | P4 | ☐ | — |
| T-49 | Calendar-period policy package (Ruby/ActiveSupport-shaped: Period + all_* constructors, Next/Prev, quarter/half/fiscal-year accessors, configurable week start defaulting Sunday) | time | P3 | ☐ | Blocks: T-50. After: chronicle grain constructors rewritten on top of it. |
| T-50 | Normalise implicit ISO/Monday week assumptions to the house default | time | P3 | ☐ | After: T-49. Behaviour change to DATEPART('week') output — changelog-visible. |
| T-52 | bal: temporal-policy model (backdating/sealing) as authoritative account-set state, not meta | bal | P2 | ☐ | After: T-51. @C04c forbids the engine reading meta; @B07's "account-set" is named but never defined. |
| T-53 | ts: rollup delete-marker fence — concurrent reader observed a defined-but-empty timeline during delete | timeseries | P2 | ☐ | Race-class: verdict needs multi-core (G-04 environment); sandbox reruns pass. |
| T-54 | dxp: in-memory reservation pivot — substrate-level reservation cache, participant interface, invalidation-by-loss error granularity | dxp | P2 | ◐ | After: item 18 core (0.16.20). Blocks: items 19–21 (adapters, defs, coordinator). Carries item 18's deferred sweeper wiring + guard-plane adoption question. |
| T-55 | chronicle temporal policy: per-timeline backdating configuration, one vocabulary across /ts, /cal, /bal | chronicle | P1 | ◐ | blocks T-51 fix shape; subsumes T-52's policy-storage question; after: none |
| T-56 | migrate xolu scripts to repoman consumers (github.com/ha1tch/repoman) | tooling | P3 | ☐ | — |
| T-66 | chronicle.Engine.Append has no locking (lost-update race on concurrent same-bucket appends) -- currently masked for bal by SQL commit latency, but the fix belongs in chronicle itself before ts migrates onto it | chronicle | P2 | ☐ | After: none. Related: T-62 (where this was found, not introduced). Blocks: nothing directly, but should be resolved before ts's wave-3-intended migration onto chronicle.Engine. |
| T-68 | iolu tenant provision-cal: provisions/rebuilds cal's Pebble occupancy index offline -- a real gap found while scoping item 24, but NOT item 24 itself (see T-69 for that correction) | iolu | P3 | ◐ | After: none. Related: T-69 (item 24's actual scope, cal CRUD, still open). |
| T-69 | iolu cal create/list/info/delete (item 24, wave 6, as actually specified): the real no-workaround gap -- no operator path exists to create a bookable calendar without writing Go | iolu | P2 | ☐ | After: none. Leads wave 6 per the roadmap's own sequencing ("the only gap with no workaround"). Related: T-68 (a different, already-shipped gap found while scoping this one). |
| T-70 | iolu query + repl (item 25, wave 6): one-shot OQL/sulpher/SQL query execution, then an interactive REPL borrowing aulsql's iaul shape | iolu | P2 | ☐ | After: none. Second in the roadmap's own sequencing, after cal CRUD (T-69). |
| T-71 | iolu db backup (item 26a, wave 6): rescoped 2026-08-17 to wrap pkg/tenantexport's already-shipped ExportTenant, called offline as a library -- ships more complete coverage than the original SQLite-online-backup-API scope, which is split out to XOT203 | iolu | P2 | ☐ | After: none. Related: T-72 (verify, unaffected by this rescope), T-73 (check), XOT203 (physical backup, the split-out original scope), XOT204 (restore, both paths). |
| T-72 | iolu db verify (item 26b, wave 6): PRAGMA integrity_check across all databases, confirming SQL-level structural integrity -- distinct from db check's oracle-based semantic checks (T-73) | iolu | P3 | ☐ | After: none. Related: T-71 (backup), T-73 (check, a different kind of verification). |
| T-73 | iolu db check: promote remaining oracles (item 26c, wave 6) -- cal index-equals-rebuild and blob-usage-walk oracles still need wiring in; graph and all three bal oracles already are | iolu | P3 | ☐ | After: none, but directly enabled by T-68 (cal's rebuild path already exists as a callable function). Related: T-71 (backup), T-72 (verify, a different kind of check). |
| T-74 | iolu db rebuild-graph (item 26d, wave 6): explicit offline graph rebuild with a before/after node/edge count report -- the rebuild logic already exists server-side, this is invocation and reporting only | iolu | P3 | ☐ | After: none. Related: T-71/T-72/T-73 -- the fourth and cheapest of the roadmap's recommended operations-hygiene batch. |
| T-75 | iolu db reindex-fts (item 27, wave 6): offline FTS backfill for deployments that enable full-text search after already accumulating data -- demand-triggered, not urgent, per the roadmap's own explicit guidance | iolu | P4 | ☐ | After: none. Deliberately last of the concretely-scoped wave-6 items -- build on first real need, not speculatively. |
| T-76 | OQL primitive-query dispatch infrastructure (item 32, wave 8): recognise TableValuedFunction/MethodCallExpression FROM/WHERE items, route to a new provider interface -- everything else in wave 8 depends on this | oql | P2 | ☐ | After: none. Blocks: T-77/78/79/80 (all four @-primitive providers). Leads wave 8 per docs/proposals/oql-primitive-queries.md's own sequencing. |
| T-77 | @FSM() OQL query provider (item 33, wave 8): first of the four primitive providers -- fsm is fully SQL-backed, proves the dispatch mechanism with the least new surface area | oql | P3 | ☐ | After: T-76 (dispatch infrastructure). Related: T-78/79/80 (the other three providers). |
| T-78 | @BAL() OQL query provider (item 34, wave 8): second of the four primitive providers -- SQL-authoritative with a Pebble satellite, moderate difficulty | oql | P3 | ☐ | After: T-76 (dispatch infrastructure). Related: T-77/79/80 (the other three providers). |
| T-79 | @CAL() OQL query provider (item 35, wave 8): third of the four primitive providers -- span/availability queries are a genuinely different pattern from bal's point lookups despite the similar SQL+Pebble storage shape | oql | P3 | ☐ | After: T-76 (dispatch infrastructure). Related: T-77/78/80 (the other three providers). |
| T-80 | @TS() OQL query provider (item 36, wave 8): last and hardest of the four primitive providers -- fully Pebble, no SQL fallback, likely a scalar/aggregate call shape rather than row-producing | oql | P3 | ☐ | After: T-76 (dispatch infrastructure). Related: T-77/78/79 (the other three providers). |
| T-81 | iolu / xia split (item 37, wave 8): rename current admin CLI to xia, repurpose iolu around querying the substrate (a normal scriptable CLI, PLUS an iaul-style REPL as one subcommand, not REPL-only) -- separable track, 43 files affected, mostly mechanical | iolu | P3 | ☐ | After: T-70 (query+REPL mechanics must exist before iolu's name means something new). Related: T-76-T-80 (same planning pass, no code dependency). |
| T-90 | Intermittent flake observed in pkg/timeseries's concurrent-delete fencing test -- not reproduced on 6 subsequent runs, filed for visibility rather than investigated (out of scope for dxp work), not dismissed | timeseries | P4 | ☐ | Unrelated to dxp work (T-87/T-88/T-89) -- filed separately rather than folded in, since the packages share no dependency. |
| T-91 | pkg/validation.JSONSchemaValidator's own doc comment claims Loose mode by default, but its actual code uses queryfy.Strict -- a real, pre-existing discrepancy found while adversarial-testing dxp/txn's bindings validation, not this session's to fix | validation | P4 | ☐ | Unrelated to dxp work -- found as a side effect of T-89's own adversarial test suite. dxp/txn's own validation deliberately matches the real (Strict) behavior, not the doc comment. |
| T-106 | two small release/version tooling gaps found while running the actual 0.20.0 release: release.py's zip exclusions didn't cover .pyc/__pycache__ (fixed), syncver.py's bump-minor doesn't respect --help and executes instead (found, not fixed) | tooling | P4 | ◐ | After: none. Neither touches xolu's own shipped code -- both are about the tooling used to ship it. |
| T-114 | System stores content (item 31, wave 7): ~metering (ts) and ~billing (bal) -- the only wave-7/8 item never filed at all | system-stores | P3 | ☐ | After: #9 (sysmask, closed T-43/T-44), #17 (bal molu surface, closed T-67 v0.20.2) -- both already met. Blocks: nothing hard; closes wave 7 alongside item 30 (T-07-T-13). |
| T-134 | obj patterns: the full §4a mechanism deferred out of T-124 (obj-01-rest-api.md §4a, obj-00-design.md §7a) -- def/extract/list/get/delete endpoints, pattern/pattern_after wiring into attach, pattern/pattern_id/pattern_deleted GET fields, XOLU-OBJ013 reachability | obj | P3 | ☐ | none directly |
| T-137 | tsqlparser chained method-call access (`@FSM.state(1).name`) drops the base call entirely -- structural parser defect, not a display bug | oql | P3 | ☐ | After: none. Relevant to: whichever wave-8 provider first needs chained access (not T-76, not scoped this wave). |
| T-148 | dispatchEvent fires unconditionally on every v1 CRUD write, regardless of APIV2Enabled -- eventStore() never checks it, so any v1-only deployment logs a WARN on every single write (event_defs genuinely doesn't exist until v2 is on) | server | P3 | ☐ | — |
| XOT196 | New repoman-descended tool, scripts/str_replace_extended.py: format-aware, journaled, base64-payload substitution -- built to retire the failure class that direct str_replace/sed/awk substitution invites (shell-quoting corruption, edits landing without a syntax check), following a real gap found and fixed during the tool's own build (a balanced escape-and-reenter injection the first delimiter check missed) | tooling | P2 | ◐ | After: none. Pending: upstream sync to github.com/ha1tch/repoman (T-56), repoman's own selftest.py extension, Horacio applying the proposed working-agreement and skill amendments. |
| XOT197 | /cron item 1: storage, def/job registration, prototype-clone, capability introspection -- the def is the prototype, the job the armed clone; verb capabilities (cancellable, progress-reporting) discovered by interface assertion at def time and recorded on the def, never self-declared | cron | P2 | ☐ | Blocks: every other /cron item. After: none (design decisions cron-primitive.md §10.2/§10.3 should be settled before item XOT198 starts, not before this one). |
| XOT198 | /cron item 2: recurrence rule object, IANA zones, DST policy, scheduler loop, tick plane, missed-fire + overlap policy, suppression-streak counter, watchdog shape + reset -- the deep pit and the racy loop, deliberately one item so the tick plane and the rules that decide which ticks chime are built and tested together | cron | P2 | ☐ | After: item XOT197. Blocks: items XOT199, XOT201, XOT202. Prerequisite decisions: cron-primitive.md §10.2 (recurrence grammar) and §10.3 (DST enum) MUST be settled before this item starts. |
| XOT199 | /cron item 3: the chime -- `cron.chime` event type into EVENT_MODEL.md's closed set, shape discriminated in event_latch_source, payload carrying due-instant/def/job/shape and originally-due for catch-up ticks; unconditional on every non-suppressed tick regardless of T-07 | cron | P2 | ☐ | After: item XOT198. Blocks: item XOT201 (job control's info reads chimes alongside ticks). Not blocked by T-07 -- designed around it (cron-primitive.md §6). |
| XOT200 | /cron item 4: verb registry, the cancellable and progress-reporting interfaces, and two verbs (`fsm_walk`, one prune) -- capabilities are what the Go type implements, discovered by item XOT197's assertion; each further verb is its own small addition, never speculative | cron | P2 | ☐ | After: item XOT197. Blocks: items XOT201, XOT202. cron-primitive.md §10.6 (OQL-verb reconciliation with the events OQL-action target) is deliberately NOT in this item. |
| XOT201 | /cron item 5: job control and execution observability -- `info`/`pause`/`resume`/`stop`/`kill` split across job and execution planes, in-memory execution and progress state (never persisted; Postgres pg_stat_progress precedent), `kill` refuses honestly on non-cancellable verbs, `resume` hands accumulated due instants to the job's own missed-fire policy | cron | P2 | ☐ | After: items XOT198, XOT199, XOT200. Blocks: item XOT202 (system jobs are controlled through this surface). cron-primitive.md §10.10 (progress phase vocabulary) and §10.11 (stop-drain deadline) decided here. |
| XOT202 | /cron item 6: retention + `~trim`, reclamation classes + Reclaimer interface + `/blob` Reclaimer + `~sweep`, system jobs armed at startup, dxp janitor migrated onto /cron, REST surface + pkg/client + molu + docs + dormant-guard registration -- the wave's closing item, where the primitive proves itself by maintaining xolu on its own clock | cron | P2 | ☐ | After: items XOT197, XOT198, XOT199, XOT200, XOT201. Blocks: none. Sequencing note: if the eager stale-checkpoint recompute (cron-primitive.md §8) is included as a v1 dogfooding job, T-55's remaining /cal and /ts rulings should land first; otherwise the janitor migration carries the dogfooding proof alone. |
| XOT203 | iolu db backup: physical/raw-file single-instance backup -- SQLite online-backup API per database file plus Pebble checkpoints for ts, cal, and bal's rollup index (all three, corrected from T-71's original cal-only scope) -- split out of T-71 when T-71 was rescoped to reuse pkg/tenantexport's logical export instead | iolu | P4 | ☐ | After: none. Related: T-71 (the logical-export path this complements, not replaces), T-72 (db verify, unaffected by this split). |
| XOT204 | iolu db restore -- documented manual procedure only today (T-71/T-72's own stated initial boundary: 'copy back onto a stopped server'); never filed as its own trackable item until now | iolu | P4 | ☐ | After: XOT203 (physical backup) for full restore-by-copy; can restore from tenantexport's logical export independently once that path exists, at reduced guarantee (see scope note). |
| XOT215 | roles.py's Go block-comment detection (_go_role) is a naive substring count of '/*'/'*/' across the whole file, not comment/string-aware -- a literal '/*' inside an ordinary // line comment (e.g. a URL pattern like /api/v2/cal/*) is misread as an unclosed block comment, misclassifying every line after it as go-comment for the rest of the file | tooling | P3 | ☐ | After: none. |
| XOT232 | JSON numbers pass through float64: integers above 2^53 change on store and read (UseNumber is used only by bal and dxp) | storage-config | P3 | ☐ | After: none. |
| XOT237 | A per-type commit-only write policy: refuse plain POST/PUT/PATCH/DELETE and OQL data statements on a managed type | storage-config | P4 | ☐ | After: X3, X6. |
| XOT241 | OQL answers XOLU-QL004 for an entity type that has no rows, though types are created implicitly on first write | oql | P3 | ☐ | After: none. |
| XOT242 | `SELECT *` on an adapted table is not translated to SQL, so it always takes the Go path and reads the whole table | oql | P4 | ☐ | After: XOT228. |
| XOT243 | `POST /schema/{entity}` saves the schema document before it migrates the table, so a refused migration leaves the saved schema ahead of the table | storage-config | P3 | ☐ | After: none. |
| XOT244 | Registering a schema for an entity type that already has rows (blob storage) hides those rows: lists, queries and reads by id stop finding them | storage-config | P2 | ☐ | After: none. |
| XOT245 | `/commit` `delete` does not cascade: it is refused while XOLU_CASCADING_DELETE is on | storage-config | P4 | ☐ | After: XOT233. |
| XOT246 | The OQL push-down thresholds are calibrated at start, so the same build pushed filters at 30 rows on one start and above 100 on another | oql | P4 | ☐ | After: none. |
| XOT247 | `$now` is not persisted across a restart, and `committed_at` is returned only when a commit sets `templates` | storage-config | P4 | ☐ | After: none. |
| XOT248 | The default store's /ts must be provisioned by a client call; there is no start-up or lazy provisioning | ts | P4 | ☐ | After: none. |
| XOT249 | The /ts leg of /commit is not atomic with the SQLite transaction: a failed undo leaves an orphan event (XOLU-CM016) | ts | P3 | ☐ | After: none. |

---



## API surface



---

## FSM read surface



## Plan deviations (recorded per @P header rule)

- **2026-08-17 — wave 6 code-reuse review: T-71 rescoped, wave 13
  revised, wave 14 created, one real tooling bug found and fixed.**
  Direct instruction: review `pkg/tenantexport` (the existing
  backup-to-blob feature) for wave-6 reuse before building T-71 fresh,
  then rescope everything the review touched and move postponed
  concerns to a new wave. Four linked outcomes, one session:
  - **T-71 rescoped in place**, not replaced: `pkg/tenantexport`
    (T-149, v0.25.0, already shipped) exports offline as a library
    call, no server required, and already covers more (`ts`/`cal`/`bal`
    Pebble stores plus `/loc`/`/obj`) than T-71's original SQLite-
    online-backup-API scope ever named. Estimate 2.0d -> 0.5d.
  - **Two postponed concerns split out and filed, not dropped**:
    XOT203 (physical/raw-file backup -- the byte-exact guarantee
    `tenantexport`'s logical export cannot make) and XOT204 (restore --
    named as out-of-scope in T-71/T-72 since the roadmap was written,
    never once filed as its own item until now). A register-wide
    search for other related debt (backup, restore, DR, Litestream)
    found nothing else genuinely adjacent -- `FLEET_ARCHITECTURE.md`'s
    Litestream replication is real but fleet/nolu-scoped, a different
    document's problem, not bundled in to avoid the wave-7-audit's own
    named failure mode (manufacturing scope).
  - **Wave 14 created** (`iolu physical backup + restore`, items
    64-65, ≈3.0d, XOT203/XOT204) via `add_wave.py`. Wave 6 revised
    8.5d -> 8.0d to match T-71's new estimate.
  - **`pkg/gc` found as in-tree prior art for wave 13 (/cron), while
    tracing `tenantexport`'s TTL sweep.** A generic, already-production
    ticker scheduler with four real consumers and two real incident-
    driven fixes (a double-`Start` lifecycle guard; panic recovery
    after a real CI-crashing panic, T-156/T-157) -- more authoritative
    than any of `cron-primitive.md` §13's external DBMS citations,
    since its incidents are this project's own. Folded in as new §13.10,
    a panic-recovery requirement added to §6 (the same lesson,
    inherited rather than re-learned), and §8's dogfooding table gained
    four already-working migration targets. Wave 13 revised 15.75d ->
    15.5d (scheduler-loop lifecycle shell -0.5d for the proven pattern
    to mirror; dogfooding item +0.25d for four consumers instead of
    one) -- the harder half of /cron's design (recurrence rules, DST,
    tick/chime planes) has no `pkg/gc` analogue and gained nothing.
  - **One real, previously-latent tooling bug found and fixed same
    session, filed and closed as XOT205:** `wave_progress.py`'s
    `all_wave_tnums` exclusion set recognised only `T-NNN` ids, never
    `XOT NNN` -- invisible until wave 14 shared a theme (`iolu`) with
    wave 6 and an XOT-format item needed excluding from the wrong
    wave's debt line. Fixed, verified against both broken and fixed
    output, no regression on an unaffected wave's own debt/blockers
    line (wave 8, checked directly).
  - **Checked, not assumed: none of this belonged in repoman.**
    T-56's own scope line names exactly five repoman-covered scripts
    (`ed`/`roles`/`register`/`guards`/`syncver`) -- `wave_progress.py`
    and `add_wave.py` are xolu-substrate-specific tooling with no
    repoman analogue, confirmed by reading T-56 directly rather than
    assumed. T-56's own text separately surfaced that the sync
    direction on two *other*, unrelated defects runs the other way --
    repoman's extraction already fixed two things xolu's local
    `register.py` still carries latently -- noted on T-56 for whenever
    it's picked up, not acted on here.
  - **Rebased 2026-08-17, same session: this entire entry originally
    filed against a stale local clone of xolu.** A fresh GitHub clone
    at session start silently returned v0.30.23, missing a full day of
    real work (v0.30.24-26, including XOT193-196) that existed only in
    an unpushed local checkpoint, surfaced only when that checkpoint
    was supplied directly. Four ids this session had assigned
    (XOT194-197 in the original filing) collided with different, real,
    already-shipped content under those same numbers in the checkpoint
    lineage; a fifth (the session's own XOT193, a `ZIP_SOURCES` fix)
    turned out to duplicate a bug already found and fixed independently
    in the checkpoint lineage under its own real XOT195, and was
    dropped rather than re-filed. All nine of this session's real items
    renumbered to the true next-free sequence (XOT197-205) against the
    checkpoint as base; content unchanged, only ids and cross-references
    corrected. Built as a separate merged tree, the stale branch and the
    real checkpoint both kept intact and unmodified for retrieval.

- **2026-08-17 — wave 7 audited for real vs. rationalised justification;
  finding split, no scope change yet, decision pending.** Direct
  question, prompted by wave 7 sitting at 0/2 since 2026-08-01 with no
  blocker recorded (`docs/RESOLVED.md`/`CHANGELOG.md` show attention
  went to waves 9, 10, 12, and now 13 instead): was wave 7 real need,
  or work rationalised into existence by the plan's own investor-facing
  framing? Examined item by item, evidence traced rather than asserted.
  - **Item 30 (event model part 2, T-07 through T-13) — real, and
    predates any wave or fundraising context.** Traced to
    `docs/API_V2_TRACKING.md`'s S9 entry (v0.10.0, patch015): the async-
    only, at-most-once base explicitly deferred "sync execution,
    retry/backoff, dead-letter/replay, `sulpher`/`fsm.walk` actions, and
    the other event types" to S13–S17 -- the identical scope, filed as
    debt the moment the base shipped, months before the wave programme
    existed. T-07 names a real, if not yet triggered, failure mode:
    "Blocks any consumer (molu included) treating events as source of
    truth for state change." Not urgent today -- nothing currently
    depends on events as authoritative, and `docs/proposals/cron-
    primitive.md` (this session, wave 13) explicitly designed `/cron`'s
    chime *around* T-07 rather than waiting on it, one more concrete
    data point that nothing live is blocked by it. **Verdict: legitimate
    debt, correctly filed, not falsely prioritised as urgent** -- the
    plan's own "(provisional)" tag on its 5-day estimate already hedges
    honestly.
  - **Item 31 (system stores content, T-114: `~metering`/`~billing`) --
    the suspicion holds up on the evidence found.** The prerequisite
    *mechanism* is real and already shipped (`pkg/timeseries/sysmask.go`
    -- `IsSystem`, tested, in production use). But no consumer, internal
    or downstream, was found anywhere asking for usage metering or
    billing: every real Seam AMS trigger in `docs/RESOLVED.md` concerns
    guard syntax, FSM validation, or client bugs -- none concern billing,
    ever. The item's own cited source, `@S03` ("residents"), does not
    resolve to any document findable in this tree, so its rationale
    beyond the plan's own prose cannot be independently confirmed either
    way. That prose is the plan's ONLY stated rationale, quoted exactly:
    *"their completion is the natural demo artefact for fundraising
    conversations."* A follow-up reconstruction (API-call counting per
    tenant, noisy-neighbour detection, Seam billing) was floated and
    checked: noisy-neighbour mitigation IS real and already documented,
    but in `docs/FLEET_ARCHITECTURE.md`'s own "Future Considerations"
    section -- unbuilt, fleet/nolu-scoped, about advisory CPU/IO quotas
    at the hot-instance level, a different and later concern than a
    substrate-resident API-call counter. Seam billing remains pure
    hypothesis, zero recorded demand. Generic per-tenant API-usage
    counting is buildable and would serve `FLEET_ARCHITECTURE.md`'s
    already-spec'd `iolu report usage --tenant` command independent of
    billing -- but "buildable and sensible" is not the same claim as
    "requested," and only the weaker claim holds here.
  - **Not decided here, deliberately -- pending Horacio's call:**
    whether to (a) leave T-114 as filed, (b) narrow its scope to generic
    API-usage counting only (drop the billing framing, keep the fleet-
    report tie-in), (c) mark it explicitly parked pending a real
    consumer, or (d) drop it from wave 7 and re-shape wave 7 around item
    30 alone (≈5 provisional days, not the current ≈10). No register
    item's status or priority changed by this entry; T-114 (§ below)
    carries a pointer to this finding.

- **2026-08-01 — waves 9 and 10 created: `/loc` and `/obj` formally
  scheduled.** `SUBSTRATE_DEVELOPMENT_PLAN.md` previously had no wave
  placement for either primitive (`loc-00-design.md`'s own self-review
  had flagged this as an open item: "Wave placement — loc is absent
  from `SUBSTRATE_DEVELOPMENT_PLAN.md` entirely; needs a decision").
  Direct instruction: create the waves. Ten new inventory items (41–50,
  four for wave 9, six for wave 10) filed as T-115 through T-124, all
  ☐ not started, all citing loc-00/01/02-design.md and
  obj-00/01/02-design.md directly rather than re-deriving scope.
  Ideal effort: wave 9 ≈ 9 days, wave 10 ≈ 10.75 days, ≈ 19.75 days
  combined — this session's own estimate, calibrated against `bal`'s
  actual build velocity (both plans explicitly mirror `bal`'s shape),
  not stated anywhere in the source design documents themselves.
  Sequencing: 9 before 10 is a hard dependency (item 45 routes through
  item 42's real geometry, not `/loc`'s Stage-2 test hook); neither
  wave depends on 6, 7, or 8. Highest-risk items named explicitly:
  #41 and #46 (T-115, T-120) are both flagged design-then-race, not
  TDD, needing dormant-guard registration and a real multi-core run
  from the team, same convention as `bal`'s G-13. Not scheduled in
  either wave: `/far`/`/dxp/mxn` (far-and-dxp-mxn.md) — see the
  entry immediately below this one for that decision's own record.

- **2026-08-01 — cross-tenant/cross-instance dxp trigger fired, not
  pulled forward.** `SUBSTRATE_DEVELOPMENT_PLAN.md` §6's Deferred entry
  naming "cross-tenant and/or cross-instance dxp transactions" as its
  trigger is answered by `docs/proposals/far-and-dxp-mxn.md` (`/far` +
  `/dxp/mxn`), reviewed and accepted as a proposal. T-54's own narrower
  "Parked" cross-tenant-same-instance sketch is reconciled as that
  document's special case, not implemented separately (T-54's own
  entry carries the pointer). Direct instruction, same day: do not
  schedule implementation yet — sequenced behind three things, in no
  particular order but all required: Pablo's CMMS-side development
  teams reaching productivity on xolu, `/loc` and `/obj` shipping, and
  nolu's own development resuming (currently paused). Instance
  discovery specifically is expected to route through nolu rather than
  `/far` building its own mechanism, one more reason this waits on
  nolu's bandwidth rather than being independently workable. If this
  is ever scheduled, it is plausibly wave 11, not 9 or 10 (both now
  taken by `/loc`/`/obj`, see the entry immediately above) — not
  decided, flagged for whoever schedules it (far-and-dxp-mxn.md §9).

- **2026-07-29 — item 21 (dxp/txn coordinator) rescoped: 2PS dropped
  entirely, 3PS is the sole execution model; item 22 (previously "3PS
  phase machine," a second model layered atop a 2PS coordinator)
  retired and merged into 21.** Direct instruction: "We don't need 2PS
  for now, we just need a complete 3PS implementation across all
  substrates, including entity create and proper invalidation." Two
  new items filed as item 21's companions/prerequisites, not
  afterthoughts: item 38 (entity CREATE — EntityAdapter is UPDATE-only
  today; the proposal's own §3 worked example assumes CREATE exists,
  a real gap between the doc's illustration and what item 19 actually
  built) and item 39 (cross-substrate invalidation — the
  dxp.Participant post-commit verb T-57 already flagged as missing,
  now properly scoped and budgeted rather than a bare cross-reference).
  Effort: item 21 revised 3d → 5d (absorbing item 22's retired 2d);
  item 38 ~1.5d; item 39 ~2d. Wave 5 total: 16d → 19.5d.
  Also recorded (docs/SUBSTRATE_DEVELOPMENT_PLAN.md §6, Deferred):
  persistent (durable, cross-process) dxp transactions are explicitly
  not planned and may never be built — the in-memory, crash-abandon
  model (T-54's pivot) is the shipped design, not an interim state.

- **2026-07-29, same day — refinement of the above.** "Dropped
  entirely" overstated it. Corrected framing, direct instruction:
  2PS and the wider phase-spectrum beyond 3PS (the external dxp
  framework's own formal spectrum names 3PS+quorum specifically,
  §2a) are **pending, not a hard requirement — deferred unless
  needed**, not abandoned. The named trigger: cross-tenant and/or
  cross-instance dxp transactions. Cross-tenant dxp is already a
  parked item in T-54's own record (mounting a remote /dxp/txn object
  from another tenant of the same instance) — its stated prerequisite
  ("after item 18") is now met, so it is live-parked, not stale.
  Cross-instance dxp is a larger scope again — quorum across
  independent instances, nolu territory per @D08, not a dxp v1
  concern regardless of tenant scope. Filed as its own Deferred entry
  (§6), separate from the persistent-transactions entry above — a
  different concept with a different (concrete, not "maybe never")
  reactivation condition.

- **2026-07-21 — item 7 (/meta subject-addressing generalisation)
  resequenced: wave 0 → wave-4 opening act.** Audit found /meta still
  entity-only (routes hard-wired to `(entity, positive-int)`; @C04c's
  subject list unimplemented). Deliberately deferred, not overlooked:
  nothing in waves 2–3 consumes generalised subjects (RI never reads
  meta — @C04c engine-inert; chronicle extraction doesn't touch it),
  the costliest slice (cal delete-path cascade hooks) would be
  refactored by wave 3's Sealer lift if done first, and bal
  (`bal.account`) is the first real non-entity consumer — landing it as
  wave 4 opens registers the subject kind live in the same breath.
  Effort estimate at decision time: ~1.25d (storage migration 0.25,
  subject model 0.25, handlers 0.25, cascade wiring 0.25–0.5,
  tests+docs 0.25). Trigger to pull earlier: a non-entity annotation
  consumer materialising before bal (none on the horizon).

### T-46. Retrofit /ts to the two-identity model (@C04d)

Theme: ts · Priority: P4 · Status: ☐
Blocks/after: needs a breaking-change window (v2 ts API). Not urgent — ts's corrected int64+helper wire form (@C04d fallback) is safe today; this is the structural upgrade from "corrected" to "correct by construction".

- **What:** give a ts timeline two identities like cal and bal — a
  caller-chosen external *string* id at the API boundary, and the
  existing `TimelineID uint32` demoted to an engine-internal dense codec
  key allocated by the store. Removes the numeric id from the wire
  entirely, so @C04d is satisfied by construction rather than by the
  int64+helper discipline.
- **Why deferred (P4, breaking):** every current ts client references
  timelines by number; moving to a string external id changes every ts
  endpoint's contract. It is the right model — cal and bal both chose it
  — but it is a v2-ts-sized redesign with client/molu/wire impact, not a
  bug fix. The 32-bit bug that motivated @C04d is already fixed by the
  wide-carry form; this item is the architectural follow-through, done
  when a breaking ts-API window opens.
- **Reference:** chronicle-substrate.md §4d (the two-identity preferred
  structure); cal (`CalendarID` string + `CalOrdinal uint32`) and bal
  (`account_id` string + internal key) as the pattern to match.



### T-47. Deflake `TestDeleteTimeline_DeletingMarker` concurrent-reader subtest

Theme: ts · Priority: P4 · Status: ☐
Blocks/after: —

Trigger: 2026-07-21 sandbox full-suite run — subtest
`concurrent_reader_during_delete_never_sees_defined-but-empty` failed once
("reader observed a defined-but-empty timeline during delete — marker did
not fence the read"), then passed 5/5 isolated reruns and a full-suite
rerun, all on the single-CPU sandbox. One spurious failure in ~4 full-suite
runs that day.

Two live hypotheses, unresolved: (a) the test's reader/delete
choreography assumes an interleaving that single-CPU scheduling only
sometimes produces — a test-harness timing assumption, not a product
defect; or (b) the deleting-marker fence has a genuine narrow window that
single-CPU scheduling occasionally exposes and multi-core would expose
more often. The distinction matters: (a) is a deflake, (b) is a defect.
Next step is characterisation on real multi-core silicon
(`GOMAXPROCS=<cores> go test ./pkg/timeseries/ -run
TestDeleteTimeline_DeletingMarker -count=200 -race`) before any code
change; do not "fix" the test blind. Until characterised, treat sandbox
full-suite failures of this one subtest as suspect-flake: rerun isolated
before diagnosing unrelated work.

### T-42. Dockerfile audit and refresh (end-of-programme)

Theme: ops · Priority: P4 · Status: ☐
Blocks/after: Scheduled deliberately late — after most of the wave programme has landed, so the image ships the best available substrate rather than a mid-flight version.

- **Trigger:** During v0.16.1 re-cut the Dockerfile was bumped `golang:1.22-alpine` → `golang:1.26-alpine` alongside the toolchain sync, but the image itself has not been rebuilt or exercised since. Wave-era changes (uint32 tenant IDs, chronicle extraction, bal, dxp, iolu operations) will each touch things a Docker build could regress on quietly.
- **Work required:** rebuild the image against latest main; smoke-test it (`docker run xolu` → binaries respond on `/health`, `/version`); confirm the base image is still current stable Go; align any CMD/ENTRYPOINT changes wave work introduces.
- **Timing:** after wave 5 (dxp) or wave 6 (iolu), whichever settles the substrate for a release deemed "shippable to a customer."
- **Estimate:** ~half a day if nothing has broken; a day if wave changes have reshaped configuration surface.

### T-27. Guard-evaluated `GetMachineTransitions` (server-side option)

Theme: fsm-read · Priority: P4 · Status: ☐
Blocks/after: Pairs: T-28 (single FSM read-surface polish release). Not required by molu Part 2 today.

- **Trigger:** the client's `GetMachineTransitions` returns input symbols with a structurally-defined transition from the current state. xolu's handler at `handleFSMMachineTransitions` deliberately does not evaluate guards. If molu's `machine_state` tool needs to advertise "walks that will actually succeed right now," there is no way to compute that without attempting each walk and handling `XOLU-FSM005` rejections — which mutates history for a probe.
- **Impact:** low today. Molu Part 2 §5.1.9 does not currently require guard-evaluated transitions. Filing so that if it comes up during molu implementation, the option exists rather than being papered over client-side.
- **Work required:**
  - Add a `?evaluate=true` query parameter to `GET /fsm/machine/{id}/transitions`. When set, the handler runs each candidate transition's guard against the current variable state and returns only those whose guards permit the walk.
  - Alternative: a separate route `GET /fsm/machine/{id}/available` that always evaluates guards.
  - Client method: extend `GetMachineTransitions` to accept an `evaluate bool` parameter, or add a sibling `GetAvailableWalks(ctx, id)`.
- **Estimate:** half a day for the server-side; guard evaluation code already exists in `pkg/fsm/eval`.

### T-28. Paginate `GET /api/v2/fsm/machine/{id}/history`

Theme: fsm-read · Priority: P4 · Status: ☐
Blocks/after: Pairs: T-27 (single FSM read-surface polish release). Not required by molu Part 2 today.

- **Trigger:** xolu v0.14.5's `handleFSMMachineHistory` returns the full history in one response. The client's `GetMachineHistory` reflects that: no pagination parameters. For long-running machines with thousands of walks, a single response can be arbitrarily large.
- **Impact:** unbounded response size on long-lived machines. Not urgent — most machines in the intended use cases are short-lived. But once molu is in real use, this becomes a real concern for any tenant with high-cardinality state processes.
- **Work required:**
  - Add pagination parameters to `GET /fsm/machine/{id}/history`: `?limit=N` (bounded by a server-side maximum), `?before_id=N` and `?after_id=N` for cursor-based paging, or `?since=<timestamp>` for time-anchored paging. Cursor-based is the safer choice.
  - Response envelope grows to include a `next_cursor` field when more history remains.
  - Client method: extend `GetMachineHistory` to accept a `HistoryFilter` struct, matching the pattern used by `ListMachines`. Existing callers passing only `(ctx, id)` continue to work; the new filter is optional.
- **Sequencing:** clean pairing with T-27 (both are FSM-machine read-surface improvements). Could ship as a single "FSM machine read-surface polish" release.
- **Estimate:** 1 day for server, half a day for client.

---


## Event model

### T-07. Durable dispatch and at-least-once delivery

Theme: events · Priority: P3 · Status: ☐
Blocks/after: Blocks: agentic reliance on events as source of truth (molu included). Overlaps: S16 (dead-letter/replay), S17 (retry/backoff).

- Current model: at-most-once, single attempt, no retry, no backoff, no dead-letter, no replay.
- Crash window between commit and dispatch loses firings.
- Requires reconciliation sweep to make "delivered" mean "provably delivered".
- Blocks any consumer (molu included) treating events as source of truth for state change.

### T-08. Wire deferred event types

Theme: events · Priority: P3 · Status: ☐
Blocks/after: Overlaps: S15 (additional trigger sources). Companion: T-20 could ship together.

- Live today: `entity.created`, `entity.updated`, `entity.deleted`, `fsm.output`, `fsm.step`, `commit.applied`.
- Not yet shipped: `graph.edge.*`, `fsm.entered`, `fsm.exited`, `fsm.terminal`, `ts.appended`, `meta.expired`.
- See `docs/EVENT_PENDING.md`.

### T-09. Wire deferred event actions

Theme: events · Priority: P3 · Status: ☐
Blocks/after: Overlaps: S14 (`fsm.walk` action); `sulpher` action in the S13–S17 family.

- Live today: `webhook`, `oql`.
- Not yet shipped: `sulpher`, `fsm.walk`.
- `fsm.walk` enables machine-reacts-to-machine without external subscriber loop.

### T-10. True synchronous event execution

Theme: events · Priority: P3 · Status: ☐
Blocks/after: Overlaps: S13 (sync event actions).

- A def may declare `"execution": "sync"`; currently stored and always runs async (response carries `X-Executed-As: async`).
- In-transaction, roll-back-on-failure execution required.

### T-11. Delivery ordering guarantees

Theme: events · Priority: P3 · Status: ☐
Blocks/after: —

- Currently: events from a single request delivered unordered; subscribers must not assume any order.
- Design and ship per-firing / per-machine / per-transaction ordering as appropriate.

### T-12. Federation-consistent subjects and references

Theme: events · Priority: P5 · Status: ☐
Blocks/after: After: 1.0 (naming settled; matching reshape is post-1.0). Companion: T-20 if new types adopt dotted subjects from day one.

- Designed but not implemented: `xolu.` dotted-subject namespace, three-level wildcard matching, `LocalRef`-consistent references for nolu interoperability.
- Naming conventions settled; matching reshape is post-1.0 work.
- See `docs/NOLU_EVENTS.md`.

### T-13. Author-named transitions in `event_latch_source`

Theme: events · Priority: P5 · Status: ☐
Blocks/after: —

- Currently element-level for FSM events: `fsm/<from>:<input>:<to>`; kind-level for `commit.applied`.
- Author-named transition support deferred.

### T-20. Emit schema-change events for event-driven schema refresh

Theme: events · Priority: P4 · Status: ☐
Blocks/after: Non-blocking (molu can poll). Companion: T-08; and T-12 if new types adopt the dotted-subject namespace.

- **Trigger:** molu Part 2 §4.3 documents that molu polls the schema endpoints because xolu does not emit events for schema, FSM def, generator, or event-def mutations.
- **Current state:** verified by source inspection at v0.14 — `handleCreateSchema`, `handleFSMDefCreate`, `handleFSMDefReplace`, `handleFSMDefDelete`, `handleSeqDefine`, `handleGenDefine`, `handleEventCreate`, `handleEventUpdate`, and `handleEventDelete` do not fire events.
- **Work required:**
  - Design a `schema.*` event type family covering entity-schema, FSM-def, generator, and event-def mutations. Candidate names: `schema.entity.created/updated/deleted`, `schema.fsm_def.created/updated/deleted`, `schema.gen.created/deleted`, `schema.event_def.created/updated/deleted`.
  - Wire firings into the mutation handlers, following the existing `commit.applied` pattern.
  - Document under `docs/EVENT_MODEL.md` and add to the "live" list in `docs/EVENT_PENDING.md`.
- **Not blocking:** molu can continue polling indefinitely. This task exists so that when the polling cost becomes visible or when real-time responsiveness matters, the substrate can support it.
- **Sequencing:** natural companion to T-08 (wire deferred event types) and could ship together with the federation-consistent subject reshape T-12 if the new event types adopt the `xolu.` dotted-subject namespace from day one.

---

## Query engines (OQL / Sulpher / FSM eval)

### T-03. TD-001 — unified function registry across OQL / Sulpher / FSM eval

Theme: query-engines · Priority: P4 · Status: ☐
Blocks/after: After: S14 (`@FSM` OQL function — see `API_V2_TRACKING.md`); registry taxonomy is finalised once `@FSM` exists.

- Current state: three subsystems with incompatible dispatch mechanisms.
  - `pkg/oql`: `map[string]ScalarFunc` (`func([]interface{}) interface{}`).
  - `pkg/sulpher`: hardcoded `switch` in `evalFunctionEnv`, no extension point.
  - `pkg/fsm/eval`: tsqlruntime `FunctionRegistry`, tagged-union `Value` type.
- Design sketch recorded in KNOWN_ISSUES:
  - `XoluFunc` interface with `Identifier()`, `Category()`, `Description()`.
  - Categories: `Pure`, `Stateful`, `Transactional`. Taxonomy to be finalised now that `@FSM` (S8) exists.
  - Per-subsystem adapter interfaces (`OQLAdapter`, `FSMAdapter`, etc.) hanging off `XoluFunc`.
- Package placement decision open: extend `pkg/qs`, or new `pkg/funcregistry`.
- Current impact: low (internal organisation only). Debt grows with new call sites.

#### Extended detail (from retired TD-001)

**Packages:** `pkg/qs`, `pkg/oql`, `pkg/sulpher`, `pkg/fsm/eval` (planned)
**Introduced:** patch006 (S5 sequences)
**Deferred until:** after `@FSM` is implemented (S8)

There is currently no unified registry for the named extension functions
(`@SEQ`, `@GEN`, `@FSM`) that are intended to be callable across OQL,
Sulpher, and FSM guard expressions. Instead each subsystem maintains its
own dispatch mechanism:

- `pkg/oql/scalar.go` — a `map[string]ScalarFunc` (`func([]interface{}) interface{}`),
  extended at engine creation time via `RegisterScalarFunc`. Backing store
  for the standard T-SQL scalar functions from `pkg/qs`.
- `pkg/sulpher/executor_env.go` — a hardcoded `switch name` inside
  `evalFunctionEnv`, with access to `ctx`, `env Env`, and `*graphSnapshot`.
  No extension point. Graph-specific functions (`NODES`, `RELATIONSHIPS`,
  `PATH`) are compiled in.
- `pkg/fsm/eval` (S6, planned) — will use aulsql's `ExpressionEvaluator`
  with its own `FunctionRegistry`, typed as `func([]Value) (Value, error)`
  where `Value` is the tsqlruntime tagged union — a different type system
  from the `interface{}` world of the OQL scalar map.

The three extension functions being built across S5–S8 have incompatible
runtime contracts. `@SEQ` is a pure read of session state. `@GEN` will
dispatch to the `gen_definitions` table and call a generator's `/next`
logic. `@FSM` will issue SQL writes (a full state machine walk) and needs
access to the server's FSM walk runtime. Forcing all three into a single
`func([]interface{}) interface{}` signature loses the context access and
type safety each genuinely requires.

A unified registry that papers over those differences would be a leaky
abstraction. The correct factoring will be visible once `@FSM` exists and
the full shape of all three function surfaces is known. The refactor is
deferred to after S8.

**Current impact:** Low. The three extension functions are registered
independently in their respective subsystems. Duplicate registration logic
exists in `pkg/server/v2_gen_handlers.go` (OQL `init()`), `pkg/oql/oql.go`
(`RegisterSeqGenFuncs` at engine creation), and will exist in `pkg/fsm/eval`
(S6). No user-visible defect; internal organisation only.

**Resolution path:** After S8, review the full function surface across OQL,
Sulpher, and FSM eval. The likely shape is an `XoluFunc` interface that
describes what a function *is* rather than what it takes and returns:

```go
type XoluFunc interface {
    Identifier()  string       // "@SEQ", "@GEN", "@FSM", "UUID_V4"
    Category()    FuncCategory // Pure, Stateful, Transactional
    Description() string       // human-readable, for docs and error messages
}
```

`Category` replaces the temptation to put `InParams()`/`OutParams()` on the
interface. Parameter types belong to per-subsystem adapter interfaces, not
to the shared identity contract. A `Pure` function closes over nothing and
can be registered directly as a scalar. A `Stateful` function needs sequence
or generator state. A `Transactional` function (`@FSM`) needs a DB
transaction and the walk runtime. Each category has a corresponding adapter
type per subsystem:

```go
type OQLAdapter interface {
    XoluFunc
    EvalOQL(args []interface{}) interface{}
}

type FSMAdapter interface {
    XoluFunc
    EvalFSM(args []fsm.Value) (fsm.Value, error)
}
```

Each subsystem registers only the functions it has adapters for. The central
registry holds `XoluFunc` values; subsystems query it for functions of
categories they support. No function is forced to implement an adapter it
does not need.

The exact category taxonomy — whether `Transactional` is one category or
splits by read vs write semantics — should not be decided before `@FSM`
is implemented. The taxonomy will be obvious from the actual runtime
requirements once S8 exists. Designing it earlier risks getting the
categories wrong and needing a second refactor.

Consider whether `pkg/qs` is the right home or whether a new
`pkg/funcregistry` package is warranted. `pkg/qs` currently holds scalar
math and string functions; a registry with category-aware dispatch is a
different concern.

**Related:** D-001. Retiring the malformed `NEWID()` generator in favour
of `UUID_V4()` is part of the same FSM eval function-surface work and is
best resolved alongside this refactor.

---

## Storage and configuration

### T-05. Remove inert config fields `BlobDir` and `DynConfigFile`

Theme: storage-config · Priority: P4 · Status: ☐
Blocks/after: Standalone since T-01 shipped.

- Fields remain on `config.Config` but are no longer read after v0.13.0 — paths derive from `BaseDir` via `pkg/storelayout`.
- Env vars `OLU_BLOB_DIR` and `OLU_DYNCONFIG_FILE` are accepted and silently ignored: a quiet footgun.
- Removal touches: fields, env vars, defaults, config validation warning logic, startup display.
- Sequence with T-01: either rename first and remove the `XOLU_*` variants, or remove first and rename what remains. Do not touch twice.

### T-06. Implement schema modes (b) strict and (c) clone

Theme: storage-config · Priority: P5 · Status: ☐
Blocks/after: No timeline committed; design in `SCHEMA_MODES_DESIGN.md`.

- Only growth mode (schema-optional) exists today.
- Default schema set at `<BaseDir>/schema/`.
- Design recorded in `docs/SCHEMA_MODES_DESIGN.md`.
- Open sub-decision: disk-vs-database home for schema-set content.
- No timeline committed.

---







### T-33. Per-tenant scoped store built with partial `StoreConfig`

Theme: storage-config · Priority: P5 · Status: ☐
Blocks/after: Nothing; hardening. Found during M4b integration-suite work (v0.16.0).

- **Trigger:** `cmd/xolu/main.go` (`loadTenantEntitiesFromStore`) constructs
  a per-tenant scoped store passing only `Type`, `TenantID`, and
  `SQLitePerFileTenants` — omitting `FullTextEnabled`, `BaseDir`, `DBPath`
  and the SQLite tuning fields that every other construction site carries
  (`cmd/xolu/main.go` startup and `pkg/server` `storeForTenant` both pass
  the full set).
- **Impact:** none today — the function only calls `List()`, and FTS
  indexing gates on writes. But the construction is a trap: any future
  write through this store silently skips FTS indexing, and the omitted
  path fields rely on factory defaulting that the other sites do not.
- **Work required:** derive the scoped `StoreConfig` from `store.Config()`
  wholesale (copy + override `TenantID`) rather than naming three fields;
  or extract a `ScopedConfig(tid)` helper on `StoreConfig` so all three
  construction sites share one derivation.
- **Context:** FTS is deliberately double-gated — the server flag opens
  the API (else 503), the store flag controls indexing. Both derive from
  the same `config.FullTextEnabled` at the wired sites, so the design is
  sound; this item is about the one site that breaks the pattern.
- **Estimate:** an hour with a construction-site sweep.

## cal



### T-16. `cal` daypart rollup-prune performance validation

Theme: cal · Priority: P5 · Status: ☐
Blocks/after: After: realistic occupancy distributions are available to measure against.

- Correctness of the rollup-prune is tested.
- Performance payoff (versus doing the work without the rollup) is an open empirical question.
- Requires realistic occupancy distributions.

---


## Tooling and verification

### T-14. SSA-based dataflow analysis for wall-clock time usage

Theme: tooling · Priority: P5 · Status: ☐
Blocks/after: —

- `TestNoBareWallClock` catches syntactic shapes only (direct field set, struct literal, address-of-temp idiom, listed persisting constructors).
- Does not track cross-function dataflow.
- Does not catch `time.Now()` passed to unlisted functions that store their argument.
- Requires an SSA-based `go/analysis` pass for full coverage.


### T-23. Add `syncver.sh` and `release.sh` to tsqlparser

Theme: tooling · Priority: P4 · Status: ☐
Blocks/after: Lands in the tsqlparser repo, not xolu.

- **Trigger:** during the tsqlparser v0.6.1 release, version bumping had to be done by hand: `VERSION` file, `pkg/version/version.txt`, and four hardcoded string literals inside `pkg/version/version_test.go`. xolu's `syncver.sh` handles the equivalent job for that repo in one command.
- **Work required:**
  - Copy the `syncver.sh` pattern from xolu, adapted for tsqlparser's simpler version layout (VERSION + version.txt only).
  - Add a lightweight `release.sh` covering: version format validation, syncver, test run, CHANGELOG top-entry check, tag creation.
  - Both scripts live in the tsqlparser repo, not xolu.
- **Impact:** trivial cost, guards against the four-place version bump growing into more places as tsqlparser evolves.

---

## bal

### T-52. Temporal-policy model: authoritative account-set state, not meta

Theme: bal · Priority: P2 · Status: ☐ · Blocks/after: after T-51.

- **Trigger:** whether backdating is permitted, and whether periods seal, is domain policy — a financial ledger closes Q1 and must reject writes into it; a museum inventory must accept an entry dated 1897. bal needs this configurable rather than hard-coded either way.
- **Where it must NOT live:** the `/meta` sidecar. @C04c states meta is engine-inert; an admission guard reading policy from `entity_meta` would also break guard locality (@C04a), since the guard's transaction does not own that store — the exact failure mode bal exists to avoid. Meta may carry the *descriptive* layer (why a policy was set, by whom, review dates); it must not be read at decision time.
- **Where it should live:** authoritative bal state, read in-transaction alongside `floor`, `ceiling` and `postable`, which the guard already reads this way.
- **Open design question — granularity.** @B07 refers to "the account-set's seal frontier" but the proposal never defines an account-set; it appears exactly once and is otherwise unspecified. Sealing is normally a property of a *book* (a set of accounts closed together), not of one account: per-account sealing would let two accounts in the same book disagree about whether March is closed. Decide whether bal introduces an explicit account-set/book entity as the policy carrier, or accepts per-account policy with callers responsible for consistency.
- **Related, out of scope here:** ts solves the same underlying problem differently, with `RollupDef.LateWindow` (wait for late arrivals before computing a bucket) rather than reject-or-recompute. If a shared policy vocabulary is wanted, these should be reconciled rather than invented twice under different names. Noted, not actioned — ts is outside wave 4.

---

## Time and calendar

### T-49. Calendar-period policy package

Theme: time · Priority: P3 · Status: ☐ · Blocks/after: blocks T-50; chronicle's grain constructors are rewritten on top of it once it lands.

- **Trigger:** bal needed month/quarter/year rollup grains. `time.Truncate` is duration-based and cannot express calendar periods; Go's stdlib provides `AddDate` and `ISOWeek` but no period boundaries, no quarters, no fiscal years. Ruby's ActiveSupport (`beginning_of_quarter`, `all_month`, `next_quarter`, `quarter`) is the reference shape — note this is Rails, not Ruby stdlib, which is much thinner.
- **Shape:** `Period{Start, End}` half-open, with `all_*`-style constructors (`Day`, `Week`, `Month`, `Quarter`, `Half`, `Year`, `FiscalYear`), `Next`/`Prev` navigation, and accessors (`QuarterOf`, `HalfOf`, `WeekOf`, `FiscalYearOf`).
- **Deliberate divergences from ActiveSupport:** half-open intervals throughout — no inclusive `end_of_*` returning 23:59:59, which is an off-by-one and sub-second-gap generator. UTC only, no location parameter: the DST ambiguity flagged in the Ruby core discussion of `floor(:month)` is sidestepped rather than handled.
- **Week start:** configurable per call, defaulting to **Sunday** (US and Uruguay convention). Sunday is the default, not the only permitted value — callers with a reason may pass Monday.
- **Home:** NOT `pkg/xolutime`, whose README explicitly declares it "a thin enforcement package, not a calendar layer: it guarantees discipline, it does not own policy." Calendar conventions are policy. A new package importing `xolutime` for the UTC invariant is the right home; it must satisfy `TestNoBareWallClock`.
- **Also note:** Go's `AddDate` normalises rather than clamps (`Jan 31 + 1 month = Mar 3`). Navigating from period *starts* avoids this; exposing raw month-addition on arbitrary instants would not.

### T-50. Normalise implicit ISO/Monday week assumptions

Theme: time · Priority: P3 · Status: ☐ · Blocks/after: after T-49.

- **Trigger:** week-start conventions are currently implicit and inconsistent. Two classes found:
  - **Explicit ISO:** `pkg/qs/scalar.go` and `pkg/fsm/eval/functions.go` call `t.ISOWeek()` for `DATEPART('week', …)` — Monday-based numbering.
  - **Accidental Monday:** `time.Truncate(7*24h)` aligns to Monday because Go truncates since year 1 (a Monday). Any fixed-width week grain inherits this without saying so.
- **Scope:** route these through the T-49 helper so the convention is explicit and overridable, rather than swapping one hidden assumption for another.
- **Explicitly NOT in scope:** `pkg/server/ts_handlers.go`'s `"7d"` interval is a rolling seven-day window, not a calendar week. It has no week-start convention and must not be changed.
- **Consequence to record at release:** `DATEPART('week', …)` output changes from ISO numbering to the house default. xolu integrity outranks T-SQL compatibility here (a deliberate decision), but it is a visible behaviour change and belongs in the changelog as such, not as a silent fix.
- **Open sub-question:** week *numbering* scheme alongside Sunday start — "week containing Jan 1" (US style) versus ISO's first-Thursday rule. Decide during T-49.

## timeseries

### T-53. Rollup delete-marker fence: reader observed defined-but-empty timeline

Theme: timeseries · Priority: P2 · Status: ☐ · Blocks/after: verdict requires the G-04 environment (multi-core, `-race`); not closable from a sandbox pass.

- **Trigger:** baseline verification of the pristine 0.16.19 checkpoint (session of 2026-07-22, before any 0.16.20 work) failed once: `TestDeleteTimeline_DeletingMarker/concurrent_reader_during_delete_never_sees_defined-but-empty` — `rollup_test.go:1180: reader observed a defined-but-empty timeline during delete — marker did not fence the read`. Five immediate reruns passed.
- **Why filed rather than dismissed:** a witnessed interleaving on a single CPU is positive evidence of a real window; a pass is only absence of evidence. The G-12 saga cuts both ways — apparent races can be artefacts, but the artefact diagnosis there was *proven* (instrumentation showed enforcement skipped), not assumed. This one gets the same treatment: diagnose, don't shrug.
- **Scope:** establish whether the deleting-marker actually fences concurrent readers in the rollup plane, or whether a window exists between marker placement and bucket removal. Reproduce under `-race` on multi-core (m1 or gh-runner) with an elevated iteration count before touching code.
- **Not in scope:** the delete path's correctness for non-concurrent use — asserted elsewhere and passing.

### T-90. Intermittent flake observed in pkg/timeseries's concurrent-delete fencing test -- not reproduced on 6 subsequent runs, filed for visibility rather than investigated (out of scope for dxp work), not dismissed

Theme: timeseries · Priority: P4 · Status: ☐ · Blocks/after: Unrelated to dxp work (T-87/T-88/T-89) -- filed separately rather than folded in, since the packages share no dependency.

- **Trigger:** observed as a side effect of the full-tree verification sweep after T-89's dxp/txn work, not sought out -- filed for visibility rather than left unrecorded, per this session's own discipline for anything found during verification.
- **What was observed:** TestDeleteTimeline_DeletingMarker/concurrent_reader_during_delete_never_sees_defined-but-empty (pkg/timeseries/rollup_test.go) failed once during a full `go test ./...` run: "reader observed a defined-but-empty timeline during delete -- marker did not fence the read."
- **Investigated directly before filing, not assumed unrelated:** pkg/timeseries has zero dependency on anything touched this session (dxp/def, dxp/txn, ParticipantStore, the four adapters). Re-ran the specific test in isolation 5 times -- passed cleanly every time. Re-ran the full tree sweep a second time -- passed clean, no reproduction. Consistent with a pre-existing, load/timing-sensitive flake that only surfaces under full-suite parallel contention, not a deterministic regression.
- **Not investigated further -- out of scope for this session's dxp work.** The test's own name and failure message describe a real concurrency-fencing property (a reader must never observe a "defined but empty" state mid-delete), which is exactly the kind of race this codebase has precedent for taking seriously (T-34's own history: a race that passed months of single-core testing, found only by running on real hardware). Whoever picks this up should treat a single non-reproducing failure as a genuine signal worth a dedicated stress-test pass (-race, repeated runs under load), not dismiss it as noise just because it didn't reproduce twice.
- Not started.

## dxp

### T-54. In-memory reservation pivot: substrate-level cache, participant interface, error granularity

Theme: dxp · Priority: P2 · Status: ◐ · Blocks/after: after item 18 core (0.16.20); blocks items 19–21 (adapters, defs, coordinator). Carries item 18's deferred sweeper wiring and the guard-plane adoption question.

- **Trigger:** design session following 0.16.20. The persisted tentative-row model forced the guard-plane adoption question onto every table any guard reads — including the RI restrict path's two prongs — before any consumer existed. The pivot: reservations are inherently ephemeral, so they live **in memory for their TTL**, not in any engine's storage.
- **Part 1 delivered:** `docs/proposals/dxp-reservation-cache.md` — the Claim model, the Cache contract, the four-verb Participant contract, and the serialisation rule that preserves guard locality (cache mutations only under the tenant's write exclusion, making cache + tables one serialisation domain).
- **Parts 2–3 delivered (same document):** the crash-abandon design — §11's enumeration proving v1 can have no half-written effects, the `abandoned` terminal state, the idempotent mount-time tombstone pass with its fsck-style invariant assertion (`abandoned-dirty` + XOLU-DXP010 on the impossible case), and the two zero-cost doors left open for cross-tenant recovery — and the complete error taxonomy: XOLU-DXP007–010 staked, with 003/005/007/009 partitioning "your transaction did not happen" into drift / clock / competitor / crash. **The design phase of this item is complete**; what remains is implementation (items 19–21 build against the closed spec) and the two decisions deliberately parked for implementation contact: eager-vs-lazy invalidation and the OpParams/Execute-handle shapes (proposal §10).
- **Decisions recorded (2026-07-22, the team):**
  - **Crash means abandon, never resume** — plus tombstoning of any half-written remains, in the manner of filesystem journal cleanup on mount. A non-terminal instance found at startup is released, and startup verifies only tombstonable remains exist.
  - **The reservation cache is a substrate-level facility**, defined by an interface **every participant primitive must honour** to engage in dxp transactions — consulted by the primitive's own guard on every write path, not only by the coordinator. Otherwise a normal HTTP-path write could spend what a reservation holds.
  - **Invalidation-by-loss gets its own error code.** Losing because a competing transaction won is a different fact from a reservation lapsing; XOLU-DXP003 as specified conflates them. Either DXP003 gains sub-specificity or the failure modes segregate into finer codes (DXP007+). Decide at coordinator build (item 21).
- **What survives from pkg/reserved unchanged:** the weights and their admission semantics, GuardPredicate/PredicateFor, the visibility taxonomy, deadline authority as a principle, and the "guards never use accelerators" doctrine. **What the pivot supersedes for dxp's use:** persisted tentative rows as the reservation medium, and therefore the sweeper's role for dxp reservations (self-cleaning by TTL in memory). bal's journal state column remains valid as bal's own device.
- **Open (deliberately undecided):** eager versus lazy invalidation bookkeeping; whether persisted tentative rows remain available as an opt-in for participants wanting reservations visible to *other processes* (nolu territory per @D08 — likely not v1).
- **Parked (the team, same session):** cross-tenant same-process dxp — token-bearing transactions mounting remote /dxp/txn objects, e.g. a purchase logged in one tenant updating a bal ledger in another tenant of the same instance. Was "after item 18" — item 18 is now done (T-54's own core), so the stated trigger condition is met; still deliberately not pulled forward, per the 2026-07-29 decision recorded in docs/SUBSTRATE_DEVELOPMENT_PLAN.md §6 (Deferred) — cross-tenant dxp is exactly the kind of need that would pull 2PS and the wider phase-spectrum (3PS+quorum) back into scope.
- **Reconciliation note (2026-08-01):** the sketch above is superseded, not duplicated, by `docs/proposals/far-and-dxp-mxn.md` — this parked scope (cross-tenant, same instance) is that document's own special case: `/dxp/mxn` generalises to cross-tenant *and* cross-instance, with cross-tenant-same-instance being the case where the remote instance identifier resolves back to this instance (far-and-dxp-mxn.md §1). Do not implement this sketch on its own terms if that work is ever picked up — build against the newer document instead. Implementation of either is not currently scheduled: sequenced behind Pablo's CMMS teams reaching productivity on xolu, `/loc` and `/obj` shipping, and nolu's own development resuming (see `SUBSTRATE_DEVELOPMENT_PLAN.md` §6's matching dated note).
- **Implementation progress (this session):** pkg/dxp built and tested — Claim, the Cache/Participant interfaces, MemCache (per-tenant Lock/Unlock exclusion, deadline-authoritative ClaimsFor), and Janitor. OpParams decided: typed-per-primitive behind a one-method marker interface. XOLU-DXP007-010 reserved in pkg/errors. Item 19's bal half is built and tested: pkg/bal/dxp_adapter.go (Adapter, TransferParams), plus Store.Transfer now folds live pessimistic claims into its guarded UPDATE on both legs (nil cache = exact pre-dxp behaviour, verified by the full pre-existing bal suite staying green under -race). Store.transferInTx extracted so a future coordinator can drive the guarded core against its own shared tx. Load-bearing cross-path test added and green: an ordinary (non-dxp) Transfer is correctly refused the amount a live dxp hold is claiming. cal, entity, and fsm adapters not started; cal specifically found to need more design work than assumed (see below).
- **cal adapter, scoped (this session):** the composed-commitment proposals claim that cal's propose/confirm is shipped and TTL-native does not hold under inspection — Booking (pkg/cal/booking.go) has no deadline/TTL field at all. cal's two-plane (binding/proposed) admission model is real and mature, but adapting it means either a schema change or accepting that a lapsed dxp claim does not auto-clean-up the underlying cal proposed row — a real half-written-state risk T-54's crash-abandon design exists to avoid. Concluded lower priority for shortest path than bal; entity (first-ever adopter of pkg/reserved, zero adopters confirmed tree-wide) and fsm (needs FsmWalkInTx split into evaluate/commit phases, no existing CAS) are comparably or more work. bal chosen as item 19's first half on this basis.
- **Filed in passing:** T-57 (dxp.Participant has no post-commit verb for derived-plane work) — discovered because bal's Execute cannot safely emit rollup deltas from inside a coordinator-owned shared transaction it does not commit.
- **Multi-participant composition proved (this session, pkg/dxp/integration/):** the question of how much of a real /dxp transaction is testable without the coordinator (item 21, not built) has a concrete, verified answer now. Three independently-built adapters (bal, fsm, entity) hand-wired the way a coordinator eventually would -- Reserve on each, one shared *sql.Tx across all three, Execute on each against it, one commit -- correctly compose: TestMultiParticipant_HotelStyle_AtomicCommit proves a full three-participant commit (bal debit, fsm transition, entity update, all atomic from one commit). TestMultiParticipant_PartialFailure_NothingCommits proves the other half: when the third participant's Execute fails inside the shared tx, rolling back discards the first two participants' already-applied-but-uncommitted effects too -- "nothing records a half-booking" verified mechanically, not just asserted by design. The seam that makes one shared file possible: bal.NewStore takes an external *sql.DB rather than opening its own, so it can share SQLiteStore's connection directly.
- **Gap found and left unfixed, documented in the test itself:** bal keys its dxp tenant shard by its own table-prefix string ("t0000_"); fsm and entity key theirs by dxpTenantKey(tenantID) ("%04X" hex, e.g. "0000"). Nothing today reconciles the two -- each primitive is independently correct (own ordinary path and own adapter agree on its own convention), but there is no unified "every live claim for tenant X, across every primitive" view. Doesn't block anything built so far; would matter for a future coordinator wanting cross-primitive claim observability, or for item 21's own tenant-key convention design.
- **Implementation progress, fsm half (this session):** pkg/storage/fsm_walk.go's monolithic FsmWalkInTx split into fsmResolveInTx (read-only: snapshot load, terminal check, transition lookup, guard eval -- no write, satisfying T-54's memory-only-reservation rule) and fsmApplyTransitionInTx (set-clause eval including NEXT VALUE FOR, then a CAS write on state -- fsm's first-ever compare-and-swap; it had none before this split, contrary to the composed-commitment proposal's claim of reusing "the T-34 SetStateFrom mechanism verbatim," which does not exist for fsm's own machine table). FsmWalkInTx itself is now a thin wrapper: claims-aware (refuses on a live PESSIMISTIC dxp hold, XOLU-FSM004) then resolve-then-apply. Zero regression: all 58 pre-existing fsm tests in pkg/server still green, both before and after, exact count matched.
- **FsmAdapter built and tested (pkg/storage/fsm_dxp_adapter.go):** Reserve/Validate/Execute/Release. Admission rule made precise where the proposal left it unstated for mixed weights on one machine: a live PESSIMISTIC claim refuses any new reservation of either weight (exclusive); a live OPTIMISTIC claim only refuses a new PESSIMISTIC one; OPTIMISTIC siblings coexist freely. Execute re-resolves fresh against the coordinator-supplied tx rather than trusting the Reserve-time snapshot, so the CAS in fsmApplyTransitionInTx is the actual enforcement, not a formality. 11 tests green, including the CAS-refusal proof (a machine moved out from under a held claim is correctly refused) and both cross-path guarantees (an ordinary walk is refused by a live pessimistic hold; unaffected by an optimistic one).
- **Tenant-key note:** fsm's dxp tenant key is 4-digit uppercase hex derived from tenantID uint16 (dxpFsmTenantKey), distinct in format from bal's raw table-prefix string -- deliberately so; each primitive is internally consistent between its own ordinary write path and its own adapter, which is what correctness requires, and dxp.Cache treats the tenant string as opaque regardless. Reserve cross-checks the passed tenant string against dxpFsmTenantKey(TenantID) and refuses a mismatch.
- **Implementation progress, entity half (this session):** entity turned out simpler than the earlier session's "first-ever pkg/reserved adopter, highest risk" assessment -- that assessment was based on a misconception (reusing pkg/reserved's persisted convention) corrected while building bal and fsm: dxp Reserve never writes through a primitive's own persisted tentative-row mechanism, it is always memory-only via dxp.Cache, so entity never needed pkg/reserved's convention at all. What entity already had -- saveInTx, a version-CAS-capable core already externally-transactable for the /commit path -- meant no resolve/apply split was needed, unlike bal and fsm. pkg/storage/entity_dxp_adapter.go: EntityAdapter (Reserve/Validate/Execute/Release) over EntityUpdateParams, v1 scoped to UPDATE only (entity must already exist; CREATE is future work, matching the v1-scoping precedent bal and fsm both set). Same mixed-weight admission rule as fsm. 8 tests green including both cross-path guarantees (ordinary Save refused by a live pessimistic hold via ErrConflict; unaffected by an optimistic one).
- **Bug caught by the tests, same class as bal's self-counting mistake:** the claims check was first wired inside saveInTx itself, which EntityAdapter.Execute also calls directly to apply its own already-held claim -- Execute immediately failed, refusing its own claim as a conflict with itself. Fixed by moving the check into saveInTx's ordinary-path CALLERS (saveInner, commitInner) instead, mirroring fsm's FsmWalkInTx/fsmResolveInTx split: the gate belongs in the wrapper the ordinary path uses, never in the core the dxp-authorised path calls directly. Caught by TestEntityAdapter_Execute_AppliesUpdateAndClearsPending failing with "version conflict" before anything shipped.
- **dxpTenantKey generalised:** renamed from dxpFsmTenantKey (fsm and entity share one SQLiteStore and one dxpClaims field; the tenant-key derivation was never fsm-specific logic, just fsm-specific naming).
- **Item 19 status: 4 of 4 participants done (bal, fsm, entity, cal) — item 19 complete.** cal was the hardest of the four for a reason distinct from the earlier (mistaken) TTL concern: cal's resource is a (calendar, time-span) pair, and conflict is interval overlap, not exact-match identity — dxp.Cache.ClaimsFor is exact-match only (primitive+resource string equality), no interval-matching semantics, unlike bal's arithmetic sum or fsm/entity's simple existence/CAS checks. Resolved via day-or-finer-granularity resource keys (coarser than cal's own 5-minute-quantum bitmap, a documented approximation) rather than a bespoke overlap structure — full detail, including two real deadlocks found and fixed along the way, in T-82.

## chronicle

### T-55. chronicle temporal policy: per-timeline backdating configuration, one vocabulary across /ts, /cal, /bal

Theme: chronicle · Priority: P1 · Status: ◐ · Blocks/after: subsumed T-51's fix shape (closed 0.16.24); subsumes T-52's policy-storage question

- **Trigger:** T-51 policy decision (the team, 2026-07-22): default is accounting-style never-backdating; backdating is opt-in per timeline (museum records, wikipedia-style timelines, out-of-order fact arrival). Operative condition: one consistent per-timeline configuration system for chronicle-based timelines across the three primitives.
- **Doctrine constraint (@C04c):** the policy is read by guards on every write, therefore it lives in each primitive's own guard plane (a column on the timeline/calendar/account-set row, read in the governing transaction) — never in /meta. This generalises T-52's finding across all three primitives.
- **Shape:** shared vocabulary in pkg/chronicle — `append_only` (default; entries dated at-or-before the timeline's latest entry refused with the primitive's own error) | `backdated` (any arrival order; checkpoint invalidation active, lazy: mark stale, recompute on read). Declared at creation; widening always legal; narrowing deferred (requires proving the past monotonic). Sealing (@B07) remains an orthogonal bal axis composing with append_only.
- **Unconditional regardless of policy:** the T-51 oracle prong — every checkpoint verified as `checkpoint.balance == SUM(journal WHERE at <= checkpoint.at)` — runs under both policies; it catches bugs, not policy violations.
- **Scope:** policy type + shared enforcement helper in pkg/chronicle; guard-plane column and admission check in each primitive; T-51's invalidate/recompute wired for backdated timelines only. ~0.5d for the shared system; T-51 fix builds on it.
- **Delivered (0.16.24):** the shared vocabulary (`pkg/chronicle/policy.go`: TemporalPolicy, ErrBackdatedRefused sentinel, CheckAdmission reference function, CanTransition) and the full bal wiring — guard-plane `temporal_policy` column with legacy detection, the admission predicate folded into both Transfer leg UPDATEs (write-first doctrine preserved), XOLU-BAL006 via BackdatedError unwrapping to the sentinel, in-transaction stale-marking of invalidated checkpoints, stale-skipping as-of reads, Checkpoint-clears-stale recompute, and the VerifyCheckpoints oracle prong. **Recorded deviation from the filed wording:** same-instant entries are ADMITTED under append_only (refusal is strictly-earlier only) — batches and bal's own two-leg writes legitimately share timestamps; asserted by test so it cannot silently narrow. A design consequence worth noting: the checkpoints table moved from InitRollup to core Init, because Transfer's write path now maintains checkpoint staleness in-transaction — the journal plane owns the table's lifecycle.
- **Remaining, re-scoped after recon (2026-07-22):** /cal wiring is feasible as designed — SQLiteBookingSource calendars have an authoritative row for the policy column — but the admission INSTANT is ambiguous (booking start time vs creation time) and needs a ruling. /ts is BLOCKED on two findings: (a) no per-timeline guard-plane row exists (points live in Pebble keyed by timeline id; policy storage requires a timeline registry that does not exist), and (b) ts ingestion normally tolerates late-arriving points, so an append_only default would drop every delayed telemetry packet — suggesting ts wants `backdated` as its own default, which cuts against the accounting-default rule as stated and therefore needs the team's explicit ruling on per-primitive defaults before any wiring.
- **Not in scope:** narrowing transitions; per-entry policy overrides; retroactive policy migration tooling.
- **Vocabulary note (2026-08-15, terminology only, no scope change):** SQL:2011's bitemporal vocabulary names this item's policy enum exactly — **system time** (when the record knew) vs **application/valid time** (when it was true in the world). `append_only` is "valid time must track system time"; `backdated` is "valid time runs free of system time." Independent confirmation of the recorded same-instant deviation: SQL Server's temporal tables stamp periods with the *transaction start time*, so batch rows legitimately share instants — a second system converging on same-instant admissibility under append_only. See docs/proposals/cron-primitive.md §13.8.


### T-66. chronicle.Engine.Append has no locking (lost-update race on concurrent same-bucket appends) -- currently masked for bal by SQL commit latency, but the fix belongs in chronicle itself before ts migrates onto it

Theme: chronicle · Priority: P2 · Status: ☐ · Blocks/after: After: none. Related: T-62 (where this was found, not introduced). Blocks: nothing directly, but should be resolved before ts's wave-3-intended migration onto chronicle.Engine.

- **Trigger:** direct question before considering T-62/63/64/65 release-ready -- "do we need any more adversarial tests?" Checking rather than assuming turned up a real, previously-unexamined gap in shared infrastructure, not in bal's own code.
- **The finding:** chronicle.Engine.Append (pkg/chronicle/engine.go) does a plain read-modify-write with zero synchronization -- `cur, ok := e.s.Get(k); ... e.s.Put(k, e.m.Combine(cur, v))`, no mutex, no atomic operation, no locking of any kind. Two goroutines appending to the same bucket key concurrently can both read the same starting value, both compute independently, and the second write silently loses the first's contribution. A classic lost-update race, confirmed by reading the code directly, not inferred.
- **Not new, not a T-62 regression:** the same unlocked Append existed when bal's rollup was SQL-backed (pre-T-62) -- the storage engine changed, the missing lock in Append itself did not. Checked directly: bal is currently the ONLY production consumer of chronicle.Engine (grep for chronicle.NewEngine/chronicle.Engine[ across the whole tree returns exactly one file, pkg/bal/rollup.go). ts has not migrated onto it despite SUBSTRATE_DEVELOPMENT_PLAN.md wave 3 describing that intent ("ts's own aggregates become its first instantiation") -- that migration has not happened yet, which is exactly why this matters as a chronicle-level ticket now, before it does.
- **Confirmed hard to trigger under bal's actual call pattern, not just theorised as a real risk:** built and ran a probe test -- 32 concurrent transfers on one account (the same load TestBalAdmission_Race already creates), checked RollupOracle after each run, repeated 10 times. Zero divergence observed. Not because the code is safe -- confirmed no Go-level lock exists anywhere around Store.Transfer or EmitDeltas -- but because SQL's own commit latency (WAL fsync, single-writer serialization) naturally spaces out when each goroutine reaches the Pebble-level Append call, keeping the actual race window (microsecond-scale, in-memory) rarely hit in practice. This is a genuine mitigating factor for bal's CURRENT call pattern specifically, not a proof of safety for the primitive in general -- a consumer with a tighter concurrent-append pattern (no SQL-latency spacing) could hit this far more easily.
- **Why this is scoped as a chronicle ticket, not a bal one:** the fix belongs in chronicle.Engine itself (the read-modify-write needs to become atomic, or Append needs an explicit lock/CAS discipline of its own), not in any one consumer working around it. Fixing it once in chronicle protects every current and future consumer; patching around it in bal specifically would leave the same gap open for ts's eventual migration and any other future adopter.
- **Scope of investigation needed (not yet done):**
  1. Audit every chronicle.Engine method for the same class of issue, not just Append -- Invalidate (pkg/chronicle/engine.go) does a comparable unlocked per-level Delete loop; worth checking whether concurrent Invalidate-vs-Append or Invalidate-vs-Invalidate has its own hazard, even though Delete alone isn't a read-modify-write in the same shape.
  2. Decide the right locking granularity: a single mutex per Engine instance (simple, correct, but serializes ALL appends to one account's cascade, including across different bucket levels that don't actually conflict) versus per-bucket-key locking (finer-grained, more complex, matches the "prefer per-account not per-tenant" granularity reasoning already recorded for bal's own SQL guard in docs/KNOWN_ISSUES.md).
  3. Confirm whether BucketStore implementations (SQL, Pebble) have any implicit serialization of their own that could inform the fix, or whether the fix must assume none (the honest current state, per this investigation).
  4. Once ts migrates onto chronicle.Engine (wave 3's stated intent, not yet executed), confirm its own call pattern doesn't have the SQL-latency-spacing mitigation bal currently benefits from -- ts's writes may be tighter and higher-frequency, making this a more live risk there than the probe results suggest for bal today.
- **Not pursued now, deliberately:** this session's work stops at investigation-and-filing. No fix attempted, no lock added -- a locking design for shared, multi-consumer infrastructure deserves its own deliberate pass, not a rushed patch appended to an unrelated release.

## tooling

### T-56. migrate xolu scripts to repoman consumers (github.com/ha1tch/repoman)

Theme: tooling · Priority: P3 · Status: ☐

- **Trigger:** repoman v0.1.0 extracted from xolu's scripts (2026-07-22): ed/roles adopted verbatim, register/guards/syncver generalised onto .repoman.json config, release orchestration split into a portable core (relcore.py, manifest-driven) with archive/syncver builtins. Eighteen-check selftest green.
- **Repoman has grown substantially since this item was filed, same day, single session (2026-08-17): v0.1.0 -> v0.5.0.** New: full generalised wave management (wave_progress.py/add_wave.py, ported FROM this project's own scripts of the same name), a config-driven id-format generalisation matching this project's own T-/XOT- dual-format need, guard_id_prefix generalisation, a fixed syncver.check()/check_detail() API split (a real footgun found and fixed entirely within repoman's own, separately-designed syncver.py -- see below, this never affected this project's own syncver.py, which is unrelated code), str_replace_extended.py ported and extended with JavaScript/TypeScript/CSS/HTML support (this project's own copy predates and lacks all four), and doctor.py, a new environment-diagnostic tool. 43-check outer selftest, plus str_replace_extended.py's own 24-path and ed.py's own 9-path embedded selftests.
- **Corrected 2026-08-17: this item's own prior text overstated repoman's current usability against this tree.** The claim "repoman's register/guards/syncver operate on this tree TODAY" was checked directly, not assumed, and found false for two of the three:
  - **register.py: does NOT work against this tree as configured today, and fails SILENTLY, not loudly.** Pointing repoman's own (config-driven, generalised) register.py at this project's real, unmodified .repoman.json returns 0 open items, then 9 (against a real 58), with no error -- a materially worse failure mode than refusing to run, since a caller trusting the result would be silently wrong. Root cause, now precisely known rather than guessed: this project's own .repoman.json sets only id_prefix; repoman's own register.py additionally needs id_separator (empty string, not the default -, since this project's XOT ids carry no separator) and legacy_id_prefix/legacy_id_separator ("T"/"-", for the frozen pre-XOT id sequence that is still most of this project's own open items). Verified: adding exactly these three keys to a scratch copy of .repoman.json makes repoman's own register.py agree byte-for-byte with this project's own scripts/register.py (58 open items both ways).
  - **guards.py: this part of the claim is TRUE, checked and confirmed.** Repoman's own guards.py, pointed at this tree with zero extra config, produces output identical to this project's own scripts/guards.py -- this project's guard ids already use the default G- prefix repoman assumes.
  - **syncver.py: the claim doesn't meaningfully apply.** This project's own scripts/syncver.py is not a repoman-descended tool at all -- its own module docstring says so directly ("Direct port of syncver.sh"), and it was never intended to be swapped for repoman's version. The two tools share a name and a similar purpose, nothing else; this project's own check() already returns a plain bool and always has, unrelated to repoman's own, separate fix to its own check() (see repoman's own CHANGELOG, 0.3.0).
  - **wave_progress.py/add_wave.py: this item's own original text does not mention these at all -- they did not exist in repoman when this item was filed.** They now do (repoman 0.2.0), ported FROM this project's own scripts of the same name. Checked the same way: repoman's own wave_progress.py, pointed at this tree, agrees with this project's own copy once (and only once) wave_tracking, wave_plan, wave_short_names, and wave_themes are also supplied in .repoman.json -- none of which this project's own config currently carries, since this project's own scripts hardcode all of it directly rather than reading the config file at all.
- **The underlying reason all of the above needed checking rather than trusting the original claim: this project's own scripts/ directory was never actually built on repoman's config.py system.** Every tool here -- register.py, guards.py, syncver.py, wave_progress.py, add_wave.py -- hardcodes its own paths and values directly (.repoman.json's own _comment field says as much for the id prefix specifically: "This file exists as a discoverable, documented anchor point, not a mechanism register.py actually reads"). This project's tools share DESIGN LINEAGE with repoman's, not ARCHITECTURE. A genuine migration is a bigger undertaking than "replace scripts/{ed,roles,register,guards,syncver}.py with thin wrappers" suggested when this item was first filed -- it means either adopting repoman's config.py system properly (config.py itself, plus the seven keys named above added to .repoman.json) or keeping this project's own call sites and swapping only the underlying implementation behind them.
- **Scope, revised:** replace scripts/{ed,roles,register,guards,syncver,wave_progress,add_wave}.py with thin wrappers over (or direct copies of) the repoman versions, PLUS add config.py and populate .repoman.json with the seven keys now precisely known (above) -- not previously scoped, since their need was only established by this correction. Express release.py's xolu-specific steps (make targets, sharded go-test via testrun.py, c04dcheck, gate, generators) as either a relcore manifest plus local step commands, or keep release.py and have it import relcore's Journal/archive.
- **Known, accepted divergence, not urgent for this project specifically:** this project's own scripts/str_replace_extended.py/scripts/roles.py predate and lack repoman's own JavaScript/TypeScript/CSS/HTML support (repoman 0.4.0) -- low cost today, since this project is Go-only, but a real gap between the two copies, named here rather than left silent.
- **A separate, low-risk fix (not part of the migration question) backported the same day this correction was written:** scripts/add_wave.py's own insert_tracking_section depended on a hardcoded "## 3." section-number anchor in SUBSTRATE_TRACKING.md -- fragile (would break silently if that document's own numbering ever changed), fixed in repoman first (0.2.0) by finding the last existing wave section and inserting after it instead, no anchor needed. Backported here as XOT206 -- a pure robustness fix, no architectural entanglement, unlike everything else in this item.
- **Not in scope:** c04dcheck (repo-local by nature), TESTING.md/badge generators, gate pins.
- **Strategic note, 2026-08-17 (direct instruction, recorded not acted on):** as soon as repoman genuinely achieves parity-or-better with this project's own ad hoc tooling, this project's own copies should likely be replaced with repoman's entirely, rather than continuing to hand-port individual features both directions indefinitely. XOT207 (wave visibility, same date) is a concrete, live instance of exactly the maintenance cost this note is about: a feature built in repoman first, then manually re-implemented here with a deliberately different persistence mechanism, because this project's own architecture (hardcoded paths and values, no config.py) doesn't yet support repoman's own approach directly. The precise gap between "repoman could operate on this tree" and "actually does, natively" is now known exactly (see this item's own 2026-08-17 correction, above) rather than estimated -- three config keys for register.py, four for wave_progress.py/add_wave.py, zero for guards.py. Not a trigger to migrate today; a standing reminder that the case for eventual migration gets stronger with every feature added twice.
- **Update, same day, 2026-08-17 (XOT208): most of the migration this note anticipated has already happened.** Following a direct instruction to move on the readiness assessment above rather than wait for every piece to be ready, this project's own scripts/{ed,roles,register,guards,syncver,wave_progress,add_wave,str_replace_extended}.py were swapped for repoman's own generalised versions -- config.py and doctor.py added alongside them -- with every swap verified against real, current project data before landing, and the whole set proven together through one real, complete release.py run (v0.30.30), not a synthetic test. release.py itself remains this project's own, deliberately -- the one piece the readiness assessment named as genuinely not ready, and still true: its 12-step pipeline has no equivalent in relcore.py's own two generic builtins. This item's own original Scope (above) is accordingly narrowed to release.py alone; everything else it named is done.


### T-106. two small release/version tooling gaps found while running the actual 0.20.0 release: release.py's zip exclusions didn't cover .pyc/__pycache__ (fixed), syncver.py's bump-minor doesn't respect --help and executes instead (found, not fixed)

Theme: tooling · Priority: P4 · Status: ◐ · Blocks/after: After: none. Neither touches xolu's own shipped code -- both are about the tooling used to ship it.

- **Trigger:** running the actual 0.20.0 release (not a quick checkpoint) surfaced two small, real gaps in the release/version tooling itself, neither previously exercised in a way that would have caught them.
- **Gap 1, found and fixed: `release.py`'s zip/clean exclusion patterns didn't cover Python bytecode cache.** `ZIP_EXCLUDE`, `CONTAMINATION_RE`, and `CLEAN_PATTERNS` all omitted `*.pyc`/`__pycache__` entirely -- the first real release run this session produced a zip containing 6 stale `.pyc` files from `scripts/__pycache__/`, silently passed the zip step's own contamination scan since that scan's regex simply didn't know about this pattern. Fixed: `*.pyc`, `*.pyo`, and `__pycache__/*` added to all three lists. Verified: re-ran `clean`+`zip` (via `--resume`, which correctly skipped the already-green build/test/lint/c04dcheck steps) and confirmed zero `.pyc`/`__pycache__` entries in the regenerated archive (669 files, down from 675).
- **Gap 2, found, not fixed -- flagged for whoever next touches `syncver.py`'s CLI:** `python3 scripts/syncver.py bump-minor --help` does not show help text and exit; it executes the real bump. Confirmed directly: `VERSION` and `pkg/version/version.go` both changed from `0.19.3` to `0.20.0` as a direct result of what was meant to be a help-text probe, before any other release work had begun. The outcome happened to be the correct target version for this release, so no revert was needed and nothing else was touched by the accidental run -- but the underlying CLI defect is real: whatever argument-parsing path handles `bump-minor` (and likely its siblings, not individually checked) doesn't special-case `--help` the way the top-level `syncver.py --help` and `release.py --help` both correctly do. Worth checking whether this is an argparse subcommand configuration gap (a subparser missing its own `add_help`) or something more specific to the bump-* commands. Not attempted here -- this session's own working agreement is explicit that a mid-release-process moment is not the time to be experimentally probing CLI tools further.
- **Both findings are about the tooling used to ship this exact release, not about xolu's own shipped code** -- filed as their own item rather than folded into T-105 or any other dxp-scoped item, since neither has anything to do with dxp.
- Left at ◐ (gap 1 fixed and verified; gap 2 genuinely still open, not closed) -- ready to formally close gap 1's portion whenever gap 2 is also addressed, or split if that's preferred later.

### XOT196. New repoman-descended tool, scripts/str_replace_extended.py: format-aware, journaled, base64-payload substitution -- built to retire the failure class that direct str_replace/sed/awk substitution invites (shell-quoting corruption, edits landing without a syntax check), following a real gap found and fixed during the tool's own build (a balanced escape-and-reenter injection the first delimiter check missed)

Theme: tooling · Priority: P2 · Status: ◐ · Blocks/after: After: none. Pending: upstream sync to github.com/ha1tch/repoman (T-56), repoman's own selftest.py extension, Horacio applying the proposed working-agreement and skill amendments.

- **Trigger:** direct request, following two edit mistakes this session (a shell-quoting corruption of a comment's own escape sequences; a duplicated line from an overlapping replacement span) plus the project's own longer-standing recorded incidents in the same class (the `` `+expr+` `` inside `"..."` corruption pattern named in the working agreement's own §7.4). Requested design: a `str_replace`-shaped tool whose search/replace content travels as base64-encoded JSON, never as a CLI argument; format-aware; generates collision-proof markers; and something Claude is actually steered toward using instead of the built-in `str_replace`, `sed`, or `awk`.
- **Design proposed first, approved, then implemented exactly as proposed** -- payload schema, refusal classes, per-format syntax validators, and the enforcement-layer question (a tool cannot be un-installed from the harness; steering has to make the right tool the reflexively cheap one, not attempt to remove the wrong ones) were all laid out and agreed before any code was written.
- **`scripts/roles.py` extended**, not forked: python/json/yaml/shell classifiers added alongside the existing go/md ones, wired into the same `classify()` dispatch `ed.py` already depends on -- one classifier, two consumers, no drift between them. Each new classifier's own docstring documents its known heuristic limitations honestly (YAML block scalars, shell heredocs, Python f-string interiors) rather than silently misclassifying them without a record.
- **`scripts/str_replace_extended.py` built**: JSON payload (stdin or file) with base64-encoded UTF-8 search/replace content; mandatory `expect` count and `roles` assertion (no silent defaults); Unicode-normalization-mismatch detection distinct from a genuine zero-match; a two-part delimiter-integrity precheck (below); per-format post-apply syntax validation (`gofmt -e`, `ast.parse`, `json.loads`, a YAML/Markdown structural lint, `bash -n`) with automatic rollback on failure; atomic multi-op/multi-file batching by default; collision-checked marker generation (`⟦RM:...⟧`, U+27E6/U+27E7) with a sweep op; and journal integration through `ed.py`'s own WAL (`ed.record`/`ed.load_journal`) -- one undo history for both tools, not two.
- **A real design gap found and fixed during the tool's own build, not assumed correct:** the first delimiter-integrity implementation only sampled syntactic role *after* a replacement, comparing original-vs-new at increasing offsets. Selftest case 5 (`hello" + injected + "` replacing `hello` inside a Go `"..."` string) exposed that this misses a *balanced* escape-and-reenter -- the injected content opens and recloses the string with an even number of quotes, so parity (and therefore role) at every point *after* the replacement matches perfectly even though unescaped code was injected *inside* it. Traced precisely (a standalone script printing the role trajectory character-by-character through the replacement's own span, not guessed at) before fixing. Fixed with a second check: when the edit starts inside a content role (string/comment, any format), every offset within the replacement's own interior must classify as that same role throughout -- any deviation refuses. Verified the fix doesn't over-correct into false positives: a companion selftest case confirms inserting a complete, self-balanced string literal into ordinary code is *not* refused, since that check only applies when the edit starts inside a content role to begin with.
- **18 selftest paths, all green**, covering: basic sub, count-mismatch, missing-expect/missing-roles, role-mismatch with real census, the delimiter-break case above (both the interior and the after-tail variant), syntax-check-failure-triggers-rollback, dry-run leaves disk untouched, marker generation/uniqueness/sweep, normalization-mismatch detection (without ever silently auto-normalizing), all four new format classifiers each producing a clean sub plus a passing validator, `ed.py undo` reverting a `str_replace_extended`-made edit, an atomic batch rolling back an already-staged change in the same file when a later op in the same batch fails its syntax check, the false-positive guard above, and a direct regression test for the original session incident (tabs/newlines/apostrophes landing byte-exact, confirmed by asserting no literal backslash-n or backslash-t sequence appears on disk).
- **Dogfooded on a real edit, not just synthetic selftest cases:** used to add its own row to `docs/TOOLING.md`'s file-inventory table, authored via the documented library path (`from str_replace_extended import apply_payload`, content as ordinary Python string literals, base64-encoded by the driver itself -- no shell one-liner at any point). Confirmed in `ed.py log` as a normal journaled transaction alongside every other edit this session.
- **Enforcement is procedural, not structural** -- the tool cannot be removed from the harness, so three layers were used to make the correct tool the reflexively easy one: (1) a memory edit steering future sessions toward it and away from the built-in `str_replace`/`sed`/`awk` with no exception; (2) a proposed working-agreement amendment (Part 1 §6.2/§6.3's tool-precedence ladder), presented to Horacio as exact replacement text since Claude cannot edit `userPreferences` directly; (3) a proposed `repoman-tools` skill update with the same content, presented as a deliverable file since `/mnt/skills/user/` is read-only to this session. None of the three claim to make bypass impossible -- only procedurally discouraged and, for (1), immediately actionable.
- **Not yet done:** upstreaming to `github.com/ha1tch/repoman` itself -- built and verified in xolu's own in-repo copy first (this repository's own scripts are authoritative pending T-56's migration), matching how every other repoman-descended tool in this tree has been introduced. Also not done: extending repoman's own top-level `selftest.py` (18 checks) with these new paths, since that file lives in the separate repoman repository, not this one.

### XOT215. roles.py's Go block-comment detection (_go_role) is a naive substring count of '/*'/'*/' across the whole file, not comment/string-aware -- a literal '/*' inside an ordinary // line comment (e.g. a URL pattern like /api/v2/cal/*) is misread as an unclosed block comment, misclassifying every line after it as go-comment for the rest of the file

Theme: tooling · Priority: P3 · Status: ☐ · Blocks/after: After: none.

- **Trigger:** found live while editing `pkg/server/server.go` for XOT213 (wave 15) -- `str_replace_extended.py` refused a plainly-code anchor (`tr.Get("/blob/export/{ticket}", ...)`) with role-mismatch, reporting `go-comment`. Traced directly to `scripts/roles.py`'s own `_go_role`: line 826's `// /api/v2/cal/*; the manager owns...` contains the literal two-character substring `/*` (from `cal/*`) inside an ordinary `//` comment -- `before.count("/*") > before.count("*/")` then reports every subsequent offset in the file as inside an unclosed block comment, confirmed directly by checking `func (s *Server) setupRoutes()` (line 918, unambiguous code) also misclassified as `go-comment`.
- **Root cause, precisely:** the block-comment fallback counts raw substring occurrences of `/*`/`*/` anywhere in the text, without excluding occurrences inside `//` line comments or string literals (double-quoted or backtick) -- unlike the rest of `_go_role`, which does correctly strip strings before checking for `//`.
- **Scope of the real fix (not done here):** `_strip_go_strings` already exists and does the right thing for the `//` check just above it in the same function -- the block-comment counting pass needs the equivalent treatment, additionally stripping `//`-comment tails (to end of each line) before counting `/*`/`*/`, not just quoted strings.
- **Worked around for this session's own edit** via `ed.py` (span-based, no role dependency) rather than blocked on this fix.
- Not started -- found and filed, not yet fixed in either repoman (the canonical source) or xolu's own migrated copy (XOT208).

## iolu

### T-68. iolu tenant provision-cal: provisions/rebuilds cal's Pebble occupancy index offline -- a real gap found while scoping item 24, but NOT item 24 itself (see T-69 for that correction)

Theme: iolu · Priority: P3 · Status: ◐ · Blocks/after: After: none. Related: T-69 (item 24's actual scope, cal CRUD, still open).

- **Trigger:** wave 6 start ("First E, then C" -- bal client library, then wave 6 iolu operations). Item 24's one-line plan summary ("iolu: cal provisioning") was interpreted as "give iolu a way to provision cal's Pebble occupancy index" -- a real, genuine gap, checked and confirmed directly (cal.Manager.assemble is the ONLY place the index ever gets created/rebuilt today, and only lazily on a running server's first request).
- **Correction, found by reading docs/proposals/iolu-operations-roadmap.md properly (not done before starting to build):** item 24 as the roadmap actually specifies it is `iolu cal create | list | info | delete` -- full calendar CRUD through the Manager, described there as "the only gap with no workaround" because there is currently no operator path to CREATE a bookable calendar at all without writing Go. This item is NOT that. Filed separately (T-69) with the roadmap's actual scope, left fully open.
- **Two real, unrelated findings during implementation, neither assumed going in:**
  1. `iolu db init` never calls InitV2Schema at all -- checked directly, zero matches in cmd/iolu for that call. V2 tables (bal/cal/fsm) are ONLY ever created lazily by a running server's first per-tenant touch. The first version of this command assumed cal_calendars/cal_bookings already existed and failed immediately against a cold, never-touched tenant -- exactly the scenario an offline provisioning tool most needs to handle. Fixed by calling InitV2Schema idempotently inside the command itself, covering both "server already ran" and "cold tenant" with one code path.
  2. cal.IndexStore's ordinal map is in-memory only and is never restored by Open() alone -- confirmed this isn't a bug by checking cal.Manager.assemble's own doc comment, which states the server calls RebuildFrom on EVERY assembly for exactly this reason ("a fresh process / first-touch correct without a separate warmup"). The first version of this item's own verification test opened a fresh IndexStore and tried to read occupancy directly, which failed the same way any code skipping RebuildFrom would -- the test was wrong, not the command. Fixed the test to mirror the correct usage pattern (RegisterCalendar to restore just the ordinal mapping needed for a read-only verification, without re-deriving data a second time).
- **New file cmd/iolu/calprovision.go:** `iolu tenant provision-cal --base-dir D --name NAME`. Idempotent and safe to re-run -- RebuildFrom always clears and repopulates from the SQL source, matching the server's own every-assembly behaviour exactly. Added a matching "Cal index: provisioned/not provisioned" line to `iolu tenant info` with the same hint-on-missing pattern the existing timeseries line uses, and updated printUsage/the top-level usage doc comment.
- **Verified exhaustively:** manually smoke-tested via the real compiled binary against a real per-file database (not-provisioned -> hint -> provision -> provisioned, directory and size confirmed). New Go test (TestTenantProvisionCal_RebuildsFromRealBookingData) proves the rebuild reflects REAL seeded booking data, not just that an empty directory gets created -- the property that actually matters, found lacking in the test's own first draft and fixed. Full cmd/iolu suite green; full tree build/vet clean; a genuine go test ./... sweep across the whole tree (not just build/vet) -- clean, every package.


### T-69. iolu cal create/list/info/delete (item 24, wave 6, as actually specified): the real no-workaround gap -- no operator path exists to create a bookable calendar without writing Go

Theme: iolu · Priority: P2 · Status: ☐ · Blocks/after: After: none. Leads wave 6 per the roadmap's own sequencing ("the only gap with no workaround"). Related: T-68 (a different, already-shipped gap found while scoping this one).

- **Trigger:** correction found while scoping T-68 -- reading docs/proposals/iolu-operations-roadmap.md's own item 1 description properly rather than working from the development plan's one-line summary alone.
- **Scope, per the roadmap doc precisely:** `iolu cal create --base-dir D --tenant NAME --calendar-id ID [--default-state binding|proposed] [--match-policy optimistic|pessimistic]`, mirroring cal.Calendar's own fields; `list`/`info` read the calendar registry; `delete` refuses while live bookings exist unless `--force`. Uses cal.Manager via the same construction path the server itself uses -- the existing iolu principle (iolu and xolu must always agree on layout and pragmas).
- **Why this is the real "no workaround" gap, not T-68:** calendars are created through cal.Manager only -- deliberately not exposed on the HTTP API, not in the Go client (checked: pkg/client has zero cal-creation methods, only CalCheck/CalOpenings/CalPropose/CalConfirm, all of which operate on an ALREADY-EXISTING calendar). There is currently no operator path to create a bookable calendar at all short of writing Go against pkg/cal directly.
- **Roadmap's own effort estimate:** ~1 day including tests.
- Not started.

### T-70. iolu query + repl (item 25, wave 6): one-shot OQL/sulpher/SQL query execution, then an interactive REPL borrowing aulsql's iaul shape

Theme: iolu · Priority: P2 · Status: ☐ · Blocks/after: After: none. Second in the roadmap's own sequencing, after cal CRUD (T-69).

- **Trigger:** wave 6 planning/grouping pass (docs/proposals/iolu-operations-roadmap.md item 3).
- **Scope, one-shot:** `iolu query --base-dir D --tenant NAME --oql "..."` executes through the pkg/oql executor against the Store interface (backend-neutral by construction -- the logical-core side of the backend split); `--sulpher "..."` for graph queries; `--sql "..."` as a raw passthrough living in the per-backend module, read-only unless `--write`.
- **Scope, interactive:** `iolu repl` borrows aulsql's iaul (interactive aul) shape directly -- chzyer/readline (pure Go, cross-compiles clean), persistent history at ~/.iolu_history with case-insensitive search, dynamic tab completion seeded from the tenant's entity types and OQL keywords, meta-commands (\tenants, \entities, \use <tenant>, help, history, quit), table-formatted output, query timeout. OQL is the default dialect; \sql switches to raw mode with the same read-only guard.
- **Why this is roadmap's own second priority:** "makes every other diagnostic conversation with a deployment cheaper, and the name [interactive xolu] now promises it."
- **Natural split within this item:** query (one-shot) first, since repl's own tab-completion and meta-commands build on the same executor wiring query needs anyway -- repl second, once the underlying execution path is proven.
- **Roadmap's own effort estimate:** ~2-3 days with iaul as the template and the executor already in-tree.
- Not started.

### T-71. iolu db backup (item 26a, wave 6): rescoped 2026-08-17 to wrap pkg/tenantexport's already-shipped ExportTenant, called offline as a library -- ships more complete coverage than the original SQLite-online-backup-API scope, which is split out to XOT203

Theme: iolu · Priority: P2 · Status: ☐ · Blocks/after: After: none. Related: T-72 (verify, unaffected by this rescope), T-73 (check), XOT203 (physical backup, the split-out original scope), XOT204 (restore, both paths).

- **Priority raised P3 -> P2**, 2026-08-17: mostly-reuse work, cheap to ship, no longer the original from-scratch estimate.
- **Trigger:** wave-6 code-reuse review, 2026-08-17. `pkg/tenantexport` (T-149, v0.25.0) already does most of what this item was going to build: `ExportTenant` takes a raw `*sql.DB`, not an HTTP client, so it is directly callable offline, the same established pattern `T-68`'s `calprovision.go` already uses -- no running server required. `blob.NewStore(dir, maxSize)` is likewise directory-based and offline-openable. `iolu tenant list` already solves "enumerate every tenant."
- **Rescoped implementation:** `iolu db backup --base-dir D [--tenant NAME]` opens each tenant's primary `*sql.DB` and blob store directly, calls `tenantexport.ExportTenant` per tenant (defaulting to every tenant `iolu tenant list` returns when `--tenant` is omitted), writing under the *same* `tenantexport.ExportKeyPrefix` key convention the server-side async path already uses. Deliberate consequence: an iolu-triggered backup and a server-triggered `POST /blob/export` become indistinguishable artifacts -- visible through the same `GET /blob/export/{ticket}`-adjacent tooling once the server is back up, and swept by the *same* TTL sweep (`SweepExpiredExports`) without iolu needing to know anything about retention itself.
- **Coverage gain over the original scope, not just a reuse shortcut:** the original text named only cal's Pebble index. `tenantexport` already correctly covers all three per-tenant Pebble stores (`ts`, `cal`, `bal`'s rollup) plus `/loc` and `/obj`'s dedicated files, none of which the original scope mentioned. Building the original scope as written would have shipped *less* coverage than reuse gives for less effort.
- **What tenantexport does NOT give this item, named rather than glossed over:** a byte-exact physical image. `ExportTenant` produces a portable JSON export -- inspectable, tolerant of schema drift, but not a guaranteed-identical restore-by-copy target. That guarantee is real and split out as its own item, XOT203, not silently dropped.
- **Restore, still explicitly out of scope here:** now tracked as its own item, XOT204, rather than a permanent scope-exclusion note with nothing behind it.
- **Estimate: ~0.5 days** (down from the original ~1d implied share of "backup+verify together, ~2 days" -- a thin CLI wrapper around already-tested library code, not new backup logic).
- Not started.

### T-72. iolu db verify (item 26b, wave 6): PRAGMA integrity_check across all databases, confirming SQL-level structural integrity -- distinct from db check's oracle-based semantic checks (T-73)

Theme: iolu · Priority: P3 · Status: ☐ · Blocks/after: After: none. Related: T-71 (backup), T-73 (check, a different kind of verification).

- **Trigger:** wave 6 planning/grouping pass (docs/proposals/iolu-operations-roadmap.md item 4, verify half).
- **Scope, precisely distinguished from T-73 (db check):** `iolu db verify` runs PRAGMA integrity_check across all databases and confirms a backup is self-consistent -- SQL-level STRUCTURAL integrity (page corruption, index consistency). This is NOT the same concern as db check's oracle-based SEMANTIC checks (derived-vs-authoritative state, e.g. cal index-equals-rebuild, graph-versus-store). Corrected an assumption made mid-session before reading the roadmap doc properly: these two were guessed to overlap; they do not.
- **Roadmap's own effort estimate (backup+verify together):** ~2 days, shared with T-71.
- Not started.

### T-73. iolu db check: promote remaining oracles (item 26c, wave 6) -- cal index-equals-rebuild and blob-usage-walk oracles still need wiring in; graph and all three bal oracles already are

Theme: iolu · Priority: P3 · Status: ☐ · Blocks/after: After: none, but directly enabled by T-68 (cal's rebuild path already exists as a callable function). Related: T-71 (backup), T-72 (verify, a different kind of check).

- **Trigger:** wave 6 planning/grouping pass (docs/proposals/iolu-operations-roadmap.md item 5, integrity suite).
- **Substantially already built, not starting from scratch:** iolu db check already exists (cmd/iolu/dbcheck.go) and has been extended multiple times this session -- graph-edges oracle originally, then bal's GlobalFoldOracle/ChainOracle/RollupOracle added as part of T-65's oracle re-scoping work for prefix-collapse. The roadmap's own framing: "the strongest consistency oracles in the project already exist as test code... promoting them to operator tooling is mostly plumbing" -- and a meaningful share of that plumbing is done.
- **What remains, per the roadmap's own list:** cal's index-equals-rebuild equality oracle is NOT yet wired into db check (only graph and bal oracles are today) -- the natural next addition, and directly enabled by T-68's provision-cal work (the rebuild path db check would compare against already exists as a callable function). Blob usage walking is also not yet wired in.
- **Roadmap's own effort estimate:** ~2 days; "highest value-per-effort after item 1, since the hard parts are already written and battle-proven" -- now even more true given this session's bal additions.
- Not started (as its own closable unit) -- but meaningfully de-risked by work already landed under T-65.

### T-74. iolu db rebuild-graph (item 26d, wave 6): explicit offline graph rebuild with a before/after node/edge count report -- the rebuild logic already exists server-side, this is invocation and reporting only

Theme: iolu · Priority: P3 · Status: ☐ · Blocks/after: After: none. Related: T-71/T-72/T-73 -- the fourth and cheapest of the roadmap's recommended operations-hygiene batch.

- **Trigger:** wave 6 planning/grouping pass (docs/proposals/iolu-operations-roadmap.md item 6).
- **Scope:** an explicit offline `iolu db rebuild-graph` with a before/after node/edge count report. The roadmap's own framing: the server already rebuilds the graph from the store at boot; a deployment whose graph state is suspect currently has no offline path -- "the fix is restart and hope." This gives operators a deliberate repair action and a verification artefact instead.
- **Roadmap's own effort estimate:** ~0.5 day -- "the rebuild logic exists; this is invocation and reporting."
- Not started. Smallest, cheapest item in the whole wave-6 batch.

### T-75. iolu db reindex-fts (item 27, wave 6): offline FTS backfill for deployments that enable full-text search after already accumulating data -- demand-triggered, not urgent, per the roadmap's own explicit guidance

Theme: iolu · Priority: P4 · Status: ☐ · Blocks/after: After: none. Deliberately last of the concretely-scoped wave-6 items -- build on first real need, not speculatively.

- **Trigger:** wave 6 planning/grouping pass (docs/proposals/iolu-operations-roadmap.md item 2).
- **Scope:** `iolu db reindex-fts --base-dir D [--tenant NAME]`, idempotent (drop-and-rebuild per entity type inside a transaction), printing per-type row counts so an operator can reconcile against `db status`. Walks every entity row per tenant, rebuilding FTS table content -- the gap: FTS indexing happens at write time only, so enabling full-text search on an already-populated deployment leaves its past permanently unsearchable, with no error and no signal.
- **Deliberately low priority, per the roadmap's own explicit guidance:** "Depends on nothing" but is a migration tool whose demand is triggered specifically by enabling FTS on existing data -- the roadmap says outright "build on first real need," not ahead of it.
- **Roadmap's own effort estimate:** ~1 day.
- Not started. Lowest priority of the concretely-scoped wave-6 items; correctly so per the roadmap's own sequencing, not an oversight.

### T-81. iolu / xia split (item 37, wave 8): rename current admin CLI to xia, repurpose iolu around querying the substrate (a normal scriptable CLI, PLUS an iaul-style REPL as one subcommand, not REPL-only) -- separable track, 43 files affected, mostly mechanical

Theme: iolu · Priority: P3 · Status: ☐ · Blocks/after: After: T-70 (query+REPL mechanics must exist before iolu's name means something new). Related: T-76-T-80 (same planning pass, no code dependency).

- **Trigger:** proposed alongside the OQL primitive-query wave -- rename the current admin CLI to xia ("xolu instance admin"), repurpose iolu around "interactive olu" -- querying the substrate as its focus, but keeping a normal scriptable CLI surface (iolu query --oql "...", one-shot, per T-70) with iolu repl as an ADDITIONAL subcommand behaving like aulsql's iaul, not the tool's only mode.
- **Blast radius checked directly, not estimated:** 43 files reference "iolu" across code, build config, and docs. The Makefile already conceptually separates the admin binary as its own build target (ADMIN_BINARY=iolu, ADMIN_PATH=./cmd/iolu) -- the rename is mostly mechanical (build target, module path, docs), not a structural redesign.
- **Separable from the rest of wave 8, deliberately:** shares no code dependency with T-76 through T-80 (pkg/oql work) -- rides along on the same wave because it was raised in the same planning pass, not because it's blocked by or blocks the query-provider work.
- **Relationship to T-70:** T-70 (query + REPL, filed under wave 6) already covers the REPL mechanics (one-shot query execution, interactive readline shell borrowing iaul's shape). This item is specifically the RENAME/repurposing -- what currently answers to "iolu" (tenant management, provisioning, db init/status/upgrade, bal prune, cal provisioning) becomes xia; the NEW iolu becomes the query-focused tool T-70 already scoped. T-70's own work is a prerequisite: repurposing iolu's name only makes sense once there's a query tool worth putting the name on.
- **Default invocation behaviour (recorded, direct instruction, refined against a named concrete precedent):** the renamed `iolu` follows the `mysqladmin`/`mysql` split precisely -- `xia` is the `mysqladmin` analog (administrative operations, no interactive mode expected), the renamed `iolu` is the `mysql` analog. `mysql`'s own actual behaviour is more precise than "bare args means REPL" alone: it dispatches on whether **stdin is a TTY**, not merely on argument count. Interactive terminal stdin -> REPL (prompt, readline, history). Piped or redirected stdin (`iolu < script.oql`, `cat queries.oql | iolu`) -> read statements from stdin and execute them as a batch, no prompt, no readline, exits when stdin is exhausted -- this is DISTINCT from both the REPL and from `iolu query --oql "..."` (a single statement via flag), a third invocation shape `mysql` itself also has (`mysql -e "..."` vs `mysql < file` are both non-interactive but different). `iolu <subcommand> ...` (`iolu query`, `iolu repl` explicitly, any future non-query subcommand) still runs as the ordinary scriptable CLI regardless of stdin's TTY status -- the TTY check only governs the FULLY-BARE invocation's dispatch between REPL and batch-from-stdin. Not verified against a live mysql binary in this sandbox (unavailable here) -- recorded as the standard, documented mysql/psql/sqlite3 convention, not a live-tested behaviour in this session. Applies to the RENAMED iolu specifically, per the same transitional-state caveat already recorded above.
- Not started.

### XOT203. iolu db backup: physical/raw-file single-instance backup -- SQLite online-backup API per database file plus Pebble checkpoints for ts, cal, and bal's rollup index (all three, corrected from T-71's original cal-only scope) -- split out of T-71 when T-71 was rescoped to reuse pkg/tenantexport's logical export instead

Theme: iolu · Priority: P4 · Status: ☐ · Blocks/after: After: none. Related: T-71 (the logical-export path this complements, not replaces), T-72 (db verify, unaffected by this split).

- **Trigger:** T-71's original scope (SQLite online-backup API + a Pebble checkpoint) was found, on review, to genuinely under-cover what `pkg/tenantexport` (T-149, v0.25.0) already does correctly -- and to solve a *different* problem from it (byte-exact physical restore vs. a portable logical export). Rather than silently drop the physical-backup case, it is split out here as its own item, named explicitly, matching this project's own "not a deferred oversight, a stated boundary" discipline.
- **Corrected scope, using tenantexport's own already-verified coverage as the reference:** T-71's original text named only cal's Pebble index. The real per-tenant Pebble footprint is three stores -- `ts`, `cal`'s occupancy index, and `bal`'s rollup index (`storelayout.TenantTSDir`/`TenantCalDir`/`TenantBalRollupDir`, confirmed directly in `pkg/tenantexport/export_tenant.go`). This item's scope is corrected to all three, not just cal's.
- **Distinct from T-71 (now the logical-export path), by design guarantee, not just format:** SQLite's own online-backup API and a Pebble checkpoint produce a byte-exact image -- restore-by-copy, no schema-drift risk, no JSON round-trip on any edge-case type. `tenantexport`'s zip-of-JSON-tables is portable and inspectable but is a *logical* export, not an image. Real disaster-recovery scenarios wanting guaranteed-identical restore need this, not that.
- **Not urgent relative to T-71:** T-71's rescoped reuse of `tenantexport` ships real backup capability now, offline-capable, more complete than this item's own original scope was. This item exists so the physical-backup case is tracked and priced, not lost -- not because it's blocking anything today.
- **Estimate:** ~1.5 days (SQLite online-backup API wrapper + Pebble checkpoint for three stores instead of one; roughly T-71's original ~1d share plus the two added Pebble stores).
- Not started.

### XOT204. iolu db restore -- documented manual procedure only today (T-71/T-72's own stated initial boundary: 'copy back onto a stopped server'); never filed as its own trackable item until now

Theme: iolu · Priority: P4 · Status: ☐ · Blocks/after: After: XOT203 (physical backup) for full restore-by-copy; can restore from tenantexport's logical export independently once that path exists, at reduced guarantee (see scope note).

- **Trigger:** T-71 and T-72 both name restore as explicitly out of scope ("Restore remains a documented manual procedure initially... rather than a command" -- the roadmap's own words, quoted in T-71's own entry) -- a real, named boundary, but one that was never itself filed as a register item, only as a scope-exclusion note inside two other items. Promoted to its own trackable item now rather than left permanently implicit.
- **Two distinct restore paths, matching the two distinct backup paths (T-71/XOT203), not one:**
  1. **From a physical backup (XOT203):** copy the SQLite files and Pebble directories back onto a stopped instance -- byte-exact, the documented manual procedure today, this item's job is to turn it into a real `iolu db restore` command (stop-server discipline enforced by the tool, not just the operator's own memory).
  2. **From a logical export (T-71/`tenantexport`):** re-materialise a tenant's data from the exported JSON tables into a fresh or existing store. A genuinely different operation -- closer to import than restore, and not guaranteed byte-identical (it's a re-derivation from the same rows, not a copy).
- **Not scoped further here, deliberately:** which path(s) ship first, and whether `iolu db restore` should be one command with a `--from-physical`/`--from-export` switch or two separate commands, is an implementation decision for whichever wave picks this up -- not decided in advance of that work.
- **Estimate:** ~1.5 days provisional (physical-restore path only, the higher-value one; export-based restore priced separately if/when scoped).
- Not started.

## oql

### T-76. OQL primitive-query dispatch infrastructure (item 32, wave 8): recognise TableValuedFunction/MethodCallExpression FROM/WHERE items, route to a new provider interface -- everything else in wave 8 depends on this

Theme: oql · Priority: P2 · Status: ☐ · Blocks/after: After: none. Blocks: T-77/78/79/80 (all four @-primitive providers). Leads wave 8 per docs/proposals/oql-primitive-queries.md's own sequencing.

- **Trigger:** direct proposal -- extend OQL so ts/cal/bal/fsm (none of which have a query language today) become queryable, using @-prefixed T-SQL user-defined-function-style syntax.
- **Validated before any design work, not assumed:** wrote a throwaway probe against the real tsqlparser v0.6.1 library directly. `FROM @FSM(1) AS x`, `SELECT @TS('metering', from, to)`, and `WHERE @FSM.walk() IS NOT NULL` (the user's own exact example) all parse with ZERO changes to tsqlparser. This is not a designed feature of the parser -- @-prefixed tokens lex as token.VARIABLE, and LPAREN is a fully generic infix "call" parselet applying to any preceding expression, so "@variable(...)" falls out of the grammar's own Pratt-parser architecture. A second probe pass, run while starting this item's own implementation, pinned down the exact node types: `*ast.TableValuedFunction` for FROM-position pseudo-tables (`Function`/`Arguments`/`Alias` fields), `*ast.FunctionCall` for scalar/SELECT-list position (`@TS(...)` as a bare value), and `*ast.MethodCallExpression` for WHERE/scalar-position method calls -- recorded in docs/proposals/oql-primitive-queries.md.
- **What's actually missing, confirmed by reading the code, not guessed:** pkg/oql/executor.go's extractEntityFromSelect does a bare `s.From.Tables[0].(*ast.TableName)` type assertion -- a TableValuedFunction-shaped FROM item fails this silently today and falls through to an empty entity name. The door is open; nothing walks through it.
- **Scope of this item specifically:** recognise a TableValuedFunction/MethodCallExpression FROM/WHERE item in the planner, extract the primitive name + arguments, and route to a new provider interface. Pure plumbing -- no primitive-specific query translation logic. This is the one piece every other wave-8 item (T-77 through T-80) depends on.
- **Architectural fit already confirmed, not a new pattern to invent:** PushNone ("stay in Go") is already a first-class PushDecision in pkg/oql/planner.go -- OQL already has a row-by-row, Go-evaluated fallback for predicates it can't push to SQLite. A MethodCallExpression predicate is architecturally the same shape.
- **Open design questions, deliberately left unresolved here (see the proposal doc's own list):** correlation semantics for WHERE @FSM.walk() (does it need implicit access to the outer row?), whether @TS()-style calls are scalar/aggregate rather than row-producing like @FSM(). Chained method access (@FSM.state(1).name) is no longer an open question here -- confirmed structural (not a display bug) and filed separately as T-137, since it doesn't block this item's own plumbing scope. Performance signalling for a Pebble round-trip potentially happening once per outer row remains open.
- Not started.


### T-77. @FSM() OQL query provider (item 33, wave 8): first of the four primitive providers -- fsm is fully SQL-backed, proves the dispatch mechanism with the least new surface area

Theme: oql · Priority: P3 · Status: ☐ · Blocks/after: After: T-76 (dispatch infrastructure). Related: T-78/79/80 (the other three providers).

- **Trigger:** wave 8 planning pass (docs/proposals/oql-primitive-queries.md), sequenced first among the four primitive providers.
- **Why first, per the proposal's own difficulty-ordered sequencing:** fsm is fully SQL-backed already -- closest to what OQL already knows how to do (push-down over SQLite). Proves the dispatch mechanism (T-76) end to end with the least new surface area, before tackling primitives with Pebble involvement at all.
- **Scope:** `@FSM(...)` as a FROM-clause pseudo-table (yields rows: state, entity_ref, etc., matching fsm's own SQL shape) and `@FSM.walk()`-style method calls in WHERE/scalar position, per the two validated syntactic shapes.
- Not started. Depends on T-76 (dispatch infrastructure).

### T-78. @BAL() OQL query provider (item 34, wave 8): second of the four primitive providers -- SQL-authoritative with a Pebble satellite, moderate difficulty

Theme: oql · Priority: P3 · Status: ☐ · Blocks/after: After: T-76 (dispatch infrastructure). Related: T-77/79/80 (the other three providers).

- **Trigger:** wave 8 planning pass (docs/proposals/oql-primitive-queries.md), sequenced second among the four primitive providers.
- **Why second:** bal is SQL-authoritative with a Pebble satellite (the rollup plane, T-62) -- moderate translation difficulty, and bal's own query shape (accounts, journal entries, balances) is well-understood after this session's wave-4 completion work (T-58 through T-67).
- **Scope:** `@BAL(...)` as a pseudo-table, likely over accounts or journal entries; `@BAL.balance()`/`@BAL.asof()`-style method calls as the natural mapping onto bal's existing Balance/BalanceAsOf surface.
- Not started. Depends on T-76 (dispatch infrastructure).

### T-79. @CAL() OQL query provider (item 35, wave 8): third of the four primitive providers -- span/availability queries are a genuinely different pattern from bal's point lookups despite the similar SQL+Pebble storage shape

Theme: oql · Priority: P3 · Status: ☐ · Blocks/after: After: T-76 (dispatch infrastructure). Related: T-77/78/80 (the other three providers).

- **Trigger:** wave 8 planning pass (docs/proposals/oql-primitive-queries.md), sequenced third among the four primitive providers.
- **Why third, not second despite the same SQL+Pebble shape as bal:** cal's occupancy/availability queries are span-based (overlap, openings search), a genuinely different query pattern from bal's point lookups (balance of one account) -- more translation novelty than bal despite the superficially similar storage split.
- **Scope:** `@CAL(...)` as a pseudo-table over calendars or bookings; `@CAL.check()`/`@CAL.openings()`-style method calls mapping onto cal's existing CalCheck/CalOpenings surface.
- Not started. Depends on T-76 (dispatch infrastructure).

### T-80. @TS() OQL query provider (item 36, wave 8): last and hardest of the four primitive providers -- fully Pebble, no SQL fallback, likely a scalar/aggregate call shape rather than row-producing

Theme: oql · Priority: P3 · Status: ☐ · Blocks/after: After: T-76 (dispatch infrastructure). Related: T-77/78/79 (the other three providers).

- **Trigger:** wave 8 planning pass (docs/proposals/oql-primitive-queries.md), sequenced last among the four primitive providers.
- **Why last:** ts is fully Pebble, no SQL fallback at all -- the hardest, most novel translation problem of the four. No existing push-down machinery to lean on the way fsm/bal/cal's SQL-resident portions allow.
- **Scope:** `@TS(...)` -- likely scalar/aggregate rather than row-producing (SELECT @TS('metering', from_time, to_time) reads as a single aggregate value, not a table source), a genuinely different call shape from @FSM()/@BAL()/@CAL()'s row-producing pattern. This distinction is itself an open question the proposal doc flags, not yet resolved.
- Not started. Depends on T-76 (dispatch infrastructure). Last of the four providers by design, not neglect.

### T-137. tsqlparser chained method-call access (`@FSM.state(1).name`) drops the base call entirely -- structural parser defect, not a display bug

Theme: oql · Priority: P3 · Status: ☐ · Blocks/after: After: none. Relevant to: whichever wave-8 provider first needs chained access (not T-76, not scoped this wave).

- **Trigger:** while probing tsqlparser v0.6.1 directly to pin down the exact AST shapes for T-76's dispatch code (routine pre-implementation check, not a response to any defect report), the proposal's own open "wrinkle" -- whether `@FSM.state(1).name` drops its qualifier at display time only, or structurally -- got an answer: it's structural. `Parse()` returns a bare `*ast.QualifiedIdentifier` for `name` alone; the `@FSM.state(1)` call is gone from the AST, not just from `String()`'s output.
- **Scope:** any OQL query relying on chained access after a primitive method call (`@FSM.state(1).name`, and presumably the same shape for `@BAL`/`@CAL`/`@TS` once those exist) silently loses the base expression rather than erroring. Not scoped to any wave-8 item today -- none of T-76/77/78/79/80 use chained access in their own filed scope -- filed so whoever eventually wants it doesn't have to rediscover this.
- **Exit:** either tsqlparser is fixed upstream (github.com/ha1tch/tsqlparser) to preserve the call through a trailing member access, or the OQL layer documents chained access as unsupported and refuses it explicitly rather than silently mis-parsing.

### XOT241. OQL answers XOLU-QL004 for an entity type that has no rows, though types are created implicitly on first write

Theme: oql · Priority: P3 · Status: ☐ · Blocks/after: After: none.

- **Trigger:** a type that was never written, or whose rows were all deleted, is not in the store's entity list, so `SELECT ... FROM <type>` is `400 XOLU-QL004 entity does not exist` instead of an empty result. Types are implicit, so "does not exist" and "has no rows" are the same state. It showed up as a regression in the first XOT229 fix (`TestExecuteWithStore_UpdateDelete`: a tenant emptied a type and queried it) and clients must treat the error as "no rows" in some places and as "cannot query" in others (a xolu without XOT229).
- **Fix to decide:** return an empty result for a name that passes identifier validation, or keep QL004 only for names with a registered schema or a known history of rows. Either is a behaviour change for clients that rely on QL004 to catch typos.
- **Source:** found while implementing XOT229; not in the original proposal's numbered items.

### XOT242. `SELECT *` on an adapted table is not translated to SQL, so it always takes the Go path and reads the whole table

Theme: oql · Priority: P4 · Status: ☐ · Blocks/after: After: XOT228.

- **Trigger:** `isFullyTranslatable` returns false for a star projection on an adapted entity (`unsupported expression type`), so `SELECT * FROM t WHERE <indexed column> = ?` scans in Go even with an index. Row queries naming columns, `id` and `_version` included, are pushed down since XOT228.
- **Fix:** expand `*` to the table's columns in the planner for an adapted entity, and rebuild each row from them (columns plus the overflow column) the way the Go path does.
- **Source:** left open under XOT228.

### XOT246. The OQL push-down thresholds are calibrated at start, so the same build pushed filters at 30 rows on one start and above 100 on another

Theme: oql · Priority: P4 · Status: ☐ · Blocks/after: After: none.

- **Trigger:** the planner logs `profile=calibrated ... blobThreshold=54 nonCoveringThreshold=160` after measuring Go and SQL per-row costs at start-up; a defect that appeared only above the threshold (XOT228) therefore appeared at different table sizes on different starts and was hard to reproduce. Not a defect in itself.
- **Fix to decide:** expose the calibrated thresholds (`/health` or `/version`), allow pinning them through configuration for tests, and log them at INFO instead of debug.
- **Source:** observed while reproducing XOT228.

## validation

### T-91. pkg/validation.JSONSchemaValidator's own doc comment claims Loose mode by default, but its actual code uses queryfy.Strict -- a real, pre-existing discrepancy found while adversarial-testing dxp/txn's bindings validation, not this session's to fix

Theme: validation · Priority: P4 · Status: ☐ · Blocks/after: Unrelated to dxp work -- found as a side effect of T-89's own adversarial test suite. dxp/txn's own validation deliberately matches the real (Strict) behavior, not the doc comment.

- **Trigger:** found while writing an adversarial test for dxp/txn's own bindings_schema validation (TestDxpTxnAPI_Adversarial_SchemaViolations) -- the test's own expectation that extra, undeclared fields would be ALLOWED (matching the doc comment's claim) was refuted by the actual running behavior, which refuses them.
- **The exact discrepancy, checked directly at both locations rather than assumed from one:** pkg/validation/validation.go's JSONSchemaValidator doc comment states "Validation runs in Loose mode by default: type coercion is applied ... and additional properties are allowed unless the schema explicitly sets additionalProperties:false." Its actual Validate method, the very next code below that comment, uses `queryfy.NewValidationContext(queryfy.Strict)` -- not Loose. The doc comment describes behavior the code does not have.
- **Not this session's bug to fix -- pkg/validation predates this session's dxp work entirely and backs entity's own adapted-table schema validation, already in production use with the real (Strict) behavior.** Fixing the doc comment is a one-line, safe change; changing the actual validation mode to Loose (to match the doc comment instead) would be a real behavioral change to already-shipped, already-relied-upon entity schema validation, and is not this session's call to make.
- **How this session handled it:** dxp/txn's own bindings validation (handleDxpTxnCreate, pkg/server/v2_dxp_def_handlers.go) deliberately matches the REAL, tested code behavior (queryfy.Strict), not the doc comment's aspirational claim -- consistent with this session's own repeated principle of trusting proven, running code over aspirational documentation (the same reasoning that led dxp_defs' own id scheme to fsm's real schema over the doctrine's aspirational def_id-plus-version example). The adversarial test that found this was corrected to expect the real behavior, with the discrepancy recorded in the test's own comment pointing here.
- **Recommended fix, for whoever picks this up:** correct pkg/validation.go's doc comment to state Strict mode accurately (or, if Loose mode was actually the intended design and Strict is the bug, that is a separate, larger decision requiring its own review of every existing consumer of JSONSchemaValidator, not a doc fix -- flagged as a real fork in the road, not resolved here).
- Not started.


## system-stores

### T-114. System stores content (item 31, wave 7): ~metering (ts) and ~billing (bal) -- the only wave-7/8 item never filed at all

Theme: system-stores · Priority: P3 · Status: ☐ · Blocks/after: After: #9 (sysmask, closed T-43/T-44), #17 (bal molu surface, closed T-67 v0.20.2) -- both already met. Blocks: nothing hard; closes wave 7 alongside item 30 (T-07-T-13).

- **Trigger:** discovered 2026-08-01 in the same wave-audit pass as the
  item-8 finding. Wave 7 has two items in the plan's inventory table:
  item 30 (event model part 2) is already filed as T-07 through T-13;
  item 31 (system stores content) had never been filed at all -- the
  only wave-7/8 item with no register entry of any kind, open or
  closed.
- **Scope, per the plan (wave 7, item 31):** system stores content --
  `~metering` (resident in `/ts`) and `~billing` (resident in `/bal`) --
  the substrate dogfooding its own primitives under the tilde-sigil
  convention (@S03). The plan's own text: *"their completion is the
  natural demo artefact for fundraising conversations."* Ideal effort:
  5 days (provisional, per the plan).
- **Prerequisites already met:** both named dependencies -- #9 (sysmask,
  closed T-43/T-44) and #17 (bal molu surface, closed T-67, v0.20.2) --
  have shipped. Nothing blocks scoping this properly once wave 7 opens.
- **Review note (2026-08-17):** this item's own justification was
  audited -- see the Plan deviations log entry of the same date, above.
  No consumer found asking for usage metering or billing; the only
  surviving rationale traced is the plan's own "demo artefact for
  fundraising conversations" line. Not closed, reprioritised, or
  rescoped by the audit itself -- decision pending.
- Not started.


## obj

### T-134. obj patterns: the full §4a mechanism deferred out of T-124 (obj-01-rest-api.md §4a, obj-00-design.md §7a) -- def/extract/list/get/delete endpoints, pattern/pattern_after wiring into attach, pattern/pattern_id/pattern_deleted GET fields, XOLU-OBJ013 reachability

Theme: obj · Priority: P3 · Status: ☐ · Blocks/after: none directly

- **Trigger:** deferred out of T-124 (wave 10, item 50) 2026-08-02, confirmed directly with the team rather than silently built or silently dropped.
- **Scope:** the same mechanism already twice-proven for loc (T-131) and cal/fsm before it, applied a third time here: POST /obj/patterns/def, POST /obj/patterns/extract, GET /obj/patterns/list, GET /obj/patterns/{id}, DELETE /obj/patterns/{id}; wiring `pattern`/`pattern_after` into `attach` (mutually exclusive with `capacity`, XOLU-OBJ013); a `pattern`-attached subject's GET surfacing `pattern`/`pattern_id`/computed `pattern_deleted`; a `pattern_after`-attached subject surfacing none of these (nothing persisted, per obj-01-rest-api.md §4a's own explicit "not a gap" framing).
- **Reachability note:** closes the two XOLU-OBJ codes T-124's own completion sweep found unreachable without this item -- XOLU-OBJ013 directly; XOLU-OBJ020 ("unknown (kind,key) subject") remains a separate, likely-orphaned code from an earlier draft (never described in obj-01-rest-api.md's own prose, patterns/extract's own "source doesn't resolve" case is documented as XOLU-OBJ001 instead) -- worth confirming with the team whether to remove it from the error table rather than build toward it, not assumed to need new reachability work of its own.
- **Exit:** matches T-131's own exit shape -- full pattern lifecycle (def, extract, apply via both pattern and pattern_after, list, get, delete with no cascade refusal) proven via real HTTP round trips, GET response fields verified for both attachment shapes.

## loc, bal

## server

### T-148. dispatchEvent fires unconditionally on every v1 CRUD write, regardless of APIV2Enabled -- eventStore() never checks it, so any v1-only deployment logs a WARN on every single write (event_defs genuinely doesn't exist until v2 is on)

Theme: server · Priority: P3 · Status: ☐

- **Trigger:** found while running the CRM launcher/seed scripts against a real server without APIV2Enabled -- every single entity write logged `WRN event dispatch: subscription lookup failed error="SQL logic error: no such table: event_defs (1)"`. (The CRM example itself has since moved to xoluman, v0.30.0 -- not in this repository's own `examples/` anymore, though the bug and its trigger described here are unaffected by where the example now lives.)
- **Root cause:** `pkg/server/event_dispatch.go`'s `dispatchEvent` runs unconditionally from every v1 CRUD write path, regardless of whether v2 is enabled. Its own `eventStore()` gate checks only whether a store resolves for the tenant and implements `storage.WriterDBProvider` -- it never checks `s.config.APIV2Enabled`. `event_defs` (and the rest of the v2 schema: `entity_delivery_log`, `entity_meta`, `gen_definitions`, `sequences`, `fsm_*`) is only created by `initV2Schema`, which only runs when `XOLU_API_V2_ENABLED=true`. On any v1-only deployment, `event_defs` never exists, and `dispatchEvent` pays for a doomed query and logs a warning on every single write, forever -- not a one-time or rare occurrence.
- **Not fixed this session:** worked around at the deployment-config level (the CRM launcher sets `XOLU_API_V2_ENABLED=true`, which makes `event_defs` genuinely exist), but the underlying gap in `eventStore()` is untouched. Any v1-only xolu deployment still hits this.
- **Correct fix, not attempted here:** `eventStore()` should check `s.config.APIV2Enabled` and return `(nil, false)` immediately when it's false, short-circuiting `dispatchEvent` before it ever reaches `matchEventDefs`'s query -- avoiding both the wasted query and the log noise, for a deployment that has no events subsystem to dispatch to in the first place.


## sulpher

## cron

### XOT197. /cron item 1: storage, def/job registration, prototype-clone, capability introspection -- the def is the prototype, the job the armed clone; verb capabilities (cancellable, progress-reporting) discovered by interface assertion at def time and recorded on the def, never self-declared

Theme: cron · Priority: P2 · Status: ☐ · Blocks/after: Blocks: every other /cron item. After: none (design decisions cron-primitive.md §10.2/§10.3 should be settled before item XOT198 starts, not before this one).

- **Design:** `docs/proposals/cron-primitive.md` §1, §3, §14.4. Mirrors `pkg/server/v2_dxp_def_handlers.go`'s def-registration shape.
- **Scope:** `pkg/cron` skeleton; def storage with shape/schedule/zone/policies/verb/reclamation-class fields; job as clone; capability record populated by interface assertion against the verb registry (item XOT200's interfaces, but the assertion mechanism lives here).
- **Not in scope:** the scheduler loop (item XOT198), any verb implementation (item XOT200).
- **Estimate:** 1.5 ideal days (cron-primitive.md §16.2 row 1).


### XOT198. /cron item 2: recurrence rule object, IANA zones, DST policy, scheduler loop, tick plane, missed-fire + overlap policy, suppression-streak counter, watchdog shape + reset -- the deep pit and the racy loop, deliberately one item so the tick plane and the rules that decide which ticks chime are built and tested together

Theme: cron · Priority: P2 · Status: ☐ · Blocks/after: After: item XOT197. Blocks: items XOT199, XOT201, XOT202. Prerequisite decisions: cron-primitive.md §10.2 (recurrence grammar) and §10.3 (DST enum) MUST be settled before this item starts.

- **Design:** `docs/proposals/cron-primitive.md` §2, §4, §5, §6 (panic recovery), §13.1, §13.8 (zone/offset rule), §13.9 (starvation), §13.10 (pkg/gc precedent, added 2026-08-17).
- **Scope:** structured recurrence rule (not five-field cron strings -- §13.2's `DBMS_SCHEDULER` calendaring precedent); zone field is an IANA name, never an offset; per-def DST policy; `at`/`after`/`every`/`watchdog` resolution to due-instants; the scheduler goroutine, its ticker-and-stop-channel lifecycle mirroring `pkg/gc.Worker`'s own proven shape including the double-start guard (§13.10); tick records as facts (suppressed ticks recorded); missed-fire (`skip`/`fire_once`/`catch_up`) and overlap (`skip`/`queue`/`concurrent`) as mandatory per-def fields; consecutive-suppression counter on the job; `watchdog` + `reset`; a panicking verb recovered and recorded as a distinct tick outcome (`panicked`), never a crashed scheduler -- the same fix `pkg/gc.Worker.RunOnce` shipped after a real incident (T-156/T-157), inherited here rather than re-learned.
- **Risk, revised 2026-08-17:** the scheduler loop's lifecycle *shell* (ticker, start/stop, panic recovery) now has a proven-in-production pattern to mirror (`pkg/gc.Worker`, §13.10), reducing that portion's risk and cost. What is NOT de-risked: overlap policy under real concurrent load is still the shape that produced 0.23.0's coordinator races -- `pkg/gc` has no per-def overlap concept -- and a sandbox pass is still not evidence. Multi-core confirmation is owed (Part 1 §5.2) and lands as its own dormant guard, registered same session (§8).
- **Estimate:** 2.5 + 1.5 + 0.75 = 4.75 ideal days (was 5.25; scheduler-loop row revised 2.0 -> 1.5 -- cron-primitive.md §16.2 rows 2-4).

### XOT199. /cron item 3: the chime -- `cron.chime` event type into EVENT_MODEL.md's closed set, shape discriminated in event_latch_source, payload carrying due-instant/def/job/shape and originally-due for catch-up ticks; unconditional on every non-suppressed tick regardless of T-07

Theme: cron · Priority: P2 · Status: ☐ · Blocks/after: After: item XOT198. Blocks: item XOT201 (job control's info reads chimes alongside ticks). Not blocked by T-07 -- designed around it (cron-primitive.md §6).

- **Design:** `docs/proposals/cron-primitive.md` §3 (tick vs chime planes), §6, §9, §10.8 (chime/verb ordering -- decided at implementation, here).
- **Scope:** one new event type, not four; `event_latch_source` coordinate grammar (e.g. `cron/every/<def>`); payload; the ordering decision between chime and verb outcome (§10.8) is made and recorded here.
- **Estimate:** 0.75 ideal days (cron-primitive.md §16.2 row 5).

### XOT200. /cron item 4: verb registry, the cancellable and progress-reporting interfaces, and two verbs (`fsm_walk`, one prune) -- capabilities are what the Go type implements, discovered by item XOT197's assertion; each further verb is its own small addition, never speculative

Theme: cron · Priority: P2 · Status: ☐ · Blocks/after: After: item XOT197. Blocks: items XOT201, XOT202. cron-primitive.md §10.6 (OQL-verb reconciliation with the events OQL-action target) is deliberately NOT in this item.

- **Design:** `docs/proposals/cron-primitive.md` §6, §13.5 (one verb per def, forever), §14.4.
- **Scope:** registry; context-based cancellation interface; progress-reporter interface (`phase?`, `done`, `total?`, `updated_at`); `fsm_walk` (machine id + input) and one prune verb (`ts_rollup_prune` or `bal_checkpoint_recompute`, chosen at implementation by which has a live consumer). Verb outcome recorded on the tick per §6.
- **Estimate:** 1.5 ideal days (cron-primitive.md §16.2 row 6).

### XOT201. /cron item 5: job control and execution observability -- `info`/`pause`/`resume`/`stop`/`kill` split across job and execution planes, in-memory execution and progress state (never persisted; Postgres pg_stat_progress precedent), `kill` refuses honestly on non-cancellable verbs, `resume` hands accumulated due instants to the job's own missed-fire policy

Theme: cron · Priority: P2 · Status: ☐ · Blocks/after: After: items XOT198, XOT199, XOT200. Blocks: item XOT202 (system jobs are controlled through this surface). cron-primitive.md §10.10 (progress phase vocabulary) and §10.11 (stop-drain deadline) decided here.

- **Design:** `docs/proposals/cron-primitive.md` §14 in full.
- **Scope:** the five endpoints; the execution as third vocabulary object; memory-only progress; terminal `stop` with no `restart` (re-clone the def); no rollback on `kill` (the def's reclamation class governs, item XOT202); existence always observable even for opaque verbs.
- **Estimate:** 1.5 ideal days (cron-primitive.md §16.2 row 7).

### XOT202. /cron item 6: retention + `~trim`, reclamation classes + Reclaimer interface + `/blob` Reclaimer + `~sweep`, system jobs armed at startup, dxp janitor migrated onto /cron, REST surface + pkg/client + molu + docs + dormant-guard registration -- the wave's closing item, where the primitive proves itself by maintaining xolu on its own clock

Theme: cron · Priority: P2 · Status: ☐ · Blocks/after: After: items XOT197, XOT198, XOT199, XOT200, XOT201. Blocks: none. Sequencing note: if the eager stale-checkpoint recompute (cron-primitive.md §8) is included as a v1 dogfooding job, T-55's remaining /cal and /ts rulings should land first; otherwise the janitor migration carries the dogfooding proof alone.

- **Design:** `docs/proposals/cron-primitive.md` §5 (retention), §8, §15 in full, §16.5.
- **Scope:** per-def retention as the third mandatory policy; `~trim` job honouring the sweep horizon (§15.4: a tick with pending reclamation is unprunable); five reclamation classes on the def; Reclaimer interface in the dxp Participant mold; `/blob` Reclaimer for `staged` and `attributed` (v1: /blob only); `~sweep` job(s); `~` system jobs arm themselves at instance startup, disabling is an explicit recorded act; the dxp janitor (T-54's Janitor) AND `pkg/gc`'s four existing Sweepers (blob GC, ts retention, fsm v2 collection, event delivery log v2 cleanup -- found 2026-08-17, cron-primitive.md §13.10) migrated onto /cron as `~` jobs, "closer to a rename than new work" per that section's own framing; REST + client + molu + docs; every dormant guard this wave creates registered in the same session (Part 3 §8).
- **Deferral lever:** if scoped down, §15 (reclamation) is the natural v1.1 deferral (≈ 2.75 ideal days saved -- cron-primitive.md §16.4 lever 2); the rest of this item stays.
- **Estimate:** 0.75 + 2.0 + 1.25 + 1.5 = 5.5 ideal days (was 5.25; dogfooding row revised 1.0 -> 1.25 for covering four gc consumers plus the janitor, not the janitor alone -- cron-primitive.md §16.2 rows 8-11).

## storage-config

### XOT232. JSON numbers pass through float64: integers above 2^53 change on store and read (UseNumber is used only by bal and dxp)

Theme: storage-config · Priority: P3 · Status: ☐ · Blocks/after: After: none.

- **Trigger:** `9007199254740993` is stored and returned as `9007199254740992`; OQL returns ids as float64. 33 `json.NewDecoder(r.Body)` sites in the server and 33 `json.Unmarshal` sites in storage.
- **Fix:** decode with `UseNumber` (or keep raw JSON) through the entity paths, marshal `json.Number`, and return integer columns as integers in OQL results. Start with entity create, update and `/commit`.
- **Source:** `docs/proposals/versioning-00-substrate-changes.md` X5. Not started.

### XOT237. A per-type commit-only write policy: refuse plain POST/PUT/PATCH/DELETE and OQL data statements on a managed type

Theme: storage-config · Priority: P4 · Status: ☐ · Blocks/after: After: X3, X6.

- **Trigger:** nothing stops a plain write bypassing a client-maintained history; the client needs detectors for gaps, missing rows and untracked deletes.
- **Fix:** a per-type flag enforced at a store-level choke point (77 write route registrations plus OQL INSERT/UPDATE/DELETE).
- **Source:** `docs/proposals/versioning-00-substrate-changes.md` X10. Not started.

### XOT243. `POST /schema/{entity}` saves the schema document before it migrates the table, so a refused migration leaves the saved schema ahead of the table

Theme: storage-config · Priority: P3 · Status: ☐ · Blocks/after: After: none.

- **Trigger:** adding a unique index over rows that already violate it (XOT236) is refused with 409 and the table is unchanged, but the schema document had already been saved and loaded into the validator; the registry's hash and the table disagree until the schema is posted again or the old one restored. Any migration failure has the same shape.
- **Fix:** run the table registration or migration first and save the document only after it succeeds, or roll the saved document back when the migration fails.
- **Source:** noted under XOT236.

### XOT244. Registering a schema for an entity type that already has rows (blob storage) hides those rows: lists, queries and reads by id stop finding them

Theme: storage-config · Priority: P2 · Status: ☐ · Blocks/after: After: none.

- **Trigger:** verified with a 5-row type: after `POST /schema/<type>` a query returned 0 rows and `GET` by id returned 404, while the rows were still in the blob table. The strict promotion route (`POST /entities/promote/strict/<type>`) migrates them, so the data is recoverable, but the registration itself succeeds silently.
- **Fix:** refuse the registration (409, naming the promotion route) when the type already holds blob rows, or migrate them as part of it.
- **Source:** found while building a client's history-schema helper, which refuses to register over existing rows for this reason.

### XOT245. `/commit` `delete` does not cascade: it is refused while XOLU_CASCADING_DELETE is on

Theme: storage-config · Priority: P4 · Status: ☐ · Blocks/after: After: XOT233.

- **Trigger:** the cascade deletes referrers in transactions of its own, so a delete in a commit that cascaded could not be atomic, and ignoring it would leave referrers behind. XOT233 refuses with `XOLU-CM018` instead.
- **Fix:** run the cascade inside the commit's transaction (the referrer set is known from the schema registry), or document that cascading deletes stay on `DELETE`. Decide the cap that applies to a cascade inside a commit.
- **Source:** deviation recorded under XOT233.

### XOT247. `$now` is not persisted across a restart, and `committed_at` is returned only when a commit sets `templates`

Theme: storage-config · Priority: P4 · Status: ☐ · Blocks/after: After: none.

- **Trigger:** the commit-instant generator (`storage.NextCommitInstant`) is in memory: strictly increasing within a process, but after a restart it starts from the wall clock, so if the clock stepped back by more than the restart took, a later commit can get an earlier `$now`. `$commit_seq` is durable and has no such limit. `committed_at` and `commit_seq` are in the response only when `templates` was set.
- **Fix to decide:** persist the last issued instant (alongside `commit_seq`) and start the generator from `max(wall clock, last + 1)`; return `committed_at` on every commit, not only templated ones, at the cost of one more field in every commit response.
- **Source:** the part of XOT235 not done in 0.30.40; closed with that item in 0.30.41.

## ts

### XOT248. The default store's /ts must be provisioned by a client call; there is no start-up or lazy provisioning

Theme: ts · Priority: P4 · Status: ☐ · Blocks/after: After: none.

- **Trigger:** `/api/v2/ts/...` serves the default store (XOT239), but every route answers `404 XOLU-TS003` until a client has called `POST /api/v2/ts/provision`, as for a tenant. A deployment that wants /ts for the default store has to remember a one-off call.
- **Fix to decide:** a start-up setting that provisions the default store when `XOLU_TIMESERIES_ENABLED` is on, or lazy provisioning on first use. Either changes what an unprovisioned default store means, so it should be a decision, not a default.
- **Source:** the part of XOT239 not done in 0.30.40; closed with that item in 0.30.41.

### XOT249. The /ts leg of /commit is not atomic with the SQLite transaction: a failed undo leaves an orphan event (XOLU-CM016)

Theme: ts · Priority: P3 · Status: ☐ · Blocks/after: After: none.

- **Trigger:** the Pebble events are written first and the SQLite transaction runs after; if SQLite fails the events are deleted by key to undo the write, and if that delete fails too the commit answers `500 XOLU-CM016` with an orphan event in /ts. A client that keeps an index in /ts (a version index) then has an event for a row that was never written; it can find and repair it (`DELETE /ts/events`, XOT240) but xolu cannot prevent it. The window is narrow (two failures in a row) and was never observed in testing.
- **Fix:** an outbox: write the events as a row in the SQLite transaction, apply them to Pebble after the commit, idempotently, and replay the outbox at start. That changes the commit's failure model (events appear shortly after the commit, not before it), so it needs a design note and a decision on how a reader sees an event that is committed but not yet applied.
- **Source:** the part of XOT240 not done in 0.30.40 (the proposal's X13c); closed with that item in 0.30.41.

---
