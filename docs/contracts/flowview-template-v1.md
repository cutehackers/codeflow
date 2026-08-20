# FlowView template v1

FlowView is one repository-owned, flow-agnostic HTML template. Compilers emit
FlowIR; they never emit feature-specific HTML, CSS, or JavaScript. The template
must not contain route names, widget names, or product prose such as `join` or
`signup`.

The root declares `data-flowview-version="1"`. Every rendered flow uses these
stable regions in this reading order:

1. `snapshot` — repository revision, worktree state, and overall trust.
2. The optional multi-flow screen map and compact current-code path.
3. `timeline-navigation` and `timeline` — previous/next controls and ordered
   causal steps.
4. `step-detail` — a selected step containing `impact-chain` and `code-lens`.
5. `architecture` — a secondary, collapsible UI/application/state/data/external
   causal map whose nodes select the corresponding timeline step. Opening the
   map scrolls to the already-selected timeline step.
6. `cognitive-debt` when current unknowns exist.

The impact chain always reads `코드 변경 → 상태 변화 → 화면 결과`. A missing
baseline is presented as `비교 기준 없음` or `baseline 미선택`, never as “no
change”. A state assignment may show an observed target value while retaining
an unknown previous value; the UI must not invent a runtime before-state.

All editor links are constructed only by the verified source-lens boundary.
The template receives a `vscode://file/...:line:column` URL only after the
relative path, manifest hash, anchor hash, and current bytes agree. Stale or
unavailable lenses render no editor link.

The code lens keeps the exact raw source window in the API, but its visible
lines remove only the window's shared leading indentation. Each row shows its
real source line number and marks the anchor range, so deeply nested Flutter
widgets start at the left edge without destroying their relative indentation.
Line numbers never wrap. Source lines follow editor conventions: they preserve
relative indentation and scroll horizontally instead of breaking at arbitrary
characters. A compact header identifies the selected line, and the anchor row
is highlighted across the code viewport.

Visible status and branch labels use reader-facing Korean phrases; stable raw
status and reason values stay in `data-*` attributes and API payloads. A branch
whose complete alternatives are source-backed says `모든 경로 확인됨`. Semantic
tests, rather than full-pixel snapshots, protect the contract: version,
region order, architecture/timeline linkage, status vocabulary, impact chain,
and editor-link trust gate. Styling may evolve without permitting flow-specific
markup or changing causal meaning.

Selection is one shared state. Choosing a timeline item, an architecture node,
or a branch outcome must select the same timeline item, architecture node, and
detail card and must reveal both selected items inside their horizontal scroll
containers. This includes returning to step zero after another step was
selected. Initial rendering may skip animated scrolling.

Mutually exclusive branch outcomes must not be drawn as a single sequential
connector. Each outcome receives a visible `분기 N · 경로 A/B` label, adjacent
alternatives break the timeline connector, and branch summaries are controls
that move the shared selection to their outcome step.

The approved successor design direction is documented in
[`../design/flowview-multi-flow-workspace-ko.md`](../design/flowview-multi-flow-workspace-ko.md).
It preserves this v1 trust and evidence contract while moving the timeline's
existing monochrome marker, connector, state-change ring, and branch grammar to
a vertical detail workspace beneath a multi-flow screen map.

The implemented publication and API rules are defined by
[`multi-flow-workspace-v1.md`](./multi-flow-workspace-v1.md). The reusable
template owns both single-flow and multi-flow rendering; compilers still emit no
feature-specific HTML.
