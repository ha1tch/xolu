// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package seed

// The tests of scripts/seed_tests/test_seed_system.py, case for case.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testAsOf = time.Date(2026, 1, 31, 10, 0, 0, 0, time.UTC)

func writeSeed(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type planCase struct {
	variables map[string]any
	opts      PlanOptions
}

func planOf(t *testing.T, text string, c planCase) ([]*Step, error) {
	t.Helper()
	path := writeSeed(t, t.TempDir(), "t.seed", text)
	header, records, _, err := ReadSeed(path, false, nil)
	if err != nil {
		return nil, err
	}
	return BuildPlan([]SeedFile{{path, header, records}}, NewContext(testAsOf, c.variables), c.opts)
}

func mustPlan(t *testing.T, text string, c planCase) []*Step {
	t.Helper()
	plan, err := planOf(t, text, c)
	if err != nil {
		t.Fatalf("%s\n-> %v", text, err)
	}
	return plan
}

func refused(t *testing.T, text, says string, c planCase) {
	t.Helper()
	_, err := planOf(t, text, c)
	if _, ok := err.(*Error); !ok || !strings.Contains(err.Error(), says) {
		t.Errorf("%s\n-> %v, want an error that says %q", text, err, says)
	}
}

func shape(plan []*Step, of func(*Step) string) string {
	parts := make([]string, len(plan))
	for i, s := range plan {
		parts[i] = of(s)
	}
	return strings.Join(parts, " ")
}

// -- format --------------------------------------------------------------------

func TestOneRecordPerLineAndOverSeveral(t *testing.T) {
	path := writeSeed(t, t.TempDir(), "t.seed", "# a comment\n{\"seed\": 2,\n  \"name\": \"x\"\n  }\n\n{\"kind\": \"entity\", \"entity\": \"a\", \"id\": 1, \"data\": {\n\t\"n\": 1\n  }}\n{\"kind\": \"entity\", \"entity\": \"a\", \"id\": 2, \"data\": {}}\n")
	header, records, digest, err := ReadSeed(path, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if name, _ := header.Str("name"); name != "x" || len(records) != 2 || len(digest) != 64 {
		t.Fatalf("header %s, %d records, digest %s", Dumps(header), len(records), digest)
	}
	if Text(records[0].Get("id")) != "1" || Line(records[0]) != 6 || Line(records[1]) != 9 {
		t.Errorf("records at lines %d and %d", Line(records[0]), Line(records[1]))
	}
}

func TestByteOrderMarkAndCRLF(t *testing.T) {
	path := writeSeed(t, t.TempDir(), "t.seed", "\xef\xbb\xbf{\"seed\": 1}\r\n{\"kind\": \"entity\", \"entity\": \"a\", \"id\": 1, \"data\": {}}\r\n")
	if _, records, _, err := ReadSeed(path, false, nil); err != nil || len(records) != 1 {
		t.Errorf("%d records, %v", len(records), err)
	}
}

func TestWhatAFileIsRefusedFor(t *testing.T) {
	for _, c := range [][2]string{
		{"{\"kind\": \"entity\"}\n", "first record must be the header"},
		{"{\"seed\": 3}\n", "unsupported seed format version"},
		{"{\"seed\": true}\n", "unsupported seed format version"},
		{"{\"seed\": 2, \"tennant\": \"x\"}\n", `unknown header key "tennant"`},
		{"{\"seed\": 1, \"description\": \"x\"}\n", `declare "seed": 2`},
		{"{\"seed\": 1}\n{\"kind\": \"entity\", \"entity\": \"a\",\n  \"id\": 1, \"data\": {}}\n", `need "seed": 2`},
		{"{\"seed\": 2}\n{\"entity\": \"a\"}\n", `has no "kind"`},
		{"{\"seed\": 2}\n[1]\n", "must be a JSON object"},
		{"{\"seed\": 2}\n{\"kind\": \n", "invalid JSON"},
		{"  {\"seed\": 2}\n", "continuation line with no record"},
		{"{\"seed\": 2, \"entities\": \"a\"}\n", "must be a list of names"},
		{"", "empty seed file"},
		// a comment ends a record
		{"{\"seed\": 2}\n{\"kind\": \"entity\", \"entity\": \"a\", \"id\": 1, \"data\": {\n# no comments inside a record\n  \"n\": 1}}\n", "continuation line with no record"},
	} {
		_, _, _, err := ReadSeed(writeSeed(t, t.TempDir(), "t.seed", c[0]), false, nil)
		if _, ok := err.(*Error); !ok || !strings.Contains(err.Error(), c[1]) {
			t.Errorf("%q -> %v, want %q", c[0], err, c[1])
		}
	}
}

func TestLenientWarns(t *testing.T) {
	var seen []string
	_, _, _, err := ReadSeed(writeSeed(t, t.TempDir(), "t.seed", "{\"seed\": 2, \"tennant\": \"x\"}\n"), true, func(m string) { seen = append(seen, m) })
	if err != nil || len(seen) != 1 {
		t.Errorf("%d warnings, %v", len(seen), err)
	}
}

func TestWrittenRecordsReadBack(t *testing.T) {
	item := Obj("a", strings.Repeat("y", 70), "b", []any{int64(1), int64(2), int64(3)})
	rec := Obj("kind", "entity", "entity", "assets", "id", int64(1), "data", Obj("name", strings.Repeat("x", 90),
		"list", []any{item, item, item}, "due", Obj("$date", "+1d"), "none", nil))
	head := Obj("seed", int64(2), "name", "w", "description", strings.Repeat("d", 120), "entities", []any{"assets"})
	text := WriteRecord(head, 0) + "\n" + WriteRecord(rec, 0) + "\n"
	for _, line := range strings.Split(text, "\n") {
		if len(line) >= 400 {
			t.Errorf("a line of %d characters", len(line))
		}
	}
	header, records, _, err := ReadSeed(writeSeed(t, t.TempDir(), "t.seed", text), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := Dumps(visible(records[0])); got != Dumps(rec) {
		t.Errorf("read back\n %s\nwrote\n %s", got, Dumps(rec))
	}
	if Dumps(header.Get("entities")) != `["assets"]` {
		t.Errorf("entities %s", Dumps(header.Get("entities")))
	}
}

// -- placeholders --------------------------------------------------------------

func TestDatesAndTimes(t *testing.T) {
	ctx := NewContext(testAsOf, nil)
	for _, c := range [][3]string{
		{"$date", "", "2026-01-31"}, {"$date", "+1d", "2026-02-01"}, {"$date", "-1w", "2026-01-24"}, {"$date", "+1mo", "2026-02-28"},
		{"$date", "+13mo", "2027-02-28"}, {"$date", "-2y", "2024-01-31"}, {"$date", "+ 90 d", "2026-05-01"},
		{"$date", "-2dT08:30:00Z", "2026-01-29T08:30:00Z"}, {"$date", "T06:00:00Z", "2026-01-31T06:00:00Z"},
		{"$now", "", "2026-01-31T10:00:00Z"}, {"$now", "-2h", "2026-01-31T08:00:00Z"}, {"$now", "+30m", "2026-01-31T10:30:00Z"},
		{"$now", "-45s", "2026-01-31T09:59:15Z"}, {"$now", "+1d", "2026-02-01T10:00:00Z"}, {"$date", "-24mo", "2024-01-31"},
		{"$date", "-1mo", "2025-12-31"}, {"$date", "+11mo", "2026-12-31"},
	} {
		if got, err := ctx.Resolve(Obj(c[0], c[1]), "t"); err != nil || got != c[2] {
			t.Errorf("{%s: %q} = %v, %v; want %s", c[0], c[1], got, err, c[2])
		}
	}
	leap := NewContext(time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), nil)
	if got, _ := leap.Resolve(Obj("$date", "+1y"), "t"); got != "2025-02-28" {
		t.Errorf("a year after 29 February: %v", got)
	}
}

func TestBadOffsets(t *testing.T) {
	for _, arg := range []any{"tomorrow", "+1", "1d", "+1.5d", "+1q", int64(5), nil, "+99999y", "+99999999999999999999999d", "-3000y"} {
		if _, err := NewContext(testAsOf, nil).Resolve(Obj("$date", arg), "t"); err == nil {
			t.Errorf("{$date: %v} resolved", arg)
		}
	}
}

func TestTextVariablesAndNesting(t *testing.T) {
	ctx := NewContext(testAsOf, map[string]any{"tenant_id": int64(7)})
	in, _ := ParseJSON(`{"a": [{"m": {"$text": "due ${date:+30d}, tenant ${var:tenant_id}, at ${now:}"}}], "t": {"$var": "tenant_id"}, "keep": {"$seed": "x"}, "two": {"$date": "", "x": 1}, "later": {"$text": "${date:} for ${seed:p}"}}`)
	got, err := ctx.Resolve(in, "t")
	want := `{"a": [{"m": "due 2026-03-02, tenant 7, at 2026-01-31T10:00:00Z"}], "t": 7, "keep": {"$seed": "x"}, "two": {"$date": "", "x": 1}, "later": {"$text": "2026-01-31 for ${seed:p}"}}`
	if err != nil || Dumps(got) != want {
		t.Errorf("got %s, %v", Dumps(got), err)
	}
	if _, err := ctx.Resolve(Obj("$var", "missing"), "t"); err == nil {
		t.Error("a variable with no value resolved")
	}
}

func TestAsOf(t *testing.T) {
	for text, want := range map[string]time.Time{
		"2026-11-17":           time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC),
		"2026-11-17T08:30:00Z": time.Date(2026, 11, 17, 8, 30, 0, 0, time.UTC),
		"2026-11-17T08:30:00":  time.Date(2026, 11, 17, 8, 30, 0, 0, time.UTC),
	} {
		if got, err := ParseAsOf(text); err != nil || !got.Equal(want) {
			t.Errorf("ParseAsOf(%s) = %v, %v", text, got, err)
		}
	}
	for _, bad := range []string{"yesterday", "2026-02-30", "2026-13-01", "2026-11-17T25:00:00Z", ""} {
		if _, err := ParseAsOf(bad); err == nil {
			t.Errorf("ParseAsOf(%q) read", bad)
		}
	}
}

func TestHeaderDefaultsAndSet(t *testing.T) {
	text := "{\"seed\": 2, \"vars\": {\"t\": 1, \"r\": \"north\"}}\n{\"kind\": \"entity\", \"entity\": \"a\", \"id\": 1, \"data\": {\"t\": {\"$var\": \"t\"}, \"r\": {\"$var\": \"r\"}}}\n"
	if got := Dumps(mustPlan(t, text, planCase{})[0].Body); got != `{"t": 1, "r": "north"}` {
		t.Errorf("defaults: %s", got)
	}
	if got := Dumps(mustPlan(t, text, planCase{variables: map[string]any{"t": int64(9)}})[0].Body); got != `{"t": 9, "r": "north"}` {
		t.Errorf("set: %s", got)
	}
}

// -- plan ----------------------------------------------------------------------

const h2 = "{\"seed\": 2}\n"

func kindOp(s *Step) string { return s.Kind + ":" + s.Op }

func TestPhasesAndFileOrder(t *testing.T) {
	plan := mustPlan(t, h2+"{\"kind\": \"entity\", \"entity\": \"b\", \"id\": 1, \"data\": {}}\n{\"kind\": \"schema\", \"entity\": \"b\", \"schema\": {}}\n"+
		"{\"kind\": \"entity\", \"entity\": \"a\", \"as\": \"x\", \"data\": {}}\n{\"kind\": \"call\", \"method\": \"GET\", \"path\": \"/api/v1/a\"}\n", planCase{})
	if got := shape(plan, kindOp); got != "schema:call entity:create entity:save call:call" {
		t.Fatalf("order: %s", got)
	}
	if plan[2].Path != "/api/v1/b/save/1" {
		t.Errorf("path %s", plan[2].Path)
	}
}

func TestTenantScope(t *testing.T) {
	text := "{\"seed\": 2, \"tenant\": \"acme\"}\n{\"kind\": \"entity\", \"entity\": \"a\", \"id\": 1, \"data\": {}}\n{\"kind\": \"schema\", \"entity\": \"a\", \"schema\": {}}\n"
	plan := mustPlan(t, text, planCase{})
	if plan[0].PathTemplate != "/api/v1/schema/a" || plan[1].Path != "/api/v1/tenant/acme/a/save/1" { // schemas are not scoped
		t.Errorf("paths %s, %s", plan[0].PathTemplate, plan[1].Path)
	}
	if got := mustPlan(t, text, planCase{opts: PlanOptions{Tenant: "other"}})[1].Path; got != "/api/v1/tenant/other/a/save/1" {
		t.Errorf("with a tenant given: %s", got)
	}
}

func TestMatchAndFind(t *testing.T) {
	plan := mustPlan(t, h2+"{\"kind\": \"entity\", \"entity\": \"notes\", \"match\": [\"code\"], \"data\": {\"code\": \"N-1\", \"asset_id\": {\"$seed\": \"pump\"}}}\n"+
		"{\"kind\": \"entity\", \"entity\": \"assets\", \"find\": {\"tag\": \"A-1\"}, \"as\": \"pump\"}\n"+
		"{\"kind\": \"fsm_def\", \"match\": [\"name\"], \"as\": \"d\", \"body\": {\"name\": \"life\"}}\n", planCase{})
	got := shape(plan, func(s *Step) string { return fmt.Sprintf("%s:%s:%v", s.Kind, s.Op, s.Tracked) })
	if got != "entity:find:false fsm_def:call:true entity:match:true" {
		t.Fatalf("order: %s", got)
	}
	if Dumps(plan[0].Body) != `{"tag": "A-1"}` { // a lookup runs before everything, so what it binds can be used anywhere
		t.Errorf("lookup body %s", Dumps(plan[0].Body))
	}
	for _, c := range [][2]string{
		{"{\"kind\": \"entity\", \"entity\": \"a\", \"find\": {\"tag\": \"x\"}}\n", `needs "as"`},
		{"{\"kind\": \"entity\", \"entity\": \"a\", \"find\": {\"tag\": \"x\"}, \"as\": \"n\", \"data\": {}}\n", "only looked up"},
		{"{\"kind\": \"entity\", \"entity\": \"a\", \"find\": {}, \"as\": \"n\"}\n", "must be an object"},
		{"{\"kind\": \"entity\", \"entity\": \"a\", \"id\": 1, \"match\": [\"c\"], \"data\": {\"c\": 1}}\n", "not both"},
		{"{\"kind\": \"entity\", \"entity\": \"a\", \"match\": [\"c\"], \"data\": {\"d\": 1}}\n", "does not have"},
		{"{\"kind\": \"fsm_def\", \"match\": [\"description\"], \"body\": {\"description\": \"x\"}}\n", "by name only"},
	} {
		refused(t, h2+c[0], c[1], planCase{})
	}
	// a lookup is not something the seed writes, so its entity type need not be in the header's list
	mustPlan(t, "{\"seed\": 2, \"entities\": [\"notes\"]}\n{\"kind\": \"entity\", \"entity\": \"assets\", \"find\": {\"tag\": \"A-1\"}, \"as\": \"p\"}\n", planCase{})
}

func TestAnAdditionRunsAfterWhatItAddsTo(t *testing.T) {
	dir := t.TempDir()
	base := writeSeed(t, dir, "base.seed", "{\"seed\": 2, \"name\": \"base\"}\n{\"kind\": \"entity\", \"entity\": \"assets\", \"id\": 1, \"data\": {\"tag\": \"A-1\"}}\n")
	add := writeSeed(t, dir, "add.seed", "{\"seed\": 2, \"name\": \"add\", \"requires\": [\"base\"]}\n{\"kind\": \"entity\", \"entity\": \"assets\", \"find\": {\"tag\": \"A-1\"}, \"as\": \"p\"}\n"+
		"{\"kind\": \"config\", \"namespace\": \"n\", \"key\": \"k\", \"body\": {\"v\": {\"$seed\": \"p\"}}}\n")
	for _, order := range [][]string{{add, base}, {base, add}} {
		var files []SeedFile
		for _, path := range order {
			header, records, _, err := ReadSeed(path, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, SeedFile{path, header, records})
		}
		plan, err := BuildPlan(files, NewContext(testAsOf, nil), PlanOptions{})
		if err != nil {
			t.Fatal(err)
		}
		// the lookup has the lowest phase of all, and still runs after the record it looks for
		if got := shape(plan, func(s *Step) string { return filepath.Base(s.File) + ":" + s.Op }); got != "base.seed:save add.seed:find add.seed:call" {
			t.Errorf("order: %s", got)
		}
	}
}

func TestTheSameRecordTwiceIsTwoRecords(t *testing.T) {
	walk := "{\"kind\": \"fsm_walk\", \"machine\": 7, \"input\": \"hold\"}\n"
	plan := mustPlan(t, h2+walk+"{\"kind\": \"fsm_walk\", \"machine\": 7, \"input\": \"resume\"}\n"+walk, planCase{})
	if plan[0].Key == plan[1].Key || plan[2].Key != plan[0].Key+"#2" {
		t.Errorf("keys %s, %s, %s", plan[0].Key, plan[1].Key, plan[2].Key)
	}
}

func TestRefsOrderAndCycles(t *testing.T) {
	ref := func(e string, i int) string { return fmt.Sprintf(`{"type": "REF", "entity": "%s", "id": %d}`, e, i) }
	plan := mustPlan(t, h2+"{\"kind\": \"entity\", \"entity\": \"p\", \"id\": 2, \"data\": {\"partner\": "+ref("p", 1)+", \"skill\": "+ref("s", 1)+"}}\n"+
		"{\"kind\": \"entity\", \"entity\": \"p\", \"id\": 1, \"data\": {\"partner\": "+ref("p", 2)+"}}\n{\"kind\": \"entity\", \"entity\": \"s\", \"id\": 1, \"data\": {}}\n", planCase{})
	got := shape(plan, func(s *Step) string { return s.Entity + "/" + Text(s.ID) + ":" + s.Op })
	if got != "s/1:save p/2:save p/1:save p/2:patch p/1:patch" {
		t.Fatalf("order: %s", got)
	}
	if Dumps(plan[1].Body) != `{"skill": {"type": "REF", "entity": "s", "id": 1}}` {
		t.Errorf("body %s", Dumps(plan[1].Body))
	}
	if !strings.HasSuffix(plan[3].Key, "/refs") || plan[3].Tracked {
		t.Errorf("the patch: key %s, tracked %v", plan[3].Key, plan[3].Tracked)
	}
}

func TestWhatAPlanIsRefusedFor(t *testing.T) {
	e := "{\"kind\": \"entity\", \"entity\": \"a\", \"id\": 1, \"data\": {}}\n"
	for _, c := range [][2]string{
		{h2 + "{\"kind\": \"entty\", \"entity\": \"a\"}\n", "unknown kind 'entty'"},
		{h2 + "{\"kind\": \"entity\", \"entity\": \"a\", \"id\": 1, \"data\": {}, \"expct\": []}\n", `unknown key "expct"`},
		{h2 + e + e, "duplicate entity a/1"},
		{h2 + "{\"kind\": \"entity\", \"entity\": \"a\", \"id\": -1, \"data\": {}}\n", "non-negative integer"},
		{h2 + "{\"kind\": \"entity\", \"entity\": \"a\", \"id\": true, \"data\": {}}\n", "non-negative integer"},
		{h2 + "{\"kind\": \"entity\", \"entity\": \"a\", \"id\": 1}\n", `needs a "data" object`},
		{"{\"seed\": 2, \"entities\": [\"a\"]}\n{\"kind\": \"entity\", \"entity\": \"b\", \"id\": 1, \"data\": {}}\n", `not in the header's "entities" list`},
		{h2 + "{\"kind\": \"entity\", \"entity\": \"a\", \"data\": {\"x\": {\"$seed\": \"later\"}}}\n{\"kind\": \"entity\", \"entity\": \"b\", \"as\": \"later\", \"data\": {}}\n", `"later" is used here before`},
		{h2 + "{\"kind\": \"call\", \"method\": \"GET\", \"path\": \"/api/v1/a/${nobody}\"}\n", `"nobody" is used here before`},
		{h2 + "{\"kind\": \"call\", \"method\": \"GET\", \"path\": \"/elsewhere\"}\n", "must start with /api/v1 or /api/v2"},
		{h2 + "{\"kind\": \"meta\", \"entity\": \"a\", \"id\": 1, \"value\": true}\n", `needs the field "key"`},
		{h2 + "{\"kind\": \"entity\", \"entity\": \"a\", \"id\": 1, \"data\": {\"v\": {\"$var\": \"nope\"}}}\n", `variable "nope" has no value`},
		{h2 + "{\"kind\": \"call\", \"method\": \"POST\", \"path\": \"/api/v1/a\", \"body_file\": \"../x\"}\n", "outside the seed's directory"},
		{h2 + "{\"kind\": \"blob\", \"key\": \"k\", \"body_b64\": \"not base64!\"}\n", "not valid base64"},
		{h2 + "{\"kind\": \"blob\", \"key\": \"k\"}\n", "exactly one of"},
	} {
		refused(t, c[0], c[1], planCase{})
	}
	refused(t, h2+"{\"kind\": \"config\", \"namespace\": \"n\", \"key\": \"k\", \"body\": {}}\n", "not allowed with --safe", planCase{opts: PlanOptions{Safe: true}})
	refused(t, h2+e, "unknown kind 'nothing'", planCase{opts: PlanOptions{Only: []string{"nothing"}}})
}

func TestOnlyAndSkip(t *testing.T) {
	text := h2 + "{\"kind\": \"schema\", \"entity\": \"a\", \"schema\": {}}\n{\"kind\": \"entity\", \"entity\": \"a\", \"id\": 1, \"data\": {}}\n"
	kind := func(s *Step) string { return s.Kind }
	if got := shape(mustPlan(t, text, planCase{opts: PlanOptions{Skip: []string{"schema"}}}), kind); got != "entity" {
		t.Errorf("skip: %s", got)
	}
	if got := shape(mustPlan(t, text, planCase{opts: PlanOptions{Only: []string{"schema"}}}), kind); got != "schema" {
		t.Errorf("only: %s", got)
	}
}

func TestABlobIsACallWithItsKeyInAHeader(t *testing.T) {
	plan := mustPlan(t, h2+"{\"kind\": \"blob\", \"key\": \"docs/a.txt\", \"body_text\": \"hola\", \"content_type\": \"text/plain\"}\n", planCase{})
	s := plan[0]
	if s.Method != "POST" || s.PathTemplate != "/api/v1/blob" || string(s.Raw) != "hola" || s.Phase != 60 || s.Label != "blob docs/a.txt" ||
		Dumps(s.Headers) != `{"Content-Type": "text/plain", "X-Blob-Key": "docs/a.txt"}` {
		t.Errorf("step %s", Dumps(s.JSON()))
	}
}

// -- the repository's own seed ---------------------------------------------------

func TestTheExampleReadsAndPlans(t *testing.T) {
	path := filepath.Join("..", "..", "seeds", "example.seed")
	header, records, _, err := ReadSeed(path, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if Text(header.Get("seed")) != "1" { // a version 1 file: still valid, and means the same
		t.Errorf("format %s", Text(header.Get("seed")))
	}
	plan, err := BuildPlan([]SeedFile{{path, header, records}}, NewContext(testAsOf, nil), PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// one step for each record, and the two patches that close the cycle of references between persons 1 and 2
	var ops []string
	for _, s := range plan {
		if s.Kind == "entity" {
			ops = append(ops, s.Op)
		}
	}
	if len(plan) != len(records)+2 || strings.Join(ops, " ") != "save save save patch patch" {
		t.Errorf("%d steps for %d records; entities: %v", len(plan), len(records), ops)
	}
}
