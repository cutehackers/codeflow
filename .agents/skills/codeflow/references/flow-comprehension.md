# Requested Flow comprehension

## 1. Confirm the entry

Call `harvest_flows` with the repository root and the user's request or a source term. Its `query` is a literal substring filter. A result marked `no_literal_match` still contains candidates to inspect. Read candidate source and choose only when the event or route matches the user's requested start. Preserve the returned `candidateId`.

For a natural-language request, analyze that candidate with the exact text:

```json
{"name":"analyze_flow","arguments":{"target":".","entrySymbolPath":"lib/features/auth/login_controller.dart#LoginController.submit","request":"What happens when login is submitted?","candidateId":"<ID from harvest_flows>"}}
```

If the entry is ambiguous, resolve it before analysis. `analyze_flow` returns a persisted `flowId`; it does not by itself create the current FlowView.

## 2. Read the analysis, then select its code

Use `get_flow_payload` with `flowId` and `compact: true` for an overview. Check `summaryLimitations` and unknowns before presenting it. The number of frames is evidence-dependent. The ordered list of frames does not mean all branches ran in sequence.

Create a current view with `open_review(target, flowId)`. If a requested implementation was confirmed by a direct call, include its exact `focusSymbol` and an evidence-based `focusReason`. Use the returned `viewId` with `get_flow_payload(target, viewId, compact: true)` to inspect stored `steps`, indexed `edges`, `frames`, `requestedFocus`, and limitations. Read the full view only for a specific fact that the compact result and relevant source cannot establish. CodeFlow verifies CodeView during path saving.

Choose source-backed points that answer the original request:

1. The selected entry and the condition that permits or blocks the action.
2. Direct implementation calls and major processing steps that change the result.
3. Necessary decisions, state changes, external effects, and async handoffs.
4. Each important outcome, or the exact point where execution cannot be proved.

Keep the path short only when that does not hide a necessary decision or outcome. Preserve mutually exclusive branches and their common continuation. The full analysis remains available as the timeline. Do not infer an edge from neighboring statement order, similar names, or a callback argument.

## 3. Save the verified path

`curate_code_reading_path` takes the original `viewId`, ordered `selections`, and `relations`. Each selection contains `stepId`, `role`, `requestPart` copied exactly from the original request, and a concrete `inclusionReason`. Valid roles are `entry`, `decision`, `transform`, `state_change`, `effect`, `handoff`, `outcome`, and `boundary`.

Each relation contains `fromStepId`, `toStepId`, and `edgeIndexes`, the zero-based indexes of the exact resolved edges traversed in the stored `semanticMap.edges`. Intermediate steps may remain below a short relation when their source and CodeView are verified; users can expand and open each one. Select a step as a main reading point when omitting it from the main path would misstate the request's decision or outcome. A verified loop edge may revisit an earlier selected loop point inside a relation; retain its exact edge indexes rather than adding a duplicate node. Branches and joins are allowed. Every selected branch must end at an outcome or boundary. The selected path must start at the entry, and when `requestedFocus` exists it must include that direct call and callee entry. CodeFlow checks the graph, source snapshot, and CodeView highlights before saving a new view.

Open the returned `viewId` with `open_review`. Check each selected decision's other confirmed outcomes. The response names how many confirmed branches were left outside the main path; inspect them in the full detail and add those needed for the request. If validation identifies an intermediate step whose exact code cannot open, end at a verified boundary or choose another verified route. If source or graph evidence cannot support a complete explanation, report the missing evidence and use the original verified view. Do not turn a partial path into a claim of complete execution.

## 4. Explain what the code proves

Cite the selected source locations. Explain alternative outcomes as alternatives. State whether a relation is direct, conditional, asynchronous, or unverified. Do not claim a fixed 4–7 gateway count. When `request_relevance_unproven` appears, the default FlowSequence has not established which frames are essential to the request; inspect and curate it before giving a concise core-flow explanation.
