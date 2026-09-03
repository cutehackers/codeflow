# 요청 흐름 이해와 실시간 Semantic Compiler — VS-01 지원 저장소 분석 근거를 검증해 받는다

- Contract ID: `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-VS-01`
- Contract Status: Proposed
- Independent Review: Passed (`gpt-5.6-terra`, high)
- Parent Contract: `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md`
- Created: 2026-09-02
- Source: `docs/design/raw/requested-flow-live-semantic-compiler-architecture-draft-ko.md`
- Decision Records: `docs/design/decisions/requested-flow-live-semantic-compiler-decisions-ko.md`

## 1. Intent and Goal

- Intent ID: `INT-01`
- Parent Intent: 사용자가 요청한 흐름을 현재 코드 근거와 unknown을 포함한 이해 가능한 결과로 파악한다.
- Goal ID: `GOAL-02`
- Slice Goal: Core caller가 Dart, TypeScript/JavaScript와 Go 지원 adapter에서 같은 분석 basis의 구조적 근거 또는 명시적 typed failure를 받는다.
- User or Caller Value: 상위 흐름 분석은 어떤 adapter가 어떤 입력을 읽었고 어떤 관계를 확인하지 못했는지 구분할 수 있다.
- Contribution to Parent: 모든 후속 SemanticMap과 live generation이 사용할 구조적 evidence, capability, basis와 failure 경계를 고정한다.
- Parent Acceptance: `A9`, `A25–A27` 및 raw §21.8

## 2. User Outcome

Core, CLI 또는 MCP caller는 지원 adapter를 통해 분석을 요청하고, raw protocol 계약으로 검증된 operation result를 받거나 재시도·호환성·입력 오류를 구분한 typed failure를 받는다. Adapter가 반환한 근거는 snapshot basis, read set, capability와 unknown 경계를 포함한다.

## 3. Scope

### In Scope

- Content-Length framed JSON-RPC 2.0 over stdio의 initialize와 capability negotiation
- adapter request ID, cancellation, batch acknowledge와 typed error 경계
- Dart, TypeScript/JavaScript와 Go native adapter 및 공통 conformance 계약
- snapshot content overlay, `computedBasisId`, workspace epoch, Analysis Read Set, negative lookup, membership, dependency frontier, Causal Observation Closure와 analyzer version 반환
- max message size, backpressure, progress와 bounded diagnostic
- adapter process isolation과 analyzer failure의 typed result/coverage 경계
- JSON Schema, valid·invalid fixture, Semantic Validator, producer-consumer compatibility와 Contract Registry 등록

### Out of Scope

- SemanticMapIR, Semantic Delta, Requirement Alignment 또는 FlowView의 사용자 projection — VS-02 이후 slice가 담당한다.
- immutable workspace 생성과 publication scheduler — VS-03과 VS-04가 담당한다.
- 다른 framing/protocol version의 bridge, probe 또는 mixed-version release set — raw §21.8의 adapter boundary를 벗어난다.
- adapter별 분석 정확도 개선과 새 언어 지원 — 별도 계약이 필요하다.

## 4. Preconditions

- Raw P0 Exit Gate와 VS-02 P1 baseline seam이 존재한다.
- Core와 공식 adapter가 raw §21.8의 protocol과 capability를 선언한다.
- 분석 요청은 repository, workspace epoch와 snapshot overlay를 식별한다.
- schema와 fixture를 등록할 Contract Registry 경로가 존재한다.
- product source는 read-only로 제공된다.

## 5. Public Seam

Core-adapter stdio protocol, CLI·MCP 분석 호출, adapter result와 typed failure가 공개 seam이다. Adapter stdout은 protocol frame만 내보내고 diagnostic은 stderr로 제한한다. Caller는 validation을 통과한 result만 완료된 분석 근거로 사용한다.

## 6. Boundary Coverage

Core caller → initialize/capability 검증 → snapshot-aware adapter request → structural evidence와 observation metadata → schema·semantic validation → typed result 또는 bounded failure → caller-visible analysis result.

## 7. Inherited Invariants

- `INV-01`, `INV-03`, `INV-07`, `INV-14`, `INV-15`, `INV-17`, `INV-19`
- Raw D18, D25, D26 및 §10.0, §21.5, §21.8
- Adapter result는 구조적 Fact의 후보이며 model 또는 agent가 Fact authority가 될 수 없다.

## 8. Slice-Specific Rules

- initialize와 capability negotiation이 유효하지 않으면 Core는 분석 operation을 보내지 않는다.
- malformed framing, unsupported capability, unknown schema version과 invalid params는 explicit typed failure가 된다.
- cancellation, timeout 또는 stale workspace의 progress와 result는 cache, staging, generation에 반영하지 않는다.
- message size와 in-flight work는 bounded policy와 backpressure를 따르며 unbounded allocation을 허용하지 않는다.
- error detail과 diagnostic은 secret redaction 후 등록된 schema와 payload bound를 지킨다.

## 9. Acceptance Criteria

- VS01-A1. WHEN Core가 adapter를 시작하면, THE system SHALL Content-Length framed JSON-RPC 2.0 initialize와 capability negotiation을 완료한 뒤에만 analysis operation을 보낸다.
- VS01-A2. IF framing, protocol version, capability 또는 request parameter가 유효하지 않으면, THEN THE system SHALL explicit typed failure를 반환하고 유효하지 않은 analysis artifact를 만들지 않는다.
- VS01-A3. WHEN Core가 지원 Dart, TypeScript/JavaScript 또는 Go adapter에 analysis operation을 요청하면, THE system SHALL operation result와 `computedBasisId`, workspace epoch, Analysis Read Set, closure observation과 analyzer version을 반환한다.
- VS01-A4. WHEN caller가 request를 취소하거나 deadline이 만료되면, THE system SHALL cancellation을 전달하고 이후 도착한 progress와 result를 cache·staging·generation에 포함하지 않는다.
- VS01-A5. THE system SHALL request ID, batch acknowledge, max message size와 backpressure를 검증 가능한 protocol behavior로 제공한다.
- VS01-A6. IF message size, concurrency 또는 adapter process bound를 초과하면, THEN THE system SHALL bounded typed failure 또는 backpressure를 반환하고 unbounded allocation을 만들지 않는다.
- VS01-A7. IF adapter process 또는 analyzer가 실패하면, THE system SHALL isolated typed failure와 영향을 받은 capability/coverage를 반환하며 실패한 결과를 current artifact로 발행하지 않는다.
- VS01-A8. THE system SHALL adapter stdout/stderr와 diagnostic payload에서 secret을 redacted bounded form으로 유지하고 product source를 수정하지 않는다.
- VS01-A9. THE system SHALL boundary payload에 등록된 `schemaId`와 `schemaVersion`, valid·invalid fixture, Semantic Validator 규칙과 producer-consumer compatibility test를 제공한다.

## 10. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| malformed frame 또는 protocol mismatch | protocol failure와 peer 상태 | invalid artifact 없음 | 호환 protocol/adapter 경계로 재시도 |
| capability 불일치 | unsupported/incompatible 상태 | analysis operation 전송 없음 | 지원 capability 사용 또는 adapter 보강 |
| cancel, timeout 또는 stale epoch | cancelled/timeout 상태 | late output 저장·발행 안 함 | 현재 epoch에서 새 request |
| invalid request/result schema | invalid typed result | canonical artifact 없음 | request 또는 adapter 계약 수정 |
| adapter/analyzer crash | isolated typed failure와 coverage gap | 실패 결과 current 발행 없음 | process 상태 복구 후 caller 재요청 |
| message/concurrency bound 초과 | queue 또는 backpressure failure | unbounded allocation 없음 | capacity 회복 후 제한된 retry |

## 11. Data and Interaction Contract

- Input: JSON-RPC request, repository/workspace epoch, `computedBasisId`, snapshot content overlay와 operation params.
- Output: operation-specific structural result, capability profile, Analysis Read Set, negative lookup·membership·dependency frontier·Causal Observation Closure 또는 typed error.
- Persistence: schema와 semantic validation을 통과한 protocol evidence만 후속 slice의 staging/CAS가 사용할 수 있다. partial output은 canonical artifact가 아니다.
- External interaction: adapter subprocess stdio만 허용한다. adapter는 product source를 수정하지 않는다.
- Compatibility: protocol/schema version 변경은 명시적 compatibility 또는 migration 계약이 필요하며 incompatible payload는 canonical artifact가 아니다.

## 12. Test Seam and Evidence

- Public seam: Core-adapter stdio, CLI·MCP analysis operation과 typed result/failure.
- Required test level: schema fixture, framing·correlation·cancellation·batch acknowledge·bound·backpressure·redaction, Dart/TypeScript/Go conformance와 isolated process failure.
- Replaceable external boundaries: adapter process, stdio, clock, process launcher와 compatibility registry.
- Evidence per criterion: VS01-A1/A2 handshake·invalid request fixture, A3 adapter conformance와 Core E2E, A4 cancellation·stale test, A5/A6 bound/backpressure fixture, A7 process-failure fixture, A8 redaction/read-only test, A9 registry·schema·compatibility fixture.

## 13. Verification Plan

| Check | Applicability / Trigger | Exact Command | Expected Evidence | Required for Completion |
|---|---|---|---|---|
| Acceptance behavior | Always | `go test ./internal/protocol ./internal/e2e` | handshake, typed error, stale-output rejection, adapter evidence behavior | Yes |
| Slice tests | Always | `go test ./internal/protocol` | VS-01 protocol and conformance tests pass | Yes |
| Type, static analysis, and lint | Go code or schema loader affected | `go vet ./...` | no applicable findings | When applicable |
| Affected build | Core or adapter entrypoint affected | `go build ./...` | Core builds successfully | When applicable |
| Architecture and policy | Contract boundary changes | `go test ./internal/contractharness -run 'TestValidateGoldenFixtures|TestValidateExportedContractBoundary'` | registered adapter schema fixtures and boundary validation pass | Yes |
| Regression | Core, adapter or protocol behavior affected | `go test ./...` | repository regression suite passes | Yes |
| Security | diagnostics, stderr or source payload crosses a process boundary | `go test ./internal/secret ./internal/protocol ./internal/e2e` | redaction, bounded diagnostic and no unsafe publication | Yes |
| Data and migration compatibility | adapter message schema and compatibility registry affected | `go test ./internal/contractharness` | valid·invalid schema and semantic compatibility fixtures pass | Yes |
| Performance and concurrency | frame/message and backpressure bounds are part of the contract | `go test ./internal/protocol -run '^Test' -count=1` | bounded message and concurrency tests pass | Yes |
| Reliability and flake | cancellation, timeout or process-failure path affected | `go test ./internal/protocol ./internal/e2e -count=1` | repeatable cancellation, late-result and process-failure evidence | Yes |
| Coverage | No numeric coverage threshold is defined for this slice | `N/A — raw contract defines conformance cases, not a coverage percentage` | reason recorded | No |
| Dart adapter conformance | Dart adapter is part of the supported adapter set | `cd adapters/dart && dart test` | Dart adapter suite passes | Yes |
| TypeScript adapter conformance | TypeScript/JavaScript adapter is part of the supported adapter set | `node adapters/typescript/test/index.test.js` | TypeScript/JavaScript adapter suite passes | Yes |
| Go adapter conformance | Go adapter is part of the raw target adapter set | `go test ./adapters/go/...` | Go adapter protocol, snapshot/read-set and typed-failure conformance suite passes | Yes |

## 14. What Could Be Wrong

- Assumption: adapter operation results can represent the language-neutral evidence needed by the baseline slice.
- Consequence: protocol conformance passes while critical candidate, anchor or unknown data is lost.
- Validation method: Dart, TypeScript/JavaScript와 Go golden repository에서 candidate, edge, source anchor, unknown과 observation metadata를 함께 검증한다.

## 15. Done When

- VS01-A1–A9 evidence is passing.
- malformed framing, unsupported capability, invalid schema와 bound 초과가 typed failure/backpressure로 처리된다.
- 모든 applicable Verification Plan row가 passing evidence를 가지며, N/A row에는 이유가 남는다.
- 후속 slice가 사용할 snapshot basis와 observation metadata가 adapter boundary에서 손실 없이 조회된다.
- 이 계약은 구현 승인 상태가 아니며, production code와 schema는 이 slicing task에서 변경하지 않는다.

## 16. Open Decisions

없음. raw 스팩의 승인된 D1–D32와 adapter cutover 결정 기록을 따른다.
