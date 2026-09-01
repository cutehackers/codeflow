# Changelog

All notable changes to CodeFlow will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

---

## [v0.3.6] - 2026-09-01

### Added
- **TypeScript/JavaScript Frontend Analysis Engine**:
  - Recursive AST scanner (`scanFunctionBody`) in `adapters/typescript/lib/scanner.js` capturing nested event handlers (`handleSubmit`, `onClick`), callbacks, and custom hooks (`useAuth`, `useMutation`) within React functional components.
  - Extended frontend candidate harvester in `adapters/typescript/lib/harvest.js` supporting 6 trigger classes (`user_action`, `state_transition`, `system_event`, `use_case_invocation`, Next.js App Router HTTP handlers, and Server Actions).
  - Multi-dot member call chaining (`api.v1.auth.login()`) and destructured hook cross-file slicing (`const { login } = useAuth()`) in `adapters/typescript/lib/slice.js`.
- **Dynamic Architecture & Framework Topology Inference**:
  - Automated project topology and dependency detector in `internal/detect/topology.go` detecting Next.js App Router, Feature-Sliced Design (FSD), Standard React SPA, and Clean Architecture.
  - Framework-tailored `codeflow.layers.yaml` generator in `internal/initcmd/initcmd.go` preserving CodeFlow's 7 Canonical Lanes (`presentation`, `controller`, `usecase`, `domain`, `data`, `infra`, `external`) with 0 `layer_order_violation` errors.
  - Expanded frontend layer aliases and monotonic order validation in `internal/fusion/layers.go`.
- **CLI & Diagnostics Improvements**:
  - Formatted `codeflow version` output adhering to `codeflow v*.*.*, date: {human readable date}` in `cmd/codeflow/main.go`.
  - Local-first Language Server diagnostics (`node_modules/.bin/typescript-language-server` fallback to global/npx) and Node.js v18+ runtime verification in `codeflow doctor`.
- **Comprehensive E2E Testing**:
  - 4 multi-framework test fixtures (`nextjs-app-fixture`, `fsd-fixture`, `react-spa-fixture`, `clean-arch-fixture`).
  - 4-tier E2E test runner (`test/e2e/runner.js`) with 79 automated assertions.

### Fixed
- Fixed scanner skipping nested arrow function bodies inside top-level React functional components.
- Fixed classification precedence to prioritize lifecycle hooks (`useEffect`, `componentDidMount`) as `system_event` before custom hooks.
- Filtered out non-entry utility hooks (`useMemo`, `useRef`, `useCallback`, `useId`, etc.) to prevent candidate inflation.
- Fixed file-level `'use server'` Server Action detection for functions without `Action` suffix.
- Fixed `isBoundaryTarget` chained receiver evaluation for external API/client targets.

---

## [v0.3.5] - 2026-08-31

### Added
- Comprehensive product documentation covering CodeFlow's Core Capabilities and Product Surfaces (`docs/PROJECT.md` and `docs/PROJECT-ko.md`).

---

## [v0.3.4] - 2026-08-31

### Added
- Embedded JSON Schema contracts (`schemas/*.schema.json`) into Go binary using `embed.FS`.
- Lazy MCP bootstrap with workspace resolution across 4 major AI agents (Codex, Claude Desktop, Cursor, Antigravity).
- Clean uninstall command (`codeflow uninstall`) removing MCP configurations, skills, and owned files.
- Resilient one-shot installation lifecycle tests.

---

## [v0.3.3] - 2026-08-30

### Added
- Polyglot multi-language architecture (Go CORE + Dart + TypeScript adapters over NDJSON stdio protocol).
- Adapter version pinning and compatibility verification table (`internal/pin/compatibility.json`).
- Standardized versioning and release synchronization protocol (`docs/VERSIONING.md`).

---

## [v0.3.2] - 2026-08-29

### Added
- One-shot remote installer (`scripts/install.sh`) supporting curl piping and local checkout builds with sha256 checksum verification.
- Automated GitHub Actions release workflow for macOS (arm64) and Linux (amd64).

---

## [v0.3.0] - 2026-08-28

### Added
- Core Flow v3 engine featuring the 7 Canonical Lanes and FlowView interactive web UI.
- Atomic flow publishing with 6-field cryptographic code anchors (`byteRange`, `fileHash`, `spanHash`, `canonicalAstFingerprint`).
