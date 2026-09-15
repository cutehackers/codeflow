# FlowView 코드 이해·재분석·변경 비교 최종 스펙

- Contract ID: `FLOWVIEW-CODE-COMPREHENSION`
- Contract Status: Approved
- Intent Status: Hardened
- Independent Review: Passed
- Created: 2026-09-14
- Source: 이 대화에서 확정한 FlowView 중심 개선 방향, 신규 환불 기능 작성 시나리오, 사용자가 요청한 최종 스펙 정리와 기존 Live View 스펙 정리 허용
- Decision Records: [FlowView 개선 방향 결정기록](../decisions/2026-09-14-flowview-code-comprehension-ko.md)
- Glossary: [설계 용어집](../glossary.md)
- Implementation Status: Specification only. 구현·배포·사용자 가치 검증 완료가 아니다.
- Supersedes: 기존 Live View production-quality 스펙과 통합 개선 계획의 제품·실행 방향. 기존 공개 protocol, 저장 데이터와 감사 기록의 삭제를 승인하지 않는다.

## 1. 문제와 목표

개발자는 새 기능을 작성하거나 기존 기능을 수정할 때, 관련 코드가 어디에 있고 어떤 조건과 계층을 거쳐 결과를 만드는지 이해해야 한다. 변경 목록을 먼저 보여주는 것만으로 이 문제를 해결할 수 없다.

최종 제품은 하나의 FlowView다. 현재 구현을 읽는 것이 기본이며, 사용자가 요청하면 다시 분석하고 이전 분석과 비교한다. 선택한 코드의 직접 연결을 따라 다른 흐름을 확인할 수 있다. 독립 Live View의 가치가 입증됐다는 주장은 철회하며 그 전체 구현을 선행하지 않는다.

| Intent | Goal | 관찰 가능한 결과 |
|---|---|---|
| INT-FLOW-01 신규·기존 기능의 현재 구현을 이해한다 | GOAL-FLOW-01 | 시작점부터 조건·계층·실패·결과까지 실제 소스로 따라간다 |
| INT-FLOW-01 | GOAL-FLOW-02 | 작성 중인 기능의 확인된 구간과 분석하지 못한 구간을 구별한다 |
| INT-FLOW-02 코드 변경 후 이해를 갱신한다 | GOAL-FLOW-03 | 수동 재분석과 전후 비교로 바뀐 동작을 확인한다 |
| INT-FLOW-02 | GOAL-FLOW-04 | 근거가 있는 직접 연결을 통해 추가로 확인할 흐름을 찾는다 |

## 2. 범위와 적용 기준

포함 범위는 Requested Flow 탐색, source context, 부분 구현의 정직한 표시, 명시적 재분석, 선택한 두 분석의 비교, 직접 영향 탐색, 사용자가 제공한 요구사항과 코드 근거의 대조다. 기존 FlowView의 단일 template와 공개 query 경계를 재사용한다.

별도 Live 화면, watcher 기반 자동 분석·자동 화면 교체, Change Episode 자동 분류, 실시간 알림 feed, 저장소 전체 영향 추정, 자동 코드 작성·실행, 자동 완성 판정, 신규 CAS layout·SQLite catalog·outbox·GC 전면 재설계는 이 기능의 범위와 선행 조건이 아니다. 기존 기능을 제거하는 코드 변경도 이번 스펙 작성에 포함하지 않는다.

이 계약이 현재 FlowView 제품 방향의 단일 기준이다. 기존 compiler 계약의 Fact/Evidence/proof·보안·schema 호환 불변식과 [FlowView template 계약](../../contracts/flowview-template-v1.md)은 유지한다. 과거 문서의 자동 갱신·near-live 시간 목표와 고정 Live template 의무는 새 FlowView에 적용하지 않는다. 기존 API/URL의 폐기나 의미 변경은 별도 호환성 검토 없이 실행하지 않는다.

## 3. 네 가지 발견의 적용

| 발견 | 제품 요구 | 검증 |
|---|---|---|
| 발견1. 탐색과 맥락 복원 비용 | 선택 statement와 직접 감싸는 조건, callable signature, 연결된 구현을 함께 제공 | FA-02 |
| 발견2. 흐름 이해와 변경 이해의 차이 | 현재 흐름을 기본으로, 전후 비교를 선택 기능으로 제공 | FA-01·FA-06 |
| 발견3. 시각적 구조보다 집중과 근거 | 요청 흐름만 먼저 표시, 전체 함수·추가 관계는 명시적 확장 | FA-02·FA-09 |
| 발견4. 기존 지식과 읽기 맥락의 영향 | 재분석 중 선택·읽기 위치·기준 유지, 실패 시 이전 화면 보존 | FA-04·FA-05 |

이 발견은 별도 Live 제품의 필요성이나 이해도 향상을 입증하지 않는다. 실제 효과는 §11에서 검증한다.

## 4. 사용자 시나리오

### 4.1 신규 기능을 작성한다

사용자가 “지금 작성한 환불 흐름을 보여줘”라고 요청한다. 이전 분석이 없어도 환불 요청→주문 조회→환불 가능 여부 검사처럼 확인된 구현을 표시한다. 이후 연결이 확인되지 않으면 해당 위치와 이유를 보여준다. 분석 실패를 미구현으로 표시하지 않는다.

결제 취소와 상태 저장을 추가한 뒤 사용자가 ‘다시 분석’을 누른다. 새 snapshot의 확인된 흐름을 보여준다. 이전 분석을 기준으로 선택하면 추가된 처리와 달라진 실패 경로도 비교할 수 있다. 처음부터 전체 저장소를 추가된 기능으로 표시하지 않는다.

“부분 환불과 중복 환불 방지가 필요한데 어디에 구현됐는가”라고 요청하면 각 요구사항의 관련 코드와 확인하지 못한 부분을 제시한다. 연결된 흐름만으로 해당 요구사항 충족이나 기능 완성을 선언하지 않는다.

### 4.2 기존 기능을 수정한다

사용자가 주문→결제 완료 흐름을 읽고 수정 전 분석을 비교 기준으로 선택한다. 코드 수정 뒤 ‘변경 전후 비교’를 요청한다. 시스템은 그 기준과 요청 시점의 새 snapshot을 분석해 조건·호출·상태 처리의 차이와 전후 코드를 보여준다.

바뀐 주문 상태를 사용하는 직접 연결을 선택하면 같은 snapshot의 관련 흐름으로 이동한다. 돌아왔을 때 원래 비교와 읽기 위치를 복원한다. 미확인 연결은 가능한 실제 영향으로 단정하지 않는다.

## 5. 명령과 화면 상태 계약

### 5.1 입력과 고정 기준

primary actor는 로컬 workspace 접근 권한이 있는 개발자다. CLI·MCP 호출은 사용자 요청을 전달할 수 있지만 코드 편집 event 자체는 분석 요청이 아니다. 모든 요청은 authorized repository/worktree, 요청 목적, target 또는 시작점 후보, capability/configuration과 immutable snapshot에 결합한다. 분석 중 live filesystem을 다시 읽어 입력을 보충하지 않는다.

| 사용자 동작 | 실행과 결과 |
|---|---|
| ‘흐름 보기’ | 목적과 entry를 해석한다. 후보가 여러 개면 선택을 요청하고, 하나로 확인되면 요청 시 snapshot을 분석한다 |
| ‘다시 분석’ | 같은 요청의 새 snapshot을 한 번 분석한다. 비교 기준은 자동 변경하지 않는다 |
| ‘비교 기준으로 선택’ | 실제 보존된 분석 ID를 baseline으로 고정한다. 이후 새 분석 성공이 baseline을 전진시키지 않는다 |
| ‘변경 전후 비교’ | 선택한 baseline과 요청 시 새 snapshot 또는 사용자가 선택한 보존 분석을 비교한다. 현재 면의 선택 방식과 시점을 화면에 명시한다 |
| ‘연결된 흐름 보기’ | 선택 관계의 before/after snapshot을 유지해 대상 흐름을 연다. snapshot을 최신으로 바꾸려면 별도 재분석을 요청한다 |
| 요구사항 대조 | 사용자가 명시한 요구사항과 같은 snapshot의 코드 근거를 연결한다. 별도 완성도 점수를 생성하지 않는다 |

비교 기준이 없으면 ‘비교 기준 없음’을 표시하고 보존 분석 선택을 제공한다. 사용자가 현재 분석을 기준으로 삼을 수는 있지만 이전 시점으로 위장하지 않는다. 임의 Git revision import, 무제한 역사 보존은 이번 범위가 아니다. 삭제된 전체 흐름은 유효한 baseline과 current 분석 범위의 부재 증거가 있을 때 제거로 표현하며 동일 entry의 존재를 비교의 필수 조건으로 요구하지 않는다.

### 5.2 갱신과 동시 요청

화면 상태는 표시 중인 analysis ID, 선택된 flow/step, source 면과 range 내 offset, 펼침 상태, baseline ID, 진행 중 request ID를 가진다. source 위치는 해당 snapshot에 결합한다. 내부 ID는 기본 화면에 표시하지 않는다.

명시적 요청 자체가 그 결과로 전환할 의사다. 따라서 ‘다시 분석’ 또는 ‘변경 전후 비교’ 성공 뒤 추가 ‘새 변경 보기’ 클릭을 요구하지 않는다. 이전 Live 설계의 버튼은 폐기하지만 사용자 요청 없이 읽던 화면을 바꾸지 않는 결정은 유지한다.

분석 중에는 기존 화면을 유지하고 진행 상태와 취소를 제공한다. 표시할 source·국소 Evidence·coverage와 저장 무결성을 검증한 결과를 완전히 구성한 뒤 한 번에 교체한다. 이 표시 검증은 전체 current publication gate 통과와 별개다. 검증된 일대일 step 대응이 있으면 선택·읽기 위치를 이전한다. 선택한 step이 삭제되거나 대응이 불명확하면 이전 source와 이유를 남기며 임의 항목으로 선택을 이동하지 않는다. 키보드 focus와 펼침 상태를 보존한다.

request ID의 최신 사용자 요청만 화면을 바꿀 수 있다. 늦게 끝난 이전 요청과 취소된 요청은 화면·baseline을 변경하지 않는다. 같은 request ID 재전송은 같은 고정 입력과 결과를 재사용하며 다른 입력을 붙인 재전송은 conflict다. 사용자가 다시 누른 새 요청은 새 snapshot을 수집한다. 분석 후 코드가 바뀌었더라도 결과는 분석 시점의 코드라고 표시하며 현재 disk와 계속 같다고 주장하지 않는다.

## 6. 흐름·변경·영향 판정

### 6.1 현재 구현과 부분 결과

Requested Flow는 entry부터 확인된 처리 완료까지 architecture layer 통과, 조건, 비동기 후속 처리와 실패 결과를 포함한다. 관련 없는 내부 문장은 상세 확장에서 보여준다. 실제 종료 지점과 분석이 중단된 지점을 구분한다.

| 상태 | 허용 표현 | 금지 |
|---|---|---|
| source와 연결 검증 성공 | 구현된 처리, 조건과 가능한 경로 | 정적 분석만으로 실제 실행됐다고 표현 |
| 선언 scope에서 target/연결 부재를 확인 | 해당 범위에서 구현/연결을 찾지 못함 | 저장소 전체 미구현 또는 요구사항 불충족 단정 |
| syntax error, adapter 미지원, dynamic dispatch, 누락 source | 분석 불가 또는 연결 미확인과 이유 | 미구현·처리 완료·영향 없음으로 대체 |
| 일부만 검증 | 근거가 있는 구간과 명시적인 중단 경계 | 누락 부분을 추정 연결하거나 전체 verified로 승격 |

partial 결과는 정직한 조회 결과이며 current generation 승격을 뜻하지 않는다. source·국소 Evidence·coverage 및 저장 무결성을 통과한 partial candidate는 최초 조회에서도, 기존 complete 결과에서 명시적으로 재분석한 경우에도 화면에 표시한다. 미검증 범위와 중단 이유를 함께 표시한다. 전체 proof gate가 거절되면 shared verified generation은 이전 값으로 유지하며, 화면은 별도 immutable candidate identity로 조회한다. candidate 저장이나 표시 검증이 실패하면 기존 화면을 유지한다. 최초 결과조차 없으면 오류 상태를 표시한다.

요구사항 대조에는 requirement text/revision, 관련 step/Evidence, 확인한 범위와 미확인 이유를 포함한다. 근거 발견·검증 실패·범위 내 미발견을 구별하고, 테스트나 실제 실행 없이 business outcome 또는 기능 완성을 선언하지 않는다.

### 6.2 비교 판정

baseline/current는 같은 repository/worktree lineage와 호환된 query 목적·capability·configuration에서 비교한다. 호환하지 않으면 incomparable을 반환한다. 각 면의 source와 Evidence는 그 면의 snapshot에서 읽는다.

formatting/comment만 바뀌면 구조 또는 근거 위치 변화로 표시한다. 실행 statement 추가·삭제, predicate·branch 변경, state/effect·호출 대상 변경을 구별한다. 삭제는 이전 존재와 현재 분석 scope의 부재를 모두 증명해야 한다. syntax error를 삭제로 취급하지 않는다.

내부 rename/move는 일대일 대응과 실행 facts·외부 binding 동일성이 확인될 때만 구조 변화다. export 이름·route·serialization field 변경은 관련 계약/관계 변화다. 대응 후보가 여럿이면 ambiguous로 남긴다. 사용자 summary는 검증된 차이를 설명하고 추측을 사실로 승격하지 않는다.

두 분석이 있어도 필요한 근거가 누락되면 검증된 의미 비교는 제공하지 않는다. 사용자가 각 partial 흐름을 읽을 수는 있지만 이를 ‘변경 없음’으로 표시하지 않는다.

### 6.3 직접 연결 탐색

기본 범위는 선택한 symbol의 검증된 직접 caller/callee, state read/write 및 명시적인 callback/event 연결이다. 다른 흐름으로 연결할 수 있을 때 사용자가 선택해 이동한다. 연결만 확인되고 전체 흐름을 찾지 못하면 그 source와 경계를 제공한다.

추가 단계는 사용자가 다시 선택할 때 탐색한다. 저장소 전체 전이 영향 분석을 자동 실행하지 않는다. 삭제 관계는 baseline, 추가 관계는 current, 변경 관계는 양쪽 endpoint를 각각 확인한다. 서로 다른 snapshot의 edge를 연결하지 않는다. 정적 연결은 실제 장애/변경 파급의 증거가 아니다. ‘영향 없음’은 확인한 범위 안에서만 사용한다.

## 7. 데이터와 architecture 경계

| 논리 결과 | 필수 내용과 불변식 |
|---|---|
| Analysis result | request/analysis ID, workspace·snapshot·query·capability identity, flow/step/relations, Evidence, coverage, complete/partial 분석 상태, proof 또는 rejection 이유 |
| Comparison result | comparison ID, baseline/current analysis refs, 대응 근거, change 종류, 전후 Evidence, coverage와 unknown. 각 면의 source·Evidence는 해당 면의 analysis/snapshot identity와 일치해야 함. baseline/current snapshot은 달라도 됨 |
| Direct relation result | 출발 step, 관계 종류, 상대 symbol, snapshot과 source 근거, 확인 범위·중단 이유 |
| Requirement evidence | 사용자 요구사항과 revision, 관련 source refs, 확인 결과와 한계. 구현 완료 판정과 분리 |
| View state | 표시 analysis/comparison, 선택과 reading position, baseline, 펼침 상태, 최신 request ID. 분석 authority로 사용하지 않음 |

이는 논리 계약이며 기존 public schema 이름/필드의 변경 지시가 아니다. 현재 strict schema가 표현하지 못하면 별도 version/capability를 등록하고 producer·consumer·valid/invalid fixture를 함께 추가한다. 기존 schema ID, enum, `cas:sha256:<hex>`, URL과 호환 fixture를 보존한다. 기존 endpoint를 새 mode에 맞게 조용히 재해석하지 않는다.

처리는 요청→권한·입력 검증→immutable capture와 보호→구조/flow 분석→Evidence·coverage 검증→저장 또는 명시적 실패→요청에 결합한 화면 결과 순이다. 비교는 두 분석에 같은 검증 규칙을 적용한다. CLI·MCP·HTTP의 분석·proof 규칙을 별도로 복제하지 않는다.

기존 compiler·snapshot·proof·storage 경계를 재사용하고, 이 기능 때문에 전체 package를 재배치하지 않는다. 필요한 연결 수정만 수행하며 기존 shared publication/proof 검증을 우회하지 않는다. 새로운 SQLite catalog, 별도 event outbox, nine slice 분할은 필수 architecture가 아니다.

## 8. 저장·보안·실패 계약

표시 중인 분석과 선택된 baseline의 source·Evidence·proof는 읽는 동안 GC에서 보호한다. 비교 요청은 양쪽 입력을 먼저 보호한 뒤 분석한다. 새 결과가 저장되고 표시 참조가 교체된 뒤 이전 요청의 보호를 해제한다. 기존 보호 기능이 이를 충족하지 못하면 그 결함을 비교 기능의 선행 수정으로 처리한다. 전체 CAS 재설계를 자동으로 요구하지 않는다.

보존 분석 목록은 실제 읽을 수 있는 결과만 제공한다. reference 누락·손상·만료는 unavailable이며 현재 disk bytes로 바꿔치기하지 않는다. 재시작 시 복구 가능한 보존 결과는 분석 시점 상태로 열고, 최신 여부 확인에는 재분석이 필요하다. 무제한 보존이나 이전 결과 자동 삭제를 도입하지 않는다.

모든 source/persistence/egress는 `internal/secret`과 canonical path 정책을 따른다. 마스킹이 원본 bytes를 바꾸는 source는 원문 저장이나 원본인 척하는 분석으로 우회하지 않고 policy gap으로 처리한다. 기존 legacy 읽기 호환, audit 보존, 소스 read-only와 Host/Origin·인증 보호를 유지한다.

| 실패 | 결과와 보존 |
|---|---|
| 시작점 후보가 여러 개/없음 | 후보 선택 또는 범위 내 미발견. 가짜 흐름 생성 없음 |
| 분석 timeout·취소·worker 실패 | 이전 화면·baseline 유지. bounded 종료와 안전한 재시도 |
| 분석 중 source 변경 | 수집된 일관된 snapshot만 사용. 혼합 capture는 재수집하거나 실패 |
| 저장 실패·disk full | 새 결과 성공/표시 없음, 기존 참조와 source 보존 |
| 비교 불가·근거 누락 | 기존 비교 유지, 원인과 가능한 복구 동작 표시 |
| 늦은 응답·중복 요청 | request identity 검증, 화면 후퇴와 중복 side effect 없음 |
| source 삭제·lease 만료·GC 경합 | 보호된 읽기 유지 또는 명시적 unavailable, live-disk fallback 없음 |
| 비인가 경로·secret·HTML source | 접근 거절/정책 gap, 원문 누출·코드 실행 없음 |

resource limit·deadline·worker 정리는 기존 설정을 사용하고 무제한 작업을 허용하지 않는다. 필요한 변경값은 적용 근거와 테스트를 기록한다. 내장 runtime 실행이나 새로운 원격 model 전송은 이 스펙이 승인하지 않는다.

## 9. 확인된 근거와 가정

기존 `internal/flowview/impact_endpoint.go`의 `handleTaskImpact`·`resolveImpactProof`는 identity와 capability/coverage를 검사한다. historical 경로는 cached map에 의존하므로 보존된 이전 분석의 근거 조회가 새 요구를 충족하는지 검증해야 한다. 기존 기능의 존재를 새 UX의 완료 근거로 삼지 않는다.

`ComputeSemanticDelta`, `handleTaskView`, 기존 contractharness와 FlowView template는 재사용 조사 대상이다. 실제 구현 전 producer/consumer, 변경할 공개 경계, snapshot 보존과 partial 결과 경로를 확인한다.

- ASM-01: 현재 adapter가 적어도 지원 corpus에서 flow·source·직접 관계를 추출한다. 틀리면 해당 capability를 미지원으로 제한하며 실제 adapter 테스트로 확인한다.
- ASM-02: 기존 저장 경계에 두 분석을 보호할 수 있는 기능이 있거나 범위가 제한된 수정으로 가능하다. 틀리면 비교 단계를 차단하고 결함·수정 범위를 먼저 보고한다.
- ASM-03: 요청 시 비교가 기존 Git diff와 FlowView 조합보다 유용할 수 있다. 이는 가설이며 §11의 실제 사용자 과제로 확인한다. 실패하면 비교·영향 기능 확장을 중단한다.

## 10. 기능 수용 기준

| ID | 상황 | 반드시 관찰할 결과 |
|---|---|---|
| FA-01 | 신규 기능, 이전 분석 없음 | 현재 확인된 흐름을 표시, 비교 기준 없음 명시, 전체 코드 추가/변경 없음으로 위장하지 않음 |
| FA-02 | 정상·분기·비동기·실패 경로 조회 | 계층 간 연결과 실제 source 맥락, 처리 종료와 분석 중단 구분 |
| FA-03 | 최초 partial 조회와 complete→partial 재분석 | 표시 검증·저장 성공 시 candidate 화면 표시, shared verified generation은 승격하지 않음, 분석 실패를 미구현으로 단정하지 않음 |
| FA-04 | 편집 event만 발생 | 분석 실행·화면 교체·baseline 이동 없음 |
| FA-05 | 재분석 성공/실패/취소/응답 역전 | 표시 검증·저장에 성공한 complete/partial 요청 결과만 전환, 실패는 이전 상태 유지, 삭제/대응 불명 선택 처리 |
| FA-06 | baseline 선택 후 전후 비교 | 선택된 두 시점만 비교, baseline 자동 이동 없음, 각 면의 source identity가 그 면의 snapshot과 일치 |
| FA-07 | formatting·조건 반전·호출 변경·내부/외부 rename | §6.2대로 분류, 불명확 대응은 unknown |
| FA-08 | 비교 대상 전체 흐름 삭제 또는 current parse 실패 | 부재 검증 시 제거, parse 실패는 미확인. 두 경우 구별 |
| FA-09 | 직접 연결 탐색과 복귀 | 같은 면/snapshot 연결만 제공, 전체 영향 확정 없음, 원래 읽기 상태 복원 |
| FA-10 | 요구사항 대조 | requirement revision과 코드 근거·미확인 범위 제공, 완성/테스트 통과 자동 선언 없음 |
| FA-11 | GC·재시작·누락 ref·disk full | 열린 분석·baseline 보호, 안전한 unavailable/실패, 원문 바꿔치기 없음 |
| FA-12 | 비인가·secret·HTML 입력 | source read-only·redaction·경로/인증 정책 유지, 임의 코드 실행 없음 |
| FA-13 | 기존 strict schema/CLI/MCP 소비자 | 기존 payload 계약 보존, 새 기능은 명시적 version/capability, 같은 proof 규칙 |
| FA-14 | 실제 사용자 이해 과제 | 시간·정확도·재탐색·오해를 §11 방식으로 측정, 기술 테스트를 가치 입증으로 대체하지 않음 |

## 11. 실행 순서와 검증

| 단계 | 전달할 결과 | 진행 조건 |
|---|---|---|
| 단계1 | 신규·기존 기능의 현재 흐름과 부분 결과 이해, 수동 재분석 | FA-01–05·10–13 통과, 실제 source 탐색 과제 결과 기록 |
| 단계2 | 요청 시 전후 비교 | 단계1 확보, 두 분석 보존/조회 선행 검증, FA-06–08·11·13 통과 |
| 단계3 | 직접 연결에서 다른 흐름 탐색 | 단계2의 사용자 가치 확인, FA-09 및 기존 안전성 회귀 통과 |

후속 사용자 요청에 따라 [vertical slice 실행 목록](../../../.tasks/2026-09-14-flowview-code-comprehension-ko/README.md)과 다섯 child 계약으로 분해했다. child는 Proposed이며 독립 검수와 명시적 사용자 승인 뒤에만 구현한다. 구현 전 각 단계의 변경 파일과 실제 test 경계를 확정한다. naming 규칙대로 계약 ID를 구현 식별자로 복사하지 않는다.

알려진 Go 검증 명령은 `make fmt`, `make check-naming`, `make vet`, `make test`다. shared snapshot/storage/query 변경 시 `go test -race ./internal/...`와 실제 다중 요청·GC 경합 테스트를 추가한다. schema·핵심 경계는 `go test ./internal/contractharness ./internal/semantic ./internal/flowview ./internal/workspace ./internal/storage ./internal/secret`로 확인한다. adapter/browser 검증 명령은 구현 전 현재 repository에서 확인하고 기록한다. fake adapter만으로 실제 언어 지원을 주장하지 않는다.

UI는 실제 제품 template에서 신규 기능·삭제된 흐름·긴 source·키보드 이동·실패·응답 역전·비교 왕복을 검증한다. 코드 없음 상태에서 invented preview를 제품 증거로 쓰지 않는다. 각 FA에 실제 입력, expected/actual, command, source revision과 환경을 연결한다.

사용자 평가는 기존 FlowView+Git diff와 개선된 FlowView를 비교한다. 신규 기능 읽기, 기존 흐름 수정, 부분 구현 확인 과제를 포함하며 프로젝트 경험에 따라 결과를 나눈다. 정확한 설명까지 걸린 총시간, 정답률, 재탐색 횟수와 잘못된 확신을 측정한다. 평가 전 corpus·정답 rubric·참여자 조건·순서·합격 기준을 기록한다. 숫자 개선율과 응답시간을 실측 없이 발명하지 않는다. near-live 5초 목표는 승계하지 않는다.

개선 효과가 작으면 다음 단계로 확장하지 않는다. 합격 기준이나 실제 평가 증거가 없으면 기술 구현 완료와 별개로 사용자 가치/production 준비를 주장하지 않는다. 공개 보안·호환성·보존 검증 실패는 항상 전달을 차단한다.

## 12. 결정, 미결정과 완료

결정1은 독립 Live View 대신 하나의 FlowView를 개선하는 것, 결정2는 신규 기능에 baseline을 요구하지 않는 것, 결정3은 명시적 재분석·비교 요청 성공 시 전환하는 것, 결정4는 직접 연결과 요구사항 근거까지만 제공하는 것, 결정5는 기존 저장 안전성을 유지하되 Live 전면 architecture를 선행하지 않는 것이다. 근거·대안·영향은 연결된 결정기록에 남긴다.

이 대화에서 actor·신규/기존 시나리오·범위·전환 의도·비교와 근거의 한계가 확인되었다. 저장·보안·호환성은 기존 보호를 유지하는 것으로 제한했다. 차단하는 제품 미결정은 없다. 지원 corpus와 수치 평가는 구현 단계에서 사전 고정할 검증 산출물이며 누락 시 가치/출시 판정은 통과할 수 없다.

완료는 해당 단계의 수용 기준과 필수 검증 통과, 같은 snapshot의 소스와 근거 보존, 공개 호환성 유지, 결과의 한계를 명확히 표시하는 상태다. 이 문서의 Approved는 parent 계약의 확정을 뜻하며 코드 구현·효과 입증·배포 승인이 아니다.
