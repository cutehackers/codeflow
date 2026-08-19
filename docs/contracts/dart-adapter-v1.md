# Dart adapter JSON-RPC v1

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
Analyzer. The selected `GoRoute` builder first identifies the owning Widget and
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
The adapter advertises `dart_analyzer_ast` and `resolved_symbols` from version
0.5.0.

Failure codes: `ADAPTER_UNAVAILABLE`, `ADAPTER_INCOMPATIBLE`,
`ADAPTER_MALFORMED`, and `ADAPTER_TIMEOUT`. They are exposed as typed
unavailable or unknown results; they never become an empty successful route
list.
