# Semantic Map Layered Architecture — VS-02 현재 기능 이해

- Contract ID: `SMAP-VS-02`
- Contract Status: Proposed
- Independent Review: Passed
- Parent Contract: `docs/design/specs/2026-09-01-semantic-map-layered-architecture-ko.md`
- Created: 2026-09-01
- Depends On: SMAP-VS-01
- Parent Acceptance Coverage: FA-01, FA-02, FA-03, FA-04, FA-11, FA-12, FA-14, FA-15, FA-19

## 1. User Outcome

개발자는 feature, debug 또는 onboarding query를 입력하고, 모델이나 runtime observer를 기다리지 않은 채 현재 worktree에 근거한 7~15개의 핵심 행동과 source evidence를 FlowView에서 이해할 수 있다.

## 2. Scope

### In Scope

- Task query, entry point 또는 기존 flow에서 분석 범위 결정
- Repository snapshot, workspace epoch와 language capability 고정
- Current source와 language analyzer로 검증된 `AnalysisSnapshot`
- `SemanticMapIR` canonical artifact와 versioned FlowSpec projection
- 구조 상태, 의미 상태, freshness, evidence scope, coverage와 unknown 표시
- 7~15개 task-scoped 핵심 행동과 source·test·contract evidence navigation
- Business Summary, Flow Story, Evidence Workbench와 Question Lens
- Staging validation 뒤 complete generation의 atomic publish
- 현재 loopback FlowView surface

### Out of Scope

- Runtime scenario 실행과 evidence fusion — VS-03이 담당한다.
- Baseline과 current behavior 비교 — VS-04가 담당한다.
- Optional model semantic enrichment — VS-05가 담당한다.
- 의미 승인과 durable overlay — VS-06이 담당한다.
- VS Code extension — 부모 계약의 1차 구현 Non-Goal이다.

## 3. Preconditions

- SMAP-VS-01의 adapter compatibility가 구현되어 있다.
- 대상 repository와 current worktree를 읽을 수 있다.
- 지원 adapter가 준비되거나 capability가 `partial` 또는 `unavailable`로 선언된다.
- 기존 active generation이 있으면 complete 상태로 읽을 수 있다.

## 4. Public Seam

- CLI 또는 MCP의 feature·entry-point 분석 요청
- MCP `analyze_flow`, `publish_core_flow`와 기존 flow retrieval
- Loopback FlowView의 task-scoped map, step selection과 evidence navigation
- Active generation의 versioned JSON artifact

Public seam은 task, snapshot identity, capability, 핵심 행동, 상태, unknown, evidence와 generation identity를 노출한다. Private graph representation이나 compiler object는 노출하지 않는다.

## 5. Boundary Coverage

CLI 또는 MCP task query → scope와 repository snapshot 고정 → CodeGraph candidate 탐색 → current source와 language analyzer 검증 → AnalysisSnapshot → deterministic SemanticMapIR → FlowSpec projection → atomic generation publish → FlowView 핵심 행동과 evidence

## 6. Inherited Invariants

- INV-01 Current-State Authority
- INV-02 Fact Ownership
- INV-03 Evidence Grounding
- INV-04 Unknown Preservation
- INV-05 Precision and Coverage Separation
- INV-08 Deterministic Baseline
- INV-09 Generation Consistency
- INV-10 Stale Isolation
- INV-11 Source Read-Only
- INV-13 Evidence Navigation
- INV-14 Schema Authority
- INV-19 Projection Compatibility

## 7. Slice-Specific Rules

- BR-01: CodeGraph는 candidate와 navigation source이며 Fact는 current source와 language analyzer 검증 뒤에만 발행한다.
- BR-02: 기본 map은 model과 runtime enrichment의 설치, 성공 또는 완료에 의존하지 않는다.
- BR-03: 기본 화면은 task와 연결된 7~15개 핵심 행동으로 제한하고 전체 repository graph를 기본 표시하지 않는다.
- BR-04: Structural status, semantic status, freshness와 evidence scope는 독립 field로 보존한다.
- BR-05: `SemanticMapIR`이 renderer-neutral canonical map이며 기존 FlowSpec은 versioned projection이다.
- BR-06: Projection은 지원 consumer, source IR version과 손실되는 field를 명시한다.
- BR-07: Active pointer는 schema, reference, checksum, coverage와 workspace epoch 검증이 모두 성공한 complete generation만 가리킨다.

## 8. Acceptance Criteria

- A1. WHEN 개발자가 지원 repository의 feature, debug 또는 onboarding query를 요청하면, THE system SHALL model과 runtime observer를 기다리지 않고 current snapshot의 Semantic Map을 FlowView에 제공한다.
- A2. THE system SHALL 기본 map을 7~15개의 시작, 판단, 상태 변화, external effect와 결과 행동으로 제한한다.
- A3. THE system SHALL 모든 중요한 map node에서 current source, test 또는 contract evidence를 열 수 있게 한다.
- A4. IF target, branch 또는 relation을 확인할 근거가 부족하면, THEN THE system SHALL 해당 항목과 필요한 evidence를 `unknown`, `unresolved` 또는 `unavailable`로 표시하고 확정 Fact를 만들지 않는다.
- A5. THE system SHALL structural status, semantic status, freshness, evidence scope와 coverage를 서로 독립적으로 표시한다.
- A6. WHEN 새 generation의 schema, reference, checksum, coverage 또는 epoch validation이 실패하면, THE system SHALL active pointer를 교체하지 않고 이전 complete generation을 계속 제공한다.
- A7. IF adapter, optional model, runtime observer, query projection 또는 renderer 일부가 실패하면, THEN THE system SHALL 가능한 deterministic artifact와 이전 valid generation을 보존하고 capability limitation을 표시한다.
- A8. THE system SHALL SemanticMapIR에서 versioned FlowSpec projection을 재생성하고 consumer별 지원 version과 projection loss를 확인 가능하게 한다.
- A9. THE first implementation SHALL loopback FlowView에서 완료 가능하며 VS Code extension을 요구하지 않는다.
- A10. THE system SHALL FlowView에서 Business Summary, ordered Flow Story, selected step의 Evidence Workbench와 근거 기반 Question Lens를 사용자-visible 결과로 제공한다.
- A11. WHEN 사용자가 Question Lens의 질문을 선택하면, THE system SHALL 관련 Flow Story step과 Evidence Workbench 근거를 함께 강조하고 답할 근거가 없으면 필요한 evidence와 `unknown`을 표시한다.

## 9. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| Scope를 결정할 수 없음 | 필요한 query, entry point 또는 change input 표시 | 추측 map 발행 없음 | Scope input 보강 후 재요청 |
| Adapter 일부 실패 | Partial 또는 unavailable capability와 unknown 표시 | 불완전 generation을 active로 공개하지 않음 | Adapter 복구 후 새 snapshot 분석 |
| Evidence validation 실패 | Invalid anchor 또는 reference 진단 | 해당 Fact와 generation 발행 없음 | Current source에서 재분석 |
| Stale epoch 또는 cancelled request | Result discarded 상태 | Active pointer 변경 없음 | Current epoch에서 재요청 |
| Projection 또는 renderer 실패 | Canonical SemanticMapIR 유지 | Canonical artifact 수정 없음 | Projection 재생성 또는 FlowView 복구 |
| Storage publish 실패 | 이전 complete generation 표시 | Pointer 변경 없음 | Staging validation과 storage 복구 후 재발행 |

## 10. Data and Interaction Contract

- Input: task query와 type, repository root, entry point 또는 flow reference, current change context와 optional scope limit
- `AnalysisSnapshot`: snapshot ID, basis SHA, repository snapshot, capability, Fact, relation, change-impact placeholder, unknown, coverage와 evidence
- `SemanticMapIR`: map·generation·basis identity, task, node, edge, coverage summary, unknown, question과 warning
- FlowSpec projection: flow·map·generation identity, task, summary, ordered step, status, evidence scope, scenario refs, edge, coverage, delta, unknown과 warning
- Persistence: versioned JSON generation이 canonical이며 query projection은 삭제 후 재생성 가능하다.
- Identity: generation의 canonical artifact는 동일한 `basisSha`, schema version, workspace epoch와 generation ID를 사용한다.

## 11. Test Seam and Evidence

- Public seam: CLI/MCP analysis call, published generation files, loopback FlowView HTTP API
- Required test level: schema valid·invalid fixture, Core integration, storage failure test, MCP end-to-end와 browser-visible FlowView behavior test
- Replaceable external boundaries: language adapter, CodeGraph candidate source, clock, storage publisher와 renderer
- Evidence required per criterion:
  - A1, A2, A3, A5, A9, A10, A11: end-to-end fixture에서 FlowView response, 네 UX surface와 evidence navigation
  - A4: unresolved target fixture에서 Fact absence와 unknown presence
  - A6, A7: fault injection과 pointer preservation
  - A8: canonical IR→projection compatibility fixture

## 12. What Could Be Wrong

- Assumption: task query와 entry evidence로 7~15개의 관련 행동을 안정적으로 선택할 수 있다.
- Consequence: 중요한 행동 누락 또는 관련 없는 행동 노출로 사용자가 기능을 잘못 이해한다.
- Validation method: 고정 query set에서 omission, correction, 재확장률과 실제 task 설명 정확도를 측정한다.

## 13. Done When

- Every criterion has passing evidence.
- 새 schema에 valid fixture 1개 이상과 서로 다른 normative rejection을 검증하는 invalid fixture 2개 이상이 있다.
- `go test ./...`와 FlowView의 사용자-visible 검증이 통과한다.
- Model과 runtime observer가 없는 end-to-end test가 통과한다.
- No contract requirement is weakened.
- No unrelated behavior changes.

## 14. Open Decisions

없음.
