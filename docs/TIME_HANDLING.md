# Time Handling in xolu

This guide states xolu's single invariant for date and time data and explains
the boundary it draws. It applies to every package that stores, serializes, or
compares a moment in time.

The rule is enforced mechanically, not just by convention: see "The lint guard"
below.


## Why a single rule?

A timestamp can mean two different things, and conflating them is the source of
most time bugs:

1. An **absolute instant** — a specific point on the universal timeline, the
   same moment everywhere on Earth. "When did this measurement arrive." "When was
   this row written." These have no time zone; they *are* a count from an epoch.

2. A **wall-clock intention** — what a human means by "9 a.m. next March," which
   is a local reading plus a zone, and which only becomes an absolute instant once
   you resolve it against that zone's rules (rules that can change between now and
   then).

xolu storage deals exclusively in the first kind. The second kind is real and
legitimate, but it belongs to the layer that owns the user's zone, never to
storage. Mixing them — storing a wall-clock reading as if it were an instant,
or letting an instant's meaning shift with whatever zone a server happens to be
configured for — is the PostgreSQL `timestamp`-without-zone footgun, and xolu
refuses to walk into it.


## The invariant

1. **A persisted timestamp is an absolute instant, stored as UTC.** Every value
   that is written to a store or serialized onto the wire is UTC. No exceptions.

2. **The only sanctioned source of "now" for a persisted value is
   `xolutime.Now()`.** A bare `time.Now()` carries the host's monotonic clock and,
   if stored without `.UTC()`, silently records local time on a non-UTC host —
   invisible in a UTC-configured test container, wrong in production.

3. **Wall-clock-plus-zone, if a layer needs it, is a separate explicit field that
   layer owns.** The storage primitive sees the instant only. A recurring "9 a.m.
   Montevideo" booking is resolved to concrete absolute instants *above* storage,
   by the layer holding the zone, before any instant reaches a store.

4. **Human-facing formatting always takes an explicit zone.** There is no
   implicit session or server zone anywhere in xolu. `xolutime.Instant.Format`
   requires the caller to name the location; there is deliberately no zone-less
   variant.


## The package: `xolutime` (`ot`)

The invariant is embodied in `pkg/xolutime`, conventionally imported as `ot`:

```go
import ot "github.com/ha1tch/xolu/pkg/xolutime"
```

It is deliberately thin. It defines `Instant` (a UTC-guaranteed newtype over
`time.Time`), the sanctioned constructors (`Now`, `FromTime`, `FromUnixNano`,
`Parse`, `MustParse`), instant arithmetic and comparison, and zone-explicit
formatting. `FromUnixNano`/`UnixNano` agree byte-for-byte with the timeseries
codec's encoding (`time.Unix(0, ns).UTC()` and `uint64(ts.UnixNano())`), so `ts`
keys and `xolutime` share one definition of an instant.

`xolutime` knows nothing about time zones as stored data, recurrence, or
DST-aware calendar arithmetic, and it must stay that way. A storage-time package
that grows zone fields and recurrence expansion has become the calendar engine
the storage layer was designed not to be. See "The boundary" below.


## What `xolutime` does NOT govern: duration measurement

Measuring elapsed time is not a storage concern and is explicitly outside the
invariant. This is correct and must not be "fixed":

```go
start := time.Now()        // correct — keeps Go's monotonic reading
doWork()
elapsed := time.Since(start)
```

Calling `.UTC()` here would strip the monotonic clock component and *degrade*
the measurement. Use the standard library directly for stopwatches, TTL
countdowns, rate-limit windows, and metrics timing. Use `xolutime` only for values
you store or serialize.


## Input boundary: reject zone-naive timestamps

`ot.Parse` accepts an RFC 3339 string only if it carries an explicit zone (a
trailing `Z` or a `±hh:mm` offset). A zone-naive string such as
`2026-07-08T09:00:00` is rejected, because it is not an absolute instant and
there is no honest way to store it without guessing a zone.

Any API surface that accepts a timestamp from a client must parse it through
`ot.Parse` (or apply the equivalent rule) rather than `time.Parse`, so the
refusal happens at the boundary and not as a silent mis-storage later. The `cal`
booking `when` field is the first surface to adopt this; see
`docs/proposals/cal-rest-api.md`.


## The boundary (what xolu does NOT solve)

It is worth being explicit: this invariant addresses the *instant* half of time
handling. It does not solve the *calendar* half — time-zone presentation across
many users, daylight-saving arithmetic, and recurrence-rule expansion. xolu's
answer to all of that is the same: it lives **above** the storage primitives, in
whatever layer owns the user's zone and intention.

This is a deliberate architectural choice, not an omission. But it has one
consequence that any upstream layer must handle and that xolu cannot handle for
it:

**DST / zone-rule changes can invalidate a stored instant's intention.** Once a
wall-clock intention ("9 a.m. Montevideo, 2027-03-15") is resolved to an absolute
instant and stored, the original intention is gone from storage by design. If the
zone's offset or DST rules change between resolution and the booked moment, the
stored instant now denotes a different wall-clock time than the human meant, and
**storage cannot detect this**, because it never kept the intention. Recovery is
the owning layer's responsibility: it must retain `local_time + zone_id` as its
own source of truth, recompute the instant when zone rules change, and issue the
correction (for `cal`, a `move`). xolu provides the instant arithmetic for the
recomputation; it does not provide the trigger, the retained intention, or the
detection. This exposure is real, it is permanent, and it is located here so that
upstream authors meet it deliberately rather than by surprise. See
`docs/KNOWN_ISSUES.md`.


## The lint guard

`pkg/xolutime/lint_test.go` walks the tree and fails the build if a bare
`time.Now()` flows into a persisted or compared wall-clock value, while leaving
duration-measurement calls untouched. It distinguishes the two by syntactic
role, including the address-of-temp idiom (`x := time.Now(); rec.At = &x`) that a
naive matcher misses.

```bash
go test ./pkg/xolutime/ -run TestNoBareWallClock
```

The guard is a **regression catcher, not a proof of exhaustive cleanliness.** It
recognises the common ways a persisted timestamp is written; it cannot recognise
every possible dataflow (for example, a `time.Now()` passed as an argument and
stored two functions away). Its known limits are recorded in `docs/KNOWN_ISSUES.md`
and as comments in the test. Treat a green result as "no *known-shape* regression,"
not "the tree is provably UTC-clean everywhere."
