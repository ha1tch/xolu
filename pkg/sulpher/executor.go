// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

import (
	"context"
	"errors"
	"fmt"
	"github.com/ha1tch/xolu/pkg/qs"
	"strconv"
	"strings"
	"sync"
	"time"

	sulpherast "github.com/ha1tch/sulpher/ast"
	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/storage"
	"github.com/rs/zerolog"
)

// Sentinel errors for graph query limit violations.
var (
	ErrVisitedNodeLimit = errors.New("graph visited node limit exceeded")
	ErrResultLimit      = errors.New("graph result limit exceeded")
)

// GraphQueryable is the narrow interface the Sulpher executor needs for
// SQL push-down of graph traversal queries. It extends AggregateQueryable
// with the edge table name for this executor's tenant scope.
//
// Any storage.SQLiteStore satisfies this via WithGraphStore; the server
// constructs a thin adapter when wiring the executor.
type GraphQueryable interface {
	storage.AggregateQueryable
	// GraphEdgesTable returns the tenant-scoped edge table name,
	// e.g. "graph_t0000" for tenant 0, "graph_t0001" for tenant 1.
	GraphEdgesTable() string
}

// EntityGetter is the narrow store interface required by the Executor for
// property hydration. Any storage.Store satisfies this interface, but test
// mocks only need to implement Get rather than the full storage.Store.
type EntityGetter interface {
	Get(ctx context.Context, entity string, id int) (map[string]interface{}, error)
}

// QueryResult represents the result of a query execution
type QueryResult struct {
	Data  []map[string]interface{} `json:"data"`
	Stats QueryStats               `json:"stats"`
}

// QueryStats contains execution statistics
type QueryStats struct {
	NodesTraversed int           `json:"nodes_traversed"`
	PathsFound     int           `json:"paths_found"`
	ExecutionTime  time.Duration `json:"execution_time_ms"`
}

// Executor executes Sulpher queries against a graph
type Executor struct {
	graph        graph.Graph
	maxDepth     int
	limits       GraphLimits
	tenantPrefix string // XXXX@ prefix for tenant isolation; empty means unscoped
	// store is optional. When set, entity data is fetched on demand during
	// query execution so that property conditions in WHERE clauses and inline
	// node patterns can match against real entity fields, not just the
	// topology-derived "type" and "id" keys that the graph stores natively.
	// The fetch is lazy (only when a property condition actually needs a field
	// not already in the snapshot) and cached per snapshot so each node is
	// fetched at most once per query.
	store EntityGetter
	// graphStore is optional. When set and the query is eligible, the executor
	// pushes the entire graph traversal down to SQLite as a JOIN chain over
	// the edge table and adapted entity tables, bypassing the in-memory BFS/DFS
	// path entirely. See GraphQueryable and generateGraphSQL.
	graphStore GraphQueryable
	logger     zerolog.Logger // nop by default; set via WithLogger
	mu         sync.RWMutex
}

// WithLogger attaches a logger to the executor.  The logger is used to emit
// WARN-level alerts when cross-tenant node IDs are detected in query results.
func (e *Executor) WithLogger(l zerolog.Logger) *Executor {
	e.logger = l
	return e
}

// WithGraphStore attaches a graph-query-capable store for SQL push-down.
// When set, queries over adapted entities with translatable WHERE clauses
// are executed as a single SQL JOIN chain rather than an in-memory traversal.
// The store must be scoped to the same tenant as the executor.
func (e *Executor) WithGraphStore(s GraphQueryable) *Executor {
	e.graphStore = s
	return e
}

// WithStore attaches a storage backend to the executor. When set, property
// conditions in WHERE clauses and inline node patterns are evaluated against
// the full entity data fetched from the store, not just the topology-derived
// "type" and "id" keys. The store should be scoped to the same tenant as the
// executor (i.e. already constructed with the matching TenantID).
func (e *Executor) WithStore(s EntityGetter) *Executor {
	e.store = s
	return e
}

// GraphLimits holds server-enforced limits for graph query execution.
type GraphLimits struct {
	MaxVisitedNodes int // Max nodes visited during traversal (0 = default 10000)
	MaxResults      int // Max result paths returned (0 = no limit)
}

// NewExecutor creates a new query executor with no tenant scoping.
func NewExecutor(g graph.Graph, maxDepth int) *Executor {
	return &Executor{
		graph:    g,
		maxDepth: maxDepth,
	}
}

// NewExecutorForTenant creates a query executor scoped to a specific tenant.
// tenantPrefix is the XXXX@ prefix string for the tenant (e.g. "0001@").
// The executor will only traverse nodes belonging to this tenant, and will
// return node IDs with the prefix stripped (i.e. client-facing "entity:id" format).
func NewExecutorForTenant(g graph.Graph, maxDepth int, tenantPrefix string) *Executor {
	return &Executor{
		graph:        g,
		maxDepth:     maxDepth,
		tenantPrefix: tenantPrefix,
	}
}

// SetLimits configures graph query execution limits.
func (e *Executor) SetLimits(limits GraphLimits) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.limits = limits
}

// Execute runs a parsed query and returns results.
// The context is checked during traversal; if cancelled, the query
// stops and returns an error rather than continuing to consume resources.
// Execute executes a parsed Cypher query using the executor's default max depth.
func (e *Executor) Execute(ctx context.Context, query *sulpherast.Query, hint *AlgorithmHint) (*QueryResult, error) {
	return e.ExecuteWithDepth(ctx, query, hint, e.maxDepth)
}

// ExecuteWithDepth executes a Cypher query with an explicit maxDepth override.
// If maxDepth <= 0 the executor's default is used.
func (e *Executor) ExecuteWithDepth(ctx context.Context, query *sulpherast.Query, hint *AlgorithmHint, maxDepth int) (*QueryResult, error) {
	if maxDepth <= 0 {
		maxDepth = e.maxDepth
	}
	return e.executeASTv2(ctx, query, maxDepth, hint)
}

// RelDirection represents the direction of a relationship in a path pattern.
type RelDirection int

const (
	RelOutgoing      RelDirection = iota // -[r]->
	RelIncoming                          // <-[r]-
	RelBidirectional                     // -[r]-
)

// Operator represents a comparison operator used in WHERE conditions
// and SQL push-down generation.
type Operator string

const (
	OpEq  Operator = "="
	OpNe  Operator = "!="
	OpLt  Operator = "<"
	OpGt  Operator = ">"
	OpLte Operator = "<="
	OpGte Operator = ">="
)

// graphSnapshot is an in-memory copy of the graph for consistent reads
type graphSnapshot struct {
	adjacency    map[string]map[string]graph.EdgeRef // node -> {neighbor -> EdgeRef} (outgoing)
	revAdjacency map[string]map[string]graph.EdgeRef // node -> {neighbor -> EdgeRef} (incoming)
	nodeData     map[string]map[string]interface{}
	// hydrated tracks which node IDs have already had their full entity data
	// fetched from the store. This prevents redundant store round-trips when
	// the same node is visited multiple times during a traversal.
	hydrated map[string]bool
}

// takeSnapshot creates a consistent snapshot of the graph.
// When e.tenantPrefix is set, only nodes belonging to that tenant are
// included, and the prefix is stripped from all node IDs in the snapshot.
// This means the rest of the traversal engine always sees clean "entity:id"
// style IDs regardless of whether a tenant context is active.
func (e *Executor) takeSnapshot() *graphSnapshot {
	snapshot := &graphSnapshot{
		adjacency:    make(map[string]map[string]graph.EdgeRef),
		revAdjacency: make(map[string]map[string]graph.EdgeRef),
		nodeData:     make(map[string]map[string]interface{}),
		hydrated:     make(map[string]bool),
	}

	prefix := e.tenantPrefix

	// Get the relevant nodes — tenant-scoped or all.
	var nodes []string
	if prefix != "" {
		var err error
		nodes, err = e.graph.GetAllNodesForTenant(prefix)
		if err != nil {
			// prefix is always non-empty here; this path should never be reached
			// in practice but is handled defensively.
			e.logger.Error().Err(err).Str("prefix", prefix).
				Msg("takeSnapshot: GetAllNodesForTenant failed")
			return snapshot
		}
	} else {
		nodes = e.graph.GetAllNodes()
	}

	// strip removes the tenant prefix from a node ID for client-facing use.
	strip := func(nodeID string) string {
		if prefix == "" {
			return nodeID
		}
		return strings.TrimPrefix(nodeID, prefix)
	}

	for _, rawNodeID := range nodes {
		clientID := strip(rawNodeID)

		// Copy outgoing adjacency, stripping prefixes from neighbour IDs.
		// If a stripped neighbour ID still contains '@', the edge points to a
		// node owned by a different tenant — a data integrity violation.
		// Log a WARN and exclude the edge so it never reaches the client.
		neighbors, _ := e.graph.GetNeighbors(rawNodeID)
		stripped := make(map[string]graph.EdgeRef, len(neighbors))
		for neighborRaw, ref := range neighbors {
			neighborClient := strip(neighborRaw)
			if prefix != "" && strings.Contains(neighborClient, "@") {
				e.logger.Warn().
					Str("tenant_prefix", prefix).
					Str("source_node", clientID).
					Str("foreign_node_raw", neighborRaw).
					Str("relationship", ref.Rel).
					Msg("cross-tenant edge detected in graph snapshot — excluding from query results")
				continue
			}
			stripped[neighborClient] = ref
		}
		snapshot.adjacency[clientID] = stripped

		// Build reverse adjacency from stripped outgoing edges.
		for neighborStripped, ref := range stripped {
			if snapshot.revAdjacency[neighborStripped] == nil {
				snapshot.revAdjacency[neighborStripped] = make(map[string]graph.EdgeRef)
			}
			snapshot.revAdjacency[neighborStripped][clientID] = ref
		}

		// Parse node data from ID (entity:id format after stripping).
		parts := strings.SplitN(clientID, ":", 2)
		if len(parts) == 2 {
			snapshot.nodeData[clientID] = map[string]interface{}{
				"type": parts[0],
				"id":   clientID,
			}
		}
	}

	return snapshot
}

// getNeighborsByDirection returns neighbors based on relationship direction
func (e *Executor) getNeighborsByDirection(snapshot *graphSnapshot, node string, direction RelDirection) map[string]graph.EdgeRef {
	result := make(map[string]graph.EdgeRef)

	switch direction {
	case RelOutgoing:
		for k, v := range snapshot.adjacency[node] {
			result[k] = v
		}
	case RelIncoming:
		for k, v := range snapshot.revAdjacency[node] {
			result[k] = v
		}
	case RelBidirectional:
		// Both outgoing and incoming
		for k, v := range snapshot.adjacency[node] {
			result[k] = v
		}
		for k, v := range snapshot.revAdjacency[node] {
			if _, exists := result[k]; !exists {
				result[k] = v
			}
		}
	}

	return result
}

// preHydrateEnvs batch-fetches entity data for all node variables referenced
// in envs that have not yet been hydrated. It groups unhydrated nodes by
// entity type and issues one GetMany query per type, then merges the results
// into the node maps in place. This replaces O(n) individual Get calls with
// O(entity_types) bulk queries before a RETURN projection.
//
// Only the storage.GetMany path is used; if the store does not implement
// GetMany the call is a no-op (the lazy per-property hydration in evalEnv
// will still work correctly).
func (e *Executor) preHydrateEnvs(ctx context.Context, envs []Env, snapshot *graphSnapshot) {
	if e.store == nil {
		return
	}
	gm, ok := e.store.(interface {
		GetMany(ctx context.Context, entity string, ids []int) (map[int]map[string]interface{}, error)
	})
	if !ok {
		return
	}

	// Collect all unhydrated node maps grouped by entity type.
	// byEntity: entityType → list of (id, *nodeMap)
	type nodeRef struct {
		id      int
		nodeMap map[string]interface{}
	}
	byEntity := make(map[string][]nodeRef)

	for _, env := range envs {
		for _, val := range env {
			m, ok := val.(map[string]interface{})
			if !ok {
				continue
			}
			nodeID, hasID := m["_nodeID"].(string)
			if !hasID || snapshot.hydrated[nodeID] {
				continue
			}
			parts := strings.SplitN(nodeID, ":", 2)
			if len(parts) != 2 {
				continue
			}
			entityType := parts[0]
			id, err := strconv.Atoi(parts[1])
			if err != nil {
				continue
			}
			byEntity[entityType] = append(byEntity[entityType], nodeRef{id, m})
		}
	}

	// One GetMany per entity type, then merge results.
	for entityType, refs := range byEntity {
		ids := make([]int, len(refs))
		for i, r := range refs {
			ids[i] = r.id
		}
		fetched, err := gm.GetMany(ctx, entityType, ids)
		if err != nil {
			e.logger.Debug().Str("entity", entityType).Err(err).
				Msg("preHydrateEnvs: GetMany failed; falling back to lazy hydration")
			continue
		}
		for _, ref := range refs {
			nodeID := entityType + ":" + strconv.Itoa(ref.id)
			snapshot.hydrated[nodeID] = true // mark regardless of whether found
			data, found := fetched[ref.id]
			if !found {
				continue
			}
			for k, v := range data {
				ref.nodeMap[k] = v
			}
		}
	}

	// Batch-fetch edge properties for any relationship variable whose _edgeID != 0.
	eps, hasEPS := e.store.(interface {
		GetManyEdges(ctx context.Context, edgeIDs []int) (map[int]*storage.EdgePropsResult, error)
	})
	if !hasEPS {
		return
	}

	type edgeRef struct {
		id      int
		edgeMap map[string]interface{}
	}
	var edgeRefs []edgeRef
	seen := make(map[int]bool)

	for _, env := range envs {
		for _, val := range env {
			m, ok := val.(map[string]interface{})
			if !ok {
				continue
			}
			if _, hasRel := m["_rel"]; !hasRel {
				continue
			}
			idVal := m["_edgeID"]
			id, _ := idVal.(int)
			if id == 0 || seen[id] {
				continue
			}
			seen[id] = true
			edgeRefs = append(edgeRefs, edgeRef{id, m})
		}
	}

	if len(edgeRefs) == 0 {
		return
	}

	ids := make([]int, len(edgeRefs))
	for i, r := range edgeRefs {
		ids[i] = r.id
	}
	fetched2, err := eps.GetManyEdges(ctx, ids)
	if err != nil {
		e.logger.Debug().Err(err).Msg("preHydrateEnvs: GetManyEdges failed; edge properties will be absent")
		return
	}
	for _, ref := range edgeRefs {
		result, found := fetched2[ref.id]
		if !found {
			continue
		}
		for k, v := range result.Properties {
			ref.edgeMap[k] = v
		}
	}
}

// hydrateNodeData fetches full entity data from the store into nodeData,
// updating the hydrated set to avoid redundant fetches.
func (e *Executor) hydrateNodeData(ctx context.Context, nodeID string, nodeData map[string]interface{}, hydrated map[string]bool) {
	if e.store == nil {
		return
	}
	// Already attempted — avoid a redundant store.Get even if the previous
	// attempt failed (e.g. the entity was deleted after the snapshot was taken).
	if hydrated[nodeID] {
		return
	}
	hydrated[nodeID] = true

	parts := strings.SplitN(nodeID, ":", 2)
	if len(parts) != 2 {
		return
	}
	entityType := parts[0]
	entityID, err := strconv.Atoi(parts[1])
	if err != nil {
		return
	}
	data, err := e.store.Get(ctx, entityType, entityID)
	if err != nil {
		// Node may have been deleted after the graph snapshot was taken.
		// Leave nodeData as-is; property conditions on missing fields return false.
		e.logger.Debug().
			Str("node", nodeID).Err(err).
			Msg("hydrateNodeData: store.Get failed; property conditions will not match")
		return
	}
	for k, v := range data {
		nodeData[k] = v
	}
}

// valuesEqual delegates to pkg/qs using typed comparison.
// Previously used fmt.Sprintf string comparison; qs.CompareValues is correct.
func valuesEqual(a, b interface{}) bool {
	return qs.CompareValues(a, b) == 0
}

// compareForSort delegates to pkg/qs.
func compareForSort(a, b interface{}) int {
	return qs.CompareValues(a, b)
}

// toFloat64 delegates to pkg/qs.
func toFloat64(v interface{}) (float64, bool) {
	return qs.ToFloatSafe(v)
}

func compareValues(value interface{}, op Operator, expected interface{}) bool {
	if value == nil {
		return false
	}

	switch op {
	case OpEq:
		return valuesEqual(value, expected)
	case OpNe:
		return !valuesEqual(value, expected)
	case OpLt, OpGt, OpLte, OpGte:
		return compareNumeric(value, op, expected)
	}

	return false
}

func compareNumeric(value interface{}, op Operator, expected interface{}) bool {
	// Try numeric conversion via qs for all types
	vFloat, vOk := qs.ToFloatSafe(value)
	if !vOk {
		// Fall back to string-to-float parsing for string values
		if s, ok := value.(string); ok {
			if f, err := parseFloat(s); err == nil {
				vFloat = f
				vOk = true
			}
		}
	}
	if !vOk {
		return false
	}

	eFloat, eOk := qs.ToFloatSafe(expected)
	if !eOk {
		if s, ok := expected.(string); ok {
			if f, err := parseFloat(s); err == nil {
				eFloat = f
				eOk = true
			}
		}
	}
	if !eOk {
		return false
	}

	switch op {
	case OpLt:
		return vFloat < eFloat
	case OpGt:
		return vFloat > eFloat
	case OpLte:
		return vFloat <= eFloat
	case OpGte:
		return vFloat >= eFloat
	}
	return false
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}
