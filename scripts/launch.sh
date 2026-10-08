#!/usr/bin/env bash
#
# launch.sh -- xolu launcher shell script
#
#
# Usage:
#   ./launch.sh --xolu-source /path/to/xolu            # foreground, Ctrl-C to stop
#   ./launch.sh --xolu-source /path/to/xolu --daemon   # background, prints PID
#   ./launch.sh --skip-build                           # use an already-built xolu (XOLU_BIN_PATH)
#   ./launch.sh --xolu-source /path/to/xolu --tenant acme            # provision a specific tenant name
#   ./launch.sh --xolu-source /path/to/xolu --with-auth KEY          # AuthType=apikey, XOLU_API_KEYS=KEY
#   ./launch.sh --xolu-source /path/to/xolu --daemon --with-seed     # launch AND seed in one step
#   ./launch.sh --xolu-source /path/to/xolu --with-seed --seed-args "--scale 2"  # forward extra args to seed
#
# Every setting below is a plain shell variable with an env-var
# override (XOLU_BASE_DIR=/other/path ./launch.sh),
# not a hidden default -- change what you need before running.
#
set -euo pipefail
XOLU_REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
XOLU_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
XOLU_SOURCE_DIR="${XOLU_SOURCE:-}"
XOLU_BIN_PATH="${BIN_PATH:-$XOLU_REPO_ROOT/bin/xolu}"
XOLU_BASE_DIR="${BASE_DIR:-$XOLU_REPO_ROOT/seeds}"
XOLU_SCHEMA_DIR="${XOLU_SCHEMA_DIR:-$XOLU_REPO_ROOT/schema}"

HOST="${XOLU_HOST:-127.0.0.1}"
PORT="${XOLU_PORT:-8080}"
LOG_LEVEL="${XOLU_LOG_LEVEL:-info}"
LOG_FILE="${XOLU_LOG_FILE:-$XOLU_REPO_ROOT/logs/xolu.log}"
READY_TIMEOUT_SECS="${XOLUREADY_TIMEOUT:-30}"

DAEMON=0
AUTH_TYPE="none"
API_KEY=""
TENANT="${XOLU_CRM_TENANT:-acme_crm}"
IOLU_BIN="${XOLU_CRM_IOLU_BIN:-$XOLU_REPO_ROOT/bin/iolu}"
WITH_SEED=0
SEED_ARGS=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --daemon)
            DAEMON=1
            shift
            ;;
        --with-auth)
            AUTH_TYPE="apikey"
            API_KEY="${2:?--with-auth requires a key argument}"
            shift 2
            ;;
        --tenant)
            TENANT="${2:?--tenant requires a name argument}"
            shift 2
            ;;
        --skip-build)
            SKIP_BUILD=1
            shift
            ;;
        --xolu-source)
            XOLU_SOURCE_DIR="${2:?--xolu-source requires a path argument}"
            shift 2
            ;;
        --with-seed)
            WITH_SEED=1
            shift
            ;;
        --seed-args)
            SEED_ARGS="${2:?--seed-args requires a quoted argument string, e.g. --seed-args \"--scale 2\"}"
            shift 2
            ;;
        -h|--help)
            sed -n '2,22p' "${BASH_SOURCE[0]}"
            exit 0
            ;;
        *)
            echo "unknown argument: $1" >&2
            exit 1
            ;;
    esac
done

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

if [[ "${SKIP_BUILD:-0}" != "1" ]]; then
    if [[ -z "$XOLU_SOURCE_DIR" ]]; then
        echo "error: building xolu from source needs its location." >&2
        echo "  Set XOLU_XOLU_SOURCE=/path/to/xolu, or pass --xolu-source /path/to/xolu," >&2
        echo "  or use --skip-build with XOLU_BIN_PATH pointing at an already-built xolu binary." >&2
        exit 1
    fi
    if [[ ! -d "$XOLU_SOURCE_DIR/cmd/xolu" ]]; then
        echo "error: $XOLU_SOURCE_DIR does not look like xolu's own source tree (no cmd/xolu found there)." >&2
        exit 1
    fi
    echo "-- building cmd/xolu and cmd/iolu (from $XOLU_SOURCE_DIR) --"
    mkdir -p "$(dirname "$BIN_PATH")"
    (cd "$XOLU_SOURCE_DIR" && go build -o "$BIN_PATH" ./cmd/xolu)
    (cd "$XOLU_SOURCE_DIR" && go build -o "$IOLU_BIN" ./cmd/iolu)
    echo "  built: $BIN_PATH"
    echo "  built: $IOLU_BIN"
else
    if [[ ! -x "XOLU_BIN_PATH" ]]; then
        echo "--skip-build given but $BIN_PATH does not exist or isn't executable" >&2
        exit 1
    fi
    echo "-- skipping build, using existing $BIN_PATH --"
fi
echo

mkdir -p "$BASE_DIR" "$SCHEMA_DIR"

# ---------------------------------------------------------------------------
# Provision the tenant BEFORE the server starts.

echo "-- provisioning tenant '$TENANT' (must happen before the server boots) --"
set +e


IOLU_OUTPUT="$("$IOLU_BIN" tenant create --base-dir "$BASE_DIR" --mode shared --name "$TENANT" 2>&1)"
IOLU_STATUS=$?
set -e
if [[ $IOLU_STATUS -eq 0 ]]; then
    echo "  $IOLU_OUTPUT"
elif [[ "$IOLU_OUTPUT" == *"already exists"* ]]; then
    echo "  tenant '$TENANT' already exists -- continuing"
else
    echo "iolu tenant create failed:" >&2
    echo "  $IOLU_OUTPUT" >&2
    exit 1
fi
echo

export XOLU_HOST="$HOST"
export XOLU_PORT="$PORT"
export XOLU_BASE_DIR="$BASE_DIR"
export XOLU_SCHEMA_DIR="$SCHEMA_DIR"
export XOLU_LOG_LEVEL="$LOG_LEVEL"


export XOLU_GRAPH_MODE="flat"
export XOLU_API_V2_ENABLED="true"
export XOLU_BAL_ENABLED="true"
export XOLU_BLOB_ENABLED="true"
export XOLU_TENANT_MODE="strict"
export XOLU_TENANT_AUTH_MODE="open"


export XOLU_AUTH_TYPE="$AUTH_TYPE"
if [[ "$AUTH_TYPE" == "apikey" ]]; then
    export XOLU_API_KEYS="$API_KEY"
fi

echo "-- configuration --"
echo "  base dir:    $BASE_DIR"
echo "  schema dir:  $SCHEMA_DIR"
echo "  listening:   http://$HOST:$PORT"
echo "  graph mode:  $XOLU_GRAPH_MODE"
echo "  api v2:      $XOLU_API_V2_ENABLED (required so event_defs exists -- see comment above)"
echo "  bal:         $XOLU_BAL_ENABLED (required for close_deal_won's revenue-recognition transfer)"
echo "  blob:        $XOLU_BLOB_ENABLED (required for the contract-document attachment step)"
echo "  tenant mode: $XOLU_TENANT_MODE (auth mode: $XOLU_TENANT_AUTH_MODE)"
echo "  auth type:   $XOLU_AUTH_TYPE"
echo

# ---------------------------------------------------------------------------
# Launch
# ---------------------------------------------------------------------------

wait_for_ready() {
    local url="http://$HOST:$PORT/health"
    local waited=0
    echo "-- waiting for $url --"
    while (( waited < READY_TIMEOUT_SECS )); do
        if curl -sf -o /dev/null "$url"; then
            echo "  ready after ${waited}s"
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done
    echo "server did not become ready within ${READY_TIMEOUT_SECS}s -- check $LOG_FILE" >&2
    return 1
}

run_seed() {
    echo
    echo "-- seeding (--with-seed given) --"
    local seed_cmd=(python3 "$SCRIPT_DIR/xolu_crm_seed.py" --tenant "$TENANT" --skip-tenant-create)
    if [[ -n "$SEED_ARGS" ]]; then
        # Intentionally word-split: SEED_ARGS is meant to be a
        # space-separated argument list (e.g. "--scale 2"), the same
        # way a person would type it on the command line themselves.
        # shellcheck disable=SC2206
        local extra=($SEED_ARGS)
        seed_cmd+=("${extra[@]}")
    fi
    XOLU_BASE_URL="http://$HOST:$PORT" XOLU_BASE_DIR="$BASE_DIR" \
        "${seed_cmd[@]}"
}

print_handoff() {
    echo
    if [[ "$WITH_SEED" == "1" ]]; then
        echo "-- ready and seeded (tenant '$TENANT') --"
        echo
        echo "  Connect xoluman (or curl) to:"
        echo "    base URL: http://$HOST:$PORT"
        echo "    tenant:   $TENANT"
        if [[ "$AUTH_TYPE" == "apikey" ]]; then
            echo "    auth:     apikey, key: $API_KEY"
        fi
        echo
        return
    fi
    echo "-- ready: run the CRM seed against this instance with --"
    echo
    echo "    export XOLU_BASE_URL=http://$HOST:$PORT"
    echo "    export XOLU_BASE_DIR=$BASE_DIR"
    if [[ "$AUTH_TYPE" == "apikey" ]]; then
        echo "    export XOLU_API_KEY=$API_KEY"
    fi
    echo "    python3 xolu_crm_seed.py --tenant $TENANT --skip-tenant-create"
    echo
    echo "  (tenant '$TENANT' was already provisioned by this launcher, before the"
    echo "   server started -- required for TenantMode=strict, see the comment above"
    echo "   the provisioning step in this script. --skip-tenant-create just avoids a"
    echo "   redundant iolu call; omitting it is also safe, iolu's own 'already"
    echo "   exists' response is treated as success by the seed script too."
    echo "   Or pass --with-seed to this launcher to skip this step entirely next time.)"
    echo
}

if [[ "$DAEMON" == "1" ]]; then
    echo "-- starting in background (log: $LOG_FILE) --"
    nohup "$BIN_PATH" > "$LOG_FILE" 2>&1 &
    XOLU_PID=$!
    echo "  pid: $XOLU_PID"
    if wait_for_ready; then
        if [[ "$WITH_SEED" == "1" ]]; then
            run_seed
        fi
        print_handoff
        echo "stop with: kill $XOLU_PID"
    else
        echo "startup failed; last 40 log lines:" >&2
        tail -n 40 "$LOG_FILE" >&2 || true
        kill "$XOLU_PID" 2>/dev/null || true
        exit 1
    fi
else
    echo "-- starting in foreground (Ctrl-C to stop; log also mirrored to $LOG_FILE) --"
    echo
    # Background it briefly just to run the readiness check and print the
    # handoff instructions once, then exec into the foreground so Ctrl-C
    # behaves normally and this script's own process becomes the server.
    "$BIN_PATH" > >(tee "$LOG_FILE") 2>&1 &
    XOLU_PID=$!
    if wait_for_ready; then
        if [[ "$WITH_SEED" == "1" ]]; then
            run_seed
        fi
        print_handoff
    else
        tail -n 40 "$LOG_FILE" >&2 || true
        kill "$XOLU_PID" 2>/dev/null || true
        exit 1
    fi
    wait "$XOLU_PID"
fi
