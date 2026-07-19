#!/usr/bin/env bash
# tests/fuzz/run.sh - launcher for xolu's Go native fuzz targets.
#
# The Fuzz* functions live in their packages (Go requires it); this script is
# the single entry point that knows about all of them. The registry is
# tests/fuzz/targets.txt.
#
# Usage:
#   ./tests/fuzz/run.sh                      Replay seed corpora only (fast,
#                                            deterministic — same as a plain
#                                            `go test`; no active fuzzing).
#   ./tests/fuzz/run.sh --active [SECS]      Actively fuzz each target for SECS
#                                            seconds (default 30). Writes any
#                                            crasher into the package's
#                                            testdata/fuzz/ tree.
#   ./tests/fuzz/run.sh --active [SECS] NAME Fuzz only the named target.
#   ./tests/fuzz/run.sh --list               List registered targets.
#
# NOTE: active fuzzing is unbounded work and may write new files. Run it
# locally or in a nightly job, never in the release pipeline. The release/
# run_tests path only ever replays seeds (no -fuzz flag), so it stays
# deterministic.
#
# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0

set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
REGISTRY="$SCRIPT_DIR/targets.txt"

export GOPROXY="${GOPROXY:-off}"
export GONOSUMDB="${GONOSUMDB:-*}"

ACTIVE=false
FUZZTIME=30
ONLY=""

while [ $# -gt 0 ]; do
    case "$1" in
        --active) ACTIVE=true; shift
                  if [ $# -gt 0 ] && echo "$1" | grep -qE '^[0-9]+$'; then FUZZTIME="$1"; shift; fi ;;
        --list)   grep -vE '^\s*#|^\s*$' "$REGISTRY"; exit 0 ;;
        --help|-h) sed -n '2,30p' "$0" | sed 's/^# \?//'; exit 0 ;;
        --*)      echo "Unknown option: $1" >&2; exit 1 ;;
        *)        ONLY="$1"; shift ;;
    esac
done

cd "$ROOT"
rc=0

while read -r name pkg; do
    [ -z "${name:-}" ] && continue
    case "$name" in \#*) continue ;; esac
    if [ -n "$ONLY" ] && [ "$ONLY" != "$name" ]; then continue; fi

    if $ACTIVE; then
        echo "== fuzzing $name ($pkg) for ${FUZZTIME}s =="
        go test "$pkg" -run x -fuzz "^${name}\$" -fuzztime "${FUZZTIME}s" || rc=1
    else
        echo "== replaying seeds: $name ($pkg) =="
        go test "$pkg" -run "^${name}\$" || rc=1
    fi
done < <(grep -vE '^\s*#|^\s*$' "$REGISTRY")

if [ $rc -eq 0 ]; then
    echo "OK: all fuzz targets clean"
else
    echo "FAIL: one or more fuzz targets reported a finding" >&2
fi
exit $rc
