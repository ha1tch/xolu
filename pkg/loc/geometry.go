// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// earthRadiusMeters is the mean Earth radius used by the haversine
// formula (loc-00-design.md §4e: sufficient accuracy for facility/
// service-radius scale, no ellipsoidal correction needed).
const earthRadiusMeters = 6371000.0

// haversineMeters is the great-circle distance between two WGS84
// points, in metres.
func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	phi1 := lat1 * math.Pi / 180
	phi2 := lat2 * math.Pi / 180
	dPhi := (lat2 - lat1) * math.Pi / 180
	dLambda := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dPhi/2)*math.Sin(dPhi/2) +
		math.Cos(phi1)*math.Cos(phi2)*math.Sin(dLambda/2)*math.Sin(dLambda/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusMeters * c
}

// ─── Circle ──────────────────────────────────────────────────────────

func (c Circle) Contains(lat, lon float64) bool {
	return haversineMeters(c.CenterLat, c.CenterLon, lat, lon) <= c.RadiusMeters
}

// Distance to the circle's boundary — 0 exactly on the boundary,
// positive whether the point is inside or outside (the interface
// contract is distance-to-boundary, not signed inside/outside
// distance).
func (c Circle) Distance(lat, lon float64) float64 {
	return math.Abs(haversineMeters(c.CenterLat, c.CenterLon, lat, lon) - c.RadiusMeters)
}

// BoundingBox approximates the circle's box via the same flat-Earth
// degrees-per-metre conversion placement.go already uses (loc-00-
// design.md §4e's accepted precision at this scale) — a cheap,
// slightly-conservative box; exact edge cases are what the R-tree
// pre-filter narrows, never decides (§7b, guard-locality).
func (c Circle) BoundingBox() (minLat, minLon, maxLat, maxLon float64) {
	dLat := c.RadiusMeters / metresPerDegreeLat
	metresPerDegreeLon := metresPerDegreeLat * math.Cos(c.CenterLat*math.Pi/180.0)
	// A bounding box wants a symmetric magnitude, never a signed
	// direction — safeLongitudeDelta's sign-preservation is correct
	// for a directional placement offset (ComposeAbsolutePosition's
	// own use), not for this, so its result is taken by magnitude
	// here regardless of any sign quirk an out-of-range CenterLat
	// (e.g. >90) could otherwise introduce via cos() going negative.
	dLon := math.Abs(safeLongitudeDelta(c.RadiusMeters, metresPerDegreeLon))
	return c.CenterLat - dLat, c.CenterLon - dLon, c.CenterLat + dLat, c.CenterLon + dLon
}

// ─── Polygon ─────────────────────────────────────────────────────────

// Contains answers point-in-polygon via ray-casting (the even-odd
// rule, PNPOLY's own reference shape) — correct on concave perimeters
// without decomposition (loc-00-design.md §4b), never triangulation.
// Falls through to isAxisAlignedRectangle's O(1) check first (§4c):
// probably the commonest real shape (yards, parking lots, warehouse
// zones), and a bounding-box test is exact for a true rectangle, not
// an approximation.
func (p Polygon) Contains(lat, lon float64) bool {
	if minLat, minLon, maxLat, maxLon, ok := axisAlignedRectangleBounds(p); ok {
		return lat >= minLat && lat <= maxLat && lon >= minLon && lon <= maxLon
	}
	n := len(p.Vertices)
	if n < 3 {
		return false
	}
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		vi, vj := p.Vertices[i], p.Vertices[j]
		if (vi.Lat > lat) != (vj.Lat > lat) {
			atLon := (vj.Lon-vi.Lon)*(lat-vi.Lat)/(vj.Lat-vi.Lat) + vi.Lon
			if lon < atLon {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}

// Distance to the polygon's boundary: the minimum point-to-segment
// distance over every edge, converted to metres via the same
// flat-Earth approximation used throughout this package at this
// scale. Correct whether the point is inside or outside — the
// interface contract is distance-to-boundary.
func (p Polygon) Distance(lat, lon float64) float64 {
	n := len(p.Vertices)
	if n == 0 {
		return math.Inf(1)
	}
	if n == 1 {
		return haversineMeters(p.Vertices[0].Lat, p.Vertices[0].Lon, lat, lon)
	}
	min := math.Inf(1)
	for i := 0; i < n; i++ {
		a := p.Vertices[i]
		b := p.Vertices[(i+1)%n]
		d := pointToSegmentMeters(lat, lon, a, b)
		if d < min {
			min = d
		}
	}
	return min
}

func (p Polygon) BoundingBox() (minLat, minLon, maxLat, maxLon float64) {
	if len(p.Vertices) == 0 {
		return 0, 0, 0, 0
	}
	minLat, minLon = p.Vertices[0].Lat, p.Vertices[0].Lon
	maxLat, maxLon = minLat, minLon
	for _, v := range p.Vertices[1:] {
		if v.Lat < minLat {
			minLat = v.Lat
		}
		if v.Lat > maxLat {
			maxLat = v.Lat
		}
		if v.Lon < minLon {
			minLon = v.Lon
		}
		if v.Lon > maxLon {
			maxLon = v.Lon
		}
	}
	return
}

// axisAlignedRectangleBounds detects a 4-vertex (plus optional closing
// 5th equal to the first) polygon whose edges alternate purely
// horizontal/vertical, and returns its box directly — §4c's fast
// path. ok is false for anything else, including a rotated square
// (correctly: a rotated square is not axis-aligned, and forcing it
// through the box test would silently admit points a true rectangle
// would refuse).
func axisAlignedRectangleBounds(p Polygon) (minLat, minLon, maxLat, maxLon float64, ok bool) {
	v := p.Vertices
	if len(v) == 5 && v[0] == v[4] {
		v = v[:4]
	}
	if len(v) != 4 {
		return 0, 0, 0, 0, false
	}
	for i := 0; i < 4; i++ {
		a, b := v[i], v[(i+1)%4]
		sameLat := a.Lat == b.Lat
		sameLon := a.Lon == b.Lon
		if sameLat == sameLon { // neither purely horizontal nor purely vertical (or degenerate: both)
			return 0, 0, 0, 0, false
		}
	}
	minLat, minLon = v[0].Lat, v[0].Lon
	maxLat, maxLon = minLat, minLon
	for _, pt := range v[1:] {
		if pt.Lat < minLat {
			minLat = pt.Lat
		}
		if pt.Lat > maxLat {
			maxLat = pt.Lat
		}
		if pt.Lon < minLon {
			minLon = pt.Lon
		}
		if pt.Lon > maxLon {
			maxLon = pt.Lon
		}
	}
	return minLat, minLon, maxLat, maxLon, true
}

// pointToSegmentMeters is the minimum distance from (lat,lon) to the
// segment [a,b], computed in a local flat-frame approximation (metres)
// rather than geodesically — consistent with this package's accepted
// precision at facility/city scale (§4e). a is treated as the local
// origin; b and the query point are converted to (x,y) metres relative
// to it.
func pointToSegmentMeters(lat, lon float64, a, b Point) float64 {
	metresPerDegreeLon := metresPerDegreeLat * math.Cos(a.Lat*math.Pi/180.0)
	toXY := func(pt Point) (x, y float64) {
		return (pt.Lon - a.Lon) * metresPerDegreeLon, (pt.Lat - a.Lat) * metresPerDegreeLat
	}
	px, py := toXY(Point{Lat: lat, Lon: lon})
	bx, by := toXY(b)
	// a's own (x,y) is (0,0) by construction — the segment is [(0,0), (bx,by)].

	abLenSq := bx*bx + by*by
	var t float64
	if abLenSq > 0 {
		t = (px*bx + py*by) / abLenSq
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
	}
	cx, cy := bx*t, by*t
	dx, dy := px-cx, py-cy
	return math.Sqrt(dx*dx + dy*dy)
}

// SelfIntersects reports whether any two non-adjacent edges of the
// polygon cross — the write-time check behind XOLU-LOC020
// (loc-00-design.md §4b: the same simple-polygon restriction SQLite's
// own Geopoly extension imposes, not a loc-specific inconvenience).
func (p Polygon) SelfIntersects() bool {
	n := len(p.Vertices)
	if n < 4 {
		return false // a triangle (or fewer) cannot self-intersect
	}
	edge := func(i int) (Point, Point) { return p.Vertices[i], p.Vertices[(i+1)%n] }
	for i := 0; i < n; i++ {
		a1, a2 := edge(i)
		for j := i + 1; j < n; j++ {
			// Skip edges adjacent to edge i (sharing a vertex) in
			// EITHER direction — j immediately follows i (shares the
			// vertex between them) or j immediately precedes i via
			// wrap-around (shares the closing vertex). Sharing a
			// vertex is not a crossing; only the wrap-around direction
			// was checked before this fix, which falsely flagged every
			// ordinary simple polygon as self-intersecting the moment
			// it had two consecutive middle edges (caught by
			// TestPolygon_SelfIntersectionRejected's own rectangle
			// case, not assumed correct from the algorithm's shape).
			if j == (i+1)%n || (j+1)%n == i {
				continue
			}
			b1, b2 := edge(j)
			if segmentsIntersect(a1, a2, b1, b2) {
				return true
			}
		}
	}
	return false
}

// degenerateAreaEpsilon: below this magnitude (raw shoelace units —
// degrees², not metres² — see shoelaceArea's own doc), a polygon
// reads as effectively zero-area. Chosen conservatively against real
// facility scale, not derived from a physical bound: a 100m×100m
// fence is already ~8e-7 degree² at mid-latitudes (1° latitude ≈
// 111km), several orders of magnitude above this threshold, so no
// legitimate small fence should ever trip it — only genuine collapse
// (near-collinear or coincident vertices, the shape floating-point
// rounding produces after e.g. a client-side simplification pass gone
// wrong).
const degenerateAreaEpsilon = 1e-10

// shoelaceArea is the standard signed polygon-area formula, evaluated
// directly in raw lat/lon degrees — a planar approximation, not a
// geodesic one (no spherical-excess correction). Deliberately not
// physically accurate: this exists solely to detect "effectively
// zero," per IsDegenerate below, never to report a real area to a
// caller — the magnitude comparison against degenerateAreaEpsilon is
// scale-invariant to this choice in the cases that matter (a
// genuinely collapsed polygon reads near-zero under any reasonable
// projection).
func (p Polygon) shoelaceArea() float64 {
	n := len(p.Vertices)
	if n < 3 {
		return 0
	}
	sum := 0.0
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		sum += p.Vertices[i].Lon*p.Vertices[j].Lat - p.Vertices[j].Lon*p.Vertices[i].Lat
	}
	return sum / 2
}

// EffectiveVertexCount counts distinct consecutive vertices, treating
// RFC 7946's closing repeat of the first vertex (loc-00-design.md
// §4b's own decode discipline always produces one) as not a distinct
// point, and collapsing any other adjacent duplicate too — a caller
// submitting the same point twice in a row shouldn't inflate the
// count. A simple polygon needs at least 3 to mean anything.
func (p Polygon) EffectiveVertexCount() int {
	n := len(p.Vertices)
	if n == 0 {
		return 0
	}
	count := 0
	for i := 0; i < n; i++ {
		v := p.Vertices[i]
		if i == n-1 && n > 1 && v == p.Vertices[0] {
			continue // RFC 7946 closure repeat
		}
		if i > 0 && v == p.Vertices[i-1] {
			continue // adjacent duplicate
		}
		count++
	}
	return count
}

// IsDegenerate reports whether this polygon has effectively zero area
// or fewer than three effective vertices — a legal but useless fence
// (loc-01-rest-api.md §2's own warnings field, T-132: never a hard
// refusal, since a degenerate fence someone can never enter is
// legitimate, just probably not what the caller intended).
func (p Polygon) IsDegenerate() bool {
	if p.EffectiveVertexCount() < 3 {
		return true
	}
	return math.Abs(p.shoelaceArea()) < degenerateAreaEpsilon
}

// segmentsIntersect is the standard orientation-based test (Cormen et
// al.'s CLRS shape): two segments cross iff their endpoints have
// opposite orientations pairwise, with collinear-overlap handled as a
// crossing too (a degenerate but still illegal self-touching polygon).
func segmentsIntersect(p1, p2, p3, p4 Point) bool {
	o1 := orientation(p1, p2, p3)
	o2 := orientation(p1, p2, p4)
	o3 := orientation(p3, p4, p1)
	o4 := orientation(p3, p4, p2)

	if o1 != o2 && o3 != o4 {
		return true
	}
	if o1 == 0 && onSegment(p1, p3, p2) {
		return true
	}
	if o2 == 0 && onSegment(p1, p4, p2) {
		return true
	}
	if o3 == 0 && onSegment(p3, p1, p4) {
		return true
	}
	if o4 == 0 && onSegment(p3, p2, p4) {
		return true
	}
	return false
}

// orientation returns 0 (collinear), 1 (clockwise), or 2 (counter-
// clockwise) for the ordered triple (p, q, r).
func orientation(p, q, r Point) int {
	val := (q.Lon-p.Lon)*(r.Lat-q.Lat) - (q.Lat-p.Lat)*(r.Lon-q.Lon)
	if val == 0 {
		return 0
	}
	if val > 0 {
		return 1
	}
	return 2
}

// onSegment reports whether q lies on segment pr, given p, q, r are
// already known collinear.
func onSegment(p, q, r Point) bool {
	return q.Lat <= math.Max(p.Lat, r.Lat) && q.Lat >= math.Min(p.Lat, r.Lat) &&
		q.Lon <= math.Max(p.Lon, r.Lon) && q.Lon >= math.Min(p.Lon, r.Lon)
}

// DecodeGeoJSONPolygon parses a GeoJSON (RFC 7946 §3.1.6) Polygon's
// exterior ring into this package's own Polygon type. GeoJSON
// coordinate pairs are [longitude, latitude] — the OPPOSITE order
// from this package's own Point{Lat, Lon} — a deliberate point of
// friction named here so it is checked once, in one place, rather
// than risked at every call site. Holes (interior rings,
// coordinates[1:], RFC 7946 §3.1.6: "any others MUST be interior
// rings") are refused outright, not silently dropped — an earlier
// version took only coordinates[0] and never even looked at any
// further rings, so a caller submitting a fully RFC-compliant
// Appendix-A.3-style "with holes" polygon got a fence that silently
// omitted the hole rather than an error explaining why: the hole
// area would incorrectly read as "inside" the fence. loc-00-
// design.md's own decision that holes are unsupported (a different,
// unneeded shape for this package's scope) was always the design;
// this fix makes the implementation actually enforce it. A GeoJSON
// ring's closing vertex (first == last, RFC 7946 §3.1.6's own
// closure requirement) is dropped on decode: this package's own
// Polygon is an open vertex list, the edge back to Vertices[0] is
// implicit (geometry.go's own Contains/SelfIntersects both close the
// loop via modulo indexing).
func DecodeGeoJSONPolygon(coordinates [][][2]float64) (Polygon, error) {
	if len(coordinates) == 0 {
		return Polygon{}, &ValidationError{Detail: "GeoJSON polygon has no rings"}
	}
	if len(coordinates) > 1 {
		return Polygon{}, &ValidationError{Detail: "GeoJSON polygon interior rings (holes) are not supported in v1 -- only a single exterior ring"}
	}
	ring := coordinates[0]
	if len(ring) < 4 { // a closed simple polygon needs >=3 distinct vertices + the closing repeat
		return Polygon{}, &ValidationError{Detail: "GeoJSON polygon ring has fewer than 4 positions (3 distinct vertices + closing repeat)"}
	}
	first, last := ring[0], ring[len(ring)-1]
	if first != last {
		return Polygon{}, &ValidationError{Detail: "GeoJSON polygon ring is not closed (first position != last)"}
	}
	verts := make([]Point, 0, len(ring)-1)
	for _, pos := range ring[:len(ring)-1] {
		lon, lat := pos[0], pos[1]
		verts = append(verts, Point{Lat: lat, Lon: lon})
	}
	return Polygon{Vertices: verts}, nil
}

// SetFenceGeometry validates and stores a fence's real shape, replacing
// Stage 2's test hook — this is what "report end-to-end resolves real
// fence membership through real geometry" (Stage 3's own exit
// criterion) actually wires up. Self-intersection is rejected
// (XOLU-LOC020) before anything is written. The bounding box is
// computed once here and stored both in fences (source of truth) and
// fences_rtree (the pre-filter index) — kept in sync in the same
// transaction, never allowed to drift apart.
func (s *Store) SetFenceGeometry(ctx context.Context, fenceID string, geom Geometry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var key int64
	if err := tx.QueryRowContext(ctx, `SELECT fence_key FROM fences WHERE fence_id = ?`, fenceID).Scan(&key); err != nil {
		if err == sql.ErrNoRows {
			return &UnknownFenceError{FenceID: fenceID}
		}
		return err
	}

	var kind string
	var centerLat, centerLon, radius interface{}
	var verticesJSON interface{}
	switch g := geom.(type) {
	case Circle:
		if g.RadiusMeters < 0 {
			// Contains uses distance <= radius; a haversine distance is
			// never negative, so a negative radius doesn't error on its
			// own — it silently creates a fence that can never be
			// entered by anyone, ever. Worse than refusing outright: a
			// caller's sign mistake would look like a defined fence
			// that mysteriously never matches, not an error explaining
			// why. Found by direct adversarial testing, not inspection.
			return &ValidationError{Detail: fmt.Sprintf("circle radius_meters must be >= 0, got %v", g.RadiusMeters)}
		}
		kind = "circle"
		centerLat, centerLon, radius = g.CenterLat, g.CenterLon, g.RadiusMeters
	case Polygon:
		if g.SelfIntersects() {
			return &SelfIntersectingPolygonError{FenceID: fenceID}
		}
		kind = "polygon"
		vj, err := json.Marshal(g.Vertices)
		if err != nil {
			return err
		}
		verticesJSON = string(vj)
	default:
		return &ValidationError{Detail: fmt.Sprintf("unsupported geometry type %T", geom)}
	}

	minLat, minLon, maxLat, maxLon := geom.BoundingBox()

	if _, err := tx.ExecContext(ctx,
		`UPDATE fences SET kind=?, center_lat=?, center_lon=?, radius_meters=?, vertices_json=?,
		 min_lat=?, min_lon=?, max_lat=?, max_lon=? WHERE fence_key=?`,
		kind, centerLat, centerLon, radius, verticesJSON, minLat, minLon, maxLat, maxLon, key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM fences_rtree WHERE id = ?`, key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO fences_rtree (id, minLat, maxLat, minLon, maxLon) VALUES (?,?,?,?,?)`,
		key, minLat, maxLat, minLon, maxLon); err != nil {
		return err
	}
	return tx.Commit()
}

// loadFenceGeometry reconstructs a fence's Geometry from storage. Nil,
// nil if the fence has no geometry set yet (Stage 2's own default —
// identity and capacity exist, shape doesn't).
func loadFenceGeometry(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}, fenceKey int64) (Geometry, error) {
	var kind sql.NullString
	var centerLat, centerLon, radius sql.NullFloat64
	var verticesJSON sql.NullString
	if err := q.QueryRowContext(ctx,
		`SELECT kind, center_lat, center_lon, radius_meters, vertices_json FROM fences WHERE fence_key = ?`,
		fenceKey).Scan(&kind, &centerLat, &centerLon, &radius, &verticesJSON); err != nil {
		return nil, err
	}
	if !kind.Valid {
		return nil, nil
	}
	switch kind.String {
	case "circle":
		return Circle{CenterLat: centerLat.Float64, CenterLon: centerLon.Float64, RadiusMeters: radius.Float64}, nil
	case "polygon":
		var verts []Point
		if err := json.Unmarshal([]byte(verticesJSON.String), &verts); err != nil {
			return nil, err
		}
		return Polygon{Vertices: verts}, nil
	default:
		return nil, fmt.Errorf("loc invariant violation: fence_key %d has unknown geometry kind %q", fenceKey, kind.String)
	}
}

// FenceDrift names one member whose last-known point no longer falls
// inside its fence's *current* geometry — the only direction this
// reconcile can detect, matching loc-01-rest-api.md §2b's own scope:
// it re-tests recorded members, it never scans for new ones a
// boundary expansion might now include (that would need a global
// subject scan, out of scope here).
type FenceDrift struct {
	SubjectRef string
	Recorded   string // always "member" -- only current members are checked
	Observed   string // "outside_new_boundary" or "no_report_point" (defensive: shouldn't happen, membership is only ever set via Report)
}

// FenceReconcileResult is §2b's advisory drift view.
type FenceReconcileResult struct {
	RecordedCount int
	ObservedCount int
	Drift         []FenceDrift
}

// ReconcileFence re-tests every subject currently recorded in
// loc_fence_membership for fenceID against the fence's *current*
// geometry — §5c of loc-00-design.md, chronicle.RebuildOracle-shaped
// in spirit (derive fresh, compare to current, surface disagreement)
// though not a literal instantiation of that type, since the useful
// output here is a structured per-subject drift list, not a single
// canonical-string fingerprint. Read-only: never writes
// loc_fence_capacity.count or loc_fence_membership, both guard-bearing
// — an advisory view exists precisely so a derived process never
// touches guard-bearing state outside the ordinary CAS path (§5c's own
// rule, T-130's filed exit criteria). Reuses the bounding-box-free
// exact Contains test directly, not the rtree pre-filter — the
// candidate set here is already exactly known (loc_fence_membership's
// own rows for this fence), so there's nothing to pre-filter.
func (s *Store) ReconcileFence(ctx context.Context, fenceID string) (FenceReconcileResult, error) {
	var res FenceReconcileResult

	var fenceKey int64
	if err := s.db.QueryRowContext(ctx, `SELECT fence_key FROM fences WHERE fence_id = ?`, fenceID).Scan(&fenceKey); err != nil {
		if err == sql.ErrNoRows {
			return res, &UnknownFenceError{FenceID: fenceID}
		}
		return res, err
	}

	geom, err := loadFenceGeometry(ctx, s.db, fenceKey)
	if err != nil {
		return res, err
	}
	if geom == nil {
		// A fence can exist with no geometry set yet (Stage 2's own
		// default) -- nothing to reconcile against, correctly zero
		// members either way since Contains was never satisfiable.
		return res, nil
	}

	rows, err := s.db.QueryContext(ctx, `SELECT subject_ref FROM loc_fence_membership WHERE fence_key = ?`, fenceKey)
	if err != nil {
		return res, err
	}
	var members []string
	for rows.Next() {
		var subj string
		if err := rows.Scan(&subj); err != nil {
			_ = rows.Close()
			return res, err
		}
		members = append(members, subj)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return res, err
	}
	_ = rows.Close()

	res.RecordedCount = len(members)
	for _, subj := range members {
		var lat, lon sql.NullFloat64
		err := s.db.QueryRowContext(ctx, `
			SELECT report_lat, report_lon FROM loc_journal
			WHERE subject_ref = ? AND kind = 'report' AND report_lat IS NOT NULL
			ORDER BY entry_id DESC LIMIT 1`, subj).Scan(&lat, &lon)
		if err != nil && err != sql.ErrNoRows {
			return res, err
		}
		if err == sql.ErrNoRows || !lat.Valid {
			// Defensive only -- membership is only ever set inside
			// Report's own delta computation, which always has a point.
			// Surfaced as drift rather than silently skipped, since a
			// member with no recoverable point is itself a real anomaly
			// worth an operator's attention.
			res.Drift = append(res.Drift, FenceDrift{SubjectRef: subj, Recorded: "member", Observed: "no_report_point"})
			continue
		}
		if geom.Contains(lat.Float64, lon.Float64) {
			res.ObservedCount++
		} else {
			res.Drift = append(res.Drift, FenceDrift{SubjectRef: subj, Recorded: "member", Observed: "outside_new_boundary"})
		}
	}
	return res, nil
}

// a raw coordinate — the two-stage design §6b describes, already built
// into the pinned SQLite dependency: fences_rtree narrows candidates
// by bounding-box overlap (cheap), then each candidate's real Contains
// test decides membership exactly (never the pre-filter's cached box
// alone, §7b's guard-locality rule — a box overlap is necessary but
// not sufficient for true containment).
func (s *Store) ResolveFenceMembership(ctx context.Context, lat, lon float64) ([]FenceKey, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM fences_rtree WHERE minLat <= ? AND maxLat >= ? AND minLon <= ? AND maxLon >= ?`,
		lat, lat, lon, lon)
	if err != nil {
		return nil, err
	}
	var candidates []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		candidates = append(candidates, id)
	}
	_ = rows.Close()

	var member []FenceKey
	for _, fk := range candidates {
		geom, err := loadFenceGeometry(ctx, s.db, fk)
		if err != nil {
			return nil, err
		}
		if geom == nil {
			continue // fence exists but has no geometry set yet — never a member
		}
		if geom.Contains(lat, lon) {
			member = append(member, FenceKey(uint32(fk)))
		}
	}
	return member, nil
}

// Report resolves a raw coordinate's fence membership and nothing
// else — it never sets or changes tree-leaf assignment (the two-write-
// path distinction, doc.go / loc-01-rest-api.md §0). The delta against
// the subject's previously-known membership (loc_fence_membership,
// tracked directly rather than derived, the same shape loc_assignment
// gives leaf position) is applied through the identical CAS guards
// Move uses for fences — a report that would exceed a fence's capacity
// is refused exactly like a move would be, per §5a's capacity guard
// being resolved identically for report and move.
func (s *Store) Report(ctx context.Context, subjectRef string, lat, lon float64) error {
	if subjectRef == "" {
		return &ValidationError{Detail: "subject_ref is required"}
	}
	newMembership, err := s.ResolveFenceMembership(ctx, lat, lon)
	if err != nil {
		return err
	}

	// Compute the delta against current membership BEFORE opening a
	// transaction — §8a's rule (loc-00-design.md), enforced at the
	// write layer: a report producing no containment change writes
	// nothing at all, no journal row, no event, no ts record. This is
	// the same "decision inside the predicate" discipline Stage 2's
	// CAS guards apply to whether a write is ADMITTED; here it decides
	// whether a write happens at all. Caught against Stage 4's own
	// spec, not assumed from Stage 3's own working implementation —
	// the first version of this function wrote a journal row
	// unconditionally, which this stage's own testing requirement
	// would have caught as a real bug had it shipped that way.
	oldRows, err := s.db.QueryContext(ctx, `SELECT fence_key FROM loc_fence_membership WHERE subject_ref = ?`, subjectRef)
	if err != nil {
		return err
	}
	oldSet := map[FenceKey]bool{}
	for oldRows.Next() {
		var fk int64
		if err := oldRows.Scan(&fk); err != nil {
			_ = oldRows.Close()
			return err
		}
		oldSet[FenceKey(uint32(fk))] = true
	}
	_ = oldRows.Close()
	if err := oldRows.Err(); err != nil {
		return err
	}

	newSet := map[FenceKey]bool{}
	for _, fk := range newMembership {
		newSet[fk] = true
	}

	var entered, exited []FenceKey
	for fk := range newSet {
		if !oldSet[fk] {
			entered = append(entered, fk)
		}
	}
	for fk := range oldSet {
		if !newSet[fk] {
			exited = append(exited, fk)
		}
	}

	if len(entered) == 0 && len(exited) == 0 {
		return nil // no containment change: write nothing, per §8a
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, fk := range exited {
		res, err := tx.ExecContext(ctx, fenceExitCAS, uint32(fk))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return &InvariantError{Detail: fmt.Sprintf("report exit found no matching capacity row for fence_key %d", fk)}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM loc_fence_membership WHERE subject_ref=? AND fence_key=?`, subjectRef, uint32(fk)); err != nil {
			return err
		}
	}
	for _, fk := range entered {
		res, err := tx.ExecContext(ctx, fenceEntryCAS, uint32(fk))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return &CapacityError{Kind: "fence", Key: uint32(fk)}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO loc_fence_membership (subject_ref, fence_key) VALUES (?,?)`, subjectRef, uint32(fk)); err != nil {
			return err
		}
	}

	now := time.Now().UTC()
	enteredJSON, _ := json.Marshal(entered)
	exitedJSON, _ := json.Marshal(exited)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO loc_journal (subject_ref, kind, from_location_key, to_location_key, report_lat, report_lon, entered_fence_keys, exited_fence_keys, at)
		 VALUES (?, 'report', NULL, NULL, ?, ?, ?, ?, ?)`,
		subjectRef, lat, lon, string(enteredJSON), string(exitedJSON), now); err != nil {
		return err
	}

	return tx.Commit()
}
