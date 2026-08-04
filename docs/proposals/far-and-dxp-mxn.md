# /far and /dxp/mxn — federated authorization and cross-tenant/cross-instance dxp

Updated: 2026-08-01
Status: proposal — reviewed, not scheduled. Implementation is
sequenced behind three things: Pablo's Seam-side CMMS development
teams getting productive on xolu, `/loc` and `/obj` shipping (waves 9
and 10, `SUBSTRATE_DEVELOPMENT_PLAN.md`), and nolu's own development
resuming (currently paused). Deliberately
leaves the harder implementation questions open (§8) rather than
resolving them prematurely. Companion to `dxp-coordinator-design.md`
(item 21, the local coordinator this extends) and
`dxp-composed-commitment.md` (the foundational participant contract).
Activates the named trigger in `SUBSTRATE_DEVELOPMENT_PLAN.md` §6
(Deferred): *"cross-tenant and/or cross-instance dxp transactions"* —
the plan's own deferred item, not new scope invented here.

## 1. What triggers this, precisely

`SUBSTRATE_DEVELOPMENT_PLAN.md` §6 already names this scope and
already parks a narrower piece of it:

> Cross-tenant dxp is already parked (T-54: token-bearing transactions
> mounting a remote /dxp/txn object from another tenant of the same
> instance — its stated prerequisite, item 18, is now done, so the
> parking is live, not stale). Cross-instance dxp is the larger scope
> again — nolu territory per @D08 regardless of tenant scope, not a
> dxp v1 concern either way.

Two things follow directly from this, and this proposal does not
relitigate either:

1. **T-54's cross-tenant sketch is a special case of what's proposed
   here, not a separate track.** Read literally, `/dxp/mxn` — "dxps
   who live in other tenants and/or instances" — is a strict
   superset: cross-tenant-same-instance is the case where the remote
   instance identifier resolves to *this* instance. Once `/far` and
   `/dxp/mxn` exist, T-54's own entry should get a reconciliation note
   pointing here rather than being implemented separately (§9).
2. **Cross-instance dxp was explicitly named as nolu territory, not a
   dxp v1 concern.** This proposal does not silently reopen that
   boundary. §3 works through what changes and what doesn't given that
   boundary is now being asked to move — that's a real decision this
   document surfaces for review, not one it makes unilaterally.

The other standing decision this proposal must not contradict without
saying so:

> Persistent (durable, cross-process-visible) dxp transactions...
> "we're not going to implement persistent transactions. We may never
> do that." ... If ever revisited, this is nolu territory per @D08,
> not a dxp v2.

§4 works through why `/dxp/mxn` does not need to violate this — the
short version: cross-instance instances are *always* phased (no
shared transaction can span two servers' files), so they inherit
exactly the torn-commit risk profile the phased path already accepts
today, just extended across a new boundary. No durable decision log is
proposed. Whether the *rate* of that risk is still "extremely rare"
once instances (not just engines) are involved is a real, open,
empirical question — not one this document assumes an answer to (§6).

## 2. Why now (the testing conversation this grew out of)

This grew directly out of a discussion about testing rigor: dxp's
current adversarial/scale testing (T-109, T-110) is real and has found
real bugs, but it's hand-designed example testing against a
single-process coordinator that cannot partition from itself. The
architecture's own no-durability justification — mid-execute crashes
are rare, and rare enough not to be worth paying for recovery against
— was reasoned through carefully for the *local* case. That reasoning
does not automatically transfer to a coordinator whose participants
are now on independently-failing, independently-durable remote
servers, reachable only over a network that partitions, delays, and
times out at rates nothing like a local process's crash rate. `/far`
and `/dxp/mxn` are the feature; genuinely Jepsen-shaped testing (fault
injection across real, separate processes — not the in-process
generator §6 also names) is the thing that actually tells us whether
the existing risk tolerance still holds once this ships. The two are
not separable: this proposal exists partly *because* answering that
question requires something to inject faults against.

## 3. Scope boundary: what moves, what doesn't

Two independent axes were conflated in casual conversation and need
separating, the same way QM and cross-instance replication were
conflated and corrected in §6 of the dev plan (worth re-reading that
correction directly before assuming anything here):

- **Cross-tenant, same instance.** No network boundary at all — both
  tenants' data lives in the same process, same file (or adjacent
  per-tenant files under `SQLitePerFileTenants`), just under separate
  `t<XXXX>_` prefixes. `tenant-access-control.md`'s existing grant
  machinery (`TenancyFlags`, `APIKeyGrant`, JWT `tenants`/`tenant_admin`
  claims — its as-built shape, §5.2) already answers "can this
  authenticated caller reach tenant X" for ordinary requests; nothing
  there answers "can a dxp instance whose def names a *different*
  tenant's account as a participant actually touch it," because that
  question has never been asked inside one request before. This is the
  smaller, lower-risk half of the proposal — no new network protocol,
  no new failure mode beyond "authorization," and it's the part that
  most directly completes T-54.
- **Cross-instance** (with or without also crossing tenants). A real
  network boundary: a different xolu server, possibly a different
  operator, possibly untrusted. This is where `/far`'s registry and
  grant model earn their keep, and where the durability question in §4
  actually bites. `@D08`'s "nolu territory" framing was written before
  this was asked for directly — this document treats that framing as a
  standing default to be explicitly overridden by direct instruction
  (which this is), not as settled and irrelevant. **What nolu-owned
  concerns this proposal deliberately does NOT take on:** hotswapping
  instances, the redirect-and-await-confirmation mechanism, and
  anything about instance lifecycle beyond "is this remote instance
  currently reachable and authorized." `/far` is a registry and
  authorization layer; it is not nolu, and does not attempt to become
  it.

Both axes are handled through one mechanism (`/far` + `/dxp/mxn`)
because the authorization question is the same shape either way: *does
the caller/coordinator hold a valid grant to touch this resource,
issued by whoever owns it* — "whoever owns it" just happens to be a
different tenant on the same file in one case and a different server
entirely in the other.

## 4. `/dxp/mxn` — cross-tenant/cross-instance dxp instances

### 4.1 What stays the same

The participant contract (`Reserve`, `Validate`, `Execute`, `Release`,
`PostCommit`) does not change shape. A remote participant still
Reserves, still gets Validated, still Executes and gets PostCommit'd —
the difference is *how the coordinator reaches it*, not what it's
asked to do. `dxp/def`'s participant spec gains an optional field
identifying which instance/tenant a participant lives in (local by
default, matching every def registered today); everything about
pattern, phase_ttl, and bindings resolution is unchanged for a
participant that stays local.

### 4.2 What has to change: the coordinator can no longer assume one process

Today's coordinator drives every participant's `Execute` against a
`ParticipantStore` it constructs directly (`*dxp.SQLStore` wrapping a
shared `*sql.Tx`, or the Pebble equivalent) — an in-process call, a Go
interface satisfied by a concrete adapter in the same binary. A remote
participant cannot satisfy that interface directly; there is no
`*sql.Tx` to hand across a network boundary. This needs a wire
protocol: the coordinator's own Reserve/Validate/Execute/Release/
PostCommit calls against a remote participant become HTTP requests
against the remote instance's own dxp participant surface (new
endpoints, not yet designed in this pass — plausibly
`/dxp/participant/{primitive}/reserve` etc., mirroring the local
verb names, but the wire shape needs its own design pass before
implementation, not sketched here to avoid a second correction later).

The collapsed dispatch path (one shared SQL transaction, every
participant in it) **cannot exist for a remote participant at all** —
a `*sql.Tx` is a single-process, single-file thing by construction.
Every `mxn` instance is, by definition, dispatched through
`dispatchPhased`'s own logic (or a close relative of it) the moment it
has one remote participant, regardless of what engines are involved
locally. This is not a new code path to design from nothing — it's the
existing phased path, extended to treat "different instance" as
another reason a participant can't share the collapsed transaction,
the same way "different engine" already is one.

### 4.3 The durability question, worked through rather than assumed

The existing no-durability decision rests on: mid-execute coordinator
crashes are rare (short guard window), and the one failure mode
durability would guard against — a crash strictly between two
participants' commits in a phased instance — is accepted as tolerable
risk given that rarity. Cross-instance participants are *always* on
the phased path (§4.2), so they inherit this exact risk shape, not a
new one. What's actually new:

- **The failure isn't just "coordinator process dies."** It's also
  "network partition between coordinator and remote participant,"
  "remote instance unreachable or slow," and "remote instance's own
  process dies mid-Execute independently of the coordinator's health."
  These are not rare in the way a local process crash inside a
  microsecond guard window is rare — network partitions and remote
  unavailability are the normal operating condition of distributed
  systems, not an edge case. The existing "extremely rare" argument
  was never evaluated against these failure rates because it never
  needed to be.
- **A remote participant's own commit is durable independently of the
  coordinator remembering anything.** If instance A's coordinator tells
  instance B's bal adapter to Execute, and B commits, that transfer is
  real and persists in B's own storage whether or not A ever comes
  back, restarts cleanly, or remembers trying. This is the actual new
  risk `/dxp/mxn` introduces beyond what local phased dispatch already
  has: a torn state that is not just "some participants committed,
  some didn't" within one crash-abandon process, but "a remote
  participant's real, durable commit, with no local record on the
  initiating side that it was ever asked for" if A crashes or loses
  the response.
- **What this proposal recommends, without a durable decision log:**
  every remote participant Reserve carries the same TTL-bounded,
  self-expiring shape local claims already have (item 18's own
  expire-and-sweep machinery) — a remote participant that never hears
  back from the coordinator releases its own hold on its own timeout,
  exactly like a local one abandoned by a crashed process does today.
  This does not eliminate the torn-commit risk in §4.3's last bullet —
  nothing short of a durable decision log does, and that's explicitly
  off the table per the standing decision — but it bounds the *blast
  radius*: an abandoned remote reservation self-heals, and a remote
  participant that already committed before losing contact stays
  committed (the same "self-heals via the oracle, not a correctness
  failure" posture already applied to bal/cal's derived planes, T-57/
  T-83, extended here to the coordination layer itself rather than
  just the rollup layer). What is genuinely unresolved, and named as
  such rather than assumed away: whether "the coordinator never finds
  out a remote commit actually landed" is an acceptable outcome for
  every use case this is meant to serve, or whether some callers
  genuinely need to know. That's a product question, not an
  engineering one, and belongs in review, not buried in this proposal.

### 4.4 Result: `/dxp/mxn` is dispatch's existing risk model, deliberately widened, not a new one invented from nothing

This is the central design claim of this document, and it's the one
most worth someone else pressure-testing rather than taking on faith:
cross-instance dxp is proposed as an *extension* of an already-accepted
risk (phased-path torn commits, self-healing derived planes) across a
new boundary, not a new durability model bolted on. If that claim is
wrong — if network-partition-rate torn commits turn out to need a
stronger guarantee than "extremely rare" tolerated — the honest
conclusion is that `/dxp/mxn` needs nolu's durable-decision-log
machinery after all, and this proposal should say so plainly rather
than build around it. §6 names exactly how to find out before
committing either way.

## 5. `/far` — a registry of remote tenants, instances, and the authorizations between them

Two things a coordinator (or a participant being reached by a remote
coordinator) needs to answer before touching anything, matching the
question in the title precisely: *who is this, and what have they
actually been granted?*

### 5.1 What `/far` tracks

- **Remote instances**: an identity (a stable instance ID, not a
  hostname — hostnames move, an identity used in a grant token
  shouldn't), a base URL, and whatever material is needed to verify
  requests actually come from that instance (§5.3).
- **Remote tenants**: scoped to a remote instance — a (instance,
  tenant) pair, since a tenant identifier alone (`uint16`, per the
  standing decision that stays unchanged, §7) means nothing without
  knowing which instance's tenant-ID space it's drawn from.
- **Grants we hold** (inbound — "authorizations we have been given"):
  a token, issued by a remote instance/tenant, that this instance's
  coordinator presents when reaching out on a local caller's behalf.
  This is the direction the framing in the request itself names as the
  motivating concern, and it's the direction `/dxp/mxn`'s own coordinator
  consults on every remote Reserve.
- **Grants we've issued** (outbound): the mirror image — what we've
  told some other instance/tenant it may do against *our* resources,
  consulted when a remote coordinator's request arrives at one of our
  own participant endpoints (§4.2's new HTTP surface). Both directions
  are needed for `/far` to be a complete answer to "what have we been
  authorized to do, and what have we authorized others to do" — issuing
  without a record of what was issued is not meaningfully different
  from having no authorization model at all.

### 5.2 Grant shape (sketch, not committed)

A grant should be scoped narrower than "this remote party can do
anything" — plausibly: which primitive(s), which specific resource or
resource pattern (an account ID, a calendar ID, a wildcard scoped to a
tenant), which operations (participate as a dxp participant only? read
access too?), an expiry, and a revocation path. This should follow
`tenant-access-control.md`'s own as-built grant vocabulary, not its
original design sketch — that document's first-cut `TenantGrant`/
`Allows()` design (§4) was itself refined during implementation into
per-credential grant structs (`APIKeyGrant`, `S3KeyGrant`) plus JWT
claims (`tenants`, `tenant_admin`), checked directly against
`pkg/config/config.go` and `pkg/config/tenancy_flags.go` rather than
assumed from the proposal's own earlier draft (§9 of that document
records exactly this kind of design-vs-as-built drift, which is why
this one gets checked rather than cited from the sketch). A `/far`
grant is closer in shape to `APIKeyGrant` — a credential-scoped record
naming what it authorizes — than to a single global policy object.

### 5.3 Proof of grant, over the wire

A grant recorded in `/far` is only useful if a remote party can prove
they hold it without `/far` itself being queried synchronously on
every single request (a hard dependency on a live network round-trip
to a third party for every dxp verb would undermine the whole
point). The natural shape, consistent with the JWT machinery
`tenant-access-control.md` already established: a signed token, issued
at grant time, carrying the scope directly in its claims, verifiable
locally by whoever receives it without a callback to the issuer for
the common case — with revocation handled the way revocation always is
for bearer tokens (short expiry plus reissue, or a revocation list
checked at some acceptable cadence, not a design question this
document resolves).

### 5.4 What `/far` deliberately does not do

It is not a service-discovery mechanism (how a remote instance's
address is learned in the first place — a config value, an operator
action, or something more automatic — is out of scope here and
probably operator-driven initially, matching xolu's existing "opt-in,
fail-closed, no forced migration" posture from `tenant-access-control.md`
§3). If discovery ever needs to become more automatic than
operator-driven, that is expected to route through nolu specifically
— nolu already owns the cross-instance identity/registry problem
(`GlobalID` ↔ `LocalRef`), and instance discovery is the same shape of
problem, not a new one `/far` should invent its own answer to
(2026-08-01, Horacio). One more reason implementation here can wait
for nolu's own bandwidth rather than being independently workable
without it. It is not a replacement for `tenant-access-control.md`'s own
local-caller-to-local-tenant model — that remains the answer to "can
this authenticated caller reach this tenant," `/far` answers "can this
remote instance/tenant's coordinator reach into ours, and vice versa,"
a genuinely different question with a different threat model (a
mutually-distrusting *server*, not just a mutually-distrusting
*caller*).

## 6. Testing implications — the actual reason this needs to exist before implementation starts

Two tiers, matching the split already worked out before this document
was written:

1. **In-process, buildable now, independent of `/far`/`/dxp/mxn`
   shipping at all:** a generator constructing random defs (random
   participant counts, random substrate mixes, random overlapping
   resources) against the *existing* local coordinator, checked against
   one invariant — every instance ends fully committed or fully
   released, never torn, and every self-healing derived plane actually
   heals. This closes a real gap in today's example-based dxp testing
   regardless of whether `/dxp/mxn` ever ships, and should not wait on
   this proposal's own review.
2. **Genuinely Jepsen-shaped, and specifically gated on `/dxp/mxn`
   existing:** real multi-process fault injection — kill a remote
   participant's process mid-Execute, partition the network between
   coordinator and remote instance, delay responses past the reserve
   TTL, kill the coordinator itself after it has told a remote
   participant to commit but before it records anything — checked
   against the claim in §4.4: does the observed torn-commit rate stay
   inside "extremely rare, tolerable," or does it not? This is the
   test suite that answers whether §4.3's design is actually sound, not
   just plausible-sounding on paper. It needs real separate processes
   (containers or equivalent) and a real network to partition, the
   same way actual Jepsen runs do — it cannot be simulated by mocking
   a `ParticipantStore` in-process, because the entire question is
   about failures that only exist once there are two independent,
   independently-crashing servers.

Sequencing matters here specifically because tier 2 is expensive and
tier 1 is cheap: build the generator+checker first regardless of this
proposal's fate, then treat its findings (does the *local* phased path
already show more tearing than assumed, at scale, before any network
is even involved?) as a cheap early signal on whether the risk
tolerance in §4.3 is well-calibrated before spending on the harder,
multi-process harness this design ultimately needs to be trusted.

## 7. What stays unchanged

- `tenant.TenantID` stays `uint16` — this proposal does not touch
  tenant-ID width; a remote tenant is identified by (instance,
  local-tenant-ID), not by widening the ID space itself.
- The four-verb (five with PostCommit) `dxp.Participant` contract is
  unchanged in shape; §4.2 adds a transport, not a new verb.
- Local, single-instance dxp is entirely unaffected — every existing
  def, every existing test, every existing guarantee stays exactly
  what it is today. `/dxp/mxn` is additive, opt-in per participant
  (§4.1's "local by default"), matching the same "opt-in, no forced
  migration" posture `tenant-access-control.md` already established
  for its own feature.

## 8. Open questions — not resolved here, deliberately

- **Wire protocol shape for remote participant verbs** (§4.2): REST
  endpoints mirroring the local verb names is the obvious default;
  whether request/response shapes need anything beyond what
  `dxp.Claim`/`dxp.OpParams` already carry (e.g., does a remote
  Reserve need to carry the presented grant token inline, or as a
  header, or as a separate pre-flight check) is unresolved.
- **What "expiry" means for a grant vs. a reserve-TTL** (§4.3/§5.2):
  these are two different timeouts with two different owners (the
  grant's issuer vs. the transaction's coordinator) and their
  interaction — can a grant expire mid-instance, leaving a
  already-Reserved-but-not-yet-Executed remote claim orphaned even
  though the reservation itself hasn't hit its own TTL? — needs
  working through, not assumed compatible by default.
- **Whether "the coordinator never learns a remote commit landed" is
  acceptable** (§4.3's last point): a product question, named but not
  answered here.
- **`/far`'s own authentication of instance-to-instance requests**
  (§5.3): whether this reuses the existing `AuthType`
  (apikey/bearertoken/jwt) machinery directly, or needs its own mode,
  is sketched as "probably JWT-shaped" but not committed.
- **Whether `@D08`'s "nolu territory" framing should simply be amended
  to name `/far`+`/dxp/mxn` as the dxp-owned exception, or whether
  nolu's own eventual scope still subsumes something distinct from
  this** (§3): this document treats moving that boundary as this
  session's direct instruction, but the plan document itself should be
  updated to reflect that explicitly if this proposal is accepted, not
  left silently superseded.

## 9. Follow-up housekeeping, once this is reviewed

- T-54's register entry should get a reconciliation note pointing to
  this document rather than being implemented to its own, narrower
  original sketch (§1).
- `SUBSTRATE_DEVELOPMENT_PLAN.md` §6's Deferred entry for cross-tenant/
  cross-instance dxp should get a dated note that its trigger has
  fired, per the tracking taxonomy's own rule for deviations — not a
  silent edit.
- If accepted, this is plausibly wave 11 (2026-08-01: waves 9-10 are
  now taken by `/loc`/`/obj`, per `SUBSTRATE_DEVELOPMENT_PLAN.md` and
  `TRACKING.md`'s Plan deviations log) rather than shoehorned
  into wave 5 or wave 6's existing item numbering — wave 5's own exit
  criteria (the hotel test) never contemplated a network boundary, and
  conflating the two would blur what "wave 5 complete" already means
  (settled, per this session's own closure). Not decided here; flagged
  for whoever schedules it.
