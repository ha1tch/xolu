# Sysmask: System Reservation in the Primitive ID Space (proposal)

Updated: 2026-07-20
Status: proposal — wave 1 (@P). Companion to the chronicle programme
(`chronicle-substrate.md` @C, `bal-conservation-primitive.md` @B,
`dxp-composed-commitment.md` @D).

This supersedes the 2026-07-19 draft, which scoped the mechanism to the
tenant ID space and modelled it as an IP-style prefix-and-mask. Both
were wrong. The mechanism's home is the per-primitive ID space widened
to 32 bits in wave 1 (@P item #8), and the mechanism is simpler than a
netmask: a single width. The superseded tenant-space application is
preserved as an appendix (§8) against future demand, built by no one now.

## 1. What this is

One 32-bit primitive ID space (a `/ts` `TimelineID`, a `/cal`
`CalOrdinal`, a future `/bal` account id) is partitioned into a
**user region** and a **system region** by a single immutable number:
the **sysmask width**. The substrate provides only the partition — the
predicate that says which side of the line an id is on, and the
guarantees that keep the line from moving under a running store.
It assigns **no meaning** to the system region beyond "not user". That
is deliberate (§5).

## 2. The sysmask width

The sysmask width is a **`uint8` in the range 0–32**: the count of
**top bits** of the 32-bit id that form the *region selector*. It is
**not** a mask value and **not** a prefix; it is a length. An id is
system-scope iff its selector bits are non-zero:

```
IsSystem(id) = (selector bits of id are non-zero)
             = width == 0 ? false
             : width == 32 ? id != 0
             : (id >> (32 - width)) != 0

IsUser(id)   = !IsSystem(id)
```

This is one shift and one compare — branchless in the common case, with
the two extremes special-cased for correctness (see §3). No range
constants, no prefix arithmetic, one predicate shared by every caller.

**The default width is 0.** A freshly initialised store reserves
nothing; its entire id space is user-space and the feature is, in
effect, off. Reservation is opt-in.

### Reading the partition

```
 A 32-bit id, width = N:

   ┌──────────┬─────────────────────────────┐
   │  top N   │          the rest           │
   │  bits    │                             │
   └──────────┴─────────────────────────────┘
        │
        ├── all zero  →  USER id
        └── any set   →  SYSTEM id
```

- **width = 0** — no selector bits; every id is user; the system region
  does not exist. (default)
- **width = 8** — top byte selects; `0x00_00_0001…0x00_FF_FFFF` are
  user (16 777 215 ids), anything with a non-zero top byte is system
  (255 blocks of 16 777 216).
- **width = 32** — every bit selects; the only all-zero value is id 0
  (already reserved), so the store is effectively **system-only** — it
  holds no user-allocatable ids. A legal, meaningful configuration for
  a store that is pure platform infrastructure.
- **0 < width < 32** — a split: low ids (zero selector) are user, high
  ids (non-zero selector) are system.

Because user ids are exactly those with zero selector bits, the natural
sequential allocation `0x0000_0001, 0x0000_0002, …` lands in user-space
automatically, and no legal width change ever relocates a low-numbered
user id.

## 3. Correctness at the extremes

- **width = 0:** the expression `id >> 32` is not well-defined for a
  32-bit type (Go rejects or mis-evaluates constant shifts of the type
  width), so width 0 is special-cased to `IsSystem == false`
  unconditionally. Not an optimisation — a correctness guard.
- **width = 32:** the shift is `id >> 0 == id`, so `IsSystem = id != 0`.
  Correct without special-casing, but stated explicitly so no reader
  wonders.
- **id = 0:** reserved/unscoped by existing convention; never allocated
  to any timeline, user or system, at any width.

## 4. Immutability, and the escape hatch that makes it survivable

The sysmask width is declared **once, at store initialisation**
(`--sysmask <width>`, a number 0–32), recorded in store metadata, and
**immutable thereafter** — like a store identity. A mutable or
per-context width would make an id's *class* — user or system — depend
on when or where it is evaluated, which is poison the moment ids are
compared across time or across stores: an id minted as user must never
later read as system.

Immutability alone would be a trap: a store initialised with the wrong
width would have no recourse but a hand-rebuild. The escape is
**transvasing** (§6) — an offline migration that pours a store's
timelines into a new store under a new width, rather than mutating the
width in place. *Within* a store's life the width is frozen and ids
never drift; *between* store generations, transvasing provides a
clean, explicit, operator-invoked change. The safety property and the
flexibility stop being in tension.

**Federation.** Locally, xolu records and enforces the declared width.
At any future federation point, **nolu refuses to join stores whose
sysmask widths disagree** — the same each-layer-owns-its-invariant
boundary the rest of the programme uses. `iolu db status` **displays
the declared width** so the partition is always inspectable, never
inferred.

## 5. Deliberately flat: no substrate semantics

The substrate provides the partition and nothing more. It does **not**:

- assign meaning to particular system-region values (no "top byte 0x01
  means metering"),
- define named system sub-regions,
- carry a tilde or any naming convention for system ids,
- differ in behaviour across `/ts`, `/cal`, `/bal` — the same width
  primitive applies identically to each primitive's own id space.

Meaning inside the system region — should metering ids and billing ids
one day want to be distinguishable, via a sub-bitmask within the
selector bits or any other convention — is **layered on later, above
the substrate**, exactly as bal's boundary-account convention layers on
top of bal's arithmetic. This is intentional: the substrate solves the
partition, not the world. A convention added later fits within whatever
width was frozen at init, which means the width choice has some
downstream reach (a store that will want a rich system-category
convention should be initialised with enough selector bits to express
it); that is the one forward-looking consideration an operator setting
the width should keep in mind.

## 6. Transvasing: non-lossy migration across widths

Transvasing is an **offline `iolu` operation** (wave 6, operations set)
that rewrites every timeline of a source store into a target store
under a new sysmask width. It is the *only* way a store's effective
width changes, and it is **non-lossy by construction**:

1. **Dry-run first, always** — even when the parameters look obviously
   safe. The dry run walks the entire source, computes each timeline's
   id under the target width, and checks each target id against the
   target store's **live** id set.
2. **Abort on any live collision** — if any computed target id is
   already occupied by a live timeline in the target, the operation
   aborts and reports the conflicts, **having written nothing**. "Non-
   lossy" means total-or-abort: it never overwrites a live timeline.
3. **Source is never dropped automatically** — on success, source and
   target **coexist**. Dropping the source is the administrator's
   explicit responsibility.

The uniqueness this relies on is the weakest sufficient one: **an id
must be unique among *live* timelines within one tenant, per primitive**
(§7). Transvasing needs no high-water mark, no monotonic counter, no new
allocation machinery — it reuses the liveness check the registry already
performs (`define` refuses an id whose timeline already exists). A
deleted id is freely reusable; what must never happen is transvasing
onto a *live* id, and the dry run is exactly that guard.

Legal width changes and their effect:

- **introducing a region** (width 0 → N) — every existing id has zero
  selector bits, all classify as user, all keep their exact value; the
  cleanest case.
- **widening the system region** (N → M > N) — user ids keep zero
  selector bits and are untouched; system ids may rebase; non-lossy so
  long as no rebased id collides with a live target id.
- **narrowing** (N → M < N) — the case most likely to induce a
  collision; the dry run catches it and aborts with the specific ids
  named, leaving the admin to widen the target, drop the colliding live
  timeline, or choose a different width.

## 7. Uniqueness scope

Id uniqueness is scoped **globally within one tenant, per primitive** —
not store-global across tenants, not shared across primitives. Within
tenant `0x0001`'s `/ts`, every live `TimelineID` is distinct; tenant
`0x0002`'s `/ts` has an independent id space that may reuse the same
numeric values; `/cal` and `/bal` within a tenant are each their own
independent uniqueness domain. This matches the existing tenant-scoped
storage layout (each tenant is its own store/directory) and keeps the
liveness check cheap and local.

## 8. Enforcement: two allocation paths

The partition is enforced structurally, not by a permission bit:

- **The user-facing allocation path** (the tenant HTTP surface's
  `define timeline` / `define calendar` / future `define account`)
  draws from **user-space only**. It structurally cannot mint a system
  id. If sequential allocation reaches the top of user-space, that is
  **user-space exhaustion — a typed error, not a silent spill** across
  the partition.
- **Explicit-id user requests** (where the API lets a client name an
  id) are **refused with a typed error if `IsSystem(id)`** under the
  store's width.
- **The system-internal allocation path** — used by platform components
  that legitimately mint system ids — is a distinct, non-user-facing
  path that draws from system-space. It is not reachable through the
  tenant HTTP surface.

Because the two paths have different id sources, there is no request an
API user can send that yields a system id. Both the refusal and the
system allocator's bound share the single `IsSystem` predicate.

## 9. Non-goals

- **Widening beyond uint32.** 2^32 ids per primitive per tenant is far
  beyond any per-tenant workload the platform targets.
- **A second address space for the width.** The sysmask governs the
  primitive id space only; it is deliberately *not* applied to tenant
  ids (§10).
- **Substrate-level system semantics.** See §5 — deferred to convention.
- **Cross-primitive coordination.** Each primitive's id space is
  independent; a system `/ts` id and a system `/cal` id with the same
  value are unrelated.

## 10. Appendix: the tenant-space application (speculative, unscheduled)

The 2026-07-19 draft applied a reservation to the **tenant** id space,
carving out tenant ids for whole system *stores* (`~metering`,
`~billing`, `~dxp`) with a tilde sigil and an authz rule keeping them
off the tenant HTTP surface.

That application is **not part of wave 1 and is not scheduled.** The
tenant id space was deliberately not widened (tenant ids remain
`uint16`; a machine hits its throughput and storage ceilings long
before 65 k tenants, and the answer if it ever did would be a second
xolu, not a wider id). And there is no confirmed use case for system
*tenants* distinct from what system *ids within an existing store*
already provide: platform metering and bookkeeping can live as
system-region ids inside the relevant store.

The idea is kept because it costs nothing and may find a use: the
identical width mechanism (§2–§4) applies unchanged to the 16-bit
tenant space if demand emerges, at roughly a day's work, with the
tilde-sigil naming ready to adopt. Recording it, not building it.

## 11. Requirements trace

- **Width mechanism** (§2–§4): one immutable `uint8` width 0–32;
  `IsSystem` predicate; default 0; extremes correct; immutable at init;
  nolu federation agreement; `iolu db status` display.
- **A-flat** (§5): substrate provides partition only; no per-primitive
  semantics, no named sub-regions, no tilde — deferred to convention.
- **Transvasing** (§6): offline `iolu`; dry-run always; abort on live
  collision; source+target coexist; admin drops manually; reuses the
  existing liveness check; no high-water mark.
- **Uniqueness** (§7): live-unique within one tenant, per primitive.
- **Enforcement** (§8): two allocation paths; user path cannot mint
  system ids; explicit-id refused via `IsSystem`; user-space exhaustion
  is a typed error.
- **Superseded and dropped:** the tenant-uint32 widening and its cost
  analysis; the prefix-and-mask model; "a system store is just a tenant
  store"; the tenant-granularity ceiling argument — all replaced by
  @P wave 1's decision to widen the *primitive* id space, leave tenant
  ids at `uint16`, and model the reservation as a single width.
- **Archived, unbuilt** (§10): system *tenants*, tilde sigil, tenant-HTTP
  authz exclusion.
