// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// v2_obj_events.go — T-123 (wave 10): obj's own event feed
// (obj-01-rest-api.md §7). Mirrors fireEntityEvent's own shape
// exactly — the identical dispatchEvent(tenantID, event{...})
// mechanism every other primitive-specific trigger already uses, not
// a new event system. Fired from the handler layer, after a
// guard-bearing write has already committed successfully — matching
// this codebase's own established discipline (dispatchEvent's own
// doc comment: "the originating operation has already committed"),
// never from inside a store-level function that might still roll
// back.
//
// Scope note: "report-driven fence transition" (§7's own named
// trigger) fires unconditionally on a successful report call here,
// not conditioned on whether a fence membership actually changed —
// obj's own Report (position.go) routes through loc.Store.Report
// directly and gets back only success/error, not loc's own §8a
// no-op-detection detail. A caller wanting to distinguish "reported,
// nothing changed" from "reported, entered/exited a fence" needs
// loc's own event feed (also not built) or a future, deeper
// integration -- named here rather than silently assumed equivalent.

package server

import (
	"net/http"
	"strconv"

	"github.com/ha1tch/xolu/pkg/obj"
)

// fireObjEvent mirrors fireEntityEvent's own shape exactly. subject
// is obj's own canonical "kind:key" ref; kind/key are split for
// Entity/ID so an event consumer's own {{event.entity}}/{{event.id}}
// templates work identically to every other entity-addressed event
// this feed already emits.
func (s *Server) fireObjEvent(r *http.Request, eventType, subject string, data map[string]interface{}) {
	kind, key, found := cutSubject(subject)
	if !found {
		return // malformed subject_ref should never reach here post-validation; defensive, not expected
	}
	id, err := strconv.Atoi(key)
	if err != nil {
		return
	}
	tenantID := getTenantIDNumeric(r.Context())
	if data == nil {
		data = map[string]interface{}{}
	}
	s.dispatchEvent(tenantID, event{
		Type:   eventType,
		Entity: kind,
		ID:     id,
		Data:   data,
	})
}

// objPositionEventData is the shared shape every obj.move/obj.report/
// obj.promote/obj.demote event's own Data carries — the subject's own
// resulting position, in the same terms subjectResponse's own JSON
// already uses, so a webhook/OQL action consuming this event doesn't
// need a separate vocabulary from the ordinary GET response.
func objPositionEventData(sub *obj.Subject) map[string]interface{} {
	d := map[string]interface{}{"position_kind": string(sub.Position.Kind)}
	if sub.Position.Kind == obj.PositionKindLocLeaf {
		d["loc_leaf_id"] = sub.Position.LocLeafID
	}
	if sub.Position.Kind == obj.PositionKindObj {
		d["container_ref"] = sub.Position.ContainedBy
	}
	return d
}
