# Adversarial corpus

The shared adversarial-input corpus lives as a Go package:

    pkg/internal/advcorpus

It must be a Go package rather than a flat data file here, because it is imported
by property tests and fuzz seeds across several packages (`pkg/storage`,
`pkg/oql`, `pkg/blob`, …) and Go test code cannot load loose data files from an
arbitrary path the way a scripting language would.

## What it contains

| Set | Purpose |
|-----|---------|
| `SQLMetacharacters` | substrings that must never survive from an untrusted identifier into emitted SQL |
| `InjectionIdentifiers` | crafted field/column-name breakout payloads (D-005, D-009) |
| `PathTraversalDigests` | malformed blob SHA digests (D-004) |
| `Identifiers` | general identifier edge cases (length, casing, Unicode, control bytes, reserved words, system-column collisions) |
| `ValidIdentifiers` | well-formed names that must always be accepted (over-rejection control) |

`AllIdentifierPayloads()` returns the identifier-shaped sets concatenated.

## Adding a payload

Add it to the appropriate set in `pkg/internal/advcorpus/advcorpus.go`. Every
property test and fuzz seed that iterates the corpus picks it up automatically —
one definition strengthens every consumer.

This directory holds only non-Go corpus data and notes; there is none at
present.
