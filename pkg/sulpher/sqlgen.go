// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

// sqlgen.go — SQL push-down support for graph traversal queries.
//
// The SQL generation functions that accept *sulpherast.Query are in
// executor_ast.go (generateGraphSQLAST, planGraphQueryAST, etc.).
//
// This file contains only the shared infrastructure types and helpers
// that both the old and new paths used, kept here for clarity.

import (
	"fmt"
	"strings"
)

// graphPlan describes how a query will be executed.
type graphPlan int

const (
	planTraversal graphPlan = iota // in-memory BFS/DFS
	planPushDown                   // SQL JOIN chain via graphStore
)

// graphSQLResult holds the output of SQL generation.
type graphSQLResult struct {
	sql     string
	args    []interface{}
	aliases []string
}

// nodeInfo holds per-node SQL metadata for JOIN chain generation.
type nodeInfo struct {
	variable string // original variable name (e.g. "p")
	alias    string // SQL alias (e.g. "p" or "n0")
	entity   string // entity type (e.g. "person")
	table    string // adapted table name (e.g. "xolu_person")
}

// argBuilder manages the SQL argument list and placeholder emission.
type argBuilder struct {
	args    []interface{}
	dialect interface{ Placeholder(n int) string }
}

func (a *argBuilder) add(v interface{}) string {
	a.args = append(a.args, v)
	return a.dialect.Placeholder(len(a.args))
}

// operatorToSQL converts a Sulpher Operator to its SQL string.
func operatorToSQL(op Operator) (string, error) {
	switch op {
	case OpEq:
		return "=", nil
	case OpNe:
		return "<>", nil
	case OpLt:
		return "<", nil
	case OpGt:
		return ">", nil
	case OpLte:
		return "<=", nil
	case OpGte:
		return ">=", nil
	default:
		return "", fmt.Errorf("operator %q not supported in push-down", op)
	}
}

// tenantIDFromPrefix parses "XXXX@" → uint16. Empty prefix returns 0.
func tenantIDFromPrefix(prefix string) (uint16, error) {
	if prefix == "" {
		return 0, nil
	}
	hex := strings.TrimSuffix(prefix, "@")
	if len(hex) != 4 {
		return 0, fmt.Errorf("malformed tenant prefix %q", prefix)
	}
	var v uint64
	_, err := fmt.Sscanf(hex, "%04X", &v)
	if err != nil {
		return 0, fmt.Errorf("tenant prefix %q: %w", prefix, err)
	}
	return uint16(v), nil
}

// isSimpleIdent reports whether s is safe to use as a SQL alias.
func isSimpleIdent(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}
