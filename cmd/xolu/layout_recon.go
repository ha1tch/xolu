// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	sl "github.com/ha1tch/xolu/pkg/storelayout"
)

// scanLayout walks baseDir one level at a time and builds an sl.Model describing
// what is actually on disk. It performs all the I/O so that pkg/storelayout can
// remain a pure (I/O-free) authority over the invariant: scanLayout produces the
// model, sl.Check verifies it.
//
// The walk is intentionally shallow and explicit rather than a recursive
// filepath.Walk: the invariant has a fixed depth (base → tenant → role → file),
// and inspecting each level deliberately makes the recon output match the
// structure the invariant describes.
func scanLayout(baseDir string) (sl.Model, error) {
	model := sl.Model{BaseDir: baseDir}

	top, err := os.ReadDir(baseDir)
	if err != nil {
		return model, err
	}

	for _, entry := range top {
		name := entry.Name()
		if !entry.IsDir() {
			model.RootFiles = append(model.RootFiles, name)
			continue
		}

		// A directory at the base is treated as a tenant/shared directory only
		// if its name parses as a tenant segment or is the shared segment.
		// Everything else is recorded as a non-tenant root directory, which the
		// check will flag (only "schema" is permitted there).
		_, isTenant := sl.ParseTenantSegment(name)
		isShared := name == "shared"
		if !isTenant && !isShared {
			model.RootDirs = append(model.RootDirs, name)
			continue
		}

		td := sl.TenantDir{Segment: name}
		segPath := filepath.Join(baseDir, name)
		inner, err := os.ReadDir(segPath)
		if err != nil {
			return model, fmt.Errorf("reading %s: %w", segPath, err)
		}
		for _, ie := range inner {
			if !ie.IsDir() {
				td.Extra = append(td.Extra, ie.Name())
				continue
			}
			role := sl.RoleDir{Name: ie.Name()}
			roleEntries, err := os.ReadDir(filepath.Join(segPath, ie.Name()))
			if err != nil {
				return model, fmt.Errorf("reading %s: %w", filepath.Join(segPath, ie.Name()), err)
			}
			for _, re := range roleEntries {
				role.Entries = append(role.Entries, re.Name())
			}
			td.Roles = append(td.Roles, role)
		}
		model.Tenants = append(model.Tenants, td)
	}

	return model, nil
}

// reconLayout is the `xolu layout-recon` subcommand: it scans the configured data
// root, prints the directory structure annotated against the invariant, runs the
// strict integrity check, and exits non-zero if the layout does not conform.
func reconLayout(baseDir string) int {
	// BaseDir may be relative (the default is "data", i.e. ./data relative to
	// where xolu was launched). Resolve it so the report names the exact
	// directory on disk being inspected.
	resolved := baseDir
	if abs, err := filepath.Abs(baseDir); err == nil {
		resolved = abs
	}
	fmt.Printf("Layout reconnaissance for data root: %s\n", resolved)
	if resolved != baseDir {
		fmt.Printf("  (configured as %q, relative to the working directory)\n", baseDir)
	}

	if _, err := os.Stat(resolved); os.IsNotExist(err) {
		fmt.Printf("  (data root does not exist yet — nothing to inspect)\n")
		return 0
	}

	model, err := scanLayout(resolved)
	if err != nil {
		fmt.Fprintf(os.Stderr, "layout-recon: %v\n", err)
		return 1
	}

	printModel(model)

	issues := sl.Check(model)
	fmt.Println()
	if len(issues) == 0 {
		fmt.Println("Integrity: OK — layout conforms to the invariant.")
		return 0
	}

	fmt.Printf("Integrity: %d violation(s):\n", len(issues))
	for _, iss := range issues {
		fmt.Printf("  ✗ %s: %s\n", iss.Path, iss.Message)
	}
	return 1
}

// printModel prints the scanned layout as an annotated tree. Each line notes
// whether the entry is expected by the invariant (✓) or not (✗), so the output
// doubles as a quick visual conformance read even before the check summary.
func printModel(m Model) {
	fmt.Println()
	fmt.Println("Structure:")

	// Tenant / shared directories, sorted for stable output.
	tenants := append([]TenantDir(nil), m.Tenants...)
	sort.Slice(tenants, func(a, b int) bool { return tenants[a].Segment < tenants[b].Segment })
	for _, td := range tenants {
		_, isTenant := sl.ParseTenantSegment(td.Segment)
		ok := isTenant || td.Segment == "shared"
		fmt.Printf("  %s %s/\n", mark(ok), td.Segment)

		for _, extra := range td.Extra {
			fmt.Printf("      %s %s   (unexpected: only role directories permitted)\n", mark(false), extra)
		}
		roles := append([]RoleDir(nil), td.Roles...)
		sort.Slice(roles, func(a, b int) bool { return roles[a].Name < roles[b].Name })
		for _, role := range roles {
			roleOK := role.Name == "store" || role.Name == "ts"
			fmt.Printf("      %s %s/\n", mark(roleOK), role.Name)
			entries := append([]string(nil), role.Entries...)
			sort.Strings(entries)
			for _, e := range entries {
				fmt.Printf("          %s\n", e)
			}
		}
	}

	rootDirs := append([]string(nil), m.RootDirs...)
	sort.Strings(rootDirs)
	for _, d := range rootDirs {
		ok := d == sl.SchemaDirName
		fmt.Printf("  %s %s/\n", mark(ok), d)
	}

	rootFiles := append([]string(nil), m.RootFiles...)
	sort.Strings(rootFiles)
	for _, f := range rootFiles {
		ok := f == sl.DynConfigFileName
		note := ""
		if !ok {
			note = "   (unexpected at base root)"
		}
		fmt.Printf("  %s %s%s\n", mark(ok), f, note)
	}
}

// Model, TenantDir and RoleDir are type aliases into pkg/storelayout so this
// file can refer to them without the sl. prefix in the printer above.
type Model = sl.Model
type TenantDir = sl.TenantDir
type RoleDir = sl.RoleDir

func mark(ok bool) string {
	if ok {
		return "✓"
	}
	return "✗"
}
