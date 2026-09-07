// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package client

// types_obj.go — wire types for the obj.go client methods (XOT210,
// wave 15). Field shapes verified directly against
// pkg/server/v2_obj_handlers.go and v2_obj_promote_handlers.go's own
// request/response structs before being written here, not inferred
// from naming convention.

// ObjCapacity mirrors objCapacityJSON server-side. All three fields
// are optional (nil = unlimited on that dimension); CurWeightKg/
// CurVolumeM3/CurCount are read-only, server-computed running totals,
// never sent on a request.
type ObjCapacity struct {
	MaxWeightKg *float64 `json:"max_weight_kg,omitempty"`
	MaxVolumeM3 *float64 `json:"max_volume_m3,omitempty"`
	MaxCount    *int64   `json:"max_count,omitempty"`
	CurWeightKg float64  `json:"cur_weight_kg,omitempty"`
	CurVolumeM3 float64  `json:"cur_volume_m3,omitempty"`
	CurCount    int64    `json:"cur_count,omitempty"`
}

// ObjAttachRequest attaches obj capability to an entity. Subject is
// the "kind:key" shorthand (e.g. "vehicles:47") -- the server also
// accepts a structured form, but the shorthand is this client's own
// deliberate choice, matching how every other obj method here takes
// a plain subject string rather than a split kind/key pair.
type ObjAttachRequest struct {
	Subject  string      `json:"subject"`
	Capacity ObjCapacity `json:"capacity,omitempty"`
}

// ObjSubject is the shape every obj read/write that returns a single
// subject uses -- attach, get, move, and capacity-patch all return
// this same shape server-side (subjectResponse).
type ObjSubject struct {
	Subject      string      `json:"subject"`
	Capacity     ObjCapacity `json:"capacity,omitempty"`
	PositionKind string      `json:"position_kind"`
	LocLeafID    *string     `json:"loc_leaf_id,omitempty"`
}

// ObjListResult is GET /obj/list's own response shape (XOT209, the
// enumeration endpoint this client method wraps).
type ObjListResult struct {
	Subjects []ObjSubject `json:"subjects"`
}

// ObjMoveTarget is a move's own destination -- exactly one of the two
// shapes below, matching objMoveTargetJSON server-side precisely:
//   - loc_leaf: Kind: "loc_leaf", LocationID: the leaf's own id.
//   - obj (containment): Kind: "obj", Subject: the container's own
//     "kind:key" shorthand (T-120).
type ObjMoveTarget struct {
	Kind       string `json:"kind"`
	LocationID string `json:"location_id,omitempty"`
	Subject    string `json:"subject,omitempty"`
}

// ObjPositionResolved is ObjPositionResult's own "resolved" field --
// LocationID is set only when Kind is "loc_leaf".
type ObjPositionResolved struct {
	Kind       string  `json:"kind"`
	LocationID *string `json:"location_id,omitempty"`
}

// ObjPositionResult is GET /obj/{kind}/{key}/position's response.
// Chain is the resolution path (e.g. subject -> container -> ...
// -> loc_leaf, for a containment chain); AsOf is always the literal
// string "live" today -- the server has no historical-position query
// yet, named here rather than typed as a real timestamp that would
// silently misrepresent that.
type ObjPositionResult struct {
	Resolved ObjPositionResolved `json:"resolved"`
	Chain    []string            `json:"chain"`
	AsOf     string              `json:"as_of"`
}

// ObjContentsResult is GET /obj/{kind}/{key}/contents's response.
type ObjContentsResult struct {
	Contents []string `json:"contents"`
}

// ObjPromoteEntity selects or creates the entity a promotion attaches
// obj capability to -- exactly one of ExistingKey or Create must be
// set, matching the server's own XOR validation (XOLU rejects both-
// or-neither with ErrObjEntitySelectionInvalid).
type ObjPromoteEntity struct {
	Kind        string                 `json:"kind"`
	ExistingKey *int                   `json:"existing_key,omitempty"`
	Create      map[string]interface{} `json:"create,omitempty"`
}

// ObjPromotePosition is where the newly-promoted subject lands.
// Kind must be "obj" (containment into an already-attached subject)
// in this server release -- Subject is that container's own
// "kind:key" shorthand.
type ObjPromotePosition struct {
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
}

// ObjPromoteRequest promotes an entity to obj capability as one
// atomic operation together with a bal transfer -- a real,
// dxp-orchestrated transaction, not two separate calls a caller
// sequences themselves. Amount is a decimal string (@B04, matching
// every other bal-touching request in this client), not a float.
type ObjPromoteRequest struct {
	BalAccount string             `json:"bal_account"`
	ToAccount  string             `json:"to_account"`
	Amount     string             `json:"amount"`
	Scale      *uint8             `json:"scale,omitempty"`
	Memo       string             `json:"memo,omitempty"`
	Entity     ObjPromoteEntity   `json:"entity"`
	Position   ObjPromotePosition `json:"position"`
}

// ObjDemoteRequest reverses a promotion -- removes obj capability
// from Subject ("kind:key" shorthand) together with a bal transfer
// back out, the same atomic pairing ObjPromoteRequest makes going in.
type ObjDemoteRequest struct {
	Subject     string `json:"subject"`
	BalAccount  string `json:"bal_account"`
	FromAccount string `json:"from_account"`
	Amount      string `json:"amount"`
	Scale       *uint8 `json:"scale,omitempty"`
	Memo        string `json:"memo,omitempty"`
}

// ObjPromoteResult is the shared response shape for both ObjPromote
// and ObjDemote -- verified identical server-side (both handlers
// write the same five fields). Status/CommittedThrough/Reason mirror
// the dxp dispatch outcome directly, not a client-invented summary.
type ObjPromoteResult struct {
	Subject          string `json:"subject"`
	TxnID            int64  `json:"txn_id"`
	Status           string `json:"status"`
	CommittedThrough int    `json:"committed_through"`
	Reason           string `json:"reason"`
}
