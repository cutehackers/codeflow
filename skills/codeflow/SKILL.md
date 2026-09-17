---
name: codeflow
description: Use CodeFlow MCP for verified core-flow extraction, FlowSequence Storyboard generation, and interactive FlowView review across polyglot codebases.
---

# CodeFlow MCP Operating Contract

Operate the installed CodeFlow MCP using the official 8 core tools. Preserve CodeFlow's evidence, identity, freshness, and authority boundaries when translating tool results into an answer.

## Preconditions

- Use this skill when the user invokes `$codeflow` or clearly requests CodeFlow analysis, business flow extraction, or FlowView review.
- Use the project root as `target`, not a feature subdirectory. Project identity and adapter detection depend on root files such as `pubspec.yaml` or `package.json`.
- If CodeFlow MCP or its language adapter is unavailable, report that installation or adapter resolution is required (`codeflow doctor <target>`).
- Keep all operations on the same MCP server and exact target repository.

## Route the User Request

- **Discover & Visualize Existing Flow**: Use `harvest_flows` to find candidate entry points, `analyze_flow` to extract and publish the Storyboard, and `open_review` to generate the interactive FlowView URL.
- **Explain Flow Steps**: Retrieve the curated FlowSequence and high-density payload via `get_flow_payload`, then explain the business intent (macro gateways) followed by line-level execution details (1:N timeline steps).
- **Verify Agent-Authored Core Flow**: Use `publish_core_flow` to ground and publish verified code anchors against the current workspace.
- **Inspect Boundaries & Unknowns**: Use `report_unknowns` to inspect unresolvable dynamic dispatches, missing types, and external boundary cutoffs.
- **Step Approval & Drafts**: Use `approve_step` when the user explicitly approves step descriptions/rules, or `submit_flow_draft` for structured session journeys.

For detailed step-by-step procedures, consult [references/static-flowview.md](references/static-flowview.md).

## Shared Authority Rules

- Treat verified anchors, AST facts, and proof manifests as evidence. Never fill missing behavior with ungrounded inference.
- Say a result is `verified` only when CodeFlow returns valid anchors matching the current snapshot. Otherwise report `stale`, `candidate`, or `boundary`.
- Internal engine telemetry (compiler epochs, lag, settlement flags, internal locks) is strictly prohibited from leaking into user responses.
- Runtime behavior is `observed` only when CodeFlow returns trusted runtime evidence. Static reachability is not runtime evidence.
- On ambiguous targets or multiple candidate flows, present CodeFlow's candidates and ask the user to select. On incomplete closure, preserve the gap (`boundary`) instead of retrying into an unwarranted success claim.

## Response Contract

Lead with the high-level business behavior and macro Storyboard gateways (`entry`, `decision`, `process`, `effect`, `result`, `boundary`). Then present the relevant line-level code path with exact repo-relative anchors, and highlight any unresolved boundary. When the user requests visual review, call `open_review` and present the authenticated FlowView URL.
