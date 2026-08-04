#!/usr/bin/env bash
#
# launch_xolu_for_crm.sh
#
# Builds and launches a xolu instance configured for the CRM seed
# script (xolu_crm_seed.py) to run against: graph enabled (REF fields
# need it -- RegisterAdaptedEdge hard-errors without it), strict tenant
# mode (matches the seed script's own explicit iolu-based tenant
# provisioning -- no silent fallback to tenant 0), and no auth by
# default (the seed script's own "typical local dev setup" case).
#
# Usage:
#   ./launch_xolu_for_crm.sh                  # foreground, Ctrl-C to stop
#   ./launch_xolu_for_crm.sh --daemon          # background, prints PID
#   ./launch_xolu_for_crm.sh --tenant acme     # provision a specific tenant name
#   ./launch_xolu_for_crm.sh --with-auth KEY   # AuthType=apikey, XOLU_API_KEYS=KEY
#
# Every setting below is a plain shell variable with an env-var
# override (XOLU_CRM_BASE_DIR=/other/path ./launch_xolu_for_crm.sh),
# not a hidden default -- change what you need before running.
#
set -euo pipefail

# ---------------------------------------------------------------------------
# Settings (override via environment before invoking, e.g.
# XOLU_CRM_PORT=9090 ./launch_xolu_for_crm.sh)
# ---------------------------------------------------------------------------

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
BIN_PATH="${XOLU_CRM_BIN_PATH:-$REPO_ROOT/bin/xolu}"
BASE_DIR="${XOLU_CRM_BASE_DIR:-$REPO_ROOT/examples/crm/xolu-crm-data}"
SCHEMA_DIR="${XOLU_CRM_SCHEMA_DIR:-$BASE_DIR/schema}"
HOST="${XOLU_CRM_HOST:-127.0.0.1}"
PORT="${XOLU_CRM_PORT:-8080}"
LOG_LEVEL="${XOLU_CRM_LOG_LEVEL:-info}"
LOG_FILE="${XOLU_CRM_LOG_FILE:-$BASE_DIR/xolu.log}"
READY_TIMEOUT_SECS="${XOLU_CRM_READY_TIMEOUT:-30}"

DAEMON=0
AUTH_TYPE="none"
API_KEY=""
TENANT="${XOLU_CRM_TENANT:-acme_crm}"
IOLU_BIN="${XOLU_CRM_IOLU_BIN:-$REPO_ROOT/bin/iolu}"

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
        -h|--help)
            sed -n '2,20p' "${BASH_SOURCE[0]}"
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
    echo "-- building cmd/xolu and cmd/iolu --"
    mkdir -p "$(dirname "$BIN_PATH")"
    (cd "$REPO_ROOT" && go build -o "$BIN_PATH" ./cmd/xolu)
    (cd "$REPO_ROOT" && go build -o "$IOLU_BIN" ./cmd/iolu)
    echo "  built: $BIN_PATH"
    echo "  built: $IOLU_BIN"
else
    if [[ ! -x "$BIN_PATH" ]]; then
        echo "--skip-build given but $BIN_PATH does not exist or isn't executable" >&2
        exit 1
    fi
    echo "-- skipping build, using existing $BIN_PATH --"
fi
echo

mkdir -p "$BASE_DIR" "$SCHEMA_DIR"

# ---------------------------------------------------------------------------
# Provision the tenant BEFORE the server starts.
#
# This ordering is required, not a convenience: TenantMode=strict has no
# dynamic tenant-registration path. tenant.Registry.Lookup reads a plain
# in-memory map that gets populated exactly once, at startup, via
# LoadFrom (pkg/tenant/tenant.go's own doc comment: "This should be
# called once at startup"). iolu writes the new tenant row through its
# own, separate DB connection -- invisible to an already-running
# server's in-memory registry until a restart. Verified directly: an
# earlier version of this script provisioned the tenant via iolu AFTER
# starting the server (leaving it to the seed script's own iolu step,
# which runs later) and every request against that tenant came back
# "Unknown tenant" despite iolu itself reporting success.
#
# The seed script also calls iolu tenant create on its own, for
# portability when run against a server this launcher didn't start --
# that's harmless here: iolu's "already exists" response is treated as
# success by both scripts.
# ---------------------------------------------------------------------------

echo "-- provisioning tenant '$TENANT' (must happen before the server boots) --"
set +e
# --mode shared is required, not optional: on a fresh/empty base dir,
# iolu's own auto-detection has nothing to detect and falls back to
# per-file mode by default (cmd/iolu/main.go's own cmdTenantCreate),
# while the server's config default is SQLitePerFileTenants=false
# (shared) -- pkg/config/config.go's Default(). Left unset, iolu writes
# the tenant registry into a per-tenant file the server's shared-mode
# store never opens, and every request against that tenant 404s with
# "Unknown tenant" no matter what order things start in. Verified
# directly: this was the actual bug, not a startup-ordering race as
# first suspected -- correcting the order alone did not fix it: iolu
# and the server were writing to two different files regardless of
# sequencing.
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

# ---------------------------------------------------------------------------
# Config -- every variable the CRM seed script's own assumptions
# actually depend on, set explicitly rather than left to defaults that
# could change.
# ---------------------------------------------------------------------------

export XOLU_HOST="$HOST"
export XOLU_PORT="$PORT"
export XOLU_BASE_DIR="$BASE_DIR"
export XOLU_SCHEMA_DIR="$SCHEMA_DIR"
export XOLU_LOG_LEVEL="$LOG_LEVEL"

# REF fields (used throughout the CRM schema: company/owner/contact/deal
# references) create graph edges on write -- RegisterAdaptedEdge hard-
# errors with "graph not enabled" if this is off. Any value other than
# "disabled" turns GraphEnabled on; "flat" is the mode used throughout
# xolu's own test suite for a standalone, non-clustered instance.
export XOLU_GRAPH_MODE="flat"

# Required, not optional, despite the CRM seed never touching a single
# /api/v2 route directly: xolu's own v1 entity-write path unconditionally
# calls into event dispatch on every create/update/delete (dispatchEvent,
# pkg/server/event_dispatch.go), which queries the event_defs table --
# but that table is only created by initV2Schema, which only runs when
# this is true. Left at its default (false), event_defs never exists,
# and every single entity write logs a WARN ("event dispatch:
# subscription lookup failed ... no such table: event_defs") for the
# entire lifetime of the server -- verified directly by actually running
# the seed against a v1-only instance, not inferred from the docs. This
# is purely additive: v2 gates a separate route surface (fsm/bal/cal/
# dxp/gen/event) and does not change v1 CRUD behavior, so there's no
# downside to leaving it on here.
export XOLU_API_V2_ENABLED="true"

# Strict: tenants must be pre-registered (iolu tenant create, which the
# CRM seed script already does), and there is no silent fallback to
# tenant 0 for an unprefixed request -- matches how the seed script
# always addresses /api/v1/tenant/{name}/... explicitly, and avoids the
# "path" mode's auto-register-on-first-access behaviour being the thing
# that quietly created the tenant instead of iolu.
export XOLU_TENANT_MODE="strict"
# "open": any authenticated caller may act on any tenant. "scoped" would
# additionally require APIKeyGrants (per-key tenant authorization),
# which needs more than an env var to configure -- out of scope for
# this launcher. Use --with-auth for a simple shared-key setup instead;
# see xolu_crm_seed.py's own docstring for the full xotogen-based scoped
# flow if you need real per-tenant credential isolation.
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

print_handoff() {
    echo
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
    echo "   exists' response is treated as success by the seed script too.)"
    echo
}

if [[ "$DAEMON" == "1" ]]; then
    echo "-- starting in background (log: $LOG_FILE) --"
    nohup "$BIN_PATH" > "$LOG_FILE" 2>&1 &
    XOLU_PID=$!
    echo "  pid: $XOLU_PID"
    if wait_for_ready; then
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
        print_handoff
    else
        tail -n 40 "$LOG_FILE" >&2 || true
        kill "$XOLU_PID" 2>/dev/null || true
        exit 1
    fi
    wait "$XOLU_PID"
fi
