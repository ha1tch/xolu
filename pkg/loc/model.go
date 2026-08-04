// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import "time"

// Placement is a location node's transform relative to its parent's
// frame — never a disconnected coordinate of its own (loc-00-design.md
// §3b, adopting IFC's IfcLocalPlacement model outright). A node's
// absolute position is composing its placement chain up to the nearest
// ancestor carrying a non-nil Anchor.
type Placement struct {
	OffsetX, OffsetY, OffsetZ float64 // relative to parent's frame; metres
	Rotation                  float64 // radians, about Z
	Anchor                    *GeoAnchor
}

// GeoAnchor georeferences a node — where a subtree meets the real
// world, exactly IFC's IfcSite. Most nodes carry no anchor; their
// placement is relative only.
type GeoAnchor struct {
	Lat, Lon, Alt float64 // WGS84
	TrueNorth     float64 // radians, orientation of this node's local axes
}

// Geometry is the shape a fence tests containment against. Circle and
// Polygon are the two implementations (loc-00-design.md §4a); their
// Contains/Distance/BoundingBox methods land in geometry.go (Stage 3,
// T-116) — declared here only as types, not yet satisfying this
// interface, so Stage 0 does not carry Stage 3's own risk.
type Geometry interface {
	Contains(lat, lon float64) bool
	Distance(lat, lon float64) float64 // to boundary, metres
	BoundingBox() (minLat, minLon, maxLat, maxLon float64)
}

// Circle is a centre point plus a radius. GeoJSON has no native circle
// type (loc-00-design.md §4d); loc represents one as its own typed
// field, never forced into a GeoJSON polygon approximation on the wire.
type Circle struct {
	CenterLat, CenterLon float64
	RadiusMeters         float64
}

// Point is one vertex of a Polygon, or a raw reported coordinate —
// always a bare typed float64 pair, never decoded through an untyped
// JSON object or string intermediate (Stage 0's pinned JSON-decode
// discipline, doc.go).
type Point struct {
	Lat, Lon float64
}

// Polygon is an ordered, simple (non-self-intersecting) vertex list —
// square, rectangle, triangle, and irregular perimeter are all
// polygons with different vertex counts, not separate types
// (loc-00-design.md §4a). Self-intersecting input is rejected at write
// time (XOLU-LOC020); enforcement lands in geometry.go, Stage 3.
type Polygon struct {
	Vertices []Point
}

// LocationKey is the internal, dense, engine-only identifier for a
// location node — never on any wire struct (the two-identity split,
// loc-00-design.md §11a, mirroring bal's AccountKey/account_id
// pattern exactly). The external identifier is LocationID, a
// namespaced string.
type LocationKey uint32

// Location is one node in the containment tree (loc-00-design.md §3a,
// adopted from bal §3a outright) plus its Placement (§3b). Root nodes
// (ParentKey == nil) must carry a non-nil Placement.Anchor — enforced
// at write time (XOLU-LOC010) in store.go, not left to a SQL CHECK
// constraint, since the rule spans a whole struct, not one column.
type Location struct {
	Key      LocationKey
	ID       string // external, namespaced, stable
	ParentKey *LocationKey
	Name     string
	Postable bool // leaf: subjects can be placed here. false: interior summary node, occupancy is a derived rollup of the subtree only.
	Placement
	CreatedAt time.Time
}

// LocationDef is the input to Store.Def — everything a caller supplies
// to create a location node. Key is assigned by the store (dense
// MAX+1 allocation, mirroring bal.DefineAccount), never caller-chosen.
type LocationDef struct {
	ID        string
	ParentID  *string // external id of the parent, nil for a root
	Name      string
	Postable  bool
	Placement Placement
}

// FenceKey is the internal, dense, engine-only identifier for a fence
// — the same two-identity split LocationKey gets. Stage 2 (T-115)
// introduces it as a bare identity plus a capacity row; Stage 3
// (T-116) adds the real Geometry a fence tests membership against.
type FenceKey uint32

