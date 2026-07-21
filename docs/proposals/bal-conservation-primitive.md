# bal — The Conservation Primitive (proposal)

Updated: 2026-07-19
Status: proposal — not scheduled. First native consumer of
`chronicle-substrate.md`, which carries the shared machinery (cascade
engine, Sealer, rebuild-oracle harness) and the taxonomy this design
assumes. No register items exist until execution is decided.

## 1. What bal is

A substrate primitive for quantities that must add up: named accounts
holding amounts, changed only by atomic transfers, with bounds the
database itself refuses to violate. Money, stock, credits, seats —
anything counted that must never be miscounted.

## 2. Problem

A quantity stored as an entity field (`"on_hand": 12`) can only be
updated read-modify-write, which under concurrency is the check-then-act
defect class (see T-34 in the resolution record) reimplemented at the
application layer by every consumer, forever. The Store interface offers
no atomic increment; the substrate currently provides **no correct way
to count things under concurrency**. Agent-driven consumers (molu)
amplify the hazard: many tireless concurrent callers for whom "exactly
one decrement wins" must be enforced, not hoped.

## 3. Model

- **Account**: `account_id` (namespaced string, e.g.
  `warehouse:A/widget`), `unit` ("EUR", "widget", "gram"), `scale`
  (decimal places), `floor` (default 0), optional `ceiling`.
- **Transfer**: the only mutation. Debits one account and credits
  another by the same amount as **two signed journal entries (−a, +a) in
  one transaction**. With that convention, conservation is an arithmetic
  identity, not a property to test: every transfer sums to zero, so the
  system total always equals the sum of its boundary accounts.
- **The chain triple** (after pgledger; @B11a): every entry also records
  `previous_balance`, `current_balance`, and the account's monotonic
  `version` (incremented per entry, stored on the balances row). The
  values are free at write time — guard locality already reads the
  balance in the entry's own transaction, and the guarded
  `UPDATE … RETURNING` yields the new value in the same statement — and
  they make the journal **self-verifying**: an arithmetic hash-chain.
- **Boundary accounts** (`~received`, `~shipped`, `~written-off`, …):
  the only places value enters or leaves. "Where did five units go?"
  always has an answer, because the books structurally cannot lose
  value — only relocate it to a named elsewhere.

## 3a. Hierarchical accounts (the chart-of-accounts tree)

Continental accounting practice (Spain's Plan General de Contabilidad
and its cousins; standard practice in Rioplatense accounting) organises
accounts as a decimal tree — `1` Activo, `1.1`, … down to leaves like
`1.1.9.10.101` — with a strict discipline: **only leaf accounts are
postable** (*imputables*); every interior node is a summary whose
balance is defined as the sum of its subtree. This is a
field-proven structure with five centuries behind double entry and
decades of codified tree practice, and bal adopts its *machinery*
while leaving its *semantics* to applications:

- Account IDs may be hierarchical codes; parentage is by prefix (the
  sysmask idea in its accounting form).
- Accounts carry a `postable` flag. Transfers naming a non-postable
  (summary) account are refused — **XOLU-BAL005** — an admission rule,
  enforced like every other guard.
- Interior balances are **derived state**: subtree rollups computed by
  the chronicle cascade over a second, orthogonal dimension — account
  prefix × time grain — so "balance of `1.1` at end of Q2" is a
  two-dimensional fold (subtree sum over checkpoint-plus-buckets).
  Visibility tier three (@D05c): never posted, never guard-consulted.
- What a class *means* (Activo/Pasivo, debit/credit normal balances,
  statement formats, any national plan) is application content, loaded
  as data — per @B11, bal is not an accounting system; it is the
  substrate one is built on.

**External reference codes** (Brazil's SPED referencial, Mexico's SAT
código agrupador, any regulator mapping): not a bal field — pure
application content per @B11. They live in /meta, whose per-subject
key/value sidecar with optional TTL already fits (mappings set no
TTL); the one prerequisite is generalising meta's subject addressing
from entities-only to any primitive resource (meta/{kind}/{key}, kind
including bal.account). Convention: keys `ref.{scheme}` — e.g.
`ref.sat_agrupador`, `ref.sped`. The subtree-rollup machinery then
serves regulator-shaped trial-balance exports by joining meta per
account. The same generalisation gives calendars and machines external
IDs for free.

Provenance: this section encodes accounting-systems practice the
project's author has worked with since 1988; the tree is adopted from
the profession, not invented here.

## 4. Numerics doctrine

Amounts are **int64 minor units** interpreted through the account's
`scale`. Exactness is absolute; SQL sums natively and exactly (keeping
the rebuild oracle a pure `SELECT SUM`); comparisons in the admission
guard are integer comparisons; headroom is ~9.2×10¹⁸ minor units.

**No float64 touches an amount anywhere, ever** — including in transit:
JSON amounts travel as strings (or decode via `json.Number`), are
validated against the account's scale, and convert once to int64.
A test attempts to smuggle a float through every surface and must fail.

## 5. Storage: two planes

**SQL plane — authoritative, guard-bearing.** The append-only journal
and a `balances` table (current value per account) maintained **in the
same transaction** as each entry. This coupling is non-negotiable: the
bounds guard's input must commit-or-abort with the entry it guards; any
architecture that reads the balance from a different engine than the one
receiving the journal write reintroduces the race this primitive
exists to abolish. (This is one derivation of the guard-locality law,
@C04a.) No rollup is ever consulted by a guard.

**Rollup plane — derived, composed over ts.** Transfers emit signed
deltas into the chronicle cascade (monoid `(int64, +, 0)`), yielding
per-account period sums at cascading grains. Cumulative reads chain:
**balance-as-of = nearest sealed checkpoint + SUM of intervening
buckets + unsealed tail** — closing balances stored at sealed period
boundaries make as-of queries independent of journal length. Sealed
periods make their buckets provably frozen: cache-forever semantics by
invariant, not TTL.

Checkpoints also bound the journal's growth: entries older than a
sealed checkpoint are derivationally redundant and may be
archived-then-pruned under retention policy — **prefix-collapse** —
with conservation surviving through the checkpoint chain and the
rebuild oracle re-scoped to the earliest retained checkpoint
(@C04b).

## 6. Admission and concurrency

The transfer guard is the house CAS discipline:
`UPDATE balances SET value = value − ? WHERE account_id = ? AND
value − ? >= floor` (and symmetrically against `ceiling`), rows-affected
checked; zero rows → `XOLU-BAL001`, nothing written. Under contention on
the last unit, exactly one claimant wins; the rest are told, not fooled.

Testing obligation (house style): a T-34-pattern race harness — N
goroutines transferring against one near-floor account, asserting
winners + refusals = N and final balance ≥ floor — **stress-tagged and
registered in the dormant-guards table in the session it is written**,
exercised on multi-core CI.

For implementers who were not present: **T-34** (resolution record,
closed v0.16.1) was a check-then-act race in cal's lifecycle — state
read, decision made, state written as *separate steps*, so two
concurrent terminal transitions could both succeed; it passed every
single-core run for months and surfaced only on multi-core hardware.
The fix is the discipline this section mandates: never read-decide-
write; make the decision *inside* the write's predicate
(`… WHERE state = ?` / `… WHERE value − ? >= floor`) and treat
rows-affected as the verdict. Any bal code path that reads a balance
and then writes based on it in a second statement is T-34 reborn.

## 7. Period close

`bal/close` advances the account-set's seal frontier (the extracted
Sealer): a closed period rejects entries dated within it
(`XOLU-BAL003`), its closing checkpoints are written, and its rollup
buckets freeze. The same append-only past cal enforces for bookings,
applied to books.

## 8. Verification

Two verifiers of different strengths, both wired into the
rebuild-oracle harness and `iolu db check`:

- **Global (the fold):** `SELECT account_id, SUM(amount) FROM journal
  GROUP BY account_id` compared row-for-row against `balances`;
  checkpoint chains re-derived across sealed boundaries.
- **Local (the chain):** per account, every entry satisfies
  `previous_balance + amount = current_balance`,
  `entryₙ.previous_balance = entryₙ₋₁.current_balance`, and versions
  are contiguous. Local verification needs no re-fold, and a lost,
  duplicated, or altered entry is not merely *detected* but
  **localised** to the exact break in the chain — strictly stronger
  audit than the global fold alone. Exact as-of balance is likewise
  readable from any single entry, independent of the rollup plane
  (which remains the fast path; the chain is the exact/audit path).

## 9. Surface

| Endpoint | Purpose |
|---|---|
| `POST bal/def` | Create account (unit, scale, floor, ceiling) |
| `POST bal/transfer` | The atomic two-sided guarded write; optional `memo` (the accountant's asiento description), stored inline on both entries — immutable with the record, per @C04c's corollary. pgledger-parity item from the @B11a review |
| `GET bal/balance` | Current or as-of balance |
| `GET bal/entries` | Journal range for an account |
| `POST bal/close` | Seal a period; write checkpoints |

Errors reserve `XOLU-BAL`:

| Code | Meaning |
|---|---|
| XOLU-BAL001 | Transfer refused: bounds (floor/ceiling) |
| XOLU-BAL002 | Unknown account |
| XOLU-BAL003 | Entry dated within a sealed period |
| XOLU-BAL004 | Amount invalid for the account's scale |
| XOLU-BAL005 | Transfer names a summary (non-postable) account |

Client: four typed methods in `pkg/client`; molu exposes transfer,
balance, and entries as tools — agent commerce made safe by physics
rather than by prompt.

### 9a. Account identity: two-identity split (satisfies @C04d by construction)

bal already uses the substrate-preferred two-identity model (@C04d,
chronicle §4d), the same one cal arrived at independently:

- **External identity:** `account_id` is a namespaced *string*
  (`warehouse:A/widget`, §3) at every API boundary. Being a string, it
  has no numeric width to truncate — bal has no id-boundary bug surface
  at all, by construction, and the id stays human-meaningful for
  operators and agents.
- **Internal identity:** the dense numeric account key (uint32, the
  wave-1 per-primitive width, @P item #8) is engine-internal, used only
  for the compact storage codec, never exposed in JSON, encoded with an
  explicit fixed-width codec and never narrowed to `int`.

Because the number never crosses the wire, bal satisfies @C04d the strong
way — the /ts boundary bug class (uint32 in `int`/`uint16` JSON fields;
`int(ceiling)` overflow on 32-bit) is structurally impossible here.

Implementation obligations at stage 1: keep the numeric account key off
the JSON boundary (never add a numeric `account_id` field to a wire
struct); encode it with an explicit fixed-width codec; and — since the
internal key is still a sized int — ship the @C04d range regression test
for it (full uint32 span incl. values above 2^31 and the ceiling; ceiling
fits uint32; codec round-trip lossless), analogous to
`pkg/timeseries/timeline_id_width_test.go`.

## 10. Future: holds

Reservations (hold five units without moving them; authorise before
capture) are the cal lifecycle applied to quantity: propose against the
balance, confirm or release, the hold counting against admission in the
interim. Explicitly deferred; recorded so the journal schema leaves room
(entry `state` column, default committed) rather than requiring
migration when holds arrive. When built, holds also make bal a
reserve-capable participant in composed transactions — see
@D05.

## 11a. Relation to pgledger

pgledger (Paul Gross, 2025 — a double-entry ledger implemented
entirely in PostgreSQL) is convergent prior art from the same
diagnosis; his companion essay names the thesis outright:
double-entry ledgers as the missing primitive in modern software.
Adopted from it: the per-entry chain triple (previous/current balance
+ account version), which upgrades the journal from oracle-verified to
self-verifying — and which guard locality makes free on either backend
(this is the anti-kernel-fence case: a design decision, not a
backend-gated one). Shared with it: the same-transaction composability
argument against external ledgers (Modern Treasury, TigerBeetle),
which bal obtains through dxp's degradation theorem. Deliberately
different: pgledger is a library-in-your-database — freeform SQL
composition, app-owned transactions; bal is a primitive-behind-an-
engine — guard-enforced bounds and weights, reserved commits (holds),
seal/retention, ts-composed rollups, and agent-safe composition
through declared dxp defs. The trade is the substrate thesis itself.

### Comparison table

Systems compared: **pgledger** (Gross's ledger, implemented entirely as
PostgreSQL functions and views — the GitHub language badge reads "Go"
only because his test/benchmark harness is written in Go; there is no
separate Go ledger); **TigerBeetle** (a dedicated ledger database run
as its own service, included as the performance pole pgledger positions
against); and **bal** on each xolu backend. bal is unimplemented; its
figures are **projections** and labelled so.

| | pgledger (pure PostgreSQL) | TigerBeetle (dedicated ledger database) | bal on the SQLite backend (projected) | bal on a future PostgreSQL backend (projected) |
|---|---|---|---|---|
| What it is | SQL functions and views installed inside an application's PostgreSQL database | A separate, purpose-built ledger service the application talks to over the network | A primitive inside the xolu engine | The same primitive, with PostgreSQL as xolu's storage backend |
| What must be run and operated | A presumably pre-existing PG instance | A dedicated cluster; a new system to learn, run, and monitor | Nothing beyond the xolu process: one static binary, data in files | A PG instance, administered as usual |
| Atomicity with the rest of the application's data | Yes — ledger calls join the app's own transaction (pgledger's central argument) | No — a second system failing independently; consistency needs orchestration | Yes — transfers commit atomically with entities, bookings, and transitions (@D06) | Same, plus the kernel fence (@R05a) |
| Balance bounds (floor/ceiling) | Per-account controls enforced under row locks | Native (e.g. debits may not exceed credits) | Guard-enforced compare-and-set with typed refusals (@B06) | Same, optionally reinforced by database CHECK constraints |
| Reservations / holds (authorise now, capture later) | Not in the base design | Native two-phase transfers | Reserved commits with TTLs and pessimistic/optimistic weights (@D05b) | Same |
| Journal verifiability | Self-verifying: every entry records previous balance, current balance, and an account version, forming a checkable chain | Internal invariants; closed storage format | The same chain (adopted here, above), **plus** the global re-sum oracle and `iolu db check` | Same |
| Accounting-period close and history retention | Not in the base design | Not a feature | Seal frontier closes periods; journal prefix-collapse via checkpoints (@B05, @C04b) | Same |
| Historical/aggregate queries | Plain SQL over the entries table (scans grow with history; point-in-time balance by reading one entry) | Limited query surface | Cascading rollups composed over ts; point-in-time balance = checkpoint + period buckets | Same |
| Concurrency control | PostgreSQL row locks — transfers touching the same account serialise on that account's row | A deterministic single-core state machine processing large batches | Guarded compare-and-set updates over SQLite's one-writer-per-tenant model (@B06) | PostgreSQL row locks, i.e. pgledger's own mechanics |
| Throughput (transfers per second) | Measured by the author on a laptop with stock PostgreSQL: 10,637/s best case; 7,559/s at 2.6 ms/transfer with 10 accounts and 20 workers. Caveat raised in public discussion and acknowledged: the benchmark database was nearly empty | Vendor-class claims in the hundreds of thousands to millions per second, achieved through heavy batching | ~5–6k/s **per tenant** (proxy: cal stress harness, 5.9–6.3k guarded ops/s on M1, same write shape). Tenants are independent writers, so machine throughput adds linearly: twenty busy tenants ≈ 100k+/s | pgledger-class (~7–10k/s stock; identical row work: two entries, two balance updates) |
| Storage per transfer | 743 bytes, measured | 128 bytes, fixed-format | Comparable to pgledger (two chain-bearing entries) plus small rollup deltas in the Pebble plane | Same as the SQLite plane |

### Reading the table

Three conclusions, honestly weighted. **On a single hot account**,
pgledger's row-locked PostgreSQL likely beats bal-on-SQLite by perhaps
1.5–2×; bal-on-PostgreSQL erases that by inheriting the same engine
physics — the gap is backend-shaped and temporary. **At machine
aggregate**, the comparison inverts: pgledger serialises per account
inside one database, while bal's per-tenant writers add linearly, so
fleet-scale projections favour bal substantially. **On feature
surface**, the lower half of the table is where bal stops being
comparable and becomes a superset: holds, period close, retention,
rollups, dual verifiers, and composed commitment are the substrate
speaking, not the ledger. All bal figures remain projections until
stage 2 ships and the race harness speaks. Concurrency verification is
at parity by different means: pgledger's Go tests hunting deadlocks and
balance-clobbering are the same defect class our T-34 harness targets
(@B06); his engine relies on Postgres row locks, ours on the guarded-CAS
discipline — with the @B05 note that a Postgres backend upgrades ours to
the same native locks.

## 11. Non-goals

- **An accounting system.** No chart-of-accounts semantics, no currency
  conversion, no reporting layer — applications build those *on* bal.
- **Windowed analytics beyond sums** — that is ts, which bal composes
  with rather than absorbs.
- **A general constraint engine** — bounds over conserved sums only.

## 12. Staging

1. Chronicle extractions land with this work (substrate doc @B04–5).
2. Journal + balances + guard + XOLU-BAL family + race harness (~2 d).
3. ts composition: delta emission, checkpoints, as-of reads (~2 d).
4. Seal/close + frozen-bucket semantics (~1 d).
5. Client methods, integration-suite flow, molu tools (~1 d).

Roughly a week wholesale, thin because the substrate carries the rest;
stages wait for a consumer — Shelf's stock model being the obvious
first.
