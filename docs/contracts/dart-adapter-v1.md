# Dart adapter JSON-RPC v1 (DEPRECATED)

> [!WARNING]
> **Deprecated**: 이 문서는 v0 아카이브 사양입니다. 현재 CodeFlow 런타임 구현은 **NDJSON v1 프로토콜(`ping`, `detect`, `harvest_candidates`, `slice`, `shutdown`)**을 따르며, 공식 표준 계약은 [`docs/spec/llm-language-adapter-protocol.md`](../spec/llm-language-adapter-protocol.md) 및 [`docs/contracts/adapter-protocol-v1.md`](adapter-protocol-v1.md)를 참조하십시오.

The Core launches the installed adapter as a child with `--stdio`. Each input
and output is one newline-delimited JSON-RPC 2.0 object. The Core sends
`initialize` first with `protocol_version: "1"`; the result must declare the
same version and `discover_entry_points` capability.

`discoverEntryPoints` receives a repository path and returns direct literal
`go_router` `GoRoute(path: '/...')` declarations only. Discovery is parsed as
a Dart AST and accepts literal paths or uniquely resolved local `const String`
paths. Each returned entry
contains `flow_id`, shorthand `alias`, and an anchor. Core revalidates every
path, file hash, and byte range against its current manifest before exposing
it. `cancel` is best effort; Core sends it upon deadline and then cleans up the
child. `shutdown` is requested at normal completion; bounded process cleanup
prevents child leakage.

`refineRouteFlow` parses the validated Core-supplied source slice with the Dart
Analyzer. An optional `analysis_paths` field supplies the bounded union of
current source paths needed by one Multi-flow compilation. Every flow keeps its
own `paths` evidence slice, while the adapter initializes one Analyzer context
from the shared union and reuses package resolution for the remaining flows.
The selected `GoRoute` builder first identifies the owning Widget and
State declarations. `onPressed` callbacks and direct calls enter observed
FlowIR only when Analyzer resolves them to executable elements owned by that
route. Text matches, unresolved identifiers, closures, computed callbacks, and
callbacks belonging to another page are not promoted. Canonical symbol IDs use
the library URI, enclosing element kind/name, and executable kind/name; they do
not use source paths, line numbers, or prose.

Every semantic fact declares a proof layer. `resolved_ast` requires a canonical
`symbol_id`; `framework_rule_v1` is a versioned, fail-closed Flutter/Riverpod/
go_router rule; `contract_v1` names versioned local contract evidence. Syntax
AST may still discover candidates, but it is never sufficient for an observed
callback or call relationship. Regex is limited to narrow framework rules that
emit nothing when their complete unique seam is absent.

For a resolved callback, the adapter may put a direct static widget label,
`Semantics` label, or tooltip in the `user_action.object` field. This is
reader-facing source evidence, not an inferred domain meaning: dynamic strings
and labels not owned by the callback's widget are omitted. Core uses it to name
the action-rooted scenario before falling back to an approved domain label.

Resolved causality is product-name independent. For navigation, the adapter
joins a resolved destination constructor to a unique switch-expression mapping
and a resolved literal route constant. For state-driven navigation, it joins
the exact provider and event at `dispatch`, the unique resolved event case and
state assignment, a listener registered for the same provider, a condition on
the assigned state member, and the listener's resolved route. Every link must
be present in the Core-supplied current source slice. Missing or ambiguous
links emit no observed chain; route names, provider names, event names, class
names, and filenames are never special-cased.

An explicit return guard immediately preceding a resolved dispatch may produce
the second, terminal `result:no_navigation` branch. This does not claim which
branch runs at runtime; it proves only that the current source contains both
possible outcomes. A state listener may own the only visible navigation for an
action—no same-body route call is required.

The adapter advertises `dart_analyzer_ast` and `resolved_symbols` from version
0.5.0.

Failure codes: `ADAPTER_UNAVAILABLE`, `ADAPTER_INCOMPATIBLE`,
`ADAPTER_MALFORMED`, and `ADAPTER_TIMEOUT`. They are exposed as typed
unavailable or unknown results; they never become an empty successful route
list.
