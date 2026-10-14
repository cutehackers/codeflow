# FlowView review, restoration, and reanalysis

## Open the requested flow

`open_review(target, flowId)` creates an intermediate analysis view from a persisted flow resolution and returns its `viewId` and authenticated URL. It may run a new analysis for the current workspace snapshot. Pass `focusSymbol` and `focusReason` only when a verified direct call identifies the requested implementation. Use the returned `viewId` to read the exact stored analysis, then save the purpose-grouped overview with `curate_requested_flow_overview` as described in [flow comprehension](flow-comprehension.md). The curated overview is the default and final FlowView.

`open_review(target, viewId)` restores that saved result without reanalysis. To show an already curated result, pass its `viewId`, not the original `flowId`. Do not pass `entrySymbolPath` as an `open_review` target.

The curated interface presents purpose-grouped categories and segments, selected code and nearby context, verified relations, FlowSequence gateways, and expandable execution steps. Its gateway count depends on evidence and can be large. Show missing connections, source limitations, and analysis failures in the relevant segment. Do not invent code or relations to fill a gap, and do not use an uncurated analysis view as the final result.

## Explicitly analyze changed code

After source changes, call `harvest_flows` as needed to verify the candidate identity, then `analyze_flow` with the original request, exact `candidateId`, and entry symbol. Call `open_review` with the new `flowId` to create an intermediate view against the current snapshot. Curate a purpose-grouped overview against its new step IDs and edges; do not reuse selections or anchors from an older snapshot.

A saved view remains immutable. Comparison requires an explicitly selected baseline. A failed or cancelled reanalysis must leave the prior view and reading position available.

## Share the result

Use the URL returned by `open_review` for the exact curated `viewId` the user should inspect. A local URL contains an access token; do not paste that token into documentation, logs, or source files. In the explanation, distinguish the current curated view, a restored saved view, and an optional before/after comparison.
