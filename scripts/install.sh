#!/usr/bin/env bash
set -euo pipefail

# CodeFlow one-shot installer. It installs an owned binary, registers its MCP
# with Codex, and installs the accompanying CodeFlow skill. No shell profile
# or repository files are changed.
#
# From a checkout: bash scripts/install.sh
# From a release script: CODEFLOW_REPO_URL=<repository> bash scripts/install.sh

INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
SRC_DIR="${CODEFLOW_SRC_DIR:-$HOME/.codeflow/src}"
REPO_URL="${CODEFLOW_REPO_URL:-}"
BRANCH="${CODEFLOW_BRANCH:-main}"
MCP_NAME="${CODEFLOW_MCP_NAME:-codeflow}"
CODEX_HOME_DIR="${CODEX_HOME:-$HOME/.codex}"
OWNED_SOURCE=false

info() { echo "› $*"; }
die() { echo "✗ $*" >&2; exit 1; }

require() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required: $2"
}

require go "install Go, then run this command again"
require dart "install Dart SDK 3.x, then run this command again"
require codex "install Codex, then run this command again to register the MCP"

# Prefer the checkout that contains this script. A remote script can supply a
# repository URL; an existing managed source is reused without reset/pull.
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
if [ -f "$SCRIPT_DIR/../go.mod" ] && grep -q '^module codeflow$' "$SCRIPT_DIR/../go.mod"; then
  SRC_DIR="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)"
  info "Using current checkout: $SRC_DIR"
elif [ -d "$SRC_DIR/.git" ]; then
  info "Using existing managed source: $SRC_DIR"
elif [ -e "$SRC_DIR" ]; then
  die "$SRC_DIR already exists and is not a CodeFlow checkout; choose CODEFLOW_SRC_DIR instead"
else
  [ -n "$REPO_URL" ] || die "set CODEFLOW_REPO_URL when running outside a CodeFlow checkout"
  mkdir -p "$(dirname "$SRC_DIR")"
  info "Cloning CodeFlow source"
  git clone --depth 1 --branch "$BRANCH" "$REPO_URL" "$SRC_DIR"
  OWNED_SOURCE=true
fi

DART_ADAPTER_DIR="$SRC_DIR/adapters/dart"
[ -f "$DART_ADAPTER_DIR/bin/codeflow_dart_adapter.dart" ] || die "Dart adapter is missing from $SRC_DIR"
ADAPTER_SPEC="dartrun:$DART_ADAPTER_DIR"
INSTALL_PATH="$INSTALL_DIR/codeflow"
SKILL_SOURCE="$SRC_DIR/skills/codeflow"
SKILL_DEST="$CODEX_HOME_DIR/skills/codeflow"
[ -f "$SKILL_SOURCE/SKILL.md" ] || die "CodeFlow skill is missing from $SRC_DIR"

# Never overwrite a user-owned MCP registration or skill. Re-running the
# installer is safe only for the exact asset installed by CodeFlow.
if MCP_CONFIG="$(codex mcp get "$MCP_NAME" --json 2>/dev/null)"; then
  if ! printf '%s\n' "$MCP_CONFIG" | grep -Fq "$INSTALL_PATH"; then
    die "Codex MCP '$MCP_NAME' already belongs to another command; use CODEFLOW_MCP_NAME to choose a new name"
  fi
fi
if [ -e "$SKILL_DEST" ] && ! cmp -s "$SKILL_SOURCE/SKILL.md" "$SKILL_DEST/SKILL.md"; then
  die "Codex skill at $SKILL_DEST was changed; refusing to overwrite it"
fi

info "Building CodeFlow"
make -C "$SRC_DIR" build
mkdir -p "$INSTALL_DIR"
cp -f "$SRC_DIR/bin/codeflow" "$INSTALL_PATH"
chmod 755 "$INSTALL_PATH"

if ! command -v codeflow >/dev/null 2>&1 || [ "$(command -v codeflow)" != "$INSTALL_PATH" ]; then
  info "Installed binary: $INSTALL_PATH (add $INSTALL_DIR to PATH to use 'codeflow' directly)"
fi

mkdir -p "$(dirname "$SKILL_DEST")"
if [ ! -e "$SKILL_DEST" ]; then
  cp -R "$SKILL_SOURCE" "$SKILL_DEST"
  info "Installed CodeFlow skill"
fi

if codex mcp get "$MCP_NAME" --json >/dev/null 2>&1; then
  info "Codex MCP '$MCP_NAME' is already registered"
else
  codex mcp add "$MCP_NAME" --env "CODEFLOW_ADAPTER_DART_BIN=$ADAPTER_SPEC" -- "$INSTALL_PATH" mcp
  info "Registered Codex MCP '$MCP_NAME'"
fi

SKILL_SHA256="$(shasum -a 256 "$SKILL_DEST/SKILL.md" | awk '{print $1}')"
"$INSTALL_PATH" install-record \
  --binary "$INSTALL_PATH" \
  --source-root "$SRC_DIR" \
  --owned-source="$OWNED_SOURCE" \
  --adapter-spec "$ADAPTER_SPEC" \
  --skill-path "$SKILL_DEST" \
  --skill-sha256 "$SKILL_SHA256" \
  --mcp-name "$MCP_NAME"

"$INSTALL_PATH" doctor . || true

cat <<EOF

✓ CodeFlow is ready in Codex.
  Start a new Codex task and ask: "이메일 회원가입 흐름을 FlowView로 만들어줘"

Remove it later with:
  $INSTALL_PATH uninstall
EOF
