// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ha1tch/xolu/pkg/chronicle"
	"github.com/ha1tch/xolu/pkg/models"
	"github.com/ha1tch/xolu/pkg/tenant"
)

// GraphEdgesOracle is the first real rebuild oracle (@C §4 extraction #3
// consumer; operations roadmap item 5): the edge table is DERIVED state —
// every row is implied by a REF field in a blob entity's document, written
// by syncGraphEdges inside the entity's own transaction. Derive re-extracts
// the implied edges from the authoritative documents; Current reads the
// live table. Divergence means the derived plane has drifted from the
// record — the class of fault `iolu db check` exists to catch.
//
// Boundary, stated deliberately: this oracle covers BLOB entities only.
// Adapted entities never populate the edge table — their REFs live
// decomposed in REF_{field}_entity/_id columns (the G-12 investigation's
// finding), and that plane's invariant is pinned by the REF
// compose/decompose conformance tests, not by this oracle.
func (s *SQLiteStore) GraphEdgesOracle() chronicle.RebuildOracle {
	return chronicle.RebuildOracle{
		Name:    fmt.Sprintf("graph.edges[t%04d]", s.config.TenantID),
		Derive:  s.deriveGraphEdgeFingerprint,
		Current: s.currentGraphEdgeFingerprint,
	}
}

// edgeLine is the canonical one-fact-per-line form both sides produce.
func edgeLine(srcEnt string, srcID int64, tgtEnt string, tgtID int64, rel string) string {
	return fmt.Sprintf("%s/%d -%s-> %s/%d", srcEnt, srcID, rel, tgtEnt, tgtID)
}

// deriveGraphEdgeFingerprint replays the authoritative record: every blob
// document's REF fields, through the same extractor the write path uses.
func (s *SQLiteStore) deriveGraphEdgeFingerprint(ctx context.Context) (string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT entity_type, id, data FROM `+s.nodesTable()+` ORDER BY entity_type, id`)
	if err != nil {
		return "", fmt.Errorf("derive: read nodes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var lines []string
	for rows.Next() {
		var ent string
		var id int64
		var raw string
		if err := rows.Scan(&ent, &id, &raw); err != nil {
			return "", fmt.Errorf("derive: scan node: %w", err)
		}
		var doc map[string]interface{}
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return "", fmt.Errorf("derive: %s/%d: unmarshal: %w", ent, id, err)
		}
		edges, err := models.ExtractEntityEdges(doc)
		if err != nil {
			return "", fmt.Errorf("derive: %s/%d: extract: %w", ent, id, err)
		}
		for _, e := range edges {
			lines = append(lines, edgeLine(ent, id, e.TargetEntity, int64(e.TargetID), e.Relationship))
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), nil
}

// currentGraphEdgeFingerprint reads the live derived plane.
func (s *SQLiteStore) currentGraphEdgeFingerprint(ctx context.Context) (string, error) {
	table := tenant.GraphTableName(s.config.TenantID)
	rows, err := s.db.QueryContext(ctx,
		`SELECT source_entity, source_id, target_entity, target_id, relationship_name FROM `+table)
	if err != nil {
		return "", fmt.Errorf("current: read edges: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var lines []string
	for rows.Next() {
		var srcEnt, tgtEnt, rel string
		var srcID, tgtID int64
		if err := rows.Scan(&srcEnt, &srcID, &tgtEnt, &tgtID, &rel); err != nil {
			return "", fmt.Errorf("current: scan edge: %w", err)
		}
		lines = append(lines, edgeLine(srcEnt, srcID, tgtEnt, tgtID, rel))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), nil
}
