# 03: 상호작용 계약 — adapter-protocol · session-artifact

**What to build:** CORE가 외부와 맺는 두 상호작용 계약을 확정한다. adapter-protocol(NDJSON 메시지 봉투, 버전 협상, timeout/cancellation/max-message-size/typed error codes/crash restart/backpressure를 normative 운영 계약으로 — R11), session-artifact(agent의 journey 초안 제출물, 앵커 참조 필수, 앵커 없는 제출 거부). 픽스처로 정상 교환과 오류 교환을 모두 고정한다.

**Blocked by:** 01 (공통 어휘 필요).

**Status:** ready-for-agent

- [ ] adapter-protocol 스키마: 요청/응답 봉투 + 버전 협상 필드
- [ ] 운영 계약(타임아웃·취소·최대 메시지·typed error·재시작·backpressure)이 normative 코드로 정의됨
- [ ] session-artifact 스키마: 앵커 참조 필수, 미충족 거부 사례 픽스처
- [ ] 정상/오류 메시지 교환 골든 픽스처 등록
