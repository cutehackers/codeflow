# Business journeys v1

## Purpose

FlowView reads a reviewed business objective before it reads screen topology.
A business journey is not generated from route names or inferred from code: it
is an explicit, confirmed definition that references current deterministic
scenarios. The referenced FlowIR remains the authority for code evidence,
causality, and trust.

## Stored definition

`.codeflow/knowledge/business-journeys.v1.json` contains a versioned list:

```json
{
  "version": "1",
  "journeys": [{
    "id": "register-push-token",
    "title": "푸시 알림을 받을 기기를 등록합니다",
    "outcome": "현재 기기에 알림을 보낼 수 있습니다",
    "segments": [
      {"flow_id": "system:push-token:lib/push.dart:PushRegistration:_registerToken", "scenario_id": "sha256:..."}
    ],
    "status": "confirmed"
  }]
}
```

`id` is reviewer-owned, lowercase, and stable across copy changes. A segment
always contains both a `flow_id` and its deterministic `scenario_id`. A
journey can contain at most 20 unique segments.

## Approval and staleness

`PUT /api/v1/business-journeys` is authenticated and validates that every
referenced scenario exists in the current same-Basis workspace. Every
consecutive pair must be rooted in different flows and have an observed
workspace edge. Scenarios with the same flow root are independent alternatives,
not an evidence-backed sequence.
The API rejects a definition that cannot be supported by current source.

## LLM and MCP approval path

Installed CodeFlow exposes `entry_points`, `prepare_workspace`,
`business_journeys`, `upsert_business_journey`, and `open_business_journey`
over MCP. The MCP server reads its loopback runtime credential internally;
LLMs and users never need to read or transmit it. `upsert_business_journey` is
a write action and is used only after the user explicitly asks to save or
update a journey. It delegates to the same authenticated Core validation as
the HTTP API.

`prepare_workspace` accepts one to three exact entry IDs and refuses to
replace a differently scoped running Core. This keeps every selected segment
on one current Basis and prevents an LLM session from silently repurposing
another reviewer's active analysis.

Later source changes can invalidate a scenario ID or an observed transition.
The saved definition is retained for review, but FlowView marks it as needing
reconnection and never presents it as an observed business path. Business copy
does not alter FlowIR, FlowDelta, facts, steps, or unknown status.

## System entry points

Supported entries are no longer limited to `route:/...`:

- `system:lifecycle:<path>:<owner>:initState|didChangeAppLifecycleState`
- `system:session:<path>:<owner>:<callback>` for a concrete session stream listener
- `system:push-token:<path>:<owner>:<callback>` for a concrete
  `onTokenRefresh.listen(callback)` subscription

`owner` is the declaring class name (or `top-level`) and prevents colliding
callback names in one file from being merged. The Dart adapter discovers only
these bounded AST shapes and Core re-resolves the selected callback before
publishing facts. An unsupported lifecycle or
dynamic callback stays unavailable or unknown; CodeFlow must not fabricate a
user action or a push-registration outcome.

## FlowView and export

The `business-journeys` region is the first navigation layer after the
snapshot. Selecting a journey opens its first verified segment; each segment
can then open its exact flow, scenario, causal timeline, and code lens. Links
for verified journey segments retain the journey and scenario selection.
Links to other screen-map or scenario entries deliberately leave that context,
so a non-member screen is never shown as part of the business journey. The
screen-flow map remains available as a secondary topology view.

Static exports retain the selected journey as context, but all journey and
segment controls become non-navigable labels.
