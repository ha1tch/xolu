// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

import (
	"fmt"
	"strings"

	sulpher "github.com/ha1tch/sulpher"
	sulpherast "github.com/ha1tch/sulpher/ast"
)

// Algorithm represents the traversal algorithm hint.
// Kept for backward compatibility; the direct AST path defaults to BFS.
type Algorithm string

const (
	BFS Algorithm = "BFS"
	DFS Algorithm = "DFS"
)

// Parser parses Sulpher queries using the github.com/ha1tch/sulpher Cypher parser.
// Parse now returns *sulpherast.Query directly; the bridge layer is no longer used.
type Parser struct{}

// NewParser creates a new Sulpher parser.
func NewParser() *Parser {
	return &Parser{}
}

// Parse parses a Sulpher query string and returns the Cypher AST.
//
// Algorithm hints can be specified in two ways:
//
//  1. Preferred: a leading comment before the MATCH keyword:
//     // sulpher.algorithm: dfs
//     MATCH (u:User)-[:FOLLOWS]->(f) RETURN f
//
//  2. Legacy (deprecated): a bare keyword prefix:
//     DFS MATCH (u:User)-[:FOLLOWS]->(f) RETURN f
//
// The hint is case-insensitive. If absent, BFS is used.
func (p *Parser) Parse(query string) (*sulpherast.Query, *AlgorithmHint, error) {
	query = strings.TrimSpace(query)

	hint := &AlgorithmHint{Algorithm: BFS}

	// Preferred form: leading // sulpher.algorithm: <alg>
	// The comment may appear before the MATCH keyword on its own line or
	// inline. We scan the first line only.
	if strings.HasPrefix(query, "//") {
		firstLine := query
		if nl := strings.IndexByte(query, '\n'); nl != -1 {
			firstLine = query[:nl]
			query = strings.TrimSpace(query[nl+1:])
		} else {
			// Comment only — no query body; will fail validation below.
			query = ""
		}
		// Parse: // sulpher.algorithm: <value>
		comment := strings.TrimSpace(strings.TrimPrefix(firstLine, "//"))
		const prefix = "sulpher.algorithm:"
		if strings.HasPrefix(strings.ToLower(comment), prefix) {
			alg := strings.TrimSpace(comment[len(prefix):])
			switch strings.ToLower(alg) {
			case "dfs":
				hint.Algorithm = DFS
			case "bfs":
				hint.Algorithm = BFS
			default:
				return nil, nil, fmt.Errorf("unknown algorithm hint %q (expected bfs or dfs)", alg)
			}
		}
	} else {
		// Legacy form: BFS /DFS prefix (deprecated).
		upperQ := strings.ToUpper(query)
		if strings.HasPrefix(upperQ, "BFS ") {
			hint.Algorithm = BFS
			query = strings.TrimSpace(query[4:])
		} else if strings.HasPrefix(upperQ, "DFS ") {
			hint.Algorithm = DFS
			query = strings.TrimSpace(query[4:])
		}
	}

	ast, errs := sulpher.Parse(query)
	if len(errs) > 0 {
		return nil, nil, fmt.Errorf("parse error: %s", strings.Join(errs, "; "))
	}

	// Post-parse validation: the Cypher parser is permissive; enforce
	// Sulpher-specific constraints that the executor requires.
	if err := validateAST(ast, query); err != nil {
		return nil, nil, err
	}

	return ast, hint, nil
}

// AlgorithmHint carries the optional traversal algorithm preference extracted
// from a Sulpher query prefix. It is passed to the executor alongside the AST.
type AlgorithmHint struct {
	Algorithm Algorithm
}

// validateAST performs post-parse validation of the Cypher AST.
// Enforces executor-level structural constraints that are intentionally outside
// the scope of the general-purpose sulpher parser:
//   - Query must contain at least one MATCH or UNWIND clause.
//   - Query must contain a RETURN clause.
//
// Grammar-level constraints (negative hop count, min > max hop range, empty
// RETURN clause) are validated by the parser since sulpher v0.2.4 and no
// longer need to be checked here.
func validateAST(q *sulpherast.Query, raw string) error {
	if q == nil || len(q.Parts) == 0 {
		return fmt.Errorf("empty query")
	}
	sq := q.Parts[0]

	// Must have at least one MATCH or UNWIND, and one RETURN clause.
	// These are executor policy, not grammar constraints — the parser
	// deliberately accepts write-only and expression-only queries.
	var hasMatch, hasUnwind, hasReturn bool
	for _, c := range sq.Clauses {
		switch c.(type) {
		case *sulpherast.MatchClause:
			hasMatch = true
		case *sulpherast.UnwindClause:
			hasUnwind = true
		case *sulpherast.ReturnClause:
			hasReturn = true
		}
	}
	if !hasMatch && !hasUnwind {
		return fmt.Errorf("query must contain a MATCH or UNWIND clause")
	}
	if !hasReturn {
		return fmt.Errorf("query must contain a RETURN clause")
	}

	// Defence-in-depth: reject any backtick-escaped or delimiter-smuggled
	// identifier before the AST reaches the push-down SQL generator. Closes the
	// RETURN-alias injection class (and any unfound identifier sink) at the parser
	// boundary; the per-sink isSimpleIdent guard remains as a backstop.
	if err := checkCypherASTForSmuggledIdentifiers(q); err != nil {
		return err
	}

	return nil
}
