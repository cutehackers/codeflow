# Adapter Protocol JSON-RPC 2.0 전면 전환

- Contract ID: `ADAPTER-PROTOCOL-V2-MIGRATION`
- Contract Status: Approved
- Created: 2026-09-01
- Updated: 2026-09-02
- Approved: 2026-09-02
- Approval Basis: Legacy backward compatibility 제외 정책 고정 후 사용자 명시 승인 및 독립 재검토 PASS
- Source: 2026-09-01 Q1~Q9 결정 및 2026-09-02 legacy 호환 제외 결정
- Parent Contract: `docs/design/specs/2026-09-01-semantic-map-layered-architecture-ko.md`
- Affects: `docs/spec/llm-language-adapter-protocol.md`, `schemas/adapter-protocol.schema.json`, Core, Dart adapter, TypeScript/JavaScript adapter, installer와 `doctor`

이 계약은 CodeFlow Core와 공식 language adapter 사이의 통신을 Content-Length framed JSON-RPC 2.0으로 전면 교체하는 public behavior를 정의한다. 이 전환은 다음 MAJOR release의 breaking change이며 NDJSON v1 adapter, bridge 또는 mixed-version compatibility를 제공하지 않는다.

## 1. Goal

CodeFlow caller는 취소, 진행 상태, 큰 결과 전송, typed failure와 bounded concurrency를 지원하는 하나의 JSON-RPC 2.0 adapter protocol을 사용해 Dart와 TypeScript/JavaScript repository를 분석할 수 있어야 한다.

이 전환의 우선순위는 이전 protocol 호환이 아니라 코드 이해의 정확한 근거, 확장 가능한 분석 경계와 측정 가능한 성능이다.

## 2. Scope

### In Scope

- JSON-RPC 2.0 request, response, notification과 correlation
- LSP-style Content-Length framing over stdio
- V2 version·capability handshake
- `ping`, `detect`, `harvest_candidates`, `slice`, `shutdown`
- Cancellation, progress와 large-result chunk notification
- Closed CodeFlow domain error, retryability와 redacted·bounded diagnostic
- 1MiB framed-message limit과 connection당 최대 64 in-flight request
- V2 message, capability, error와 result schema 및 valid·invalid fixture
- Core v2 client, Dart native v2와 TypeScript/JavaScript native v2
- Installer의 MAJOR 전환, 실제 handshake 기반 `doctor`, full-release rollback과 release verification

### Out of Scope

- NDJSON v1 bridge, fallback, adapter 지원 또는 v1·v2 canonical equality 검증 — legacy 호환을 제공하지 않는다.
- Mixed Core·adapter version 조합 — release set 전체가 같은 v2 compatibility contract를 사용한다.
- Adapter가 생성하는 분석 결과의 v1 wire-shape 보존 — v2 schema가 새 권위다.
- Semantic Map artifact 구현 — SMAP slice가 담당한다.
- Runtime observer protocol — 별도 contract가 필요하다.
- Network transport, UDS와 Windows Named Pipe — 이 계약은 stdio만 다룬다.
- Protocol 전환과 무관한 analyzer 정확도 개선 — adapter별 별도 slice가 필요하다.

## 3. Confirmed Current State

- CF-01 `[Confirmed]` 현재 Core와 adapter는 NDJSON v1 over stdio를 사용한다. 근거: `internal/protocol/`, `schemas/adapter-protocol.schema.json`.
- CF-02 `[Confirmed]` 현재 operation은 `ping`, `detect`, `harvest_candidates`, `slice`, `shutdown`이다. 근거: `docs/spec/llm-language-adapter-protocol.md`.
- CF-03 `[Confirmed]` 현재 request ID, timeout, cancellation token, typed error와 retryability seam이 존재한다. 근거: `docs/spec/llm-language-adapter-protocol.md`, `internal/protocol/`.
- CF-04 `[Confirmed]` 현재 단일 message limit은 1MiB이고 최대 concurrent request 계약은 64개다. 근거: `docs/spec/llm-language-adapter-protocol.md`.
- CF-05 `[Confirmed]` Dart와 TypeScript/JavaScript adapter 및 compatibility pin이 존재한다. 근거: `adapters/dart/`, `adapters/typescript/`, `internal/pin/compatibility.json`.
- CF-06 `[Confirmed]` malformed, oversized, timeout, crash와 unsupported-version behavior를 검증하는 test seam이 존재한다. 근거: `internal/protocol/*_test.go`, `adapters/dart/test/`, `adapters/typescript/test/`.
- CF-07 `[Confirmed]` 현재 Core는 adapter stderr tail을 최대 8KiB로 제한한다. 근거: `internal/protocol/client.go`의 `stderrTailBytes = 8 << 10`.

## 4. Actors and Preconditions

- Primary caller: CLI 또는 MCP 요청을 처리하는 CodeFlow Core
- Protocol peer: Dart 또는 TypeScript/JavaScript adapter process
- Operator: installer, `doctor`와 release compatibility를 관리하는 maintainer
- Permission: Core는 adapter child process의 stdin, stdout과 stderr만 사용하며 product source를 수정하지 않는다.
- Precondition: Core와 공식 adapter release는 v2 protocol과 capability를 선언한다.
- Precondition: Installer는 Core와 두 공식 adapter를 하나의 compatible release set으로 제공한다.

## 5. Resolved Decisions

| ID | Decision | Rationale |
|---|---|---|
| D-01 | LSP-style `Content-Length` framed JSON-RPC 2.0 over stdio를 사용한다. Stdout은 protocol 전용이며 log는 stderr로 보낸다. | JSON message boundary와 multiline payload를 명확히 처리한다. |
| D-02 | Core와 공식 adapter는 v2만 지원하며 handshake에서 `protocolVersions: [2]`와 capability를 검증한다. V1 probe, transport 추측과 fallback은 없다. | 단일 전송 규격으로 bootstrap ambiguity와 compatibility 분기를 제거한다. |
| D-03 | 기존 5개 operation의 사용자 목적은 유지하되 request·result의 권위는 새 v2 schema다. Progress와 chunk는 notification이며 final response만 authoritative completion이다. | Semantic Map에 필요한 분석 능력을 유지하면서 wire contract를 새로 정의한다. |
| D-04 | `$/cancelRequest` notification으로 cancellation을 요청하고 취소된 result와 late response를 publish에서 제외한다. | 취소된 workspace request가 current generation을 오염시키지 않게 한다. |
| D-05 | `schemas/adapter-protocol-v2.schema.json`을 새 source of truth로 추가한다. 기존 v1 schema는 역사적 기준으로 남길 수 있지만 v2 conformance 또는 runtime fallback에 사용하지 않는다. | 새 계약의 권위를 하나로 고정한다. |
| D-06 | NDJSON v1 bridge와 mixed-version adapter 지원을 구현하지 않는다. | 이번 개선에서 legacy compatibility보다 근본적인 분석 경계와 성능을 우선한다. |
| D-07 | Core v2, Dart v2, TypeScript/JavaScript v2, installer v2 default 순서로 전환한다. | 각 단계의 public seam을 독립 검증한다. |
| D-08 | V2 전환은 MAJOR release로 제공하고 설치 전에 breaking change, 요구 adapter와 rollback 단위를 명시한다. | 이전 release와 호환되지 않음을 설치 시점에 숨기지 않는다. |
| D-09 | JSON-RPC 표준 error에 closed CodeFlow domain error와 retryability를 보존한다. Frame은 1MiB, in-flight request는 64개를 유지하고 큰 result는 chunk한다. | Bounded operation과 typed failure를 유지한다. |
| D-10 | Rollback은 개별 adapter fallback이 아니라 이전 valid Core·adapter 전체 release set 복원이다. | Mixed-version 상태를 만들지 않는다. |

Open Decision은 없다.

### 5.1 Backward Compatibility Policy

- 이 MAJOR cutover는 NDJSON v1 Core, adapter, framing, envelope, schema, error shape와의 backward compatibility를 요구하지 않는다.
- V2 request·result는 기존 operation의 사용자 목적을 제공하지만 v1 field, ordering, error code, serialization 또는 output equality를 보존할 의무가 없다.
- Core는 dual stack, bridge, fallback, compatibility window와 mixed v1·v2 process set을 구현하지 않는다.
- 기존 v1 contract와 schema는 historical artifact일 뿐이며 v2 runtime selection, conformance 또는 acceptance authority가 아니다.
- 이전 full release set 복원은 실패한 설치를 원자적으로 되돌리는 운영 복구다. V2 Core가 이전 adapter와 상호 운용한다는 compatibility 보장이 아니다.
- 이후 protocol version과의 호환 정책은 이 계약에 포함하지 않으며 별도 승인 계약 없이는 추정하지 않는다.

## 6. Business Rules and Invariants

- INV-01 V2 Only: Core는 Content-Length JSON-RPC 2.0만 송수신하고 v1을 probe하거나 fallback하지 않는다.
- INV-02 Framing: 모든 v2 message는 `Content-Length: <UTF-8 body bytes>\r\n\r\n<JSON body>` 형식이며 선언 길이와 실제 body byte 수가 같아야 한다.
- INV-03 Stdout Isolation: Adapter stdout은 protocol frame만 포함하고 diagnostic log와 stack trace는 stderr로 출력한다.
- INV-04 Correlation: 모든 response와 request-scoped notification은 원 request ID를 참조한다.
- INV-05 Final Authority: Progress와 result chunk는 partial evidence이며 schema-valid final response가 성공한 뒤에만 assembled result를 사용한다.
- INV-06 Cancellation Isolation: Cancelled, timed-out 또는 stale request의 response와 chunk는 cache, staging 또는 active generation에 포함하지 않는다.
- INV-07 No Legacy Compatibility: NDJSON v1 adapter, bridge와 mixed-version release set은 supported capability로 표시하지 않는다.
- INV-08 V2 Semantic Authority: `detect`, `harvest_candidates`와 `slice`의 output 의미는 v2 schema와 fixture가 정의한다.
- INV-09 Source Read-Only: Protocol과 adapter 분석은 product source를 수정하지 않는다.
- INV-10 Schema Authority: V2 message, capability, error와 chunk contract는 versioned JSON Schema와 valid·invalid fixture로 강제한다.
- INV-11 Bounded Operation: 하나의 framed message는 1MiB 이하이며 하나의 connection은 최대 64개 in-flight request를 가진다.
- INV-12 No Partial Publish: Missing, duplicate, reordered, invalid 또는 checksum-mismatched chunk set은 result 또는 generation으로 발행하지 않는다.
- INV-13 Release Atomicity: Installer와 rollback은 동일 release contract의 Core·Dart·TypeScript set 전체를 검증한 뒤 활성화하며 혼합 set을 만들지 않는다.
- INV-14 Install Disclosure: Installer와 `doctor`는 v2 breaking change, adapter version, capability, incompatibility와 rollback 단위를 표시한다.

## 7. Version and Capability Handshake

Core는 adapter process를 시작한 뒤 Content-Length framed `ping`을 최초 request로 보낸다.

```json
{
  "adapterVersion": "<semver>",
  "protocolVersions": [2],
  "capabilities": {
    "cancellation": true,
    "progress": true,
    "resultChunks": true,
    "maxMessageBytes": 1048576,
    "maxInFlight": 64
  }
}
```

- Core는 protocol version 2가 없으면 analysis method를 보내지 않는다.
- Selected version과 capability는 caller-visible diagnostic과 `doctor` result에 포함한다.
- V1 line 또는 malformed Content-Length header는 protocol failure다. Core는 다른 framing으로 재시도하지 않는다.
- Capability declaration과 실제 behavior가 다르면 해당 adapter는 conformance failure이며 release-ready로 표시하지 않는다.

## 8. V2 Message Contract

### 8.1 Requests and Responses

- JSON body는 JSON-RPC 2.0의 `jsonrpc: "2.0"`, `id`, `method`, `params` 또는 `result`·`error` shape를 따른다.
- Request ID는 하나의 connection에서 active request 사이에 고유해야 한다.
- Method는 `ping`, `detect`, `harvest_candidates`, `slice`, `shutdown`이다.
- Adapter는 unknown method에 JSON-RPC method-not-found error를 반환한다.
- `shutdown` 성공 뒤 adapter는 새 analysis request를 받지 않는다.

### 8.2 Cancellation

- Core는 `$/cancelRequest` notification과 `{ "id": <request-id> }` params로 취소를 요청한다.
- Adapter는 취소가 완료되면 domain error `E_CANCELLED`로 request를 종료한다.
- Core는 취소 뒤 도착한 progress, chunk와 final response를 사용하지 않는다.

### 8.3 Progress and Result Chunks

- `$/progress`는 request ID, monotonic sequence와 progress payload를 가진다.
- `codeflow/resultChunk`는 request ID, zero-based monotonic sequence와 JSON-safe chunk를 가진다.
- 각 notification을 포함한 전체 frame은 1MiB 이하여야 한다.
- Inline result가 1MiB frame을 넘으면 ordered chunk 뒤 final response에 chunk count, assembled SHA-256과 media type을 제공한다.
- Core는 sequence, count, digest와 final result schema를 모두 검증한 뒤에만 assembled result를 사용한다.

## 9. Error, Retry and Diagnostic Contract

- JSON parse, invalid request, unknown method, invalid params와 internal error는 JSON-RPC 표준 numeric error code를 사용한다.
- CodeFlow domain failure는 closed enum `E_TIMEOUT`, `E_CANCELLED`, `E_CRASHED`, `E_BACKPRESSURE`, `E_BAD_REQUEST`, `E_UNSUPPORTED_VERSION`, `E_ADAPTER_INTERNAL`과 `retryable`을 `error.data`에 제공한다.
- `E_TIMEOUT`과 `E_BACKPRESSURE`만 application-level retryable이다.
- 첫 adapter process crash는 request가 여전히 active이고 cancellation, deadline과 workspace epoch가 유효할 때만 transport recovery로 새 process와 새 request ID를 사용해 정확히 한 번 재실행한다.
- 재실행된 request가 다시 crash하면 Core는 최종 `E_CRASHED`, `retryable: false`를 반환하고 더 이상 자동 재실행하지 않는다.
- Cancelled, invalid, unsupported-version, schema-invalid와 최종 non-retryable failure는 application-level 자동 재시도하지 않는다.
- Error detail은 secret redaction 뒤 전체 1MiB error frame bound 안에서만 노출하고 stderr tail은 secret redaction 뒤 최대 8KiB까지만 caller에게 노출한다.

## 10. Schema and Fixture Contract

- `schemas/adapter-protocol-v2.schema.json`이 v2 body의 request, success, error, notification, capability, progress, chunk와 final streamed-result manifest를 정의한다.
- V2는 valid·invalid framing body fixture와 end-to-end conformance fixture를 가진다.
- Dart와 TypeScript/JavaScript adapter는 같은 v2 handshake, operation, cancellation, progress, chunk, error, bounds와 crash suite를 통과해야 한다.
- V1 fixture 또는 output equality는 v2 acceptance evidence가 아니다.

## 11. Observable Cutover Flow

1. Core v2 client, v2 schema와 conformance peer를 제공한다.
2. Dart adapter를 native v2로 전환하고 Core end-to-end 분석을 검증한다.
3. TypeScript/JavaScript adapter를 native v2로 전환하고 같은 suite를 검증한다.
4. Installer가 compatible v2 release set을 MAJOR default로 제공하고 `doctor`가 실제 handshake 상태를 표시한다.
5. 설치 또는 upgrade 실패 시 operator는 이전 valid Core·adapter release set 전체로 rollback할 수 있다.

## 12. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| V1 line 또는 malformed Content-Length | Protocol framing failure | Connection 폐기, fallback·result 발행 없음 | V2 adapter 설치 |
| Protocol version 2 없음 | Unsupported와 peer version 표시 | Analysis method 전송 없음 | Compatible v2 release set 설치 |
| Capability와 실제 behavior 불일치 | Conformance failure | 해당 adapter release-ready 표시 없음 | Adapter 수정과 suite 재실행 |
| Cancel 뒤 late response | Cancelled와 discarded response | Cache·generation 반영 없음 | Current request 재실행 |
| Chunk missing, duplicate, out-of-order 또는 digest mismatch | Invalid streamed result | Assembled result와 publish 없음 | Entire request 재실행 |
| In-flight limit 초과 | Queue 또는 `E_BACKPRESSURE` | Unbounded allocation 없음 | Capacity 뒤 bounded retry |
| 한 adapter가 incompatible | 해당 adapter unavailable | 다른 valid process와 이전 generation 유지 | Compatible full release set 설치 |
| New release validation 실패 | Cutover 또는 rollback failure | 기존 active release set 유지, mixed set 없음 | Artifact 복구 후 전체 set 재시도 |

## 13. Feature-Level Acceptance

- A1. WHEN Core가 v2 adapter를 시작하면, THE system SHALL Content-Length framed JSON-RPC 2.0 `ping`으로 version 2와 capability를 검증한 뒤 analysis method를 보낸다.
- A2. IF adapter가 NDJSON v1, malformed framing 또는 protocol version 2 미지원 상태이면, THEN THE system SHALL explicit protocol failure를 반환하고 v1 probe, fallback 또는 analysis artifact를 생성하지 않는다.
- A3. WHEN caller가 native v2 Dart adapter로 `detect`, `harvest_candidates` 또는 `slice`를 호출하면, THE system SHALL 해당 method의 v2 schema가 정의한 operation-specific result를 반환한다.
- A4. WHEN caller가 native v2 TypeScript/JavaScript adapter로 `detect`, `harvest_candidates` 또는 `slice`를 호출하면, THE system SHALL 해당 method의 v2 schema가 정의한 operation-specific result를 반환한다.
- A5. WHEN caller가 v2 request를 취소하면, THE system SHALL cancel notification을 보내고 취소 뒤 도착한 progress, chunk와 final response를 cache 또는 generation에 포함하지 않는다.
- A6. WHEN result가 하나의 1MiB frame을 초과하면, THE system SHALL bounded ordered chunk와 final digest manifest를 사용하고 전체 validation 뒤에만 결과를 반환한다.
- A7. IF streamed result에 missing, duplicate, reordered, oversized 또는 digest-mismatched chunk가 있으면, THEN THE system SHALL result와 generation을 발행하지 않는다.
- A8. THE system SHALL malformed framing, invalid JSON-RPC shape, unknown method, invalid params와 closed domain error를 versioned schema와 typed error로 구분한다.
- A9. THE system SHALL connection당 framed message 1MiB와 최대 64 in-flight request bound를 강제하고 초과 상태를 unbounded allocation 없이 처리한다.
- A10. THE system SHALL Dart와 TypeScript/JavaScript adapter에 동일한 v2 conformance suite를 적용한다.
- A11. THE system SHALL `doctor`에서 selected protocol, adapter version, capability, incompatibility와 unavailable 상태를 실제 handshake 결과로 표시한다.
- A12. WHEN adapter process가 처음 crash하고 request가 여전히 active이며 cancellation, deadline과 workspace epoch가 유효하면, THE system SHALL 해당 상태를 재검증한 뒤 새 process와 새 request ID로 request를 정확히 한 번 재실행한다.
- A13. IF 재실행된 request가 다시 crash하거나 request가 cancelled, schema-invalid, unsupported-version 또는 최종 non-retryable failure이면, THEN THE system SHALL 더 이상 자동 재실행 또는 application-level retry를 하지 않는다.
- A14. WHEN operator가 cutover 실패 뒤 rollback을 요청하면, THE system SHALL 이전 valid Core·Dart·TypeScript release set 전체를 검증해 원자적으로 복원하고 mixed-version set을 활성화하지 않는다.
- A15. WHEN v2 cutover release를 설치하거나 upgrade하면, THE system SHALL MAJOR breaking change, 필수 v2 adapter, 제거된 v1 지원, 예상 기능 변화와 full-release rollback 방법을 적용 전에 표시한다.
- A16. WHEN caller-visible error diagnostic을 반환하면, THE system SHALL error detail을 secret-redacted 1MiB error frame 안에 제한하고 stderr tail을 secret-redacted 최대 8KiB로 제한한다.

## 14. Test Seam and Evidence

- Public seam: Core-adapter stdio, CLI·MCP analysis operation, `doctor`, installer artifact와 release compatibility set
- Required test level:
  - V2 schema valid·invalid fixture
  - Core framing, correlation, cancellation, chunk assembly, limit, retry와 redaction test
  - Dart native v2 conformance와 Core end-to-end
  - TypeScript/JavaScript native v2 conformance와 Core end-to-end
  - V1 rejection, crash, timeout, malformed frame, message flood와 stale workspace adversarial test
  - Clean install, upgrade disclosure, full-release rollback과 mixed-version rejection
- Replaceable external boundaries: adapter process, stdio, clock, process launcher, installer root와 release compatibility registry
- Evidence mapping:
  - A1, A2, A8: handshake, v1 rejection과 protocol fixture
  - A3, A4, A10: official adapter conformance와 CLI·MCP analysis
  - A5, A12, A13: cancellation·deadline·workspace epoch 재검증, one-crash restart, final non-retryable failure와 publish-absence assertion
  - A6, A7, A9: chunk·limit·backpressure adversarial fixture
  - A11: doctor handshake matrix
  - A14, A15: installer disclosure, atomic release-set activation과 rollback rehearsal
  - A16: secret-bearing·oversized error detail과 stderr fixture

## 15. What Could Be Wrong

- ASM-01: 1MiB frame과 chunk protocol이 expected adapter result에 충분하다.
  - Consequence if false: Chunk overhead와 assembly latency가 analysis budget을 넘는다.
  - Validation method: Small, near-limit와 multi-chunk result fixture에서 latency, memory와 checksum behavior를 측정한다.
- ASM-02: V2 operation schema가 Semantic Map의 deterministic baseline에 필요한 language-neutral evidence를 충분히 표현한다.
  - Consequence if false: Protocol은 동작하지만 핵심 코드 이해 evidence가 누락된다.
  - Validation method: Dart와 TypeScript/JavaScript golden repository에서 required candidate, edge, source anchor와 unknown을 검증한다.
- ASM-03: 최대 64 in-flight request가 Core와 adapter memory budget 안에서 동작한다.
  - Consequence if false: Backpressure 전에 memory와 latency가 허용 범위를 넘는다.
  - Validation method: 1, 16, 64와 overflow concurrency fixture에서 RSS, latency와 failure를 측정한다.

## 16. Done When

- 이 개정 계약과 영향받은 SMAP 부모가 명시적으로 Approved 상태다.
- V2 schema와 valid·invalid fixture가 protocol message에 적용되는 criteria를 강제하고 Section 14의 evidence mapping이 A1부터 A16까지 전체 behavior를 검증한다.
- Core v2, Dart v2와 TypeScript/JavaScript v2가 같은 conformance suite를 통과한다.
- V1 rejection, cancellation, late response, chunk corruption, backpressure, crash와 redaction test가 partial publish 부재를 증명한다.
- Installer와 `doctor`가 MAJOR breaking change, selected version, capability, incompatibility와 full-release rollback을 표시한다.
- `go test ./...`, `cd adapters/dart && dart test`, `node adapters/typescript/test/index.test.js`가 통과한다.
- `docs/VERSIONING.md`의 모든 version 위치가 MAJOR release로 동기화된다.

## 17. Approval Readiness

- Contract Status: Approved
- Resolved Decisions: D-01부터 D-10까지
- Open Decisions: 없음
- Existing v1 contract modified: 아니오. 새 MAJOR release에서 unsupported 상태로 남는다.
- Production schema or code modified: 아니오
- Next action: 영향받은 SMAP 부모 amendment를 재승인한 뒤 proposed slice를 독립 검토한다.
