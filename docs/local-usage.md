# CodeFlow local usage

CodeFlow v2는 로컬 전용 도구다. 설치부터 FlowView까지 한 페이지에서 진행한다.
명령은 모두 실제 검증된 경로다. 이전 CF-G 코어 엔진 기반 사용법
(`open`/`analyze`/멀티플로우 `serve --repo ... --flow ...`)은 `legacy/core`
바이너리 대상이므로 이 문서에서 다루지 않는다.

## 1. 설치

필요 것: Go 1.26+, Dart SDK 3.x. 원샷 설치에는 Codex CLI도 필요하다.

### 원샷 (권장)

```sh
bash scripts/install.sh
```

- `$HOME/.local/bin/codeflow` 바이너리 설치
- Codex MCP 등록 — Dart 어댑터 경로가 함께 기록되어 `CODEFLOW_ADAPTER_DART_BIN`
  설정이 불필요하다
- `$HOME/.codex/skills/codeflow` 스킬 설치
- 셸 rc와 분석 대상 저장소는 수정하지 않는다. 다른 명령을 가리키는 동명 MCP나
  수정된 스킬은 덮어쓰지 않고 중단한다(`CODEFLOW_MCP_NAME`으로 이름 변경 가능).

### 소스 직접 빌드 (Codex 없이)

```sh
make build   # bin/codeflow 생성
```

이 경로에서는 매 명령마다 어댑터를 지정해야 한다:

```sh
export CODEFLOW_ADAPTER_DART_BIN="dartrun:$PWD/adapters/dart"
./bin/codeflow ...
```

## 2. 저장소 준비 (최초 1회)

```sh
codeflow init HOME/workspace/<플러터-앱>
```

`.codeflow/workspace.json`을 만들고 어댑터 핀을 기록한다.

## 3. 흐름 추출과 발행

```sh
# 후보 나열 — 자동 점수순, deduped/excluded 플래그 포함
codeflow flows HOME/workspace/<플러터-앱>

# harvest+slice+fuse를 원자적으로 발행
codeflow publish HOME/workspace/<플러터-앱>

# 하나의 흐름 읽기 — ID는 publish 결과/flows 출력에서 확인
codeflow show flow-7232d63b96bd6efa --json HOME/workspace/<플러터-앱>
```

## 4. FlowView

```sh
codeflow serve HOME/workspace/<플러터-앱>
# 포트 지정: codeflow serve <저장소> --port 4569
```

출력되는 loopback URL(예: `http://127.0.0.1:4567/?token=...`)을 브라우저로
열면 발행된 세대의 흐름을 타임라인·코드 앵커와 함께 읽을 수 있다. 종료는
Ctrl-C.

## 5. 진단과 제거

```sh
# 환경 진단 — manifest, Dart SDK, 어댑터, 스키마를 항목별로 판정
codeflow doctor HOME/workspace/<플러터-앱>
codeflow version

# 설치 제거 — CodeFlow가 소유한 MCP·스킬·바이너리만 제거,
# 남기는 자산은 이유와 함께 출력
$HOME/.local/bin/codeflow uninstall
```

## 신뢰 경계

FlowView는 로컬 리뷰 페이지다. 서버는 문자 그대로 `127.0.0.1:<포트>`에만
바인딩되며 인증이 필요한 JSON API는 런타임 토큰(URL 파라미터 또는
`X-CodeFlow-Token` 헤더)을 요구한다. 이 포트를 프록시·터널로 노출하지
마십시오. 배포·호스팅·원격 동기화는 기본 경로에 없으며, 현재 릴리스 라인은
로컬 빌드다(버전 확인은 `codeflow version`). 서명 릴리스·Homebrew 배포는
별도 결정 전까지 보류 중이다([release-handoff](./release-handoff.md)).

LLM 또는 코딩 에이전트가 이 결과를 읽고 설명할 때의 도구 선택과 신뢰 규칙은
[`llm-usage.md`](./llm-usage.md)를 참고한다.
