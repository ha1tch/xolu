// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package seed

// value.go -- the JSON values a .seed file holds, read and written the way
// the Python loader (scripts/seed_system) reads and writes them.
//
// A record's identity across loads is a digest of its text with the keys
// sorted (RecordKey), and that digest is stored in the tenant. For a tenant
// loaded by one implementation to be continued by the other, both must write
// the same text for the same value: the same separators, the same escapes,
// and the same spelling of a number. encoding/json does none of the three the
// way Python's json module does, and it does not keep the order of an
// object's keys, which the layout of a written file and the order of a plan
// both follow. Hence this file.
//
// A value is one of: nil, bool, int64, *big.Int (an integer that does not fit
// in 64 bits), float64, string, []any, *Object.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Object is a JSON object that keeps its keys in the order they were written.
type Object struct {
	keys []string
	m    map[string]any
}

// NewObject returns an empty object.
func NewObject() *Object { return &Object{m: map[string]any{}} }

// Obj builds an object from alternating keys and values, in that order.
func Obj(pairs ...any) *Object {
	o := NewObject()
	for i := 0; i+1 < len(pairs); i += 2 {
		o.Set(pairs[i].(string), pairs[i+1])
	}
	return o
}

// Len is the number of keys.
func (o *Object) Len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

// Keys returns the keys in order. The slice is the object's own: do not change it.
func (o *Object) Keys() []string {
	if o == nil {
		return nil
	}
	return o.keys
}

// Get returns the value of a key, or nil when the object does not have it.
func (o *Object) Get(key string) any {
	if o == nil {
		return nil
	}
	return o.m[key]
}

// Has says whether the object has the key.
func (o *Object) Has(key string) bool {
	if o == nil {
		return false
	}
	_, ok := o.m[key]
	return ok
}

// Set gives a key its value. A key the object already has keeps its place.
func (o *Object) Set(key string, value any) {
	if _, ok := o.m[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.m[key] = value
}

// Del removes a key.
func (o *Object) Del(key string) {
	if _, ok := o.m[key]; !ok {
		return
	}
	delete(o.m, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i:i], o.keys[i+1:]...)
			break
		}
	}
}

// Copy returns a new object with the same keys and values (the values themselves are shared).
func (o *Object) Copy() *Object {
	out := NewObject()
	for _, k := range o.Keys() {
		out.Set(k, o.m[k])
	}
	return out
}

// Str returns the value of a key when it is a string.
func (o *Object) Str(key string) (string, bool) {
	s, ok := o.Get(key).(string)
	return s, ok
}

// ---------------------------------------------------------------------------
// Reading

// jsonError is a syntax error, worded as Python's json module words it.
type jsonError struct {
	msg string
	pos int
	doc []rune
}

func (e *jsonError) Error() string {
	line, col := 1, e.pos+1
	for i := 0; i < e.pos && i < len(e.doc); i++ {
		if e.doc[i] == '\n' {
			line++
			col = e.pos - i
		}
	}
	return fmt.Sprintf("%s: line %d column %d (char %d)", e.msg, line, col, e.pos)
}

type parser struct {
	s []rune
	i int
}

// ParseJSON reads one JSON value from text.
func ParseJSON(text string) (any, error) {
	p := &parser{s: []rune(text)}
	p.space()
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	p.space()
	if p.i != len(p.s) {
		return nil, &jsonError{"Extra data", p.i, p.s}
	}
	return v, nil
}

func (p *parser) space() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) lit(word string) bool {
	w := []rune(word)
	if p.i+len(w) > len(p.s) {
		return false
	}
	for k, r := range w {
		if p.s[p.i+k] != r {
			return false
		}
	}
	p.i += len(w)
	return true
}

func (p *parser) value() (any, error) {
	if p.i >= len(p.s) {
		return nil, &jsonError{"Expecting value", p.i, p.s}
	}
	switch c := p.s[p.i]; {
	case c == '"':
		return p.str()
	case c == '{':
		return p.object()
	case c == '[':
		return p.array()
	case c == 'n' && p.lit("null"):
		return nil, nil
	case c == 't' && p.lit("true"):
		return true, nil
	case c == 'f' && p.lit("false"):
		return false, nil
	case c == 'N' && p.lit("NaN"):
		return math.NaN(), nil
	case c == 'I' && p.lit("Infinity"):
		return math.Inf(1), nil
	case c == '-' && p.lit("-Infinity"):
		return math.Inf(-1), nil
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	return nil, &jsonError{"Expecting value", p.i, p.s}
}

func (p *parser) number() (any, error) {
	start := p.i
	digits := func() int {
		n := 0
		for p.i < len(p.s) && p.s[p.i] >= '0' && p.s[p.i] <= '9' {
			p.i++
			n++
		}
		return n
	}
	if p.s[p.i] == '-' {
		p.i++
	}
	if p.i < len(p.s) && p.s[p.i] == '0' {
		p.i++
	} else if digits() == 0 {
		p.i = start
		return nil, &jsonError{"Expecting value", start, p.s}
	}
	isInt := true
	if p.i+1 < len(p.s) && p.s[p.i] == '.' && p.s[p.i+1] >= '0' && p.s[p.i+1] <= '9' {
		isInt = false
		p.i++
		digits()
	}
	if p.i < len(p.s) && (p.s[p.i] == 'e' || p.s[p.i] == 'E') {
		save := p.i
		p.i++
		if p.i < len(p.s) && (p.s[p.i] == '+' || p.s[p.i] == '-') {
			p.i++
		}
		if digits() == 0 {
			p.i = save
		} else {
			isInt = false
		}
	}
	text := string(p.s[start:p.i])
	if isInt {
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			return n, nil
		}
		b, _ := new(big.Int).SetString(text, 10)
		return b, nil
	}
	f, _ := strconv.ParseFloat(text, 64) // out of range gives an infinity, as Python does
	return f, nil
}

func (p *parser) str() (string, error) {
	start := p.i
	p.i++
	var b strings.Builder
	for {
		if p.i >= len(p.s) {
			return "", &jsonError{"Unterminated string starting at", start, p.s}
		}
		c := p.s[p.i]
		switch {
		case c == '"':
			p.i++
			return b.String(), nil
		case c < 0x20:
			return "", &jsonError{"Invalid control character at", p.i, p.s}
		case c != '\\':
			b.WriteRune(c)
			p.i++
		default:
			p.i++
			if p.i >= len(p.s) {
				return "", &jsonError{"Unterminated string starting at", start, p.s}
			}
			e := p.s[p.i]
			p.i++
			switch e {
			case '"', '\\', '/':
				b.WriteRune(e)
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				r, ok := p.hex4()
				if !ok {
					return "", &jsonError{"Invalid \\uXXXX escape", p.i - 1, p.s}
				}
				if r >= 0xD800 && r <= 0xDBFF && p.i+1 < len(p.s) && p.s[p.i] == '\\' && p.s[p.i+1] == 'u' {
					save := p.i
					p.i += 2
					if lo, ok := p.hex4(); ok && lo >= 0xDC00 && lo <= 0xDFFF {
						r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
					} else {
						p.i = save
					}
				}
				b.WriteRune(r)
			default:
				return "", &jsonError{"Invalid \\escape", p.i - 2, p.s}
			}
		}
	}
}

func (p *parser) hex4() (rune, bool) {
	if p.i+4 > len(p.s) {
		return 0, false
	}
	n, err := strconv.ParseUint(string(p.s[p.i:p.i+4]), 16, 32)
	if err != nil {
		return 0, false
	}
	p.i += 4
	return rune(n), true
}

func (p *parser) array() (any, error) {
	p.i++
	out := []any{}
	p.space()
	if p.i < len(p.s) && p.s[p.i] == ']' {
		p.i++
		return out, nil
	}
	for {
		p.space()
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		p.space()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
			continue
		}
		if p.i < len(p.s) && p.s[p.i] == ']' {
			p.i++
			return out, nil
		}
		return nil, &jsonError{"Expecting ',' delimiter", p.i, p.s}
	}
}

func (p *parser) object() (any, error) {
	p.i++
	out := NewObject()
	p.space()
	if p.i < len(p.s) && p.s[p.i] == '}' {
		p.i++
		return out, nil
	}
	for {
		p.space()
		if p.i >= len(p.s) || p.s[p.i] != '"' {
			return nil, &jsonError{"Expecting property name enclosed in double quotes", p.i, p.s}
		}
		key, err := p.str()
		if err != nil {
			return nil, err
		}
		p.space()
		if p.i >= len(p.s) || p.s[p.i] != ':' {
			return nil, &jsonError{"Expecting ':' delimiter", p.i, p.s}
		}
		p.i++
		p.space()
		v, err := p.value()
		if err != nil {
			return nil, err
		}
		out.Set(key, v)
		p.space()
		if p.i < len(p.s) && p.s[p.i] == ',' {
			p.i++
			continue
		}
		if p.i < len(p.s) && p.s[p.i] == '}' {
			p.i++
			return out, nil
		}
		return nil, &jsonError{"Expecting ',' delimiter", p.i, p.s}
	}
}

// ---------------------------------------------------------------------------
// Writing

// FloatText spells a float the way Python's repr does: the shortest digits
// that read back to the same number, in plain notation from 1e-4 up to 1e16
// and with an exponent outside that.
func FloatText(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	s := strconv.FormatFloat(f, 'e', -1, 64) // d.ddddde±XX
	mant, expText, _ := strings.Cut(s, "e")
	exp, _ := strconv.Atoi(expText)
	neg := strings.HasPrefix(mant, "-")
	mant = strings.TrimPrefix(mant, "-")
	digits := strings.Replace(mant, ".", "", 1)
	var out string
	switch {
	case exp >= -4 && exp < 16:
		switch {
		case exp < 0:
			out = "0." + strings.Repeat("0", -exp-1) + digits
		case len(digits) <= exp+1:
			out = digits + strings.Repeat("0", exp+1-len(digits)) + ".0"
		default:
			out = digits[:exp+1] + "." + digits[exp+1:]
		}
	default:
		out = digits[:1]
		if len(digits) > 1 {
			out += "." + digits[1:]
		}
		sign := "+"
		if exp < 0 {
			sign, exp = "-", -exp
		}
		out += fmt.Sprintf("e%s%02d", sign, exp)
	}
	if neg {
		return "-" + out
	}
	return out
}

func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

func writeValue(b *strings.Builder, v any, sorted bool, indent, level int) {
	nl := func(l int) {
		if indent > 0 {
			b.WriteByte('\n')
			b.WriteString(strings.Repeat(" ", indent*l))
		}
	}
	sep := ", "
	if indent > 0 {
		sep = ","
	}
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if x {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case int:
		b.WriteString(strconv.Itoa(x))
	case *big.Int:
		b.WriteString(x.String())
	case float64:
		b.WriteString(FloatText(x))
	case string:
		writeString(b, x)
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				b.WriteString(sep)
			}
			nl(level + 1)
			writeValue(b, item, sorted, indent, level+1)
		}
		nl(level)
		b.WriteByte(']')
	case []string:
		items := make([]any, len(x))
		for i, s := range x {
			items[i] = s
		}
		writeValue(b, items, sorted, indent, level)
	case *Object:
		if x.Len() == 0 {
			b.WriteString("{}")
			return
		}
		keys := x.Keys()
		if sorted {
			keys = append([]string(nil), keys...)
			sort.Strings(keys)
		}
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteString(sep)
			}
			nl(level + 1)
			writeString(b, k)
			b.WriteString(": ")
			writeValue(b, x.m[k], sorted, indent, level+1)
		}
		nl(level)
		b.WriteByte('}')
	default:
		writeString(b, fmt.Sprint(x))
	}
}

// Dumps writes a value on one line: ", " between items, ": " after a key, text as it is (not escaped to ASCII).
func Dumps(v any) string {
	var b strings.Builder
	writeValue(&b, v, false, 0, 0)
	return b.String()
}

// DumpsASCII is Dumps with every character outside ASCII written as an escape: the text of a request's body.
func DumpsASCII(v any) string {
	var b strings.Builder
	for _, r := range Dumps(v) {
		switch {
		case r >= 0x20 && r <= 0x7e:
			b.WriteRune(r)
		case r >= 0x10000:
			r -= 0x10000
			fmt.Fprintf(&b, `\u%04x\u%04x`, 0xd800+(r>>10), 0xdc00+(r&0x3ff))
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
	}
	return b.String()
}

// DumpsSorted is Dumps with the keys of every object in order.
func DumpsSorted(v any) string {
	var b strings.Builder
	writeValue(&b, v, true, 0, 0)
	return b.String()
}

// DumpsIndent writes a value over several lines, one space of indentation for each level.
func DumpsIndent(v any) string {
	var b strings.Builder
	writeValue(&b, v, false, 1, 0)
	return b.String()
}

// ---------------------------------------------------------------------------
// The rules of the language the reference implementation is written in, where they show

// Text is a value as it reads when put inside a message or a path.
func Text(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	case int:
		return strconv.Itoa(x)
	case *big.Int:
		return x.String()
	case float64:
		return floatRepr(x)
	}
	return Repr(v)
}

func floatRepr(f float64) string {
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	return FloatText(f)
}

// Repr is a value as it reads when a message shows it exactly: a string in quotes.
func Repr(v any) string {
	switch x := v.(type) {
	case string:
		q := "'"
		if strings.Contains(x, "'") && !strings.Contains(x, `"`) {
			q = `"`
		}
		var b strings.Builder
		b.WriteString(q)
		for _, r := range x {
			switch {
			case r == '\\':
				b.WriteString(`\\`)
			case string(r) == q:
				b.WriteString(`\` + q)
			case r == '\n':
				b.WriteString(`\n`)
			case r == '\r':
				b.WriteString(`\r`)
			case r == '\t':
				b.WriteString(`\t`)
			case r < 0x20 || r == 0x7f:
				fmt.Fprintf(&b, `\x%02x`, r)
			case !unicode.IsPrint(r) && r <= 0xff:
				fmt.Fprintf(&b, `\x%02x`, r)
			case !unicode.IsPrint(r) && r <= 0xffff:
				fmt.Fprintf(&b, `\u%04x`, r)
			case !unicode.IsPrint(r):
				fmt.Fprintf(&b, `\U%08x`, r)
			default:
				b.WriteRune(r)
			}
		}
		b.WriteString(q)
		return b.String()
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = Repr(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *Object:
		parts := make([]string, 0, x.Len())
		for _, k := range x.Keys() {
			parts = append(parts, Repr(k)+": "+Repr(x.m[k]))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return Text(v)
}

// Truthy says whether a value counts as set: not null, false, zero, or empty.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case int64:
		return x != 0
	case int:
		return x != 0
	case *big.Int:
		return x.Sign() != 0
	case float64:
		return x != 0
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case *Object:
		return x.Len() > 0
	}
	return true
}

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	case *big.Int:
		f, _ := new(big.Float).SetInt(x).Float64()
		return f, true
	case float64:
		return x, true
	}
	return 0, false
}

func asBig(v any) (*big.Int, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return big.NewInt(1), true
		}
		return big.NewInt(0), true
	case int64:
		return big.NewInt(x), true
	case int:
		return big.NewInt(int64(x)), true
	case *big.Int:
		return x, true
	}
	return nil, false
}

// Equal compares two values: numbers by what they are worth (1 and 1.0 are
// equal, and so are true and 1), objects whatever the order of their keys.
func Equal(a, b any) bool {
	if ia, ok := asBig(a); ok {
		if ib, ok := asBig(b); ok {
			return ia.Cmp(ib) == 0
		}
	}
	if fa, ok := asFloat(a); ok {
		fb, ok := asFloat(b)
		return ok && fa == fb
	}
	switch x := a.(type) {
	case nil:
		return b == nil
	case string:
		y, ok := b.(string)
		return ok && x == y
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !Equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case *Object:
		y, ok := b.(*Object)
		if !ok || x.Len() != y.Len() {
			return false
		}
		for _, k := range x.Keys() {
			if !y.Has(k) || !Equal(x.m[k], y.m[k]) {
				return false
			}
		}
		return true
	}
	return false
}

// Kind names what a value is: two values that are Equal can still be of different kinds (1 and 1.0).
func Kind(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case int64, int, *big.Int:
		return "int"
	case float64:
		return "float"
	case string:
		return "str"
	case []any:
		return "list"
	case *Object:
		return "object"
	}
	return "other"
}

// IsInt says whether a value is a whole number (and not true or false).
func IsInt(v any) bool { return Kind(v) == "int" }

// Int64 returns a whole number that fits in 64 bits.
func Int64(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case int:
		return int64(x), true
	}
	return 0, false
}

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// cut returns the first n characters of s.
func cut(s string, n int) string {
	if runeLen(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

const unreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.-~"

// Quote percent-encodes text for one segment of a path: everything but letters, digits and "_.-~".
func Quote(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(unreserved, s[i]) >= 0 {
			b.WriteByte(s[i])
		} else {
			fmt.Fprintf(&b, "%%%02X", s[i])
		}
	}
	return b.String()
}

func quotePlus(s string) string { return strings.ReplaceAll(Quote(s), "%20", "+") }

func digestText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
