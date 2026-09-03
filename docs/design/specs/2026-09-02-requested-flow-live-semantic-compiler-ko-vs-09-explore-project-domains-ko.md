# 요청 흐름 이해와 실시간 Semantic Compiler — VS-09 프로젝트의 주요 책임을 탐색한다

- Contract ID: `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-VS-09`
- Contract Status: Proposed
- Independent Review: Passed (`gpt-5.6-terra`, high)
- Parent Contract: `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md`
- Created: 2026-09-02
- Source: `docs/design/raw/requested-flow-live-semantic-compiler-architecture-draft-ko.md`
- Decision Records: `docs/design/decisions/requested-flow-live-semantic-compiler-decisions-ko.md`

## 1. Intent and Goal

- Intent ID: `INT-01`
- Parent Intent: 사용자가 요청한 흐름을 현재 코드 근거와 unknown을 포함한 이해 가능한 결과로 파악한다.
- Goal ID: `GOAL-09`
- Slice Goal: 사용자는 전체 graph를 펼치지 않고 domain 책임, 대표 흐름, glossary와 근거를 단계적으로 탐색한다.
- User or Caller Value: 익숙하지 않은 프로젝트에서도 저장소 전체를 읽지 않고 주요 책임과 대표 user flow의 위치를 파악할 수 있다.
- Contribution to Parent: Raw P5 onboarding을 domain summary → representative flow → evidence/glossary로 이어지는 observable progressive-disclosure outcome으로 만든다.
- Parent Acceptance: `A16`, `A22–A23`, `A25–A27`

## 2. User Outcome

사용자가 repository를 선택하면 시스템은 domain과 책임 요약, 대표 flow 선택 근거와 coverage를 먼저 제공하고, 선택한 flow의 전체 SemanticMapIR과 7~15개 projection, ownership·glossary·Evidence로 단계적으로 진입하게 한다.

## 3. Scope

### In Scope

- `mode=onboarding`과 required `repositoryId`
- AnalysisSnapshot/SCIP/README/contract/test 기반 domain·ownership mining
- domain summary와 representative flow ranking
- selection evidence, coverage boundary와 low-coverage honest labeling
- domain → representative flows → flow rail → ownership/glossary/evidence progressive disclosure
- VS-02/04 canonical generation과 VS-08 approved semantic overlay의 재사용
- 전체 repository graph를 기본 표시하지 않는 task-scoped onboarding UX

### Out of Scope

- 새로운 structural analysis authority 또는 adapter protocol — VS-01/02가 담당한다.
- live edit publication, review/impact/debug/incident query — VS-03–VS-07이 담당한다.
- model을 필수 조건으로 만드는 행위와 model proposal 승인 자체 — VS-08이 담당한다.
- 사용자 repository를 기본 benchmark corpus로 수집하는 행위 — VS-10 release policy가 다룬다.

## 4. Preconditions

- `repositoryId`와 읽을 수 있는 snapshot/index/catalog이 있다.
- domain·representative flow 후보의 source, contract, test 또는 approved semantic evidence가 조회된다.
- low coverage와 unknown boundary를 표현할 projection seam이 존재한다.
- VS-02의 FlowView projection과 VS-04의 generation status를 사용할 수 있다.

## 5. Public Seam

Onboarding Task View Query, domain summary, representative flow list, coverage/selection evidence, Flow Rail, glossary·ownership·CodeLens가 공개 seam이다. 사용자는 선택한 domain/flow를 단계적으로 펼치며 current/last_verified 상태를 확인한다.

## 6. Boundary Coverage

Repository ID → snapshot/index/docs/contracts/tests catalog → domain/ownership miner → representative flow ranker → coverage and selection evidence → progressive onboarding projection → canonical FlowView/Evidence/glossary.

## 7. Inherited Invariants

- `INV-01`, `INV-02`, `INV-03`, `INV-04`, `INV-05`, `INV-08`, `INV-12`, `INV-13`, `INV-15`, `INV-19`, `INV-25`
- Raw D2, D6, D11, D12, D22, D32 및 §8.2, §8.9, §9.2–§9.3, §9.7, §18.5 R13–R14, §22.6.
- onboarding은 프로젝트 전체를 설명한다고 주장하지 않으며 coverage와 selection evidence를 표시한다.

## 8. Slice-Specific Rules

- `repositoryId`는 필수이며 없으면 임의의 current repository 또는 default domain을 선택하지 않는다.
- domain summary와 representative flow ranking은 source/catalog/test/contract Evidence와 selection rationale을 갖는다.
- coverage가 낮거나 capability가 부족하면 범위를 축소해 설명하고 unknown/unavailable을 표시한다.
- Flow Rail은 canonical SemanticMapIR 전체를 유지하며 7~15 display budget과 critical boundary 보존을 따른다.
- Answer Strip, Flow Rail, Change Pulse, Evidence Dock의 기본 위치와 stable step identity는 mode 변경에도 유지한다.
- approved semantic overlay는 구조 Fact를 변경하지 않으며 model이 없을 때 deterministic domain/flow label을 사용한다.

## 9. Acceptance Criteria

- VS09-A1. WHEN `repositoryId`가 있는 onboarding query가 주어지면, THE system SHALL domain과 책임 요약을 selection Evidence와 coverage boundary와 함께 제공한다.
- VS09-A2. IF `repositoryId`가 없거나 repository catalog/capability를 읽을 수 없으면, THEN THE system SHALL typed `missing_precondition` 또는 `unsupported_capability`를 반환하고 전체 graph를 추정하지 않는다.
- VS09-A3. THE system SHALL representative flow를 선택한 rationale, source/test/contract Evidence와 coverage로 제공한다.
- VS09-A4. WHEN 사용자가 domain과 representative flow를 선택하면, THE system SHALL 전체 canonical flow와 7~15개 projection을 단계적으로 제공하고 critical entry/result/branch/failure/effect/unknown boundary를 보존한다.
- VS09-A5. THE system SHALL ownership, glossary와 selected flow Evidence로 이동할 수 있게 하며, model이 없어도 deterministic onboarding result를 제공한다.
- VS09-A6. IF coverage가 낮거나 unresolved dynamic relation이 남으면, THEN THE system SHALL “프로젝트 전체 설명”으로 표현하지 않고 해당 경계와 unknown을 표시한다.
- VS09-A7. WHEN generation 또는 mode가 바뀌면, THE system SHALL stable layout, selected step, scroll과 Evidence context를 가능한 범위에서 유지한다.
- VS09-A8. THE system SHALL onboarding payload에 등록된 schema identity, basis, generation과 Evidence references를 제공하고 source를 수정하지 않는다.

## 10. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| repositoryId missing | missing-precondition error | domain summary 없음 | repository 지정 |
| catalog/index unavailable | unsupported/partial coverage | 전체 project claim 없음 | index 또는 supported capability 복구 |
| representative flow evidence 부족 | low-confidence/unknown selection | flow를 대표 사실로 확정하지 않음 | 추가 Evidence 또는 user selection |
| low coverage | coverage boundary와 unknown 표시 | 전체 graph/전체 프로젝트 주장 없음 | domain 범위 축소 또는 보강 |
| generation stale | last_verified와 affected scope | 오래된 summary를 current로 가장하지 않음 | 최신 generation 재조회 |
| selected flow removed | tombstone/selection reset reason | 다른 flow로 조용히 대체하지 않음 | 사용자가 새 flow 선택 |

## 11. Data and Interaction Contract

- Input: `mode=onboarding` query with `repositoryId`, optional domain and display filters, current/basis selector.
- Output: domain summaries, representative flow refs, selection rationale, coverage/unknowns, FlowViewProjection, ownership/glossary and Evidence refs.
- Onboarding result는 canonical SemanticMapIR 또는 generation을 참조하고 projection은 같은 generation의 step refs만 사용한다.
- Coverage와 selection rationale은 domain/flow 선택 근거이며 product 전체 completeness 또는 implementation settlement를 의미하지 않는다.
- Mode-specific query failure는 typed error로 반환하며 임의 default scope를 만들지 않는다.

## 12. Test Seam and Evidence

- Public seam: onboarding query, domain summary, representative flow list, coverage, glossary/ownership and FlowView navigation.
- Required test level: precondition, domain selection, representative ranking evidence, low coverage, unknown, full-flow/projection preservation, stable layout and deterministic fallback.
- Replaceable external boundaries: SCIP/index provider, docs/contracts/tests catalog, generation store, semantic overlay, clock and FlowView projector.
- Evidence per criterion: VS09-A1/A2 query/domain fixture, A3 ranking fixture, A4 projection fixture, A5 navigation/fallback, A6 coverage negative fixture, A7 stable-layout fixture, A8 schema/source read-only test.

## 13. Verification Plan

| Check | Applicability / Trigger | Exact Command | Expected Evidence | Required for Completion |
|---|---|---|---|---|
| Acceptance behavior | Always | `go test ./internal/mcp ./internal/harvest ./internal/fusion ./internal/flowview ./internal/e2e` | onboarding query, domain/flow selection, coverage, evidence and projection pass | Yes |
| Slice tests | Always | `go test ./internal/mcp ./internal/harvest ./internal/fusion ./internal/flowview` | VS-09 package tests pass | Yes |
| Type, static analysis, and lint | onboarding/catalog/projection code affected | `go vet ./...` | no applicable findings | When applicable |
| Affected build | Core or FlowView affected | `go build ./...` | Core builds successfully | When applicable |
| Architecture and policy | onboarding payload or refs affected | `go test ./internal/contractharness -run 'TestValidateGoldenFixtures|TestValidateExportedContractBoundary'` | domain, flow, projection, coverage and Evidence fixtures validate | Yes |
| Regression | shared harvest/fusion/FlowView behavior affected | `go test ./...` | full Go regression suite passes | Yes |
| Security | source/docs/contract Evidence is exposed | `go test ./internal/secret ./internal/fusion ./internal/flowview` | redaction and source read-only behavior pass | Yes |
| Data and migration compatibility | catalog/generation/projection refs affected | `go test ./internal/contractharness` | basis, generation and schema compatibility pass | Yes |
| Performance and concurrency | no numeric onboarding target is defined | `N/A — raw requires progressive disclosure and evaluation, not a repository performance threshold for this slice` | reason recorded | No |
| Reliability and flake | index/generation unavailable or flow removed | `go test ./internal/mcp ./internal/harvest ./internal/e2e -count=1` | repeatable partial/unknown and selection recovery | Yes |
| Coverage | raw coverage is an output and release metric, not a repository threshold | `N/A — no coverage percentage command is configured` | reason recorded | No |
| Browser UX | onboarding, progressive disclosure, stable selection and Evidence navigation are user-facing; raw §21.10 UX verification is required before completion | `npm --prefix web/live-comprehension-workspace run test:e2e -- --project=chromium` | Playwright proves domain/flow selection, progressive disclosure, low-coverage/unknown state, stable selection and glossary/Evidence navigation | Yes |
| Accessibility | onboarding and progressive-disclosure states are user-facing | `npm --prefix web/live-comprehension-workspace run test:a11y` | `@axe-core/playwright`, keyboard navigation, screen-reader outline, reduced motion and contrast evidence pass | Yes |

## 14. What Could Be Wrong

- Assumption: domain signals and representative-flow ranking can provide a useful first view without implying repository-wide completeness.
- Consequence: onboarding hides the most important flow or presents a low-coverage flow as representative.
- Validation method: fixed repositories with labeled domains, ownership, representative flows, coverage boundaries and user task questions.

## 15. Done When

- VS09-A1–A8 provide evidence for progressive onboarding and honest coverage.
- No default result claims full-project understanding when domain/flow evidence is partial.
- Canonical flow, projection, glossary and Evidence navigation share generation identity.
- Applicable verification rows pass or N/A reasons are explicit.
- No production code, schema or raw source is changed by this slicing task.

## 16. Open Decisions

없음. onboarding ranking과 coverage 표시 및 결정 사항은 아래 Resolved Implementation Decisions로 확정되었다.

## 17. Resolved Implementation Decisions

- `SID-C1` (정규 payload 물리 구성): `schemas/domain-overview.schema.json`, `schemas/representative-flow-catalog.schema.json` 독립 스키마 분할 및 BaseURL 등록.
- `SID-C2` (validation 경계): 프로젝트 전체 도메인 클러스터링 유효성 검증, 대표 흐름 랭킹 점수 정합성 검증, 저커버리지 도메인의 정직한 Unknown 표시 검증.
- `SID-C3` (계약 검증 범위): 도메인 마이닝 픽스처, 대규모 저장소 계층 집계 픽스처 구축.
- `SID-C4` (기존 구현 재사용/교체): `internal/fusion` 아키텍처 맵과 VS-02 `SemanticMapIR`의 요약 집계 레이어 재사용.
- `SID-C5` & `SID-09` (도메인 마이닝 신호, 대표 흐름 랭킹, Onboarding Critical Obligation):
  - 디렉터리 경로, 모듈/패키지 선언, 진입점(UI/API) 호출 빈도, 설정 마커를 결합한 결정론적 도메인 스코어링 적용.
  - Onboarding Mode Settlement Gate 입력: 프로젝트 상위 3~5개 핵심 사용자 여정(Core Flows) 식별 완료 및 미확인 영역(`uncovered_domain_ratio`)의 명시적 수치 제시.
