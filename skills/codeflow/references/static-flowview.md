# FlowView

Use this mode to discover, publish, retrieve, explain, and visually review an existing code flow. FlowView describes captured code. It does not prove that the result remains current after later edits.

## Discover and Analyze

1. For a requested FlowView, call `query_task_view` in feature mode with the exact entry symbol or the user's business request and the project root as `target`.
2. If the result reports ambiguous candidates, select only an unambiguous match or ask the user to choose. Preserve analysis failures and missing evidence.
3. Open the returned `flowView.url` immediately using the host's browser tool. The returned `viewId` restores that exact saved result, including FlowSequence and source context. Do not create a separate HTML file or issue another analysis to display it.
4. To reopen a saved result, pass its exact `viewId` and target to `open_review`, then open the returned URL. Missing historical source is shown explicitly.
5. Use `analyze_flow` or `publish_core_flow` when the user requests persisted core-flow publication. When showing that result, use its returned `flowId` with `open_review`.
6. Reanalysis creates a new saved result only on an explicit request. Do not watch edits or replace the open view automatically.

Do not describe anchor verification as an asynchronous FlowView state. `publish_core_flow` either returns a concrete verification error or a published `flowId`; report only that returned result.

For an agent-authored artifact:

- Start at the entry-layer event and follow every handling step needed to reach completion across architecture layers.
- Exclude statements that do not advance that traversal.
- Honor an existing `codeflow.layers.yaml`. If it is absent and layer classification is needed, derive it from repository structure and dependency/provider relationships before class-name suffixes.
- Every step must contain `layer`, `kind`, `name`, and the complete anchor required by the registered `core-artifact` schema.
- Use canonical layers such as `presentation`, `controller`, `usecase`, `domain`, `data`, `infra`, and `external`.
- Represent backward/error transitions with a valid branch rather than violating layer order.

## Recovery

- On `anchor_verification_failed`, reread the cited file, recompute the anchor against current bytes, and retry once.
- On `artifact_too_large`, split the result into meaningful subflows. Do not resend the same oversized artifact.
- Use `report_unknowns` for unresolved boundaries. Do not invent the missing traversal.

## Review and Explain

1. Retrieve the persisted result with `get_flow_payload` using the returned `flowId` or exact entry symbol.
2. Explain the business outcome first, then the ordered layer traversal, state changes, branches, external effects, and explicit unknowns.
3. Call `open_review` only when the user asks to see FlowView. Use the same target and returned flow identity.

## Optional Draft and Step Approval

- Use `submit_flow_draft` only for a requested structured journey-draft workflow.
- Use `approve_step` only when the user explicitly approves a FlowView step name or rules. This legacy step approval is separate from live Semantic Approval.
