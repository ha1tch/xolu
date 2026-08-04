// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// store.go — T-119 (wave 10), Stage 0-1: pkg/obj's guard-bearing SQL
// store. Mirrors pkg/bal's own file layout (obj-02-implementation.md's
// own Principles: "mirror bal, not loc" -- obj has no geometry of its
// own), with its own dedicated per-tenant SQLite file
// (storelayout.TenantObjDir), the identical storage-separation
// reasoning loc's own Stage 0 already established and this session's
// own T-127/T-130 work confirmed the consequences of threading
// through dxp correctly the first time.
//
// Subject-format validation (meta_subject.go's ParseMetaSubject) is
// deliberately NOT done in this package -- it lives at the HTTP
// handler layer, mirroring pkg/loc's own T-127 precedent exactly
// (composedFenceSubjectID lives in pkg/server, not pkg/loc). Every
// function here accepts an already-canonical subjectRef string.

package obj

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"modernc.org/sqlite"

	"github.com/ha1tch/xolu/pkg/tenant"
)

// sqliteConstraintUnique/PrimaryKey: SQLite's own stable extended
// result codes (SQLITE_CONSTRAINT_UNIQUE = 2067,
// SQLITE_CONSTRAINT_PRIMARYKEY = 1555). Both checked, not just the
// first -- pkg/loc's own T-131 found the hard way that a table whose
// caller-facing id IS the literal PRIMARY KEY (obj_subjects.subject_ref
// here, exactly loc_patterns.pattern_id's own shape) raises the
// PRIMARYKEY code, not UNIQUE, and a duplicate-detection check that
// only handles one silently falls through to a raw 500.
const (
	sqliteConstraintUnique     = 2067
	sqliteConstraintPrimaryKey = 1555
)

type Store struct {
	db       *sql.DB
	tenantID tenant.TenantID
	// graph is obj's own derived, best-effort mirror target (T-123,
	// obj-00-design.md §10) -- the identical optional-nil pattern
	// bal.Store's own rollup field uses (pkg/bal/store.go): nil until
	// SetGraph is called, checked before every use, a mirror failure
	// (including a nil graph) degrades rather than fails the
	// authoritative containment write it followed. No guard ever
	// reads it (guard-locality, obj-00-design.md §5's own reasoning
	// for why containment.go's cycle check reads transaction-scoped
	// SQL rows, never this).
	graph objGraph
}

// objGraph is the minimal surface Store needs from *graph.FlatGraph —
// an interface, not the concrete type, so pkg/obj's own tests can
// supply a lightweight fake rather than pulling in pkg/graph's full
// weight, and so a future non-FlatGraph mirror target isn't a
// breaking change here.
type objGraph interface {
	AddEdge(from, to, relationship string) error
	RemoveEdge(from, to string) error
}

// SetGraph attaches the live graph instance obj's own containment
// writes should best-effort mirror into. Mirrors bal.Store's own
// SetRollupPebble naming and contract exactly. Never required —
// obj-00-design.md §10's mirror is advisory throughout; a store that
// never calls this simply never mirrors, no error, matching every
// other optional-derived-plane pattern in this codebase.
func (s *Store) SetGraph(g objGraph) { s.graph = g }

func NewStore(db *sql.DB, tenantID tenant.TenantID) *Store {
	return &Store{db: db, tenantID: tenantID}
}

func (s *Store) TenantID() tenant.TenantID { return s.tenantID }

// DB exposes the underlying *sql.DB for callers needing raw access —
// tests checking a real side effect directly, not ordinary callers.
func (s *Store) DB() *sql.DB { return s.db }

// Init creates obj's tables. Idempotent.
func (s *Store) Init(ctx context.Context) error {
	ddl := `
	CREATE TABLE IF NOT EXISTS obj_subjects (
		subject_ref   TEXT PRIMARY KEY,       -- meta_subject.go's canonical "kind:key" form
		max_weight_kg REAL NULL,
		max_volume_m3 REAL NULL,
		max_count     INTEGER NULL,
		cur_weight_kg REAL NOT NULL DEFAULT 0,
		cur_volume_m3 REAL NOT NULL DEFAULT 0,
		cur_count     INTEGER NOT NULL DEFAULT 0,
		retired_at    TIMESTAMP NULL,          -- T-122: obj-00-design.md §12's terminal lifecycle state -- set once, never cleared (irreversible, no un-retire). The row persists (closer in shape to a bal account closure than to Detach's own deletion) so GET and the journal fold can still see it.
		created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS obj_position (
		subject_ref  TEXT PRIMARY KEY REFERENCES obj_subjects(subject_ref),
		kind         TEXT NOT NULL DEFAULT '',  -- 'loc_leaf' | 'obj' (Stage 2) | '' (unassigned)
		loc_leaf_id  TEXT NULL,                  -- set iff kind = 'loc_leaf'
		contains_ref TEXT NULL REFERENCES obj_subjects(subject_ref), -- set iff kind = 'obj' (Stage 2) -- this IS the containment edge, contained->container; no separate edge table needed
		updated_at   TIMESTAMP NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_obj_position_contains ON obj_position(contains_ref);

	CREATE TABLE IF NOT EXISTS obj_journal (
		entry_id      INTEGER PRIMARY KEY AUTOINCREMENT,
		subject_ref   TEXT    NOT NULL,
		kind          TEXT    NOT NULL,        -- 'attach' | 'move' | 'detach' | 'retire' (T-122)
		position_kind TEXT    NULL,             -- for 'attach'/'move': the resulting position kind ('loc_leaf' | 'obj' | '')
		loc_leaf_id   TEXT    NULL,
		container_ref TEXT    NULL,
		at            TIMESTAMP NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_obj_journal_subject ON obj_journal(subject_ref, entry_id);
	`
	_, err := s.db.ExecContext(ctx, ddl)
	return err
}

// Attach gives an existing entity obj capability (obj-01-rest-api.md
// §1). XOLU-OBJ006 if already attached. subjectRef must already be
// format-validated and canonicalised by the caller (the HTTP handler
// layer, T-127's own precedent).
func (s *Store) Attach(ctx context.Context, subjectRef string, capacity Capacity) error {
	if subjectRef == "" {
		return &ValidationError{Detail: "subject is required"}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if err := attachInTx(ctx, tx, subjectRef, capacity); err != nil {
		return err
	}
	return tx.Commit()
}

// attachInTx is Attach's own transaction-scoped core — split out so
// dxp's own Execute (dxp_adapter.go, T-121) can run the identical
// insert logic against the coordinator-supplied transaction, matching
// containment.go's own moveToContainerInTx split. Promote's own obj
// leg composes this immediately followed by moveToContainerInTx in
// one Execute call — attach-then-contain, atomically, matching the
// ordinary two-step Attach-then-MoveToContainer flow exactly, just
// within one coordinator-supplied transaction instead of two
// independently-committed ones.
// journalEntry appends one immutable row to obj_journal (T-122) — the
// append-only record of every position/containment/lifecycle change,
// mirroring loc_journal's own shape and role. kind is one of
// "attach"/"move"/"detach"/"retire". For "attach"/"move", positionKind
// is written literally, even when "" (explicitly unassigned) — NULL
// is reserved for "detach"/"retire", where position_kind genuinely
// does not apply; collapsing these two cases via a blanket
// nullableString would make an unassigned attach indistinguishable
// from a detach in the journal, corrupting the fold oracle below.
func journalEntry(ctx context.Context, tx *sql.Tx, subjectRef, kind, positionKind, locLeafID, containerRef string) error {
	var positionKindVal interface{}
	if kind == "attach" || kind == "move" {
		positionKindVal = positionKind
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO obj_journal (subject_ref, kind, position_kind, loc_leaf_id, container_ref, at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		subjectRef, kind, positionKindVal, nullableString(locLeafID), nullableString(containerRef), time.Now().UTC())
	return err
}

func attachInTx(ctx context.Context, tx *sql.Tx, subjectRef string, capacity Capacity) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO obj_subjects (subject_ref, max_weight_kg, max_volume_m3, max_count)
		VALUES (?, ?, ?, ?)`,
		subjectRef, capacity.MaxWeightKg, capacity.MaxVolumeM3, capacity.MaxCount)
	if err != nil {
		var sqliteErr *sqlite.Error
		if errors.As(err, &sqliteErr) &&
			(sqliteErr.Code() == sqliteConstraintUnique || sqliteErr.Code() == sqliteConstraintPrimaryKey) {
			return &AlreadyAttachedError{SubjectRef: subjectRef}
		}
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO obj_position (subject_ref, kind, updated_at) VALUES (?, '', ?)`,
		subjectRef, time.Now().UTC()); err != nil {
		return err
	}
	return journalEntry(ctx, tx, subjectRef, "attach", "", "", "")
}

// Get fetches one subject's full obj state. XOLU-OBJ001 (as
// NotAttachedError) if never attached.
// queryer is satisfied by both *sql.DB and *sql.Tx — the same
// tx-agnostic-read pattern pkg/loc's loadFenceGeometry already uses,
// so a single implementation of Get's own read logic serves both
// ordinary callers (s.db) and guard-bearing transactions (containment.go's
// own MoveToContainer, which must read fresh, transaction-scoped state,
// not a pre-transaction snapshot).
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
}

func (s *Store) Get(ctx context.Context, subjectRef string) (*Subject, error) {
	return getQ(ctx, s.db, subjectRef)
}

func getQ(ctx context.Context, q queryer, subjectRef string) (*Subject, error) {
	var sub Subject
	sub.Ref = subjectRef
	var maxW, maxV sql.NullFloat64
	var maxC sql.NullInt64
	var retiredAt sql.NullTime
	if err := q.QueryRowContext(ctx, `
		SELECT max_weight_kg, max_volume_m3, max_count, cur_weight_kg, cur_volume_m3, cur_count, retired_at, created_at
		FROM obj_subjects WHERE subject_ref = ?`, subjectRef).
		Scan(&maxW, &maxV, &maxC, &sub.Capacity.CurWeightKg, &sub.Capacity.CurVolumeM3, &sub.Capacity.CurCount, &retiredAt, &sub.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, &NotAttachedError{SubjectRef: subjectRef}
		}
		return nil, err
	}
	if retiredAt.Valid {
		v := retiredAt.Time
		sub.RetiredAt = &v
	}
	if maxW.Valid {
		v := maxW.Float64
		sub.Capacity.MaxWeightKg = &v
	}
	if maxV.Valid {
		v := maxV.Float64
		sub.Capacity.MaxVolumeM3 = &v
	}
	if maxC.Valid {
		v := maxC.Int64
		sub.Capacity.MaxCount = &v
	}

	var kind string
	var locLeafID, containsRef sql.NullString
	if err := q.QueryRowContext(ctx, `
		SELECT kind, loc_leaf_id, contains_ref, updated_at FROM obj_position WHERE subject_ref = ?`,
		subjectRef).Scan(&kind, &locLeafID, &containsRef, &sub.Position.UpdatedAt); err != nil {
		return nil, err // invariant: Attach always creates the position row in the same tx
	}
	sub.Position.Kind = PositionKind(kind)
	if locLeafID.Valid {
		sub.Position.LocLeafID = locLeafID.String
	}
	if containsRef.Valid {
		sub.Position.ContainedBy = containsRef.String
	}
	return &sub, nil
}

// Detach removes obj capability entirely (obj-01-rest-api.md §1) —
// bookkeeping cleanup, not the lifecycle retire operation (§6).
// XOLU-OBJ007 if the subject is positioned anywhere other than
// unassigned. The "or currently contains anything" half of §1's OR
// refusal condition has no table to check against yet in this stage
// (the containment edge table is Stage 2's own deliverable,
// obj-02-implementation.md) — named here rather than silently
// omitted, and Stage 2 must extend this check when that table exists,
// not treat it as already handled.
func (s *Store) Detach(ctx context.Context, subjectRef string) error {
	sub, err := s.Get(ctx, subjectRef)
	if err != nil {
		return err
	}
	if sub.Position.Kind != PositionKindUnassigned {
		return &DetachRefusedError{SubjectRef: subjectRef, Reason: "positioned"}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM obj_position WHERE subject_ref = ?`, subjectRef); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM obj_subjects WHERE subject_ref = ?`, subjectRef)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return &NotAttachedError{SubjectRef: subjectRef}
	}
	if err := journalEntry(ctx, tx, subjectRef, "detach", "", "", ""); err != nil {
		return err
	}
	return tx.Commit()
}
