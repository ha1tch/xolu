// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package loc

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"modernc.org/sqlite"

	"github.com/ha1tch/xolu/pkg/tenant"
)

// sqliteConstraintUnique is SQLite's own stable numeric result code
// for a UNIQUE constraint violation (SQLITE_CONSTRAINT_UNIQUE = 2067,
// per SQLite's own public C API — not specific to this Go binding).
// Defined locally rather than imported: modernc.org/sqlite's own
// top-level package doesn't re-export this constant, only its
// internal /lib subpackage does, which isn't meant as this project's
// public surface to depend on.
const sqliteConstraintUnique = 2067

// sqliteConstraintPrimaryKey is SQLITE_CONSTRAINT_PRIMARYKEY = 1555 —
// the code SQLite actually raises when the conflicting column is
// itself the table's PRIMARY KEY, distinct from sqliteConstraintUnique
// above (which fires for a separate UNIQUE column over a surrogate
// primary key — location_id/fence_id's own shape). loc_patterns is
// this package's first table where the caller-chosen external id IS
// the literal PRIMARY KEY (patterns have no surrogate dense key the
// way locations/fences do), so DefPattern needs to check both codes —
// found the hard way, T-131: DefPattern's first version only checked
// sqliteConstraintUnique and let a duplicate pattern_id fall through
// to a raw 500.
const sqliteConstraintPrimaryKey = 1555

// Store is loc's SQL plane. Canonical state is SQL throughout — no
// bit-packed codec, no Pebble plane (loc-00-design.md §6a) — mirroring
// bal's shape, not cal's or ts's. Unlike bal, which shares the
// tenant's primary store file and needs "t0000_"-style table-name
// prefixing to avoid collision with other primitives' tables in that
// same file, loc gets its own dedicated per-tenant SQLite file
// (storelayout.TenantLocDir, Stage 0's own pinned decision) — the db
// handle passed to NewStore is expected to already be scoped to that
// file, so table names here are bare, not prefixed.
type Store struct {
	db       *sql.DB
	tenantID tenant.TenantID
}

// NewStore wraps a db handle already opened against loc's own
// per-tenant SQLite file (storelayout.TenantLocDir). tenantID is kept
// for TenantID() — the dxp adapter (Stage 5, T-118) needs a
// cross-primitive-comparable tenant key, the same reason bal.Store
// keeps its own tenantID despite deriving its table prefix from it.
func NewStore(db *sql.DB, tenantID tenant.TenantID) *Store {
	return &Store{db: db, tenantID: tenantID}
}

// TenantID returns the tenant this Store is scoped to.
func (s *Store) TenantID() tenant.TenantID { return s.tenantID }

// DB exposes the underlying *sql.DB for callers that need raw access
// to loc's own tables — tests checking a real side effect directly,
// matching the hotel test's own "raw SQL against cal_bookings"
// discipline (v2_dxp_hotel_test.go), not a derived read path. Not
// meant for ordinary callers, who should use Store's own methods.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) locationsTable() string { return "locations" }

// Init creates loc's tables. Idempotent. Stage 1's locations table,
// plus Stage 2's capacity/assignment/journal tables — fences here is a
// bare identity (fence_key, fence_id) only; Stage 3 (T-116) adds the
// real geometry columns via its own Init addition, not a rewrite of
// this one.
func (s *Store) Init(ctx context.Context) error {
	ddl := `
	CREATE TABLE IF NOT EXISTS ` + s.locationsTable() + ` (
		location_key INTEGER PRIMARY KEY,          -- internal uint32, dense
		location_id  TEXT    NOT NULL UNIQUE,       -- external string (§11a)
		parent_key   INTEGER NULL REFERENCES ` + s.locationsTable() + `(location_key),
		name         TEXT    NOT NULL,
		postable     INTEGER NOT NULL DEFAULT 1,
		offset_x     REAL    NOT NULL DEFAULT 0,
		offset_y     REAL    NOT NULL DEFAULT 0,
		offset_z     REAL    NOT NULL DEFAULT 0,
		rotation     REAL    NOT NULL DEFAULT 0,
		anchor_lat   REAL    NULL,
		anchor_lon   REAL    NULL,
		anchor_alt   REAL    NULL,
		anchor_true_north REAL NULL,
		created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE INDEX IF NOT EXISTS idx_locations_parent
		ON ` + s.locationsTable() + `(parent_key);

	CREATE TABLE IF NOT EXISTS loc_patterns (
		pattern_id TEXT    PRIMARY KEY, -- caller-chosen, the same declare-at-known-id convention as location_id/fence_id
		capacity   INTEGER NOT NULL,
		created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS loc_capacity (
		location_key INTEGER PRIMARY KEY REFERENCES ` + s.locationsTable() + `(location_key),
		ceiling      INTEGER NULL,
		count        INTEGER NOT NULL DEFAULT 0,
		pattern_id   TEXT    NULL REFERENCES loc_patterns(pattern_id) -- T-131: lineage, NULL unless cloned from a pattern
	);

	CREATE TABLE IF NOT EXISTS fences (
		fence_key     INTEGER PRIMARY KEY,
		fence_id      TEXT    NOT NULL UNIQUE,
		aligned_location_key INTEGER NULL REFERENCES ` + s.locationsTable() + `(location_key), -- non-NULL for a tree-aligned fence (loc-01-rest-api.md §2); membership additionally follows the free tree walk, per loc-00-design.md §5
		kind          TEXT    NULL,  -- 'circle' | 'polygon', NULL until geometry is set (Stage 2 default)
		center_lat    REAL    NULL,  -- circle only
		center_lon    REAL    NULL,
		radius_meters REAL    NULL,
		vertices_json TEXT    NULL,  -- polygon only: JSON array of {"lat":.., "lon":..} objects
		min_lat       REAL    NULL,  -- bounding box, either kind — the R-tree pre-filter's own source of truth
		min_lon       REAL    NULL,
		max_lat       REAL    NULL,
		max_lon       REAL    NULL
	);
	CREATE INDEX IF NOT EXISTS idx_fences_aligned ON fences(aligned_location_key);
	CREATE VIRTUAL TABLE IF NOT EXISTS fences_rtree USING rtree(
		id, minLat, maxLat, minLon, maxLon
	);
	CREATE TABLE IF NOT EXISTS loc_fence_capacity (
		fence_key  INTEGER PRIMARY KEY REFERENCES fences(fence_key),
		ceiling    INTEGER NULL,
		count      INTEGER NOT NULL DEFAULT 0,
		pattern_id TEXT    NULL REFERENCES loc_patterns(pattern_id) -- T-131: lineage, NULL unless cloned from a pattern
	);
	CREATE TABLE IF NOT EXISTS loc_fence_membership (
		subject_ref TEXT    NOT NULL,
		fence_key   INTEGER NOT NULL REFERENCES fences(fence_key),
		PRIMARY KEY (subject_ref, fence_key)
	);

	CREATE TABLE IF NOT EXISTS loc_assignment (
		subject_ref  TEXT PRIMARY KEY,
		location_key INTEGER NULL REFERENCES ` + s.locationsTable() + `(location_key),
		updated_at   TIMESTAMP NOT NULL
	);

	CREATE TABLE IF NOT EXISTS loc_journal (
		entry_id           INTEGER PRIMARY KEY AUTOINCREMENT,
		subject_ref        TEXT    NOT NULL,
		kind               TEXT    NOT NULL,        -- 'move' | 'report' (Stage 5)
		from_location_key  INTEGER NULL,
		to_location_key    INTEGER NULL,
		report_lat         REAL    NULL,             -- report only (Stage 6): the raw reported coordinate, for SubjectPosition's last_report_point -- Stage 5's Report never persisted this at all, a real gap closed here, not carried forward silently
		report_lon         REAL    NULL,
		report_alt         REAL    NULL,
		entered_fence_keys TEXT    NOT NULL DEFAULT '[]', -- JSON array of fence_key
		exited_fence_keys  TEXT    NOT NULL DEFAULT '[]',
		at                 TIMESTAMP NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_loc_journal_subject
		ON loc_journal(subject_ref, entry_id);
	`
	_, err := s.db.ExecContext(ctx, ddl)
	return err
}

// rootAnchorOK enforces XOLU-LOC010: a root row (ParentKey nil) must
// carry a non-nil Anchor. The GeoAnchor struct bundles all four
// underlying columns (lat/lon/alt/true_north) as one Go value, so
// "any anchor_* column NULL" (the SQL-level statement of this rule,
// loc-02-implementation.md Stage 1) collapses to "Anchor is nil" at
// the typed level — there is no way to construct a GeoAnchor with only
// some fields set, which is a stricter and simpler invariant than four
// independently-nullable columns would give, not a weaker one.
func rootAnchorOK(def LocationDef) bool {
	if def.ParentID != nil {
		return true // not a root; the rule doesn't apply
	}
	return def.Placement.Anchor != nil
}

// Def creates a location node. Mirrors bal.DefineAccount's shape:
// dense MAX(key)+1 allocation inside the same transaction as the
// insert, external id supplied by the caller and never reused.
// Def creates a location node. Mirrors bal.DefineAccount's shape:
// dense MAX(key)+1 allocation, external id supplied by the caller and
// never reused — but write-first, unlike bal's own version and this
// function's own earlier form: the allocation and the parent_id
// resolution both happen INSIDE one INSERT...SELECT...RETURNING
// statement, the transaction's opening write, rather than a preceding
// SELECT. An early version did the allocation as a separate read
// first — a sandbox adversarial concurrency test (30 goroutines
// defining distinct locations) surfaced real, if intermittent,
// SQLITE_BUSY failures before this fix, the same WAL read-then-write-
// upgrade class T-115 already found and fixed in Move; not assumed
// safe here by analogy, confirmed directly with the same kind of
// test.
func (s *Store) Def(ctx context.Context, def LocationDef) (LocationKey, error) {
	if def.ID == "" {
		return 0, &ValidationError{Detail: "location_id is required"}
	}
	if !rootAnchorOK(def) {
		return 0, &RootAnchorError{LocationID: def.ID}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	post := 0
	if def.Postable {
		post = 1
	}
	var aLat, aLon, aAlt, aTrueNorth interface{}
	if def.Placement.Anchor != nil {
		aLat, aLon, aAlt, aTrueNorth = def.Placement.Anchor.Lat, def.Placement.Anchor.Lon, def.Placement.Anchor.Alt, def.Placement.Anchor.TrueNorth
	}
	var parentIDArg interface{}
	if def.ParentID != nil {
		parentIDArg = *def.ParentID
	}

	var newKey int64
	var resolvedParentKey sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO `+s.locationsTable()+`
			(location_key, location_id, parent_key, name, postable,
			 offset_x, offset_y, offset_z, rotation,
			 anchor_lat, anchor_lon, anchor_alt, anchor_true_north)
		SELECT COALESCE(MAX(location_key), 0) + 1, ?,
			(SELECT location_key FROM `+s.locationsTable()+` WHERE location_id = ?),
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		FROM `+s.locationsTable()+`
		RETURNING location_key, parent_key`,
		def.ID, parentIDArg, def.Name, post,
		def.Placement.OffsetX, def.Placement.OffsetY, def.Placement.OffsetZ, def.Placement.Rotation,
		aLat, aLon, aAlt, aTrueNorth).Scan(&newKey, &resolvedParentKey)
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqliteConstraintUnique {
			return 0, &DuplicateLocationError{LocationID: def.ID}
		}
		return 0, err
	}
	// The subquery silently resolves to NULL both for "no parent
	// wanted" (parentIDArg itself was NULL) and "parent_id given but
	// not found" — these must not be conflated, or a typo'd parent_id
	// would silently create an orphaned root instead of refusing.
	if def.ParentID != nil && !resolvedParentKey.Valid {
		return 0, &UnknownLocationError{LocationID: *def.ParentID}
	}

	// Every location gets a capacity row, ceiling NULL (unlimited) by
	// default — mirrors bal.DefineAccount's own balances-row-in-the-
	// same-tx pattern. Needed even for non-postable nodes: Move's CAS
	// is an UPDATE, never an insert, so the row must already exist
	// before any first entry, and postable can change later only via
	// re-parenting (a v1 non-goal, loc-01-rest-api.md §1) — never via
	// promoting a fresh node with no capacity row waiting for it.
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO loc_capacity (location_key, ceiling, count) VALUES (?, NULL, 0)`, newKey); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return LocationKey(uint32(newKey)), nil
}

// DefFence creates a fence identity plus its loc_fence_capacity row
// (ceiling NULL, count 0), the same paired-insert-in-one-tx shape
// Def uses for locations. Stage 2 gives fences no geometry at all —
// only identity and capacity — since fence membership is a caller-
// supplied test hook here (Move's EnteredFenceKeys/ExitedFenceKeys),
// not yet computed from real Contains tests (Stage 3, T-116).
// DefFence creates a fence identity plus its loc_fence_capacity row
// (ceiling NULL, count 0), the same paired-insert-in-one-tx shape
// Def uses for locations. alignedLocationID, if non-nil, marks this
// as a tree-aligned fence (loc-01-rest-api.md §2): its identity is
// still fence_id (kept as the internal addressing key throughout this
// package), but Move's own ancestor walk (admission.go) uses
// aligned_location_key to auto-derive entered/exited fences for a
// tree-assigned subject — the "free tree walk" loc-00-design.md §5
// describes, not requiring the exact Contains test §7b reserves for
// a guard decision on a genuinely reported coordinate.
// DefFence creates a fence identity plus its loc_fence_capacity row
// (ceiling NULL, count 0), the same paired-insert-in-one-tx shape
// Def uses for locations. alignedLocationID, if non-nil, marks this
// as a tree-aligned fence (loc-01-rest-api.md §2): its identity is
// still fence_id (kept as the internal addressing key throughout this
// package), but Move's own ancestor walk (admission.go) uses
// aligned_location_key to auto-derive entered/exited fences for a
// tree-assigned subject — the "free tree walk" loc-00-design.md §5
// describes, not requiring the exact Contains test §7b reserves for
// a guard decision on a genuinely reported coordinate.
//
// Write-first, same fix and same reason as Def: the dense-key
// allocation and the aligned_location_key resolution both happen
// inside one INSERT...SELECT...RETURNING statement rather than a
// preceding SELECT — an early version had the identical read-first
// race Def's own adversarial concurrency test caught, fixed here
// alongside it rather than left for a later session to rediscover.
func (s *Store) DefFence(ctx context.Context, fenceID string, alignedLocationID *string) (FenceKey, error) {
	if fenceID == "" {
		return 0, &ValidationError{Detail: "fence id is required"}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var alignedIDArg interface{}
	if alignedLocationID != nil {
		alignedIDArg = *alignedLocationID
	}

	var newKey int64
	var resolvedAlignedKey sql.NullInt64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO fences (fence_key, fence_id, aligned_location_key)
		SELECT COALESCE(MAX(fence_key), 0) + 1, ?,
			(SELECT location_key FROM `+s.locationsTable()+` WHERE location_id = ?)
		FROM fences
		RETURNING fence_key, aligned_location_key`,
		fenceID, alignedIDArg).Scan(&newKey, &resolvedAlignedKey)
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) && sqliteErr.Code() == sqliteConstraintUnique {
			return 0, &DuplicateFenceError{FenceID: fenceID}
		}
		return 0, err
	}
	if alignedLocationID != nil && !resolvedAlignedKey.Valid {
		return 0, &UnknownLocationError{LocationID: *alignedLocationID}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO loc_fence_capacity (fence_key, ceiling, count) VALUES (?, NULL, 0)`, newKey); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return FenceKey(uint32(newKey)), nil
}

// scanLocation reads one row into a Location. Shared by Get and List
// so the column list and Anchor-reconstruction logic exist in exactly
// one place.
func scanLocation(row interface {
	Scan(dest ...interface{}) error
}) (*Location, error) {
	var loc Location
	var parentKey sql.NullInt64
	var aLat, aLon, aAlt, aTrueNorth sql.NullFloat64
	var postable int
	if err := row.Scan(
		&loc.Key, &loc.ID, &parentKey, &loc.Name, &postable,
		&loc.OffsetX, &loc.OffsetY, &loc.OffsetZ, &loc.Rotation,
		&aLat, &aLon, &aAlt, &aTrueNorth, &loc.CreatedAt); err != nil {
		return nil, err
	}
	loc.Postable = postable != 0
	if parentKey.Valid {
		pk := LocationKey(uint32(parentKey.Int64))
		loc.ParentKey = &pk
	}
	if aLat.Valid && aLon.Valid && aAlt.Valid && aTrueNorth.Valid {
		loc.Anchor = &GeoAnchor{Lat: aLat.Float64, Lon: aLon.Float64, Alt: aAlt.Float64, TrueNorth: aTrueNorth.Float64}
	}
	return &loc, nil
}

const locationColumns = `location_key, location_id, parent_key, name, postable,
	offset_x, offset_y, offset_z, rotation,
	anchor_lat, anchor_lon, anchor_alt, anchor_true_north, created_at`

// Get returns one location by its external id. XOLU-LOC003 if unknown.
func (s *Store) Get(ctx context.Context, locationID string) (*Location, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+locationColumns+` FROM `+s.locationsTable()+` WHERE location_id = ?`, locationID)
	loc, err := scanLocation(row)
	if err == sql.ErrNoRows {
		return nil, &UnknownLocationError{LocationID: locationID}
	}
	return loc, err
}

// List returns every location, ordered by key (stable, insertion
// order). No pagination in Stage 1 — the same v1-scoping precedent
// bal and cal both set for their own early stages; added later only
// against a concrete need.
func (s *Store) List(ctx context.Context) ([]*Location, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+locationColumns+` FROM `+s.locationsTable()+` ORDER BY location_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Location
	for rows.Next() {
		loc, err := scanLocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, loc)
	}
	return out, rows.Err()
}

// PatchParams: name, placement, and capacity are mutable
// (loc-01-rest-api.md §1). postable and the tree position are not —
// "changing whether a node can hold subjects, or where it sits in the
// tree, is a structural move with its own admission questions...
// deliberately does not open in v1." Capacity is *ceiling*, XOLU-LOC011
// if set on a non-postable node.
type PatchParams struct {
	Name      *string
	Placement *Placement
	Ceiling   **int64 // nil: no change. non-nil pointing at nil: clear the ceiling (unlimited). non-nil pointing at a value: set it.
}

// Patch updates a location's mutable fields in place. XOLU-LOC003 if
// the location doesn't exist; XOLU-LOC011 if Ceiling is set on a
// non-postable node.
func (s *Store) Patch(ctx context.Context, locationID string, p PatchParams) error {
	if p.Name == nil && p.Placement == nil && p.Ceiling == nil {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var key int64
	var postable int
	var curLat, curLon, curAlt, curTN sql.NullFloat64
	if err := tx.QueryRowContext(ctx,
		`SELECT location_key, postable, anchor_lat, anchor_lon, anchor_alt, anchor_true_north
		   FROM `+s.locationsTable()+` WHERE location_id = ?`, locationID).
		Scan(&key, &postable, &curLat, &curLon, &curAlt, &curTN); err != nil {
		if err == sql.ErrNoRows {
			return &UnknownLocationError{LocationID: locationID}
		}
		return err
	}

	if p.Name != nil {
		if _, err := tx.ExecContext(ctx,
			`UPDATE `+s.locationsTable()+` SET name = ? WHERE location_key = ?`, *p.Name, key); err != nil {
			return err
		}
	}
	if p.Placement != nil {
		var aLat, aLon, aAlt, aTrueNorth interface{}
		var newHasAnchor bool
		var nLat, nLon, nAlt, nTN float64
		if p.Placement.Anchor != nil {
			newHasAnchor = true
			nLat, nLon, nAlt, nTN = p.Placement.Anchor.Lat, p.Placement.Anchor.Lon, p.Placement.Anchor.Alt, p.Placement.Anchor.TrueNorth
			aLat, aLon, aAlt, aTrueNorth = nLat, nLon, nAlt, nTN
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE `+s.locationsTable()+`
				SET offset_x=?, offset_y=?, offset_z=?, rotation=?,
				    anchor_lat=?, anchor_lon=?, anchor_alt=?, anchor_true_north=?
			 WHERE location_key = ?`,
			p.Placement.OffsetX, p.Placement.OffsetY, p.Placement.OffsetZ, p.Placement.Rotation,
			aLat, aLon, aAlt, aTrueNorth, key); err != nil {
			return err
		}

		// §5b's residual (T-128, wave 9b): a discretely-repositioned
		// anchor otherwise has no historical record at all, since §8's
		// journal records subject movement only. Append exactly one
		// entry when the anchor itself actually changed — not for every
		// placement PATCH (an offset/rotation-only change never touches
		// the real-world anchor), and never when the new value is
		// identical to the old, mirroring §8a's no-op-writes-nothing
		// discipline exactly. subject_ref holds locationID here, not a
		// subject entity ref — kind='anchor' is the discriminator, the
		// same reuse-the-column shape a tagged union takes; there is no
		// subject involved in a location's own anchor changing.
		// report_lat/lon/alt (named for report's own raw coordinate) are
		// reused to carry the new anchor's position — the historically
		// useful part. true_north has no column to land in without a
		// schema change, out of this item's stated scope ("no new
		// schema"); the gap is real but minor, named here rather than
		// silently dropped.
		oldHasAnchor := curLat.Valid
		anchorChanged := oldHasAnchor != newHasAnchor
		if oldHasAnchor && newHasAnchor {
			anchorChanged = curLat.Float64 != nLat || curLon.Float64 != nLon ||
				curAlt.Float64 != nAlt || curTN.Float64 != nTN
		}
		if anchorChanged {
			var jLat, jLon, jAlt interface{}
			if newHasAnchor {
				jLat, jLon, jAlt = nLat, nLon, nAlt
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO loc_journal (subject_ref, kind, report_lat, report_lon, report_alt, at)
				 VALUES (?, 'anchor', ?, ?, ?, ?)`,
				locationID, jLat, jLon, jAlt, time.Now().UTC()); err != nil {
				return err
			}
		}
	}
	if p.Ceiling != nil {
		if postable == 0 {
			return &CapacityOnNonPostableError{LocationID: locationID}
		}
		var ceilingVal interface{}
		if *p.Ceiling != nil {
			ceilingVal = **p.Ceiling
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE loc_capacity SET ceiling = ? WHERE location_key = ?`, ceilingVal, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Delete removes a location. Refuses (XOLU-LOC012) unconditionally —
// regardless of force — if the node or any descendant currently holds
// an assigned subject (loc-01-rest-api.md §1: "silently vacating a
// subject's canonical position is a correctness violation, not a
// convenience, and no flag overrides it"). Otherwise refuses
// (XOLU-LOC013) if the node has children, unless force is set, which
// cascades to remove *empty* descendants only — safe once the
// occupied check above has already run, since force can no longer
// reach an occupied node by the time it executes.
func (s *Store) Delete(ctx context.Context, locationID string, force bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	var key int64
	if err := tx.QueryRowContext(ctx,
		`SELECT location_key FROM `+s.locationsTable()+` WHERE location_id = ?`, locationID).Scan(&key); err != nil {
		if err == sql.ErrNoRows {
			return &UnknownLocationError{LocationID: locationID}
		}
		return err
	}

	// XOLU-LOC012, unconditional: any occupied node anywhere in the
	// subtree (including the target itself) blocks the whole delete,
	// reporting which one via a recursive CTE walk down the tree.
	var occupiedID string
	err = tx.QueryRowContext(ctx, `
		WITH RECURSIVE subtree(location_key) AS (
			SELECT location_key FROM `+s.locationsTable()+` WHERE location_key = ?
			UNION ALL
			SELECT l.location_key FROM `+s.locationsTable()+` l JOIN subtree s ON l.parent_key = s.location_key
		)
		SELECT l.location_id FROM loc_assignment a
		JOIN `+s.locationsTable()+` l ON l.location_key = a.location_key
		WHERE a.location_key IN (SELECT location_key FROM subtree)
		LIMIT 1`, key).Scan(&occupiedID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	if err == nil {
		return &OccupiedError{LocationID: occupiedID}
	}

	var childCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM `+s.locationsTable()+` WHERE parent_key = ?`, key).Scan(&childCount); err != nil {
		return err
	}
	if childCount > 0 && !force {
		return &HasChildrenError{LocationID: locationID}
	}
	if childCount > 0 && force {
		// Cascade to empty descendants only. Depth-first post-order —
		// safe from occupancy concerns: the check above already
		// refused the whole delete if anything in this subtree were
		// occupied, so every node the recursion reaches here is
		// provably empty.
		if err := deleteSubtree(ctx, tx, s.locationsTable(), key); err != nil {
			return err
		}
		return tx.Commit()
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+s.locationsTable()+` WHERE location_key = ?`, key); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteSubtree(ctx context.Context, tx *sql.Tx, table string, key int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT location_key FROM `+table+` WHERE parent_key = ?`, key)
	if err != nil {
		return err
	}
	var children []int64
	for rows.Next() {
		var ck int64
		if err := rows.Scan(&ck); err != nil {
			rows.Close()
			return err
		}
		children = append(children, ck)
	}
	rows.Close()
	for _, ck := range children {
		if err := deleteSubtree(ctx, tx, table, ck); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE location_key = ?`, key)
	return err
}
