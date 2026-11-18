// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package seed

// load.go -- loading a plan into a tenant, with the guards around it.
//
// Before the first write the loader finds out what the tenant holds. An empty
// tenant is loaded. A tenant that holds this same seed is continued: records
// that exist are left alone (mode "create") or written again (mode
// "replace"). Anything else is refused unless the caller allows it. What was
// loaded is recorded in the tenant, in the entity type ProvenanceEntity,
// which is how a later run tells those cases apart and how an interrupted
// load is continued with the same reference time.

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ProvenanceEntity is the entity type that records what was loaded into a tenant.
const ProvenanceEntity = "seed_loads"

// LoaderName is what a load record says wrote it.
const LoaderName = "xoseed 1.0.0"

// Outcomes are what can happen to a record in a load, in the order they are counted.
var Outcomes = []string{"created", "present", "replaced", "skipped", "failed"}

func nowText() string { return time.Now().UTC().Format(timeLayout) }

func short(value any) string {
	text, ok := value.(string)
	if !ok {
		text = Dumps(value)
	}
	if runeLen(text) <= 200 {
		return text
	}
	return cut(text, 200) + "..."
}

func dig(obj any, dotted string) (any, bool) {
	cur := obj
	for _, part := range strings.Split(dotted, ".") {
		o, ok := cur.(*Object)
		if !ok || !o.Has(part) {
			return nil, false
		}
		cur = o.Get(part)
	}
	return cur, true
}

// SeedName is the name of a seed: the one its header gives, or the file's name without its extension.
func SeedName(path string, header *Object) string {
	if name, _ := header.Str("name"); name != "" {
		return name
	}
	base := filepath.Base(path)
	if i := strings.LastIndex(base, "."); i > 0 && strings.Trim(base[:i], ".") != "" {
		return base[:i]
	}
	return base
}

// LoadFile is one file of a load: what ReadSeed returned for it.
type LoadFile struct {
	Path   string
	Header *Object
	Digest string
}

type fileState struct {
	LoadFile
	name     string
	tenant   string
	prior    *Object
	done     *Object
	loadPath string
	loadNew  bool
}

type tenantState struct {
	empty bool
	known bool // the instance said whether the tenant is empty
	loads []*Object
	fresh bool
}

// Options are what a load can be told.
type Options struct {
	Tenant        string       // load into this tenant, whatever the headers say
	Mode          string       // "create" (the default) or "replace"
	AllowExisting bool         // load into a tenant that holds other data
	UndoOnFailure bool         // delete what the run created when a record fails
	KeepGoing     bool         // go on after a record fails
	ForceSchemas  bool         // register a schema over rows that are already there
	Verbose       bool         // say what happens to every record
	Partial       bool         // the plan leaves kinds out (--only, --skip)
	Say           func(string) // where the run's lines go
}

// Loader is one load of one or more files into the tenants they name.
//
// Use: Inspect (asks the server what is there), then AsOf to settle the
// reference time, build the plan with it, then Run.
type Loader struct {
	client  *Client
	opts    Options
	files   []*fileState
	tenants map[string]*tenantState
}

// NewLoader returns a loader for the files given.
func NewLoader(c *Client, files []LoadFile, opts Options) (*Loader, error) {
	if opts.Mode == "" {
		opts.Mode = "create"
	}
	if opts.Mode != "create" && opts.Mode != "replace" {
		return nil, errorf("mode must be \"create\" or \"replace\"")
	}
	if opts.Say == nil {
		opts.Say = func(string) {}
	}
	l := &Loader{client: c, opts: opts, tenants: map[string]*tenantState{}}
	for _, f := range files {
		tenant := opts.Tenant
		if tenant == "" {
			tenant, _ = f.Header.Str("tenant")
		}
		l.files = append(l.files, &fileState{LoadFile: f, name: SeedName(f.Path, f.Header), tenant: tenant, done: NewObject()})
	}
	return l, nil
}

// -- what is there -----------------------------------------------------------

func (l *Loader) summary(tenant string) (empty, known bool, err error) {
	status, body, err := l.client.Request("GET", fullPath("/api/v1", "/tenant-summary", true, tenant), Req{})
	if err != nil {
		return false, false, err
	}
	if obj, ok := body.(*Object); ok && status == 200 {
		if e, isBool := obj.Get("empty").(bool); isBool {
			return e, true, nil
		}
	}
	return false, false, nil
}

func (l *Loader) loads(tenant string) ([]*Object, error) {
	status, body, err := l.client.Request("GET", fullPath("/api/v1", "/"+ProvenanceEntity, true, tenant), Req{Query: Obj("per_page", int64(500))})
	if err != nil {
		return nil, err
	}
	rows := body
	if obj, ok := body.(*Object); ok {
		rows = obj.Get("data")
	}
	var out []*Object
	if list, ok := rows.([]any); ok && status == 200 {
		for _, r := range list {
			if row, isRow := r.(*Object); isRow {
				out = append(out, row)
			}
		}
	}
	return out, nil
}

// Inspect asks the instance what each tenant of the load holds.
func (l *Loader) Inspect() error {
	for _, f := range l.files {
		state, seen := l.tenants[f.tenant]
		if !seen {
			empty, known, err := l.summary(f.tenant)
			if err != nil {
				return err
			}
			state = &tenantState{empty: empty, known: known, fresh: empty}
			if !empty {
				if state.loads, err = l.loads(f.tenant); err != nil {
					return err
				}
			}
			l.tenants[f.tenant] = state
		}
		f.prior, f.done = nil, NewObject()
		for i := len(state.loads) - 1; i >= 0; i-- {
			if Equal(state.loads[i].Get("seed"), f.name) {
				f.prior = state.loads[i]
				break
			}
		}
		if done, ok := f.prior.Get("done").(*Object); ok {
			f.done = done.Copy()
		}
	}
	return nil
}

// AsOf is the reference time of the load: the one given; else the one this
// seed was first loaded with; else the zero time, which a Context takes as now.
func (l *Loader) AsOf(given time.Time) time.Time {
	if !given.IsZero() {
		return given
	}
	for _, f := range l.files {
		if text, ok := f.prior.Str("as_of"); ok {
			if t, err := time.Parse(timeLayout, text); err == nil {
				return t
			}
		}
	}
	return time.Time{}
}

func quoted(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "\"" + n + "\""
	}
	return strings.Join(out, ", ")
}

func (l *Loader) guard() error {
	for _, f := range l.files {
		state := l.tenants[f.tenant]
		scope := "the instance"
		if f.tenant != "" {
			scope = "tenant \"" + f.tenant + "\""
		}
		requires := Names(f.Header, "requires")
		loaded := map[string]bool{}
		for _, r := range state.loads {
			if status, _ := r.Str("status"); status == "loaded" {
				if name, ok := r.Str("seed"); ok {
					loaded[name] = true
				}
			}
		}
		for _, other := range l.files {
			if other.name != f.name {
				loaded[other.name] = true
			}
		}
		var missing []string
		for _, n := range requires {
			if !loaded[n] {
				missing = append(missing, n)
			}
		}
		if len(missing) > 0 {
			s, are, them := "", "is", "it"
			if len(missing) > 1 {
				s, are, them = "s", "are", "them"
			}
			return refusedf("seed \"%s\" adds to the seed%s %s, which %s not loaded in %s. Load %s first, or name both in one load.",
				f.name, s, quoted(missing), are, scope, them)
		}
		if state.empty || f.prior != nil {
			continue
		}
		if len(requires) > 0 || Truthy(f.Header.Get("addition")) || l.opts.AllowExisting {
			continue
		}
		if !state.known {
			return refusedf("cannot tell whether %s is empty (GET tenant-summary did not answer); "+
				"pass --allow-existing to load anyway", scope)
		}
		set := map[string]bool{}
		for _, r := range state.loads {
			set[Text(r.Get("seed"))] = true
		}
		others := sortedKeys(set)
		what := "holds data that no seed load accounts for"
		if len(others) > 0 {
			s := ""
			if len(others) > 1 {
				s = "s"
			}
			what = fmt.Sprintf("holds the seed%s %s", s, quoted(others))
		}
		return refusedf("%s already %s; seed \"%s\" was not loaded. Load into an empty tenant, or pass "+
			"--allow-existing to add to what is there.", strings.ToUpper(scope[:1])+scope[1:], what, f.name)
	}
	return nil
}

// -- provenance ----------------------------------------------------------------

func (l *Loader) recordStart(ctx *Context) error {
	for _, f := range l.files {
		row := Obj("seed", f.name, "digest", f.Digest, "format", f.Header.Get("seed"), "loader", LoaderName,
			"as_of", ctx.AsOfText(), "status", "loading", "mode", l.opts.Mode, "started_at", nowText(), "partial", l.opts.Partial)
		base := fullPath("/api/v1", "/"+ProvenanceEntity, true, f.tenant)
		var status int
		var body any
		var err error
		if f.prior.Has("id") {
			f.loadPath = base + "/" + Text(f.prior.Get("id"))
			status, body, err = l.client.Request("PATCH", f.loadPath, Req{Body: row})
		} else {
			status, body, err = l.client.Request("POST", base, Req{Body: row})
			f.loadPath = ""
			if obj, ok := body.(*Object); ok && obj.Has("id") {
				f.loadPath = base + "/" + Text(obj.Get("id"))
			}
			f.loadNew = true
		}
		if err != nil {
			return err
		}
		if status < 200 || status >= 300 {
			return errorf("cannot record the load in \"%s\": HTTP %d: %s", ProvenanceEntity, status, short(body))
		}
	}
	return nil
}

func (l *Loader) recordEnd(word string, stats *Object) {
	for _, f := range l.files {
		if f.loadPath != "" {
			// the load itself is over: an answer that does not come changes nothing that can still be done
			_, _, _ = l.client.Request("PATCH", f.loadPath, Req{Body: Obj("status", word, "finished_at", nowText(), "records", stats, "done", f.done)})
		}
	}
}

// -- the run -------------------------------------------------------------------

// schemaGuard: registering a schema over existing rows hides them. It skips one already there and refuses one over rows.
func (l *Loader) schemaGuard(step *Step) (outcome, message string, err error) {
	entity := step.GuardSchema
	status, _, err := l.client.Request("GET", "/api/v1/schema/"+Quote(entity), Req{})
	if err != nil {
		return "", "", err
	}
	if status == 200 {
		return "present", fmt.Sprintf("schema '%s' is already registered", entity), nil
	}
	if status != 404 {
		return "", "", errorf("cannot check whether a schema for '%s' exists: HTTP %d", entity, status)
	}
	path := fullPath("/api/v1", "/"+Quote(entity), true, step.Tenant)
	status, body, err := l.client.Request("GET", path, Req{Query: Obj("per_page", int64(1))})
	if err != nil {
		return "", "", err
	}
	rows := false
	switch {
	case status < 300:
		data := body
		if obj, ok := body.(*Object); ok {
			data = obj.Get("data")
			if page, isPage := obj.Get("pagination").(*Object); isPage {
				rows = Truthy(page.Get("total_items"))
			}
		}
		rows = rows || Truthy(data)
	case status != 404:
		return "", "", errorf("cannot check whether '%s' already has rows: GET %s -> HTTP %d", entity, path, status)
	}
	if rows {
		return "failed", fmt.Sprintf("%s: refusing to register a schema for '%s': the type already has rows and registering would "+
			"hide them. Promote it with POST /entities/promote/..., or pass --force-schemas", step.Where, entity), nil
	}
	return "", "", nil
}

// Undone is what an undo did.
type Undone struct {
	Complete bool
	Deleted  int
	Left     *Object // what could not be deleted: kind -> how many
	Why      string
}

// Result is what a load did.
type Result struct {
	OK       bool
	Stats    *Object // kind -> outcome -> how many
	Failures []string
	Stopped  bool
	Steps    int
	AsOf     string
	Mode     string
	Seeds    []string
	Fresh    bool
	Undone   *Undone
	Requests int
}

// JSON is the result as a value.
func (r *Result) JSON() *Object {
	var undone any
	if r.Undone != nil {
		undone = Obj("complete", r.Undone.Complete, "deleted", int64(r.Undone.Deleted), "left", r.Undone.Left, "why", r.Undone.Why)
	}
	return Obj("ok", r.OK, "stats", r.Stats, "failures", stringList(r.Failures), "stopped", r.Stopped, "steps", int64(r.Steps),
		"as_of", r.AsOf, "mode", r.Mode, "seeds", stringList(r.Seeds), "fresh", r.Fresh, "undone", undone, "requests", int64(r.Requests))
}

func stringList(items []string) []any {
	out := make([]any, len(items))
	for i, s := range items {
		out[i] = s
	}
	return out
}

type run struct {
	bindings map[string]any
	stats    *Object
}

func (r *run) tally(kind, outcome string) {
	counts, ok := r.stats.Get(kind).(*Object)
	if !ok {
		counts = NewObject()
		for _, o := range Outcomes {
			counts.Set(o, int64(0))
		}
		r.stats.Set(kind, counts)
	}
	n, _ := Int64(counts.Get(outcome))
	counts.Set(outcome, n+1)
}

func (r *run) interpolate(text, at string) (string, error) {
	var failed error
	out := boundRe.ReplaceAllStringFunc(text, func(found string) string {
		name := found[2 : len(found)-1]
		v, ok := r.bindings[name]
		if !ok && failed == nil {
			failed = errorf("%s: ${%s} has no binding yet", at, name)
		}
		return Text(v)
	})
	return out, failed
}

func (r *run) resolve(value any, at string) (any, error) {
	switch x := value.(type) {
	case *Object:
		if x.Len() == 1 && x.Has("$seed") {
			name := Text(x.Get("$seed"))
			v, ok := r.bindings[name]
			if !ok {
				return nil, errorf("%s: placeholder {\"$seed\": \"%s\"} has no binding yet", at, name)
			}
			return v, nil
		}
		if text, ok := x.Str("$text"); ok && x.Len() == 1 {
			var failed error
			out := seedInTextRe.ReplaceAllStringFunc(text, func(found string) string {
				name := seedInTextRe.FindStringSubmatch(found)[1]
				v, bound := r.bindings[name]
				if !bound && failed == nil {
					failed = errorf("%s: ${seed:%s} has no binding yet", at, name)
				}
				return Text(v)
			})
			return out, failed
		}
		out := NewObject()
		for _, k := range x.Keys() {
			v, err := r.resolve(x.Get(k), at)
			if err != nil {
				return nil, err
			}
			out.Set(k, v)
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			v, err := r.resolve(item, at)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	}
	return value, nil
}

func fieldList(fields []string, body any) string {
	obj, _ := body.(*Object)
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = f + "=" + Text(obj.Get(f))
	}
	return strings.Join(parts, ", ")
}

// Run loads the plan. It returns a *Refused error when a guard stops the load before any write.
func (l *Loader) Run(plan []*Step, ctx *Context) (*Result, error) {
	if len(l.tenants) == 0 {
		if err := l.Inspect(); err != nil {
			return nil, err
		}
	}
	if err := l.guard(); err != nil {
		return nil, err
	}
	if err := l.recordStart(ctx); err != nil {
		return nil, err
	}
	r := &run{bindings: map[string]any{}, stats: NewObject()}
	var failures, written []string
	kept := map[string]int{}
	present := map[string]bool{}
	doneOf := map[string]*Object{}
	for _, f := range l.files {
		doneOf[f.Path] = f.done
	}
	stopped := false
	// fail counts a record as failed and says whether the load stops there.
	fail := func(step *Step, message string) bool {
		r.tally(step.Kind, "failed")
		failures = append(failures, message)
		stopped = !l.opts.KeepGoing
		return stopped
	}

steps:
	for _, step := range plan {
		at := step.Where
		fresh := false
		if state, ok := l.tenants[step.Tenant]; ok {
			fresh = state.fresh
		}
		done := doneOf[step.File]
		if done == nil {
			done = NewObject()
		}
		if step.Tracked && len(step.Match) == 0 && done.Has(step.Key) {
			// the load record says this ran: it is not run twice, and what it bound is bound again.
			// A record with "match" is looked up instead, every time.
			if step.As != "" {
				r.bindings[step.As] = done.Get(step.Key)
			}
			r.tally(step.Kind, "present")
			continue
		}
		path, err := r.interpolate(step.URLPath(), at)
		var body, query any
		if err == nil && step.Body != nil {
			body, err = r.resolve(step.Body, at)
		}
		if err == nil && Truthy(step.Query) {
			query, err = r.resolve(step.Query, at)
		}
		if err != nil {
			if fail(step, err.Error()) {
				break
			}
			continue
		}

		if step.GuardSchema != "" && !l.opts.ForceSchemas {
			outcome, message, err := l.schemaGuard(step)
			if err != nil {
				return nil, err
			}
			if outcome == "failed" {
				if fail(step, message) {
					break
				}
				continue
			}
			if outcome != "" {
				r.tally(step.Kind, outcome)
				if l.opts.Verbose {
					l.opts.Say("  [present] " + message)
				}
				continue
			}
		}

		if len(step.Match) > 0 {
			found, err := Find(l.client, step, body)
			if err != nil {
				if fail(step, err.Error()) {
					break
				}
				continue
			}
			if step.Op == "find" {
				if found == nil {
					if fail(step, fmt.Sprintf("%s: nothing in %s has %s; this file adds to data that must be there first",
						at, step.Entity, fieldList(step.Match, body))) {
						break
					}
					continue
				}
				r.bindings[step.As] = found
				r.tally(step.Kind, "present")
				continue
			}
			if found != nil {
				outcome := "present"
				if step.Op == "match" && l.opts.Mode == "replace" {
					status, parsed, err := l.client.Request("POST", step.ItemPath+"/save/"+Text(found), Req{Body: body})
					if err != nil {
						return nil, err
					}
					outcome = "replaced"
					if status < 200 || status >= 300 {
						outcome = "failed"
						failures = append(failures, fmt.Sprintf("%s: %s: HTTP %d: %s", at, step.Label, status, short(parsed)))
					}
				}
				if step.As != "" {
					r.bindings[step.As] = found
				}
				done.Set(step.Key, found)
				r.tally(step.Kind, outcome)
				if outcome == "failed" && !l.opts.KeepGoing {
					stopped = true
					break steps
				}
				continue
			}
		}

		key := step.Tenant + "\x00" + step.Entity + "\x00" + Text(step.ID)
		existed := false
		if step.Op == "save" && !fresh {
			status, _, err := l.client.Request("GET", step.ReadPath, Req{})
			if err != nil {
				return nil, err
			}
			existed = status == 200
			if existed && l.opts.Mode == "create" {
				present[key] = true
				if step.As != "" {
					r.bindings[step.As] = step.ID
				}
				r.tally(step.Kind, "present")
				continue
			}
		}
		if step.Op == "patch" && present[key] {
			continue // the record was left as it is, and so are its references
		}

		status, parsed, err := l.client.Request(step.Method, path, Req{Body: body, Raw: step.Raw, HasRaw: step.HasRaw, Headers: step.Headers, Query: query})
		if err != nil {
			return nil, err
		}
		expected := false
		for _, e := range step.Expect {
			expected = expected || Equal(e, int64(status))
		}
		outcome := "failed"
		switch {
		case (status >= 200 && status < 300) || expected:
			outcome = "created"
			if existed {
				outcome = "replaced"
			}
			if step.Op == "patch" {
				outcome = "" // part of a record already counted
			}
		case step.ExistsOK && status == 409:
			outcome = "present"
		}

		if outcome != "failed" && step.As != "" {
			if step.Op == "save" {
				r.bindings[step.As] = step.ID
			} else if v, ok := dig(parsed, Text(step.Bind)); ok {
				r.bindings[step.As] = v
			} else {
				outcome = "failed"
				parsed = fmt.Sprintf("response has no \"%s\" to bind as \"%s\" (got %s)", Text(step.Bind), step.As, short(parsed))
			}
		}
		answer, _ := parsed.(*Object)
		if outcome == "created" {
			switch {
			case step.Op == "save":
				written = append(written, step.ItemPath+"/"+Text(step.ID))
			case (step.Op == "create" || step.Op == "match") && answer.Has("id"):
				written = append(written, step.ItemPath+"/"+Text(answer.Get("id")))
			case step.Op == "call":
				kept[step.Kind]++
			}
		}
		if step.Tracked && (outcome == "created" || outcome == "present") {
			var mark any = true
			if step.As != "" {
				if v, ok := r.bindings[step.As]; ok {
					mark = v
				}
			} else if len(step.Match) > 0 && answer.Has("id") {
				mark = answer.Get("id")
			}
			done.Set(step.Key, mark)
		}
		if outcome != "" {
			r.tally(step.Kind, outcome)
		}
		if l.opts.Verbose || outcome == "failed" {
			word := outcome
			if word == "" {
				word = "refs"
			}
			l.opts.Say(fmt.Sprintf("  [%s] %s %s -> %d", word, step.Method, path, status))
		}
		if outcome == "failed" {
			hint := ""
			if status == 404 && strings.Contains(path, "/api/v2/") {
				hint = v2Hint
			}
			failures = append(failures, fmt.Sprintf("%s: %s: HTTP %d: %s%s", at, step.Label, status, short(parsed), hint))
			if !l.opts.KeepGoing {
				stopped = true
				break
			}
		}
	}

	res := &Result{OK: len(failures) == 0, Stats: r.stats, Failures: failures, Stopped: stopped, Steps: len(plan),
		AsOf: ctx.AsOfText(), Mode: l.opts.Mode, Fresh: true, Requests: l.client.Requests}
	for _, f := range l.files {
		res.Seeds = append(res.Seeds, f.name)
	}
	for _, t := range l.tenants {
		res.Fresh = res.Fresh && t.fresh
	}
	if len(failures) > 0 && l.opts.UndoOnFailure {
		res.Undone = l.undo(written, kept)
	}
	if !(len(failures) > 0 && res.Undone != nil && res.Undone.Complete) {
		word := "loaded"
		if len(failures) > 0 {
			word = "failed"
		}
		l.recordEnd(word, r.stats)
	}
	res.Requests = l.client.Requests
	return res, nil
}

// undo deletes the entities this run created. Records that existed before it are not touched.
func (l *Loader) undo(written []string, kept map[string]int) *Undone {
	deleted, failed := 0, 0
	for i := len(written) - 1; i >= 0; i-- {
		status, _, err := l.client.Request("DELETE", written[i], Req{})
		if err == nil && ((status >= 200 && status < 300) || status == 404) {
			deleted++
		} else {
			failed++
		}
	}
	for _, f := range l.files {
		if f.loadNew && f.loadPath != "" {
			_, _, _ = l.client.Request("DELETE", f.loadPath, Req{}) // best effort: the record of a load that left nothing
		}
	}
	if failed > 0 {
		kept["entity"] = failed
	}
	names := make([]string, 0, len(kept))
	for k := range kept {
		names = append(names, k)
	}
	sort.Strings(names)
	left := NewObject()
	var parts []string
	for _, k := range names {
		left.Set(k, int64(kept[k]))
		parts = append(parts, fmt.Sprintf("%d %s", kept[k], k))
	}
	u := &Undone{Complete: len(names) == 0, Deleted: deleted, Left: left}
	if !u.Complete {
		u.Why = "these cannot be deleted through the API and were left: " + strings.Join(parts, ", ")
	}
	return u
}
