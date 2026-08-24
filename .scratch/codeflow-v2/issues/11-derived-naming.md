# 11: derived naming 엔진 (fusion 분할 1/3)

**What to build:** 식별자 규칙 변환기 — `submitOrder` → "주문을 제출한다"처럼 결정적 규칙 테이블로 자연어 step 이름을 생성하고 provenance=derived로 기록한다. LLM 없음, 추측 금지: 규칙에 매칭 안 하는 식별자는 unknown 유지. fusion의 한 축이지만 독립 스위트로 검증 가능한 순수 함수 영역.

**Blocked by:** 08 (슬라이싱 결과의 심볼 필요).

**Status:** ready-for-agent

- [ ] 규칙 테이블 기반 변환 스위트 통과(동사·목적어 패턴)
- [ ] 비매칭 식별자는 unknown 유지(derived 날조 없음)
- [ ] 한국어 문장 패턴이 결정적으로 재현됨
