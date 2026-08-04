// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// entity_list_handlers.go — GET /api/v1/entity/list: enumerates every
// entity type that actually has data for the current tenant, whether
// or not it has a registered schema.
//
// Deliberately NOT the same thing as GET /api/v1/schemas
// (handleListSchemas): that endpoint lists entity types with a
// registered schema, which misses schemaless entities entirely --
// data written to a type nobody ever called POST /api/v1/schema/{type}
// for. This one queries the actual node data (GROUP BY entity_type on
// the tenant's own nodes table), so a schemaless entity type shows up
// here with has_schema:false, count of its rows, and nothing else --
// which is exactly the population the schema-promotion feature (T-151)
// needs to be able to enumerate.

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"sort"

	xoluerr "github.com/ha1tch/xolu/pkg/errors"
	"github.com/ha1tch/xolu/pkg/storage"
)

// entityListEntry describes one entity type in the response.
type entityListEntry struct {
	EntityType string `json:"entity_type"`
	Count      int64  `json:"count"`
	HasSchema  bool   `json:"has_schema"`
	Adapted    bool   `json:"adapted"`
	// Columns and Indexes are populated only when Adapted is true --
	// a schemaless or schema-only (not yet adapted) entity type has
	// neither, since its data lives entirely in the generic nodes
	// table's own data column, not a dedicated table.
	Columns    []string             `json:"columns,omitempty"`
	Indexes    []entityListIndex    `json:"indexes,omitempty"`
	Graph      *entityListGraphInfo `json:"graph,omitempty"`
	FirstSeen  string               `json:"first_seen,omitempty"`
	LastUpdate string               `json:"last_update,omitempty"`
}

type entityListIndex struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
}

// entityListGraphInfo is only populated when the caller opted in via
// ?include_graph=true -- see handleListEntities' own comment on why
// this is opt-in, not default.
type entityListGraphInfo struct {
	OutEdges          int64    `json:"out_edges"`
	InEdges           int64    `json:"in_edges"`
	RelationshipTypes []string `json:"relationship_types,omitempty"`
}

func (s *Server) handleListEntities(w http.ResponseWriter, r *http.Request) {
	store := s.getStore(r.Context())
	sqlStore, ok := store.(*storage.SQLiteStore)
	if !ok {
		s.writeError(w, http.StatusNotImplemented, xoluerr.ErrStorageFailed,
			"Entity listing is only supported against a SQLite-backed store")
		return
	}

	includeGraph := r.URL.Query().Get("include_graph") == "true"

	schemaSet := make(map[string]bool)
	for _, e := range s.validator.LoadedEntities() {
		schemaSet[e] = true
	}

	entries, err := listTenantEntities(r.Context(), sqlStore, schemaSet, includeGraph)
	if err != nil {
		s.logger.Error().Err(err).Msg("entity list: query failed")
		s.writeError(w, http.StatusInternalServerError, xoluerr.ErrStorageFailed, "Failed to list entities")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"entities": entries,
		"count":    len(entries),
	})
}

// listTenantEntities does the actual work, factored out of the HTTP
// handler so it's independently testable and reusable (the schema-
// promotion feature's own heuristics need this same enumeration to
// find candidate entity types). schemaSet names every entity type
// with a registered schema (s.validator.LoadedEntities(), the same
// source handleListSchemas itself reads) -- passed in rather than
// computed here because "which entities have a schema" is a server-
// level (validator) concern, not a storage-level one; this function
// only knows about the SQL store.
//
// Entity type names come from the node-ID sequence table (nseq), not
// a GROUP BY on the nodes table -- a real bug caught by directly
// testing this end to end (an adapted entity type seeded and
// immediately missing from the result): SQLiteStore.createInner's own
// comment says it plainly, "Insert entity: adapted table or blob" --
// an adapted entity type's data goes to its own dedicated table and
// NEVER gets a row in the generic nodes table at all. nseq is
// incremented before that branch splits, so it's the one place every
// entity type that has ever had an entity created is guaranteed to
// appear, adapted or not.
func listTenantEntities(ctx context.Context, sqlStore *storage.SQLiteStore, schemaSet map[string]bool, includeGraph bool) ([]entityListEntry, error) {
	tid := sqlStore.Config().TenantID
	db := sqlStore.DB()
	nseqTable := tid.NodeSeqTableName()
	nodesTable := tid.NodesTableName()

	nameRows, err := db.QueryContext(ctx, fmt.Sprintf(
		`SELECT entity_type FROM %s ORDER BY entity_type`, nseqTable))
	if err != nil {
		return nil, fmt.Errorf("query %s: %w", nseqTable, err)
	}
	var names []string
	for nameRows.Next() {
		var name string
		if err := nameRows.Scan(&name); err != nil {
			_ = nameRows.Close()
			return nil, fmt.Errorf("scan %s row: %w", nseqTable, err)
		}
		names = append(names, name)
	}
	nameErr := nameRows.Err()
	_ = nameRows.Close()
	if nameErr != nil {
		return nil, fmt.Errorf("iterating %s: %w", nseqTable, nameErr)
	}

	adapted := sqlStore.AdaptedRegistry()
	entries := make([]entityListEntry, 0, len(names))
	for _, name := range names {
		e := entityListEntry{EntityType: name, HasSchema: schemaSet[name]}

		if spec := adapted.Get(name); spec != nil {
			e.Adapted = true
			e.Columns = spec.ColumnNames()
			for _, idx := range spec.Indexes {
				e.Indexes = append(e.Indexes, entityListIndex{
					Name: idx.Name, Columns: idx.Columns, Unique: idx.Unique,
				})
			}
			// Adapted tables carry no timestamp columns (their column
			// set is derived purely from the registered schema's own
			// fields plus system columns) -- FirstSeen/LastUpdate stay
			// empty for these, not a bug, there is genuinely nothing
			// to report.
			var count int64
			if err := db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s", spec.TableName())).Scan(&count); err != nil {
				return nil, fmt.Errorf("count %s: %w", spec.TableName(), err)
			}
			e.Count = count
		} else {
			var count int64
			var firstSeen, lastUpdate sql.NullString
			err := db.QueryRowContext(ctx, fmt.Sprintf(
				`SELECT COUNT(*), MIN(created_at), MAX(updated_at) FROM %s WHERE entity_type = ?`, nodesTable),
				name).Scan(&count, &firstSeen, &lastUpdate)
			if err != nil {
				return nil, fmt.Errorf("count %s for %s: %w", nodesTable, name, err)
			}
			e.Count = count
			e.FirstSeen = firstSeen.String
			e.LastUpdate = lastUpdate.String
		}

		entries = append(entries, e)
	}

	if includeGraph && sqlStore.Config().GraphEnabled {
		graphTable := tid.GraphTableName()
		for i := range entries {
			info, err := entityGraphFootprint(ctx, db, graphTable, entries[i].EntityType)
			if err != nil {
				return nil, fmt.Errorf("graph footprint for %s: %w", entries[i].EntityType, err)
			}
			entries[i].Graph = info
		}
	}

	return entries, nil
}

// entityGraphFootprint computes one entity type's edge counts and the
// distinct relationship names touching it. Three queries, each using
// one of the graph table's own indexes
// (source_entity/source_id, target_entity/target_id) directly --
// index-range scans, not full table scans, but still one full pass
// per entity type, which is why this is opt-in (?include_graph=true)
// rather than always computed: cheap for a handful of entity types,
// not free for a tenant with hundreds of them.
func entityGraphFootprint(ctx context.Context, db *sql.DB, graphTable, entityType string) (*entityListGraphInfo, error) {
	info := &entityListGraphInfo{}

	if err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE source_entity = ?", graphTable),
		entityType).Scan(&info.OutEdges); err != nil {
		return nil, fmt.Errorf("out-edge count: %w", err)
	}
	if err := db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE target_entity = ?", graphTable),
		entityType).Scan(&info.InEdges); err != nil {
		return nil, fmt.Errorf("in-edge count: %w", err)
	}

	rows, err := db.QueryContext(ctx, fmt.Sprintf(
		`SELECT DISTINCT relationship_name FROM %s WHERE source_entity = ? OR target_entity = ? ORDER BY relationship_name`,
		graphTable), entityType, entityType)
	if err != nil {
		return nil, fmt.Errorf("relationship types: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var rel string
		if err := rows.Scan(&rel); err != nil {
			return nil, err
		}
		info.RelationshipTypes = append(info.RelationshipTypes, rel)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(info.RelationshipTypes)
	return info, nil
}
