// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package eval

// exclusivity.go — guard mutual-exclusivity recognizer for determinism: loose.
//
// CheckExclusivity decides whether a set of guards on one (state, input) is
// PROVABLY mutually exclusive. It is sound (never claims exclusivity for guards
// that can both be true) and deliberately incomplete (reports "not proven" for
// anything outside its recognized patterns, which the author resolves by
// restructuring or declaring firstmatch).
//
// Model. Each guard reduces to a predicate over exactly one variable: a NULL
// state requirement plus either a value region (a union of integer intervals)
// or a relation to another variable (var == var / var != var). Two guards are
// provably disjoint when, on their shared variable, their must-hold regions
// cannot be simultaneously satisfied: incompatible null states, non-intersecting
// value regions, or complementary var-vs-var relations. Guards on different
// variables are never provably disjoint.

import (
	"fmt"
	"math"
	"strings"

	"github.com/ha1tch/tsqlparser/ast"
)

// GuardExpr pairs a guard's source text with its parsed AST.
type GuardExpr struct {
	Source string
	AST    ast.Expression
}

// ExclusivityResult is the recognizer's verdict.
type ExclusivityResult struct {
	Exclusive bool
	Reason    string
	OverlapA  string
	OverlapB  string
}

// CheckExclusivity reports whether guards are provably pairwise mutually
// exclusive. Empty or single-guard sets are trivially exclusive.
func CheckExclusivity(guards []GuardExpr) ExclusivityResult {
	if len(guards) <= 1 {
		return ExclusivityResult{Exclusive: true}
	}
	preds := make([]predicate, len(guards))
	for i, g := range guards {
		p, ok := reducePredicate(g.AST)
		if !ok {
			return ExclusivityResult{
				Exclusive: false,
				Reason: fmt.Sprintf(
					"guard %q is not in a recognized exclusivity pattern; "+
						"restructure into a null-partition, distinct-equality, interval, "+
						"or var-vs-var form, or declare firstmatch",
					g.Source),
				OverlapA: g.Source,
			}
		}
		p.source = g.Source
		preds[i] = p
	}
	for i := 0; i < len(preds); i++ {
		for j := i + 1; j < len(preds); j++ {
			if !predicatesDisjoint(preds[i], preds[j]) {
				return ExclusivityResult{
					Exclusive: false,
					Reason: fmt.Sprintf(
						"guards %q and %q can both be true (%s); they are not mutually exclusive",
						preds[i].source, preds[j].source, overlapReason(preds[i], preds[j])),
					OverlapA: preds[i].source,
					OverlapB: preds[j].source,
				}
			}
		}
	}
	return ExclusivityResult{Exclusive: true}
}

type nullState int

const (
	nullAny nullState = iota
	nullRequired
	nullAbsent
)

type predicate struct {
	source string

	varName string
	null    nullState

	// nullSatisfies marks a predicate that is ALSO satisfied when the variable
	// is null (e.g. "X IS NULL OR X <= 0"): it holds in the null case OR within
	// its value region.
	nullSatisfies bool

	hasRegion bool
	region    intervalSet

	isRelational bool
	relVar       string
	relEq        bool
}

type interval struct{ lo, hi int64 }
type intervalSet []interval

const negInf = math.MinInt64
const posInf = math.MaxInt64

func reducePredicate(node ast.Expression) (predicate, bool) {
	switch ex := node.(type) {
	case *ast.IsNullExpression:
		name, ok := varNameOf(ex.Expr)
		if !ok {
			return predicate{}, false
		}
		if ex.Not {
			return predicate{varName: name, null: nullAbsent}, true
		}
		return predicate{varName: name, null: nullRequired}, true

	case *ast.InfixExpression:
		op := strings.ToUpper(ex.Operator)
		switch op {
		case "AND":
			return reduceAnd(ex)
		case "OR":
			return reduceOr(ex)
		default:
			return reduceComparison(ex)
		}
	}
	return predicate{}, false
}

func reduceComparison(ex *ast.InfixExpression) (predicate, bool) {
	op := ex.Operator
	switch op {
	case "=", "!=", "<>", "<", "<=", ">", ">=":
	default:
		return predicate{}, false
	}
	lname, lIsVar := varNameOf(ex.Left)
	if !lIsVar {
		return predicate{}, false
	}
	if rname, rIsVar := varNameOf(ex.Right); rIsVar {
		switch op {
		case "=":
			return predicate{varName: lname, null: nullAbsent, isRelational: true, relVar: rname, relEq: true}, true
		case "!=", "<>":
			return predicate{varName: lname, null: nullAbsent, isRelational: true, relVar: rname, relEq: false}, true
		default:
			return predicate{}, false
		}
	}
	rlit, rIsLit := intLiteralOf(ex.Right)
	if !rIsLit {
		return predicate{}, false
	}
	reg := comparisonRegion(op, rlit)
	if reg == nil {
		return predicate{}, false
	}
	return predicate{varName: lname, null: nullAbsent, hasRegion: true, region: reg}, true
}

func comparisonRegion(op string, k int64) intervalSet {
	switch op {
	case "=":
		return intervalSet{{k, k}}
	case "!=", "<>":
		return intervalSet{{negInf, k - 1}, {k + 1, posInf}}
	case "<":
		return intervalSet{{negInf, k - 1}}
	case "<=":
		return intervalSet{{negInf, k}}
	case ">":
		return intervalSet{{k + 1, posInf}}
	case ">=":
		return intervalSet{{k, posInf}}
	}
	return nil
}

func reduceAnd(ex *ast.InfixExpression) (predicate, bool) {
	parts := flattenOp(ex, "AND")
	var varName string
	null := nullAny
	region := intervalSet{{negInf, posInf}}
	haveRegion := false
	var rel *predicate

	for _, part := range parts {
		p, ok := reducePredicate(part)
		if !ok {
			return predicate{}, false
		}
		if varName == "" {
			varName = p.varName
		} else if p.varName != varName {
			return predicate{}, false
		}
		if p.null == nullRequired {
			return predicate{}, false
		}
		if p.null == nullAbsent && !p.hasRegion && !p.isRelational {
			null = nullAbsent
		}
		if p.isRelational {
			if rel != nil {
				return predicate{}, false
			}
			rp := p
			rel = &rp
			null = nullAbsent
		}
		if p.hasRegion {
			region = intersectSets(region, p.region)
			haveRegion = true
			null = nullAbsent
		}
	}
	if rel != nil {
		if haveRegion {
			return predicate{}, false
		}
		rel.null = nullAbsent
		return *rel, true
	}
	return predicate{varName: varName, null: null, hasRegion: haveRegion, region: region}, true
}

func reduceOr(ex *ast.InfixExpression) (predicate, bool) {
	parts := flattenOp(ex, "OR")
	var varName string
	var union intervalSet
	nullSat := false
	for _, part := range parts {
		p, ok := reducePredicate(part)
		if !ok {
			return predicate{}, false
		}
		// An IS NULL disjunct makes the whole predicate satisfiable in the null
		// case ("missing or ...").
		if p.null == nullRequired && !p.hasRegion && !p.isRelational {
			if varName != "" && p.varName != varName {
				return predicate{}, false
			}
			if varName == "" {
				varName = p.varName
			}
			nullSat = true
			continue
		}
		if !p.hasRegion {
			return predicate{}, false // only IS NULL or value-region disjuncts
		}
		if varName == "" {
			varName = p.varName
		} else if p.varName != varName {
			return predicate{}, false
		}
		union = append(union, p.region...)
	}
	out := predicate{varName: varName, null: nullAbsent, hasRegion: len(union) > 0, region: normalize(union)}
	if nullSat {
		out.nullSatisfies = true
		// The predicate holds when null OR within region; it is not a
		// pure-present predicate, so do not assert nullAbsent.
		out.null = nullAny
	}
	return out, true
}

func flattenOp(ex *ast.InfixExpression, op string) []ast.Expression {
	var out []ast.Expression
	var walk func(e ast.Expression)
	walk = func(e ast.Expression) {
		if inf, ok := e.(*ast.InfixExpression); ok && strings.EqualFold(inf.Operator, op) {
			walk(inf.Left)
			walk(inf.Right)
			return
		}
		out = append(out, e)
	}
	walk(ex)
	return out
}

func predicatesDisjoint(a, b predicate) bool {
	// Different variables: independent, can both hold.
	if a.varName != b.varName {
		return false
	}

	// Null-dimension disjointness applies to ALL predicate kinds (including
	// relational): a predicate requiring presence (IS NOT NULL) is disjoint from
	// one requiring null (IS NULL), since the variable cannot be both.
	aReqNull := a.null == nullRequired && !a.nullSatisfies
	bReqNull := b.null == nullRequired && !b.nullSatisfies
	aReqPresent := a.null == nullAbsent
	bReqPresent := b.null == nullAbsent
	if (aReqNull && bReqPresent) || (bReqNull && aReqPresent) {
		return true
	}

	// Relational var-vs-var complementarity.
	if a.isRelational && b.isRelational {
		if a.relVar == b.relVar && a.relEq != b.relEq {
			return true
		}
		return false
	}
	if a.isRelational != b.isRelational {
		// A relation says nothing about the other's region; they can co-occur
		// (unless already separated by the null dimension above).
		return false
	}

	// Both region/null predicates on the same variable: decompose into
	// (admitsNull, valueRegion) and require disjointness in both dimensions.
	admitsNullA, regionA := satisfyingSet(a)
	admitsNullB, regionB := satisfyingSet(b)
	if admitsNullA && admitsNullB {
		return false
	}
	if !setEmpty(intersectSets(regionA, regionB)) {
		return false
	}
	return true
}

// satisfyingSet decomposes a predicate into (admitsNull, valueRegion).
func satisfyingSet(p predicate) (admitsNull bool, region intervalSet) {
	full := intervalSet{{negInf, posInf}}
	switch {
	case p.null == nullRequired && !p.nullSatisfies:
		// Pure IS NULL: only the null case, no values.
		return true, nil
	case p.nullSatisfies:
		// null OR region.
		if p.hasRegion {
			return true, p.region
		}
		return true, nil
	case p.null == nullAbsent:
		// Present and (region, or any value if no region).
		if p.hasRegion {
			return false, p.region
		}
		return false, full
	default:
		// nullAny with a region (a bare comparison): values only; the null case
		// makes a comparison UNKNOWN→false, so null is not admitted.
		if p.hasRegion {
			return false, p.region
		}
		return false, full
	}
}

func setEmpty(s intervalSet) bool {
	for _, iv := range s {
		if iv.lo <= iv.hi {
			return false
		}
	}
	return true
}

func intersectSets(a, b intervalSet) intervalSet {
	var out intervalSet
	for _, x := range a {
		for _, y := range b {
			lo := x.lo
			if y.lo > lo {
				lo = y.lo
			}
			hi := x.hi
			if y.hi < hi {
				hi = y.hi
			}
			if lo <= hi {
				out = append(out, interval{lo, hi})
			}
		}
	}
	return normalize(out)
}

func emptyIntersection(a, b intervalSet) bool {
	return setEmpty(intersectSets(a, b))
}

var _ = emptyIntersection

func normalize(s intervalSet) intervalSet {
	if len(s) == 0 {
		return s
	}
	var cleaned intervalSet
	for _, iv := range s {
		if iv.lo <= iv.hi {
			cleaned = append(cleaned, iv)
		}
	}
	if len(cleaned) == 0 {
		return cleaned
	}
	for i := 1; i < len(cleaned); i++ {
		for j := i; j > 0 && cleaned[j-1].lo > cleaned[j].lo; j-- {
			cleaned[j-1], cleaned[j] = cleaned[j], cleaned[j-1]
		}
	}
	out := intervalSet{cleaned[0]}
	for _, iv := range cleaned[1:] {
		last := &out[len(out)-1]
		if iv.lo <= last.hi || (last.hi != posInf && iv.lo <= last.hi+1) {
			if iv.hi > last.hi {
				last.hi = iv.hi
			}
		} else {
			out = append(out, iv)
		}
	}
	return out
}

func overlapReason(a, b predicate) string {
	if a.varName == b.varName {
		return fmt.Sprintf("both can hold for some value of %s", a.varName)
	}
	return "their conditions can be simultaneously satisfied"
}

func varNameOf(node ast.Expression) (string, bool) {
	switch ex := node.(type) {
	case *ast.Variable:
		return strings.ToLower(strings.TrimPrefix(ex.Name, "@")), true
	case *ast.QualifiedIdentifier:
		parts := make([]string, len(ex.Parts))
		for i, p := range ex.Parts {
			parts[i] = p.Value
		}
		return strings.ToLower(strings.Join(parts, ".")), true
	case *ast.Identifier:
		return strings.ToLower(ex.Value), true
	}
	return "", false
}

func intLiteralOf(node ast.Expression) (int64, bool) {
	if lit, ok := node.(*ast.IntegerLiteral); ok {
		return lit.Value, true
	}
	return 0, false
}
