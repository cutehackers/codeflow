# CodeFlow 기능 및 프롬프트 가이드

이 문서는 사용자가 CodeFlow에 무엇을 요청할 수 있고, AI 에이전트가 CodeFlow MCP를 통해 어떤 결과를 제공하는지 설명한다. CodeFlow skill은 자동 호출되지 않으므로 모든 프롬프트에서 `$codeflow`를 명시한다.

## 1. 사용 전 준비

CodeFlow의 전체 기능을 사용하려면 Core, 언어 adapter, MCP, CodeFlow skill이 설치되어 있어야 한다.

```sh
curl -fsSL https://raw.githubusercontent.com/cutehackers/codeflow/main/scripts/install.sh | bash
```

설치 후 AI 에이전트 앱을 재시작하거나 새 task를 열고 프로젝트 루트를 점검한다.

```sh
$HOME/.local/bin/codeflow doctor <프로젝트-루트>
```

`Workspace not initialized`가 표시되면 한 번만 초기화한다.

```sh
$HOME/.local/bin/codeflow init <프로젝트-루트>
```

항상 `pubspec.yaml`, `package.json`처럼 프로젝트 종류를 식별할 수 있는 파일이 있는 프로젝트 루트를 대상으로 사용한다. 특정 feature 디렉터리만 target으로 전달하지 않는다.

## 2. 기존 기능의 전체 흐름 이해

CodeFlow는 UI 이벤트, API 요청, 시스템 이벤트와 같은 시작점부터 controller, use case, domain, data, infra, external 계층을 거쳐 처리가 끝나는 지점까지 핵심 흐름을 추적한다. 각 단계는 실제 코드 위치와 검증된 anchor에 연결된다.

### 프롬프트 예제

```text
$codeflow 이 프로젝트의 이메일 회원가입 기능이 시작부터 완료까지 어떤 코드 경로로 실행되는지 분석해줘.
```

```text
$codeflow 장바구니에서 결제 버튼을 누른 뒤 주문이 저장되고 외부 결제 서비스가 호출될 때까지의 전체 코드 흐름을 보여줘.
```

```text
$codeflow 로그인 화면의 제출 처리 함수에서 시작해 상태가 어떻게 바뀌고 어떤 조건과 데이터 저장 코드를 거치는지 분석해줘.
```

```text
$codeflow 이 코드 흐름을 검토 화면으로 열어줘.
```

### 제공 결과

- 후보 흐름과 정확한 진입점 탐색
- 특정 entry symbol의 즉시 분석
- 아키텍처 계층을 관통하는 Core Flow 발행
- 상태 변화, 조건 분기, 외부 효과와 unresolved boundary 설명
- 검증된 source anchor가 포함된 Static FlowView
- 기존에 발행된 흐름의 상세 payload 조회

Static FlowView는 캡처한 시점의 코드 구조를 설명한다. 이후 코드가 변경됐을 때도 최신임을 보장하려면 Live Semantic Map을 사용한다.

## 3. Live Semantic Map

Live Semantic Map은 사용자가 요청한 기능을 기준으로 코드 편집을 받아 다시 분석하고, 최신 결과를 `current`로 검증하거나 최신 코드와 마지막 검증 결과 사이의 `Verified Gap`을 보여준다.

### 시작 프롬프트

```text
$codeflow 결제 처리 기능을 코드 수정과 함께 계속 분석해줘. 관련 코드가 바뀔 때마다 설명도 최신 상태로 갱신해줘.
```

```text
$codeflow 박테리아 워에서 사용자가 칸을 선택할 때 실행되는 코드 흐름을 분석하고, 이후 코드 수정이 설명에 반영됐는지 계속 확인해줘.
```

### 에이전트가 수행하는 과정

1. 사용자의 기능 질문을 Task View에 등록한다.
2. 요청한 경우 같은 MCP coordinator의 FlowView를 연다.
3. 실제 코드 변경을 완료한다.
4. 변경된 각 문서의 전체 내용을 versioned edit로 semantic snapshot에 제출한다.
5. 약 2초의 편집 병합 구간 뒤 workspace 상태를 확인한다.
6. 최신 결과가 검증되면 current answer와 Generation Proof를 반환한다.
7. 검증할 수 없으면 마지막 검증 결과를 유지하고 Verified Gap의 원인을 반환한다.

### 진행상태 확인 프롬프트

```text
$codeflow 방금 수정한 코드가 기능 설명과 코드 흐름에 반영됐는지 확인해줘.
```

```text
$codeflow 현재 분석이 진행 중인지, 반영을 기다리는 변경이 몇 개인지, 결과가 얼마나 지연되고 있는지 알려줘.
```

```text
$codeflow 분석 결과가 최신 코드까지 검증됐다면 그 근거를 보여주고, 아직 최신이라고 확정할 수 없다면 확인이 필요한 부분과 원인을 설명해줘.
```

### FlowView에서 확인할 항목

- SSE 연결 상태
- workspace activity: `editing`, `reconciling`, `analyzing`, `idle`
- workspace epoch
- pending revisions
- analysis lag
- 검증된 current result와 Generation Proof
- affected scope와 원인을 포함한 Verified Gap

### 현재 편집 연동 범위

현재 일반 편집기의 파일 저장만으로는 변경 내용이 live compiler에 자동 전달되지 않는다. AI 에이전트, IDE integration 또는 watcher integration이 CodeFlow의 versioned edit 입력을 호출해야 한다.

MCP 기반 live session에서는 별도의 CLI `codeflow view` 프로세스를 실행하지 않는다. Task View, edit 제출, 상태 확인과 FlowView 열기를 같은 MCP 서버와 같은 프로젝트 target에서 수행해야 동일한 live coordinator를 사용한다.

## 4. 변경 전후의 동작 비교

두 generation 또는 basis 사이에서 코드 줄 차이가 아니라 사용자 동작의 의미가 어떻게 달라졌는지 비교한다.

### 프롬프트 예제

```text
$codeflow 이번 수정 전과 후에 로그인 실패 처리 동작이 어떻게 달라졌는지 비교해줘.
```

```text
$codeflow 이전 분석 결과와 현재 분석 결과를 비교해서 추가·변경·제거된 동작과 근거만 달라진 부분을 구분해줘.
```

### 제공 결과

- 추가된 동작
- 변경된 동작
- 제거된 동작
- 동작은 같지만 근거가 달라진 항목
- 서로 다른 workspace epoch라 직접 비교할 수 없는 경우의 명시적 제한

## 5. 요구사항 충족 여부 확인

요구사항을 구현 설명과 단순히 대조하지 않고, current proof와 검증된 Evidence를 기준으로 확인한다.

### 프롬프트 예제

```text
$codeflow 이번 구현이 회원 탈퇴 요구사항을 모두 충족하는지 코드 근거와 함께 확인해줘.
```

```text
$codeflow 각 완료 조건을 충족, 일부 충족, 확인되지 않음, 근거 충돌, 판단 불가로 구분해줘.
```

CodeFlow는 근거가 충분한 항목만 `confirmed`로 표시한다. 확인되지 않은 항목을 구현 완료로 승격하지 않는다.

## 6. 변경 영향 분석

특정 symbol 또는 변경 묶음에서 시작해 직접 호출부와 제한된 범위의 간접 영향을 추적한다.

### 프롬프트 예제

```text
$codeflow PaymentService.capture 결제 승인 함수의 변경이 어떤 호출 코드, 상태, 외부 서비스와 테스트에 영향을 주는지 분석해줘.
```

```text
$codeflow 이번 변경이 직접 영향을 주는 부분과 연쇄적으로 영향을 줄 수 있는 부분을 구분하고, 아직 확인하지 못한 범위도 알려줘.
```

### 제공 결과

- 직접 호출 관계
- 제한된 깊이의 간접 호출 관계
- override와 instance 생성 관계
- 상태 변경과 외부 효과
- 관련 테스트와 관련 흐름
- 분석 범위를 벗어난 unknown frontier

분석 깊이와 노드 수 제한은 결과의 coverage 범위다. 목록에 없다는 이유만으로 영향이 없다고 단정하지 않는다.

## 7. 오류와 incident 조사

정확한 semantic basis와 failure evidence를 사용해 오류가 발생할 수 있는 경로를 재구성한다.

### 프롬프트 예제

```text
$codeflow 이 결제 시간 초과 오류가 어떤 코드 경로를 통해 발생할 수 있는지 조사해줘.
```

```text
$codeflow 이 장애 추적 기록을 현재 코드 흐름과 연결해서 실패 지점과 오류가 전달되는 경로를 보여줘.
```

```text
$codeflow 코드만 보고 발생 가능하다고 판단한 경로와 실제 실행에서 확인된 사실을 구분해줘.
```

CodeFlow는 다음을 서로 다른 근거로 유지한다.

- 정적 코드에서 도달 가능한 실패 경로
- 결정론적으로 재현한 결과
- 신뢰된 runtime observation에서 실제로 관찰된 결과

로컬 실행이 필요한 조사는 사용자가 정확한 실행 작업을 승인한 경우에만 수행한다.

## 8. 선택적 Semantic Labeling과 Evidence

Semantic Labeling은 검증된 deterministic 분석에 사람이 읽기 쉬운 후보 라벨을 추가한다. SLM이 없거나 실패해도 기본 분석 결과는 유지된다.

### 프롬프트 예제

```text
$codeflow 현재 검증된 코드 흐름을 바탕으로 각 단계가 하는 일을 더 이해하기 쉽게 설명해줘.
```

```text
$codeflow 이 설명이 어떤 코드 구조, 호출 관계와 테스트를 근거로 작성됐는지 보여줘.
```

모델 설명은 구현 Fact가 아니며, exact generation, basis, target, model revision과 Evidence Pack에 연결된 proposal로 취급한다.

## 9. Semantic Approval

사용자가 명시적으로 요청하면 저장된 semantic proposal과 Evidence Pack을 검토해 승인 lifecycle을 기록할 수 있다.

### 프롬프트 예제

```text
$codeflow 자동으로 생성된 기능 설명 후보와 그 근거를 보여줘. 확인한 뒤 승인 여부를 결정하겠다.
```

```text
$codeflow 이 기능 설명을 내가 수정한 문구로 교체한 뒤 승인해줘.
```

```text
$codeflow 이전에 승인한 기능 설명을 취소하고 지금까지의 승인 이력을 보여줘.
```

지원 decision은 approve, edit-then-approve, reject, revoke, supersede다. 단순히 긍정적인 말을 했다는 이유로 approval을 생성하지 않는다.

Semantic Approval은 설명에 대한 신뢰 기록이다. 코드 구현 Fact, freshness, settlement, 요구사항 충족 여부 또는 runtime observation을 변경하지 않는다.

## 10. 프로젝트 탐색과 onboarding

처음 보는 저장소에서 증거가 있는 domain과 대표 흐름부터 단계적으로 살펴본다.

### 프롬프트 예제

```text
$codeflow 이 프로젝트가 어떤 업무 영역으로 나뉘는지 코드에서 확인되는 범위만 설명해줘.
```

```text
$codeflow 결제 업무 영역의 대표적인 코드 흐름을 보여주고 아직 분석되지 않은 범위를 알려줘.
```

현재 상태로 설명하려면 current proof가 필요하다. 과거 상태를 탐색할 때는 정확한 generation, basis와 snapshot을 사용한다.

## 11. 릴리즈 capability 평가

수집된 benchmark, invariant 검증, 승인된 threshold와 실행 근거를 사용해 capability별 릴리즈 준비 상태를 평가한다.

### 프롬프트 예제

```text
$codeflow v0.4.0을 배포할 준비가 됐는지 수집된 검증 결과를 평가하고 완료, 미완료, 배포 차단 항목을 구분해줘.
```

```text
$codeflow 코드 변경 감지 시간과 최신 분석 결과를 표시하는 시간이 승인된 성능 기준을 충족하는지 확인해줘.
```

릴리즈 평가 기능은 제공된 근거를 평가한다. benchmark를 대신 실행하거나, 누락된 수치를 만들거나, threshold를 자동 승인하지 않는다. 근거가 누락되면 `incomplete`, hard invariant가 실패하면 해당 capability는 차단된다.

## 12. MCP 기능 대응표

다음 표는 사용자 기능과 이를 수행하는 MCP 작업을 연결한다. 사용자는 도구 이름을 직접 지정할 필요가 없다.

| 사용자 목적 | MCP 작업 |
|---|---|
| 후보 흐름 찾기 | `harvest_flows` |
| 특정 진입점 즉시 분석 | `analyze_flow` |
| agent가 구성한 Core Flow 검증·발행 | `publish_core_flow` |
| 발행된 흐름 상세 조회 | `get_flow_payload` |
| 구조화된 flow draft 제출 | `submit_flow_draft` |
| Static FlowView 단계 이름·규칙 승인 | `approve_step` |
| 확인하지 못한 경계 조회 | `report_unknowns` |
| FlowView 열기 | `open_review` |
| 기능·review·impact·debug·incident 질의 | `query_task_view` |
| 최신 current answer 확인 | `get_current_answer` |
| live 분석 진행상태 확인 | `get_workspace_activity` |
| versioned edit 제출 (선택 fast-path, live 실행 중 디스크 저장만으로 충분) | `submit_versioned_edit` |
| current Generation Proof 확인 | `get_generation_proof` |
| latest-vs-verified gap 확인 | `get_verified_gap` |
| generation 간 의미 변화 비교 | `get_semantic_delta` |
| 요구사항 충족 상태 확인 | `get_requirement_alignment` |
| 직접·간접 변경 영향 분석 | `get_change_impact` |
| failure와 incident 경로 조사 | `investigate_failure` |
| 선택적 FlowSequence 라벨 요청 | `POST /api/semantic/labels` |
| 검증·비식별 처리된 근거 조회 | `get_evidence_pack` |
| semantic approval lifecycle 변경 | `submit_semantic_approval` |
| semantic approval 이력 조회 | `get_semantic_approval_history` |
| 프로젝트 domain과 대표 흐름 탐색 | `explore_project_domains` |
| 수집된 릴리즈 근거 평가 | `validate_release_capability` |

## 13. 결과를 해석하는 기준

- `current`: 현재 workspace snapshot과 정확히 일치하는 검증 결과
- `historical`: 정확한 과거 generation에 속한 결과
- `candidate`: 검증이나 publication authority가 아직 없는 후보
- `Verified Gap`: 최신 변경과 마지막 검증 결과 사이의 차이가 존재하며 current로 승격할 수 없는 상태
- `settlement=pending`: 현재 코드를 설명할 수는 있지만 요구된 검증 작업이 아직 끝나지 않은 상태
- `unknown`: 근거 또는 관찰 범위가 부족해 판단할 수 없는 상태

모델 설명, 사용자 approval 또는 agent의 완료 보고만으로 다른 상태가 `current`, `confirmed`, `settled` 또는 `runtime observed`로 바뀌지 않는다.
