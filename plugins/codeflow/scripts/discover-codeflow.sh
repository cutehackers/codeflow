#!/bin/sh
set -eu
if [ -n "${CODEFLOW_BIN:-}" ]; then exec "$CODEFLOW_BIN" "$@"; fi
exec codeflow "$@"
