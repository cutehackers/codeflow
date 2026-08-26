# 05: 레이어 간 위임 가시화

**What to build:** 각 단계 카드의 “이 단계에서 이어지는 곳” 칩과 Map의 레인 간 연결이 `resolved_cross_file`·`boundary_call` 엣지로 표시되어 `page → controller → useCase → repository → api` 위임이 끊기지 않고 읽히며, 해소되지 않은 호출은 `unknown`으로, 깊이 초과는 “일부 구간만 추적됨”으로 정직하게 표시된다.

**Blocked by:** 03: 5레인 Architecture Map (page → controller → useCase → repository → api), 04: 레이어를 관통하는 핵심 타임라인.

**Status:** done

- [x] 엣지가 `stepOrdinal`을 carry하여 단계별 위임 대상이 카드에 렌더링되고 Map 레인 간에 연결로 표시된다
- [x] `unresolved_dynamic` 호출이 `unknown` 패널에, `truncated`가 경고 칩으로 표시된다
