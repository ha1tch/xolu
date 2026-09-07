# /cron — a scheduling primitive (proposal)

Updated: 2026-08-15
Status: proposal — design sketch from a working conversation, not yet
scheduled, no register item. Records the vocabulary, the shape taxonomy,
the tick/chime plane split, and the dependency map. Nothing here is
committed; the open decisions are listed in §10.

## 1. Provenance and the one-sentence idea

/events fires when something happens. **/cron fires when nothing happens
except time passing.** It is the primitive that owns temporal causation,
the way /fsm owns state causation — and it makes xolu able to maintain
itself on its own clock: clockworks.

The prototype-clone pattern is inherited unchanged from the house
convention (`/fsm/def` → machine, `/rollup/def` → rollup, `/dxp/def` →
txn): a definition is a prototype, an armed instance is a clone, and the
class-mutation problem ("what happens to instances when the class
changes") is impossible by construction rather than handled by policy.

## 1a. Terminology convention (repo-wide, adopted here)

Slash-prefixed names — **/fsm, /bal, /cal, /ts, /dxp, /cron** — always
denote the xolu-managed primitive of that name. The words "state
machine", spelled out, denote the abstract concept. This document
additionally keeps two causation vocabularies deliberately distinct;
see §3.

## 2. Doctrinal fit: /cron is the layer TIME_HANDLING.md predicted

`docs/TIME_HANDLING.md` splits time into two kinds and assigns them to
different layers:

1. **Absolute instants** — all storage, always UTC, sourced only from
   `xolutime.Now()`.
2. **Wall-clock intentions** — "9 a.m. next March," a local reading plus
   a zone, resolved to instants "by the layer that owns the user's zone,
   *above* storage."

That second layer does not exist as a primitive today. A schedule is
precisely a wall-clock intention: "daily at 09:00 America/Montevideo" is
a rule plus a zone that resolves to concrete instants one occurrence at
a time, and whose meaning legitimately shifts when zone rules change.
**/cron is the first primitive whose entire job is owning wall-clock
intentions and resolving them to instants.** The doctrine did not
anticipate /cron, but it pre-dug the foundation; nothing in the
invariant needs bending.

Consequences the doctrine forces on the def, not on the reader:

- **The zone is a stored, explicit field of every recurrence-shaped
  def.** There is no implicit server zone anywhere in xolu; a schedule
  without a zone is invalid, not defaulted.
- **The zone field is an IANA zone name, never a fixed offset.** An
  offset (`+02:00`) is a *reading*; a zone (`America/Montevideo`) is a
  *rulebook* that resolves future readings, DST transitions included.
  SQL Server's `datetimeoffset` stores the reading and therefore cannot
  schedule correctly across a DST boundary — half the world's
  "timezone-aware" schemas carry this confusion (§13.8). /cron is where
  it would materialize, so /cron states the rule.
- **DST ambiguity is decided at def time.** The 02:30 that does not
  exist on spring-forward night, and the 02:30 that exists twice on
  fall-back night, each get an explicit per-def policy (skip / shift /
  fire-both — exact enum to be decided at implementation, §10). Not
  deciding is deciding by accident.
- **Every persisted occurrence is an instant.** The wall-clock rule
  lives only in the def; everything downstream (tick records, chime
  events) carries resolved UTC instants per the invariant.

## 3. Vocabulary: def → job → tick → chime

| Term | Plane | Meaning |
|---|---|---|
| **def** (`/cron/def`) | definition | The prototype: shape, schedule, zone, policies, optional verb. Never executes. |
| **job** (`/cron/job`) | instance | An armed clone of a def. The thing that comes due. |
| **tick** | scheduling | One occurrence of a job coming due at a resolved instant. The unit that missed-fire and overlap policy count. |
| **chime** | event | The `cron.chime` event emitted by a non-suppressed tick into the existing /events machinery. |

Two deliberate choices:

**"Job", not "watch."** An earlier sketch used `/cron/watch`; the word
is a misnomer, proven empirically — its first technical reader assumed
condition-observation (file watchers, `kubectl watch`, watchpoints),
because the observing sense has fully captured the word in software and
the horological sense is dead as a technical term. "Job" is the boring,
universally pre-understood cron word; no reader ever asks what a cron
job is. The shape (§4) is a field in the def body, not a path segment,
exactly as an /fsm def's flavour is a field.

**"Tick" and "chime", not "firing."** `EVENT_MODEL.md` already owns
"firing" with its own lifecycle (matched → delivered). Under this
design a tick *produces* an event firing; two "firings" in one causal
chain would be genuinely confusing, whereas "tick → chime → firing"
reads as the causal sentence it is. More fundamentally, these are two
different causation classes: an event **fires** because something
happened; a cron job **chimes** because time passed. Giving temporal
causation its own verb is the same discipline the repo already applies
to vocabulary collisions (slash-prefixes, "state machine" spelled out),
applied to a new axis.

**A tick and a chime are facts on different planes, and they are not
one-to-one.** A tick suppressed by overlap policy, or dropped by
missed-fire policy, is a tick that never chimes. This gives the policy
layer its precise phrasing: *missed-fire and overlap policy are exactly
the rules deciding which ticks chime* (§5).

## 4. The shape taxonomy

Four shapes, discriminated by a field in the def:

| Shape | Fires | Horological analogue |
|---|---|---|
| `at` | once, at an absolute instant | alarm |
| `after` | once, a duration after arming | kitchen timer |
| `every` | on a recurrence rule (zone-explicit) | alarm clock / cron proper |
| `watchdog` | when a deadline passes **without** being reset | deadman switch |

Notes:

- **The stopwatch is deliberately excluded.** "Timer, stopwatch,
  recurring alarm clock" was the original sketch; the stopwatch is the
  odd one out — it measures elapsed time and never fires. Measurement is
  /ts and ~metering territory. /cron schedules; it does not measure.
- **The watchdog earns its place.** It fires on *absence* ("if no
  heartbeat in five minutes"), needs one extra verb (`reset` — working
  name; "pet" considered and set aside as cute-but-greppable-by-nobody),
  and is what lets /cron *monitor* xolu's own maintenance rather than
  merely perform it: the janitor gets a watchdog watching the janitor.
  It is also the shape that deserves the chime most — a bell that rings
  because nothing stopped it is the oldest alarm there is.
- **`at` and `after` are one-shot jobs** that terminate after their
  single tick (or after their missed-fire policy disposes of it). They
  exist so callers stop hand-rolling one-shot timers against /cal or
  in application code.

## 5. Per-def policies (declared at creation, guard-plane resident)

Following the T-55 pattern — policy read on every relevant operation
lives in the primitive's own plane, declared at creation, never in
/meta:

**Missed-fire policy** — the server was down (or the scheduler
suspended) across one or more due instants:

| Policy | Behaviour |
|---|---|
| `skip` | Missed ticks are recorded as suppressed; nothing chimes. |
| `fire_once` | Exactly one catch-up tick chimes on recovery, regardless of how many were missed. |
| `catch_up` | Every missed occurrence ticks and chimes, in order, at recovery. |

**Overlap policy** — the next tick comes due while work from the
previous chime's verb (§6) is still running:

| Policy | Behaviour |
|---|---|
| `skip` | The new tick is suppressed. |
| `queue` | The new tick waits; ticks execute serially. |
| `concurrent` | The new tick proceeds; the def owner accepts overlap. |

**Tick-record retention policy** — decided (2026-08-15, the team):
retention is **configurable per def**, because the two legitimate ends
of the spectrum are both real. Sometimes you need a compliance-grade
long log of nearly everything that happened; sometimes you need no more
than your log of daily backups. One system-wide setting cannot serve
both, and a def's retention need is a property of what the def does,
not of the installation. The retention *grammar* (age, count, or both)
is the remaining open sub-decision, §10. Trimming itself runs as a
system job under the `~` namespace (§8) — the scheduler maintains its
own history on its own clock, and the trim is therefore observable like
any other job rather than a silent internal behavior (see §13 for the
prior-art scar this deliberately avoids).

All three policies are per-def, all three are mandatory fields (no
defaults that encode a silent opinion), and all three operate on the
tick plane — which is why the tick must exist as a recorded fact even
when suppressed: the suppression *is* the policy working, and it must
be observable.

**Suppression streaks are a first-class observable, not a derived
query.** Postgres's autovacuum teaches this the hard way (§13.9): a
polite job that yields to foreground conflict every single time starves
silently, and the starvation is invisible until it is an emergency. A
job whose ticks keep being suppressed by overlap policy is exactly that
pattern wearing /cron's clothes. Therefore the job carries a running
consecutive-suppression count on its own plane, and the `watchdog`
shape (§4) can be armed against *completion* — "chime if job J has not
completed a verb in N" — so the system's answer to silent starvation is
its own absence-detector, not an external monitoring bolt-on. Whether
starvation should additionally *escalate* (a suppressed-too-long tick
overriding its own overlap policy, the way autovacuum's wraparound mode
ignores politeness) is deliberately open, §10.

## 6. Actions: the unconditional chime and the optional verb

An earlier fork posed emit-only (a job only emits an event; existing
event defs do all real work) against direct verbs (typed operations:
walk an /fsm machine, prune /ts rollups) as an either/or. Emit-only is
the elegant symmetry but inherits /events' delivery debt: dispatch today
is async fire-and-forget, and T-07 (durable dispatch, at-least-once) is
open. A clock that ticks into a bucket with a hole is not clockworks.

**Resolution: the chime is unconditional; the verb is optional.**

1. **Every non-suppressed tick emits `cron.chime`.** This is the audit
   trail and the observer hook, unconditionally, for every shape
   including the watchdog.
2. **A def MAY additionally attach one direct verb** that executes
   in-process on the same tick, against the primitives' own guarded
   write paths like any other caller.

The consequence that makes this worth its coupling cost: **T-07's
delivery hole affects who *hears about* maintenance, never whether
maintenance *happens*.** The janitor runs even if the webhook drops;
the chime record is exactly the thing T-07's eventual retry machinery
makes durable. /cron does not wait on the events debt to be honest.

The v1 verb set should be small and driven by §8's internal consumers,
not speculative: candidates are `fsm_walk` (machine id + input),
`ts_rollup_prune`, `bal_checkpoint_recompute`, and `oql` (a stored
query — note this overlaps the OQL-action target /events already
defines; reconcile at implementation rather than duplicating). Every
verb executes under the target primitive's own guards; /cron adds no
new guard surface and the clock is not a guard.

**The verb's outcome is a recorded fact on the tick.** SQL Server
Agent's `sp_start_job` returns success when the job *starts*, and
generations of automation checked that return value believing it meant
the job *succeeded* (§13.8) — an API that conflates arming, running,
and outcome manufactures wrong beliefs at scale. Here the tick record
carries the verb's outcome (succeeded / failed with the primitive's own
error / still running), so "did last night's prune actually run, and
did it work" is a query, never an inference. What this forces open is
the *ordering*: the chime marks "due and not suppressed" — does it
precede the verb, follow it carrying the outcome, or split into two
facts (a chime and a completion event)? Decided at implementation,
§10, but the requirement that the outcome exists as a fact is decided
here.

**A panicking verb is a recorded fact, never a crashed scheduler.**
Not hypothetical: `pkg/gc` (§13.10) shipped without this and paid for
it — a real type-assertion panic in a registered `Sweeper` took down
CI outright (T-156), fixed by wrapping every sweep in a `recover()`
that logs and returns a normal error instead (T-157, "we never want a
server to panic do we"). /cron inherits the lesson rather than
re-learning it: the scheduler recovers a panic from a tick's verb
execution the same way, records it as a distinct tick outcome
(`panicked`, not folded into `failed` — the stack trace is diagnostic
information a plain error string would lose), and the next tick fires
on schedule regardless. One misbehaving job must never take the clock
down with it.

**Named limitation — maintenance politeness.** Postgres runs autovacuum
under cost-based throttling because maintenance competing at full speed
with foreground work is a production incident, not a tuning preference
(§13.9). xolu has no resource governor, and SQLite's single-writer
model makes the gap sharper: a heavy prune verb *is* the foreground's
problem for as long as it holds the writer. v1 ships without a
politeness mechanism — deliberately, because inventing a resource
governor inside /cron is scope sprawl of exactly the kind §13.5
forbids — but the gap is named here and belongs in `KNOWN_ISSUES.md`
when /cron ships, so it is discovered by reading rather than by a
stalled tenant. The near-term mitigations are def-level: schedule heavy
verbs into quiet windows, and keep verbs incremental where the target
primitive allows it.

## 7. Ambient authority

Every write in xolu today traces to a request with a bearer. A
cron-driven write happens with nobody attached — the first ambient
authority in the system. Requirements:

- The job is a **first-class actor identity** in audit and event
  subjects: "cron job J (def D) tick at instant I" must be expressible
  wherever a principal is recorded today. This brushes against T-12
  (federation-consistent subjects and references), open at P5; /cron
  does not need T-12 resolved, but its actor-identity design must not
  contradict whatever T-12 eventually settles.
- A def's verb executes with an authority **granted at def time and
  frozen into the clone** (prototype-clone discipline applied to
  credentials: a def whose authority is later narrowed does not
  retroactively defang armed jobs — arming is the grant). Exact
  mechanism (scoped token vs. capability list) is an implementation
  decision, §10.
- The execution's identity does double duty: beyond audit, it is the
  **provenance key for reclamation** (§15) — writes attributed to an
  execution are what the sweeper can find again after that execution
  dies. An identity designed for audit alone would have been enough;
  an identity that also anchors cleanup must be present on the writes
  themselves, which raises the storage question §10 carries.

## 8. Dogfooding: system jobs and the ~ namespace

The strongest argument for building /cron at all is that xolu already
has scattered internal maintenance running ad hoc, each hand-rolling
its own timing:

| Existing maintenance | Today | Under /cron |
|---|---|---|
| dxp claim janitor (T-54's Janitor) | own goroutine | `every` job + `watchdog` watching it |
| Stale /bal checkpoint recompute (T-55) | lazy, on read | eager `every` job in quiet periods; lazy path remains the correctness backstop |
| /cal rollup prune (T-16) | unvalidated performance | `every` job, measurable because ticks are recorded |
| FTS backfill (T-75) | demand-triggered, manual | `after` or `at` job |
| blob GC, ts retention, fsm v2 collection, event delivery log v2 cleanup | `pkg/gc.Worker` (§13.10) — already `every`-shaped, already working | migrate as `~` jobs, "closer to a rename than new work" |

The last row is the strongest entry in this table, found only during
wave 6's own review (§13.10): four *already-shipped, already-working*
maintenance sweeps, not a proposed one. These become **system jobs** under the reserved `~` namespace, the
convention T-114 establishes for ~metering/~billing system stores.
xolu maintaining itself on its own clock is the "clockworks" claim made
literal — and it means /cron's first consumer exists before its first
external user, which is the healthiest way this project has shipped
primitives.

**System jobs ship in core, armed by default.** This is a ruling, not
a preference, and it is bought with Postgres's scar tissue (§13.9):
autovacuum began life as an optional external daemon, nobody ran it,
and databases bloated into transaction-wraparound emergencies until it
moved into core, on by default, in 8.1. Maintenance offered as an
option is maintenance not performed. xolu's `~` jobs are therefore not
an operator recipe in the RUNBOOK — they arm themselves at instance
startup with sane defaults, and an operator who wants them off must
say so explicitly, leaving a record.

## 9. Event-model integration

- `cron.chime` is **one new event type** added to `EVENT_MODEL.md`'s
  closed set — not four sibling types. The shape is discriminated in
  `event_latch_source`, following the /fsm precedent of
  coordinates-in-source: e.g. `cron/every/<def>` vs `cron/watchdog/<def>`
  (exact coordinate grammar at implementation).
- The chime's notification payload carries the resolved tick instant,
  the def and job identities, the shape, and — for catch-up ticks —
  the originally-due instant, so a consumer can distinguish "on time"
  from "late but honest."
- Suppressed ticks do **not** chime (by definition), but are recorded
  on the tick plane (§5); whether that record is queryable via a /cron
  endpoint only or additionally mirrored anywhere is open, §10.

## 10. Open decisions (deliberately undecided here)

1. **Scheduler scope: single-instance v1.** One process, one scheduler
   loop. Multi-instance ("who fires?") is fleet territory
   (`FLEET_ARCHITECTURE.md`) — leases/leadership are explicitly out of
   scope for v1 and must be named as such in the def semantics (a job
   is armed *on an instance*).
2. **Recurrence grammar for `every`.** Classic five-field cron
   expressions vs. a structured rule object. Calendar-aware schedules
   ("every business day") should lease /cal's vocabulary rather than
   reinvent it — which makes T-49 (calendar-period policy package) and
   T-50 (ISO week normalisation) prerequisites *for that tier of
   schedule only*, not for /cron core.
3. **DST policy enum** (§2): exact options and names.
4. **Authority mechanism** (§7): what is frozen into the clone.
5. **Tick-record retention grammar**: the policy-level decision is
   made — retention is configurable per def (§5). What remains is the
   grammar: age-based, count-based, or both; and whether trimmed ticks
   leave an aggregate rollup behind or vanish entirely.
6. **Verb/OQL-action reconciliation** (§6): one stored-query execution
   mechanism, not two.
7. **Eager-vs-lazy interaction with T-55**: the eager recompute job
   must compose with, never race, the lazy read-path recompute.
8. **Chime-verb ordering and the outcome fact's shape** (§6): the
   outcome-is-recorded requirement is decided; open is whether the
   chime precedes the verb, follows it carrying the outcome, or splits
   into two facts (chime + completion event) — and, if split, whether
   completion is a second event type or a second `event_latch_source`
   coordinate under `cron.chime`.
9. **Escalation axis** (§5): whether a starving job — one whose
   suppression streak crosses a threshold — may override its own
   overlap policy, the way autovacuum's wraparound mode ignores its
   own politeness. v1 can ship without it (the watchdog-on-completion
   makes starvation *visible*, which is the non-negotiable part);
   deciding whether visibility is enough is deferred until a real
   starving job exists to look at.
10. **Progress schema and phase vocabulary** (§14): the fields are
    decided (`phase?`, `done`, `total?`, `updated_at`) — open is
    whether `phase` is free-text per verb or a small shared enum, and
    whether verbs may report sub-progress for nested work.
11. **Stop-drain semantics** (§14): `stop` lets the running execution
    finish — open is whether the drain gets a deadline, and what the
    tick outcome records if the deadline passes on a non-cancellable
    verb (there is no third option: it either keeps running observed,
    or cancellation is delivered if the verb supports it).
12. **Attribution storage for the `attributed` reclamation class**
    (§15): where the execution identity lives on the written data —
    blob metadata is natural for /blob, but entities and primitive
    rows need a decision (a column is a guard-plane change; piggybacking
    on audit records couples reclamation to audit retention). Per
    primitive, at Reclaimer implementation time.

## 11. Dependency map

| Item | Relationship to /cron |
|---|---|
| T-07 (durable dispatch, at-least-once) | NOT a prerequisite, by §6's design — but the chime is the record T-07's machinery will make durable; build the chime payload with that in mind. |
| T-49 / T-50 (calendar-period policy, ISO weeks) | Prerequisites for calendar-aware `every` schedules only; /cron core does not wait on them. |
| T-12 (federation-consistent subjects) | Design-compatibility constraint on §7's actor identity; not a blocker. |
| T-55 (chronicle temporal policy) | The eager stale-checkpoint job (§8) builds on T-55's stale-marking; sequencing: T-55's remaining rulings first. |
| T-54 (dxp janitor) | First system-job consumer; no code dependency. |
| T-16, T-75, T-114 | Consumers (§8); T-114's ~ namespace convention is adopted, not extended. |

## 12. Not in scope

- Distributed scheduling, leases, leader election (fleet, later).
- Elapsed-time measurement (stopwatch — /ts and ~metering territory).
- Condition-polling monitors ("every N, evaluate predicate, fire if
  true"). Genuinely useful, genuinely a different animal; if wanted
  later it composes as an `every` job whose verb is an OQL evaluation
  whose *result* gates a second emission — build it above /cron, not
  into it.
- Per-tick policy overrides; retroactive policy migration.

## 13. Prior art: what twenty years of DBMS schedulers settled, and the one way they all went wrong

This section exists because the question "surely Oracle and SQL Server
solved this ages ago" is the right question, and the answer is
instructive in both directions. Every persistence system that lives
long enough grows a scheduler — Oracle's `DBMS_JOB` and then
`DBMS_SCHEDULER`, SQL Server Agent, MySQL's Event Scheduler, Postgres's
pg_cron. That is convergent evolution, and convergent evolution is
evidence the need is real. It is not evidence any one of them should be
copied. What follows is the scar tissue of the two most mature systems,
mapped onto this proposal's decisions — because nearly every hard-won
fix in their combined history is either already structural here or was
forced on this design by `TIME_HANDLING.md` before the comparison was
ever made, and the one failure they share is the one this proposal must
guard against permanently.

### 13.1 Oracle `DBMS_JOB` (Oracle 7 through 9i): the cautionary tale

`DBMS_JOB` — shipped with Oracle 7's job queue and the reigning
scheduler through 8i and 9i, the one people mean when they say "surely
Oracle solved
this" — is the cautionary tale, not the solution. A job was three
things: *what* (an anonymous PL/SQL block), *when next* (a date), and
*interval* — and the interval was not a rule but a **date expression,
evaluated at the end of the previous run**. `SYSDATE + 1/24` meant "one
hour after this run finishes," not "hourly." Every job drifted by its
own execution time, famously and unfixably; a nightly job that took
eleven minutes crept eleven minutes per night. The model has no concept
of an occurrence that exists independently of execution — no way to ask
"when was this *due*," only "when did it last run and what did the
expression yield."

This proposal is structurally immune, not carefully immune: ticks are
due-instants derived from the def's rule (§3, §5), and the tick/chime
plane split means completion-anchored scheduling is not merely
discouraged but inexpressible. There is no field in which to write
`SYSDATE + 1/24`.

### 13.2 Oracle `DBMS_SCHEDULER` (10g onward): the good one, and its lessons

Oracle replaced `DBMS_JOB` with `DBMS_SCHEDULER` in 10g, and its design
reads as a point-by-point repudiation of its predecessor. Four lessons
transfer:

**The calendaring syntax.** `FREQ=DAILY; BYHOUR=9; BYMINUTE=0` — a
declarative recurrence *rule*, evaluated against the calendar, existing
precisely because interval expressions drifted. This is the strongest
single precedent for §10's recurrence-grammar decision: a structured
rule object over five-field cron strings, because Oracle tried
expression-based scheduling at full production scale and retired it.

**The decomposition, and where it goes wrong.** `DBMS_SCHEDULER`
splits Schedule (a named, reusable when), Program (a named, reusable
what), and Job (the binding of the two) — the same prototype instinct
as def → job, validated at scale. But Oracle's named schedules and
programs are **shared and mutable**: alter one schedule and every job
referencing it changes behavior at its next evaluation. That is
exactly the class-mutation problem this proposal's clone semantics
exists to make impossible by construction (§1). The decomposition is
right; the sharing is the part this design deliberately rejects.

**Credentials.** External jobs in `DBMS_SCHEDULER` run under named
credential objects — an explicit grant, created and audited separately,
rather than authority resolved implicitly at fire time. Oracle arrived
at frozen, first-class credentials for scheduled work after the earlier
model's ambiguity; §7's authority-frozen-into-the-clone is the same
conclusion with the prototype discipline extended to the grant itself.

**Scheduler events.** `DBMS_SCHEDULER` raises `job_started`,
`job_succeeded`, `job_failed` and kin as messages into Advanced
Queuing — scheduler occurrences as first-class events delivered into
the database's existing messaging plane. §6's unconditional chime is
this idea, independently reinvented and then found in the prior art:
the strongest kind of validation.

And one lesson in the negative: `DBMS_SCHEDULER` also grew **chains**
(full DAG workflows with per-step rules), maintenance **windows**,
resource-manager integration, and remote external jobs. See §13.4.

### 13.3 SQL Server Agent: four scars, each mapped

**History trimming.** Agent's job history lives in `msdb` and is
trimmed by default to 1,000 rows total and 100 per job — a famous
operational gotcha in which the evidence vanishes exactly when an
incident makes it needed, silently, because the default encodes an
opinion nobody consented to. This scar is why §5's retention policy is
a *mandatory per-def field with no default*, why the spectrum runs
explicitly from compliance-grade to minimal (decided 2026-08-15), and
why the trim runs as an observable `~` system job rather than an
internal behavior: the deletion of history is itself history.

**Server-local time.** Agent schedules evaluate in the server's local
time zone; the DST-boundary bug genre this produces is documented,
recurring, and structural. §2's mandatory explicit zone and per-def DST
policy — forced by `TIME_HANDLING.md`'s own invariant before any prior
art was consulted — is the fix Agent never shipped.

**Ownership and execution identity.** Agent jobs execute as their
owner or via proxy accounts; ownership is mutable after creation, and
the sysadmin-owned-job pattern carries a long security history. The
lesson matches Oracle's credential conclusion from the opposite
direction: authority for scheduled work must be an explicit, frozen,
auditable grant (§7), never a property that can be quietly re-pointed
after arming.

**Nobody watches the watcher.** Agent is a separate service; when it
is down, every job is silently not running, and nothing inside the
system notices — the monitoring for this is conventionally bolted on
outside. Neither Oracle nor SQL Server has, as a primitive, a shape
that fires on *absence*. The `watchdog` (§4) is this proposal's answer,
and it is the one place this design is genuinely ahead of the prior art
rather than converging with it: the janitor gets a watchdog watching
the janitor, and /cron's own scheduler loop can be watched by the same
mechanism it provides.

### 13.4 The shared failure: sprawl, and why it happened

Both systems became workflow engines, and both are worse for it. Agent
grew multi-step jobs where each step carries retry counts and
on-success/on-failure *goto* targets — every nontrivial Agent job is a
small unstructured BASIC program, written in a dialog box, versioned
nowhere. `DBMS_SCHEDULER` grew chains: full DAGs with step conditions
and rules, a workflow language grafted onto a clock.

The diagnosis matters more than the symptom: in both systems the
scheduler was the **only programmable automation surface inside the
database**. Every automation need that appeared over two decades had
nowhere else to land, so each was bolted onto the scheduler — steps
because there was no state-machine primitive, chains because there was
no composition primitive, alerting hooks because there was no event
primitive. The sprawl was not a design failure so much as the absence
of neighbors.

### 13.5 Why xolu is structurally exempt — stated as doctrine

xolu does not have that pressure, and this is the strongest argument
for §6's one-verb-per-def rule being permanent rather than provisional:

- Agent's step-goto logic is a poor man's state machine built by people
  who did not have /fsm. Multi-step work in xolu is an /fsm machine,
  chimed into motion.
- Oracle's chains are composition built by people who did not have
  /events. Fan-out, sequencing-by-reaction, and conditional follow-on
  belong to event defs consuming `cron.chime`.
- Cross-primitive atomicity in scheduled work belongs to /dxp, exactly
  as it does in request-driven work.

**The anti-sprawl rule:** a def carries at most one verb, forever. The
moment a request arrives for "on success, run step 2" — and the prior
art guarantees it will arrive — the correct answer is not a second verb
slot but an /fsm walk as the verb, with the machine owning the steps.
/cron can stay the size of a clock precisely because the composition
machinery its predecessors were forced to grow already exists as
neighbors. Oracle and SQL Server jointly prove two things: the clock is
needed, and what the clock turns into when it is the only machine in
the room. This proposal takes both proofs seriously.

### 13.6 Scar-to-section map

| Prior-art scar | System | Where this proposal answers it |
|---|---|---|
| Interval expressions anchored to completion; drift | `DBMS_JOB` | §3/§5: ticks are rule-derived due-instants; completion-anchoring inexpressible |
| Expression-based recurrence retired for calendaring syntax | Oracle 10g | §10: structured rule object favored for `every` |
| Shared-mutable named schedules; class-mutation on edit | `DBMS_SCHEDULER` | §1: clone semantics; mutation of a def cannot reach armed jobs |
| Scheduler state in a system database (`msdb`) | SQL Server Agent | §8: `~` namespace system jobs |
| History trimmed by silent default; evidence vanishes | SQL Server Agent | §5: retention mandatory, per-def, no default; trim is an observable system job |
| Server-local-time schedules; DST bug genre | SQL Server Agent | §2: mandatory explicit zone, per-def DST policy |
| Mutable job ownership; implicit fire-time authority | Both | §7: authority frozen into the clone at arming |
| Scheduler-down means silent universal failure | Both | §4: the `watchdog` shape; the scheduler is itself watchable |
| Scheduler occurrences as events into the messaging plane | `DBMS_SCHEDULER` → AQ | §6: the unconditional chime (independently converged) |
| Steps, gotos, chains: the scheduler becomes a workflow engine | Both | §13.5: one verb per def, forever; /fsm, /events, /dxp are the neighbors |
| Queryable past parasitic on rollback undo; `ORA-01555` | Oracle Flashback | chronicle (outside /cron): the journal is authoritative, the present is derived — §13.7 |
| One system-wide `undo_retention` horizon for every application's past | Oracle 9i | §5: retention per def; Oracle itself converged there with 11g's per-archive Flashback Data Archive — §13.7 |
| `datetime` rounds to 3.33ms; type-level debt survives its successor | SQL Server | §13.8: the wire surface is one representation — RFC3339 UTC instants — forever |
| `datetimeoffset` stores an offset, not a zone; cannot resolve future DST | SQL Server | §2: the zone field is an IANA zone name, never an offset |
| `sp_start_job` returns "started"; callers read it as "succeeded" | SQL Server Agent | §6: the verb's outcome is a recorded fact on the tick |
| Per-table `HISTORY_RETENTION_PERIOD` (2017) | SQL Server temporal tables | §5: third independent convergence on per-group retention — with Oracle FDA and this proposal, it is a law now |
| Optional maintenance is unperformed maintenance (`pg_autovacuum` → core 8.1) | Postgres | §8: `~` system jobs ship in core, armed by default |
| A polite job cancelled every time starves silently until emergency | Postgres autovacuum | §5: suppression streaks first-class observable; watchdog-on-completion; escalation open, §10 |
| Force-kill possible only because jobs ran in slave OS processes | Oracle `STOP_JOB(FORCE)`, Agent `sp_stop_job` | §14: in-process goroutines cannot be killed; capabilities introspected and declared, `kill` refuses on non-cancellable verbs |
| Progress as shared-memory counters read on demand, never persisted | Postgres `pg_stat_progress_*` | §14: execution progress is memory-only; the tick persists only the outcome |
| Dead tuples reclaimed by joining tuple provenance (xmin/xmax) against the commit log | Postgres VACUUM | §15: garbage = provenance join of attributed writes against /cron's death list |
| Half-finished multipart uploads reaped by staging-TTL lifecycle rule | S3 `AbortIncompleteMultipartUpload` | §15: the `staged` reclamation class; directly relevant to /blob's S3-compat surface |
| Double-`Start` silently launched a second goroutine; crashed later, far from the cause | `pkg/gc.Worker` (this codebase, T-140-family) | §13.10: lifecycle transitions (arm/pause/resume/stop) must fail fast on misuse, never silently race |
| A panicking sweep took down CI outright, until wrapped in `recover()` | `pkg/gc.Worker.RunOnce` (this codebase, T-156/T-157) | §6: a panicking verb is a recorded tick outcome, never a crashed scheduler |

### 13.7 The other Oracle lesson: `AS OF`, undo, and why chronicle owns its past

This subsection reaches beyond /cron's scope on purpose. The prior art
above is about scheduling the future; Oracle's other famous time
machinery — Flashback Query, `SELECT ... AS OF` — is about reading the
past, and it is prior art for two things this proposal leans on:
chronicle (the substrate under /ts, /cal, and /bal's as-of reads, T-55's
territory) and §5's per-def retention ruling. Both have exact Oracle
ancestors, and in both cases Oracle's own later trajectory concedes the
point.

**What `AS OF` was.** Flashback Query arrived with 9i — first as the
`DBMS_FLASHBACK` package, then as SQL's own `AS OF` clause — and it was
a brilliant *parasite*. It did not store history; it reconstructed
consistent past images out of **undo segments that existed for
rollback**. The queryable past was a side effect of transaction
plumbing. That parasitism dictated both of its famous limits: the
horizon was whatever `undo_retention` (9i's automatic undo management)
happened to keep, and the failure mode was `ORA-01555: snapshot too
old` — the past you were reading could be recycled out from under you
by unrelated write pressure, because the undo you needed belonged to
rollback first and to you never. The past was best-effort, and
best-effort as a *structural property*, not a tuning oversight.

**Oracle's own two concessions.** The trajectory that followed is the
argument:

1. **`RETENTION GUARANTEE` (10g).** Oracle acknowledged that a
   best-effort past was not enough, and shipped a mode that preserves
   undo needed by queries even at the cost of **failing current DML**
   when undo space runs out. Read that trade carefully: to make the
   past reliable inside the parasitic model, Oracle had to be willing
   to sacrifice the present. That is what happens when history is a
   tenant in someone else's storage.
2. **Flashback Data Archive (11g, "Total Recall").** The full
   concession: a **dedicated, durable archive of row history, separate
   from undo entirely**, written as a first-class record — and with
   **retention configured per archive**, tables assigned to archives
   according to how much past each is worth. Oracle's endpoint, after
   a decade, was authoritative history with per-group retention.

**Chronicle starts where that trajectory ends.** The journal is the
authoritative plane and the present is *derived* from it — checkpoints
are caches of the journal's sum, verifiable by the T-55 oracle prong
(`checkpoint.balance == SUM(journal WHERE at <= checkpoint.at)`), and
an as-of read that finds a stale checkpoint recomputes from the record
rather than failing. The past cannot be "too old," because the past is
the primary record, not exhaust fumes from rollback plumbing. There is
no `ORA-01555` in a system where deleting history is itself a recorded,
policy-governed act (§5's trim job) rather than an invisible recycling.

**And the retention rhyme.** `undo_retention` was the tick-retention
decision made *system-wide*: one knob, one horizon, every application
on the instance sharing a single reach into the past, set by a DBA as
a storage negotiation. §5's ruling — retention configurable per def,
because a compliance log and a backup log have different pasts worth
keeping — is the same principle at the granularity Oracle itself
eventually adopted in Flashback Data Archive's per-archive retention.
Arrived at here from first principles, then found in the prior art:
the same double-confirmation pattern as the chime (§13.2). The
principle both systems agree on, stated once: **you can only
time-travel as far as you paid to keep — so let each def sign its own
lease.**

### 13.8 SQL Server's time realm: the type zoo and the half of SQL:2011 they shipped

Where §13.3 mined Agent (SQL Server's scheduler), this subsection mines
SQL Server's *representation* of time — a realm where the scars are
type-level, which makes them the most instructive kind: a scheduling
bug ships in a patch, but a time type, once persisted, is forever.

**What: `datetime` rounds to 3.33 milliseconds.** The original type
stores time in 1/300-second ticks against a 1900 epoch, so every value
rounds to a `.000`/`.003`/`.007` boundary. **Why it matters:** the
equality-comparison footgun this manufactured is legendary — a value
round-trips through a client library and no longer equals itself — and
when 2008's `datetime2` fixed the precision, the old type did not die;
it survives in production schemas to this day, because persisted
representation debt cannot be paid off, only inherited around. **How
we answer:** the doctrine's single `Instant` already prevents the
internal version of this; the transferable rule is about the *wire*:
/cron's API carries exactly one time representation — RFC3339 UTC
instants — on every field, in every direction, from v1, because the
wire surface is the one place a second representation could still
creep in and become permanent.

**What: `datetimeoffset` stores an offset, not a zone.** **Why it
matters:** `+02:00` is a frozen reading of a clock; `America/Montevideo`
is the rulebook that produces readings, DST transitions and future
rule changes included. A stored offset answers "what did the clock say
then" and is *structurally unable* to answer "when is 9 a.m. there
next March." SQL Server named the type as if it were zone-awareness,
and half the world's schemas absorbed the confusion. **How we answer:**
§2 now states the rule /cron lives by — the def's zone field is an
IANA zone name, never an offset — because a scheduler is precisely the
component where the reading/rulebook distinction stops being pedantry
and starts being whether the job fires at the right hour after the
clocks change.

**What: Agent encodes times as decimal integers.** `active_start_time
= 90000` means 09:00:00 — HHMMSS as an integer, in `msdb`, with the
arithmetic and zero-padding bugs that representation guarantees. **Why
it matters:** it is the cheapest scar in this whole section to avoid
and among the most embarrassing to acquire. **How we answer:** a
one-line permanent rule — no integer-encoded times anywhere in /cron's
storage or API. Instants are instants; rules are structured rule
objects.

**What: `sp_start_job` returns success when the job starts.** **Why it
matters:** the API conflates three different facts — armed, running,
succeeded — and generations of automation checked the return value
believing it meant the last when it meant the first. An interface that
manufactures a wrong belief at scale is worse than one that answers
nothing. **How we answer:** §6's new ruling — the verb's outcome is a
recorded fact on the tick record, queryable, never inferable — with
the chime/outcome ordering held open as §10 item 8.

**What: temporal tables (2016) shipped half of SQL:2011.** The
standard defines two independent time axes — **system time** (when the
database knew a fact) and **application time**, also called valid time
(when the fact was true in the world) — and full bitemporality is
their product. SQL Server shipped system-versioning only. **Why it
matters to us — and it is T-55's gain, not /cron's:** the standard's
vocabulary names chronicle's policy enum exactly. `append_only` is the
discipline "valid time must track system time" (the world's order and
the record's order agree); `backdated` is "valid time runs free of
system time" (facts may arrive after, even long after, they were
true). Two further Agent-era details transfer: the temporal period is
stamped with the *transaction start time*, so every row versioned in
one transaction shares an instant and within-transaction order is
invisible in history — an independent, second-system confirmation of
T-55's recorded deviation that same-instant entries must be admissible
under `append_only`, because batches legitimately share instants. And
2017's `HISTORY_RETENTION_PERIOD` is configured **per table**: the
third independent convergence on per-group retention, after Oracle's
per-archive Flashback Data Archive and this proposal's §5 ruling.
Three arrivals from three directions is no longer a design choice; it
is a law of the domain. **How we answer:** a vocabulary note is filed
on T-55 (terminology only, no scope change), and the retention row
joins the scar map.

### 13.9 Postgres: the scheduler it refused, and the one it hid in the walls

Postgres spent thirty years declining to ship a general scheduler, and
the ecosystem answer — pg_cron, pgAgent, pg_timetable, plain OS cron —
fragmented exactly as unowned needs do. That refusal is itself minor
prior art (a substrate that ships no clock forces every consumer to
bring their own, badly). But the deep lessons come from the scheduler
Postgres *did* ship without calling it one: **autovacuum**, in core,
load-bearing, and carrying the best-documented maintenance-scheduling
scars in the industry.

**What: optional maintenance went unperformed.** Autovacuum began life
as `pg_autovacuum`, an external contrib daemon in the 7.4 era. Vacuum
was essential — Postgres's MVCC bloats without it and transaction-ID
wraparound is an existential failure, not a degradation — and yet, as
an optional add-on, it simply was not run. Databases bloated; XID
wraparound emergencies happened in production. It moved into core in
8.1, enabled by default, and the failure class largely vanished. **Why
it matters:** this is the cleanest natural experiment ever run on the
question "can maintenance be an operator's opt-in?" and the answer was
no, at scale, with data loss on the line. **How we answer:** §8's new
ruling — `~` system jobs ship in core and arm themselves at instance
startup; disabling one is an explicit, recorded act. The RUNBOOK
documents them; it does not *constitute* them.

**What: a polite job can starve to death, silently.** Autovacuum
yields by design — cost-based throttling, and self-cancellation when
it conflicts with a foreground lock request. The canonical failure: a
hot table whose autovacuum is cancelled every single attempt, forever,
invisible until the deferred work matures into a wraparound emergency
shutdown. Postgres's own answer has two prongs: the starvation
eventually becomes *visible* (monitoring views, log lines), and the
existential case *escalates* — the anti-wraparound autovacuum ignores
the politeness rules that starved its predecessors. **Why it matters:**
overlap-policy `skip` is /cron's politeness, and a job whose every
tick is suppressed is autovacuum-being-cancelled wearing our
vocabulary. **How we answer:** §5's new ruling makes the suppression
streak a first-class observable on the job's own plane, and — the
structurally satisfying part — the `watchdog` shape closes the loop
from inside: arm a watchdog against a job's *completion* and the
system detects its own starvation by the same absence-mechanism it
offers everyone else. Whether visibility must be joined by escalation
(a starving job overriding its own overlap policy, as anti-wraparound
does) is §10 item 9 — deliberately deferred until a real starving job
exists to look at, because Postgres also teaches that escalation
mechanisms designed speculatively tend to escalate the wrong thing.

**What: maintenance needed a politeness mechanism.** Cost-based
throttling exists because early vacuum competing at full I/O speed
with foreground queries was itself a production incident — the cure
degrading the patient. **Why it matters here, sharpened:** xolu has no
resource governor at all, and SQLite's single-writer model concentrates
the risk — a heavy prune verb does not merely *compete* with foreground
writes, it *excludes* them for as long as it holds the writer. **How
we answer:** honestly — v1 does not solve this, because building a
resource governor inside /cron is exactly the sprawl §13.5 forbids.
The gap is named in §6 and goes to `KNOWN_ISSUES.md` at ship time; the
def-level mitigations (quiet-window scheduling, incremental verbs) are
real but partial. This is the one scar in the whole prior-art section
we choose to carry knowingly rather than answer structurally.

**What: the most successful maintenance job ever built is a
condition-poller.** Autovacuum is not schedule-triggered. It is
`autovacuum_naptime` — wake every N seconds — plus a threshold
predicate over dead-tuple statistics deciding whether there is work.
**Why it matters:** that is *exactly* the shape §12 excludes from
/cron core and prescribes as composition — an `every` tick whose verb
evaluates a condition that gates the real work. The industry's most
battle-proven maintenance scheduler is an existence proof that the
condition-poller composes cleanly *above* a plain clock and never
needed to be *inside* one. **How we answer:** no change — §12's scope
line stands, now with its strongest supporting citation.

Three smaller Postgres artifacts, each one sentence of what/why/how:
**`timestamptz` stores no timezone** — the name lies; it is a UTC
instant converted on display, the exact sibling of the footgun
`TIME_HANDLING.md` already refuses, and it confirms the doctrine
rather than amending it. **The `interval` type keeps months, days,
and seconds as three separate fields** — because a day is 23, 24, or
25 hours across DST transitions, "every day" is not "every 86,400
seconds," and Postgres encoding that distinction *in the type system*
is the proof that recurrence arithmetic is calendar arithmetic,
belonging in /cron (leaning on /cal for the calendar-aware tier, §10)
and never in `xolutime`, whose boundary section already forbids it.
**`now()` is transaction-frozen while `clock_timestamp()` advances** —
every system eventually answers "which now?" explicitly; /cron's
answer is that a tick's due-instant comes from the rule and the
observation instant from `xolutime.Now()`, and the two are different
fields because they are different facts.

### 13.10 xolu's own proto-scheduler: `pkg/gc`, found during the wave-6 backup review

Every scar so far came from outside this codebase. This one didn't —
found while reviewing `pkg/tenantexport`'s TTL sweep for wave-6 reuse,
which turned out to be registered as a `Sweeper` on a `gc.Worker`.
`pkg/gc` is a small, generic, already-in-production scheduler: a
`Sweeper` interface (`Sweep(ctx) (Report, error)`), a `Worker` that
runs one on a fixed `time.Ticker` interval, and four real subsystems
already registered on it today — blob GC, timeseries retention, FSM
machine collection (v2), and event delivery log cleanup (v2). It is
authoritative prior art in a way the DBMS schedulers above aren't:
it's *this* codebase, its incidents are *this* project's incidents, and
its four consumers are /cron's most concrete dogfooding targets, more
concrete than §8's own janitor-migration story.

**What it validates, independently converged on before this was
found:** `Report{Examined, Collected, Quarantined, Errors, Duration}`
is close to the tick-outcome shape this proposal built from scratch —
the third instance in this document of a design arrived at
independently and then found already proven (after the chime/AQ
convergence, §13.2, and the retention-per-group convergence, §13.7).
`LastReport()` exposing the most recent sweep's outcome is `info`
(§14.2) in miniature. The admin surface (`POST
/admin/gc/{name}/run`, `GET /admin/gc`) is a manual-trigger-plus-status
pattern this proposal reinvents as `kill`/`info` at a different scope.

**What it teaches that this proposal didn't already have — two real
incidents, not hypotheticals:**

1. **`Worker.Start()` called twice panics, on purpose, because it
   used to fail silently instead.** The original bug: a second `Start`
   silently launched a second `run()` goroutine, and the eventual
   symptom was a `close of closed channel` panic in a *different*
   goroutine's deferred cleanup — a crash "far from the call that
   caused it," in the fix's own words, and the same lifecycle-unsafety
   family as `pkg/server`'s own pre-T-140 Start/Shutdown race. The
   fix made a double-`Start` fail loudly and immediately instead of
   corrupting state quietly. Direct lesson for the scheduler loop
   (§10 item 2's `at`/`after`/`every`/`watchdog` dispatch): job
   lifecycle transitions (arm, pause, resume, stop, kill) need the
   same discipline — a double-arm or a resume-of-a-non-paused-job
   should fail fast, not silently launch a second goroutine racing the
   first.
2. **A panicking `Sweep` took down CI outright, until it didn't.**
   T-156 found a real type-assertion panic in a registered `Sweeper`;
   T-157 wrapped `RunOnce` in a `recover()` that logs the stack trace
   and returns a normal error, on the team's own direct framing — "we
   never want a server to panic do we." §6 now carries this
   requirement directly rather than as an open question, because it
   already cost a real incident once and there is no reason to pay
   for it twice.

**What it does NOT give /cron, stated plainly so the reuse claim isn't
oversold:** one fixed interval per named worker, no `defs`/`jobs`/
clone semantics, no shape beyond "run every N," no missed-fire or
overlap policy, no zones or DST, no chime into `/events`. The harder
half of /cron's design — the recurrence rule, DST policy, the
tick/chime plane split, multi-shape dispatch — has no `pkg/gc`
analogue and gains nothing from it. What transfers is the *lifecycle
safety shell* around whatever runs inside it: the ticker-and-stop-
channel shape, the double-start guard, the per-execution panic
recovery. That's real, proven-in-production reuse, not aspirational —
and it is why §16.2's scheduler-loop estimate is revised downward
(§16.3), while the recurrence-rule estimate is not.

**The dogfooding upgrade this earns (§8):** `pkg/gc`'s four existing
consumers — blob GC, timeseries retention, FSM machine collection,
event delivery log cleanup — are a stronger migration target than the
hypothetical eager-recompute job §8 proposed. All four are `every`-
shaped, all four already have a working `Sweeper`, and migrating them
onto `/cron` as `~` system jobs would be closer to a rename than new
work — the single clearest, lowest-risk proof that /cron is worth its
cost, found only because wave 6's own review looked at the code
`/cron` will eventually replace.

## 14. Job control and execution observability

A running clock needs a hand on it. This section adds the control and
observability surface — and it begins with a plane split, because the
verbs people reach for ("pause it", "kill it") do not act on the same
object, and an API that pretends they do will conflate facts the way
`sp_start_job` did (§13.8).

### 14.1 The third object: the execution

The vocabulary (§3) gains one row:

| Term | Plane | Meaning |
|---|---|---|
| **execution** | execution | One tick's verb, actually running: a goroutine with a start instant, live progress (if the verb reports it), and eventually an outcome that persists to the tick (§6). |

Job control divides along the planes: **pause, resume, and stop act on
the job** (whether ticks are produced); **kill acts on the execution**
(whether the currently running verb continues); **info reads across
all three planes** (job state, live execution, recent tick history).
The photo-copy progress bar the motivating example asks for belongs to
an execution, never to the job — the job is the standing arrangement,
the execution is tonight's work.

### 14.2 The surface

| Endpoint | Plane | Semantics |
|---|---|---|
| `GET /cron/job/{id}/info` | all | Job state and policies; capabilities of its verb (§14.4); the live execution if one is running (started-at, progress); recent ticks with outcomes, suppression streak included (§5). |
| `POST /cron/job/{id}/pause` | job | Stop producing ticks. The job remains armed; due instants continue to pass. |
| `POST /cron/job/{id}/resume` | job | Resume ticking — and hand the due instants that accumulated while paused to the job's **own missed-fire policy** (§5). A paused job whose due instants passed is indistinguishable from a downed scheduler whose due instants passed, so pause/resume introduces **no new policy**: `skip`, `fire_once`, or `catch_up` mean exactly what the def author already declared. |
| `POST /cron/job/{id}/stop` | job | Graceful and terminal: no further ticks, the running execution (if any) drains to completion, the job ends in a `stopped` terminal state. Restarting is cloning the def again — jobs are cheap clones (§1), and a terminal stop is cleaner than a resurrectable one. Drain-deadline semantics open, §10. |
| `POST /cron/job/{id}/kill` | execution | Deliver cancellation to the running execution **if its verb is cancellable** (§14.4). The execution's tick records a cancelled outcome. Against a non-cancellable verb: an honest refusal (its own error code), never a pretend-kill. No effect on the job's arming — killing tonight's copy does not unschedule tomorrow's. Kill does **not** roll back the verb's partial writes; what becomes of them is the def's reclamation class, §15. |

Pause is not stop and stop is not kill: pause expects resumption, stop
expects an orderly end, kill expects an emergency. The one deliberate
asymmetry: there is no `restart` — prototype-clone discipline makes
re-cloning the def the restart, keeping the job's lifecycle a straight
line (armed → [paused ⇄ resumed] → stopped) with no cycles.

### 14.3 Progress is memory-only, and the prior art says so

Persisting every progress update would be write amplification in
service of a fact that is stale the moment it is written. Postgres
settled this shape: the `pg_stat_progress_*` views (vacuum,
create-index, basebackup) are **shared-memory counters read on
demand** — phase plus done/total — never persisted, gone at crash,
authoritative only about *now*. /cron adopts the same split:

- **Live execution state lives in memory**, keyed by job: `phase?`
  (optional label for multi-stage verbs), `done`, `total?` (nullable —
  indeterminate work is legitimate and must be representable),
  `updated_at`. `info` reads it; nothing writes it to storage.
- **Only the outcome persists**, to the tick, per §6's ruling.
- **Crash loses progress and keeps the fact.** The in-memory state
  vanishes; the tick records the interruption. This is T-54's
  crash-means-abandon doctrine applying to its second ephemeral —
  reservations and progress are the same kind of citizen: real,
  memory-resident, never resumed, honestly accounted for after death.

Composition, not mechanism: a reporting verb plus §5's
watchdog-on-completion already yields **stall detection** — arm a
watchdog against progress advancing ("chime if `done` hasn't moved in
N") and the system notices a wedged copy by the same absence-detector
it offers everyone else. No new machinery.

### 14.4 Capability honesty: the four classes of verb

The motivating sketch — "configurable goroutines who take a channel to
respond, and others are i-don't-report-you-wait-pray-and-die" — is two
independent axes, giving four classes:

| | Reports progress | Opaque |
|---|---|---|
| **Cancellable** | The full citizen: progress bar and a working kill. | Kill works; you just can't see how far it got. |
| **Non-cancellable** | You can watch it; you cannot stop it. | The pray-and-die class: `info` shows it exists and when it started, nothing more. |

Rules, all three load-bearing:

1. **Capabilities are introspected, never asserted.** The verb's Go
   type either implements the optional interfaces (cancellation via
   context; progress via a reporter supplied in the execution's
   parameters) or it does not. Discovery happens by interface
   assertion at def registration and is recorded on the def; a def
   author cannot claim a capability the code does not have. "It's good
   to know what to expect" becomes a queryable fact of the def, not a
   hope.
2. **`kill` refuses honestly on the non-cancellable classes.** Go
   cannot kill a goroutine; cancellation is cooperative via context,
   and there is no force option to fall back on. Oracle's
   `STOP_JOB(FORCE=>TRUE)` and Agent's `sp_stop_job` could genuinely
   terminate work because their jobs ran in slave OS processes — a
   hammer an in-process scheduler does not own. An API that returns
   "killed" while the goroutine runs on — possibly holding SQLite's
   single writer (§6's named politeness limitation, now with a second
   reason to exist) — manufactures exactly the wrong-belief-at-scale
   failure §13.8 condemns in `sp_start_job`. The refusal is the honest
   answer, and the def-time capability record means nobody armed a
   pray-and-die verb without being told.
3. **Existence is always observable, even when progress is not.** The
   opaque classes still surface `started_at` and `running` in `info`;
   the floor of observability is never zero. What varies by class is
   depth, and the class is knowable before arming.

### 14.5 Scope check against §13.5

Job control quietly widens /cron's role: the one-shot shapes (`at`,
`after`) carrying a heavy, progress-reporting verb make /cron xolu's
general async-task surface, not only its clock — the motivating
example (copy a ton of photos to /blob, once, with a progress bar) is
not recurring at all. Checked against the sprawl doctrine and passed:
§13.5's sprawl was *workflow logic* — steps, chains, conditions —
grafted onto a scheduler because nothing else could host it.
Lifecycle control of work the clock itself started is a different
thing, and the prior art agrees: every scheduler in §13 grew stop and
status verbs, legitimately, while only some grew workflow, ruinously.
The line holds: /cron may manage the executions it launches; it may
never sequence them. What follows an execution is /fsm's and /events'
business, chimed into motion.

## 15. Reclamation: sweeping the estates of dead executions

A killed, crashed, or drain-expired execution can leave partial work
behind — half of a large photo set copied to /blob is the motivating
case. This section is the answer to "could we have a /cron sweeper,"
and the answer is yes — built as a composition of things this proposal
already has, plus one new contract, rather than as a /cron subsystem
that understands other primitives' data. It never could understand it,
for a reason worth stating as the section's first principle.

### 15.1 Garbage is intent-relative

The half-copied photos are, to /blob, perfectly valid blobs: every
byte checksummed, every write guarded, nothing about them marked
wrong. **Garbage is not a property of the data; it is data minus a
living intent.** Two consequences structure everything below:

1. **Partial work of a live job is progress, not garbage.** An
   idempotent copy verb re-runs, skips what is done, and finishes;
   killing tonight's execution and letting tomorrow's tick complete
   the work is a feature. Garbage crystallizes only when the intent is
   terminally dead — job stopped, or a one-shot's execution ended
   without success and nothing will retry it.
2. **Identifying garbage requires a join between two parties.** Only
   the primitive knows *what was written*; only /cron knows *which
   intents died* (it owns the tick records and job states). Neither
   can identify garbage alone — so the sweeper is structurally a join,
   and the design question is what each side must record to make the
   join computable.

### 15.2 The two proven mechanisms

The join has exact prior art, and it is already this document's star
witness. **Postgres VACUUM is precisely this mechanism**: every tuple
carries provenance (`xmin`/`xmax` transaction ids), the commit log is
the death list, and vacuum reclaims by joining the two — dead tuples
are the estates of misfortuned transactions, found because writes were
attributed at write time. The second mechanism is dumber and equally
proven: **S3's `AbortIncompleteMultipartUpload` lifecycle rule** reaps
half-finished multipart uploads — literally the half-copied-photos
case, on the same S3-compatible surface /blob already speaks — by
staging-TTL: staged-but-never-committed *is* the garbage marker, no
provenance needed. Two mechanisms, decades-proven between them:
**provenance-join** and **staging-TTL**. The design below offers both
and forces neither.

### 15.3 The design: reclamation classes, Reclaimers, and ~sweep jobs

**Every def declares a reclamation class** — the same
declared-capability discipline as §14.4, applied to what the verb
leaves behind:

| Class | Meaning | Sweep mechanism |
|---|---|---|
| `atomic` | The verb runs under /dxp; a dead execution left nothing by construction (T-54's crash-abandon). | None needed. |
| `idempotent` | Partial work is progress; a re-run completes it. | Sweep only after the *job* is terminal, and then as `attributed` or `staged` per the def. |
| `staged` | Writes land in a staging area; success promotes them in one act. | TTL over staging: anything staged longer than the def's horizon is reaped. |
| `attributed` | Every write carries the execution's identity (§7's actor identity doing its second job). | The provenance join: reclaim writes attributed to executions on /cron's death list. |
| `none` | The honest leaker: the verb declares that its partial work is unswept. | None — legal, declared, and visible in `info`, exactly as §14.4's pray-and-die class is. |

**Primitives that participate implement a Reclaimer** — a small
interface in the dxp Participant mold: given the def's class and, for
`attributed`, a set of dead execution identities, find and reclaim the
leftovers under the primitive's own guards. A primitive with no
Reclaimer simply cannot host `staged`/`attributed` defs, and def
registration says so upfront.

**The sweeps themselves are `~sweep` system jobs** — per category or
per primitive, which is the natural reading of "groups or categories"
— in core and armed by default per §8's ruling, observable and
controllable like any job (§14), their own outcomes recorded on their
own ticks. There is no `/cron/sweeper` endpoint because there is no
sweeper *object*: there are sweep jobs, and `info` already exists.

**/cron's own contribution is the death list**: the set of execution
identities whose outcome is dead (killed, failed, drain-expired,
crash-interrupted) and whose intent will not resume (job terminal, or
one-shot concluded). This is a query over tick records — facts /cron
already keeps per §6 and §14.

/cron never inspects primitive data and never decides what is garbage.
It schedules sweeps and answers "which intents are dead." That is the
§13.5 line holding under the strongest temptation yet to cross it.

### 15.4 Retention respects the sweep horizon

The provenance join has a right-hand side: the death list, which lives
in tick records — which §5's retention policy trims. If retention
deletes a tick before its execution's garbage is swept, the join loses
the row that made the garbage identifiable, and the leftovers are
orphaned *forever*, invisible to any future sweep. Therefore, stated
as a rule and not a tuning note: **a tick whose reclamation is pending
is unprunable.** Retention trims up to the sweep horizon and no
further — exactly as a WAL cannot truncate past the last checkpoint.
The trim job (§5) and the sweep jobs are thereby ordered by data
dependency, not by schedule coincidence; the trim's own verb checks
the horizon, so the ordering holds even if an operator rearranges the
schedules.

### 15.5 What this section deliberately does not build

- **No rollback.** Reclamation deletes identified garbage; it never
  reconstructs the pre-execution state. Verbs needing all-or-nothing
  semantics declare `atomic` and run under /dxp — the machinery for
  "nothing happened" already exists and will not be duplicated here
  (§14.2's kill row says the same from the other side).
- **No cross-primitive garbage graph.** A dead execution that wrote to
  /blob *and* walked /fsm is swept per primitive by each Reclaimer
  independently; nothing globally orders or coordinates the
  reclamations. If the leftovers are entangled enough to need
  coordinated cleanup, the verb needed /dxp in the first place.
- **No garbage detection heuristics.** The sweeper never guesses.
  Unattributed, unstaged leftovers from a `none`-class def are not
  garbage the system can see — they are the declared cost of that
  class, knowable before arming.

## 16. Effort estimate and wave scoping

Status: this section was added 2026-08-15 when /cron was scheduled as a
wave. The proposal above is the design; this is what it is expected to
cost, in the substrate programme's own currency (ideal days, per
`SUBSTRATE_DEVELOPMENT_PLAN.md` §2), calibrated against what comparable
primitives actually took rather than estimated from the design alone.

### 16.1 Calibration

The plan priced comparable primitives at: /loc 9.0 ideal days (items
41–44), /obj 9.75 (45–49), /bal 8 (14–17), the dxp coordinator 5 (item
21). What actually happened is the calibration:

- /loc's four items shipped 0.20.4→0.21.0 across 2026-07-31/08-01; /obj's
  six items shipped 0.22.1→0.22.5 on 2026-08-02. Roughly 19 planned
  ideal days landed in about three calendar days — the compression a
  **design-first** primitive gets, and /cron has its design in unusual
  depth.
- The plan numbers never priced the tail: 0.21.1's adversarial pass
  found five real bugs in /loc (and two in /bal by pattern); 0.23.0 and
  0.24.0 found two data races and a lock-order deadlock in the shared
  coordinator that only /obj's adversarial load exposed; multi-core
  confirmation was owed and later paid. The tail ran roughly half again
  on top of the planned work. This estimate prices it explicitly.
- Code volume of the comparables, for shape: /loc ≈ 3.4k production +
  3.2k test + 1.2k server handlers; /obj ≈ 1.8k + 1.3k + 1.0k.

### 16.2 Work breakdown (v1 as §10 leaves it)

Scope assumptions: single-instance (§10.1); no calendar-aware `every`
tier (T-49/T-50 gate it); minimal verb set; reclamation for /blob only.

| Item | Ideal days | Notes |
|---|---|---|
| Storage, def/job registration, prototype-clone, capability introspection at def time (§14.4) | 1.5 | Mirrors `/dxp/def` handlers; introspection is small once the verb interfaces exist |
| Recurrence rule object, IANA zones, DST policy; `at`/`after`/`every` resolution | 2.5 | The deep pit. Go's `time` handles zones; the rule grammar and DST semantics are ours. §10.2/§10.3 must be decided *before* this item starts. `pkg/gc` (§13.10) has no analogue here — unchanged |
| Scheduler loop, tick plane, missed-fire + overlap policy, suppression-streak counter | 1.5 (was 2.0) | Real concurrency, still — but the ticker-and-stop-channel lifecycle shell, the double-start guard, and the per-execution panic recovery now have a proven-in-production pattern to mirror directly (`pkg/gc.Worker`, §13.10), not a blank page. Reduces both time and risk on the shell; the missed-fire/overlap/suppression logic inside it is still novel and still this line's own risk |
| `watchdog` shape + `reset` verb | 0.75 | A scheduler-loop branch once the tick plane exists |
| Chime: `cron.chime` event type, `event_latch_source` coordinates, payload | 0.75 | Existing events machinery; one new closed-set type |
| Verb registry + interfaces (cancellable, progress-reporting) + two verbs (`fsm_walk`, one prune) | 1.5 | Each additional verb ≈ 0.25–0.5; §10.6's OQL-verb reconciliation not included |
| Job control: `info`/`pause`/`resume`/`stop`/`kill`; in-memory execution + progress state | 1.5 | Resume-via-missed-fire and terminal-stop keep this small; `info` is the largest piece |
| Retention policy + `~trim` job honouring the sweep horizon (§15.4) | 0.75 | Retention follows T-55's established pattern |
| Reclamation: classes on the def, Reclaimer interface, `/blob` Reclaimer (staged + attributed), `~sweep` job | 2.0 | /blob only for v1; each further primitive's Reclaimer ≈ 0.5–1 |
| System jobs armed at startup (§8); migrate `pkg/gc`'s four existing Sweepers (blob GC, ts retention, fsm v2 collection, event delivery log v2) onto /cron as `~` jobs | 1.25 (was 1.0, dxp-janitor-only) | §13.10: all four are already `every`-shaped with a working `Sweeper` — "closer to a rename than new work" per that section's own framing. Stronger, more concrete dogfooding proof than the janitor alone; the dxp janitor migration (§8's original scope) is folded into this same item, not dropped |
| REST surface, `pkg/client`, molu tools, docs, dormant-guard registration | 1.5 | The house tail every primitive carries |
| **Planned** | **15.5** (was 15.75) | Net of the scheduler-loop reduction (−0.5) and the dogfooding item's own small increase for covering four consumers instead of one (+0.25) |
| Hardening allowance: adversarial pass, concurrency findings, multi-core confirmation owed | **+5.0**, unchanged | Not reduced alongside the scheduler-loop line, deliberately — the allowance is calibrated against /loc's and /obj's *whole-primitive* adversarial-pass history, not this one component; job control (§14) and reclamation (§15) remain as novel and untested as before `pkg/gc` was found |
| **v1 total** | **≈ 20.5** (was ≈ 21) | |

### 16.3 Calendar translation and confidence

At the observed rate for design-first primitives: 5–7 focused
session-days for the planned work, plus the hardening tail's own
multi-core confirmation cycle, which depends on hardware turnaround
rather than session time. A wave-and-a-half in programme terms — larger
than /loc or /obj individually, smaller than the two together, closer to
/loc's *total* cost including its 9b spec-reconciliation than to its
headline number.

Least trusted line, revised 2026-08-17: the scheduler loop's own
missed-fire/overlap/suppression logic, not the loop's lifecycle shell
any more — `pkg/gc.Worker` (§13.10) is a proven-in-production precedent
for the shell specifically (ticker lifecycle, double-start safety,
panic recovery), found and inherited this session, which is why that
line dropped from 2.0 to 1.5 rather than just being asserted lower.
What it does *not* de-risk: overlap policy under real concurrent load
is still the shape that produced this project's recorded coordinator
races (0.23.0), `pkg/gc` has no per-def overlap concept to learn from,
and the project's own discipline still says a sandbox pass is not
evidence. Job control (§14) and reclamation (§15) remain fully novel —
no in-tree precedent for either was found, and none should be assumed.

### 16.4 Levers that move the number

1. **Decide §10.2/§10.3 before code.** Highest leverage — they sit
   under the 2.5-day recurrence item and everything after it.
2. **Defer §15 (reclamation) to v1.1**: saves ≈ 2.75 (the /blob
   Reclaimer plus the sweep-horizon check). Defensible if the first
   consumers are the internal maintenance jobs, which are mostly
   `atomic`/`idempotent` and need no sweeper; costs the motivating
   photo-copy case its sweeper at v1.
3. **Calendar-aware tier later**: T-49/T-50 plus ≈ 1.5.
4. **Each further verb or Reclaimer**: 0.25–1 apiece; do not let the
   verb list grow speculatively (§13.5).

### 16.5 Prerequisites and sequencing

No hard prerequisites: T-07 is designed around (§6); T-49/T-50 gate only
the deferred tier; T-12 is a compatibility constraint (§7). One
sequencing note: if the eager stale-checkpoint recompute (§8) is a v1
dogfooding job, T-55's remaining /cal and /ts rulings should land first;
otherwise that job waits and the janitor migration carries the
dogfooding proof alone.

### 16.6 Wave shape

Scheduled as its own wave (see `SUBSTRATE_TRACKING.md` for the live
table and `SUBSTRATE_DEVELOPMENT_PLAN.md` for the rationale paragraph;
the wave number is computed by `scripts/add_wave.py`, never asserted
here). The wave's items are the §16.2 rows grouped into six roughly
comparable chunks so that `wave_progress.py`'s per-item weighting
(✓/◐/☐) yields meaningful partial credit — not one giant item plus five
small ones. The hardening allowance is not a wave item: it is priced
here so the wave's own number is honest, and it lands as its own
register items when the adversarial pass finds what it finds, per the
established practice (T-125, T-135, T-138 were all filed that way).
