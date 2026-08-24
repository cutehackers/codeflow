---
name: codeflow
description: Turn a requested code or business journey into an evidence-backed CodeFlow FlowView using the installed CodeFlow MCP.
---

# CodeFlow FlowView

Use this skill when the user asks to analyze a code/business flow, create a FlowView, or explain a previously published CodeFlow flow.

1. Call `harvest_flows` with the user's intent as `query` and select the clear best candidate.
2. If candidates are genuinely ambiguous, ask the user which entry point they mean. If no candidate fits but they supplied an entry symbol path, use `analyze_flow` for that path.
3. Call `get_flow_payload` with the chosen `flowId` and explain the ordered steps from the returned evidence. Call `report_unknowns` before filling any gap with an inference.
4. When the user asked for a FlowView, call `open_review` with that `flowId` and give them its returned URL. Do not open FlowView merely because it would be useful.

Keep `freshness`, `provenance`, and every `unknown` reason intact. Never infer a state transition or runtime behavior not present in the payload.

Use `submit_flow_draft` or `approve_step` only after the user explicitly asks to save a draft or approve a step; anchors are required for drafts.
