# Semantic Operations & Advanced Flow Exploration

This guide covers advanced flow analysis, boundary inspection, and step approvals using the official CodeFlow core tools.

## 1. Boundary & Unknown Inspection

- Use `report_unknowns` with `target` to inspect unresolvable dynamic dispatches, missing types, and external boundary cutoffs across the repository.
- Treat boundaries as honest architectural limits: do not fabricate unverified execution hops.

## 2. Direct Relationship & Radar Exploration

- Within FlowView, the Blast Radius Radar explores verified 1st-degree direct relationships (callers, callees, state mutations, and test anchors) for the selected scene.
- All relationships are grounded in the same immutable workspace snapshot.

## 3. Step Approvals & Drafts

- Use `approve_step` when the user explicitly approves or modifies a step name or business rule.
- Use `submit_flow_draft` when capturing interactive session journey drafts with verified anchors.

## 4. Optional Semantic Labeling (SLMLabeler)

- If a local SLM runtime is enabled, CodeFlow can asynchronously suggest descriptive Korean business labels for FlowSequence gateways.
- If the SLM is unavailable or times out (500ms), CodeFlow silently falls back to deterministic AST titles without errors.
- Suggested labels are display proposals and do not modify underlying code facts or anchors.
