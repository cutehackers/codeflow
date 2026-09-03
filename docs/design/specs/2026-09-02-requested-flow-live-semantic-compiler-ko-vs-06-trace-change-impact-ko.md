# 요청 흐름 이해와 실시간 Semantic Compiler — VS-06 변경 영향 범위를 추적한다

- Contract ID: `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-VS-06`
- Contract Status: Proposed
- Independent Review: Passed (`gpt-5.6-terra`, high)
- Parent Contract: `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md`
- Created: 2026-09-02
- Source: `docs/design/raw/requested-flow-live-semantic-compiler-architecture-draft-ko.md`
- Decision Records: `docs/design/decisions/requested-flow-live-semantic-compiler-decisions-ko.md`

## 1. Intent and Goal

- Intent ID: `INT-01`
- Parent Intent: 사용자가 요청한 흐름을 현재 코드 근거와 unknown을 포함한 이해 가능한 결과로 파악한다.
- Goal ID: `GOAL-06`
- Slice Goal: 사용자는 변경 symbol 또는 change batch에서 제한된 caller, state, external effect, API와 test 영향을 확인한다.
- User or Caller Value: 전체 repository graph를 펼치지 않고 변경을 검토하거나 수정하기 전에 직접 영향, 한 단계 간접 영향과 추가 탐색 경계를 판단할 수 있다.
- Contribution to Parent: Raw P3 impact mode를 task-scoped projection으로 제공하고, 미확인 dynamic caller와 coverage 경계를 숨기지 않는다.
- Parent Acceptance: `A16`, `A19`, `A23`, `A25–A27`

## 2. User Outcome

사용자가 `symbolId` 또는 `changeBatchId`를 지정하면 시스템은 변경 대상에서 caller를 역방향으로, state·external effect·test를 순방향으로 제한적으로 확장하고 각 관계를 Evidence 또는 unknown으로 표시한다.

## 3. Scope

### In Scope

- `mode=impact` query와 `symbolId`/`changeBatchId` 필수 precondition
- changed symbol resolver와 active task scope 연결
- caller reverse slice와 state/external/test forward slice
- direct impact, bounded indirect impact, additional exploration 경계
- related flow, state mutation, API/persistence, external effect, test 연결
- unresolved dynamic caller와 unsupported capability의 unknown 표시
- 같은 SemanticMapIR/Evidence/Current Proof 기반의 impact projection

### Out of Scope

- 전체 transitive dependency graph 또는 repository-wide ownership map — raw가 기본 표시에서 제외한다.
- review의 baseline/current semantic delta — VS-05가 담당한다.
- runtime incident correlation과 semantic model enrichment — VS-07과 VS-08이 담당한다.

## 4. Preconditions

- VS-02의 SemanticMapIR와 Evidence index가 존재한다.
- VS-04의 current proof 또는 explicit gap 상태를 조회할 수 있다.
- query에 변경 symbol 또는 change batch가 있고 관련 repository scope가 식별된다.
- 현재 adapter capability와 coverage boundary를 읽을 수 있다.

## 5. Public Seam

Impact Task View Query, bounded impact response, selected symbol/change batch, flow step, Evidence Dock와 CodeLens가 공개 seam이다. 사용자는 direct/indirect/unknown 관계를 확장하고 source/test/contract Evidence로 이동한다.

## 6. Boundary Coverage

Changed symbol/change batch → impact target resolver → caller reverse traversal + state/external/test forward traversal → bounded impact facts → Evidence/unknown validation → impact projection → caller/FlowView result.

## 7. Inherited Invariants

- `INV-01`, `INV-02`, `INV-03`, `INV-04`, `INV-05`, `INV-08`, `INV-12`, `INV-13`, `INV-14`, `INV-15`, `INV-19`
- Raw D2, D11, D22, D23, §8.2, §8.6, §10.3–§10.5, §18.5 R13–R14.
- 변경과 관련성이 unknown이면 보수적으로 scope/closure 충돌 또는 unknown으로 처리한다.

## 8. Slice-Specific Rules

- impact query는 `symbolId` 또는 `changeBatchId` 중 하나를 요구하며 둘 다 없으면 `missing_precondition`을 반환한다.
- 확장 순서는 changed symbol → direct callers/related flows → one-step indirect callers and effects → explicit additional exploration으로 제한한다.
- caller, state, API, persistence, external effect와 test 관계는 current Evidence 또는 adapter observation으로 뒷받침한다.
- 동적 target, 미지원 capability, 닫히지 않은 frontier는 확정 관계가 아니라 unknown/unresolved로 표시한다.
- 영향 결과는 active Task Intent와 관련된 working set에 한정하고 전체 graph를 기본 결과로 발행하지 않는다.
- impact projection은 canonical SemanticMapIR의 step/edge를 삭제하지 않고 visible/folded reference만 만든다.

## 9. Acceptance Criteria

- VS06-A1. WHEN `symbolId` 또는 `changeBatchId`가 있는 impact query가 주어지면, THE system SHALL 해당 변경 대상을 active task scope와 연결해 impact analysis를 시작한다.
- VS06-A2. IF 두 변경 시작 조건이 모두 없거나 target을 해석할 capability가 없으면, THEN THE system SHALL typed `missing_precondition` 또는 `unsupported_capability`를 반환하고 임의의 repository scope를 사용하지 않는다.
- VS06-A3. THE system SHALL changed symbol에서 direct caller reverse slice와 state, API, persistence, external effect, test forward slice를 구분해 제공한다.
- VS06-A4. THE system SHALL direct impact, bounded indirect impact와 additional exploration boundary를 별도 표시하고 전체 transitive graph를 기본 결과로 표시하지 않는다.
- VS06-A5. WHEN 관계가 current Evidence, read-set observation 또는 closure에 의해 확인되지 않으면, THE system SHALL 해당 관계를 unknown/unresolved로 표시하고 confirmed impact로 만들지 않는다.
- VS06-A6. WHEN 영향 step을 선택하면, THE system SHALL 관련 flow, state delta, external effect, test와 source Evidence Anchor로 이동할 수 있게 한다.
- VS06-A7. THE system SHALL impact 결과의 basis, coverage boundary, unknown count와 current/last_verified 상태를 함께 표시한다.
- VS06-A8. THE system SHALL source를 수정하지 않고 secret-redacted Evidence만 public impact response와 Evidence Dock에 전달한다.

## 10. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| target missing | missing-precondition error | impact traversal 없음 | symbol 또는 change batch 지정 |
| unsupported analyzer capability | unsupported status와 coverage boundary | 미확인 관계 확정 없음 | 지원 capability 사용 또는 unknown 유지 |
| dynamic caller unresolved | unresolved/unknown caller edge | caller를 confirmed로 표시하지 않음 | 추가 static/runtime Evidence |
| closure open/overlap | last_verified 또는 affected scope overlay | current impact로 가장하지 않음 | 최신 snapshot에서 재분석 |
| traversal limit/overload | bounded partial result와 omitted frontier | 전체 graph 확장 없음 | 사용자 additional exploration 요청 |
| stale anchor | stale/orphaned evidence | 오래된 영향 관계를 current로 유지하지 않음 | anchor relink 또는 새 generation |

## 11. Data and Interaction Contract

- Input: `mode=impact` query, `symbolId` 또는 `changeBatchId`, Task Intent revision, basis selector와 display filters.
- Output: bounded impact nodes/edges, direct·indirect classification, related flow/state/effect/test refs, Evidence, unknowns와 coverage.
- Impact result는 current generation의 `generationId`, `computedBasisId`, `validatedAgainstSnapshotId`와 closure 상태를 참조한다.
- Additional exploration은 명시적 user/caller action이며 기본 query가 전체 graph를 확장하는 방식이 아니다.

## 12. Test Seam and Evidence

- Public seam: impact query, bounded response, selected symbol/change batch, source/test/contract navigation.
- Required test level: precondition, direct/indirect boundary, caller reverse, effect/test forward, dynamic unknown, closure gap, stale anchor and redaction fixtures.
- Replaceable external boundaries: graph/index provider, adapter capability, generation store, clock, Evidence resolver and projector.
- Evidence per criterion: VS06-A1/A2 query matrix, A3/A4 bounded traversal fixture, A5 unknown fixture, A6 navigation test, A7 basis/coverage response, A8 secret fixture.

## 13. Verification Plan

| Check | Applicability / Trigger | Exact Command | Expected Evidence | Required for Completion |
|---|---|---|---|---|
| Acceptance behavior | Always | `go test ./internal/mcp ./internal/fusion ./internal/slicing ./internal/flowview ./internal/e2e` | impact query, bounded traversal, unknown and evidence navigation pass | Yes |
| Slice tests | Always | `go test ./internal/mcp ./internal/fusion ./internal/slicing ./internal/flowview` | VS-06 package tests pass | Yes |
| Type, static analysis, and lint | traversal/projection code affected | `go vet ./...` | no applicable findings | When applicable |
| Affected build | Core or FlowView affected | `go build ./...` | Core builds successfully | When applicable |
| Architecture and policy | impact payload boundary changes | `go test ./internal/contractharness -run 'TestValidateGoldenFixtures|TestValidateExportedContractBoundary'` | impact, unknown and evidence refs validate | Yes |
| Regression | shared graph/fusion/query behavior affected | `go test ./...` | full Go regression suite passes | Yes |
| Security | source and test Evidence is exposed | `go test ./internal/secret ./internal/fusion ./internal/flowview` | redaction and source read-only behavior pass | Yes |
| Data and migration compatibility | graph/index and generation refs affected | `go test ./internal/contractharness` | basis and reference compatibility pass | Yes |
| Performance and concurrency | no numeric impact-mode target is defined | `N/A — raw assigns impact comprehension and boundedness to quality evaluation, not a numeric command` | reason recorded | No |
| Reliability and flake | graph traversal and unknown handling affected | `go test ./internal/mcp ./internal/fusion ./internal/e2e -count=1` | repeatable bounded traversal and stale behavior | Yes |
| Coverage | no repository coverage percentage threshold is configured | `N/A — no coverage percentage command is configured` | reason recorded | No |
| Browser UX | bounded impact, unknown boundary and Evidence navigation are user-facing; raw §21.10 UX verification is required before completion | `npm --prefix web/live-comprehension-workspace run test:e2e -- --project=chromium` | Playwright proves bounded direct/indirect impact, additional-exploration boundary, unknown state and source/test/contract navigation | Yes |
| Accessibility | impact and Evidence states are user-facing | `npm --prefix web/live-comprehension-workspace run test:a11y` | `@axe-core/playwright`, keyboard navigation, screen-reader outline, reduced motion and contrast evidence pass | Yes |

## 14. What Could Be Wrong

- Assumption: the available dependency/index frontier is sufficient to identify direct and one-step indirect impact for the active task.
- Consequence: the result is falsely narrow or expands into unrelated repository noise.
- Validation method: labeled impact fixtures with added callers, state writes, external calls, tests and unresolved dynamic edges.

## 15. Done When

- VS06-A1–A8 have evidence at the bounded impact public seam.
- Direct, bounded indirect and unknown impact are distinct and all important results have Evidence navigation.
- The default result does not expose the entire repository graph or invent dynamic targets.
- Applicable verification rows pass or N/A reasons are explicit.
- No production code, schema or raw source is changed by this slicing task.

## 16. Open Decisions

없음. impact mode의 확장 경계와 결정 사항은 아래 Resolved Implementation Decisions로 확정되었다.

## 17. Resolved Implementation Decisions

- `SID-C1` (정규 payload 물리 구성): `schemas/change-impact-graph.schema.json` 독립 스키마 분할 및 BaseURL 등록.
- `SID-C2` (validation 경계): 직접 영향(1차), 간접 영향(2차), 추가 탐색 경계의 깊이 제한 및 순환 참조 방지 검증.
- `SID-C3` (계약 검증 범위): 직접 vs 간접 vs 미해결 프론티어 영향 전파 픽스처, cyclic graph 차단 픽스처 구축.
- `SID-C4` (기존 구현 재사용/교체): 언어별 어댑터의 역방향 호출 그래프 및 의존성 추출기 재사용.
- `SID-C5` & `SID-06` (실행 예산, 동적 호출자 경계, Impact Critical Obligation):
  - 직접 영향(1차 호출자): 100% 전수 탐색 및 증거 바인딩.
  - 간접 영향(2차 호출자): 최대 깊이 5, 최대 노드 50개 소프트 예산 적용. 동적 디스패치나 리플렉션 등으로 정적 추적이 불가한 경계는 `unresolved_dynamic_caller`로 명시 분리하여 날조 금지 (`INV-04`).
  - Impact Mode Settlement Gate 입력: 변경 심볼을 참조하는 모든 상위 진입점(UI/API)의 영향 평가 완료 및 미해결 차단 영향(`critical_impact_unknown == 0`) 확인.
