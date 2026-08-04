#!/usr/bin/env bash
# release.sh — shim; orchestration lives in scripts/release.py.
exec python3 "$(dirname "$0")/scripts/release.py" "$@"
