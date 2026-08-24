# 15: MCP server — 도구 7종 + 인증

**What to build:** agent 세션이 codeflow를 쓰는 공식 통로. 도구 7종(harvest_flows, get_flow_payload, analyze_flow, submit_flow_draft, report_unknowns, open_review, approve_step)을 JSON-RPC over stdio로 노출, loopback + per-run token, 쓰기 도구 authorization. harvest_flows 응답은 intent-matching 신호 세트를 포함해 agent의 자연어 의도 매칭을 지원한다. analyze_flow는 마커가 놓친 임의 진입점(resolved symbol)에 대한 즉석 분석 요청 — 자연어 프롬프트로 흐름을 요청하는 사용자 시나리오의 fallback 경로.

**Blocked by:** 06 (harvest 실데이터), 14 (E2 흡수 경로).

**Status:** ready-for-agent

- [ ] 7개 도구가 프로토콜 계약대로 응답
- [ ] harvest_flows 응답에 intent-matching 신호(심볼·클래스·trigger class·derived 이름·doc 라인) 포함
- [ ] analyze_flow로 마커 미발견 진입점 지정 → 슬라이스→발행→open_review까지 연결
- [ ] 토큰 없는 쓰기 요청 거부
- [ ] 실 agent 클라이언트에서 submit_flow_draft → session 배지 흐름 시연
- [ ] 종단 시연: "회원가입 흐름 분석해서 flowview로 만들어줘" 프롬프트 → 후보 매칭(또는 analyze_flow) → FlowView 오픈
