# 04: 레이어를 관통하는 핵심 타임라인

**What to build:** 하단 타임라인이 레이어를 관통하는 핵심 연속(`mutation`/`call` 또는 `stateDelta`/`sideEffect`가 있는 단계만)으로 보이고, 각 아이템에 레이어 뱃지가 붙으며 “핵심 N개 · 전체 M개 중” 배지와 `전체 보기` 토글이 제공되고, `prev/next`가 핵심 인덱스 기준으로 동작하여 비핵심 선택에서도 이동이 막히지 않는다.

**Blocked by:** 02: 비즈니스 흐름 탭 (큰 흐름만, 1~2줄 요약), 03: 5레인 Architecture Map (page → controller → useCase → repository → api).

**Status:** done

- [x] `isCoreStep` 정의로 타임라인이 핵심만 렌더링되고 레이어 뱃지가 표시되며 `핵심 N개 · 전체 M개 중` 배지와 토글이 동작한다
- [x] `selectStep`의 `prev/next` 비활성 로직이 `p===-1`(비핵심 선택) 및 단일 코어 케이스를 올바르게 처리한다
