// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package seed

import (
	"os"
	"strconv"
	"testing"
)

// The vectors were written by the Python loader (scripts/seed_system): the
// text it gives a number, a record, and the key it gives a record. A tenant
// loaded by one implementation is continued by the other only while these
// are the same in both.
func vectors(t *testing.T) *Object {
	t.Helper()
	raw, err := os.ReadFile("testdata/python_vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	v, err := ParseJSON(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*Object)
}

func TestNumbersAreSpelledAsPythonSpellsThem(t *testing.T) {
	for _, item := range vectors(t).Get("floats").([]any) {
		pair := item.([]any)
		f, err := strconv.ParseFloat(pair[0].(string), 64)
		if err != nil {
			t.Fatal(err)
		}
		if got := Text(f); got != pair[0].(string) {
			t.Errorf("Text(%v) = %s, want %s", f, got, pair[0])
		}
		if got := Dumps(f); got != pair[1].(string) {
			t.Errorf("Dumps(%v) = %s, want %s", f, got, pair[1])
		}
	}
}

func TestRecordsAreWrittenAndKeyedAsPythonDoes(t *testing.T) {
	for i, item := range vectors(t).Get("values").([]any) {
		want := item.(*Object)
		text, _ := want.Str("text")
		v, err := ParseJSON(text)
		if err != nil {
			t.Fatalf("value %d: %v", i, err)
		}
		rec := v.(*Object)
		shown := any(rec)
		if rec.Has("data") {
			shown = rec.Get("data")
		}
		for name, got := range map[string]string{"text": Dumps(rec), "sorted": DumpsSorted(rec), "ascii": DumpsASCII(rec),
			"indent": DumpsIndent(rec), "key": RecordKey(rec), "written": WriteRecord(rec, 60), "repr": Repr(shown)} {
			if w, _ := want.Str(name); got != w {
				t.Errorf("value %d, %s:\n got  %s\n want %s", i, name, got, w)
			}
		}
	}
}

func TestValuesCompareAsPythonCompares(t *testing.T) {
	parse := func(s string) any {
		v, err := ParseJSON(s)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"1", "1.0", true}, {"true", "1", true}, {"0", "false", true}, {"1", "2", false}, {`"1"`, "1", false}, {"null", "0", false},
		{`{"a": 1, "b": [1, 2]}`, `{"b": [1.0, 2], "a": 1}`, true}, {`{"a": 1}`, `{"a": 1, "b": 2}`, false}, {"[1, 2]", "[2, 1]", false},
		{"123456789012345678901234567890", "123456789012345678901234567890", true},
	} {
		if got := Equal(parse(c.a), parse(c.b)); got != c.same {
			t.Errorf("Equal(%s, %s) = %v", c.a, c.b, got)
		}
	}
	if Kind(parse("1")) == Kind(parse("1.0")) || Kind(parse("true")) == Kind(parse("1")) {
		t.Error("1, 1.0 and true are equal and still of three kinds")
	}
	for _, bad := range []string{"", "{", `{"a" 1}`, "[1,]", `"x`, "01", "{} x", `{"a": tru}`, "'a'"} {
		if _, err := ParseJSON(bad); err == nil {
			t.Errorf("ParseJSON(%q) read", bad)
		}
	}
	if got := Quote("a b/ñ~_.-"); got != "a%20b%2F%C3%B1~_.-" {
		t.Errorf("Quote = %s", got)
	}
}

func TestAnObjectKeepsItsOrder(t *testing.T) {
	o := Obj("b", int64(1), "a", int64(2))
	o.Set("c", int64(3))
	o.Set("b", int64(9))
	o.Del("a")
	if got := Dumps(o); got != `{"b": 9, "c": 3}` {
		t.Errorf("got %s", got)
	}
	var none *Object
	if none.Has("x") || none.Get("x") != nil || none.Len() != 0 {
		t.Error("an object that is not there has nothing")
	}
}
