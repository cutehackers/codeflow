# CodeFlow — Project Overview

> **Understand Large Codebases Through End-to-End Business Flow Extraction & Storyboard Visualization**

CodeFlow is an interactive code analysis engine and developer code comprehension platform for developers and AI coding agents. Given a repository and a request, CodeFlow identifies a Requested Flow, preserves source-backed execution steps and relations, and presents their code through FlowView. A short reading path can be curated from verified steps while the complete detail remains available. The number of gateways depends on the evidence; an uncurated analysis reports its summary limits.

---

## 1. Core Capabilities

Core Capabilities describe the domain engine, analytical methodologies, and correctness guarantees provided by CodeFlow.

### 1.1 End-to-End Core Flow Extraction
* **Cross-Layer Execution Traversal**: Traces the complete execution path starting from an entry trigger (UI click, HTTP endpoint, route, or system event) through controllers, use cases, domain entities, repositories, down to infrastructure and external APIs.
* **Requested Flow Selection**: Selects the entry from source evidence and retains the statements needed to read its verified execution. Unproven invocation bodies are identified separately; summarization does not delete source steps.

### 1.2 Zero-Hallucination Code Grounding (Fact Anchors)
* **Verifiable Code Anchors**: Every single step is tied to real code facts: exact repo-relative file paths, byte ranges, line numbers, file hashes, span hashes, and canonical AST fingerprints.
* **Honest Gap Surfacing**: When dynamic dispatch, missing type information, or external boundaries interrupt static resolution, CodeFlow explicitly flags them as `unresolved_dynamic`, `unknowns[]`, or `boundary` gateways instead of inferring plausible but unverified steps.

### 1.3 Storyboard Business Gateways & Asymmetric 1:N Timeline Model
* **FlowSequence Storyboard Scenes**: Organizes code execution into evidence-dependent gateways (`entry`, `decision`, `process`, `effect`, `result`, `boundary`). Four to seven is a review guideline, not a limit or proof of summary quality.
* **Asymmetric 1:N Execution Timeline Containment**: Connects high-level business intent (`FlowSequenceFrame`) with microscopic execution steps (`SemanticStep`), preserving exact line-level traceability inside expandable accordions.
* **Flexible Architectural Context**: Rather than forcing every codebase into an identical 7-layer hierarchy, architecture classifications serve as secondary context labels attached to scenes.

### 1.4 Provenance & Freshness Lifecycle Tracking
* **Freshness Guarantees**: Tracks whether anchors are `fresh`, `stale` (source modified after generation), or `orphaned` (symbol deleted or relocated).
* **Authority Hierarchy**: Enforces confidence levels across step approvals (`approved` > `session` > `derived` > `unknown`).

### 1.5 Business Intent Signal Mining & Candidate Scoring
* **Candidate Signals**: Extracts entry-point hints such as `derivedName`, `docLine`, and `triggerClass`. The MCP `harvest_flows` query filters candidates by literal text; source inspection establishes which candidate matches the request.

### 1.6 Track A "Layer Authority" Separation
* **Structural AST vs. Architectural Mapping**: Language adapters extract purely structural AST facts (`guard`, `mutation`, `call`, `effect`, `branch`), leaving high-level layer mapping and flow synthesis to the Go Core curation engine and AI agent.

### 1.7 FlowView Code Comprehension Premises
* **Navigation & Context Restoration**: Directly presents relevant source code and surrounding context without navigational disorientation.
* **Explicit Re-analysis & Pinned Comparison**: Current flow is the default; baseline comparison is explicitly chosen by the user. Background file edits or watcher events never automatically swap the screen or move reading positions.
* **Controlled Disclosure & Focus**: Verified code reading paths can foreground selected steps while detailed execution steps and direct relations remain available on demand. The default analysis reports when request-specific gateway selection lacks evidence.
* **Anti-Telemetry Guard**: Internal engine telemetry (compiler epochs, lag, settlement flags, index statistics) is strictly forbidden from leaking into primary views. Only verifiable business flow traversals, architecture layers, and source code are presented.

---

## 2. Product Surfaces & Interfaces

Product Surfaces are the concrete software components, user touchpoints, protocols, and tools shipped with CodeFlow.

### 2.1 FlowView Interactive Web UI
* **FlowView Workbench**: Svelte 5 frontend with gateway navigation, exact source highlights, expandable execution details, and direct relation exploration. It does not promise a fixed gateway count or a fixed three-column layout.
* **Explicit Re-analysis & Baseline Comparison**: Single-shot re-analysis upon explicit user command; side-by-side comparison against a pinned historical baseline without automatic screen refreshes.
* **Evidence-Backed Direct Relation Radar**: Explores verified direct caller/callee relationships, state mutations, and related tests for the selected scene without speculative global impact inference.
* **Secure Token-Authenticated Loopback**: Embedded HTTP server bound strictly to `127.0.0.1` and protected by per-session cryptographic auth tokens (`?token=...`).

### 2.2 Multi-Agent Model Context Protocol (MCP) Server
* **Stdio JSON-RPC Interface**: Built-in MCP server providing out-of-the-box auto-configuration for 4 major AI agent ecosystems: **Codex, Claude Desktop, Cursor IDE, and Antigravity / Gemini CLI**.
* **Nine MCP Tools**:
  * `publish_core_flow`: Atomically verifies anchors and publishes architecture-layer core flows.
  * `harvest_flows`: Discovers entry candidates. Its optional query is a literal substring filter, not semantic matching.
  * `get_flow_payload`: Retrieves a FlowSpec or an exact stored FlowView, with an optional compact projection that retains step and relation references.
  * `analyze_flow`: On-demand slice, fuse, and publish for arbitrary symbol entry points.
  * `submit_flow_draft`: Submits structured session journey drafts with verified anchors.
  * `approve_step`: Human/agent in-place approval for step descriptions and business rules.
  * `report_unknowns`: Inspects unresolved gaps, missing types, and dynamic call boundaries.
  * `curate_code_reading_path`: Verifies a request-specific selection against source, execution relations, and CodeView before saving a new view.
  * `open_review`: Lazily launches FlowView and provides an authenticated review URL.
* **Dynamic Target Routing**: Analyzes arbitrary target directories or monorepo sub-packages without polluting host working directories.

### 2.3 Multi-Language Polyglot Engine & Process Pools
* **Isolated Subprocess Protocol (`stdio` NDJSON v1)**: Decoupled language runtimes communicating with the Go Core via streaming line-delimited JSON.
* **Language Adapters**:
  * **Dart / Flutter Adapter**: Deep static analysis via the Dart Analyzer SDK.
  * **TypeScript / JavaScript Adapter**: Parser-backed source analysis for supported JavaScript and TypeScript syntax.
  * **Go Adapter**: `go/ast` source analysis for supported statements and direct calls.
* **Process Pool Management (`AdapterRegistry`)**: Maintains reusable, isolated adapter subprocess worker pools per repository and language.

### 2.4 Declarative Architecture Configuration (`codeflow.layers.yaml`)
* **Layer Rules & Path Matching**: Declarative YAML configuration defining directory glob patterns (`pathPatterns`) and project-specific nomenclature (`aliases`).
* **Strict Validation Controls**: Configurable validation strictness (`strictOrder`, `allowUnknownLayer`).

### 2.5 Native CLI Toolchain
* `codeflow init [path]`: Prepares repository and sets up `.codeflow/workspace.json`.
* `codeflow flows [path]`: Lists harvested candidate flows sorted by score.
* `codeflow publish [path]`: Executes harvest, slice, fuse, and atomic generation publish.
* `codeflow show <id|entry>`: Displays flow steps and rules in JSON or human-readable format.
* `codeflow view` / `codeflow serve`: Launches the FlowView interactive web UI.
* `codeflow mcp`: Starts the MCP stdio JSON-RPC server.
* `codeflow doctor [path]`: Runs environment, toolchain, adapter pin, and schema integrity diagnostics.
* `codeflow uninstall`: Cleanly removes CodeFlow MCP registrations, agent skills, and binaries.

### 2.6 Zero-Friction One-Shot Installer
* **Single-Line Remote Setup**: Automatically detects OS/architecture, downloads pre-built binaries, installs adapters, and registers MCP servers and agent skills without modifying shell rc files:
  ```sh
  curl -fsSL https://raw.githubusercontent.com/cutehackers/codeflow/main/scripts/install.sh | bash
  ```

### 2.7 Centralized Single-Gate Secret Redaction
* **Automated Sanitization**: Centralized regex filter that strips API keys, tokens, passwords, and private credentials from code spans, descriptions, and FlowView payloads prior to storage or presentation.

---

## 3. Architectural Topology

```mermaid
graph TD
    Client["AI Agent / User / CLI"] -->|MCP / CLI| Core["CodeFlow Go Core"]
    
    subgraph Modules ["5-Module Internal Architecture (internal/)"]
        direction TB
        M1["analyzer (detect, protocol, workspace, doctor)"]
        M2["collector (harvest, slicing, fusion, secret, storage)"]
        M3["curator (flow sequence, macro clumping, significance ranking)"]
        M4["presenter (flowview UI, workbench server)"]
        M5["agentgateway (mcp server, payload serializer)"]
        
        M1 --> M2
        M2 --> M3
        M3 --> M4
        M3 --> M5
    end
    
    M1 -->|stdio NDJSON v1| DartAdapter["adapters/dart (Dart SDK)"]
    M1 -->|stdio NDJSON v1| TSAdapter["adapters/typescript (Node.js Built-ins)"]
    M1 -->|stdio NDJSON v1| OtherAdapters["adapters/<lang> (Polyglot)"]
    
    M4 -->|HTTP 127.0.0.1 / Token Auth| Browser["Interactive FlowView 3-Column UI"]
    M5 -->|stdio JSON-RPC| AIAgent["AI Coding Agent (MCP)"]
```

---

## 4. Documentation Index

* **Architecture & Internals**: [`docs/ARCHITECTURE.md`](ARCHITECTURE.md)
* **LLM & Coding Agent Guide**: [`docs/guides/llm-usage.md`](guides/llm-usage.md)
* **Features & Prompts Guide**: [`docs/guides/feature.md`](guides/feature.md)
* **Development and CLI Guide**: [`docs/guides/development.md`](guides/development.md)
* **Multi-Language Adapter Protocol**: [`docs/design/specs/llm-language-adapter-protocol.md`](design/specs/llm-language-adapter-protocol.md)
* **Korean Project Overview**: [`docs/PROJECT-ko.md`](PROJECT-ko.md)
