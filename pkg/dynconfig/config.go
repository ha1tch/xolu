// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package dynconfig provides a concurrent-safe, file-backed store of runtime
// settings that can be changed without restarting the server.
//
// # Structure
//
// Settings are organised in a two-level namespace: namespace → key → value.
// Values are JSON scalars or objects; the package enforces well-formedness on
// every write. Built-in namespaces:
//
//   - "global"        — system-wide overrides (e.g. blob.max_bytes)
//   - "tenant.{name}" — per-tenant overrides
//
// Any other namespace string is accepted; the two above are conventions.
//
// # Persistence
//
// Settings are stored in a single JSON file:
//
//	{
//	  "global": {
//	    "blob.max_bytes": 104857600
//	  },
//	  "tenant.acme": {
//	    "blob.max_bytes": 10485760
//	  }
//	}
//
// The file is reloaded on a configurable interval. Writes go through Set,
// which updates the in-memory store and flushes to disk atomically.
//
// # Well-formedness
//
// A namespace must be a non-empty string containing only letters, digits,
// hyphens, underscores, and dots. A key follows the same rules. A value must
// be valid JSON. These constraints are enforced on every Set call; malformed
// input is rejected before touching the in-memory store or disk.
//
// On reload, the file is parsed and validated in full before the in-memory
// store is replaced. A malformed file leaves the existing store intact and
// logs a warning.
package dynconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode"

	"github.com/rs/zerolog/log"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

// Value is a raw JSON value. It may be a number, string, boolean, null,
// array, or object — anything that is valid JSON.
type Value = json.RawMessage

// store is the in-memory representation: namespace → key → raw JSON value.
type store map[string]map[string]Value

// DynConfig is the runtime configuration store.
type DynConfig struct {
	mu       sync.RWMutex
	data     store
	filePath string
}

// ---------------------------------------------------------------------------
// Constructor
// ---------------------------------------------------------------------------

// New creates a DynConfig backed by filePath. If the file exists it is loaded
// immediately; if it does not exist the store starts empty. Returns an error
// only if the file exists but cannot be parsed.
func New(filePath string) (*DynConfig, error) {
	dc := &DynConfig{
		data:     make(store),
		filePath: filePath,
	}
	if _, err := os.Stat(filePath); err == nil {
		if err := dc.reload(); err != nil {
			return nil, fmt.Errorf("dynconfig: load %s: %w", filePath, err)
		}
	}
	return dc, nil
}

// ---------------------------------------------------------------------------
// Read
// ---------------------------------------------------------------------------

// Get returns the raw JSON value for namespace/key, or nil if not set.
func (dc *DynConfig) Get(namespace, key string) Value {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	if ns, ok := dc.data[namespace]; ok {
		return ns[key]
	}
	return nil
}

// GetInt64 returns the int64 value for namespace/key.
// Returns (0, false) if the key is absent or not a JSON number.
func (dc *DynConfig) GetInt64(namespace, key string) (int64, bool) {
	raw := dc.Get(namespace, key)
	if raw == nil {
		return 0, false
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false
	}
	return n, true
}

// GetFloat64 returns the float64 value for namespace/key.
func (dc *DynConfig) GetFloat64(namespace, key string) (float64, bool) {
	raw := dc.Get(namespace, key)
	if raw == nil {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	return f, true
}

// GetString returns the string value for namespace/key.
func (dc *DynConfig) GetString(namespace, key string) (string, bool) {
	raw := dc.Get(namespace, key)
	if raw == nil {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// GetBool returns the bool value for namespace/key.
func (dc *DynConfig) GetBool(namespace, key string) (bool, bool) {
	raw := dc.Get(namespace, key)
	if raw == nil {
		return false, false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil {
		return false, false
	}
	return b, true
}

// Namespace returns a copy of all key/value pairs in a namespace.
// Returns nil if the namespace does not exist.
func (dc *DynConfig) Namespace(namespace string) map[string]Value {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	ns, ok := dc.data[namespace]
	if !ok {
		return nil
	}
	out := make(map[string]Value, len(ns))
	for k, v := range ns {
		cp := make(Value, len(v))
		copy(cp, v)
		out[k] = cp
	}
	return out
}

// Dump returns a deep copy of the entire store, suitable for serialisation.
func (dc *DynConfig) Dump() map[string]map[string]Value {
	dc.mu.RLock()
	defer dc.mu.RUnlock()
	out := make(map[string]map[string]Value, len(dc.data))
	for ns, kv := range dc.data {
		m := make(map[string]Value, len(kv))
		for k, v := range kv {
			cp := make(Value, len(v))
			copy(cp, v)
			m[k] = cp
		}
		out[ns] = m
	}
	return out
}

// ---------------------------------------------------------------------------
// Write
// ---------------------------------------------------------------------------

// Set writes a value to namespace/key. The value must be valid JSON. The
// namespace and key must match [a-zA-Z0-9._-]+. The in-memory store is
// updated and the file is flushed atomically. Returns an error on any
// validation failure or I/O error; the in-memory store is not modified if
// the flush fails.
func (dc *DynConfig) Set(namespace, key string, value Value) error {
	if err := validateName(namespace); err != nil {
		return fmt.Errorf("dynconfig: invalid namespace %q: %w", namespace, err)
	}
	if err := validateName(key); err != nil {
		return fmt.Errorf("dynconfig: invalid key %q: %w", key, err)
	}
	if err := validateJSON(value); err != nil {
		return fmt.Errorf("dynconfig: invalid value: %w", err)
	}

	dc.mu.Lock()
	defer dc.mu.Unlock()

	// Build new store with the change applied.
	next := dc.copyLocked()
	if next[namespace] == nil {
		next[namespace] = make(map[string]Value)
	}
	cp := make(Value, len(value))
	copy(cp, value)
	next[namespace][key] = cp

	if err := dc.flushLocked(next); err != nil {
		return fmt.Errorf("dynconfig: flush: %w", err)
	}
	dc.data = next
	return nil
}

// Delete removes namespace/key. If the namespace becomes empty it is removed
// too. Flushes to disk atomically. Returns nil if the key did not exist.
func (dc *DynConfig) Delete(namespace, key string) error {
	dc.mu.Lock()
	defer dc.mu.Unlock()

	next := dc.copyLocked()
	if ns, ok := next[namespace]; ok {
		delete(ns, key)
		if len(ns) == 0 {
			delete(next, namespace)
		}
	}

	if err := dc.flushLocked(next); err != nil {
		return fmt.Errorf("dynconfig: flush: %w", err)
	}
	dc.data = next
	return nil
}

// ---------------------------------------------------------------------------
// Reload (called by the watcher on each tick, and at startup)
// ---------------------------------------------------------------------------

func (dc *DynConfig) reload() error {
	raw, err := os.ReadFile(dc.filePath)
	if err != nil {
		return err
	}

	// Unmarshal into map[string]map[string]json.RawMessage.
	var parsed map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	// Validate every namespace name, key name, and value before accepting.
	for ns, kv := range parsed {
		if err := validateName(ns); err != nil {
			return fmt.Errorf("namespace %q: %w", ns, err)
		}
		for k, v := range kv {
			if err := validateName(k); err != nil {
				return fmt.Errorf("namespace %q key %q: %w", ns, k, err)
			}
			if err := validateJSON(v); err != nil {
				return fmt.Errorf("namespace %q key %q value: %w", ns, k, err)
			}
		}
	}

	// All valid — swap in atomically.
	next := make(store, len(parsed))
	for ns, kv := range parsed {
		m := make(map[string]Value, len(kv))
		for k, v := range kv {
			m[k] = v
		}
		next[ns] = m
	}

	dc.mu.Lock()
	dc.data = next
	dc.mu.Unlock()
	return nil
}

// Reload re-reads the backing file. A malformed file leaves the existing
// in-memory store intact and returns the error. This is the public form used
// by the watcher and by tests.
func (dc *DynConfig) Reload() error {
	return dc.reload()
}

// ---------------------------------------------------------------------------
// Flush helpers (called under dc.mu write lock)
// ---------------------------------------------------------------------------

func (dc *DynConfig) flushLocked(s store) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// Atomic write: write to temp file then rename.
	dir := filepath.Dir(dc.filePath)
	tmp, err := os.CreateTemp(dir, ".dynconfig-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, dc.filePath)
}

func (dc *DynConfig) copyLocked() store {
	next := make(store, len(dc.data))
	for ns, kv := range dc.data {
		m := make(map[string]Value, len(kv))
		for k, v := range kv {
			cp := make(Value, len(v))
			copy(cp, v)
			m[k] = cp
		}
		next[ns] = m
	}
	return next
}

// ---------------------------------------------------------------------------
// Watcher
// ---------------------------------------------------------------------------

// Watcher periodically reloads the backing file of a DynConfig.
// It follows the same Start/Stop lifecycle as every other worker in the
// server. On each tick it calls dc.Reload(); a malformed file is logged as a
// warning and the existing in-memory store is preserved.
type Watcher struct {
	dc       *DynConfig
	interval time.Duration
	stop     chan struct{}
	done     chan struct{}
}

// NewWatcher creates a Watcher. Call Start to begin reloading.
func NewWatcher(dc *DynConfig, interval time.Duration) *Watcher {
	return &Watcher{
		dc:       dc,
		interval: interval,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start launches the watcher goroutine.
func (w *Watcher) Start() {
	go w.run()
}

// Stop signals the watcher to stop and blocks until it has exited.
func (w *Watcher) Stop() {
	close(w.stop)
	<-w.done
}

func (w *Watcher) run() {
	defer close(w.done)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-ticker.C:
			if err := w.dc.Reload(); err != nil {
				log.Warn().Err(err).
					Str("file", w.dc.filePath).
					Msg("dynconfig: reload failed — keeping existing config")
			} else {
				log.Debug().
					Str("file", w.dc.filePath).
					Msg("dynconfig: reloaded")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Validation helpers
// ---------------------------------------------------------------------------

// validateName returns an error if s is not a non-empty string of letters,
// digits, hyphens, underscores, and dots.
func validateName(s string) error {
	if s == "" {
		return fmt.Errorf("must not be empty")
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) &&
			r != '-' && r != '_' && r != '.' {
			return fmt.Errorf("character %q not allowed (use letters, digits, -, _, .)", r)
		}
	}
	return nil
}

// validateJSON returns an error if raw is not syntactically valid JSON.
func validateJSON(raw json.RawMessage) error {
	if len(raw) == 0 {
		return fmt.Errorf("value must not be empty")
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	return nil
}

// TenantNamespace returns the conventional namespace string for a tenant.
func TenantNamespace(tenant string) string {
	return "tenant." + tenant
}
