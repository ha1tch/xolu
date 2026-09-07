// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// types_loc.go — wire types for the core loc.go client methods
// (XOT211, wave 15): def/list/get/patch/delete/report/move. Field
// shapes verified directly against pkg/server/v2_loc_handlers.go's
// own request/response structs before being written here.
//
// Fences (attach/list/get/delete/patch/reconcile) are deliberately
// NOT covered by this file -- split out as its own follow-up rather
// than silently delivering less than this item's own originally
// filed scope without saying so. Nothing in this wave's own other
// items depends on fences.

// LocAnchor is a location's own geo-referenced point -- present only
// on a location whose own placement chain includes an anchor
// (directly or inherited).
type LocAnchor struct {
	Lat       float64 `json:"lat"`
	Lon       float64 `json:"lon"`
	Alt       float64 `json:"alt"`
	TrueNorth float64 `json:"true_north"`
}

// LocPlacement is a location's own placement relative to its parent
// (offsets + rotation), plus an optional Anchor establishing (or
// re-establishing) a real-world geo reference at this node -- most
// locations inherit their anchor from an ancestor and never set their
// own.
type LocPlacement struct {
	OffsetX  float64    `json:"offset_x"`
	OffsetY  float64    `json:"offset_y"`
	OffsetZ  float64    `json:"offset_z"`
	Rotation float64    `json:"rotation"`
	Anchor   *LocAnchor `json:"anchor,omitempty"`
}

// LocDefineRequest defines or reconfigures a location. Postable
// governs whether obj/entities may be placed directly on this node
// (false for a summary/grouping node, matching bal's own postable-
// account distinction); nil leaves the server's own default. Capacity
// is a plain item-count ceiling, nil for unlimited.
type LocDefineRequest struct {
	LocationID string       `json:"location_id"`
	ParentID   *string      `json:"parent_id"`
	Name       string       `json:"name"`
	Postable   *bool        `json:"postable,omitempty"`
	Capacity   *int64       `json:"capacity,omitempty"`
	Pattern    *string      `json:"pattern,omitempty"`
	Placement  LocPlacement `json:"placement"`
}

// LocLocation is the shape every loc read/write that returns a
// single location uses (define, get, patch, and each entry in
// LocListResult).
type LocLocation struct {
	LocationID     string       `json:"location_id"`
	ParentID       *string      `json:"parent_id"`
	Name           string       `json:"name"`
	Postable       bool         `json:"postable"`
	Capacity       *int64       `json:"capacity,omitempty"`
	Pattern        *string      `json:"pattern,omitempty"`
	PatternID      *string      `json:"pattern_id,omitempty"`
	PatternDeleted *bool        `json:"pattern_deleted,omitempty"`
	Placement      LocPlacement `json:"placement"`
	Warnings       []string     `json:"warnings,omitempty"`
}

// LocListResult is GET /loc/list's own response shape.
type LocListResult struct {
	Locations []LocLocation `json:"locations"`
}

// LocPatchRequest partially updates a location. Every field is
// tri-state, matching the server's own raw-map-keyed patch semantics
// exactly: a nil field is genuinely absent from the request and
// leaves that property unchanged; Capacity is a double pointer for
// exactly this reason -- a non-nil *Capacity pointing at a nil
// *int64 explicitly clears the ceiling to unlimited, while a nil
// outer pointer (the zero value) omits the field entirely and leaves
// the existing ceiling untouched. Name and Placement don't need the
// same double-pointer treatment: the server has no "clear the name"
// or "clear the placement" operation, only "set" or "leave alone."
type LocPatchRequest struct {
	Name      *string       `json:"name,omitempty"`
	Placement *LocPlacement `json:"placement,omitempty"`
	Capacity  **int64       `json:"-"` // marshaled specially, see loc.go's own LocPatch
}

// LocSubject identifies the entity a move or report acts on. Type is
// carried through to the server but not currently used server-side
// to compute the canonical subject reference (Entity + ID alone
// produce "entity:id"); included here because the wire shape requires
// it, not because this client asserts a meaning for it beyond what
// the server itself currently does.
type LocSubject struct {
	Type   string `json:"type,omitempty"`
	Entity string `json:"entity"`
	ID     int64  `json:"id"`
}

// LocPoint is a raw lat/lon/alt report, independent of any /loc leaf
// placement -- the same shape obj's own Report uses, duplicated
// rather than shared across packages for the same reason bal/cal/dxp
// each keep their own wire types instead of a shared package: each
// primitive's own request shape is free to diverge from the others'
// without a shared type forcing them to move together.
type LocPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
	Alt float64 `json:"alt,omitempty"`
}

// LocFenceDelta is the entered/exited fence id sets a move or report
// produces, when fence membership actually changed.
type LocFenceDelta struct {
	Entered []string `json:"entered"`
	Exited  []string `json:"exited"`
}

// LocMoveResult is POST /loc/move's own response shape.
type LocMoveResult struct {
	Moved  bool          `json:"moved"`
	Leaf   string        `json:"leaf"`
	Fences LocFenceDelta `json:"fences"`
}

// LocReportResult is POST /loc/report's own response shape. Changed
// is true iff this report crossed at least one fence boundary;
// Fences is always the subject's own full current fence membership,
// not just what changed.
type LocReportResult struct {
	Changed bool     `json:"changed"`
	Fences  []string `json:"fences"`
}

// types_loc.go, continued (XOT214, wave 16 Stage 2): fences, subject
// position/history, contains/nearby, patterns -- verified directly
// against pkg/server/v2_loc_handlers.go and v2_loc_pattern_handlers.go
// before being written here, not inferred from names.

// LocFenceCenter is a circle fence's own center point. Self, when
// true, means "anchored to the subject itself" -- confirmed NOT
// supported in this server release (obj-dependent, wave 10 not
// built), refused with a real error before this client ever sends
// the request.
type LocFenceCenter struct {
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
	Self bool    `json:"self,omitempty"`
}

// LocFenceGeometry is exactly one of two shapes, selected by Type --
// the two literal strings the server accepts, preserved verbatim
// rather than normalized: "Polygon" (Coordinates set -- a single
// closed exterior ring, [longitude, latitude] pairs per position,
// standard GeoJSON order and NOT [lat, lon] -- verified directly
// against loc.DecodeGeoJSONPolygon before writing this, not assumed;
// at least 3 distinct vertices plus a closing repeat of the first
// position, interior rings/holes not supported in this server
// release) or "circle" (Center + RadiusM set).
type LocFenceGeometry struct {
	Type        string          `json:"type"`
	Coordinates [][][2]float64  `json:"coordinates,omitempty"`
	Center      *LocFenceCenter `json:"center,omitempty"`
	RadiusM     float64         `json:"radius_m,omitempty"`
}

// LocFenceAttachRequest defines a fence. Exactly one of Subject or
// AlignedTo identifies it -- Subject for an obj-anchored fence
// ("kind:key" shorthand, proven equivalent to the server's own
// structured REF form; this client only ever sends the shorthand),
// AlignedTo for a tree-aligned fence (identity is the location itself
// -- a plain location id, not a subject). Capacity and Pattern are
// mutually exclusive (XOLU-LOC022) -- set at most one, never both.
type LocFenceAttachRequest struct {
	Subject   string           `json:"subject,omitempty"`
	AlignedTo string           `json:"aligned_to,omitempty"`
	Geometry  LocFenceGeometry `json:"geometry"`
	Capacity  *int64           `json:"capacity,omitempty"`
	Pattern   *string          `json:"pattern,omitempty"`
}

// LocFence is the shape every fence read/write that returns a single
// fence uses -- attach, get, and patch.
type LocFence struct {
	FenceID        string           `json:"fence_id"`
	AlignedTo      string           `json:"aligned_to,omitempty"`
	Geometry       LocFenceGeometry `json:"geometry"`
	Capacity       *int64           `json:"capacity,omitempty"`
	PatternID      *string          `json:"pattern_id,omitempty"`
	PatternDeleted *bool            `json:"pattern_deleted,omitempty"`
	Warnings       []string         `json:"warnings,omitempty"`
}

// LocFenceRef identifies which fence a lookup (Get/Delete/Patch/
// Reconcile) targets -- Kind and Key exactly mirror the server's own
// GET /loc/fences/{kind}/{key} path, two explicit segments, never a
// single string to parse apart. Construct via LocFenceRefByLocation
// or LocFenceRefBySubject rather than by hand.
//
// This replaces an earlier design (a single fenceID string, split on
// ":") that collapsed the server's own already-unambiguous two-
// segment URL into one ambiguous client parameter and then tried to
// guess the split back out -- a real bug, confirmed directly by
// TestAdversarial_FenceLookupPathColonCollision: a tree-aligned
// location legitimately named "root:special" defeated the guess.
// Mirrors dxp.Claim's own Primitive+Resource shape (an explicit tag
// field alongside a primitive-scoped string, not one overloaded
// string) -- the same pattern already established elsewhere in this
// codebase for exactly this problem.
type LocFenceRef struct {
	Kind string
	Key  string
}

// LocFenceRefByLocation identifies a tree-aligned fence by its own
// location's plain id -- safe for any id, including one containing a
// colon, since Kind ("id") and Key (the id verbatim) are always two
// separate fields here, never inferred from the id's own shape.
func LocFenceRefByLocation(locationID string) LocFenceRef {
	return LocFenceRef{Kind: "id", Key: locationID}
}

// LocFenceRefBySubject identifies an obj-anchored fence by the same
// kind/key split LocFenceAttachRequest's own Subject shorthand uses
// (e.g. kind="vehicles", key="47" for "vehicles:47"), passed here as
// two already-split fields rather than a string to re-split.
func LocFenceRefBySubject(kind, key string) LocFenceRef {
	return LocFenceRef{Kind: kind, Key: key}
}

// LocFenceListResult is GET /loc/fences/list's own response.
type LocFenceListResult struct {
	Fences []string `json:"fences"`
}

// LocFenceDrift is one subject's own recorded-vs-observed disagreement
// in a reconcile response. Recorded and Observed are short status
// strings the server defines (e.g. "member", "outside_new_boundary"),
// not booleans -- verified directly against pkg/loc's own FenceDrift
// before being typed here.
type LocFenceDrift struct {
	SubjectRef string `json:"subject_ref"`
	Recorded   string `json:"recorded"`
	Observed   string `json:"observed"`
}

// LocFenceReconcileResult is GET /loc/fences/{id}/reconcile's own
// response -- advisory only, this call never writes anything.
type LocFenceReconcileResult struct {
	FenceID       string          `json:"fence_id"`
	RecordedCount int             `json:"recorded_count"`
	ObservedCount int             `json:"observed_count"`
	Drift         []LocFenceDrift `json:"drift"`
}

// LocSubjectPosition is GET /loc/subjects/{entity}/{id}/position's own
// response. Leaf is nil for a subject only ever report-tracked;
// LastReportPoint is nil for one only ever moved -- both nil means
// this subject has never been referenced by either verb at all.
type LocSubjectPosition struct {
	Leaf            *string   `json:"leaf,omitempty"`
	Fences          []string  `json:"fences"`
	LastReportPoint *LocPoint `json:"last_report_point,omitempty"`
	AsOf            *string   `json:"as_of,omitempty"`
}

// LocHistoryEntry is one row in a subject's own movement journal.
// From/To are set only for a move entry; Entered/Exited are set only
// for a report entry (or a tree-aligned move, which also carries
// fence deltas).
type LocHistoryEntry struct {
	At      string   `json:"at"`
	Kind    string   `json:"kind"`
	From    *string  `json:"from,omitempty"`
	To      *string  `json:"to,omitempty"`
	Entered []string `json:"entered,omitempty"`
	Exited  []string `json:"exited,omitempty"`
}

// LocSubjectHistoryResult is GET /loc/subjects/{entity}/{id}/history's
// own response -- newest first.
type LocSubjectHistoryResult struct {
	Entries    []LocHistoryEntry `json:"entries"`
	NextCursor *string           `json:"next_cursor,omitempty"`
}

// LocContainsResult is GET /loc/contains's own response -- every
// fence a given lat/lon point currently falls inside.
type LocContainsResult struct {
	Fences []string `json:"fences"`
}

// LocNearbyLocation and LocNearbyFence are GET /loc/nearby's own
// per-result entries.
type LocNearbyLocation struct {
	LocationID string  `json:"location_id"`
	DistanceM  float64 `json:"distance_m"`
}
type LocNearbyFence struct {
	FenceID   string  `json:"fence_id"`
	DistanceM float64 `json:"distance_m"`
}

// LocNearbyResult is GET /loc/nearby's own response.
type LocNearbyResult struct {
	Locations []LocNearbyLocation `json:"locations"`
	Fences    []LocNearbyFence    `json:"fences"`
}

// LocPattern is a fence-type pattern (T-131) -- not a fence or a
// location itself, addressed by a plain (tenant, id), the shape every
// pattern read/write uses.
type LocPattern struct {
	Name     string `json:"name"`
	Capacity int64  `json:"capacity"`
}

// LocPatternListResult is GET /loc/patterns/list's own response.
type LocPatternListResult struct {
	Patterns []LocPattern `json:"patterns"`
}
