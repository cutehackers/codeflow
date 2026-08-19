# CF-G13 review — `sgp-981-app` `/join`

**Status:** Passed by direct local source and FlowView review
**Repository:** `HOME/workspace/sgp-981-app`
**Selector:** `route:/join`
**Basis:** `090bb06cead7b91daf1c3fdfd00ed881381dd412`, dirty worktree

## Reproducible evidence

```sh
codeflow analyze --repo HOME/workspace/sgp-981-app \
  --codegraph-url http://127.0.0.1:65534 \
  --adapter 'dart HOME/workspace/codeflow/adapters/dart/bin/codeflow-dart-adapter.dart' \
  route:/join
codeflow compare --repo HOME/workspace/sgp-981-app --baseline HEAD \
  --codegraph-url http://127.0.0.1:65534 \
  --adapter 'dart HOME/workspace/codeflow/adapters/dart/bin/codeflow-dart-adapter.dart' \
  route:/join
```

The owned Dart structural backend produced a current anchored entry point in
`packages/feature_account/lib/src/routing/account_routes.dart:60-61` and the
anchored user action `JoinPage._requestExit` at
`packages/feature_account/lib/src/features/join/presentation/join_page.dart:195`.
It follows the bounded static destination seam through the concrete dispatcher
and exhaustive resolver and proves `route:/home` at the action's
`HomeDestination` call (`join_page.dart:115`). That outcome is correctly
attached to the current `state.isCompleted` condition at line 114 through a
deterministic branch. Its alternative continues to the confirmation guard.
The guard's explicit `return` is an observed no-navigation terminal result;
the confirmed alternative dispatches `JoinCancelEvent`, observes
`JoinState.isCanceled=true`, and reaches `/auth`. The complete current flow is
therefore `observed` with zero unknowns. Current vs `HEAD` reports no added,
removed, changed, or newly unknown behavior.

## Required user outcome

| Criterion | Evidence | User outcome |
| --- | --- | --- |
| Starting action and final visible result are correct | `JoinPage._requestExit` has conditional observed `/home` plus confirmed-cancel observed `/auth` | PASS |
| Important branches/state transitions are present and ordered | Condition branch, dialog confirmation, `JoinCancelEvent`, state assignment, listener, and `/auth` are ordered | PASS |
| Default code lenses explain each step | Verified 12-line lenses render the action, condition, event, state assignment, and route calls | PASS |
| FlowDelta has signal without file-diff noise | `HEAD` delta: no added/removed/changed steps or new unknowns | PASS |
| Unknowns are minimized without guessing | Both conditions have two source-backed outcomes; the plain provider read is a dependency, not a fabricated state mutation | PASS |
| Sessionless reconstruction has same facts/flow | Two no-session analyses compared byte-for-byte equal | PASS |

## Reviewer outcome

The cancellation-path review is passed: after explicit dialog confirmation,
`JoinCancelEvent` reaches `JoinState(isCanceled: true)` in
`join_controller.dart:275-276`; the same-widget listener executes
`GoRouter.of(context).go(authPath)` at `join_page.dart:159-160`. The target
test at `join_page_test.dart:726-736` confirms the auth-entry destination, and
current FlowIR exposes this ordered observed chain alongside the conditional
`/home` branch.

CF-G13F corrected the former code-lens defect: FlowView now renders verified
12-line source windows and refuses stale source text. This verdict was checked
directly against current source and the rendered local FlowView; no behavioral
claim relies on session input.

## Live review proof

`codeflow serve` published the actual loopback FlowView for this selector. The
page rendered nine Korean-labeled steps, two `모든 경로 확인됨` branches, the
observed `/home`, no-navigation, and `/auth` outcomes, and no current
cognitive-debt section. The `graph:owned_dart_structural` backend remains
visible.
