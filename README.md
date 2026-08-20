# CodeFlow

CodeFlow는 Flutter/Dart 코드의 사용자 동작, 상태 변화, 조건 분기, 화면 결과를
현재 소스 근거와 함께 FlowView 타임라인으로 보여 주는 로컬 도구입니다.

현재는 로컬 사용만 지원합니다. 분석 대상 product source는 수정하지 않습니다.

## 빠른 설치

필요한 도구는 Git, Go, Dart SDK입니다.

```sh
cd HOME/workspace/codeflow
make local
```

이 명령은 `bin/codeflow`와 로컬 분석용 AOT Dart adapter를 만듭니다. 한 번
빌드한 adapter를 재사용하므로 매 분석마다 Dart JIT를 준비하는 시간을 줄입니다.

Codex에서 MCP로 사용하려면 한 번만 등록합니다.

```sh
codex mcp add codeflow -- \
  HOME/workspace/codeflow/bin/codeflow \
  mcp \
  --repo HOME/workspace/sgp-981-app
```

등록을 확인한 뒤 Codex 앱을 재시작합니다.

```sh
codex mcp get codeflow
```

## 실행

FlowView를 검토하는 동안 별도 터미널에서 Core를 실행해 둡니다.

```sh
HOME/workspace/codeflow/bin/codeflow serve \
  --repo HOME/workspace/sgp-981-app \
  route:/join
```

출력된 `CodeFlow review URL`을 브라우저에서 엽니다. 종료할 때는 `Ctrl-C`를
누릅니다.

Codex에는 다음처럼 요청할 수 있습니다.

```text
CodeFlow current로 route:/join을 코드 → 상태 변화 → 화면 결과 순서로 설명해줘.
unknown은 추측하지 말고 따로 알려줘.
```

MCP는 실행 중인 Core에 연결하며 Core를 대신 시작하지 않습니다. 제공 도구는
`workspace`, `current`, `step`, `unknowns`, `refresh`, `diff`, `open`입니다.

관련된 여러 화면을 같은 코드 기준으로 검토하려면 `--flow`를 최대 세 번
반복합니다.

```sh
HOME/workspace/codeflow/bin/codeflow serve \
  --repo HOME/workspace/sgp-981-app \
  --flow route:/join \
  --flow route:/home \
  --flow route:/auth
```

FlowView의 화면 흐름 지도에서 flow를 선택하면 해당 화면의 Architecture,
수직 타임라인, 코드·상태·화면 결과와 `VS Code에서 열기`가 함께 바뀝니다.
열어 둔 Core가 파일 이벤트를 놓쳤다고 의심될 때는
`./bin/codeflow refresh --repo HOME/workspace/sgp-981-app`로 갱신합니다. 기본
출력은 짧은 요약이며, 전체 기계 응답은 `--format json`에서만 출력합니다.

## CLI만 사용하기

```sh
# 현재 FlowIR 출력
./bin/codeflow analyze \
  --repo HOME/workspace/sgp-981-app \
  route:/join

# 로컬 Git 기준선과 비교
./bin/codeflow compare \
  --repo HOME/workspace/sgp-981-app \
  --baseline main \
  route:/join
```

자세한 내용:

- [로컬 명령과 캐시 관리](./docs/local-usage.md)
- [LLM과 MCP 사용 규칙](./docs/llm-usage.md)
- [Multi-flow Workspace 계약](./docs/contracts/multi-flow-workspace-v1.md)
- [CodeFlow 설계 명세](./docs/codeflow-production-design-ko.md)
