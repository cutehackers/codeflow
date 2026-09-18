---
name: codeflow
description: Use CodeFlow MCP for verified core-flow comprehension, FlowSequence Storyboard generation, and interactive FlowView review across polyglot codebases.
---

# CodeFlow MCP Operating Contract

Operate the installed CodeFlow MCP using the official 8 core tools. Preserve CodeFlow's evidence, identity, freshness, and authority boundaries when translating tool results into an answer.

## Preconditions & Synergy

- **Target Root**: Always pass the project root as `target` (never a feature subdirectory). Project identity and language adapters depend on root files (e.g., `pubspec.yaml`, `package.json`, `go.mod`).
- **CodeGraph Synergy**: When a `.codegraph/` directory exists at repository root, run `codegraph explore "<query or symbol>"` first to locate entry candidate symbols before invoking CodeFlow.
- **Diagnostic Fallback**: If an adapter or toolchain is missing or analysis fails, guide the user to run CLI diagnosis: `codeflow doctor <target>`.
- **Single Session Consistency**: Keep all flow operations on the same MCP server and exact target repository.

## 5 Golden Execution Paths

Select the most direct execution path based on the user's intent:

```
[User Request]
      │
      ├─► 1. Flow Comprehension ──────► harvest_flows ──► analyze_flow ──► get_flow_payload(compact: true) ──► 4~7 Macro Gateways Briefing
      ├─► 2. Visual Review ──────────► open_review(flowId) ──► Authenticated URL & 3-Column Workbench
      ├─► 3. Explicit Re-analysis ───► analyze_flow(re-run) ──► open_review(flowId)
      ├─► 4. Blast Radius/Boundaries ─► CompactRadarSummary ──► report_unknowns(flowId) ──► Direct Callers/Callees & Honest Boundaries
      └─► 5. Core Flow Publication ──► publish_core_flow ──► 6-field Anchor Verification (1-time re-read retry on mismatch)
```

1. **Flow Comprehension (Default)**:
   - Discover entry points with `harvest_flows(target, query)`.
   - Slice AST trace and obtain `flowId` with `analyze_flow(target, entrySymbolPath)`.
   - Retrieve curated ~500-token payload via `get_flow_payload(target, flowId, compact: true)`.
   - Detailed guide: [references/flow-comprehension.md](references/flow-comprehension.md).

2. **Visual Review**:
   - Open interactive FlowView web UI via `open_review(target, flowId)`.
   - **Critical**: Pass `flowId` (or saved `viewId`), NEVER `entrySymbolPath`.
   - Present the authenticated URL (`http://127.0.0.1:<port>/?token=...`) to the user.
   - Detailed guide: [references/flowview-workbench.md](references/flowview-workbench.md).

3. **Explicit Re-analysis**:
   - CodeFlow does not watch files continuously. After code modifications, re-analyze on demand with `analyze_flow(target, entrySymbolPath)` and update the view with `open_review(target, flowId)`.
   - Detailed guide: [references/flowview-workbench.md](references/flowview-workbench.md).

4. **Blast Radius & Boundary Inspection**:
   - Inspect 1st-degree callers/callees in `CompactRadarSummary`.
   - Query unresolvable dynamic dispatches or external cutoffs with `report_unknowns(target, flowId)`.
   - Detailed guide: [references/advanced-operations.md](references/advanced-operations.md).

5. **Agent Core Flow Publication**:
   - Publish verified architecture-layer flows using `publish_core_flow(target, artifact)`.
   - Requires valid 6-field anchors. On `anchor_verification_failed`, reread target file, recompute offsets, and retry once.
   - Detailed guide: [references/advanced-operations.md](references/advanced-operations.md).

## Core Guards

- **Compact First Guard**: Always pass `compact: true` to `get_flow_payload`. Consume the ~500-token curated payload first (4–7 macro gateways). Only inspect full specs or individual source files when deep line-level debugging is required.
- **Anti-Telemetry Guard**: NEVER leak internal compiler, benchmark, or engine telemetry (such as epochs, lag, internal settlement flags, locks) into user responses. Prioritize developer code comprehension by presenting only business flow traversals, architecture layers, and verifiable source code.
- **Zero-Hallucination Guard**: Never invent unverified execution hops or assume dynamic dispatch targets. Report unverified transitions honestly as `boundary` gateways and ground them with `report_unknowns`.
- **Preserve Verified Identity**: Treat AST facts, exact line numbers, and file hashes as immutable evidence. Never rewrite or fabricate flow identities or anchors.

## Response Contract

1. **Lead with Macro Business Intent**: Explain the sequence of 4–7 macro gateways (`entry`, `decision`, `process`, `effect`, `result`, `boundary`).
2. **Drill Down to Source Anchors**: Reference exact repo-relative file paths and line numbers (`path/to/file:line`) for critical execution steps.
3. **Report Boundaries Honestly**: Highlight any cutoff or unresolved interface discovered via `report_unknowns`.
4. **Offer Visual Review**: When visual inspection is requested or helpful, provide the authenticated FlowView URL returned by `open_review`.
