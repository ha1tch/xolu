// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package seed

// A load against a stand-in for the instance: enough of the entity routes to
// follow the guards, the load record and a second run. What a real xolu
// answers is the conformance suite's to check (scripts/seed_conformance).

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ha1tch/xolu/pkg/client"
)

type fakeXolu struct {
	rows  map[string]map[int64]*Object // entity -> id -> record
	next  int64
	calls []string
	heads []http.Header
}

var itemRe = regexp.MustCompile(`^/api/v1/([a-z_]+)(?:/(save/)?(\d+))?$`)

func (f *fakeXolu) RawWithHeaders(_ context.Context, method, path, _ string, header http.Header, body io.Reader) (*client.RawResult, error) {
	f.calls = append(f.calls, method+" "+path)
	f.heads = append(f.heads, header)
	answer := func(status int, v any) (*client.RawResult, error) {
		return &client.RawResult{StatusCode: status, Body: []byte(Dumps(v))}, nil
	}
	var sent *Object
	if body != nil {
		raw, _ := io.ReadAll(body)
		if v, err := ParseJSON(string(raw)); err == nil {
			sent, _ = v.(*Object)
		}
	}
	path, _, _ = strings.Cut(path, "?")
	if path == "/api/v1/tenant-summary" {
		return answer(200, Obj("empty", len(f.rows) == 0))
	}
	m := itemRe.FindStringSubmatch(path)
	if m == nil {
		return answer(404, Obj("error", "no such route"))
	}
	entity := m[1]
	id, _ := strconv.ParseInt(m[3], 10, 64)
	switch {
	case method == "GET" && m[3] == "":
		list := []any{}
		for i := int64(1); i <= f.next; i++ {
			if row, ok := f.rows[entity][i]; ok {
				list = append(list, row)
			}
		}
		return answer(200, Obj("data", list))
	case method == "GET":
		if row, ok := f.rows[entity][id]; ok {
			return answer(200, row)
		}
		return answer(404, Obj("error", "not found"))
	case method == "POST" && sent.Has("refuse"):
		return answer(422, Obj("error", "refused"))
	case method == "POST" || method == "PATCH":
		if m[3] == "" {
			f.next++
			id = f.next
		}
		if f.rows[entity] == nil {
			f.rows[entity] = map[int64]*Object{}
		}
		row := Obj("id", id)
		if old, ok := f.rows[entity][id]; ok && method == "PATCH" {
			row = old
		}
		for _, k := range sent.Keys() {
			row.Set(k, sent.Get(k))
		}
		f.rows[entity][id] = row
		if id > f.next {
			f.next = id
		}
		return answer(200, row)
	}
	return answer(405, Obj("error", "method"))
}

func loadText(t *testing.T, f *fakeXolu, text string, opts Options) (*Result, error) {
	t.Helper()
	path := writeSeed(t, t.TempDir(), "demo.seed", text)
	header, records, digest, err := ReadSeed(path, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := NewClientFor(f, "http://fake")
	loader, err := NewLoader(c, []LoadFile{{path, header, digest}}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := loader.Inspect(); err != nil {
		t.Fatal(err)
	}
	ctx := NewContext(loader.AsOf(time.Time{}), nil)
	plan, err := BuildPlan([]SeedFile{{path, header, records}}, ctx, PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return loader.Run(plan, ctx)
}

const demo = "{\"seed\": 2, \"name\": \"demo\"}\n" +
	"{\"kind\": \"entity\", \"entity\": \"assets\", \"id\": 1, \"data\": {\"tag\": \"A-1\", \"since\": {\"$date\": \"-1d\"}}}\n" +
	"{\"kind\": \"entity\", \"entity\": \"notes\", \"as\": \"n\", \"data\": {\"text\": \"ñ\"}}\n" +
	"{\"kind\": \"entity\", \"entity\": \"links\", \"data\": {\"note\": {\"$seed\": \"n\"}}}\n"

func count(res *Result, kind, outcome string) int64 {
	of, _ := res.Stats.Get(kind).(*Object)
	n, _ := Int64(of.Get(outcome))
	return n
}

func TestALoadIsRecordedAndContinued(t *testing.T) {
	f := &fakeXolu{rows: map[string]map[int64]*Object{}}
	res, err := loadText(t, f, demo, Options{})
	if err != nil || !res.OK || count(res, "entity", "created") != 3 || !res.Fresh {
		t.Fatalf("first load: %v, %+v", err, res)
	}
	var record *Object
	for _, row := range f.rows[ProvenanceEntity] {
		record = row
	}
	if s, _ := record.Str("status"); s != "loaded" || record.Get("loader") != LoaderName || record.Get("seed") != "demo" {
		t.Errorf("the load record: %s", Dumps(record))
	}
	done, _ := record.Get("done").(*Object)
	if done.Len() != 2 { // the two records with no id: what cannot be read back by id is remembered by key
		t.Errorf("done: %s", Dumps(done))
	}
	var link *Object
	for _, row := range f.rows["links"] {
		link = row
	}
	if !Equal(link.Get("note"), f.rows["notes"][2].Get("id")) {
		t.Errorf("the bound id was not used: %s", Dumps(link))
	}

	// the same file again: nothing is written twice, and the reference time is the first load's
	asOf, _ := record.Str("as_of")
	before := len(f.rows["notes"]) + len(f.rows["links"])
	res, err = loadText(t, f, demo, Options{})
	if err != nil || !res.OK || count(res, "entity", "present") != 3 || count(res, "entity", "created") != 0 || res.AsOf != asOf {
		t.Fatalf("second load: %v, %s", err, Dumps(res.JSON()))
	}
	if len(f.rows["notes"])+len(f.rows["links"]) != before || len(f.rows[ProvenanceEntity]) != 1 {
		t.Error("the second load wrote records again")
	}

	// with "replace" the record with an id is written again
	res, err = loadText(t, f, demo, Options{Mode: "replace"})
	if err != nil || count(res, "entity", "replaced") != 1 || count(res, "entity", "present") != 2 {
		t.Fatalf("replace: %v, %s", err, Dumps(res.JSON()))
	}
}

func TestATenantThatHoldsOtherDataIsRefused(t *testing.T) {
	f := &fakeXolu{rows: map[string]map[int64]*Object{"things": {1: Obj("id", int64(1))}}, next: 1}
	_, err := loadText(t, f, demo, Options{})
	if _, ok := err.(*Refused); !ok || !strings.Contains(err.Error(), "holds data that no seed load accounts for") {
		t.Fatalf("got %v", err)
	}
	for _, call := range f.calls {
		if !strings.HasPrefix(call, "GET ") {
			t.Errorf("a refused load made the request %s", call)
		}
	}
	if res, err := loadText(t, f, demo, Options{AllowExisting: true}); err != nil || !res.OK || res.Fresh {
		t.Errorf("allowed: %v, %+v", err, res)
	}
}

func TestAFailureStopsTheLoadAndUndoDeletesWhatItMade(t *testing.T) {
	bad := demo + "{\"kind\": \"entity\", \"entity\": \"notes\", \"data\": {\"refuse\": true}}\n"
	f := &fakeXolu{rows: map[string]map[int64]*Object{}}
	res, err := loadText(t, f, bad, Options{})
	if err != nil || res.OK || !res.Stopped || len(res.Failures) != 1 || !strings.Contains(res.Failures[0], "HTTP 422") {
		t.Fatalf("got %v, %s", err, Dumps(res.JSON()))
	}
	var record *Object
	for _, row := range f.rows[ProvenanceEntity] {
		record = row
	}
	if s, _ := record.Str("status"); s != "failed" {
		t.Errorf("the load record: %s", Dumps(record))
	}

	f = &fakeXolu{rows: map[string]map[int64]*Object{}}
	res, err = loadText(t, f, bad, Options{UndoOnFailure: true})
	if err != nil || res.Undone == nil {
		t.Fatalf("got %v, %+v", err, res)
	}
	deletes := 0
	for _, call := range f.calls {
		if strings.HasPrefix(call, "DELETE ") {
			deletes++
		}
	}
	if deletes != 3 { // the two records it created, and the load record
		t.Errorf("%d deletes: %v", deletes, f.calls)
	}
}

func TestARequestIsSentAsThePythonLoaderSendsIt(t *testing.T) {
	f := &fakeXolu{rows: map[string]map[int64]*Object{}}
	c := NewClientFor(f, "http://fake/")
	status, body, err := c.Request("POST", "/api/v1/notes", Req{Body: Obj("text", "ñ"), Headers: Obj("x-trace", int64(7)),
		Query: Obj("a b", []any{int64(1), "é"}, "embed", false)})
	if err != nil || status != 200 || Kind(body) != "object" || c.Requests != 1 {
		t.Fatalf("got %d, %v, %v", status, body, err)
	}
	if f.calls[0] != "POST /api/v1/notes?a+b=1&a+b=%C3%A9&embed=False" {
		t.Errorf("the request: %s", f.calls[0])
	}
	if got := f.heads[0].Get("X-Trace"); got != "7" {
		t.Errorf("the header: %q", got)
	}
	if _, _, err := c.Request("POST", "/api/v1/blob", Req{Raw: []byte{}, HasRaw: true}); err != nil {
		t.Fatal(err)
	}
	if values, named := f.heads[1]["Content-Type"]; !named || len(values) != 0 {
		t.Errorf("a raw body with no type must say so: %v", f.heads[1])
	}
	if _, err := NewClient("ftp://x", "", "", time.Second); err == nil {
		t.Error("ftp://x was taken for a base URL")
	}
}
