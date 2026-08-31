# CodeFlow — Project Overview

> **Understand Large Codebases Through End-to-End Business Flow Extraction & Visualization**

CodeFlow is an interactive code analysis engine and visualization platform for developers and AI coding agents. Given a large, complex repository, CodeFlow extracts end-to-end **Core Business Flows** (핵심 흐름) across architectural layers, validates every step against verifiable code anchors, and visualizes the complete execution path through an interactive 7-lane interface.

---

## 1. Core Capabilities

Core Capabilities describe the domain engine, analytical methodologies, and correctness guarantees provided by CodeFlow.

### 1.1 End-to-End Core Flow Extraction
* **Architecture-Layer Traversal**: Traces the complete execution path starting from an entry trigger (UI click, HTTP endpoint, route, or system event) through controllers, use cases, domain entities, repositories, down to infrastructure and external APIs.
* **Non-Core Noise Pruning**: Isolates the requested business flow by filtering out non-advancing statements, boilerplate, and irrelevant utility invocations.

### 1.2 Zero-Hallucination Code Grounding (Fact Anchors)
* **Verifiable Code Anchors**: Every single step is tied to real code facts: exact file paths, byte ranges, line numbers, file hashes, span hashes, and canonical AST fingerprints.
* **Honest Gap Surfacing**: When dynamic dispatch, missing type information, or external boundaries interrupt static resolution, CodeFlow explicitly flags them as `unresolved_dynamic` or `unknowns[]` instead of inferring plausible but unverified steps.

### 1.3 Universal 7-Layer Normalization & Monotonic Progression
* **Canonical 7-Lane Architecture**: Normalizes diverse architectural nomenclature into 7 standardized layers:
  $$\text{presentation} \longrightarrow \text{controller} \longrightarrow \text{usecase} \longrightarrow \text{domain} \longrightarrow \text{data} \longrightarrow \text{infra} \longrightarrow \text{external}$$
* **Architecture-Agnostic Adaptation**: Seamlessly supports Feature-First Clean Architecture, Hexagonal / Ports & Adapters, Feature-Sliced Design (FSD), Layered MVC/MVVM, and Monorepos.
* **Monotonic Order Validation**: Enforces top-down architectural progression and validates layer integrity to catch architectural backtracking or unclassified layers.

### 1.4 Provenance & Freshness Lifecycle Tracking
* **Freshness Guarantees**: Tracks whether anchors are `fresh`, `stale` (source modified after generation), or `orphaned` (symbol deleted or relocated).
* **Authority Hierarchy**: Enforces confidence levels across step approvals (`approved` > `session` > `derived` > `unknown`).

### 1.5 Business Intent Signal Mining & Candidate Scoring
* **Semantic Discovery**: Automatically identifies and ranks candidate business flows by extracting natural language intent signals (`derivedName`, `docLine`, `triggerClass`) for semantic queries (e.g., *"Show email signup flow"*).

### 1.6 Track A "Layer Authority" Separation
* **Structural AST vs. Architectural Mapping**: Language adapters extract purely structural AST facts (`guard`, `mutation`, `call`, `effect`, `branch`), leaving high-level layer mapping and flow synthesis to the AI agent and Go Core validation engine.

---

## 2. Product Surfaces & Interfaces

Product Surfaces are the concrete software components, user touchpoints, protocols, and tools shipped with CodeFlow.

### 2.1 FlowView Interactive Web UI
* **7-Lane Architecture Timeline**: Interactive, browser-based visual map displaying the execution path traversing across architectural lanes.
* **Embedded CodeLens**: Inspects exact function bodies, source code spans, and highlighted step focuses directly within the browser.
* **Secure Token-Authenticated Loopback**: Embedded HTTP server bound strictly to `127.0.0.1` and protected by per-session cryptographic auth tokens (`?token=...`).

### 2.2 Multi-Agent Model Context Protocol (MCP) Server
* **Stdio JSON-RPC Interface**: Built-in MCP server providing out-of-the-box auto-configuration for 4 major AI agent ecosystems: **Codex, Claude Desktop, Cursor IDE, and Antigravity / Gemini CLI**.
* **Specialized MCP Toolset (8 Tools)**:
  * `publish_core_flow`: Atomically verifies anchors and publishes architecture-layer core flows.
  * `harvest_flows`: Discovers and scores candidate entry-point flows matching natural language queries.
  * `get_flow_payload`: Retrieves structured FlowSpec JSON for a specific flow.
  * `analyze_flow`: On-demand slice, fuse, and publish for arbitrary symbol entry points.
  * `submit_flow_draft`: Submits structured session journey drafts with verified anchors.
  * `approve_step`: Human/agent in-place approval for step descriptions and business rules.
  * `report_unknowns`: Inspects unresolved gaps, missing types, and dynamic call boundaries.
  * `open_review`: Lazily launches FlowView and provides an authenticated review URL.
* **Dynamic Target Routing**: Analyzes arbitrary target directories or monorepo sub-packages without polluting host working directories.

### 2.3 Multi-Language Polyglot Engine & Process Pools
* **Isolated Subprocess Protocol (`stdio` NDJSON v1)**: Decoupled language runtimes communicating with the Go Core via streaming line-delimited JSON.
* **Production Adapters**:
  * **Dart / Flutter Adapter**: Deep static analysis via the Dart Analyzer SDK.
  * **TypeScript / JavaScript Adapter**: Fast, zero-external-dependency AST scanner for React, Node.js, Express, and Next.js.
* **Process Pool Management (`AdapterRegistry`)**: Maintains reusable, multi-threaded adapter subprocess worker pools per repository and language.

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
    
    subgraph Go Core Engine ["Go Core Engine (internal/)"]
        Detect["detect (Project & Lang)"]
        Harvest["harvest (Discovery & Dedup)"]
        Pool["protocol / pool (Adapter Registry)"]
        Fusion["fusion (Monotonic Layer Check)"]
        Secret["secret (Single-Gate Redaction)"]
        FlowView["flowview (7-Lane HTML/CSS/JS)"]
    end
    
    Core --> Pool
    Pool -->|stdio NDJSON v1| DartAdapter["adapters/dart (Dart SDK)"]
    Pool -->|stdio NDJSON v1| TSAdapter["adapters/typescript (Node.js Built-ins)"]
    Pool -->|stdio NDJSON v1| OtherAdapters["adapters/<lang> (Kotlin, Swift, etc.)"]
    
    Core --> FlowView
    FlowView -->|HTTP 127.0.0.1 / Token Auth| Browser["Interactive 7-Lane Browser UI"]
```

---

## 4. Documentation Index

* **Architecture & Internals**: [`docs/ARCHITECTURE.md`](ARCHITECTURE.md)
* **LLM & Coding Agent Guide**: [`docs/llm-usage.md`](llm-usage.md)
* **Local CLI Usage**: [`docs/local-usage.md`](local-usage.md)
* **Multi-Language Adapter Protocol**: [`docs/spec/llm-language-adapter-protocol.md`](spec/llm-language-adapter-protocol.md)
* **Korean Project Overview**: [`docs/PROJECT-ko.md`](PROJECT-ko.md)
