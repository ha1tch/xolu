# `/obj` and `/loc` — stress-test findings record

Updated: 2026-07-31. Not a proposal document — a citable record of the
adversarial design review that produced `obj-00-design.md` and
`loc-00-design.md`'s revision, so a future reader can see why a
decision was made without re-mining a chat transcript. Two passes are
recorded: a first pass of ten deliberately varied CMMS/EAM scenarios
against an earlier, single-primitive `/loc` design, and a second,
longer thread (Coca-Cola distribution, hospital/mortuary logistics)
that pursued specific findings from the first pass to the point of
resolution. Final terminology is used throughout, not the phrasing
that existed mid-discussion.

## How to read the status column

- **Clean fit** — the design already handled this correctly; included
  for balance, not just to record problems.
- **Resolved** — a real gap, closed in `obj-00-design.md` or
  `loc-00-design.md`'s revision. Cited by section.
- **Open** — named explicitly, not resolved. Carried forward in both
  design documents' own §13/§15.
- **Confirmed boundary** — looked like a gap on first inspection, and
  turned out to be a correctly-drawn scope line instead. Recorded
  because the negative result has its own value: it's evidence the
  subject/`bal` boundary and the policy-non-goal boundary were checked
  against a real case, not just asserted.

## Pass 1 — ten scenarios against the original single-primitive design

| # | Scenario | Status | Where addressed |
|---|---|---|---|
| 1 | Field technician visiting multiple customer sites/day | Clean fit, with caveat | `report`-only tracking works; requires sites pre-fenced, which most deployments won't do by default — not a design gap, a deployment-data gap |
| 2 | Asset sent off-site to a third-party repair vendor | **Resolved** | `obj-00-design.md` §12 — nil/off-site is a first-class lifecycle state |
| 3 | Vehicle fleet, continuous GPS, service-radius fence | Clean fit | The design's own flagship case; no changes needed |
| 4 | High-frequency warehouse bin putaway (forklift) | **Resolved** | `loc-02-implementation.md` Stage 2 — throughput benchmark requirement added, not previously stated. Same standard as every other "Resolved" entry: the design now has an answer it didn't have before. An earlier version of this table hedged this one entry as less resolved than the rest because no benchmark has actually been *run* — inconsistent, since nothing in this entire body of work has been run; corrected on review |
| 5 | Pallet carrying sub-assets, moved as one unit | **Resolved** — the single strongest finding of pass 1 | `obj-00-design.md` §3–§5: the manipulability test, containment as entity-to-entity edges, universal cycle safety |
| 6 | Loading dock — real-time capacity *and* time-windowed booking | **Resolved** | The actual location/containment content of this scenario — is the dock physically full — was closed before this stress-testing began, by `loc`'s own original §5a fence-capacity guard. What was held open past that point was a *different*, self-imported question (should a `cal` booking arbitrate against that guard) that was never `/obj`/`/loc`'s mandate — this system solves location and containment, and only incidentally turns out to touch capacity; extending it to booking-arbitration is a possible future win, not a gap in the current job. Written into both design documents (`loc-00-design.md` §15, `obj-00-design.md` §13) |
| 7 | Hazmat containment — capacity plus regulatory entry/exit | **Confirmed boundary** | `obj-00-design.md` §2 — capacity is handled; identity-gated entry and dwell-limit enforcement are a deliberate, named non-goal, not an oversight |
| 8 | Compressor removed from a vehicle, tracked independently | **Confirmed boundary** | Entity-lifecycle question, correctly outside `/obj`'s scope; no gap found |
| 9 | Mobile location itself moving (ship, site trailer) | **Resolved for the device-equipped case; small residual named** | For any moving anchor with a continuous position feed (a ship's AIS, a tracked convoy): store the anchor's own position history in a `/ts` timeline: historical resolution = the contained subject's unchanging relative offset composed with the anchor's `/ts`-queried position *as of* the query date, no versioned-placement mechanism needed inside `/loc` at all. Checked directly against `loc-00-design.md` §8: the movement journal records *subject* movement only, never a location node's own anchor changing — so a **discretely-repositioned, non-device anchor** (a site trailer relocated twice a year by `PATCH`, no continuous signal to feed `/ts`) still has no historical record at all. Residual fix, small: `PATCH`'s anchor-update path should append one journal entry too, the same discipline §8 already applies to subject moves — not a `/ts` feed, since there's no continuous signal to store. Both the `/ts` resolution and the residual (still open) are written into `loc-00-design.md` §5b, and `obj-00-design.md` §11/§13 for the self-anchored-fence case specifically |
| 10 | Cross-facility / cross-tenant asset transfer | Mostly clean fit, one boundary confirmed | Same-tenant cross-tree transfer was never actually blocked; genuine cross-tenant transfer correctly deferred to `far-and-dxp-mxn.md`, not `/obj`'s concern |

## Pass 2 — extended thread, pursued to resolution

| Finding | Status | Where addressed |
|---|---|---|
| Containment vs. location — one primitive or two? | **Resolved** | Two primitives, sharing one subject-addressing convention and one guard-locality discipline — not merged, not fully separate. `obj-00-design.md` throughout; the "same closure principle, different mutation-safety profile" argument is the core of the split |
| `/obj` rename and generalization (lorry, bottle, case, truck as units) | **Resolved**, with real corrections along the way | Universal cycle safety (not opt-in per-node flagging); individual-unit-vs-bulk-quantity fork named explicitly; multi-dimensional capacity |
| A fence "can't be an object — it's a where, not a what" | **Resolved** — confirmed and sharpened | `obj-00-design.md` §2 — object is "has a position," fence is "tests a position"; duals of each other, not instances of the same relation |
| Relational/plural position (lorry → Camden → distributed to 3 restaurants) | **Confirmed boundary** | Every apparent case of plural position decomposed into: one object, one position, plus a `bal` quantity doing the multiplying. No plural-position capability was ever needed |
| Bottles come in cases — packaging hierarchy | **Resolved** | `obj-00-design.md` §8 — the `bal` boundary is manipulability, not packaging level; promote/demote is the transition between tiers, not a new tier itself |
| Bricks are sold in approximate packs, not exact counts | **Resolved** | `obj-00-design.md` §8 — commercial units resolve upstream of any guard; the guard-bearing fact is always the exact physical unit |
| Ship containing containers containing pallets; ship docked at a `/loc` harbour | **Resolved** | Hold-tree stays `/loc`-shaped (fixed, rare structural change); ship composes `obj.position`; the tree's root placement resolves through the ship's own position — `loc-00-design.md` §5's composition case |
| Bricks can't skip a containment child (packaging as a physical constraint) | **Resolved** | `obj-00-design.md` §3 — manipulability test refined from "distinguishable" to "distinguishable *and* independently movable" |
| Do we need both `/obj` and `/loc`? | **Resolved** | Yes — different guard disciplines over structural-change rate (rare/administrative vs. constant/ordinary), and `/loc` carries real geometric machinery (`Placement` composition) `/obj` has no analog for |
| "Loaded lorries must never enter the parking lot" — cross-primitive guard | **Resolved**, then **corrected** | First proposed as a new obj-containment-closure-over-a-bal-predicate guard mechanism inside the substrate; **rejected on review** — the actual answer is the application watching the existing event feed and refusing to issue the call, `obj-00-design.md` §2's policy non-goal, not new substrate machinery |
| Hospital compliance examples (surgical instruments, controlled substances) | **Resolved as a non-goal**, consciously | Same resolution as above, generalized; the substrate-guard-vs-application-policy guarantee-strength gap is named explicitly rather than papered over — `obj-00-design.md` §13 |
| Prosthetic "attached to" a patient — position relative to an entity | **Resolved**, with a real correction mid-thread | First anchored to "patient" (wrong — an administrative entity with no position); corrected to the *body*, a real physical `/obj`. Named attachment slots left as an explicit open question |
| Deceased patient / body → morgue — entity and object lifecycles diverge | **Resolved** — produced a genuinely new finding | `obj-00-design.md` §12 — permanent retirement, a terminal state distinct from off-site and from demotion; an object's tracking was never conditional on any particular entity state |
| Can `/obj`'s guard logic reuse `pkg/graph`? | **Resolved** | `obj-00-design.md` §5, §10 — the live graph can never back a guard decision (derived, hydrated, same category as `cal`'s H3/`bal`'s rollup), but `wouldCreateCycle`'s bounded-BFS algorithm is worth extracting, and every read-only traversal function is reusable directly once containment edges are mirrored post-commit |
| Cross-primitive `REF` extension for non-entity primitives | **Resolved — rejected as unnecessary** | `obj-00-design.md` §4 — dissolved once subjects were fixed as always-entities; no new addressing scheme was ever needed |
| `Sulpher` hydration silently opaque to non-entity graph nodes | **Resolved — rejected as unnecessary**, same reason | `obj-00-design.md` §4 — the concern was real and was verified directly against `pkg/sulpher`'s executor, but doesn't apply once `/obj` never mints a non-entity graph node in the first place |
| Should `/obj` follow `/meta`'s design (attach to entities)? | **Resolved — became the central decision** | `obj-00-design.md` §4 — subject-addressing convention adopted from `/meta`'s namespaced-subject pattern; engine-inertness explicitly *not* adopted, since `/obj` is guard-bearing |
| Should `/loc/fence` compose onto entities the same way? | **Resolved**, sharpened from the first proposal | `loc-00-design.md` §5 revision — not every fence needs a *new* dedicated place-entity; some compose onto an entity that already has a position (self-anchoring), some need a dedicated position-free place-entity. Both are legitimate, conflating them would have been wrong |
| Composition: can one entity have both `obj` and `loc` capability? | **Resolved** — closes the self-anchoring and ship-hold cases cleanly | `obj-00-design.md` §11 / `loc-00-design.md` §5 — expected, not exceptional; sharpens rather than removes the non-versioned-placement gap, since a self-anchored fence now inherits it too |

## What remains genuinely open, across both documents

Three items now, not four — #6 dropped off this list entirely on
review (below), and #9's item is narrower than it was, not removed:

1. **Non-versioned placement, residual case only.** The general
   problem is resolved for any moving anchor with a continuous
   position feed (§9's `/ts` mechanism, above). What remains open is
   narrower than the original finding: a discretely-repositioned,
   non-device anchor has no historical record of its own past anchor
   at all. `obj-00-design.md` §13 still names the broader item; it
   should be narrowed to match on its next revision.
2. **Named attachment slots** — whether prosthetic/device-to-body
   relationships need their own edge shape distinct from containment.
   `obj-00-design.md` §5, §13. Unchanged by this pass.
3. **The guarantee-strength gap** between a substrate guard and an
   application-enforced policy check, for any case resembling the
   hospital examples. `obj-00-design.md` §13. Unchanged by this pass —
   not reconsidered here, only the two items above were.

**Removed from this list:** `loc`+`cal` composability (#6) — was never
actually an open item in `/obj`/`/loc`'s own mandate; see #6's
corrected row above.

## Provenance note: this record was briefly ahead of the design documents; both are now in sync

Three corrections landed in dialogue — #4's inconsistent standard,
#6's resolution, #9's partial resolution. For a period after that,
neither `loc-00-design.md` nor `obj-00-design.md` had been edited to
reflect the latter two, which meant this record and both design
documents briefly disagreed. Both have since been revised to match:
`loc-00-design.md` §5b carries the `/ts` resolution and the narrowed
residual directly (a third revision pass, documented in its own
header), §15 strikes the `loc`+`cal` item with the corrected reasoning
rather than deleting it silently, and `obj-00-design.md` §11/§13 carry
the same two corrections, including the fact that a self-anchored
fence on a continuously-tracked entity turns out to be covered by the
identical `/ts` mechanism, not a separate case. The residual named in
#9's row above — discretely-repositioned, non-device anchors still
having no historical record — remains genuinely open in all three
documents; that part was never claimed resolved.

## Note on the original documentation-campaign plan

The plan named a separate composition/integration document as a
fourth deliverable. It is not written as its own file: by the time
`obj-00-design.md` was drafted, its own §4 (rejected alternatives —
`REF` extension, `Sulpher` hydration) and §11 (composition with `/loc`)
already carried this material in full, and a separate document would
have substantially duplicated both rather than adding anything. Recorded
here as a deliberate consolidation, not a dropped deliverable.
