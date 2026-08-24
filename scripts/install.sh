#!/usr/bin/env bash
set -euo pipefail

# CodeFlow one-shot installer — local build (brew 배포 전)
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/OWNER/codeflow/main/scripts/install.sh | bash
#   # or local clone:
#   bash scripts/install.sh
#   # optional: INSTALL_DIR=$HOME/.local/bin bash scripts/install.sh

REPO_URL="${CODEFLOW_REPO_URL:-https://github.com/OWNER/codeflow.git}"
SRC_DIR="${CODEFLOW_SRC_DIR:-$HOME/.codeflow-src}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
BRANCH="${CODEFLOW_BRANCH:-main}"

info() { echo "› $*"; }
warn() { echo "⚠ $*" >&2; }
die()  { echo "✗ $*" >&2; exit 1; }

# 1. prerequisite checks
if ! command -v go >/dev/null 2>&1; then
  die "Go 1.26+ not found — https://go.dev/dl 에서 설치 후 재실행"
fi
GO_VER=$(go version | grep -oE 'go1\.[0-9]+' | cut -c3-)
# simple check: require 1.26+
if ! command -v dart >/dev/null 2>&1; then
  warn "Dart SDK not found — harvest/slice는 dart 필요, 없으면 codeflow flows/publish 실패"
else
  info "Dart $(dart --version 2>&1 | head -1) 확인"
fi

# 2. fetch source
if [ -d "$SRC_DIR/.git" ]; then
  info "Updating existing source at $SRC_DIR"
  git -C "$SRC_DIR" fetch --depth 1 origin "$BRANCH" 2>/dev/null || true
  git -C "$SRC_DIR" checkout "$BRANCH" 2>/dev/null || true
  git -C "$SRC_DIR" pull --ff-only 2>/dev/null || true
else
  if [ -d "$SRC_DIR" ]; then
    warn "$SRC_DIR exists but not a git repo — removing"
    rm -rf "$SRC_DIR"
  fi
  # if running inside a codeflow checkout, reuse it
  if [ -f "./go.mod" ] && grep -q "^module codeflow" ./go.mod 2>/dev/null; then
    info "Using current checkout as source"
    SRC_DIR="$PWD"
  else
    info "Cloning $REPO_URL → $SRC_DIR"
    git clone --depth 1 --branch "$BRANCH" "$REPO_URL" "$SRC_DIR" || die "git clone 실패 — CODEFLOW_REPO_URL 확인"
  fi
fi

# 3. build
info "Building bin/codeflow"
make -C "$SRC_DIR" build 2>&1 | sed 's/^/  /'
[ -x "$SRC_DIR/bin/codeflow" ] || die "빌드 실패 — $SRC_DIR/bin/codeflow 없음"

# 4. install binary
mkdir -p "$INSTALL_DIR"
if [ -w "$INSTALL_DIR" ]; then
  cp -f "$SRC_DIR/bin/codeflow" "$INSTALL_DIR/codeflow"
else
  info "Requesting sudo for $INSTALL_DIR"
  sudo cp -f "$SRC_DIR/bin/codeflow" "$INSTALL_DIR/codeflow"
fi
info "Installed to $INSTALL_DIR/codeflow"

# 5. PATH hint
if ! echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
  warn "$INSTALL_DIR 가 PATH에 없음 — 아래를 shell rc에 추가:"
  echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
fi

# 6. adapter env hint
DART_ADAPTER_DIR="$SRC_DIR/adapters/dart"
if [ -d "$DART_ADAPTER_DIR" ]; then
  info "Dart adapter at $DART_ADAPTER_DIR"
  echo "  export CODEFLOW_ADAPTER_DART_BIN=\"dartrun:$DART_ADAPTER_DIR\""
  echo "  (위 값을 ~/.zshrc 또는 ~/.bashrc에 추가하면 매번 지정 불필요)"
fi

# 7. verify
info "Verifying"
"$INSTALL_DIR/codeflow" version || true
if [ -n "${CODEFLOW_ADAPTER_DART_BIN:-}" ]; then
  CODEFLOW_ADAPTER_DART_BIN="$CODEFLOW_ADAPTER_DART_BIN" "$INSTALL_DIR/codeflow" doctor . 2>&1 | sed 's/^/  /' || true
else
  CODEFLOW_ADAPTER_DART_BIN="dartrun:$DART_ADAPTER_DIR" "$INSTALL_DIR/codeflow" doctor . 2>&1 | sed 's/^/  /' || true
fi

cat <<EOF

✓ 설치 완료 — 다음 명령으로 확인:
  codeflow version
  codeflow flows ./testdata/example_app   # (CODEFLOW_ADAPTER_DART_BIN 설정 후)

  FlowView: codeflow view <repo>  → http://127.0.0.1:4567/?token=...
EOF
