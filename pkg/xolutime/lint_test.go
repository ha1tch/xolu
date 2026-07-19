// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// This test enforces xolu's UTC-instant invariant across the whole tree: a
// timestamp that is persisted or serialized must be UTC, minted via
// xolutime.Now (or .UTC()), never a bare time.Now() whose meaning depends on the
// host's configured zone.
//
// It deliberately does NOT flag duration measurement. `start := time.Now()` used
// only with time.Since is correct and must keep Go's monotonic reading; calling
// .UTC() there would strip it. The test distinguishes the two by syntactic role,
// the same discipline the working agreement requires for any mass operation:
// classify by role, never by surface text alone.
//
// Run from the xolutime package: `go test ./pkg/xolutime/ -run TestNoBareWallClock`.
package xolutime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findRepoRoot walks up from the test's working directory to the module root
// (the directory containing go.mod), so the test works regardless of where the
// runner invokes it from.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate go.mod above %s", dir)
		}
		dir = parent
	}
}

// violation records a flagged bare time.Now() that reaches a persisted field.
type violation struct {
	file string
	line int
	ctx  string
}

func TestNoBareWallClock(t *testing.T) {
	root := findRepoRoot(t)
	pkgRoot := filepath.Join(root, "pkg")

	// Field/identifier names that mark an assignment target as a *persisted* or
	// *serialized* wall-clock value. A bare time.Now() flowing into one of these
	// is a violation. This list is the contract; extend it as new persisted
	// timestamp fields appear.
	persistedTargets := []string{
		"CreatedAt", "UpdatedAt", "DeletedAt", "FirstWriteAt", "LastWriteAt",
		"Timestamp", "Time", "At", "Date", "ExpiresAt", "cutoff", "ModifiedAt",
		"StartedAt", "EndedAt", "FinishedAt", "CompletedAt",
	}

	// Packages whose time.Now() usage is legitimately monotonic/ephemeral
	// (TTL countdowns, rate-limit windows, metrics stopwatches, cache expiry
	// measured as a local interval). These are reviewed by hand and exempted as
	// a unit; revisit if any of them starts *persisting* a timestamp.
	exemptPkgPrefixes := []string{
		filepath.Join(pkgRoot, "middleware"),
		filepath.Join(pkgRoot, "cache"),
	}

	// persistingConstructors: function names that, when called with a bare
	// time.Now() argument, produce a value destined for storage or for a user
	// query result that should be UTC. A call like NewDateTime(time.Now()) would
	// be flagged. This list is intentionally EMPTY: the OQL/FSM evaluator
	// builtins (pkg/fsm/eval, pkg/qs/scalar.go) use NewDateTime for BOTH the
	// local T-SQL builtins (GETDATE/SYSDATETIME — local by contract, correctly
	// bare time.Now()) and the UTC ones (now sourced from ot.Now()). Flagging
	// NewDateTime wholesale would therefore be wrong: it would condemn the
	// contract-correct local builtins. (Resolved 2026-06-22; see
	// docs/KNOWN_ISSUES.md.) Populate only if a constructor appears that is
	// unconditionally persisted and must be UTC.
	persistingConstructors := []string{}

	// Known limits of this guard (recorded so a green result is read honestly):
	//
	//   1. Dataflow across functions is NOT tracked. A time.Now() returned from
	//      one function and stored in another escapes detection. A whole-program
	//      static analysis (e.g. a go/analysis pass with SSA) would be required;
	//      that is out of scope for a unit-test-level guard.
	//   2. time.Now() passed as an argument is only caught for the explicitly
	//      listed persistingConstructors above; arbitrary functions that store
	//      their argument are not known to this guard.
	//   3. Detection is name-based (persistedTargets). A persisted field whose
	//      name is not in the list is not flagged. The list is the contract;
	//      extend it as fields appear.
	//
	// The guard catches the common shapes (direct field set, struct literal,
	// address-of-temp, listed constructors). It is a regression catcher, not a
	// proof of exhaustive UTC-cleanliness.

	var violations []violation

	err := filepath.Walk(pkgRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// xolutime itself is the one place time.Now() is sanctioned.
		if strings.HasPrefix(path, filepath.Join(pkgRoot, "xolutime")) {
			return nil
		}
		for _, ex := range exemptPkgPrefixes {
			if strings.HasPrefix(path, ex) {
				return nil
			}
		}

		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Logf("skip unparseable %s: %v", path, perr)
			return nil
		}

		ast.Inspect(f, func(n ast.Node) bool {
			// We look for assignments / key-value pairs whose target name is a
			// persisted field AND whose value is a bare time.Now() call (not
			// chained to .UTC()).
			switch node := n.(type) {
			case *ast.KeyValueExpr:
				if isPersistedKey(node.Key, persistedTargets) && isBareTimeNow(node.Value) {
					pos := fset.Position(node.Pos())
					violations = append(violations, violation{path, pos.Line, exprName(node.Key)})
				}
			case *ast.AssignStmt:
				for i, rhs := range node.Rhs {
					if !isBareTimeNow(rhs) {
						continue
					}
					if i < len(node.Lhs) && isPersistedKey(node.Lhs[i], persistedTargets) {
						pos := fset.Position(node.Pos())
						violations = append(violations, violation{path, pos.Line, exprName(node.Lhs[i])})
					}
				}
			case *ast.CallExpr:
				// time.Now() passed directly as an argument to a known
				// persisting constructor, e.g. NewDateTime(time.Now()).
				if len(persistingConstructors) == 0 {
					break
				}
				fnName := exprName(node.Fun)
				listed := false
				for _, c := range persistingConstructors {
					if c == fnName {
						listed = true
						break
					}
				}
				if !listed {
					break
				}
				for _, arg := range node.Args {
					if isBareTimeNow(arg) {
						pos := fset.Position(node.Pos())
						violations = append(violations, violation{
							path, pos.Line, fnName + "(time.Now())",
						})
					}
				}
			}
			return true
		})

		// Second pass: catch the address-of-temp idiom that the direct check
		// above misses. A persisted *time.Time field cannot be set from
		// time.Now() directly (you can't take the address of a call result), so
		// code writes `now := time.Now(); job.StartedAt = &now`. The temp's
		// assignment target is a local, not the persisted field, so pass one
		// does not see it. Here we collect locals bound to a bare time.Now() and
		// then flag any `persistedField = &thatLocal`.
		ast.Inspect(f, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			// locals in this function bound to a bare time.Now().
			bareTimeLocals := map[string]int{} // name -> line
			ast.Inspect(fn.Body, func(m ast.Node) bool {
				as, ok := m.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for i, rhs := range as.Rhs {
					if i < len(as.Lhs) && isBareTimeNow(rhs) {
						if id, ok := as.Lhs[i].(*ast.Ident); ok {
							bareTimeLocals[id.Name] = fset.Position(as.Pos()).Line
						}
					}
				}
				return true
			})
			if len(bareTimeLocals) == 0 {
				return true
			}
			// find `persistedField = &local` where local is a bare-time local.
			ast.Inspect(fn.Body, func(m ast.Node) bool {
				as, ok := m.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for i, rhs := range as.Rhs {
					unary, ok := rhs.(*ast.UnaryExpr)
					if !ok || unary.Op != token.AND {
						continue
					}
					id, ok := unary.X.(*ast.Ident)
					if !ok {
						continue
					}
					if _, isBare := bareTimeLocals[id.Name]; !isBare {
						continue
					}
					if i < len(as.Lhs) && isPersistedKey(as.Lhs[i], persistedTargets) {
						pos := fset.Position(as.Pos())
						violations = append(violations, violation{
							path, pos.Line,
							exprName(as.Lhs[i]) + " (via &" + id.Name + ")",
						})
					}
				}
				return true
			})
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}

	if len(violations) > 0 {
		var b strings.Builder
		b.WriteString("bare time.Now() flowing into a persisted or compared wall-clock value ")
		b.WriteString("(use xolutime.Now(): these are stored or compared against stored\n")
		b.WriteString("instants, not measured as elapsed durations — a non-UTC host would skew them):\n")
		for _, v := range violations {
			rel, _ := filepath.Rel(root, v.file)
			b.WriteString("  " + rel + ":" + itoa(v.line) + "  -> " + v.ctx + "\n")
		}
		t.Error(b.String())
	}
}

// isBareTimeNow reports whether e is exactly `time.Now()` (optionally wrapped in
// .Add(...) or .In(...), which are still bare for our purposes), but NOT
// `time.Now().UTC()`.
func isBareTimeNow(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch sel.Sel.Name {
	case "UTC":
		// time.Now().UTC() — sanctioned. Anything ending in .UTC() is fine.
		return false
	case "Now":
		// time.Now() — check the receiver is the `time` package.
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "time" {
			return true
		}
		return false
	case "Add", "In", "Format", "Truncate", "Unix", "UnixNano":
		// time.Now().Add(...) etc — recurse into the receiver; still bare unless
		// a .UTC() appears somewhere in the chain.
		return isBareTimeNow(sel.X)
	}
	return false
}

func isPersistedKey(e ast.Expr, targets []string) bool {
	name := exprName(e)
	for _, tgt := range targets {
		if name == tgt {
			return true
		}
	}
	return false
}

// exprName returns the trailing identifier name of a key or lvalue
// (Ident "cutoff" -> "cutoff"; SelectorExpr job.UpdatedAt -> "UpdatedAt").
func exprName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}

// itoa avoids importing strconv for a single use.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
