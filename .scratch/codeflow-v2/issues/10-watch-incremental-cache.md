# 10: watch 증분 루프 + 이중 캐시 무효화

**What to build:** workspace 감시 → 변경 파일의 AST fact 캐시 무효화 → 영향 받는 slice만 재계산(미변경 slice는 캐시 히트). fingerprint 불일치 시 폐기·재분석, 지속 변경 시 마지막 일관 snapshot + stale 표시. 캐시 히트를 로그로 증명하는 것이 수용 기준 — "같은 입력은 두 번 분석하지 않는다" 원칙의 실현.

**Blocked by:** 08 (cross-file 추적 완료된 슬라이서), 09 (캐시·발행 인프라).

**Status:** ready-for-agent

- [ ] 파일 1개 수정 시 미변경 slice 캐시 히트 로그로 증명
- [ ] 변경 파일에 의존하는 slice만 재계산됨
- [ ] 지속 변경 시 마지막 일관 snapshot 유지 + stale 표시
