# CodeFlow Architectural Modernization & Target Blueprint

- Contract ID: CODEFLOW-ARCH-MODERNIZATION
- Contract Status: Historical Supporting Blueprint
- Created: 2026-09-04
- Intent Status: Hardened
- Source: `docs/ARCHITECTURE.md`, `docs/design/raw/requested-flow-live-semantic-compiler-architecture-draft-ko.md`, User instruction (current request 2026-09-04)
- Decision Records: `docs/design/decisions/2026-09-04-codeflow-architectural-modernization-decisions.md`
- Glossary: `docs/design/glossary.md`
- Current Product Contract: `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md`
- Status Note: This document records the 2026-09-04 modernization proposal. Its architecture and execution order are historical. The current FlowView contract does not require a wholesale storage or package redesign.

---

## 1. Problem and Goal

CodeFlow has evolved from an initial prototype into a sophisticated Live Semantic Compiler. While its algorithmic primitives and domain vision are proven, the current implementation exhibits severe architectural debt:
1. **Pipeline Duplication & Leaky Boundaries**: The core 15-step semantic compilation pipeline is copied across three distinct packages (`cmd/codeflow/query.go`, `internal/flowview/server.go`, and `internal/mcp/semantic_handlers.go`). The 1,386-line `flowview.Server` god-struct bundles 11 orthogonal responsibilities and spawns OS child processes per HTTP request.
2. **State Ephemerality & Concurrency Hazards**: In-memory workspace snapshots cause CLI amnesia and MCP/FlowView split-brain. A critical garbage collection flaw in `SnapshotEngine.PruneOrphanCAS` permanently deletes formal `GenerationProofManifest` JSON artifacts from disk. Global HTTP timeouts terminate SSE streams after 15 seconds.
3. **Security Leaks & Epistemic Facades**: Outbound HTTP, SSE, and MCP streams lack credential redaction. FlowView host prefix checks permit DNS rebinding. Approval endpoints synthesize fake proposals on the fly without cryptographic signatures or persistent disk records.
4. **Adapter Feature Parity Deficits**: The Go language adapter skips struct receiver methods (`fn.Recv != nil`) and returns a non-functional 1-step dummy slice with 0 edges. Language worker pools lack `maxActive` bounds and process group isolation.

### Observable Outcome
CodeFlow transitions into a mature, enterprise-grade Hexagonal (Ports & Adapters) system where:
- All interfaces (CLI, Web, MCP) share a single, authoritative `CompilerService` application layer.
- Workspace state and formal generation proofs are durable, immutable, and consistent across processes.
- Outbound egress streams are credential-safe, loopback-hardened, and immune to DNS rebinding.
- Go source code enjoys first-class call graph slicing parity with TypeScript and Dart.
- Subprocesses are strictly bounded and isolated using OS process groups (`Setpgid: true`).

### Intent and Goal IDs
- **INT-01: System Decomposition & Single-Source Pipeline**
  - **GOAL-01**: Unified `CompilerService` Application Layer orchestrates the 15-step compilation pipeline identically for CLI, FlowView, and MCP.
  - **GOAL-02**: Canonical `internal/domain/proof` models eliminate duplicate struct declarations and manual field copying between `semantic` and `storage`.
  - **GOAL-03**: Decompose `flowview.Server` into specialized controllers sharing a persistent worker pool, eliminating per-request process spawning (fork bombs).
  - **GOAL-04**: Relocate lane clustering and architecture graph analytics from web presentation to `internal/domain/archmap/`.
- **INT-02: State Lifecycle, Concurrency & Reactive Consistency**
  - **GOAL-05**: Segregated CAS storage (`.codeflow/cas/blobs/` vs `.codeflow/cas/manifests/`) ensures garbage collection never deletes generation proof manifests.
  - **GOAL-06**: Streaming SSE endpoints configure per-route response deadlines, eliminating the 15-second global HTTP write timeout termination.
  - **GOAL-07**: Disk-backed snapshot engine (`.codeflow/workspace/snapshots.json`) provides unified, durable state across CLI, FlowView, and MCP runtimes.
  - **GOAL-08**: Real-time reactive loop connects `internal/watch` to `CoalescingScheduler` and a background worker, automatically publishing generation updates on file edits.
- **INT-03: Defense-in-Depth Security & Epistemic Grounding**
  - **GOAL-09**: Strict loopback origin and host validation blocks DNS rebinding and cross-origin invocation of FlowView endpoints.
  - **GOAL-10**: Path traversal guard enforcing `isSubpath` prevents MCP tools from accessing host files outside the repository root.
  - **GOAL-11**: Centralized egress credential scrubbing filters Bearer tokens, PEM keys, AWS secrets, and database URIs on all outbound streams.
  - **GOAL-12**: Semantic approvals enforce verifiable Ed25519 signatures and persistent audit storage, rejecting ungrounded or synthetic proposals.
- **INT-04: Polyglot Adapter Extensibility & Language Parity**
  - **GOAL-13**: Go language adapter extracts struct receiver methods (`fn.Recv`) and performs statement-level AST slicing with call graph traversal.
  - **GOAL-14**: Process pool enforces `maxActive` bounds, `Setpgid: true` group isolation, and cleanly reaps broken retry subprocesses.
  - **GOAL-15**: Standardized Content-Length framed JSON-RPC 2.0 protocol eliminates legacy NDJSON translation bridges in TypeScript and Dart adapters.

---

## 2. Scope

### In Scope
- **Domain Decoupling**: Creation of `internal/domain/proof/` (canonical proof/pointer models), `internal/domain/semir/` (semantic models), and `internal/domain/archmap/` (relocated from `flowview/lanes.go`).
- **Application Services**: Implementation of `internal/application/compiler/` (`CompilerService`), `internal/application/workspace/` (`WorkspaceService`), and `internal/application/approval/` (`ApprovalService`).
- **Presentation Refactoring**: Refactoring of `cmd/codeflow/`, `internal/flowview/`, and `internal/mcp/` to delegate all compilation, workspace, and approval operations to Application Services.
- **Storage Hardening**: Directory segregation for CAS blobs (`.codeflow/cas/blobs/`) and manifests (`.codeflow/cas/manifests/`), CAS GC scoping, and disk-backed snapshot metadata.
- **Security & Redaction**: Injection of `secret.RedactJSON` across FlowView HTTP writers, SSE event formatters, and MCP stdout streams; loopback Host/Origin parsing; MCP `isSubpath` enforcement.
- **Concurrency & Protocol**: SSE per-route deadlines, event hub channel leak fixes, process pool persistent sharing, `maxActive` bounds, `Setpgid` isolation, and Go adapter AST slicing rewrite.

### Non-Goals
- **Altering External Schema Contracts**: Existing JSON schemas (`schemas/semantic-map-ir.schema.json`, `schemas/generation-proof-manifest.schema.json`, etc.) remain backward-compatible.
- **Breaking Wire Protocols**: REST HTTP endpoints (`/api/task/...`, `/api/workspace/...`) and JSON-RPC 2.0 tool schemas remain identical for frontend SPA and AI agent consumers.
- **Rewriting Dart/TS Language Adapters from Scratch**: Dart and TypeScript adapters retain their existing AST extraction engines, only eliminating hidden NDJSON translation layers and fixing framing.

---

## 3. Actors and Preconditions

- **Primary Actors**:
  - Human Software Developer (interacts via FlowView Web UI or CLI).
  - Autonomous AI Coding Agent (interacts via MCP JSON-RPC stdio protocol).
- **System Preconditions**:
  - Operating System: macOS or Linux supporting standard POSIX process groups.
  - Go Toolchain: Go 1.22+ installed and available on PATH.
  - Node.js & Dart SDK: Available for respective language adapter execution.
- **Data Preconditions**:
  - Repository workspace root initialized with or without an existing `.codeflow/` metadata directory.

---

## 4. Confirmed Facts

1. `cmd/codeflow/query.go#L74-L143`, `internal/flowview/server.go#L606-L811`, and `internal/mcp/semantic_handlers.go#L107-L173` execute identical 15-step compiler pipelines, but CLI and MCP skip CAS commits and gate evaluations.
2. `internal/semantic/proof_models.go#L8-L71` and `internal/storage/active_pointer.go#L26-L89` declare identical structs (`GenerationProofManifest`, `ActivePointer`, `SettlementEvaluation`, `ArtifactRefs`, `CurrentPublicationResult`) solely to evade circular imports.
3. `internal/flowview/server.go#L617` instantiates `protocol.NewPool(adapterCfg, 2)` with `defer pool.Close()` on every incoming HTTP query, causing process table exhaustion under load.
4. `internal/flowview/server.go#L1078, L1127, L1174` iterates over Go maps without ordering (`for _, m := range s.mapCache { activeMap = m; break }`), causing non-deterministic graph results.
5. `internal/workspace/snapshot_engine.go#L288-L330` deletes all non-revision files in `.codeflow/cas/`, permanently purging `GenerationProofManifest` JSON documents.
6. `internal/flowview/server.go#L127-L128` sets global `WriteTimeout: 15s` on `http.Server`, severing streaming SSE connections after 15 seconds.
7. `adapters/go/main.go#L368` skips `fn.Recv != nil`, ignoring all Go struct receiver methods, and lines 403–416 return a 1-step dummy slice with 0 edges.
8. `internal/flowview/server.go#L166-L183` uses `strings.HasPrefix(host, "localhost")`, which erroneously allows malicious domains such as `localhost.attacker.com`.
9. `internal/mcp/server.go#L121` accepts absolute paths directly without executing the `isSubpath` check defined at line 658.

---

## 5. Assumptions

- **ASM-01**: Existing clients (FlowView SPA and MCP agents) depend strictly on the JSON payload structures and wire protocols defined in `schemas/`.
  - *Consequence if false*: Internal refactoring could break frontend visualization or MCP tool integrations.
  - *Validation method*: Run full Playwright E2E suite (`web/live-comprehension-workspace/tests/e2e.spec.ts`) and MCP tool test suites against refactored code.
- **ASM-02**: Separating CAS files into `blobs/` and `manifests/` subdirectories does not break historical repository state if a fallback lookup to the root `.codeflow/cas/` is maintained.
  - *Consequence if false*: Existing repositories with pre-modernization CAS files might fail to load historical generations.
  - *Validation method*: Unit tests in `internal/storage/` verifying that `ReadManifestCAS` checks `manifests/<hash>.json` first, then falls back to `<hash>.json`.

---

## 6. Business Rules and Invariants

- **INV-01 (Single Compiler Authority)**: All semantic map compilations, gate evaluations, proof manifest generations, and active pointer commits MUST be executed exclusively by `internal/application/compiler/service.go`. CLI, FlowView, and MCP are strictly prohibited from implementing compilation logic directly.
- **INV-02 (Epistemic Segregation)**: Deterministic AST facts, SLM probabilistic proposals, runtime execution observations, and human approvals MUST maintain distinct authority tags. Model proposals must never be marked `approved` or `verified` without a cryptographically valid human approval signature.
- **INV-03 (Proof Manifest Preservation)**: Workspace snapshot pruning and CAS garbage collection MUST NEVER delete files under `.codeflow/cas/manifests/`.
- **INV-04 (Zero Egress Secret Leakage)**: Every byte transmitted over FlowView HTTP responses, SSE event frames, and MCP JSON-RPC stdio MUST pass through `secret.RedactJSON`.
- **INV-05 (Loopback Enclosure)**: FlowView HTTP server MUST strictly reject any request whose `Host` or `Origin` header resolves to a non-loopback address (`127.0.0.1`, `[::1]`, `localhost`).
- **INV-06 (Workspace State Single-Truth)**: Workspace snapshots and active pointers MUST be persisted to disk (`.codeflow/workspace/snapshots.json`, `.codeflow/active-pointer.json`) to guarantee identical state across CLI, FlowView, and MCP.
- **INV-07 (Subprocess Safety & Termination)**: All adapter subprocesses MUST be spawned in their own process groups (`Setpgid: true`) and reaped via negative PID group termination upon pool shutdown. Broken retry connections must be closed immediately.
- **INV-08 (Go Struct Method Parity)**: The Go language adapter MUST parse struct receiver methods (`fn.Recv != nil`) and construct multi-step call graphs with cyclic detection.

---

## 7. Observable User Flow

### Flow 1: Unified Semantic Query (CLI, Web, MCP)
1. **User Trigger**: Developer runs `codeflow query "Submit order"` via CLI, clicks a flow in FlowView Web, or an AI agent calls MCP tool `query_task_view`.
2. **Inbound Adaptation**: The respective inbound controller decodes the parameters and invokes `CompilerService.CompileTaskView(ctx, req)`.
3. **Application Orchestration**:
   - `CompilerService` retrieves the active snapshot from `WorkspaceService`.
   - Obtains a worker from the shared `protocol.Pool` (without spawning new OS processes).
   - Extracts AST slices, compiles `SemanticMapIR`, evaluates the 5 critical obligations, and runs `PublicationGate`.
   - Constructs `GenerationProofManifest` and writes to `.codeflow/cas/manifests/<hash>.json`.
   - Atomically updates `.codeflow/active-pointer.json`.
4. **Outbound Sanitization**: The result is scrubbed via `secret.RedactJSON` and returned to the caller.
5. **Reactive Broadcast**: If published via Web daemon, `generation.published` is broadcast over the persistent SSE stream to all connected browsers.

### Flow 2: Reactive Code Editing & Live Update
1. **User Edit**: Developer edits a source file in their IDE.
2. **File Watcher**: `internal/infra/watcher` detects the file modification, debounces bursts (300ms), and notifies `WorkspaceService`.
3. **Ephemeral ACK**: `WorkspaceService` records a new `DocumentRevision` and emits an `activity.updated` event over SSE (<300ms).
4. **Coalesced Compilation**: After the 2-second edit quiescence window, `CoalescingScheduler` triggers background re-indexing via `CompilerService`.
5. **Proof & Publication**: A new verified generation is compiled, persisted to CAS manifests, atomic pointer updated, and `generation.published` broadcast over SSE without manual page refresh.

---

## 8. Failure and Boundary Behavior

- **CAS Pointer Conflict (Concurrent Publication)**: If two processes attempt to publish a generation simultaneously, `storage.CompareAndSwapActivePointer` detects the expected snapshot/generation mismatch, fails the atomic write, returns `ErrCASConflict`, and triggers a reactive re-compilation from the latest live head.
- **Adapter Crash or Timeout**: If a language worker subprocess crashes or times out during slicing:
  - `protocol.Pool` marks the connection broken.
  - Teardown issues `-cmd.Process.Pid` to terminate any orphaned child processes in the process group.
  - The connection is closed immediately (`retryConn.Close()`).
  - A fallback worker is acquired up to `maxRetries: 2`. If slicing still fails, an explicit `slice_failed` error is returned without panicking.
- **SSE Client Disconnection**: When a browser closes an SSE tab, `handleWorkspaceStream` detects context cancellation, unregisters the subscriber, closes the channel, and terminates the streaming goroutine cleanly without data races.
- **Path Traversal Attack**: If an MCP query passes `target: "/etc/passwd"` or `../../.env`, `resolveTarget` evaluates `isSubpath(repoRoot, target)` and immediately returns an `access_denied` JSON-RPC error.
- **DNS Rebinding Attack**: If a malicious web page makes a cross-origin request with `Host: localhost.attacker.com`, FlowView security middleware parses the host with `net.SplitHostPort`, verifies it is not equal to `localhost`, `127.0.0.1`, or `[::1]`, and immediately rejects the connection with HTTP 403 Forbidden.

---

## 9. Decisions

| ID | Question | Decision | Rationale | Rejected Alternative | Consequences | Source / Evidence | Decision Record |
|---|---|---|---|---|---|---|---|
| **ARCH-D01** | How to structure core system boundaries? | Hexagonal Architecture (Ports & Adapters) | Decouples domain rules from storage and UI transport; enables headless reuse. | Layered architecture with ad-hoc package utilities | Domain models have zero external dependencies. Infrastructure implements ports. | ARCHITECTURE.md §2 | `docs/design/decisions/2026-09-04-codeflow-architectural-modernization-decisions.md#arch-d01` |
| **ARCH-D02** | How to resolve triple compiler pipeline duplication? | Single `CompilerService` in `internal/application/compiler` | Eliminates logic drift across CLI, Web, and MCP; guarantees gate and CAS enforcement. | Maintain separate handlers with shared helper funcs | Centralizes 15-step compiler pipeline into a single maintainable service. | ARCHITECTURE.md §3.2 | `...#arch-d02` |
| **ARCH-D03** | How to eliminate circular import model duplications? | Extract `internal/domain/proof` package | Restores Go type safety and removes 5 duplicate struct definitions. | Retain duplicate structs and manual copying | Eliminates field-by-field copy loops; enables clean domain imports. | ARCHITECTURE.md §3.1 | `...#arch-d03` |
| **ARCH-D04** | Where should 7-lane graph analytics live? | Relocate from `flowview/lanes.go` to `domain/archmap` | Architecture classification is core domain knowledge needed by CLI and MCP. | Keep in FlowView and expose internal HTTP API | CLI, Web UI, and AI agents obtain identical layer assignments. | ARCHITECTURE.md §3.1 | `...#arch-d04` |
| **ARCH-D05** | How to fix FlowView per-request fork bombs? | Decompose `Server` and share persistent `protocol.Pool` | Spawning pools per HTTP request exhausts OS process table. | Increase OS process limits | Eliminates 500ms–2s query latency; prevents fork bombs under load. | ARCHITECTURE.md §3.4 | `...#arch-d05` |
| **ARCH-D06** | How to prevent CAS GC from deleting proof manifests? | Segregate into `.codeflow/cas/blobs/` and `manifests/` | Manifests are permanent audit proofs that must never be deleted by revision GC. | Disable CAS garbage collection completely | Protects proof history indefinitely while allowing safe revision cleanup. | ARCHITECTURE.md §5 | `...#arch-d06` |
| **ARCH-D07** | How to keep SSE streams alive? | Remove global `WriteTimeout`; use per-route deadlines | Global HTTP write timeout kills long-lived SSE streams every 15 seconds. | Increase WriteTimeout to 1 hour | SSE streams remain open indefinitely until client disconnects. | ARCHITECTURE.md §3.4 | `...#arch-d07` |
| **ARCH-D08** | How to eliminate CLI amnesia and MCP split-brain? | Persist snapshots to `.codeflow/workspace/snapshots.json` | In-memory engine loses state across process lifecycles. | Run background daemon with Unix domain socket | Durable multi-process state consistency without daemon operational complexity. | ARCHITECTURE.md §5 | `...#arch-d08` |
| **ARCH-D09** | How to activate the real-time reactive loop? | Connect watcher to scheduler and spawn background worker | Dead checkpoint channel prevents automated re-indexing on edits. | Keep manual on-demand compilation only | Code edits automatically compile and broadcast generation updates in 2–3s. | ARCHITECTURE.md §1 | `...#arch-d09` |
| **ARCH-D10** | How to secure egress streams and loopback access? | Inject `secret.RedactJSON` and parse loopback URLs | Prevents credential leaks and DNS rebinding / CSRF exploitation. | Client-side sanitization only | Complete server-side defense-in-depth across HTTP, SSE, and MCP. | ARCHITECTURE.md §1 | `...#arch-d10` |
| **ARCH-D11** | How to support Go source code slicing? | Implement AST statement slicer with `fn.Recv` support | Go adapter currently skips struct methods and returns 1-step dummy stub. | Exclude Go from supported slicing languages | CodeFlow achieves first-class slicing parity for Go backend services. | ARCHITECTURE.md §4 | `...#arch-d11` |
| **ARCH-D12** | How to eliminate subprocess leaks and zombie workers? | Add `maxActive`, `Setpgid: true`, and close broken retries | Leaked worker processes exhaust host resources. | Rely on OS init process reaper | Strict worker bounds and reliable process group termination. | ARCHITECTURE.md §3.3 | `...#arch-d12` |

---

## 10. Quality Constraints

1. **Performance**:
   - In-memory active map queries (`/api/task/review`, etc.) must respond in $\le 50	ext{ms}$ (P95).
   - Re-compilation of warm feature flows must complete in $\le 500	ext{ms}$ (P95).
   - Shared process pool must eliminate per-query worker startup latency (saving 500ms–2s per request).
2. **Concurrency Safety**:
   - `go test -race ./internal/flowview/... ./internal/workspace/... ./internal/storage/...` must pass cleanly with 0 data races.
   - Subscriber disconnection must terminate streaming goroutines and close channels without memory leaks.
3. **Storage Reliability**:
   - `PruneOrphanCAS` must delete orphan revision blobs while leaving 100% of `.codeflow/cas/manifests/` intact.

---

## 11. Feature-Level Acceptance

- **FA-01**: THE `CompilerService` SHALL execute the complete 15-step semantic compilation pipeline when invoked by CLI (`cmd/codeflow/query.go`), FlowView (`internal/flowview`), or MCP (`internal/mcp`), producing identical `SemanticMapIR` and `GenerationProofManifest` outputs.
- **FA-02**: WHEN a semantic query executes via CLI or MCP, THE `CompilerService` SHALL evaluate the publication subgates and persist the generation proof manifest to CAS storage.
- **FA-03**: THE system SHALL declare canonical proof models in `internal/domain/proof/`, and `internal/semantic/proof_models.go` and `internal/storage/active_pointer.go` SHALL NOT contain duplicate struct definitions.
- **FA-04**: THE `internal/domain/archmap/` package SHALL provide 7-lane consensus ordering and clustering functions, and `internal/flowview/lanes.go` SHALL delegate to this domain package.
- **FA-05**: THE `flowview.Server` SHALL share a single, persistent `protocol.Pool` instance across HTTP requests, and SHALL NOT instantiate a new process pool during `handleTaskView`.
- **FA-06**: WHEN `SnapshotEngine.PruneOrphanCAS` executes, THE system SHALL delete unreferenced revision blobs in `.codeflow/cas/blobs/` AND SHALL NOT delete any proof manifest in `.codeflow/cas/manifests/`.
- **FA-07**: WHEN an HTTP client establishes a Server-Sent Events connection to `/api/workspace/stream`, THE server SHALL keep the connection open past 15 seconds without termination by global HTTP write timeouts.
- **FA-08**: WHEN a developer runs `codeflow status` via CLI following workspace modifications, THE CLI SHALL read the durable state from `.codeflow/workspace/snapshots.json` and display the current active snapshot ID and pending revisions.
- **FA-09**: WHEN a file is modified on disk, THE reactive file watcher and `CoalescingScheduler` SHALL trigger automated background re-indexing and broadcast a `generation.published` event over SSE within 3 seconds.
- **FA-10**: THE system SHALL filter all outbound HTTP responses, SSE event frames, and MCP JSON-RPC messages through `secret.RedactJSON`, replacing credentials and tokens with `[REDACTED]`.
- **FA-11**: IF an incoming HTTP request to FlowView contains a `Host` header such as `localhost.attacker.com`, THEN THE server SHALL reject the request with HTTP 403 Forbidden.
- **FA-12**: WHEN the Go language adapter slices a Go source file containing struct receiver methods (`func (s *Server) Handle()`), THE adapter SHALL parse the method, traverse its call graph, and return discrete statement steps and causal edges.
- **FA-13**: WHEN an adapter subprocess is spawned, THE system SHALL set `SysProcAttr.Setpgid = true`, and upon worker pool shutdown, THE system SHALL terminate the entire process group using negative PID signaling.
- **FA-14**: WHEN a human developer or authorized caller submits a semantic approval via FlowView or MCP, THE system SHALL verify physical AST evidence grounding and a valid Ed25519 signature, persisting the signed approval record to `.codeflow/approvals/<proposal_id>.json`.
- **FA-15**: THE TypeScript and Dart language adapters SHALL communicate over native Content-Length framed JSON-RPC 2.0 stdio protocols without internal NDJSON translation bridges or frame tearing.

---

## 12. Vertical Slice Contracts

The architectural modernization intent is partitioned into 10 goal-driven Vertical Slice contracts under `.tasks/2026-09-04-codeflow-architectural-modernization/`:

| Slice | Stable Contract ID | Contract Path | Primary Goal | User Outcome | Dependencies | Parent Acceptance | Status |
|---|---|---|---|---|---|---|---|
| **VS-01** | `CODEFLOW-ARCH-MODERNIZATION-VS-01` | `.tasks/2026-09-04-codeflow-architectural-modernization/vs-01-prevent-host-rebinding-and-secure-egress-streams.md` | `GOAL-09` | Prevent host rebinding, MCP file traversal, and scrub egress credentials. | None | FA-10, FA-11 | Review Passed (2026-09-04) |
| **VS-02** | `CODEFLOW-ARCH-MODERNIZATION-VS-02` | `.tasks/2026-09-04-codeflow-architectural-modernization/vs-02-retain-proof-manifests-across-cas-garbage-collection.md` | `GOAL-05` | Segregate CAS storage so pruning never deletes proof manifests. | None | FA-06 | Review Passed (2026-09-04) |
| **VS-03** | `CODEFLOW-ARCH-MODERNIZATION-VS-03` | `.tasks/2026-09-04-codeflow-architectural-modernization/vs-03-maintain-persistent-process-isolation-and-long-lived-streams.md` | `GOAL-14` | Isolate subprocesses with Setpgid, reuse process pool, and support infinite SSE streams. | None | FA-05, FA-07, FA-13 | Review Passed (2026-09-04) |
| **VS-04** | `CODEFLOW-ARCH-MODERNIZATION-VS-04` | `.tasks/2026-09-04-codeflow-architectural-modernization/vs-04-share-canonical-proof-models-and-lane-architecture-analytics.md` | `GOAL-02` | Share canonical proof models and lane analytics across all packages without duplication. | None | FA-03, FA-04 | Review Passed (2026-09-04) |
| **VS-05** | `CODEFLOW-ARCH-MODERNIZATION-VS-05` | `.tasks/2026-09-04-codeflow-architectural-modernization/vs-05-unify-semantic-compilation-across-cli-web-and-mcp.md` | `GOAL-01` | Unify 15-step semantic compilation across CLI, Web, and MCP via `CompilerService`. | VS-02, VS-03, VS-04 | FA-01, FA-02 | Review Passed (2026-09-04) |
| **VS-06** | `CODEFLOW-ARCH-MODERNIZATION-VS-06` | `.tasks/2026-09-04-codeflow-architectural-modernization/vs-06-persist-verifiable-semantic-approvals.md` | `GOAL-12` | Validate evidence grounding and persist verifiable approvals durably. | VS-05 | FA-14 | Review Passed (2026-09-04) |
| **VS-07** | `CODEFLOW-ARCH-MODERNIZATION-VS-07` | `.tasks/2026-09-04-codeflow-architectural-modernization/vs-07-synchronize-durable-workspace-snapshots-across-runtimes.md` | `GOAL-07` | Synchronize workspace snapshots across CLI, Web, and MCP backed by disk. | VS-02 | FA-08 | Review Passed (2026-09-04) |
| **VS-08** | `CODEFLOW-ARCH-MODERNIZATION-VS-08` | `.tasks/2026-09-04-codeflow-architectural-modernization/vs-08-stream-live-generation-updates-on-workspace-edits.md` | `GOAL-08` | Trigger automatic background compilation and broadcast SSE updates on file edits within 3s. | VS-03, VS-05, VS-07 | FA-09 | Review Passed (2026-09-04) |
| **VS-09** | `CODEFLOW-ARCH-MODERNIZATION-VS-09` | `.tasks/2026-09-04-codeflow-architectural-modernization/vs-09-slice-go-source-files-with-struct-receiver-parity.md` | `GOAL-13` | Extract Go struct receiver methods and perform statement-level AST call graph slicing. | None | FA-12 | Review Passed (2026-09-04) |
| **VS-10** | `CODEFLOW-ARCH-MODERNIZATION-VS-10` | `.tasks/2026-09-04-codeflow-architectural-modernization/vs-10-eliminate-legacy-ndjson-adapter-bridges-and-standardize-framing.md` | `GOAL-15` | Eliminate legacy NDJSON adapter bridges and standardize Content-Length framed JSON-RPC 2.0. | None | FA-15 | Review Passed (2026-09-04) |

Execution Order:
`[VS-01, VS-02, VS-03, VS-04] (Phase 1 & Domain Foundations) → VS-05 (Unified Compiler) → VS-06 (Approvals) & VS-07 (Durable Snapshots) → VS-08 (Reactive Loop), VS-09 (Go Parity) & VS-10 (Polyglot Protocol Framing)`

---

## 13. Open Decisions

| ID | Question | Options | Trade-off | Recommendation |
|---|---|---|---|---|
| **Q-01** | Should legacy single-folder CAS files (`.codeflow/cas/<hashHex>.json`) be migrated automatically on startup? | A: Lazy fallback read check; B: Eager migration script on startup. | Eager migration touches user disk; lazy fallback is non-destructive. | Adopt Option A: `ReadManifestCAS` checks `manifests/` first, falls back to legacy path. |
| **Q-02** | Should the Go adapter use `golang.org/x/tools/go/packages` or standard `go/parser`? | A: `go/parser` (zero external dependencies); B: `go/packages` (type-checked AST with dependency resolution). | `go/packages` requires downloading external Go modules; `go/parser` keeps the adapter binary completely standalone. | Adopt Option A (`go/parser` + AST walker) for zero-dependency standalone execution. |

---

## 14. Done When

This architectural modernization parent specification is delivered when:
1. All 15 Feature Acceptance criteria (FA-01 through FA-15) are verified by automated tests.
2. The 3 modernization phases (Phase 1: Stabilization & Security, Phase 2: Domain Decoupling & Application Service, Phase 3: Reactive Loop & Polyglot Parity) are implemented through verified Vertical Slice contracts under `.tasks/2026-09-04-codeflow-architectural-modernization/`.
3. Concurrency test suite (`go test -race ./...`) passes with zero race detections.
4. All existing Playwright E2E tests (`web/live-comprehension-workspace/tests/e2e.spec.ts`) pass without regressions.
