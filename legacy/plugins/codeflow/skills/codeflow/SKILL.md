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

## Domain Subgraph & Flow Extraction

When the user asks about an arbitrary business flow or domain process (e.g., push token registration and reception, payment/order processing, user auth/session refresh, BLE device pairing):
- Use `domain_subgraph(query: "...")` to extract the full multi-step causal graph across UI triggers, service logic, state mutations, and remote I/O endpoints.
- The returned structured result contains ordered lifecycle stages (Trigger -> Execution -> State Mutation -> I/O -> Reaction) and evidence-backed code anchors.

## Business journeys

When the user asks to create, update, list, or open a BusinessJourney, use MCP
only; never ask the user for a runtime token, port, scenario hash, or HTTP
command.

1. Call `domain_subgraph` or `entry_points` to discover relevant flow and system entry IDs.
2. Call `prepare_workspace` with one to three exact IDs needed by the journey.
3. Read `workspace` and, where needed, `current` to select only returned
   `flow_id` and `scenario_id` values.
4. Call `upsert_business_journey` only after the user explicitly requests the
   save or update. Use their stated business title and outcome; do not invent
   missing business meaning or causal links.
5. Report Core validation failures, `unknown` status, and scope mismatch
   verbatim. Do not replace another active workspace.
6. Call `open_business_journey` only when the user asks to view the FlowView.

For system-triggered or cross-layer requests, use `domain_subgraph` or `entry_points`. Core is the authority for scenario existence, causal edges, and
journey approval; do not create a sequential journey from independent
same-flow scenarios.

The first MCP request starts a compatible Core for the requested flow when one
is not already running. This makes the installed plugin usable without a
separate `serve` command or adapter setup. If it returns a typed setup error,
report the error and remediation; do not replace, emulate, or infer Core
results. `CODEFLOW_BIN` is an optional override for a non-default local
installation. Optional session hooks may request `refresh` or import supported
evidence only; hook failure is non-authoritative and must not alter current-flow
claims.
