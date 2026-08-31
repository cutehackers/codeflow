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
CODEFLOW_VERSION="${CODEFLOW_VERSION:-v0.3.3}"
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

  # TypeScript adapter
  TS_SRC="$SRC_DIR/adapters/typescript"
  TS_BIN="$INSTALL_DIR/codeflow_ts_adapter"
  TS_DEST_LIB="$HOME/.local/share/codeflow/adapters/typescript"
  if [ -d "$TS_SRC" ]; then
    mkdir -p "$TS_DEST_LIB"
    cp -R "$TS_SRC/"* "$TS_DEST_LIB/"
    chmod 755 "$TS_DEST_LIB/bin/codeflow_ts_adapter.js" 2>/dev/null || true
    cat << 'WRAPPER' > "$TS_BIN"
#!/usr/bin/env bash
TS_TARGET="$HOME/.local/share/codeflow/adapters/typescript/bin/codeflow_ts_adapter.js"
if [ -f "$TS_TARGET" ]; then
  exec node "$TS_TARGET" "$@"
fi
echo "TypeScript adapter entrypoint not found at $TS_TARGET" >&2
exit 1
WRAPPER
    chmod 755 "$TS_BIN"
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

  if [ -d "$TMP_DIR/adapters/typescript" ]; then
    TS_DEST_LIB="$HOME/.local/share/codeflow/adapters/typescript"
    mkdir -p "$TS_DEST_LIB"
    cp -R "$TMP_DIR/adapters/typescript/"* "$TS_DEST_LIB/"
    chmod 755 "$TS_DEST_LIB/bin/codeflow_ts_adapter.js" 2>/dev/null || true
    cat << 'WRAPPER' > "$INSTALL_DIR/codeflow_ts_adapter"
#!/usr/bin/env bash
TS_TARGET="$HOME/.local/share/codeflow/adapters/typescript/bin/codeflow_ts_adapter.js"
if [ -f "$TS_TARGET" ]; then
  exec node "$TS_TARGET" "$@"
fi
echo "TypeScript adapter entrypoint not found at $TS_TARGET" >&2
exit 1
WRAPPER
    chmod 755 "$INSTALL_DIR/codeflow_ts_adapter"
  fi

  SKILL_SOURCE="$TMP_DIR/skills/codeflow"
fi

SKILL_DEST="$CODEX_HOME_DIR/skills/codeflow"

# Helper to merge codeflow into an MCP JSON config file safely
register_json_mcp() {
  local json_path="$1"
  local mcp_name="$2"
  local bin_path="$3"

  local parent_dir
  parent_dir="$(dirname "$json_path")"
  if [ ! -d "$parent_dir" ]; then
    return 0
  fi

  if command -v node >/dev/null 2>&1; then
    node -e '
      const fs = require("fs");
      const [filePath, name, bin] = [process.argv[1], process.argv[2], process.argv[3]];
      let data = {};
      if (fs.existsSync(filePath)) {
        try { data = JSON.parse(fs.readFileSync(filePath, "utf8")); } catch (_) { data = {}; }
      }
      if (!data || typeof data !== "object") data = {};
      if (!data.mcpServers) data.mcpServers = {};
      data.mcpServers[name] = { command: bin, args: ["mcp"] };
      fs.writeFileSync(filePath, JSON.stringify(data, null, 2) + "\n");
    ' "$json_path" "$mcp_name" "$bin_path" 2>/dev/null && return 0 || true
  fi

  if command -v python3 >/dev/null 2>&1; then
    python3 -c '
import json, os, sys
file_path, name, bin_path = sys.argv[1], sys.argv[2], sys.argv[3]
data = {}
if os.path.exists(file_path):
    try:
        with open(file_path, "r", encoding="utf-8") as f:
            data = json.load(f)
    except Exception:
        data = {}
if not isinstance(data, dict):
    data = {}
if "mcpServers" not in data or not isinstance(data["mcpServers"], dict):
    data["mcpServers"] = {}
data["mcpServers"][name] = {"command": bin_path, "args": ["mcp"]}
with open(file_path, "w", encoding="utf-8") as f:
    json.dump(data, f, indent=2)
    f.write("\n")
' "$json_path" "$mcp_name" "$bin_path" 2>/dev/null && return 0 || true
  fi
}

# 1. Codex Registration
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
    info "Installed CodeFlow skill for Codex"
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

# 2. Claude Desktop Registration
CLAUDE_CONFIG_DIR=""
if [ "$(uname -s)" = "Darwin" ]; then
  CLAUDE_CONFIG_DIR="$HOME/Library/Application Support/Claude"
else
  CLAUDE_CONFIG_DIR="$HOME/.config/Claude"
fi

if [ -d "$CLAUDE_CONFIG_DIR" ]; then
  register_json_mcp "$CLAUDE_CONFIG_DIR/claude_desktop_config.json" "$MCP_NAME" "$INSTALL_PATH"
  info "Configured Claude Desktop MCP ($CLAUDE_CONFIG_DIR/claude_desktop_config.json)"
fi

# 3. Cursor IDE Registration
CURSOR_CONFIG_DIR="$HOME/.cursor"
if [ -d "$CURSOR_CONFIG_DIR" ]; then
  register_json_mcp "$CURSOR_CONFIG_DIR/mcp.json" "$MCP_NAME" "$INSTALL_PATH"
  if [ -d "$SKILL_SOURCE" ] && [ -d "$CURSOR_CONFIG_DIR/skills" ]; then
    CURSOR_SKILL="$CURSOR_CONFIG_DIR/skills/codeflow"
    if [ ! -e "$CURSOR_SKILL" ]; then
      cp -R "$SKILL_SOURCE" "$CURSOR_SKILL"
      info "Installed CodeFlow skill for Cursor"
    fi
  fi
  info "Configured Cursor MCP ($CURSOR_CONFIG_DIR/mcp.json)"
fi

# 4. Antigravity / Gemini CLI Registration
GEMINI_DIR="$HOME/.gemini"
if [ -d "$GEMINI_DIR" ]; then
  if [ -d "$SKILL_SOURCE" ]; then
    GEMINI_SKILL="$GEMINI_DIR/antigravity-cli/skills/codeflow"
    mkdir -p "$(dirname "$GEMINI_SKILL")"
    if [ ! -e "$GEMINI_SKILL" ]; then
      cp -R "$SKILL_SOURCE" "$GEMINI_SKILL"
      info "Installed CodeFlow skill for Antigravity"
    fi
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

  Auto-configured for all detected agents:
  - Codex: $CODEX_HOME_DIR/skills/codeflow
  - Claude Desktop: $CLAUDE_CONFIG_DIR/claude_desktop_config.json
  - Cursor: $CURSOR_CONFIG_DIR/mcp.json
  - Antigravity: $GEMINI_DIR/antigravity-cli/skills/codeflow

  Manual run command:
  $INSTALL_PATH mcp

  Remove anytime with:
  $INSTALL_PATH uninstall
EOF

