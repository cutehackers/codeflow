# 언어 adapter 적합성 결정

- Status: Superseded by [요청 흐름 코드 읽기 통합 결정](2026-09-23-requested-flow-code-reading-delivery-ko.md)
- Created: 2026-09-23
- Parent: [언어 adapter 공통 분석 적합성](../specs/2026-09-23-language-adapter-conformance-ko.md)
- Source: 2026-09-23 사용자 대화와 현재 Dart·TypeScript·Go adapter 조사

## 결정1. 문법 추출과 공통 검증의 책임

언어별 parser와 compiler 도구는 심볼·문장·직접 관계와 소스 범위를 추출한다. 공통 경계는 같은 snapshot에 대한 범위·해시·참조 일관성을 검증한다. 관계가 실제 구문에서 나온 사실인지는 언어별 소스 사례의 독립적인 기대 결과와 비교한다. 단일 parser를 만들거나 언어별 adapter마다 공통 구조 검사를 다시 구현하는 대안은 문법 차이와 규칙 중복 때문에 채택하지 않는다. 새 언어도 언어 도구가 확인한 사실을 제출해야 하지만 공통 구조 검사를 복제할 필요는 없다. INV-01·02와 FA-01·02·05에 적용한다.

## 결정2. 적합성 검사의 한계

현재 `sliced-payload`에는 간선의 AST 출처가 없다. 따라서 공통 검사는 구조상 유효하지만 실제 실행과 다른 간선을 보편적으로 식별할 수 없다. 언어별 사례에서 기대 관계와 금지 관계를 명시해 해당 사례의 거짓 간선을 거부한다. 제출하지 않은 중간 문장이나 다른 문법·프레임워크의 실행 의미까지 완전하다고 증명하지 않는다. 검사가 통과했다는 이유로 기존 VS-02·03·05·06의 사용자 경로와 CodeView 검증을 생략하는 대안은 채택하지 않는다. INV-03·04와 FA-03~05에 적용한다.

## 결정3. 기존 계약과 호환성

기존 analyzer v2, `sliced-payload`, source read-only, secret redaction, 저장 View를 유지한다. 검사가 새 공개 필드나 저장 migration 없이는 성립하지 않는다는 증거가 나오면 변경을 강행하지 않고 별도 호환성 결정을 기록한다. 이 결정은 기존 언어별 VS의 범위·상태를 바꾸지 않는다. INV-05에 적용한다.

## 후속 확인

구현 전 세 adapter의 실제 fixture 반환값과 공통 schema를 비교해 ASM-01을 확인한다. source read-only 감사는 일반 `slice` 입력과 별도 경계에서 확인한다. 이 결정은 새 언어 지원 범위를 승인하지 않는다.
