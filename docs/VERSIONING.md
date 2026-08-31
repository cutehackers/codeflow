# CodeFlow Versioning & Release Protocol

This document defines the normative versioning rules and release checklist for CodeFlow. All AI agents and maintainers MUST adhere to this protocol to ensure consistent releases without version discrepancies.

---

## 1. Semantic Versioning Scheme

CodeFlow follows [Semantic Versioning 2.0.0](https://semver.org/) with a `v` prefix (`vMAJOR.MINOR.PATCH`):

- **MAJOR (`v1.0.0`)**: Incompatible wire protocol (`v=2`), breaking schema changes (`schemas/`), or major architectural shifts.
- **MINOR (`v0.4.0`)**: New features, new language adapters (e.g., Kotlin, Swift, Python), or core engine enhancements.
- **PATCH (`v0.3.4`)**: Bug fixes, parser hardening, edge case resilience, and documentation updates.

---

## 2. Synchronized Version Checklist (The 4-File Rule)

Whenever preparing a release or bumping the version to `vX.Y.Z`, **all files in the checklist below MUST be updated in the same changeset**:

| File | Exact Location | Format / Example |
|---|---|---|
| [`README.md`](../README.md) | Line 1 Header | `# CodeFlow (vX.Y.Z)` |
| [`scripts/install.sh`](../scripts/install.sh) | `CODEFLOW_VERSION` default variable | `CODEFLOW_VERSION="${CODEFLOW_VERSION:-vX.Y.Z}"` |
| [`internal/pin/compatibility.json`](../internal/pin/compatibility.json) | Adapter compatibility table | `"pinned": "X.Y.Z"` (if adapter changed) |
| `adapters/<lang>/package.json` or `pubspec.yaml` | Adapter package version | `"version": "X.Y.Z"` (if adapter changed) |

---

## 3. Pre-Release Verification Protocol

Before proposing or tagging a release:

1. **Run Full Multi-Language Test Suite**:
   ```sh
   go test -count=1 ./...
   (cd adapters/dart && dart test)
   node adapters/typescript/test/index.test.js
   ```
2. **Verify Workspace Cleanliness**:
   - Ensure transient agent session directories (`.agents/auditor_*`, `PROJECT.md`, `TEST_INFRA.md`, etc.) are removed.
   - Run `git status` to verify only intended production and test assets are modified.
3. **Verify Path Compliance**:
   - Ensure **zero** hardcoded absolute home-directory paths (`/Users/<username>`) exist in any documentation or code. Use `HOME/...` or relative paths.

---

## 4. Git Release Standard Format

Per workspace rules, AI agents do not execute `git commit`, `git tag`, or `git push` directly. Instead, the agent provides the exact pre-formatted commands:

```sh
# 1. Stage all synchronized files
git add .

# 2. Create standard release commit
git commit -m "release: vX.Y.Z - <short summary of changes>"

# 3. Create annotated tag
git tag -a vX.Y.Z -m "Release vX.Y.Z: <detailed summary of features and fixes>"

# 4. Push commit and tag to remote
git push origin <branch>
git push origin vX.Y.Z
```
