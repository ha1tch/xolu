package server

// S7 — FSM definitions and machines: shared types and helpers.
//
// This file holds the wire-format types for FSM definitions and the
// definition -> fsm-toolkit FSM builder used by both the definition and
// machine handlers. Endpoint handlers live in v2_fsm_def_handlers.go and
// v2_fsm_machine_handlers.go.
//
// Two validation concerns are kept strictly separate (per the development
// plan, S7):
//
//   - Structural validity is delegated to fsm-toolkit: state and transition
//     reference integrity (FSM.Validate), reachability of a terminal state
//     from every non-terminal state (the xolu lifecycle rule, layered on
//     top via UnreachableStates/DeadStates), determinism and analysis
//     (FSM.Analyse).
//   - Guard and set-clause expression validity is delegated to
//     pkg/fsm/eval: every guard and set fragment must parse. Expressions
//     are never evaluated at definition or machine creation time;
//     evaluation happens only at walk time (S8).

import (
	"encoding/json"
	"fmt"

	toolkit "github.com/ha1tch/fsm-toolkit/pkg/fsm"
	"github.com/ha1tch/xolu/pkg/fsm/eval"
)

// ---- Wire-format types ----

// fsmStateSpec is a single state declaration in a definition.
type fsmStateSpec struct {
	Terminal bool `json:"terminal"`
}

// fsmVariableSpec is a single machine-variable declaration.
type fsmVariableSpec struct {
	Type    string      `json:"type"`
	Default interface{} `json:"default"`
}

// fsmTransitionSpec is a single transition. From may be a single state name
// (string) or a list of state names; both forms are accepted on the wire and
// normalised to a list by fromStates(). Input is required for FSM
// transitions in this subsystem. Output is the optional Mealy output. Guard
// and Set are T-SQL expression fragments evaluated at walk time.
type fsmTransitionSpec struct {
	From   json.RawMessage   `json:"from"`
	Input  string            `json:"input"`
	To     string            `json:"to"`
	Guard  string            `json:"guard,omitempty"`
	Output string            `json:"output,omitempty"`
	Set    map[string]string `json:"set,omitempty"`
}

// fromStates normalises the From field, which may be a JSON string or a JSON
// array of strings, into a slice of state names.
func (t *fsmTransitionSpec) fromStates() ([]string, error) {
	if len(t.From) == 0 {
		return nil, fmt.Errorf("transition has no 'from' state")
	}
	// Try single string first.
	var single string
	if err := json.Unmarshal(t.From, &single); err == nil {
		return []string{single}, nil
	}
	// Try list of strings.
	var list []string
	if err := json.Unmarshal(t.From, &list); err == nil {
		if len(list) == 0 {
			return nil, fmt.Errorf("transition 'from' list is empty")
		}
		return list, nil
	}
	return nil, fmt.Errorf("transition 'from' must be a state name or a list of state names")
}

// fsmGCSpec is the optional GC policy block carried in a definition.
type fsmGCSpec struct {
	StalledAfter string `json:"stalled_after,omitempty"`
	DeadAfter    string `json:"dead_after,omitempty"`
	OnGCCollect  string `json:"on_gc_collect,omitempty"`
}

// fsmDefinitionSpec is the full body of a definition create/replace request
// and the canonical stored form (spec_json).
type fsmDefinitionSpec struct {
	Name           string                     `json:"name"`
	Description    string                     `json:"description,omitempty"`
	Initial        string                     `json:"initial"`
	Determinism    string                     `json:"determinism"`
	States         map[string]fsmStateSpec    `json:"states"`
	Variables      map[string]fsmVariableSpec `json:"variables,omitempty"`
	Transitions    []fsmTransitionSpec        `json:"transitions"`
	OutputAlphabet []string                   `json:"output_alphabet,omitempty"`
	LinkedStates   map[string]int64           `json:"linked_states,omitempty"`
	GC             *fsmGCSpec                 `json:"gc,omitempty"`

	// InputQueries associates an OQL SELECT with an input symbol. Before a walk
	// on that input, the server runs the query (read-only, before the walk
	// transaction opens, forced to TOP 1) and binds the first result row into
	// the guard/set evaluator under the "query." prefix, alongside "payload.".
	// This lets a transition's guards consult data the caller would otherwise
	// have had to fetch and pass in the payload, saving a round-trip. The map
	// is keyed by input rather than by transition: all candidate guards for an
	// input share one query, run once, before guard evaluation selects among
	// them. Because the query runs before the walk transaction, its result
	// reflects state immediately before the walk, not a snapshot atomic with the
	// state advance. A transition whose guards depend on query results cannot be
	// proven exclusive by the recognizer, so such a machine must be firstmatch.
	InputQueries map[string]string `json:"input_queries,omitempty"`
}

// Determinism levels. The field is mandatory at definition creation: a
// definition without an explicit, valid determinism level is rejected and
// cannot be created or instantiated.
//
//	strict     — at most one transition per (state, input). Enforced
//	             structurally at creation.
//	loose      — multiple transitions per (state, input) permitted, but their
//	             guards must be provably mutually exclusive (recognizer added
//	             in a later stage). Until then, loose is accepted but its
//	             exclusivity is not yet verified.
//	firstmatch — multiple transitions permitted; the first whose guard passes,
//	             in definition order, fires. Transition order is semantic.
const (
	determinismStrict     = "strict"
	determinismLoose      = "loose"
	determinismFirstMatch = "firstmatch"
)

func validDeterminism(level string) bool {
	switch level {
	case determinismStrict, determinismLoose, determinismFirstMatch:
		return true
	}
	return false
}

// groupTransitionsByStateInput maps each (state, input) to the list of
// transition indices that share it, expanding multi-source `from` lists. Used
// by the loose exclusivity check.
func groupTransitionsByStateInput(spec *fsmDefinitionSpec) (map[string][]int, error) {
	groups := make(map[string][]int)
	for i := range spec.Transitions {
		froms, err := spec.Transitions[i].fromStates()
		if err != nil {
			return nil, err
		}
		in := spec.Transitions[i].Input
		for _, f := range froms {
			key := f + "\x00" + in
			groups[key] = append(groups[key], i)
		}
	}
	return groups, nil
}

// firstDuplicateEdge returns the first (state, input) pair that has more than
// one transition, expanding multi-source `from` lists. Used to enforce
// determinism: strict. found is false when every (state, input) is unique.
func firstDuplicateEdge(spec *fsmDefinitionSpec) (state, input string, found bool) {
	seen := make(map[string]struct{})
	for i := range spec.Transitions {
		froms, err := spec.Transitions[i].fromStates()
		if err != nil {
			// Malformed `from` is reported elsewhere; skip for this check.
			continue
		}
		in := spec.Transitions[i].Input
		for _, f := range froms {
			key := f + "\x00" + in
			if _, dup := seen[key]; dup {
				return f, in, true
			}
			seen[key] = struct{}{}
		}
	}
	return "", "", false
}

// fsmAnalysis is the analysis block returned on successful create/validate.
type fsmAnalysis struct {
	Reachable           bool     `json:"reachable"`
	Deterministic       bool     `json:"deterministic"`
	Determinism         string   `json:"determinism"`
	ExclusivityVerified bool     `json:"exclusivity_verified,omitempty"`
	TerminalStates      []string `json:"terminal_states"`
	Cycles              []string `json:"cycles,omitempty"`
	Warnings            []string `json:"warnings,omitempty"`
}

// ---- Spec -> toolkit FSM ----

// buildToolkitFSM constructs a fsm-toolkit *FSM from a definition spec. It
// does not validate; call validateDefinition for that. Transitions with a
// list 'from' are expanded into one toolkit transition per source state, so
// the toolkit sees a flat transition list.
func buildToolkitFSM(spec *fsmDefinitionSpec) (*toolkit.FSM, error) {
	// A definition with any transition output is Mealy; otherwise a plain DFA.
	hasOutput := false
	for i := range spec.Transitions {
		if spec.Transitions[i].Output != "" {
			hasOutput = true
			break
		}
	}
	t := toolkit.TypeDFA
	if hasOutput {
		t = toolkit.TypeMealy
	}

	f := toolkit.New(t)
	f.Name = spec.Name
	f.Description = spec.Description
	f.SetInitial(spec.Initial)

	for state := range spec.States {
		f.AddState(state)
	}

	var accepting []string
	for state, ss := range spec.States {
		if ss.Terminal {
			accepting = append(accepting, state)
		}
	}
	f.SetAccepting(accepting)

	if len(spec.OutputAlphabet) > 0 {
		f.OutputAlphabet = append([]string(nil), spec.OutputAlphabet...)
		for _, o := range spec.OutputAlphabet {
			f.AddOutput(o)
		}
	}

	for i := range spec.Transitions {
		ts := &spec.Transitions[i]
		froms, err := ts.fromStates()
		if err != nil {
			return nil, err
		}
		input := ts.Input
		var inPtr *string
		if input != "" {
			inPtr = &input
			f.AddInput(input)
		}
		var outPtr *string
		if ts.Output != "" {
			out := ts.Output
			outPtr = &out
		}
		for _, from := range froms {
			f.AddTransition(from, inPtr, []string{ts.To}, outPtr)
		}
	}

	for state, machine := range spec.LinkedStates {
		f.SetLinkedMachine(state, fmt.Sprintf("%d", machine))
	}

	return f, nil
}

// ---- Validation ----

// fsmValidationError carries a code/message pair so handlers can map it to
// the correct XOLU-FSM error and HTTP status.
type fsmValidationError struct {
	Code    string
	Message string
}

func (e *fsmValidationError) Error() string { return e.Message }

// validateDefinition runs the full S7 validation pipeline against a spec and
// returns the analysis block on success. On failure it returns a
// *fsmValidationError naming the appropriate XOLU-FSM code.
//
// The pipeline, in order:
//  1. Basic shape: states present, initial present and declared.
//  2. Structural integrity via toolkit.Validate().
//  3. Output-alphabet membership: every transition output is listed.
//  4. Lifecycle: every non-terminal state reaches a terminal state.
//  5. Guard and set expression syntax via pkg/fsm/eval (parse only).
func validateDefinition(spec *fsmDefinitionSpec, ev *eval.Evaluator) (*fsmAnalysis, error) {
	// Determinism is mandatory. A definition without an explicit, valid level
	// cannot be created or instantiated.
	if spec.Determinism == "" {
		return nil, &fsmValidationError{
			Code:    "XOLU-FSM006",
			Message: "determinism is required: declare determinism as \"strict\", \"loose\", or \"firstmatch\"",
		}
	}
	if !validDeterminism(spec.Determinism) {
		return nil, &fsmValidationError{
			Code:    "XOLU-FSM006",
			Message: fmt.Sprintf("invalid determinism %q: must be \"strict\", \"loose\", or \"firstmatch\"", spec.Determinism),
		}
	}

	if len(spec.States) == 0 {
		return nil, &fsmValidationError{Code: "XOLU-FSM006", Message: "definition declares no states"}
	}
	if spec.Initial == "" {
		return nil, &fsmValidationError{Code: "XOLU-FSM006", Message: "definition has no initial state"}
	}
	if _, ok := spec.States[spec.Initial]; !ok {
		return nil, &fsmValidationError{
			Code:    "XOLU-FSM006",
			Message: fmt.Sprintf("initial state %q is not declared in states", spec.Initial),
		}
	}

	// strict determinism: reject any (state, input) with more than one
	// transition. This is the decidable structural check; loose/firstmatch
	// permit multiple edges per (state, input).
	if spec.Determinism == determinismStrict {
		if dupState, dupInput, found := firstDuplicateEdge(spec); found {
			return nil, &fsmValidationError{
				Code: "XOLU-FSM006",
				Message: fmt.Sprintf(
					"determinism \"strict\" requires at most one transition per (state, input), "+
						"but state %q has multiple transitions on input %q; "+
						"use \"loose\" (with mutually exclusive guards) or \"firstmatch\"",
					dupState, dupInput),
			}
		}
	}

	f, err := buildToolkitFSM(spec)
	if err != nil {
		return nil, &fsmValidationError{Code: "XOLU-FSM006", Message: err.Error()}
	}

	if err := f.Validate(); err != nil {
		return nil, &fsmValidationError{Code: "XOLU-FSM006", Message: err.Error()}
	}

	// Output-alphabet membership.
	allowed := make(map[string]struct{}, len(spec.OutputAlphabet))
	for _, o := range spec.OutputAlphabet {
		allowed[o] = struct{}{}
	}
	for i := range spec.Transitions {
		out := spec.Transitions[i].Output
		if out == "" {
			continue
		}
		if _, ok := allowed[out]; !ok {
			return nil, &fsmValidationError{
				Code:    "XOLU-FSM006",
				Message: fmt.Sprintf("transition output %q is not in output_alphabet", out),
			}
		}
	}

	// Lifecycle: every non-terminal state must have at least one path to a
	// terminal state. DeadStates() only catches states with zero outgoing
	// transitions, so it misses cycles that never reach a terminal (e.g. a
	// self-loop). Compute terminal-reachability directly: walk the transition
	// graph backwards from the terminal set and flag any non-terminal state
	// not reached.
	terminal := make(map[string]struct{})
	for state, ss := range spec.States {
		if ss.Terminal {
			terminal[state] = struct{}{}
		}
	}
	if len(terminal) == 0 {
		return nil, &fsmValidationError{
			Code:    "XOLU-FSM009",
			Message: "definition has no terminal state",
		}
	}
	// Build reverse adjacency: to -> [from...].
	reverse := make(map[string][]string)
	for i := range spec.Transitions {
		ts := &spec.Transitions[i]
		froms, err := ts.fromStates()
		if err != nil {
			return nil, &fsmValidationError{Code: "XOLU-FSM006", Message: err.Error()}
		}
		reverse[ts.To] = append(reverse[ts.To], froms...)
	}
	// BFS backwards from all terminal states.
	canReachTerminal := make(map[string]struct{}, len(spec.States))
	queue := make([]string, 0, len(terminal))
	for state := range terminal {
		canReachTerminal[state] = struct{}{}
		queue = append(queue, state)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, pred := range reverse[cur] {
			if _, seen := canReachTerminal[pred]; !seen {
				canReachTerminal[pred] = struct{}{}
				queue = append(queue, pred)
			}
		}
	}
	for state, ss := range spec.States {
		if ss.Terminal {
			continue
		}
		if _, ok := canReachTerminal[state]; !ok {
			return nil, &fsmValidationError{
				Code:    "XOLU-FSM009",
				Message: fmt.Sprintf("non-terminal state %q has no path to a terminal state", state),
			}
		}
	}

	// Guard and set expression syntax (parse only, no evaluation). Also
	// flags a real, silent footgun (reported by the Seam AMS team,
	// 2026-08-04): xolu's guard language is T-SQL, where '...' is a
	// string literal and "..." is a quoted IDENTIFIER -- the opposite
	// of JSON/JavaScript convention. A guard written with a double-
	// quoted string, e.g. payload.x != "", parses without error but
	// silently compares against nothing (a lookup for an identifier
	// with that literal text), never what the author meant. Confirmed
	// directly this couldn't be auto-corrected instead of flagged:
	// payload."odd key" was, before XOLU-FSM014 closed it, the only
	// syntax able to reference a payload field whose name wasn't a
	// bare identifier -- rewriting every " to ' would have silently
	// broken that legitimate case rather than the mistaken one. See
	// hasSuspiciousDoubleQuote's own doc comment for the detection
	// approach and why it works at the raw-text level, not the parsed
	// AST (the quote-style distinction does not survive tokenizing).
	var guardSyntaxWarnings []string
	for i := range spec.Transitions {
		ts := &spec.Transitions[i]
		if ts.Guard != "" {
			if _, err := eval.ParseGuard(ts.Guard); err != nil {
				return nil, &fsmValidationError{
					Code:    "XOLU-FSM011",
					Message: fmt.Sprintf("guard %q failed to parse: %v", ts.Guard, err),
				}
			}
			if hasSuspiciousDoubleQuote(ts.Guard) {
				froms, _ := ts.fromStates()
				guardSyntaxWarnings = append(guardSyntaxWarnings, fmt.Sprintf(
					"guard %q on transition %v->%q contains a double-quoted "+
						"string -- xolu's guard syntax uses single quotes for "+
						"string literals ('...'); double quotes (\"...\") are "+
						"a quoted identifier reference, almost certainly not "+
						"what was intended here",
					ts.Guard, froms, ts.To))
			}
		}
		for name, expr := range ts.Set {
			if _, err := eval.ParseGuard(expr); err != nil {
				return nil, &fsmValidationError{
					Code:    "XOLU-FSM011",
					Message: fmt.Sprintf("set clause for %s (%q) failed to parse: %v", name, expr, err),
				}
			}
			if hasSuspiciousDoubleQuote(expr) {
				froms, _ := ts.fromStates()
				guardSyntaxWarnings = append(guardSyntaxWarnings, fmt.Sprintf(
					"set clause %q (%q) on transition %v->%q contains a "+
						"double-quoted string -- xolu's guard syntax uses "+
						"single quotes for string literals ('...'); double "+
						"quotes (\"...\") are a quoted identifier reference, "+
						"almost certainly not what was intended here",
					name, expr, froms, ts.To))
			}
		}
	}

	// determinism: loose — every (state, input) group with multiple
	// transitions must have provably mutually exclusive guards. The recognizer
	// supplies the precise reason when it cannot prove exclusivity, which
	// becomes the error message. This is where a machine that declares loose but
	// is not actually exclusive is caught at definition time.
	if spec.Determinism == determinismLoose {
		groups, err := groupTransitionsByStateInput(spec)
		if err != nil {
			return nil, &fsmValidationError{Code: "XOLU-FSM006", Message: err.Error()}
		}
		for _, grp := range groups {
			if len(grp) <= 1 {
				continue
			}
			gexprs := make([]eval.GuardExpr, 0, len(grp))
			for _, ti := range grp {
				ts := &spec.Transitions[ti]
				if ts.Guard == "" {
					// An unguarded edge in a multi-edge loose group always fires,
					// so it cannot be mutually exclusive with anything.
					return nil, &fsmValidationError{
						Code: "XOLU-FSM006",
						Message: fmt.Sprintf(
							"determinism \"loose\": transition %q from a state with multiple "+
								"transitions on that input has no guard, so it always fires and "+
								"cannot be mutually exclusive; add a guard or declare \"firstmatch\"",
							ts.Input),
					}
				}
				node, perr := eval.ParseGuard(ts.Guard)
				if perr != nil {
					return nil, &fsmValidationError{
						Code:    "XOLU-FSM011",
						Message: fmt.Sprintf("guard %q failed to parse: %v", ts.Guard, perr),
					}
				}
				gexprs = append(gexprs, eval.GuardExpr{Source: ts.Guard, AST: node})
			}
			res := eval.CheckExclusivity(gexprs)
			if !res.Exclusive {
				return nil, &fsmValidationError{
					Code: "XOLU-FSM006",
					Message: fmt.Sprintf(
						"determinism \"loose\" requires mutually exclusive guards, but %s. "+
							"Make the guards mutually exclusive (e.g. add IS NULL / IS NOT NULL "+
							"presence checks, or use distinct equality values), or declare "+
							"\"firstmatch\" if transition order is intended to decide.",
						res.Reason),
				}
			}
		}
	}

	// Analysis block.
	analysis := &fsmAnalysis{
		Reachable:           len(f.UnreachableStates()) == 0,
		Deterministic:       len(f.NonDeterministicStates()) == 0,
		Determinism:         spec.Determinism,
		ExclusivityVerified: spec.Determinism == determinismLoose,
		TerminalStates:      terminalStateList(spec),
	}
	for _, w := range f.Analyse() {
		analysis.Warnings = append(analysis.Warnings, w.Message)
	}
	analysis.Warnings = append(analysis.Warnings, guardSyntaxWarnings...)
	return analysis, nil
}

// hasSuspiciousDoubleQuote reports whether expr contains a double-quote
// character outside any single-quoted string literal.
//
// Deliberately a raw-text scan, not an AST walk: checked directly before
// choosing this approach (2026-08-04) that tsqlparser's own lexer does
// not preserve the distinction downstream -- a double-quoted token and a
// bare identifier both come out as the same IDENT token type once
// tokenized, so there is no way to ask a parsed guard's AST "was this
// originally double-quoted." The information only exists in the raw
// source text, before lexing.
//
// Correct in the presence of escaped single-quotes: T-SQL's own escaping
// convention is to double a literal single-quote inside a string
// ('it”s fine'), and toggling a boolean on every ' character handles
// that correctly without special-casing it -- two toggles from the
// escaped pair cancel out, leaving the scanner in the same in/out-of-
// string state it was already in.
//
// This flags a real quote character appearing anywhere outside a
// single-quoted string, including inside what would otherwise be a
// legitimate quoted-identifier reference (payload."odd_key"). That is
// intentional, not a gap: XOLU-FSM014 (validatePayloadKeys,
// pkg/server/v2_fsm_walk.go) now rejects any payload key that isn't a
// bare strict identifier, so a transition payload can never actually
// carry a field needing that syntax -- confirmed directly before
// relying on it, not assumed. A double-quoted token in a guard is
// always worth a second look now, not merely usually.
func hasSuspiciousDoubleQuote(expr string) bool {
	inSingle := false
	for i := 0; i < len(expr); i++ {
		switch expr[i] {
		case '\'':
			inSingle = !inSingle
		case '"':
			if !inSingle {
				return true
			}
		}
	}
	return false
}

// terminalStateList returns the declared terminal states in a stable form.
func terminalStateList(spec *fsmDefinitionSpec) []string {
	var out []string
	for state, ss := range spec.States {
		if ss.Terminal {
			out = append(out, state)
		}
	}
	return out
}

// ---- Machine snapshot and overrides ----

// fsmOverrides is the overrides block accepted at machine creation and patch.
// variables overrides a variable's default; transitions overrides a named
// transition's guard, keyed by the transition's input symbol.
type fsmOverrides struct {
	Variables   map[string]fsmVariableSpec       `json:"variables,omitempty"`
	Transitions map[string]fsmTransitionOverride `json:"transitions,omitempty"`
}

// fsmTransitionOverride carries the overridable fields of a transition.
type fsmTransitionOverride struct {
	Guard *string `json:"guard,omitempty"`
}

// fsmMachineSnapshot is the self-contained copy a machine holds. After
// creation the machine reads only this; the source definition may be deleted
// or changed without effect. Children holds snapshotted linked-state child
// definitions, resolved by ID at creation time.
type fsmMachineSnapshot struct {
	Spec     fsmDefinitionSpec            `json:"spec"`
	Children map[string]fsmDefinitionSpec `json:"children,omitempty"`
}

// applyOverrides applies an overrides block to a snapshot spec in place. It
// returns a *fsmValidationError (XOLU-FSM013) if an override references a
// transition input not present in the spec. Variable overrides for unknown
// variables are added as new declarations (the spec permits per-instance
// variation); only transition-input mismatches are rejected.
func applyOverrides(spec *fsmDefinitionSpec, ov *fsmOverrides) error {
	if ov == nil {
		return nil
	}
	for name, vspec := range ov.Variables {
		if spec.Variables == nil {
			spec.Variables = make(map[string]fsmVariableSpec)
		}
		spec.Variables[name] = vspec
	}
	for input, tov := range ov.Transitions {
		found := false
		for i := range spec.Transitions {
			if spec.Transitions[i].Input == input {
				found = true
				if tov.Guard != nil {
					spec.Transitions[i].Guard = *tov.Guard
				}
			}
		}
		if !found {
			return &fsmValidationError{
				Code:    "XOLU-FSM013",
				Message: fmt.Sprintf("override references transition input %q not present in definition", input),
			}
		}
	}
	return nil
}

// initialVars builds the initial variable-value map from a spec's variable
// declarations, using each declaration's default.
func initialVars(spec *fsmDefinitionSpec) map[string]interface{} {
	vars := make(map[string]interface{}, len(spec.Variables))
	for name, vspec := range spec.Variables {
		vars[name] = vspec.Default
	}
	return vars
}
