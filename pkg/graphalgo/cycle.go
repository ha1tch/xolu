// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package graphalgo holds small, storage-agnostic graph algorithms
// shared across primitives that each maintain their own edge storage.
// T-120 (wave 10): extracted from pkg/graph's own FlatGraph.
// wouldCreateCycle so /obj's containment guard (obj-00-design.md §5)
// can reuse the identical, already-proven bounded-BFS-with-
// conservative-budget shape against its own transaction-scoped SQL
// rows, rather than against pkg/graph's in-memory g.nodes map, which
// (obj-00-design.md §10) is a derived, hydrated-from-storage mirror —
// using it to authorize a guard decision would violate the same
// guard-locality law cal's H1/H3 split and bal's rollup plane already
// exist to respect. Same proven shape, two different data sources,
// one small and deliberate refactor — not a rewrite, and not a
// second, independently-maintained copy either: pkg/graph's own
// wouldCreateCycle is retrofitted to call this too (cycle.go's own
// change, see pkg/graph/flat_graph.go), so there is exactly one
// implementation of this algorithm in the whole codebase, not two
// that can quietly diverge.
package graphalgo

// WouldCreateCycle reports whether adding an edge from→to would
// create a cycle, via bounded BFS from `to` looking for `from` — if
// `to` can already reach `from`, adding from→to closes the loop.
//
// Budget is measured by unique nodes visited (len(visited)), matching
// pkg/graph's own established metric (chosen there because bushy
// graphs with many parallel paths over-counted a raw dequeue-count
// variable, triggering conservative rejection earlier than intended —
// preserved here rather than re-derived). limit <= 0 means unbounded.
//
// Conservative on budget exhaustion: assumes a cycle exists rather
// than risk a false negative, exactly pkg/graph's own documented
// behaviour — easy to get wrong on a first attempt, already gotten
// right once, worth reusing the *shape* directly rather than
// re-deriving it (obj-00-design.md §5's own reasoning for why this
// extraction exists at all).
//
// neighbors returns every outbound-edge target for a given node — for
// an in-memory graph, a map lookup; for a SQL-backed transaction-
// scoped edge set (obj's own guard), a query. An error from neighbors
// aborts the search and propagates to the caller rather than being
// swallowed — a guard decision must never silently treat "the lookup
// failed" as "no neighbours, therefore no cycle."
func WouldCreateCycle(from, to string, limit int, neighbors func(node string) ([]string, error)) (bool, error) {
	if from == to {
		return true, nil
	}
	visited := make(map[string]struct{})
	queue := []string{to}
	head := 0
	for head < len(queue) {
		cur := queue[head]
		head++
		if cur == from {
			return true, nil
		}
		if limit > 0 && len(visited) >= limit {
			return true, nil // budget exhausted — conservatively assume cycle
		}
		if _, seen := visited[cur]; seen {
			continue
		}
		visited[cur] = struct{}{}
		neigh, err := neighbors(cur)
		if err != nil {
			return false, err
		}
		for _, n := range neigh {
			if _, seen := visited[n]; !seen {
				queue = append(queue, n)
			}
		}
	}
	return false, nil
}
