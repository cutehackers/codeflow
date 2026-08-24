# 04: CORE Go 스캐폴드 + codeflow init

**What to build:** v2 CORE의 Go 모듈 골격과 첫 사용자 커맨드를 만든다. `codeflow init`: 대상 repo에서 workspace.json 생성(어댑터 pin 버전 해석 포함), Dart 프로젝트 감지, 발견된 v1 잔여 산출물 제거(fresh start — 결정 #16). 재실행 시 멱등. 실제 Flutter 프로젝트에서 실행해 성공하는 것이 시연 기준.

**Blocked by:** None (can start immediately).

**Status:** ready-for-agent

- [ ] Go 모듈 골격 + 빌드가 CI에서 통과
- [ ] init이 workspace.json을 생성하고 어댑터 pin을 기록함
- [ ] Dart 프로젝트 감지(pubspec 존재) 동작
- [ ] v1 잔여 데이터 발견 시 제거 또는 명확한 안내 후 제거
- [ ] 재실행 멱등 확인
