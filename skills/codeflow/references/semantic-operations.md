# Semantic Operations

Use these operations after resolving the exact target and required current or historical basis. Load only the section relevant to the request.

## Review and Semantic Delta

- Use `query_task_view` in `review` mode or `get_semantic_delta` to compare two exact generations/bases.
- Report added, changed, and removed behavior separately from evidence-only changes.
- Do not compare incompatible workspace epochs as one continuous current history.

## Requirement Alignment and Change Impact

- Use `get_requirement_alignment` to retrieve evidence-grounded criteria states. Only CodeFlow may return `confirmed`; preserve `partial`, `not_observed`, `conflicting`, and `unknown`.
- Use `get_change_impact` for an exact symbol or change batch with explicit basis, generation, freshness, traversal bounds, and relation kinds.
- Separate direct callers, bounded indirect impact, state mutations, external effects, related tests, and unknown frontiers. Traversal bounds are coverage limits, not proof that no other impact exists.

## Failure and Incident Investigation

- Use `investigate_failure` or the strict debug/incident form of `query_task_view` with the exact required identities.
- Caller-supplied logs or observations are leads, not trusted runtime authority.
- Request trusted-local execution only when the user has explicitly authorized the exact runtime operation and CodeFlow accepts its consent contract.
- Keep static failure reachability, deterministic simulation, and observed runtime failure as separate results.

## Optional Semantic Labeling

- Use `POST /api/semantic/labels` only when the user requests SLM-assisted FlowSequence labels.
- Preserve deterministic semantic facts when enrichment is unavailable, crashes, or times out.
- An available proposal must remain bound to its exact generation, basis, target, prompt revision, model revision, and Evidence Pack.

## Evidence and Semantic Approval

1. Use `get_evidence_pack` to inspect verified, redacted AST, call, and test evidence for the exact target.
2. Use `submit_semantic_approval` only after the user explicitly requests an approval lifecycle decision and the exact stored proposal and Evidence Pack are available.
3. Preserve the returned proposal, evidence-pack, basis, generation, intent revision, expected version/state, actor, and idempotency identities.
4. Use `get_semantic_approval_history` for the durable append-only history.

Never infer approval from positive feedback about code or a proposal. Approval records trust in semantic wording; it does not change implementation facts, currentness, settlement, Requirement Alignment, or runtime evidence.

## Project Onboarding

- Use `explore_project_domains` when the user wants an evidence-backed domain map or representative-flow catalog.
- Current onboarding requires current proof. Historical onboarding requires the exact basis, generation, and validated snapshot.
- Present unknown coverage boundaries instead of describing the repository as completely understood.

## Release Capability Validation

- Use `validate_release_capability` only with explicitly collected immutable evaluation artifacts, declared profile/corpus, executed reports, and approved thresholds.
- Missing evidence remains `incomplete`; a failed invariant blocks the relevant capability.
- This tool evaluates supplied evidence. It does not run benchmarks, manufacture measurements, approve thresholds, or make a release decision by itself.
