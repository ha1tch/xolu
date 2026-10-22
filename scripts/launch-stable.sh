#!/usr/bin/env bash
#
# launch-stable.sh -- launch xolu with the conservative profile.
#
# Equivalent to: launch.sh --stable "$@"
# All launch.sh options work unchanged. The stop and status subcommands are
# passed through as they are, since they do not depend on the profile.
#
set -euo pipefail
LAUNCH="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/launch.sh"
case "${1:-}" in
    stop|status) exec "$LAUNCH" "$@" ;;
    *)           exec "$LAUNCH" --stable "$@" ;;
esac
