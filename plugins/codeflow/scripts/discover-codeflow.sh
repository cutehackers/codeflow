#!/bin/sh
set -eu
BIN=${CODEFLOW_BIN:-"$HOME/.codeflow/bin/codeflow"}
if [ ! -x "$BIN" ]; then
  echo "CodeFlow is not installed. Run the packaged codeflow install command." >&2
  exit 127
fi
exec "$BIN" "$@"
