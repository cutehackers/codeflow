# 요청 흐름 이해와 실시간 Semantic Compiler — VS-07 오류와 장애 경로를 조사한다

- Contract ID: `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-VS-07`
- Contract Status: Implemented
- Independent Review: Passed (`gpt-5.6-terra`, high)
- Parent Contract: `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md`
- Created: 2026-09-02
- Source: `docs/design/raw/requested-flow-live-semantic-compiler-architecture-draft-ko.md`
- Decision Records: `docs/design/decisions/requested-flow-live-semantic-compiler-decisions-ko.md`

## 1. Intent and Goal

- Intent ID: `INT-01`
- Parent Intent: 사용자가 요청한 흐름을 현재 코드 근거와 unknown을 포함한 이해 가능한 결과로 파악한다.
- Goal ID: `GOAL-07`
- Slice Goal: 사용자는 오류·장애의 확인된 인과 경로, runtime 관찰 범위와 미확인 복구 경로를 구분해 조사한다.
- User or Caller Value: 정적 후보, 실제 실행 Evidence와 외부 효과를 섞지 않고 어디서 실패했고 무엇이 아직 관찰되지 않았는지 판단할 수 있다.
- Contribution to Parent: Raw P4의 debug/incident view를 동일 SemanticMapIR과 Evidence 권위 위에서 제공한다.
- Parent Acceptance: `A16`, `A20–A21`, `A23`, `A25–A27`

## 2. User Outcome

사용자가 오류·symptom·failure Evidence 또는 trace를 지정하면 시스템은 실패 생성·전달·변환·처리·무시 경로, 마지막 확인 상태와 외부 효과를 보여준다. 실행되지 않은 branch, timeout과 compensation은 관찰되지 않은 후보로 표시하고 실제 실행 범위로 일반화하지 않는다.

## 3. Scope

### In Scope

- `mode=debug`의 error·symptom·failureEvidenceId query
- `mode=incident`의 traceId/incidentEvidenceId, runtime scope와 time window query
- Failure Target Resolver와 error 중심 reverse cause slice
- `thrown`, `transformed`, `handles_failure`, `ignored`, state-before/after 관계
- runtime scenario, input, environment, dependency fingerprint, trace coverage와 isolation level
- `calls_external`, timeout, retry, circuit break, compensation, partial commit timeline
- static candidate와 runtime observation의 correlation, conflict와 unknown 표시
- trusted-local 실행 전 command/access 범위 표시와 매 실행 사용자 승인

### Out of Scope

- feature baseline과 일반 caller impact — VS-02와 VS-06이 담당한다.
- runtime evidence가 없는 상태에서 target이나 실행 경로를 추론하는 행위.
- 사용자의 동의 없는 code execution, network access 또는 source mutation.
- model semantic proposal과 approval — VS-08이 담당한다.

## 4. Preconditions

- VS-02의 canonical flow, Evidence anchor와 unknown seam이 존재한다.
- VS-04의 current/last_verified proof와 generation state를 조회할 수 있다.
- debug query에는 error, symptom 또는 failure Evidence가 있고 incident query에는 trace/incident Evidence, runtime scope와 time window가 있다.
- runtime 실행이 필요한 경우 승인된 synthetic scenario 또는 기존 test/fixture와 isolation policy가 존재한다.

## 5. Public Seam

Debug/incident Task View Query, failure-focused response, Evidence Dock의 Why/Why-not 질문, runtime scenario metadata와 source/test/trace navigation이 공개 seam이다. 사용자는 마지막 확인 지점과 관찰 범위를 직접 확인한다.

## 6. Boundary Coverage

Error/symptom/trace input → failure target/runtime scope resolver → static reverse cause and runtime correlation → external/retry/compensation timeline → Evidence and isolation validation → debug/incident projection → source/test/trace result and unknowns.

## 7. Inherited Invariants

- `INV-01`, `INV-03`, `INV-04`, `INV-05`, `INV-06`, `INV-08`, `INV-12`, `INV-13`, `INV-15`, `INV-18`, `INV-19`, `INV-23`
- Raw D2, D3, D5, D11, D14, D15, §8.2, §8.7, §8.8, §11.1–§11.5, §14–§15, §18.3, §18.5, §18.6.
- runtime observation proves only its declared scenario, environment, dependency and time scope.

## 8. Slice-Specific Rules

- debug는 error/symptom/failure Evidence를 시작점으로 역추적하고, incident는 trace와 external boundary를 기준으로 시간 순서를 보인다.
- `observed_runtime`는 실행 trace와 scope가 있을 때만 사용한다. static candidate는 실행 사실로 승격하지 않는다.
- 실행되지 않은 branch, timeout, dynamic hop과 compensation은 `unknown` 또는 unobserved candidate로 유지한다.
- `trusted_local`은 실행 command와 접근 범위를 먼저 표시하고 매 실행 사용자의 승인을 요구한다. blocked는 실행하지 않는다.
- 정적 Evidence와 runtime Evidence가 충돌하면 `conflicting`으로 표시하고 자동 Fact 승격을 하지 않는다.
- source, credential, network와 external effect는 isolation policy를 벗어나지 않으며 failure 조사 자체가 product source를 수정하지 않는다.

## 9. Acceptance Criteria

- VS07-A1. WHEN valid debug query가 주어지면, THE system SHALL error/symptom/failure Evidence에서 시작해 thrown, transformed, handled, ignored와 마지막 확인 상태를 역방향으로 제공한다.
- VS07-A2. WHEN valid incident query가 주어지면, THE system SHALL trace scope와 time window 안에서 external call, timeout, retry, compensation과 partial commit을 시간 순서로 제공한다.
- VS07-A3. IF debug/incident 필수 입력이 없거나 runtime scope가 없는 경우, THEN THE system SHALL typed `missing_precondition`을 반환하고 임의의 failure target 또는 time window를 만들지 않는다.
- VS07-A4. THE system SHALL static candidate, runtime-observed path, corroborated path, conflicting path와 unknown을 서로 다른 상태로 표시한다.
- VS07-A5. WHEN runtime evidence가 표시되면, THE system SHALL scenario, input, environment, dependency fingerprint, trace coverage, observedAt와 isolation level을 함께 제공한다.
- VS07-A6. IF branch, dynamic target, timeout 또는 compensation이 관찰되지 않았으면, THEN THE system SHALL 이를 unknown/unobserved로 표시하고 실행된 전체 동작으로 설명하지 않는다.
- VS07-A7. WHEN isolation이 `trusted_local`이면, THE system SHALL 실행 command와 source·credential·network 접근 범위를 표시한 뒤 매 실행 사용자 승인을 받는다.
- VS07-A8. IF 정적 Evidence와 runtime Evidence가 충돌하면, THEN THE system SHALL 해당 관계를 `conflicting`으로 유지하고 자동 Fact 또는 settlement 승격을 하지 않는다.
- VS07-A9. THE system SHALL failure 결과에서도 source/test/trace Evidence로 이동할 수 있게 하고 secret과 전체 agent transcript를 기본 노출하지 않는다.

## 10. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| failure target missing | missing-precondition error | 임의 reverse path 없음 | error/symptom/failure Evidence 지정 |
| incident trace 또는 scope missing | missing-precondition error | 실행 또는 timeline 생성 없음 | traceId와 time window 제공 |
| runtime blocked/timeout | unknown과 coverage gap | 동작 부재 Fact 없음 | 승인된 isolation/scenario로 별도 재실행 |
| static/runtime conflict | conflicting relation | 자동 승격·settlement 없음 | 추가 Evidence 또는 사람 판단 |
| dynamic hop unresolved | unknown/unresolved boundary | target 추측 없음 | adapter 또는 scoped runtime Evidence 추가 |
| trusted-local approval denied | blocked/not-observed | command 실행 없음 | 사용자가 명시적으로 다시 승인 |
| trace scope incomplete | observed scope와 unobserved candidate 분리 | 전체 장애 경로 주장 없음 | wider trace 또는 static Evidence |

## 11. Data and Interaction Contract

- Input: `mode=debug` 또는 `mode=incident` discriminated query, failure/trace Evidence, runtime scope와 time window.
- Output: reverse cause slice 또는 observed incident timeline, static/runtime status, state/effect relations, Evidence refs, unknowns, conflicts와 coverage.
- Runtime evidence는 scenario, input, environment, dependency fingerprint, trace coverage, isolation level과 observedAt를 보유한다.
- debug/incident projection은 하나의 generation proof를 참조하고, 현재성 proof가 없는 결과는 historical 또는 last_verified로 표시한다.
- command mutation과 runtime execution은 별도 동작이며, trusted-local approval event가 없으면 실행하지 않는다.

## 12. Test Seam and Evidence

- Public seam: debug/incident query, failure response, trace scope, runtime approval, Why/Why-not, source/test/trace Evidence navigation.
- Required test level: precondition, static reverse path, runtime scope, observed/unobserved, conflict, isolation approval, secret redaction and no-execution fixtures.
- Replaceable external boundaries: trace provider, runtime executor, clock, isolation sandbox, generation store and FlowView projector.
- Evidence per criterion: VS07-A1 reverse-cause fixture, A2 incident timeline fixture, A3 query matrix, A4/A6 static-runtime status fixture, A5 scope fixture, A7 approval fixture, A8 conflict fixture, A9 redaction/navigation test.

## 13. Verification Plan

| Check | Applicability / Trigger | Exact Command | Expected Evidence | Required for Completion |
|---|---|---|---|---|
| Acceptance behavior | Always | `go test ./internal/mcp ./internal/fusion ./internal/flowview ./internal/e2e` | debug/incident queries, static-runtime scope, unknown/conflict and navigation pass | Yes |
| Slice tests | Always | `go test ./internal/mcp ./internal/fusion ./internal/flowview` | VS-07 package tests pass | Yes |
| Type, static analysis, and lint | failure/runtime projection code affected | `go vet ./...` | no applicable findings | When applicable |
| Affected build | Core or FlowView affected | `go build ./...` | Core builds successfully | When applicable |
| Architecture and policy | runtime/evidence payload boundary changes | `go test ./internal/contractharness -run 'TestValidateGoldenFixtures|TestValidateExportedContractBoundary'` | runtime scope, evidence, unknown and isolation fixtures validate | Yes |
| Regression | shared generation, fusion or query behavior affected | `go test ./...` | full Go regression suite passes | Yes |
| Security | runtime/source/trace data is exposed or executed | `go test ./internal/secret ./internal/mcp ./internal/e2e` | redaction, approval and no-unsafe-execution evidence | Yes |
| Data and migration compatibility | runtime and trace Evidence are persisted | `go test ./internal/contractharness` | schema and scope compatibility pass | Yes |
| Performance and concurrency | no numeric debug/incident target is defined | `N/A — raw defines scope and comprehension correctness, not a repository performance threshold for this slice` | reason recorded | No |
| Reliability and flake | trace replay, timeout or isolation failure is affected | `go test ./internal/mcp ./internal/e2e -count=1` | repeatable replay, timeout and approval behavior | Yes |
| Coverage | no repository coverage percentage threshold is configured | `N/A — no coverage percentage command is configured` | reason recorded | No |
| Browser UX | debug/incident timeline, observed/unobserved, conflict and Evidence navigation are user-facing; raw §21.10 UX verification is required before completion | `npm --prefix web/live-comprehension-workspace run test:e2e -- --project=chromium` | Playwright proves failure timeline, runtime scope, unknown/conflict status, approval boundary and source/test/trace navigation | Yes |
| Accessibility | debug/incident states are user-facing | `npm --prefix web/live-comprehension-workspace run test:a11y` | `@axe-core/playwright`, keyboard navigation, screen-reader outline, reduced motion and contrast evidence pass | Yes |

## 14. What Could Be Wrong

- Assumption: failure Evidence and runtime traces include enough scope to distinguish observed execution from static candidates.
- Consequence: users may treat a partial trace as the full incident or miss the last confirmed state.
- Validation method: fixtures with missing spans, dynamic hops, timeout, retry, compensation and static/runtime conflicts, evaluated against a labeled observation boundary.

## 15. Done When

- VS07-A1–A9 distinguish static, runtime, conflict and unknown paths.
- No runtime execution happens without the declared isolation and approval policy.
- Failure results link to source/test/trace Evidence and hide secrets by default.
- Applicable verification rows pass or N/A reasons are explicit.
- No production code, schema or raw source is changed by this slicing task.

## 16. Open Decisions

없음. runtime isolation과 scope 및 결정 사항은 아래 Resolved Implementation Decisions로 확정되었다.

## 17. Resolved Implementation Decisions

- `SID-C1` (정규 payload 물리 구성): `schemas/failure-path-trace.schema.json`, `schemas/runtime-observation.schema.json` 독립 스키마 분할 및 BaseURL 등록.
- `SID-C2` (validation 경계): 정적 AST 장애 분기와 런타임/테스트 실패 스택의 상관관계(correlation) 검증, 자격증명/토큰 마스킹 검증.
- `SID-C3` (계약 검증 범위): 합성 장애 재현 샌드박스 픽스처, timeout/retry/compensation 복구 경로 픽스처 구축.
- `SID-C4` (기존 구현 재사용/교체): VS-02의 `SemanticMapIR` 내 `error_path` 노드 및 `internal/secret` 단일 게이트 마스킹 재사용.
- `SID-C5` & `SID-07` (격리 수준, Sandbox, Debug Critical Obligation):
  - 런타임 추적은 기본 no-egress, read-only 샌드박스에서만 실행되며, 외부 네트워크나 자격증명 접근 확대 시 사용자 명시적 승인 필수 (`INV-15`, `INV-18`).
  - Debug Mode Settlement Gate 입력: 장애 유발 조건(Triggering Condition), 실패 전이 단계(Failure Transition), 최종 실패 상태(Terminal Error State)의 증거가 모두 `verified` 상태여야 함.
