# /cal ID-width audit — wave 1 item #8 companion

Date: 2026-07-20
Scope: verify @P wave 1's per-primitive ID widening obligation
against `/cal` (the second commitment primitive), analogous to the
TimelineID uint16 → uint32 change applied to `/ts` in the same wave.

## Findings

**cal is already uint32-native**: no widening owed.

Evidence:

- `CalOrdinal` (the dense per-tenant calendar identifier that plays
  the same role in cal's codec that `TimelineID` plays in ts's) is
  declared `uint32` in `pkg/cal/cal.go:123`. The type comment even
  cites the intent explicitly: *"32-bit width the codec widens over
  ts's uint16 timeline id"* — meaning cal's design deliberately
  learned the lesson ts is now catching up to.
- The Pebble key codec at `pkg/cal/codec.go:67` already reads
  `binary.BigEndian.Uint32(key[1:5])` — a 4-byte prefix from the
  start.
- `CalendarID` and `BookingID` are `string`, unbounded by design.

No code change owed for cal. Recording as compliant so the release
gate can distinguish "audited compliant" from "not audited yet".

## bal (pre-implementation choice)

Recording the wave-1 obligation here even though bal is not yet
implemented: when bal ships (@B), account IDs should be sized to
match cal's uint32 posture rather than ts's original uint16 —
matching the per-tenant scale ceiling that motivated this wave.
The bal proposal (@B) already indicates hierarchical string codes
(chart-of-accounts, @B03a), which is unbounded; if a dense numeric
ID is introduced alongside for keying, it starts as uint32.
