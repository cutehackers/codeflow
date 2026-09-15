---
name: codeflow
description: Use CodeFlow MCP for verified core-flow publication and FlowView, Live Semantic View/Map creation with the fixed template, edit-driven current-or-gap tracking, and evidence-bound semantic review, impact, failure, approval, onboarding, or release evaluation.
---

# CodeFlow MCP Operating Contract

Operate the installed CodeFlow MCP using the smallest workflow that satisfies the request. Preserve CodeFlow's evidence, identity, freshness, and authority boundaries when translating tool results into an answer.

## Preconditions

- Use this skill when the user invokes `$codeflow` or clearly requests CodeFlow analysis, live semantic tracking, semantic review, or FlowView.
- Use the project root as `target`, not a feature subdirectory. Project identity and adapter detection depend on root files such as `pubspec.yaml` or `package.json`.
- If CodeFlow MCP or its language adapter is unavailable, report that installation or adapter resolution is required. Do not claim live behavior from a CLI-only substitute.
- Keep every related live operation on the same MCP server and exact target. `open_review` then points to that coordinator's FlowView.

## Route the User Request

- When the user asks how an existing feature works or wants to see its code path, read [references/static-flowview.md](references/static-flowview.md).
- When the user asks what changed, what is affected, why something failed, whether requirements are met, whether an explanation should be approved, how an unfamiliar project is organized, or whether a release is ready, read [references/semantic-operations.md](references/semantic-operations.md).

Read only the references required by the request. Do not start the live edit loop for a static flow request or load specialized semantic operations for ordinary flow visualization.

## Shared Authority Rules

- Treat verified anchors, analyzer results, Evidence Packs, and proof manifests as evidence. Never fill missing behavior with inference.
- Say a result is `current` only when CodeFlow returns a valid current answer and Generation Proof for the same target, generation, basis, snapshot, intent revision, and query. Otherwise report `historical`, `candidate`, `unknown`, or the returned Verified Gap.
- Keep these states separate: implementation Fact, freshness, settlement, Requirement Alignment, model enrichment, Semantic Approval, and runtime observation. One state never upgrades another.
- A model proposal is display-only until separately grounded and approved. Approval does not change implementation facts, freshness, settlement, or alignment.
- Runtime behavior is `observed` only when CodeFlow returns trusted runtime evidence. Static reachability is not runtime evidence.
- Preserve exact IDs returned by CodeFlow. Do not invent or rewrite repository, worktree, epoch, snapshot, basis, generation, intent, proposal, evidence-pack, approval, or event identities.
- On ambiguous targets, present CodeFlow's candidates and ask the user to select. On incomplete closure or proof, preserve the gap instead of retrying into a success claim.

## Response Contract

Lead with the business behavior. Then state the relevant code path or semantic change, its evidence/freshness, and any unknown or blocking gap. For a Live Semantic Map request, open or present the `flowView.url` returned by `query_task_view` immediately. The returned URL restores the exact request and resolved entry symbol in the same FlowView coordinator.
