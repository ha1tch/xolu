// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package server

// v2_gen_config.go
//
// S10: typed configuration for named stateful generators.
//
// A generator is a named definition stored in gen_definitions as
// (tenant_id, type, name, config_json). The config_json is a typed structure
// specific to the generator type. This file defines those typed structs, their
// validation (run at define time so a bad definition is rejected up front, not
// at first use), and the dispatch from a stored (type, config_json) pair to a
// generated value by calling the underlying generator logic.
//
// The generator logic itself (genToken, genNanoID, genRandomInt, genTimestamp)
// lives in v2_gen_stateless_handlers.go and is reused unchanged.

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/ha1tch/xolu/pkg/storage"
)

// genType enumerates the stateful generator types dispatchable by name.
// (sequence is handled separately via its own table and NEXT VALUE FOR / @SEQ.)
type genType string

const (
	genTypeToken     genType = "token"
	genTypeNanoID    genType = "nanoid"
	genTypeRandomInt genType = "random_int"
	genTypeTimestamp genType = "timestamp"
	genTypePick      genType = "pick"
	genTypeSlug      genType = "slug"
)

// validGenType reports whether t is a generator type this batch can dispatch.
func validGenType(t string) bool {
	switch genType(t) {
	case genTypeToken, genTypeNanoID, genTypeRandomInt, genTypeTimestamp,
		genTypePick, genTypeSlug:
		return true
	default:
		return false
	}
}

// ─── Typed configs ────────────────────────────────────────────────────────────
// Each config validates itself and knows how to produce a value by delegating
// to the existing generator logic. Pointers in the JSON-decoded form let
// validation distinguish "unset" (use default) from "explicitly set to zero".

type tokenConfig struct {
	Length *int `json:"length,omitempty"`
}

func (c tokenConfig) validate() error {
	if c.Length != nil && (*c.Length < tokenMinLength || *c.Length > tokenMaxLength) {
		return fmt.Errorf("token length must be between %d and %d", tokenMinLength, tokenMaxLength)
	}
	return nil
}

func (c tokenConfig) generate() (string, error) {
	n := tokenDefaultLength
	if c.Length != nil {
		n = *c.Length
	}
	return genToken(n), nil
}

type nanoidConfig struct {
	Alphabet string `json:"alphabet,omitempty"`
	Length   *int   `json:"length,omitempty"`
}

func (c nanoidConfig) validate() error {
	if c.Length != nil && (*c.Length < nanoidMinLength || *c.Length > nanoidMaxLength) {
		return fmt.Errorf("nanoid length must be between %d and %d", nanoidMinLength, nanoidMaxLength)
	}
	// A single-character alphabet produces a degenerate (constant) id; reject it.
	if c.Alphabet != "" && len(c.Alphabet) < 2 {
		return fmt.Errorf("nanoid alphabet must have at least 2 characters")
	}
	return nil
}

func (c nanoidConfig) generate() (string, error) {
	n := nanoidDefaultLength
	if c.Length != nil {
		n = *c.Length
	}
	return genNanoID(c.Alphabet, n), nil
}

type randomIntConfig struct {
	Min *int64 `json:"min,omitempty"`
	Max *int64 `json:"max,omitempty"`
}

func (c randomIntConfig) validate() error {
	if c.Min == nil || c.Max == nil {
		return fmt.Errorf("random_int requires both min and max")
	}
	if *c.Max < *c.Min {
		return fmt.Errorf("random_int max must be >= min")
	}
	return nil
}

func (c randomIntConfig) generate() (string, error) {
	return fmt.Sprintf("%d", genRandomInt(*c.Min, *c.Max)), nil
}

type timestampConfig struct {
	Zone   string `json:"zone,omitempty"`
	Layout string `json:"layout,omitempty"`
}

func (c timestampConfig) validate() error {
	// A bad zone is caught at define time by attempting to generate once.
	if _, err := genTimestamp(c.Zone, c.Layout); err != nil {
		return err
	}
	return nil
}

func (c timestampConfig) generate() (string, error) {
	return genTimestamp(c.Zone, c.Layout)
}

// ─── Parse / validate / dispatch ──────────────────────────────────────────────

// ─── pick ─────────────────────────────────────────────────────────────────────
// Selects an element from a declared set. mode: "random" (uniform, with
// replacement) or "round_robin" (sequential, wraps). "weighted" is accepted by
// the spec but deferred to Part 2, so it is rejected at define time here.

const (
	pickModeRandom     = "random"
	pickModeRoundRobin = "round_robin"
	pickModeWeighted   = "weighted"
)

type pickConfig struct {
	Set     []string `json:"set,omitempty"`
	Mode    string   `json:"mode,omitempty"`
	Weights []int    `json:"weights,omitempty"`
}

func (c pickConfig) effectiveMode() string {
	if c.Mode == "" {
		return pickModeRandom
	}
	return c.Mode
}

func (c pickConfig) validate() error {
	if len(c.Set) == 0 {
		return fmt.Errorf("pick requires a non-empty set")
	}
	switch c.effectiveMode() {
	case pickModeRandom, pickModeRoundRobin:
		// ok
	case pickModeWeighted:
		return fmt.Errorf("pick weighted mode is not yet supported (deferred to Part 2)")
	default:
		return fmt.Errorf("pick mode must be random or round_robin")
	}
	return nil
}

// generate produces a value for stateless modes (random). Round-robin is
// stateful and must be produced via generateAt with a caller-supplied cursor;
// calling generate on a round-robin config returns a random element as a safe
// fallback (the HTTP/OQL layer routes round-robin through generateAt instead).
func (c pickConfig) generate() (string, error) {
	idx := genRandomInt(0, int64(len(c.Set)-1))
	return c.Set[idx], nil
}

// generateAt returns the element at index (mod set size). Used for round-robin
// where the caller advances and supplies the cursor.
func (c pickConfig) generateAt(index int) string {
	n := len(c.Set)
	if n == 0 {
		return ""
	}
	return c.Set[((index%n)+n)%n]
}

// ─── slug ─────────────────────────────────────────────────────────────────────
// Human-readable random identifier from built-in vocabularies. Part 1 ships
// fixed word lists and three composition modes; custom word lists are Part 2.

const (
	slugVocabAdjNoun    = "adjective-noun"
	slugVocabAdjAdjNoun = "adjective-adjective-noun"
	slugVocabWord       = "word"
)

// Small built-in word lists. Deliberately compact; custom lists arrive in S21.
var slugAdjectives = []string{
	"quiet", "brave", "swift", "calm", "bright", "bold", "gentle", "keen",
	"lucky", "noble", "proud", "sharp", "warm", "wise", "young", "eager",
}

var slugNouns = []string{
	"river", "mountain", "forest", "meadow", "harbor", "canyon", "valley",
	"summit", "island", "glacier", "desert", "lagoon", "prairie", "delta",
}

type slugConfig struct {
	Words      *int   `json:"words,omitempty"`
	Separator  string `json:"separator,omitempty"`
	Vocabulary string `json:"vocabulary,omitempty"`
}

func (c slugConfig) effectiveVocab() string {
	if c.Vocabulary == "" {
		return slugVocabAdjNoun
	}
	return c.Vocabulary
}

func (c slugConfig) effectiveSeparator() string {
	if c.Separator == "" {
		return "-"
	}
	return c.Separator
}

func (c slugConfig) validate() error {
	switch c.effectiveVocab() {
	case slugVocabAdjNoun, slugVocabAdjAdjNoun, slugVocabWord:
		// ok
	default:
		return fmt.Errorf("slug vocabulary must be adjective-noun, adjective-adjective-noun, or word")
	}
	if c.Words != nil && (*c.Words < 1 || *c.Words > 8) {
		return fmt.Errorf("slug words must be between 1 and 8")
	}
	return nil
}

func (c slugConfig) generate() (string, error) {
	pick := func(list []string) string {
		return list[genRandomInt(0, int64(len(list)-1))]
	}
	var parts []string
	switch c.effectiveVocab() {
	case slugVocabAdjNoun:
		parts = []string{pick(slugAdjectives), pick(slugNouns)}
	case slugVocabAdjAdjNoun:
		parts = []string{pick(slugAdjectives), pick(slugAdjectives), pick(slugNouns)}
	case slugVocabWord:
		// `words` count of nouns (single word list).
		n := 1
		if c.Words != nil {
			n = *c.Words
		}
		for i := 0; i < n; i++ {
			parts = append(parts, pick(slugNouns))
		}
	}
	sep := c.effectiveSeparator()
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += sep
		}
		out += p
	}
	return out, nil
}

// genConfig is the common behaviour every typed config provides.
type genConfig interface {
	validate() error
	generate() (string, error)
}

// parseGenConfig decodes config_json into the typed config for the given type.
// Unknown fields are rejected so a typo in a definition is caught rather than
// silently ignored. The returned config is NOT yet validated — call validate().
func parseGenConfig(t string, configJSON []byte) (genConfig, error) {
	if len(configJSON) == 0 {
		configJSON = []byte("{}")
	}
	dec := func(target interface{}) error {
		return strictUnmarshal(configJSON, target)
	}
	switch genType(t) {
	case genTypeToken:
		var c tokenConfig
		if err := dec(&c); err != nil {
			return nil, err
		}
		return c, nil
	case genTypeNanoID:
		var c nanoidConfig
		if err := dec(&c); err != nil {
			return nil, err
		}
		return c, nil
	case genTypeRandomInt:
		var c randomIntConfig
		if err := dec(&c); err != nil {
			return nil, err
		}
		return c, nil
	case genTypeTimestamp:
		var c timestampConfig
		if err := dec(&c); err != nil {
			return nil, err
		}
		return c, nil
	case genTypePick:
		var c pickConfig
		if err := dec(&c); err != nil {
			return nil, err
		}
		return c, nil
	case genTypeSlug:
		var c slugConfig
		if err := dec(&c); err != nil {
			return nil, err
		}
		return c, nil
	default:
		return nil, fmt.Errorf("unknown generator type %q", t)
	}
}

// dispatchGen parses and validates the stored config for a generator type, then
// produces one value. This is the single point both the HTTP /next handler and
// the @GEN OQL dispatcher call, so they share identical semantics.
func dispatchGen(t string, configJSON []byte) (string, error) {
	cfg, err := parseGenConfig(t, configJSON)
	if err != nil {
		return "", err
	}
	if err := cfg.validate(); err != nil {
		return "", err
	}
	return cfg.generate()
}

// strictUnmarshal decodes JSON rejecting unknown fields, so a misspelled config
// key is surfaced as an error at define time rather than silently dropped.
func strictUnmarshal(data []byte, target interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("invalid generator config: %w", err)
	}
	return nil
}

// ─── Server-side resolution and dispatch ──────────────────────────────────────

// genLookup reads a named generator's type and config_json from gen_definitions
// for the given tenant. Returns sql.ErrNoRows if no such generator exists.
// Sequences (type='sequence') are deliberately not resolvable via @GEN — they
// have their own @SEQ / NEXT VALUE FOR surface — so they are excluded here.
func genLookup(ctx context.Context, db *sql.DB, tenantID uint16, name string) (gtype string, configJSON []byte, err error) {
	var typ, cfg string
	err = db.QueryRowContext(ctx, `
		SELECT type, config_json
		FROM gen_definitions
		WHERE tenant_id = ? AND name = ? AND type != 'sequence'`,
		tenantID, name).Scan(&typ, &cfg)
	if err != nil {
		return "", nil, err
	}
	return typ, []byte(cfg), nil
}

// serverGenDispatcher returns the OQL @GEN dispatch closure for this server.
// It resolves the named generator in gen_definitions, then produces one value
// via dispatchGenStateful (so HTTP /next and @GEN behave identically, including
// round-robin cursor advancement).
func (s *Server) serverGenDispatcher() func(tenantID uint16, name string) (string, error) {
	return func(tenantID uint16, name string) (string, error) {
		store, err := s.storeForTenant(tenantID)
		if err != nil {
			return "", err
		}
		wdp, ok := store.(storage.WriterDBProvider)
		if !ok {
			return "", sql.ErrNoRows
		}
		typ, cfg, err := genLookup(context.Background(), wdp.WriterDB(), tenantID, name)
		if err != nil {
			return "", err
		}
		return s.dispatchGenStateful(tenantID, name, typ, cfg)
	}
}

// dispatchGenStateful produces one generated value, handling the one stateful
// case (pick round_robin) via the server's in-memory cursor and delegating
// everything else to the pure dispatchGen. Keeping this on the server means the
// HTTP /next handler and the @GEN dispatcher share identical semantics.
func (s *Server) dispatchGenStateful(tenantID uint16, name, gtype string, configJSON []byte) (string, error) {
	if genType(gtype) == genTypePick {
		cfg, err := parseGenConfig(gtype, configJSON)
		if err != nil {
			return "", err
		}
		if err := cfg.validate(); err != nil {
			return "", err
		}
		pc, ok := cfg.(pickConfig)
		if ok && pc.effectiveMode() == pickModeRoundRobin {
			idx := s.nextPickCursor(tenantID, name)
			return pc.generateAt(int(idx)), nil
		}
		// random mode falls through to the pure path
	}
	return dispatchGen(gtype, configJSON)
}

// nextPickCursor atomically returns the current round-robin index for a named
// pick generator and advances it. In-memory only (Part 1); a restart resets to
// 0, as documented. Persistence is deferred to S21.
func (s *Server) nextPickCursor(tenantID uint16, name string) int64 {
	key := fmt.Sprintf("%d:%s", tenantID, name)
	actual, _ := s.pickCursors.LoadOrStore(key, new(int64))
	p := actual.(*int64)
	// Post-increment: return current, then advance. atomic.AddInt64 returns the
	// new value, so subtract 1 to get the index used this call.
	return atomic.AddInt64(p, 1) - 1
}
