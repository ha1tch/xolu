#!/usr/bin/env bash
#
# seed.sh -- load, check and dump the .seed files in <repo>/seeds
#
# Usage:
#   seed.sh [SEED ...] [options]            load those seeds; with no SEED, every
#                                           *.seed file in <repo>/seeds, in name order
#   seed.sh load|check|dump|list|info ...   the commands of seed.py, as they are
#
# A SEED is a path, or a name inside the seeds directory ("demo" means
# seeds/demo.seed). Everything is passed to seed.py, which needs only python3
# (no curl). Options of a load:
#
#   --tenant NAME      override the tenant named in the seed headers
#   --dry-run          print the requests; contact nothing
#   --only KINDS       load only these kinds (comma list), e.g. schema,entity
#   --skip KINDS       leave these kinds out
#   --keep-going       carry on after a failed record
#   --url URL          default: XOLU_BASE_URL, else XOLU_HOST/XOLU_PORT/
#                      XOLU_ADDR, else http://127.0.0.1:9090 (as launch.sh).
#                      --base-url is the same option.
#   --allow-existing   load into a tenant that already holds data
#   --replace          write again the records that already exist
#   --list             list the seed files (the same as: seed.sh list)
#   --format-help      say where the .seed format is described
#
# Since 0.30.45 a load refuses a tenant that holds other data, leaves alone the
# records that exist, and records what it loaded: docs/SEED_FORMAT.md, section
# 11, lists what changed. "seed.sh load --help" lists every option.
#
# Environment: XOLU_SEEDS_DIR (default <repo>/seeds), XOLU_API_KEY, XOLU_TOKEN,
# PYTHON (default python3).
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
export XOLU_SEEDS_DIR="${XOLU_SEEDS_DIR:-$REPO_ROOT/seeds}"
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

command -v "$PYTHON" >/dev/null 2>&1 || { echo "error: '$PYTHON' not found on PATH" >&2; exit 1; }

case "${1:-}" in
    load|check|dump|list|info)
        exec "$PYTHON" "$SCRIPT_DIR/seed.py" "$@"
        ;;
esac

# No command: a load, as this script always did. Options that take a value are
# stepped over with it, to tell whether a SEED was named.
named=0
args=()
while [ $# -gt 0 ]; do
    case "$1" in
        -h|--help)
            print_help
            exit 0
            ;;
        --list)
            exec "$PYTHON" "$SCRIPT_DIR/seed.py" list
            ;;
        --format-help)
            echo "The .seed format, versions 1 and 2: $REPO_ROOT/docs/SEED_FORMAT.md"
            exit 0
            ;;
        --tenant|--only|--skip|--url|--base-url|--seeds-dir|--as-of|--set|--api-key|--token|--wait|--timeout)
            [ $# -ge 2 ] || { echo "error: $1 needs a value" >&2; exit 2; }
            args+=("$1" "$2")
            shift
            ;;
        -*)
            args+=("$1")
            ;;
        *)
            named=1
            args+=("$1")
            ;;
    esac
    shift
done

[[ -d "$XOLU_SEEDS_DIR" ]] || { echo "error: seeds directory not found: $XOLU_SEEDS_DIR" >&2; exit 1; }

if [ "$named" -eq 0 ]; then
    args+=(--all)
fi
exec "$PYTHON" "$SCRIPT_DIR/seed.py" load "${args[@]}"
