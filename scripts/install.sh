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
CODEFLOW_VERSION="${CODEFLOW_VERSION:-v0.4.0}"
OWNED_SOURCE=false
SRC_DIR="${CODEFLOW_SRC_DIR:-}"
SKILL_DEST="$CODEX_HOME_DIR/skills/codeflow"
SKILL_TARGETS="${SKILL_TARGETS:-auto}"
if [ "$(uname -s)" = "Darwin" ]; then
  CLAUDE_CONFIG_DIR="$HOME/Library/Application Support/Claude"
else
  CLAUDE_CONFIG_DIR="$HOME/.config/Claude"
fi
CURSOR_CONFIG_DIR="$HOME/.cursor"
GEMINI_DIR="$HOME/.gemini"
GEMINI_CONFIG_DIR="$GEMINI_DIR/config"
WORKSPACE_ROOT="${CODEFLOW_WORKSPACE_ROOT:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --skills=*|--skill-targets=*)
      SKILL_TARGETS="${1#*=}"
      shift
      ;;
    --skills|--skill-targets)
      SKILL_TARGETS="$2"
      shift 2
      ;;
    --install-dir=*)
      INSTALL_DIR="${1#*=}"
      shift
      ;;
    --mcp-name=*)
      MCP_NAME="${1#*=}"
      shift
      ;;
    --help|-h)
      echo "Usage: install.sh [options]"
      echo "Options:"
      echo "  --skills=<targets>        Comma-separated skill targets: codex, claude, gemini, cursor, agents, or all (default: auto)"
      echo "  --install-dir=<path>      Directory for binaries (default: \$HOME/.local/bin)"
      echo "  --mcp-name=<name>         MCP registration name (default: codeflow)"
      exit 0
      ;;
    *)
      shift
      ;;
  esac
done

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

recorded_skill_sha256() {
  local state_path="$HOME/.codeflow/install-state.json"
  if [ -f "$state_path" ]; then
    sed -n 's/.*"skillSHA256"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$state_path" | head -1
  fi
}

skill_needs_update() {
  local src="$1"
  local dest="$2"
  if [ ! -e "$dest" ]; then
    return 0
  fi
  if [ ! -f "$dest/SKILL.md" ] || ! cmp -s "$src/SKILL.md" "$dest/SKILL.md"; then
    return 0
  fi
  if [ -d "$src/references" ]; then
    if [ ! -d "$dest/references" ]; then
      return 0
    fi
    for ref_file in "$src/references"/*; do
      [ -e "$ref_file" ] || continue
      local base="$(basename "$ref_file")"
      if [ ! -f "$dest/references/$base" ] || ! cmp -s "$ref_file" "$dest/references/$base"; then
        return 0
      fi
    done
  fi
  if [ -d "$dest/references" ]; then
    for dest_ref in "$dest/references"/*; do
      [ -e "$dest_ref" ] || continue
      local base="$(basename "$dest_ref")"
      if [ ! -f "$src/references/$base" ]; then
        return 0
      fi
    done
  fi
  return 1
}

target_enabled() {
  local target="$1"
  if [ "$SKILL_TARGETS" = "all" ]; then
    return 0
  fi
  if [ "$SKILL_TARGETS" = "auto" ]; then
    case "$target" in
      codex)
        return 0 ;;
      claude)
        [ -d "$HOME/.claude" ] || [ -d "$CLAUDE_CONFIG_DIR" ] || command -v claude >/dev/null 2>&1 ;;
      cursor)
        [ -d "$CURSOR_CONFIG_DIR" ] ;;
      gemini|antigravity)
        [ -d "$GEMINI_DIR" ] || [ -d "$GEMINI_CONFIG_DIR" ] ;;
      agents)
        [ -d ".agents" ] || [ -d "${SRC_DIR:-}/.agents" ] || [ "$IS_CHECKOUT" = true ] || [ -d ".git" ] ;;
      *)
        return 1 ;;
    esac
  else
    case ",$SKILL_TARGETS," in
      *",$target,"*) return 0 ;;
      *) return 1 ;;
    esac
  fi
}

install_skill_to_dir() {
  local target_dir="$1"
  local target_label="$2"
  if [ -d "$SKILL_SOURCE" ]; then
    mkdir -p "$(dirname "$target_dir")"
    if [ ! -e "$target_dir" ]; then
      cp -R "$SKILL_SOURCE" "$target_dir"
      info "Installed CodeFlow skill for $target_label ($target_dir)"
    elif skill_needs_update "$SKILL_SOURCE" "$target_dir"; then
      rm -rf "$target_dir"
      cp -R "$SKILL_SOURCE" "$target_dir"
      info "Updated CodeFlow skill for $target_label ($target_dir)"
    fi
  fi
}

preflight_skill_update() {
  local source="$1"
  if [ ! -e "$SKILL_DEST" ] || ! skill_needs_update "$source" "$SKILL_DEST"; then
    return 0
  fi

  local installed_sha recorded_sha
  installed_sha="$(calc_sha256 "$SKILL_DEST/SKILL.md")"
  recorded_sha="$(recorded_skill_sha256)"
  if [ -z "$installed_sha" ] || [ "$installed_sha" != "$recorded_sha" ]; then
    die "Codex skill at $SKILL_DEST was changed; refusing to overwrite it"
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

  SKILL_SOURCE="$SRC_DIR/skills/codeflow"
  preflight_skill_update "$SKILL_SOURCE"

  info "Building CodeFlow binaries"
  BUILD_DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  if [ -n "${CODEFLOW_TEST_CODEFLOW_BIN:-}" ] && [ -f "${CODEFLOW_TEST_CODEFLOW_BIN}" ]; then
    cp -f "${CODEFLOW_TEST_CODEFLOW_BIN}" "$INSTALL_PATH"
  else
    (cd "$SRC_DIR" && go build -ldflags "-X main.version=$CODEFLOW_VERSION -X main.date=$BUILD_DATE" -o "$INSTALL_PATH" ./cmd/codeflow)
  fi
  chmod 755 "$INSTALL_PATH"

  ADAPTER_BIN="$INSTALL_DIR/dart-adapter"
  DART_SRC="$SRC_DIR/adapters/dart"
  if [ -n "${CODEFLOW_TEST_DART_BIN:-}" ] && [ -f "${CODEFLOW_TEST_DART_BIN}" ]; then
    cp -f "${CODEFLOW_TEST_DART_BIN}" "$ADAPTER_BIN"
    chmod 755 "$ADAPTER_BIN"
    ADAPTER_SPEC="$ADAPTER_BIN"
  elif command -v dart >/dev/null 2>&1; then
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

if command -v node >/dev/null 2>&1; then
  NODE_BIN="$(command -v node)"
elif [ -x "/opt/homebrew/bin/node" ]; then
  NODE_BIN="/opt/homebrew/bin/node"
elif [ -x "/usr/local/bin/node" ]; then
  NODE_BIN="/usr/local/bin/node"
elif [ -x "$HOME/.local/bin/node" ]; then
  NODE_BIN="$HOME/.local/bin/node"
else
  echo "Error: Node.js runtime not found on PATH or standard Homebrew locations" >&2
  exit 1
fi

if [ -f "$TS_TARGET" ]; then
  exec "$NODE_BIN" "$TS_TARGET" "$@"
fi
echo "TypeScript adapter entrypoint not found at $TS_TARGET" >&2
exit 1
WRAPPER
    chmod 755 "$TS_BIN"
    ln -sf "$TS_BIN" "$INSTALL_DIR/codeflow_typescript_adapter" 2>/dev/null || cp -f "$TS_BIN" "$INSTALL_DIR/codeflow_typescript_adapter"
  fi

  if [ -f "$ADAPTER_BIN" ]; then
    ln -sf "$ADAPTER_BIN" "$INSTALL_DIR/codeflow_dart_adapter" 2>/dev/null || cp -f "$ADAPTER_BIN" "$INSTALL_DIR/codeflow_dart_adapter"
  fi
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

  if [ "$OS" = "darwin" ] && [ "$ARCH" = "amd64" ]; then
    die "Pre-compiled binaries for Intel Mac (darwin-amd64) are not supported. Only Apple Silicon (ARM64) is supported for macOS."
  fi

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

  SKILL_SOURCE="$TMP_DIR/skills/codeflow"
  preflight_skill_update "$SKILL_SOURCE"

  cp -f "$TMP_DIR/bin/codeflow" "$INSTALL_PATH"
  chmod 755 "$INSTALL_PATH"

  ADAPTER_BIN="$INSTALL_DIR/dart-adapter"
  if [ -f "$TMP_DIR/bin/dart-adapter" ]; then
    cp -f "$TMP_DIR/bin/dart-adapter" "$ADAPTER_BIN"
    chmod 755 "$ADAPTER_BIN"
    ln -sf "$ADAPTER_BIN" "$INSTALL_DIR/codeflow_dart_adapter" 2>/dev/null || cp -f "$ADAPTER_BIN" "$INSTALL_DIR/codeflow_dart_adapter"
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

if command -v node >/dev/null 2>&1; then
  NODE_BIN="$(command -v node)"
elif [ -x "/opt/homebrew/bin/node" ]; then
  NODE_BIN="/opt/homebrew/bin/node"
elif [ -x "/usr/local/bin/node" ]; then
  NODE_BIN="/usr/local/bin/node"
elif [ -x "$HOME/.local/bin/node" ]; then
  NODE_BIN="$HOME/.local/bin/node"
else
  echo "Error: Node.js runtime not found on PATH or standard Homebrew locations" >&2
  exit 1
fi

if [ -f "$TS_TARGET" ]; then
  exec "$NODE_BIN" "$TS_TARGET" "$@"
fi
echo "TypeScript adapter entrypoint not found at $TS_TARGET" >&2
exit 1
WRAPPER
    chmod 755 "$INSTALL_DIR/codeflow_ts_adapter"
    ln -sf "$INSTALL_DIR/codeflow_ts_adapter" "$INSTALL_DIR/codeflow_typescript_adapter" 2>/dev/null || cp -f "$INSTALL_DIR/codeflow_ts_adapter" "$INSTALL_DIR/codeflow_typescript_adapter"
  fi
fi

# A copied executable is not enough: reject stale or incompatible adapters
# before changing any agent configuration or recording installation success.
if [ -n "$ADAPTER_SPEC" ]; then
  info "Verifying installed Dart adapter protocol"
  "$INSTALL_PATH" install-check-adapter --language dart --adapter-spec "$ADAPTER_SPEC"
fi

# Collect robust runtime PATH for GUI desktop clients
collect_runtime_path() {
  local extra_paths=()
  extra_paths+=("$INSTALL_DIR")
  extra_paths+=("/opt/homebrew/bin" "/opt/homebrew/sbin" "/usr/local/bin")
  extra_paths+=("$HOME/.local/bin" "$HOME/bin")

  # Detect active Node / NVM / FNM / Volta / ASDF paths
  if [ -d "$HOME/.nvm/versions/node" ]; then
    for v in "$HOME/.nvm/versions/node"/*; do
      if [ -d "$v/bin" ]; then
        extra_paths+=("$v/bin")
      fi
    done
  fi
  if [ -d "$HOME/.local/share/fnm/current/bin" ]; then
    extra_paths+=("$HOME/.local/share/fnm/current/bin")
  fi
  if [ -d "$HOME/.asdf/shims" ]; then
    extra_paths+=("$HOME/.asdf/shims")
  fi
  if [ -d "$HOME/.volta/bin" ]; then
    extra_paths+=("$HOME/.volta/bin")
  fi

  # Detect Dart / Flutter paths
  if [ -d "$HOME/.pub-cache/bin" ]; then
    extra_paths+=("$HOME/.pub-cache/bin")
  fi
  if [ -d "$HOME/fvm/default/bin" ]; then
    extra_paths+=("$HOME/fvm/default/bin")
  fi
  if [ -d "$HOME/flutter/bin" ]; then
    extra_paths+=("$HOME/flutter/bin")
  fi

  local combined_path="$PATH"
  for p in "${extra_paths[@]}"; do
    if [ -d "$p" ]; then
      case ":$combined_path:" in
        *":$p:"*) ;;
        *) combined_path="$combined_path:$p" ;;
      esac
    fi
  done
  echo "$combined_path"
}

RUNTIME_PATH="$(collect_runtime_path)"

# Helper to merge codeflow into an MCP JSON config file safely
register_json_mcp() {
  local json_path="$1"
  local mcp_name="$2"
  local bin_path="$3"
  local runtime_path="${4:-$RUNTIME_PATH}"

  local parent_dir
  parent_dir="$(dirname "$json_path")"
  mkdir -p "$parent_dir"

  if command -v node >/dev/null 2>&1; then
    node -e '
      const fs = require("fs");
      const [filePath, name, bin, rpath] = [process.argv[1], process.argv[2], process.argv[3], process.argv[4]];
      let data = {};
      if (fs.existsSync(filePath)) {
        try { data = JSON.parse(fs.readFileSync(filePath, "utf8")); } catch (_) { data = {}; }
      }
      if (!data || typeof data !== "object") data = {};
      if (!data.mcpServers) data.mcpServers = {};
      const entry = { command: bin, args: ["mcp"] };
      if (rpath) {
        entry.env = entry.env || {};
        entry.env.PATH = rpath;
      }
      data.mcpServers[name] = entry;
      fs.writeFileSync(filePath, JSON.stringify(data, null, 2) + "\n");
    ' "$json_path" "$mcp_name" "$bin_path" "$runtime_path" 2>/dev/null && return 0 || true
  fi

  if command -v python3 >/dev/null 2>&1; then
    python3 -c '
import json, os, sys
file_path, name, bin_path = sys.argv[1], sys.argv[2], sys.argv[3]
rpath = sys.argv[4] if len(sys.argv) > 4 else ""
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
entry = {"command": bin_path, "args": ["mcp"]}
if rpath:
    entry["env"] = entry.get("env", {})
    entry["env"]["PATH"] = rpath
data["mcpServers"][name] = entry
with open(file_path, "w", encoding="utf-8") as f:
    json.dump(data, f, indent=2)
    f.write("\n")
' "$json_path" "$mcp_name" "$bin_path" "$runtime_path" 2>/dev/null && return 0 || true
  fi
}

# 1. Codex Registration
if command -v codex >/dev/null 2>&1; then
  if MCP_CONFIG="$(codex mcp get "$MCP_NAME" --json 2>/dev/null)"; then
    if ! printf '%s\n' "$MCP_CONFIG" | grep -Fq "$INSTALL_PATH"; then
      die "Codex MCP '$MCP_NAME' already belongs to another command; use CODEFLOW_MCP_NAME to choose a new name"
    fi
  fi
fi

if [ -d "$SKILL_SOURCE" ] && target_enabled codex; then
  install_skill_to_dir "$SKILL_DEST" "Codex"
fi

if command -v codex >/dev/null 2>&1; then
  if codex mcp get "$MCP_NAME" --json >/dev/null 2>&1; then
    info "Codex MCP '$MCP_NAME' is already registered"
  else
    if [ -n "$ADAPTER_SPEC" ]; then
      codex mcp add "$MCP_NAME" --env "CODEFLOW_ADAPTER_DART_BIN=$ADAPTER_SPEC" -- "$INSTALL_PATH" mcp
    else
      codex mcp add "$MCP_NAME" -- "$INSTALL_PATH" mcp
    fi
    info "Registered Codex MCP '$MCP_NAME'"
  fi
fi

# 2. Claude Desktop Registration
register_json_mcp "$CLAUDE_CONFIG_DIR/claude_desktop_config.json" "$MCP_NAME" "$INSTALL_PATH" "$RUNTIME_PATH"
info "Configured Claude Desktop MCP ($CLAUDE_CONFIG_DIR/claude_desktop_config.json)"

# 3. Claude Code CLI Registration
if command -v claude >/dev/null 2>&1; then
  if claude mcp get "$MCP_NAME" >/dev/null 2>&1; then
    info "Claude Code MCP '$MCP_NAME' is already registered"
  else
    claude mcp add -s user "$MCP_NAME" "$INSTALL_PATH" mcp >/dev/null 2>&1 || true
    info "Registered Claude Code MCP '$MCP_NAME'"
  fi
fi

if target_enabled claude; then
  install_skill_to_dir "$HOME/.claude/skills/codeflow" "Claude Code"
fi

# 4. Cursor IDE Registration
register_json_mcp "$CURSOR_CONFIG_DIR/mcp.json" "$MCP_NAME" "$INSTALL_PATH" "$RUNTIME_PATH"
if target_enabled cursor; then
  install_skill_to_dir "$CURSOR_CONFIG_DIR/skills/codeflow" "Cursor"
fi
info "Configured Cursor MCP ($CURSOR_CONFIG_DIR/mcp.json)"

# 5. Antigravity / Gemini CLI Registration
register_json_mcp "$GEMINI_CONFIG_DIR/mcp_config.json" "$MCP_NAME" "$INSTALL_PATH" "$RUNTIME_PATH"
info "Configured Antigravity MCP ($GEMINI_CONFIG_DIR/mcp_config.json)"

if target_enabled gemini || target_enabled antigravity; then
  install_skill_to_dir "$GEMINI_CONFIG_DIR/skills/codeflow" "Antigravity"
  if [ -d "$GEMINI_DIR/antigravity-cli" ] || [ -d "$GEMINI_DIR" ]; then
    install_skill_to_dir "$GEMINI_DIR/antigravity-cli/skills/codeflow" "Antigravity CLI"
  fi
fi

# 6. Shared Workspace Skills (.agents/skills/codeflow)
if [ -z "$WORKSPACE_ROOT" ]; then
  if [ "$IS_CHECKOUT" = true ] && [ -n "$SRC_DIR" ]; then
    WORKSPACE_ROOT="$SRC_DIR"
  elif [ -d ".git" ] || [ -d ".agents" ]; then
    WORKSPACE_ROOT="$(pwd)"
  fi
fi

if target_enabled agents && [ -n "$WORKSPACE_ROOT" ]; then
  install_skill_to_dir "$WORKSPACE_ROOT/.agents/skills/codeflow" "Shared Workspace (.agents)"
fi

# Antigravity CLI MCP tool schemas
AGY_MCP_DIR="$GEMINI_DIR/antigravity-cli/mcp/$MCP_NAME"
mkdir -p "$AGY_MCP_DIR"
rm -f "$AGY_MCP_DIR"/*.json

cat <<'EOF' > "$AGY_MCP_DIR/harvest_flows.json"
{
  "name": "harvest_flows",
  "description": "Find candidate entry points for a natural-language flow request. Returns ranked candidate list with candidateId, entrySymbolPath, and intentSignals.",
  "parameters": {
    "type": "object",
    "properties": {
      "target": {
        "type": "string",
        "description": "Target repository path or subdirectory (defaults to working directory)"
      },
      "query": {
        "type": "string",
        "description": "Optional case-insensitive substring filter across entrySymbolPath, intentSignals, markerKind, triggerClass"
      }
    }
  }
}
EOF

cat <<'EOF' > "$AGY_MCP_DIR/analyze_flow.json"
{
  "name": "analyze_flow",
  "description": "Slice and publish one exact entry point. Returns a persisted FlowSpec containing flowId; when the user requested a visual result, pass that exact flowId to open_review.",
  "parameters": {
    "type": "object",
    "required": ["entrySymbolPath"],
    "properties": {
      "entrySymbolPath": {
        "type": "string",
        "description": "Exact entry symbol path from harvest_flows (e.g. app/page.tsx#HomePage.submit)"
      },
      "target": {
        "type": "string",
        "description": "Target repository path or subdirectory (defaults to working directory)"
      }
    }
  }
}
EOF

cat <<'EOF' > "$AGY_MCP_DIR/get_flow_payload.json"
{
  "name": "get_flow_payload",
  "description": "Retrieve FlowSpec JSON by flowId or entrySymbolPath. Set compact: true to receive high-density ~500-token macro gateway summary.",
  "parameters": {
    "type": "object",
    "properties": {
      "flowId": {
        "type": "string",
        "description": "Target flow ID returned by analyze_flow or publish_core_flow"
      },
      "entrySymbolPath": {
        "type": "string",
        "description": "Target entry symbol path"
      },
      "compact": {
        "type": "boolean",
        "description": "When true, returns ~500-token CompactFlowPayload with 4-7 macro gateways and radar summary"
      },
      "format": {
        "type": "string",
        "enum": ["compact", "full"],
        "description": "Payload format ('compact' or 'full')"
      },
      "target": {
        "type": "string",
        "description": "Target repository path or subdirectory (defaults to working directory)"
      }
    }
  }
}
EOF

cat <<'EOF' > "$AGY_MCP_DIR/open_review.json"
{
  "name": "open_review",
  "description": "Return the authenticated URL for a saved viewId or a persisted flowId. A saved view is restored without analysis.",
  "parameters": {
    "type": "object",
    "properties": {
      "flowId": {
        "type": "string",
        "description": "The exact flowId returned by analyze_flow or publish_core_flow"
      },
      "viewId": {
        "type": "string",
        "description": "Saved FlowView result ID to restore exact result without re-analysis"
      },
      "target": {
        "type": "string",
        "description": "Target repository path or subdirectory (defaults to working directory)"
      }
    }
  }
}
EOF

cat <<'EOF' > "$AGY_MCP_DIR/report_unknowns.json"
{
  "name": "report_unknowns",
  "description": "List unresolved gaps, missing types, and dynamic dispatch cutoffs in the workspace or for a specific flow.",
  "parameters": {
    "type": "object",
    "properties": {
      "flowId": {
        "type": "string",
        "description": "Optional flow ID to filter unknowns for a specific flow"
      },
      "target": {
        "type": "string",
        "description": "Target repository path or subdirectory (defaults to working directory)"
      }
    }
  }
}
EOF

cat <<'EOF' > "$AGY_MCP_DIR/publish_core_flow.json"
{
  "name": "publish_core_flow",
  "description": "Publish a verified architecture-layer core flow from an agent-authored intermediate artifact. Verifies every 6-field anchor against current worktree bytes; on mismatch returns a correctable error without persisting.",
  "parameters": {
    "type": "object",
    "required": ["artifact"],
    "properties": {
      "artifact": {
        "type": "object",
        "description": "Core flow artifact conforming to core-artifact schema"
      },
      "target": {
        "type": "string",
        "description": "Target repository path or subdirectory (defaults to working directory)"
      },
      "token": {
        "type": "string",
        "description": "Auth token when required"
      }
    }
  }
}
EOF

cat <<'EOF' > "$AGY_MCP_DIR/approve_step.json"
{
  "name": "approve_step",
  "description": "Approve a step name and business rules (in-place approval recorded to event log).",
  "parameters": {
    "type": "object",
    "required": ["flowId", "symbolPath", "name"],
    "properties": {
      "flowId": {
        "type": "string",
        "description": "Target flow ID"
      },
      "symbolPath": {
        "type": "string",
        "description": "Enclosing symbol path of the step being approved"
      },
      "name": {
        "type": "string",
        "description": "Approved business step name"
      },
      "rules": {
        "type": "array",
        "items": {"type": "string"},
        "description": "Approved business rules or rationale"
      },
      "target": {
        "type": "string",
        "description": "Target repository path or subdirectory (defaults to working directory)"
      },
      "token": {
        "type": "string",
        "description": "Auth token when required"
      }
    }
  }
}
EOF

cat <<'EOF' > "$AGY_MCP_DIR/submit_flow_draft.json"
{
  "name": "submit_flow_draft",
  "description": "Submit structured session journey draft with verified anchors.",
  "parameters": {
    "type": "object",
    "required": ["artifact"],
    "properties": {
      "artifact": {
        "type": "object",
        "description": "Session journey draft artifact conforming to session-artifact schema"
      },
      "target": {
        "type": "string",
        "description": "Target repository path or subdirectory (defaults to working directory)"
      },
      "token": {
        "type": "string",
        "description": "Auth token when required"
      }
    }
  }
}
EOF

cat <<'EOF' > "$AGY_MCP_DIR/instructions.md"
# CodeFlow MCP Server

CodeFlow provides business-flow-first code comprehension, FlowSequence Storyboard generation, and interactive FlowView visual review across polyglot codebases.

## Official 8 Core Tools:
1. `harvest_flows`: Discover candidate entry points for a natural language flow query.
2. `analyze_flow`: Slice AST execution paths and publish FlowSpec by entrySymbolPath.
3. `get_flow_payload`: Retrieve curated flow payload (~500-token macro gateway summary with `compact: true`).
4. `open_review`: Generate authenticated FlowView URL for interactive visual inspection.
5. `report_unknowns`: Inspect unresolved boundaries, missing types, and dynamic dispatch cutoffs.
6. `publish_core_flow`: Publish verified architecture-layer core flow with 6-field anchors.
7. `approve_step`: Record human or agent step approval and business rules.
8. `submit_flow_draft`: Submit structured session journey draft.
EOF
info "Installed Antigravity MCP tool schemas ($AGY_MCP_DIR)"

SKILL_SHA256=""
if [ -f "$SKILL_DEST/SKILL.md" ]; then
  SKILL_SHA256="$(calc_sha256 "$SKILL_DEST/SKILL.md")"
elif [ -f "$SKILL_SOURCE/SKILL.md" ]; then
  SKILL_SHA256="$(calc_sha256 "$SKILL_SOURCE/SKILL.md")"
fi

"$INSTALL_PATH" install-record \
  --binary "$INSTALL_PATH" \
  --source-root "${SRC_DIR:-${WORKSPACE_ROOT:-}}" \
  --owned-source="$OWNED_SOURCE" \
  --adapter-spec "$ADAPTER_SPEC" \
  --skill-path "$SKILL_DEST" \
  --skill-sha256 "$SKILL_SHA256" \
  --mcp-name "$MCP_NAME"

cat <<EOF

✓ CodeFlow installation complete!
  Binary: $INSTALL_PATH

  Auto-configured for detected agents:
  - Codex: $CODEX_HOME_DIR/skills/codeflow
  - Claude Desktop: $CLAUDE_CONFIG_DIR/claude_desktop_config.json
  - Claude Code: $HOME/.claude/skills/codeflow
  - Cursor: $CURSOR_CONFIG_DIR/mcp.json
  - Antigravity / Gemini: $GEMINI_CONFIG_DIR/mcp_config.json ($GEMINI_CONFIG_DIR/skills/codeflow)
  - Shared Workspace: .agents/skills/codeflow

  Manual run command:
  $INSTALL_PATH mcp

  Remove anytime with:
  $INSTALL_PATH uninstall
EOF

case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *)
    cat << EOF

⚠️  Note: '$INSTALL_DIR' is not in your current PATH.
   To run 'codeflow' directly from your terminal, add this to your shell profile (~/.zshrc or ~/.bashrc):
   export PATH="$INSTALL_DIR:\$PATH"
EOF
    ;;
esac
