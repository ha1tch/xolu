# System Bookkeeping Scope (proposal)

Updated: 2026-07-19
Status: proposal — not scheduled. Companion to the chronicle programme
(`chronicle-substrate.md`, `bal-conservation-primitive.md`,
`dxp-composed-commitment.md`), whose system-level bookkeeping needs
motivate it.

## 1. Problem

The substrate increasingly needs stores that belong to the *platform*,
not to any customer tenant: usage metering, platform billing, shared
dxp definition libraries, operational bookkeeping. Today every store is
a customer tenant store; system data has nowhere first-class to live.

Two options exist: widen the tenant ID space (currently `uint16`), or
reserve a range within it.

## 2. Decision: widen to uint32 now, and reserve within it

**ID space: `uint32`. Reserved system space: a **sysmask** — a prefix mask, netmask
style — default `0xFFFFFF00/24` (the top 256 IDs).**

The reservation is defined by the **sysmask** — a system prefix and
mask — not a numeric range: an ID is system-scope iff
`id & sysmask.Mask == sysmask.Prefix`. This yields one branchless
predicate (`tenant.IsSystem(id)`) used by the registry guard, the
allocator, and the HTTP surface — no range constants scattered — and
makes the partition width **policy, not code**: a deployment needing
more system slots declares a wider prefix (e.g. `/20` for 4,096) at
initialisation instead of awaiting a migration. Width is sanity-bounded
(`/16`–`/28`), and the prefix must sit at the top of the space
(excluding tenant 0's unscoped semantics by construction). System space
subdivides hierarchically with the same notation if categories
(`~metering`, `~billing`) later want their own blocks.

**Immutability rule — the trap the sysmask must not spring:** it is
declared once at `db init` (`--sysmask 0xFFFFFF00/24`), recorded in store metadata, and immutable
thereafter, like a cluster identity. A mutable or divergent mask would
make an ID's *class* deployment-relative — poison for cluster-global
IDs. Locally, xolu records and enforces the declared sysmask; at any
federation point, nolu refuses to join stores whose sysmasks disagree.
Per-store *chosen*; never per-store *drifting*.

An earlier draft recommended reserving within `uint16` and rejecting
the widening on assumed cost. Measurement reversed it. The audited cost
of widening (2026-07-19): 218 Go signature sites (mechanical,
compiler-chased; a `TenantID` type alias introduced during the pass
makes this the last such migration), four `%04X` format functions of
which **only `StorageDirSegment` reaches disk** (node-ID strings and
scope keys are runtime-only; edge tables store integers), exactly two
binary-packed persisted sites (the ts Pebble key codec's 2-byte tenant
prefix → 4 bytes), 101 test-fixture lines, two client sites; SQLite
columns are already 64-bit INTEGER and the wire carries names and JSON
numbers. **Roughly three days, suite- and CI-arbitrated — provided it
lands before production data exists.** The identical change
post-production adds Pebble key migration, live directory renames, and
transition parsers: weeks, with risk, at the worst time.

The cost of *not* widening: nolu's tenant-migration model makes IDs
cluster-global, so ~65k bounds the **platform**, not a node — and any
per-site or per-workspace tenancy granularity brings that ceiling into
the low thousands of customers, colliding precisely during scale-up.
The asymmetry decides: widening's cost grows monotonically with every
deployment shipped; the ceiling's collision is timed adversarially.
`uint32` ends the question permanently at any granularity.

Execution note: the widening is register-class work with a working-agreement §7-style
two-pass discipline (production code green on `TenantID uint32` first;
fixture strings second) and a tree-wide `%04X`/2-byte-codec completion
sweep before declaring done.

A system store is mechanically **just a tenant store**: layout, iolu,
backup, every primitive, isolation — all unchanged. New code is only:

1. a registry guard: user-space tenant creation refuses IDs matching
   the sysmask (`tenant.IsSystem`);
2. an authz rule: system stores are not served by the tenant HTTP
   surface; access is operator/administrative only;
3. naming convention: system tenants carry the tilde sigil
   (`~metering`, `~billing`, `~dxp`) — extending bal's boundary-account
   convention: **the tilde marks the system's own books.**

**Adoption prerequisite:** the uint32 widening lands first; the
default system prefix then sits in space no deployment can have
touched, and `iolu db status` displays the store's declared sysmask.

## 3. Intended residents

- **`~metering`** — platform usage as ts series: per-tenant request
  counts, blob bytes, store sizes; the substrate observing itself.
- **`~billing`** — platform revenue as a bal book: metering feeds
  invoicing through the conservation primitive; the platform's own
  money carries the same arithmetic identity it sells.
- **`~dxp`** — shared transaction-definition templates.
- Operational bookkeeping as needs arise; 256 slots is generous.

## 4. Non-goals

- Cross-tenant data access paths: system stores read customer data
  through the same interfaces any operator tooling would, or receive
  emitted observations; the reserved range grants no special bypass.
- Widening beyond `uint32`: 4.29 billion tenants suffices at any
  tenancy granularity the platform model admits.
