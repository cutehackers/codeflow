# CodeFlow v2

비즈니스 흐름(Trigger → Rule → State Mutation → Side Effect)을 코드에서 end-to-end로
추출해 FlowView로 명쾌하게 읽게 하는 로컬 도구.

- **설계 문서**: [`docs/design-v2.md`](docs/design-v2.md) — 상태 REVIEWED, P1 게이트 해소 완료
- **현재 단계**: M1 — 데이터 계약 6종 스키마 + `codeflow.flows.yaml` 정의
- **v1 코드**: `legacy/` — 참고용 보관 (fresh start, v2가 루트에서 시작)
