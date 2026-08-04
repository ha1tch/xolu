#!/usr/bin/env bash
# syncver.sh — shim; logic lives in scripts/syncver.py.
exec python3 "$(dirname "$0")/scripts/syncver.py" "$@"
