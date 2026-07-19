// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package sulpher

import "testing"

func TestCypherGate_RejectsSmuggled(t *testing.T) {
	p := NewParser()
	bad := []string{
		// backtick alias with quote breakout (D-011 shape)
		"MATCH (p:person)-[:WORKS_IN]->(d:dept) RETURN p.name AS `x\" , (SELECT 1) AS \"y`",
		// backtick label
		"MATCH (p:`evil` ) RETURN p.name",
		// backtick property key
		"MATCH (p:person) WHERE p.`a) DROP--` = 1 RETURN p.name",
	}
	for _, q := range bad {
		if _, _, err := p.Parse(q); err == nil {
			t.Errorf("gate FAILED to reject smuggled Cypher identifier: %s", q)
		}
	}
}

func TestCypherGate_AllowsLegitimate(t *testing.T) {
	p := NewParser()
	good := []string{
		"MATCH (p:person)-[:WORKS_IN]->(d:dept) RETURN p.name AS employee, d.name AS team",
		"MATCH (p:person) WHERE p.age > 30 RETURN p.name, p.age",
		"MATCH (p:person)-[:WORKS_IN]->(d:dept) RETURN p.name, p.age ORDER BY p.age DESC",
		"MATCH (p:person) RETURN count(*) AS c",
	}
	for _, q := range good {
		if _, _, err := p.Parse(q); err != nil {
			t.Errorf("gate wrongly rejected legitimate Cypher query %q: %v", q, err)
		}
	}
}
