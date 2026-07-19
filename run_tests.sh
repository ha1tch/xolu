#!/bin/bash

# run_tests.sh - xolu test suite with coverage and count reporting
#
# Coverage reporting is always on. Every run produces coverage.out
# and a per-package summary. Colour output is automatic when stdout
# is connected to a known colour-capable terminal.
#
# Usage:
#   ./run_tests.sh                  Standard run (short mode; skips stress tests)
#   ./run_tests.sh --full           Include stress tests (no -short)
#   ./run_tests.sh --race           Enable race detector
#   ./run_tests.sh --threshold 75   Fail if aggregate coverage below 75%
#   ./run_tests.sh --html           Generate coverage.html report
#   ./run_tests.sh --quiet          Skip per-package table; print summary only
#   ./run_tests.sh --no-colour      Force-disable colour output
#   ./run_tests.sh --charts         Show coverage heat map and test-count treemap
#                                   (requires colour-capable terminal + python3)
#
# NOTE: --full removes -short and will run storage/timeseries stress tests;
# expect ~45s instead of ~15s.
#
# Summary format (short mode):
#   SYSTEM TESTS:      N PASS
#   CACHE INTEGRATION: N PASS
#   STRESS TESTS:      N SKIPPED
#   BENCHMARKS:        N SKIPPED
#   FAIL:              0
#
# Copyright (c) 2026 haitch
# Licensed under Apache 2.0

set -uo pipefail

# --- Environment -----------------------------------------------------------
export GOPATH="${GOPATH:-$HOME/go}"
export PATH="$PATH:/usr/local/go/bin:/usr/lib/go/bin:$GOPATH/bin"
export GOPROXY="${GOPROXY:-off}"
export GONOSUMDB="${GONOSUMDB:-*}"

# --- Defaults --------------------------------------------------------------
SHORT="-short"
RACE=""
THRESHOLD=""
HTML=false
QUIET=false
NO_COLOUR=false
CHARTS=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --full)       SHORT=""; shift ;;
        --race)       RACE="-race"; shift ;;
        --html)       HTML=true; shift ;;
        --quiet)      QUIET=true; shift ;;
        --charts)     CHARTS=true; shift ;;
        --no-colour|--no-color) NO_COLOUR=true; shift ;;
        --threshold)  THRESHOLD="$2"; shift 2 ;;
        --help|-h)
            sed -n '3,29p' "$0" | sed 's/^# \?//'
            exit 0 ;;
        *)
            echo "Unknown option: $1" >&2; exit 1 ;;
    esac
done

# --- Colour setup ----------------------------------------------------------
USE_COLOUR=false
if ! $NO_COLOUR && [ -z "${NO_COLOR:-}" ] && [ -t 1 ]; then
    case "${TERM:-}" in
        xterm*|rxvt*|screen*|tmux*|vte*|alacritty*|foot*|linux|ansi)
            if command -v tput >/dev/null 2>&1 && [ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]; then
                USE_COLOUR=true
            fi
            ;;
    esac
fi

if $USE_COLOUR; then
    C_RESET=$(tput sgr0)
    C_BOLD=$(tput bold)
    C_GREEN=$(tput setaf 2)
    C_RED=$(tput setaf 1)
    C_YELLOW=$(tput setaf 3)
    C_CYAN=$(tput setaf 6)
    C_DIM=$(tput dim 2>/dev/null || printf '')
    C_HEADING="${C_BOLD}"
    C_OK="${C_GREEN}"
    C_FAIL="${C_RED}${C_BOLD}"
    C_SKIP="${C_YELLOW}"
    C_NOTESTS="${C_DIM}"
    C_COVER_HIGH="${C_GREEN}"
    C_COVER_MID="${C_YELLOW}"
    C_COVER_LOW="${C_RED}"
else
    C_RESET="" C_BOLD="" C_GREEN="" C_RED="" C_YELLOW="" C_CYAN="" C_DIM=""
    C_HEADING="" C_OK="" C_FAIL="" C_SKIP="" C_NOTESTS=""
    C_COVER_HIGH="" C_COVER_MID="" C_COVER_LOW=""
fi

bold()    { printf '%s%s%s' "$C_BOLD"    "$*" "$C_RESET"; }
ok()      { printf '%s%s%s' "$C_OK"      "$*" "$C_RESET"; }
fail()    { printf '%s%s%s' "$C_FAIL"    "$*" "$C_RESET"; }
skip()    { printf '%s%s%s' "$C_SKIP"    "$*" "$C_RESET"; }
notests() { printf '%s%s%s' "$C_NOTESTS" "$*" "$C_RESET"; }
heading() { printf '%s%s%s' "$C_HEADING" "$*" "$C_RESET"; }
cyan()    { printf '%s%s%s' "$C_CYAN"    "$*" "$C_RESET"; }

cover_colour() {
    local pct="${1//%/}"
    if [ -z "$pct" ] || [ "$pct" = "n/a" ]; then
        printf '%s' "$1"
        return
    fi
    if [ "$(echo "$pct >= 80" | bc -l 2>/dev/null || echo 0)" -eq 1 ]; then
        printf '%s%s%s' "$C_COVER_HIGH" "$1" "$C_RESET"
    elif [ "$(echo "$pct >= 60" | bc -l 2>/dev/null || echo 0)" -eq 1 ]; then
        printf '%s%s%s' "$C_COVER_MID" "$1" "$C_RESET"
    else
        printf '%s%s%s' "$C_COVER_LOW" "$1" "$C_RESET"
    fi
}

COVERFILE="coverage.out"
MODULE="github.com/ha1tch/xolu"

# --- Header ----------------------------------------------------------------
heading "xolu test suite"
printf '\n'
printf '===============\n'
if [ -n "$SHORT" ]; then
    printf '%s\n' "$(skip "(short mode — stress tests and benchmarks skipped; use --full to include them)")"
fi
printf '\n'

# --- Run -------------------------------------------------------------------
# shellcheck disable=SC2086
STDOUT=$(go test $SHORT $RACE -count=1 -v -coverprofile="$COVERFILE" ./... 2>/tmp/_run_tests_stderr.txt)
EXIT=$?
STDERR=$(cat /tmp/_run_tests_stderr.txt)
rm -f /tmp/_run_tests_stderr.txt

OUTPUT="${STDOUT}"$'\n'"${STDERR}"

# --- Test classification ---------------------------------------------------
#
# CACHE INTEGRATION: TestRedisCache_* and TestSlabbis_*
# STRESS TESTS:      TestStress_* and TestTSStress_*
# BENCHMARKS:        Tests containing _Benchmark or BlobVsAdapted
# SYSTEM TESTS:      everything else
#
# Each category counts PASS, FAIL, SKIP from the verbose output lines:
#   --- PASS: TestFoo (0.00s)
#   --- FAIL: TestFoo (0.00s)
#   --- SKIP: TestFoo (0.00s)

count_result() {
    local result="$1"   # PASS | FAIL | SKIP
    local pattern="$2"  # grep -E pattern to match test name
    # Match both top-level ("^--- PASS:") and subtest ("^    --- PASS:") lines.
    echo "$STDOUT" \
        | grep -- "--- ${result}:" \
        | grep -E "$pattern" \
        | wc -l \
        | tr -d ' '
}

CACHE_PAT="TestRedisCache_|TestSlabbis_"
STRESS_PAT="TestStress_|TestTSStress_"
BENCH_PAT="_Benchmark|BlobVsAdapted"
# System = all passing/failing tests NOT matching the above categories
SYS_PAT="TestRedisCache_|TestSlabbis_|TestStress_|TestTSStress_|_Benchmark|BlobVsAdapted"

CACHE_PASS=$(count_result PASS "$CACHE_PAT")
CACHE_FAIL=$(count_result FAIL "$CACHE_PAT")
CACHE_SKIP=$(count_result SKIP "$CACHE_PAT")

STRESS_PASS=$(count_result PASS "$STRESS_PAT")
STRESS_FAIL=$(count_result FAIL "$STRESS_PAT")
STRESS_SKIP=$(count_result SKIP "$STRESS_PAT")

BENCH_PASS=$(count_result PASS "$BENCH_PAT")
BENCH_FAIL=$(count_result FAIL "$BENCH_PAT")
BENCH_SKIP=$(count_result SKIP "$BENCH_PAT")

# System: all PASS/FAIL/SKIP lines not matching any special category
SYS_PASS=$(echo "$STDOUT" | grep -- "--- PASS:" | grep -vE "$SYS_PAT" | wc -l | tr -d ' ')
SYS_FAIL=$(echo "$STDOUT" | grep -- "--- FAIL:" | grep -vE "$SYS_PAT" | wc -l | tr -d ' ')
SYS_SKIP=$(echo "$STDOUT" | grep -- "--- SKIP:" | grep -vE "$SYS_PAT" | wc -l | tr -d ' ')

TOTAL_FAIL=$(( SYS_FAIL + CACHE_FAIL + STRESS_FAIL + BENCH_FAIL ))

# --- Failures first --------------------------------------------------------
FAIL_PKGS=$(echo "$OUTPUT" | grep -c '^FAIL\s' || true)
if [ "$TOTAL_FAIL" -gt 0 ] || [ "$FAIL_PKGS" -gt 0 ]; then
    printf '%s\n' "$(fail "FAILURES")"
    printf '%s\n' "$(fail "--------")"
    echo "$STDOUT" | grep -E '^--- FAIL:|^FAIL\s' | while IFS= read -r line; do
        printf '  %s\n' "$(fail "$line")"
    done
    printf '\n'
fi

# --- Per-package table -----------------------------------------------------
PASS_PKGS=$(echo "$STDOUT" | grep -c '^ok' || true)
NOTESTS_PKGS=$(echo "$OUTPUT" | grep -c '^\?' || true)

if ! $QUIET; then
    printf '%s\n' "$(bold "Package results:")"
    printf '\n'
    printf "  %-38s %7s %8s %6s\n" \
        "$(bold "Package")" "$(bold "Time")" "$(bold "Cover")" "$(bold "Status")"
    printf "  %-38s %7s %8s %6s\n" "-------" "----" "-----" "------"

    echo "$STDOUT" | grep '^ok' | while IFS= read -r line; do
        pkg=$(echo "$line"   | awk '{print $2}')
        short=${pkg#${MODULE}/}
        timing=$(echo "$line" | awk '{print $3}')
        cov=$(echo "$line"   | grep -oE '[0-9]+\.[0-9]+%' || echo "n/a")
        printf "  %-38s %7s %8s %6s\n" \
            "$short" "$timing" "$(cover_colour "$cov")" "$(ok "ok")"
    done

    echo "$OUTPUT" | grep '^FAIL\s' | while IFS= read -r line; do
        pkg=$(echo "$line" | awk '{print $2}')
        short=${pkg#${MODULE}/}
        printf "  %-38s %7s %8s %6s\n" \
            "$(fail "$short")" "-" "-" "$(fail "FAIL")"
    done

    echo "$OUTPUT" | grep '^\?' | while IFS= read -r line; do
        pkg=$(echo "$line" | awk '{print $2}')
        short=${pkg#${MODULE}/}
        printf "  %-38s %7s %8s %6s\n" \
            "$(notests "$short")" "-" "-" "$(notests "  -")"
    done

    printf '\n'
fi

# --- Aggregate coverage ----------------------------------------------------
AGGREGATE="n/a"
if [ -f "$COVERFILE" ]; then
    AGGREGATE=$(go tool cover -func="$COVERFILE" 2>/dev/null | tail -1 | awk '{print $NF}')
fi

# --- Categorised summary ---------------------------------------------------
printf '%s\n' "$(bold "Summary")"
printf '%s\n' "$(bold "-------")"

# Render a summary row. Arguments: label, pass, fail, skip
summary_row() {
    local label="$1" pass="$2" fail_n="$3" skip_n="$4"
    if [ "$fail_n" -gt 0 ]; then
        printf "  %-22s %s\n" "$label" "$(fail "${fail_n} FAIL")  (${pass} pass, ${skip_n} skip)"
    elif [ "$skip_n" -gt 0 ] && [ "$pass" -eq 0 ]; then
        printf "  %-22s %s\n" "$label" "$(skip "${skip_n} SKIPPED")"
    else
        local detail=""
        if [ "$skip_n" -gt 0 ]; then
            detail="  (${skip_n} skipped)"
        fi
        printf "  %-22s %s%s\n" "$label" "$(ok "${pass} PASS")" "$detail"
    fi
}

summary_row "SYSTEM TESTS:"      "$SYS_PASS"    "$SYS_FAIL"    "$SYS_SKIP"
summary_row "CACHE INTEGRATION:" "$CACHE_PASS"   "$CACHE_FAIL"  "$CACHE_SKIP"
summary_row "STRESS TESTS:"      "$STRESS_PASS"  "$STRESS_FAIL" "$STRESS_SKIP"
summary_row "BENCHMARKS:"        "$BENCH_PASS"   "$BENCH_FAIL"  "$BENCH_SKIP"

if [ "$TOTAL_FAIL" -gt 0 ]; then
    printf "  %-22s %s\n" "FAIL:" "$(fail "$TOTAL_FAIL")"
else
    printf "  %-22s %s\n" "FAIL:" "$(ok "0")"
fi

printf '\n'
printf "  %-22s %s\n" "Coverage:" "$(cover_colour "$AGGREGATE")"
printf "  %-22s %s ok, %s fail, %s no-tests\n" \
    "Packages:" \
    "$(ok "$PASS_PKGS")" \
    "$([ "$FAIL_PKGS" -gt 0 ] && fail "$FAIL_PKGS" || ok "$FAIL_PKGS")" \
    "$(notests "$NOTESTS_PKGS")"
printf '\n'

# --- HTML report -----------------------------------------------------------
if $HTML && [ -f "$COVERFILE" ]; then
    go tool cover -html="$COVERFILE" -o coverage.html 2>/dev/null
    printf '%s\n\n' "Reports: coverage.out, coverage.html"
fi

# --- Threshold gate --------------------------------------------------------
if [ -n "$THRESHOLD" ] && [ -f "$COVERFILE" ]; then
    ACTUAL=$(echo "$AGGREGATE" | tr -d '%')
    PASS_GATE=$(echo "$ACTUAL >= $THRESHOLD" | bc -l 2>/dev/null || echo "0")
    if [ "$PASS_GATE" -eq 1 ]; then
        printf '%s\n' "$(ok "Threshold: ${ACTUAL}% >= ${THRESHOLD}% (ok)")"
    else
        printf '%s\n' "$(fail "Threshold: ${ACTUAL}% < ${THRESHOLD}% (FAIL)")"
        exit 1
    fi
fi

# --- Charts ----------------------------------------------------------------
if $CHARTS; then
    CHARTS_PY="$(dirname "$0")/scripts/charts.py"
    if ! command -v python3 >/dev/null 2>&1; then
        printf '%s\n' "$(skip "Charts skipped: python3 not found")"
    elif [ ! -f "$CHARTS_PY" ]; then
        printf '%s\n' "$(skip "Charts skipped: charts.py not found alongside run_tests.sh")"
    else
        printf '%s' "$STDOUT" | python3 "$CHARTS_PY" --from-go-output both
    fi
fi

exit $EXIT
