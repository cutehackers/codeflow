# 언어 adapter 공통 분석 적합성

- Contract ID: LANGUAGE-ADAPTER-CONFORMANCE
- Contract Status: Superseded
- Superseded by: [요청 흐름 코드 읽기 통합 계약](2026-09-23-requested-flow-code-reading-delivery-ko.md)의 VS-05. 이 문서는 이전 제안의 기록이다.
- Intent Status: Hardened
- Created: 2026-09-23
- Source: 2026-09-23 사용자 대화와 현재 Dart·TypeScript·Go adapter 조사
- Decision Record: [언어 adapter 적합성 결정](../decisions/2026-09-23-language-adapter-conformance-ko.md)
- Decision Record Status: Superseded by [요청 흐름 코드 읽기 통합 결정](../decisions/2026-09-23-requested-flow-code-reading-delivery-ko.md).
- Glossary: [CodeFlow 설계 용어집](../glossary.md)
- Related Contract: [코드 읽기 근거와 언어별 분석 품질](2026-09-23-code-reading-evidence-and-language-parity-ko.md)
- Replacement Vertical Slice: [VS-05 언어 adapter 결과의 근거를 같은 기준으로 확인하기](../../../.tasks/2026-09-23-requested-flow-code-reading-delivery-ko/vs-05-check-language-adapter-evidence-consistently-ko.md)

## 1. 문제와 목표

현재 언어 adapter마다 소스 해석뿐 아니라 단계 분류와 관계 판단까지 구현하면, 언어별 결과가 달라질 수 있다. 이 구현 부담을 줄이는 책임 배분은 기존 언어별 VS가 맡고, 이 계약은 같은 최소 검증을 매번 다시 작성해야 하는 부담을 줄인다. 기존 adapter 테스트가 통과해도 실제 문장 범위와 실행 관계의 정확성을 충분히 보장하지 않는다. Go adapter의 현재 `slice`는 파일 전체를 한 단계로 내보내고, Dart와 TypeScript adapter에도 구문 분석과 별도로 정규식·휴리스틱 경로가 남아 있다.

- INT-01: 언어 adapter를 추가·변경할 때 공통 근거 검증을 언어마다 다시 만들지 않고, 지원 사례의 실행 의미 차이를 일찍 발견한다.
- GOAL-01: adapter 작성자는 공통 소스·참조 검사와 언어별 기대 실행 관계 사례를 한 검증 경로에서 실행하고, 실패한 파일·단계·관계를 구체적으로 알 수 있다.

## 2. 범위와 기존 계약의 책임

포함: 현재 지원하는 Dart·TypeScript·Go adapter의 공개 `slice` 결과와 snapshot을 입력으로 한 공통 소스·참조 검사, 언어별 소스 사례의 기대 문장·관계와 실제 결과 비교, 실패 위치·이유의 명시적 진단. 문법별 추출은 각 언어 도구가 맡는다.

제외: 새 언어 지원, 단일 언어 독립 parser, 새로운 사용자 FlowView 기능, 언어별 수집기 전면 재작성, 기존 공개 protocol·저장 형식 변경. 이 범위가 필요해지면 별도 호환성 결정을 먼저 기록한다.

기존 [코드 읽기 근거와 언어별 분석 품질](2026-09-23-code-reading-evidence-and-language-parity-ko.md)의 VS-02는 Dart 중간 문장, VS-03은 실행 관계, VS-05·06은 TypeScript·Go의 사용자 코드 읽기 결과와 공통 후속 처리의 적용을 각각 소유한다. 이 계약은 adapter의 단계 분류·관계 생성 코드를 공통 구현으로 옮기지 않고 그 사용자 결과를 새로 정의하거나 완료를 선언하지 않는다. adapter 작성자가 같은 입력 경계에서 근거의 적합성을 검사하는 **개발자 결과**만 소유한다. 기존 VS 파일과 계약 파일은 수정하지 않는다.

## 3. 주체와 전제

- 주체: 언어 adapter를 구현하거나 변경하는 개발자.
- 시작: 개발자가 동일 snapshot에서 생성한 adapter `slice` 결과에 적합성 검사를 실행한다.
- 전제: 공개 protocol로 전달된 snapshot과 해당 언어 사례의 기대 결과를 사용할 수 있다. source read-only 감사는 별도 검증 경계에 있다.
- 관찰 결과: 통과한 사실과 거부된 사실의 소스 위치·이유를 확인할 수 있다.

## 4. 확인된 근거와 가정

- 현재 Go `slice`는 파일 전체를 한 단계로 반환하며 간선을 만들지 않는다. 기존 adapter 테스트 통과는 코드 읽기 품질의 증거가 아니다.
- TypeScript adapter는 TypeScript parser를 사용하지만 일부 선언 탐색은 정규식을 사용한다. Dart adapter는 Dart parser와 별개로 일부 문장을 정규식으로 추출한다.
- ASM-01: 기존 `sliced-payload`와 analyzer v2 계약은 소스 범위·해시·참조 검사의 입력을 제공한다. 간선이 실제 AST에서 도출됐는지는 현재 payload만으로 증명할 수 없다. 언어별 기대 결과를 소스 사례에 명시하고 실제 반환값과 비교해 지원 사례의 의미를 검증한다. 검증 방법은 세 언어의 정상·오류 사례를 실행해 허위이지만 형식상 유효한 간선도 실패시키는 것이다.

## 5. 불변식

- INV-01: adapter는 언어 문법과 언어별 심볼 해석을 담당한다. 공통 경계는 언어와 무관한 소스 범위, 해시, 단계·관계 참조의 일관성을 검증한다. 실행 의미는 언어별 소스 사례의 독립적인 기대 결과와 비교한다.
- INV-02: 텍스트 유사성이나 인접 순서만으로 실행 관계를 확정하지 않는다. 미확인 관계는 미확인으로 남긴다.
- INV-03: 파일 전체 대체 단계와 다른 심볼 범위는 언어별 소스 사례의 기대 문장·심볼 범위와 비교해 거부한다. 공통 해시 검사가 이 의미를 홀로 증명한다고 주장하지 않는다.
- INV-04: adapter 적합성 통과만으로 특정 언어의 Requested Flow·CodeView 완료를 선언하지 않는다.
- INV-05: source read-only, secret redaction, 기존 protocol, 저장 View 호환성을 유지한다.

## 6. 사용자 흐름과 실패

1. 개발자가 저장소의 adapter 적합성 테스트를 실행한다. 테스트는 각 언어의 실제 `slice` 결과와 해당 snapshot을 사용한다.
2. 공통 검사는 소스 범위·해시·단계 참조를 판정한다. 언어별 사례는 기대 문장·심볼·직접 관계를 실제 결과와 비교한다.
3. 개발자는 각 사례의 통과 여부 또는 파일·심볼·단계·관계별 실패 이유를 테스트 결과에서 얻는다.
4. 실패한 결과는 언어 지원 품질의 완료 근거로 사용할 수 없다. 검사는 읽기 전용으로 실행하고 다른 adapter의 검사 결과를 바꾸지 않는다.

## 7. 결정

| ID | 결정 | 이유 |
|---|---|---|
| D-01 | 언어 도구가 구문을 해석하고 공통 경계가 소스·참조 일관성을 검증한다. | 언어 문법은 다르지만 확인 가능한 구조 규칙은 공유할 수 있다. |
| D-02 | 실행 관계는 언어별 소스 사례의 독립적인 기대 결과와 비교한다. | 현재 payload만으로는 adapter가 선언한 관계의 AST 출처를 증명할 수 없다. |
| D-03 | 기존 protocol과 snapshot을 우선 사용한다. | 이 계약만을 위해 공개 형식과 저장 호환성을 바꾸지 않는다. |

## 8. 기능 수용 기준

- FA-01: 하나의 저장소 테스트 경로는 Dart·TypeScript·Go adapter의 실제 `slice` 결과에 같은 공통 검사를 적용하고, 언어별 문법 규칙을 공통 검사에 복제하지 않는다.
- FA-02: 같은 snapshot의 올바른 소스 범위·해시·참조와 언어별 사례의 기대 문장·관계가 일치하는 결과는 통과한다.
- FA-03: 잘못된 해시·단계 참조는 공통 검사에서 실패한다. 다른 심볼, 파일 전체 대체 범위, 형식상 유효하지만 실제 실행과 다른 간선은 해당 언어의 소스 사례에서 실패한다. 각 실패는 대상과 이유를 포함한다.
- FA-04: 근거가 부족한 부분은 지원 완료로 보고하지 않는다. 한 adapter 사례의 실패 후에도 다른 언어 사례의 결과는 독립적으로 보고하고, 검사는 저장 View에 쓰지 않는다.
- FA-05: 테스트 결과는 공통 소스·참조 검사와 언어별 기대 관계 비교의 성공·실패를 구분한다. 통과를 CodeView 표시 성공으로 표현하지 않는다.

## 9. 품질과 검증

세 언어의 실제 adapter 출력을 호출하는 새 적합성 테스트를 마련하고, `internal/analyzer/protocol`, `internal/collector/slicing`, `internal/collector/evidence`의 기존 공통 경계 검사와 함께 실행한다. 소스 사례에는 기대 심볼·문장·관계와 금지 관계를 명시한다. 형식상 올바른 허위 간선도 거부되는 사례를 포함한다. source read-only는 일반 `slice` payload가 아닌 별도 기존 감사 경계에서 검증한다. Go 코드 변경 시 `make fmt`, `make vet`, `make test`를 실행한다. 공개 protocol 또는 저장 형식 변경이 발견되면 이 계약의 범위를 넘는다고 기록하고 호환성 결정을 먼저 다룬다.

## 10. Open Decisions

없음. 적합성 테스트는 기존 `internal/analyzer/protocol` 테스트 경계에서 실행하며 공개 형식은 바꾸지 않는다.

## 11. 완료 조건

FA-01~05의 실행 증거가 있고, 적합성 검사가 기존 VS-02·03·05·06의 언어별 사용자 결과를 대신하지 않음이 확인된다. 하위 VS는 독립 검토 후 제안 상태로 유지하고, 구현은 별도 승인에 따른다.
