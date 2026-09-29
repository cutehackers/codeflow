---
name: codeflow
description: Use CodeFlow MCP to select a requested execution flow, read verified source and relations, and open its FlowView.
---

# CodeFlow MCP Operating Contract

Use CodeFlow's nine MCP tools to identify the user's Requested Flow and explain it from source evidence. An adapter extracts language syntax and direct relations. The shared Core validates evidence, retains detail, and presents CodeView. Neither a compact payload nor a generated title proves which processing steps are essential to the request.

## Start from the requested flow

- Pass the repository root as `target`. If `.codegraph/` exists, use `codegraph explore` to inspect likely entry symbols before selecting one.
- `harvest_flows(target, query)` uses a literal substring filter. If it reports `no_literal_match`, inspect the returned candidates and their source evidence. Do not treat a literal match or a similar name as proof of execution.
- Select one candidate supported by its entry source. For a natural-language request, call `analyze_flow(target, entrySymbolPath, request, candidateId)` with the original request and the candidate's exact ID. Resolve a genuine ambiguity with the user before analysis.
- Call `get_flow_payload(target, flowId, compact: true)` for a first overview. Inspect `summaryLimitations` and unknowns. Its frame count is not guaranteed to be 4–7, and its frame order alone is not a single executed path.

## Deliver a source-backed reading path

When the user needs to understand the implementation or requests a visual result, create the current FlowView with `open_review(target, flowId)`. If the request identifies an implementation target, provide `focusSymbol` and `focusReason` only after checking its direct call in source. Use the returned `viewId` to read the exact stored analysis with `get_flow_payload(target, viewId, compact: true)`. This compact result retains step IDs, source locations, indexed edges, frame membership, and limitations. Read the full view only when those facts or targeted source reads cannot resolve a specific question.

Inspect the compact result's `steps`, indexed `edges`, `frames`, `requestedFocus`, and limitations. Read the relevant source to decide which code explains the request's entry, necessary decisions, processing, effects, and outcomes or analysis boundaries. Include the verified direct call and callee entry when `requestedFocus` exists. Preserve important alternative outcomes and their join. A callback argument proves that code was passed, not that its body ran. Do not use an adjacent ordinal, matching name, or unverified edge as an execution relation.

For each chosen step, provide its exact `stepId`, a valid role, an exact phrase from the original request as `requestPart`, and a concrete `inclusionReason`. Connect chosen steps with the exact indexes of resolved execution edges. A relation may summarize intermediate code only when its source and CodeView are verified; users can expand those steps beneath the relation. Inspect every alternative outcome at a selected decision and include the outcomes needed to explain the request. Relations may branch and join; every selected branch must end at an outcome or boundary. Call `curate_code_reading_path(target, viewId, selections, relations)`. CodeFlow verifies the selected source, relations, and CodeView before saving a new `viewId`. Open that returned `viewId` with `open_review` and use its URL for visual review. The full FlowSequence and source steps remain available as detail.

If the evidence cannot support a complete short path, do not submit an invented selection. Show the verified analysis, identify the missing connection or request relevance, and use `report_unknowns` where helpful. The result is an analysis with an explicit limit, not a completed curated path.

Detailed workflow: [flow comprehension](references/flow-comprehension.md). FlowView restoration and explicit reanalysis: [FlowView workbench](references/flowview-workbench.md).

## Evidence and state guards

- Keep operations on one MCP server and exact target repository. Preserve the original request, selected candidate, snapshot, and `viewId`.
- Use `viewId` to restore a saved analysis. Use `flowId` with `open_review` only when intentionally creating a current view from its flow resolution. Reanalysis is explicit; never imply that a saved view updated itself.
- A confirmed source range does not prove that every branch ran. Describe static possibilities and unverified boundaries accurately.
- Do not rewrite source anchors, file hashes, step IDs, or validation status. Do not expose internal compiler or benchmark telemetry in the primary explanation.
- If an adapter or toolchain is missing, use `codeflow doctor <target>` for diagnosis.

## Answer from the code

Explain the chosen entry, the necessary processing decisions and effects, and the verified result or boundary in order. Cite the relevant source locations and distinguish alternative paths. State any missing call or CodeView evidence. Present the curated FlowView URL when visual inspection was requested or helps the user read the result.
