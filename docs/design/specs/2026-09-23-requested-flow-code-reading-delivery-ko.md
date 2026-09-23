# 요청 흐름을 실제 코드로 읽는 구현 통합 계약

- Contract ID: REQUESTED-FLOW-CODE-READING-DELIVERY
- Contract Status: Approved
- Intent Status: Hardened
- Created: 2026-09-23
- Source: 2026-09-23 사용자 재정리 요청, 현재 구현 조사, 아래 관련 계약
- Decision Record: [요청 흐름 코드 읽기 통합 결정](../decisions/2026-09-23-requested-flow-code-reading-delivery-ko.md)
- Glossary: [CodeFlow 설계 용어집](../glossary.md)
- Related Contracts: [요청 흐름 확정과 분석 신뢰성](2026-09-21-requested-flow-resolution-reliability-ko.md), [Requested Flow 관문과 실행 타임라인](2026-09-18-flowsequence-requested-flow-hierarchy-ko.md), [FlowView 코드 이해](2026-09-14-flowview-code-comprehension-ko.md)
- Replaces Proposed Implementation Plans: [코드 읽기 근거와 언어별 분석 품질](2026-09-23-code-reading-evidence-and-language-parity-ko.md), [언어 adapter 공통 분석 적합성](2026-09-23-language-adapter-conformance-ko.md), 2026-09-21 계약의 제안 상태 VS-04

## 1. 문제와 목표

요청한 흐름을 잘못 선택하거나, analyzer가 실제 문장을 누락하거나, 관계를 추정하거나, FlowView가 해당 코드를 열지 못하면 사용자는 그럴듯한 화면을 보면서도 구현을 잘못 이해한다. 기존 제안 VS는 이 문제를 계약별로 반복 기술해 구현 순서와 완료 주체가 불명확하다.

- INT-01: 선택된 Requested Flow를 실제 문장·확인된 관계·같은 snapshot의 코드로 읽을 수 있게 한다.
- GOAL-01: 사용자는 Dart 요청의 중요한 중간 처리와 실행 경계를 실제 코드로 읽는다.
- GOAL-02: 사용자는 기본 항목과 상세 단계에서 설명에 맞는 정확한 코드를 연다.
- GOAL-03: 사용자는 검증된 상세를 잃지 않는 짧은 읽기 경로를 따른다.
- GOAL-04: adapter 작성자는 공통 근거 검사로 언어별 결과의 지원 범위와 실패를 확인한다.
- GOAL-05: 사용자는 지원 범위의 Go·TypeScript 요청도 같은 근거 규칙으로 읽는다.

## 2. 범위와 권위

포함: Dart 문장·직접 관계 개선, 항목별 CodeView 표시, 1:N 상세 보존, 언어별 공통 근거 검사, Go와 TypeScript의 독립적인 요청→코드 읽기 경로. 제외: 모든 문장·언어·프레임워크의 완전 지원 주장, 단일 언어 독립 parser, 새 화면 디자인, 런타임 추적, 과거 View의 소급 재작성, 이 계약만으로 production-ready 선언.

2026-09-21 계약은 요청 확정, 실패 상태, `architecture` 금지, 대표 코드 미표시 시 저장 중단의 권위다. 그 VS-01은 구현됐고 VS-02·03은 구현 후 독립 검토 중이다. 이 계약은 그 작업을 재구현하거나 완료로 간주하지 않는다. 2026-09-18 계약은 관문·조건·비교·선택 복원·사용자 검증의 권위다. 진행 중인 그 VS-01~05를 삭제하거나 완료로 바꾸지 않는다. 2026-09-14 계약의 명시적 재분석·선택 baseline·직접 관계 탐색은 유지하되, `architecture` 관련 표현은 2026-09-21의 최신 규칙을 따른다.

이 계약은 위 공개 동작을 바꾸지 않고 **남은 코드 읽기 구현의 단일 작업 순서와 수용 근거**를 소유한다. 언어별 문법·심볼·직접 관계 구현은 계속 필요하다. 공통화로 줄이는 반복 작업은 snapshot 소스 범위·해시·참조 검증, 근거를 보존하는 요약·저장·표시, 언어별 결과를 같은 방식으로 판정하는 검사다. 언어별 분석기 구현량 자체가 감소했다고 주장하려면 VS-06·07에서 중복 코드와 수작업 규칙의 전후 범위를 별도로 확인한다. 기존 공개 protocol·schema·저장 형식 변경이 필요하면 소비자·과거 View 호환성을 먼저 확인하고 별도 결정을 기록한다.

## 3. 이전 제안과 새 VS의 대응

| 이전 제안 | 새 소유자 | 처리 |
|---|---|---|
| 2026-09-21 VS-04, 2026-09-23 코드 읽기 VS-01 | VS-03 | 요청·선택·실패·CodeView를 하나의 화면 결과로 검증 |
| 2026-09-23 코드 읽기 VS-02 | VS-01 | Dart 중간 문장과 소스 범위 |
| 2026-09-23 코드 읽기 VS-03 | VS-02 | 조건·반복·예외·취소·비동기 직접 관계 |
| 2026-09-23 코드 읽기 VS-04 | VS-04 | 새로 보존한 중간 단계의 1:N 상세 접근 |
| 2026-09-23 adapter 적합성 VS-01 | VS-05 | 공통 구조 검사와 언어별 기대 결과 비교 |
| 2026-09-23 코드 읽기 VS-06·05 | VS-06·07 | Go·TypeScript 각각의 사용자 경로 |

2026-09-18의 승인된 VS는 계속 별도 사용자 결과를 소유한다. 이 계약의 VS-04는 그 관문·복귀 기능 전체를 중복 구현하지 않고 **VS-01에서 추가된 문장이 상세 코드까지 보존되는지** 확인한다. 2026-09-14의 제안 상태 VS는 현재 구현 순서가 아니다. 선택 비교와 명시적 재분석은 2026-09-18 VS-04 및 기존 계약의 완료 조건으로 남는다.

## 4. 확인된 사실과 가정

- Go adapter의 현재 `slice`는 파일 전체를 단일 단계로 내보내고 직접 관계를 만들지 않는다.
- Dart adapter는 Dart parser를 사용하지만 일부 실행 문장을 정규식으로 찾는다. TypeScript adapter는 compiler parser를 사용하면서 일부 선언 탐색·대상 해석에 휴리스틱을 사용한다.
- 기존 adapter 테스트 통과는 실제 FlowView 항목의 정확한 코드 표시나 간선의 AST 출처를 증명하지 않는다. 현재 `sliced-payload`만으로 구조상 유효한 거짓 간선을 보편적으로 판정할 수 없다.
- 이전 Molycard 조사에서 67개 항목에 소스 줄은 보였지만 정확한 줄 강조는 17개였다. 구현 전 현재 공개 경로에서 다시 측정한다.
- ASM-01: 언어별 parser는 문법·심볼·직접 관계를 추출하고, 공통 Core는 snapshot 근거·참조·요약·저장·표시를 처리할 수 있다. 거짓이면 VS-05의 세 언어 실제 결과와 VS-06·07의 사용자 경로에서 드러난다. 단일 parser로 모든 언어 문법을 대체하지 않는다.

## 5. 불변식

- INV-01: 원문 요청·선택 근거·심볼·문장·관계·CodeView는 동일 snapshot과 computed basis를 가리킨다.
- INV-02: 확인하지 못한 진입점·문장·관계는 파일 전체 단계, 동명 심볼, 인접 순서, 가짜 결과로 대체하지 않는다.
- INV-03: 동작을 바꾸는 중간 문장은 수집·보존하되 모든 문장의 실행이나 `await` 성공을 확정하지 않는다.
- INV-04: 상위 읽기 경로가 짧아져도 검증된 상세 단계와 실제 코드로 이동할 수 있다.
- INV-05: 언어별 adapter는 문법 사실을 맡고 Core는 공유 가능한 검증·큐레이션·표시를 맡는다. 적합성 검사는 소스·해시·참조를 검사하며 언어별 실행 의미는 독립 기대 사례로 검증한다.
- INV-06: source read-only, authorization, secret redaction, 과거 View 복원, 저장·HTTP·MCP 호환성과 명시적 재분석·비교·읽기 위치 보존을 유지한다.
- INV-07: 새 결과의 선정·정렬·화면에 폐기된 `architecture` 필드를 사용하지 않는다.

## 6. 구현 순서

선행: 2026-09-21 VS-02·03의 독립 검토와 결함 수정. 이 완료를 새 VS로 중복 발급하지 않는다.

| 순서 | VS | 주 결과 | 의존성 |
|---|---|---|---|
| 1 | [VS-01](../../../.tasks/2026-09-23-requested-flow-code-reading-delivery-ko/vs-01-read-dart-intermediate-statements-ko.md) | Dart의 중요한 중간 문장을 실제 소스로 읽음 | 2026-09-21 VS-02·03의 검증된 기준 |
| 2 | [VS-02](../../../.tasks/2026-09-23-requested-flow-code-reading-delivery-ko/vs-02-read-proven-dart-execution-relations-ko.md) | 확인된 조건·예외·비동기 관계만 따라 읽음 | VS-01 |
| 3 | [VS-03](../../../.tasks/2026-09-23-requested-flow-code-reading-delivery-ko/vs-03-open-exact-code-for-requested-flow-ko.md) | 요청한 흐름의 모든 기본 항목에서 정확한 코드 표시 | VS-01·02, 2026-09-21 선택 계약 |
| 4 | [VS-04](../../../.tasks/2026-09-23-requested-flow-code-reading-delivery-ko/vs-04-read-short-path-with-complete-detail-ko.md) | 짧은 관문에서 상세 단계를 잃지 않고 코드로 이동 | VS-03, 2026-09-18 관문·상태 계약 |
| 5 | [VS-05](../../../.tasks/2026-09-23-requested-flow-code-reading-delivery-ko/vs-05-check-language-adapter-evidence-consistently-ko.md) | 공통 검사에서 언어별 사실의 통과·실패 판정 | VS-01·02의 기준 사례. Go·TypeScript 실패는 진단으로 기록 |
| 6 | [VS-06](../../../.tasks/2026-09-23-requested-flow-code-reading-delivery-ko/vs-06-read-go-request-through-source-ko.md) | Go 요청을 실제 코드로 읽음 | VS-03~05 |
| 7 | [VS-07](../../../.tasks/2026-09-23-requested-flow-code-reading-delivery-ko/vs-07-read-typescript-request-through-source-ko.md) | TypeScript 요청을 실제 코드로 읽음 | VS-03~05. VS-06과 병행 가능 |

## 7. 기능 수용 기준

- FA-01: 확인된 Dart 요청에서 결과를 바꾸는 선언·대입·변환·호출·반환이 해당 문장의 정확한 소스 범위로 남는다.
- FA-02: 조건·반복·예외·취소·`await`는 확인된 직접 관계와 미확인 경계를 구별하며, 실행 불가능한 후속 처리를 정상 실행으로 연결하지 않는다.
- FA-03: 확정된 요청의 모든 기본 항목과 상세 단계는 설명한 심볼·문장을 같은 snapshot의 CodeView에서 열고, 대표 코드 검증 실패 시 새 View를 저장하지 않는다.
- FA-04: FlowSequence와 상세 단계는 중요한 판단·변환·효과·결과 또는 경계를 누락하지 않고 저장·HTTP·MCP에서 같은 소속·순서·근거를 유지한다.
- FA-05: 세 adapter의 실제 `slice` 결과가 같은 소스·해시·참조 검사와 언어별 기대·금지 관계 사례에서 판정되고, 실패가 다른 언어 결과를 바꾸지 않는다.
- FA-06: Go receiver·동명 메서드·오류 반환 사례에서 파일 전체 대체 단계 없이 요청부터 결과 또는 명시적 경계까지 실제 코드로 읽는다.
- FA-07: TypeScript 함수·TSX 이벤트·조건·변환·비동기 실패 사례에서 요청부터 결과 또는 명시적 경계까지 실제 코드로 읽는다.
- FA-08: 실패·모호성·부분 분석은 정상 Requested Flow로 저장·표시하지 않으며, 이전 View·선택·비교 기준을 유지한다.

## 8. 품질과 출시 판단

각 VS는 실제 adapter 반환값, 공통 계약, 저장·HTTP·MCP 왕복, 해당 FlowView 선택을 자신의 범위에 맞게 검증한다. 전체 완료 전 `make fmt`, `make vet`, `make check-naming`, `make test`, `make test-flowview`, `make test-ui`, `make build`, `cd adapters/dart && dart test`, `cd adapters/typescript && npm test`, `go test ./adapters/go/...`를 실행한다. 저장·MCP 변경은 과거 View 호환과 race를 검증한다. Molycard와 Go·TypeScript의 실제 요청은 기본·상세 항목을 각각 선택해 CodeView의 코드 범위까지 확인한다.

이 계약의 구현 완료만으로 production-ready를 선언하지 않는다. 2026-09-18 VS-01~05의 남은 수용 기준, 실제 개발자의 코드 이해 과제, 성능 기준·지원 범위 결정, 대상 플랫폼 설치·배포 검증은 별도 출시 조건이다. 합격 수치나 미지원 문법의 완료를 추정하지 않는다.

## 9. Open Decisions

없음. 공개 protocol 또는 저장 migration이 필요한 구현은 기존 consumer 조사와 별도 호환성 결정이 먼저다. 출시 성능 수치와 지원 범위는 2026-09-18 계약의 OPEN-UX-02에서 확정한다.

## 10. 완료 조건

FA-01~08에 현재 실행 증거가 있고, 선행 2026-09-21 검토가 종료되며, 각 새 VS가 독립적으로 검토·승인·검증된다. 기존 구현 기록과 과거 View를 변경하지 않는다.
