# 02: 파이프라인 데이터 계약 — candidate · sliced-payload · flowspec

**What to build:** E1 파이프라인(Harvest→Slice→Fusion)이 주고받는 세 데이터 계약을 확정한다. candidate(trigger class enum, dedup/tie-break 판별 필드 — R11), sliced-payload(언어 중립 step 서술, truncated/unknown edge 명시, redaction 완료 표시), flowspec(provenance+freshness+basisSha 필수 — R2). 각 스키마마다 골든 픽스처를 만들어 검증 하네스에 등록한다.

**Blocked by:** 01 (공통 어휘와 anchor 참조 필요).

**Status:** ready-for-agent

- [ ] candidate 스키마: trigger class enum과 dedup/tie-break 판별 필드 포함
- [ ] candidate에 intent-matching 신호 필드 명시 — entry symbol path, 클래스명, trigger class, derived 이름, 선언부 doc 라인 (agent가 자연어 의도를 후보에 매칭할 수 있는 최소 신호 세트)
- [ ] sliced-payload 스키마: unknown/truncated edge 표현이 normative로 포함
- [ ] flowspec 스키마: provenance·freshness·basisSha 누락 시 검증 거부
- [ ] 세 스키마의 유효/무효 골든 픽스처가 하네스에 등록됨
