# CodeFlow Architectural Modernization Decision Records

- Record Status: Proposed (Auto-accepted upon Intent Hardening Gate)
- Created: 2026-09-04
- Parent Contract: `docs/design/specs/2026-09-04-codeflow-architectural-modernization.md`
- Source: `docs/architecture/architectural-maturity-review.md`, `docs/architecture/architecture.md`, user prompt 2026-09-04
- Approval Basis: Intent Hardening Interview confirmed 2026-09-04

This document records the architectural decisions for transitioning CodeFlow from an early monolithic prototype to a hardened, enterprise-grade Hexagonal (Ports & Adapters) system.

---

<a id="arch-d01"></a>
## ARCH-D01 · Hexagonal Architecture (Ports & Adapters) Decomposition

- Status: Accepted
- Context: Core semantic logic, persistence, OS subprocesses, and HTTP/SSE transport are tightly coupled in god-structs like `flowview.Server`, causing leaky abstractions and making headless execution inconsistent.
- Decision: Adopt Hexagonal Architecture with clean separation across Domain Core (`internal/domain/`), Application Layer (`internal/application/`), Infrastructure Adapters (`internal/infra/`), and Inbound Presentation Adapters (`internal/api/`, `cmd/codeflow/`).
- Rejected Alternative: Maintain existing layered package structure and apply ad-hoc package refactoring.
- Rationale: Hexagonal architecture decouples pure business invariants from transport and storage details, allowing identical engine behavior across CLI, Web, and MCP.
- Consequences: Domain models have zero external dependencies. Infrastructure components implement domain ports.
- Source / Evidence: architectural-maturity-review.md §7; architecture.md §2; User approval 2026-09-04.
- Contract Trace: INT-01, GOAL-01–GOAL-03, FA-01.

---

<a id="arch-d02"></a>
## ARCH-D02 · Single-Source Application Layer (`CompilerService`)

- Status: Accepted
- Context: The 15-step semantic compilation pipeline is currently duplicated across `cmd/codeflow/query.go`, `internal/flowview/server.go`, and `internal/mcp/semantic_handlers.go`.
- Decision: Unify the entire 15-step pipeline into a single `CompilerService` in `internal/application/compiler/`. CLI, FlowView, and MCP must delegate directly to this service.
- Rejected Alternative: Keep separate handlers and extract helper utility functions.
- Rationale: Shared application services guarantee zero logic drift in gate evaluation, proof construction, and CAS commits across all interfaces.
- Consequences: Eliminates triple maintenance overhead. Any compilation change applies uniformly to CLI, Web, and MCP.
- Source / Evidence: architectural-maturity-review.md §2.1; architecture.md §3.2.
- Contract Trace: INT-01, GOAL-01, FA-01, FA-02.

---

<a id="arch-d03"></a>
## ARCH-D03 · Canonical Domain Proof Models Elimination of Duplication

- Status: Accepted
- Context: 5 core proof structures (`GenerationProofManifest`, `ActivePointer`, `SettlementEvaluation`, `ArtifactRefs`, `CurrentPublicationResult`) were duplicated verbatim between `semantic` and `storage` to evade Go circular dependency errors, forcing manual field copying.
- Decision: Extract canonical domain models into `internal/domain/proof/` with zero dependencies on storage or presentation.
- Rejected Alternative: Retain duplicate structs and maintain manual copying functions in `server.go`.
- Rationale: Circular import evasion by code duplication breaks Go type safety and causes silent schema drift.
- Consequences: `semantic`, `storage`, and `flowview` import the canonical domain types directly.
- Source / Evidence: architectural-maturity-review.md §2.3; architecture.md §3.1.
- Contract Trace: INT-01, GOAL-02, FA-03.

---

<a id="arch-d04"></a>
## ARCH-D04 · Relocation of Lane Analytics to Domain Core

- Status: Accepted
- Context: 729 lines of sophisticated graph analysis and 7-lane consensus voting logic are trapped inside presentation layer `internal/flowview/lanes.go`, preventing CLI and MCP from accessing architecture clustering.
- Decision: Relocate graph analytics and lane clustering algorithms to `internal/domain/archmap/`.
- Rejected Alternative: Keep lane logic in FlowView and expose an internal HTTP endpoint for CLI/MCP.
- Rationale: Architecture lane classification is core domain knowledge, not a presentation styling concern.
- Consequences: CLI, Web UI, and AI agents obtain identical layer assignments for all analyzed codebases.
- Source / Evidence: architectural-maturity-review.md §2.4; architecture.md §3.1.
- Contract Trace: INT-01, GOAL-03, FA-04.

---

<a id="arch-d05"></a>
## ARCH-D05 · Decompose `flowview.Server` & Share Persistent Worker Pool

- Status: Accepted
- Context: `flowview.Server` bundles 11 responsibilities over 1,386 lines and instantiates `protocol.NewPool` per HTTP request, triggering OS fork bombs under concurrent queries.
- Decision: Decompose `flowview.Server` into specialized controllers (`TaskViewController`, `WorkspaceStreamController`, `ApprovalController`, `StaticAssetController`) and maintain a shared, persistent `protocol.Pool` instance across requests.
- Rejected Alternative: Increase OS process limits and retain monolithic server struct.
- Rationale: HTTP request handlers must not spawn and destroy worker pools on each query.
- Consequences: Eliminates 500ms–2s latency penalty per query; prevents process table exhaustion.
- Source / Evidence: architectural-maturity-review.md §2.2, D-04; architecture.md §3.4.
- Contract Trace: INT-01, GOAL-03, FA-05.

---

<a id="arch-d06"></a>
## ARCH-D06 · CAS Storage Dual-Namespace Segregation

- Status: Accepted
- Context: `SnapshotEngine.PruneOrphanCAS` deletes all non-revision files in `.codeflow/cas/`, permanently wiping `GenerationProofManifest` JSON artifacts stored in the same directory.
- Decision: Segregate CAS storage into `.codeflow/cas/blobs/` for revisions and `.codeflow/cas/manifests/` for proof documents. Configure GC to scan only `blobs/`.
- Rejected Alternative: Disable CAS garbage collection entirely.
- Rationale: Proof manifests and settlement evaluations are permanent epistemic audit records that must never be deleted by workspace revision cleanup.
- Consequences: Historical proofs and settlement evaluations remain intact across all workspace transactions.
- Source / Evidence: architectural-maturity-review.md §3.4, D-03; architecture.md §5.
- Contract Trace: INT-02, GOAL-04, FA-06.

---

<a id="arch-d07"></a>
## ARCH-D07 · Elimination of Global HTTP WriteTimeout on SSE Streams

- Status: Accepted
- Context: Global `http.Server.WriteTimeout: 15s` forcibly kills long-lived Server-Sent Events streams (`/api/workspace/stream`) every 15 seconds.
- Decision: Remove global `WriteTimeout` from `http.Server`. Enforce per-route timeouts on non-streaming handlers via `http.ResponseController` or middleware.
- Rejected Alternative: Increase `WriteTimeout` to a larger value (e.g. 1 hour).
- Rationale: SSE connections are indefinite streaming channels that cannot operate under fixed write timeouts.
- Consequences: SSE connections remain open indefinitely until explicitly disconnected by clients.
- Source / Evidence: architectural-maturity-review.md §3.5, D-06; architecture.md §3.4.
- Contract Trace: INT-02, GOAL-05, FA-07.

---

<a id="arch-d08"></a>
## ARCH-D08 · Disk-Backed Workspace State Unification

- Status: Accepted
- Context: `SnapshotEngine` stores all workspace snapshots and file revisions in RAM maps, causing CLI `codeflow status` to report amnesia and creating split-brain between MCP and FlowView.
- Decision: Persist workspace snapshots and revision manifests to `.codeflow/workspace/snapshots.json`. CLI, FlowView, and MCP share the identical disk-backed engine instance.
- Rejected Alternative: Run a background daemon process that CLI and MCP communicate with via Unix domain socket.
- Rationale: Disk-backed JSON persistence provides simple, robust multi-process state sharing without daemon management complexity.
- Consequences: CLI status commands accurately reflect current workspace state; MCP edits are immediately visible in FlowView.
- Source / Evidence: architectural-maturity-review.md §3.2, D-12; architecture.md §5.
- Contract Trace: INT-02, GOAL-06, FA-08.

---

<a id="arch-d09"></a>
## ARCH-D09 · Activation of the Reactive Incremental Engine

- Status: Accepted
- Context: `CoalescingScheduler.Checkpoints()` channel is pushed to by `NotifyEdit` but never consumed by any goroutine, leaving background delta re-indexing completely dead.
- Decision: Wire `internal/watch` to `WorkspaceService` and spawn a background consumer worker listening to `scheduler.Checkpoints()` to trigger automated compilation and broadcast `generation.published` SSE events.
- Rejected Alternative: Rely solely on synchronous on-demand compilation during manual HTTP GET queries.
- Rationale: Sub-second reactive feedback is a core product capability. The compilation loop must be automatic.
- Consequences: Code edits automatically generate updated semantic flows within 2–3 seconds without user manual refresh.
- Source / Evidence: architectural-maturity-review.md §2.2, D-13; architecture.md §1.
- Contract Trace: INT-02, GOAL-07, FA-09.

---

<a id="arch-d10"></a>
## ARCH-D10 · Egress Credential Scrubbing & Loopback Origin Hardening

- Status: Accepted
- Context: Outbound HTTP, SSE, and MCP streams contain zero secret redaction. FlowView Host/Origin checks use `strings.HasPrefix("localhost")`, vulnerable to `localhost.attacker.com` DNS rebinding.
- Decision: Wrap all outgoing response writers in `secret.RedactJSON`. Replace prefix string comparisons with strict `url.Parse` validation enforcing loopback hosts (`127.0.0.1`, `[::1]`, `localhost`).
- Rejected Alternative: Rely on client-side frontend sanitization.
- Rationale: Server-side defense-in-depth ensures credentials never leave the local boundary, and loopback enforcement blocks cross-origin exploitation.
- Consequences: Outbound payloads are stripped of Bearer tokens, private keys, and credentials. CSRF attacks are blocked.
- Source / Evidence: architectural-maturity-review.md §4.2, §4.3, D-01, D-02; architecture.md §1, §3.3.
- Contract Trace: INT-03, GOAL-08–GOAL-10, FA-10, FA-11.

---

<a id="arch-d11"></a>
## ARCH-D11 · Go Adapter Slicing & Struct Method Normalization

- Status: Accepted
- Context: `adapters/go/main.go` skips `fn.Recv != nil` (discarding all struct methods) and returns a hardcoded 1-step dummy slice with 0 edges.
- Decision: Re-architect the Go adapter using `go/parser` and call graph extraction to parse struct receiver methods and construct discrete statement-level call slices.
- Rejected Alternative: Exclude Go from supported slicing targets and keep it discovery-only.
- Rationale: Go is CodeFlow's primary implementation language and must have first-class feature slicing parity with TypeScript and Dart.
- Consequences: CodeFlow can slice and visualize Go backend services and idiomatic struct methods.
- Source / Evidence: architectural-maturity-review.md §4.2, D-09; architecture.md §4.
- Contract Trace: INT-04, GOAL-12, FA-12.

---

<a id="arch-d12"></a>
## ARCH-D12 · Process Pool Bounding & Process Group Isolation

- Status: Accepted
- Context: `protocol.Pool` tracks only idle workers, spawns unbounded processes under load, leaks subprocesses on non-crash retry failures, and fails to set `Setpgid: true`, leaving orphaned zombie processes.
- Decision: Enforce `maxActive` bounds on `protocol.Pool`, configure `SysProcAttr: &syscall.SysProcAttr{Setpgid: true}`, terminate process groups using negative PIDs (`-cmd.Process.Pid`), and guarantee broken retry connections are explicitly closed.
- Rejected Alternative: Rely on OS init (PID 1) process reaper to clean up orphaned workers.
- Rationale: Uncontrolled subprocess creation and leaking processes cause host exhaustion and degraded analysis performance.
- Consequences: Language adapter workers are strictly bounded and reliably reaped upon shutdown or failure.
- Source / Evidence: architectural-maturity-review.md §4.1, D-08; architecture.md §3.3.
- Contract Trace: INT-04, GOAL-13, FA-13.
