// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Commit templates (XOT234, XOT235).
//
// A /commit request that sets "templates": true may use these string values,
// each as the whole value, anywhere inside update.data and append[].data:
//
//	$now              the commit's instant: UTC, fixed-width nanoseconds
//	                  (2006-01-02T15:04:05.000000000Z, so it sorts as text), taken
//	                  once per request, strictly increasing across requests in
//	                  one xolu process, and the same value in every row and in
//	                  every timeseries event time of the request
//	$commit_seq       a per-tenant counter, durable, +1 per commit that uses it,
//	                  allocated inside the commit's transaction so it never has
//	                  gaps and a rolled-back commit does not consume one
//	$update.version   the _version the update leaves the entity at
//	$update.data      the document the update wrote (without id and _version)
//	$previous.data    the document the commit replaced or deleted, read inside
//	                  the transaction, or null if the entity did not exist
//	$previous.version the _version it had, or 0
//
// A string that starts with "$$" loses one "$" (so "$$now" stores "$now"). A
// string that looks like a token (a "$", lowercase letters and underscores,
// optionally one ".name") and is not one of the above is an error, to catch a
// typo; any other string, such as "$100", is data and is left alone. Without
// "templates": true nothing is interpreted.

// ErrCommitTemplate is returned (400) for an unusable template.
var ErrCommitTemplate = errors.New("invalid commit template")

// TemplateError carries the reason.
type TemplateError struct{ Message string }

func (e *TemplateError) Error() string        { return e.Message }
func (e *TemplateError) Is(target error) bool { return target == ErrCommitTemplate }

// CommitInstantLayout is the format of $now.
const CommitInstantLayout = "2006-01-02T15:04:05.000000000Z"

var commitClock struct {
	mu   sync.Mutex
	last int64
}

// NextCommitInstant returns the next commit instant: the wall clock, but never
// less than one nanosecond after the previous call in this process. For any two
// commits where one completes before the other starts, the later one gets the
// later instant. Across a restart it relies on the wall clock not having stepped
// back by more than the restart took.
func NextCommitInstant() string {
	commitClock.mu.Lock()
	defer commitClock.mu.Unlock()
	ns := time.Now().UTC().UnixNano()
	if ns <= commitClock.last {
		ns = commitClock.last + 1
	}
	commitClock.last = ns
	return time.Unix(0, ns).UTC().Format(CommitInstantLayout)
}

// commitInstantPersist decides whether commit instants are made durable (XOT247).
// It is on unless the server is configured otherwise (XOLU_COMMIT_INSTANT_PERSIST).
var commitInstantPersist atomic.Bool

func init() { commitInstantPersist.Store(true) }

// SetCommitInstantPersistence turns the durable record of commit instants on or
// off. Call it at startup, before the first store is opened.
func SetCommitInstantPersistence(on bool) { commitInstantPersist.Store(on) }

// SeedCommitInstant raises the process's commit clock to at least ns (Unix
// nanoseconds), so the next instant is later than every instant a previous
// process issued and recorded. It never lowers the clock.
func SeedCommitInstant(ns int64) {
	commitClock.mu.Lock()
	defer commitClock.mu.Unlock()
	if ns > commitClock.last {
		commitClock.last = ns
	}
}

// persistCommitInstant records now as the last commit instant of this store,
// inside the commit's transaction (XOT247). The table is created on first use,
// like commit_seq. An instant that does not parse is not recorded; it is never a
// reason to fail a commit.
func (s *SQLiteStore) persistCommitInstant(ctx context.Context, tx *sql.Tx, now string) error {
	t, err := time.Parse(CommitInstantLayout, now)
	if err != nil {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS commit_clock (
		tenant_id INTEGER NOT NULL PRIMARY KEY,
		last_ns   INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("commit_clock: create table: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO commit_clock (tenant_id, last_ns) VALUES (?, ?)
		ON CONFLICT(tenant_id) DO UPDATE SET last_ns = MAX(last_ns, excluded.last_ns)`,
		s.config.TenantID, t.UnixNano()); err != nil {
		return fmt.Errorf("commit_clock: record: %w", err)
	}
	return nil
}

// seedCommitInstant reads the last recorded instant of this store, if any, and
// raises the process's commit clock to it. Called when the store opens, which is
// before any request can take an instant. Best effort: no table or no row means
// nothing was ever recorded.
func (s *SQLiteStore) seedCommitInstant(ctx context.Context) {
	if !commitInstantPersist.Load() {
		return
	}
	var name string
	if err := s.db.QueryRowContext(ctx,
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'commit_clock'`).Scan(&name); err != nil {
		return
	}
	var last sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT MAX(last_ns) FROM commit_clock WHERE tenant_id = ?`, s.config.TenantID).Scan(&last); err != nil || !last.Valid {
		return
	}
	SeedCommitInstant(last.Int64)
}

// TemplateValues are the values the tokens stand for. A token whose Has flag is
// false is not available yet.
type TemplateValues struct {
	Now              string
	HasNow           bool
	CommitSeq        int64
	HasCommitSeq     bool
	UpdateVersion    int
	HasUpdateVersion bool
	UpdateData       map[string]interface{}
	HasUpdateData    bool
	PreviousData     interface{} // nil means null
	HasPrevious      bool
	PreviousVersion  int
}

var templateTokenShape = regexp.MustCompile(`^\$[a-z_]+(\.[a-z_]+)?$`)

var knownTemplateTokens = []string{"$commit_seq", "$now", "$previous.data", "$previous.version", "$update.data", "$update.version"}

func isKnownTemplateToken(s string) bool {
	i := sort.SearchStrings(knownTemplateTokens, s)
	return i < len(knownTemplateTokens) && knownTemplateTokens[i] == s
}

// TemplateTokensIn returns the known tokens used in v, and an error for an
// unknown token-shaped string.
func TemplateTokensIn(v interface{}) (map[string]bool, error) {
	used := map[string]bool{}
	var walk func(interface{}) error
	walk = func(x interface{}) error {
		switch t := x.(type) {
		case map[string]interface{}:
			for _, e := range t {
				if err := walk(e); err != nil {
					return err
				}
			}
		case []interface{}:
			for _, e := range t {
				if err := walk(e); err != nil {
					return err
				}
			}
		case string:
			if strings.HasPrefix(t, "$$") {
				return nil
			}
			if templateTokenShape.MatchString(t) {
				if !isKnownTemplateToken(t) {
					return &TemplateError{Message: fmt.Sprintf("unknown template %q; the templates are %s (write %q for the literal string)",
						t, strings.Join(knownTemplateTokens, ", "), "$"+t)}
				}
				used[t] = true
			}
		}
		return nil
	}
	if err := walk(v); err != nil {
		return nil, err
	}
	return used, nil
}

// ResolveTemplates returns a copy of v with the tokens replaced by the values in
// tv. A known token whose value is not available yet is left in place unless
// final is true, in which case it is an error. "$$" at the start of a string is
// unescaped.
func ResolveTemplates(v interface{}, tv TemplateValues, final bool) (interface{}, error) {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, e := range t {
			r, err := ResolveTemplates(e, tv, final)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, e := range t {
			r, err := ResolveTemplates(e, tv, final)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case string:
		if strings.HasPrefix(t, "$$") {
			return t[1:], nil
		}
		if !templateTokenShape.MatchString(t) {
			return t, nil
		}
		if !isKnownTemplateToken(t) {
			return nil, &TemplateError{Message: fmt.Sprintf("unknown template %q; the templates are %s (write %q for the literal string)",
				t, strings.Join(knownTemplateTokens, ", "), "$"+t)}
		}
		var val interface{}
		ok := false
		switch t {
		case "$now":
			val, ok = tv.Now, tv.HasNow
		case "$commit_seq":
			val, ok = tv.CommitSeq, tv.HasCommitSeq
		case "$update.version":
			val, ok = tv.UpdateVersion, tv.HasUpdateVersion
		case "$update.data":
			val, ok = tv.UpdateData, tv.HasUpdateData
		case "$previous.data":
			val, ok = tv.PreviousData, tv.HasPrevious
		case "$previous.version":
			val, ok = tv.PreviousVersion, tv.HasPrevious
		}
		if !ok {
			if final {
				return nil, &TemplateError{Message: fmt.Sprintf("template %s is not available here", t)}
			}
			return t, nil
		}
		return deepCopyJSON(val), nil
	}
	return v, nil
}

// deepCopyJSON copies the maps and slices of a decoded JSON value, so a value
// that is inserted in several places is not shared between them.
func deepCopyJSON(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(t))
		for k, e := range t {
			out[k] = deepCopyJSON(e)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, e := range t {
			out[i] = deepCopyJSON(e)
		}
		return out
	}
	return v
}

// TemplatePlaceholders returns the values a validator should see in place of
// the tokens it cannot know yet: typed like the real ones, so a schema that says
// "snapshot is an object" or "version is an integer" accepts the request.
func TemplatePlaceholders(updateData map[string]interface{}) TemplateValues {
	return TemplateValues{
		HasCommitSeq: true, CommitSeq: 1,
		HasUpdateVersion: true, UpdateVersion: 1,
		HasUpdateData: true, UpdateData: updateData,
		HasPrevious: true, PreviousData: map[string]interface{}{}, PreviousVersion: 0,
	}
}

// appendDataParts returns the data maps of the appends, for scanning.
func appendDataParts(appends []CommitAppend) []interface{} {
	out := make([]interface{}, 0, len(appends))
	for _, a := range appends {
		out = append(out, a.Data)
	}
	return out
}

// nextCommitSeq allocates the next per-tenant commit sequence number inside the
// commit's transaction (XOT235). The table is created on first use, so stores
// that never use $commit_seq never have it.
func (s *SQLiteStore) nextCommitSeq(ctx context.Context, tx *sql.Tx) (int64, error) {
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS commit_seq (
		tenant_id INTEGER NOT NULL PRIMARY KEY,
		seq       INTEGER NOT NULL
	)`); err != nil {
		return 0, fmt.Errorf("commit_seq: create table: %w", err)
	}
	var seq int64
	err := tx.QueryRowContext(ctx, `INSERT INTO commit_seq (tenant_id, seq) VALUES (?, 1)
		ON CONFLICT(tenant_id) DO UPDATE SET seq = seq + 1 RETURNING seq`, s.config.TenantID).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("commit_seq: allocate: %w", err)
	}
	return seq, nil
}

// previousForTemplates reads the entity a commit is about to replace or delete,
// inside the transaction, for $previous.data and $previous.version. A missing
// entity is (nil, 0, false). The document comes without id and _version.
func (s *SQLiteStore) previousForTemplates(ctx context.Context, tx *sql.Tx, entity string, id int) (interface{}, int, bool, error) {
	var doc map[string]interface{}
	var version int
	if spec := s.adapted.Get(entity); spec != nil {
		m, v, err := adaptedGetInTx(ctx, tx, spec, s.dialect, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows) {
				return nil, 0, false, nil
			}
			return nil, 0, false, fmt.Errorf("templates: read previous: %w", err)
		}
		doc = m
		version = v
	} else {
		var raw string
		err := tx.QueryRowContext(ctx,
			`SELECT data, _version FROM `+s.nodesTable()+` WHERE entity_type = ? AND id = ?`, entity, id).Scan(&raw, &version)
		if err == sql.ErrNoRows {
			return nil, 0, false, nil
		}
		if err != nil {
			return nil, 0, false, fmt.Errorf("templates: read previous: %w", err)
		}
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return nil, 0, false, fmt.Errorf("templates: decode previous: %w", err)
		}
	}
	out := make(map[string]interface{}, len(doc))
	for k, v := range doc {
		if k != "id" && k != "_version" {
			out[k] = v
		}
	}
	return out, version, true, nil
}
