# 콜백 전달 관계의 저장 및 표시 결정

- Status: Accepted
- Created: 2026-09-28
- Parent: [요청 흐름을 실제 코드로 읽는 구현 통합 계약](../specs/2026-09-23-requested-flow-code-reading-delivery-ko.md)
- Scope: `SlicedPayload`, `FlowSpec`, `SemanticMapIR`, FlowView

## 결정1. 전달과 실행을 별도 사실로 표현한다

언어 adapter가 함수 리터럴이 호출 인자로 전달된 사실과 해당 함수 본문의 첫 문장을 AST에서 확인하면 `callback_argument` 관계를 낸다. 이 관계는 전달 문장과 콜백 본문의 코드 위치를 연결하지만, 콜백이 실제로 호출됐다는 뜻은 아니다. 호출 시점, 취소 여부, 호출 횟수를 증명하지 못하면 기존 `unknown_edge`를 함께 유지한다. Core는 관계 양끝의 심볼, 호출 범위, 서로 다른 실행 단위와 콜백 첫 단계를 확인한다. 실제 호출이 확인된 `resolved_cross_file`로 바꾸지 않는다.

## 결정2. 기존 결과와 소비자의 해석을 보호한다

새 관계 종류를 `SlicedPayload`와 `FlowSpec`의 허용 값에 추가한다. 기존 저장 결과에는 새 필드나 재분석을 요구하지 않는다. Core의 새 분석 요청만 `includeCallbackRelations`을 지정하므로 이 옵션을 쓰지 않는 adapter 직접 호출은 종전 결과를 받는다. FlowView는 관계를 “콜백 전달 · 실행 여부 미확인”으로 표시하고 대상 코드로 이동할 수 있게 한다. 조건 도달성, 호출 영향, 실행 연결 검증과 요약은 이 관계를 실행 간선으로 사용하지 않는다.

새 종류를 모르는 과거 바이너리나 엄격한 외부 schema 소비자는 새 분석 결과를 거부할 수 있다. 따라서 이전 버전으로 되돌린 뒤 새 결과를 읽는 동작은 보장하지 않는다. 기존 View를 현 버전에서 다시 여는 경로와 새 결과의 저장·복원·HTTP·MCP 경로를 검증한다.
