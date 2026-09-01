# Semantic Map Layered Architecture — VS-03 동적 경로 검증

- Contract ID: `SMAP-VS-03`
- Contract Status: Proposed
- Independent Review: Passed
- Parent Contract: `docs/design/specs/2026-09-01-semantic-map-layered-architecture-ko.md`
- Created: 2026-09-01
- Depends On: SMAP-VS-02
- Parent Acceptance Coverage: FA-03, FA-05, FA-06, FA-10, FA-12, FA-20

## 1. User Outcome

개발자는 미확정 dynamic boundary와 coverage gap을 확인하고, 허용된 scenario를 선택적으로 실행해 관찰 범위와 격리 수준이 명시된 검증 결과를 같은 FlowView에서 확인할 수 있다.

## 2. Scope

### In Scope

- Language capability와 unresolved dynamic boundary 식별
- Parent D-17의 지원 feature subset
- Existing test, fixture 또는 승인된 synthetic scenario 선택
- `containerized`, `sandboxed`, `trusted_local`, `blocked` isolation policy
- Source read-only instrumentation과 runtime event 수집
- Static candidate, type constraint, framework rule과 runtime evidence fusion
- `observed_runtime`, `corroborated`, `conflicting`, `unknown` 상태
- Scenario-scoped evidence, CoverageLedger와 complete·partial 상태 갱신
- Enriched complete generation의 비동기 publish

### Out of Scope

- Runtime에서 실행하지 않은 branch 또는 target 확정 — unknown으로 유지한다.
- Production traffic나 production credential 사용 — 보안 경계를 넘는다.
- 임의 reflection, runtime code generation, `eval`과 native FFI 내부 target 지원 — 부모 D-17의 초기 subset 밖이다.
- Model을 사용한 dynamic target 선택 — Analysis Layer 권위를 위반한다.

## 3. Preconditions

- SMAP-VS-02 deterministic Semantic Map이 current snapshot에서 발행되어 있다.
- Unresolved boundary와 필요한 evidence가 식별되어 있다.
- 실행 가능한 existing test, fixture, 저장된 request/event 또는 승인된 synthetic scenario가 있다.
- Runtime policy가 실제 isolation level과 command 접근 범위를 판정할 수 있다.

## 4. Public Seam

- FlowView의 unresolved boundary, coverage와 scenario 선택 surface
- 동등한 CLI 또는 MCP runtime-verification 요청과 상태 조회
- 새 generation의 runtime evidence reference, isolation, scenario, structural status와 coverage summary

Public seam은 command, isolation, environment scope, 실행 상태와 결과를 노출하되 product source mutation이나 instrumentation 내부 형식을 노출하지 않는다.

## 5. Boundary Coverage

FlowView·CLI·MCP의 dynamic verification 요청 → isolation·approval 판정 → 임시 실행 환경에서 scenario 수행 → runtime event와 source candidate 연결 → analyzer evidence fusion → CoverageLedger와 AnalysisSnapshot 갱신 → validated generation publish → 동일 Timeline의 scenario marker와 status 갱신

## 6. Inherited Invariants

- INV-01 Current-State Authority
- INV-02 Fact Ownership
- INV-03 Evidence Grounding
- INV-04 Unknown Preservation
- INV-05 Precision and Coverage Separation
- INV-06 Runtime Scope
- INV-09 Generation Consistency
- INV-10 Stale Isolation
- INV-11 Source Read-Only
- INV-12 Security Boundary
- INV-14 Schema Authority
- INV-18 Runtime Isolation
- INV-20 Dynamic-Language GA

## 7. Slice-Specific Rules

- BR-01: Initial supported subset은 module/import resolution, function·method call, inheritance/interface dispatch, async flow, framework route·event·DI binding과 scoped runtime으로 검증 가능한 callback target이다.
- BR-02: `containerized`와 `sandboxed`는 승인된 scenario만 정책에 따라 실행할 수 있다.
- BR-03: `trusted_local`은 command와 source·credential·network 접근 범위를 표시하고 매 실행 사용자의 승인을 받는다.
- BR-04: `blocked`는 실행하지 않고 coverage gap을 기록한다.
- BR-05: Runtime evidence는 해당 snapshot, scenario, input, runtime, dependency, environment와 isolation 범위만 증명한다.
- BR-06: Static과 runtime evidence가 충돌하면 edge를 `conflicting`으로 표시하고 Fact 자동 승격과 semantic enrichment를 막는다.
- BR-07: 지원 subset의 critical flow는 선언된 required critical boundary 전부가 resolved일 때만 `complete`다. 하나라도 unresolved면 `partial` 또는 `unknown`이다.

## 8. Acceptance Criteria

- A1. WHEN deterministic map에 중요한 unresolved dynamic boundary가 있으면, THE system SHALL boundary, coverage gap, 필요한 evidence와 실행 가능한 scenario를 표시한다.
- A2. WHEN 사용자가 `containerized` 또는 `sandboxed`의 승인된 scenario를 실행하면, THE system SHALL 실제 isolation policy와 scenario scope가 연결된 runtime evidence를 발행한다.
- A3. WHEN isolation이 `trusted_local`이면, THE system SHALL command와 접근 범위를 먼저 표시하고 해당 실행에 대한 사용자 승인 후에만 scenario를 시작한다.
- A4. IF isolation이 `blocked`, scenario가 실패 또는 timeout이면, THEN THE system SHALL 동작 부재 Fact를 만들지 않고 실패 범위와 coverage gap을 표시한다.
- A5. WHEN static candidate와 runtime target이 일치하면, THE system SHALL 해당 scenario 범위에서 relation을 `observed_runtime` 또는 `corroborated`로 표시한다.
- A6. IF static candidate와 runtime target이 충돌하면, THEN THE system SHALL edge를 `conflicting`으로 표시하고 자동 Fact 승격을 수행하지 않는다.
- A7. THE system SHALL runtime Fact마다 scenario, input, runtime, dependency, environment, isolation과 current basis를 확인 가능하게 한다.
- A8. WHEN runtime evidence가 current code, dependency 또는 environment와 맞지 않게 되면, THE system SHALL 해당 evidence와 파생 claim을 `stale`로 표시하고 fresh Fact로 유지하지 않는다.
- A9. WHERE dynamic-language Fact가 지원 subset에서 GA로 표시되면, THE system SHALL compiler-resolved fixture와 같은 publication precision 및 declared-critical-boundary coverage gate를 통과한 evidence를 제공한다.
- A10. UNTIL SMAP-VS-07의 dynamic-language release gate가 통과하면, THE system SHALL 이 slice의 dynamic capability를 `partial` 또는 `experimental`로 표시하고 GA evidence로 사용하지 않는다.

## 9. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| Scenario 없음 | Required evidence와 coverage gap 표시 | 실행 없음 | Fixture 또는 승인 scenario 제공 |
| Trusted-local 승인 거절 | Not run 상태 유지 | Process 실행과 artifact 변경 없음 | 새 실행에서 다시 승인 가능 |
| Sandbox 준비 실패 | Blocked 또는 failed isolation 상태 | Product source 변경 없음 | 지원 environment에서 재시도 |
| Runtime timeout 또는 crash | Scenario failure와 partial coverage | 동작 부재 Fact 없음 | 환경 수정 후 새 run |
| Static/runtime 충돌 | Conflicting edge와 evidence 표시 | Fact 승격과 model enrichment 없음 | 추가 scenario 또는 사람 검토 |
| Workspace epoch 변경 | Run result discarded 또는 stale | Active pointer 변경 없음 | Current epoch에서 재실행 |

## 10. Data and Interaction Contract

- Scenario input: scenario ID, command digest, fixture 또는 test reference, expected isolation, timeout과 allowed resource policy
- RuntimeEvidence: run ID, basis SHA, scenario ref, input digest, runtime·dependency·environment fingerprint, isolation, policy version과 event references
- RuntimeEvent: execution context, sequence, symbol·source reference, call·branch·state·exception kind와 value-free evidence
- CoverageLedger: discovered·exercised entry와 branch, dynamic boundary counts, critical-flow required·resolved refs, status와 scenario refs
- Persistence: run별 manifest와 event stream을 `.codeflow/` 아래에 저장하고 current generation에는 validated reference만 포함한다.
- External interaction: network는 isolation policy가 명시적으로 허용한 경우에만 사용하며 credential을 전달하지 않는다.

## 11. Test Seam and Evidence

- Public seam: runtime-verification request, scenario status, published generation과 FlowView scenario overlay
- Required test level: isolation policy unit test, fake runtime integration, source read-only assertion, end-to-end dynamic fixture와 stale/conflict adversarial test
- Replaceable external boundaries: process runner, sandbox/container provider, clock, filesystem, network와 runtime event collector
- Evidence required per criterion:
  - A1, A4: unresolved·failed fixture의 UI/API result와 Fact absence
  - A2, A3, A7: isolation·approval·scope manifest assertion
  - A5, A6: static/runtime fusion fixture
  - A8: dependency 또는 basis 변경 후 stale assertion
  - A9, A10: shared benchmark gate report와 pre-gate capability status

## 12. What Could Be Wrong

- Assumption: 중요한 unresolved boundary에 안전하게 실행 가능한 test나 fixture가 존재한다.
- Consequence: critical flow가 계속 partial로 남아 dynamic-language capability를 GA로 표시할 수 없다.
- Validation method: 지정 benchmark corpus에서 scenario availability와 dynamic boundary resolution rate를 측정한다.

## 13. Done When

- Every criterion has passing evidence.
- Supported subset fixture가 precision, complete·partial·unknown, conflict와 stale behavior를 검증한다.
- Source read-only, isolation, approval, timeout과 network policy 검증이 통과한다.
- `go test ./...`와 승인된 adapter·runtime integration 검증이 통과한다.
- No contract requirement is weakened.
- No unrelated behavior changes.

## 14. Open Decisions

없음.
