// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package loc is the spatial primitive (wave 9, T-115..T-118): tracking
// where things are, both as tree-structured containment (a site, a
// building, a floor, a room, a bin) and as geofenced regions (a yard, a
// jurisdiction, a service radius). Canonical state is SQL throughout —
// no bit-packed codec, no Pebble plane — mirroring bal's shape, not
// cal's or ts's (loc-00-design.md §6a).
//
// Two write paths, not one — read this before touching anything else
// in this package (loc-01-rest-api.md §0, reproduced here verbatim so
// this package's own doc states the same contract its wire surface
// does, not a paraphrase drifting from it over time):
//
//   - move — explicit tree-leaf reassignment, by location id. No
//     geometry involved: a leaf's membership in its ancestors is
//     identity (prefix parentage, loc-00-design.md §3a), not a
//     geometric test. This is the only way a subject's canonical
//     tree-leaf position ever changes.
//   - report — a raw coordinate. Location nodes carry a Placement
//     (loc-00-design.md §3b) — an offset-and-rotation transform, not a
//     boundary shape — so a coordinate cannot be tested against a leaf
//     the way it can against a fence, which does carry real Geometry.
//     report therefore only ever resolves fence membership. It never
//     sets or changes tree-leaf assignment.
//
// A subject can be tree-assigned (via move), fence-tracked (via
// report), both, or neither. Nothing in this package silently promotes
// a report into an implicit move, and nothing infers a fence crossing
// from a move alone unless the destination leaf happens to be
// tree-aligned with a fence, in which case that fence's membership
// follows the free tree walk automatically — no separate report needed
// for that specific fence.
//
// Decisions pinned at Stage 0 (loc-02-implementation.md), so later
// stages cannot drift from them:
//
//  1. Tenancy follows bal's pattern, not cal's. cal needs a manager
//     type (enable/disable, per-tenant lifecycle) because cal can be
//     absent from a given server instance; nothing about loc names an
//     equivalent reason. A plain per-request store constructor is the
//     default; a manager type is added later only against a concrete
//     need. Storage lives at <data-root>/tNNNN/loc/, a sibling to
//     store/ and ts/ (storelayout.TenantLocDir) — loc's own tables
//     (locations, fences, capacity, journal) are numerous enough to
//     warrant their own file, the same reasoning ts already applies.
//
//  2. Coordinate fields decode as plain typed float64 struct fields,
//     full stop — never through a raw untyped-map decode step or a
//     string intermediate. bal's Amount precedent needed that raw-map
//     step because Amount requires custom decimal parsing; loc's
//     coordinates have no equivalent reason, and a bare JSON number
//     decoded directly into a float64 field is already rejected by
//     encoding/json if malformed, with no literal NaN/Infinity token
//     in the JSON spec to smuggle through. If a string-based coordinate
//     path is ever introduced (a GeoJSON string export/import, say),
//     that path needs an explicit math.IsInf/math.IsNaN guard after
//     strconv.ParseFloat — strconv.ParseFloat accepts "NaN"/"Inf" as
//     valid input even though JSON never permits them as bare tokens.
//     Regression guard: grepping this package for Go's untyped JSON
//     object type outside test files must return nothing.
//
//  3. Client library (pkg/client) and iolu CLI support are explicit v1
//     non-goals, stated rather than left silent. bal's own client
//     methods shipped as T-67, well after bal itself was solid — the
//     same deferral is correct here.
package loc
