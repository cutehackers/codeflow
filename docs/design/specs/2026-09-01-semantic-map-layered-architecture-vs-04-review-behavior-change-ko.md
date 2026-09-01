# Semantic Map Layered Architecture — VS-04 동작 변경 검토

- Contract ID: `SMAP-VS-04`
- Contract Status: Proposed
- Independent Review: Passed
- Parent Contract: `docs/design/specs/2026-09-01-semantic-map-layered-architecture-ko.md`
- Created: 2026-09-01
- Depends On: SMAP-VS-02
- Parent Acceptance Coverage: FA-02, FA-04, FA-09, FA-10, FA-11

## 1. User Outcome

Reviewer는 baseline과 current snapshot 사이에서 추가, 변경, 제거된 동작과 영향받는 flow, state, API, test 및 unresolved risk를 파일 diff보다 먼저 확인하고 근거로 이동할 수 있다.

## 2. Scope

### In Scope

- Baseline과 current repository snapshot 비교
- 같은 schema와 analyzer version에서의 structural delta
- Added, changed, removed behavior와 state transition·external contract 변화
- 영향받는 caller, flow, API, test와 boundary
- Current evidence와 baseline evidence navigation
- 변경된 evidence와 semantic claim의 stale·orphaned 처리
- Review·impact task의 Behavior Delta FlowView projection

### Out of Scope

- PR 또는 task 근거가 없는 변경 이유 생성 — 확인 근거 없이는 unknown으로 남긴다.
- Source code diff 도구 대체 — Behavior Delta에서 source diff로 이동할 수만 있다.
- Runtime scenario 실행 — VS-03이 담당한다.
- Semantic proposal 생성과 승인 — VS-05와 VS-06이 담당한다.

## 3. Preconditions

- SMAP-VS-02의 deterministic snapshot과 SemanticMapIR contract가 구현되어 있다.
- Baseline과 current source를 product worktree 수정 없이 읽을 수 있다.
- 두 snapshot에 적용할 schema, analyzer와 framework rule version을 식별할 수 있다.

## 4. Public Seam

- CLI 또는 MCP의 review·impact query와 baseline reference
- FlowView의 Behavior Delta, 영향받는 flow와 evidence selection
- Versioned SemanticMapIR과 FlowSpec projection의 delta field

Public seam은 behavior와 impact를 노출하며 private graph invalidation 방식이나 baseline mirror 구조를 요구하지 않는다.

## 5. Boundary Coverage

Review·impact 요청과 baseline → baseline/current snapshot을 같은 분석 기준으로 생성 → structural Fact와 relation 비교 → affected flow·state·API·test 계산 → stale·orphaned 판정 → SemanticMapIR delta → atomic publish → FlowView Behavior Delta와 evidence

## 6. Inherited Invariants

- INV-01 Current-State Authority
- INV-03 Evidence Grounding
- INV-04 Unknown Preservation
- INV-09 Generation Consistency
- INV-10 Stale Isolation
- INV-11 Source Read-Only
- INV-13 Evidence Navigation
- INV-19 Projection Compatibility

## 7. Slice-Specific Rules

- BR-01: Baseline과 current는 같은 schema, analyzer, framework rule과 relation taxonomy로 비교한다.
- BR-02: Delta는 파일 수가 아니라 added, changed, removed behavior와 영향받는 boundary로 구성한다.
- BR-03: Current evidence가 바뀐 approved 또는 inferred claim은 `stale`, 대응 Fact가 없어진 claim은 `orphaned`로 표시한다.
- BR-04: Removed behavior는 current Fact인 것처럼 유지하지 않고 baseline evidence와 removal status를 보존한다.
- BR-05: Task, PR, ADR 또는 human evidence가 없는 rationale은 생성하지 않고 `unknown`으로 표시한다.
- BR-06: Delta generation validation이 실패하면 이전 complete generation을 유지한다.

## 8. Acceptance Criteria

- A1. WHEN reviewer가 baseline과 current snapshot을 제공하면, THE system SHALL added, changed, removed behavior와 영향받는 flow, state, API와 test를 표시한다.
- A2. THE system SHALL 각 changed 또는 removed behavior에서 baseline evidence와 관련 current evidence 또는 absence evidence로 이동할 수 있게 한다.
- A3. WHEN source, dependency, analyzer 또는 framework rule이 변경되면, THE system SHALL 영향을 받는 evidence와 semantic claim을 `stale` 또는 `orphaned`로 표시한다.
- A4. IF 변경 이유를 뒷받침하는 task, PR, ADR 또는 human evidence가 없으면, THEN THE system SHALL rationale을 생성하지 않고 근거 부족을 표시한다.
- A5. THE system SHALL Behavior Delta를 added behavior, changed rule·branch, removed behavior, state transition change, external contract change, newly affected caller·API·test와 new unknown·coverage loss로 구분한다.
- A6. WHEN delta generation validation이 실패하거나 workspace epoch가 바뀌면, THE system SHALL current active pointer를 교체하지 않는다.
- A7. THE system SHALL review 화면에서 Behavior Delta → 영향받는 flow와 boundary → changed step source → test·runtime verification → unresolved risk 순서로 확인할 수 있게 한다.

## 9. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| Baseline reference를 읽을 수 없음 | Baseline-unavailable failure와 remediation | Current map 또는 source 변경 없음 | Valid commit 또는 snapshot 제공 |
| Analyzer/schema version 불일치 | Incomparable 상태와 version 차이 표시 | 왜곡된 delta 발행 없음 | 같은 version으로 snapshot 재생성 |
| Baseline 일부 분석 실패 | Partial comparison과 coverage gap | Missing behavior를 removed로 확정하지 않음 | 실패 capability 복구 후 재비교 |
| Evidence relink 실패 | Stale 또는 orphaned 표시 | Current claim으로 조용히 유지하지 않음 | 추가 evidence 또는 사용자 검토 |
| Publish validation 실패 | 이전 complete review generation 유지 | Pointer 변경 없음 | Staging artifact 수정 후 재발행 |

## 10. Data and Interaction Contract

- Input: baseline revision 또는 snapshot ID, current snapshot, review·impact query와 optional task·PR·ADR evidence refs
- Delta identity: baseline/current basis SHA, schema, analyzer, framework rule와 generation IDs
- Output: added·changed·removed node refs, removed behavior snapshots, affected flow·symbol·state·API·test refs, stale·orphaned refs, unknown과 warnings
- Evidence: Delta item은 baseline evidence와 current evidence 또는 verified absence를 참조한다.
- Persistence: Baseline mirror와 generated delta는 `.codeflow/` 아래에 격리하고 product checkout을 변경하지 않는다.

## 11. Test Seam and Evidence

- Public seam: review·impact request, published delta artifact와 FlowView review surface
- Required test level: schema fixtures, baseline/current integration, stale·orphaned identity test, storage fault test와 user-visible FlowView test
- Replaceable external boundaries: Git object reader, analyzer, clock, storage publisher와 task evidence provider
- Evidence required per criterion:
  - A1, A5, A7: fixed baseline/current fixture의 Behavior Delta와 UI order
  - A2: source/test navigation result
  - A3: code·dependency·rule change matrix
  - A4: missing intent evidence에서 rationale absence
  - A6: stale epoch와 invalid generation pointer assertion

## 12. What Could Be Wrong

- Assumption: Stable Fact와 node identity가 의미 있는 baseline/current 대응 관계를 유지한다.
- Consequence: Rename 또는 line shift가 제거·추가로 과대 표시되어 reviewer가 실제 behavior change를 구분하지 못한다.
- Validation method: rename, line shift, extraction, branch change와 deletion fixture에서 delta classification을 검증한다.

## 13. Done When

- Every criterion has passing evidence.
- Baseline/current schema와 identity fixture가 rename, removal, stale와 orphaned를 검증한다.
- `go test ./...`와 FlowView review 검증이 통과한다.
- Product checkout 변경 없이 baseline 분석이 수행된다.
- No contract requirement is weakened.
- No unrelated behavior changes.

## 14. Open Decisions

없음.
