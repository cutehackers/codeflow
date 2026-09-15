# 이전 Live View production 결정 대체 기록

- Record Status: Superseded
- Created: 2026-09-14
- Superseded By: [FlowView 개선 방향 결정기록](2026-09-14-flowview-code-comprehension-ko.md)
- Parent Contract: [FlowView 코드 이해 최종 스펙](../specs/2026-09-14-flowview-code-comprehension-ko.md)

기존 독립 Live View와 상시 분석의 결정은 새 FlowView 방향으로 대체되었다. 사용자의 명시적 동작 없이 읽는 화면을 바꾸지 않는 원칙은 유지하며, 수동 재분석·비교 요청의 성공 결과로 전환한다. 이전 저장 architecture 전체를 구현 선행 조건으로 사용하지 않는다.
