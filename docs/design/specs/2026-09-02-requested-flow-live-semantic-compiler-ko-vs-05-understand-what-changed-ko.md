# 요청 흐름 이해와 실시간 Semantic Compiler — VS-05 무엇이 달라졌는지 이해한다

- Contract ID: `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-VS-05`
- Contract Status: Proposed
- Independent Review: Passed (`gpt-5.6-terra`, high)
- Parent Contract: `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md`
- Created: 2026-09-02
- Source: `docs/design/raw/requested-flow-live-semantic-compiler-architecture-draft-ko.md`
- Decision Records: `docs/design/decisions/requested-flow-live-semantic-compiler-decisions-ko.md`

## 1. Intent and Goal

- Intent ID: `INT-02`
- Parent Intent: coding agent 구현 변경이 현재 요청 흐름의 의미를 최신성·근거·차이와 함께 계속 파악한다.
- Goal ID: `GOAL-05`
- Slice Goal: 사용자는 baseline과 current 사이의 added·changed·removed 행동과 요청 acceptance criterion의 현재 정렬 상태를 확인한다.
- User or Caller Value: 파일 수나 원시 line diff가 아니라 사용자가 검토해야 할 행동·규칙·상태·effect 차이를 먼저 판단할 수 있다.
- Contribution to Parent: Raw P3의 Semantic Delta와 Requirement Alignment를 하나의 비교 가능한 review outcome으로 만들고 구현 완료 오인을 줄인다.
- Parent Acceptance: `A12–A16`, `A18`, `A23`, `A25–A27`

## 2. User Outcome

사용자가 명시한 baseline과 current를 비교하면 시스템은 구조 변경과 의미 변경을 분리하고, 각 acceptance criterion에 대해 current Evidence 기준의 `confirmed`, `partial`, `not_observed`, `conflicting` 또는 `unknown` 상태를 표시한다. 요청 해석 확인과 구현 정렬은 별도 상태로 보인다.

## 3. Scope

### In Scope

- review mode의 baseline/current query와 비교 가능성 검증
- same repository/epoch/schema/analyzer basis 확인
- SemanticDeltaIR의 `added_behavior`, `changed_rule`, `removed_behavior`, `structural_only`, `evidence_updated` 구분
- changed step의 상태 변화, branch, external effect, failure와 affected flow 연결
- RequirementAlignment와 acceptance criterion별 Evidence mapping
- evidence 변경 시 stale/orphaned/historical 처리
- raw diff와 file list를 보조 정보로 유지하고 semantic change를 먼저 표시
- current FlowView의 Change Pulse, Requirement Alignment와 Evidence Dock 연결

### Out of Scope

- 새로운 revision/snapshot과 current proof 생성 — VS-03과 VS-04가 담당한다.
- caller·external effect·test frontier의 bounded impact 탐색 — VS-06이 담당한다.
- debug/incident runtime correlation과 model semantic proposal — VS-07과 VS-08이 담당한다.

## 4. Preconditions

- VS-02의 canonical SemanticMapIR, Evidence와 Requirement 기준이 존재한다.
- VS-04가 baseline과 current generation을 proof manifest로 조회할 수 있게 한다.
- 두 generation의 repository, workspace epoch, schema/analyzer compatibility를 확인할 수 있다.
- Task Intent revision과 acceptance criterion ID가 비교 session에 연결된다.

## 5. Public Seam

Review Task View Query, baseline pin/reference, Semantic Delta response, Requirement Alignment board, Change Pulse와 Evidence Dock이 공개 seam이다. 사용자는 두 generation을 선택하고 당시 결과를 preview하거나 current로 돌아온다.

## 6. Boundary Coverage

Base generation/baseline → current generation → structural and semantic comparison → change decomposition → requirement/evidence alignment → review projection/Change Pulse → source·test·contract Evidence.

## 7. Inherited Invariants

- `INV-01`, `INV-02`, `INV-03`, `INV-04`, `INV-09`, `INV-10`, `INV-12`, `INV-15`, `INV-19`, `INV-22`
- Raw D2, D7, D14, D15, D20, D29 및 §8.2, §9.9–§9.12, §10.8–§10.13, §12.5–§12.6.
- `not_observed`는 coverage가 충분한 경우에도 구현 부재의 증명으로 자동 해석하지 않는다.

## 8. Slice-Specific Rules

- review query는 baseline과 current를 모두 요구하며 서로 다른 repository/epoch 또는 호환되지 않는 schema/analyzer면 `incomparable_basis`를 반환한다.
- line/file 변경은 semantic delta가 아니며, 행동·조건·상태·external effect·failure·requirement 관계의 변화를 근거와 함께 분류한다.
- `confirmed` RequirementAlignment는 current basis에서 criterion을 뒷받침하는 critical step과 검증된 Evidence가 모두 있을 때만 사용한다.
- agent completion declaration, model proposal 또는 사용자 intent confirmation만으로 current implementation을 confirmed로 만들지 않는다.
- 같은 행동의 근거만 바뀌면 `evidence_updated`로 표시하고, source symbol 삭제·이동과 anchor 불일치는 stale/orphaned/historical로 표시한다.

## 9. Acceptance Criteria

- VS05-A1. WHEN baseline과 current가 제공되면, THE system SHALL 두 basis의 repository, workspace epoch, schema와 analyzer 호환성을 검증한 뒤 비교를 실행한다.
- VS05-A2. IF baseline과 current가 비교 불가하거나 하나가 없으면, THEN THE system SHALL typed `missing_precondition` 또는 `incomparable_basis`를 반환하고 임의의 baseline을 선택하지 않는다.
- VS05-A3. THE system SHALL structural delta와 semantic delta를 별도 결과로 제공하고, semantic delta에서 added, changed, removed behavior와 evidence-only change를 구분한다.
- VS05-A4. WHEN 행동, branch, state, external effect 또는 failure가 달라지면, THE system SHALL 해당 변경을 영향을 받은 flow step과 current Evidence에 연결한다.
- VS05-A5. WHEN acceptance criterion을 비교하면, THE system SHALL current Evidence 기준의 `confirmed`, `partial`, `not_observed`, `conflicting` 또는 `unknown` 상태와 missing evidence를 표시한다.
- VS05-A6. IF Evidence가 없거나 agent/model 설명만 존재하면, THEN THE system SHALL RequirementAlignment를 `unknown` 또는 `not_observed`로 유지하고 `confirmed`로 승격하지 않는다.
- VS05-A7. WHEN code, dependency, runtime 또는 framework rule이 바뀌어 기존 anchor가 영향을 받으면, THE system SHALL 해당 evidence/claim을 stale 또는 orphaned로 표시하고 current 사실로 조용히 유지하지 않는다.
- VS05-A8. THE system SHALL Change Pulse에서 semantic change를 raw file event와 raw diff보다 먼저 제공하고, 사용자가 선택한 generation의 source·test·contract evidence로 이동할 수 있게 한다.
- VS05-A9. THE system SHALL Task Intent의 `intentStatus`와 RequirementAlignment의 implementation status를 서로 다른 상태로 표시한다.

## 10. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| baseline 없음 | missing-precondition error | 비교/Delta 없음 | baseline pin 또는 generation 지정 |
| basis incompatibility | incomparable-basis error | 임의 rebase 없음 | 호환 generation 재선택 |
| current evidence stale | stale/orphaned claim과 gap | confirmed 유지 없음 | current generation 재분석 |
| evidence 없는 criterion | unknown/not_observed와 missing evidence | 구현 부재 단정 없음 | evidence 추가 또는 scope 확인 |
| static evidence conflict | conflicting alignment/step | 자동 승격 없음 | 추가 evidence 또는 사용자 판단 |
| structural-only change | 접힌 structural delta | semantic behavior 변경으로 표시 안 함 | 상세 요청 시 구조 diff 열기 |
| agent/model-only explanation | proposal/hint label | implementation confirmed 없음 | source/test/contract evidence 검증 |

## 11. Data and Interaction Contract

- Input: `baseline`과 `current` generation/reference, Task Intent revision, acceptance criteria와 optional change batch.
- Output: `SemanticDeltaIR`, `RequirementAlignment`, change pulse, affected flow refs, Evidence refs, stale/orphaned/unknown status.
- `SemanticDeltaIR`는 baseline/current computed basis와 current validated snapshot을 모두 유지한다.
- RequirementAlignment의 `confirmed`는 current basis Evidence와 critical step 검증에만 권위를 둔다. SemanticApproval은 별도 객체다.
- Baseline은 명시적 session generation, user-pinned generation, Git revision 또는 approved release generation 중 하나이며 조용히 바꾸지 않는다.
- review와 Evidence navigation은 product source를 수정하지 않으며, public response에는 secret-redacted Evidence만 포함한다.

## 12. Test Seam and Evidence

- Public seam: review query, baseline selection, Semantic Delta response, alignment board, Change Pulse와 Evidence navigation.
- Required test level: comparable/incomparable basis, added/changed/removed, structural-only, evidence-only, stale/orphaned, alignment status and agent/model negative fixtures.
- Replaceable external boundaries: generation store, clock, evidence index, source/test/contract resolver, FlowView projector.
- Evidence per criterion: VS05-A1/A2 basis fixture, A3/A4 delta gold fixture, A5/A6 alignment matrix, A7 anchor mutation fixture, A8 review UX/integration, A9 status separation fixture.

## 13. Verification Plan

| Check | Applicability / Trigger | Exact Command | Expected Evidence | Required for Completion |
|---|---|---|---|---|
| Acceptance behavior | Always | `go test ./internal/mcp ./internal/fusion ./internal/flowview ./internal/e2e` | review query, delta, alignment, stale evidence와 Change Pulse behavior pass | Yes |
| Slice tests | Always | `go test ./internal/mcp ./internal/fusion ./internal/flowview` | VS-05 package tests pass | Yes |
| Type, static analysis, and lint | comparison/projection code affected | `go vet ./...` | no applicable findings | When applicable |
| Affected build | Core or FlowView affected | `go build ./...` | Core builds successfully | When applicable |
| Architecture and policy | Delta/alignment schemas or refs affected | `go test ./internal/contractharness -run 'TestValidateGoldenFixtures|TestValidateExportedContractBoundary'` | semantic delta and alignment fixtures validate | Yes |
| Regression | shared FlowView or evidence behavior affected | `go test ./...` | full Go regression suite passes | Yes |
| Security | source/test/contract Evidence is exposed | `go test ./internal/secret ./internal/fusion ./internal/flowview` | redaction and evidence scope pass | Yes |
| Data and migration compatibility | baseline/current artifact references affected | `go test ./internal/contractharness` | cross-generation references and schema identity validate | Yes |
| Performance and concurrency | no raw numeric review-mode target is defined | `N/A — raw specifies comprehension evaluation, not a repository performance threshold for review mode` | reason recorded | No |
| Reliability and flake | review generation selection or evidence relink is affected | `go test ./internal/mcp ./internal/fusion ./internal/e2e -count=1` | repeatable comparison and stale handling | Yes |
| Coverage | no repository coverage threshold is configured | `N/A — no coverage percentage command is configured` | reason recorded | No |
| Browser UX | review, delta, alignment, Change Pulse and Evidence navigation are user-facing; raw §21.10 UX verification is required before completion | `npm --prefix web/live-comprehension-workspace run test:e2e -- --project=chromium` | Playwright proves baseline/current selection, added/changed/removed pulse, alignment status, stale/orphaned handling and Evidence navigation | Yes |
| Accessibility | review and Change Pulse states are user-facing | `npm --prefix web/live-comprehension-workspace run test:a11y` | `@axe-core/playwright`, keyboard navigation, screen-reader outline, reduced motion and contrast evidence pass | Yes |

## 14. What Could Be Wrong

- Assumption: stable step and claim identity can match behavior across baseline/current even when source lines move or symbols are renamed.
- Consequence: a structural move is reported as a false removal/addition or a real semantic change is hidden as evidence-only.
- Validation method: rename, move, add, delete, rule-change and anchor-relink gold fixtures with human-labeled expected delta.

## 15. Done When

- VS05-A1–A9 have evidence from the review and alignment seams.
- Structural and semantic changes are distinguishable and every confirmed alignment has current Evidence.
- Agent/model-only claims never become confirmed implementation status.
- Every applicable Verification Plan row passes or has an explicit N/A reason.
- No production code, schema or raw source is changed by this slicing task.

## 16. Open Decisions

없음. review mode의 세부 delta 분류와 결정 사항은 아래 Resolved Implementation Decisions로 확정되었다.

## 17. Resolved Implementation Decisions

- `SID-C1` (정규 payload 물리 구성): `schemas/semantic-delta-ir.schema.json`, `schemas/requirement-alignment.schema.json` 독립 스키마 분할 및 BaseURL 등록.
- `SID-C2` (validation 경계): 안정된 step/claim identity 매칭 검증, 의미적 추가/수정/삭제/미변경 4대 범주 분류 검증, Requirement Alignment의 근거 연결성 검증.
- `SID-C3` (계약 검증 범위): rename/move 판정 골든 픽스처, delta 4대 분류 픽스처, requirement alignment cross-validation 픽스처 구축.
- `SID-C4` (기존 구현 재사용/교체): VS-02의 `SemanticMapIR`을 $T_{prev}$, $T_{curr}$ 쌍으로 입력받아 결정론적 차이 계산 엔진 구현.
- `SID-C5` (운영 한계): 원시 라인 diff를 나열하지 않고 비즈니스 행동 단위(추가, 변경, 제거된 행동/조건/효과)로 요약하여 전달.
- `SID-C6` & `SID-05` (Stable Identity, Rename, Review Critical Obligation):
  - 파일 내 라인 이동이나 심볼 이름 변경 시 AST 호출/선언 구조 핑거프린트 매칭을 통해 단순 삭제/추가가 아닌 rename/move로 정밀 판정.
  - Review Mode Settlement Gate 입력: Task Intent의 모든 필수 수용 기준에 대해 `RequirementAlignment.status == 'aligned'`이며, `unresolvedCriticalCount == 0`, `conflictingCriticalCount == 0` 충족.
