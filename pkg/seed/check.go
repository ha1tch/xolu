// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package seed

// check.go -- reading an instance: checking it against a seed, and writing it out as one.

import (
	"fmt"
	"sort"
	"strings"
)

// ids returns the ids an entity type holds; ok is false when they cannot be listed.
func ids(c *Client, tenant, entity string) (list []int64, ok bool, err error) {
	if !nameRe.MatchString(entity) {
		return nil, false, nil
	}
	status, body, err := c.Request("POST", fullPath("/api/v1", "/oql/query", true, tenant), Req{Body: Obj("query", "SELECT id FROM "+entity)})
	if err != nil {
		return nil, false, err
	}
	obj, _ := body.(*Object)
	rows, isList := obj.Get("data").([]any)
	if status != 200 || !isList {
		return nil, status == 400 || status == 404, nil // a type with no rows does not exist yet
	}
	for _, r := range rows {
		if row, isRow := r.(*Object); isRow {
			if id, isInt := Int64(row.Get("id")); isInt {
				list = append(list, id)
			}
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i] < list[j] })
	return list, true, nil
}

// Difference is a record the instance holds with other values than the seed's.
type Difference struct {
	Record string
	Fields []string
}

// CheckResult is what comparing a plan with an instance found.
type CheckResult struct {
	Same      bool
	Missing   []string
	Different []Difference
	Extra     []string // records of the seed's entity types that the seed does not have
	Compared  int
	Unchecked int // records with no id, which cannot be read back
}

// JSON is the result as a value.
func (r *CheckResult) JSON() *Object {
	different := make([]any, len(r.Different))
	for i, d := range r.Different {
		different[i] = Obj("record", d.Record, "fields", stringList(d.Fields))
	}
	return Obj("same", r.Same, "missing", stringList(r.Missing), "different", different, "extra", stringList(r.Extra),
		"compared", int64(r.Compared), "unchecked", int64(r.Unchecked))
}

// Check compares what the plan would write with what the instance holds; nothing is written.
//
// It looks at entity records that have an id, and at those found by "match".
func Check(c *Client, plan []*Step) (*CheckResult, error) {
	res := &CheckResult{}
	type typeKey struct{ tenant, entity string }
	seen := map[typeKey]map[int64]bool{}
	idKey := func(s *Step) string { return s.Tenant + "\x00" + s.Entity + "\x00" + Text(s.ID) }
	later := map[string]*Object{} // fields a record gets in a second request, when its references form a cycle
	for _, step := range plan {
		if step.Op != "patch" {
			continue
		}
		if later[idKey(step)] == nil {
			later[idKey(step)] = NewObject()
		}
		if body, ok := step.Body.(*Object); ok {
			for _, k := range body.Keys() {
				later[idKey(step)].Set(k, body.Get(k))
			}
		}
	}
	for _, step := range plan {
		unbound := len(BindingsIn(step.Body)) == 0
		var status int
		var found any
		var label string
		var err error
		switch {
		case step.Op == "find":
			if unbound {
				id, ferr := Find(c, step, step.Body)
				if ferr != nil {
					return nil, ferr
				}
				if id == nil {
					res.Missing = append(res.Missing, fmt.Sprintf("%s (%s), which the seed expects to find", step.Entity, fieldList(step.Match, step.Body)))
				}
			}
			continue
		case step.Op == "match" && unbound:
			id, ferr := Find(c, step, step.Body)
			if ferr != nil {
				return nil, ferr
			}
			label = fmt.Sprintf("%s (%s)", step.Entity, fieldList(step.Match, step.Body))
			if id == nil {
				res.Missing = append(res.Missing, label)
				continue
			}
			status, found, err = c.Request("GET", step.ItemPath+"/"+Text(id), Req{Query: Obj("embed", "false")})
		case step.Op != "save":
			if step.Kind == "entity" && (step.Op == "create" || step.Op == "match") {
				res.Unchecked++
			}
			continue
		default:
			k := typeKey{step.Tenant, step.Entity}
			if seen[k] == nil {
				seen[k] = map[int64]bool{}
			}
			if id, ok := Int64(step.ID); ok {
				seen[k][id] = true
			}
			status, found, err = c.Request("GET", step.ReadPath, Req{Query: Obj("embed", "false")})
			label = step.Entity + "/" + Text(step.ID)
		}
		if err != nil {
			return nil, err
		}
		if status == 404 {
			res.Missing = append(res.Missing, label)
			continue
		}
		row, ok := found.(*Object)
		if status != 200 || !ok {
			return nil, errorf("cannot read %s: HTTP %d", label, status)
		}
		res.Compared++
		want := NewObject()
		if body, isObj := step.Body.(*Object); isObj {
			want = body.Copy()
		}
		for _, k := range later[idKey(step)].Keys() {
			want.Set(k, later[idKey(step)].Get(k))
		}
		names := map[string]bool{}
		for _, k := range want.Keys() {
			names[k] = true
		}
		for _, k := range row.Keys() {
			if !contains(SystemFields, k) {
				names[k] = true
			}
		}
		var fields []string
		for _, k := range sortedKeys(names) {
			if len(BindingsIn(want.Get(k))) > 0 {
				continue
			}
			w, h := want.Get(k), row.Get(k)
			if !want.Has(k) || !row.Has(k) || !Equal(w, h) || Kind(w) != Kind(h) {
				fields = append(fields, k)
			}
		}
		if len(fields) > 0 {
			res.Different = append(res.Different, Difference{label, fields})
		}
	}
	keys := make([]typeKey, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].tenant != keys[j].tenant {
			return keys[i].tenant < keys[j].tenant
		}
		return keys[i].entity < keys[j].entity
	})
	for _, k := range keys {
		have, ok, err := ids(c, k.tenant, k.entity)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, id := range have {
			if !seen[k][id] {
				res.Extra = append(res.Extra, fmt.Sprintf("%s/%d", k.entity, id))
			}
		}
	}
	res.Same = len(res.Missing) == 0 && len(res.Different) == 0 && len(res.Extra) == 0
	return res, nil
}

// DumpOptions are what a dump can be told.
type DumpOptions struct {
	Tenant      string
	Entities    []string // the entity types to write; all of them when empty
	Description string
}

// Dump returns what a tenant holds as the text of a version 2 .seed file with explicit ids, and how many records it has.
func Dump(c *Client, name string, opts DumpOptions) (string, int, error) {
	entities := opts.Entities
	if len(entities) == 0 {
		status, body, err := c.Request("GET", fullPath("/api/v1", "/entities", true, opts.Tenant), Req{})
		if err != nil {
			return "", 0, err
		}
		rows := body
		if obj, ok := body.(*Object); ok {
			rows = obj.Get("data")
			if obj.Has("entities") {
				rows = obj.Get("entities")
			}
		}
		list, ok := rows.([]any)
		if status != 200 || !ok {
			return "", 0, errorf("cannot list the entity types (GET /entities -> HTTP %d); name them with --entities", status)
		}
		for _, r := range list {
			if s, isStr := r.(string); isStr {
				entities = append(entities, s)
				continue
			}
			row, _ := r.(*Object)
			var name any
			for _, k := range []string{"entity_type", "name", "entity"} {
				if Truthy(row.Get(k)) {
					name = row.Get(k)
					break
				}
			}
			entities = append(entities, Text(name))
		}
		sort.Strings(entities)
	}
	header := Obj("seed", int64(2), "name", name)
	if opts.Description != "" {
		header.Set("description", opts.Description)
	}
	if opts.Tenant != "" {
		header.Set("tenant", opts.Tenant)
	}
	var kept []any
	for _, e := range entities {
		if e != "" && e != ProvenanceEntity {
			kept = append(kept, e)
		}
	}
	if kept == nil {
		kept = []any{}
	}
	header.Set("entities", kept)
	out := []string{Dumps(header)}
	total := 0
	for _, e := range kept {
		entity := e.(string)
		list, ok, err := ids(c, opts.Tenant, entity)
		if err != nil {
			return "", 0, err
		}
		if !ok {
			return "", 0, errorf("cannot list the ids of \"%s\"", entity)
		}
		if len(list) == 0 {
			continue
		}
		out = append(out, fmt.Sprintf("\n# %s: %d", entity, len(list)))
		for _, id := range list {
			status, body, err := c.Request("GET", fullPath("/api/v1", fmt.Sprintf("/%s/%d", Quote(entity), id), true, opts.Tenant), Req{Query: Obj("embed", "false")})
			if err != nil {
				return "", 0, err
			}
			row, isRow := body.(*Object)
			if status != 200 || !isRow {
				return "", 0, errorf("cannot read %s/%d: HTTP %d", entity, id, status)
			}
			data := NewObject()
			for _, k := range row.Keys() {
				if !contains(SystemFields, k) {
					data.Set(k, row.Get(k))
				}
			}
			out = append(out, WriteRecord(Obj("kind", "entity", "entity", entity, "id", id, "data", data), 0))
			total++
		}
	}
	return strings.Join(out, "\n") + "\n", total, nil
}
