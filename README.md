# CodeFlow v2

비즈니스 흐름(Trigger → Rule → State Mutation → Side Effect)을 코드에서 end-to-end로
추출해 FlowView로 명쾌하게 읽게 하는 로컬 도구.

- **설계 문서**: [`docs/design-v2.md`](docs/design-v2.md) — 상태 REVIEWED, P1 게이트 해소 완료
- **구현 티켓**: [`docs/implementation-tickets.md`](docs/implementation-tickets.md) — CF-G01~G17 완료, 백로그 CF-G18~
- **사용 문서**: [`docs/local-usage.md`](docs/local-usage.md) · LLM/에이전트 계약 [`docs/llm-usage.md`](docs/llm-usage.md)
- **v1 코드**: `legacy/` — 참고용 보관 (fresh start, v2가 루트에서 시작)

## 빠른 시작

필요 것: Go 1.26+, Dart SDK 3.x. 원샷 설치에는 Codex CLI도 필요하다.

### 1. 설치

한 번에 설치 (바이너리 + Codex MCP + 스킬):

```sh
bash scripts/install.sh
$HOME/.local/bin/codeflow doctor <분석할-저장소>
```

- Dart 어댑터 경로가 설치 상태에 함께 기록되어 `CODEFLOW_ADAPTER_DART_BIN` 설정이 불필요하다.
- 셸 rc와 분석 대상 저장소는 수정하지 않는다. 다른 명령을 가리키는 동명 MCP나
  수정된 스킬은 덮어쓰지 않고 중단하며, 그때는 `CODEFLOW_MCP_NAME`으로 다른
  이름을 쓰거나 충돌을 정리 후 다시 실행한다.
- 제거는 한 명령: `$HOME/.local/bin/codeflow uninstall`

Codex 없이 소스에서 직접 빌드:

```sh
make build   # bin/codeflow 생성 — 이 경로에서는 어댑터 env 필요 (아래 3번 참고)
```

### 2. 분석할 저장소 준비 (최초 1회)

```sh
codeflow init HOME/workspace/<플러터-앱>
```

### 3. 흐름 추출 → 발행 → 읽기

```sh
# 후보 나열 (자동 점수순)
codeflow flows HOME/workspace/<플러터-앱>

# harvest+slice+fuse 원자적 발행
codeflow publish HOME/workspace/<플러터-앱>

# 하나의 흐름을 JSON으로 읽기
codeflow show flow-7232d63b96bd6efa --json HOME/workspace/<플러터-앱>
```

직접 빌드 경로에서는 각 명령 앞에 어댑터를 지정한다:

```sh
CODEFLOW_ADAPTER_DART_BIN="dartrun:$PWD/adapters/dart" ./bin/codeflow publish ./testdata/example_app
```

### 4. FlowView로 보기

```sh
codeflow serve HOME/workspace/<플러터-앱>
# 출력되는 loopback URL(예: http://127.0.0.1:4567/?token=...)을 브라우저로 열기
```

FlowView에서 비즈니스 흐름 상태, 단계별 타임라인, 코드 렌스, 인과 연결을
읽는다. 종료는 Ctrl-C.

## 로컬 전용 원칙

CodeFlow는 루프백(127.0.0.1)에서만 동작하는 로컬 리뷰 도구다. 배포·호스팅·원격
동기화는 기본 경로에 없다. 현재 릴리스 라인은 로컬 빌드이며 버전 확인은
`codeflow version` — 서명 릴리스·Homebrew 배포는 별도 결정 전까지 보류
중이다([release-handoff](docs/release-handoff.md)).
