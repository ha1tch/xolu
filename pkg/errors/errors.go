// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package errors defines structured API error codes for xolu.
//
// Every error returned to HTTP clients includes a stable, machine-readable
// code in the format XOLU-SSNNN where SS is a two-letter category and NNN
// is a three-digit sequence number. Client code should switch on the code
// field, not the human-readable message.
package errors

import "fmt"

// Code is a machine-readable error identifier.
type Code string

// APIError pairs a stable code with a human-readable message and an
// HTTP status hint. The status is a suggestion — handlers may override
// it if context requires a different HTTP status.
type APIError struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
	Status  int    `json:"status"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// New creates an APIError with a formatted message.
func New(code Code, status int, format string, args ...interface{}) *APIError {
	return &APIError{
		Code:    code,
		Message: fmt.Sprintf(format, args...),
		Status:  status,
	}
}

// Wrap creates an APIError wrapping an underlying error's message.
func Wrap(code Code, status int, err error) *APIError {
	return &APIError{
		Code:    code,
		Message: err.Error(),
		Status:  status,
	}
}

// ---------------------------------------------------------------------------
// Storage errors (ST)
// ---------------------------------------------------------------------------

const (
	ErrEntityNotFound   Code = "XOLU-ST001"
	ErrEntityExists     Code = "XOLU-ST002"
	ErrInvalidEntity    Code = "XOLU-ST003"
	ErrInvalidID        Code = "XOLU-ST004"
	ErrVersionConflict  Code = "XOLU-ST005"
	ErrStorageFailed    Code = "XOLU-ST006"
	ErrEntityTooLarge   Code = "XOLU-ST007"
	ErrSchemaNotFound   Code = "XOLU-ST008"
	ErrSchemaLoadFailed Code = "XOLU-ST009"
	// ErrPromoteInFlight is returned when POST .../entities/promote/strict/{type}
	// is called for an entity type that already has a strict promotion running
	// (pkg/server's PromoteJobManager, T-151). The existing ticket is included
	// in the error message so a caller can poll it directly instead of retrying.
	ErrPromoteInFlight Code = "XOLU-ST010"
	// ErrPromoteTicketNotFound is returned when GET
	// .../entities/promote/status/{ticket} names a ticket that was never
	// issued.
	ErrPromoteTicketNotFound Code = "XOLU-ST011"
)

// ---------------------------------------------------------------------------
// Graph errors (GR)
// ---------------------------------------------------------------------------

const (
	ErrCycleDetected     Code = "XOLU-GR001"
	ErrGraphDisabled     Code = "XOLU-GR002"
	ErrGraphUnsupported  Code = "XOLU-GR003"
	ErrGraphFailed       Code = "XOLU-GR004"
	ErrGraphVisitedLimit Code = "XOLU-GR005"
	ErrGraphResultLimit  Code = "XOLU-GR006"
	// ErrDuplicateEdgeRef is returned when an entity document contains two or
	// more REF fields that point to the same (entity, id) target. Each ordered
	// node pair in the graph carries at most one labelled edge; a document
	// violating this constraint is a client error, not an infrastructure fault.
	ErrDuplicateEdgeRef Code = "XOLU-GR007"
)

// ---------------------------------------------------------------------------
// Query errors — OQL and Sulpher (QL)
// ---------------------------------------------------------------------------

const (
	ErrQuerySyntax        Code = "XOLU-QL001"
	ErrQueryDepthExceeded Code = "XOLU-QL002"
	ErrQueryNotFound      Code = "XOLU-QL003"
	ErrQueryFailed        Code = "XOLU-QL004"
	ErrQueryRequired      Code = "XOLU-QL005"
	ErrQueryIDRequired    Code = "XOLU-QL006"
	ErrQueryEngineNotInit Code = "XOLU-QL007"
	ErrQueryTimeout       Code = "XOLU-QL008"
	ErrQueryRowLimit      Code = "XOLU-QL009"
	ErrQueryScanLimit     Code = "XOLU-QL010"
	ErrQueryResponseSize  Code = "XOLU-QL011"
	ErrSearchFailed       Code = "XOLU-QL012"
	ErrSearchDisabled     Code = "XOLU-QL013"
)

// ---------------------------------------------------------------------------
// Validation errors (VL)
// ---------------------------------------------------------------------------

const (
	ErrValidationFailed Code = "XOLU-VL001"
	ErrInvalidJSON      Code = "XOLU-VL002"
	ErrMissingParam     Code = "XOLU-VL003"
)

// ---------------------------------------------------------------------------
// Authentication and authorization errors (AU)
// ---------------------------------------------------------------------------

const (
	ErrUnauthorized Code = "XOLU-AU001"
	ErrInvalidToken Code = "XOLU-AU002"
	ErrForbidden    Code = "XOLU-AU003"
)

// ---------------------------------------------------------------------------
// Rate limiting errors (RL)
// ---------------------------------------------------------------------------

const (
	ErrRateLimited Code = "XOLU-RL001"
)

// ---------------------------------------------------------------------------
// Tenant errors (TN)
// ---------------------------------------------------------------------------

const (
	ErrTenantNotFound  Code = "XOLU-TN001"
	ErrTenantRequired  Code = "XOLU-TN002"
	ErrTenantForbidden Code = "XOLU-TN003"
)

// ---------------------------------------------------------------------------
// Configuration errors (CF)
// ---------------------------------------------------------------------------

const (
	ErrConfigInvalid Code = "XOLU-CF001"
)

// ---------------------------------------------------------------------------
// Commit errors (CM)
// ---------------------------------------------------------------------------

const (
	// ErrCMVersionConflict is returned when the Update version check fails.
	// The response body includes a current_version field.
	ErrCMVersionConflict Code = "XOLU-CM001"
	// ErrCMUpdateMissing is returned when the commit request has no update object.
	ErrCMUpdateMissing Code = "XOLU-CM002"
	// ErrCMAppendEmpty is returned when the append array is empty or absent.
	ErrCMAppendEmpty Code = "XOLU-CM003"
	// ErrCMAppendTooLarge is returned when append exceeds the 25-entry limit.
	ErrCMAppendTooLarge Code = "XOLU-CM004"
	// ErrCMInvalidUpdateEntity is returned when the update entity type is invalid.
	ErrCMInvalidUpdateEntity Code = "XOLU-CM005"
	// ErrCMInvalidAppendEntity is returned when an append entity type is invalid.
	ErrCMInvalidAppendEntity Code = "XOLU-CM006"
	// ErrCMAppendIDExists is returned when an explicit append ID already exists.
	ErrCMAppendIDExists Code = "XOLU-CM007"
	// ErrCMTransactionFailed is returned when the storage transaction fails.
	ErrCMTransactionFailed Code = "XOLU-CM008"
	// ErrCMNotAvailable is returned when the /commit endpoint is called
	// against a backend that does not support it. Currently this means the
	// jsonfile backend, which provides only best-effort atomicity and has
	// been deprecated for production use.
	ErrCMNotAvailable Code = "XOLU-CM009"

	// ErrCMTSDisabled is returned when timeseries events are included in a
	// CommitRequest but XOLU_TIMESERIES_ENABLED is false or the server was
	// started without a timeseries manager.
	ErrCMTSDisabled Code = "XOLU-CM010"

	// ErrCMTSNotProvisioned is returned when timeseries events are included
	// but the tenant has not been provisioned for timeseries storage via
	// POST /ts/provision.
	ErrCMTSNotProvisioned Code = "XOLU-CM011"

	// ErrCMTSBadTimeline is returned when a CommitTSEvent references a
	// timeline that is not defined for the tenant.
	ErrCMTSBadTimeline Code = "XOLU-CM012"

	// ErrCMTSBadDims is returned when a CommitTSEvent carries the wrong
	// number of dimension values for its declared timeline.
	ErrCMTSBadDims Code = "XOLU-CM013"

	// ErrCMTSBatchTooLarge is returned when the timeseries array in a
	// CommitRequest exceeds XOLU_TS_MAX_BATCH_SIZE.
	ErrCMTSBatchTooLarge Code = "XOLU-CM014"

	// ErrCMTSWriteFailed is returned when the Pebble timeseries write fails.
	// The SQLite transaction was not opened; the caller may retry the entire
	// /commit request safely.
	ErrCMTSWriteFailed Code = "XOLU-CM015"

	// ErrCMTSRollbackFailed is returned when the Pebble write succeeded but
	// the SQLite transaction failed AND the subsequent DeleteKeys tombstone
	// call also failed. Entity state is unchanged; the timeseries store may
	// contain an orphaned entry. Manual remediation is required.
	ErrCMTSRollbackFailed Code = "XOLU-CM016"
)

// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Blob store errors (BL)
// ---------------------------------------------------------------------------

const (
	// ErrBlobDisabled is returned when a blob API endpoint is called but
	// XOLU_BLOB_ENABLED is false or the server was started without a blob store.
	ErrBlobDisabled Code = "XOLU-BL001"
	// ErrBlobNotFound is returned when a key or SHA does not exist.
	ErrBlobNotFound Code = "XOLU-BL002"
	// ErrBlobTooLarge is returned when the content exceeds XOLU_BLOB_MAX_SIZE.
	ErrBlobTooLarge Code = "XOLU-BL003"
	// ErrBlobInvalidKey is returned when the caller-supplied key contains
	// disallowed characters or is otherwise malformed.
	ErrBlobInvalidKey Code = "XOLU-BL004"
	// ErrBlobStoreFailed is returned for unexpected filesystem errors.
	ErrBlobStoreFailed Code = "XOLU-BL005"
	// ErrBlobQuotaExceeded is returned when a Put would push a tenant's total
	// stored bytes over the configured XOLU_BLOB_MAX_TOTAL_BYTES limit.
	ErrBlobQuotaExceeded Code = "XOLU-BL006"
	// ErrBlobExportNotFound is returned when a GET .../blob/export/{ticket}
	// status check names a ticket that was never issued (or belongs to a
	// different tenant -- tickets are not shared across tenants).
	ErrBlobExportNotFound Code = "XOLU-BL007"
	// ErrBlobExportInFlight is returned when POST .../blob/export is called
	// for a tenant that already has a running export job -- the per-tenant
	// throttle (pkg/tenantexport.JobManager). The existing ticket is
	// included in the error response so a caller can poll it directly
	// instead of retrying the POST.
	ErrBlobExportInFlight Code = "XOLU-BL008"
)

// Dynamic configuration errors (DC)
// ---------------------------------------------------------------------------

const (
	// ErrDCDisabled is returned when a dynconfig API endpoint is called but
	// DynConfigEnabled or DynConfigAPIEnabled is false.
	ErrDCDisabled Code = "XOLU-DC001"
	// ErrDCNotFound is returned when a namespace or key does not exist.
	ErrDCNotFound Code = "XOLU-DC002"
	// ErrDCInvalidInput is returned when a namespace, key, or value fails
	// well-formedness validation.
	ErrDCInvalidInput Code = "XOLU-DC003"
	// ErrDCStoreFailed is returned for unexpected I/O errors on the backing file.
	ErrDCStoreFailed Code = "XOLU-DC004"
)

const (
	ErrTSNotAvailable     Code = "XOLU-TS001" // wrong tenant mode
	ErrTSNotEnabled       Code = "XOLU-TS002" // feature flag off
	ErrTSNotProvisioned   Code = "XOLU-TS003" // tenant not provisioned for TS
	ErrTSInvalidTrigger   Code = "XOLU-TS004"
	ErrTSInvalidTimestamp Code = "XOLU-TS005"
	ErrTSBatchTooLarge    Code = "XOLU-TS006"
	ErrTSMissingField     Code = "XOLU-TS007"
	ErrTSInvalidAggFunc   Code = "XOLU-TS008"
	ErrTSInvalidAggField  Code = "XOLU-TS009"
	ErrTSInvalidInterval  Code = "XOLU-TS010"
	ErrTSRangeTooWide     Code = "XOLU-TS011"
	ErrTSLimitExceeded    Code = "XOLU-TS012"
	ErrTSInternal         Code = "XOLU-TS013"
	ErrTSRetentionFailed  Code = "XOLU-TS014"
	ErrTSProvisionFailed  Code = "XOLU-TS015"
	// ErrTSDimsImmutable is returned when a caller attempts to change the
	// dimension count of a timeline after its first write.
	ErrTSDimsImmutable Code = "XOLU-TS016"
	// ErrTSNaNValue is returned when a Nums field contains a NaN value.
	ErrTSNaNValue Code = "XOLU-TS017"
	// ErrTSReservedID is returned when timeline ID 0x0000 is used; it is
	// reserved and may not be assigned to any user-defined timeline.
	ErrTSReservedID Code = "XOLU-TS018"
	// ErrTSBucketLimit is returned when a windowed aggregate query would
	// produce more time buckets than XOLU_TS_MAX_AGGREGATE_BUCKETS.
	ErrTSBucketLimit Code = "XOLU-TS019"

	// ErrTSInvalidWriteConfig is returned when a write-config request
	// contains an unrecognised field or an invalid value.
	ErrTSInvalidWriteConfig Code = "XOLU-TS020"

	// ErrTSWriteConfigSaveFailed is returned when the write-config file
	// cannot be persisted to disk.
	ErrTSWriteConfigSaveFailed Code = "XOLU-TS021"

	// ErrTSRootTimeline is returned when a rollup operation is attempted
	// on timeline 0, which is the structural root and carries no data.
	ErrTSRootTimeline Code = "XOLU-TS022"

	// ErrTSRollupCycle is returned when a proposed rollup definition would
	// create a cycle in the rollup tree.
	ErrTSRollupCycle Code = "XOLU-TS023"

	// ErrTSRollupDepth is returned when a proposed rollup definition would
	// exceed the maximum allowed rollup tree depth.
	ErrTSRollupDepth Code = "XOLU-TS024"

	// ErrTSRollupNotFound is returned when a rollup definition ID does not
	// exist on the specified source timeline.
	ErrTSRollupNotFound Code = "XOLU-TS025"

	// ErrTSRollupDestInUse is returned when the destination timeline is
	// already the target of another rollup definition (single-parent rule).
	ErrTSRollupDestInUse Code = "XOLU-TS026"
	// ErrTSSystemScopeID: a user-facing define named an id in the system
	// region under the store's sysmask width (@S §8). System ids are
	// mintable only via the system-internal path.
	ErrTSSystemScopeID Code = "XOLU-TS027"
)

const (
	// --- Referential integrity (@R) ---

	// ErrRIRestrictViolation is returned when a DELETE is refused because
	// live referrers under a restrict on_delete policy name the target
	// (@R02.2). HTTP 409. The SQL ON DELETE RESTRICT behaviour.
	ErrRIRestrictViolation Code = "XOLU-RI001"

	// ErrRICascadeBudget is returned when a cascade delete would exceed
	// the MaxCascadeDeletions budget; the whole operation fails before
	// anything is deleted (@R02.2). Stage 3. HTTP 409.
	ErrRICascadeBudget Code = "XOLU-RI002"

	// ErrRIValidateTarget is returned when a write-time validate check
	// finds the referenced target missing (@R02.3). Stage 4. HTTP 400.
	// Its in-transaction enforcement shipped early as the create-side
	// closure of the G-12 race (2026-07-21): target existence is checked
	// inside the write's own transaction, which with serialised writers
	// makes create-vs-delete linearisable in either commit order.
	ErrRIValidateTarget Code = "XOLU-RI003"

	// ErrRISchemaXRef is returned when an entity's schema carries a
	// malformed x-ref annotation (@R02.1). HTTP 400 at schema-load or
	// registry-build time.
	ErrRISchemaXRef Code = "XOLU-RI004"
)

const (
	// --- bal: conservation primitive (@B) ---

	// ErrBalBounds: transfer refused by the floor/ceiling guard — the
	// CAS predicate matched zero rows (@B06). HTTP 409.
	ErrBalBounds Code = "XOLU-BAL001"
	// ErrBalUnknownAccount: a transfer or query names an account that
	// does not exist (@B09). HTTP 404.
	ErrBalUnknownAccount Code = "XOLU-BAL002"
	// ErrBalSealedPeriod: entry dated within a sealed (closed) period
	// (@B07). HTTP 409.
	ErrBalSealedPeriod Code = "XOLU-BAL003"
	// ErrBalAmountScale: amount invalid for the account's scale or not
	// an exact integer of minor units (@B04). HTTP 400.
	ErrBalAmountScale Code = "XOLU-BAL004"
	// ErrBalNotPostable: transfer names a summary (non-postable)
	// account; only leaves are imputables (@B03a). HTTP 409.
	ErrBalNotPostable Code = "XOLU-BAL005"
	// ErrBalBackdated: entry dated strictly before the latest existing
	// entry on an append_only account (T-55, @B03). Distinct from
	// ErrBalSealedPeriod: this is a per-account default, overridable
	// by the account's own backdated policy; a sealed period is a
	// tenant-wide, policy-independent boundary. HTTP 409.
	ErrBalBackdated Code = "XOLU-BAL006"
	// ErrBalDuplicateAccount: DefineAccount refused — account_id
	// already defined. HTTP 409. Added post-hoc by adversarial testing
	// on /loc (T-118's own hardening pass), which found the identical
	// gap in bal's own DefineAccount: a UNIQUE constraint violation
	// had no typed error, falling through to a bare 500. Fixed here
	// alongside loc's own version (XOLU-LOC014) rather than left as a
	// flagged-but-unfixed finding.
	ErrBalDuplicateAccount Code = "XOLU-BAL007"
)

const (
	// --- Metadata (v2) ---

	// ErrMetaEntityNotFound is returned when the entity referenced in a
	// metadata request does not exist.
	ErrMetaEntityNotFound Code = "XOLU-META001"

	// ErrMetaKeyNotFound is returned when the requested metadata key does
	// not exist for the entity.
	ErrMetaKeyNotFound Code = "XOLU-META002"

	// ErrMetaValueTooLarge is returned when the value body exceeds
	// XOLU_META_MAX_VALUE_BYTES.
	ErrMetaValueTooLarge Code = "XOLU-META003"

	// ErrMetaInvalidKey is returned when the key contains characters outside
	// the allowed set: [a-zA-Z0-9_], max 64 characters.
	ErrMetaInvalidKey Code = "XOLU-META004"

	// ErrMetaInvalidExpiry is returned when expires_at is present but is
	// not a valid RFC3339 timestamp.
	ErrMetaInvalidExpiry Code = "XOLU-META005"

	// --- GC admin ---

	// ErrGCWorkerNotFound is returned when the named GC worker does not exist.
	ErrGCWorkerNotFound Code = "XOLU-GC001"

	// ErrGCSweepFailed is returned when a synchronous sweep triggered via
	// the admin endpoint returns an error.
	ErrGCSweepFailed Code = "XOLU-GC002"
)

const (
	// --- Stateful generators (v2 S5+) ---

	// ErrGenNameExists is returned when a generator with the given name
	// already exists under any type for the tenant.
	ErrGenNameExists Code = "XOLU-GEN002"

	// ErrGenNotFound is returned when the named generator does not exist.
	ErrGenNotFound Code = "XOLU-GEN003"

	// ErrGenInvalidConfig is returned when the generator definition is invalid
	// (e.g. increment_by=0, min > max, start out of range).
	ErrGenInvalidConfig Code = "XOLU-GEN004"

	// ErrGenExhausted is returned when a non-cyclic sequence has reached its
	// max_val and cannot produce further values.
	ErrGenExhausted Code = "XOLU-GEN005"

	// ErrGenCurrentBeforeNext is returned when @CURRENT_VALUE is called before
	// NEXT VALUE FOR in the same OQL session.
	ErrGenCurrentBeforeNext Code = "XOLU-GEN006"
)

const (
	// --- Event subscriptions (v2 S9) ---

	// ErrEventInvalid is returned when a subscription definition is malformed
	// (unknown event_type or action_type, or invalid action config).
	ErrEventInvalid Code = "XOLU-EV001"

	// ErrEventNotFound is returned when the referenced subscription does not exist.
	ErrEventNotFound Code = "XOLU-EV002"

	// ErrEventDeliveryFailed is returned/recorded when an action dispatch fails
	// (e.g. webhook endpoint unreachable). In Part 1 this is logged to the
	// delivery log rather than surfaced synchronously, since dispatch is async.
	ErrEventDeliveryFailed Code = "XOLU-EV003"
)

const (
	// --- FSM definitions and machines (v2 S7+) ---
	//
	// Note: XOLU-FSM007 is intentionally absent. The spec error table
	// (docs/API_V2.md) defines 001-006 and 008-013 with no 007. The codes
	// are not renumbered to close the gap.

	// ErrFSMDefNotFound is returned when the referenced definition does not
	// exist for the tenant.
	ErrFSMDefNotFound Code = "XOLU-FSM001"

	// ErrFSMMachineNotFound is returned when the referenced machine does not
	// exist for the tenant.
	ErrFSMMachineNotFound Code = "XOLU-FSM002"

	// ErrFSMNoTransition is returned when no transition exists for the given
	// input from the machine's current state.
	ErrFSMNoTransition Code = "XOLU-FSM003"

	// ErrFSMGuardRejected is returned when a transition's guard expression
	// evaluates false at walk time.
	ErrFSMGuardRejected Code = "XOLU-FSM004"

	// ErrFSMTerminal is returned when a walk is attempted on a machine that
	// is already in a terminal state.
	ErrFSMTerminal Code = "XOLU-FSM005"

	// ErrFSMValidation is returned when a definition or machine snapshot
	// fails structural validation (state/transition/output-alphabet
	// consistency, or post-patch validity).
	ErrFSMValidation Code = "XOLU-FSM006"

	// ErrFSMCommitConflict is returned when an FSM walk embedded in a commit
	// fails due to a version mismatch or guard failure.
	ErrFSMCommitConflict Code = "XOLU-FSM008"

	// ErrFSMNoTerminalReachable is returned when one or more non-terminal
	// states have no path to any terminal state.
	ErrFSMNoTerminalReachable Code = "XOLU-FSM009"

	// ErrFSMVariableInvalid is returned when a variable declaration is
	// malformed (unknown type, bad default).
	ErrFSMVariableInvalid Code = "XOLU-FSM010"

	// ErrFSMSetClauseFailed is returned when a set-clause expression fails to
	// parse or evaluate.
	ErrFSMSetClauseFailed Code = "XOLU-FSM011"

	// ErrFSMChildNotFound is returned when a linked-state child definition
	// does not exist at machine creation time, or when a walk reaches a
	// linked state in the v2 preview (bundle composition not yet implemented).
	ErrFSMChildNotFound Code = "XOLU-FSM012"

	// ErrFSMOverrideUnknownInput is returned when an override block references
	// a transition input not present in the definition.
	ErrFSMOverrideUnknownInput Code = "XOLU-FSM013"

	// ─── CAL family — calendar subsystem HTTP surface ────────────────────
	// Introduced with T-18 (v0.14.7): xolu's cal subsystem exposed via
	// /api/v2/cal/*.

	// ErrCalDisabled is returned when the /api/v2/cal/* endpoints are hit
	// but the cal subsystem is disabled (XOLU_CAL_ENABLED=false).
	ErrCalDisabled Code = "XOLU-CAL001"

	// ErrCalInvalidSpan is returned when a request carries a span whose
	// Start is not strictly before End, or where either instant is zero.
	ErrCalInvalidSpan Code = "XOLU-CAL002"

	// ErrCalInvalidObjective is returned when an Openings request carries
	// an objective outside the fixed set {earliest, first-fit, emptiest,
	// longest-clear-margin}.
	ErrCalInvalidObjective Code = "XOLU-CAL003"

	// ErrCalCalendarNotFound is returned when a request references a
	// calendar_id that does not exist in the current tenant scope.
	ErrCalCalendarNotFound Code = "XOLU-CAL004"

	// ErrCalBookingNotFound is returned when a request references a
	// booking_id that does not exist on the named calendar.
	ErrCalBookingNotFound Code = "XOLU-CAL005"

	// ErrCalTransitionRejected is returned when a lifecycle transition
	// (confirm, decline, complete, cancel) is not permitted from the
	// booking's current state per the A9 lifecycle rules.
	ErrCalTransitionRejected Code = "XOLU-CAL006"

	// ErrCalModeNotSupported is returned when a booking is submitted
	// with a mode outside the exclusive-only vocabulary. Introduced in
	// v0.14.12 when ModeShared and ModeSubPrefix were removed from the
	// pkg/cal type surface.
	ErrCalModeNotSupported Code = "XOLU-CAL007"

	// ErrCalCalendarExists is returned when a request tries to create
	// a calendar_id that already exists in the current tenant scope.
	// Added alongside POST /cal/calendars (XM-8, 2026-08-12) -- until
	// then classifyCalError's own cal.ErrCalendarExists case mapped
	// here to ErrCalCalendarNotFound ("not found"), a genuine,
	// pre-existing mismatch (semantically the wrong code for a
	// conflict) that sat dormant purely because no route existed that
	// could ever trigger it.
	ErrCalCalendarExists Code = "XOLU-CAL008"

	// ─── DXP family — declarative composed commitments ────────────────────
	// Introduced with the reserved-commit facility (item 18, v0.16.20),
	// reserved for the dxp coordinator per the dxp proposal §10.
	// XOLU-DXP002 and XOLU-DXP003 carry the refusing participant's own
	// error (e.g. a XOLU-BAL001) — composition must not launder the
	// underlying refusal.

	// ErrDXPBindings is returned when instantiation is refused because
	// the supplied bindings fail the definition's slot types.
	ErrDXPBindings Code = "XOLU-DXP001"

	// ErrDXPReserveRefused is returned when a participant guard refuses
	// during the reserve phase. The refusing participant is named and
	// its own error is carried.
	ErrDXPReserveRefused Code = "XOLU-DXP002"

	// ErrDXPValidateFailed is returned when validation at confirmation
	// finds a reservation no longer holds. Carries the participant's
	// own error where one exists.
	ErrDXPValidateFailed Code = "XOLU-DXP003"

	// ErrDXPPhaseOrder is returned on a phase-order violation: a verb
	// invoked outside the pattern's legal phase sequence.
	ErrDXPPhaseOrder Code = "XOLU-DXP004"

	// ErrDXPInstanceExpired is returned when an instance's deadline
	// lapsed; its reservations are released.
	ErrDXPInstanceExpired Code = "XOLU-DXP005"

	// ErrDXPDefinitionInvalid is returned when a definition is rejected
	// at registration, with detail.
	ErrDXPDefinitionInvalid Code = "XOLU-DXP006"

	// ErrDXPInvalidatedByLoss is returned when a held reservation is
	// superseded by a competing transaction's commit (3PS validate).
	// Distinct from ErrDXPValidateFailed: losing to a competitor is a
	// different fact from a guard input drifting for any other reason.
	// The winner's transaction id is named in the message.
	ErrDXPInvalidatedByLoss Code = "XOLU-DXP007"

	// ErrDXPExecuteFailed is returned when a participant's Execute
	// errors during the commit phase. The whole transaction rolls
	// back; the participant's own error is carried.
	ErrDXPExecuteFailed Code = "XOLU-DXP008"

	// ErrDXPAbandoned is returned when a status query names an instance
	// tombstoned by the mount-time startup pass after a coordinator
	// restart — distinct from ErrDXPInstanceExpired (the clock ran out
	// in a live process); a spike of this code is an incident signature.
	ErrDXPAbandoned Code = "XOLU-DXP009"

	// ErrDXPAbandonmentInvariant is an internal alarm code, never
	// returned to API callers: the mount-time pass found a participant
	// effect row bearing an abandoned instance's txn id, which part 2
	// of the reservation-cache design proves impossible in v1. Logged
	// at error and fatal under XOLU_STRICT_DXP.
	ErrDXPAbandonmentInvariant Code = "XOLU-DXP010"
)

const (
	// --- loc: spatial primitive (T-115..T-118, wave 9) ---
	// First pass, per loc-01-rest-api.md's own caution: read this list
	// the way cal's history actually went (XOLU-CAL001-007 were only
	// hardened during and after implementation), not the way bal's
	// (settled early, stayed close to final) did. Not every code below
	// is guaranteed to survive contact with loc-02-implementation.md's
	// own stages.

	// ErrLocFenceCapacity: report/move refused — an entered fence is
	// at capacity (Stage 2's CAS predicate matched zero rows). HTTP 409.
	ErrLocFenceCapacity Code = "XOLU-LOC001"
	// ErrLocLeafCapacity: move refused — destination leaf is at
	// capacity. HTTP 409.
	ErrLocLeafCapacity Code = "XOLU-LOC002"
	// ErrLocUnknownLocation: unknown location_id. HTTP 404.
	ErrLocUnknownLocation Code = "XOLU-LOC003"
	// ErrLocUnknownFence: unknown fence — bad location_id for a
	// tree-aligned fence, or unresolvable (kind, key) for a standalone
	// one. HTTP 404.
	ErrLocUnknownFence Code = "XOLU-LOC004"
	// ErrLocUnknownSubject: entity ref does not resolve — used for
	// both report/move's own subject and fences/attach's subject.
	// HTTP 404.
	ErrLocUnknownSubject Code = "XOLU-LOC005"
	// ErrLocAlreadyAttached: fences/attach refused — subject already
	// has loc.fence composed. HTTP 409.
	ErrLocAlreadyAttached Code = "XOLU-LOC006"
	// ErrLocRootWithoutAnchor: a tree root (parent_id: null) was
	// defined without an anchor. HTTP 400.
	ErrLocRootWithoutAnchor Code = "XOLU-LOC010"
	// ErrLocCapacityOnNonPostable: capacity set on a non-postable
	// node. HTTP 400.
	ErrLocCapacityOnNonPostable Code = "XOLU-LOC011"
	// ErrLocDeleteHasAssignedSubject: delete refused — node (or a
	// descendant) currently holds an assigned subject. HTTP 409.
	ErrLocDeleteHasAssignedSubject Code = "XOLU-LOC012"
	// ErrLocDeleteHasChildren: delete refused — node has children and
	// force was not set. HTTP 409.
	ErrLocDeleteHasChildren Code = "XOLU-LOC013"
	// ErrLocDuplicateLocation: def refused — location_id already
	// defined. HTTP 409. Added post-hoc by adversarial testing, not
	// present in loc-01-rest-api.md's original error table — a
	// UNIQUE-constraint violation previously fell through to a bare
	// 500, the same gap bal's own DefineAccount has and hasn't fixed.
	ErrLocDuplicateLocation Code = "XOLU-LOC014"
	// ErrLocDuplicateFence: fences/attach refused — fence_id already
	// defined. HTTP 409. Same finding as ErrLocDuplicateLocation,
	// fence_id's own version.
	ErrLocDuplicateFence Code = "XOLU-LOC015"
	// ErrLocSelfIntersectingPolygon: fence geometry rejected —
	// self-intersecting polygon (loc-00-design.md §4b). HTTP 400.
	ErrLocSelfIntersectingPolygon Code = "XOLU-LOC020"
	// ErrLocNonFiniteCoordinate: coordinate field rejected — non-finite
	// float (§4e's numerics doctrine). Should be unreachable given
	// Stage 0's decode discipline (typed float64 fields only, never a
	// map[string]interface{} or string intermediate) — this code exists
	// for the string-based-import path named as a future risk, not the
	// primary JSON decode path, which encoding/json already refuses.
	ErrLocNonFiniteCoordinate Code = "XOLU-LOC021"
	// ErrLocPatternCapacityConflict: def/attach set both inline
	// capacity and a pattern reference (T-131). HTTP 400.
	ErrLocPatternCapacityConflict Code = "XOLU-LOC022"
	// ErrLocDuplicatePattern: pattern_id already defined (T-131).
	// HTTP 409.
	ErrLocDuplicatePattern Code = "XOLU-LOC023"
	// ErrLocUnknownPattern: pattern_id does not resolve (T-131).
	// HTTP 404.
	ErrLocUnknownPattern Code = "XOLU-LOC024"

	// ─── XOLU-OBJ family (wave 10, T-119 onward) ──────────────────

	// ErrObjUnknownSubject: subject does not resolve — either a
	// malformed (kind, key) shape (format-only, T-119's own package
	// doc explains why not a live existence check) or a well-formed
	// subject with no obj capability attached. HTTP 404.
	ErrObjUnknownSubject Code = "XOLU-OBJ001"
	// ErrObjCapacity: destination obj subject at capacity, T-120's own
	// count dimension (weight/volume deliberately deferred — see
	// pkg/obj/containment.go's own doc comment for why). HTTP 409.
	ErrObjCapacity Code = "XOLU-OBJ003"
	// ErrObjCycle: the move would create a containment cycle (T-120).
	// HTTP 409.
	ErrObjCycle Code = "XOLU-OBJ004"
	// ErrObjContainerNotAttached: the target obj subject (the intended
	// container) is not itself obj-attached (T-120). HTTP 409.
	ErrObjContainerNotAttached Code = "XOLU-OBJ005"
	// ErrObjAlreadyAttached: attach refused — subject already has obj
	// capability composed. HTTP 409.
	ErrObjAlreadyAttached Code = "XOLU-OBJ006"
	// ErrObjCapacityInvalid: capacity update leaves every dimension
	// unconstrained-and-unset, or otherwise malformed (obj-01-rest-
	// api.md §4, /loc's own XOLU-LOC011 counterpart). HTTP 400.
	ErrObjCapacityInvalid Code = "XOLU-OBJ008"
	// ErrObjRetireRefused: retire refused — subject currently contains
	// something, or is already retired (obj-01-rest-api.md §6). HTTP 409.
	ErrObjRetireRefused Code = "XOLU-OBJ012"
	// ErrObjDetachRefused: detach refused — subject currently
	// contains something or is positioned anywhere other than
	// unassigned (obj-01-rest-api.md §1). HTTP 409.
	ErrObjDetachRefused Code = "XOLU-OBJ007"
	// ErrObjEntitySelectionInvalid: promote's own entity selection is
	// malformed — both existing_key and create set, or neither
	// (obj-01-rest-api.md §5). HTTP 400. XOLU-OBJ009 (bal_account
	// balance insufficient) is not a distinct HTTP error code here —
	// it surfaces through the underlying dxp transaction's own
	// dispatch result (status "released", reason naming the bal leg),
	// the same way every other dxp-composed refusal already does.
	ErrObjEntitySelectionInvalid Code = "XOLU-OBJ010"
)
