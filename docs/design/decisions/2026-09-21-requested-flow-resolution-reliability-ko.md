# 요청 흐름 확정과 분석 신뢰성 결정

- Status: Accepted
- Created: 2026-09-21
- Parent: [요청 흐름 확정과 분석 신뢰성](../specs/2026-09-21-requested-flow-resolution-reliability-ko.md)
- Source: 2026-09-21 사용자 대화와 Molycard FlowView 분석. 같은 자연어 요청이 서로 다른 진입점과 단일 fallback 단계로 표현된 사례.

## 결정1. 요청 흐름 확정을 분석 전 계약으로 둔다

자연어 요청, 선택된 후보, 진입 심볼, 소스 근거를 하나의 기록으로 보존한다. 대화 에이전트는 CodeGraph 등으로 소스 근거를 찾고 선택하며, CodeFlow는 snapshot 안의 근거와 대상 연결을 검증한다. CodeGraph는 탐색 도구이고 선택 주체가 아니다.

문자열 후보 검색만으로 선택하거나 선택 뒤 원문 요청을 심볼 경로로 바꾸는 대안은 거부한다. 그 방식은 잘못 선택한 흐름을 사용자가 검증할 수 없게 한다.

## 결정2. 분석 실패를 정상 FlowView로 대체하지 않는다

대상 메서드를 찾지 못하거나 실행 가능한 본문에서 분석 단계가 나오지 않으면 명시적 실패를 반환한다. 전체 파일 범위의 임의 단일 단계, 가짜 entry/result, 저장 View 생성은 허용하지 않는다.

이 결정은 Molycard `BattleController`의 primary constructor를 scanner가 잘못 해석해 `_onEndTurn`을 놓친 사례에서 필요하다. 유효한 Dart 파일의 분석 실패를 코드가 짧은 정상 흐름으로 표현하면 안 된다.

## 결정3. 실행 관계가 FlowSequence보다 먼저다

호출 문맥과 `sequence`·`control_flow` 관계는 adapter가 AST로 확인한 사실만 전달한다. curator는 이를 사용해 1:N 관문을 만들 수 있지만, 관계가 없을 때 인접 단계나 같은 심볼만으로 흐름을 만들지 않는다.

## 결정4. 기본 산출물은 코드 읽기 경로다

CodeFlow는 요청한 구현을 빠르게 읽게 하는 제품이다. FlowSequence는 중요한 코드 결정과 전환을 묶는 상위 색인이고, 실행 관계 그래프는 각 항목의 포함·이동 근거다. 그래프 자체를 기본 화면의 목적물로 만들거나, 모든 정점과 간선을 먼저 노출하지 않는다.

좌측 탐색은 코드 본문을 중복하지 않고 제목, 실제 심볼, 포함 이유, 확인된 관계를 보여 준다. 사용자가 선택한 항목의 실제 구현과 주변 문맥은 CodeView가 제공한다.

## 결정5. `architecture`를 프로젝트에서 폐기한다

프로젝트마다 구조와 용어가 다르므로 `controller`, `usecase`, `data` 같은 architecture label을 CodeFlow에서 사용하지 않는다. 새 분석·저장 View·MCP·FlowView에서 관련 field와 label을 생성·저장·전달·표시하지 않는다. 중요 항목은 요청의 결과를 바꾸는 진입, 판단, 변환, 상태 변경, 외부 효과, 직접 인계, 결과 또는 분석 경계라는 확인 가능한 코드 역할로 판단한다.

기존 저장 데이터와 공개 계약은 producer·consumer 목록화 뒤 schema, writer, reader, UI, fixture를 함께 이행한다. 지원 기간의 legacy reader는 값을 무시할 수 있지만 FlowView와 MCP에 재노출하지 않는다.

## 결정6. 요청 설명은 CodeView까지 검증한다

사용자 요청과 코드의 연결은 “이름이 비슷하다”는 후보 선택이나 요약 문구로 끝나면 안 된다. 요청 연결, 선택 이유, 실제 심볼, 정확한 코드 범위, 확인된 관계, CodeView 표시가 같은 snapshot에서 이어져야 한다.

대표 코드의 범위나 CodeView source context를 확인하지 못하면 정상 FlowView를 저장하지 않는다. 비대표 하위 코드가 누락된 경우에는 마지막으로 검증한 코드에서 분석 경계를 표시하며, 다른 동명 심볼이나 파일 전체 범위로 대체하지 않는다.

## 결과

새 분석·저장 View에 표준 필드 `flowResolution`을 정의하고 사용한다. 과거 View는 필드 부재를 한계로 유지한다. 새 계약은 기존 2026-09-18 FlowSequence 계약을 대체하지 않으며, 해당 계약이 전제한 Requested Flow를 확정하는 역할을 맡는다.
