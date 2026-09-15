# FlowView Storyboard 코드 이해 최종 스펙

- Contract ID: `FLOWVIEW-CODE-COMPREHENSION`
- Contract Status: Approved
- Created: 2026-09-14
- Amended: 2026-09-15
- Intent Status: Hardened
- Implementation Status: Specification only. 구현·삭제·배포·사용자 가치 검증 완료가 아니다.
- Source: 기존 FlowView 코드 이해 스펙, 현재 구현 조사, 사용자 지시. Storyboard를 기본 화면으로 사용하고, 현재 디자인을 보존하며, Live Semantic Map을 제거한다.
- Decision Records: [FlowView 개선 방향](../decisions/2026-09-14-flowview-code-comprehension-ko.md), [Storyboard 중심 전환](../decisions/2026-09-15-flowview-storyboard-direction-ko.md)
- Glossary: [설계 용어집](../glossary.md)
- Supersedes: `FLOWVIEW-ADAPTIVE-LANES`, `FLOWVIEW-MACRO-CONTEXT-STORYBOARD`, `FLOWVIEW-MODULAR-FRONTEND`, `FLOWVIEW-SVELTE-PIPELINE-MCP`의 Live 화면·고정 관문·계층 우선 표시 방향. 기존 분석, source, 보안, schema 호환 계약은 이 계약으로 대체하지 않는다.

## 1. 문제와 목표

현재 FlowView는 분석된 모든 step과 모든 source context를 기본 화면에 전달하고, workspace event가 발생하면 화면을 다시 분석·교체한다. 이 동작은 코드베이스의 중요한 처리 순서보다 내부 구현과 변경 상태를 먼저 보이게 한다.

FlowView는 사용자가 이해하려는 코드 동작을 storyboard 장면으로 보여준다. 각 장면은 시작, 판단, 처리, 외부 효과, 결과 또는 분석 경계처럼 이해에 필요한 사건을 나타낸다. 프로젝트별 architecture는 장면을 나누는 기준이 아니라 선택한 장면의 보조 정보다.

| Intent | Goal | 관찰 가능한 결과 |
|---|---|---|
| INT-FLOW-01 | GOAL-FLOW-01 | 사용자는 storyboard에서 코드 동작의 시작부터 결과 또는 분석 경계까지 이해한다. |
| INT-FLOW-01 | GOAL-FLOW-02 | 사용자는 선택한 장면의 실제 source, 조건, 직접 연결을 확인한다. |
| INT-FLOW-02 | GOAL-FLOW-03 | 사용자는 명시적으로 다시 분석한 결과 또는 선택한 두 분석의 차이를 확인한다. |
| INT-FLOW-02 | GOAL-FLOW-04 | 사용자는 선택한 장면의 검증된 직접 연결을 따라 다른 storyboard를 보고 원래 위치로 돌아온다. |

## 2. 범위

### 포함 범위

- storyboard projection, source context, partial 결과와 분석 경계 표시
- 현재 Svelte FlowView의 storyboard, 코드 뷰, 좌측 rail, 조건 강조, Blast Radius Radar, 3열 레이아웃과 시각 언어 보존
- 사용자가 누른 다시 분석, 비교 기준 선택, 변경 전후 비교, 직접 연결 탐색
- 별도 Live Semantic Map 화면, 자동 watcher·stream 갱신, Live 전용 문서·샘플·테스트·코드의 제거
- 삭제 전의 caller·consumer·공개 URL·MCP·저장 data inventory와 호환성 검증

### 제외 범위

- 프로젝트마다 동일한 architecture 또는 고정 5개 관문 역할을 강제하는 동작
- source 근거 없이 장면 제목, 처리 연결, 실행 결과를 만들어 내는 동작
- edit event만으로 분석, 화면 교체, baseline 이동, 비교 시작을 하는 동작
- 전역 영향 확정, 자동 코드 작성·실행, 기능 완료 판정
- snapshot, Evidence, proof, secret redaction, source read-only, public schema를 우회하거나 약화하는 변경

## 3. 확인된 사실과 원인

1. `BuildFlowViewProjection`은 비핵심 step을 접고 보이는 step 수를 제한하지만, 현재 HTTP task-view 응답은 전체 semantic map과 모든 step의 source context를 보낸다.
2. 현재 Svelte store는 projection이 아닌 `semanticMap.steps`를 렌더링한다.
3. 현재 Svelte bootstrap은 workspace stream의 `generation.published`를 수신하면 같은 query를 다시 분석한다.
4. 현재 디자인에는 storyboard, 코드 뷰, 좌측 rail, source context, Radar가 이미 있다. 이 컴포넌트는 유지 대상이다.
5. 별도 Live 화면, `/live` route, `live=1`, Live template, watcher, pipeline, event stream, prototype와 전용 browser workspace가 남아 있다.

## 4. Storyboard 계약

### 4.1 장면 구성 규칙

Storyboard는 source와 검증된 연결에서 만든 순서 있는 장면과 분기 집합이다. 장면은 단일 함수, 클래스, 파일 또는 architecture label과 일대일 대응하지 않는다.

다음 중 하나가 새 장면을 만든다.

1. 코드 동작의 시작 이벤트 또는 entry
2. 결과를 바꾸는 검증, 분기, 실패 처리
3. 의미 있는 상태 변화 또는 transaction 경계
4. 외부 호출, 영속화, 비동기 인계
5. 확인된 처리 결과
6. 더 이상 연결을 확인하지 못한 분석 경계

위 조건을 만들지 않는 연속 내부 step은 같은 장면에 묶는다. architecture label, 패키지 경계, 파일 변경, 함수 호출 수는 단독으로 장면 분리 근거가 아니다.

### 4.2 `StoryboardProjection`

`StoryboardProjection`은 canonical semantic map에서 파생하는 화면 전용 결과다. 기존 map, Evidence, public payload를 바꾸지 않는다. public 결과로 노출해야 하면 별도 schema version과 producer·consumer fixture를 먼저 추가한다.

| 필드 | 규칙 |
|---|---|
| `frameId` | 같은 분석 안에서 안정적으로 선택할 수 있는 장면 식별자 |
| `ordinal` | 기본 경로에서의 읽기 순서 |
| `role` | `entry`, `decision`, `process`, `effect`, `result`, `boundary` 중 하나 |
| `title` | 기술 함수명이 아닌 처리 목적. 근거가 부족하면 추측 대신 확인되지 않은 목적임을 표시 |
| `stepRefs` | 장면을 뒷받침하는 canonical step 목록 |
| `primaryStepRef` | 기본 source·rail·Radar 선택에 쓰는 step |
| `sourceAnchor` | 같은 snapshot의 기본 source 위치 |
| `condition` | 결과를 바꾸는 검증된 조건 또는 없음 |
| `outcomes` | 성공, 실패, 비동기 인계, 분석 경계 |
| `collapsedDetail` | 접은 내부 step 수와 접은 이유 |
| `architecture` | 선택 사항. 관측된 경우에만 보조 label로 표시 |
| `status` | `verified`, `partial`, `unknown` 중 하나 |

### 4.3 표시 규칙

- 기본 화면은 `StoryboardProjection`의 장면과 장면 사이의 확인된 관계만 렌더링한다.
- `semanticMap.steps`, 모든 edge, 모든 source context는 기본 화면의 목록으로 렌더링하지 않는다.
- 장면을 선택하면 해당 장면의 primary source context를 연다. 접힌 detail과 직접 relation은 사용자가 요청했을 때만 연다.
- source, condition, direct caller/callee, state read/write, 관련 테스트는 같은 snapshot과 Evidence에 결합해야 한다.
- partial·unknown·분석 실패는 구현되지 않음, 실행됨, 영향 없음으로 바꾸어 표현하지 않는다.

## 5. 화면과 상호작용

### 5.1 현재 디자인 보존

`MacroStoryboard`, `NavRail`, `CodeFlowPanel`, `ProcessFlowPanel`, `ContextAside`, Blast Radius Radar, 선택 카드, 코드 강조, 반응형 3열 레이아웃의 시각적 구조와 상호작용을 보존한다.

변경은 컴포넌트의 위치나 디자인 언어가 아니라 데이터의 우선순위다.

| 표면 | 유지할 동작 | 변경할 동작 |
|---|---|---|
| storyboard | 카드, track, 선택 스타일, source로 이동 | 전체 step 대신 storyboard 장면 표시 |
| 좌측 rail | 장면 선택과 현재 선택 강조 | step 목록 대신 장면 목록 표시 |
| 코드 뷰 | 코드/처리 보기, 조건 강조, 접기·펼치기 | 선택 장면의 source를 먼저 표시 |
| Radar | 선택 대상, 직접 caller/callee, state, 관련 테스트 | 검증된 직접 관계만 사용하고 자동 전역 영향 추정을 하지 않음 |
| 상단 상태 영역 | query, 분석 상태, 다시 분석, 비교 진입 | Live 상태, pause, pending, 자동 apply, Change Pulse 제거 |

### 5.2 명시적 명령

| 사용자 동작 | 결과 |
|---|---|
| storyboard 보기 | entry 후보를 해석하고 하나로 확인된 snapshot을 분석해 storyboard를 표시한다. |
| 다시 분석 | 같은 요청의 새 snapshot을 한 번 분석한다. 성공한 결과만 화면을 바꾸며 baseline은 유지한다. |
| 비교 기준 선택 | 실제 보존된 분석을 baseline으로 고정한다. |
| 변경 전후 비교 | 사용자가 고른 baseline과 명시적으로 고른 현재 분석을 비교한다. |
| 직접 연결 보기 | 선택 장면의 같은 snapshot 직접 relation만 연다. |

edit event, watcher event, SSE event, background publication은 위 동작 어느 것도 시작하거나 화면을 교체하지 않는다.

## 6. Live Semantic Map 제거와 호환성

제거는 FlowView 구현과 검증이 통과한 뒤에 수행한다. 먼저 storyboard 화면을 완성한 뒤 Live 경로를 삭제한다. 이 순서는 사용자 경험의 공백을 막기 위한 것이다.

### 삭제 후보

- `/live`, `live=1`, `LiveViewHTML`, Live prototype 선택과 관련 server routing
- Live coordinator, watcher, scheduler, pipeline, workspace stream, Live-only MCP handler와 전용 event hub 경로
- `internal/flowview/live_view.html`, generator, Live sample HTML, Live Semantic Map browser workspace와 전용 test suite
- Live Semantic Map을 제품 기능으로 소개하는 README, guide, skill, architecture 문서
- Change Pulse, Live notice, pause/pending/apply 상태와 자동 baseline·자동 refresh UI

### 보존 대상

- snapshot capture, adapter 분석, semantic map, Evidence, redaction, proof, current/historical 결과, source context
- 명시적 재분석, 선택 비교, 직접 relation, requirement evidence
- 기존 FlowView URL·CLI·MCP 중 Live 전용이 아닌 query와 source 결과
- persisted data와 공개 schema. 제거가 schema 또는 저장 reader에 영향을 주면 migration 또는 compatibility reader를 별도 계약으로 검증한다.

삭제 대상의 caller와 consumer를 먼저 inventory한다. Live URL이나 MCP operation을 다른 의미로 조용히 재사용하지 않는다. Live 전용 public seam은 제거 사실과 대체 FlowView entry를 release note에 기록한다.

## 7. 불변식과 실패 동작

1. storyboard의 모든 source와 relation은 같은 immutable snapshot의 근거를 사용한다.
2. 화면은 명시적 사용자 요청에 성공한 결과만 교체한다. 늦은 응답, 취소, 실패는 현재 선택, source 위치, 펼침 상태, baseline을 바꾸지 않는다.
3. 선택한 장면이 새 분석에서 대응되지 않으면 임의 장면으로 이동하지 않고 기존 위치와 이유를 보존한다.
4. source, Evidence, baseline은 화면이 읽는 동안 GC와 재시작에서 보호하거나 명시적으로 unavailable을 표시한다.
5. authorization, Host/Origin, canonical path, secret redaction, source read-only는 모든 FlowView 결과에서 유지한다.
6. 화면에는 compiler epoch, lag, settlement flag, 내부 scheduler 상태 같은 telemetry를 보이지 않는다.

## 8. 결정

| ID | 질문 | 결정 | 기각한 대안 | 결과 |
|---|---|---|---|---|
| 결정1 | 기본 화면의 조직 기준은 무엇인가 | storyboard 장면 | architecture lane, 전체 step timeline | 서로 다른 프로젝트에도 같은 이해 단위를 제공한다. |
| 결정2 | architecture 정보는 어떻게 쓰는가 | 선택 장면의 보조 label | 모든 프로젝트에 고정 계층 적용 | architecture를 확인하지 못해도 화면이 동작한다. |
| 결정3 | 현재 디자인은 어떻게 하는가 | storyboard·코드 뷰·Radar·3열 레이아웃 유지 | 화면 전체 재설계 | 구현 범위를 정보 구조와 Live 제거로 제한한다. |
| 결정4 | 변경과 갱신은 어떻게 하는가 | 명시적 재분석·비교만 허용 | watcher 기반 자동 분석·교체 | 읽기 위치와 baseline이 안정적이다. |
| 결정5 | Live Semantic Map은 어떻게 하는가 | 별도 제품 경로와 전용 코드를 제거 | FlowView 안에 Live 기능을 계속 유지 | FlowView 하나만 남기고 분석 안전성은 보존한다. |

## 9. 기능 수용 기준

- FA-01: 사용자가 흐름을 조회하면 시스템은 storyboard 장면, 시작, 결과 또는 분석 경계와 해당 source를 표시한다.
- FA-02: 시스템은 architecture label이 없거나 프로젝트별 구조가 달라도 storyboard를 구성한다.
- FA-03: 시스템은 내부 step을 기본 화면에 모두 나열하지 않고, 장면 분리 규칙에 맞지 않는 detail을 접는다.
- FA-04: 사용자가 장면을 선택하면 storyboard, rail, 코드 뷰, Radar가 같은 primary step과 source 위치를 선택한다.
- FA-05: 시스템은 정상, 조건 분기, 실패, 비동기 인계, 외부 효과, 분석 중단을 서로 구분해 표시한다.
- FA-06: edit event만 발생하면 시스템은 분석, 화면 교체, baseline 이동, source 위치 변경을 하지 않는다.
- FA-07: 다시 분석 성공은 새 결과로 한 번 전환하고, 실패·취소·늦은 응답은 기존 화면을 보존한다.
- FA-08: 비교는 사용자가 선택한 두 분석에만 적용하며 baseline을 자동으로 바꾸지 않는다.
- FA-09: Radar와 직접 연결 탐색은 같은 snapshot의 검증된 직접 relation만 보이며, 전역 영향이나 runtime 결과를 단정하지 않는다.
- FA-10: partial, dynamic dispatch, 누락 source, adapter 미지원은 unknown 또는 분석 경계와 이유로 표시한다.
- FA-11: Live Semantic Map route, 자동 stream 갱신, Live 전용 UI와 문서가 제거되고 FlowView 하나만 제공된다.
- FA-12: schema, source read-only, secret redaction, authorization과 보존된 비-Live CLI·MCP 소비자는 회귀하지 않는다.
- FA-13: 각 FlowView 기능은 단위, HTTP/contract, browser interaction 수준에서 실제 입력과 기대 결과로 검증된다.
- FA-14: 실제 코드 이해 과제에서 storyboard가 기존 전체-step 화면보다 이해 시간, 정답률, 재탐색, 잘못된 확신에 주는 영향을 기록한다. 수치와 합격 기준은 평가 전에 고정한다.

## 10. 검증 계획

| 검증 | 필수 증거 |
|---|---|
| projection | 장면 분리·묶기, branch/failure/effect/boundary 보존, architecture 부재, 접힌 detail을 Go 단위·contract test로 검증 |
| task-view HTTP | projection과 map/Evidence/source identity 일치, 기본 화면에 전달할 장면 선택, partial·unknown·오류 응답 검증 |
| Svelte store·component | raw step 대신 storyboard 장면을 렌더링하고 선택 상태가 모든 기존 표면에 동기화됨을 검증 |
| browser | query, 장면 선택, keyboard, 긴 source, 실패, partial, 재분석, 비교, relation 왕복, 반응형 레이아웃을 실제 제품 화면에서 검증 |
| Live 제거 | `/live`, `live=1`, stream auto-refresh, Live-only MCP·test·asset·doc reference가 남지 않음을 inventory와 회귀 test로 검증 |
| 안전성과 호환성 | schema fixture, secret/path/auth, storage/GC, non-Live CLI/MCP을 검증 |
| repository 명령 | `make fmt`, `make check-naming`, `make vet`, `make test`; shared snapshot·storage·request 변경 시 `go test -race ./internal/...`; TypeScript adapter 변경 시 `npm --prefix adapters/typescript test`; browser 명령은 구현 전 현재 runner를 확인해 기록 |

fake fixture만으로 실제 지원 언어 또는 사용자 이해 개선을 주장하지 않는다. 모든 수용 기준은 command, source revision, actual result를 연결한 증거가 있어야 완료다.

## 11. 구현 순서와 완료

1. canonical map에서 `StoryboardProjection`을 만들고, current endpoint와 Svelte store가 이를 기본 데이터로 사용하게 한다.
2. 현재 디자인 안에서 storyboard, rail, 코드 뷰, Radar 선택 동기화를 유지한 채 raw-step 기본 렌더링을 제거한다.
3. 명시적 재분석·비교·직접 연결의 상태 보존과 실패 동작을 검증한다.
4. Live caller·consumer inventory와 replacement entry를 확인한 뒤 Live 코드·route·UI·asset·문서를 제거한다.
5. 전체 repository 검증과 실제 코드 이해 과제를 실행한다.

완료는 FA-01부터 FA-13까지의 검증, 적용되는 안전성·호환성 검증, Live 제거 inventory, 같은 snapshot source/Evidence 보존으로 판단한다. FA-14의 사용자 평가는 기술 완료와 별도이며, 결과가 없으면 다음 확장을 사용자 가치로 주장하지 않는다.

## 12. 미결정

차단하는 제품 미결정은 없다. 장면 제목을 위한 model enrichment는 선택 기능이며, 근거 없는 제목이나 연결을 만들 수 없다. 지원 언어별 실제 corpus와 사용자 평가 rubric은 구현 전에 고정한다.
