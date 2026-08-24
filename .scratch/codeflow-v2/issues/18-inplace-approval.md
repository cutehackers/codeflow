# 18: in-place 승인 + 승인 큐

**What to build:** FlowView 카드에서 session/derived 배지 항목의 이름·규칙을 인라인 수정 → [승인] → event-log append → view 갱신 → approved 배지 전환. stale/orphaned 항목을 모으는 승인 큐 배너. agent 제안 승인(approve_step)도 동일 경로 유입. E3가 최고 권위가 되는 순간 — 제품의 핵심 루프 완성.

**Blocked by:** 13 (stale 판정), 14 (event-log), 17 (FlowView 실데이터).

**Status:** ready-for-agent

- [ ] 인라인 수정→승인→재시작 후에도 유지(event-log 기반)
- [ ] approved 배지 전환과 view 갱신
- [ ] stale/orphaned가 큐 배너에 집계됨
