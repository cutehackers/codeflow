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

`open` uses the owned Dart adapter beside this source checkout, uses the owned
Dart structural graph when no CodeGraph URL is supplied, publishes one current
snapshot, and opens its loopback FlowView. Keep the command running while
reviewing; `Ctrl-C` releases the local runtime.

To keep the browser launch manual:

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
timeline, step detail, source lens, and **VS Code에서 열기** action then use that
flow only. CodeFlow never flattens steps from several screens into one timeline.

The first output line is the review URL. If the current repository has exactly
one supported route, the selector may be omitted. If it has multiple routes,
CodeFlow fails closed and returns exact `route:/...` candidates instead of
guessing.

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
