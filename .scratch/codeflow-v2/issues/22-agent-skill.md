# 22: codeflow agent 스킬 — 자연어 의도 → 흐름 절차 각인

**What to build:** agent 세션용 스킬(SKILL.md)로 사용자 프롬프트("회원가입 흐름 분석해서 flowview로 만들어줘")가 결정적 워크플로로 이어지게 한다. 절차: 1) harvest_flows로 후보 조회(intent-matching 신호 기반 매칭) 2) 모호하면 사용자에게 확인 질문 3) 매칭 없으면 analyze_flow로 진입점 지정 4) get_flow_payload로 이해 후 submit_flow_draft(앵커 규칙 준수) 5) open_review로 FlowView 오픈. 의도 해석은 호스팅 agent의 능력, codeflow는 구조화 신호 공급·수신 — 이 역할 분담을 스킬이 명문화.

**Blocked by:** 15 (도구 면 확정).

**Status:** ready-for-agent

- [ ] 스킬이 위 5단계 절차와 fallback 경로를 지시함
- [ ] 앵커 필수 등 submit_flow_draft 작성 규칙 포함
- [ ] 실 프롬프트 종단 시연이 스킬 없이는 실패/서툴렀다가 스킬 적용 시 성공하는 것 비교 가능
