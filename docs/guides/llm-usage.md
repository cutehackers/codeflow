# CodeFlow v0.4.0 LLM·에이전트 사용 계약

이 문서는 AI 에이전트가 CodeFlow v0.4.0을 사용할 때 따라야 하는 실행 및 설명 기준이다. CodeFlow가 반환한 코드 근거, 식별자와 검증 상태만 사실로 사용한다. 확인되지 않은 내용을 추론으로 채우지 않는다.

사용자가 실제로 요청할 수 있는 기능과 프롬프트 예시는 [기능 및 프롬프트 가이드](feature.md)를 참고한다.

## 0. 설치와 실행 전 확인

### 원격 설치

```sh
curl -fsSL https://raw.githubusercontent.com/cutehackers/codeflow/main/scripts/install.sh | bash
```

설치 프로그램은 릴리즈에 포함된 CodeFlow Core와 어댑터를 설치하고 지원하는 에이전트의 MCP 설정 및 CodeFlow skill 배포를 시도한다. 설치 결과는 환경에 따라 달라질 수 있으므로 반드시 확인한다.

```sh
$HOME/.local/bin/codeflow version
$HOME/.local/bin/codeflow doctor <프로젝트-루트>
```

- TypeScript/JavaScript 어댑터 실행에는 Node.js가 필요하다.
- 릴리즈에 Dart 실행 파일이 없고 소스 어댑터를 사용하는 경우 Dart SDK가 필요하다.
- 소스 체크아웃에서 `bash scripts/install.sh`로 빌드하려면 Go가 필요하다.
- MCP나 어댑터가 준비되지 않았다면 설치 또는 어댑터 설정이 필요하다고 보고한다. CLI 결과를 Live MCP 결과처럼 설명하지 않는다.
- 설치 후 에이전트 앱을 재시작하거나 새 task를 열어 MCP와 skill을 다시 로드한다.

프로젝트가 아직 초기화되지 않았다면 한 번 실행한다.

```sh
$HOME/.local/bin/codeflow init <프로젝트-루트>
```

삭제는 다음 명령으로 수행한다. 설치기가 소유하지 않은 파일이나 사용자가 수정한 설정은 보존될 수 있다.

```sh
$HOME/.local/bin/codeflow uninstall
```

## 0.1 프로젝트 아키텍처 분석 및 `codeflow.layers.yaml` 작성 가이드

항상 `pubspec.yaml`, `package.json`, `go.mod`처럼 프로젝트 종류를 식별할 수 있는 파일이 있는 **프로젝트 루트**를 `target`으로 사용한다. 특정 feature 디렉터리를 `target`으로 전달하지 않는다. 분석 범위는 질의나 entry symbol로 좁힌다.

`codeflow.layers.yaml`이 있으면 먼저 읽고 그대로 따른다. 없고 Core Flow의 레이어 분류가 필요하면 다음 순서로 작성한다.

1. 매니페스트와 주요 프레임워크를 확인한다.
2. feature-first, layer-first, FSD, MVC/MVVM, Clean/Hexagonal 또는 monorepo 구조인지 파악한다.
3. 디렉터리 이름보다 실제 책임과 호출 방향을 우선해 레이어를 분류한다.
4. 프로젝트에 실제로 존재하는 레이어만 진입점에서 말단 순서로 선언한다.
5. 실제 저장소 상대 경로에 맞는 `pathPatterns`와 프로젝트 용어를 `aliases`에 기록한다.

지원하는 canonical layer는 다음 7개다.

| 레이어 | 책임 예시 |
|---|---|
| `presentation` | 화면, 컴포넌트, 사용자 이벤트 |
| `controller` | 요청 수신, 상태 관리, ViewModel, handler |
| `usecase` | 애플리케이션 작업 조율 |
| `domain` | 핵심 규칙, entity, value object |
| `data` | repository 구현, DAO, 데이터 변환 |
| `infra` | 저장소·플랫폼·메시징 같은 기반 기능 |
| `external` | 외부 API, 원격 client, 제3자 SDK |

`unknown`은 분석 결과에서 확인되지 않은 레이어를 나타내는 상태이며 `layers[].name`으로 선언하는 canonical layer가 아니다.

최소 예제:

```yaml
version: 1
strictOrder: true
allowUnknownLayer: false
layers:
  - name: presentation
    aliases: [ui, view, page]
    pathPatterns: ["**/presentation/**", "**/ui/**"]
  - name: controller
    aliases: [controller, handler, viewmodel]
    pathPatterns: ["**/controllers/**", "**/handlers/**"]
  - name: usecase
    aliases: [usecase, application, service]
    pathPatterns: ["**/usecases/**", "**/application/**"]
  - name: data
    aliases: [repository, datasource, dao]
    pathPatterns: ["**/repositories/**", "**/datasources/**"]
  - name: external
    aliases: [api, client, gateway]
    pathPatterns: ["**/clients/**", "**/external/**"]
```

역방향 또는 오류 전이는 레이어 순서를 깨뜨리지 말고 branch로 표현한다. `strictOrder: false`나 `allowUnknownLayer: true`는 불확실성을 숨기지 않으며 경고와 unknown을 남긴다.

## 1. 요청에 맞는 작업 경로 선택

CodeFlow skill은 자동 호출되지 않는다. 사용자가 `$codeflow`를 명시했을 때 사용하며, 요청에 필요한 경로만 선택한다.

### 1.1 기존 기능의 정적 Core Flow

현재 캡처한 코드에서 기능의 시작부터 완료까지 경로를 알고 싶을 때 사용한다.

```text
harvest_flows 또는 analyze_flow
→ 필요한 경우 publish_core_flow
→ get_flow_payload
→ report_unknowns
→ 사용자가 화면을 요청한 경우 open_review
```

- `harvest_flows`: 자연어 질의로 후보 진입점을 찾는다.
- `analyze_flow`: 정확한 entry symbol을 어댑터로 분석하고 발행한다.
- `publish_core_flow`: 에이전트가 전체 레이어 흐름을 구성해야 할 때 검증된 artifact를 발행한다.
- 모든 단계의 anchor는 현재 캡처한 파일 내용과 일치해야 한다.
- `anchor_verification_failed`이면 해당 파일을 다시 읽고 현재 내용으로 anchor를 다시 계산해 한 번 재시도한다.
- `artifact_too_large`이면 의미 있는 하위 흐름으로 나눈다.
- Static FlowView는 캡처 시점의 구조를 보여줄 뿐 이후 편집까지 최신임을 증명하지 않는다.

### 1.2 편집을 따라가는 Live Semantic Map

사용자가 코드 변경 이후에도 설명이 최신인지 확인하고 싶을 때 사용한다.

```text
query_task_view(feature)
→ 실제 코드 수정
→ submit_versioned_edit(변경된 각 파일)
→ get_workspace_activity
→ get_current_answer
→ get_generation_proof 또는 get_verified_gap
```

`submit_versioned_edit`에는 저장소 상대 경로, 수정 후 파일 전체 내용, 증가한 `documentVersion`과 변경 출처를 전달한다. 이 호출은 semantic snapshot에 변경을 제출하며 실제 파일 수정을 대신하지 않는다.

현재 일반 편집기의 파일 저장은 자동으로 Live Compiler에 전달되지 않는다. AI 에이전트, IDE 연동 또는 watcher 연동이 `submit_versioned_edit`를 호출해야 한다. 버전 충돌이 발생하면 반환된 현재 버전보다 1 큰 값으로 같은 내용을 한 번 재시도한다.

상태 확인은 다음 순서를 따른다.

1. `get_workspace_activity`에서 `editing` 또는 `reconciling`, `analyzing`, 최종 상태를 확인한다.
2. 기본 편집 병합 구간인 약 2초 동안 과도하게 반복 조회하지 않는다.
3. `get_current_answer`가 current를 반환해도 `get_generation_proof`가 동일한 식별자를 검증해야만 “현재 코드까지 검증됨”이라고 설명한다.
4. current proof가 없으면 `get_verified_gap`의 영향 범위, 지연, 대기 중인 revision과 원인을 설명한다.

Live 작업은 같은 MCP 서버와 같은 프로젝트 루트에서 계속 수행한다. 별도의 `codeflow view` 프로세스는 MCP가 관리하는 Live 상태를 공유하지 않는다.

### 1.3 변경 검토, 요구사항, 영향과 실패 분석

- 동작 변경 비교: `query_task_view`의 review mode 또는 `get_semantic_delta`
- 요구사항 충족 여부: `get_requirement_alignment`
- 특정 symbol 또는 change batch 영향: `get_change_impact`
- 실패 가능 경로와 incident 조사: `investigate_failure` 또는 strict debug/incident query

Semantic Delta에서는 추가·변경·제거된 동작과 근거만 바뀐 항목을 구분한다. 서로 다른 `workspaceEpoch`의 결과는 하나의 연속된 current 이력처럼 비교하지 않는다.

Requirement Alignment는 CodeFlow가 근거로 확인한 항목만 `confirmed`로 취급한다. `partial`, `not_observed`, `conflicting`, `unknown`을 완료로 바꾸지 않는다.

Change Impact의 `maxDepth`와 `maxNodes`는 조사 범위다. 결과에 없다는 이유로 영향이 없다고 단정하지 않는다.

Failure 조사에서는 다음 상태를 구분한다.

- 정적 코드에서 도달 가능한 경로
- 결정적으로 재현한 결과
- 신뢰된 runtime evidence에서 실제로 관찰한 결과

사용자가 제공한 로그는 조사 단서이며 그 자체로 신뢰된 runtime evidence가 아니다. 로컬 실행은 사용자가 정확한 실행 작업을 승인하고 CodeFlow가 consent 계약을 수락한 경우에만 수행한다.

### 1.4 선택적 Semantic Enrichment와 Semantic Approval

`request_semantic_enrichment`는 사용자가 모델 기반 제목, 요약 또는 설명을 요청한 경우에만 사용한다. 모델이 없거나 실패하거나 시간이 초과되어도 결정적 분석 결과를 유지한다.

현재 v0.4.0 배포 CLI에는 Model Host 설치·설정 명령이 없다. 별도 개발자 설정이 없으면 `Semantic Enrichment: unavailable`은 정상이다. 설정 범위와 모델 후보는 [Semantic Enrichment 가이드](semantic-enrichment.md)를 따른다.

Semantic Approval은 **저장된 모델 설명 후보를 사람이 검토한 뒤 그 문구에 대한 결정을 기록할 때** 사용한다.

```text
request_semantic_enrichment
→ get_evidence_pack
→ 사용자의 명시적 결정
→ submit_semantic_approval
→ 필요한 경우 get_semantic_approval_history
```

- 단순한 긍정 표현을 승인 요청으로 해석하지 않는다.
- 정확한 proposal, Evidence Pack, basis, generation, intent revision, 예상 승인 버전과 상태를 보존한다.
- Approval은 설명 문구에 대한 신뢰 기록이다.
- Approval은 구현 사실, current 여부, settlement, 요구사항 충족 상태나 runtime evidence를 변경하지 않는다.

### 1.5 프로젝트 탐색

처음 보는 프로젝트의 업무 영역과 대표 흐름을 알고 싶을 때 `explore_project_domains`를 사용한다. current 탐색에는 current proof가 필요하다. 과거 상태에는 정확한 basis, generation과 snapshot을 사용한다. 확인하지 못한 범위를 프로젝트 전체를 이해한 것처럼 설명하지 않는다.

### 1.6 릴리즈 capability 평가

`validate_release_capability`는 이미 수집된 immutable 평가 자료, 실행 보고서와 승인된 threshold를 평가한다. benchmark를 실행하거나 누락된 수치를 생성하거나 릴리즈를 승인하지 않는다.

- 근거가 빠지면 `incomplete`다.
- hard invariant가 실패하면 해당 capability가 차단된다.
- 실제 근거 수집과 측정은 `flowmeter`와 [릴리즈 근거 실행 가이드](../validation/release-capability-evidence-runbook.md)를 사용한다.

## 2. MCP 도구 대응표

v0.4.0은 다음 24개 MCP 도구를 제공한다. 호출 인자는 MCP가 등록한 현재 `inputSchema`를 따른다.

| 목적 | MCP 도구 |
|---|---|
| 검증된 Core Flow 발행 | `publish_core_flow` |
| 후보 흐름 탐색 | `harvest_flows` |
| 발행된 흐름 조회 | `get_flow_payload` |
| 정확한 진입점 즉시 분석 | `analyze_flow` |
| 구조화된 session draft 제출 | `submit_flow_draft` |
| Static FlowView 단계 이름·규칙 승인 | `approve_step` |
| 확인되지 않은 경계 조회 | `report_unknowns` |
| FlowView 열기 | `open_review` |
| 기능·review·impact·debug·incident 질의 | `query_task_view` |
| 현재 답 확인 | `get_current_answer` |
| Live 분석 진행상태 확인 | `get_workspace_activity` |
| versioned edit 제출 | `submit_versioned_edit` |
| current Generation Proof 확인 | `get_generation_proof` |
| 최신 코드와 마지막 검증 결과의 차이 확인 | `get_verified_gap` |
| generation 간 의미 변화 비교 | `get_semantic_delta` |
| 요구사항 충족 상태 확인 | `get_requirement_alignment` |
| 직접·간접 변경 영향 분석 | `get_change_impact` |
| failure와 incident 경로 조사 | `investigate_failure` |
| 선택적 모델 설명 요청 | `request_semantic_enrichment` |
| 검증되고 비밀 값이 제거된 근거 조회 | `get_evidence_pack` |
| Semantic Approval 상태 변경 | `submit_semantic_approval` |
| Semantic Approval 이력 조회 | `get_semantic_approval_history` |
| 프로젝트 domain과 대표 흐름 탐색 | `explore_project_domains` |
| 수집된 릴리즈 근거 평가 | `validate_release_capability` |

`approve_step`은 기존 Static FlowView 단계의 이름과 규칙을 승인한다. `submit_semantic_approval`은 exact proposal과 Evidence Pack에 연결된 Live Semantic Approval lifecycle을 기록한다. 서로 대체하지 않는다.

## 3. 결과 신뢰 상태

### 핵심 용어

| 용어 | 의미 |
|---|---|
| `workspaceEpoch` | 같은 작업 공간의 연속된 분석 이력을 구분하는 번호. 초기화나 기준 재설정으로 값이 바뀌면 이전 generation은 historical이다. |
| `snapshotId` | 분석기가 실제로 읽은 파일 상태의 식별자 |
| `computedBasisId` | 분석 입력과 설정을 묶은 계산 기준 식별자 |
| `generationId` | 그 기준으로 생성된 semantic 결과의 식별자 |
| `current` | 현재 live snapshot과 정확히 일치하며 proof 검증을 통과한 결과 |
| `historical` | 정확한 과거 generation에 속한 결과 |
| `candidate` | 검증 또는 publication authority가 아직 없는 결과 |
| `Verified Gap` | 최신 변경은 관찰했지만 현재 결과로 승격할 수 없는 상태와 그 원인 |
| `settlement=pending` | 현재 코드를 설명할 수 있으나 요구된 검증 작업이 아직 끝나지 않은 상태 |
| `unknown` | 근거나 관찰 범위가 부족해 판단할 수 없는 상태 |

다음 조건을 모두 만족할 때만 current라고 설명한다.

- 같은 프로젝트 target
- 같은 workspace epoch
- 같은 snapshot
- 같은 computed basis
- 같은 generation
- 같은 task intent revision과 query
- 유효한 current answer와 Generation Proof

하나라도 확인되지 않으면 CodeFlow가 반환한 `historical`, `candidate`, `unknown` 또는 Verified Gap을 유지한다.

모델 proposal, 사용자 Approval 또는 에이전트의 완료 보고는 다른 상태를 `current`, `confirmed`, `settled` 또는 runtime `observed`로 승격하지 않는다.

## 4. FlowSpec과 Core Flow 설명 규칙

정적 FlowSpec은 먼저 `flowId`, `title`, `description`, `basisSha`, `generatedAt`을 확인한 뒤 `steps`, `edges`, `truncated`, `unknowns`를 읽는다.

각 단계에서는 다음 정보를 우선한다.

1. `ordinal`과 `layer`에 따른 실행 순서
2. `kind`: mutation, call, guard 또는 branch
3. `provenance`: approved, session, derived 또는 unknown
4. `freshness`: fresh, stale 또는 orphaned
5. `anchor`와 `codeLens`
6. 상태 변화, 외부 효과, 분기와 edge resolution

- `stale`은 코드 변경으로 재검증이 필요하다.
- `orphaned`는 기존 symbol이 사라진 상태다. 다른 symbol로 임의 대체하지 않는다.
- `unresolved_dynamic`, `unresolved_type` 또는 `truncated` 경계를 추측으로 연결하지 않는다.
- `codeLens` 줄 번호를 만들지 말고 반환된 anchor를 그대로 사용한다.
- `unknowns`가 있으면 원인과 다음 확인 대상을 설명한다.
- Core Flow는 사용자 이벤트나 요청에서 시작해 처리가 끝날 때까지 아키텍처 레이어를 통과하는 단계만 포함한다.

사용자 응답은 다음 순서로 작성한다.

```text
1. 사용자가 요청한 기능의 결과
2. 시작점부터 완료까지의 코드 경로
3. 상태 변화, 조건, 외부 효과
4. 근거와 current/historical/candidate 상태
5. 확인되지 않은 범위 또는 Verified Gap
6. 사용자가 요청한 경우에만 FlowView URL
```

FlowView URL의 token이나 `.codeflow` 내부 포인터, 비밀 값은 로그나 일반 설명에 노출하지 않는다. `open_review`가 사용자에게 반환하도록 설계된 URL은 사용자가 화면을 요청했을 때 그대로 제공한다.

## 5. CLI 사용 범위

MCP가 없는 환경에서는 정적 탐색과 1회성 candidate 분석에 CLI를 사용할 수 있다.

```sh
codeflow flows --json <프로젝트-루트>
codeflow publish <프로젝트-루트>
codeflow show <flow-id> <프로젝트-루트> --json
codeflow query <프로젝트-루트> --mode feature --request "<기능 질문>" --json
codeflow status <프로젝트-루트> --json
codeflow view <프로젝트-루트>
```

- `publish_core_flow`는 MCP 전용이다.
- `codeflow query` 결과는 1회성 candidate 분석이며 MCP Live session의 current proof를 대신하지 않는다.
- `codeflow status`는 해당 호출에서 연 작업 공간 상태를 보여준다. 별도 MCP 프로세스의 메모리 상태를 공유한다고 가정하지 않는다.
- `refresh` 명령은 없다. 정적 결과를 다시 만들려면 `publish`를 사용하고, Live 결과는 versioned edit 제출 후 current-or-gap 흐름으로 확인한다.
- Semantic Enrichment용 `codeflow model install`, 설정 또는 상태 명령은 v0.4.0에 없다.

## 6. 에이전트 최소 체크리스트

```text
[ ] 사용자가 $codeflow를 명시했는가?
[ ] target을 feature 폴더가 아닌 프로젝트 루트로 지정했는가?
[ ] 요청에 맞는 정적, Live 또는 semantic operation 경로 하나를 선택했는가?
[ ] 정확한 flow, entry symbol, generation, basis와 snapshot 식별자를 보존했는가?
[ ] current를 말하기 전에 current answer와 같은 Generation Proof를 확인했는가?
[ ] Verified Gap, unknown, incomplete를 성공이나 완료로 바꾸지 않았는가?
[ ] 모델 proposal, Approval, 구현 사실과 runtime observation을 구분했는가?
[ ] anchor와 coverage 범위를 넘어 추론하지 않았는가?
[ ] 사용자가 화면을 요청한 경우에만 같은 MCP session에서 FlowView를 열었는가?
```

개발 환경과 캐시 관리는 [개발 가이드](development.md), 전체 기능 예시는 [기능 및 프롬프트 가이드](feature.md), SLM 관련 제한은 [Semantic Enrichment 가이드](semantic-enrichment.md)를 참고한다.
