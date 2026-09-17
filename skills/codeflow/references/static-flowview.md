# FlowView Storyboard & Core Flow Review

Use this guide to discover, publish, retrieve, explain, and visually review an existing code flow in CodeFlow.

## 1. Discover and Analyze

1. **Harvest Candidate Flows**: Call `harvest_flows` with `query` (natural language request or business domain) and `target` (project root).
2. **Select Entry Point**: If multiple candidates are returned, select the most relevant match or present candidates to the user to choose.
3. **Analyze Flow**: Call `analyze_flow` with the exact `entrySymbolPath` and `target`. This slices the execution trace, curates 4–7 macro business gateways (`FlowSequenceFrame`), and atomically publishes the result.
4. **Retrieve Curated Payload**: Call `get_flow_payload` with `flowId` or `entrySymbolPath` to obtain the compact ~500-token payload (`CompactFlowPayload`) and full FlowSpec.
5. **Open FlowView Review**: When the user requests visual review, call `open_review` with `flowId` (or `entrySymbolPath`) and `target` to obtain the authenticated FlowView URL (`http://127.0.0.1:<port>/?token=...`).

## 2. Explain the Storyboard

1. **Macro Intent First**: Start with the 4–7 macro gateways (`entry`, `decision`, `process`, `effect`, `result`, `boundary`) to explain what business task each stage performs.
2. **Micro Execution Trace**: Drill down into the 1:N timeline steps (`call`, `guard`, `mutation`, `result`) inside the relevant gateways, referencing exact file paths and line ranges.
3. **Honest Boundary Reporting**: If an unresolvable dynamic dispatch or external call cutoff occurred, explain it as a `boundary` gateway using facts from `report_unknowns`.

## 3. Agent-Authored Core Flow Publication

When an agent synthesizes an end-to-end flow artifact directly:
- Call `publish_core_flow` with `target` and `artifact`.
- Ensure every step includes valid `layer`, `kind`, `name`, and 6-field code anchor (`repoRelativePath`, `byteRange`, `fileHash`, `spanHash`, `enclosingSymbolPath`, `canonicalAstFingerprint`).
- On `anchor_verification_failed`, reread the target file to recompute offsets against current bytes and retry once.

## 4. Explicit Re-analysis & Baseline Comparison

- Re-analysis is executed on-demand only when explicitly requested by the user. Do not watch edits or auto-refresh views.
- When comparing changes, compare against an explicitly pinned baseline rather than assuming continuous live streaming.
