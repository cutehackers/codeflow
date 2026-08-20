# Domain scenarios v1

## 목적

한 화면의 여러 사용자 행동을 하나의 순차 타임라인으로 읽히게 하지 않고,
사용자가 선택하는 경로별로 도메인 흐름을 확인하게 한다. 예를 들어 회원가입
화면은 이메일, 전화번호, 소셜 계정 가입을 서로 다른 시나리오로 표시한다.

이 계약은 FlowIR의 사실·인과·비교 의미를 바꾸지 않는다. 도메인 문구는
사용자에게 읽기 쉬운 투영이며, 코드 근거를 대신하거나 runtime 결과를 주장하지
않는다.

## 결정적 시나리오 projection

`Document.scenarios[]`는 현재 화면의 `user_action` Fact 하나마다 생성한다.

```text
Scenario
  id                sha256("scenario", flow_id, interaction_fact)
  interaction_fact  resolved user_action Fact ID
  step_ids          그 action에서 인과적으로 도달한 현재 Step ID 순서
  status            observed | mixed | unknown
```

- UI action의 소스 순서는 다른 action의 인과 관계가 아니다.
- 한 scenario의 조건 결과는 기존 `Branch`로 표현한다.
- 공통 helper나 service가 여러 scenario에서 관찰되면, 각 scenario는 같은 Fact와
  evidence를 참조할 수 있다. 근거를 복제하거나 새 사실을 만들지 않는다.
- action이 없는 화면에는 scenario projection을 만들지 않는다.

## 사용자 문구의 출처

기본 제목은 다음 순서를 따른다.

1. 현재 Flutter 위젯에서 직접 읽은 정적 버튼 텍스트 또는 접근성 라벨
2. 현재 FlowIR 사실을 과장 없이 바꾼 중립 문장

명시적으로 승인된 domain label은 위 기본 제목을 대체할 수 있다. 이는 사람이
확정한 독해용 문구일 뿐, LLM의 추론이나 새로운 코드 사실이 아니다.

예를 들어 `isCompleted`는 `이전 작업이 완료되었는지 확인합니다`로 표시한다.
이것은 조건식의 세부 구현을 숨기는 것이 아니라, 기본 화면에서 구현 용어를
도메인 질문으로 바꾸는 것이다. 조건식·상태값·호출 경로는 코드 근거 상세에서
계속 확인할 수 있다.

소스 텍스트가 동적이거나 현재 callback과 직접 연결되지 않으면 action 제목으로
채택하지 않는다. 도메인 의미를 추정한 문구가 필요한 경우에는 승인 label이
필요하다.

## 승인된 domain label

승인 label은 FlowIR 밖의 `.codeflow/knowledge/domain-labels.v1.json`에 저장한다.
각 label은 `flow_id`, `scenario_id`, 선택적인 `step_id`, 제목, 그리고
`confirmed` 상태를 가진다. ID는 이 대상들로 결정된다.

`PUT /api/v1/domain-labels`는 인증된 로컬 reviewer의 명시적 승인 경계다. Core는
저장 전에 대상 flow, scenario, step이 현재 published FlowIR에 존재하는지
검증한다. 코드 변경으로 대상 ID가 달라지면 이전 문구는 더 이상 연결되지 않아
재검토가 필요하다.

승인 label, import된 semantic overlay, LLM 후보는 모두 다음을 변경할 수 없다.

- Fact, CausalEdge, Branch, Step의 ID와 상태
- FlowDelta와 baseline 비교
- unknown의 observed 여부

## FlowView

화면 흐름 지도 아래, 상세 타임라인 위에 `domain-scenarios` 영역을 둔다.
각 card는 사용자 경로의 제목, 단계 수, 신뢰 상태, 미확정 연결 수를 보인다.
선택한 card만 타임라인·아키텍처 지도·인지 부채에 적용한다.

기술 Fact의 이름은 기본 제목으로 사용하지 않는다. `조건 확인`, `state
transition`, `repository access` 같은 구현 표현은 코드 근거 상세에서만 보인다.

## 정적 HTML export

`codeflow export --output REPORT.html`은 현재의 검증된 Basis를 단일 HTML
보고서로 만든다. `--flow`와 `--scenario`로 화면·사용자 경로를 선택할 수 있다.
`GET /api/v1/export`도 같은 렌더를 인증된 로컬 요청에 제공한다.

export는 PR에 첨부할 수 있도록 다음을 보장한다.

- 분석 Basis, 도메인 단계, trust 상태, 코드 근거 조각을 보존한다.
- runtime polling, 인증 토큰, `vscode://` 링크를 포함하지 않는다.
- 정적 파일에서 동작하지 않는 화면·시나리오 탐색 링크는 제공하지 않는다.
- 이미 존재하는 출력 경로는 덮어쓰지 않으며, 동시 생성도 하나만 성공한다.
- export 이후 코드가 바뀌어도 보고서가 현재 코드라고 주장하지 않는다. Basis가
  보고서의 분석 시점을 명시한다.
