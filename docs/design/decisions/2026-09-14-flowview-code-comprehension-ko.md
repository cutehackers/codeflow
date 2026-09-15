# FlowView 개선 방향 결정기록

- Record Status: Accepted product direction
- Created: 2026-09-14
- Parent Contract: [FlowView 코드 이해 스펙](../specs/2026-09-14-flowview-code-comprehension-ko.md)
- Source: 사용자의 FlowView 중심 개선 동의, 신규 기능 시나리오 지적과 최종 스펙 작성 요청
- Scope: 제품·문서 방향 확정. 코드 삭제, 저장 데이터 migration, API 폐기나 배포 승인이 아니다.

## 결정1. FlowView를 중심으로 개선한다

- Context: 기존 Live 스펙은 구현·운영 범위가 크지만 FlowView 대비 사용자 가치가 입증되지 않았다.
- Decision: 하나의 FlowView에서 현재 흐름, 선택적 비교와 직접 연결 탐색을 제공한다.
- Alternatives: 독립 Live View와 상시 분석을 먼저 구현하는 안은 비용 대비 효과가 불확실해 제외한다. 기존 FlowView만 그대로 두는 안은 비교의 추가 가치를 검증할 기회가 없어 선택하지 않는다.
- Rationale: 사용자가 묻는 현재 동작 이해를 우선하고 추가 기능은 실제 과제로 검증한다.
- Consequences: Live near-live 시간 목표·Episode·별도 화면과 저장 전면 개편은 필수 작업에서 제외한다. 기존 구현을 이 문서 작업에서 제거하지 않는다.
- Evidence: 이 대화의 중립적 가치 재검토와 사용자의 권고 방향 동의.
- Contract Trace: §1–§3, §11, FA-01·FA-14.
- Follow-up: 사용자 평가 없이 효과를 확정하지 않는다.

## 결정2. 신규 기능에는 비교 기준을 요구하지 않는다

- Context: 기능을 처음 작성하면 이전 분석이나 구현이 없을 수 있다.
- Decision: 현재 확인된 흐름과 분석 중단 경계를 먼저 제공한다. 이전 분석을 선택한 경우에만 전후 비교를 제공한다.
- Alternatives: 최초 source를 전부 added behavior로 표시하거나 baseline이 없으면 조회를 막는 안은 신규 기능 이해에 맞지 않아 제외한다.
- Rationale: 현재 구현을 이해하는 것과 변화량을 이해하는 것은 별도 작업이다.
- Consequences: partial 결과를 명시하고 분석 실패·연결 미발견·미구현을 구별해야 한다. baseline 없는 상태는 ‘변경 없음’이 아니다.
- Evidence: 사용자의 신규 기능 지적과 환불 기능 작성 시나리오.
- Contract Trace: §4.1·§6.1, FA-01·FA-03·FA-10.
- Follow-up: 실제 adapter로 incomplete function·syntax error·missing entry를 검증한다.

## 결정3. 사용자 요청의 성공 결과만 화면에 적용한다

- Context: 읽던 비교를 자동 갱신하지 않는 결정은 유지하되 새 방향은 상시 결과 도착 모델이 아니다.
- Decision: ‘다시 분석’과 ‘변경 전후 비교’는 분석과 성공 후 전환을 함께 요청하는 동작이다. 별도 ‘새 변경 보기’ 버튼은 요구하지 않는다. 실패·취소·늦은 이전 응답은 읽던 상태를 변경하지 않는다.
- Alternatives: 편집 event 자동 갱신은 사용자 제어를 깨므로 제외한다. 수동 분석 후 다시 승인 버튼을 요구하는 안은 이미 요청한 동작을 반복하므로 제외한다.
- Rationale: 기존 명시적 전환 의도를 단순한 요청형 제품에 적용한다.
- Consequences: request identity와 표시 상태를 분리하고 baseline을 자동 전진시키지 않는다.
- Evidence: ‘새 변경 보기’ 클릭 전환 확정과 이후 ‘다시 분석’ 시나리오를 최종화한 현재 요청.
- Contract Trace: §5, FA-04–06.
- Follow-up: 응답 역전·취소·삭제된 선택의 UI 검증.

## 결정4. 직접 연결과 요구사항 근거의 한계를 유지한다

- Context: 전역 영향 추정이나 기능 완료 선언은 정적 연결보다 강한 증거가 필요하다.
- Decision: 직접 caller/callee·state·명시적 event 관계와 요구사항에 연결된 source 근거를 보여준다. 더 깊은 탐색은 사용자 선택으로 수행한다.
- Alternatives: 저장소 전체 영향 자동 확정, 흐름 연결을 기능 완성으로 판정하는 안은 확인 범위를 넘어 제외한다.
- Rationale: 실제 코드 탐색을 돕고 확인하지 못한 의미를 단정하지 않는다.
- Consequences: 동일 snapshot의 관계만 연결하고 실행 사실·테스트 결과·요구사항 충족을 독립적으로 다룬다.
- Evidence: 기존 영향 범위 제한과 신규 기능 요구사항 대조 시나리오.
- Contract Trace: §6.3, FA-09·FA-10.
- Follow-up: 효과가 작으면 추가 영향 기능 확장을 중단한다.

## 결정5. 필요한 안전성만 선행한다

- Context: 비교에는 두 시점의 근거가 필요하지만 전체 Live 저장 architecture가 반드시 필요한 것은 아니다.
- Decision: 기존 snapshot/proof/storage 경계를 재사용하고 열린 분석·baseline 보호, source identity·secret·호환성 결함만 필요한 범위에서 해결한다.
- Alternatives: typed namespace·SQLite catalog·outbox·GC 전면 교체를 먼저 하는 안은 현재 사용자 기능보다 넓어 제외한다. 근거 보존 없이 live disk를 읽는 안은 잘못된 비교를 만들어 제외한다.
- Rationale: 안전성을 낮추지 않으면서 구현 범위를 사용자 가치에 맞춘다.
- Consequences: 기존 저장 구현이 보호 요구를 충족하지 못하면 비교 단계는 차단되지만 전면 개편이 자동 승인되지는 않는다.
- Evidence: 사용자의 구현관리 범위 우려와 FlowView 업그레이드 방향 동의.
- Contract Trace: §7–§9, FA-11–13.
- Follow-up: 구현 전 실제 보호 기능과 regression 경계를 조사한다.

차단하는 제품 미결정은 없다. 단계별 실행 명령·지원 corpus·평가 기준은 parent §11의 선행 산출물이며 누락한 상태로 production-ready를 선언할 수 없다.
