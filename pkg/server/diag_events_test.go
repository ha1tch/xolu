package server_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// rawRecorder captures the verbatim bytes, headers, and timing of every webhook
// delivery, for diagnostic inspection of what actually reaches the wire.
type rawRecorder struct {
	mu    sync.Mutex
	calls []rawCall
}

type rawCall struct {
	at          time.Time
	body        []byte
	contentType string
}

func (h *rawRecorder) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.calls = append(h.calls, rawCall{
			at:          time.Now(),
			body:        b,
			contentType: r.Header.Get("Content-Type"),
		})
		h.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}
}

func (h *rawRecorder) snapshot() []rawCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]rawCall, len(h.calls))
	copy(out, h.calls)
	return out
}

// waitForN polls until at least n deliveries have arrived or the deadline
// passes, then returns whatever was captured.
func (h *rawRecorder) waitForN(n int, d time.Duration) []rawCall {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if calls := h.snapshot(); len(calls) >= n {
			return calls
		}
		time.Sleep(10 * time.Millisecond)
	}
	return h.snapshot()
}

func dumpCalls(t *testing.T, label string, calls []rawCall) {
	t.Logf("=========== %s: %d deliveries ===========", label, len(calls))
	for i, c := range calls {
		// Pretty-print the JSON body if it parses, else show raw.
		var pretty interface{}
		if err := json.Unmarshal(c.body, &pretty); err == nil {
			pj, _ := json.MarshalIndent(pretty, "    ", "  ")
			t.Logf("  [%d] Content-Type=%s\n    RAW: %s\n    PRETTY: %s", i, c.contentType, string(c.body), pj)
		} else {
			t.Logf("  [%d] Content-Type=%s (non-JSON)\n    %q", i, c.contentType, string(c.body))
		}
	}
}

// TestDiag_AllEventPaths drives every event path and dumps the raw payloads the
// webhook receives, so the actual firing behaviour can be inspected directly
// rather than inferred from narrow assertions.
//
// Run with: go test ./pkg/server/ -run TestDiag_AllEventPaths -v
func TestDiag_AllEventPaths(t *testing.T) {
	// ---- Path 1: standalone walk → fsm.output + fsm.step (default envelope) ----
	t.Run("standalone_walk_default_envelope", func(t *testing.T) {
		rec := &rawRecorder{}
		srv := httptest.NewServer(rec.handler())
		defer srv.Close()
		env := newV2Server(t)

		// Two defs, no body/jsonplate → each fires the DEFAULT envelope so we
		// see exactly what the raw event carries for output and step.
		defineSub(t, env, "fsm.output", "webhook", map[string]interface{}{"url": srv.URL})
		defineSub(t, env, "fsm.step", "webhook", map[string]interface{}{"url": srv.URL})

		id := newAssetMachine(t, env)
		walk(t, env, id, "ready_for_inspection", nil) // step, no output
		walk(t, env, id, "inspection_passed",
			map[string]interface{}{"result": "pass", "technician": "a"}) // step + output

		// Expect: 2 steps + 1 output = 3 deliveries (machine id used below only for context)
		_ = id
		calls := rec.waitForN(3, 2*time.Second)
		dumpCalls(t, "PATH 1 standalone walk (fsm.output + fsm.step, default envelope)", calls)
	})

	// ---- Path 2: standalone walk → fsm.step via jsonplate (see resolved state delta) ----
	t.Run("standalone_walk_step_jsonplate", func(t *testing.T) {
		rec := &rawRecorder{}
		srv := httptest.NewServer(rec.handler())
		defer srv.Close()
		env := newV2Server(t)

		defineSub(t, env, "fsm.step", "webhook", map[string]interface{}{
			"url": srv.URL,
			"jsonplate": map[string]interface{}{
				"machine":  map[string]interface{}{"$ref": "machine_id"},
				"from":     map[string]interface{}{"$ref": "previous"},
				"to":       map[string]interface{}{"$ref": "current"},
				"terminal": map[string]interface{}{"$ref": "terminal"},
				"vars":     map[string]interface{}{"$ref": "vars"},
			},
		})

		id := newAssetMachine(t, env)
		walk(t, env, id, "ready_for_inspection", nil)
		walk(t, env, id, "inspection_failed", map[string]interface{}{"result": "fail"}) // increments @retries

		calls := rec.waitForN(2, 2*time.Second)
		dumpCalls(t, "PATH 2 standalone walk fsm.step (jsonplate: from/to/terminal/vars)", calls)
	})

	// ---- Path 3: commit with embedded fsm_walk → commit.applied + fsm.output + fsm.step ----
	t.Run("commit_embedded_walk_all_three", func(t *testing.T) {
		rec := &rawRecorder{}
		srv := httptest.NewServer(rec.handler())
		defer srv.Close()
		env := newV2Server(t)

		// One def per type, all default envelope, so we see all three raw payloads.
		defineSub(t, env, "commit.applied", "webhook", map[string]interface{}{"url": srv.URL})
		defineSub(t, env, "fsm.output", "webhook", map[string]interface{}{"url": srv.URL})
		defineSub(t, env, "fsm.step", "webhook", map[string]interface{}{"url": srv.URL})

		// Set up a machine positioned to take an output-producing transition.
		id := newAssetMachine(t, env)
		walk(t, env, id, "ready_for_inspection", nil) // now in AwaitingInspection

		// Commit: update an entity AND run the inspection_passed walk atomically.
		// inspection_passed emits asset_activated and transitions to InService.
		commitBody := map[string]interface{}{
			"update": map[string]interface{}{
				"entity": "asset",
				"id":     9100,
				"data":   map[string]interface{}{"state": "checked"},
			},
			"fsm_walk": map[string]interface{}{
				"machine": id,
				"input":   "inspection_passed",
				"payload": map[string]interface{}{"result": "pass", "technician": "diag"},
			},
		}
		st, resp := doJSONRequest(t, "POST",
			fmt.Sprintf("%s/api/v1/tenant/default/commit", env.ts.URL), commitBody)
		t.Logf("commit response: status=%d body=%v", st, resp)

		// Expect 3 deliveries: commit.applied, fsm.output, fsm.step.
		calls := rec.waitForN(3, 2*time.Second)
		dumpCalls(t, "PATH 3 commit+embedded walk (commit.applied + fsm.output + fsm.step, default envelope)", calls)
	})

	// ---- Path 4: commit.applied via jsonplate (see affected REFs + request copy resolved) ----
	t.Run("commit_applied_jsonplate_full", func(t *testing.T) {
		rec := &rawRecorder{}
		srv := httptest.NewServer(rec.handler())
		defer srv.Close()
		env := newV2Server(t)

		defineSub(t, env, "commit.applied", "webhook", map[string]interface{}{
			"url": srv.URL,
			"jsonplate": map[string]interface{}{
				"all_affected": map[string]interface{}{"$ref": "affected"},
				"first_ref":    map[string]interface{}{"$ref": "affected[0].ref"},
				"first_id":     map[string]interface{}{"$ref": "affected[0].ref.id"},
				"created":      map[string]interface{}{"$ref": "affected[0].created"},
				"version":      map[string]interface{}{"$ref": "affected[0].version"},
				"the_request":  map[string]interface{}{"$ref": "request"},
			},
		})

		commitBody := map[string]interface{}{
			"update": map[string]interface{}{
				"entity": "asset",
				"id":     9200,
				"data":   map[string]interface{}{"state": "x"},
			},
			"append": []map[string]interface{}{
				{"entity": "audit_log", "data": map[string]interface{}{"note": "n"}},
			},
		}
		st, resp := doJSONRequest(t, "POST",
			fmt.Sprintf("%s/api/v1/tenant/default/commit", env.ts.URL), commitBody)
		t.Logf("commit response: status=%d body=%v", st, resp)

		calls := rec.waitForN(1, 2*time.Second)
		dumpCalls(t, "PATH 4 commit.applied (jsonplate: affected REFs, created, version, request copy)", calls)
	})
}
