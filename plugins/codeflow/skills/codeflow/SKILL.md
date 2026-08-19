---
name: codeflow
description: Use installed CodeFlow Core for evidence-backed current flow, diff, unknowns, refresh, and requested FlowView review.
---

# CodeFlow

Use the `codeflow` MCP server as the sole source of flow analysis. Do not scan,
compile, infer evidence, or calculate deltas independently.

First use `current` to understand a requested route. Use `diff` only for a
review against a selected baseline. Use `unknowns` whenever an explanation
would otherwise fill a gap with inference. Preserve each returned trust status
and unknown reason verbatim in responses. Request `refresh` only when current
source may have changed. Use `open` only when the user explicitly asks to view
the local FlowView.

If Core is unavailable or incompatible, report its typed error and remediation.
Discover the executable from `CODEFLOW_BIN` when configured, otherwise PATH;
never install, replace, or emulate Core. Optional session hooks may request
`refresh` or import supported evidence only; hook failure is non-authoritative
and must not alter current-flow claims.
