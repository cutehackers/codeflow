---
name: codeflow
description: Use CodeFlow MCP to select a requested execution flow, read verified source and relations, and open its FlowView.
---

# CodeFlow MCP Operating Contract

Use CodeFlow's MCP tools to identify the user's Requested Flow and explain it from source evidence. The default and final FlowView is the purpose-grouped result saved by `curate_requested_flow_overview`. An adapter extracts language syntax and direct relations. The shared Core validates evidence, retains detail, and presents CodeView. Neither a compact payload nor a generated title proves which processing steps are essential to the request.

## Start from the requested flow

- Pass the repository root as `target`. If `.codegraph/` exists, use `codegraph explore` to inspect likely entry symbols before selecting one.
- `harvest_flows(target, query)` uses a literal substring filter. If it reports `no_literal_match`, inspect the returned candidates and their source evidence. Do not treat a literal match or a similar name as proof of execution.
- Select one candidate supported by its entry source. For a natural-language request, call `analyze_flow(target, entrySymbolPath, request, candidateId)` with the original request and the candidate's exact ID. Resolve a genuine ambiguity with the user before analysis.
- Call `get_flow_payload(target, flowId, compact: true)` to inspect the analysis. This is intermediate evidence, not the final FlowView. Inspect `summaryLimitations` and unknowns. Its frame count is not guaranteed to be 4–7, and its frame order alone is not a single executed path.

## Deliver a source-backed reading path

When the user needs to understand the implementation or requests a visual result, create an intermediate analysis view with `open_review(target, flowId)`. If the request identifies an implementation target, provide `focusSymbol` and `focusReason` only after checking its direct call in source. Use the returned `viewId` to read the exact stored analysis with `get_flow_payload(target, viewId, compact: true)`. This compact result retains step IDs, source locations, indexed edges, frame membership, and limitations. Read the full view only when those facts or targeted source reads cannot resolve a specific question.

Inspect the compact result's `steps`, indexed `edges`, `frames`, `requestedFocus`, and limitations. Read the relevant source to decide which code explains the request's entry, necessary decisions, processing, effects, and outcomes or analysis boundaries. Include the verified direct call and callee entry when `requestedFocus` exists. Preserve important alternative outcomes and their join. A callback argument proves that code was passed, not that its body ran. Do not use an adjacent ordinal, matching name, or unverified edge as an execution relation.

Connect segments with the exact indexes of resolved execution edges. Relations may summarize intermediate code only when its source and CodeView are verified; users can expand those steps beneath the relation. Inspect alternative outcomes at relevant decisions and include outcomes needed to explain the request. Relations may branch and join; every selected branch must end at an outcome or boundary. Group steps into purpose-based processing segments after reading the original request and source. Each segment contains only `segmentId`, `purpose`, `inclusionReason`, `stepIds`, and `blocks` partitioning those steps by one file and callable. A block references step IDs only. CodeFlow determines its continuous source range and precise highlights from the preserved snapshot. Group by processing purpose, never merely by adjacent lines or function names. Preserve decisions, failures, effects, returns and unresolved handoffs. Relations contain `fromSegmentId`, `toSegmentId`, `fromStepId`, `toStepId`, and exact ordered `edgeIndexes`. Loops and same-segment relations are allowed. Optionally provide `categories` with `categoryId`, business-purpose `title`, and child `segmentIds`; when provided, every overview segment belongs to exactly one category. Categories are the large FlowSequence reading units, and their children are execution segments rather than one-to-one copies. Category and segment lists define reading order, not execution proof. Call `curate_requested_flow_overview(target, viewId, segments, relations, categories)`, then call `open_review` with its returned new `viewId` and open the returned URL. The server verifies code and relations, not the semantic accuracy of purpose descriptions.

If source or graph evidence is incomplete, do not invent a segment or relation. Curate the supported overview and show the missing evidence in the relevant segment, using `report_unknowns` where helpful. The result remains a limited analysis, not a claim that the Requested Flow is complete. Never substitute a statement path or uncurated analysis as the final FlowView.

Detailed workflow: [flow comprehension](references/flow-comprehension.md). FlowView restoration and explicit reanalysis: [FlowView workbench](references/flowview-workbench.md).

## Evidence and state guards

- Keep operations on one MCP server and exact target repository. Preserve the original request, selected candidate, snapshot, and `viewId`.
- Use `viewId` to restore a saved analysis. Use `flowId` with `open_review` only when intentionally creating a current view from its flow resolution. Reanalysis is explicit; never imply that a saved view updated itself.
- A confirmed source range does not prove that every branch ran. Describe static possibilities and unverified boundaries accurately.
- Do not rewrite source anchors, file hashes, step IDs, or validation status. Do not expose internal compiler or benchmark telemetry in the primary explanation.
- If an adapter or toolchain is missing, use `codeflow doctor <target>` for diagnosis.

## Answer from the code

Explain the chosen entry, the necessary processing decisions and effects, and the verified result or boundary in order. Cite the relevant source locations and distinguish alternative paths. State any missing call or CodeView evidence. Present the curated FlowView URL when visual inspection was requested or helps the user read the result.
