#!/usr/bin/env bash
#
# launch.sh -- build and run xolu
#
# Usage:
#   launch.sh [options]        build (unless --skip-build) and start xolu
#   launch.sh stop             stop a server started with --daemon
#   launch.sh status           report whether it is running and healthy
#
# Profiles:
#   (default)   latest: API v2, bal, cal and blob are all enabled.
#   --stable    conservative: those four are left off (xolu's own defaults).
#               Note: a v1-only server logs an "event_defs" warning on writes
#               (see docs/TRACKING.md); that is a known xolu issue, not a fault
#               in this launcher.
#
# Options:
#   --daemon                 run in the background, write a pidfile
#   --skip-build             use already-built binaries (XOLU_BIN_PATH, XOLU_IOLU_BIN)
#   --xolu-source PATH       source tree to build from (default: this checkout)
#   --stable                 conservative profile (see above)
#   --enable LIST            enable features: comma list of v2,bal,cal,blob
#   --disable LIST           disable features, same names (flags apply in order)
#   --store shared|per-file  store layout for server AND iolu (default: shared)
#   --tenant NAME            pre-register a tenant before boot (repeatable)
#   --strict-tenants         XOLU_TENANT_MODE=strict (requires at least one --tenant)
#   --with-auth KEY          apikey auth; prefer exporting XOLU_API_KEYS instead,
#                            since a key on the command line is visible in ps
#   --after COMMAND          run COMMAND (via bash -c) once the server is ready.
#                            It sees XOLU_BASE_URL, XOLU_BASE_DIR, XOLU_TENANTS
#                            and, with auth, XOLU_API_KEY. If it fails, the
#                            server is stopped and this script exits non-zero.
#   -h, --help               show this help
#
# Examples:
#   launch.sh --xolu-source ~/src/xolu
#   launch.sh --daemon --tenant acme --tenant globex --strict-tenants
#   launch.sh --stable --daemon
#   launch.sh --disable cal,blob --after './my_seed.sh'
#
# The listen port is 9090 (xolu's default) unless XOLU_PORT, or the port in
# XOLU_ADDR, says otherwise.
# Every other XOLU_* variable in your environment passes through to xolu
# untouched. Settings below can be overridden the same way, for example
# XOLU_BASE_DIR=/other/path launch.sh. The profile and --enable/--disable
# decide the four feature flags and the store layout; anything you export for
# those is overwritten, so that a profile is always reproducible.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# This script lives in <repo>/scripts, so the repo root is one level up.
XOLU_REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

XOLU_SOURCE_DIR="${XOLU_SOURCE:-$XOLU_REPO_ROOT}"
BIN_PATH="${XOLU_BIN_PATH:-$XOLU_REPO_ROOT/bin/xolu}"
IOLU_BIN="${XOLU_IOLU_BIN:-$XOLU_REPO_ROOT/bin/iolu}"
BASE_DIR="${XOLU_BASE_DIR:-$XOLU_REPO_ROOT/data}"
SCHEMA_DIR="${XOLU_SCHEMA_DIR:-$XOLU_REPO_ROOT/schema}"
LOG_FILE="${XOLU_LOG_FILE:-$XOLU_REPO_ROOT/logs/xolu.log}"
PID_FILE="${XOLU_PID_FILE:-$XOLU_REPO_ROOT/run/xolu.pid}"
READY_TIMEOUT_SECS="${XOLU_READY_TIMEOUT:-30}"

# Host and port follow xolu's own precedence: XOLU_HOST / XOLU_PORT, then
# XOLU_ADDR (host:port), then the launcher's defaults. The default port is
# spelled out as 9090 (xolu's own default) rather than left to chance.
ADDR_HOST=""
ADDR_PORT=""
if [[ "${XOLU_ADDR:-}" == *:* ]]; then
    ADDR_HOST="${XOLU_ADDR%:*}"
    ADDR_PORT="${XOLU_ADDR##*:}"
fi
HOST="${XOLU_HOST:-${ADDR_HOST:-127.0.0.1}}"
PORT="${XOLU_PORT:-${ADDR_PORT:-9090}}"
LOG_LEVEL="${XOLU_LOG_LEVEL:-info}"

COMMAND="start"
DAEMON=0
SKIP_BUILD=0
STABLE=0
STORE="shared"
STRICT_TENANTS=0
AFTER_CMD=""
AUTH_TYPE="none"
API_KEY=""
TENANTS=()
FEATURE_OVERRIDES=()

# Feature state; set from the profile, then --enable/--disable.
F_V2="true"; F_BAL="true"; F_CAL="true"; F_BLOB="true"

die() { echo "error: $*" >&2; exit 1; }

print_help() {
    {
        read -r _shebang
        while IFS= read -r line && [[ "$line" == \#* ]]; do
            line="${line#\#}"
            printf '%s\n' "${line# }"
        done
    } < "${BASH_SOURCE[0]}"
}

pid_alive() {
    local pid="$1"
    [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null
}

read_pidfile() {
    [[ -f "$PID_FILE" ]] && cat "$PID_FILE" || true
}

cmd_status() {
    local pid
    pid="$(read_pidfile)"
    if pid_alive "$pid"; then
        if curl -sf -o /dev/null "http://$HOST:$PORT/health"; then
            echo "running (pid $pid), healthy at http://$HOST:$PORT"
        else
            echo "running (pid $pid), but /health at http://$HOST:$PORT did not answer"
            return 1
        fi
    else
        echo "not running"
        return 3
    fi
}

cmd_stop() {
    local pid waited=0
    pid="$(read_pidfile)"
    if ! pid_alive "$pid"; then
        echo "not running"
        rm -f "$PID_FILE"
        return 0
    fi
    kill "$pid"
    while pid_alive "$pid" && (( waited < 10 )); do
        sleep 1
        waited=$((waited + 1))
    done
    if pid_alive "$pid"; then
        die "pid $pid did not exit within 10s (try: kill -9 $pid)"
    fi
    rm -f "$PID_FILE"
    echo "stopped (pid $pid)"
}

# ---------------------------------------------------------------------------
# Arguments
# ---------------------------------------------------------------------------

if [[ $# -gt 0 ]]; then
    case "$1" in
        stop|status) COMMAND="$1"; shift ;;
    esac
fi

add_features() {
    # add_features <true|false> <comma list>
    local value="$1" list="$2" name
    local IFS=','
    for name in $list; do
        case "$name" in
            v2|bal|cal|blob) FEATURE_OVERRIDES+=("$name=$value") ;;
            *) die "unknown feature '$name' (want: v2, bal, cal, blob)" ;;
        esac
    done
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --daemon)         DAEMON=1; shift ;;
        --skip-build)     SKIP_BUILD=1; shift ;;
        --stable)         STABLE=1; shift ;;
        --strict-tenants) STRICT_TENANTS=1; shift ;;
        --xolu-source)    XOLU_SOURCE_DIR="${2:?--xolu-source requires a path}"; shift 2 ;;
        --store)          STORE="${2:?--store requires shared or per-file}"; shift 2 ;;
        --tenant)         TENANTS+=("${2:?--tenant requires a name}"); shift 2 ;;
        --enable)         add_features true  "${2:?--enable requires a feature list}"; shift 2 ;;
        --disable)        add_features false "${2:?--disable requires a feature list}"; shift 2 ;;
        --after)          AFTER_CMD="${2:?--after requires a command string}"; shift 2 ;;
        --with-auth)      API_KEY="${2:?--with-auth requires a key}"; shift 2 ;;
        -h|--help)        print_help; exit 0 ;;
        *)                die "unknown argument: $1 (try --help)" ;;
    esac
done

case "$COMMAND" in
    status) cmd_status; exit $? ;;
    stop)   cmd_stop;   exit $? ;;
esac

case "$STORE" in
    shared)   PER_FILE="false" ;;
    per-file) PER_FILE="true" ;;
    *)        die "--store must be 'shared' or 'per-file', got '$STORE'" ;;
esac

if [[ "$STRICT_TENANTS" == "1" && ${#TENANTS[@]} -eq 0 ]]; then
    die "--strict-tenants needs at least one --tenant, otherwise no request could succeed"
fi

# Profile first, then explicit flags in the order given.
if [[ "$STABLE" == "1" ]]; then
    F_V2="false"; F_BAL="false"; F_CAL="false"; F_BLOB="false"
    PROFILE="stable"
else
    PROFILE="latest"
fi
for override in ${FEATURE_OVERRIDES[@]+"${FEATURE_OVERRIDES[@]}"}; do
    case "${override%%=*}" in
        v2)   F_V2="${override#*=}" ;;
        bal)  F_BAL="${override#*=}" ;;
        cal)  F_CAL="${override#*=}" ;;
        blob) F_BLOB="${override#*=}" ;;
    esac
done

# Auth: an explicit key wins; otherwise an already-exported XOLU_API_KEYS
# switches apikey auth on.
if [[ -n "$API_KEY" ]]; then
    AUTH_TYPE="apikey"
elif [[ -n "${XOLU_API_KEYS:-}" ]]; then
    AUTH_TYPE="apikey"
    API_KEY="$XOLU_API_KEYS"
fi

# Refuse to start a second copy.
existing_pid="$(read_pidfile)"
if pid_alive "$existing_pid"; then
    die "already running (pid $existing_pid); use '$0 stop' first"
fi

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

NEED_IOLU=0
[[ ${#TENANTS[@]} -gt 0 ]] && NEED_IOLU=1

if [[ "$SKIP_BUILD" != "1" ]]; then
    command -v go >/dev/null 2>&1 || die "'go' not found on PATH; needed to build xolu (or use --skip-build)"
    [[ -d "$XOLU_SOURCE_DIR/cmd/xolu" ]] || die "$XOLU_SOURCE_DIR has no cmd/xolu; pass --xolu-source /path/to/xolu"
    echo "-- building from $XOLU_SOURCE_DIR --"
    mkdir -p "$(dirname "$BIN_PATH")"
    (cd "$XOLU_SOURCE_DIR" && go build -o "$BIN_PATH" ./cmd/xolu)
    echo "  built: $BIN_PATH"
    if [[ "$NEED_IOLU" == "1" ]]; then
        mkdir -p "$(dirname "$IOLU_BIN")"
        (cd "$XOLU_SOURCE_DIR" && go build -o "$IOLU_BIN" ./cmd/iolu)
        echo "  built: $IOLU_BIN"
    fi
else
    [[ -x "$BIN_PATH" ]] || die "--skip-build given but $BIN_PATH does not exist or is not executable"
    if [[ "$NEED_IOLU" == "1" && ! -x "$IOLU_BIN" ]]; then
        die "--skip-build given but $IOLU_BIN is missing (set XOLU_IOLU_BIN); iolu is needed for --tenant"
    fi
    echo "-- skipping build, using $BIN_PATH --"
fi
echo

mkdir -p "$BASE_DIR" "$SCHEMA_DIR" "$(dirname "$LOG_FILE")" "$(dirname "$PID_FILE")"

# ---------------------------------------------------------------------------
# Tenants: must exist before boot when --strict-tenants is used, and are
# harmless to pre-create otherwise.
# ---------------------------------------------------------------------------

for tenant in ${TENANTS[@]+"${TENANTS[@]}"}; do
    echo "-- provisioning tenant '$tenant' (store: $STORE) --"
    set +e
    iolu_output="$("$IOLU_BIN" tenant create --base-dir "$BASE_DIR" --mode "$STORE" --name "$tenant" 2>&1)"
    iolu_status=$?
    set -e
    if [[ $iolu_status -eq 0 ]]; then
        echo "  $iolu_output"
    elif [[ "$iolu_output" == *"already exists"* ]]; then
        echo "  tenant '$tenant' already exists -- continuing"
    else
        echo "iolu tenant create failed for '$tenant':" >&2
        echo "  $iolu_output" >&2
        exit 1
    fi
done
[[ ${#TENANTS[@]} -gt 0 ]] && echo

# ---------------------------------------------------------------------------
# Environment for the server
# ---------------------------------------------------------------------------

export XOLU_HOST="$HOST"
export XOLU_PORT="$PORT"
export XOLU_BASE_DIR="$BASE_DIR"
export XOLU_SCHEMA_DIR="$SCHEMA_DIR"
export XOLU_LOG_LEVEL="$LOG_LEVEL"

# The server and iolu must agree on the store layout, so both come from --store.
export XOLU_SQLITE_PER_FILE_TENANTS="$PER_FILE"

export XOLU_API_V2_ENABLED="$F_V2"
export XOLU_BAL_ENABLED="$F_BAL"
export XOLU_CAL_ENABLED="$F_CAL"
export XOLU_BLOB_ENABLED="$F_BLOB"

if [[ "$STRICT_TENANTS" == "1" ]]; then
    export XOLU_TENANT_MODE="strict"
fi

export XOLU_AUTH_TYPE="$AUTH_TYPE"
if [[ "$AUTH_TYPE" == "apikey" ]]; then
    export XOLU_API_KEYS="$API_KEY"
fi

echo "-- configuration --"
echo "  profile:     $PROFILE"
echo "  listening:   http://$HOST:$PORT"
echo "  base dir:    $BASE_DIR"
echo "  schema dir:  $SCHEMA_DIR"
echo "  store:       $STORE"
echo "  features:    v2=$F_V2 bal=$F_BAL cal=$F_CAL blob=$F_BLOB"
echo "  tenants:     ${TENANTS[*]:-(none pre-registered)}"
[[ "$STRICT_TENANTS" == "1" ]] && echo "  tenant mode: strict"
echo "  auth type:   $AUTH_TYPE"
echo

# ---------------------------------------------------------------------------
# Launch
# ---------------------------------------------------------------------------

XOLU_PID=""

wait_for_ready() {
    local url="http://$HOST:$PORT/health"
    local waited=0
    echo "-- waiting for $url --"
    while (( waited < READY_TIMEOUT_SECS )); do
        if ! pid_alive "$XOLU_PID"; then
            echo "server exited during startup -- check $LOG_FILE" >&2
            return 1
        fi
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

abort_server() {
    echo "startup failed; last 40 log lines:" >&2
    tail -n 40 "$LOG_FILE" >&2 || true
    kill "$XOLU_PID" 2>/dev/null || true
    rm -f "$PID_FILE"
    exit 1
}

run_after() {
    [[ -n "$AFTER_CMD" ]] || return 0
    echo
    echo "-- running --after command --"
    local status=0
    XOLU_BASE_URL="http://$HOST:$PORT" \
    XOLU_BASE_DIR="$BASE_DIR" \
    XOLU_TENANTS="${TENANTS[*]:-}" \
    XOLU_API_KEY="${API_KEY%%,*}" \
        bash -c "$AFTER_CMD" || status=$?
    if [[ $status -ne 0 ]]; then
        echo "--after command failed (exit $status); stopping the server" >&2
        kill "$XOLU_PID" 2>/dev/null || true
        rm -f "$PID_FILE"
        exit "$status"
    fi
}

print_handoff() {
    echo
    echo "-- xolu is up --"
    echo "  base URL: http://$HOST:$PORT"
    echo "  profile:  $PROFILE"
    if [[ ${#TENANTS[@]} -gt 0 ]]; then
        echo "  tenants:  ${TENANTS[*]}"
    fi
    if [[ "$AUTH_TYPE" == "apikey" ]]; then
        echo "  auth:     apikey (key not echoed)"
    fi
    echo
}

if [[ "$DAEMON" == "1" ]]; then
    echo "-- starting in background (log: $LOG_FILE) --"
    nohup "$BIN_PATH" > "$LOG_FILE" 2>&1 &
    XOLU_PID=$!
    echo "$XOLU_PID" > "$PID_FILE"
    echo "  pid: $XOLU_PID"
    wait_for_ready || abort_server
    run_after
    print_handoff
    echo "stop with: $0 stop"
else
    echo "-- starting in foreground (Ctrl-C to stop; log also written to $LOG_FILE) --"
    echo
    "$BIN_PATH" > >(tee "$LOG_FILE") 2>&1 &
    XOLU_PID=$!
    echo "$XOLU_PID" > "$PID_FILE"
    trap 'kill "$XOLU_PID" 2>/dev/null || true' INT TERM
    trap 'rm -f "$PID_FILE"' EXIT
    wait_for_ready || abort_server
    run_after
    print_handoff
    wait "$XOLU_PID"
fi
