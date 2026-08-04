#!/bin/bash
#
# run_tests.sh — thin launcher for scripts/runtests.py.
#
# All test-orchestration logic (sharding, coverage, category
# classification, colour output, threshold gating, HTML reports,
# charts) lives in scripts/runtests.py -- the same module baseline.py/
# release.py/regrun.py already share via testrun.py. This script does
# nothing but locate python3 and forward every argument, per this
# project's own tooling discipline: shell scripts are launchers only,
# never a second implementation of orchestration logic that could
# silently diverge from the Python one (T-139's own root-cause
# investigation is the reason this file used to be ~300 lines of bash
# with its own independent go-test invocation, coverage parsing, and
# category classification -- superseded here, not merely trimmed).
#
# See `python3 scripts/runtests.py --help` for all flags.
#
# Copyright (c) 2026 haitch
# Licensed under Apache 2.0

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if ! command -v python3 >/dev/null 2>&1; then
    echo "run_tests.sh: python3 not found on PATH" >&2
    exit 1
fi

exec python3 "$HERE/scripts/runtests.py" "$@"
