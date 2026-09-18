# Paths 4 & 5: Advanced Operations, Publication & Boundaries

This guide covers impact radar inspection, zero-hallucination boundary reporting, agent core flow publication, and environment diagnostics.

---

## 1. Impact Radar & Boundary Inspection (`report_unknowns`)

### Blast Radius Radar
When `get_flow_payload` is called with `compact: true`, `CompactRadarSummary` provides direct 1st-degree relationship metrics:
```json
"radarSummary": {
  "directCallers": 3,
  "directCallees": 5
}
```
Use these counts to assess immediate blast radius before refactoring or modifying a flow.

### Zero-Hallucination Boundaries (`report_unknowns`)
Inspect unresolvable dynamic dispatches, third-party library boundaries, and missing type definitions:

```json
{
  "name": "report_unknowns",
  "arguments": {
    "target": ".",
    "flowId": "flow-a1b2c3d4e5f6..."
  }
}
```

### Result Schema
```json
[
  {
    "subject": "PaymentGateway.charge",
    "reason": "dynamic_dispatch_target_unresolved: interface implemented by 3 external adapters"
  },
  {
    "subject": "ThirdPartySDK.track",
    "reason": "external_package_boundary: source code outside workspace"
  }
]
```

### Reporting Rules
- **Never Hallucinate**: Do not guess which interface implementation is executed at runtime.
- **Explain as `boundary` Gateways**: Present unresolvable hops as explicit `boundary` gateways with the exact reason returned by `report_unknowns`.

---

## 2. Agent Core Flow Publication (`publish_core_flow`)

When an agent synthesizes or verifies an end-to-end architecture flow, publish it using `publish_core_flow`:

```json
{
  "name": "publish_core_flow",
  "arguments": {
    "target": ".",
    "token": "optional-token",
    "artifact": {
      "entrySymbolPath": "src/controllers/order_controller.ts#OrderController.checkout",
      "title": "Order Checkout Flow",
      "layers": ["presentation", "controller", "usecase", "data"],
      "steps": [
        {
          "ordinal": 1,
          "name": "Validate Cart",
          "layer": "presentation",
          "kind": "guard",
          "anchor": {
            "repoRelativePath": "src/controllers/order_controller.ts",
            "byteRange": [120, 350],
            "fileHash": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
            "spanHash": "ca978112ca1bbdcafac231b39a23dc4da786081441609261a6ff58b231cc3649",
            "enclosingSymbolPath": "src/controllers/order_controller.ts#OrderController.checkout",
            "canonicalAstFingerprint": "ast-fp-1a2b3c4d"
          }
        }
      ]
    }
  }
}
```

### 6-Field Code Anchor Contract
Every step requires an exact, verifiable `anchor` with all 6 fields:
1. `repoRelativePath`: Workspace-relative path to source file.
2. `byteRange`: Exactly two integers `[startByte, endByte]`.
3. `fileHash`: SHA-256 hash of the entire file content.
4. `spanHash`: SHA-256 hash of the exact bytes within `byteRange`.
5. `enclosingSymbolPath`: Symbol path containing the span (`path#Type.member`).
6. `canonicalAstFingerprint`: Deterministic AST signature or span fingerprint.

### Anchor Mismatch Retry Protocol
If publication fails with `anchor_verification_failed` or `span_mismatch`:
1. Read the target file directly from disk to obtain the latest raw bytes.
2. Recompute `byteRange`, `fileHash`, and `spanHash` against the current bytes.
3. Retry `publish_core_flow` **once** with the corrected anchor.
4. If it fails again, abort and report the structural discrepancy to the user.

---

## 3. Interactive Step Approval & Journey Drafts

### In-Place Step Approval (`approve_step`)
Record user confirmation or refinement of step names and business rules:

```json
{
  "name": "approve_step",
  "arguments": {
    "target": ".",
    "flowId": "flow-a1b2c3d4e5f6...",
    "symbolPath": "src/controllers/order_controller.ts#OrderController.checkout",
    "name": "Validate Customer Balance",
    "rules": [
      "Ensure balance exceeds cart total",
      "Reject suspended accounts"
    ],
    "token": "optional-token"
  }
}
```

### Session Journey Drafts (`submit_flow_draft`)
Submit multi-step interactive exploration drafts to the append-only event ledger:

```json
{
  "name": "submit_flow_draft",
  "arguments": {
    "target": ".",
    "token": "optional-token",
    "artifact": {
      "artifactId": "draft-01",
      "submittedByAgent": {
        "name": "CodeFlowAgent",
        "sessionId": "sess-xyz"
      },
      "journeyDraft": {
        "flowIdRef": "flow-a1b2c3d4e5f6...",
        "proposedEntrySymbolPath": "src/controllers/order_controller.ts#OrderController.checkout",
        "steps": [
          {
            "ordinal": 1,
            "name": "Validate Customer Balance",
            "rationale": "Must verify solvency before lock",
            "anchor": { ... }
          }
        ]
      }
    }
  }
}
```

---

## 4. Environment & Toolchain Diagnosis (`codeflow doctor`)

When tools return adapter errors, compilation issues, or unexpected execution failures, guide the user to run the CLI diagnostic command in terminal:

```bash
codeflow doctor [target]
```

### What `doctor` Verifies
- **Language Adapters**: Dart SDK (`dart`), TypeScript / Node.js (`node`), Go (`go`).
- **Binary Permissions**: Adapter executables located in `$HOME/.local/bin` or bundled paths.
- **Layer Configuration**: Validity of `codeflow.layers.yaml` if present.
- **Storage Health**: Integrity of `.codeflow/` index and event ledgers.
