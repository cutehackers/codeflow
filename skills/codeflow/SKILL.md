---
name: codeflow
description: Turn a requested core flow / 핵심 흐름 / architecture-layer flow into a verified, evidence-backed FlowView via the installed CodeFlow MCP.
---

# CodeFlow Core Flow

Use this skill when the user asks to understand, explain, or visualize a code/business/core flow, architecture layer flow, or any implementation flow they want to understand.

1. Explore the codebase to locate the entry-layer initial event for the user's intent and trace its layer traversal to completion. Prefer repo structure (feature/*/presentation|domain|data) and DI/provider graph over class-name suffixes. If the project has `codeflow.layers.yaml`, read it first and honor its `layers`/`aliases`/`pathPatterns` for layer names.
2. Build a layer-annotated intermediate artifact and call `publish_core_flow` with it. Every step MUST carry layer, kind, name, and a 6-field anchor (repoRelativePath, byteRange, fileHash, spanHash, enclosingSymbolPath, canonicalAstFingerprint). Use canonical English layer values (`presentation, controller, usecase, domain, data, infra, external`); aliases are normalized by CORE.
3. If `publish_core_flow` returns anchor_verification_failed, re-read the cited file, recompute that step's anchor, and retry once before asking the user.
4. Call `get_flow_payload` with the returned flowId and explain steps in layer order. Surface provenance, freshness, and every unknown without inference. Use `report_unknowns` before filling a gap.
5. When the user asked for a FlowView, call `open_review` with that flowId and give its returned URL (contains ?token=). Do not open FlowView speculatively.

Triggers: `core flow, 핵심 흐름, 아키텍처 흐름, 코드 흐름, business flow, 레이어 흐름`.
