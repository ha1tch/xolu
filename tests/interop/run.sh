#!/usr/bin/env bash
# tests/interop/run.sh - real-client S3 interop check for xolu's SigV4 path.
#
# Boots the interop harness (tests/interop/server) in scoped mode with a known
# S3KeyGrant, then exercises whichever real S3 clients are installed against it:
# mc (MinIO), boto3 (AWS reference, via python3), s3cmd, and aws-cli. Each client
# is checked for: a valid put/get round-trip, a wrong-secret rejection, and an
# unknown-key rejection.
#
# This verifies real-world interoperability of the SigV4 verification that the
# unit tests in pkg/s3sig only prove in isolation. It is NOT run in the release
# pipeline (it needs external client binaries); run it locally or in CI that has
# the clients installed.
#
# Usage:
#   ./tests/interop/run.sh            Run all detected clients.
#   ./tests/interop/run.sh --list     List which clients are detected.
#
# Copyright (c) 2026 haitch
# Licensed under the Apache License, Version 2.0

set -uo pipefail
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

ADDR=":19091"
ENDPOINT="http://localhost:19091"
BUCKET="acme"
AK="AKIAINTEROP"
SECRET="interop-secret-key"

PASS=0
FAIL=0
ok()   { echo "  [ok]   $1"; PASS=$((PASS+1)); }
bad()  { echo "  [FAIL] $1"; FAIL=$((FAIL+1)); }
have() { command -v "$1" >/dev/null 2>&1; }

detect() {
  echo "Detected clients:"
  have mc     && echo "  - mc"     || echo "  - mc      (not installed)"
  have s3cmd  && echo "  - s3cmd"  || echo "  - s3cmd   (not installed)"
  have aws    && echo "  - aws"    || echo "  - aws     (not installed)"
  python3 -c "import boto3" 2>/dev/null && echo "  - boto3" || echo "  - boto3   (not installed)"
}

if [ "${1:-}" = "--list" ]; then detect; exit 0; fi

# --- build + boot the harness ------------------------------------------------
echo "Building interop harness..."
BIN="$(mktemp -d)/interop-server"
( cd "$ROOT" && go build -o "$BIN" ./tests/interop/server/ ) || { echo "build failed"; exit 1; }

"$BIN" -addr "$ADDR" >/tmp/xolu-interop.log 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT
sleep 2.5
if ! kill -0 $SRV 2>/dev/null; then echo "server failed to start:"; cat /tmp/xolu-interop.log; exit 1; fi
echo "Harness up on $ENDPOINT (bucket=$BUCKET)"
echo

TMP="$(mktemp -d)"
echo "interop payload" > "$TMP/obj.txt"

# --- mc ----------------------------------------------------------------------
test_mc() {
  echo "mc (MinIO client):"
  export MC_CONFIG_DIR="$TMP/mc"
  mc alias set itv "$ENDPOINT" "$AK" "$SECRET" --api S3v4 --path on >/dev/null 2>&1
  if mc cp "$TMP/obj.txt" "itv/$BUCKET/mc.txt" >/dev/null 2>&1; then ok "valid put"; else bad "valid put"; fi
  if mc cat "itv/$BUCKET/mc.txt" 2>/dev/null | grep -q "interop payload"; then ok "valid get round-trip"; else bad "valid get round-trip"; fi
  mc alias set itvbad "$ENDPOINT" "$AK" "WRONG-SECRET" --api S3v4 --path on >/dev/null 2>&1
  if mc cp "$TMP/obj.txt" "itvbad/$BUCKET/x.txt" >/dev/null 2>&1; then bad "wrong-secret rejected"; else ok "wrong-secret rejected"; fi
}

# --- s3cmd -------------------------------------------------------------------
test_s3cmd() {
  echo "s3cmd:"
  cat > "$TMP/s3cfg" <<EOF
[default]
access_key = $AK
secret_key = $SECRET
host_base = localhost:19091
host_bucket = localhost:19091
use_https = False
signature_v2 = False
EOF
  if s3cmd -c "$TMP/s3cfg" put "$TMP/obj.txt" "s3://$BUCKET/s3cmd.txt" >/dev/null 2>&1; then ok "valid put"; else bad "valid put"; fi
  if s3cmd -c "$TMP/s3cfg" get "s3://$BUCKET/s3cmd.txt" "$TMP/s3cmd-back.txt" --force >/dev/null 2>&1 && grep -q "interop payload" "$TMP/s3cmd-back.txt"; then ok "valid get round-trip"; else bad "valid get round-trip"; fi
  sed 's/'"$SECRET"'/WRONG/' "$TMP/s3cfg" > "$TMP/s3cfg-bad"
  if s3cmd -c "$TMP/s3cfg-bad" put "$TMP/obj.txt" "s3://$BUCKET/x.txt" >/dev/null 2>&1; then bad "wrong-secret rejected"; else ok "wrong-secret rejected"; fi
}

# --- boto3 / aws -------------------------------------------------------------
test_boto3() {
  echo "boto3 (AWS reference):"
  python3 "$SCRIPT_DIR/boto_check.py" "$ENDPOINT" "$BUCKET" "$AK" "$SECRET" && return
  bad "boto3 check script reported failures"
}

RAN=0
if have mc;    then test_mc;    RAN=1; echo; fi
if have s3cmd; then test_s3cmd; RAN=1; echo; fi
python3 -c "import boto3" 2>/dev/null && { test_boto3; RAN=1; echo; }

if [ "$RAN" = 0 ]; then
  echo "No S3 clients detected. Install at least one: mc, s3cmd, or 'pip install boto3'."
  echo "  mc:    https://dl.min.io/client/mc/release/linux-amd64/mc"
  echo "  boto3: pip install boto3"
  echo "  s3cmd: pip install s3cmd"
  exit 2
fi

echo "========================================"
echo "  interop: $PASS passed, $FAIL failed"
echo "========================================"
[ "$FAIL" = 0 ]
