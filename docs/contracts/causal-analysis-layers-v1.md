# Causal analysis layers v1

CodeFlow does not treat an AST diff as a behavioral change. Each layer answers
a different question and must not claim evidence owned by another layer.

1. **Current bytes and Git basis** determine which repository revision and
   worktree bytes are being inspected. This is the source of `clean`, `dirty`,
   and manifest identity.
2. **Resolved Dart AST and symbols** determine structural facts such as a
   callback target, direct call, condition expression, assignment, route
   constructor, or referenced declaration. Syntax-only fallback is bounded and
   cannot invent a resolved receiver.
3. **Framework semantic rules** map supported structures to domain-neutral
   facts such as event dispatch, provider operation, state transition, state
   observation, visible result, and an explicit terminal result such as a
   source-backed `return` with no navigation. Unsupported shapes become typed
   unknowns.
4. **FlowIR causal edges** connect those facts with evidence-backed relations:
   `causes`, `guards`, `permits`, `changes_state`, `observed_by`, and `produces`.
5. **Behavioral comparison** compiles baseline and current source through the
   same pipeline, then compares stable behavior keys, symbols, fingerprints,
   causal edges, results, branches, and unknowns. Only this layer may label a
   behavior `unchanged`, `changed`, `added`, or `removed`.
6. **Tests and local contracts** guard expected observed results and causal
   relations. They validate evidence; they do not promote inference.

Runtime/session traces are optional future corroboration. They may confirm that
an evidence-backed path executed, but they are not required to reconstruct
current flow and never replace current source evidence.

A runtime choice is not itself an unknown when resolved AST proves the complete
set of alternatives. For example, a condition whose outcomes are `/home`, an
explicit return without navigation, and a cancellation chain to `/auth` is a
fully observed static branch even though only one outcome occurs per execution.

FlowView therefore uses `비교 기준 없음` for a current-only analysis. It shows
change labels only when a baseline was compiled successfully from an immutable
Git mirror.
