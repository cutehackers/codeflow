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

Codex까지 포함해 한 번에 준비하려면, 빌드한 번들에서 아래 한 명령만 실행합니다.

```sh
./bin/codeflow install
```

이 명령은 Core와 AOT Dart adapter를 `HOME/.codeflow`에 함께 설치하고, 포함된
Codex plugin을 등록·활성화한다. adapter 환경 변수, PATH 설정, 별도 MCP 등록은
필요 없다. 설치가 끝나면 새 Codex task에서 바로 CodeFlow를 요청한다. 새 task는
Codex가 새 MCP 도구를 읽는 경계다.

배포된 패키지는 `bin/codeflow install`로 실행한다. 개발 중에는 위의 `make local`
다음에 `./bin/codeflow install`을 실행하면 같은 흐름을 검증할 수 있다.

## 실행

Codex MCP는 첫 `current` 또는 `open` 요청에서 요청된 `route:/...` 또는
`system:...` 진입점 기준으로 Core를 자동 시작한다. BusinessJourney를 만들 때는
LLM이 `entry_points`와 `prepare_workspace`로 필요한 흐름을 함께 준비한 뒤,
`upsert_business_journey`로 안전하게 등록한다. 사용자가 토큰·포트·scenario ID를
직접 다룰 필요는 없다. FlowView를 검토하는 동안 별도 터미널을 열 필요가 없다.
터미널에서 오래 유지할 runtime이 필요할 때만 다음 명령을 사용한다.

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

MCP는 실행 중인 Core를 재사용하고, 없으면 첫 요청에 필요한 Core 하나만 시작한다.
제공 도구는 `entry_points`, `prepare_workspace`, `workspace`, `business_journeys`,
`upsert_business_journey`, `current`, `step`, `unknowns`, `refresh`, `diff`, `open`,
`open_business_journey`입니다.

관련된 여러 화면을 같은 코드 기준으로 검토하려면 `--flow`를 최대 세 번
반복합니다.

```sh
HOME/workspace/codeflow/bin/codeflow serve \
  --repo HOME/workspace/sgp-981-app \
  --flow route:/join \
  --flow route:/home \
  --flow route:/auth
```

승인된 비즈니스 여정이 있으면 FlowView는 이를 먼저 보여 주고, 여정을 구성한
사용자 또는 시스템 경로로 이동합니다. 화면 흐름 지도에서 flow를 선택하면 해당 화면의 Architecture,
수직 타임라인, 코드·상태·화면 결과와 `VS Code에서 열기`가 함께 바뀝니다.
화면 안에 여러 사용자 행동이 있으면 `이 화면에서 선택할 수 있는 경로`에서
이메일·전화번호·소셜 가입처럼 하나의 경로를 먼저 선택합니다. 기본 단계는
사용자 목적과 결과로 읽히며, 구현 조건·상태·호출은 코드 근거에서 확인합니다.
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

# PR에 첨부할 정적 HTML 보고서 생성
./bin/codeflow export \
  --repo HOME/workspace/sgp-981-app \
  --output join-email-flow.html \
  --flow route:/join
```

export 파일은 분석 당시 Basis와 코드 근거를 보존하지만 로컬 서버, 인증 토큰,
`VS Code에서 열기` 링크는 포함하지 않습니다. 화면에 여러 경로가 있을 때
`--scenario`으로 특정 경로를 선택할 수 있습니다.

자세한 내용:

- [로컬 명령과 캐시 관리](./docs/local-usage.md)
- [LLM과 MCP 사용 규칙](./docs/llm-usage.md)
- [Multi-flow Workspace 계약](./docs/contracts/multi-flow-workspace-v1.md)
- [Business Journey 계약](./docs/contracts/business-journeys-v1.md)
- [CodeFlow 설계 명세](./docs/codeflow-production-design-ko.md)
