# Purpose

Codeflow is a tool that helps developers understand code in large codebases. Given a user prompt, it identifies the relevant business flows and presents them through an interactive UX that shows how each flow executes end-to-end — not just what the flow is, but how it runs through the code.

Core flow (핵심 흐름) — the flow the user requested: the complete set of architecture-layer traversals that starts at the initial event in the entry layer (UI action, system event, route, or equivalent layer-defined trigger) and follows every handling step through each architecture layer until processing completes. When the user says "requested flow", "core flow", or "the implementation flow I want to understand", treat it as this definition. Non-core statements that do not advance the layer traversal do not belong in the core flow.

For a comprehensive explanation of CodeFlow's **Core Capabilities** and **Product Surfaces**, refer to [`docs/PROJECT.md`](docs/PROJECT.md) (or [`docs/PROJECT-ko.md`](docs/PROJECT-ko.md)).

## Naming Rules

- Use descriptive domain names. Do not introduce internal acronyms, ticket identifiers, or vertical-slice labels such as `RFLSC`, `LPCA`, or `VS01` in new package, directory, file, type, function, variable, or test names. `make check-naming` enforces this for changed code paths and declarations.
- Use the full official terms defined in [`docs/design/glossary.md`](docs/design/glossary.md). Do not add an acronym in parentheses after them.
- Keep `Requested Flow` as the domain term for the execution flow selected by a user's intent. It is not part of the `Live Semantic Compiler` product name.
- Preserve existing schema IDs, protocol values, storage keys, public API names, and compatibility fixtures when renaming would break consumers. Rename legacy internal names only when the surrounding code is already being changed.
- Design-plan labels remain valid only as external metadata, such as registry IDs, acceptance IDs, schema IDs, protocol values, and compatibility mappings. They must not be copied into implementation identifiers.

## Code Comprehension First & Anti-Telemetry Guard

│ "NEVER leak internal compiler, benchmark, or engine telemetry (such as epochs, lag, or internal settlement flags) into the
│ primary view; prioritize developer code comprehension by presenting only business flow traversals, architecture layers, and
│ verifiable source code."

## FlowView Code Comprehension Premises (Mandatory)

The current product is one FlowView for new and existing features, explicit reanalysis, optional before/after comparison, and evidence-backed direct relation exploration. The canonical contract is [FlowView code comprehension](docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md).

1. Code comprehension requires navigation and context restoration. Show relevant source and nearby context directly.
2. Understanding current execution and understanding changes are distinct tasks. Current flow is the default. Comparison requires an explicitly selected baseline and is optional for new features.
3. More visual structure does not guarantee comprehension. Prioritize focus, source evidence and controlled disclosure.
4. Preserve reading position, selection and baseline during updates. Only an explicit analysis/comparison request may replace the view after successful validation.

Standalone Live View, watcher-triggered analysis, near-live latency targets and the previous Live storage redesign are not prerequisites for this direction. Partial analysis must distinguish verified code, missing connections and analysis failures without declaring a feature complete or unimplemented without evidence. Existing public protocols, persisted data, source read-only behavior and security/proof invariants remain protected.

## Workspace Rules

- Do not write the absolute home-directory path (for example, `/Users/<username>`) directly in documentation, code, configuration, or examples.
- When a home-directory path is needed, use `HOME` followed by the relative path instead. For example: `HOME/workspace/codeflow`.
- **No Git Commits/Tags/Pushes**: Antigravity is strictly prohibited from running `git commit`, `git tag`, or `git push`. All version control commits and history modifications must be performed directly by the user.
- **Go Coding Standards & Naming Conventions**: All Go code written, modified, or reviewed by agents MUST adhere to [`docs/guides/CODING_STANDARDS.md`](docs/guides/CODING_STANDARDS.md). Rigorously follow Go naming conventions (Google Go Style: strict initialism casing `FlowID`/`URL`/`MCP`, scope-proportional variable lengths, stutter-free package APIs, getters without `Get` prefix, `Err*` sentinel errors, and the naming rules above). Also adhere to package structure, `%w` error wrapping, anti-telemetry preservation, single-gate secret redaction in `internal/secret`, context timeouts, and table-driven testing. Always verify changes with `make fmt`, `make vet`, and `make test`.
- **Version Management & Release Synchronization**: When preparing a release or bumping a version (`vX.Y.Z`), the agent MUST follow `docs/VERSIONING.md` and synchronize all version locations simultaneously (`README.md`, `scripts/install.sh`, `internal/pin/compatibility.json`, adapter package manifests, and `CHANGELOG.md`) before suggesting release tags.
