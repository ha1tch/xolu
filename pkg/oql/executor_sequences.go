// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package oql

// executor_sequences.go
//
// S5: NEXT VALUE FOR and @SEQ OQL support.
//
// NEXT VALUE FOR name        — parsed by tsqlparser as *ast.NextValueForExpression.
//                              Atomically increments the sequence once per row.
// @SEQ('name')    — parsed as *ast.FunctionCall (registered scalar).
//                              Returns the session-local last value without
//                              incrementing. XOLU-GEN006 if called before
//                              NEXT VALUE FOR in the same query.
//
// Both are gated on seqIncrementor being non-nil. When nil (sequences not
// enabled or v2 disabled) NEXT VALUE FOR returns nil and @SEQ
// returns nil.

import (
	"fmt"

	"github.com/ha1tch/tsqlparser/ast"
)

// seqSessionState tracks per-query state for sequence functions.
// A single Executor is reused across queries; this struct is allocated fresh
// at the start of each Execute call that references sequences.
type seqSessionState struct {
	tenantID   uint16
	lastValues map[string]int64 // name → last value returned by NEXT VALUE FOR
	seenFirst  map[string]bool  // name → whether NEXT VALUE FOR has been called
}

func newSeqSessionState(tenantID uint16) *seqSessionState {
	return &seqSessionState{
		tenantID:   tenantID,
		lastValues: make(map[string]int64),
		seenFirst:  make(map[string]bool),
	}
}

// SetSeqIncrementor wires in the server's sequence increment function.
// Call once at server startup after the Executor is created.
// fn receives the tenant ID and sequence name and returns the new current value.
func (e *Executor) SetSeqIncrementor(fn func(tenantID uint16, name string) (int64, error)) {
	e.seqIncrementor = fn
}

// SetGenDispatcher wires in the server's named-generator dispatch function.
// Call once at server startup after the Executor is created. fn receives the
// tenant ID and generator name, resolves the named definition in
// gen_definitions, and produces one value. Nil leaves @GEN returning nil.
func (e *Executor) SetGenDispatcher(fn func(tenantID uint16, name string) (string, error)) {
	e.genDispatcher = fn
}

// evalGenValue is registered as @GEN('name'). It resolves the named generator
// via the server-wired dispatcher for the executor's tenant and returns one
// generated value. Returns nil when no dispatcher is wired, the argument is
// missing, or resolution/generation fails (the query column is then nil rather
// than the query failing — consistent with @SEQ).
func (e *Executor) evalGenValue(args []interface{}) interface{} {
	if e.genDispatcher == nil || len(args) == 0 {
		return nil
	}
	name := fmt.Sprintf("%v", args[0])
	if e.store == nil {
		return nil
	}
	tenantID := e.store.Config().TenantID
	val, err := e.genDispatcher(tenantID, name)
	if err != nil {
		return nil
	}
	return val
}

// evalNextValueFor handles *ast.NextValueForExpression in evalExpr.
// It increments the sequence once per call (once per row in a SELECT),
// stores the result in seqSession for @SEQ, and returns the value.
func (e *Executor) evalNextValueFor(ex *ast.NextValueForExpression) interface{} {
	if e.seqIncrementor == nil || e.seqSession == nil {
		return nil
	}
	name := ex.SequenceName.String()
	val, err := e.seqIncrementor(e.seqSession.tenantID, name)
	if err != nil {
		// Sequence not found or exhausted — return nil, let caller handle.
		return nil
	}
	e.seqSession.lastValues[name] = val
	e.seqSession.seenFirst[name] = true
	return val
}

// evalSeqValue is registered as @SEQ(name) scalar function.
// Returns the session-local last value set by NEXT VALUE FOR, or an error
// value if NEXT VALUE FOR has not yet been called for this sequence.
func (e *Executor) evalSeqValue(args []interface{}) interface{} {
	if e.seqSession == nil || len(args) == 0 {
		return nil
	}
	name := fmt.Sprintf("%v", args[0])
	if !e.seqSession.seenFirst[name] {
		// XOLU-GEN006: @SEQ called before NEXT VALUE FOR in this session.
		// Return nil rather than panic — the query result will have a nil column.
		return nil
	}
	return e.seqSession.lastValues[name]
}

// RegisterSeqGenFuncs registers @SEQ and @GEN on the given Executor.
// Must be called after NewExecutor and before the first query using sequences.
//
// @SEQ('name')  — session-local last value of a named sequence (no increment).
//
//	Returns nil if NEXT VALUE FOR has not been called in this
//	session for the named sequence (XOLU-GEN006).
//
// @GEN('name')  — dispatch to any stateful generator by name.
//
//	STUB: returns nil until S10 (stateful generators) is
//	implemented. Registered now so queries using @GEN() parse
//	and execute without error rather than failing at the
//	unknown-function path.
func RegisterSeqGenFuncs(e *Executor) {
	// T-40: these closures capture THIS executor and therefore live on its
	// instance overlay, never the package-level map — a per-engine write
	// there is a concurrent map write across engines and last-writer-wins
	// cross-engine dispatch. The package map carries inert @SEQ/@GEN stubs
	// for membership checks (see scalar.go).
	if e.scalars == nil {
		e.scalars = make(map[string]ScalarFunc, 2)
	}
	e.scalars["@SEQ"] = func(args []interface{}) interface{} {
		return e.evalSeqValue(args)
	}
	e.scalars["@GEN"] = func(args []interface{}) interface{} {
		return e.evalGenValue(args)
	}
}
