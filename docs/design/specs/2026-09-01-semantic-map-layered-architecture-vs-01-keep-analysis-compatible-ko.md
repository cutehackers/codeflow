# Semantic Map Layered Architecture — VS-01 분석 호환성 유지

- Contract ID: `SMAP-VS-01`
- Contract Status: Proposed
- Independent Review: Passed
- Parent Contract: `docs/design/specs/2026-09-01-semantic-map-layered-architecture-ko.md`
- Created: 2026-09-01
- Depends On: `docs/spec/llm-language-adapter-protocol.md`와 `schemas/adapter-protocol.schema.json`의 versioned JSON-RPC 2.0·NDJSON v1 bridge 개정 승인
- Parent Acceptance Coverage: FA-15, FA-18

## 1. User Outcome

Dart 또는 TypeScript/JavaScript repository를 분석하는 caller는 adapter protocol 전환 중에도 기존 분석 명령을 계속 사용할 수 있고, 사용 중인 protocol과 migration 상태를 확인할 수 있다.

## 2. Scope

### In Scope

- Framed JSON-RPC 2.0 adapter protocol의 새 version
- NDJSON v1 bridge와 종료 기준 기록
- Dart와 TypeScript/JavaScript adapter migration
- handshake, correlation, cancellation, backpressure, timeout, message-size와 typed error compatibility
- CLI와 MCP 분석 호출에서 protocol·adapter capability 상태 노출
- 두 adapter에 공통으로 적용되는 protocol compatibility suite

### Out of Scope

- `SemanticMapIR`과 새 FlowView UX — VS-02가 담당한다.
- Runtime scenario 실행 — VS-03이 담당한다.
- Optional model 설치 또는 호출 — VS-05가 담당한다.
- NDJSON v1 bridge 제거 — 종료 기준 충족과 별도 compatibility 승인 전에는 수행하지 않는다.

## 3. Preconditions

- 부모 계약 `SMAP`이 Approved 상태다.
- Adapter protocol 개정 계약이 JSON-RPC 2.0 framing, method, error, cancellation과 bridge 규칙을 확정하고 Approved 상태다.
- Versioned adapter protocol schema와 valid·invalid bridge fixture가 승인된 개정 계약을 강제한다. 문서 승인만으로 schema prerequisite가 충족된 것으로 간주하지 않는다.
- 현재 NDJSON v1 schema, Dart adapter, TypeScript/JavaScript adapter와 compatibility pin이 식별 가능하다.

## 4. Public Seam

- 기존 CLI의 `flows`, `publish`와 관련 분석 호출
- MCP의 `harvest_flows`, `analyze_flow`, `publish_core_flow`
- Core와 adapter 사이의 version handshake와 분석 request/response
- `doctor` 또는 동등한 capability 진단 결과

Caller는 private process 관리 방식이 아니라 분석 결과, protocol version, adapter version, capability와 typed failure를 관찰한다.

## 5. Boundary Coverage

CLI 또는 MCP 분석 요청 → Core version negotiation과 bridge 선택 → Dart 또는 TypeScript/JavaScript adapter 분석 → schema·secret validation → 기존 분석 결과 또는 명시적 compatibility failure

## 6. Inherited Invariants

- INV-09 Generation Consistency
- INV-10 Stale Isolation
- INV-11 Source Read-Only
- INV-14 Schema Authority
- INV-17 Protocol Migration

## 7. Slice-Specific Rules

- BR-01: Core는 handshake 결과에 따라 지원되는 protocol을 선택하고 caller 요청마다 protocol을 임의로 바꾸지 않는다.
- BR-02: Bridge 기간에는 현재 유효한 NDJSON v1 adapter와 JSON-RPC 2.0 adapter를 모두 지원한다.
- BR-03: JSON-RPC 2.0 framing, cancellation과 error 의미는 승인된 adapter protocol 개정 계약이 유일한 권위다.
- BR-04: Dart와 TypeScript/JavaScript adapter는 동일한 protocol compatibility suite를 통과해야 한다.
- BR-05: Unsupported, malformed, oversized, timed-out 또는 cancelled response는 분석 성공이나 publish 성공으로 변환하지 않는다.
- BR-06: NDJSON v1 종료 기준에는 지원 release 범위, adapter migration 완료, 사용자-visible deprecation과 rollback 방법이 포함되어야 한다.

## 8. Acceptance Criteria

- A1. WHEN caller가 JSON-RPC 2.0을 지원하는 Dart 또는 TypeScript/JavaScript adapter로 기존 분석을 요청하면, THE system SHALL schema-valid 분석 결과와 사용한 protocol·adapter version을 반환한다.
- A2. WHILE NDJSON v1 bridge가 지원 상태이면, THE system SHALL 기존 valid v1 adapter의 분석 결과를 기존 CLI와 MCP caller에 계속 제공한다.
- A3. IF Core와 adapter 사이에 지원 가능한 protocol version이 없으면, THEN THE system SHALL 명시적인 unsupported-version failure와 지원 범위를 반환하고 분석 artifact를 발행하지 않는다.
- A4. WHEN caller가 진행 중인 JSON-RPC 2.0 분석을 취소하면, THE system SHALL 취소된 결과를 active generation에 포함하지 않는다.
- A5. IF adapter response가 malformed, oversized, timeout 또는 correlation 불일치이면, THEN THE system SHALL typed failure를 반환하고 이전 valid generation을 유지한다.
- A6. THE system SHALL Dart와 TypeScript/JavaScript adapter에 같은 handshake, cancellation, timeout, backpressure, crash와 schema compatibility evidence를 제공한다.
- A7. THE system SHALL 기존 CLI, MCP와 adapter consumer별 protocol version, migration 상태와 지원 종료 조건을 확인 가능하게 한다.

## 9. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| 공통 protocol version 없음 | Unsupported-version failure와 지원 version 표시 | 분석 또는 generation 발행 없음 | 지원 adapter 설치 또는 bridge 설정 후 재시도 |
| v2 frame 또는 response schema 위반 | Protocol failure와 adapter stderr의 제한된 진단 정보 | 해당 connection을 재사용하지 않음 | Adapter 수정 또는 제한된 재시작 |
| Request timeout 또는 cancellation | Timeout 또는 cancelled 상태 | 늦은 response와 staging result 폐기 | 현재 workspace epoch에서 새 요청 |
| Adapter crash | Partial 또는 unavailable capability 표시 | 이전 valid generation 유지 | 제한된 재시작 후 circuit open |
| v1 bridge 설정 손상 | Compatibility 진단 실패 | v1 adapter 호출 없음 | Pin과 bridge 설정 복구 |

## 10. Data and Interaction Contract

- Input: repository root, language 또는 detected language, operation, request ID, timeout, cancellation token과 operation-specific params
- Output: protocol version, adapter version, capability, schema-valid operation result 또는 closed typed error
- Persistence: adapter pin과 compatibility state는 versioned artifact로 기록하며 product source를 변경하지 않는다.
- Compatibility: v1과 v2 result는 같은 language-neutral candidate와 sliced-payload semantic contract로 정규화한다.
- Migration: Dart와 TypeScript/JavaScript의 migration 상태를 개별 기록하며 하나의 adapter 완료를 전체 migration 완료로 간주하지 않는다.

## 11. Test Seam and Evidence

- Public seam: CLI process, MCP JSON-RPC request, Core-adapter stdio contract
- Required test level: JSON Schema fixtures, Go protocol conformance, Dart protocol test, TypeScript adapter test, CLI/MCP integration과 crash·timeout adversarial test
- Replaceable external boundaries: mock adapter process, clock, process launcher와 stdio
- Evidence required per criterion:
  - A1, A2, A6: Dart·TypeScript compatibility matrix와 end-to-end result
  - A3, A5: invalid fixture와 이전 generation 보존 assertion
  - A4: cancellation 후 publish absence assertion
  - A7: doctor 또는 capability response snapshot

## 12. What Could Be Wrong

- Assumption: v1과 v2 response를 같은 candidate와 sliced-payload 의미로 정규화할 수 있다.
- Consequence: caller가 protocol에 따라 다른 분석 결과를 받거나 bridge가 private 차이를 숨긴다.
- Validation method: 같은 repository fixture와 operation을 v1·v2 adapter로 실행해 canonicalized result와 failure 의미를 비교한다.

## 13. Done When

- Every criterion has passing evidence.
- `go test ./...`, `cd adapters/dart && dart test`, `node adapters/typescript/test/index.test.js`가 통과한다.
- Dart와 TypeScript/JavaScript migration evidence와 NDJSON v1 종료 기준이 기록된다.
- No contract requirement is weakened.
- No unrelated behavior changes.

## 14. Open Decisions

없음.
