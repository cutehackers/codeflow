# 01: 바이트 오프셋·Flutter 워크스페이스 수확 보정

**What to build:** 한글 주석이 포함된 Flutter 모노레포에서 `codeflow publish`를 실행하면, 발행된 FlowSpec의 앵커가 주석이 아닌 실제 `state = ...`·`await ref.read(...)(draft)` 라인을 정확히 가리키고, `*Controller/*Page` 비즈니스 진입점이 후보로 수확되어 이후 탭과 맵이 정확한 코드 근거 위에서 동작한다.

**Blocked by:** None (can start immediately).

**Status:** done

- [x] `slice.dart`의 `byteRange`·`symbolRange`가 `utf8.encode` 보정으로 코드 라인을 가리키고, `spanHash`가 바이트 슬라이스와 일치하며 `dart analyze`·`dart test` 37/37이 통과한다
- [x] `profile.dart`에 `Controller` 도메인 마커와 워크스페이스 폴백(`packages/*/lib`, `apps/*/lib`)이 적용되어 `codeflow flows $WORKSPACE`에서 `JoinController._onJoinSubmit`이 `pinned` 후보로 수확된다
