# Path 1: Flow Comprehension Guide

This guide covers discovering, slicing, compressing, and explaining business execution flows using CodeFlow's core intelligence pipeline.

---

## 1. Discover Entry Points (`harvest_flows`)

Discover candidate entry points corresponding to the user's intent or query:

```json
{
  "name": "harvest_flows",
  "arguments": {
    "target": ".",
    "query": "login submit"
  }
}
```

### Discovery Guidelines
- **Multi-term Query**: Words are split and matched against `entrySymbolPath`, `intentSignals` (class, method, doc comments), `markerKind`, and `triggerClass`.
- **Target Parameter**: Always specify the repository root (`.` or absolute path).
- **Candidate Disambiguation**:
  - If a single candidate clearly matches the user's intent, proceed directly to `analyze_flow`.
  - If multiple plausible candidates are found, list the top candidates with their `entrySymbolPath` and intent summary, and ask the user to confirm.

---

## 2. Slice and Publish Flow (`analyze_flow`)

Extract the deterministic AST execution trace starting from the selected entry point:

```json
{
  "name": "analyze_flow",
  "arguments": {
    "target": ".",
    "entrySymbolPath": "lib/features/auth/login_controller.dart#LoginController.submit"
  }
}
```

### Analysis Guidelines
- **Deterministic Traversal**: Slices call chains across architectural layers (controller -> usecase -> repository -> data).
- **Preserve `flowId`**: Save the returned `flowId` (e.g., `flow-a1b2c3d4e5f6...`). It is required for all downstream operations (`get_flow_payload`, `open_review`, `report_unknowns`).

---

## 3. Retrieve High-Density Briefing (`get_flow_payload`)

**Mandatory**: Always request the compact representation first to preserve token budget (~500 tokens):

```json
{
  "name": "get_flow_payload",
  "arguments": {
    "target": ".",
    "flowId": "flow-a1b2c3d4e5f6...",
    "compact": true
  }
}
```

### Structure of `CompactFlowPayload`
```json
{
  "flowId": "flow-a1b2c3d4e5f6...",
  "summary": "LoginController.submit -> AuthUseCase.login -> UserRepository.authenticate -> AuthState.update",
  "frames": [
    {
      "id": "f1",
      "gate": "entry",
      "symbol": "LoginController.submit",
      "steps": 2,
      "file": "lib/features/auth/login_controller.dart:42"
    },
    {
      "id": "f2",
      "gate": "decision",
      "symbol": "LoginValidator.validate",
      "steps": 3,
      "file": "lib/features/auth/login_validator.dart:15"
    },
    {
      "id": "f3",
      "gate": "process",
      "symbol": "AuthUseCase.login",
      "steps": 4,
      "file": "lib/features/auth/auth_usecase.dart:28"
    },
    {
      "id": "f4",
      "gate": "effect",
      "symbol": "UserRepository.authenticate",
      "steps": 2,
      "file": "lib/features/auth/user_repository.dart:55"
    },
    {
      "id": "f5",
      "gate": "result",
      "symbol": "AuthState.update",
      "steps": 1,
      "file": "lib/features/auth/auth_state.dart:80"
    }
  ],
  "radarSummary": {
    "directCallers": 2,
    "directCallees": 4
  }
}
```

---

## 4. Explaining the Flow to Developers

### Macro Gateway Progression
Present the execution path as an orderly progression through 4 to 7 macro gateways:

| Gateway Role | Semantic Meaning | What to Highlight |
|:---|:---|:---|
| `entry` | Flow trigger | UI event handler, HTTP route, messaging consumer, CLI action |
| `decision` | Guard & validation | Input validation, auth checks, precondition branches |
| `process` | Core orchestration | Domain logic execution, data transformation, business calculations |
| `effect` | State mutation & I/O | Database updates, remote API calls, cache writes, event emits |
| `result` | Output & response | View model update, HTTP response payload, success/failure status |
| `boundary` | Architectural limit | Unresolved dynamic dispatch, external SDK boundary, cutoff point |

### Micro Drill-Down with Source Anchors
When describing individual steps:
1. Reference the exact file location provided in `CompactFrame.file` (`repo/relative/path:line`).
2. If line-level verification is needed, read the local file around that line range.
3. State the step kind (`call`, `guard`, `mutation`, `result`) and business rationale.

### Anti-Telemetry Enforcement
- **Never mention**: compiler epochs, build timestamps, lock acquisition times, cache hit ratios, or internal settlement flags.
- **Always focus on**: user-facing business flow, architectural layers, and verifiable code behavior.
