// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package seed

// plan.go -- from records to an ordered list of requests.
//
// Keys are checked, the placeholders for dates, times and variables are
// resolved, and a name bound with "as" must be bound before the record that
// uses it: all of it found here, before a load starts, and not halfway
// through one. scripts/seed_system/plan.py is the reference this follows,
// record for record.

import (
	"container/heap"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type kindSpec struct {
	phase    int64
	method   string
	api      string
	path     string
	scoped   bool
	body     []string // one name: the field that is the body; several: the fields that make it up
	whole    bool     // body is one field
	existsOK bool
}

func one(phase int64, method, api, path string, scoped bool, field string, existsOK bool) kindSpec {
	return kindSpec{phase, method, api, path, scoped, []string{field}, true, existsOK}
}

func made(phase int64, method, api, path string, fields ...string) kindSpec {
	return kindSpec{phase, method, api, path, true, fields, false, false}
}

// kinds: kind -> route and behaviour.
var kinds = map[string]kindSpec{
	"config":       one(10, "PUT", "/api/v1", "/admin/config/{namespace}/{key}", false, "body", false),
	"schema":       one(20, "POST", "/api/v1", "/schema/{entity}", false, "schema", false),
	"seq":          one(30, "POST", "/api/v2", "/seq", true, "body", true),
	"gen":          one(30, "POST", "/api/v2", "/gen/{type}", true, "body", true),
	"bal_account":  one(30, "POST", "/api/v2", "/bal/def", true, "body", true),
	"loc_pattern":  one(30, "POST", "/api/v2", "/loc/patterns/def", true, "body", true),
	"fsm_def":      one(30, "POST", "/api/v2", "/fsm/def", true, "body", false),
	"dxp_def":      one(30, "POST", "/api/v2", "/dxp/def", true, "body", false),
	"loc":          one(35, "POST", "/api/v2", "/loc/def", true, "body", true),
	"cal_calendar": one(45, "POST", "/api/v2", "/cal/calendars", true, "body", true),
	"edge":         made(50, "POST", "/api/v1", "/graph/edges", "from", "to", "rel", "props"),
	"event_def":    one(55, "POST", "/api/v2", "/event/def", true, "body", false),
	"meta":         made(60, "PUT", "/api/v2", "/meta/{entity}/{id}/{key}", "value", "expires_at"),
	"fsm_machine":  one(46, "POST", "/api/v2", "/fsm/machine", true, "body", false),
	"fsm_walk":     made(47, "POST", "/api/v2", "/fsm/machine/{machine}/walk", "input", "payload"),
	"ts_provision": one(30, "POST", "/api/v2", "/ts/provision", true, "body", true),
	"ts_timeline":  one(35, "POST", "/api/v2", "/ts/tl/def", true, "body", true),
	"ts_event":     one(48, "POST", "/api/v2", "/ts/events", true, "body", false),
	"ts_batch":     one(48, "POST", "/api/v2", "/ts/events/batch", true, "body", false),
	"dxp_txn":      one(70, "POST", "/api/v2", "/dxp/txn", true, "body", false),
	"obj":          one(70, "POST", "/api/v2", "/obj/attach", true, "body", false),
	"loc_fence":    one(70, "POST", "/api/v2", "/loc/fences/attach", true, "body", false),
	"bal_transfer": one(80, "POST", "/api/v2", "/bal/transfer", true, "body", false),
	"cal_propose":  one(80, "POST", "/api/v2", "/cal/propose", true, "body", false),
	"cal_confirm":  one(81, "POST", "/api/v2", "/cal/confirm", true, "body", false),
	"bal_close":    one(90, "POST", "/api/v2", "/bal/close", true, "body", false),
}

const (
	callPhase = 75
	findPhase = 5
)

var (
	entityKeys = []string{"entity", "id", "data", "match", "find"}
	callKeys   = []string{"method", "path", "scoped", "query", "content_type", "headers", "body", "body_file", "body_b64", "body_text"}
	blobKeys   = []string{"key", "content_type", "body_file", "body_b64", "body_text"}
	// matchable: kinds, besides entity, whose records may say "match", and what they can be found by.
	matchable = map[string][]string{"fsm_def": {"name"}}
	// unsafeKinds: kinds a file from a source that is not trusted may not use
	// (--safe): they reach any route, or the server's configuration.
	unsafeKinds = []string{"call", "config"}

	nameRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	fieldRe    = regexp.MustCompile(`\{(\w+)\}`)
	boundRe    = regexp.MustCompile(`\$\{([\p{L}\p{N}_]+)\}`)
	callPathRe = regexp.MustCompile(`^(/api/v[12])(/.*)$`)
)

// AllKinds returns every kind a record may be, in order.
func AllKinds() []string {
	out := []string{"entity", "call", "blob"}
	for k := range kinds {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// KindPhase is the phase the records of a kind run in unless they say another.
func KindPhase(kind string) (int64, bool) {
	switch kind {
	case "entity":
		return 40, true
	case "call":
		return callPhase, true
	case "blob":
		return 60, true
	}
	spec, ok := kinds[kind]
	return spec.phase, ok
}

func typedKeys(kind string) []string {
	spec := kinds[kind]
	var keys []string
	for _, m := range fieldRe.FindAllStringSubmatch(spec.path, -1) {
		keys = append(keys, m[1])
	}
	return append(keys, spec.body...)
}

// Step is one request of a plan, and what the load does around it.
type Step struct {
	Phase        float64
	Label        string
	Kind         string
	Where        string // file:line of the record
	Tenant       string
	As           string // the name the answer's value is bound to, or ""
	Bind         any    // the field of the answer that is bound: "id" unless the record says another
	Expect       []any  // statuses the record says are fine
	ExistsOK     bool   // a 409 means it was there already
	Headers      *Object
	Query        any
	Body         any
	Raw          []byte // a body that is not JSON
	HasRaw       bool
	Entity       string
	ID           any
	Op           string // call, save, patch, create, match, find
	File         string
	Match        []string
	Key          string // what identifies the record across loads
	Method       string
	PathTemplate string // a path with ${name} where a bound value goes
	Path         string
	ListPath     string
	QueryPath    string
	ItemPath     string
	ReadPath     string
	GuardSchema  string
	Tracked      bool // the load record remembers it ran
}

// URLPath is the step's path: the one it has, or the template still to be filled.
func (s *Step) URLPath() string {
	if s.PathTemplate != "" {
		return s.PathTemplate
	}
	return s.Path
}

// JSON is the step as a value, for showing a plan and for comparing two.
func (s *Step) JSON() *Object {
	var raw any
	if s.HasRaw {
		raw = base64.StdEncoding.EncodeToString(s.Raw)
	}
	opt := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	var match any
	if s.Match != nil {
		list := make([]any, len(s.Match))
		for i, m := range s.Match {
			list[i] = m
		}
		match = list
	}
	phase := any(s.Phase)
	if s.Phase == float64(int64(s.Phase)) {
		phase = int64(s.Phase)
	}
	expect := s.Expect
	if expect == nil {
		expect = []any{}
	}
	return Obj("phase", phase, "label", s.Label, "kind", s.Kind, "where", s.Where, "tenant", opt(s.Tenant), "as", opt(s.As),
		"bind", s.Bind, "expect", expect, "exists_ok", s.ExistsOK, "headers", s.Headers, "query", s.Query, "body", s.Body,
		"raw", raw, "entity", opt(s.Entity), "id", s.ID, "op", s.Op, "file", s.File, "match", match, "key", s.Key,
		"method", s.Method, "path", opt(s.URLPath()), "list_path", opt(s.ListPath), "query_path", opt(s.QueryPath),
		"item_path", opt(s.ItemPath), "read_path", opt(s.ReadPath), "guard_schema", opt(s.GuardSchema), "tracked", s.Tracked)
}

func fullPath(api, suffix string, scoped bool, tenant string) string {
	if scoped && tenant != "" {
		return api + "/tenant/" + Quote(tenant) + suffix
	}
	return api + suffix
}

func fillPath(template string, rec *Object, at string) (string, error) {
	var failed error
	out := fieldRe.ReplaceAllStringFunc(template, func(found string) string {
		name := found[1 : len(found)-1]
		if !rec.Has(name) {
			if failed == nil {
				failed = errorf("%s: kind '%s' needs the field \"%s\"", at, Text(rec.Get("kind")), name)
			}
			return ""
		}
		if o, ok := rec.Get(name).(*Object); ok && o.Len() == 1 && o.Has("$seed") {
			return "${" + Text(o.Get("$seed")) + "}" // resolved when the step runs
		}
		return Quote(Text(rec.Get(name)))
	})
	return out, failed
}

func isRef(v any) bool {
	o, ok := v.(*Object)
	if !ok {
		return false
	}
	s, _ := o.Str("type")
	return s == "REF"
}

func refsIn(v any) []*Object {
	if isRef(v) {
		return []*Object{v.(*Object)}
	}
	var out []*Object
	if list, ok := v.([]any); ok {
		for _, item := range list {
			if isRef(item) {
				out = append(out, item.(*Object))
			}
		}
	}
	return out
}

// RecordKey is what identifies a record across loads of the same file: a digest of its content.
func RecordKey(rec *Object) string {
	return digestText(DumpsSorted(visible(rec)))[:20]
}

func baseStep(rec *Object, phase int64, label, tenant string) (*Step, error) {
	at := recWhere(rec)
	s := &Step{Phase: float64(phase), Label: label, Kind: Text(rec.Get("kind")), Where: at, Tenant: tenant, Bind: "id",
		Headers: NewObject(), Op: "call", File: File(rec)}
	if rec.Has("phase") {
		f, ok := asFloat(rec.Get("phase"))
		if _, isBool := rec.Get("phase").(bool); !ok || isBool {
			return nil, errorf("%s: \"phase\" must be a number", at)
		}
		s.Phase = f
	}
	if Truthy(rec.Get("as")) {
		s.As = Text(rec.Get("as"))
	}
	if rec.Has("bind") {
		s.Bind = rec.Get("bind")
	}
	if rec.Has("expect") {
		list, ok := rec.Get("expect").([]any)
		if !ok {
			return nil, errorf("%s: \"expect\" must be a list of statuses", at)
		}
		s.Expect = list
	}
	if key, _ := rec.Str("_key"); key != "" {
		s.Key = key
	} else {
		s.Key = RecordKey(rec)
	}
	return s, nil
}

func matchFields(rec *Object, at string, body any, allowed []string) ([]string, error) {
	list, _ := rec.Get("match").([]any)
	var fields []string
	for _, v := range list {
		if f, ok := v.(string); ok && nameRe.MatchString(f) {
			fields = append(fields, f)
		}
	}
	if len(list) == 0 || len(fields) != len(list) {
		return nil, errorf("%s: \"match\" must be a list of field names", at)
	}
	obj, _ := body.(*Object)
	for _, f := range fields {
		if allowed != nil && !contains(allowed, f) {
			return nil, errorf("%s: a '%s' record can be matched by %s only", at, Text(rec.Get("kind")), strings.Join(allowed, ", "))
		}
		if !obj.Has(f) {
			return nil, errorf("%s: \"match\" names the field \"%s\", which the record does not have", at, f)
		}
	}
	return fields, nil
}

func readBodyFile(rec *Object, at string, outsideOK bool) ([]byte, error) {
	name := Text(rec.Get("body_file"))
	abs, err := filepath.Abs(File(rec))
	if err != nil {
		return nil, errorf("%s: cannot read body_file: %s", at, err)
	}
	base := filepath.Dir(abs)
	fpath := name
	if !filepath.IsAbs(fpath) {
		fpath = filepath.Join(base, name)
	}
	fpath = filepath.Clean(fpath)
	inside := fpath == base || strings.HasPrefix(fpath, strings.TrimSuffix(base, string(filepath.Separator))+string(filepath.Separator))
	if !outsideOK && !inside {
		return nil, errorf("%s: body_file \"%s\" is outside the seed's directory (allow it with --allow-outside-files)", at, name)
	}
	raw, err := os.ReadFile(fpath) // #nosec G304 -- the path is checked above, or the caller allowed it
	if err != nil {
		return nil, errorf("%s: cannot read body_file: %s", at, err)
	}
	return raw, nil
}

func planTyped(rec *Object, tenant string) (*Step, error) {
	kind := Text(rec.Get("kind"))
	spec := kinds[kind]
	at := recWhere(rec)
	step, err := baseStep(rec, spec.phase, kind, tenant)
	if err != nil {
		return nil, err
	}
	step.Method = spec.method
	suffix, err := fillPath(spec.path, rec, at)
	if err != nil {
		return nil, err
	}
	step.PathTemplate = fullPath(spec.api, suffix, spec.scoped, tenant)
	step.ExistsOK = spec.existsOK
	if spec.whole {
		if !rec.Has(spec.body[0]) {
			return nil, errorf("%s: kind '%s' needs the field \"%s\"", at, kind, spec.body[0])
		}
		step.Body = rec.Get(spec.body[0])
	} else {
		body := NewObject()
		for _, k := range spec.body {
			if rec.Has(k) {
				body.Set(k, rec.Get(k))
			}
		}
		step.Body = body
	}
	ident := ""
	for _, k := range []string{"entity", "namespace", "type"} {
		if Truthy(rec.Get(k)) {
			ident = Text(rec.Get(k))
			break
		}
	}
	step.Label = strings.TrimSpace(kind + " " + ident)
	if kind == "schema" {
		step.GuardSchema = Text(rec.Get("entity"))
	}
	if allowed, ok := matchable[kind]; ok && rec.Has("match") {
		if step.Match, err = matchFields(rec, at, step.Body, allowed); err != nil {
			return nil, err
		}
		step.ListPath = step.PathTemplate
	}
	return step, nil
}

func planCall(rec *Object, tenant string, outsideFiles bool) (*Step, error) {
	at := recWhere(rec)
	method := ""
	if rec.Has("method") {
		method = strings.ToUpper(Text(rec.Get("method")))
	}
	if !contains([]string{"GET", "POST", "PUT", "PATCH", "DELETE"}, method) {
		return nil, errorf("%s: call needs a method (GET, POST, PUT, PATCH or DELETE)", at)
	}
	var path any = ""
	if rec.Has("path") {
		path = rec.Get("path")
	}
	pathText, _ := path.(string)
	m := callPathRe.FindStringSubmatch(pathText)
	if m == nil {
		return nil, errorf("%s: call path must start with /api/v1 or /api/v2, got %s", at, Repr(path))
	}
	step, err := baseStep(rec, callPhase, "call "+method+" "+pathText, tenant)
	if err != nil {
		return nil, err
	}
	step.Method = method
	step.PathTemplate = fullPath(m[1], m[2], Truthy(rec.Get("scoped")), tenant)
	step.Query = rec.Get("query")
	if rec.Has("content_type") {
		step.Headers.Set("Content-Type", rec.Get("content_type"))
	}
	if Truthy(rec.Get("headers")) {
		headers, ok := rec.Get("headers").(*Object)
		if !ok {
			return nil, errorf("%s: call \"headers\" must be an object", at)
		}
		for _, k := range headers.Keys() {
			step.Headers.Set(k, headers.Get(k))
		}
	}
	var given []string
	for _, k := range []string{"body", "body_file", "body_b64", "body_text"} {
		if rec.Has(k) {
			given = append(given, k)
		}
	}
	if len(given) > 1 {
		return nil, errorf("%s: call may have only one of body, body_file, body_b64, body_text", at)
	}
	if len(given) == 1 {
		switch given[0] {
		case "body":
			step.Body = rec.Get("body")
		case "body_file":
			if step.Raw, err = readBodyFile(rec, at, outsideFiles); err != nil {
				return nil, err
			}
			step.HasRaw = true
		case "body_b64":
			text, ok := rec.Get("body_b64").(string)
			if !ok || strings.ContainsAny(text, "\r\n") {
				return nil, errorf("%s: body_b64 is not valid base64: it must be a string of base64 text", at)
			}
			if step.Raw, err = base64.StdEncoding.Strict().DecodeString(text); err != nil {
				return nil, errorf("%s: body_b64 is not valid base64: %s", at, err)
			}
			step.HasRaw = true
		case "body_text":
			step.Raw, step.HasRaw = []byte(Text(rec.Get("body_text"))), true
		}
	}
	return step, nil
}

// planBlob: a blob is a raw-body POST with its key in the X-Blob-Key header.
func planBlob(rec *Object, tenant string, outsideFiles bool) (*Step, error) {
	at := recWhere(rec)
	key, _ := rec.Str("key")
	if key == "" {
		return nil, errorf("%s: blob needs a \"key\"", at)
	}
	var options []string
	for _, k := range []string{"body_file", "body_b64", "body_text"} {
		if rec.Has(k) {
			options = append(options, k)
		}
	}
	if len(options) != 1 {
		return nil, errorf("%s: blob needs exactly one of body_file, body_b64, body_text", at)
	}
	call := NewObject()
	for _, k := range append(options, "_file", "_line", "_key", "as", "bind", "expect") {
		if rec.Has(k) {
			call.Set(k, rec.Get(k))
		}
	}
	var contentType any = "application/octet-stream"
	if rec.Has("content_type") {
		contentType = rec.Get("content_type")
	}
	call.Set("kind", "blob")
	call.Set("method", "POST")
	call.Set("path", "/api/v1/blob")
	call.Set("scoped", true)
	call.Set("headers", Obj("X-Blob-Key", key))
	call.Set("content_type", contentType)
	step, err := planCall(call, tenant, outsideFiles)
	if err != nil {
		return nil, err
	}
	step.Label = "blob " + key
	step.Phase = 60
	if rec.Has("phase") {
		f, ok := asFloat(rec.Get("phase"))
		if _, isBool := rec.Get("phase").(bool); !ok || isBool {
			return nil, errorf("%s: \"phase\" must be a number", at)
		}
		step.Phase = f
	}
	return step, nil
}

func cleanData(rec *Object) (*Object, error) {
	data, ok := rec.Get("data").(*Object)
	if !ok {
		return nil, errorf("%s: entity needs a \"data\" object", recWhere(rec))
	}
	out := NewObject()
	for _, k := range data.Keys() {
		if !contains(SystemFields, k) {
			out.Set(k, data.Get(k))
		}
	}
	return out, nil
}

// idText spells an id so that two ids that are the same number are the same text.
func idText(v any) (string, bool) {
	if f, ok := v.(float64); ok {
		if f != float64(int64(f)) {
			return "", false
		}
		v = int64(f)
	}
	if !IsInt(v) {
		return "", false
	}
	return Text(v), true
}

func entityKey(tenant string, entity, id any) (string, bool) {
	name, ok := entity.(string)
	text, isID := idText(id)
	if !ok || !isID {
		return "", false
	}
	return tenant + "\x00" + name + "\x00" + text, true
}

type intHeap []int

func (h intHeap) Len() int           { return len(h) }
func (h intHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h intHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *intHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *intHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// planEntities orders entity records so that what a REF points at exists first, and breaks cycles with PATCH.
func planEntities(recs []*Object, tenants map[string]string) ([]*Step, error) {
	var steps []*Step
	var idless, ided []*Object
	for _, rec := range recs {
		at := recWhere(rec)
		entity, _ := rec.Str("entity")
		if entity == "" {
			return nil, errorf("%s: entity record needs an \"entity\" name", at)
		}
		if rec.Has("find") {
			// A record that is looked up and never written: what another seed, or the application, put there.
			if rec.Has("id") || rec.Has("data") || rec.Has("match") {
				return nil, errorf("%s: a record with \"find\" is only looked up: it takes no \"id\", \"data\" or \"match\"", at)
			}
			if !nameRe.MatchString(entity) {
				return nil, errorf("%s: \"find\" needs an entity name made of letters, digits and underscores", at)
			}
			fields, ok := rec.Get("find").(*Object)
			for _, f := range fields.Keys() {
				ok = ok && nameRe.MatchString(f)
			}
			if !ok || fields.Len() == 0 {
				return nil, errorf("%s: \"find\" must be an object of field names and the values they have", at)
			}
			if !Truthy(rec.Get("as")) {
				return nil, errorf("%s: a record with \"find\" needs \"as\", the name its id is bound to", at)
			}
			tenant := tenants[File(rec)]
			step, err := baseStep(rec, findPhase, "entity "+entity+" (find)", tenant)
			if err != nil {
				return nil, err
			}
			step.Method, step.Op, step.Entity = "POST", "find", entity
			step.Path = fullPath("/api/v1", "/oql/query", true, tenant)
			step.QueryPath = step.Path
			step.Body = fields.Copy()
			step.Match = append([]string{}, fields.Keys()...)
			sort.Strings(step.Match)
			step.ItemPath = fullPath("/api/v1", "/"+Quote(entity), true, tenant)
			steps = append(steps, step)
			continue
		}
		if rec.Has("id") {
			id := rec.Get("id")
			if big, ok := asBig(id); !IsInt(id) || !ok || big.Sign() < 0 {
				return nil, errorf("%s: entity \"id\" must be a non-negative integer", at)
			}
		}
		if rec.Has("match") {
			if rec.Has("id") {
				return nil, errorf("%s: an entity has an \"id\" or is found by \"match\", not both", at)
			}
			if !nameRe.MatchString(entity) {
				return nil, errorf("%s: \"match\" needs an entity name made of letters, digits and underscores", at)
			}
			if _, err := matchFields(rec, at, rec.Get("data"), nil); err != nil {
				return nil, err
			}
		}
		if rec.Has("id") {
			ided = append(ided, rec)
		} else {
			idless = append(idless, rec)
		}
	}

	entityStep := func(rec *Object, data *Object, patch *Object) (*Step, error) {
		tenant := tenants[File(rec)]
		entity, _ := rec.Str("entity")
		quoted := Quote(entity)
		id := Text(rec.Get("id"))
		var step *Step
		var err error
		switch {
		case patch != nil:
			if step, err = baseStep(rec, 41, fmt.Sprintf("entity %s/%s (refs)", entity, id), tenant); err != nil {
				return nil, err
			}
			step.Method, step.Op = "PATCH", "patch"
			step.Path = fullPath("/api/v1", "/"+quoted+"/"+id, true, tenant)
			step.Body, step.As = patch, ""
		case rec.Has("id"):
			if step, err = baseStep(rec, 40, fmt.Sprintf("entity %s/%s", entity, id), tenant); err != nil {
				return nil, err
			}
			step.Method, step.Op = "POST", "save"
			step.Path = fullPath("/api/v1", "/"+quoted+"/save/"+id, true, tenant)
			step.ReadPath = fullPath("/api/v1", "/"+quoted+"/"+id, true, tenant)
			step.Body = data
		default:
			if step, err = baseStep(rec, 39, "entity "+entity+" (new)", tenant); err != nil {
				return nil, err
			}
			step.Method, step.Op = "POST", "create"
			step.Path = fullPath("/api/v1", "/"+quoted, true, tenant)
			step.Body = data
			if rec.Has("match") {
				step.Op = "match"
				step.Match, _ = matchFields(rec, step.Where, rec.Get("data"), nil)
			}
			step.QueryPath = fullPath("/api/v1", "/oql/query", true, tenant)
		}
		step.Entity = entity
		if rec.Has("id") {
			step.ID = rec.Get("id")
		}
		step.ItemPath = fullPath("/api/v1", "/"+quoted, true, tenant)
		return step, nil
	}
	add := func(rec *Object, data *Object, patch *Object) error {
		step, err := entityStep(rec, data, patch)
		if err == nil {
			steps = append(steps, step)
		}
		return err
	}

	for _, rec := range idless {
		data, err := cleanData(rec)
		if err != nil {
			return nil, err
		}
		if err := add(rec, data, nil); err != nil {
			return nil, err
		}
	}

	index := map[string]int{} // key -> position in ided
	keyOf := make([]string, len(ided))
	for i, rec := range ided {
		key, _ := entityKey(tenants[File(rec)], rec.Get("entity"), rec.Get("id"))
		if first, dup := index[key]; dup {
			return nil, errorf("%s: duplicate entity %s/%s (first at %s)", recWhere(rec), Text(rec.Get("entity")), Text(rec.Get("id")), recWhere(ided[first]))
		}
		index[key], keyOf[i] = i, key
	}

	targetsOf := func(i int, value any) []int {
		var out []int
		for _, ref := range refsIn(value) {
			if key, ok := entityKey(tenants[File(ided[i])], ref.Get("entity"), ref.Get("id")); ok {
				if t, known := index[key]; known {
					out = append(out, t)
				}
			}
		}
		return out
	}
	indegree := make([]int, len(ided))
	dependents := make([][]int, len(ided))
	cleaned := make([]*Object, len(ided))
	for i, rec := range ided {
		data, err := cleanData(rec)
		if err != nil {
			return nil, err
		}
		cleaned[i] = data
		found := map[int]bool{}
		for _, k := range data.Keys() {
			for _, t := range targetsOf(i, data.Get(k)) {
				found[t] = true
			}
		}
		indegree[i] = len(found)
		for t := range found {
			dependents[t] = append(dependents[t], i)
		}
	}
	ready := &intHeap{}
	for i, n := range indegree {
		if n == 0 {
			*ready = append(*ready, i)
		}
	}
	heap.Init(ready)
	done := make([]bool, len(ided))
	for ready.Len() > 0 {
		i := heap.Pop(ready).(int)
		done[i] = true
		if err := add(ided[i], cleaned[i], nil); err != nil {
			return nil, err
		}
		for _, next := range dependents[i] {
			indegree[next]--
			if indegree[next] == 0 {
				heap.Push(ready, next)
			}
		}
	}

	var patches []*Step
	for i, rec := range ided {
		if done[i] {
			continue
		}
		data, deferred := NewObject(), NewObject()
		for _, k := range cleaned[i].Keys() {
			value := cleaned[i].Get(k)
			held := false
			for _, t := range targetsOf(i, value) {
				held = held || !done[t]
			}
			if held {
				deferred.Set(k, value)
			} else {
				data.Set(k, value)
			}
		}
		if err := add(rec, data, nil); err != nil {
			return nil, err
		}
		if deferred.Len() > 0 {
			patch, err := entityStep(rec, nil, deferred)
			if err != nil {
				return nil, err
			}
			patches = append(patches, patch)
		}
	}
	return append(steps, patches...), nil
}

// SeedFile is one .seed file as ReadSeed returns it.
type SeedFile struct {
	Path    string
	Header  *Object
	Records []*Object
}

// PlanOptions are what a plan can be told besides the files.
type PlanOptions struct {
	Tenant       string       // load into this tenant, whatever the headers say
	Only, Skip   []string     // kinds to keep, kinds to leave out
	Lenient      bool         // an unknown kind or key is a warning
	Safe         bool         // refuse the kinds that reach any route or the server's configuration
	OutsideFiles bool         // a body_file may be outside the seed's directory
	Warn         func(string) // where warnings go
}

// BuildPlan returns the ordered list of steps for the files given.
//
// It fails for anything that can be known without the server: an unknown kind
// or key, an entity type the header does not list, a placeholder with no
// value, a binding used before the record that makes it. The records are
// changed: their placeholders are resolved in place.
func BuildPlan(files []SeedFile, ctx *Context, opts PlanOptions) ([]*Step, error) {
	warn := opts.Warn
	if warn == nil {
		warn = func(string) {}
	}
	all := AllKinds()
	tenants := map[string]string{}
	passes := map[string]int{}
	additions := 0
	var plan []*Step
	var entityRecs []*Object
	for _, f := range files {
		tenant := opts.Tenant
		if tenant == "" {
			tenant, _ = f.Header.Str("tenant")
		}
		tenants[f.Path] = tenant
		var declared []string
		hasDeclared := f.Header.Has("entities") && f.Header.Get("entities") != nil
		if hasDeclared {
			declared = Names(f.Header, "entities")
		}
		variables := map[string]any{}
		if vars, ok := f.Header.Get("vars").(*Object); ok {
			for _, k := range vars.Keys() {
				variables[k] = vars.Get(k)
			}
		}
		for k, v := range ctx.Variables {
			variables[k] = v
		}
		fileCtx := NewContext(ctx.AsOf, variables)
		// A file that adds to others runs after them, as a pass of its own, in the order the files were named:
		// what it looks up with "find" has to be there, also when both are named in one load.
		if Truthy(f.Header.Get("addition")) || Truthy(f.Header.Get("requires")) {
			additions++
			passes[f.Path] = additions
		} else {
			passes[f.Path] = 0
		}
		for _, rec := range f.Records {
			kind := Text(rec.Get("kind"))
			if _, isStr := rec.Get("kind").(string); !isStr {
				kind = "\x00" + kind // not a name any kind has
			}
			at := recWhere(rec)
			if !contains(all, kind) {
				if opts.Lenient {
					warn(fmt.Sprintf("%s: unknown kind '%s' skipped", at, Text(rec.Get("kind"))))
					continue
				}
				return nil, errorf("%s: unknown kind '%s'. Known kinds: %s", at, Text(rec.Get("kind")), strings.Join(all, ", "))
			}
			if opts.Safe && contains(unsafeKinds, kind) {
				return nil, errorf("%s: a '%s' record is not allowed with --safe", at, kind)
			}
			var allowed []string
			switch kind {
			case "entity":
				allowed = entityKeys
			case "call":
				allowed = callKeys
			case "blob":
				allowed = blobKeys
			default:
				allowed = typedKeys(kind)
			}
			if _, ok := matchable[kind]; ok {
				allowed = append(append([]string{}, allowed...), "match")
			}
			if err := CheckRecordKeys(rec, allowed, opts.Lenient, warn); err != nil {
				return nil, err
			}
			rec.Set("_key", RecordKey(rec)) // taken before the placeholders are resolved, so it does not move with the clock
			for _, key := range append([]string{}, rec.Keys()...) {
				if strings.HasPrefix(key, "_") || key == "kind" {
					continue
				}
				v, err := fileCtx.Resolve(rec.Get(key), at)
				if err != nil {
					return nil, err
				}
				rec.Set(key, v)
			}
			if (kind == "entity" || kind == "schema") && hasDeclared && !rec.Has("find") {
				if name, ok := rec.Str("entity"); !ok || !contains(declared, name) {
					return nil, errorf("%s: entity type \"%s\" is not in the header's \"entities\" list", at, Text(rec.Get("entity")))
				}
			}
			var step *Step
			var err error
			switch kind {
			case "entity":
				entityRecs = append(entityRecs, rec)
				continue
			case "call":
				step, err = planCall(rec, tenant, opts.OutsideFiles)
			case "blob":
				step, err = planBlob(rec, tenant, opts.OutsideFiles)
			default:
				step, err = planTyped(rec, tenant)
			}
			if err != nil {
				return nil, err
			}
			plan = append(plan, step)
		}
	}
	entitySteps, err := planEntities(entityRecs, tenants)
	if err != nil {
		return nil, err
	}
	plan = append(plan, entitySteps...)

	named := append(append([]string{}, opts.Only...), opts.Skip...)
	sort.Strings(named)
	for _, name := range named {
		if !contains(all, name) {
			return nil, errorf("--only/--skip: unknown kind '%s'", name)
		}
	}
	kept := plan[:0]
	for _, s := range plan {
		if (len(opts.Only) == 0 || contains(opts.Only, s.Kind)) && !contains(opts.Skip, s.Kind) {
			kept = append(kept, s)
		}
	}
	plan = kept
	sort.SliceStable(plan, func(i, j int) bool {
		if pi, pj := passes[plan[i].File], passes[plan[j].File]; pi != pj {
			return pi < pj
		}
		return plan[i].Phase < plan[j].Phase
	})

	seen := map[string]int{}
	for _, step := range plan {
		// two identical records in a file are two things: the second gets its own key
		id := step.File + "\x00" + step.Key
		seen[id]++
		if step.Op == "patch" {
			step.Key += "/refs"
		} else if n := seen[id]; n > 1 {
			step.Key += fmt.Sprintf("#%d", n)
		}
		// what the load record remembers having run: everything but records that can be read back by their id
		step.Tracked = step.Op != "save" && step.Op != "patch" && step.Op != "find" && step.Kind != "schema"
	}

	bound := map[string]bool{}
	for _, step := range plan {
		used := BindingsIn(step.Body)
		for name := range BindingsIn(step.Query) {
			used[name] = true
		}
		for _, m := range boundRe.FindAllStringSubmatch(step.URLPath(), -1) {
			used[m[1]] = true
		}
		for _, name := range sortedKeys(used) {
			if !bound[name] {
				return nil, errorf("%s: \"%s\" is used here before any record binds it with \"as\"; records run by phase, then in "+
					"file order (docs/SEED_FORMAT.md, \"Order\")", step.Where, name)
			}
		}
		if step.As != "" {
			bound[step.As] = true
		}
	}
	return plan, nil
}
