// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func xoseed(args ...string) (int, string, string) {
	var out, err bytes.Buffer
	code := run(args, &out, &err)
	return code, out.String(), err.String()
}

// The command line without a server, as scripts/seed_tests has it for the Python loader.
func TestCommandLineWithoutAServer(t *testing.T) {
	seeds := filepath.Join("..", "..", "seeds")
	for _, c := range []struct {
		args []string
		code int
		says string
	}{
		{[]string{"list", "--seeds-dir", seeds}, 0, "example.seed"},
		{[]string{"info", "example", "--seeds-dir", seeds, "--json"}, 0, `"digest": "`},
		{[]string{"info", "--seeds-dir=" + seeds, "example"}, 0, "records"},
		{[]string{"load", "example", "--seeds-dir", seeds, "--dry-run"}, 0, "nothing was contacted"},
		{[]string{"load", "example", "--seeds-d", seeds, "--dry-run", "--as-of", "2026-01-31"}, 0, "reference time 2026-01-31T00:00:00Z"},
		{[]string{"plan", "example", "--seeds-dir", seeds}, 0, `"steps": [`},
		{[]string{"load", "--seeds-dir", seeds}, 2, "--all"}, // no seed named
		{[]string{"load", "nothing-like-this", "--seeds-dir", seeds, "--dry-run"}, 2, "not found"},
		{[]string{"load", "example", "--seeds-dir", seeds, "--dry-run", "--as-of", "yesterday"}, 2, "neither a day"},
		{[]string{"load", "example", "--no-such-option"}, 2, "unrecognized arguments"},
		{[]string{"load", "example", "--tenant"}, 2, "expected one argument"},
		{[]string{"check"}, 2, "required: SEED"},
		{[]string{"dump", "extra"}, 2, "unrecognized arguments"},
		{[]string{"lod"}, 2, "invalid choice"},
		{[]string{"load", "example", "--seeds-dir", seeds, "--url", "ftp://x", "--wait", "0"}, 2, "is not a base URL"},
		{[]string{"--version"}, 0, "xoseed " + version},
		{[]string{"load", "--help"}, 0, "--allow-existing"},
		{[]string{}, 2, "usage"},
	} {
		code, out, err := xoseed(c.args...)
		if code != c.code || !strings.Contains(out+err, c.says) {
			t.Errorf("xoseed %s: exit %d, want %d and %q in\n%s%s", strings.Join(c.args, " "), code, c.code, c.says, out, err)
		}
	}
}
