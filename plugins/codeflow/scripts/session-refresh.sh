#!/bin/sh
# Optional and non-authoritative: failure is intentionally ignored by callers.
set -eu
ROOT=${1:-.}
printf '%s\n%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2026-07-28"}}' '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"refresh","arguments":{}}}' | "$(dirname "$0")/discover-codeflow.sh" mcp --repo "$ROOT" >/dev/null
