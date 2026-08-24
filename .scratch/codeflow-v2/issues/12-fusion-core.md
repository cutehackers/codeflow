# 12: fusion 코어 — 권위 순위 융합 (fusion 분할 2/3)

**What to build:** 세 증거원의 융합 엔진. E1 구조 사실 + E2 session 제출 + E3 approved 지식을 권위 순위(approved > session > derived > unknown)로 병합하고, 각 step에 provenance/freshness/confidence/basisSha를 기록한다. 불변식: **E1 구조 사실은 E3/E2가 덮어쓰지 못한다** — 승인된 설명이 코드와 모순되면 stale은 되지만 구조 카드는 대체되지 않는다. unknown 보존, 추측으로 흐름 완성 금지. 산출 FlowSpec을 staging→publish 경로로 발행.

**Blocked by:** 03 (session-artifact 계약), 09 (발행 인프라), 11 (derived naming).

**Status:** ready-for-agent

- [ ] 충돌 시나리오 매트릭스(E1×E2×E3 조합) 테스트 통과
- [ ] flowspec에 provenance/freshness/confidence/basisSha 기록
- [ ] unknown이 임의 값으로 채워지지 않고 보존됨
