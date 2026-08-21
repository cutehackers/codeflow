# CodeFlow local usage

CodeFlow is currently operated locally. Build the Core once from this checkout:

```sh
make local
```

On macOS, the shortest reviewed journey for the supplied Flutter project is:

```sh
./bin/codeflow open --repo HOME/workspace/sgp-981-app route:/join
```

Run `make local` once to build `bin/codeflow` and the adjacent AOT adapter in
`libexec/`. Both are local ignored outputs. This is the recommended local path
because later analysis and refresh commands do not pay Dart JIT startup again.

For the one-shot Codex setup, run this once after the build:

```sh
./bin/codeflow install
```

It installs the paired Core and adapter under `HOME/.codeflow`, registers the
included local CodeFlow marketplace, and activates its plugin. No adapter
environment variable, PATH edit, manual MCP entry, or separate `serve` process
is required. Start a new Codex task after it finishes; the first `current` or
`open` request starts a Core for its exact `route:/...` or `system:...` flow
automatically. For a BusinessJourney, the MCP flow starts with `entry_points`
and `prepare_workspace` so all referenced entries share one Basis.

`open` uses the owned Dart adapter beside this source checkout, uses the owned
Dart structural graph when no CodeGraph URL is supplied, publishes one current
snapshot, and opens its loopback FlowView. It accepts both a screen entry
(`route:/...`) and a supported system entry (`system:...`). Keep the command
running while reviewing; `Ctrl-C` releases the local runtime.

To keep the browser launch manual or hold a persistent local Core:

```sh
./bin/codeflow serve --repo HOME/workspace/sgp-981-app route:/join
```

To review up to three related screens against one exact worktree snapshot:

```sh
./bin/codeflow serve --repo HOME/workspace/sgp-981-app \
  --flow route:/join \
  --flow route:/home \
  --flow route:/auth
```

The screen-flow map changes the selected flow; its Architecture map, vertical
timeline, Hero Code Lens, inline causal delta bar, and **VS Code에서 열기** action then use that
flow only. CodeFlow never flattens steps from several screens into one timeline.
FlowView provides:
- **Business Journey Bar**: User/domain business flow status at top.
- **Sequential Timeline & Impact Chain**: Step-by-step state and UI causal links.
- **Hero Code Lens**: Full-width (~80% panel) high-readability source viewer (520px height) with line highlight.
- **Architecture Map (코드 인과 지도)**: Preserved 3-lane visual architecture map with synchronized 3-way highlighting.
- **Monochrome Black & White Theme**: Crisp, accessible, high-contrast engineering aesthetic.
Detailed specifications are documented in [business-flow-redesign-spec-ko.md](./design/business-flow-redesign-spec-ko.md).

The first output line is the review URL. If the current repository has exactly
one supported route, the selector may be omitted. If it has multiple routes,
CodeFlow fails closed and returns exact `route:/...` candidates instead of
guessing.

System events are first-class entry points for flows that do not begin with a
screen action. The Dart adapter currently discovers bounded lifecycle,
session-listener, and FCM token-refresh callbacks. Discover the exact selector
from the MCP `entry_points` tool (or `codeflow resolve`), then use that selector
with `open`, `serve`, or `analyze`; do not replace it with `route:/auth` merely
because the callback eventually changes authentication state.

## BusinessJourney를 로컬에서 등록하기

BusinessJourney는 route 이름이나 코드 추측으로 생성되지 않고, 현재 동일
Basis에서 검증된 scenario를 참조하는 명시적 정의입니다. 일반적인 사용자는
MCP에서 다음 순서로 등록합니다.

1. `entry_points`로 필요한 `route:/...`와 `system:...` 후보를 확인합니다.
2. `prepare_workspace`에 정확한 `flow_ids`를 1–3개 전달해 하나의 분석 범위를
   준비합니다.
3. `workspace`와 `current`에서 실제 scenario ID를 확인합니다.
4. 사용자가 저장을 명시한 경우에만 `upsert_business_journey`를 호출합니다.
5. 화면 검토가 필요할 때만 `open_business_journey`를 호출합니다.

MCP가 runtime 포트와 인증 토큰을 내부적으로 처리하므로 사용자가 토큰,
`.codeflow/runtime.json`, HTTP 요청을 직접 다룰 필요는 없습니다. 이미 다른
범위의 Core가 실행 중이면 `prepare_workspace`는 이를 교체하지 않고
`WORKSPACE_SCOPE_MISMATCH`를 반환합니다. 기존 분석을 중단하거나 덮어쓰지
말고, 현재 범위를 확인한 뒤 별도 분석 task에서 다시 준비합니다.

CLI만 사용하는 경우에는 `serve`/`open`으로 시스템 entry와 화면 entry를 함께
게시할 수 있지만, BusinessJourney 저장·검증은 현재 MCP 또는 인증된 Core API
경계에서 수행됩니다. 수동 JSON 편집으로 scenario ID를 만들거나 갱신하지
마십시오. 소스 변경 후에는 기존 여정이 stale/unknown으로 표시될 수 있으며,
먼저 `refresh` 후 현재 scenario와 관찰된 화면 간 전이를 다시 확인해야 합니다.

Useful local commands:

```sh
# Print deterministic current FlowIR without opening a browser.
./bin/codeflow analyze --repo HOME/workspace/sgp-981-app route:/join

# Compare behavior, state transitions, causal edges, and unknowns with a local
# Git revision. No checkout or network fetch is performed.
./bin/codeflow compare --repo HOME/workspace/sgp-981-app \
  --baseline main route:/join

# Check an explicit local flow expectation contract.
./bin/codeflow verify --repo HOME/workspace/sgp-981-app \
  --expectations /absolute/path/to/flow-expectations.json route:/join

# Inspect reconstructable baseline mirrors and their total size.
./bin/codeflow cache status --repo HOME/workspace/sgp-981-app

# Remove only baseline mirrors. state.db, runtime state, knowledge, config,
# Git data, and product files are not touched.
./bin/codeflow cache clean --repo HOME/workspace/sgp-981-app
```

Baseline mirrors contain only Dart sources, analyzer/package configuration,
CodeFlow configuration, and the optional external-contract file. Documentation,
binaries, media, and other product assets are never copied. The normal target
is three mirrors. Mirrors touched within the last hour receive a concurrency
grace period, but even that period has a hard limit of eight. Each mirror is
fully reconstructable from local Git objects without checkout or network use.

FlowView navigation requires no additional command. Select an architecture
node or timeline step, inspect `코드 변경 → 상태 변화 → 화면 결과`, then use
**VS Code에서 열기** to open the hash-verified current anchor at its line.

여러 사용자 행동이 있는 화면은 `이 화면에서 선택할 수 있는 경로`에서 하나를
고른 뒤 해당 scenario의 타임라인을 읽는다. PR 검토용 정적 보고서가 필요하면
다음처럼 새 HTML 파일을 생성한다.

```sh
./bin/codeflow export \
  --repo HOME/workspace/sgp-981-app \
  --output join-flow.html \
  --flow route:/join
```

`--scenario`에는 `analyze` 결과의 `scenarios[].id`를 지정할 수 있다. export는
로컬 runtime, 인증 토큰, editor link, 화면·scenario 이동 링크를 포함하지 않는다.
이미 존재하는 출력 파일은 덮어쓰지 않으며, 동시 export도 하나만 그 경로를 만들
수 있다.

## Local trust boundary

FlowView is intentionally a local review page: Core binds only to the literal
IPv4 loopback address and accepts only its exact `127.0.0.1:PORT` Host. The page
uses no-store, no-frame, same-origin resource, no-referrer, and restrictive CSP
headers. Its HTML view does not require a token so `serve` remains one-command
local usage; authenticated JSON endpoints and all mutations still require the
random runtime token. CodeFlow sends no CORS permission and never places that
token in the URL, HTML, logs, or error text. Do not proxy the loopback port or
expose it through a tunnel.

`serve`는 제품 manifest에 포함되는 파일 변경을 자동 감지하고 같은 Dart
Analyzer 세션으로 다시 분석한다. 열린 FlowView도 원자적으로 게시된 새
snapshot을 감지해 갱신된다. `.codeflow`, build output, tool cache 변경은
분석을 유발하지 않는다. 이벤트를 놓쳤다고 의심될 때만 `codeflow refresh`
를 복구 명령으로 사용한다. 기본 출력은 상태, flow 수, 미확정 수와 FlowView
주소만 보여 준다. 자동화에서 전체 응답이 필요할 때만
`codeflow refresh --format json`을 사용한다.

LLM 또는 코딩 에이전트가 이 결과를 읽고 설명할 때의 도구 선택과 신뢰
규칙은 [`llm-usage.md`](./llm-usage.md)를 참고한다.
