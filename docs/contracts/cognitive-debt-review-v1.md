# Cognitive debt review v1

CodeFlow treats an `unknown` as an actionable, source-bound debt rather than a
blank space for inference. The deterministic FlowIR record carries its reason,
affected flow, completion criteria, and suggested evidence. A separate SQLite
review record carries only workflow state:

- `open`: current evidence still leaves the causal boundary unresolved.
- `accepted`: a developer has acknowledged the open debt; the FlowIR remains
  unknown and its canonical bytes do not change.
- `resolved`: CodeFlow no longer finds that unknown in a newly verified current
  snapshot. This state is automatic; a user cannot mark missing evidence as
  resolved by assertion.

Authenticated local endpoints:

- `GET /api/v1/debt` lists review records for the active flow.
- `POST /api/v1/debt/review` with `{"id":"…","state":"accepted"}` accepts an
  open item. Passing `open` reopens an accepted item.

FlowView overlays `open` or `accepted` on current unknowns. Resolved history
remains available through the authenticated debt API, but it is removed from
the current-flow screen once the matching unknown disappears. This prevents
obsolete analyzer terminology from becoming new cognitive debt. Review state
never changes fact trust, causal edge status, hashes, snapshot basis, or
canonical FlowIR content.
