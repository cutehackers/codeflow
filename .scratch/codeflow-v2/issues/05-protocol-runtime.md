# 05: 어댑터 프로토콜 런타임 + 컨포먼스 스위트

**What to build:** CORE의 어댑터 실행 인프라. 영속 프로세스 풀 스폰/재사용, 요청 id 상관, typed error codes, 요청별 timeout, cancellation, 크래시 감지 후 재시작 1회 + 해당 요청 재전송, backpressure(큐 상한). mock 어댑터를 대상으로 fault-injection 컨포먼스 스위트를 만든다 — 이후 모든 언어 어댑터(Kotlin 포함)가 통과해야 하는 동일 계측.

**Blocked by:** 03 (adapter-protocol 계약), 04 (CORE 골격).

**Status:** ready-for-agent

- [ ] mock 어댑터와 NDJSON 왕복 성공
- [ ] 강제 종료 후 자동 재시작 + 재전송 회복
- [ ] timeout 유발 시 typed error 반환
- [ ] 대량 응답에서 backpressure 에러 동작
- [ ] 컨포먼스 스위트가 재사용 가능한 형태로 분리됨
