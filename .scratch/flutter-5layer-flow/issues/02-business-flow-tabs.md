# 02: 비즈니스 흐름 탭 (큰 흐름만, 1~2줄 요약)

**What to build:** 여러 세대가 발행된 상태에서 FlowView를 열면, 상단 탭 바에 비즈니스 흐름만( `triggerClass=user_action|system_event` 또는 `*Controller/*Page/*Notifier` 진입, `*UseCase.call` 제외) 목록이 보이고, 각 탭에 `title`과 1~2줄 `description`( `docLine` 우선, 없으면 `DerivedName` 폴백) 및 단계 수가 표시되어 회원가입/레이싱/랭킹 간 전환이 가능하다.

**Blocked by:** 01: 바이트 오프셋·Flutter 워크스페이스 수확 보정.

**Status:** done

- [x] `/api/flows` 또는 FlowView 필터가 비즈니스 흐름만 반환하고 각 항목에 `description`이 포함된다 — publish 단계에서 `use_case_invocation` 후보는 발행 자체에서 제외(pinned 제외), 재발행 결과 UseCase 단독 흐름 0건
- [x] `flow-tabs`가 `description` 2줄 클램프·활성 상태·가로 스크롤로 렌더링되고, 단일 흐름에서는 숨겨지며 `flow-534` “회원가입을 제출한다” 탭이 확인된다
