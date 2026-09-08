# Static FlowView

Use this mode to discover, publish, retrieve, explain, and visually review an existing code flow. Static publication describes captured code. It does not prove that the result remains current after later edits.

## Discover and Analyze

1. Call `harvest_flows` when CodeFlow should discover candidate entry points. Use the user's requested behavior as the optional query filter.
2. If the user supplies an exact entry symbol, call `analyze_flow` for adapter-driven slicing and publication.
3. When the agent must author a complete architecture-layer flow, inspect the implementation and call `publish_core_flow` with a verified intermediate artifact.

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
- Use `approve_step` only when the user explicitly approves a static FlowView step name or rules. This legacy step approval is separate from live Semantic Approval.
