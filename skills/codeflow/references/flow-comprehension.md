# Requested Flow comprehension

## 1. Confirm the entry

Call `harvest_flows` with the repository root and the user's request or a source term. Its `query` is a literal substring filter. A result marked `no_literal_match` still contains candidates to inspect. Read candidate source and choose only when the event or route matches the user's requested start. Preserve the returned `candidateId`.

For a natural-language request, analyze that candidate with the exact text:

```json
{"name":"analyze_flow","arguments":{"target":".","entrySymbolPath":"lib/features/auth/login_controller.dart#LoginController.submit","request":"What happens when login is submitted?","candidateId":"<ID from harvest_flows>"}}
```

If the entry is ambiguous, resolve it before analysis. `analyze_flow` returns a persisted `flowId`; it does not by itself create a FlowView.

## 2. Read the analysis, then curate the overview

Use `get_flow_payload` with `flowId` and `compact: true` for an overview. Check `summaryLimitations` and unknowns before presenting it. The number of frames is evidence-dependent. The ordered list of frames does not mean all branches ran in sequence.

Create an intermediate analysis view with `open_review(target, flowId)`. This view is not the final FlowView. If a requested implementation was confirmed by a direct call, include its exact `focusSymbol` and an evidence-based `focusReason`. Use the returned `viewId` with `get_flow_payload(target, viewId, compact: true)` to inspect stored `steps`, indexed `edges`, `frames`, `requestedFocus`, and limitations. Read the full view only for a specific fact that the compact result and relevant source cannot establish. CodeFlow verifies CodeView during overview curation.

Group source-backed steps into processing segments that answer the original request. Show verified steps and put missing connections or evidence limits in the relevant segment. Never invent a step, relation, or complete traversal. Preserve the distinction between verified code, missing connections, and analysis failures.

Choose the steps and outcomes that answer the original request:

1. The selected entry and the condition that permits or blocks the action.
2. Direct implementation calls and major processing steps that change the result.
3. Necessary decisions, state changes, external effects, and async handoffs.
4. Each important outcome, or the exact point where execution cannot be proved.

Keep the overview focused only when that does not hide a necessary decision or outcome. Preserve mutually exclusive branches and their common continuation. The analysis remains available as detail. Do not infer an edge from neighboring statement order, similar names, or a callback argument.

## 3. Save the purpose-grouped overview

Call `curate_requested_flow_overview(target, viewId, segments, relations, categories)` with the intermediate analysis `viewId`. Each segment contains `segmentId`, concrete processing `purpose`, `inclusionReason`, `stepIds`, and `blocks` partitioning those steps by file and callable. A block references step IDs and groups them by one file and callable. The overview API does not take per-step roles, `requestPart`, or `inclusionReason` fields.

Each relation contains `fromSegmentId`, `toSegmentId`, `fromStepId`, `toStepId`, and `edgeIndexes`, the zero-based indexes of exact resolved edges traversed in the stored `semanticMap.edges`. Intermediate steps may remain within a relation when their source and CodeView are verified; users can expand and open each one. Include steps whose omission would misstate the request's decision or outcome. A verified loop edge may revisit an earlier loop point inside a relation; retain its exact edge indexes. Branches and joins are allowed. Every selected branch must end at an outcome or boundary. When `requestedFocus` exists, include that direct call and callee entry. `categories` is optional. When supplied, each category contains `categoryId`, business-purpose `title`, and child `segmentIds`; every overview segment belongs to exactly one category. Categories are the large FlowSequence reading units and children are execution segments, not one-to-one copies. Lists define reading order, not execution proof. CodeFlow checks the graph, source snapshot, and CodeView highlights before saving a new view.

Open the returned `viewId` with `open_review` and use the returned URL as the final FlowView. Check each selected decision's other confirmed outcomes and include those needed for the request. If validation identifies a step whose exact code cannot open, show the evidence limit in its relevant segment and stop the claimed traversal at a verified boundary. The original analysis is intermediate and must not replace the curated overview as the final result.

## 4. Explain what the code proves

Cite the included source locations. Explain alternative outcomes as alternatives. State whether a relation is direct, conditional, asynchronous, or unverified. Do not claim a fixed gateway count. If `request_relevance_unproven` or other missing evidence appears, show it in the relevant segment; do not treat the intermediate analysis as the final FlowView or claim a complete Requested Flow.
