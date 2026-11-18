// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package seed

// format.go -- the .seed file: reading it, what its keys may be, and its
// placeholders. Nothing here talks to a server. docs/SEED_FORMAT.md is the
// specification; scripts/seed_system/format.py is the reference this follows.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// FormatVersions are the versions of the format this package reads.
var FormatVersions = []int64{1, 2}

// SystemFields are the fields the server owns in every stored record.
var SystemFields = []string{"id", "_version"}

var (
	headerKeysV1 = []string{"seed", "name", "tenant"}
	headerKeys   = append(append([]string{}, headerKeysV1...), "description", "tags", "version", "requires_xolu",
		"entities", "vars", "exclusive", "addition", "requires", "notes", "x")
	// commonKeys are the keys any record may carry, whatever its kind.
	commonKeys     = []string{"kind", "as", "bind", "phase", "expect", "x"}
	placeholders   = []string{"$seed", "$date", "$now", "$var", "$text"}
	v2Placeholders = []string{"$date", "$now", "$var", "$text"}
)

// Error is bad input: a file, an option or an answer that cannot be used.
// The command reports it without more and ends with status 2.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// Refused is a guard that stopped a load before anything was written.
// The command ends with status 3.
type Refused struct{ Message string }

func (e *Refused) Error() string { return e.Message }

func errorf(format string, args ...any) error { return &Error{fmt.Sprintf(format, args...)} }

func refusedf(format string, args ...any) error { return &Refused{fmt.Sprintf(format, args...)} }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func where(path string, line int64) string { return fmt.Sprintf("%s:%d", path, line) }

// File and Line say where a record (or a header) was read from.
func File(rec *Object) string { s, _ := rec.Str("_file"); return s }

// Line is the number of the line a record starts on.
func Line(rec *Object) int64 { n, _ := Int64(rec.Get("_line")); return n }

func recWhere(rec *Object) string { return where(File(rec), Line(rec)) }

// splitLines breaks text where a line ends: after "\n", "\r", "\r\n" and the
// other characters the reference implementation takes as the end of a line.
func splitLines(text string) []string {
	var out []string
	rs := []rune(text)
	start := 0
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '\r':
			out = append(out, string(rs[start:i]))
			if i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			start = i + 1
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			out = append(out, string(rs[start:i]))
			start = i + 1
		}
	}
	if start < len(rs) {
		out = append(out, string(rs[start:]))
	}
	return out
}

type chunk struct {
	line   int64
	text   string
	spans  bool
	closed bool
}

// ReadSeed returns the header, the records and the digest of one .seed file.
//
// A record is one JSON object. It starts in the first column and may continue
// on following lines that start with white space (version 2). Blank lines and
// lines starting with '#' separate records and are otherwise ignored. Each
// record gets "_file" and "_line". With lenient set, a header key the format
// does not define is passed to warn instead of being an error.
func ReadSeed(path string, lenient bool, warn func(string)) (header *Object, records []*Object, digest string, err error) {
	if warn == nil {
		warn = func(string) {}
	}
	raw, rerr := os.ReadFile(path) // #nosec G304 -- reading the file it was asked to read is what a loader does
	if rerr != nil {
		return nil, nil, "", errorf("cannot read %s: %s", path, rerr)
	}
	sum := sha256.Sum256(raw)
	digest = hex.EncodeToString(sum[:])
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	if !utf8.Valid(raw) {
		return nil, nil, "", errorf("%s: not UTF-8 text", path)
	}

	var chunks []*chunk
	for i, line := range splitLines(string(raw)) {
		lineno := int64(i) + 1
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if len(chunks) == 0 || chunks[len(chunks)-1].closed {
				return nil, nil, "", errorf("%s: a continuation line with no record before it", where(path, lineno))
			}
			last := chunks[len(chunks)-1]
			last.text += "\n" + line
			last.spans = true
			continue
		}
		if strings.HasPrefix(line, "#") {
			if len(chunks) > 0 {
				chunks[len(chunks)-1].closed = true // a comment closes the record before it
			}
			continue
		}
		chunks = append(chunks, &chunk{line: lineno, text: line})
	}

	multiline := false
	for _, c := range chunks {
		v, perr := ParseJSON(c.text)
		if perr != nil {
			return nil, nil, "", errorf("%s: invalid JSON: %s", where(path, c.line), perr)
		}
		obj, ok := v.(*Object)
		if !ok {
			return nil, nil, "", errorf("%s: each record must be a JSON object", where(path, c.line))
		}
		multiline = multiline || c.spans
		if header == nil {
			if !obj.Has("seed") {
				return nil, nil, "", errorf("%s: the first record must be the header, e.g. {\"seed\": 2, \"name\": \"demo\"}", where(path, c.line))
			}
			if _, isBool := obj.Get("seed").(bool); isBool || !(Equal(obj.Get("seed"), int64(1)) || Equal(obj.Get("seed"), int64(2))) {
				return nil, nil, "", errorf("%s: unsupported seed format version %s (this loader reads versions 1 and 2)",
					where(path, c.line), Repr(obj.Get("seed")))
			}
			header = obj
			header.Set("_file", path)
			header.Set("_line", c.line)
			continue
		}
		if !obj.Has("kind") {
			return nil, nil, "", errorf("%s: record has no \"kind\"", where(path, c.line))
		}
		obj.Set("_file", path)
		obj.Set("_line", c.line)
		records = append(records, obj)
	}
	if header == nil {
		return nil, nil, "", errorf("%s: empty seed file (no header record)", path)
	}

	v2 := Equal(header.Get("seed"), int64(2))
	allowed := headerKeysV1
	if v2 {
		allowed = headerKeys
	}
	for _, key := range header.Keys() {
		if strings.HasPrefix(key, "_") || contains(allowed, key) {
			continue
		}
		hint := ""
		if contains(headerKeys, key) {
			hint = " (a version 2 key: declare \"seed\": 2)"
		}
		message := fmt.Sprintf("%s: unknown header key \"%s\"%s", recWhere(header), key, hint)
		if !lenient {
			return nil, nil, "", &Error{message}
		}
		warn(message)
	}
	if !v2 && multiline {
		return nil, nil, "", errorf("%s: records that span lines need \"seed\": 2 in the header", path)
	}
	if err := checkHeaderTypes(header); err != nil {
		return nil, nil, "", err
	}
	if !v2 {
		for _, rec := range records {
			var used []string
			found := PlaceholdersIn(visible(rec))
			for _, p := range v2Placeholders {
				if found[p] {
					used = append(used, p)
				}
			}
			if len(used) > 0 {
				sort.Strings(used)
				return nil, nil, "", errorf("%s: %s needs \"seed\": 2 in the header", recWhere(rec), strings.Join(used, ", "))
			}
		}
	}
	return header, records, digest, nil
}

// visible is a record without the keys the reader added to it.
func visible(rec *Object) *Object {
	out := NewObject()
	for _, k := range rec.Keys() {
		if !strings.HasPrefix(k, "_") {
			out.Set(k, rec.Get(k))
		}
	}
	return out
}

func checkHeaderTypes(header *Object) error {
	at := recWhere(header)
	for _, c := range []struct{ key, kind, label string }{
		{"name", "str", "a string"}, {"tenant", "str", "a string"}, {"description", "str", "a string"},
		{"notes", "str", "a string"}, {"version", "str", "a string"}, {"requires_xolu", "str", "a string"},
		{"exclusive", "bool", "true or false"}, {"addition", "bool", "true or false"}, {"vars", "object", "an object"},
		{"x", "object", "an object"},
	} {
		if header.Has(c.key) && Kind(header.Get(c.key)) != c.kind {
			return errorf("%s: header \"%s\" must be %s", at, c.key, c.label)
		}
	}
	if Truthy(header.Get("exclusive")) && (Truthy(header.Get("addition")) || Truthy(header.Get("requires"))) {
		return errorf("%s: a whole dataset (\"exclusive\") cannot also be an addition to another", at)
	}
	for _, key := range []string{"tags", "entities", "requires"} {
		if !header.Has(key) {
			continue
		}
		list, ok := header.Get(key).([]any)
		for _, v := range list {
			if s, isStr := v.(string); !isStr || s == "" {
				ok = false
			}
		}
		if !ok {
			return errorf("%s: header \"%s\" must be a list of names", at, key)
		}
	}
	return nil
}

// Names returns a header's list of names ("requires", "tags", "entities") as strings.
func Names(header *Object, key string) []string {
	list, _ := header.Get(key).([]any)
	out := make([]string, 0, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// CheckRecordKeys refuses a key the record's kind does not define. With
// lenient set the key is passed to warn (when there is one) instead.
func CheckRecordKeys(rec *Object, allowed []string, lenient bool, warn func(string)) error {
	for _, key := range rec.Keys() {
		if strings.HasPrefix(key, "_") || contains(commonKeys, key) || contains(allowed, key) {
			continue
		}
		message := fmt.Sprintf("%s: unknown key \"%s\" on a '%s' record", recWhere(rec), key, Text(rec.Get("kind")))
		if !lenient {
			known := map[string]bool{}
			for _, k := range allowed {
				known[k] = true
			}
			for _, k := range commonKeys {
				known[k] = true
			}
			return errorf("%s (known: %s)", message, strings.Join(sortedKeys(known), ", "))
		}
		if warn != nil {
			warn(message)
		}
	}
	return nil
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// IsPlaceholder says whether a value is a placeholder: an object with one key, and that key one of "$seed", "$date", "$now", "$var", "$text".
func IsPlaceholder(v any) bool {
	o, ok := v.(*Object)
	return ok && o.Len() == 1 && contains(placeholders, o.Keys()[0])
}

// PlaceholdersIn is the set of placeholder names ("$seed", "$date", ...) used anywhere in a value.
func PlaceholdersIn(v any) map[string]bool {
	found := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case *Object:
			if IsPlaceholder(x) {
				found[x.Keys()[0]] = true
				return
			}
			for _, k := range x.Keys() {
				walk(x.Get(k))
			}
		case []any:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(v)
	return found
}

var (
	offsetRe     = regexp.MustCompile(`^\s*(?:([+-])\s*(\d+)\s*(s|m|h|d|w|mo|y))?\s*$`)
	dayTimeRe    = regexp.MustCompile(`^(.*?)(T\d\d:\d\d:\d\dZ)$`)
	inTextRe     = regexp.MustCompile(`\$\{(date|now|var):([^}]*)\}`)
	seedInTextRe = regexp.MustCompile(`\$\{seed:([\p{L}\p{N}_]+)\}`)
	asOfRe       = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})(?:T(\d{1,2}):(\d{1,2}):(\d{1,2})Z?)?$`)
)

// BindingsIn is the set of names bound with "as" that a value refers to, through {"$seed": name} or "${seed:name}" inside a {"$text"}.
func BindingsIn(v any) map[string]bool {
	found := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case *Object:
			if x.Len() == 1 && x.Has("$seed") {
				found[Text(x.Get("$seed"))] = true
				return
			}
			if text, ok := x.Str("$text"); ok && x.Len() == 1 {
				for _, m := range seedInTextRe.FindAllStringSubmatch(text, -1) {
					found[m[1]] = true
				}
				return
			}
			for _, k := range x.Keys() {
				walk(x.Get(k))
			}
		case []any:
			for _, item := range x {
				walk(item)
			}
		}
	}
	walk(v)
	return found
}

func daysIn(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

var unitSeconds = map[string]int64{"s": 1, "m": 60, "h": 3600, "d": 86400, "w": 604800}

// shift moves a time by an offset such as "+90d", "-2h", "+1mo", "-3y" or "".
func shift(base time.Time, offset any, at string) (time.Time, error) {
	text, isStr := offset.(string)
	if !isStr {
		text = "?"
	}
	m := offsetRe.FindStringSubmatch(text)
	if m == nil {
		return base, errorf("%s: \"%s\" is not an offset: use a sign, a whole number and one of s, m, h, d, w, mo, y "+
			"(for example \"+90d\", \"-2h\"), or \"\" for the load time itself", at, Text(offset))
	}
	if m[1] == "" {
		return base, nil
	}
	left := errorf("%s: \"%s\" leaves the calendar", at, Text(offset))
	count, ok := new(big.Int).SetString(m[2], 10)
	if !ok || count.BitLen() > 40 { // far past any year the calendar has, whatever the unit
		return base, left
	}
	n := count.Int64()
	if m[1] == "-" {
		n = -n
	}
	unit := m[3]
	if unit == "mo" || unit == "y" {
		if unit == "y" {
			n *= 12
		}
		months := int64(base.Year())*12 + int64(base.Month()-1) + n
		year, month := months/12, months%12
		if month < 0 {
			month += 12
			year--
		}
		if year < 1 || year > 9999 {
			return base, left
		}
		day := base.Day()
		if last := daysIn(int(year), time.Month(month+1)); day > last {
			day = last
		}
		return time.Date(int(year), time.Month(month+1), day, base.Hour(), base.Minute(), base.Second(), 0, time.UTC), nil
	}
	moved := time.Unix(base.Unix()+n*unitSeconds[unit], 0).UTC()
	if moved.Year() < 1 || moved.Year() > 9999 {
		return base, left
	}
	return moved, nil
}

// Context is what the placeholders of one load resolve against: the reference time and the variables.
type Context struct {
	AsOf      time.Time
	Variables map[string]any
}

// NewContext returns a context. A zero asOf means now, to the second.
func NewContext(asOf time.Time, variables map[string]any) *Context {
	if asOf.IsZero() {
		asOf = time.Now().UTC().Truncate(time.Second)
	}
	c := &Context{AsOf: asOf.UTC(), Variables: map[string]any{}}
	for k, v := range variables {
		c.Variables[k] = v
	}
	return c
}

// ParseAsOf reads a day (2026-11-17, taken at 00:00:00 UTC) or a time (2026-11-17T08:30:00Z).
func ParseAsOf(text string) (time.Time, error) {
	bad := errorf("--as-of: \"%s\" is neither a day (2026-11-17) nor a time (2026-11-17T08:30:00Z)", text)
	m := asOfRe.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, bad
	}
	n := make([]int, 7)
	for i := 1; i <= 6; i++ {
		if m[i] != "" {
			n[i], _ = strconv.Atoi(m[i])
		}
	}
	if n[1] < 1 || n[2] < 1 || n[2] > 12 || n[3] < 1 || n[3] > daysIn(n[1], time.Month(n[2])) || n[4] > 23 || n[5] > 59 || n[6] > 59 {
		return time.Time{}, bad
	}
	return time.Date(n[1], time.Month(n[2]), n[3], n[4], n[5], n[6], 0, time.UTC), nil
}

const (
	dayLayout  = "2006-01-02"
	timeLayout = "2006-01-02T15:04:05Z"
)

// Date is a day relative to the load; with a time of day after it ("-2dT08:30:00Z"), that time on that day.
func (c *Context) Date(arg any, at string) (string, error) {
	if text, ok := arg.(string); ok {
		if m := dayTimeRe.FindStringSubmatch(text); m != nil {
			t, err := shift(c.AsOf, m[1], at)
			if err != nil {
				return "", err
			}
			return t.Format(dayLayout) + m[2], nil
		}
	}
	t, err := shift(c.AsOf, arg, at)
	if err != nil {
		return "", err
	}
	return t.Format(dayLayout), nil
}

// AsOfText is the reference time as text.
func (c *Context) AsOfText() string { return c.AsOf.Format(timeLayout) }

// Resolve replaces {"$date"}, {"$now"}, {"$var"} and {"$text"} anywhere inside a value, and leaves {"$seed"} for the run.
func (c *Context) Resolve(value any, at string) (any, error) {
	switch x := value.(type) {
	case *Object:
		if x.Len() == 1 {
			key := x.Keys()[0]
			arg := x.Get(key)
			switch key {
			case "$date":
				return c.Date(arg, at)
			case "$text":
				text, ok := arg.(string)
				if !ok {
					return nil, errorf("%s: {\"$text\"} takes a string", at)
				}
				var failed error
				text = inTextRe.ReplaceAllStringFunc(text, func(found string) string {
					m := inTextRe.FindStringSubmatch(found)
					v, err := c.Resolve(Obj("$"+m[1], m[2]), at)
					if err != nil && failed == nil {
						failed = err
					}
					return Text(v)
				})
				if failed != nil {
					return nil, failed
				}
				// a bound value inside the text is known only while loading: the rest is settled here
				if seedInTextRe.MatchString(text) {
					return Obj("$text", text), nil
				}
				return text, nil
			case "$now":
				t, err := shift(c.AsOf, arg, at)
				if err != nil {
					return nil, err
				}
				return t.Format(timeLayout), nil
			case "$var":
				name, _ := arg.(string)
				v, ok := c.Variables[name]
				if _, isStr := arg.(string); !isStr || !ok {
					return nil, errorf("%s: variable \"%s\" has no value: give it with --set %s=VALUE or a default "+
						"in the header's \"vars\"", at, Text(arg), Text(arg))
				}
				return v, nil
			}
		}
		out := NewObject()
		for _, k := range x.Keys() {
			v, err := c.Resolve(x.Get(k), at)
			if err != nil {
				return nil, err
			}
			out.Set(k, v)
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			v, err := c.Resolve(item, at)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	}
	return value, nil
}

// layout is a JSON value as text, broken over lines (each starting with white space) where it does not fit.
func layout(value any, indent, width int) string {
	one := Dumps(value)
	list, isList := value.([]any)
	obj, isObj := value.(*Object)
	if runeLen(one)+indent <= width || (!isList && !isObj) || (isList && len(list) == 0) || (isObj && obj.Len() == 0) {
		return one
	}
	pad := strings.Repeat(" ", indent+2)
	var items []string
	if isList {
		for _, v := range list {
			items = append(items, pad+layout(v, indent+2, width))
		}
		return "[\n" + strings.Join(items, ",\n") + "\n" + strings.Repeat(" ", indent) + "]"
	}
	for _, k := range obj.Keys() {
		items = append(items, pad+Dumps(k)+": "+layout(obj.Get(k), indent+2, width))
	}
	return "{\n" + strings.Join(items, ",\n") + "\n" + strings.Repeat(" ", indent) + "}"
}

// WriteRecord is one record as text: on one line when it fits in width
// (110 when width is 0), otherwise its body laid out over indented lines.
func WriteRecord(rec *Object, width int) string {
	if width == 0 {
		width = 110
	}
	one := Dumps(rec)
	keys := rec.Keys()
	if rec.Has("seed") && !rec.Has("kind") && runeLen(one) > width { // a header: one key per line
		first := Dumps(Obj(keys[0], rec.Get(keys[0])))
		lines := []string{first[:len(first)-1] + ","}
		for i, k := range keys[1:] {
			comma := ""
			if i < len(keys)-2 {
				comma = ","
			}
			lines = append(lines, fmt.Sprintf("  %s: %s%s", Dumps(k), layout(rec.Get(k), 2, width), comma))
		}
		return strings.Join(append(lines, "  }"), "\n")
	}
	bodyKey := ""
	for _, k := range []string{"data", "body", "schema"} {
		if o, ok := rec.Get(k).(*Object); ok && o.Len() > 0 {
			bodyKey = k
			break
		}
	}
	if runeLen(one) <= width || bodyKey == "" {
		return one
	}
	head := rec.Copy()
	head.Del(bodyKey)
	headText := Dumps(head)
	lines := []string{headText[:len(headText)-1] + ", " + Dumps(bodyKey) + ": {"}
	body := rec.Get(bodyKey).(*Object)
	for i, k := range body.Keys() {
		comma := ""
		if i < body.Len()-1 {
			comma = ","
		}
		lines = append(lines, fmt.Sprintf("  %s: %s%s", Dumps(k), layout(body.Get(k), 2, width), comma))
	}
	return strings.Join(append(lines, "  }}"), "\n")
}
