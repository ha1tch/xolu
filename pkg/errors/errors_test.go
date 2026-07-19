// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

package errors

import (
	"fmt"
	"testing"
)

func TestAPIError_Error(t *testing.T) {
	e := &APIError{
		Code:    ErrEntityNotFound,
		Message: "sensors with ID 42 not found",
		Status:  404,
	}
	got := e.Error()
	want := "XOLU-ST001: sensors with ID 42 not found"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestNew(t *testing.T) {
	e := New(ErrInvalidID, 400, "ID %d is out of range", -1)
	if e.Code != ErrInvalidID {
		t.Errorf("Code = %q, want %q", e.Code, ErrInvalidID)
	}
	if e.Status != 400 {
		t.Errorf("Status = %d, want 400", e.Status)
	}
	if e.Message != "ID -1 is out of range" {
		t.Errorf("Message = %q, want %q", e.Message, "ID -1 is out of range")
	}
}

func TestWrap(t *testing.T) {
	orig := fmt.Errorf("disk full")
	e := Wrap(ErrStorageFailed, 500, orig)
	if e.Code != ErrStorageFailed {
		t.Errorf("Code = %q, want %q", e.Code, ErrStorageFailed)
	}
	if e.Message != "disk full" {
		t.Errorf("Message = %q, want %q", e.Message, "disk full")
	}
}

func TestCodeFormat(t *testing.T) {
	// Verify all codes follow XOLU-SSNNN format
	codes := []Code{
		ErrEntityNotFound, ErrEntityExists, ErrInvalidEntity, ErrInvalidID,
		ErrVersionConflict, ErrStorageFailed, ErrEntityTooLarge,
		ErrSchemaNotFound, ErrSchemaLoadFailed,
		ErrCycleDetected, ErrGraphDisabled, ErrGraphUnsupported, ErrGraphFailed,
		ErrGraphVisitedLimit, ErrGraphResultLimit,
		ErrQuerySyntax, ErrQueryDepthExceeded, ErrQueryNotFound, ErrQueryFailed,
		ErrQueryRequired, ErrQueryIDRequired, ErrQueryEngineNotInit,
		ErrQueryTimeout, ErrQueryRowLimit, ErrQueryScanLimit, ErrQueryResponseSize,
		ErrSearchFailed, ErrSearchDisabled,
		ErrValidationFailed, ErrInvalidJSON, ErrMissingParam,
		ErrUnauthorized, ErrInvalidToken, ErrForbidden,
		ErrRateLimited,
		ErrTenantNotFound, ErrTenantRequired,
		ErrConfigInvalid,
		// Blob store codes (XOLU-BL001..006)
		ErrBlobDisabled, ErrBlobNotFound, ErrBlobTooLarge,
		ErrBlobInvalidKey, ErrBlobStoreFailed, ErrBlobQuotaExceeded,
		// Dynamic configuration codes (XOLU-DC001..004)
		ErrDCDisabled, ErrDCNotFound, ErrDCInvalidInput, ErrDCStoreFailed,
		// Cal subsystem codes (XOLU-CAL001..006) — T-18 v0.14.7
		ErrCalDisabled, ErrCalInvalidSpan, ErrCalInvalidObjective,
		ErrCalCalendarNotFound, ErrCalBookingNotFound, ErrCalTransitionRejected,
	}

	seen := make(map[Code]bool, len(codes))
	for _, code := range codes {
		if seen[code] {
			t.Errorf("Duplicate error code: %q", code)
		}
		seen[code] = true

		s := string(code)
		// Format: XOLU-<AREA><NUM> where AREA is 2 or 3 uppercase letters
		// and NUM is exactly 3 digits. Total length is 10 or 11.
		// The 2-letter form was the original convention; 3-letter forms
		// (CAL, and future extensions) were introduced with T-18.
		if len(s) != 10 && len(s) != 11 {
			t.Errorf("Code %q has length %d, want 10 or 11 (XOLU-<AREA><NUM>)", s, len(s))
			continue
		}
		if s[:5] != "XOLU-" {
			t.Errorf("Code %q does not start with XOLU-", s)
		}
		// The last 3 chars must be digits.
		numStart := len(s) - 3
		for _, c := range s[numStart:] {
			if c < '0' || c > '9' {
				t.Errorf("Code %q has non-digit in numeric portion", s)
				break
			}
		}
		// The area (between "XOLU-" and the digits) must be uppercase letters.
		for _, c := range s[5:numStart] {
			if c < 'A' || c > 'Z' {
				t.Errorf("Code %q has non-uppercase-letter in area portion", s)
				break
			}
		}
	}
}

func TestNew_BlobQuotaExceeded(t *testing.T) {
	e := New(ErrBlobQuotaExceeded, 413, "tenant %q quota exceeded", "acme")
	if e.Code != ErrBlobQuotaExceeded {
		t.Errorf("Code = %q, want %q", e.Code, ErrBlobQuotaExceeded)
	}
	if e.Status != 413 {
		t.Errorf("Status = %d, want 413", e.Status)
	}
	want := `tenant "acme" quota exceeded`
	if e.Message != want {
		t.Errorf("Message = %q, want %q", e.Message, want)
	}
	if e.Error() != "XOLU-BL006: "+want {
		t.Errorf("Error() = %q", e.Error())
	}
}

func TestNew_DCErrorFamily(t *testing.T) {
	cases := []struct {
		code   Code
		status int
	}{
		{ErrDCDisabled, 503},
		{ErrDCNotFound, 404},
		{ErrDCInvalidInput, 400},
		{ErrDCStoreFailed, 500},
	}
	for _, tc := range cases {
		e := New(tc.code, tc.status, "test message")
		if e.Code != tc.code {
			t.Errorf("Code = %q, want %q", e.Code, tc.code)
		}
		if e.Status != tc.status {
			t.Errorf("%q Status = %d, want %d", tc.code, e.Status, tc.status)
		}
	}
}

func TestNew_BlobErrorFamily(t *testing.T) {
	cases := []struct {
		code   Code
		status int
	}{
		{ErrBlobDisabled, 503},
		{ErrBlobNotFound, 404},
		{ErrBlobTooLarge, 413},
		{ErrBlobInvalidKey, 400},
		{ErrBlobStoreFailed, 500},
		{ErrBlobQuotaExceeded, 413},
	}
	for _, tc := range cases {
		e := New(tc.code, tc.status, "msg")
		if e.Code != tc.code {
			t.Errorf("Code = %q, want %q", e.Code, tc.code)
		}
	}
}

func TestNew_CalErrorFamily(t *testing.T) {
	// The XOLU-CAL* family was added with T-18 in v0.14.7 and refined with
	// typed cal-side sentinels in v0.14.8. Verifies each code carries the
	// intended HTTP status posture used by pkg/server/v2_cal_handlers.go
	// (see classifyCalError there).
	cases := []struct {
		code   Code
		status int
	}{
		{ErrCalDisabled, 501},
		{ErrCalInvalidSpan, 400},
		{ErrCalInvalidObjective, 400},
		{ErrCalCalendarNotFound, 404},
		{ErrCalBookingNotFound, 404},
		{ErrCalTransitionRejected, 422},
	}
	for _, tc := range cases {
		e := New(tc.code, tc.status, "test message")
		if e.Code != tc.code {
			t.Errorf("Code = %q, want %q", e.Code, tc.code)
		}
		if e.Status != tc.status {
			t.Errorf("%q Status = %d, want %d", tc.code, e.Status, tc.status)
		}
	}
}

func TestCodeFormat_AllCalCodesDistinct(t *testing.T) {
	// Guards against accidentally assigning two sentinels the same string.
	// Duplicates would silently mis-classify errors at the handler layer.
	codes := []Code{
		ErrCalDisabled, ErrCalInvalidSpan, ErrCalInvalidObjective,
		ErrCalCalendarNotFound, ErrCalBookingNotFound, ErrCalTransitionRejected,
	}
	seen := make(map[Code]bool, len(codes))
	for _, code := range codes {
		if seen[code] {
			t.Errorf("Duplicate CAL error code: %q", code)
		}
		seen[code] = true
	}
	if len(seen) != 6 {
		t.Errorf("Expected 6 distinct CAL codes, got %d", len(seen))
	}
}
