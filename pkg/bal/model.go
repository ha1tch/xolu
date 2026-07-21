// Copyright (c) 2026 haitch
// Licensed under the Apache License, Version 2.0
// https://www.apache.org/licenses/LICENSE-2.0

// Package bal is the conservation primitive (@B): an append-only
// double-entry journal over bounded accounts, where conservation is an
// arithmetic identity — every transfer writes two signed entries
// (−a, +a) in one transaction, so the system total always equals the
// sum of its boundary accounts.
//
// Identity (@B09a): the substrate-preferred two-identity split. The
// external account_id is a namespaced STRING at every boundary — no
// numeric width exists to truncate, so the /ts id-boundary bug class is
// structurally impossible here (@C04d the strong way). The internal
// account key is a dense uint32, engine-internal, fixed-width codec,
// never on any wire struct.
//
// Numerics (@B04): amounts are int64 minor units through the account's
// scale. No float64 touches an amount anywhere, ever — ParseAmount is
// the only sanctioned string→amount crossing.
package bal

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// AccountDef defines an account (@B03, @B03a).
type AccountDef struct {
	ID       string // namespaced external id, e.g. "warehouse:A/widget" or "1.1.9.10"
	Unit     string // "EUR", "widget", "gram"
	Scale    uint8  // decimal places of the minor unit
	Floor    int64  // minimum balance (default 0)
	Ceiling  *int64 // optional maximum balance
	Postable bool   // only leaf (imputable) accounts accept entries (@B03a)
}

// ValidateAccountID enforces the external-id shape: non-empty, ≤256,
// no whitespace; '/' and ':' and '.' are the namespace vocabulary.
func ValidateAccountID(id string) error {
	if id == "" || len(id) > 256 {
		return fmt.Errorf("account_id must be 1..256 chars")
	}
	if strings.ContainsAny(id, " \t\n\r") {
		return fmt.Errorf("account_id must not contain whitespace")
	}
	return nil
}

// Validate checks a definition.
func (d AccountDef) Validate() error {
	if err := ValidateAccountID(d.ID); err != nil {
		return err
	}
	if d.Unit == "" || len(d.Unit) > 64 {
		return fmt.Errorf("unit must be 1..64 chars")
	}
	if d.Scale > 18 {
		return fmt.Errorf("scale must be 0..18")
	}
	if d.Ceiling != nil && *d.Ceiling < d.Floor {
		return fmt.Errorf("ceiling %d below floor %d", *d.Ceiling, d.Floor)
	}
	return nil
}

// ─── Internal account key (@B09a, @C04d obligations) ────────────────────────

// AccountKey is the engine-internal dense identity: uint32, the wave-1
// per-primitive width. It never appears in JSON.
type AccountKey uint32

// MaxAccountKey is the codec ceiling; it fits uint32 exactly.
const MaxAccountKey AccountKey = 0xFFFFFFFF

// EncodeAccountKey writes the key with an explicit fixed-width codec
// (4 bytes, big-endian) — the sanctioned byte crossing.
func EncodeAccountKey(k AccountKey) [4]byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(k))
	return b
}

// DecodeAccountKey reads the fixed-width codec back, losslessly across
// the full uint32 span.
func DecodeAccountKey(b [4]byte) AccountKey {
	return AccountKey(binary.BigEndian.Uint32(b[:]))
}

// ─── Amounts (@B04) ─────────────────────────────────────────────────────────

// ParseAmount converts a decimal string to int64 minor units under the
// account's scale — the ONLY string→amount crossing. It refuses
// anything that is not an exact integer of minor units at that scale
// (XOLU-BAL004 territory), and never passes through float64.
func ParseAmount(s string, scale uint8) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	neg := false
	switch s[0] {
	case '-':
		neg, s = true, s[1:]
	case '+':
		s = s[1:]
	}
	intPart, fracPart := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, fracPart = s[:i], s[i+1:]
	}
	if intPart == "" && fracPart == "" {
		return 0, fmt.Errorf("malformed amount %q", s)
	}
	if len(fracPart) > int(scale) {
		return 0, fmt.Errorf("amount %q has more than %d decimal places", s, scale)
	}
	// Right-pad the fraction to the scale, then fold digit by digit
	// with overflow checks — pure integer arithmetic.
	fracPart += strings.Repeat("0", int(scale)-len(fracPart))
	var v int64
	for _, digits := range []string{intPart, fracPart} {
		for i := 0; i < len(digits); i++ {
			d := digits[i]
			if d < '0' || d > '9' {
				return 0, fmt.Errorf("malformed amount %q", s)
			}
			if v > (1<<63-1-int64(d-'0'))/10 {
				return 0, fmt.Errorf("amount %q overflows int64 minor units", s)
			}
			v = v*10 + int64(d-'0')
		}
	}
	if neg {
		v = -v
	}
	return v, nil
}

// FormatAmount renders minor units back to the canonical decimal string
// at the account's scale.
func FormatAmount(v int64, scale uint8) string {
	neg := v < 0
	u := uint64(v)
	if neg {
		u = uint64(-v)
	}
	s := fmt.Sprintf("%d", u)
	if scale > 0 {
		for len(s) <= int(scale) {
			s = "0" + s
		}
		s = s[:len(s)-int(scale)] + "." + s[len(s)-int(scale):]
	}
	if neg {
		s = "-" + s
	}
	return s
}
