---
name: codeflow
description: Use installed CodeFlow for evidence-backed current flow, diff, unknowns, refresh, and requested FlowView review.
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

The first MCP request starts a compatible Core for the requested flow when one
is not already running. This makes the installed plugin usable without a
separate `serve` command or adapter setup. If it returns a typed setup error,
report the error and remediation; do not replace, emulate, or infer Core
results. `CODEFLOW_BIN` is an optional override for a non-default local
installation. Optional session hooks may request `refresh` or import supported
evidence only; hook failure is non-authoritative and must not alter current-flow
claims.
