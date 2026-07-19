// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

// executor_env.go — Env-based execution engine (Phase 2 binding model redesign)
//
// This file replaces the positional pathNode/pathEnv model in executor_ast.go
// with a flat map-based Env. Key improvements:
//
//   - Variables map directly to values: nodes, paths, lists, scalars, nil.
//   - Path objects are first-class map values ("nodes", "relationships", "length").
//   - No positional index arithmetic, no synthetic binding workarounds.
//   - Relationship variables are bindable (future: when edge properties exist).
//   - UNWIND, WITH aggregation, multiple OPTIONAL MATCH all become natural.
//
// The public API (Execute, ExecuteWithDepth) is unchanged.
// The graph layer, snapshot, and hydrateNodeData are unchanged.
// Test files require no modification.

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	sulpherast "github.com/ha1tch/sulpher/ast"
	"github.com/ha1tch/xolu/pkg/graph"
	"github.com/ha1tch/xolu/pkg/qs"
)

// ── Env type ──────────────────────────────────────────────────────────────────

// Env is the execution environment for a single query result row.
// Variable names map to their current bound values.
//
// Node values use lazy hydration: a node freshly bound from the graph carries
// only {"_nodeID": "entity:id"}. On first property access the executor calls
// hydrateNodeData which writes all store fields into the same map in place.
// Subsequent accesses are free.
//
// Path values carry {"nodes": []interface{}, "relationships": []interface{},
// "length": int}. Endpoint variables (a, b in MATCH p = shortestPath((a)-(b)))
// are additionally bound as node values.
type Env map[string]interface{}

// nodeEnv creates an Env entry for a graph node: a map pre-populated with
// the node ID so the executor can hydrate on demand.
func nodeEnv(bareID string) map[string]interface{} {
	return map[string]interface{}{"_nodeID": bareID}
}

// edgeEnv creates an Env entry for a graph edge relationship variable.
// Fields: _rel (label), _from (source bare ID), _to (target bare ID),
// _edgeID (surrogate ID; 0 when no property row exists).
// Additional properties from t<X>_edges are merged in by preHydrateEnvs.
func edgeEnv(rel, from, to string, edgeID int) map[string]interface{} {
	return map[string]interface{}{
		"_rel":    rel,
		"_from":   from,
		"_to":     to,
		"_edgeID": edgeID,
	}
}

// isEdgeEnv reports whether a map is an edge environment (bound by a
// relationship variable such as [r:KNOWS]).
func isEdgeEnv(v interface{}) bool {
	m, ok := v.(map[string]interface{})
	if !ok {
		return false
	}
	_, has := m["_rel"]
	return has
}

// pathEnvValue creates the map value stored under a path variable name.
func pathEnvValue(nodes []interface{}, rels []interface{}) map[string]interface{} {
	return map[string]interface{}{
		"nodes":         nodes,
		"relationships": rels,
		"length":        len(nodes) - 1,
	}
}

// isNodeEnv reports whether a map is a (possibly unhydrated) node environment.
func isNodeEnv(v interface{}) bool {
	m, ok := v.(map[string]interface{})
	if !ok {
		return false
	}
	_, has := m["_nodeID"]
	return has
}

// ── Entry points ──────────────────────────────────────────────────────────────

// executeASTv2 is the Env-based entry point.
func (e *Executor) executeASTv2(ctx context.Context, q *sulpherast.Query, maxDepth int, hint *AlgorithmHint) (*QueryResult, error) {
	if e.planGraphQueryAST(q) == planPushDown {
		return e.executeGraphPushDownAST(ctx, q)
	}
	// UNION / UNION ALL: multiple SingleQuery parts.
	if len(q.Union) > 0 {
		return e.executeUnionEnv(ctx, q, maxDepth, hint)
	}
	return e.executeTraversalEnv(ctx, q, maxDepth, hint)
}

func (e *Executor) executeTraversalEnv(ctx context.Context, q *sulpherast.Query, maxDepth int, hint *AlgorithmHint) (*QueryResult, error) {
	if len(q.Parts) == 0 {
		return nil, fmt.Errorf("empty query")
	}
	sq := q.Parts[0]

	cs, err := extractAllClauses(sq)
	if err != nil {
		return nil, err
	}

	// UNWIND without MATCH: expand list and return directly.
	if cs.unwind != nil && cs.match == nil {
		return e.executeUnwindEnv(ctx, cs, maxDepth)
	}

	// UNWIND with MATCH: expand list, run BFS for each item.
	if cs.unwind != nil && cs.match != nil {
		return e.executeUnwindMatchEnv(ctx, cs, maxDepth, hint)
	}

	if pathVar, isAll, ok := isShortestPathPattern(cs.match.Pattern); ok {
		if isAll {
			return e.executeAllShortestPathsEnv(ctx, cs.match, cs.ret, pathVar, maxDepth)
		}
		return e.executeShortestPathEnv(ctx, cs.match, cs.ret, pathVar, maxDepth)
	}

	startTime := time.Now()
	stats := QueryStats{}

	e.mu.RLock()
	snapshot := e.takeSnapshot()
	e.mu.RUnlock()

	alg := AlgBFS
	if hint != nil && hint.Algorithm == DFS {
		alg = AlgDFS
	}

	allParts, err := extractAllPathParts(cs.match.Pattern)
	if err != nil {
		return nil, fmt.Errorf("MATCH pattern: %w", err)
	}
	if len(allParts) == 0 || len(allParts[0]) == 0 {
		return nil, fmt.Errorf("empty MATCH pattern")
	}
	segs := allParts[0] // primary segment chain (first part)

	// ── KL-6: Convergence join on single-path topology ───────────────────────
	// Pattern: (a)-[rel1*]->(m)<-[rel2*]-(b)  parsed as one 3-segment path.
	// Detect and execute as a hash-join; skip normal BFS dispatch when matched.
	var envs []Env
	if convEnvs, isConv, convErr := e.tryConvergenceJoin(ctx, snapshot, segs, &stats, maxDepth); isConv {
		if convErr != nil {
			return nil, convErr
		}
		envs = convEnvs
	} else {
		// ── Normal BFS/DFS dispatch ───────────────────────────────────────────
		startNodes := e.findMatchingNodesAST(ctx, snapshot, segs[0], nil)
		if alg == AlgDFS {
			// DFS: independent traversal per start node (DFS order is per-root).
			for _, startNode := range startNodes {
				if err := ctx.Err(); err != nil {
					return nil, fmt.Errorf("graph query cancelled: %w", err)
				}
				rows := e.dfsEnv(ctx, snapshot, startNode, segs, &stats, maxDepth)
				envs = append(envs, rows...)
			}
		} else {
			// BFS: single unified traversal seeded with all start nodes.
			// Hub nodes downstream of multiple start nodes are expanded only once.
			envs = e.bfsEnvMulti(ctx, snapshot, startNodes, segs, &stats, maxDepth)
		}
	}

	// Comma-separated MATCH parts: each additional part refines the result set.
	// If the additional part's start-node variable is already bound in the
	// accumulated envs, we use those bound node IDs as start nodes rather than
	// all nodes of that type — this enforces the shared-variable identity
	// constraint (KL-7 fix). If the variable is not yet bound, we fall back to
	// the original Cartesian-product behaviour (correct for unrelated patterns).
	for _, partSegs := range allParts[1:] {
		if len(partSegs) == 0 {
			continue
		}

		startVarName := astNodeVar(partSegs[0].node)
		endVarName := astNodeVar(partSegs[len(partSegs)-1].node)
		var partEnvs []Env

		// ── KL-6: Convergence join ────────────────────────────────────────────
		// Detect when the additional pattern's *end* variable is the same as
		// the *terminal* variable of the primary arm, and both arms are
		// variable-length. This is the true convergence topology:
		//
		//   MATCH (a)-[*]->(m), (b)-[*]->(m)   ← m is terminal in both arms
		//
		// We must NOT fire on sequential continuations or cases where m appears
		// as an intermediate (non-terminal) variable in the primary arm.
		//
		// Correct detection criteria (all must hold):
		//   1. Additional pattern's end variable is named.
		//   2. That end variable equals the terminal variable of the primary arm.
		//   3. The terminal relationship of the primary arm is variable-length.
		//   4. The additional pattern's start variable differs from its end variable.
		//   5. The additional pattern's start variable is NOT the terminal variable
		//      of the primary arm (ruling out sequential KL-7-style continuation).
		primaryTerminalVar := astNodeVar(segs[len(segs)-1].node)
		var primaryTerminalIsVarLen bool
		if len(segs) >= 2 {
			primaryTerminalRel := segs[len(segs)-2].rel
			primaryTerminalIsVarLen = primaryTerminalRel != nil && primaryTerminalRel.HasRange
		}

		isConvergence := endVarName != "" &&
			endVarName == primaryTerminalVar &&
			primaryTerminalIsVarLen &&
			startVarName != endVarName &&
			startVarName != primaryTerminalVar

		if isConvergence {
			// Build index: endVar nodeID → []Env from first arm.
			// The node map produced by envBindWithSnapshot contains 'id' as the
			// full "entity:id" string (e.g. "person:3") — use that as the key.
			indexA := make(map[string][]Env)
			for _, env := range envs {
				if bound, ok := env[endVarName]; ok {
					if m, isMap := bound.(map[string]interface{}); isMap {
						key := nodeMapID(m)
						if key != "" {
							indexA[key] = append(indexA[key], env)
						}
					}
				}
			}

			// Run second arm from all nodes of its start type (unrestricted).
			partStart := e.findMatchingNodesAST(ctx, snapshot, partSegs[0], nil)
			var armBEnvs []Env
			if alg == AlgDFS {
				for _, sn := range partStart {
					if err := ctx.Err(); err != nil {
						return nil, fmt.Errorf("graph query cancelled: %w", err)
					}
					armBEnvs = append(armBEnvs, e.dfsEnv(ctx, snapshot, sn, partSegs, &stats, maxDepth)...)
				}
			} else {
				armBEnvs = e.bfsEnvMulti(ctx, snapshot, partStart, partSegs, &stats, maxDepth)
			}

			// Build index: endVar nodeID → []Env from second arm.
			indexB := make(map[string][]Env)
			for _, env := range armBEnvs {
				if bound, ok := env[endVarName]; ok {
					if m, isMap := bound.(map[string]interface{}); isMap {
						key := nodeMapID(m)
						if key != "" {
							indexB[key] = append(indexB[key], env)
						}
					}
				}
			}

			// Intersect: emit merged envs for every node present in both arms.
			for key, aEnvs := range indexA {
				if bEnvs, ok := indexB[key]; ok {
					for _, ea := range aEnvs {
						for _, eb := range bEnvs {
							partEnvs = append(partEnvs, mergeEnvs(ea, eb))
						}
					}
				}
			}

			envs = partEnvs
			continue
		}
		// ── End convergence join ──────────────────────────────────────────────

		if startVarName != "" && len(envs) > 0 {
			// Collect the set of already-bound node IDs for the start variable,
			// then run a separate BFS/DFS seeded from each one, carrying the
			// parent Env forward so the merged result contains all bindings.
			for _, env := range envs {
				if bound, ok := env[startVarName]; !ok {
					// Variable not yet bound in this env — fall back to full seeding.
					partStart := e.findMatchingNodesAST(ctx, snapshot, partSegs[0], nil)
					var sub []Env
					if alg == AlgDFS {
						for _, sn := range partStart {
							if err := ctx.Err(); err != nil {
								return nil, fmt.Errorf("graph query cancelled: %w", err)
							}
							sub = append(sub, e.dfsEnv(ctx, snapshot, sn, partSegs, &stats, maxDepth)...)
						}
					} else {
						sub = e.bfsEnvMulti(ctx, snapshot, partStart, partSegs, &stats, maxDepth)
					}
					for _, r := range sub {
						merged := mergeEnvs(env, r)
						partEnvs = append(partEnvs, merged)
					}
				} else {
					// Variable is bound — extract the node ID and seed only from it.
					var boundNodeID string
					if m, isMap := bound.(map[string]interface{}); isMap {
						if nid, ok := m["_nodeID"].(string); ok {
							boundNodeID = nid
						}
					}
					if boundNodeID == "" {
						continue
					}
					seedNodes := []string{boundNodeID}
					if e.tenantPrefix != "" {
						// findMatchingNodesAST returns bare IDs; ensure consistency.
						bare := strings.TrimPrefix(boundNodeID, e.tenantPrefix)
						seedNodes = []string{bare}
					}
					var sub []Env
					if alg == AlgDFS {
						for _, sn := range seedNodes {
							if err := ctx.Err(); err != nil {
								return nil, fmt.Errorf("graph query cancelled: %w", err)
							}
							sub = append(sub, e.dfsEnv(ctx, snapshot, sn, partSegs, &stats, maxDepth)...)
						}
					} else {
						sub = e.bfsEnvMulti(ctx, snapshot, seedNodes, partSegs, &stats, maxDepth)
					}
					for _, r := range sub {
						merged := mergeEnvs(env, r)
						partEnvs = append(partEnvs, merged)
					}
				}
			}
		} else {
			// No shared variable — original Cartesian behaviour.
			partStart := e.findMatchingNodesAST(ctx, snapshot, partSegs[0], nil)
			if alg == AlgDFS {
				for _, startNode := range partStart {
					if err := ctx.Err(); err != nil {
						return nil, fmt.Errorf("graph query cancelled: %w", err)
					}
					rows := e.dfsEnv(ctx, snapshot, startNode, partSegs, &stats, maxDepth)
					partEnvs = append(partEnvs, rows...)
				}
			} else {
				partEnvs = e.bfsEnvMulti(ctx, snapshot, partStart, partSegs, &stats, maxDepth)
			}
			envs = crossJoinEnvs(envs, partEnvs)
			continue
		}

		// When we processed envs individually, partEnvs already contains merged
		// results — no cross-join needed.
		envs = partEnvs
	}

	maxVisited := e.limits.MaxVisitedNodes
	if maxVisited <= 0 {
		maxVisited = 10000
	}
	if stats.NodesTraversed >= maxVisited {
		return nil, fmt.Errorf("%w: visited %d nodes (max %d)", ErrVisitedNodeLimit, stats.NodesTraversed, maxVisited)
	}
	if e.limits.MaxResults > 0 && len(envs) > e.limits.MaxResults {
		return nil, fmt.Errorf("%w: %d paths (max %d)", ErrResultLimit, len(envs), e.limits.MaxResults)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("graph query cancelled: %w", err)
	}

	if cs.match.Where != nil {
		envs = e.applyWhereEnv(ctx, envs, cs.match.Where, snapshot)
	}

	for _, optMatch := range cs.optionalMatches {
		envs = e.applyOptionalMatchEnv(ctx, envs, optMatch, snapshot, maxDepth)
	}

	if cs.withClause() != nil {
		return e.executeWithEnv(ctx, cs, envs, segs, snapshot, maxDepth)
	}

	stats.PathsFound = len(envs)

	e.preHydrateEnvs(ctx, envs, snapshot)
	results := e.projectEnvs(ctx, envs, cs.ret, snapshot)
	if cs.ret.Distinct {
		results = qs.ApplyDistinct(results)
	}
	if len(cs.ret.OrderBy) > 0 {
		results = e.applyOrderByEnv(results, cs.ret.OrderBy)
	}
	if skip := evalIntExpr(cs.ret.Skip); skip > 0 {
		if skip >= len(results) {
			results = nil
		} else {
			results = results[skip:]
		}
	}
	if limit := evalIntExpr(cs.ret.Limit); limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	stats.ExecutionTime = time.Since(startTime)
	return &QueryResult{Data: results, Stats: stats}, nil
}

// ── BFS traversal (Env) ───────────────────────────────────────────────────────

// bfsEnvMulti runs a single BFS seeded with all start nodes simultaneously.
// Each start node has its own origin scope in the visited set, so that
// shared hub nodes downstream of multiple start nodes each produce a result
// per distinct origin rather than only one result for whichever origin
// arrives first (the previous completeness bug).
func (e *Executor) bfsEnvMulti(ctx context.Context, snapshot *graphSnapshot, startNodes []string, segs []pathSegment, stats *QueryStats, maxDepth int) []Env {
	if len(startNodes) == 0 {
		return nil
	}
	queue := make([]bfsQItem, 0, len(startNodes))
	for _, startNode := range startNodes {
		queue = append(queue, bfsQItem{node: startNode, segIdx: 0, env: Env{}, depth: 0, origin: startNode})
	}
	return e.bfsEnvFromQueue(ctx, snapshot, queue, segs, stats, maxDepth)
}

// bfsEnv runs BFS from a single start node. Used by OPTIONAL MATCH and WITH.
func (e *Executor) bfsEnv(ctx context.Context, snapshot *graphSnapshot, startNode string, segs []pathSegment, stats *QueryStats, maxDepth int) []Env {
	return e.bfsEnvFromQueue(ctx, snapshot,
		[]bfsQItem{{node: startNode, segIdx: 0, env: Env{}, depth: 0, origin: startNode}},
		segs, stats, maxDepth)
}

// bfsQItem is the BFS queue item. Defined at package level so that
// bfsEnv and bfsEnvMulti can share the queue type with bfsEnvFromQueue.
type bfsQItem struct {
	node       string
	segIdx     int
	env        Env
	depth      int // node-binding count for O(1) depth check
	varLenHops int
	inVarLen   bool
	// origin is the start node that seeded this traversal path. It is used
	// to key the visited set so that the same downstream node can be reached
	// from multiple independent start nodes without one origin "stealing" the
	// result from another. This fixes a completeness bug: in a multi-seed BFS
	// where two start nodes converge on a shared hub, both paths must produce
	// results. The old (node, segIdx) key caused the second-arriving origin to
	// be silently dropped. With origin in the key, each start node gets its own
	// traversal scope for visited-node tracking.
	origin string
}

// bfsEnvFromQueue is the shared BFS core used by both bfsEnv and bfsEnvMulti.
func (e *Executor) bfsEnvFromQueue(ctx context.Context, snapshot *graphSnapshot, queue []bfsQItem, segs []pathSegment, stats *QueryStats, maxDepth int) []Env {
	var results []Env

	// visited prevents re-queueing the same (node, segIdx) pair.
	// For var-length hops the key includes the hop count.
	visited := make(map[string]bool)
	maxIter := e.limits.MaxVisitedNodes
	if maxIter <= 0 {
		maxIter = 10000
	}
	iterations := 0
	head := 0

	for head < len(queue) && iterations < maxIter {
		if iterations&0xFF == 0 {
			if err := ctx.Err(); err != nil {
				return results
			}
		}
		iterations++
		cur := queue[head]
		head++

		if cur.depth > maxDepth {
			continue
		}

		var vk string
		if cur.inVarLen {
			vk = fmt.Sprintf("%s|%s:%d:%d", cur.origin, cur.node, cur.segIdx, cur.varLenHops)
		} else {
			vk = fmt.Sprintf("%s|%s:%d", cur.origin, cur.node, cur.segIdx)
		}
		if visited[vk] {
			continue
		}
		visited[vk] = true
		stats.NodesTraversed++

		// Bind the current node into the Env for this segment.
		// When inVarLen=true we are re-visiting the same segment during a
		// variable-length expansion: the segment's start-node variable was
		// already bound when the expansion began and must not be overwritten
		// with the intermediate traversal position. Carry the incoming Env
		// unchanged; only the end-node binding is added on emit.
		var newEnv Env
		if cur.inVarLen {
			newEnv = cur.env
		} else {
			newEnv = envBindWithSnapshot(cur.env, segs, cur.segIdx, cur.node, snapshot)
		}
		// Increment depth only when the segment has a named variable (i.e. a
		// node binding was actually added to the env).
		newDepth := cur.depth
		if !cur.inVarLen && cur.segIdx < len(segs) && astNodeVar(segs[cur.segIdx].node) != "" {
			newDepth++
		}

		if cur.segIdx >= len(segs)-1 {
			results = append(results, newEnv)
			continue
		}

		seg := segs[cur.segIdx]
		nextSeg := segs[cur.segIdx+1]
		rel := seg.rel

		if rel != nil && rel.HasRange {
			minHops, maxHops := astHops(rel, maxDepth)
			dir := astRelDirection(rel)

			// Unified neighbour loop: emit and/or expand in a single pass.
			//
			// varLenHops counts hops already taken to reach cur.node.
			// Taking one more hop to 'neighbor' makes nextHops = varLenHops+1.
			//
			// Emit when nextHops >= minHops AND neighbor matches the end pattern.
			// Expand when nextHops < maxHops (continue BFS from neighbor).
			//
			// KL-4 fix: emit 'neighbor', not 'cur.node', as the end-node binding.
			// KL-5 fix: if the end-node variable is already bound in Env, enforce
			//           identity — the neighbour must equal the bound node.
			for _, nb := range e.sortedNeighbors(snapshot, cur.node, dir) {
				neighbor, edgeRef := nb.nodeID, nb.ref
				if astRelType(rel) != "" && edgeRef.Rel != astRelType(rel) {
					continue
				}
				nextHops := cur.varLenHops + 1

				if nextHops >= minHops {
					if e.matchesNodePatternAST(ctx, neighbor, snapshot.nodeData[neighbor], nextSeg.node, snapshot, nil) {
						// Identity constraint for repeated variable (KL-5).
						emit := true
						if endVar := astNodeVar(nextSeg.node); endVar != "" {
							if bound, ok := newEnv[endVar]; ok {
								if boundMap, isMap := bound.(map[string]interface{}); isMap {
									if boundID, hasID := boundMap["_nodeID"].(string); hasID {
										neighborFull := neighbor
										if e.tenantPrefix != "" {
											neighborFull = e.tenantPrefix + neighbor
										}
										if boundID != neighborFull && boundID != neighbor {
											emit = false
										}
									}
								}
							}
						}
						if emit {
							if cur.segIdx+1 >= len(segs)-1 {
								nextEnv := envBindWithSnapshot(newEnv, segs, cur.segIdx+1, neighbor, snapshot)
								results = append(results, nextEnv)
							} else {
								queue = append(queue, bfsQItem{
									node:   neighbor,
									segIdx: cur.segIdx + 1,
									env:    newEnv,
									depth:  newDepth,
									origin: cur.origin,
								})
							}
						}
					}
				}

				if nextHops < maxHops {
					queue = append(queue, bfsQItem{
						node:       neighbor,
						segIdx:     cur.segIdx,
						env:        newEnv,
						depth:      newDepth,
						varLenHops: nextHops,
						inVarLen:   true,
						origin:     cur.origin,
					})
				}
			}
		} else {
			dir := astRelDirection(rel)
			for _, nb := range e.sortedNeighbors(snapshot, cur.node, dir) {
				neighbor, ref := nb.nodeID, nb.ref
				if rel != nil && astRelType(rel) != "" && ref.Rel != astRelType(rel) {
					continue
				}
				if !e.matchesNodePatternAST(ctx, neighbor, snapshot.nodeData[neighbor], nextSeg.node, snapshot, nil) {
					continue
				}
				// Bind the relationship variable if the pattern names it (e.g. [r:KNOWS]).
				envForQueue := newEnv
				if relVarName := astRelVar(rel); relVarName != "" {
					bare := cur.node
					if e.tenantPrefix != "" {
						bare = strings.TrimPrefix(bare, e.tenantPrefix)
					}
					neighborBare := neighbor
					if e.tenantPrefix != "" {
						neighborBare = strings.TrimPrefix(neighborBare, e.tenantPrefix)
					}
					envForQueue = envWithValue(newEnv, relVarName, edgeEnv(ref.Rel, bare, neighborBare, ref.ID))
				}
				queue = append(queue, bfsQItem{
					node:   neighbor,
					segIdx: cur.segIdx + 1,
					env:    envForQueue,
					depth:  newDepth,
					origin: cur.origin,
				})
			}
		}
	}

	return results
}

// envBindWithSnapshot reuses snapshot.nodeData so that
// already-hydrated nodes are not wrapped in a fresh unhydrated map.
func envBindWithSnapshot(parent Env, segs []pathSegment, segIdx int, nodeID string, snapshot *graphSnapshot) Env {
	newEnv := make(Env, len(parent)+2)
	for k, v := range parent {
		newEnv[k] = v
	}
	if segIdx < len(segs) {
		varName := astNodeVar(segs[segIdx].node)
		if varName != "" {
			if existing := snapshot.nodeData[nodeID]; existing != nil {
				// Ensure _nodeID is present for lazy hydration.
				if _, hasID := existing["_nodeID"]; !hasID {
					existing["_nodeID"] = nodeID
				}
				newEnv[varName] = existing
			} else {
				nm := nodeEnv(nodeID)
				snapshot.nodeData[nodeID] = nm
				newEnv[varName] = nm
			}
		}
	}
	return newEnv
}

// ── DFS traversal (Env) ───────────────────────────────────────────────────────

func (e *Executor) dfsEnv(ctx context.Context, snapshot *graphSnapshot, startNode string, segs []pathSegment, stats *QueryStats, maxDepth int) []Env {
	var results []Env
	visited := make(map[string]bool)
	e.dfsEnvRecursive(ctx, snapshot, startNode, 0, segs, Env{}, 0, &results, stats, maxDepth, visited)
	return results
}

func (e *Executor) dfsEnvRecursive(ctx context.Context, snapshot *graphSnapshot, nodeID string, segIdx int, segs []pathSegment, env Env, depth int, results *[]Env, stats *QueryStats, maxDepth int, visited map[string]bool) {
	if depth >= maxDepth {
		return
	}
	if err := ctx.Err(); err != nil {
		return
	}
	vk := fmt.Sprintf("%s:%d", nodeID, segIdx)
	if visited[vk] {
		return
	}
	visited[vk] = true
	defer delete(visited, vk)

	stats.NodesTraversed++
	newEnv := envBindWithSnapshot(env, segs, segIdx, nodeID, snapshot)
	newDepth := depth
	if segIdx < len(segs) && astNodeVar(segs[segIdx].node) != "" {
		newDepth++
	}

	if segIdx >= len(segs)-1 {
		cp := make(Env, len(newEnv))
		for k, v := range newEnv {
			cp[k] = v
		}
		*results = append(*results, cp)
		return
	}

	seg := segs[segIdx]
	nextSeg := segs[segIdx+1]
	rel := seg.rel
	dir := astRelDirection(rel)

	for neighbor, ref := range e.getNeighborsByDirection(snapshot, nodeID, dir) {
		if rel != nil && astRelType(rel) != "" && ref.Rel != astRelType(rel) {
			continue
		}
		if !e.matchesNodePatternAST(ctx, neighbor, snapshot.nodeData[neighbor], nextSeg.node, snapshot, nil) {
			continue
		}
		// Bind the relationship variable if the pattern names it.
		envForRecurse := newEnv
		if relVarName := astRelVar(rel); relVarName != "" {
			bare := nodeID
			if e.tenantPrefix != "" {
				bare = strings.TrimPrefix(bare, e.tenantPrefix)
			}
			neighborBare := neighbor
			if e.tenantPrefix != "" {
				neighborBare = strings.TrimPrefix(neighborBare, e.tenantPrefix)
			}
			envForRecurse = envWithValue(newEnv, relVarName, edgeEnv(ref.Rel, bare, neighborBare, ref.ID))
		}
		e.dfsEnvRecursive(ctx, snapshot, neighbor, segIdx+1, segs, envForRecurse, newDepth, results, stats, maxDepth, visited)
	}
}

// ── WHERE evaluation (Env) ───────────────────────────────────────────────────

func (e *Executor) applyWhereEnv(ctx context.Context, envs []Env, where sulpherast.Expression, snapshot *graphSnapshot) []Env {
	var filtered []Env
	for _, env := range envs {
		if e.evalEnv(ctx, where, env, snapshot) == true {
			filtered = append(filtered, env)
		}
	}
	return filtered
}

// evalEnv evaluates a Cypher expression against an Env.
// This replaces evalExprAST. The logic is identical; the input type changes.
func (e *Executor) evalEnv(ctx context.Context, expr sulpherast.Expression, env Env, snapshot *graphSnapshot) interface{} {
	switch ex := expr.(type) {
	case *sulpherast.InfixExpression:
		op := strings.ToUpper(ex.Operator)
		switch op {
		case "AND":
			l := e.evalEnv(ctx, ex.Left, env, snapshot)
			if lb, ok := l.(bool); ok && !lb {
				return false
			}
			return e.evalEnv(ctx, ex.Right, env, snapshot)
		case "OR":
			l := e.evalEnv(ctx, ex.Left, env, snapshot)
			if lb, ok := l.(bool); ok && lb {
				return true
			}
			return e.evalEnv(ctx, ex.Right, env, snapshot)
		case "=":
			return qs.CompareValues(e.evalEnv(ctx, ex.Left, env, snapshot), e.evalEnv(ctx, ex.Right, env, snapshot)) == 0
		case "<>", "!=":
			return qs.CompareValues(e.evalEnv(ctx, ex.Left, env, snapshot), e.evalEnv(ctx, ex.Right, env, snapshot)) != 0
		case "<":
			return qs.CompareValues(e.evalEnv(ctx, ex.Left, env, snapshot), e.evalEnv(ctx, ex.Right, env, snapshot)) < 0
		case ">":
			return qs.CompareValues(e.evalEnv(ctx, ex.Left, env, snapshot), e.evalEnv(ctx, ex.Right, env, snapshot)) > 0
		case "<=":
			return qs.CompareValues(e.evalEnv(ctx, ex.Left, env, snapshot), e.evalEnv(ctx, ex.Right, env, snapshot)) <= 0
		case ">=":
			return qs.CompareValues(e.evalEnv(ctx, ex.Left, env, snapshot), e.evalEnv(ctx, ex.Right, env, snapshot)) >= 0
		default:
			l := e.evalEnv(ctx, ex.Left, env, snapshot)
			r := e.evalEnv(ctx, ex.Right, env, snapshot)
			lf, lok := qs.ToFloatSafe(l)
			rf, rok := qs.ToFloatSafe(r)
			if lok && rok {
				switch ex.Operator {
				case "+":
					return lf + rf
				case "-":
					return lf - rf
				case "*":
					return lf * rf
				case "/":
					if rf == 0 {
						return nil
					}
					return lf / rf
				case "%":
					return float64(int64(lf) % int64(rf))
				}
			}
			if ex.Operator == "+" {
				return fmt.Sprintf("%v", l) + fmt.Sprintf("%v", r)
			}
			return nil
		}

	case *sulpherast.PrefixExpression:
		r := e.evalEnv(ctx, ex.Right, env, snapshot)
		switch strings.ToUpper(ex.Operator) {
		case "NOT":
			if rb, ok := r.(bool); ok {
				return !rb
			}
			return false
		case "-":
			if f, ok := qs.ToFloatSafe(r); ok {
				return -f
			}
		}
		return nil

	case *sulpherast.PropertyAccess:
		obj := e.evalEnv(ctx, ex.Object, env, snapshot)
		m, ok := obj.(map[string]interface{})
		if !ok {
			return nil
		}
		prop := ex.Property.Value
		// Lazy hydration for node maps: if this map has a _nodeID and the
		// property isn't yet loaded, fetch it now.
		if nodeID, hasID := m["_nodeID"].(string); hasID {
			if _, present := m[prop]; !present && prop != "_nodeID" {
				e.hydrateNodeData(ctx, nodeID, m, snapshot.hydrated)
			}
			// Special: "id" returns the entity ID portion.
			if prop == "id" {
				bare := nodeID
				if e.tenantPrefix != "" {
					bare = strings.TrimPrefix(bare, e.tenantPrefix)
				}
				parts := strings.SplitN(bare, ":", 2)
				if len(parts) == 2 {
					return parts[1]
				}
				return nil
			}
		}
		// Edge maps: properties are merged in by preHydrateEnvs; access directly.
		// No special handling needed — m[prop] works for both node and edge maps.
		return m[prop]

	case *sulpherast.Identifier:
		v, ok := env[ex.Value]
		if !ok {
			return nil
		}
		// Node reference: hydrate and return as full node map.
		if m, ok := v.(map[string]interface{}); ok {
			if nodeID, hasID := m["_nodeID"].(string); hasID {
				e.hydrateNodeData(ctx, nodeID, m, snapshot.hydrated)
				// Add topology fields.
				bare := nodeID
				if e.tenantPrefix != "" {
					bare = strings.TrimPrefix(bare, e.tenantPrefix)
				}
				parts := strings.SplitN(bare, ":", 2)
				if len(parts) == 2 {
					if _, hasType := m["type"]; !hasType {
						m["type"] = parts[0]
					}
					if _, hasID2 := m["_id"]; !hasID2 {
						m["_id"] = parts[1]
					}
				}
			}
		}
		return v

	case *sulpherast.IsNullExpression:
		v := e.evalEnv(ctx, ex.Expr, env, snapshot)
		if ex.Not {
			return v != nil
		}
		return v == nil

	case *sulpherast.StringPredicate:
		left := fmt.Sprintf("%v", e.evalEnv(ctx, ex.Left, env, snapshot))
		right := fmt.Sprintf("%v", e.evalEnv(ctx, ex.Right, env, snapshot))
		var result bool
		switch ex.Predicate {
		case "STARTS WITH":
			result = strings.HasPrefix(left, right)
		case "ENDS WITH":
			result = strings.HasSuffix(left, right)
		case "CONTAINS":
			result = strings.Contains(left, right)
		case "=~":
			matched, err := regexp.MatchString(right, left)
			result = err == nil && matched
		}
		if ex.Not {
			return !result
		}
		return result

	case *sulpherast.InExpression:
		left := e.evalEnv(ctx, ex.Left, env, snapshot)
		rightVal := e.evalEnv(ctx, ex.Right, env, snapshot)
		var found bool
		if list, ok := rightVal.([]interface{}); ok {
			for _, v := range list {
				if qs.CompareValues(left, v) == 0 {
					found = true
					break
				}
			}
		}
		if ex.Not {
			return !found
		}
		return found

	case *sulpherast.ListLiteral:
		items := make([]interface{}, len(ex.Elements))
		for i, elem := range ex.Elements {
			items[i] = e.evalEnv(ctx, elem, env, snapshot)
		}
		return items

	case *sulpherast.ShortestPathExpression:
		return e.evalShortestPathEnvExpr(ctx, ex, env, snapshot)

	case *sulpherast.QuantifierExpression:
		listVal := e.evalEnv(ctx, ex.InExpr, env, snapshot)
		list, ok := toInterfaceSlice(listVal)
		if !ok {
			return false
		}
		iterVar := ex.Variable.Value
		switch strings.ToUpper(ex.Quantifier) {
		case "ALL":
			for _, item := range list {
				if ex.Where == nil {
					continue
				}
				childEnv := envWithValue(env, iterVar, item)
				if e.evalEnv(ctx, ex.Where, childEnv, snapshot) != true {
					return false
				}
			}
			return true
		case "ANY":
			for _, item := range list {
				if ex.Where == nil {
					return len(list) > 0
				}
				childEnv := envWithValue(env, iterVar, item)
				if e.evalEnv(ctx, ex.Where, childEnv, snapshot) == true {
					return true
				}
			}
			return false
		case "NONE":
			for _, item := range list {
				if ex.Where == nil {
					return len(list) == 0
				}
				childEnv := envWithValue(env, iterVar, item)
				if e.evalEnv(ctx, ex.Where, childEnv, snapshot) == true {
					return false
				}
			}
			return true
		case "SINGLE":
			count := 0
			for _, item := range list {
				if ex.Where == nil {
					count++
				} else {
					childEnv := envWithValue(env, iterVar, item)
					if e.evalEnv(ctx, ex.Where, childEnv, snapshot) == true {
						count++
					}
				}
				if count > 1 {
					return false
				}
			}
			return count == 1
		case "FILTER":
			var out []interface{}
			for _, item := range list {
				if ex.Where == nil {
					out = append(out, item)
				} else {
					childEnv := envWithValue(env, iterVar, item)
					if e.evalEnv(ctx, ex.Where, childEnv, snapshot) == true {
						out = append(out, item)
					}
				}
			}
			return out
		}
		return false

	case *sulpherast.ListComprehension:
		listVal := e.evalEnv(ctx, ex.InExpr, env, snapshot)
		list, ok := toInterfaceSlice(listVal)
		if !ok {
			return nil
		}
		iterVar := ex.Variable.Value
		var result []interface{}
		for _, item := range list {
			childEnv := envWithValue(env, iterVar, item)
			if ex.Where != nil {
				if e.evalEnv(ctx, ex.Where, childEnv, snapshot) != true {
					continue
				}
			}
			if ex.MapExpr != nil {
				result = append(result, e.evalEnv(ctx, ex.MapExpr, childEnv, snapshot))
			} else {
				result = append(result, item)
			}
		}
		return result

	case *sulpherast.FunctionCall:
		return e.evalFunctionEnv(ctx, ex, env, snapshot)
	}

	return evalLiteralAST(expr, env)
}

// envWithValue creates a child Env with one additional binding.
// This replaces envWithBinding — no synthetic pathNode needed.
func envWithValue(parent Env, varName string, value interface{}) Env {
	child := make(Env, len(parent)+1)
	for k, v := range parent {
		child[k] = v
	}
	child[varName] = value
	return child
}

// ── Function call evaluation (Env) ────────────────────────────────────────────

func (e *Executor) evalFunctionEnv(ctx context.Context, fc *sulpherast.FunctionCall, env Env, snapshot *graphSnapshot) interface{} {
	name := strings.ToUpper(fc.Name.String())
	arg0 := func() interface{} {
		if len(fc.Arguments) > 0 {
			return e.evalEnv(ctx, fc.Arguments[0], env, snapshot)
		}
		return nil
	}

	switch name {
	case "NODES":
		v := arg0()
		// Path object: extract nodes list.
		if m, ok := v.(map[string]interface{}); ok {
			if nodes, ok := m["nodes"]; ok {
				return nodes
			}
		}
		if s, ok := toInterfaceSlice(v); ok {
			return s
		}
		return nil

	case "RELATIONSHIPS":
		v := arg0()
		if m, ok := v.(map[string]interface{}); ok {
			if rels, ok := m["relationships"]; ok {
				return rels
			}
		}
		if s, ok := toInterfaceSlice(v); ok {
			return s
		}
		return nil

	case "LENGTH", "SIZE":
		v := arg0()
		if m, ok := v.(map[string]interface{}); ok {
			if l, ok := m["length"]; ok {
				return l
			}
		}
		if s, ok := toInterfaceSlice(v); ok {
			return len(s)
		}
		if s, ok := v.(string); ok {
			return len(s)
		}
		return nil

	case "HEAD":
		if s, ok := toInterfaceSlice(arg0()); ok && len(s) > 0 {
			return s[0]
		}
		return nil

	case "LAST":
		if s, ok := toInterfaceSlice(arg0()); ok && len(s) > 0 {
			return s[len(s)-1]
		}
		return nil

	case "TAIL":
		if s, ok := toInterfaceSlice(arg0()); ok && len(s) > 1 {
			return s[1:]
		}
		return []interface{}{}

	case "COALESCE":
		for _, arg := range fc.Arguments {
			v := e.evalEnv(ctx, arg, env, snapshot)
			if v != nil {
				return v
			}
		}
		return nil

	case "TOSTRING", "TOSTR":
		v := arg0()
		if v == nil {
			return nil
		}
		return fmt.Sprintf("%v", v)

	case "TOINTEGER", "TOINT":
		if f, ok := qs.ToFloatSafe(arg0()); ok {
			return int(f)
		}
		return nil

	case "TOFLOAT":
		if f, ok := qs.ToFloatSafe(arg0()); ok {
			return f
		}
		return nil

	case "ABS":
		if f, ok := qs.ToFloatSafe(arg0()); ok {
			if f < 0 {
				return -f
			}
			return f
		}
		return nil

	case "TOUPPER":
		if s, ok := arg0().(string); ok {
			return strings.ToUpper(s)
		}
		return nil

	case "TOLOWER":
		if s, ok := arg0().(string); ok {
			return strings.ToLower(s)
		}
		return nil

	case "TRIM":
		if s, ok := arg0().(string); ok {
			return strings.TrimSpace(s)
		}
		return nil

	case "TYPE":
		if m, ok := arg0().(map[string]interface{}); ok {
			// Directly-bound relationship variables (created by edgeEnv) store
			// the relationship label under "_rel". Path relationships store it
			// under "type". Check "_rel" first so that type(r) works for both.
			if v, ok := m["_rel"]; ok {
				return v
			}
			return m["type"]
		}
		return nil

	case "ID":
		if m, ok := arg0().(map[string]interface{}); ok {
			if id, ok := m["_id"]; ok {
				return id
			}
			// Hydrated node: derive from _nodeID.
			if nodeID, ok := m["_nodeID"].(string); ok {
				bare := nodeID
				if e.tenantPrefix != "" {
					bare = strings.TrimPrefix(bare, e.tenantPrefix)
				}
				parts := strings.SplitN(bare, ":", 2)
				if len(parts) == 2 {
					return parts[1]
				}
			}
		}
		return nil

	case "LABELS":
		if m, ok := arg0().(map[string]interface{}); ok {
			if t, ok := m["type"].(string); ok {
				return []interface{}{t}
			}
			// Derive from _nodeID.
			if nodeID, ok := m["_nodeID"].(string); ok {
				bare := nodeID
				if e.tenantPrefix != "" {
					bare = strings.TrimPrefix(bare, e.tenantPrefix)
				}
				parts := strings.SplitN(bare, ":", 2)
				if len(parts) == 2 {
					return []interface{}{parts[0]}
				}
			}
		}
		return nil

	case "EXISTS":
		return arg0() != nil
	}

	return nil
}

// ── Aggregation ──────────────────────────────────────────────────────────────

// aggregateFuncs is the set of RETURN function names that are aggregate
// (require grouping and accumulation across rows rather than per-row evaluation).
var aggregateFuncs = map[string]bool{
	"COUNT": true, "COLLECT": true, "AVG": true, "SUM": true,
	"MIN": true, "MAX": true, "PERCENTILECONT": true, "PERCENTILEDISC": true,
	"STDEV": true, "STDEVP": true,
}

// isAggregateExpr reports whether a RETURN expression contains an aggregate
// function call at the top level.
func isAggregateExpr(expr sulpherast.Expression) bool {
	fc, ok := expr.(*sulpherast.FunctionCall)
	if !ok {
		return false
	}
	return aggregateFuncs[strings.ToUpper(fc.Name.String())]
}

// hasAggregation reports whether any RETURN item is an aggregate expression.
func hasAggregation(ret *sulpherast.ReturnClause) bool {
	for _, item := range ret.Items {
		if !item.Star && isAggregateExpr(item.Expr) {
			return true
		}
	}
	return false
}

// applyAggregation applies Cypher implicit GROUP BY semantics:
// - Non-aggregate RETURN items are grouping keys
// - Aggregate items are computed per group
// - Returns one row per unique combination of grouping key values
func (e *Executor) applyAggregation(ctx context.Context, envs []Env, ret *sulpherast.ReturnClause, snapshot *graphSnapshot) []map[string]interface{} {
	type aggState struct {
		count             int
		collected         []interface{}
		sum               float64
		sumOK             bool
		min               interface{}
		max               interface{}
		countDistinctSeen map[interface{}]bool
	}

	// Separate grouping keys from aggregate items.
	type aggItem struct {
		key string
		fc  *sulpherast.FunctionCall
	}
	var groupKeys []struct {
		key  string
		expr sulpherast.Expression
	}
	var aggItems []aggItem

	for _, item := range ret.Items {
		if item.Star {
			continue
		}
		key := exprToKey(item.Expr)
		if item.Alias != nil {
			key = item.Alias.Value
		}
		if isAggregateExpr(item.Expr) {
			aggItems = append(aggItems, aggItem{
				key: key,
				fc:  item.Expr.(*sulpherast.FunctionCall),
			})
		} else {
			groupKeys = append(groupKeys, struct {
				key  string
				expr sulpherast.Expression
			}{key, item.Expr})
		}
	}

	// Accumulate per group.
	type groupEntry struct {
		keyVals []interface{}
		states  []aggState
	}

	groups := make(map[string]*groupEntry)
	var groupOrder []string // preserve first-seen order

	for _, env := range envs {
		// Compute grouping key values.
		keyVals := make([]interface{}, len(groupKeys))
		for i, gk := range groupKeys {
			keyVals[i] = e.evalEnvForReturn(ctx, gk.expr, env, snapshot)
		}

		// Build a canonical string key for the group.
		groupKey := fmt.Sprintf("%v", keyVals)

		entry, exists := groups[groupKey]
		if !exists {
			states := make([]aggState, len(aggItems))
			for i := range states {
				states[i].countDistinctSeen = make(map[interface{}]bool)
			}
			entry = &groupEntry{keyVals: keyVals, states: states}
			groups[groupKey] = entry
			groupOrder = append(groupOrder, groupKey)
		}

		// Accumulate each aggregate.
		for i, ai := range aggItems {
			fn := strings.ToUpper(ai.fc.Name.String())
			st := &entry.states[i]

			// Evaluate the argument (or use nil for count(*)).
			var argVal interface{}
			if len(ai.fc.Arguments) > 0 {
				argVal = e.evalEnvForReturn(ctx, ai.fc.Arguments[0], env, snapshot)
			}

			// Handle DISTINCT: skip if already seen.
			if ai.fc.Distinct {
				key := fmt.Sprintf("%v", argVal)
				if st.countDistinctSeen[key] {
					continue
				}
				st.countDistinctSeen[key] = true
			}

			switch fn {
			case "COUNT":
				// count(*) uses ast.CountStar as the argument.
				isStar := len(ai.fc.Arguments) == 0
				if !isStar && len(ai.fc.Arguments) == 1 {
					_, isStar = ai.fc.Arguments[0].(*sulpherast.CountStar)
				}
				if isStar || argVal != nil {
					st.count++
				}
			case "COLLECT":
				if argVal != nil {
					st.collected = append(st.collected, argVal)
				}
			case "SUM":
				if f, ok := qs.ToFloatSafe(argVal); ok {
					st.sum += f
					st.sumOK = true
				}
			case "AVG":
				if f, ok := qs.ToFloatSafe(argVal); ok {
					st.sum += f
					st.sumOK = true
					st.count++
				}
			case "MIN":
				if argVal != nil && (st.min == nil || qs.CompareValues(argVal, st.min) < 0) {
					st.min = argVal
				}
			case "MAX":
				if argVal != nil && (st.max == nil || qs.CompareValues(argVal, st.max) > 0) {
					st.max = argVal
				}
			}
		}
	}

	// Build result rows.
	var results []map[string]interface{}
	for _, gk := range groupOrder {
		entry := groups[gk]
		row := make(map[string]interface{}, len(groupKeys)+len(aggItems))

		for i, gkDef := range groupKeys {
			row[gkDef.key] = entry.keyVals[i]
		}

		for i, ai := range aggItems {
			fn := strings.ToUpper(ai.fc.Name.String())
			st := entry.states[i]
			switch fn {
			case "COUNT":
				row[ai.key] = st.count
			case "COLLECT":
				if st.collected == nil {
					row[ai.key] = []interface{}{}
				} else {
					row[ai.key] = st.collected
				}
			case "SUM":
				if st.sumOK {
					row[ai.key] = st.sum
				} else {
					row[ai.key] = nil
				}
			case "AVG":
				if st.count > 0 {
					row[ai.key] = st.sum / float64(st.count)
				} else {
					row[ai.key] = nil
				}
			case "MIN":
				row[ai.key] = st.min
			case "MAX":
				row[ai.key] = st.max
			}
		}

		results = append(results, row)
	}

	return results
}

// ── RETURN projection (Env) ──────────────────────────────────────────────────

func (e *Executor) projectEnvs(ctx context.Context, envs []Env, ret *sulpherast.ReturnClause, snapshot *graphSnapshot) []map[string]interface{} {
	// If any RETURN item is an aggregate function, use the GROUP BY engine.
	if hasAggregation(ret) {
		return e.applyAggregation(ctx, envs, ret, snapshot)
	}
	var results []map[string]interface{}
	for _, env := range envs {
		row := e.projectEnv(ctx, env, ret, snapshot)
		results = append(results, row)
	}
	return results
}

func (e *Executor) projectEnv(ctx context.Context, env Env, ret *sulpherast.ReturnClause, snapshot *graphSnapshot) map[string]interface{} {
	row := make(map[string]interface{})

	for _, item := range ret.Items {
		if item.Star {
			// RETURN *: project all bound node and edge variables.
			for varName, val := range env {
				if strings.HasPrefix(varName, "_") {
					continue // internal key
				}
				if m, ok := val.(map[string]interface{}); ok {
					if isNodeEnv(m) {
						e.hydrateNodeData(ctx, m["_nodeID"].(string), m, snapshot.hydrated)
						row[varName] = e.nodeMapForReturn(m)
					} else if isEdgeEnv(m) {
						row[varName] = edgeMapForReturn(m)
					}
				}
			}
			continue
		}

		key := exprToKey(item.Expr)
		if item.Alias != nil {
			key = item.Alias.Value
		}

		val := e.evalEnvForReturn(ctx, item.Expr, env, snapshot)
		row[key] = val
	}

	return row
}

// evalEnvForReturn evaluates a RETURN expression with full node hydration.
// For bare variable references, returns the full node map with _id.
// For property access, returns the property value.
// For all other expressions, delegates to evalEnv.
func (e *Executor) evalEnvForReturn(ctx context.Context, expr sulpherast.Expression, env Env, snapshot *graphSnapshot) interface{} {
	switch ex := expr.(type) {
	case *sulpherast.Identifier:
		val, ok := env[ex.Value]
		if !ok {
			return nil
		}
		if m, ok := val.(map[string]interface{}); ok {
			if isNodeEnv(m) {
				nodeID := m["_nodeID"].(string)
				e.hydrateNodeData(ctx, nodeID, m, snapshot.hydrated)
				return e.nodeMapForReturn(m)
			}
			if isEdgeEnv(m) {
				return edgeMapForReturn(m)
			}
		}
		// Path object, list, scalar — return as-is.
		return val

	case *sulpherast.PropertyAccess:
		obj := e.evalEnv(ctx, ex.Object, env, snapshot)
		if m, ok := obj.(map[string]interface{}); ok {
			prop := ex.Property.Value
			if nodeID, hasID := m["_nodeID"].(string); hasID {
				if prop == "id" {
					bare := nodeID
					if e.tenantPrefix != "" {
						bare = strings.TrimPrefix(bare, e.tenantPrefix)
					}
					parts := strings.SplitN(bare, ":", 2)
					if len(parts) == 2 {
						return parts[1]
					}
					return nil
				}
				if _, present := m[prop]; !present {
					e.hydrateNodeData(ctx, nodeID, m, snapshot.hydrated)
				}
			}
			return m[prop]
		}
		return nil
	}

	return e.evalEnv(ctx, expr, env, snapshot)
}

// nodeMapForReturn builds the public node map from the internal node env.
// Adds _id and type from the _nodeID; strips the internal _nodeID key.
func (e *Executor) nodeMapForReturn(m map[string]interface{}) map[string]interface{} {
	nodeID, _ := m["_nodeID"].(string)
	result := make(map[string]interface{}, len(m))
	for k, v := range m {
		if k != "_nodeID" {
			result[k] = v
		}
	}
	bare := nodeID
	if e.tenantPrefix != "" {
		bare = strings.TrimPrefix(bare, e.tenantPrefix)
	}
	parts := strings.SplitN(bare, ":", 2)
	if len(parts) == 2 {
		result["_id"] = parts[1]
		if _, hasType := result["type"]; !hasType {
			result["type"] = parts[0]
		}
	}
	return result
}

// edgeMapForReturn builds the public edge map from an internal edge env.
// Strips internal keys (_rel, _from, _to, _edgeID) and exposes them as
// public fields (rel, from, to, edge_id). All merged property fields are
// included as-is.
func edgeMapForReturn(m map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(m))
	for k, v := range m {
		switch k {
		case "_rel":
			result["rel"] = v
		case "_from":
			result["from"] = v
		case "_to":
			result["to"] = v
		case "_edgeID":
			if id, ok := v.(int); ok && id != 0 {
				result["edge_id"] = id
			}
		default:
			result[k] = v
		}
	}
	return result
}

// tryConvergenceJoin detects and executes the convergence join topology:
//
//	(a)-[rel1*]->(m)<-[rel2*]-(b)
//
// The parser emits this as a single 3-segment path where:
//
//	segs[0]  node=a, rel=rel1 (HasRange, outgoing)
//	segs[1]  node=m, rel=rel2 (HasRange, incoming)
//	segs[2]  node=b, no rel
//
// Both variable-length segments must end at the same intermediate node m.
// Returns (envs, true, err) when the pattern is detected and executed.
// Returns (nil, false, nil) when the pattern is not detected; caller falls
// through to normal BFS dispatch.
func (e *Executor) tryConvergenceJoin(
	ctx context.Context,
	snapshot *graphSnapshot,
	segs []pathSegment,
	stats *QueryStats,
	maxDepth int,
) ([]Env, bool, error) {
	// Topology check: exactly 3 segments, first two rels are variable-length.
	if len(segs) != 3 {
		return nil, false, nil
	}
	rel1 := segs[0].rel
	rel2 := segs[1].rel
	if rel1 == nil || rel2 == nil {
		return nil, false, nil
	}
	if !rel1.HasRange || !rel2.HasRange {
		return nil, false, nil
	}

	// Middle variable must be named (it's the convergence point).
	midVar := astNodeVar(segs[1].node)
	if midVar == "" {
		return nil, false, nil
	}

	// The second rel must be incoming (←) so that b is the true end of
	// the second arm, not m. If both rels are outgoing, this is a linear
	// chain, not a convergence.
	dir2 := astRelDirection(rel2)
	if dir2 == RelOutgoing {
		return nil, false, nil
	}

	// ── Arm A: segs[0..1] — expand from a toward m ───────────────────────
	// Build a 2-segment path: (a)-[rel1*]->(m).
	armA := []pathSegment{segs[0], segs[1]}
	startA := e.findMatchingNodesAST(ctx, snapshot, segs[0], nil)
	armAEnvs := e.bfsEnvMulti(ctx, snapshot, startA, armA, stats, maxDepth)

	// ── Arm B: segs[1..2] reversed — expand from b toward m ─────────────
	// The second rel is incoming (b←m), which means b has an outgoing edge
	// toward m in the graph. To find all b nodes that can reach m, we run
	// BFS from b-type nodes following the *reversed* direction.
	// Construct a 2-segment arm: (b)-[rel2_reversed*]->(m).
	rel2Rev := *rel2
	// Flip direction: incoming becomes outgoing for the arm-B traversal.
	rel2Rev.Left = !rel2.Left
	rel2Rev.Right = !rel2.Right
	armB := []pathSegment{
		{node: segs[2].node, rel: segs[1].rel}, // start: b-type nodes, rel2
		{node: segs[1].node},                   // end:   m-type nodes
	}
	// Override the rel direction: for arm B we want outgoing (b→m).
	rel2RevCopy := rel2Rev
	armB[0].rel = &rel2RevCopy
	startB := e.findMatchingNodesAST(ctx, snapshot, segs[2], nil)
	armBEnvs := e.bfsEnvMulti(ctx, snapshot, startB, armB, stats, maxDepth)

	// ── Hash-join on midVar ───────────────────────────────────────────────
	indexA := make(map[string][]Env, len(armAEnvs))
	for _, env := range armAEnvs {
		if bound, ok := env[midVar]; ok {
			if m, isMap := bound.(map[string]interface{}); isMap {
				if key := nodeMapID(m); key != "" {
					indexA[key] = append(indexA[key], env)
				}
			}
		}
	}

	// Arm B binds the end-node to the variable named in segs[1].node (= midVar).
	// After BFS, that binding appears in the Env under midVar.
	var result []Env
	for _, env := range armBEnvs {
		if bound, ok := env[midVar]; ok {
			if m, isMap := bound.(map[string]interface{}); isMap {
				if key := nodeMapID(m); key != "" {
					if aEnvs, matched := indexA[key]; matched {
						for _, ea := range aEnvs {
							result = append(result, mergeEnvs(ea, env))
						}
					}
				}
			}
		}
	}

	return result, true, nil
}

// nodeMapID extracts a stable string key from a node's projected Env map.
// The map produced by envBindWithSnapshot contains:
//
//	"id"     → "entity:123"  (full node ID string — preferred key)
//	"_id"    → "123"          (entity ID fragment)
//	"_nodeID"→ "entity:123"  (raw graph node ID, present before hydration)
//
// We prefer "id" as it is stable across all code paths.
func nodeMapID(m map[string]interface{}) string {
	if v, ok := m["id"].(string); ok && v != "" {
		return v
	}
	if v, ok := m["_nodeID"].(string); ok && v != "" {
		return v
	}
	if v, ok := m["_id"].(string); ok && v != "" {
		return v
	}
	return ""
}

// sortedNeighbors returns the neighbours from getNeighborsByDirection in
// lexicographic order of node ID. This makes BFS expansion order deterministic
// across runs (Go map iteration is randomised), which in turn makes query
// results order-stable when no ORDER BY is specified.
func (e *Executor) sortedNeighbors(snapshot *graphSnapshot, node string, dir RelDirection) []neighborItem {
	m := e.getNeighborsByDirection(snapshot, node, dir)
	items := make([]neighborItem, 0, len(m))
	for nodeID, ref := range m {
		items = append(items, neighborItem{nodeID: nodeID, ref: ref})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].nodeID < items[j].nodeID })
	return items
}

type neighborItem struct {
	nodeID string
	ref    graph.EdgeRef
}

// mergeEnvs produces a new Env containing all bindings from both a and b.
// When the same variable appears in both, b's binding takes precedence.
func mergeEnvs(a, b Env) Env {
	merged := make(Env, len(a)+len(b))
	for k, v := range a {
		merged[k] = v
	}
	for k, v := range b {
		merged[k] = v
	}
	return merged
}

// ── ORDER BY (Env) ────────────────────────────────────────────────────────────

func (e *Executor) applyOrderByEnv(results []map[string]interface{}, orderBy []*sulpherast.SortItem) []map[string]interface{} {
	if len(results) == 0 || len(orderBy) == 0 {
		return results
	}
	sort.SliceStable(results, func(i, j int) bool {
		for _, si := range orderBy {
			key := exprToKey(si.Expr)
			vi := qs.GetNestedValue(results[i], key)
			vj := qs.GetNestedValue(results[j], key)
			cmp := qs.CompareValues(vi, vj)
			if cmp == 0 {
				continue
			}
			if si.Descending {
				return cmp > 0
			}
			return cmp < 0
		}
		return false
	})
	return results
}

// ── OPTIONAL MATCH (Env) ──────────────────────────────────────────────────────

func (e *Executor) applyOptionalMatchEnv(ctx context.Context, envs []Env, optional *sulpherast.MatchClause, snapshot *graphSnapshot, maxDepth int) []Env {
	optSegs, err := extractPathElements(optional.Pattern)
	if err != nil || len(optSegs) == 0 {
		return envs
	}

	firstOptVar := astNodeVar(optSegs[0].node)
	var result []Env
	stats := &QueryStats{}

	for _, env := range envs {
		// Find start nodes for the optional pattern.
		var startNodes []string
		if firstOptVar != "" {
			if v, ok := env[firstOptVar]; ok {
				if m, ok := v.(map[string]interface{}); ok {
					if nodeID, ok := m["_nodeID"].(string); ok {
						startNodes = []string{nodeID}
					}
				}
			}
		}
		if len(startNodes) == 0 {
			startNodes = e.findMatchingNodesAST(ctx, snapshot, optSegs[0], nil)
		}

		var optEnvs []Env
		for _, startNode := range startNodes {
			rows := e.bfsEnv(ctx, snapshot, startNode, optSegs, stats, maxDepth)
			optEnvs = append(optEnvs, rows...)
		}

		if optional.Where != nil {
			optEnvs = e.applyWhereEnv(ctx, optEnvs, optional.Where, snapshot)
		}

		if len(optEnvs) == 0 {
			// No match: emit mandatory env with nil for optional variables.
			nilEnv := make(Env, len(env))
			for k, v := range env {
				nilEnv[k] = v
			}
			for _, seg := range optSegs {
				if v := astNodeVar(seg.node); v != "" {
					if _, already := nilEnv[v]; !already {
						nilEnv[v] = nil
					}
				}
			}
			result = append(result, nilEnv)
		} else {
			// Cross-join: merge optional env into mandatory env.
			for _, optEnv := range optEnvs {
				merged := make(Env, len(env)+len(optEnv))
				for k, v := range env {
					merged[k] = v
				}
				for k, v := range optEnv {
					if _, already := merged[k]; !already {
						merged[k] = v
					}
				}
				result = append(result, merged)
			}
		}
	}

	return result
}

// ── UNION / UNION ALL ─────────────────────────────────────────────────────────

// executeUnionEnv executes a UNION or UNION ALL query.
// Each SingleQuery part is executed independently; results are merged.
// UNION deduplicates; UNION ALL does not.
func (e *Executor) executeUnionEnv(ctx context.Context, q *sulpherast.Query, maxDepth int, hint *AlgorithmHint) (*QueryResult, error) {
	var allResults []map[string]interface{}
	allDistinct := true // true unless any connector is UNION ALL

	for i, part := range q.Parts {
		// Execute this part as a standalone single-query.
		singleQ := &sulpherast.Query{Parts: []*sulpherast.SingleQuery{part}}
		result, err := e.executeTraversalEnv(ctx, singleQ, maxDepth, hint)
		if err != nil {
			return nil, fmt.Errorf("UNION part %d: %w", i+1, err)
		}
		allResults = append(allResults, result.Data...)

		// Check if the connector to the next part is UNION ALL.
		if i < len(q.Union) && q.Union[i].All {
			allDistinct = false
		}
	}

	if allDistinct {
		allResults = qs.ApplyDistinct(allResults)
	}

	return &QueryResult{
		Data:  allResults,
		Stats: QueryStats{PathsFound: len(allResults)},
	}, nil
}

// ── UNWIND ───────────────────────────────────────────────────────────────────

// executeUnwindEnv handles UNWIND list AS x RETURN ... (no MATCH clause).
func (e *Executor) executeUnwindEnv(ctx context.Context, cs *clauseSet, maxDepth int) (*QueryResult, error) {
	startTime := time.Now()

	e.mu.RLock()
	snapshot := e.takeSnapshot()
	e.mu.RUnlock()

	// Evaluate the UNWIND expression in an empty Env.
	listVal := e.evalEnv(ctx, cs.unwind.Expr, Env{}, snapshot)
	list, ok := toInterfaceSlice(listVal)
	if !ok {
		list = []interface{}{listVal} // scalar unwraps to single-item list
	}

	varName := cs.unwind.Alias.Value
	var envs []Env
	for _, item := range list {
		envs = append(envs, Env{varName: item})
	}

	e.preHydrateEnvs(ctx, envs, snapshot)
	results := e.projectEnvs(ctx, envs, cs.ret, snapshot)
	if cs.ret.Distinct {
		results = qs.ApplyDistinct(results)
	}
	if len(cs.ret.OrderBy) > 0 {
		results = e.applyOrderByEnv(results, cs.ret.OrderBy)
	}
	if skip := evalIntExpr(cs.ret.Skip); skip > 0 {
		if skip >= len(results) {
			results = nil
		} else {
			results = results[skip:]
		}
	}
	if limit := evalIntExpr(cs.ret.Limit); limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	return &QueryResult{
		Data:  results,
		Stats: QueryStats{PathsFound: len(results), ExecutionTime: time.Since(startTime)},
	}, nil
}

// executeUnwindMatchEnv handles UNWIND list AS x MATCH (x)-[:R]->(b) RETURN b.
// For each item in the UNWIND list, the item is bound to the UNWIND variable
// and used to seed or constrain the subsequent MATCH.
func (e *Executor) executeUnwindMatchEnv(ctx context.Context, cs *clauseSet, maxDepth int, hint *AlgorithmHint) (*QueryResult, error) {
	startTime := time.Now()
	stats := QueryStats{}

	e.mu.RLock()
	snapshot := e.takeSnapshot()
	e.mu.RUnlock()

	listVal := e.evalEnv(ctx, cs.unwind.Expr, Env{}, snapshot)
	list, ok := toInterfaceSlice(listVal)
	if !ok {
		list = []interface{}{listVal}
	}

	unwindVar := cs.unwind.Alias.Value

	segs, err := extractPathElements(cs.match.Pattern)
	if err != nil {
		return nil, fmt.Errorf("UNWIND MATCH pattern: %w", err)
	}

	alg := AlgBFS
	if hint != nil && hint.Algorithm == DFS {
		alg = AlgDFS
	}

	var allEnvs []Env
	for _, item := range list {
		seedEnv := Env{unwindVar: item}

		// If the first pattern node's variable matches the UNWIND variable,
		// use the item's node ID as the start node directly.
		firstVar := astNodeVar(segs[0].node)
		var startNodes []string
		if firstVar == unwindVar {
			if m, ok := item.(map[string]interface{}); ok {
				if nodeID, ok := m["_nodeID"].(string); ok {
					startNodes = []string{nodeID}
				}
			}
		}
		if len(startNodes) == 0 {
			startNodes = e.findMatchingNodesAST(ctx, snapshot, segs[0], seedEnv)
		}

		for _, startNode := range startNodes {
			var rows []Env
			if alg == AlgDFS {
				rows = e.dfsEnv(ctx, snapshot, startNode, segs, &stats, maxDepth)
			} else {
				rows = e.bfsEnv(ctx, snapshot, startNode, segs, &stats, maxDepth)
			}
			for _, row := range rows {
				// Merge UNWIND seed into each traversal result.
				merged := make(Env, len(seedEnv)+len(row))
				for k, v := range seedEnv {
					merged[k] = v
				}
				for k, v := range row {
					merged[k] = v
				}
				allEnvs = append(allEnvs, merged)
			}
		}
	}

	if cs.match.Where != nil {
		allEnvs = e.applyWhereEnv(ctx, allEnvs, cs.match.Where, snapshot)
	}

	stats.PathsFound = len(allEnvs)
	e.preHydrateEnvs(ctx, allEnvs, snapshot)
	results := e.projectEnvs(ctx, allEnvs, cs.ret, snapshot)
	if cs.ret.Distinct {
		results = qs.ApplyDistinct(results)
	}
	if len(cs.ret.OrderBy) > 0 {
		results = e.applyOrderByEnv(results, cs.ret.OrderBy)
	}
	if skip := evalIntExpr(cs.ret.Skip); skip > 0 {
		if skip >= len(results) {
			results = nil
		} else {
			results = results[skip:]
		}
	}
	if limit := evalIntExpr(cs.ret.Limit); limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	stats.ExecutionTime = time.Since(startTime)
	return &QueryResult{Data: results, Stats: stats}, nil
}

// ── WITH pipeline (Env) ───────────────────────────────────────────────────────

func (e *Executor) executeWithEnv(ctx context.Context, cs *clauseSet, firstEnvs []Env, firstSegs []pathSegment, snapshot *graphSnapshot, maxDepth int) (*QueryResult, error) {
	startTime := time.Now()
	stats := QueryStats{}

	// Pipeline: iterate each withStage in order. Each stage projects the current
	// envs through its WITH clause, then optionally traverses a following MATCH
	// to produce the envs for the next stage.
	curEnvs := firstEnvs

	for _, stage := range cs.withStages {
		// 1. Project WITH items (handles aggregation internally via projectWithEnv).
		curEnvs = e.projectWithEnv(ctx, curEnvs, stage.with, snapshot)

		// 3. Apply WITH WHERE filter.
		if stage.with.Where != nil {
			curEnvs = e.applyWhereEnv(ctx, curEnvs, stage.with.Where, snapshot)
		}

		// 4. DISTINCT / SKIP / LIMIT on WITH.
		if stage.with.Distinct {
			curEnvs = applyDistinctEnvs(curEnvs)
		}
		if skip := evalIntExpr(stage.with.Skip); skip > 0 {
			if skip >= len(curEnvs) {
				curEnvs = nil
			} else {
				curEnvs = curEnvs[skip:]
			}
		}
		if limit := evalIntExpr(stage.with.Limit); limit > 0 && len(curEnvs) > limit {
			curEnvs = curEnvs[:limit]
		}

		// 5. Post-WITH MATCH — traverse and merge with current envs.
		if stage.match != nil {
			allParts, err := extractAllPathParts(stage.match.Pattern)
			if err != nil || len(allParts) == 0 || len(allParts[0]) == 0 {
				return nil, fmt.Errorf("WITH stage MATCH pattern: %w", err)
			}
			segs := allParts[0]
			var nextEnvs []Env
			for _, withEnv := range curEnvs {
				startVar := astNodeVar(segs[0].node)
				var startNodes []string
				if startVar != "" {
					if v, ok := withEnv[startVar]; ok {
						if m, ok := v.(map[string]interface{}); ok {
							if nodeID, ok := m["_nodeID"].(string); ok {
								startNodes = []string{nodeID}
							}
						}
					}
				}
				if len(startNodes) == 0 {
					startNodes = e.findMatchingNodesAST(ctx, snapshot, segs[0], nil)
				}
				for _, startNode := range startNodes {
					rows := e.bfsEnv(ctx, snapshot, startNode, segs, &stats, maxDepth)
					for _, row := range rows {
						merged := make(Env, len(withEnv)+len(row))
						for k, v := range withEnv {
							merged[k] = v
						}
						for k, v := range row {
							merged[k] = v
						}
						nextEnvs = append(nextEnvs, merged)
					}
				}
			}
			if stage.match.Where != nil {
				nextEnvs = e.applyWhereEnv(ctx, nextEnvs, stage.match.Where, snapshot)
			}
			curEnvs = nextEnvs
		}
	}

	stats.PathsFound = len(curEnvs)

	e.preHydrateEnvs(ctx, curEnvs, snapshot)
	results := e.projectEnvs(ctx, curEnvs, cs.ret, snapshot)
	if cs.ret.Distinct {
		results = qs.ApplyDistinct(results)
	}
	if len(cs.ret.OrderBy) > 0 {
		results = e.applyOrderByEnv(results, cs.ret.OrderBy)
	}
	if skip := evalIntExpr(cs.ret.Skip); skip > 0 {
		if skip >= len(results) {
			results = nil
		} else {
			results = results[skip:]
		}
	}
	if limit := evalIntExpr(cs.ret.Limit); limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	stats.ExecutionTime = time.Since(startTime)
	return &QueryResult{Data: results, Stats: stats}, nil
}

// hasAggregationWith reports whether any WITH item is an aggregate expression.
// hasAggregationWith reports whether any WITH item is an aggregate expression.
func hasAggregationWith(with *sulpherast.WithClause) bool {
	for _, item := range with.Items {
		if isAggregateExpr(item.Expr) {
			return true
		}
	}
	return false
}

// applyAggregationWith applies Cypher implicit GROUP BY semantics to a WITH
// clause. Non-aggregate items are grouping keys; aggregate items are computed
// per group. Returns one Env per unique combination of grouping key values.
// Node-env values (carrying _nodeID) are preserved in the result so the
// subsequent MATCH phase can anchor from them.
func (e *Executor) applyAggregationWith(ctx context.Context, envs []Env, with *sulpherast.WithClause, snapshot *graphSnapshot) []Env {
	type aggState struct {
		count             int
		collected         []interface{}
		sum               float64
		sumOK             bool
		min               interface{}
		max               interface{}
		countDistinctSeen map[interface{}]bool
	}
	type aggItem struct {
		key string
		fc  *sulpherast.FunctionCall
	}
	type groupKeyDef struct {
		key      string
		expr     sulpherast.Expression
		rawIdent string // non-empty when expr is a bare identifier
	}

	var groupKeys []groupKeyDef
	var aggItems []aggItem

	for _, item := range with.Items {
		key := exprToKey(item.Expr)
		if item.Alias != nil {
			key = item.Alias.Value
		}
		if isAggregateExpr(item.Expr) {
			aggItems = append(aggItems, aggItem{key: key, fc: item.Expr.(*sulpherast.FunctionCall)})
		} else {
			rawIdent := ""
			if ident, ok := item.Expr.(*sulpherast.Identifier); ok {
				rawIdent = ident.Value
			}
			groupKeys = append(groupKeys, groupKeyDef{key, item.Expr, rawIdent})
		}
	}

	type groupEntry struct {
		// scalarVals holds evaluated scalar representations for the group key
		// (used for grouping identity via fmt.Sprintf).
		scalarVals []interface{}
		// rawVals holds the raw Env values, preserving node envs (_nodeID
		// intact) so subsequent MATCH phases can anchor from them.
		rawVals []interface{}
		states  []aggState
	}

	groups := make(map[string]*groupEntry)
	var groupOrder []string

	for _, env := range envs {
		scalarVals := make([]interface{}, len(groupKeys))
		rawVals := make([]interface{}, len(groupKeys))
		for i, gk := range groupKeys {
			scalarVals[i] = e.evalEnvForReturn(ctx, gk.expr, env, snapshot)
			if gk.rawIdent != "" {
				rawVals[i] = env[gk.rawIdent]
			} else {
				rawVals[i] = scalarVals[i]
			}
		}
		groupKey := fmt.Sprintf("%v", scalarVals)

		entry, exists := groups[groupKey]
		if !exists {
			states := make([]aggState, len(aggItems))
			for i := range states {
				states[i].countDistinctSeen = make(map[interface{}]bool)
			}
			entry = &groupEntry{scalarVals: scalarVals, rawVals: rawVals, states: states}
			groups[groupKey] = entry
			groupOrder = append(groupOrder, groupKey)
		}

		for i, ai := range aggItems {
			fn := strings.ToUpper(ai.fc.Name.String())
			st := &entry.states[i]
			var argVal interface{}
			if len(ai.fc.Arguments) > 0 {
				argVal = e.evalEnvForReturn(ctx, ai.fc.Arguments[0], env, snapshot)
			}
			if ai.fc.Distinct {
				dk := fmt.Sprintf("%v", argVal)
				if st.countDistinctSeen[dk] {
					continue
				}
				st.countDistinctSeen[dk] = true
			}
			switch fn {
			case "COUNT":
				isStar := len(ai.fc.Arguments) == 0
				if !isStar && len(ai.fc.Arguments) == 1 {
					_, isStar = ai.fc.Arguments[0].(*sulpherast.CountStar)
				}
				if isStar || argVal != nil {
					st.count++
				}
			case "COLLECT":
				if argVal != nil {
					st.collected = append(st.collected, argVal)
				}
			case "SUM":
				if f, ok := qs.ToFloatSafe(argVal); ok {
					st.sum += f
					st.sumOK = true
				}
			case "AVG":
				if f, ok := qs.ToFloatSafe(argVal); ok {
					st.sum += f
					st.sumOK = true
					st.count++
				}
			case "MIN":
				if argVal != nil && (st.min == nil || qs.CompareValues(argVal, st.min) < 0) {
					st.min = argVal
				}
			case "MAX":
				if argVal != nil && (st.max == nil || qs.CompareValues(argVal, st.max) > 0) {
					st.max = argVal
				}
			}
		}
	}

	var result []Env
	for _, gk := range groupOrder {
		entry := groups[gk]
		env := make(Env, len(groupKeys)+len(aggItems))
		for i, gkDef := range groupKeys {
			env[gkDef.key] = entry.rawVals[i]
		}
		for i, ai := range aggItems {
			fn := strings.ToUpper(ai.fc.Name.String())
			st := entry.states[i]
			switch fn {
			case "COUNT":
				env[ai.key] = st.count
			case "COLLECT":
				if st.collected == nil {
					env[ai.key] = []interface{}{}
				} else {
					env[ai.key] = st.collected
				}
			case "SUM":
				if st.sumOK {
					env[ai.key] = st.sum
				} else {
					env[ai.key] = nil
				}
			case "AVG":
				if st.count > 0 {
					env[ai.key] = st.sum / float64(st.count)
				} else {
					env[ai.key] = nil
				}
			case "MIN":
				env[ai.key] = st.min
			case "MAX":
				env[ai.key] = st.max
			}
		}
		result = append(result, env)
	}
	return result
}

// projectWithEnv projects the WITH clause items from a set of Envs.
// Node variables preserve their _nodeID so subsequent MATCH phases can use them
// as start node anchors. Non-node values (scalars, lists) are projected as-is.
// When any WITH item is an aggregate function, routes through applyAggregationWith.
func (e *Executor) projectWithEnv(ctx context.Context, envs []Env, with *sulpherast.WithClause, snapshot *graphSnapshot) []Env {
	if hasAggregationWith(with) {
		return e.applyAggregationWith(ctx, envs, with, snapshot)
	}
	var result []Env
	for _, env := range envs {
		projected := make(Env, len(with.Items))
		for _, item := range with.Items {
			key := exprToKey(item.Expr)
			if item.Alias != nil {
				key = item.Alias.Value
			}
			// For bare variable references to nodes, preserve the internal
			// node env (with _nodeID) so subsequent MATCH can anchor from it.
			if ident, ok := item.Expr.(*sulpherast.Identifier); ok {
				if v, exists := env[ident.Value]; exists {
					if m, ok := v.(map[string]interface{}); ok && isNodeEnv(m) {
						projected[key] = v // keep internal nodeEnv
						continue
					}
				}
			}
			projected[key] = e.evalEnvForReturn(ctx, item.Expr, env, snapshot)
		}
		result = append(result, projected)
	}
	return result
}

// applyDistinctEnvs removes duplicate Envs using string-coerced keys.
func applyDistinctEnvs(envs []Env) []Env {
	// Convert to []map[string]interface{} for qs.ApplyDistinct.
	rows := make([]map[string]interface{}, len(envs))
	for i, env := range envs {
		rows[i] = map[string]interface{}(env)
	}
	deduped := qs.ApplyDistinct(rows)
	result := make([]Env, len(deduped))
	for i, row := range deduped {
		result[i] = Env(row)
	}
	return result
}

// ── shortestPath (Env) ────────────────────────────────────────────────────────

func (e *Executor) executeShortestPathEnv(ctx context.Context, match *sulpherast.MatchClause, ret *sulpherast.ReturnClause, pathVar string, maxDepth int) (*QueryResult, error) {
	startTime := time.Now()
	stats := QueryStats{}

	e.mu.RLock()
	snapshot := e.takeSnapshot()
	e.mu.RUnlock()

	var shortPart *sulpherast.PatternPart
	for _, part := range match.Pattern.Parts {
		if part.Variable != nil && part.Variable.Value == pathVar {
			shortPart = part
			break
		}
	}
	if shortPart == nil {
		return nil, fmt.Errorf("shortestPath: pattern variable %q not found", pathVar)
	}

	fromPattern := shortPart.Elements[0].(*sulpherast.NodePattern)
	rp := shortPart.Elements[1].(*sulpherast.RelationshipPattern)
	toPattern := shortPart.Elements[2].(*sulpherast.NodePattern)

	graphDir := astToPathDir(astRelDirection(rp))
	relType := astRelType(rp)

	fromNodes := e.findMatchingNodesAST(ctx, snapshot, pathSegment{node: fromPattern}, nil)
	toNodes := e.findMatchingNodesAST(ctx, snapshot, pathSegment{node: toPattern}, nil)

	fromVar := astNodeVar(fromPattern)
	toVar := astNodeVar(toPattern)

	var envs []Env

	for _, fromID := range fromNodes {
		for _, toID := range toNodes {
			if fromID == toID {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("shortestPath cancelled: %w", err)
			}

			fromFull := e.fullNodeID(fromID)
			toFull := e.fullNodeID(toID)

			pathIDs, err := e.graph.FindPathDirected(fromFull, toFull, maxDepth, graphDir)
			if err != nil {
				continue
			}

			if relType != "" && !e.pathMatchesRelType(snapshot, pathIDs, relType) {
				continue
			}

			// Build the path environment.
			env := e.buildPathEnv(ctx, pathIDs, pathVar, fromVar, toVar, rp, snapshot)

			// Apply WHERE.
			if match.Where != nil {
				if e.evalEnv(ctx, match.Where, env, snapshot) != true {
					continue
				}
			}

			stats.PathsFound++
			stats.NodesTraversed += len(pathIDs)
			envs = append(envs, env)
		}
	}

	if ret.Distinct {
		envs = applyDistinctEnvs(envs)
	}

	e.preHydrateEnvs(ctx, envs, snapshot)
	results := e.projectEnvs(ctx, envs, ret, snapshot)
	if len(ret.OrderBy) > 0 {
		results = e.applyOrderByEnv(results, ret.OrderBy)
	}
	if skip := evalIntExpr(ret.Skip); skip > 0 {
		if skip >= len(results) {
			results = nil
		} else {
			results = results[skip:]
		}
	}
	if limit := evalIntExpr(ret.Limit); limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	stats.ExecutionTime = time.Since(startTime)
	return &QueryResult{Data: results, Stats: stats}, nil
}

// executeAllShortestPathsEnv handles MATCH p = allShortestPaths((a)-[*]-(b)).
// For each (from, to) pair it calls graph.AllShortestPaths and produces one
// Env per path found, then applies WHERE, projection, and RETURN modifiers.
func (e *Executor) executeAllShortestPathsEnv(ctx context.Context, match *sulpherast.MatchClause, ret *sulpherast.ReturnClause, pathVar string, maxDepth int) (*QueryResult, error) {
	startTime := time.Now()
	stats := QueryStats{}

	e.mu.RLock()
	snapshot := e.takeSnapshot()
	e.mu.RUnlock()

	var shortPart *sulpherast.PatternPart
	for _, part := range match.Pattern.Parts {
		if part.Variable != nil && part.Variable.Value == pathVar {
			shortPart = part
			break
		}
	}
	if shortPart == nil {
		return nil, fmt.Errorf("allShortestPaths: pattern variable %q not found", pathVar)
	}

	fromPattern := shortPart.Elements[0].(*sulpherast.NodePattern)
	rp := shortPart.Elements[1].(*sulpherast.RelationshipPattern)
	toPattern := shortPart.Elements[2].(*sulpherast.NodePattern)

	graphDir := astToPathDir(astRelDirection(rp))
	relType := astRelType(rp)

	fromNodes := e.findMatchingNodesAST(ctx, snapshot, pathSegment{node: fromPattern}, nil)
	toNodes := e.findMatchingNodesAST(ctx, snapshot, pathSegment{node: toPattern}, nil)

	fromVar := astNodeVar(fromPattern)
	toVar := astNodeVar(toPattern)

	var envs []Env

	for _, fromID := range fromNodes {
		for _, toID := range toNodes {
			if fromID == toID {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, fmt.Errorf("allShortestPaths cancelled: %w", err)
			}

			fromFull := e.fullNodeID(fromID)
			toFull := e.fullNodeID(toID)

			allPaths, err := e.graph.AllShortestPaths(fromFull, toFull, maxDepth, graphDir)
			if err != nil {
				continue
			}

			for _, pathIDs := range allPaths {
				if relType != "" && !e.pathMatchesRelType(snapshot, pathIDs, relType) {
					continue
				}

				env := e.buildPathEnv(ctx, pathIDs, pathVar, fromVar, toVar, rp, snapshot)

				if match.Where != nil {
					if e.evalEnv(ctx, match.Where, env, snapshot) != true {
						continue
					}
				}

				stats.PathsFound++
				stats.NodesTraversed += len(pathIDs)
				envs = append(envs, env)
			}
		}
	}

	if ret.Distinct {
		envs = applyDistinctEnvs(envs)
	}

	e.preHydrateEnvs(ctx, envs, snapshot)
	results := e.projectEnvs(ctx, envs, ret, snapshot)
	if len(ret.OrderBy) > 0 {
		results = e.applyOrderByEnv(results, ret.OrderBy)
	}
	if skip := evalIntExpr(ret.Skip); skip > 0 {
		if skip >= len(results) {
			results = nil
		} else {
			results = results[skip:]
		}
	}
	if limit := evalIntExpr(ret.Limit); limit > 0 && len(results) > limit {
		results = results[:limit]
	}

	stats.ExecutionTime = time.Since(startTime)
	return &QueryResult{Data: results, Stats: stats}, nil
}

// buildPathEnv builds the Env for a shortestPath result, binding the path
// variable and endpoint variables as first-class values.
func (e *Executor) buildPathEnv(ctx context.Context, pathIDs []string, pathVar, fromVar, toVar string, rp *sulpherast.RelationshipPattern, snapshot *graphSnapshot) Env {
	env := make(Env, 3)

	// Build path object.
	edgeLabels := e.buildPathEdgeLabels(snapshot, pathIDs)
	nodes := make([]interface{}, len(pathIDs))
	for i, fullID := range pathIDs {
		bare := fullID
		if e.tenantPrefix != "" {
			bare = strings.TrimPrefix(bare, e.tenantPrefix)
		}
		// Get or create the node map; always ensure _nodeID, _id, type are set.
		parts := strings.SplitN(bare, ":", 2)
		var nm map[string]interface{}
		if existing := snapshot.nodeData[bare]; existing != nil {
			nm = existing
		} else {
			nm = make(map[string]interface{})
			snapshot.nodeData[bare] = nm
		}
		nm["_nodeID"] = bare
		if len(parts) == 2 {
			nm["type"] = parts[0]
			nm["_id"] = parts[1]
		}
		nodes[i] = nm
	}

	rels := make([]interface{}, len(edgeLabels))
	for i, label := range edgeLabels {
		rels[i] = map[string]interface{}{"type": label}
	}

	env[pathVar] = pathEnvValue(nodes, rels)

	// Bind endpoint variables.
	if fromVar != "" && len(nodes) > 0 {
		env[fromVar] = nodes[0]
	}
	if toVar != "" && len(nodes) > 0 {
		env[toVar] = nodes[len(nodes)-1]
	}

	return env
}

// evalShortestPathEnvExpr handles RETURN shortestPath(...) expressions.
func (e *Executor) evalShortestPathEnvExpr(ctx context.Context, sp *sulpherast.ShortestPathExpression, env Env, snapshot *graphSnapshot) interface{} {
	if sp.Pattern == nil || len(sp.Pattern.Elements) != 3 {
		return nil
	}
	fromPat, ok1 := sp.Pattern.Elements[0].(*sulpherast.NodePattern)
	rp, ok2 := sp.Pattern.Elements[1].(*sulpherast.RelationshipPattern)
	toPat, ok3 := sp.Pattern.Elements[2].(*sulpherast.NodePattern)
	if !ok1 || !ok2 || !ok3 {
		return nil
	}

	fromVar := astNodeVar(fromPat)
	toVar := astNodeVar(toPat)

	var fromFull, toFull string
	if fromVar != "" {
		if v, ok := env[fromVar]; ok {
			if m, ok := v.(map[string]interface{}); ok {
				if nodeID, ok := m["_nodeID"].(string); ok {
					fromFull = e.fullNodeID(nodeID)
				}
			}
		}
	}
	if toVar != "" {
		if v, ok := env[toVar]; ok {
			if m, ok := v.(map[string]interface{}); ok {
				if nodeID, ok := m["_nodeID"].(string); ok {
					toFull = e.fullNodeID(nodeID)
				}
			}
		}
	}

	if fromFull == "" || toFull == "" || fromFull == toFull {
		return nil
	}

	graphDir := astToPathDir(astRelDirection(rp))
	pathIDs, err := e.graph.FindPathDirected(fromFull, toFull, e.maxDepth, graphDir)
	if err != nil {
		return nil
	}

	relType := astRelType(rp)
	if relType != "" && !e.pathMatchesRelType(snapshot, pathIDs, relType) {
		return nil
	}

	pathEnvResult := e.buildPathEnv(ctx, pathIDs, "_p", "", "", rp, snapshot)
	return pathEnvResult["_p"]
}

// ── graph.PathDirection sentinel ─────────────────────────────────────────────

// Ensure graph package is used.
var _ graph.PathDirection = graph.PathDirOutgoing
