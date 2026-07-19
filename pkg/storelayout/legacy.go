// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storelayout

import (
	"fmt"
	"sort"
)

// Legacy-location detection. Before the layout normalization, data lived at
// different paths: a base store at <base>/xolu.db, per-file tenant stores under
// <base>/sql/tXXXX/, and timeseries under <base>/ts/tXXXX/ (backend-first). A
// server starting against such a directory with the new layout would create
// fresh empty stores at the new paths and silently leave the old data orphaned.
//
// DetectLegacy inspects a scanned Model for these old locations so startup can
// refuse with a clear pointer rather than silently stranding data. Like Check,
// it is pure: the caller scans disk into a Model and passes it here.

// LegacyFinding describes one detected pre-normalization data location.
type LegacyFinding struct {
	Path    string // path relative to the base directory
	Message string // what it is and why it blocks startup
}

func (f LegacyFinding) String() string {
	return fmt.Sprintf("%s: %s", f.Path, f.Message)
}

// Names of the pre-normalization directories that must not appear at the base
// root under the new layout. These are the backend-first groupings the
// normalization replaced.
const (
	legacySQLDirName = "sql" // old per-file tenant stores: <base>/sql/tXXXX/
	legacyTSDirName  = "ts"  // old backend-first timeseries: <base>/ts/tXXXX/
)

// DetectLegacy returns findings for any pre-normalization data locations present
// in the model. An empty result means no legacy layout was detected (the
// directory is either empty, freshly created, or already conforms). A non-empty
// result means the caller is looking at data written by an older xolu and should
// refuse to start rather than create fresh stores alongside it.
//
// Detection is conservative: it reports a legacy location only when the old
// directory/file is actually present, so a clean or new-layout directory never
// trips it.
func DetectLegacy(m Model) []LegacyFinding {
	var findings []LegacyFinding
	add := func(path, msg string) {
		findings = append(findings, LegacyFinding{Path: path, Message: msg})
	}

	// 1. A store file directly at the base root: the old single-file base store.
	for _, f := range m.RootFiles {
		if f == StoreFile {
			add(f, "pre-normalization base store; the store now lives under "+
				"t0000/store/ (per-file) or shared/store/")
		}
	}

	// 2. The old backend-first grouping directories at the base root.
	for _, d := range m.RootDirs {
		switch d {
		case legacySQLDirName:
			add(d, "pre-normalization per-file tenant stores; tenant stores now "+
				"live at tXXXX/store/")
		case legacyTSDirName:
			add(d, "pre-normalization backend-first timeseries; timeseries now "+
				"lives at tXXXX/ts/")
		}
	}

	sort.Slice(findings, func(a, b int) bool { return findings[a].Path < findings[b].Path })
	return findings
}
