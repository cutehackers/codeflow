# CodeFlow Goal-Driven Implementation Tickets

Status: implemented and verified for local use
Source contract: [CodeFlow Production Design Specification](./codeflow-production-design-ko.md)

## How to use this backlog

Each ticket is a tracer-bullet goal. Completion means that the stated user or system capability works end to end and can be demonstrated with the listed evidence. Completing isolated packages, interfaces, or mocks without the integrated outcome does not complete a ticket.

### Shared completion contract

- Product repositories remain read-only. CodeFlow may write only its configuration when explicitly requested and its own knowledge, flow, runtime, and cache locations.
- Current filesystem bytes and Git state are authoritative. Session content and watcher events are never the fact ledger.
- An `observed` behavior has current code, config, test, or contract evidence. Missing evidence produces `unknown`, `unavailable`, or a typed error.
- Deterministic FlowIR facts remain physically separate from semantic text and publication metadata.
- Every AFK ticket includes focused automated tests and a repeatable verification command or demo.
- Tests assert schemas, invariants, and behavior. They do not introduce a maintained Golden Flow corpus or full-page HTML snapshots.
- No ticket may weaken an existing trust state, hash check, unknown boundary, or product-code read-only guarantee.
- A ticket is complete only when its public path no longer depends on an unowned placeholder. Test doubles are allowed only at boundaries explicitly named in the ticket.

---

## CF-G01 — Diagnose whether CodeFlow can analyze a repository

**Type:** AFK
**Blocked by:** None
**Status:** Completed

### Goal

A developer can run `codeflow doctor` and receive an actionable, read-only assessment of whether the current repository is ready for CodeFlow.

### What to build

Deliver the smallest runnable Go CLI and configuration path that identifies the repository, validates the supported `codeflow.yaml` contract, checks Git, CodeGraph HTTP capabilities, the Dart or Flutter SDK, and adapter compatibility, and reports each dependency independently.

### Acceptance criteria

- [x] `codeflow doctor` works with no configuration and with a valid versioned configuration.
- [x] Exact logical entry points and configured feature aliases validate against the contract; unknown schema versions stop with remediation.
- [x] Git, CodeGraph, Dart/Flutter, configuration, and version failures are distinguishable typed results.
- [x] Human-readable and machine-readable output identify what is ready, unavailable, or incompatible without claiming analysis succeeded.
- [x] Running the command leaves the inspected product repository and Git state unchanged.

### Completion evidence

- A repeatable integration test runs `doctor` against ready, partially configured, and incompatible fixture repositories.
- The project build and focused doctor/config tests pass from a clean checkout.

### Non-goals

Flow compilation, persistent Core process, browser UI, or automatic dependency installation.

---

## CF-G02 — Open a complete fixture-backed flow

**Type:** AFK
**Blocked by:** CF-G01
**Status:** Completed

### Goal

A developer can open one trustworthy CodeFlow timeline end to end, establishing the complete product walking skeleton before real analyzer integration.

### What to build

Use a controlled fixture fact provider to drive the production FlowIR, validation, storage, authenticated loopback API, and minimal FlowView paths. The fixture represents one route with a user action, system action, visible result, and current code evidence.

### Acceptance criteria

- [x] Versioned schemas cover the deterministic FlowIR types and reject the specification’s invalid trust/evidence combinations.
- [x] Canonical FlowIR content is byte-identical for identical inputs while publication time and runtime status live outside it.
- [x] A repository-scoped Core publishes the fixture flow transactionally to SQLite in WAL mode.
- [x] The loopback-only API requires the runtime token and returns the common response envelope.
- [x] A minimal vertical FlowView shows basis, ordered steps, trust state, and the anchored code range.
- [x] Stale locks are recoverable using both PID and repository fingerprint.

### Completion evidence

- One integration test launches Core, requests the flow, loads the page, and asserts semantic DOM content without a full-page snapshot.
- Schema conformance and deterministic serialization tests pass.

### Non-goals

Real CodeGraph or Dart analysis, Git baseline comparison, file watching, or production packaging.

---

## CF-G03 — Show the authoritative current worktree

**Type:** AFK
**Blocked by:** CF-G02
**Status:** Completed

### Goal

The flow screen and API identify the exact current repository state from real filesystem and Git evidence, even when no watcher or session history exists.

### What to build

Replace the walking skeleton’s synthetic basis with a real manifest, Git classification, exact hashing, exclusions, and consistent snapshot publication. Surface revision, dirty state, and analysis status through the existing CLI, API, and FlowView path.

### Acceptance criteria

- [x] Manifest entries use raw-byte SHA-256, repository-relative normalized paths, file type/mode, Git state, and generated-file classification.
- [x] Git classification covers clean, added, modified, deleted, renamed, and untracked files; ambiguous rename is deletion plus addition.
- [x] Symlinks are not followed and hash their link target; default secret, build, tool, and generated exclusions apply.
- [x] Relevant hashes are reread before publication; one concurrent change retries and continued mutation preserves the last consistent snapshot with `analyzing` status.
- [x] The CLI, API, and FlowView present the same `FlowBasis` and never mix files from different observations.

### Completion evidence

- A temporary Git repository test exercises every Git state and a mid-analysis mutation.
- Cache deletion followed by analysis reconstructs the same deterministic basis.

### Non-goals

Watcher scheduling, graph indexing, semantic call resolution, or baseline FlowDelta.

---

## CF-G04 — Resolve one `go_router` entry point without guessing

**Type:** AFK
**Blocked by:** CF-G03
**Status:** Completed

### Goal

A developer can name a Flutter feature and see the uniquely resolved route entry point or a clear candidate list when resolution is ambiguous.

### What to build

Run the Dart adapter as a versioned JSON-RPC child process and use it to discover supported `go_router` entry points. Connect exact IDs, configured aliases, and unique discovered shorthand to the existing flow request and view.

### Acceptance criteria

- [x] The adapter supports initialize, capability negotiation, request correlation, cancellation/deadline, shutdown, and cleanup with versioned request/result/error schemas.
- [x] `route:/signup`, a configured `signup` alias, and a uniquely discovered `signup` shorthand resolve to the same logical entry point.
- [x] Zero matches show available entry points; multiple matches show candidates and do not auto-select.
- [x] Every discovered entry point has a current source anchor and stable logical `flow_id`.
- [x] Adapter absence, incompatibility, malformed output, and timeout remain typed unavailable or unknown results in CLI, API, and FlowView.

### Completion evidence

- Adapter contract tests cover success and every declared failure class.
- An integration fixture demonstrates exact, configured, unique, missing, and ambiguous selectors.

### Non-goals

Full call ordering, Riverpod state analysis, external effects, or heuristic winner selection.

---

## CF-G05 — Trace one real Dart route to a visible result

**Type:** AFK
**Blocked by:** CF-G04
**Status:** Completed

### Goal

A developer can follow one real Flutter route from its user entry action through typed Dart calls to a visible route or UI result, with current code evidence at every observed step.

### What to build

Connect the versioned CodeGraph HTTP adapter and targeted Dart semantic refinement to the existing compiler path. Produce a minimal causal flow and architecture slice for one supported route, folding helpers and rejecting stale relationships.

### Acceptance criteria

- [x] CodeGraph status, tool discovery, query, indexing, pagination, job polling, deadline, cancellation, and error behavior conform to versioned fixtures.
- [x] Core validates graph paths, symbols, revision scope, file hashes, and spans against the current worktree before using a relationship.
- [x] Dart refinement resolves actual call targets only for the affected graph slice.
- [x] The compiler emits causal steps for the user trigger, meaningful processing, and visible result while folding helpers, mappers, and loggers.
- [x] Every observed step passes the Evidence Gate and exposes no more than three current primary anchors with 5–20 line code lenses.
- [x] Missing or stale graph evidence is visible as `unknown` or `STALE_GRAPH`, never as a completed observed path.

### Completion evidence

- A real small Flutter fixture is indexed and produces the expected invariant-valid route flow through CLI, API, and FlowView.
- Mutating or deleting an anchored source file invalidates the affected fact on the next reconcile.

### Non-goals

Riverpod state transitions, complex branches, repository boundaries, baseline comparison, or semantic prose.

---

## CF-G06 — Explain a Riverpod state change in the flow

**Type:** AFK
**Blocked by:** CF-G05
**Status:** Completed

### Goal

A developer can see which Riverpod dependency and Notifier operation changes application or screen state during the selected user flow.

### What to build

Extend the targeted Dart analysis and causal compiler path for one supported `ref.watch` or `ref.read` dependency and one `Notifier` or `AsyncNotifier` state transition, presenting the change and its code lens in the existing timeline.

### Acceptance criteria

- [x] Provider dependencies resolve to canonical Dart symbols with current anchors.
- [x] The supported state assignment or transition becomes a causal state-change fact and step result.
- [x] Async loading, data, or error transitions are ordered only when control-flow evidence establishes the order.
- [x] Unsupported Riverpod patterns appear at the correct timeline position as `unknown`.
- [x] FlowIR, API, CLI, and FlowView preserve the same trust state and evidence.

### Completion evidence

- A Flutter fixture demonstrates one synchronous and one asynchronous Notifier path.
- Focused tests prove session text cannot create or upgrade a state-transition fact.

### Non-goals

Exhaustive Riverpod API coverage or inferred business intent.

---

## CF-G07 — Show an important branch and unresolved path honestly

**Type:** AFK
**Blocked by:** CF-G05
**Status:** Completed

### Goal

A developer can see an important condition, its evidence-backed outcomes, and exactly where an unresolved dynamic path prevents certainty.

### What to build

Extend one route flow with a meaningful condition and multiple outcomes. Preserve structural ordering and represent dynamic dispatch or missing relations as an inline unknown rather than completing the branch speculatively.

### Acceptance criteria

- [x] A branch cannot enter FlowIR without a condition fact and current condition anchor.
- [x] Outcome ordering and `Branch.id` follow the deterministic contract without line-number or prose inputs.
- [x] A visible result requires UI-state or route evidence.
- [x] Dynamic dispatch with no unique target creates a reasoned `unknown` associated with the affected step.
- [x] FlowView expands the branch vertically and visually separates observed and unknown outcomes.

### Completion evidence

- A fixture demonstrates one fully observed outcome and one unresolved outcome.
- Schema and compiler tests reject anchorless branches and guessed callees.

### Non-goals

Whole-program control-flow completeness or graphical all-branch maps.

---

## CF-G08 — Follow a flow to repository and external boundaries

**Type:** AFK
**Blocked by:** CF-G05
**Status:** Completed

### Goal

A developer can see where a user flow reads or writes a repository and where it crosses into an external system, without CodeFlow inventing behavior beyond available contracts.

### What to build

Recognize one repository access and one external API call in the selected graph slice, connect them causally to the existing timeline and ArchitectureSlice, and terminate unsupported external behavior with an explicit unknown.

### Acceptance criteria

- [x] Repository read/write and external call facts contain canonical targets and current evidence.
- [x] ArchitectureSlice includes only the relevant application, domain, data, and external boundaries and relations.
- [x] A present contract may support an observed boundary result; absence of a contract ends the path with `EXTERNAL_BOUNDARY_UNKNOWN`.
- [x] Network execution is never used to infer runtime external behavior.
- [x] CLI, API, and FlowView show the same boundary and uncertainty.

### Completion evidence

- A fixture demonstrates a repository call followed by a contract-backed external call and a missing-contract variant.
- Tests prove external behavior cannot become observed from session assertions alone.

### Non-goals

Calling production APIs, distributed tracing, or modeling the remote system internally.

---

## CF-G09 — Compare current behavior with an immutable Git baseline

**Type:** AFK
**Blocked by:** CF-G05
**Status:** Completed

### Goal

A developer can see what behavior changed from a selected Git revision without checking out that revision or reading a noisy file diff.

### What to build

Resolve a baseline to a commit SHA, materialize its read-only analysis mirror from Git objects, compile the same feature against both bases, and show deterministic added, removed, changed, and newly unknown behavior inline.

### Acceptance criteria

- [x] Baseline input resolves to an immutable commit SHA; missing or invalid revisions fail with a typed error.
- [x] The mirror is built under CodeFlow cache without checkout, worktree mutation, or automatic network dependency fetch.
- [x] Baseline anchors use commit/blob evidence; missing local dependencies produce unknown rather than a guessed flow.
- [x] Matching is one-to-one within duplicate `behavior_key` buckets using symbol then structural fingerprint; ambiguity becomes deletion plus addition.
- [x] FlowDelta reports added/removed steps, changed results, changed branches, and new unknowns through CLI, API, and inline FlowView presentation.
- [x] Repeating the comparison with identical bases produces identical deterministic FlowIR and delta.

### Completion evidence

- An integration repository with two commits proves the worktree remains untouched and the expected invariant-level delta is produced.
- Tests cover duplicate keys, ambiguous matching, deleted files, and unavailable baseline dependencies.

### Non-goals

Golden expected-flow storage, branch history visualization, or LLM-assisted matching.

---

## CF-G10 — Keep an open flow current while files change

**Type:** AFK
**Blocked by:** CF-G03, CF-G05
**Status:** Completed

### Goal

A developer’s open FlowView updates after relevant source changes while preserving reconcile-first correctness and the last consistent result.

### What to build

Treat file events as debounced change notifications, run a full authoritative reconcile before impact calculation, recompile affected flows, and notify the existing page only after transactional publication.

### Acceptance criteria

- [x] Events only schedule work and are never persisted or interpreted as source facts.
- [x] Reconcile precedes reverse-dependency impact calculation and compilation.
- [x] Only affected open flows recompile after a consistent snapshot is available.
- [x] Changes during analysis follow the one-retry/last-consistent-snapshot rule and show `analyzing` status.
- [x] A missed event is recovered by `refresh`, `analyze`, `open`, or the next scheduled reconcile.

### Completion evidence

- An integration test modifies, deletes, renames, and rapidly rewrites fixture files while observing page/API state.
- The test proves no published flow combines hashes from different observations.
- A recursive watcher integration proves tool/cache changes are ignored, product
  changes schedule work, a persistent Dart Analyzer session is reused, and the
  open page reloads only after the publication identity changes.

### Non-goals

Using watcher history for rename reconstruction or running a second analysis daemon.

---

## CF-G11 — Give agents the same trustworthy flow through both MCP eras

**Type:** AFK
**Blocked by:** CF-G05
**Status:** Completed

### Goal

An agent using either supported MCP era can request the same Core-produced flow, evidence, and unknowns without launching a competing analysis engine.

### What to build

Expose the specified CodeFlow tools over stdio with modern `2026-07-28` and legacy `2025-11-25` negotiation paths that converge on one handler and one repository-scoped Core.

### Acceptance criteria

- [x] Both eras expose current, diff, step, unknowns, refresh, and open operations using the common response envelope.
- [x] The client’s first request selects the era; unsupported versions receive an actionable compatibility error.
- [x] Both eras return semantically identical deterministic data for the same FlowBasis.
- [x] Multiple MCP processes reuse the valid repository Core and do not compete for SQLite or the Dart adapter.
- [x] MCP preserves observed, inferred, confirmed, unknown, stale, and unavailable distinctions.

### Completion evidence

- Protocol conformance tests run one client for each supported era against the same Core.
- A concurrency test proves multiple MCP clients observe one repository runtime and snapshot.

### Non-goals

Remote MCP hosting, agent-specific flow analysis, or protocol-era-specific business logic.

---

## CF-G12 — Add reviewed intent without changing observed behavior

**Type:** AFK
**Blocked by:** CF-G05, CF-G07
**Status:** Completed

### Goal

A developer can view optional inferred intent, approve useful names or descriptions, and know that these overlays cannot change the code-derived flow.

### What to build

Import an authorized JSONL or transcript export into the Micro Ontology, filter secret-bearing events, create inferred semantic overlays or intent candidates, and persist only human-confirmed knowledge separately from deterministic facts.

### Acceptance criteria

- [x] Only the allowed event classes are normalized; raw transcripts are not persisted by default.
- [x] Secret-bearing events are excluded from semantic processing and storage.
- [x] Session evidence can create inferred overlays and intent candidates but cannot create or alter facts, branches, stable IDs, or FlowDelta.
- [x] FlowView visually separates inferred text and supports explicit approval to `confirmed`.
- [x] Only approved names and intent persist in `.codeflow/knowledge`; deleting all overlays leaves deterministic FlowIR unchanged.

### Completion evidence

- The same worktree analyzed with and without session input produces identical FactSet and BehaviorFlow.
- Tests demonstrate approval persistence and secret exclusion without storing raw source material.

### Non-goals

LLM-required compilation, autonomous business-intent confirmation, or live collection from unsupported tools.

---

## CF-G13A — Prove the CodeGraphContext bridge against a real repository

**Type:** AFK
**Blocked by:** CF-G05
**Status:** Completed

### Goal

CodeFlow only declares CodeGraph ready when the running public HTTP service can
index and query the selected repository through the actual advertised tool
schemas, and returns evidence that Core can validate against the current
worktree.

### What to build

Replace the fixture-only relationship invocation with a versioned
CodeGraphContext bridge adapter. Discover the tool schemas at runtime, map
CodeFlow's repository/entry-point request to the compatible public operations,
and re-anchor graph results in Core before they become observed facts. A
service that merely exposes similarly named tools is incompatible unless this
bridge succeeds for the selected repository.

### Acceptance criteria

- [x] The compatibility probe validates required tool input/output schemas, not
  only tool names, and reports a typed incompatible state with remediation.
- [x] Repository discovery, explicit indexing request, asynchronous job polling,
  pagination, deadline, and cancellation conform to captured real-service
  fixtures.
- [x] Core never treats unanchored CodeGraph symbols or snippets as observed;
  it deterministically creates current source anchors or returns an unknown.
- [x] A real CodeGraphContext service indexes a temporary Dart/Flutter fixture
  and supplies a route-relevant relationship slice accepted by the Evidence
  Gate.
- [x] `doctor`, `analyze`, `compare`, API, and FlowView surface the same typed
  unavailable/incompatible/indexing states.

### Completion evidence

- Contract fixtures are captured from the supported CodeGraphContext release.
- An integration test starts the real service, indexes the fixture, compiles a
  flow, and proves stale source/graph data cannot publish an observed fact.

### Non-goals

Changing CodeGraphContext itself, or silently falling back to a synthetic
graph response for a real repository.

---

## CF-G13B — Supply a Dart-capable structural graph bridge

**Type:** AFK
**Blocked by:** CF-G13A
**Status:** Completed

### Goal

CodeFlow can obtain a current, evidence-valid structural relationship slice for
supported Dart/Flutter routes when the external CodeGraphContext service does
not support Dart.

### What to build

Provide an owned local Dart structural graph bridge behind the same narrow
CodeGraph boundary. It may report only syntactically provable, bounded route,
method-call, provider, repository, and external-boundary relationships. Every
relationship must carry raw-byte file hash, span, symbol, and current revision;
unsupported constructs must remain typed unknowns. The external service remains
preferred when it is compatible and Dart-capable.

### Acceptance criteria

- [x] Backend selection is explicit and visible in doctor/API/FlowView; an
  incompatible external service never silently becomes a claimed external
  analysis success.
- [x] The owned bridge derives relationships from actual current Dart source,
  never session text or fixture-specific responses.
- [x] Dynamic dispatch, computed route strings, unparseable files, and missing
  targets become typed unknowns rather than guessed edges.
- [x] Every owned bridge anchor passes the same Core manifest/hash/span/revision
  validation as an external CodeGraph response.
- [x] A real Flutter repository with a const-defined `GoRoute` can produce the
  minimal route slice required for CF-G13 without writing project source.

### Completion evidence

- Unit and integration tests cover const route resolution, direct calls,
  unsupported dynamic dispatch, stale file rejection, and external-service
  preference.
- The supplied `sgp-981-app` target produces a current, evidence-backed route
  slice without a synthetic CodeGraph response.

### Non-goals

Whole-program Dart analysis, execution, or claiming behavior beyond statically
anchored source relationships.

---

## CF-G13C — Follow static destination seams to a visible route

**Type:** AFK
**Blocked by:** CF-G13B
**Status:** Completed

### Goal

CodeFlow can preserve a feature package's dependency boundary while proving a
visible route result through an application-owned, statically exhaustive
`RouteDestination` resolver.

### What to build

Extend the owned structural graph only for the bounded pattern `dispatcher.go(
const Destination())` → concrete dispatcher → exhaustive resolver switch →
literal or same-worktree constant route. Keep all other dispatchers, dynamic
destinations, interpolated paths, and incomplete resolver cases unknown.

### Acceptance criteria

- [x] The selected destination constructor, dispatcher implementation, resolver
  switch arm, and resulting route each carry current source anchors.
- [x] The resolver must be statically exhaustive for the selected destination;
  duplicate or ambiguous arms fail closed.
- [x] Const path aliases resolve only inside the captured worktree and are
  revalidated against the manifest.
- [x] The real `sgp-981-app` `/join` route shows its direct confirmed outcomes
  without treating unrelated provider-state conditions as runtime facts.
- [x] Dynamic and unrecognized destination seams remain explicit unknowns.

### Completion evidence

- Tests cover the complete seam, ambiguous/missing resolver cases, stale
  anchors, and code-lens evidence.
- The supplied target produces an observed visible result where the source
  proves one, plus explicitly unknown conditional outcomes where it does not.

### Non-goals

Interprocedural execution, resolving arbitrary dependency-injection systems,
or inferring paths from documentation prose.

---

## CF-G13D — Serve a reviewed real flow without packaging it

**Type:** AFK
**Blocked by:** CF-G13C
**Status:** Completed

### Goal

A reviewer can keep a real, already-compiled FlowView running locally and
inspect its CurrentFlow, branches, code lenses, architecture boundaries, and
unknowns before the installed `open` journey is packaged.

### What to build

Expose a bounded `codeflow serve` command that resolves and compiles one
selector, starts the existing authenticated loopback Core, prints the local
review URL, and stays alive until interrupted. It is not the packaged `open`
experience and must not launch a browser, change product source, or claim
success before compilation completes.

### Acceptance criteria

- [x] `serve` uses the same `StartAnalysis` Core path and FlowIR as `analyze`.
- [x] It prints a loopback review URL only after transactional publication;
  shutdown releases runtime resources safely.
- [x] The page/API show the same branch, current code lenses, backend boundary,
  and unknowns as the CLI document.
- [x] Failure returns typed diagnostics and does not leave a stale runtime
  owner or product source mutation.

### Completion evidence

- An integration command against `sgp-981-app` `/join` retrieves the live
  FlowView/API and semantically verifies the conditional `/home` branch plus
  its unknown alternative.

### Non-goals

Browser launching, installation, versioned release packaging, or daemon
management beyond the one foreground process.

---

## CF-G13E — Trace a bounded notifier event through a listener result

**Type:** AFK
**Blocked by:** CF-G13C
**Status:** Completed

### Goal

CodeFlow follows a direct, statically provable Riverpod event-to-state
assignment into a same-widget listener guard and its literal route result,
without treating arbitrary asynchronous provider behavior as observed.

### What to build

Support the bounded pattern `dispatch(Provider, const Event())` → matching
`Notifier` event case → direct `state = AsyncData(State(flag: true))` →
`ref.listen` condition on that flag → literal or statically resolved route.
All cross-provider, await-dependent, mutable, non-exhaustive, or ambiguous
cases remain unknown.

### Acceptance criteria

- [x] Every event, controller assignment, listener condition, and result has a
  current anchor and one causal ordering chain.
- [x] Event handling or state construction with side effects, multiple matching
  handlers, or ambiguous provider bindings fails closed to unknown.
- [x] The real `/join` cancellation path shows the confirmed result `authPath`
  only after the explicit dialog-confirmed `JoinCancelEvent` path.
- [x] Existing unsupported Riverpod patterns remain unknown and cannot be
  upgraded merely because adjacent text resembles a state assignment.

### Completion evidence

- Compiler/adapter tests cover the bounded chain and all fail-closed cases.
- The supplied project FlowView shows the conditional `/home` outcome and the
  separately observed cancellation-to-auth outcome with their current lenses.

### Non-goals

General Riverpod control-flow analysis, executing event handlers, or resolving
asynchronous repository effects.

---

## CF-G13F — Render verified source code lenses for review

**Type:** AFK
**Blocked by:** CF-G13E
**Status:** Completed

### Goal

A FlowView reviewer sees the actual current 5–20-line source context that
explains each primary evidence anchor, rather than only a path and line range.

### What to build

Build source lenses from the published FlowIR basis manifest and each anchor's
range. Verify the current raw file hash before rendering; return a typed stale
or unavailable lens rather than reading changed content as evidence. Render
semantic code snippets for timeline and branch evidence in FlowView and expose
the same lens data through the authenticated API.

### Acceptance criteria

- [x] Every displayed observed/unknown primary anchor receives a bounded 5–20
  line lens including the anchored range and deterministic context window.
- [x] Source outside the captured manifest, changed source, invalid ranges, and
  unreadable files produce a typed lens state without exposing unverified text.
- [x] Lens output never contains runtime tokens or unrelated repository files.
- [x] Timeline/branch DOM ordering and trust labels remain intact while the
  rendered snippet identifies the route/action/condition code directly.

### Completion evidence

- Tests cover window boundaries, stale/missing source, path traversal refusal,
  and DOM/API consistency.
- Live `sgp-981-app` `/join` FlowView visibly renders the cancellation,
  condition, controller-state, and `/auth` source lenses.

### Non-goals

Full source browsing, editing, syntax highlighting, or a project file viewer.

---

## CF-G13 — Validate that one real Flutter feature is understandable

**Type:** HITL
**Blocked by:** CF-G06, CF-G07, CF-G08, CF-G09, CF-G10, CF-G13A, CF-G13B, CF-G13C, CF-G13D, CF-G13E, CF-G13F
**Status:** Completed

### Goal

A real user confirms that CodeFlow makes one production Flutter feature faster and safer to explain from current code.

### What to build

Run CodeFlow against one agreed real feature and prepare the CurrentFlow, FlowDelta, code lenses, architecture boundaries, and unknowns for direct review. Convert discovered defects into follow-up tickets rather than encoding an expected Golden Flow.

### Acceptance criteria

- [x] The user confirms the starting action and final visible result are correct.
- [x] The user confirms important branches and state transitions are present and correctly ordered.
- [x] The user confirms default code lenses expose the code that actually explains each step.
- [x] The user confirms FlowDelta has useful signal without file-diff noise.
- [x] Every uncertain or out-of-scope boundary remains visibly unknown.
- [x] The same feature reconstructed without session evidence has the same FactSet and BehaviorFlow.

### Completion evidence

- A recorded approval outcome lists pass/fail for the six criteria and links any resulting defect tickets.
- No expected-flow fixture or full-page snapshot is committed.

### Non-goals

Large usability studies, measuring individual developers, or validating every repository feature.

---

## CF-G14 — Deliver the one-command installed experience

**Type:** AFK
**Blocked by:** CF-G11, CF-G13
**Status:** Completed — local package and runtime reuse verified; release signing or Homebrew distribution is explicitly out of scope for now

### Goal

A macOS user can install CodeFlow and run `codeflow open <feature>` as the complete default journey without learning daemon operations.

### What to build

Package the Core, Dart adapter, and FlowView assets so `open` diagnoses prerequisites, starts or reuses the repository runtime, reconciles, compiles, serves, and opens the selected flow with compatible component versions.

### Acceptance criteria

- [x] A relocatable local package installs without modifying product source files; signed release and Homebrew distribution remain deferred by explicit decision.
- [x] `codeflow open <feature>` starts or reuses Core, validates dependencies, resolves the feature, publishes the flow, and opens its authenticated local view.
- [x] `serve` remains available for explicit persistent control, while `analyze`, `diff`, and `refresh` work without watcher history.
- [x] Core, schema, CodeGraph contract, Dart adapter, and packaged assets participate in the version handshake.
- [x] Installation, update, launch, or compatibility failure leaves no false ready state and provides remediation.

### Completion evidence

- A clean-machine or isolated macOS installation test completes the default journey against the validated fixture.
- Upgrade and incompatible-version tests prove safe failure and cache rebuild behavior.

### Non-goals

Windows/Linux distribution, cloud hosting, or automatic project dependency changes.

---

## CF-G15 — Package the trustworthy agent workflow

**Type:** AFK
**Blocked by:** CF-G11, CF-G12, CF-G14
**Status:** Completed — one-shot local marketplace installation, automatic first-request Core startup, and refresh hook verified

### Goal

A team can install one CodeFlow plugin that guides agents to use current evidence, behavioral deltas, and unknowns consistently.

### What to build

Package a thin Skill, MCP configuration, Core discovery, FlowView assets, and optional supported session hook. The workflow delegates all analysis to the installed Core and preserves its trust model in agent-facing results.

### Acceptance criteria

- [x] The Skill uses current flow for understanding, diff for review, and opens FlowView only when requested.
- [x] Agent responses preserve trust states and explicitly carry unknowns rather than completing them with model inference.
- [x] One installer places the paired Core and adapter in a predictable user location, registers the local marketplace through Codex CLI, and activates the plugin.
- [x] The plugin uses the installed executable by default; `CODEFLOW_BIN` remains an optional override for a non-default location.
- [x] The first requested flow starts or reuses one compatible Core without a separate terminal command.
- [x] Optional session hooks only request refresh or import supported evidence; failure does not affect current-state reconstruction.
- [x] The package contains no duplicate scanner, compiler, evidence, or delta implementation.

### Completion evidence

- An installation test exercises current, diff, unknowns, and open through the packaged MCP configuration.
- A sessionless run and a hook-failure run both produce the same deterministic current flow.

### Non-goals

Replacing Core, forcing session-log collection, or allowing an agent to edit the analyzed product repository.

---

## CF-G16 — Make causal state changes reviewable in FlowView

**Type:** AFK
**Blocked by:** CF-G06, CF-G07, CF-G09, CF-G13F
**Status:** Completed — locally verified against `sgp-981-app` `/join`

### Goal

A developer can explain why code state changes, what user-visible result follows,
and which missing evidence still creates cognitive debt without reading the
feature repository from scratch.

### What to build

Promote source-backed causal edges and actionable debt into deterministic
FlowIR, use Dart Analyzer AST/resolved symbols for supported relationships,
show code state/change in the existing monochrome timeline foundation, and add
repository-owned flow expectations for local or CI verification.

### Acceptance criteria

- [x] Event, provider operation, state assignment, listener, condition, and
  visible result can be connected by deterministic, evidence-backed causal
  edges without changing timeline order.
- [x] Every unresolved causal boundary states its impact, completion criteria,
  and useful next evidence; review state is stored outside canonical FlowIR and
  only disappears automatically when the unknown disappears from current code.
- [x] The existing black-and-white FlowView keeps previous/next controls above
  the timeline, exposes per-step source state and behavioral change, and shows
  the current code lens beside incoming causes and outgoing results.
- [x] Supported Dart structures use Analyzer AST and resolved symbols; bounded
  unsupported framework shapes remain explicit unknowns.
- [x] `codeflow verify` can enforce required visible results, causal relation
  kinds, allowed debt reasons, and an open-debt budget without promoting
  inferred behavior to observed.

### Completion evidence

- `route:/join` produces nine ordered steps, two complete observed branches,
  observed `/home`, no-navigation, and `/auth` results, with zero open unknowns.
- The real local FlowView was inspected and previous/next navigation selected
  the matching timeline panel and current source lens.
- An explicit local expectation contract verifies the real target without
  writing product source.
- Focused model, store, compiler, API, CLI, and FlowView tests pass.

### Non-goals

Runtime/session event streaming, product-source editing, visual color themes,
deployment, release signing, or marketplace distribution.

---

## CF-G17 — Review several screen flows in one coherent workspace

**Type:** AFK
**Blocked by:** CF-G10, CF-G13, CF-G16
**Status:** Completed — implemented and locally verified (2026-08-19)

### Goal

A developer can request several related screens, understand their observed
screen-to-screen routes, and inspect each screen's detailed code → state → result
timeline without mixing different worktree snapshots or flattening all steps into
one unreadable sequence.

### What to build

Implement the approved
[`Multi-flow Workspace`](./design/flowview-multi-flow-workspace-ko.md): capture one
Basis, reuse one Dart Analyzer session, compile a bounded set of selectors,
atomically publish a batch of independently valid FlowIR documents, and render a
screen flow map above the selected flow's architecture and vertical timeline.

The vertical timeline preserves the current monochrome FlowView grammar: circular
trust markers, thin causal connectors, state-change rings, broken connectors for
exclusive outcomes, explicit `분기 N · 경로 A/B`, and previous/next controls at the
detail-card header level.

### Acceptance criteria

- [x] A public CLI accepts repeated selectors and rejects empty, duplicate, or
  ambiguous selectors with typed remediation.
- [x] All requested flows share the exact repository, revision, manifest and
  worktree fingerprint; a changing workspace never publishes a mixed batch.
- [x] Compilation shares manifest capture and one initialized Dart Analyzer
  session while preserving per-flow validation and unknown boundaries.
- [x] Batch publication is atomic: readers see the complete previous or complete
  new flow set, never a partial set.
- [x] Only an observed visible result and a same-Basis current entry point create
  an observed cross-flow edge.
- [x] The screen flow map selects one flow; the architecture map, vertical
  timeline, detail card, branch outcomes, and previous/next controls share one
  bidirectional selection state.
- [x] `VS Code에서 열기` remains a primary detail action and follows that shared
  selection to the exact verified file, line, and column; stale or unavailable
  lenses expose no editor URI and explain why the source cannot be opened.
- [x] The vertical timeline retains current visual semantics, clearly breaks
  mutually exclusive branches, and gives state-changing steps an unmistakable
  ring without relying on color.
- [x] Single-flow usage remains the same simple command and renders through the
  same reusable workspace components without a separate forked template.
- [x] The workspace works at 320px, 736px and 1024px, with large text, keyboard,
  screen reader and reduced-motion checks.
- [x] Up to three supported flows meet the 25-second local MVP target without
  weakening evidence validation; a timeout preserves the last consistent batch.
- [x] MCP can request and open a flow set while retaining current per-flow tools
  and the same CodeFlow trust semantics in a compact, non-duplicated projection.

### Completion evidence

- Integration fixture requests `/join`, `/home`, and `/auth` against one captured
  Basis and proves batch atomicity, selector order determinism, and no product
  source mutation.
- Semantic DOM tests prove flow-map → architecture → vertical timeline → detail
  linkage in both directions, including returning from the last to first step.
- Editor-action tests prove every selection surface updates one verified
  `vscode://file` target and that stale or mismatched anchors remove the action.
- Visual checks cover the supplied `/join` nine-step/two-branch flow at desktop,
  narrow and large-text sizes.
- `go test ./...`, `go test -race ./...`, `go vet ./...`, and Dart Analyzer pass.

### Non-goals

Merging steps from different flows into one timeline, cross-revision flow maps,
runtime trace collection, product-source editing, deployment, or color themes.

---

## Remaining review backlog after the v0.1 pin

These are the important follow-up tickets that remain after the local 0.1 pin
and the completed tracer-bullet implementation set above.

### CF-G18 — Make FlowView read like a causal review surface

**Type:** HITL
**Blocked by:** None
**Status:** Open

### Goal

A reviewer can scan the screen and immediately understand which code change
caused which state change, which visible result followed, and which branches or
unknowns still need attention.

### What to build

Refine the existing monochrome FlowView so the vertical timeline, architecture
causal map, code lens, and detail card feel like one review surface instead of
separate panels. Keep the current `VS Code에서 열기` action, make state changes
visibly stronger than plain labels, and rewrite unresolved items in user-facing
language.

### Acceptance criteria

- [ ] Timeline selection and architecture selection stay bidirectionally linked.
- [ ] The code view keeps a natural left alignment and makes the current anchor
      line easy to spot.
- [ ] State changes are visually stronger than neutral metadata and are easy to
      compare step to step.
- [ ] Unknowns explain what is missing and what evidence would resolve them in
      plain language.
- [ ] Desktop and mobile views both keep the timeline readable without hiding
      the review controls.

### Completion evidence

- A final design review sample matches the updated review language and layout.
- Browser-level tests prove selection sync, no horizontal overflow, and intact
  code-lens links.

### Non-goals

New analysis capability, deployment, or color-theme exploration.

### CF-G19 — Keep multi-flow workspaces visually consistent

**Type:** AFK
**Blocked by:** None
**Status:** Open

### Goal

When several related screens are requested together, the workspace keeps one
coherent template and one interaction model across all flows.

### What to build

Reinforce the shared workspace shell so repeated selectors, screen switching,
and detail navigation always behave the same way regardless of how many flows
are open. The shell should reuse the same verified layout and not drift into
different ad hoc variants for different flow counts.

### Acceptance criteria

- [ ] Single-flow and multi-flow requests render through the same workspace
      template.
- [ ] Flow selection stays synchronized across the screen map, architecture
      map, timeline, and detail card.
- [ ] Multiple flows reuse one basis and one analyzer session where possible.
- [ ] A mixed snapshot can never appear when the workspace updates.
- [ ] The same visible controls appear in the same places for one flow or many.

### Completion evidence

- A local multi-flow fixture shows the same shell and selection behavior across
  different flow counts.
- Tests cover repeated selectors, switching, and no mixed publication.

### Non-goals

Historical replay, full-screen portfolio views, or per-project theming.

### CF-G20 — Keep `.codeflow/cache` lean and disposable

**Type:** AFK
**Blocked by:** None
**Status:** Open

### Goal

The cache only holds artifacts that materially speed up local reuse, and users
can tell what remains safe to delete.

### What to build

Audit the cache layout, define which artifacts are required for runtime reuse,
and add a cleanup policy for stale or superseded data. The policy should keep
the current flow experience intact while preventing old lenses, obsolete
baselines, and abandoned workspace state from growing without bound.

### Acceptance criteria

- [ ] The cache contents are documented by purpose, not just by file name.
- [ ] Stale or superseded cache entries can be removed without breaking active
      flows.
- [ ] A repeatable size check makes cache growth visible over time.
- [ ] Cleanup preserves the current runtime and open view behavior.
- [ ] The local experience still starts quickly after cache cleanup.

### Completion evidence

- A cleanup or audit command reports what is kept, what can be deleted, and
  why.
- Tests prove cleanup does not corrupt a live runtime or current flow.

### Non-goals

Remote cache syncing, build artifact deduplication, or package-manager cache
policy.

### CF-G21 — Minimize runtime unknowns with explicit evidence

**Type:** HITL
**Blocked by:** None
**Status:** Open

### Goal

Unknowns stay small, readable, and intentional, especially when a result depends
on runtime behavior that static code alone cannot prove.

### What to build

Clarify which unknowns come from runtime-only effects such as dependency
injection, server responses, or dynamic dispatch, and define the smallest
evidence needed to turn each one into a confirmed result. The product should
never pretend that static Dart AST alone can prove runtime truth, but it should
also avoid keeping unknowns around when better evidence is available.

### Acceptance criteria

- [ ] Unknown labels are written in user language instead of protocol jargon.
- [ ] Static evidence and runtime evidence remain separate sources of truth.
- [ ] Any runtime upgrade path is explicit and deterministic.
- [ ] The review surface explains why a step is still unknown and what would
      reduce that uncertainty.
- [ ] The system keeps the number of open unknowns as low as the available
      evidence allows.

### Completion evidence

- A review of a real flow shows fewer, better-explained unknowns than the
  current state.
- Tests prove unknowns are not upgraded without their required evidence.

### Non-goals

Guessing runtime behavior, executing the app for every step, or storing raw
session logs as the primary truth source.

### CF-G22 — Make local setup and first run obvious

**Type:** AFK
**Blocked by:** None
**Status:** Completed — quickstart verified end to end against `testdata/example_app` (2026-08-24)

### Goal

A new local user can install and run CodeFlow without reading the full design
spec first.

### What to build

Add a short, practical quickstart that covers local installation, the verified
`0.1` release line, the core commands for `doctor`, `analyze`, `serve`, and
`open`, and the expectation that deployment is not part of the default path.
Keep the wording concise enough that the first-run path is obvious.

### Acceptance criteria

- [x] README or local usage docs show a minimal install and run sequence.
- [x] The docs explain the local-only workflow and the current release line.
      No distributed `0.1` artifact exists yet, so the docs state the local
      build line (`codeflow version`) and defer the signed release to
      [release-handoff](./release-handoff.md) instead of implying a version
      pin that does not exist.
- [x] The example commands match the verified local usage path.
- [x] A new user can reach a readable FlowView without hunting across multiple
      documents.
- [x] The first-run instructions do not imply deployment is required.

### Completion evidence

- README gained a quickstart (one-shot install or `make build`, `init`,
  `flows`, `publish`, `show`, `serve`) and `docs/local-usage.md` was rewritten
  to the v2 root CLI; the previous text described the CF-G core engine now
  living under `legacy/core` and referenced a nonexistent `make local` target.
- Every documented command was executed against `testdata/example_app`:
  `doctor` all-green, `flows` listed 10 candidates, `publish` created
  generation `gen-1787565406244804000` (10 flows), `show flow-7232d63b96bd6efa
  --json` returned 5 anchored steps, and `serve --port 4569` answered HTTP 200
  with the FlowView page.
- `serve --port N` argument parsing was fixed so the documented flag form
  works (previously only `--port=N` parsed; parse errors were silently
  ignored).

### Non-goals

Release signing, production hosting, or remote deployment instructions.

---

## CF-G24 — High-Performance AST Symbol Table & In-Memory Indexing for Structural Subgraph

**Type:** AFK
**Blocked by:** CF-G13B
**Status:** Backlog

### Goal

Scale the offline `DartStructuralDomainSubgraph` extractor to repositories with tens of thousands of source files by indexing declaration and call-site symbols in memory instead of executing full-file linear regex scans per query.

### What to build

1. Implement an in-memory symbol table cache within `core/internal/codegraph` that records file-to-symbol, class-to-method, and receiver-to-callsite mappings on first scan or worktree change.
2. Invalidate symbol indices only when file fingerprints or Git revisions change.
3. Replace linear multi-file regex matching in caller/callee resolution with instant map lookups while preserving byte-accurate source anchors (`flowir.Anchor`).

### Acceptance criteria

- [ ] Symbol indexing executes lazily on the first structural query or basis change.
- [ ] Subsequent `domain_subgraph` requests execute in sub-millisecond time on large repositories.
- [ ] Modifying a single file invalidates and rebuilds only that file's index entry without dropping repository-wide graph consistency.
- [ ] Preserves byte-range precision and file hash validation for all generated `Anchor` nodes.

### Completion evidence

- Benchmark tests demonstrating <10ms lookup latency on a 1,000+ file mock Dart repository.
- Unit and integration tests verifying identical subgraph outputs between indexed and unindexed traversals.

### Non-goals

Writing persistent index files to disk or replacing the full CodeGraph daemon when running.

---

## CF-G25 — Configurable Multi-Hop Depth & Framework Boundary Filtering

**Type:** AFK
**Blocked by:** CF-G24
**Status:** Backlog

### Goal

Allow domain subgraph traversal to safely expand to deeper depths (e.g. 1-10 hops) without getting polluted by internal Flutter SDK framework boilerplate, low-level HTTP primitives, or synthetic generated code.

### What to build

1. Introduce an extensible boundary filter (`FrameworkFilter` / `Denylist`) in `core/internal/subgraph` that excludes framework-internal calls (e.g. `Widget.createElement`, `ChangeNotifier.addListener`, raw `http.Client.send`) from the high-level business journey.
2. Support configurable traversal depth with automatic cycle detection and pruning when reaching external boundaries.
3. Enhance edge synthesis to surface high-value domain hops (e.g., UI Action -> Riverpod Provider -> Domain Repository -> API Service -> Stream Consumer) clearly.

### Acceptance criteria

- [ ] Queries with `depth > 2` traverse deep domain dependency chains without noise from framework lifecycle internals.
- [ ] Generated code (`*.g.dart`, `*.freezed.dart`) is treated as implementation details and mapped back to user-declared domain models.
- [ ] Cycles in call chains or event loops are detected and pruned deterministically.
- [ ] `domain_subgraph` MCP tool and CLI accept explicit depth arguments up to the safe maximum.

### Completion evidence

- Fixture tests verifying clean 4-hop and 5-hop domain journeys with no SDK boilerplate noise.
- Automated tests confirming cycle termination and consistent node deduplication.

### Non-goals

AST-level dynamic code execution or runtime bytecode instrumentation.

---

### CF-G26 — Review several flows together in one FlowView

**Type:** AFK
**Blocked by:** None
**Status:** Backlog

### Goal

A reviewer can open one local FlowView and read several flows of the current
published generation together — like the legacy workspace page — while every
step keeps its own evidence and no steps are merged across flows.

Today's v2 FlowView reaches other flows only through the Cmd+K switcher,
which replaces the displayed flow one at a time. There is no simultaneous
display and no screen-level view of how the flows connect.

### What to build

Add an opt-in stacked reading mode that renders each selected flow's timeline
sequentially under one page header, and an optional screen-level map of
cross-flow connections derived only from evidence-backed visible-result or
route-transition facts inside the same published generation. One generation is
one Basis by construction, so the legacy mixed-snapshot hazard cannot occur;
the work is presentation plus conservative edge synthesis. Keep the existing
single-flow rendering as the default.

### Acceptance criteria

- [ ] Stacked mode renders two or more flows from one published generation on
      one page with clear per-flow headers and intact ordering.
- [ ] Every step keeps its own anchors, trust labels, and code lens; nothing
      is deduplicated or merged across flow boundaries.
- [ ] Cross-flow edges are drawn only when both endpoints belong to the same
      generation and the connecting fact status is `observed`; unknown facts
      produce no edge.
- [ ] The switcher, `?flow=` deep links, and stacked mode stay consistent
      after republishing: stale flow IDs degrade to an explanatory empty state,
      never a broken page.
- [ ] Stacked mode stays usable at 320px width, with keyboard navigation and
      no horizontal overflow.
- [ ] With stacked mode off, the default single-flow page behaves exactly as
      before.

### Completion evidence

- An integration test publishes the `testdata/example_app` generation and
  asserts the stacked DOM contains both requested flows' headers and their
  ordered timelines.
- Tests prove no cross-flow edge is synthesized from unknown facts or from
  flows outside the active generation.

### Non-goals

Merging steps into one combined timeline, historical replay, cross-revision
flow maps, runtime trace collection, or color themes.

---

## Delivery order and parallelism

The first usable tracer bullet is:

`CF-G01 → CF-G02 → CF-G03 → CF-G04 → CF-G05`

After CF-G05, these goals can proceed independently:

- CF-G06 — Riverpod state
- CF-G07 — branches and unresolved paths
- CF-G08 — repository and external boundaries
- CF-G09 — baseline delta
- CF-G10 — automatic refresh
- CF-G11 — dual-era MCP

CF-G12 adds optional semantic meaning after the deterministic path exists. CF-G13 is the sole mandatory human validation gate. CF-G14 and CF-G15 productize the validated system. CF-G16 deepens the locally validated experience around causal state changes and cognitive-debt closure. CF-G17 extends that proven single-flow experience into an atomic, same-Basis multi-flow workspace. CF-G24 and CF-G25 scale and refine domain subgraph extraction across large codebases. CF-G26 brings the legacy multi-flow reading experience into the v2 FlowView.
