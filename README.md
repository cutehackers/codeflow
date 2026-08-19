# CodeFlow

CodeFlow는 Flutter/Dart 코드의 사용자 동작, 상태 변화, 조건 분기, 화면 결과를
현재 소스 근거와 함께 FlowView 타임라인으로 보여 주는 로컬 도구입니다.

현재는 로컬 사용만 지원합니다. 분석 대상 product source는 수정하지 않습니다.

## 빠른 설치

필요한 도구는 Git, Go, Dart SDK입니다.

```sh
cd HOME/workspace/codeflow
go build -o ./codeflow ./core/cmd/codeflow
```

Codex에서 MCP로 사용하려면 한 번만 등록합니다.

```sh
codex mcp add codeflow -- \
  HOME/workspace/codeflow/codeflow \
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
HOME/workspace/codeflow/codeflow serve \
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
`current`, `step`, `unknowns`, `refresh`, `diff`, `open`입니다.

## CLI만 사용하기

```sh
# 현재 FlowIR 출력
./codeflow analyze \
  --repo HOME/workspace/sgp-981-app \
  route:/join

# 로컬 Git 기준선과 비교
./codeflow compare \
  --repo HOME/workspace/sgp-981-app \
  --baseline main \
  route:/join
```

자세한 내용:

- [로컬 명령과 캐시 관리](./docs/local-usage.md)
- [LLM과 MCP 사용 규칙](./docs/llm-usage.md)
- [CodeFlow 설계 명세](./docs/codeflow-production-design-ko.md)
