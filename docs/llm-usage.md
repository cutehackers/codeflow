# CodeFlow LLM usage

이 문서는 LLM 또는 코딩 에이전트가 CodeFlow의 결과를 정확하게 읽고,
사용자에게 이해하기 쉬운 코드 흐름으로 설명하기 위한 사용 계약이다.
CodeFlow는 코드 흐름의 근거를 만들고, LLM은 그 근거를 설명한다. LLM이
FlowIR의 빈 부분을 자체 추론으로 채우면 안 된다.

## 1. 가장 짧은 사용 흐름

먼저 CodeFlow 저장소에서 Core를 빌드한다.

```sh
make local
```

분석할 프로젝트에 대해 로컬 Core를 실행하고 계속 켜 둔다.

```sh
./bin/codeflow serve \
  --repo HOME/workspace/sgp-981-app \
  route:/join
```

`serve`가 출력한 `CodeFlow review URL`은 분석 결과가 저장된 뒤에만
노출된다. MCP는 이 Core에 연결할 뿐 Core, SQLite 또는 Dart adapter를
별도로 시작하지 않는다.

MCP 설정은 다음과 같다.

```json
{
  "mcpServers": {
    "codeflow": {
      "command": "HOME/workspace/codeflow/bin/codeflow",
      "args": [
        "mcp",
        "--repo",
        "HOME/workspace/sgp-981-app"
      ]
    }
  }
}
```

실행 파일이 `PATH`에 있다면 `command`는 `codeflow`로 줄일 수 있다.
MCP를 사용할 수 없는 LLM은 아래 CLI 명령의 JSON을 읽어도 된다.

```sh
./bin/codeflow analyze \
  --repo HOME/workspace/sgp-981-app \
  route:/join
```

## 2. MCP 도구 선택

항상 정확한 `flow_id`를 전달한다. 기본값에 의존하지 않는다.

| 목적 | 도구 | 입력 | 사용 시점 |
| --- | --- | --- | --- |
| 여러 화면 관계 이해 | `workspace` | 없음 | 둘 이상의 flow가 게시됐을 때 가장 먼저 |
| 현재 흐름 이해 | `current` | `flow_id: route:/join` | 단일 flow일 때 첫 번째 |
| 한 단계 상세 확인 | `step` | `flow_id`, `step_id` | 특정 상태·호출·화면 결과를 설명할 때 |
| 미확정 경계 확인 | `unknowns` | `flow_id` | 설명에 추론이 필요해지는 즉시 |
| 코드 변경 반영 | `refresh` | 없음 | 파일이 바뀌었거나 basis가 오래됐을 때 |
| FlowView URL 취득 | `open` | `flow_id` | 사용자가 화면 확인을 요청했을 때만 |
| 기준선 비교 | `diff` | 없음 | Core에 baseline 비교가 이미 게시됐을 때 |

여러 화면은 다음처럼 하나의 Core에 함께 요청한다. 각 flow는 독립된 FlowIR을
유지하며 `workspace`는 동일한 Basis에서 확인된 화면 이동만 연결한다.

```sh
./bin/codeflow serve \
  --repo HOME/workspace/sgp-981-app \
  --flow route:/join \
  --flow route:/home \
  --flow route:/auth
```

LLM은 `workspace.flow_ids`와 `screen_flow_edges`를 먼저 읽고, 필요한 화면만
`current(flow_id)`로 상세 조회한다. 여러 flow의 step을 하나의 timeline으로
합치거나 서로 다른 `worktree_fingerprint`를 연결하면 안 된다.

`diff`가 `BASELINE_NOT_SELECTED`를 반환하면 결과를 추측하지 않는다.
실행 중인 `serve`를 `Ctrl-C`로 종료한 뒤 로컬 Git 기준선과 직접 비교하고,
계속 FlowView가 필요하면 `serve`를 다시 실행한다.

```sh
./bin/codeflow compare \
  --repo HOME/workspace/sgp-981-app \
  --baseline main \
  route:/join
```

## 3. 응답을 읽는 순서

CodeFlow 응답은 공통 envelope를 사용한다.

```text
basis → status → data → unknowns → view_url
```

LLM은 다음 순서로 읽는다.

1. `basis.repository`, `head_revision`, `worktree_fingerprint`, `dirty`로
   어떤 코드 스냅샷인지 확인한다.
2. envelope의 `status`로 Core가 `ready`인지 확인하고,
   `data.current.id`로 요청한 흐름이 맞는지 확인한다.
3. `data.current.status`로 흐름의 신뢰 상태가 `observed`, `mixed`,
   `unknown` 중 무엇인지 확인한다.
4. `data.current.steps`를 `order` 순서대로 읽는다.
5. 각 step의 `trigger_fact → behavior_facts → result_facts`를 `facts`와
   연결한다.
6. `causal_edges`로 이전 원인과 다음 결과를 확인한다.
7. `branches`는 모든 `outcome_step_ids`를 각각 설명한다.
8. 마지막으로 `unknowns`를 확인하고, 빠진 연결이 있으면 설명에 함께
   표시한다.

경로를 실제 실행할 때 하나만 선택한다는 사실만으로 `unknown`이라고
판단하지 않는다. resolved AST가 모든 조건 결과를 증명했다면 그 분기는
완전한 정적 흐름이다.

## 4. 신뢰 상태 규칙

| 상태 | LLM이 할 수 있는 말 | 금지되는 말 |
| --- | --- | --- |
| `observed` | 현재 basis의 코드 근거로 확인됐다고 설명 | 런타임에서 반드시 실행됐다고 단정 |
| `mixed` | 확인된 부분과 미확정 부분을 분리해 설명 | 전체 흐름이 확인됐다고 요약 |
| `unknown` | 어디까지 확인됐고 무엇이 빠졌는지 설명 | 가장 그럴듯한 대상·상태·서버 응답을 선택 |
| `stale` | 코드가 바뀌어 `refresh`가 필요하다고 설명 | 이전 anchor를 현재 코드 근거로 사용 |
| `unavailable` | typed error와 복구 방법을 전달 | 자체 코드 스캔으로 CodeFlow 결과를 대체 |

LLM의 세션 기억, 파일명 유추, 일반적인 Flutter 관례는 `observed` 증거가
아니다. `unknown`을 보완해야 할 때는 우선 현재 소스의 resolved AST,
심볼, 명시적 return, 상태 대입, listener, route 결과를 더 연결한다.
런타임 trace는 정적 코드로 선택지를 완성할 수 없을 때만 보조 증거로
제안한다.

## 5. 사용자에게 설명하는 형식

내부 ID와 analyzer 용어를 그대로 나열하지 말고 다음 형식을 사용한다.

```text
현재 코드 흐름
1. 사용자가 뒤로가기를 누릅니다.
2. 가입이 끝났으면 /home으로 이동합니다.
3. 끝나지 않았으면 취소 확인창을 보여 줍니다.
4. 계속 가입하면 화면 이동 없이 현재 흐름을 종료합니다.
5. 취소하면 JoinCancelEvent가 전달되고 isCanceled=true로 바뀝니다.
6. listener가 상태를 감지해 /auth로 이동합니다.

상태 변화
JoinCancelEvent → JoinState.isCanceled=true → listener 감지 → /auth

확인되지 않은 부분
없음
```

설명에는 다음 원칙을 적용한다.

- 사용자 동작, 코드 호출, 상태 변화, 화면 결과를 원인 순서로 쓴다.
- `symbol_id`, hash, 내부 reason code는 사용자가 근거를 요청할 때만
  부가 정보로 제공한다.
- `unknowns`가 있으면 “왜 남았는지 / 현재까지 확인한 것 / 다음에 확인할
  코드”로 바꿔 쓴다.
- 소스 위치는 `evidence.path:line_range`를 사용한다. 기억한 경로나 줄
  번호를 만들지 않는다.
- FlowView를 열어 달라는 요청에는 `open`의 `view_url`을 제공한다.

## 6. 변경 후 재확인

사용자 또는 에이전트가 코드를 변경했다면 이전 결과를 재사용하지 않는다.

1. 실행 중인 `serve`가 파일 변경을 감지해 새 snapshot을 게시할 때까지
   `status`를 확인한다. watcher 이벤트가 누락됐거나 즉시 재확인이 필요하면
   `refresh`를 호출한다.
2. 새 `current`를 가져온다.
3. 이전과 새 `worktree_fingerprint`가 다른지 확인한다.
4. 변경된 step의 상태 변화와 화면 결과를 다시 설명한다.
5. baseline이 필요하면 `compare` 결과의 added, removed, changed state,
   changed causal edge, new unknown만 요약한다.

MCP 응답의 `basis`에는 전체 manifest 대신 `manifest_count`가 포함된다.
정확한 현재성은 `head_revision`과 `worktree_fingerprint`로 확인하고, 필요한
코드만 `step`의 anchor/lens 또는 FlowView에서 읽는다.

`refresh` 실패 시 마지막 스냅샷이 보존될 수 있으므로 `status=analyzing`
또는 typed error를 사용자에게 알리고, 새 분석이 성공한 것처럼 말하지
않는다.

## 7. 금지 사항

- CodeFlow와 별개의 스캐너나 인과 분석기를 LLM이 즉석에서 구현하지 않는다.
- product source를 CodeFlow 사용을 위해 수정하지 않는다.
- `unknown`을 일반 지식이나 추측으로 `observed`로 승격하지 않는다.
- auth token, `.codeflow/runtime.json`, 서버 응답 body 또는 비밀 값을
  답변에 노출하지 않는다.
- 사용자가 요청하지 않았는데 FlowView를 열거나 외부로 게시하지 않는다.
- 현재 basis와 다른 Git revision의 코드 근거를 섞지 않는다.

## 8. LLM용 최소 체크리스트

```text
[ ] 정확한 repo와 flow_id를 사용했는가?
[ ] current를 먼저 읽었는가?
[ ] basis와 worktree fingerprint를 확인했는가?
[ ] step을 order 순으로 설명했는가?
[ ] 코드 → 상태 → 화면 결과가 연결됐는가?
[ ] 모든 branch outcome을 빠짐없이 설명했는가?
[ ] unknown을 추측하지 않고 사용자 언어로 설명했는가?
[ ] 코드가 바뀌었다면 refresh 후 새 결과를 읽었는가?
[ ] FlowView는 사용자가 요청했을 때만 제공했는가?
```

사람이 직접 실행하는 명령과 캐시 관리는
[`local-usage.md`](./local-usage.md)를 참고한다.
