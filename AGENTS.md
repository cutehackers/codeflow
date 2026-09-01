# Purpose

Codeflow is a tool that helps developers understand code in large codebases. Given a user prompt, it identifies the relevant business flows and presents them through an interactive UX that shows how each flow executes end-to-end — not just what the flow is, but how it runs through the code.

Core flow (핵심 흐름) — the flow the user requested: the complete set of architecture-layer traversals that starts at the initial event in the entry layer (UI action, system event, route, or equivalent layer-defined trigger) and follows every handling step through each architecture layer until processing completes. When the user says "requested flow", "core flow", or "the implementation flow I want to understand", treat it as this definition. Non-core statements that do not advance the layer traversal do not belong in the core flow.

For a comprehensive explanation of CodeFlow's **Core Capabilities** and **Product Surfaces**, refer to [`docs/PROJECT.md`](docs/PROJECT.md) (or [`docs/PROJECT-ko.md`](docs/PROJECT-ko.md)).

# Workspace Rules

- Do not write the absolute home-directory path (for example, `/Users/<username>`) directly in documentation, code, configuration, or examples.
- When a home-directory path is needed, use `HOME` followed by the relative path instead. For example: `HOME/workspace/codeflow`.
- **No Git Commits/Tags/Pushes**: Antigravity is strictly prohibited from running `git commit`, `git tag`, or `git push`. All version control commits and history modifications must be performed directly by the user.
- **Version Management & Release Synchronization**: When preparing a release or bumping a version (`vX.Y.Z`), the agent MUST follow `docs/VERSIONING.md` and synchronize all version locations simultaneously (`README.md`, `scripts/install.sh`, `internal/pin/compatibility.json`, adapter package manifests, and `CHANGELOG.md`) before suggesting release tags.

