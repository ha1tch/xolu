# xolutime (`ot`)

```go
import ot "github.com/ha1tch/xolu/pkg/xolutime"
```

`xolutime` secures xolu's single time invariant. It is a **thin enforcement
package**, not a calendar layer: it guarantees discipline, it does not own
policy.

## The invariant

1. A persisted timestamp is an absolute instant, stored as **UTC**.
2. The only sanctioned source of "now" for a persisted value is `ot.Now()`.
3. Wall-clock-plus-zone, if a layer needs it, is a **separate explicit field
   that layer owns**. The storage primitive sees the instant only.
4. Human-facing formatting **always takes an explicit zone**. There is no
   implicit session/server zone anywhere in xolu (the PostgreSQL `timestamptz`
   footgun, where a value's meaning silently shifts with the configured zone).

## What it is NOT

`xolutime` has no concept of recurrence, no stored time zone, and no DST-aware
calendar arithmetic. Those belong **above** the storage primitives, in whatever
layer owns the user's zone and intention. That layer resolves "9am Montevideo,
weekly" into concrete absolute instants *before* handing them to storage. Keeping
that out of `xolutime` is the whole point: a storage time package that grows zone
and recurrence handling has become the calendar engine the codec was designed not
to be.

`xolutime` also does **not** govern duration measurement. A `start := time.Now()`
used only with `time.Since` is correct as written and must keep Go's monotonic
clock reading — `.UTC()` would strip it. Use `xolutime` for values you **store or
serialize**; use the standard library directly for stopwatches, TTLs, and
rate-limit windows.

## Surface

| Constructor | Use |
|---|---|
| `ot.Now()` | current instant, UTC — for persisted values |
| `ot.FromTime(t)` | normalize an arbitrary `time.Time` to UTC, instant preserved |
| `ot.FromUnixNano(ns)` | reconstruct from epoch-nanoseconds (matches the `ts` codec) |
| `ot.Parse(s)` | RFC 3339 → `Instant`, **rejects zone-naive input** |
| `ot.MustParse(s)` | `Parse` for constant literals (panics on error) |
| `ot.Zero()` | the epoch, as an explicit "unset" sentinel |

`Instant` methods: `Time()`, `UnixNano()`, `IsZero()`, `Before/After/Equal`,
`Add`, `Sub`, `String()` (RFC 3339 UTC), `Format(layout, loc)` (explicit zone),
and JSON marshalling that round-trips and rejects naive input.

## The lint guard

`lint_test.go` walks `pkg/` and fails if a bare `time.Now()` flows into a
persisted or compared wall-clock value (`CreatedAt`, `UpdatedAt`, `cutoff`, …),
while leaving duration-measurement calls untouched. It distinguishes the two by
**syntactic role**, not surface text. `cache` and `middleware` are exempted as
units (their `time.Now()` use is monotonic/ephemeral); revisit if either starts
persisting a timestamp.

Run it:

```bash
go test ./pkg/xolutime/ -run TestNoBareWallClock
```

## Relationship to the storage primitives

`ts` already follows this invariant uniformly (every decode forces `.UTC()`).
`xolutime` makes the rule explicit and shared, so `cal`, the job managers in `oql`
and `sulpher`, and anything future use one definition of "an xolu instant" rather
than each re-deriving it. For `cal` specifically: the codec stores the UTC
instant; the booking intention and its zone, if needed for re-display or
DST-rule-change recovery, are the caller's to keep — `cal` re-issues a `move`
when an upstream layer recomputes a shifted instant.
