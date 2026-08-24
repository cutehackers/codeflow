# 06: Dart 어댑터 harvest + codeflow flows (첫 트레이서)

**What to build:** 첫 사용자 가시 성과. Dart 어댑터의 detect/harvestCandidates(Riverpod·Bloc·go_router 기본 마커, framework profile YAML 로딩)를 구현하고, CORE가 이를 받아 마커 구체성×진입점 팬인×경계 도달성 점수화, root-equivalence dedup, tie-breaker, `codeflow.flows.yaml` 고정·제외·이름 오버라이드를 적용해 CLI `codeflow flows`로 점수순 후보 목록을 출력한다.

**Blocked by:** 02 (candidate 계약), 05 (프로토콜 런타임).

**Status:** ready-for-agent

- [ ] example 앱에서 유스케이스·노티파이어 후보가 점수순 출력됨
- [ ] 동일 흐름으로 수렴하는 후보가 dedup됨
- [ ] flows.yaml 제외·고정·이름 지정이 목록에 반영됨
- [ ] profile YAML(Riverpod/Bloc/go_router 기본 세트) 로딩 동작
