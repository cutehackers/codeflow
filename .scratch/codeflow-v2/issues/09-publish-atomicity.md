# 09: publish 원자성 + 저장 레이아웃

**What to build:** `.codeflow` 저장 레이아웃과 단일 일관 시점 발행. facts(ast/slice 이중 캐시, slice 키 = 바이트해시∥candidateId∥opts∥profile·어댑터·스키마버전∥패키지 리졸루션 — R4), semantics(events 원장/view 파생), ir/, index.sqlite(WAL), staging generation 디렉터리 → commit pointer rename으로 세대 전환(R6), read-set worktree fingerprint 전후 검증(R5), crash-injection 복구 테스트. 공통 secret scanner 단일 관문을 저장 경로에 설치 — 모든 필드가 이 관문을 통과(R7).

**Blocked by:** 07 (발행할 실제 슬라이스 산출물 필요).

**Status:** ready-for-agent

- [ ] publish 중 kill -9 후 재기동 시 pointer 기준 정합 상태 회복
- [ ] fingerprint 불일치 시 폐기·재분석, 마지막 일관 snapshot 유지
- [ ] 시크릿 샘플이 scanner 관문에서 차단됨
- [ ] staging → pointer 전환이 단일 rename임을 테스트로 고정
