// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Command xoseed loads, checks and dumps .seed files for xolu.
//
// It is a front to pkg/seed, which is the format and its semantics; the
// format itself is in docs/SEED_FORMAT.md. The commands, their options and
// what they print are those of the Python loader (scripts/seed.py).
//
// Exit status: 0 done; 1 a record failed, or check found differences; 2 bad
// input or usage; 3 a guard refused the load before anything was written.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ha1tch/xolu/pkg/seed"
)

const version = "1.0.0"

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// say and sayln write to where the command writes. A write that fails (a
// closed pipe) has nowhere to be reported, and the exit status is what counts.
func say(w io.Writer, format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }

func sayln(w io.Writer, text string) { _, _ = fmt.Fprintln(w, text) }

// -- options -------------------------------------------------------------------

type option struct {
	names []string // "--long" and "-s" forms
	value string   // the name of its value in the help; "" for a switch
	help  string
}

type command struct {
	name    string
	summary string
	seeds   string // "", "*" (any number) or "+" (one or more)
	options []option
	run     func(*app) (int, error)
}

func common(server, seeds bool) []option {
	out := []option{
		{[]string{"--seeds-dir"}, "DIR", "where seeds are looked for by name (default: XOLU_SEEDS_DIR, else ./seeds, else this directory)"},
		{[]string{"--json"}, "", "print the result as JSON"},
		{[]string{"--lenient"}, "", "warn about unknown kinds and keys instead of stopping"},
	}
	if seeds {
		out = append(out,
			option{[]string{"--tenant"}, "TENANT", "override the tenant named in each seed header"},
			option{[]string{"--as-of"}, "WHEN", "reference day or time for {\"$date\"} and {\"$now\"} (default: the recorded load, else now)"},
			option{[]string{"--set"}, "NAME=VALUE", "give a variable its value; repeat for more"},
			option{[]string{"--only"}, "KINDS", "comma list of kinds to use (" + strings.Join(seed.AllKinds()[:4], ", ") + ", ...)"},
			option{[]string{"--skip"}, "KINDS", "comma list of kinds to leave out"},
			option{[]string{"--safe"}, "", "refuse 'call' and 'config' records (for a file from a source that is not trusted)"},
			option{[]string{"--allow-outside-files"}, "", "let a body_file point outside the seed's directory"})
	}
	if server {
		out = append(out,
			option{[]string{"--url", "--base-url"}, "URL", "xolu's address (default: XOLU_BASE_URL, else XOLU_HOST/XOLU_PORT/XOLU_ADDR, else http://127.0.0.1:9090)"},
			option{[]string{"--api-key"}, "KEY", "sent as X-API-Key (default: XOLU_API_KEY)"},
			option{[]string{"--token"}, "TOKEN", "sent as a Bearer token (default: XOLU_TOKEN)"},
			option{[]string{"--wait"}, "SECS", "wait up to SECS for /health first (default 15; 0 to skip)"},
			option{[]string{"--timeout"}, "SECS", "per-request timeout (default 30)"})
	}
	return out
}

func commands() []command {
	return []command{
		{"load", "load seeds into a tenant", "*", append(common(true, true),
			option{[]string{"--all"}, "", "load every .seed file in the seeds directory"},
			option{[]string{"--replace"}, "", "write again the records that already exist (default: leave them alone)"},
			option{[]string{"--allow-existing"}, "", "load into a tenant that holds other data"},
			option{[]string{"--undo-on-failure"}, "", "if a record fails and the tenant was empty, delete what this run created"},
			option{[]string{"--keep-going"}, "", "continue after a failed record"},
			option{[]string{"--force-schemas"}, "", "register schemas without checking for a registered one or for rows"},
			option{[]string{"--dry-run"}, "", "print the requests that would be made; contact nothing"},
			option{[]string{"--verbose", "-v"}, "", "print every request"}), cmdLoad},
		{"check", "compare a tenant with seeds; write nothing", "+", append(common(true, true),
			option{[]string{"--show"}, "N", "how many of each kind of difference to print (default 20)"}), cmdCheck},
		{"dump", "write what a tenant holds as a .seed file", "", append(common(true, false),
			option{[]string{"--tenant"}, "TENANT", "the tenant to read"},
			option{[]string{"--name"}, "NAME", "the name for the header (default: dump)"},
			option{[]string{"--description"}, "TEXT", "a description for the header"},
			option{[]string{"--entities"}, "TYPES", "comma list of entity types (default: all of them)"},
			option{[]string{"--output", "-o"}, "FILE", "file to write (default: standard output)"}), cmdDump},
		{"list", "list the seeds in the seeds directory", "", common(false, false), cmdList},
		{"info", "show a seed's header and what it holds", "+", common(false, false), cmdInfo},
		{"plan", "print the requests of a load as JSON; contact nothing", "+", common(false, true), cmdPlan},
	}
}

// app is one run of one command: its options, and where it writes.
type app struct {
	cmd    command
	flags  map[string]bool
	values map[string]string
	sets   []string
	seeds  []string
	out    io.Writer
	err    io.Writer
}

func (a *app) value(name, fallback string) string {
	if v, ok := a.values[name]; ok {
		return v
	}
	return fallback
}

type usageError struct{ message string }

func (e *usageError) Error() string { return e.message }

func usagef(format string, args ...any) error { return &usageError{fmt.Sprintf(format, args...)} }

// find returns the option a name on the command line means: the name itself, or the one long name it starts.
func (c command) find(name string) (*option, error) {
	var starts []*option
	for i := range c.options {
		for _, n := range c.options[i].names {
			if n == name {
				return &c.options[i], nil
			}
			if strings.HasPrefix(name, "--") && strings.HasPrefix(n, name) {
				starts = append(starts, &c.options[i])
				break
			}
		}
	}
	switch len(starts) {
	case 1:
		return starts[0], nil
	case 0:
		return nil, usagef("unrecognized arguments: %s", name)
	}
	return nil, usagef("ambiguous option: %s could match %s, %s", name, starts[0].names[0], starts[1].names[0])
}

var errHelp = errors.New("help")

func (a *app) parse(args []string) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			a.seeds = append(a.seeds, args[i+1:]...)
			break
		}
		if arg == "-h" || arg == "--help" {
			return errHelp
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			a.seeds = append(a.seeds, arg)
			continue
		}
		name, given, hasValue := strings.Cut(arg, "=")
		if !strings.HasPrefix(arg, "--") {
			name, given, hasValue = arg[:2], arg[2:], len(arg) > 2 // -oFILE
		}
		opt, err := a.cmd.find(name)
		if err != nil {
			return err
		}
		dest := opt.names[0]
		if opt.value == "" {
			if hasValue {
				return usagef("argument %s: ignored explicit argument '%s'", strings.Join(opt.names, "/"), given)
			}
			a.flags[dest] = true
			continue
		}
		if !hasValue {
			if i+1 >= len(args) {
				return usagef("argument %s: expected one argument", strings.Join(opt.names, "/"))
			}
			i++
			given = args[i]
		}
		if dest == "--set" {
			a.sets = append(a.sets, given)
		} else {
			a.values[dest] = given
		}
	}
	if a.cmd.seeds == "" && len(a.seeds) > 0 {
		return usagef("unrecognized arguments: %s", strings.Join(a.seeds, " "))
	}
	if a.cmd.seeds == "+" && len(a.seeds) == 0 {
		return usagef("the following arguments are required: SEED")
	}
	return nil
}

func (a *app) seconds(name string, fallback float64) (time.Duration, error) {
	text, ok := a.values[name]
	if !ok {
		return time.Duration(fallback * float64(time.Second)), nil
	}
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || f < 0 || f > 1e9 {
		return 0, usagef("argument %s: invalid float value: '%s'", name, text)
	}
	return time.Duration(f * float64(time.Second)), nil
}

func usage(w io.Writer) {
	sayln(w, "usage: xoseed [-h] [--version] COMMAND ...")
	sayln(w, "\nLoad, check and dump .seed files for xolu (format in docs/SEED_FORMAT.md).")
	sayln(w, "\ncommands:")
	for _, c := range commands() {
		say(w, "  %-8s %s\n", c.name, c.summary)
	}
	sayln(w, "\nxoseed COMMAND --help shows the options of a command.")
}

func (c command) usageLine() string {
	line := "usage: xoseed " + c.name + " [options]"
	switch c.seeds {
	case "*":
		line += " [SEED ...]"
	case "+":
		line += " SEED [SEED ...]"
	}
	return line
}

func (c command) help(w io.Writer) {
	sayln(w, c.usageLine())
	sayln(w, "\n"+c.summary)
	if c.seeds != "" {
		sayln(w, "\n  SEED                    a path, or a name in the seeds directory")
	}
	sayln(w, "\noptions:")
	for _, o := range c.options {
		names := append([]string{}, o.names...)
		sort.SliceStable(names, func(i, j int) bool { return len(names[i]) < len(names[j]) })
		left := strings.Join(names, ", ")
		if o.value != "" {
			left += " " + o.value
		}
		say(w, "  %-23s %s\n", left, o.help)
	}
}

func run(argv []string, stdout, stderr io.Writer) int {
	if len(argv) == 0 {
		usage(stdout)
		return 2
	}
	switch argv[0] {
	case "-h", "--help":
		usage(stdout)
		return 0
	case "--version":
		sayln(stdout, "xoseed "+version)
		return 0
	}
	for _, c := range commands() {
		if c.name != argv[0] {
			continue
		}
		a := &app{cmd: c, flags: map[string]bool{}, values: map[string]string{}, out: stdout, err: stderr}
		err := a.parse(argv[1:])
		if errors.Is(err, errHelp) {
			c.help(stdout)
			return 0
		}
		code := 0
		if err == nil {
			code, err = c.run(a)
		}
		var refused *seed.Refused
		var bad *seed.Error
		var wrong *usageError
		switch {
		case err == nil:
			return code
		case errors.As(err, &wrong):
			sayln(stderr, c.usageLine())
			say(stderr, "xoseed %s: error: %s\n", c.name, wrong.message)
			return 2
		case errors.As(err, &refused):
			say(stderr, "refused: %s\n", refused.Message)
			return 3
		case errors.As(err, &bad):
			say(stderr, "error: %s\n", bad.Message)
			return 2
		}
		say(stderr, "error: %s\n", err)
		return 2
	}
	sayln(stderr, "usage: xoseed [-h] [--version] COMMAND ...")
	say(stderr, "xoseed: error: argument COMMAND: invalid choice: '%s' (choose from load, check, dump, list, info, plan)\n", argv[0])
	return 2
}

// -- what the commands share ---------------------------------------------------

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func isFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func (a *app) seedsDir() string {
	if dir := a.value("--seeds-dir", os.Getenv("XOLU_SEEDS_DIR")); dir != "" {
		return dir
	}
	if isDir("seeds") {
		return "seeds"
	}
	return "."
}

func allSeeds(dir string) ([]string, error) {
	if !isDir(dir) {
		return nil, &seed.Error{Message: "seeds directory not found: " + dir}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, &seed.Error{Message: fmt.Sprintf("cannot read the seeds directory %s: %s", dir, err)}
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".seed") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// subdirs: additions are kept by module in directories of their own, seeds/issues/seed-NAME.seed.
func subdirs(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var out []string
	for _, e := range entries {
		name := e.Name()
		if name == "seed_system" || name == "conformance" || name == "tests" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		if isDir(filepath.Join(dir, name)) {
			out = append(out, filepath.Join(dir, name))
		}
	}
	sort.Strings(out)
	return out
}

func (a *app) resolve() ([]string, error) {
	dir := a.seedsDir()
	var paths []string
	for _, item := range a.seeds {
		cands := []string{item, filepath.Join(dir, item), filepath.Join(dir, item+".seed"), filepath.Join(dir, "seed-"+item+".seed")}
		for _, sub := range subdirs(dir) {
			cands = append(cands, filepath.Join(sub, "seed-"+item+".seed"))
		}
		found := ""
		for _, cand := range cands {
			if isFile(cand) {
				found = cand
				break
			}
		}
		if found == "" {
			return nil, &seed.Error{Message: fmt.Sprintf("seed '%s' not found (looked for it as a path and in %s)", item, dir)}
		}
		paths = append(paths, found)
	}
	return paths, nil
}

type seedFile struct {
	path    string
	header  *seed.Object
	records []*seed.Object
	digest  string
}

func (a *app) warn(message string) { sayln(a.err, "warning: "+message) }

func (a *app) read(paths []string) ([]seedFile, error) {
	var files []seedFile
	var whole []string
	for _, path := range paths {
		header, records, digest, err := seed.ReadSeed(path, a.flags["--lenient"], a.warn)
		if err != nil {
			return nil, err
		}
		files = append(files, seedFile{path, header, records, digest})
		if seed.Truthy(header.Get("exclusive")) {
			whole = append(whole, seed.SeedName(path, header))
		}
	}
	if len(whole) > 1 {
		return nil, &seed.Error{Message: fmt.Sprintf("seeds \"%s\" and \"%s\" are each a whole dataset (\"exclusive\": true) and cannot be loaded together", whole[0], whole[1])}
	}
	return files, nil
}

func (a *app) variables() (map[string]any, error) {
	out := map[string]any{}
	for _, item := range a.sets {
		name, value, ok := strings.Cut(item, "=")
		if !ok || name == "" {
			return nil, &seed.Error{Message: fmt.Sprintf("--set: \"%s\" is not NAME=VALUE", item)}
		}
		if parsed, err := seed.ParseJSON(value); err == nil {
			out[name] = parsed
		} else {
			out[name] = value
		}
	}
	return out, nil
}

func (a *app) client() (*seed.Client, time.Duration, error) {
	wait, err := a.seconds("--wait", 15)
	if err != nil {
		return nil, 0, err
	}
	timeout, err := a.seconds("--timeout", 30)
	if err != nil {
		return nil, 0, err
	}
	c, err := seed.NewClient(a.value("--url", seed.DefaultBaseURL()), a.value("--api-key", os.Getenv("XOLU_API_KEY")),
		a.value("--token", os.Getenv("XOLU_TOKEN")), timeout)
	if err != nil {
		return nil, 0, err
	}
	if wait > 0 {
		if err := c.WaitReady(wait); err != nil {
			return nil, 0, err
		}
	}
	return c, wait, nil
}

func kindList(text string) []string {
	var out []string
	for _, k := range strings.Split(text, ",") {
		if k != "" {
			out = append(out, k)
		}
	}
	return out
}

func (a *app) plan(files []seedFile, ctx *seed.Context) ([]*seed.Step, error) {
	in := make([]seed.SeedFile, len(files))
	for i, f := range files {
		in[i] = seed.SeedFile{Path: f.path, Header: f.header, Records: f.records}
	}
	return seed.BuildPlan(in, ctx, seed.PlanOptions{Tenant: a.value("--tenant", ""), Only: kindList(a.value("--only", "")),
		Skip: kindList(a.value("--skip", "")), Lenient: a.flags["--lenient"], Safe: a.flags["--safe"],
		OutsideFiles: a.flags["--allow-outside-files"], Warn: a.warn})
}

func (a *app) asOf() (time.Time, error) {
	if text := a.value("--as-of", ""); text != "" {
		return seed.ParseAsOf(text)
	}
	return time.Time{}, nil
}

func loadFiles(files []seedFile) []seed.LoadFile {
	out := make([]seed.LoadFile, len(files))
	for i, f := range files {
		out[i] = seed.LoadFile{Path: f.path, Header: f.header, Digest: f.digest}
	}
	return out
}

func counts(stats *seed.Object) []string {
	kinds := append([]string{}, stats.Keys()...)
	sort.Strings(kinds)
	var lines []string
	for _, kind := range kinds {
		of, _ := stats.Get(kind).(*seed.Object)
		var parts []string
		for _, o := range seed.Outcomes {
			if seed.Truthy(of.Get(o)) {
				parts = append(parts, o+"="+seed.Text(of.Get(o)))
			}
		}
		text := strings.Join(parts, " ")
		if text == "" {
			text = "nothing"
		}
		lines = append(lines, fmt.Sprintf("  %-14s %s", kind, text))
	}
	return lines
}

func list(items []any) []any {
	if items == nil {
		return []any{}
	}
	return items
}

// -- the commands --------------------------------------------------------------

func cmdList(a *app) (int, error) {
	dir := a.seedsDir()
	paths, err := allSeeds(dir)
	if err != nil {
		return 0, err
	}
	for _, sub := range subdirs(dir) {
		more, err := allSeeds(sub)
		if err != nil {
			return 0, err
		}
		paths = append(paths, more...)
	}
	var rows []any
	for _, path := range paths {
		rel, rerr := filepath.Rel(dir, path)
		if rerr != nil {
			rel = path
		}
		header, records, _, err := seed.ReadSeed(path, true, nil)
		if err != nil {
			rows = append(rows, seed.Obj("file", rel, "error", err.Error()))
			continue
		}
		var description any = ""
		if header.Has("description") {
			description = header.Get("description")
		}
		rows = append(rows, seed.Obj("file", rel, "name", seed.SeedName(path, header), "format", header.Get("seed"),
			"records", int64(len(records)), "description", description))
	}
	if a.flags["--json"] {
		sayln(a.out, seed.DumpsIndent(list(rows)))
		return 0, nil
	}
	if len(rows) == 0 {
		say(a.out, "no .seed files in %s\n", dir)
	}
	for _, r := range rows {
		row := r.(*seed.Object)
		text := seed.Text(row.Get("error"))
		if !seed.Truthy(row.Get("error")) {
			n, _ := seed.Int64(row.Get("records"))
			text = fmt.Sprintf("%5d records  %s", n, seed.Text(row.Get("description")))
		}
		say(a.out, "%-46s %s\n", seed.Text(row.Get("file")), text)
	}
	return 0, nil
}

func cmdInfo(a *app) (int, error) {
	paths, err := a.resolve()
	if err != nil {
		return 0, err
	}
	files, err := a.read(paths)
	if err != nil {
		return 0, err
	}
	var out []any
	for _, f := range files {
		kinds := seed.NewObject()
		for _, rec := range f.records {
			label := seed.Text(rec.Get("kind"))
			if entity, ok := rec.Str("entity"); ok && (label == "entity" || label == "schema") {
				label += ":" + entity
			}
			n, _ := seed.Int64(kinds.Get(label))
			kinds.Set(label, n+1)
		}
		header := seed.NewObject()
		for _, k := range f.header.Keys() {
			if !strings.HasPrefix(k, "_") {
				header.Set(k, f.header.Get(k))
			}
		}
		out = append(out, seed.Obj("file", f.path, "name", seed.SeedName(f.path, f.header), "digest", f.digest,
			"records", int64(len(f.records)), "counts", kinds, "header", header))
	}
	if a.flags["--json"] {
		if len(out) == 1 {
			sayln(a.out, seed.DumpsIndent(out[0]))
		} else {
			sayln(a.out, seed.DumpsIndent(out))
		}
		return 0, nil
	}
	for _, o := range out {
		item := o.(*seed.Object)
		header := item.Get("header").(*seed.Object)
		say(a.out, "%s  (%s)\n", seed.Text(item.Get("name")), seed.Text(item.Get("file")))
		for _, key := range []string{"description", "version", "requires_xolu", "tenant", "exclusive"} {
			if header.Has(key) {
				say(a.out, "  %-14s %s\n", key, seed.Text(header.Get(key)))
			}
		}
		say(a.out, "  %-14s %s\n", "records", seed.Text(item.Get("records")))
		kinds := item.Get("counts").(*seed.Object)
		labels := append([]string{}, kinds.Keys()...)
		sort.Strings(labels)
		for _, label := range labels {
			say(a.out, "    %-28s %s\n", label, seed.Text(kinds.Get(label)))
		}
	}
	return 0, nil
}

func firstChars(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func cmdLoad(a *app) (int, error) {
	if len(a.seeds) == 0 {
		if !a.flags["--all"] {
			sayln(a.err, "Name the seed to load, or pass --all to load every file in the directory. Seeds here:")
			if _, err := cmdList(a); err != nil {
				return 0, err
			}
			return 2, nil
		}
		every, err := allSeeds(a.seedsDir())
		if err != nil {
			return 0, err
		}
		a.seeds = every
	}
	paths, err := a.resolve()
	if err != nil {
		return 0, err
	}
	files, err := a.read(paths)
	if err != nil {
		return 0, err
	}
	given, err := a.asOf()
	if err != nil {
		return 0, err
	}
	variables, err := a.variables()
	if err != nil {
		return 0, err
	}

	if a.flags["--dry-run"] {
		ctx := seed.NewContext(given, variables)
		plan, err := a.plan(files, ctx)
		if err != nil {
			return 0, err
		}
		for _, step := range plan {
			body := ""
			if step.Body != nil {
				body = "  " + firstChars(seed.Dumps(step.Body), 200)
			}
			say(a.out, "%-6s %s%s\n", step.Method, step.URLPath(), body)
		}
		say(a.out, "%d requests would be made (reference time %s); nothing was contacted\n", len(plan), ctx.AsOfText())
		return 0, nil
	}

	c, _, err := a.client()
	if err != nil {
		return 0, err
	}
	mode := "create"
	if a.flags["--replace"] {
		mode = "replace"
	}
	loader, err := seed.NewLoader(c, loadFiles(files), seed.Options{Tenant: a.value("--tenant", ""), Mode: mode,
		AllowExisting: a.flags["--allow-existing"], UndoOnFailure: a.flags["--undo-on-failure"], KeepGoing: a.flags["--keep-going"],
		ForceSchemas: a.flags["--force-schemas"], Verbose: a.flags["--verbose"],
		Partial: a.value("--only", "") != "" || a.value("--skip", "") != "", Say: func(m string) { sayln(a.err, m) }})
	if err != nil {
		return 0, err
	}
	if err := loader.Inspect(); err != nil {
		return 0, err
	}
	ctx := seed.NewContext(loader.AsOf(given), variables)
	plan, err := a.plan(files, ctx)
	if err != nil {
		return 0, err
	}
	result, err := loader.Run(plan, ctx)
	if err != nil {
		return 0, err
	}

	code := 1
	if result.OK {
		code = 0
	}
	if a.flags["--json"] {
		sayln(a.out, seed.DumpsIndent(result.JSON()))
		return code, nil
	}
	word := "NOT fully loaded"
	if result.OK {
		word = "loaded"
	}
	say(a.out, "%s: %s\n", strings.Join(result.Seeds, ", "), word)
	sayln(a.out, strings.Join(counts(result.Stats), "\n"))
	say(a.out, "  reference time %s, mode %s, %d requests\n", result.AsOf, result.Mode, result.Requests)
	if len(result.Failures) > 0 {
		say(a.err, "\n%d failure(s):\n", len(result.Failures))
		for _, f := range result.Failures {
			sayln(a.err, "  "+f)
		}
		if result.Undone != nil {
			say(a.err, "undo: %d record(s) deleted. %s\n", result.Undone.Deleted, result.Undone.Why)
		} else {
			sayln(a.err, "What was written stays. Fix the cause and run the same command again: records that exist are left "+
				"alone and the rest are loaded.")
		}
		return code, nil
	}
	for _, f := range files {
		if seed.Truthy(f.header.Get("notes")) {
			sayln(a.out, "\n"+seed.Text(f.header.Get("notes")))
		}
	}
	return code, nil
}

func cmdCheck(a *app) (int, error) {
	paths, err := a.resolve()
	if err != nil {
		return 0, err
	}
	files, err := a.read(paths)
	if err != nil {
		return 0, err
	}
	show := 20
	if text, ok := a.values["--show"]; ok {
		if show, err = strconv.Atoi(text); err != nil {
			return 0, usagef("argument --show: invalid int value: '%s'", text)
		}
	}
	c, _, err := a.client()
	if err != nil {
		return 0, err
	}
	loader, err := seed.NewLoader(c, loadFiles(files), seed.Options{Tenant: a.value("--tenant", "")})
	if err != nil {
		return 0, err
	}
	if err := loader.Inspect(); err != nil {
		return 0, err
	}
	given, err := a.asOf()
	if err != nil {
		return 0, err
	}
	asOf := loader.AsOf(given)
	variables, err := a.variables()
	if err != nil {
		return 0, err
	}
	ctx := seed.NewContext(asOf, variables)
	plan, err := a.plan(files, ctx)
	if err != nil {
		return 0, err
	}
	result, err := seed.Check(c, plan)
	if err != nil {
		return 0, err
	}
	from := "now (no load of this seed is recorded)"
	switch {
	case !given.IsZero():
		from = "the command line"
	case !asOf.IsZero():
		from = "the recorded load"
	}
	code := 1
	if result.Same {
		code = 0
	}
	if a.flags["--json"] {
		out := result.JSON()
		out.Set("as_of", ctx.AsOfText())
		out.Set("as_of_from", from)
		sayln(a.out, seed.DumpsIndent(out))
		return code, nil
	}
	upTo := func(n int) int {
		if show < 0 {
			show = 0
		}
		if n > show {
			return show
		}
		return n
	}
	say(a.out, "%d record(s) compared, reference time %s (from %s)\n", result.Compared, ctx.AsOfText(), from)
	if result.Unchecked > 0 {
		say(a.out, "%d record(s) without an id were not compared\n", result.Unchecked)
	}
	for _, label := range result.Missing[:upTo(len(result.Missing))] {
		say(a.out, "  missing    %s\n", label)
	}
	for _, d := range result.Different[:upTo(len(result.Different))] {
		say(a.out, "  different  %s: %s\n", d.Record, strings.Join(d.Fields, ", "))
	}
	for _, label := range result.Extra[:upTo(len(result.Extra))] {
		say(a.out, "  extra      %s\n", label)
	}
	if result.Same {
		sayln(a.out, "same")
	} else {
		say(a.out, "%d missing, %d different, %d extra\n", len(result.Missing), len(result.Different), len(result.Extra))
	}
	return code, nil
}

func cmdDump(a *app) (int, error) {
	c, _, err := a.client()
	if err != nil {
		return 0, err
	}
	text, total, err := seed.Dump(c, a.value("--name", "dump"), seed.DumpOptions{Tenant: a.value("--tenant", ""),
		Entities: kindList(a.value("--entities", "")), Description: a.value("--description", "")})
	if err != nil {
		return 0, err
	}
	if output := a.value("--output", "-"); output != "-" {
		if err := os.WriteFile(output, []byte(text), 0o644); err != nil { // #nosec G306 -- a seed file is meant to be read
			return 0, &seed.Error{Message: fmt.Sprintf("cannot write %s: %s", output, err)}
		}
		say(a.err, "%d record(s) written to %s\n", total, output)
		return 0, nil
	}
	say(a.out, "%s", text)
	return 0, nil
}

// cmdPlan prints the plan of a load, every step in full: what the two
// implementations of the format are compared by (scripts/seed_conformance).
func cmdPlan(a *app) (int, error) {
	paths, err := a.resolve()
	if err != nil {
		return 0, err
	}
	files, err := a.read(paths)
	if err != nil {
		return 0, err
	}
	given, err := a.asOf()
	if err != nil {
		return 0, err
	}
	variables, err := a.variables()
	if err != nil {
		return 0, err
	}
	ctx := seed.NewContext(given, variables)
	plan, err := a.plan(files, ctx)
	if err != nil {
		return 0, err
	}
	steps := make([]any, len(plan))
	for i, step := range plan {
		steps[i] = step.JSON()
	}
	sayln(a.out, seed.DumpsIndent(seed.Obj("as_of", ctx.AsOfText(), "steps", steps)))
	return 0, nil
}
