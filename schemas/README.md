# CodeFlow v2 — Contract Schemas (M1)

Six JSON Schema (draft 2020-12) contracts fixing every data/interaction boundary
of the CodeFlow v2 pipeline (design-v2 §7). Golden fixtures plus a Go harness
keep implementations from drifting (design-v2 §13, "계약" row).

## Files

| `$id` | Boundary | Direction |
|---|---|---|
| `identity.schema.json` | shared vocabulary: flowId/stepId derivation + collision rule, supersedes chain, tombstone, R3 anchor, provenance/freshness enums | referenced by all |
| `candidate.schema.json` | Harvest → Slice | Stage 1 out |
| `sliced-payload.schema.json` | Slice → Fusion | Stage 2 out |
| `flowspec.schema.json` | Fusion/Publish → FlowView | Stage 3/4 out |
| `session-artifact.schema.json` | MCP `submit_flow_draft` → Fusion | E2 in |
| `adapter-protocol.schema.json` | CORE ↔ language adapter | NDJSON over stdio |

## Conventions

**$id scheme.** Every contract's `$id` is
`https://codeflow.local/schemas/<name>.schema.json`. The host is a logical
namespace only — never fetched. Cross-file references use absolute URLs into it,
e.g. `{"$ref": "https://codeflow.local/schemas/identity.schema.json#/$defs/anchor"}`.
Shared definitions live once in `identity.schema.json#/$defs` (anchor,
provenance, freshness, flowId, stepId, canonicalEntrySymbolPath, supersedes,
tombstone).

**How CORE consumes these.** All validation goes through the compiled harness:
`internal/contractharness.Validate(schemaID string, data []byte) error`, with
`schemaID = contractharness.BaseURL + "<name>.schema.json"`. The harness maps
`$id`s to the embedded `codeflow/schemas` filesystem (`schemas.FS`) and caches compiled schemas; stages call it at
every contract boundary (harvest output, slice output, fusion input/output, MCP
submissions, adapter envelopes) instead of hand-rolled checks. A returned error
is a `*jsonschema.ValidationError` whose text names each failing keyword and its
instance location.

**Fixture naming.** `schemas/fixtures/<contract>/valid/*.json` must validate;
`schemas/fixtures/<contract>/invalid/*.json` must fail — one normative rejection
per invalid fixture, named after what it rejects (`bad-trigger-class`,
`edge-depth-over-limit`, …). `go test ./internal/contractharness/...` enforces
both directions and fails with fixture-path + reason on any mismatch. When a
contract legitimately changes, update fixtures in the same commit; an invalid
fixture that starts passing means the schema got looser without review.

## Normative invariants

**Provenance is required, and separate from freshness.** Every FlowSpec step
carries REQUIRED `provenance` (`approved | session | derived | unknown`) and
REQUIRED `freshness` (`fresh | stale | orphaned`). Provenance records who
produced a semantic value under authority order approved > session > derived >
unknown; freshness records whether that value still matches the live worktree.
They are orthogonal on purpose: an approved name whose anchor no longer matches
becomes `stale`, not unapproved. Confidence (0..1) and per-step `basisSha`
accompany them, and document-level `basisSha` pins the read-set fingerprint so
publish can detect mid-analysis worktree drift.

**Anchor required for submissions.** An E2 session artifact (`submit_flow_draft`)
is structurally invalid unless *every* draft step carries a full R3 anchor
(repoRelativePath, byteRange, fileHash, spanHash, enclosingSymbolPath,
canonicalAstFingerprint) and `anchorsMustPassVerification` is `const true`.
Anchoring never uses line numbers alone; relink matches on
enclosingSymbolPath + canonicalAstFingerprint, so line-shift-only edits keep
approvals linked, behavior changes flip to stale → approval queue, and vanished
symbols become orphaned. Unanchored submissions are rejected whole and parked as
`unlinked`.

**E1-overrides-blocked.** Structural evidence (E1 AST slices) cannot be
overwritten by E2/E3 semantics regardless of authority order. Fusion may fill
names, rules, state deltas, and side effects, but it may not delete or rewrite
an E1 structural step card; when semantics conflict with current code the value
is marked `stale` while the structural card survives intact until the approval
queue resolves it. `flowspec.unknowns[]` is likewise mandatory (possibly empty):
blanks stay explicit unknowns, never guessed away.

## Identity & redaction rules embedded in schemas

- `flowId` = `flow-` + first 16 lowercase hex of
  sha256(canonicalEntrySymbolPath); collisions append `-2..N`; stable under
  move/rename because the key is the symbol path, not a filesystem path.
- `stepId` = same recipe over `<flowId>#<ordinal>:<symbolPath>`.
- Split/merge chains are tracked via `supersedes`; dead roots get a tombstone
  with nullable `supersededBy`.
- Sliced payloads are deterministic for identical input bytes + opts; secrets
  pass one common scanner and `redactedCount` counts replaced matches.
