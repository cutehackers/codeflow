# 01: 계약 기반 — identity · anchor · 공통 어휘 스키마

**What to build:** 모든 계약이 참조하는 기반 스키마를 확정한다. identity(flowId 생성 규칙 = canonical entry symbol path 해시, collision 규칙, move/rename 안정성, supersedes 체인, tombstone — R9), anchor 계약(repo-relative path + byte range + fileHash + spanHash + enclosingSymbolPath + canonical AST fingerprint — R3), 공통 어휘(provenance enum: approved/session/derived/unknown, freshness enum: fresh/stale/orphaned). 골든 픽스처와 스키마 검증 하네스를 만들어 CI에서 전 계약 드리프트를 잡는다.

**Blocked by:** None (can start immediately).

**Status:** ready-for-agent

- [ ] identity 스키마: flowId 생성·collision·supersedes·tombstone 규칙 명문화
- [ ] anchor 계약: 라인 번호 비사용, AST fingerprint 기반 검증 정의
- [ ] provenance/freshness enum이 공통 어휘로 확정
- [ ] 유효/무효 골든 픽스처 세트가 하네스에서 통과·실패를 정확히 판별
- [ ] "라인만 밀린 변경은 orphan 아님"을 보이는 앵커 픽스처 포함
