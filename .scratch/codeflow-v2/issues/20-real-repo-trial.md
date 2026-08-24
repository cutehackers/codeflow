# 20: 실 repo 시험 + 우선순위 튜닝 (M5)

**What to build:** 실제 Flutter 프로젝트에서 end-to-end(harvest → flow story → 승인) 가동. 마커 패턴·경계 마커·점수 가중치·tie-breaker를 실데이터로 튜닝하고 profile에 반영, 발견된 갭을 문서화. 제품 목표(비즈니스 흐름의 명확한 이해와 명쾌한 코드 요약) 충족 여부를 실사용으로 판정하는 단계.

**Blocked by:** 10 (watch 증분), 15 (MCP server), 18 (in-place 승인).

**Status:** ready-for-agent

- [ ] 실 repo에서 최소 복수 흐름이 end-to-end 완결
- [ ] 튜닝 결과가 profile/스코어링에 반영됨
- [ ] 알려진 갭과 unknown 잔존 목록 문서화
