# 08: cross-file resolved-symbol 추적 (결정 #7 개정 본체)

**What to build:** 슬라이서가 import 해석이 확인된(resolved) 직접 참조 심볼은 파일이 달라도 따라간다. 필드 타입 확인 기반(예: `_useCase.execute` → `SignupUseCase`), depth ≤ 5, visited-set 순환 차단, 타입 미확인·동적 호출은 unknown edge 카드로 명시. Controller→UseCase→Repository가 파일별로 분리된 일반 구조에서 중간 규칙·상태변경이 누락되지 않는 것이 핵심 수용 기준.

**Blocked by:** 07 (동일 파일 슬라이싱 위에 확장).

**Status:** ready-for-agent

- [ ] 분리 파일 시나리오(Controller→UseCase→Repository)에서 end-to-end step 추출
- [ ] depth 초과 시 truncated, 순환 참조 차단
- [ ] unresolved/dynamic 호출이 unknown edge 카드로 명시됨
- [ ] 전체 DAG 생성 없음 — resolved 직접 참조만 추적함을 테스트로 고정
