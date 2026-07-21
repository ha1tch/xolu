// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package storage

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/ha1tch/xolu/pkg/timeseries"
)

// Meta subject addressing (@C04c; plan item 7, wave-4 opening act).
//
// A meta subject is (kind, key): the thing an annotation attaches to.
// Two kind families share one namespace, distinguished by the dot:
//
//   - ENTITY kinds carry no dot: the kind IS the entity name ("users"),
//     the key is the entity's positive integer id as decimal text. This
//     is the original /meta surface, unchanged in shape.
//   - NAMESPACED kinds carry a dot ("ts.timeline", "cal.calendar"):
//     first-class primitive subjects per @C04c's list, each with its own
//     key validation. Kinds for primitives not yet built are registered
//     but GATED — the vocabulary is reserved, use is refused until the
//     primitive lands (bal at wave 4, dxp at wave 5).
//
// Meta stays engine-inert (@C04c law): nothing here is read by any
// engine; subjects are an addressing scheme for annotations only.

// MetaSubject is a validated subject address.
type MetaSubject struct {
	Kind string // entity name, or dotted namespaced kind
	Key  string // canonical key text (validated per kind)
}

// String renders the canonical "kind/key" form used in errors and logs.
func (s MetaSubject) String() string { return s.Kind + "/" + s.Key }

// subjectKindSpec validates keys for one namespaced kind and gates kinds
// whose primitive has not landed.
type subjectKindSpec struct {
	validate func(key string) (canonical string, err error)
	gated    bool // registered vocabulary, primitive not yet built
}

// namespacedKinds is the @C04c subject list. Adding a kind here (and
// flipping gated) is the entire act of giving a primitive meta subjects.
var namespacedKinds = map[string]subjectKindSpec{
	"ts.timeline": {validate: validateTimelineKey},
	"cal.calendar": {validate: validateOpaqueKey},
	"cal.booking":  {validate: validateOpaqueKey},
	"fsm.machine":  {validate: validateOpaqueKey},
	// Reserved for primitives that have not landed yet:
	"bal.account": {validate: validateOpaqueKey, gated: true}, // wave 4
	"dxp.def":     {validate: validateOpaqueKey, gated: true}, // wave 5
	"dxp.txn":     {validate: validateOpaqueKey, gated: true}, // wave 5
}

// validateTimelineKey routes the key through the sanctioned JSON
// crossing helper (@C04d): the id has one width, uint32, preserved here
// as at every other boundary.
func validateTimelineKey(key string) (string, error) {
	n, err := strconv.ParseInt(key, 10, 64)
	if err != nil {
		return "", fmt.Errorf("ts.timeline key must be a decimal integer: %q", key)
	}
	tid, err := timeseries.TimelineIDFromJSON(n)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(uint64(tid), 10), nil
}

// validateOpaqueKey accepts caller-chosen string ids (cal's external
// identity model, bal's namespaced account ids): non-empty, no
// whitespace or slash, bounded length.
func validateOpaqueKey(key string) (string, error) {
	if key == "" || len(key) > 256 {
		return "", fmt.Errorf("subject key must be 1..256 chars")
	}
	if strings.ContainsAny(key, " \t\n/") {
		return "", fmt.Errorf("subject key must not contain whitespace or '/'")
	}
	return key, nil
}

// ParseMetaSubject validates a (kind, key) pair from the API boundary and
// returns the canonical subject. entityNameOK validates undotted kinds as
// entity names (the server passes its validateEntityName; storage-level
// callers may pass nil to accept any well-formed entity name shape).
func ParseMetaSubject(kind, key string, entityNameOK func(string) error) (MetaSubject, error) {
	if strings.Contains(kind, ".") {
		spec, ok := namespacedKinds[kind]
		if !ok {
			return MetaSubject{}, fmt.Errorf("unknown subject kind %q", kind)
		}
		if spec.gated {
			return MetaSubject{}, fmt.Errorf("subject kind %q is reserved but not yet available", kind)
		}
		canon, err := spec.validate(key)
		if err != nil {
			return MetaSubject{}, fmt.Errorf("subject kind %q: %w", kind, err)
		}
		return MetaSubject{Kind: kind, Key: canon}, nil
	}
	// Entity kind: positive integer key, canonical decimal.
	if entityNameOK != nil {
		if err := entityNameOK(kind); err != nil {
			return MetaSubject{}, err
		}
	}
	n, err := strconv.Atoi(key)
	if err != nil || n <= 0 {
		return MetaSubject{}, fmt.Errorf("entity subject key must be a positive integer: %q", key)
	}
	return MetaSubject{Kind: kind, Key: strconv.Itoa(n)}, nil
}

// EntitySubject builds the subject for an entity row — the cascade-delete
// path's constructor.
func EntitySubject(entity string, id int) MetaSubject {
	return MetaSubject{Kind: entity, Key: strconv.Itoa(id)}
}
