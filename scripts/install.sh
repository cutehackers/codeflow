#!/usr/bin/env bash
set -euo pipefail

# CodeFlow one-shot installer.
# Supports:
# 1. Direct one-liner via curl: curl -fsSL https://raw.githubusercontent.com/cutehackers/codeflow/main/scripts/install.sh | bash
# 2. Local checkout build: bash scripts/install.sh

INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
MCP_NAME="${CODEFLOW_MCP_NAME:-codeflow}"
CODEX_HOME_DIR="${CODEX_HOME:-$HOME/.codex}"
CODEFLOW_REPO="${CODEFLOW_REPO:-cutehackers/codeflow}"
CODEFLOW_VERSION="${CODEFLOW_VERSION:-v0.3.2}"
OWNED_SOURCE=false
SRC_DIR="${CODEFLOW_SRC_DIR:-}"

info() { echo "› $*"; }
die() { echo "✗ $*" >&2; exit 1; }

calc_sha256() {
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    echo ""
  fi
}

# Determine if we are inside a CodeFlow checkout
SCRIPT_DIR=""
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "${BASH_SOURCE[0]}" ]; then
  SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" 2>/dev/null && pwd || true)"
fi

IS_CHECKOUT=false
if [ -n "$SCRIPT_DIR" ] && [ -f "$SCRIPT_DIR/../go.mod" ] && grep -q '^module codeflow$' "$SCRIPT_DIR/../go.mod"; then
  IS_CHECKOUT=true
  SRC_DIR="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)"
fi

TMP_DIR=""
cleanup() {
  if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then
    rm -rf "$TMP_DIR"
  fi
}
trap cleanup EXIT

INSTALL_PATH="$INSTALL_DIR/codeflow"
mkdir -p "$INSTALL_DIR"

if [ "$IS_CHECKOUT" = true ]; then
  info "Installing CodeFlow from local checkout: $SRC_DIR"
  command -v go >/dev/null 2>&1 || die "Go is required to build from source checkout"

  info "Building CodeFlow binaries"
  (cd "$SRC_DIR" && go build -o "$INSTALL_PATH" ./cmd/codeflow)
  chmod 755 "$INSTALL_PATH"

  ADAPTER_BIN="$INSTALL_DIR/dart-adapter"
  DART_SRC="$SRC_DIR/adapters/dart"
  if command -v dart >/dev/null 2>&1; then
    if (cd "$DART_SRC" && dart compile exe bin/codeflow_dart_adapter.dart -o "$ADAPTER_BIN" >/dev/null 2>&1); then
      chmod 755 "$ADAPTER_BIN"
      ADAPTER_SPEC="$ADAPTER_BIN"
    else
      ADAPTER_SPEC="dartrun:$DART_SRC"
    fi
  else
    ADAPTER_SPEC="dartrun:$DART_SRC"
  fi

  SKILL_SOURCE="$SRC_DIR/skills/codeflow"
else
  info "Installing CodeFlow pre-compiled binary ($CODEFLOW_VERSION)..."

  OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
  case "$OS" in
    darwin|linux) ;;
    *) die "Unsupported operating system: $OS (only macOS and Linux are supported)" ;;
  esac

  ARCH="$(uname -m)"
  case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    *) die "Unsupported architecture: $ARCH (only amd64 and arm64 are supported)" ;;
  esac

  TMP_DIR="$(mktemp -d)"
  TARBALL_NAME="codeflow-${CODEFLOW_VERSION}-${OS}-${ARCH}.tar.gz"
  DOWNLOAD_URL="https://github.com/${CODEFLOW_REPO}/releases/download/${CODEFLOW_VERSION}/${TARBALL_NAME}"

  info "Downloading $DOWNLOAD_URL"
  if command -v curl >/dev/null 2>&1; then
    if ! curl -fsSL "$DOWNLOAD_URL" -o "$TMP_DIR/$TARBALL_NAME"; then
      die "Failed to download $DOWNLOAD_URL. Please check release version or network connection."
    fi
  elif command -v wget >/dev/null 2>&1; then
    if ! wget -q "$DOWNLOAD_URL" -O "$TMP_DIR/$TARBALL_NAME"; then
      die "Failed to download $DOWNLOAD_URL. Please check release version or network connection."
    fi
  else
    die "Neither curl nor wget was found on PATH"
  fi

  tar -xzf "$TMP_DIR/$TARBALL_NAME" -C "$TMP_DIR"

  cp -f "$TMP_DIR/bin/codeflow" "$INSTALL_PATH"
  chmod 755 "$INSTALL_PATH"

  ADAPTER_BIN="$INSTALL_DIR/dart-adapter"
  if [ -f "$TMP_DIR/bin/dart-adapter" ]; then
    cp -f "$TMP_DIR/bin/dart-adapter" "$ADAPTER_BIN"
    chmod 755 "$ADAPTER_BIN"
    ADAPTER_SPEC="$ADAPTER_BIN"
  else
    ADAPTER_SPEC=""
  fi

  SKILL_SOURCE="$TMP_DIR/skills/codeflow"
fi

SKILL_DEST="$CODEX_HOME_DIR/skills/codeflow"
if command -v codex >/dev/null 2>&1; then
  if MCP_CONFIG="$(codex mcp get "$MCP_NAME" --json 2>/dev/null)"; then
    if ! printf '%s\n' "$MCP_CONFIG" | grep -Fq "$INSTALL_PATH"; then
      die "Codex MCP '$MCP_NAME' already belongs to another command; use CODEFLOW_MCP_NAME to choose a new name"
    fi
  fi
  if [ -e "$SKILL_DEST" ] && [ -f "$SKILL_SOURCE/SKILL.md" ] && ! cmp -s "$SKILL_SOURCE/SKILL.md" "$SKILL_DEST/SKILL.md"; then
    die "Codex skill at $SKILL_DEST was changed; refusing to overwrite it"
  fi
fi

if [ -d "$SKILL_SOURCE" ]; then
  mkdir -p "$(dirname "$SKILL_DEST")"
  if [ ! -e "$SKILL_DEST" ]; then
    cp -R "$SKILL_SOURCE" "$SKILL_DEST"
    info "Installed CodeFlow skill"
  fi
fi

if command -v codex >/dev/null 2>&1; then
  if codex mcp get "$MCP_NAME" --json >/dev/null 2>&1; then
    info "Codex MCP '$MCP_NAME' is already registered"
  else
    codex mcp add "$MCP_NAME" --env "CODEFLOW_ADAPTER_DART_BIN=$ADAPTER_SPEC" -- "$INSTALL_PATH" mcp
    info "Registered Codex MCP '$MCP_NAME'"
  fi
fi

SKILL_SHA256=""
if [ -f "$SKILL_DEST/SKILL.md" ]; then
  SKILL_SHA256="$(calc_sha256 "$SKILL_DEST/SKILL.md")"
fi

"$INSTALL_PATH" install-record \
  --binary "$INSTALL_PATH" \
  --source-root "${SRC_DIR:-}" \
  --owned-source="$OWNED_SOURCE" \
  --adapter-spec "$ADAPTER_SPEC" \
  --skill-path "$SKILL_DEST" \
  --skill-sha256 "$SKILL_SHA256" \
  --mcp-name "$MCP_NAME"

"$INSTALL_PATH" doctor . || true

cat <<EOF

✓ CodeFlow installation complete!
  Binary: $INSTALL_PATH

  To use in Codex / Claude / Cursor / Antigravity:
  - Command: $INSTALL_PATH mcp
  - Env: CODEFLOW_ADAPTER_DART_BIN=$ADAPTER_SPEC

  Remove anytime with:
  $INSTALL_PATH uninstall
EOF
