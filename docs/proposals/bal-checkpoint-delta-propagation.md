# bal checkpoints — delta-propagation instead of stale-flag recompute (proposal)

Updated: 2026-07-28
Status: proposal — T-54 dxp session finding, not yet implemented.
Amends the checkpoint mechanics of `docs/proposals/bal-conservation-primitive.md`
(@B05) and the shipped T-51 fix (0.16.24, `docs/RESOLVED.md`). Does not
touch buckets, the journal, or admission — those are unaffected and
their correctness argument is part of why this proposal works (§2).

## 1. Provenance

Found while auditing the dxp reservation-cache work (T-54) for a
different concern: whether rollups need range-based invalidation
rather than exact-match, prompted by scoping cal's dxp participation.
Tracing bal's own checkpoint mechanism to check surfaced a real,
currently-latent defect in **unbuilt** work: item 16 (seal/close +
prefix-collapse retention) cannot ship as currently sketched without
silently corrupting as-of balances for `backdated`-policy accounts —
the museum case, where a fact about the past arrives at the present
and must be inserted at its true past position.

Nothing shipped is wrong today. The gap is that two functions —
`Checkpoint`'s recompute and `VerifyCheckpoints`'s oracle check — both
depend on summing the full journal from the epoch, and item 16's whole
purpose is to make that sum unavailable for pruned ranges. The moment
prefix-collapse ships, both break the same way at once, so the oracle
cannot catch the defect it exists to catch.

## 2. The insight

A checkpoint is `SUM(journal WHERE at <= boundary)` — a fold under
`SumInt64`, exactly like a rollup bucket. `SumInt64` is associative and
commutative. Buckets already exploit this: `chronicle.Engine.Append`
folds a delta into every level's bucket directly (`Combine(cur, v)`),
regardless of insertion order, and never needs to consult the other
terms that make up the sum. That is *why* backdated entries already
fold correctly into bal's rollup buckets today, with no invalidation
step at all (verified: `EmitDeltas` → `Append` runs unconditionally for
every transfer, backdated or not, and nothing about it depends on
whether earlier data still exists).

Checkpoints don't get this for free today only because `Checkpoint`
and `VerifyCheckpoints` both *recompute the fold from its terms*
instead of *combining a correction into the existing fold*. A fold
under an associative monoid never needs to be recomputed from its
terms to absorb one more term — it only needs the new term combined
in. The fix is to make checkpoints behave the way buckets already do.

## 3. The mechanism

Replace the stale-flag write in `transferInTx` (currently `UPDATE
bal_checkpoints SET stale = 1 WHERE account_key IN (?, ?) AND at_unix
>= ?`) with two signed delta-adjustments, one per leg, in the same
transaction as the entry:

```sql
UPDATE bal_checkpoints
   SET balance = balance + ?      -- signed: -amount for the debit leg, +amount for the credit leg
 WHERE account_key = ? AND at_unix >= ?
```

Two statements (one per `account_key`) because the two legs carry
opposite signs — the current single `IN (?, ?)` form can't express
that. `at_unix >= entry_time` is unchanged from today's staleness
range and still correct for the same reason: a checkpoint boundary
sums everything at-or-before it (`j.at <= boundary`), so a checkpoint
whose boundary equals the entry's instant already includes it and must
be adjusted too — `>=` captures exactly that.

This runs unconditionally, for every transfer, not just backdated
ones — a forward-dated entry landing after every existing checkpoint
matches zero rows (cheap, correct no-op), so there's no need to gate
this on temporal policy or on whether the entry is actually backdated.
One code path, always correct.

## 4. Why eager is now safe (it wasn't, before)

T-51's original defect record raised the cost objection directly:
*"naive recompute is O(checkpoints at or after the entry). A museum
backdating to 1897 against 130 years of monthly checkpoints rewrites
~1,560 rows per accession."* That's why the shipped fix went lazy
(mark stale, defer the cost to an explicit later `Checkpoint` call).

The objection was real, but it was an objection to *recompute* cost,
not to *touching every affected checkpoint*. Recompute is O(checkpoints
× journal-length) — every one of those 1,560 rows required its own
full journal sum. Delta-adjustment is O(checkpoints) — 1,560 rows,
each a single cheap `balance = balance + constant`. Rewriting 1,560
rows with a scalar increment inside the transaction that already holds
the tenant's write lock is not the cost the original objection was
about. The lazy design was working around the wrong half of the
problem; fixing the recompute cost removes the reason to defer at all.

## 5. What this removes

- **The `stale` column becomes dead weight.** Every checkpoint is
  correct the instant its transaction commits; there is no "stale"
  state to represent. `BalanceAsOf`'s `WHERE stale = 0` filter and its
  skip-back-to-an-older-checkpoint fallback both become unnecessary —
  the *nearest* checkpoint at-or-before `t` is always trustworthy.
- **The lazy-recompute path in `Checkpoint`** (`SUM(journal WHERE at <=
  ?)`) is still needed for what it was actually for — sealing a *new*
  period boundary that has no checkpoint yet — but is no longer the
  repair mechanism for backdating. It stops being called to "fix" an
  existing checkpoint; existing checkpoints are never wrong.
- **`VerifyCheckpoints`'s stale exemption** goes away — every stored
  checkpoint is checked, unconditionally, which is strictly stronger
  than today's oracle (a checkpoint can no longer hide behind
  staleness while actually being wrong for an unrelated reason).

Column removal itself: leave `stale` in place as an inert, always-zero
legacy column rather than an ALTER-TABLE drop — matching the house's
existing legacy-detection convention (e.g. `temporal_policy`'s
default-on-missing-column pattern) rather than a migration that could
fail on an unusual existing deployment. A future session can drop it
once no live checkpoint predates this change.

## 6. Buckets: unaffected, and worth saying so explicitly

Nothing here touches `EmitDeltas` / `Append`. They were already correct
for the reason in §2, before this proposal existed. This document is
scoped entirely to checkpoints, and the completeness of that scoping —
buckets don't need a matching fix — is itself part of the claim, not
an oversight.

## 7. Prefix-collapse (item 16) compatibility

This is the point of the exercise: once item 16 prunes journal entries
older than a sealed, collapsed checkpoint, delta-adjustment keeps
working exactly as before — it never reads the journal, so pruning it
changes nothing. Recompute-from-journal (both `Checkpoint`'s current
form and `VerifyCheckpoints`'s oracle query) would silently degrade the
moment pruning starts. This proposal is a prerequisite for item 16
shipping safely for `backdated`-policy accounts, not an optional
hardening.

**Not solved here, flagged for item 16's own design:**
`VerifyCheckpoints`'s oracle still sums the journal from the epoch for
its comparison value, which will need the same re-scoping the bal
proposal already names as owed — *"the rebuild oracle re-scoped to the
earliest retained checkpoint (@C04b)."* This document doesn't attempt
that re-scoping; it only removes the *stale-exemption* half of the
oracle's current design, which is orthogonal and safe to do now,
independent of when the re-scoping work happens.

## 8. Interaction with the dxp work (T-54, item 19)

Single point of change: `transferInTx` (`pkg/bal/store.go`), shared by
`Store.Transfer` (the ordinary path) and `bal.Adapter.Execute` (the dxp
path) since T-54's session extracted it. No dxp-specific work is
needed — both paths get the fix from one edit, exactly the payoff the
extraction was for.

## 9. Testing obligations

- `TestPolicy_BackdatedInvalidatesCheckpoint_T51` needs its assertions
  updated: today it checks the checkpoint is *stale* after the
  backdated entry and requires an explicit second `Checkpoint` call to
  correct it. Under this proposal the checkpoint is correct
  immediately — no second call, no staleness window — so the test
  should assert `BalanceAsOf` returns 157 right after the backdated
  transfer, with no intervening recompute.
- `TestPolicy_VerifyCheckpointsCatchesDivergence` is unaffected (it
  manufactures a divergence directly via a corrupting `UPDATE`,
  unrelated to backdating) and should still pass unchanged — worth
  running as a regression check, not worth rewriting.
- New: a multi-checkpoint range test — several checkpoints spanning a
  backdated entry's position, asserting every one at-or-after the
  entry's instant is adjusted by the correct signed amount and every
  one before it is untouched.
- New: the two-leg sign test — confirm the debit and credit legs'
  checkpoints (different accounts, opposite signs) both land correctly
  from the two separate `UPDATE` statements.
- Re-run `TestRollup_*` (bucket cascade tests) unchanged as a
  regression check — this proposal shouldn't touch their behavior at
  all, and confirming that is cheap.

## 10. Non-goals

- Does not implement item 16 (prefix-collapse) itself.
- Does not re-scope `VerifyCheckpoints`'s oracle query for a pruned
  journal — flagged in §7 as item 16's own obligation.
- Does not touch cal. Cal's version of "range, not exact match" is a
  genuinely different problem (interval overlap on a calendar, not
  fold-correction on a monoid) and needs its own design, not this one
  generalized.
- Does not change admission, sealing, or the temporal-policy vocabulary
  (T-55) — those are orthogonal and unaffected.

## 11. Open questions

- Drop the `stale` column outright now, or leave it inert per §5's
  migration-safety argument? Leaning inert-for-now; asking rather than
  deciding, since it's a one-line schema decision with no compelling
  reason to resolve before implementation starts either way.
- File as its own register item, or fold into item 16's eventual scope
  directly (since §7 argues it's a prerequisite, not an independent
  improvement)? Leaning towards its own item — it's implementable and
  valuable *before* item 16 exists, and blocking on item 16's own
  (unscheduled) start would sit a cheap, self-contained fix behind a
  large, unscheduled one for no reason.
