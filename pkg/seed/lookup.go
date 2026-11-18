// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package seed

// lookup.go -- finding the record a "match" names: by fields, for an entity; in the list, for a definition.

import (
	"fmt"
	"strings"
)

const v2Hint = " (xolu answers 404 on every /api/v2 route when it runs without XOLU_API_V2_ENABLED=true)"

func literal(value any, at string) (string, error) {
	switch Kind(value) {
	case "int", "float":
		return Text(value), nil
	case "str":
		return "'" + strings.ReplaceAll(value.(string), "'", "''") + "'", nil
	}
	return "", errorf("%s: a record is matched by text or whole numbers; %s is neither", at, Repr(value))
}

func rowsOf(res any) []any {
	if obj, ok := res.(*Object); ok {
		for _, k := range obj.Keys() {
			if list, isList := obj.Get(k).([]any); isList {
				return list
			}
		}
		return nil
	}
	list, _ := res.([]any)
	return list
}

// Find returns the id of the record the step's "match" finds, or nil. body is the record with its bindings resolved.
func Find(c *Client, step *Step, body any) (any, error) {
	fields, _ := body.(*Object)
	if step.Kind == "entity" {
		conds := make([]string, len(step.Match))
		for i, f := range step.Match {
			lit, err := literal(fields.Get(f), step.Where)
			if err != nil {
				return nil, err
			}
			conds[i] = f + " = " + lit
		}
		query := fmt.Sprintf("SELECT id FROM %s WHERE %s ORDER BY id", step.Entity, strings.Join(conds, " AND "))
		status, res, err := c.Request("POST", step.QueryPath, Req{Body: Obj("query", query)})
		if err != nil {
			return nil, err
		}
		obj, ok := res.(*Object)
		if status != 200 || !ok {
			return nil, nil // an entity type with no rows does not exist yet
		}
		var rows []*Object
		if list, isList := obj.Get("data").([]any); isList {
			for _, r := range list {
				if row, isRow := r.(*Object); isRow {
					rows = append(rows, row)
				}
			}
		}
		if step.Op == "find" && len(rows) > 1 {
			var ids []string
			for i, r := range rows {
				if i < 5 {
					ids = append(ids, Text(r.Get("id")))
				}
			}
			return nil, errorf("%s: \"find\" on %s matches %d records (ids %s): it must name one", step.Where, step.Entity, len(rows), strings.Join(ids, ", "))
		}
		if len(rows) == 0 {
			return nil, nil
		}
		return rows[0].Get("id"), nil
	}
	status, res, err := c.Request("GET", step.ListPath, Req{})
	if err != nil {
		return nil, err
	}
	if status != 200 {
		hint := ""
		if status == 404 && strings.Contains(step.ListPath, "/api/v2/") {
			hint = v2Hint
		}
		return nil, errorf("%s: cannot list what exists to match against: GET %s -> HTTP %d%s", step.Where, step.ListPath, status, hint)
	}
	for _, r := range rowsOf(res) {
		row, ok := r.(*Object)
		if !ok {
			continue
		}
		all := true
		for _, f := range step.Match {
			all = all && Equal(row.Get(f), fields.Get(f))
		}
		if all {
			return row.Get("id"), nil
		}
	}
	return nil, nil
}
