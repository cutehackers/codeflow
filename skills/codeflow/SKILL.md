---
name: codeflow
description: Turn a requested core flow / 핵심 흐름 / 아키텍처 흐름 / 코드 흐름 / business flow / 레이어 흐름 / implementation flow into a verified, evidence-backed FlowView via the installed CodeFlow MCP.
---

# CodeFlow Core Flow

Use this skill when the user asks to understand, explain, or visualize a code/business/core flow, architecture layer flow, or any implementation flow they want to understand.

## Workflow

1. **Explore**: Locate the entry-layer initial event for the user's intent and trace its layer traversal to completion. Prefer repo structure (`feature/*/presentation|domain|data`) and DI/provider graph over class-name suffixes. If `codeflow.layers.yaml` exists, read it first and honor its `layers`/`aliases`/`pathPatterns`.
2. **Build & Publish**: Construct the intermediate artifact and call `publish_core_flow`.
   - Every step MUST carry `layer`, `kind` (`guard|mutation|call|branch`), `name`, and a 6-field `anchor` (`repoRelativePath`, `byteRange` [start, end], `fileHash`, `spanHash`, `enclosingSymbolPath`, `canonicalAstFingerprint`).
   - Use canonical English layer values (`presentation, controller, usecase, domain, data, infra, external`).
   - Backward layer hops (e.g. error handling return) MUST use `kind: "branch"` to pass layer order validation.
3. **Error Recovery**:
   - On `anchor_verification_failed`: re-read the cited file, recompute `byteRange`/`fileHash`/`spanHash`, and retry `publish_core_flow` once.
   - On `artifact_too_large`: do not retry the same payload; split the flow into smaller subflows.
4. **Explain**: Call `get_flow_payload` with the returned `flowId` and explain in layer order:
   - **Business summary** in the first sentence.
   - **Steps by layer** (`presentation → controller → usecase → data ...`).
   - **State delta & delegations** (`edges[].toLayer`).
   - **Unknowns & uncertainties** explicitly (never invent/guess missing logic; call `report_unknowns` if needed).
5. **View**: When the user requested a visual FlowView, call `open_review` with `flowId` and provide the returned URL (contains `?token=`). Do not open FlowView speculatively.

Triggers: `core flow, 핵심 흐름, 아키텍처 흐름, 코드 흐름, business flow, 레이어 흐름, implementation flow`.
