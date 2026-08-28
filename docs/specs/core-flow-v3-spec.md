# Core Flow v3 Spec — Architecture-Layer Based Core Flow with Agent-Authored Intermediate Artifact

- Status: FINAL — decisions §13 resolved 2026-08-29, ready for implementation
- Date: 2026-08-29
- Base: `AGENTS.md:purpose` core flow definition, `docs/design-v2.md`, `schemas/flowspec.schema.json` v2, `docs/llm-usage.md` v2, research 2026-08-29
- Target: production ready — implementable as-is
- Resolved: D1 A, D2 A, D3 A, D4 A, D5 A, D6 B, D7 keep all, D8 MCP only (see §13)

---

## 1. Purpose and scope

### 1.1 One-sentence purpose

Codeflow shows the code flow the user actually asked for — from the first event in the entry architecture layer through every layered handling step until the request is done — as a verified, evidence-backed FlowView.

### 1.2 Core flow definition (normative, `AGENTS.md:purpose` 계승)

> Core flow (핵심 흐름) — the flow the user requested: the complete set of architecture-layer traversals that starts at the initial event in the entry layer (UI action, system event, route, or equivalent layer-defined trigger) and follows every handling step through each architecture layer until processing completes. When the user says "requested flow", "core flow", or "the implementation flow I want to understand", treat it as this definition. Non-core statements that do not advance the layer traversal do not belong in the core flow.

Implications:

- Flow starts at an architecture-layer event, not at an arbitrary symbol found by suffix.
- A flow is a cross-layer traversal, not a per-method list.
- A step belongs to the core flow iff it advances the traversal (layer transition, state mutation that changes the traversal outcome, boundary hand-off, or branching that decides the traversal path). Local helper Plumbing that stays inside one layer without changing the traversal outcome is not core.

### 1.3 Goals

- G1 Every core flow rendered in FlowView has an explicit `layer` per step and is ordered by layer traversal, not by statement order alone.
- G2 Zero meaningless data by default: FlowView shows core steps first; non-core detail is collapsed and never counted as core.
- G3 User requests a core flow in one natural-language sentence via the installed skill, with no manual `entrySymbolPath` or layer input.
- G4 Every rendered step is anchored and verified against the current worktree — hallucinated anchors are rejected before publish.
- G5 One-shot setup and uninstall guarantee stays intact (`scripts/install.sh` / `codeflow uninstall`).

### 1.4 Non-goals

- No cloud service, no LLM call inside CORE, no product-code mutation.
- No replacement of the discovery track (`harvest_flows` / `codeflow publish` stays for browsing). Core flow creation uses a separate verified path.
- No automatic architecture inference that tries to guess layers without agent input — agent supplies layers, CORE verifies anchors.

---

## 2. Background — why the current pipeline cannot express core flow

Evidence from 2026-08-29 review (`adapters/dart/lib/src/harvest.dart:372`, `profile.dart:74`, `adapters/dart/lib/src/slice.dart:1003`, `internal/harvest/score.go:239`, `internal/fusion/fusion.go:165`, `cmd/codeflow/main.go:235`):

- Harvest classifies by class-name suffix (`Notifier/Controller/UseCase`). Project-specific layer maps (`feature/presentation/domain/data`) are unknown, so entry detection is syntactically correct but architecturally wrong.
- `RootEquivalenceKey = className` collapses independent intents on the same screen into one root candidate; most real core flows are hidden as `dedupedInto`.
- Slicing extracts every `if/state=/foo();` via regex and promotes it to a step. No layer-significance weight exists, so `scaffold/theme/log` calls become first-class steps.
- Depth-5 traversal follows every resolvable call regardless of whether it crosses a layer boundary.
- Fusion maps sliced steps 1:1 to `FlowStep` with no compression.
- Publish selects `pinned + top-N by score` with no business-flow cut, so the top-50 mixes helper flows with real core flows.
- FlowView renders `ordinal` order with equal visual weight; lane inference is suffix-based (`internal/flowview/layers.go:InferLayer`).

Fixing harvest/slice alone cannot close the gap — layer semantics are project-specific and require the agent's semantic reading of the codebase. The pipeline must therefore split.

---

## 3. Architecture — two-track model

```text
                    ┌─────────────────────────┐
                    │  User (natural language) │
                    │  "회원가입 핵심 흐름 보여줘" │
                    └────────────┬────────────┘
                                 │ triggers skill
                                 ▼
                    ┌─────────────────────────┐
                    │  Codex / coding agent    │
                    │  skill: skills/codeflow/ │
                    │  SKILL.md (v3)            │
                    │  - explores codebase      │
                    │  - identifies entry-layer │
                    │    event + layer map      │
                    │  - builds layer-annotated │
                    │    intermediate artifact  │
                    └────────────┬────────────┘
                                 │ MCP publish_core_flow
                                 ▼
┌──────────────┐   ┌──────────────────────────┐   ┌──────────────┐
│ Target Repo  │──▶│  CORE (Go single binary) │──▶│  FlowView    │
│ (read-only)  │   │  Verifier + Storage +     │   │  (loopback)  │
└──────────────┘   │  Fusion + Publish         │   └──────────────┘
                   │                          │
                   │  Track A (core):         │
                   │   verify → fuse → commit │
                   │  Track B (discovery):    │
                   │   harvest/slice/publish  │
                   │   (unchanged, browsing)  │
                   └──────────────────────────┘
                               │
                               ▼
                        .codeflow/ (same layout, §6.1 design-v2)
```

- Track A — core flow creation. Agent-authored, CORE-verified. This spec.
- Track B — discovery. Existing `harvest_flows` / `analyze_flow` / `codeflow publish` stays for candidate browsing. Not used for core flow publication.

CORE never calls an LLM. All layer labels come from the agent; CORE only verifies anchors and persists.

---

## 4. Contracts

### 4.1 FlowSpec extension — additive only, backward compatible

`schemas/flowspec.schema.json` is extended. Consumers that do not understand `layer` degrade gracefully (treat absent layer as `unknown`).

#### 4.1.1 New `layer` on `steps[]`

Add to `schemas/flowspec.schema.json:properties.steps.items.properties`:

```json
"layer": {
  "type": "string",
  "enum": ["presentation","controller","usecase","domain","data","infra","external","unknown"],
  "description": "Architecture layer this step advances. Supplied by the agent in the intermediate artifact; verified steps keep it. Absent in legacy specs — consumers treat as 'unknown'. Used to order and lane the FlowView."
}
```

Spec enum is closed for the contract, but agent may submit project-specific raw labels (e.g. `feature/order/presentation`). CORE normalizes via `normalizeLayer` (§4.1.3). Unknown raw values become `unknown` and are recorded in `unknowns[]` as `reason: unresolved_type` rather than rejected.

Add to `edges[]` in the same schema:

```json
"toLayer": {
  "type": "string",
  "enum": ["presentation","controller","usecase","domain","data","infra","external","unknown"],
  "description": "Layer of the delegation target, when known. Mirrors step layer semantics."
}
```

`toLayer` is OPTIONAL. Existing specs without it remain valid.

#### 4.1.2 Core artifact schema (input to `publish_core_flow`)

This is the MCP input shape, validated against a new schema `schemas/core-artifact.schema.json` (new file) before any fusion. It is intentionally stricter than the stored FlowSpec — it requires `layer` and a minimal anchor.

```json
{
  "$id": "https://codeflow.local/schemas/core-artifact.schema.json",
  "type": "object",
  "required": ["entrySymbolPath", "title", "steps"],
  "additionalProperties": false,
  "properties": {
    "flowId": { "$ref": "https://codeflow.local/schemas/identity.schema.json#/$defs/flowId" },
    "entrySymbolPath": { "$ref": "https://codeflow.local/schemas/identity.schema.json#/$defs/canonicalEntrySymbolPath" },
    "title": { "type": "string", "minLength": 1, "maxLength": 120 },
    "description": { "type": "string", "maxLength": 300 },
    "layers": {
      "type": "array",
      "items": { "type": "string", "enum": ["presentation","controller","usecase","domain","data","infra","external"] },
      "description": "Ordered layer traversal declared by the agent for this flow, e.g. [\"presentation\",\"controller\",\"usecase\",\"data\",\"external\"]. Used to validate step layer ordering."
    },
    "steps": {
      "type": "array",
      "minItems": 1,
      "maxItems": 64,
      "items": {
        "type": "object",
        "required": ["ordinal","name","layer","kind","anchor"],
        "additionalProperties": false,
        "properties": {
          "ordinal": { "type": "integer", "minimum": 1 },
          "name": { "type": "string", "minLength": 1, "maxLength": 120 },
          "layer": { "type": "string", "enum": ["presentation","controller","usecase","domain","data","infra","external"] },
          "kind": { "type": "string", "enum": ["guard","mutation","call","branch"] },
          "description": { "type": "string", "maxLength": 400 },
          "anchor": { "$ref": "https://codeflow.local/schemas/identity.schema.json#/$defs/anchor" },
          "stateDelta": {
            "type": "object",
            "required": ["before","after"],
            "properties": { "before": {"type":"string"}, "after": {"type":"string"} },
            "additionalProperties": false
          },
          "sideEffect": { "type": "string" },
          "branch": { "type": "string" },
          "rules": { "type": "array", "items": {"type":"string"} }
        }
      }
    },
    "edges": {
      "type": "array",
      "maxItems": 64,
      "items": {
        "type": "object",
        "required": ["stepOrdinal","toSymbolPath","kind","resolutionStatus"],
        "additionalProperties": false,
        "properties": {
          "stepOrdinal": {"type":"integer","minimum":1},
          "toSymbolPath": {"type":"string"},
          "toLayer": {"type":"string","enum":["presentation","controller","usecase","domain","data","infra","external","unknown"]},
          "kind": {"type":"string","enum":["resolved_cross_file","boundary_call","unknown_edge"]},
          "resolutionStatus": {"type":"string","enum":["resolved","unresolved_dynamic","unresolved_type","truncated"]}
        }
      }
    },
    "unknowns": {
      "type": "array",
      "items": {
        "type":"object",
        "required":["subject","reason"],
        "properties":{
          "subject":{"type":"string"},
          "reason":{"type":"string","enum":["unresolved_dynamic_call","unresolved_type","truncated_traversal","no_evidence","stale_anchor","adapter_error"]}
        }
      }
    }
  }
}
```

Limits (`steps max 64`, `edges max 64`) bound prompt cost and render cost. `maxItems` is contract-enforced; exceeding it yields error `artifact_too_large` (§7).

#### 4.1.3 Layer normalization and `codeflow.layers.yaml` (D6 B — CORE reads and validates)

Project layer map file: `codeflow.layers.yaml` at repo root (next to `codeflow.flows.yaml`). Optional but expected — CORE reads it on every `publish_core_flow` call (no cache beyond the call). When present it is the source of truth for allowed layers, aliases, and path hints. When absent CORE falls back to the 8 canonical values with a warning `unknown` entry rather than failing.

File shape (normative, `schemas/layers-config.schema.json` new):

```yaml
version: 1
layers:
  - name: presentation        # canonical enum value (must be one of the 8)
    aliases: [ui, view, widget]
    pathPatterns: ["**/presentation/**", "**/features/*/presentation/**"]
  - name: controller
    aliases: [controller]
    pathPatterns: ["**/controller/**", "**/notifier/**"]
  - name: usecase
    aliases: [application, service, interactor, use_case]
    pathPatterns: ["**/usecase/**", "**/domain/usecase/**"]
  - name: domain
    aliases: [domain, entity]
    pathPatterns: ["**/domain/**"]
  - name: data
    aliases: [repository, datasource, data_source]
    pathPatterns: ["**/data/**", "**/repository/**"]
  - name: infra
    aliases: [infrastructure, platform]
    pathPatterns: ["**/infra/**"]
  - name: external
    aliases: [api, remote, gateway, client]
    pathPatterns: ["**/api/**", "**/external/**"]
strictOrder: true            # when true, layer order violation is an error (D5 A). When false, violation is a warning + unknowns[] entry.
allowUnknownLayer: false      # when false, raw layer not in name/aliases → layer_order_violation error. When true → maps to unknown with warning.
```

Go type `internal/fusion/layers.go` (new):

```go
type LayersConfig struct {
  Version int           `yaml:"version"`
  Layers  []LayerDef    `yaml:"layers"`
  StrictOrder bool      `yaml:"strictOrder"`
  AllowUnknownLayer bool `yaml:"allowUnknownLayer"`
}
type LayerDef struct {
  Name         string   `yaml:"name"`
  Aliases      []string `yaml:"aliases"`
  PathPatterns []string `yaml:"pathPatterns"` // doublestar globs, matched against anchor.repoRelativePath
}
```

`normalizeLayer(raw string) (canonical string, unknown bool)`:

- Lower-cases, trims, takes last segment after `/` when raw contains `/`.
- Looks up `LayersConfig` (if file present): exact match on `name` or any `aliases` → canonical `name`. If file absent → built-in alias table (`ui,view,widget → presentation` etc.).
- If still not found → `unknown, true`. When `allowUnknownLayer==false` in config, caller returns `layer_order_violation` / `schema_validation_failed` instead of silently mapping (strict mode).

`validateLayerOrder` also consults `LayersConfig.StrictOrder`. Path-pattern check is advisory: if `pathPatterns` is non-empty for a layer and `anchor.repoRelativePath` matches none of them, CORE records a warning `unknowns[] {subject: stepOrdinal, reason:"unresolved_type"}` with message `path does not match layer patterns` but does not fail unless `strictOrder==true` and the mismatch is paired with a backward hop.

The closed enum in the stored FlowSpec stays the 8 canonical values; project-specific directory conventions are expressed via `codeflow.layers.yaml` aliases/patterns, not via new enum values.

---

## 5. MCP — new tool `publish_core_flow`

### 5.1 Tool registration

Add to `internal/mcp/server.go:listTools()` (§229):

```json
{
  "name": "publish_core_flow",
  "description": "Publish a verified architecture-layer core flow from an agent-authored intermediate artifact. Verifies every anchor against the current worktree; on mismatch returns a correctable error without persisting.",
  "inputSchema": {
    "type": "object",
    "required": ["artifact"],
    "properties": {
      "artifact": { "$ref": "https://codeflow.local/schemas/core-artifact.schema.json" },
      "token": { "type": "string", "description": "Auth token when RequireToken=true (FlowView server). Omitted in local dev." }
    }
  }
}
```

Existing 7 tools keep their names and schemas (`docs/llm-usage.md:58` table extended by one row).

### 5.2 Execution semantics

File: `internal/mcp/server.go:executeTool` new `case "publish_core_flow":`

Pseudocode (normative order):

```
1. checkAuth(token) if cfg.RequireToken
2. rawArtifactBytes = json.Marshal(args["artifact"])
3. secret.RedactJSON(rawArtifactBytes)  // same gate as slicing/fusion
4. contractharness.Validate(core-artifact.schema.json, sanitized)
     on fail → return error {code:"schema_validation_failed", details: validation errors}
5. unmarshal to CoreArtifact struct

6. FOR EACH step s IN artifact.steps (in ordinal order):
     a. verify anchor file exists: os.ReadFile(RepoRoot + "/" + s.anchor.repoRelativePath)
        - not found → error AnchorError{ordinal, reason:"file_not_found"}
     b. recompute fileHash = sha256(fileBytes) hex
        - if fileHash != s.anchor.fileHash → still continue (file changed since agent read), but record as stale condition
     c. slice span = fileBytes[s.anchor.byteRange[0]:s.anchor.byteRange[1]]
        - out of bounds → error AnchorError{ordinal, reason:"byte_range_out_of_bounds", expected:fileLen}
     d. spanHash = sha256(span) hex → must equal s.anchor.spanHash
        - mismatch → error AnchorError{ordinal, reason:"span_hash_mismatch", hint:"re-read file and recompute anchor"}
     e. canonicalAstFingerprint is NOT recomputed here (adapter-specific). Store as-is; freshness will be derived later via fusion checkFreshness against enclosingSymbolPath.
     f. enclosingSymbolPath must be non-empty and match s.anchor.repoRelativePath's symbol table via simple existence check (scan file for dotted last segment). Missing → error AnchorError{ordinal, reason:"enclosing_symbol_not_found"}

   On first error → return error with structured payload (see §7) WITHOUT persisting. Do not partially publish.

7. Load and apply `codeflow.layers.yaml` (D6 B):
     - Read `<RepoRoot>/codeflow.layers.yaml` if present (yaml). On parse error → return error {code:"layers_config_invalid", details: yaml error, retryable:false}.
     - Build normalized alias map and path-pattern globs from the file; when file is absent use the built-in alias table and 8 canonical layers.
     - On `allowUnknownLayer==false` and raw layer not in name/aliases → error LayerError{ordinal, reason:"layer_order_violation", hint:"add alias to codeflow.layers.yaml or use a canonical layer"}.

8. Validate layer traversal:
     - If artifact.layers is present, ensure every s.layer appears in that order (monotonic non-decreasing by index in layers[]). A step whose layer goes backward (e.g. data → presentation) is allowed only if kind==branch (error path); otherwise → error LayerError{ordinal, reason:"layer_order_violation"} when StrictOrder==true, else warning + unknowns[] entry.
     - If artifact.layers is absent, infer expected order from first occurrence of each distinct layer in steps[] order — no error, only used for render.
     - Path-pattern advisory: for each step, if its layer's pathPatterns is non-empty and anchor.repoRelativePath matches none, record unknowns[] {subject:"step:<ordinal>", reason:"unresolved_type"} with message "path does not match layer patterns in codeflow.layers.yaml".

9. Normalize layers: for each step/edge, s.layer = normalizeLayer(s.layer) via LayersConfig; record unknowns/warnings for raw→unknown mappings when allowUnknownLayer==true, else already errored at step 7.

10. Compute basisSha:
     - If artifact contains basisSha → use it.
     - Else compute storage.ComputeWorktreeFingerprint(RepoRoot, unique file parts of steps[].anchor.repoRelativePath + entrySymbolPath file part + codeflow.layers.yaml when present)
     - basisSha is hex64.

11. Build slicing.SlicedPayload in-memory (no adapter call):
     - For each step → slicing.SliceStep{Ordinal, Kind, Description: s.name/description, SymbolPath: s.anchor.enclosingSymbolPath, Anchor: s.anchor, GuardCondition/StateBefore/After/EffectTarget from s.branch/stateDelta/sideEffect}
     - For each edge → slicing.SliceEdge{Kind, ToSymbolPath, ResolutionStatus, Depth: layer distance (presentation=0…external=6), StepOrdinal}
     - Truncated = false unless artifact declares it (no depth cap to re-apply).

12. Fuse: fusion.Fuse(sliced, FuseOptions{
        CustomTitle: artifact.title,
        CustomDescription: artifact.description,
        RepoRoot: cfg.RepoRoot,
        BasisSha: basisSha,
        // No session/approved overrides on core publish — core artifact is already authoritative. E2/E3 still apply on top for later in-place edits.
     })
     Fuse derives CodeLens via deriveCodeLens(RepoRoot, anchor) for every step, so the stored FlowSpec always has presentation lens even though the artifact did not carry lines.

13. Atomically publish as new generation (same mechanism as analyze_flow:469):
     - existingPtr = storage.ReadPointer()
     - existingIdx = storage.ReadLatestIndex() if ptr exists
     - sess = storage.BeginGeneration(basisSha)
     - copy all existing flows except same FlowID (replace semantics)
     - sess.AddFlowSpec(spec.FlowID, specBytes, FlowSummary{FlowID:spec.FlowID, Title:spec.Title, Description:spec.Description, EntrySymbolPath:artifact.entrySymbolPath, StepCount:len(spec.Steps)})
     - sess.Commit()

14. Return success payload:
     { status:"published", flowId, title, stepCount, basisSha, url, token, warnings: []string (layer normalizations, path-pattern advisories, codeflow.layers.yaml fallback) }
     url/token come from FlowView server if running; otherwise url = "" and caller may call open_review next.

Idempotency: same artifact published twice with identical flowId and identical spanHashes results in same generation content; second Commit overwrites prior generation entry for that flowId (replace, not duplicate). No duplicate flowIds.
```

### 5.3 Auth, concurrency, timeouts

- Auth reuses `checkAuth` (§314). Token is per-run FlowView token when `RequireToken=true`; otherwise absent.
- Concurrency: MCP `Server` is single-process; `storage.BeginGeneration` / `storage.Commit` is already atomic via pointer rename (`docs/design-v2.md:6.3`). Concurrent `publish_core_flow` calls serialize on the generation commit — last writer wins for same flowId.
- Timeout: entire tool call inherits the MCP `ctx` (no extra timeout). Anchor verification is file I/O bound; expected p50 <200ms for 15 steps on SSD.
- Max artifact size: JSON after redaction ≤ 512 KiB. Exceeding returns `artifact_too_large`. `maxItems` in schema enforces step/edge counts.

---

## 6. Storage and fusion

### 6.1 Storage layout

Unchanged: `.codeflow/` layout per `docs/design-v2.md:6.1`. New artifact does not create a new top-level dir. Generation layout stays:

```
.codeflow/
  pointer.json
  generations/<generationId>/{flows/<flowId>.json, index.json}
  facts/  semantics/  (unchanged)
```

`FlowSummary` written by `publish_core_flow` includes `LayerCount` implicitly via steps; no new index field required for v1. Optional future: add `layers: string[]` to `GenerationIndex` flows entries — additive, not required for this spec.

### 6.2 Fusion changes

- `internal/fusion/fusion.go:FlowStep` gains `Layer string json:"layer,omitempty"` (additive).
- `internal/fusion/fusion.go:FlowEdge` gains `ToLayer string json:"toLayer,omitempty"`.
- `internal/fusion/fusion.go:Fuse` preserves `s.Layer` from sliced payload into `FlowStep.Layer` (or `unknown` if absent). Existing `provenance/freshness/confidence/basisSha/anchor` handling unchanged.
- `internal/fusion/fusion.go:deriveCodeLens` unchanged — it already derives `viewStartLine/viewEndLine` from `anchor.symbolRange` when present.

New file `internal/fusion/layers.go` (or extend `internal/flowview/layers.go`): `NormalizeLayer`, `LayerOrder` map (`presentation:0 … external:6, unknown:99`), `ValidateLayerOrder`.

No change to `checkFreshness` semantics; stale/orphaned still driven by `anchor.canonicalAstFingerprint` mismatch. Layer mismatch alone does not make a step stale.

---

## 7. Errors — structured, correctable

All `publish_core_flow` anchor/layer errors return MCP `isError:true` with `content[0].text` as JSON:

```json
{
  "code": "anchor_verification_failed",
  "message": "anchor verification failed at ordinal 4",
  "details": [
    {
      "ordinal": 4,
      "field": "anchor.byteRange",
      "reason": "span_hash_mismatch",
      "expected": "sha256 hex of actual span",
      "hint": "re-read HOME/workspace/<repo>/lib/features/auth/join_controller.dart around JoinController.submit and recompute byteRange/fileHash/spanHash; enclosingSymbolPath must be 'JoinController.submit'",
      "path": "lib/features/auth/join_controller.dart"
    }
  ],
  "retryable": true
}
```

Codes:

| code | when | retryable |
|---|---|---|
| `schema_validation_failed` | core-artifact schema violation (missing layer, ordinal gap, maxItems) | true — fix payload |
| `anchor_verification_failed` | file_not_found / byte_range_out_of_bounds / span_hash_mismatch / enclosing_symbol_not_found | true — re-read file |
| `layer_order_violation` | step layer goes backward outside a branch | true — reorder or mark branch step |
| `artifact_too_large` | JSON >512 KiB or steps/edges >64 | false — split flow |
| `layers_config_invalid` | `codeflow.layers.yaml` parse error or unknown canonical name | false — fix yaml |
| `storage_commit_failed` | `BeginGeneration/Commit` I/O error | true — retry |
| `unauthorized` | bad token when RequireToken | false |

Agent behavior on `anchor_verification_failed`: re-read the cited file, recompute the 6 anchor fields, and retry `publish_core_flow` once before asking the user.

---

## 8. FlowView — render by explicit layer

### 8.1 Scope

Changes limited to `internal/flowview/` — no new JS framework, monochrome stays.

### 8.2 Data source

FlowView already reads `schemas/flowspec.schema.json` via `storage.ReadActiveFlowSpec`. After this spec it reads `steps[].layer` and `edges[].toLayer` when present; fallback to `internal/flowview/layers.go:InferLayer` when absent (legacy specs).

### 8.3 Rendering rules

- Lane order is canonical layer order: `presentation → controller → usecase → domain → data → infra → external → unknown`. A flow that only uses a subset shows only those lanes.
- Primary timeline shows core steps only — every step in the stored FlowSpec is core by definition (core publish only stores core steps). Discovery flows (legacy) keep showing all steps but render with `layer==unknown` in a single lane.
- Non-core detail does not exist in a core FlowSpec. If the agent included a helper as a separate step, it is a core step by declaration — review feedback (`approve_step`) is the mechanism to demote it, not automatic hiding.
- Edge lane hop is drawn from `step.layer` to `edge.toLayer` (or inferred from `toSymbolPath` when `toLayer` absent). `unresolved_dynamic` edges render as dashed with label `미확인 위임`.
- CodeLens uses `deriveCodeLens` (§6.2) — view window is symbol-scoped (`anchor.symbolRange` when present, else `focus ±12`). No new lens logic.

### 8.4 What does NOT change

- Quick switcher (`Cmd+K`), auth token flow (`?token=`), `open_review` semantics, generation pointer, watch polling — unchanged.
- Monochrome palette, no new view modes.

---

## 9. Skill and LLM usage

### 9.1 Skill `skills/codeflow/SKILL.md` (new SKILL.md v3)

Frontmatter:

```yaml
---
name: codeflow
description: Turn a requested core flow / 핵심 흐름 / architecture-layer flow into a verified, evidence-backed FlowView via the installed CodeFlow MCP.
---
```

Body (normative 5 steps, replaces 17-line v2):

```md
# CodeFlow Core Flow

Use this skill when the user asks to understand, explain, or visualize a code/business/core flow, architecture layer flow, or any implementation flow they want to understand.

1. Explore the codebase to locate the entry-layer initial event for the user's intent and trace its layer traversal to completion. Prefer repo structure (feature/*/presentation|domain|data) and DI/provider graph over class-name suffixes.
2. Build a layer-annotated intermediate artifact and call `publish_core_flow` with it. Every step MUST carry layer, kind, name, and a 6-field anchor (repoRelativePath, byteRange, fileHash, spanHash, enclosingSymbolPath, canonicalAstFingerprint). If the project has an explicit layer map, honor it.
3. If `publish_core_flow` returns anchor_verification_failed, re-read the cited file, recompute that step's anchor, and retry once before asking the user.
4. Call `get_flow_payload` with the returned flowId and explain steps in layer order. Surface provenance, freshness, and every unknown without inference. Use `report_unknowns` before filling a gap.
5. When the user asked for a FlowView, call `open_review` with that flowId and give its returned URL (contains ?token=). Do not open FlowView speculatively.
```

Trigger phrases that must match via Codex skill search (for `description` indexing): `core flow, 핵심 흐름, 아키텍처 흐름, 코드 흐름, business flow, 레이어 흐름`.

### 9.2 `docs/llm-usage.md` delta

- §0 install unchanged.
- §1 shortest flow: `harvest_flows → get_flow_payload → open_review` replaced by primary path `explore → publish_core_flow → get_flow_payload → open_review`; keep `harvest_flows` as alternative path labeled `discovery (browsing, not core)`.
- §2 MCP tools table: add `publish_core_flow | 핵심 흐름 발행 | artifact{entrySymbolPath,title,description,layers,steps[anchor+layer],edges} | 사용자 요청 핵심 흐름 — 앵커/레이어 검증 후 원자적 게시 (codeflow.layers.yaml 있으면 그에 맞춰 검증)`.
- §3 reading order: insertion `steps[].layer` — `layer` by canonical order `presentation→…→external` (codeflow.layers.yaml가 있으면 그 순서); `edges[].toLayer` for lane hops. No inference when `resolutionStatus==unresolved_dynamic`.
- §3 add: `codeflow.layers.yaml`가 있으면 모든 layer/alias/pathPatterns는 그 파일을 따른다. 에이전트는 탐색 전 이 파일을 먼저 읽는다.
- §8 checklist: add `[ ] publish_core_flow artifact의 모든 앵커가 검증됐는가? layer가 codeflow.layers.yaml에 정의된 값인가? layer 순서가 단조 증가하는가 (branch 제외)?`

No CLI. Core flow publish is MCP-only (D8). `docs/local-usage.md` does not gain a `publish-core` CLI entry.

---

## 10. Security and privacy

- Secret gate unchanged: every artifact `json.Marshal` passes through `secret.RedactJSON` before validation and before storage — same entrance as slice/fusion.
- Loopback + per-run token + Host/Origin check + CSRF defense stays per `docs/design-v2.md:11.3`. `publish_core_flow` respects `checkAuth`.
- No secret, token, or `pointer.json` is returned in success payloads.

---

## 11. Performance and limits

- Verification is O(steps) file reads; typical core flow 8–15 steps → <200ms.
- Storage commit reuse keeps single-writer atomicity; no new lock needed.
- Limits enforced by schema: `steps ≤64`, `edges ≤64`, artifact JSON ≤512 KiB. Exceeding is a correctable error, not a silent truncation.
- Slice cache is bypassed for core publish (payload is agent-built). No `facts/slice/` entry is written for core flows.

---

## 12. Testing and acceptance

### 12.1 Unit

- `internal/fusion/layers_test.go`: `NormalizeLayer` alias table, slash-trim, unknown mapping.
- `internal/fusion/fusion_test.go`: `Fuse` preserves `layer`/`toLayer`, backward compat when absent.
- `internal/mcp/server_test.go`: `publish_core_flow` happy path, `schema_validation_failed`, `anchor_verification_failed` (span_hash_mismatch, file_not_found, byte_range_out_of_bounds), `layer_order_violation`, `artifact_too_large`, idempotency (same flowId twice → one generation entry).

### 12.2 Integration — E2E in `internal/installation/lifecycle_test.go` style

- Isolated `HOME` + fake `codex` stub is not needed here; core flow E2E instead uses a temp repo under `t.TempDir()` with 3 Dart files (presentation/controller/usecase_data) and a pre-built artifact with real file hashes.
- Sequence under test: `publish_core_flow(valid) → get_flow_payload → open_review` and `publish_core_flow(bad byteRange) → expect anchor_verification_failed → fix → publish succeeds`.

### 12.3 Contract

- `schemas/core-artifact.schema.json` ↔ Go structs drift check (like `contractharness.Validate` for other schemas).
- `schemas/flowspec.schema.json` v3 validates both legacy specs (no layer) and new specs (with layer/toLayer).

### 12.4 Manual acceptance

- User prompt: `이메일 회원가입 핵심 흐름을 FlowView로 만들어줘` in a fresh Codex task on `HOME/workspace/sgp-981-app` produces a FlowView URL whose lanes are `presentation→controller→usecase→data` and whose step count matches the artifact's core steps (no collapsed plumbing shown).
- `codeflow uninstall` still removes only owned assets (`internal/installation/uninstall.go:25`).

---

## 13. Decisions — resolved 2026-08-29

**D1 — Layer enum closed vs open — A (closed 8 canonical)**
Contract closed to 8 values, aliases normalized, raw unknown → `unknown` with `unknowns[]`. Implemented via `codeflow.layers.yaml` aliases + built-in fallback.

**D2 — Language of layer labels — A (English canonical, Korean at render)**
Stored `layer` is English enum only; FlowView i18n maps `presentation→프레젠테이션` etc.

**D3 — Required vs optional `layers[]` — A (optional)**
`layers` optional; validates monotonic order only when present.

**D4 — Scope of `publish_core_flow` vs discovery — A (coexist)**
Core flows only via `publish_core_flow`; `codeflow publish`/`harvest_flows` stay for discovery/browsing.

**D5 — Truncation and large flows — A (reject, do not truncate)**
`steps>64` or `JSON>512 KiB` → `artifact_too_large`; agent splits via `supersedes`.

**D6 — Project layer map — B (CORE reads `codeflow.layers.yaml`)**
File at repo root, read and validated on every `publish_core_flow` call (§4.1.3, §5.2 steps 7–9). Absent → fallback to 8 canonical with warning; invalid yaml → `layers_config_invalid`.

**D7 — Skill trigger wording — keep all**
`핵심 흐름, 아키텍처 흐름, 코드 흐름, 레이어 흐름, 비즈니스 흐름 + core flow, architecture flow` — no removal.

**D8 — CLI counterpart — MCP only**
No `codeflow publish-core` CLI in this milestone. Core publish is MCP-only.

---

## 14. Rollout

- Phase 1 — contracts (`core-artifact`, `layers-config`, `flowspec` extension) + `codeflow.layers.yaml` fixture + `internal/fusion/layers.go` + tests for `NormalizeLayer`/`LayersConfig` parsing.
- Phase 2 — MCP `publish_core_flow` with anchor + layer validation (reads `codeflow.layers.yaml`) + fusion/storage + contract tests.
- Phase 3 — FlowView lane render by stored `layer` (behind `steps[0].layer != ""` flag); legacy specs fallback to suffix inference.
- Phase 4 — SKILL.md v3 + `docs/llm-usage.md` v3 (§1, §2, §3, §8). No CLI change (D8).
- No migration: legacy generations stay readable; `Fresh start` not required.

---

## 15. File and symbol map (where to edit)

- New: `schemas/core-artifact.schema.json`
- New: `schemas/layers-config.schema.json`
- New: `codeflow.layers.yaml` (repo-root fixture + example for `HOME/workspace/sgp-981-app`)
- Modify: `schemas/flowspec.schema.json` (add `steps[].layer`, `edges[].toLayer`)
- Modify: `internal/fusion/fusion.go:48 FlowStep`, `internal/fusion/fusion.go:68 FlowEdge`, `internal/fusion/fusion.go:136 Fuse` (preserve layer)
- New: `internal/fusion/layers.go` (`LayersConfig`, `NormalizeLayer`, `LayerOrder`, `LoadLayersConfig`, `ValidateLayerOrder`, `ValidatePathPatterns`)
- Modify: `internal/mcp/server.go:229 listTools`, `internal/mcp/server.go:340 executeTool` (new case `publish_core_flow`, reads `codeflow.layers.yaml`)
- Modify: `internal/flowview/layers.go` + `internal/flowview/server.go` / `embedded_html.go` (lane render by stored layer)
- Modify: `skills/codeflow/SKILL.md` (v3 body — description keeps all 7 phrases per D7)
- Modify: `docs/llm-usage.md` (§1, §2, §3, §8) — no CLI change per D8
- Tests: `internal/fusion/layers_test.go` (NormalizeLayer + LayersConfig parse + pathPatterns), `internal/fusion/fusion_test.go` (extend), `internal/mcp/server_test.go` (extend with layers_config_invalid), optional `internal/mcp/core_flow_e2e_test.go`

No change to `scripts/install.sh` or `internal/installation/uninstall.go` — one-shot guarantee unaffected (MCP only per D8).

