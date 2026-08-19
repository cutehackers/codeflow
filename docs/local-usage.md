# CodeFlow local usage

CodeFlow is currently operated locally. Build the Core once from this checkout:

```sh
go build -o ./codeflow ./core/cmd/codeflow
```

On macOS, the shortest reviewed journey for the supplied Flutter project is:

```sh
./codeflow open --repo HOME/workspace/sgp-981-app route:/join
```

`open` uses the owned Dart adapter beside this source checkout, uses the owned
Dart structural graph when no CodeGraph URL is supplied, publishes one current
snapshot, and opens its loopback FlowView. Keep the command running while
reviewing; `Ctrl-C` releases the local runtime.

To keep the browser launch manual:

```sh
./codeflow serve --repo HOME/workspace/sgp-981-app route:/join
```

The first output line is the review URL. If the current repository has exactly
one supported route, the selector may be omitted. If it has multiple routes,
CodeFlow fails closed and returns exact `route:/...` candidates instead of
guessing.

Useful local commands:

```sh
# Print deterministic current FlowIR without opening a browser.
./codeflow analyze --repo HOME/workspace/sgp-981-app route:/join

# Compare behavior, state transitions, causal edges, and unknowns with a local
# Git revision. No checkout or network fetch is performed.
./codeflow compare --repo HOME/workspace/sgp-981-app \
  --baseline main route:/join

# Check an explicit local flow expectation contract.
./codeflow verify --repo HOME/workspace/sgp-981-app \
  --expectations /absolute/path/to/flow-expectations.json route:/join

# Inspect reconstructable baseline mirrors and their total size.
./codeflow cache status --repo HOME/workspace/sgp-981-app

# Remove only baseline mirrors. state.db, runtime state, knowledge, config,
# Git data, and product files are not touched.
./codeflow cache clean --repo HOME/workspace/sgp-981-app
```

Baseline mirrors contain only Dart sources, analyzer/package configuration,
CodeFlow configuration, and the optional external-contract file. Documentation,
binaries, media, and other product assets are never copied. At most three old
mirrors are retained automatically; each is fully reconstructable from local
Git objects without a checkout or network access.

FlowView navigation requires no additional command. Select an architecture
node or timeline step, inspect `코드 변경 → 상태 변화 → 화면 결과`, then use
**VS Code에서 열기** to open the hash-verified current anchor at its line.

LLM 또는 코딩 에이전트가 이 결과를 읽고 설명할 때의 도구 선택과 신뢰
규칙은 [`llm-usage.md`](./llm-usage.md)를 참고한다.
