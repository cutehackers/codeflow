# 요청 흐름 이해와 실시간 Semantic Compiler — VS-02 자연어로 기능 흐름을 이해한다

- Contract ID: `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-VS-02`
- Contract Status: Approved
- Implementation Status: Completed
- Independent Review: Passed (`gpt-5.6-terra`, high)
- Parent Contract: `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md`
- Created: 2026-09-02
- Source: `docs/design/raw/requested-flow-live-semantic-compiler-architecture-draft-ko.md`
- Decision Records: `docs/design/decisions/requested-flow-live-semantic-compiler-decisions-ko.md`

## 1. Intent and Goal

- Intent ID: `INT-01`
- Parent Intent: 사용자가 요청한 흐름을 현재 코드 근거와 unknown을 포함한 이해 가능한 결과로 파악한다.
- Goal ID: `GOAL-01`
- Slice Goal: 사용자는 자연어 feature 요청으로 entry부터 result까지의 deterministic 흐름, 핵심 행동, 근거와 unknown을 확인한다.
- User or Caller Value: 전체 repository graph와 모델 설명을 읽지 않고도 자신이 묻는 기능의 현재 구현을 설명할 수 있다.
- Contribution to Parent: Raw의 P1 Requested Flow Baseline을 첫 사용자 결과로 고정하고, 이후 live 변경·delta·mode가 참조할 canonical generation을 만든다.
- Parent Acceptance: `A1–A4`, `A15` (deterministic-flow portion; Delta is VS-05 and timeout state is VS-08), `A16–A17`, `A25–A27`

## 2. User Outcome

사용자가 feature query를 제출하면 CodeFlow는 model과 runtime enrichment를 기다리지 않고 요청 범위의 전체 SemanticMapIR과 task-scoped FlowViewProjection을 제공한다. 화면은 시작, 핵심 판단, 상태 변화, external effect, 결과, 근거와 확인되지 않은 경계를 구분한다.

## 3. Scope

### In Scope

- 자연어 request에서 불변 `rawRequest`, normalized intent, acceptance criteria와 `intentStatus`를 분리하는 feature Task Intent
- `mode=feature` Task View Query와 request·flowId·entrySymbol·domain 중 하나의 시작 조건
- entry resolver, task working set, deterministic structural flow compiler와 model 없는 fallback
- 전체 흐름을 보존하는 SemanticMapIR과 soft 7~15 visible-step FlowViewProjection
- entry, result, critical branch, failure, external effect, unknown boundary 보존
- source, test, contract 또는 scoped runtime Evidence Anchor와 CodeLens 연결
- `unknown`, `unresolved`, `unavailable`, `historical`의 위치 기반 표시
- 현재 loopback FlowView와 CLI/MCP 조회 surface

### Out of Scope

- versioned edit, live publication, currentness proof와 SSE — VS-03과 VS-04가 담당한다.
- baseline/current Semantic Delta와 Requirement Alignment — VS-05가 담당한다.
- impact, debug, incident, onboarding mode — VS-06, VS-07, VS-09가 담당한다.
- optional model proposal, human approval과 model installation — VS-08이 담당한다.

## 4. Preconditions

- Raw P0 Exit Gate가 완료되어 six mode query contracts, UX/state prototype과 first-payload schema ownership을 확인할 수 있다.
- repository structural evidence seam이 존재한다. VS-01의 P2 snapshot-aware adapter closure는 이 P1 slice의 선행 조건이 아니다.
- 대상 repository와 worktree를 읽을 수 있고 언어 capability가 식별된다.
- feature query는 지원 repository 또는 명시적 entry/flow reference를 가리킨다.
- product source는 read-only이며 Evidence payload는 secret 정책을 통과한다.

## 5. Public Seam

Feature Task View Query를 받는 CLI·MCP surface와 current FlowView/structured response가 공개 seam이다. 사용자는 query 결과에서 step을 선택해 source, test, contract 또는 runtime evidence로 이동한다. 첫 화면은 raw file tree나 전체 graph가 아니라 Current Answer와 task-scoped Flow Rail이다.

## 6. Boundary Coverage

User natural-language request → Task Intent normalizer → feature query validation → entry/target resolver → adapter evidence and task working set → deterministic flow compiler → SemanticMapIR → FlowViewProjection → FlowView/MCP result와 Evidence Dock.

## 7. Inherited Invariants

- `INV-01`, `INV-02`, `INV-03`, `INV-04`, `INV-05`, `INV-12`, `INV-13`, `INV-15`, `INV-19`, `INV-25`
- Raw D1, D2, D3, D4, D5, D6, D22, D32 및 §6.4, §8.2, §9.3–§9.12, §10.0.
- Model 또는 agent 설명은 구조 Fact와 Evidence의 authority가 아니다.

## 8. Slice-Specific Rules

- `rawRequest`는 immutable로 보존하며 normalized intent와 `intentStatus`를 같은 field로 합치지 않는다.
- `mode=feature`는 명시된 시작 조건을 요구하고 둘 이상의 동등한 target이면 `ambiguous_target`을 반환한다. 임의의 default scope를 만들지 않는다.
- SemanticMapIR은 확인된 전체 step과 edge를 보존한다. 7~15는 projection의 soft display budget이며 흐름을 자르거나 짧은 흐름을 채우지 않는다.
- entry, result, critical branch, failure, external effect와 unknown boundary는 display budget 때문에 제거할 수 없다.
- 근거가 없는 target, branch, business rule 또는 source range는 Fact나 confirmed claim으로 만들지 않고 unknown/unresolved로 둔다.
- 모델 또는 runtime 보강이 없어도 deterministic map이 발행되어야 한다.

## 9. Acceptance Criteria

- VS02-A1. WHEN 사용자가 feature request를 제출하면, THE system SHALL immutable `rawRequest`, normalized intent, acceptance criteria와 `intentStatus`를 분리한 Task Intent를 생성한다.
- VS02-A2. IF feature query의 시작 조건이 없거나 여러 target이 동등하게 일치하면, THEN THE system SHALL 각각 `missing_precondition` 또는 `ambiguous_target`을 반환하고 임의의 scope를 선택하지 않는다.
- VS02-A3. WHEN 유효한 feature query가 주어지면, THE system SHALL model과 runtime enrichment를 기다리지 않고 현재 snapshot에 근거한 deterministic SemanticMapIR을 제공한다.
- VS02-A4. THE system SHALL SemanticMapIR에 entry부터 result까지 확인된 전체 flow를 보존하고, projection에서는 7~15개의 soft budget과 critical boundary 보존 규칙을 적용한다.
- VS02-A5. WHEN 사용자가 중요한 step을 선택하면, THE system SHALL 해당 step의 source, test, contract 또는 scoped runtime Evidence Anchor와 CodeLens를 같은 task context에서 제공한다.
- VS02-A6. IF call target, branch, capability 또는 evidence가 확인되지 않으면, THEN THE system SHALL 해당 위치를 `unknown`, `unresolved` 또는 `unavailable`로 표시하고 확정 Fact를 만들지 않는다.
- VS02-A7. THE system SHALL current answer, semantic flow, evidence scope와 unknown을 구분해 표시하고 raw source, 전체 graph 또는 model prompt를 기본 화면의 권위로 사용하지 않는다.
- VS02-A8. THE system SHALL 분석과 FlowView payload가 product source를 수정하지 않고 secret-redacted Evidence만 저장·전달하도록 한다.

## 10. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| target 없음 | typed missing-precondition error | map과 active pointer 변경 없음 | query에 entry, flow 또는 domain 조건 추가 |
| target 다수 | ambiguous target과 후보 표시 | 임의 flow 발행 없음 | 사용자가 target을 좁혀 재요청 |
| adapter capability partial/unavailable | 가능한 범위와 coverage boundary 표시 | 미확인 relation을 Fact로 저장하지 않음 | capability 복구 후 새 generation |
| anchor 또는 evidence validation 실패 | affected step unknown/unresolved | 해당 claim 승격 없음 | anchor 재분석 또는 추가 근거 |
| model/runtime 미설치 또는 실패 | deterministic map 유지, enrichment 없음 | 실패 proposal을 canonical map에 반영하지 않음 | 선택적 보강을 별도 실행 |
| source 변경이 아직 live 분석 대상이 아님 | 현재 generation과 basis 상태 표시 | 오래된 결과를 current로 가장하지 않음 | VS-04 current/gap 경로로 갱신 |

## 11. Data and Interaction Contract

- Input: `codeflow.task-intent`와 `mode=feature` Task View Query, repository/workspace basis, optional entry/flow/domain selector.
- Output: `codeflow.semantic-map-ir`, `codeflow.flow-view-projection`, Evidence index, unknowns와 coverage boundary.
- SemanticMapIR의 `computedBasisId`와 모든 Evidence Anchor는 같은 분석 basis에 연결된다. Projection reference는 같은 `generationId`를 사용한다.
- `not_observed` 또는 unknown은 구현 부재의 증명이 아니며 coverage boundary와 함께 표시한다.
- UI 기본 정보 순서는 Current Answer → Flow Rail → Evidence → Unknown이며 raw diff와 전체 graph는 보조 탐색이다.

## 12. Test Seam and Evidence

- Public seam: feature query, CLI·MCP response, current FlowView, CodeLens/Evidence navigation.
- Required test level: Task Intent lifecycle, feature precondition/ambiguity, deterministic baseline, SemanticMapIR·projection fixture, Evidence anchor navigation, unknown preservation, redaction.
- Replaceable external boundaries: language adapter, clock, FlowView renderer, source reader, runtime enrichment.
- Evidence per criterion: VS02-A1/A2 intent·query schema fixture, A3/A4 baseline golden flow, A5 CodeLens endpoint test, A6 unknown fixture, A7 projection/accessibility integration, A8 secret and read-only test.

## 13. Verification Plan

| Check | Applicability / Trigger | Exact Command | Expected Evidence | Required for Completion |
|---|---|---|---|---|
| Acceptance behavior | Always | `go test ./internal/mcp ./internal/harvest ./internal/slicing ./internal/fusion ./internal/flowview ./internal/e2e` | feature query, flow compilation, evidence navigation와 FlowView response pass | Yes |
| Slice tests | Always | `go test ./internal/mcp ./internal/harvest ./internal/slicing ./internal/fusion ./internal/flowview` | VS-02 package tests pass | Yes |
| Type, static analysis, and lint | Go code, schema loader or renderer affected | `go vet ./...` | no applicable findings | When applicable |
| Affected build | Core or embedded FlowView affected | `go build ./...` | Core builds successfully | When applicable |
| Architecture and policy | canonical payload or boundary changes | `go test ./internal/contractharness -run 'TestValidateGoldenFixtures|TestValidateExportedContractBoundary'` | Task Intent, query, map, projection and Evidence fixtures validate | Yes |
| Regression | public flow/query behavior affected | `go test ./...` | full Go regression suite passes | Yes |
| Security | source spans or FlowView payloads exposed | `go test ./internal/secret ./internal/fusion ./internal/flowview ./internal/e2e` | secret redaction and source read-only behavior pass | Yes |
| Data and migration compatibility | SemanticMapIR and projection are persisted or published | `go test ./internal/contractharness` | schema identity, reference and version checks pass | Yes |
| Performance and concurrency | initial deterministic query is measured | `N/A — repository has no configured initial-query benchmark; raw §16.2 target remains a later release measurement` | reason recorded | No |
| Reliability and flake | adapter/renderer failure path is affected | `go test ./internal/mcp ./internal/e2e -count=1` | deterministic fallback and failure recovery are repeatable | Yes |
| Coverage | Raw defines quality targets but no current Go coverage command | `N/A — no repository coverage threshold is configured` | reason recorded | No |
| Dart adapter compatibility | adapter contract is consumed by baseline | `cd adapters/dart && dart test` | Dart adapter evidence fixtures pass | When applicable |
| TypeScript adapter compatibility | adapter contract is consumed by baseline | `node adapters/typescript/test/index.test.js` | TypeScript/JavaScript evidence fixtures pass | When applicable |
| Browser UX | FlowView와 Evidence navigation user surface is in scope; raw §21.10 browser verification is required before slice completion | `npm --prefix web/live-comprehension-workspace run test:e2e -- --project=chromium` | Playwright proves feature query, Flow Rail, critical-boundary preservation, Evidence/CodeLens navigation and unknown/coverage display | Yes |
| Accessibility | FlowView and Evidence Dock are user-facing | `npm --prefix web/live-comprehension-workspace run test:a11y` | `@axe-core/playwright`, keyboard navigation, screen-reader outline, reduced motion and contrast evidence pass | Yes |

### 13.1. Verification Results

| Check | Exact Command | Result | Evidence Summary |
|---|---|---|---|
| Acceptance behavior | `go test ./internal/mcp ./internal/harvest ./internal/slicing ./internal/fusion ./internal/flowview ./internal/semantic ./internal/e2e` | PASS | All 7 acceptance packages pass; task query, deterministic compilation, CodeLens, unknowns verified |
| Slice tests | `go test ./internal/mcp ./internal/harvest ./internal/slicing ./internal/fusion ./internal/flowview ./internal/semantic` | PASS | All core unit/slice tests pass cleanly |
| Type, static analysis, and lint | `go vet ./...` | PASS | Exit code 0, 0 findings across all packages |
| Affected build | `go build ./...` | PASS | Core and cmd/codeflow build successfully with no warnings |
| Architecture and policy | `go test ./internal/contractharness -run 'TestValidateGoldenFixtures\|TestValidateExportedContractBoundary'` | PASS | Golden fixtures: 19 valid + 37 invalid = 56 exercised; task-intent, task-view-query, semantic-map-ir, flow-view-projection pass |
| Regression | `go test ./...` | PASS | Full Go regression suite (26 packages) passes with zero errors |
| Security | `go test ./internal/secret ./internal/fusion ./internal/flowview ./internal/semantic ./internal/e2e` | PASS | Secret redaction enforced and product source confirmed strictly read-only |
| Data and migration compatibility | `go test ./internal/contractharness` | PASS | All registered schema IDs compile and validate |
| Performance and concurrency | N/A | Recorded | Repository has no configured initial-query benchmark |
| Reliability and flake | `go test ./internal/mcp ./internal/e2e -count=1` | PASS | Count=1 run deterministic without flakiness |
| Coverage | N/A | Recorded | No repository coverage threshold configured |
| Dart adapter compatibility | `cd adapters/dart && dart test` | PASS | 38/38 Dart tests pass |
| TypeScript adapter compatibility | `node adapters/typescript/test/index.test.js` | PASS | All unit and adversarial empirical suites pass |
| Browser UX | `npm --prefix web/live-comprehension-workspace run test:e2e -- --project=chromium` | PASS | Playwright Chromium passes: feature query, ambiguity resolution, Current Answer, Flow Rail, CodeLens |
| Accessibility | `npm --prefix web/live-comprehension-workspace run test:a11y` | PASS | Axe-core accessibility audit passes with 0 critical or serious violations |

## 14. What Could Be Wrong

- Assumption: task-scoped entry resolution can select a useful flow from natural-language intent or one explicit selector.
- Consequence: the first answer omits a critical path or includes unrelated repository facts.
- Validation method: fixed feature-query corpus with reviewer-labeled entry, result, critical branch, external effect and unknown boundary.

## 15. Done When

- VS02-A1–A8 have observable evidence from the public query/FlowView seam.
- The full flow remains in SemanticMapIR and the projection obeys D32 preservation rules.
- Model/runtime absence, unknown relation and secret policy are visible in the result.
- Every applicable Verification Plan row passes or every N/A row has a repository-specific reason.
- No code, schema or raw source is changed by this slicing task.

## 16. Open Decisions

없음. feature baseline의 세부 schema 물리 파일 분할과 결정 사항은 아래 Resolved Implementation Decisions로 확정되었다.

## 17. Resolved Implementation Decisions

- `SID-C1` (정규 payload 물리 구성): `schemas/task-intent.schema.json`, `schemas/task-view-query.schema.json`, `schemas/semantic-map-ir.schema.json`, `schemas/flow-view-projection.schema.json` 4개 독립 스키마로 분할 및 Registry 등록 완료.
- `SID-C2` (validation 경계): 각 JSON Schema 구조 검증과 함께, `internal/contractharness/semantic_map.go`에 크로스 필드 시맨틱 검증기(`ValidateSemanticMapIR`, `ValidateTaskIntent`, `ValidateTaskViewQuery`, `ValidateFlowViewProjection`)를 구현하여 typed failure 처리.
- `SID-C3` (계약 검증 범위): 4개 스키마에 대해 8개 골든 픽스처(4 valid, 4 invalid) 등록 및 Playwright E2E / Axe-core A11y 자동화 검증 완료.
- `SID-C4` (기존 구현 재사용/교체): 기존 슬라이싱 엔진(`internal/slicing`)과 퓨전(`internal/fusion`)을 정적 추출기로 재사용하고, 신규 `internal/semantic` 패키지로 외부 모델 없는 결정론적 시맨틱 컴파일러를 구현.
- `SID-C5` (운영 한계): 전체 흐름은 `SemanticMapIR`에 100% 보존하되, 시각적 인지 부하를 줄이기 위해 `FlowViewProjection`에서 7~15단계 소프트 표시 예산(D32 핵심 분기/결과 불변 보존) 적용. 비밀정보는 `internal/secret` 단일 게이트 마스킹 강제.
- `SID-C6` & `SID-02` (Feature Mode Critical Obligations 및 동순위 Ambiguity):
  - 후보 심볼 매칭: 심볼명 완전일치 > 접두/접미 > 포함 순으로 스코어링하며, 2개 이상 동순위 발생 시 임의 추측 없이 `ambiguous_target`을 즉시 반환하여 사용자 중의성 해결을 유도.
  - Feature Mode Settlement Gate 입력:
    1) `entry_resolution` (`status=verified`): 진입 심볼 확정
    2) `terminal_resolution` (`status=verified`): 종료/결과 심볼 확정
    3) `causal_chain` (계층 전이 연결성 보존)
    4) `critical_branch` (핵심 판단/가드 보존)
    5) `no_critical_unknown` (`unresolvedCriticalCount=0`): 핵심 경로 상의 차단성 미해결 사항 부재 확인.
