# Incremental Semantic Graph Backlog

## Status

Backlog. The current 128MiB adapter-message limit is a compatibility measure for Live Semantic Map. It is not the long-term scale architecture.

## Problem

The current protocol serializes every captured workspace file into each analyzer request. Workspace size therefore determines request size, memory pressure, and whether a Live Semantic Map can begin.

## Target Outcome

Live Semantic Map must accept workspaces independently of their total source-byte size. After a versioned edit, it must recompute only the affected semantic subgraph and publish either a matching current Generation Proof or a Verified Gap.

## Proposed Architecture

1. Replace byte-bearing analyzer snapshots with immutable snapshot references. Requests carry snapshot identity and analysis intent, not workspace file bytes or a complete manifest.
2. Add a scoped SnapshotReader. Adapters enumerate and read immutable snapshot files on demand through `list`, `read`, and `readRange` operations. Each read records document identity for the analysis read set.
3. Persist a versioned Semantic Graph. Nodes and edges bind to document revision, content identity, snapshot, and workspace epoch.
4. On an edit, invalidate changed symbols and their reverse dependency closure, recompute that subgraph, then publish a graph patch only after proof validation.
5. FlowView consumes graph patches and continues to display activity, pending revisions, proof, and Verified Gap states over SSE.

## Delivery Order

1. Define a new analyzer protocol version with SnapshotReader capability negotiation and conformance fixtures.
2. Implement the core immutable SnapshotReader and migrate Go, TypeScript, and Dart adapters away from `contentOverlay`.
3. Add per-document semantic-index persistence and adapter output for symbols, relations, and dependency frontiers.
4. Add invalidation planning and affected-subgraph recomputation to the live checkpoint pipeline.
5. Add FlowView graph-patch rendering and end-to-end large-workspace tests.

## Acceptance Criteria

1. A workspace of any aggregate source size can begin analysis without serializing all source bytes into one request.
2. A single versioned edit does not require a complete workspace re-analysis when its dependency closure is known.
3. Every published graph patch has a Generation Proof whose snapshot, basis, query, and read-set identities match.
4. Unresolved analysis publishes a Verified Gap and retains the last verified map.
5. Integration tests cover large snapshots, large candidate sets, adapter restart, edit coalescing, proof validation, and FlowView updates.

## Non-Goals

- Treating a larger fixed message limit as the scale solution.
- Reading the live repository after a snapshot is captured.
- Displaying inferred changes without a matching proof.
