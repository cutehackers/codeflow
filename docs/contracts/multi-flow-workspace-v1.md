# Multi-flow workspace v1

CodeFlow may analyze one to three explicit selectors in one local operation:

```sh
codeflow serve --repo PROJECT \
  --flow route:/join \
  --flow route:/home \
  --flow route:/auth
```

The request is rejected when a selector is empty, duplicated, ambiguous, or the
set contains more than three entries. A positional selector remains supported
for the existing single-flow journey.

## Publication

Core captures one `FlowBasis`, starts one Dart adapter process, discovers entry
points once, and reuses one repository `AnalysisContextCollection`. Each
selector still produces and validates an independent FlowIR document.

SQLite publishes the ordered flow ID list, all FlowIR bytes, publication time,
runtime status, and cognitive-debt rows in one transaction. Readers observe the
complete previous or complete new workspace. A changing worktree or failed flow
keeps the previous workspace and marks it `analyzing`.

All documents must have byte-for-byte equal Basis values, including repository,
revision, dirty state, worktree fingerprint, and manifest.

## API and MCP

Authenticated `GET /api/v1/workspace` uses the common CodeFlow response envelope.
Its `data` is:

```text
schema_version, basis, flow_ids, flows, screen_flow_edges
```

`flow_ids` preserves request order. A `screen_flow_edge` is `observed` only when
an observed route or visible-result fact in one document names another current
flow ID from the same workspace. No route-name similarity or LLM inference
creates an edge.

Authenticated `GET /api/v2/workspace` is the preferred navigation read model.
It preserves the same status, ordered flow IDs, complete facts, causal edges,
timelines, unknowns, and screen-flow edges, while sending snapshot identity
once. Its top-level Basis is a summary containing repository, revisions, dirty
state, worktree fingerprint, and `manifest_count`; it does not transmit the
manifest. Each flow omits its duplicated Basis. Canonical, independently
validatable FlowIR remains available from `GET /api/v1/flows/...`.

MCP `workspace` uses the compact v2 read model. Existing `current`, `step`,
`unknowns`, `refresh`, and `open` remain per-flow operations. The v1 workspace
endpoint remains available for compatible local clients.

## FlowView

FlowView renders the screen-flow map above the selected flow. Selecting a flow
loads only that flow's architecture, vertical causal timeline, branches, state
changes, code lens, and cognitive debt. Steps from different flows are never
flattened into one timeline.

When a selected screen has several observed `user_action` roots, its FlowIR
also contains `scenarios`. FlowView selects one scenario before rendering the
timeline, architecture, and debt. These action-rooted paths are not additional
workspace flows and do not consume the one-to-three screen-flow limit.

The verified `VS Code에서 열기` action remains attached to the selected step and
is emitted only when the current manifest and source anchor still match.
