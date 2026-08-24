# 14: E2 흡수 경로 — session-artifact 수신 → event-log

**What to build:** agent 제출물의 공식 진입 경로. session-artifact 검증 → 앵커 재검증 게이트 → 통과분만 semantics event-log append → 파생 view 갱신. 앵커 부정합은 폐기하지 않고 unlinked 격리 보관(나중에 relink 가능). append-only 원장과 인라인 뷰의 분리가 이 티켓에서 확립됨.

**Blocked by:** 03 (session-artifact 계약), 09 (저장 인프라), 12 (fusion 코어 — 흡수된 의미가 flowspec에 반영되어야 시연 가능).

**Status:** ready-for-agent

- [ ] 유효 제출이 view에 session 배지로 반영됨
- [ ] 앵커 부정합 제출이 unlinked 격리 보관됨
- [ ] event-log append 기록과 파생 view 일치 확인
