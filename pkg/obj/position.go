// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// position.go — T-119/T-120 (wave 10): Move (all three of §0's target
// kinds — loc_leaf and unassigned built in T-119, "obj" containment
// routed to containment.go's own MoveToContainer as of T-120), Report
// (fence resolution only, mirroring loc's own report/move split), and
// ResolvePosition (walking the full containment chain to its actual
// termination, obj-00-design.md §6).

package obj

import (
	"context"
	"fmt"
	"time"

	"github.com/ha1tch/xolu/pkg/loc"
)

// MoveTarget is obj-01-rest-api.md §0's own "to" object — one of
// three kinds.
type MoveTarget struct {
	Kind         PositionKind
	LocLeafID    string // set iff Kind == PositionKindLocLeaf
	ContainerRef string // set iff Kind == PositionKindObj (Stage 2, T-120)
}

// Move sets subjectRef's canonical position (obj-01-rest-api.md §2).
//
// For PositionKindLocLeaf, locStore performs /loc's own real
// guard-bearing leaf-capacity CAS (loc.Store.Move) — that decision is
// authoritative for leaf occupancy, obj never re-implements it.
// Named plainly, not glossed over: /loc and /obj live in two separate
// per-tenant SQLite files (storelayout.TenantLocDir/TenantObjDir), so
// this is two sequential commits, not one atomic transaction spanning
// both. If loc's own commit succeeds and obj's own write of
// obj_position then fails (a narrow window — process crash between
// the two), obj_position goes stale relative to /loc's own true
// state until the next successful Move/Report for this subject
// corrects it. This is the same class of gap dxp's own coordinator
// exists to close for multi-primitive transactions generally — Stage
// 1's ordinary REST Move endpoint is not dxp-dispatched, so it does
// not get that protection. Worth a future item if this proves a real
// problem in practice; not attempted here.
//
// For PositionKindUnassigned, a REAL, NAMED GAP: if the subject was
// previously PositionKindLocLeaf, this does NOT vacate its slot in
// /loc's own leaf-capacity count — /loc has no "move to nothing"
// primitive, only "move to X", so there is no existing /loc call this
// function can make to relinquish the leaf. The leaf's occupancy
// count stays incremented for a subject /obj now considers
// unassigned. This is a genuine consistency gap between the two
// primitives for this specific transition, not an oversight papered
// over — closing it needs either a new /loc capability (a genuine
// "vacate" verb) or an accepted, documented limitation. Flagged here
// for the same reason every other real gap this session found got
// named rather than silently worked around.
func (s *Store) Move(ctx context.Context, subjectRef string, target MoveTarget, locStore *loc.Store) error {
	if target.Kind == PositionKindObj {
		// MoveToContainer (containment.go, T-120) owns its own full
		// guard-bearing transaction, including the obj_position write
		// itself -- it returns directly rather than falling through to
		// this function's own shared tail update below, which would
		// otherwise double-write (and, worse, overwrite contains_ref
		// with the wrong value, since that column update lives here).
		return s.MoveToContainer(ctx, subjectRef, target.ContainerRef)
	}

	if _, err := s.Get(ctx, subjectRef); err != nil {
		return err
	}

	switch target.Kind {
	case PositionKindLocLeaf:
		if target.LocLeafID == "" {
			return &ValidationError{Detail: "loc_leaf move requires location_id"}
		}
		if err := locStore.Move(ctx, loc.MoveParams{SubjectRef: subjectRef, ToLocationID: target.LocLeafID}); err != nil {
			return err // loc's own typed errors (UnknownLocationError, CapacityError, ...) pass through as-is
		}
	case PositionKindUnassigned:
		// See doc comment above: does not vacate a prior loc_leaf slot.
	default:
		return &ValidationError{Detail: fmt.Sprintf("unknown move target kind %q", target.Kind)}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		UPDATE obj_position SET kind = ?, loc_leaf_id = ?, contains_ref = NULL, updated_at = ?
		WHERE subject_ref = ?`,
		string(target.Kind), nullableString(target.LocLeafID), time.Now().UTC(), subjectRef); err != nil {
		return err
	}
	if err := journalEntry(ctx, tx, subjectRef, "move", string(target.Kind), target.LocLeafID, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// nullableString turns "" into a real SQL NULL rather than storing an
// empty string — loc_leaf_id should read back as absent, not present-
// but-blank, for a non-loc_leaf position.
func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// Report mirrors loc's own report/move split (loc-01-rest-api.md §0)
// for the identical reason: a raw coordinate resolves fence
// membership only, it never sets Move's own canonical position. For
// an obj subject, this means routing directly through locStore's own
// Report — obj adds no logic of its own here, per this stage's own
// filed scope ("mirrors loc's own directly -- no new logic, just
// routing through it").
func (s *Store) Report(ctx context.Context, subjectRef string, lat, lon float64, locStore *loc.Store) error {
	if _, err := s.Get(ctx, subjectRef); err != nil {
		return err
	}
	return locStore.Report(ctx, subjectRef, lat, lon)
}

// ResolvedPosition is obj-01-rest-api.md §2's own GET .../position
// response shape, pre-JSON: what the chain terminates at, and how
// many hops it took to get there. Genuinely multi-hop as of T-120 —
// walking through several PositionKindObj containment edges before
// reaching a loc_leaf or unassigned termination.
type ResolvedPosition struct {
	Kind      PositionKind
	LocLeafID string   // set iff Kind == PositionKindLocLeaf
	Chain     []string // subject refs walked, nearest-first, always at least [subjectRef] itself
}

// ResolvePosition walks a subject's position to its termination
// (obj-00-design.md §6). Stage 1 handles exactly two of the three
// cases directly (loc_leaf, unassigned) — the third (contained by
// another obj subject, walking transitively) is Stage 2's own
// deliverable; encountering PositionKindObj here is an invariant
// violation in this stage, since nothing yet writes it.
// ResolvePosition walks a subject's position to its termination
// (obj-00-design.md §6): a loc_leaf, unassigned, or (T-120) another
// obj subject's own position, walked transitively until it reaches
// one of the first two. The write-time cycle guard (containment.go)
// makes an infinite walk here impossible for legitimately-written
// data; chainLimit is a defensive bound against that invariant ever
// being violated (corruption, a bug), not a normal operating limit —
// hit only ever means "assert failed," not "grew too complex."
func (s *Store) ResolvePosition(ctx context.Context, subjectRef string) (ResolvedPosition, error) {
	const chainLimit = 1000
	chain := []string{subjectRef}
	cur := subjectRef
	for {
		if len(chain) > chainLimit {
			return ResolvedPosition{}, &ValidationError{
				Detail: fmt.Sprintf("invariant violation: containment chain for %q exceeded %d hops -- the write-time cycle guard should make this impossible", subjectRef, chainLimit),
			}
		}
		sub, err := s.Get(ctx, cur)
		if err != nil {
			return ResolvedPosition{}, err
		}
		switch sub.Position.Kind {
		case PositionKindLocLeaf, PositionKindUnassigned:
			return ResolvedPosition{
				Kind:      sub.Position.Kind,
				LocLeafID: sub.Position.LocLeafID,
				Chain:     chain,
			}, nil
		case PositionKindObj:
			cur = sub.Position.ContainedBy
			chain = append(chain, cur)
		default:
			return ResolvedPosition{}, &ValidationError{Detail: fmt.Sprintf("invariant violation: unknown position kind %q", sub.Position.Kind)}
		}
	}
}
