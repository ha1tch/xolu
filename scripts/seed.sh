#!/usr/bin/env bash
#
# seed.sh -- load the .seed files in <repo>/seeds into a running xolu
#
# Usage:
#   seed.sh [options] [SEED ...]
#
# With no SEED, every *.seed file in <repo>/seeds is loaded, in name order.
# A SEED is a path, or a name inside the seeds directory ("demo" means
# seeds/demo.seed). Everything else is passed to seed.py, which needs only
# python3 (no curl). Useful options:
#
#   --tenant NAME     override the tenant named in the seed headers
#   --dry-run         print the requests; contact nothing
#   --list            list the seed files and their record counts
#   --only KINDS      load only these kinds (comma list), e.g. schema,entity
#   --skip KINDS      leave these kinds out
#   --keep-going      carry on after a failed record
#   --base-url URL    default: XOLU_BASE_URL, else XOLU_HOST/XOLU_PORT/
#                     XOLU_ADDR, else http://127.0.0.1:9090 (as launch.sh)
#   --format-help     describe the .seed file format
#
# Environment: XOLU_SEEDS_DIR (default <repo>/seeds), XOLU_API_KEY, XOLU_TOKEN,
# PYTHON (default python3).
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
SEEDS_DIR="${XOLU_SEEDS_DIR:-$REPO_ROOT/seeds}"
PYTHON="${PYTHON:-python3}"

print_help() {
    {
        read -r _shebang
        while IFS= read -r line && [[ "$line" == \#* ]]; do
            line="${line#\#}"
            printf '%s\n' "${line# }"
        done
    } < "${BASH_SOURCE[0]}"
}

for arg in "$@"; do
    case "$arg" in
        -h|--help)
            print_help
            echo
            exec "$PYTHON" "$SCRIPT_DIR/seed.py" --help
            ;;
    esac
done

command -v "$PYTHON" >/dev/null 2>&1 || { echo "error: '$PYTHON' not found on PATH" >&2; exit 1; }
[[ -d "$SEEDS_DIR" ]] || { echo "error: seeds directory not found: $SEEDS_DIR" >&2; exit 1; }

# A --seeds-dir given by the caller comes later and therefore wins.
exec "$PYTHON" "$SCRIPT_DIR/seed.py" --seeds-dir "$SEEDS_DIR" "$@"
