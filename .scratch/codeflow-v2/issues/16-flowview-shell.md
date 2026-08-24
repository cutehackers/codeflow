# 16: FlowView 셸 + Cmd+K Quick Switcher

**What to build:** FlowView 프론트엔드(React + Vite + Zustand). 픽스처 FlowSpec을 소비하지만 브라우저에서 완결 경험: Flow Story 세로 타임라인(단계 카드, 배지 approved/session/derived/unknown/stale, unknown 실존 카드), 요청된 흐름 탭 모델(전체 목록 레일 없음 — 결정 #3), Cmd+K Quick Switcher 팝오버로 캐시된 후보에서 검색→탭 추가·전환(R13), 흑백 모노크롬. CORE loopback serve + per-run token + Host/Origin 검사 + CSRF 방어(R8).

**Blocked by:** 02 (flowspec 계약), 04 (CORE 골격).

**Status:** ready-for-agent

- [ ] 브라우저에서 픽스처 흐름의 Flow Story 낭독
- [ ] Cmd+K로 흐름 추가·전환
- [ ] 토큰 없는 접근 거부, Host/Origin 검사 동작
- [ ] 단계 카드 명세(행동/규칙/상태/외부/분기) 렌더링
