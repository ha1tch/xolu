// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package main implements c04dcheck, the mechanical guard for substrate law
// @C04d ("sized ids survive the wire", chronicle-substrate §4d): a
// primitive's id has one width, preserved at every boundary it crosses.
//
// Two checks (T-45):
//
//  1. NARROWING CONVERSION: converting a value whose type is a registered
//     sized-id type to a narrower or platform-dependent integer type
//     (int, int8, int16, int32, uint8, uint16). This covers both variable
//     conversions (int(e.Timeline)) and typed ceiling constants
//     (int(MaxTimelineID) — a compile break on 32-bit targets, silent
//     truncation elsewhere).
//
//  2. SHORT-PARSE FLOW: a value produced by strconv.ParseUint (or
//     ParseInt) with a constant bitSize narrower than the id's width,
//     converted to a registered sized-id type in the same function. This
//     is the pattern that silently capped timeline ids at 65535 on every
//     platform (ParseUint(s, 10, 16) → TimelineID(v)).
//
// Registered sized-id types are matched by NAME plus UNDERLYING TYPE
// (uint32), not by package path, so the analyzer's test fixtures stay
// hermetic and future primitives that follow the naming convention are
// covered automatically. For this repository the match is exact:
// timeseries.TimelineID and cal.CalOrdinal. bal's internal account key
// joins the table when bal is built.
//
// Legitimate uint16 quantities (tenant ids) are unaffected: they are not
// registered id types, and a 16-bit parse only trips check 2 if its
// result is converted to a registered 32-bit id type.
package main

import (
	"go/ast"
	"go/constant"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// registeredIDWidths maps sized-id type names to their bit width.
// A type matches when its name is in this table AND its underlying type
// is the unsigned integer of that width.
var registeredIDWidths = map[string]int{
	"TimelineID": 32, // timeseries
	"CalOrdinal": 32, // cal
	// "AccountKey": 32, // bal — add when bal lands (@B §9a)
}

// narrowTargets are conversion targets that lose or platform-depend the
// width of a 32-bit id. int is included because it is 32-bit on 386/arm.
var narrowTargets = map[types.BasicKind]string{
	types.Int:    "int (platform-dependent, 32-bit on 386/arm)",
	types.Int8:   "int8",
	types.Int16:  "int16",
	types.Int32:  "int32 (cannot hold ids above 2^31)",
	types.Uint8:  "uint8",
	types.Uint16: "uint16 (caps ids at 65535)",
}

var Analyzer = &analysis.Analyzer{
	Name: "c04dcheck",
	Doc:  "flags @C04d violations: narrowing conversions of sized-id types and short-parse flows into them (chronicle-substrate §4d, T-45)",
	Run:  run,
}

func run(pass *analysis.Pass) (interface{}, error) {
	for _, file := range pass.Files {
		// Per-function state for check 2: objects assigned from a
		// short ParseUint/ParseInt.
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				// Also run check 1 on non-function positions via the
				// whole-file walk below; conversions outside functions
				// (const/var decls) are handled there.
				return true
			}
			checkFunc(pass, fn)
			return true
		})

		// Checks 1 and 3 everywhere in the file (covers package-level decls too).
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			reportNarrowing(pass, call)
			reportLossyConstruction(pass, call)
			return true
		})
	}
	return nil, nil
}

// registeredID returns (name, width, true) if t is a registered sized-id
// type: a named type whose name is in the table and whose underlying type
// is the unsigned integer of the registered width.
func registeredID(t types.Type) (string, int, bool) {
	named, ok := t.(*types.Named)
	if !ok {
		return "", 0, false
	}
	name := named.Obj().Name()
	width, ok := registeredIDWidths[name]
	if !ok {
		return "", 0, false
	}
	basic, ok := named.Underlying().(*types.Basic)
	if !ok {
		return "", 0, false
	}
	wantKind := map[int]types.BasicKind{8: types.Uint8, 16: types.Uint16, 32: types.Uint32, 64: types.Uint64}[width]
	if basic.Kind() != wantKind {
		return "", 0, false
	}
	return name, width, true
}

// reportNarrowing implements check 1 on one call expression, and check 3:
// a conversion TO a registered id FROM a source type too narrow to have
// held the full id range (the value was already truncated upstream, e.g.
// a `Timeline uint16` JSON field fed into TimelineID).
func reportNarrowing(pass *analysis.Pass, call *ast.CallExpr) {
	// Is this a type conversion to a basic integer type?
	funType := pass.TypesInfo.TypeOf(call.Fun)
	if funType == nil {
		return
	}
	// A conversion's Fun is a type expression; TypeOf yields the target type.
	basic, ok := funType.Underlying().(*types.Basic)
	if !ok {
		return
	}
	desc, narrow := narrowTargets[basic.Kind()]
	if !narrow {
		return
	}
	// But only when Fun genuinely denotes a type (not a function returning int).
	if _, isType := pass.TypesInfo.Types[call.Fun]; !isType {
		return
	}
	if tv, ok := pass.TypesInfo.Types[call.Fun]; !ok || !tv.IsType() {
		return
	}
	argType := pass.TypesInfo.TypeOf(call.Args[0])
	if argType == nil {
		return
	}
	if name, width, ok := registeredID(argType); ok {
		pass.Reportf(call.Pos(),
			"@C04d violation: %s (uint%d sized-id) converted to %s — carry ids as int64 on the wire and route JSON crossings through the validating helper (chronicle-substrate §4d)",
			name, width, desc)
	}
}

// lossySources are source types that cannot hold a full uint32 id: if a
// registered id is constructed FROM one of these, the value was already
// truncated (or platform-dependent) upstream — the `Timeline uint16`
// wire-field shape of the historical bug.
var lossySources = map[types.BasicKind]string{
	types.Int:    "int (platform-dependent, 32-bit on 386/arm)",
	types.Int8:   "int8",
	types.Int16:  "int16",
	types.Int32:  "int32 (cannot hold ids above 2^31)",
	types.Uint8:  "uint8",
	types.Uint16: "uint16 (already capped at 65535)",
}

// reportLossyConstruction implements check 3 on one call expression.
func reportLossyConstruction(pass *analysis.Pass, call *ast.CallExpr) {
	tv, ok := pass.TypesInfo.Types[call.Fun]
	if !ok || !tv.IsType() {
		return
	}
	name, width, ok := registeredID(tv.Type)
	if !ok {
		return
	}
	argType := pass.TypesInfo.TypeOf(call.Args[0])
	if argType == nil {
		return
	}
	// Untyped constants (e.g. TimelineID(5)) are fine — the compiler
	// range-checks them. Only flag typed lossy sources.
	basic, ok := argType.(*types.Basic)
	if !ok || basic.Info()&types.IsUntyped != 0 {
		return
	}
	if desc, lossy := lossySources[basic.Kind()]; lossy {
		pass.Reportf(call.Pos(),
			"@C04d violation: %s (uint%d sized-id) constructed from %s — the value was already narrowed upstream; carry it as int64 (or uint32/uint64) end-to-end (chronicle-substrate §4d)",
			name, width, desc)
	}
}

// checkFunc implements check 2 within one function body: values parsed at
// a bit width narrower than a registered id's width must not be converted
// to that id type.
func checkFunc(pass *analysis.Pass, fn *ast.FuncDecl) {
	// Pass A: collect objects assigned from short parses, with the parse width.
	shortParsed := map[types.Object]int{} // object -> parsed bit width
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		width, ok := parseUintBitSize(pass, call)
		if !ok {
			return true
		}
		// ParseUint returns (uint64, error); the value is Lhs[0].
		if len(assign.Lhs) >= 1 {
			if ident, ok := assign.Lhs[0].(*ast.Ident); ok && ident.Name != "_" {
				if obj := pass.TypesInfo.ObjectOf(ident); obj != nil {
					shortParsed[obj] = width
				}
			}
		}
		return true
	})
	if len(shortParsed) == 0 {
		return
	}

	// Pass B: find conversions of those objects to registered id types.
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		tv, ok := pass.TypesInfo.Types[call.Fun]
		if !ok || !tv.IsType() {
			return true
		}
		name, idWidth, ok := registeredID(tv.Type)
		if !ok {
			return true
		}
		ident, ok := call.Args[0].(*ast.Ident)
		if !ok {
			return true
		}
		obj := pass.TypesInfo.ObjectOf(ident)
		if obj == nil {
			return true
		}
		if parsedWidth, short := shortParsed[obj]; short && parsedWidth < idWidth {
			pass.Reportf(call.Pos(),
				"@C04d violation: %s parsed with bitSize %d but %s is uint%d — parse with strconv.ParseUint(s, 10, %d) or the id silently caps at %d bits (chronicle-substrate §4d)",
				ident.Name, parsedWidth, name, idWidth, idWidth, parsedWidth)
		}
		return true
	})
}

// parseUintBitSize reports the constant bitSize argument of a
// strconv.ParseUint / strconv.ParseInt call, if that is what call is.
func parseUintBitSize(pass *analysis.Pass, call *ast.CallExpr) (int, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || (sel.Sel.Name != "ParseUint" && sel.Sel.Name != "ParseInt") {
		return 0, false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok || pkgIdent.Name != "strconv" {
		return 0, false
	}
	if len(call.Args) != 3 {
		return 0, false
	}
	tv, ok := pass.TypesInfo.Types[call.Args[2]]
	if !ok || tv.Value == nil {
		return 0, false
	}
	w, ok := constant.Int64Val(tv.Value)
	if !ok {
		return 0, false
	}
	return int(w), true
}
