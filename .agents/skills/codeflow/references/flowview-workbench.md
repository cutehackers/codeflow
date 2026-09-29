# FlowView review, restoration, and reanalysis

## Open the requested flow

`open_review(target, flowId)` creates a current FlowView from a persisted flow resolution and returns its `viewId` and authenticated URL. It may run a new analysis for the current workspace snapshot. Pass `focusSymbol` and `focusReason` only when a verified direct call identifies the requested implementation. Use the returned `viewId` to read the exact stored analysis and, when evidence permits, save a shorter code reading path as described in [flow comprehension](flow-comprehension.md).

`open_review(target, viewId)` restores that saved result without reanalysis. To show an already curated result, pass its `viewId`, not the original `flowId`. Do not pass `entrySymbolPath` as an `open_review` target.

The interface presents selected code and its nearby context, verified relations, FlowSequence gateways, and expandable execution steps. Its default gateway count depends on the evidence and can be large. `summaryLimitations` and unknown connections explain where curation or execution proof is incomplete.

## Explicitly analyze changed code

After source changes, call `harvest_flows` as needed to verify the candidate identity, then `analyze_flow` with the original request, exact `candidateId`, and entry symbol. Call `open_review` with the new `flowId` to create a view against the current snapshot. If the user wants a short path, inspect the new view and curate it against its new step IDs and edges; do not reuse selections or anchors from an older snapshot.

A saved view remains immutable. Comparison requires an explicitly selected baseline. A failed or cancelled reanalysis must leave the prior view and reading position available.

## Share the result

Use the URL returned by `open_review` for the exact `viewId` the user should inspect. A local URL contains an access token; do not paste that token into documentation, logs, or source files. In the explanation, distinguish the current view, a restored saved view, and an optional before/after comparison.
