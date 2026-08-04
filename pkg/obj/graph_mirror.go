// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// graph_mirror.go — T-123 (wave 10), Stage 5: after a containment
// write commits in /obj's own guard-bearing tables, best-effort
// mirror the edge into the live graph (obj-00-design.md §10). The
// identical commit-first-authoritative/mirror-second-best-effort
// shape bal.Adapter.PostCommit/EmitDeltas already established
// (pkg/bal/store.go's own "optional-nil derived plane" pattern,
// reused directly here via Store.graph, not re-derived) — a mirror
// failure (including no graph attached at all) never surfaces as
// though the already-committed containment write itself failed.
//
// No equivalent file exists in pkg/bal or pkg/loc: bal mirrors into a
// Pebble rollup cascade, loc has no derived plane fed only by
// committed writes at all (pkg/loc/dxp_adapter.go's own PostCommit
// doc comment). This is /obj's own first, genuinely new mirror
// target — the live graph itself.

package obj

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/ha1tch/xolu/pkg/tenant"
)

// nodeIDForSubject converts a canonical "kind:key" subjectRef into
// the graph node id tenantID.NodeID(entity, id) produces for the same
// entity — obj-00-design.md §4's own central decision (every
// obj-tracked thing is an entity, hydrating through the identical
// path Sulpher already gets right) means this is always a real,
// already-existing graph node, never a synthetic one obj invents.
// Calls tenant.TenantID.NodeID directly rather than replicating its
// own format string here — that format (tenant-0 vs scoped, "@"
// separator via GraphNodePrefix) is tenant.TenantID's own concern to
// own, not something worth a second, driftable copy.
func nodeIDForSubject(tenantID tenant.TenantID, subjectRef string) (string, error) {
	kind, key, found := strings.Cut(subjectRef, ":")
	if !found {
		return "", fmt.Errorf("obj graph mirror: malformed subject_ref %q", subjectRef)
	}
	id, err := strconv.Atoi(key)
	if err != nil {
		return "", fmt.Errorf("obj graph mirror: subject_ref %q key is not numeric: %w", subjectRef, err)
	}
	return tenantID.NodeID(kind, id), nil
}

// containmentRelationship names the mirrored edge's own relationship
// label — a stable, obj-specific string a Sulpher query or a human
// reading the graph can recognise as this primitive's own containment
// fact, not an arbitrary or entity-schema-derived one.
const containmentRelationship = "obj_contains"

// mirrorContainmentAdd best-effort mirrors "containerRef contains
// subjectRef" into the live graph — direction matches
// obj-00-design.md §10's own worked language ("mirror the edge into
// the live graph"): container -> subject, so GetNeighbors/FindPath
// starting from a container answers "what does this hold" directly,
// the §10 closure-query use case named explicitly. Never returns an
// error to a caller that would fail the write it followed — logged
// via degraded, matching bal's own rollupDegraded shape exactly.
func (s *Store) mirrorContainmentAdd(subjectRef, containerRef string) {
	if s.graph == nil {
		return
	}
	subjNode, err := nodeIDForSubject(s.tenantID, subjectRef)
	if err != nil {
		s.mirrorDegraded(err)
		return
	}
	contNode, err := nodeIDForSubject(s.tenantID, containerRef)
	if err != nil {
		s.mirrorDegraded(err)
		return
	}
	if err := s.graph.AddEdge(contNode, subjNode, containmentRelationship); err != nil {
		s.mirrorDegraded(err)
	}
}

// mirrorContainmentRemove best-effort mirrors the dissolution of
// "containerRef contains subjectRef" — the detach/demote-side twin of
// mirrorContainmentAdd.
func (s *Store) mirrorContainmentRemove(subjectRef, containerRef string) {
	if s.graph == nil {
		return
	}
	subjNode, err := nodeIDForSubject(s.tenantID, subjectRef)
	if err != nil {
		s.mirrorDegraded(err)
		return
	}
	contNode, err := nodeIDForSubject(s.tenantID, containerRef)
	if err != nil {
		s.mirrorDegraded(err)
		return
	}
	if err := s.graph.RemoveEdge(contNode, subjNode); err != nil {
		s.mirrorDegraded(err)
	}
}

// mirrorDegraded records that the graph mirror fell behind for one
// edge -- kept as a named, single place (matching
// bal.Store.rollupDegraded's own shape) even though it does nothing
// but note the fact today: a real staleness-tracking mechanism (an
// oracle, a metric) has exactly one call site to instrument later,
// not a scattered set of silently-swallowed errors.
func (s *Store) mirrorDegraded(err error) {
	_ = err // T-123's own minimal scope: recorded as a no-op hook today, matching this file's own doc comment on why a single call site matters more than what it currently does with it.
}

// lastKnownContainerFromJournal finds subjectRef's own most recent
// 'attach'/'move' entry with position_kind = 'obj' — the container it
// was contained by immediately before whatever just happened
// (detach). Needed specifically because, by the time
// Adapter.PostCommit runs after a detach, obj_position/obj_subjects
// no longer have a row for subjectRef at all (store.go's own deletion
// semantics) — the journal (T-122) is the only place this fact still
// lives, exactly the kind of question an append-only record answers
// that a mutable current-state table cannot once the row is gone.
func (s *Store) lastKnownContainerFromJournal(ctx context.Context, subjectRef string) (containerRef string, found bool, err error) {
	var cr sql.NullString
	err = s.db.QueryRowContext(ctx, `
		SELECT container_ref FROM obj_journal
		WHERE subject_ref = ? AND kind IN ('attach', 'move') AND position_kind = 'obj'
		ORDER BY entry_id DESC LIMIT 1`, subjectRef).Scan(&cr)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if !cr.Valid {
		return "", false, nil
	}
	return cr.String, true, nil
}
