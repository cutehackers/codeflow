# 요청 흐름 이해와 실시간 Semantic Compiler — VS-10 근거로 capability를 선언한다

- Contract ID: `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-VS-10`
- Contract Status: Implemented
- Independent Review: Passed (`gpt-5.6-terra`, high)
- Parent Contract: `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md`
- Created: 2026-09-02
- Source: `docs/design/raw/requested-flow-live-semantic-compiler-architecture-draft-ko.md`
- Decision Records: `docs/design/decisions/requested-flow-live-semantic-compiler-decisions-ko.md`

## 1. Intent and Goal

- Intent ID: `INT-02`
- Parent Intent: coding agent 구현 변경이 현재 요청 흐름의 의미를 최신성·근거·차이와 함께 계속 파악한다.
- Goal ID: `GOAL-10`
- Slice Goal: maintainer는 reference trace와 보안·복원력·성능·이해도 evidence로 capability를 선언하고 미검증 기능을 GA로 표시하지 않는다.
- User or Caller Value: 사용자와 caller는 지원 언어·mode·live SLO·보안 경계의 실제 검증 상태를 알고, 구현되지 않은 capability를 완료된 것으로 사용하지 않는다.
- Contribution to Parent: Raw P6와 제품 최종 수용 기준을 release-level evidence gate로 연결해 각 slice의 주장과 제품 선언을 분리한다.
- Parent Acceptance: `A16`, `A23–A28` 및 raw §16–§18, §20.

## 2. User Outcome

release evaluation 결과는 어떤 slice와 capability가 reference fixture, end-to-end trace, conformance, security, resilience와 comprehension evidence를 통과했는지 표시한다. 필수 evidence가 없거나 gate를 통과하지 못한 capability는 `experimental`, `partial` 또는 `unsupported`로 남고 GA/완료로 표시되지 않는다.

## 3. Scope

### In Scope

- Contract Registry의 모든 구현 payload에 대한 schema identity/version, valid·invalid fixture, Semantic Validator와 producer-consumer compatibility 확인
- raw §18.6/R18의 scenario를 덮는 versioned fixture corpus: single/multi-file, rename/delete, syntax error, branch switch, dynamic dispatch, model timeout와 crash
- Dart, TypeScript/JavaScript와 Go adapter conformance와 supported capability matrix
- rapid edit, multi-file, rename/delete, syntax error, branch switch, watcher gap, model timeout/crash trace replay
- edit.capture → ux.acknowledge end-to-end P95 3초 current/gap SLO와 P95 300ms activity target 측정
- current 오발행 0, settled 오발행 0, fallback, CAS/replay/idempotency, redaction/isolation/security evidence
- comprehension, delta precision, unknown/coverage와 resource telemetry를 합산하지 않고 분리 측정
- release capability state와 unsupported/experimental/GA declaration

### Out of Scope

- 새 product behavior, new mode 또는 adapter implementation 자체 — VS-01–VS-09가 담당한다.
- 사용자 repository를 기본 benchmark corpus에 넣는 행위.
- SLO를 맞추기 위한 correctness/evidence gate 약화와 numeric target의 임의 변경.
- 이 작업에서 release tag, commit, push 또는 production code 변경.

## 4. Preconditions

- VS-01–VS-09의 구현 후보와 Verification Plan이 존재하고, 각 payload의 Registry entry를 조회할 수 있다.
- versioned fixture manifest가 scenario identity, repository shape, entry query, expected fact/unknown/metric과 지원 toolchain/hardware profile을 보유한다.
- end-to-end trace, UI acknowledgement, adapter conformance, security와 fault-injection 실행 환경이 선언된다.
- raw §16–§18의 target과 hard invariant를 변경 없이 평가한다.

## 5. Public Seam

release evaluation report, Contract Registry, benchmark/evaluation report, capability matrix와 user-visible support status가 공개 seam이다. Maintainer는 evidence link와 실패 원인을 보고 capability state를 결정한다.

## 6. Boundary Coverage

Versioned scenario fixture/release input → adapter/Core/FlowView execution trace → schema/semantic/security/reliability/comprehension measurement → gate evaluation → capability matrix/release report.

## 7. Inherited Invariants

- `INV-01`–`INV-25` 전체. 특히 `INV-05`, `INV-06`, `INV-08`, `INV-10`, `INV-15`, `INV-16`, `INV-17`, `INV-18`, `INV-19`, `INV-20`, `INV-21`, `INV-24`.
- Raw D9, D10, D17, D19, D21, D24–D31 및 §10.0, §14–§18, §20 A23–A28, §21.10.
- 측정되지 않은 capability는 지원 또는 GA로 승격하지 않는다. 성능 SLO는 correctness gate를 대체하지 않는다.

## 8. Slice-Specific Rules

- fixture manifest는 versioned scenario identity, repository shape, toolchain/hardware profile, entry query, expected critical edge/unknown과 scenario command를 기록한다.
- P95는 단계별 수치를 더하지 않고 `edit.capture`부터 `ux.acknowledge`까지 같은 trace ID의 end-to-end 분포로 판정한다.
- current/gap SLO 성공과 settled 승격을 분리한다. current proof가 없는 결과와 required Critical Obligation 미충족 결과는 각각 current/settled로 선언할 수 없다.
- schema fixture가 없거나 Semantic Validator/compatibility evidence가 없으면 해당 payload를 release-ready로 표시하지 않는다.
- security는 secret redaction, source read-only, isolation/approval, no transcript leakage와 no unsafe current publication을 포함한다.
- 실패는 capability 전체를 숨기지 않고 지원 범위, failure cause, recovery condition과 state를 보고한다.

## 9. Acceptance Criteria

- VS10-A1. THE system SHALL Contract Registry에서 각 implemented slice의 boundary/persistence/CAS/publication payload에 schema identity/version, valid·invalid fixture, Semantic Validator와 producer-consumer compatibility evidence가 있음을 확인한다.
- VS10-A2. THE system SHALL versioned reference fixture를 scenario identity, repository shape, toolchain/hardware profile, entry query, expected Fact/unknown과 metric으로 고정하고 사용자의 repository를 기본 corpus에 포함하지 않는다.
- VS10-A3. WHEN supported adapter/Core/FlowView trace를 재생하면, THE system SHALL activity acknowledgement, current verified generation 또는 explicit latest-vs-verified gap의 end-to-end P95와 trace evidence를 보고한다.
- VS10-A4. THE system SHALL current 오발행, proof 없는 active pointer, settled 오발행, cross-generation mix, stale late result와 replay/idempotency failure를 release-blocking failure로 분류한다.
- VS10-A5. THE system SHALL rapid edit, multi-file transaction, rename/delete, syntax error, branch switch, watcher gap, adapter/model failure와 reconnect traces에서 fallback·recovery·unknown behavior를 측정한다.
- VS10-A6. THE system SHALL adapter conformance, secret redaction, source read-only, runtime isolation/approval, error diagnostic bound와 transcript non-exposure를 보안 evidence로 확인한다.
- VS10-A7. THE system SHALL semantic delta precision, requirement link validity, unknown/coverage, comprehension result와 resource/latency metric을 별도 보고하고 하나의 종합 점수로 숨기지 않는다.
- VS10-A8. IF required evidence, release target 또는 hard invariant가 충족되지 않으면, THEN THE system SHALL capability를 `experimental`, `partial` 또는 `unsupported`로 유지하고 GA/완료로 표시하지 않는다.
- VS10-A9. THE system SHALL release report와 capability matrix에서 selected adapter/protocol, capability, version, evidence status, incompatibility가 있으면 그 원인과 recovery condition, capability state를 표시한다.

## 10. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| fixture identity/profile/query not declared | evaluation invalid/incomplete | capability declaration 없음 | scenario identity/version, profile, query와 expected evidence 고정 |
| schema/validator/compatibility evidence missing | contract gate failed | release-ready/GA 없음 | Registry와 fixture 보완 |
| P95 current/gap target missed | SLO failed + latest/gap evidence | correctness gate 완화 없음 | 병목·queue·cache·adapter 측정 후 개선 |
| proof-less current or settled false positive | release-blocking failure | release/capability 승격 없음 | publication/settlement gate 수정 |
| adapter/model/runtime failure | partial/experimental/unsupported와 fallback evidence | 실패를 성공으로 집계하지 않음 | component recovery와 trace 재실행 |
| security/redaction/isolation failure | security gate failed | unsafe payload/release 없음 | policy·fixture·isolation 수정 |
| replay/idempotency failure | reliability gate failed | duplicate/mixed state release 없음 | consumer contract 수정 후 재실행 |
| comprehension or delta quality failure | quality gate failed | capability state 보수적으로 유지 | corpus, projection 또는 grounding 개선 |

## 11. Data and Interaction Contract

- Input: fixture manifest, implementation slice registry, execution traces, benchmark result, security/fault reports와 release metadata.
- Output: capability matrix, gate result, evidence links, measured metrics, failure cause/recovery와 release status.
- Metrics are traceable to fixture commit/toolchain/profile and are not substituted for raw acceptance or invariants.
- A capability report distinguishes `supported`, `partial`, `experimental`, `unsupported`, `GA` and `blocked` with gate reasons.
- No release mutation is performed by this slicing task. Release tag/version synchronization remains governed by repository versioning policy.

## 12. Test Seam and Evidence

- Public seam: Contract Registry, reference fixture manifest, adapter conformance, end-to-end trace runner, capability matrix and release report.
- Required test level: schema/semantic validation, versioned fixture manifest, protocol/Dart/TypeScript/Go adapter conformance, live SLO trace, publication/settlement adversarial, replay/idempotency, security, resilience, comprehension and delta quality.
- Replaceable external boundaries: versioned reference fixtures, toolchains, adapter/model/runtime processes, browser/trace runner, hardware profile and release artifact store.
- Evidence per criterion: VS10-A1 registry audit, A2 versioned scenario manifest, A3 end-to-end trace report, A4 publication adversarial report, A5 fault matrix, A6 security report, A7 separated metrics, A8 gate state, A9 release report/capability matrix.

## 13. Verification Plan

| Check | Applicability / Trigger | Exact Command | Expected Evidence | Required for Completion |
|---|---|---|---|---|
| Acceptance behavior | Always | `go test ./...` | current/gap, gate, registry, fallback and capability behavior pass | Yes |
| Slice tests | all implemented slices are evaluated together | `go test ./internal/contractharness ./internal/e2e ./internal/protocol ./internal/mcp ./internal/fusion ./internal/storage ./internal/flowview` | cross-slice contract and adversarial tests pass | Yes |
| Type, static analysis, and lint | Go implementation or evaluator affected | `go vet ./...` | no applicable findings | When applicable |
| Affected build | Core/release evaluator affected | `go build ./...` | Core builds successfully | When applicable |
| Architecture and policy | every registered boundary/persistence/CAS/publication payload | `go test ./internal/contractharness -run 'TestValidateGoldenFixtures|TestValidateExportedContractBoundary'` | registry, schema, semantic validator and fixture audit pass | Yes |
| Regression | release declaration depends on all current behavior | `go test ./...` | full Go regression suite passes | Yes |
| Security | any source, model, runtime, diagnostic or release payload is evaluated | `go test ./internal/secret ./internal/protocol ./internal/e2e` | redaction, bounds, isolation and unsafe-publication tests pass | Yes |
| Data and migration compatibility | schemas, CAS, manifest, event or version registry changed | `go test ./internal/contractharness` | compatibility and migration evidence pass | Yes |
| Performance and concurrency | raw P95 targets and queue/CAS behavior are in scope | `go test ./internal/e2e -count=1` | trace report measures edit.capture→ux.acknowledge P95, current/gap and concurrency recovery | Yes |
| Reliability and flake | fault/reconnect/replay/rapid-edit traces are in scope | `go test ./internal/e2e ./internal/protocol ./internal/storage -count=1` | repeated failure/recovery and no-mix evidence is stable | Yes |
| Reference scenario corpus | raw §18.6/R18 scenarios are in scope | `node test/e2e/runner.js` | versioned fixture evidence covers single/multi-file, rename/delete, syntax error, branch switch, dynamic dispatch, model timeout and crash | Yes |
| Browser UX | raw §21.10 Playwright interaction, stream, reconnect, stable-selection and grayscale behavior are in scope | `npm --prefix web/live-comprehension-workspace run test:e2e -- --project=chromium` | end-to-end UI trace proves activity/current-gap states, selection preservation, replay and semantic status identification | Yes |
| Accessibility | raw §21.10 accessibility and contrast checks are in scope | `npm --prefix web/live-comprehension-workspace run test:a11y` | `@axe-core/playwright`, keyboard navigation, screen-reader outline, reduced motion and text/non-text contrast evidence pass | Yes |
| Comprehension evaluation | raw §17 comprehension measures are a release gate | `npm --prefix web/live-comprehension-workspace run test:comprehension` | answer accuracy, first-answer time, Evidence navigation success, unknown misunderstanding and raw-diff comparison are retained separately | Yes |
| Coverage | raw defines quality/evidence measures, not a repository coverage percentage threshold | `N/A — no repository coverage percentage command is defined` | reason recorded | No |
| Dart adapter conformance | Dart is a supported release adapter | `cd adapters/dart && dart test` | Dart conformance and fixture evidence pass | Yes |
| TypeScript adapter conformance | TypeScript/JavaScript is a supported release adapter | `node adapters/typescript/test/index.test.js` | TypeScript/JavaScript conformance and fixture evidence pass | Yes |
| Go adapter conformance | Go is a supported raw target adapter | `go test ./adapters/go/...` | Go protocol, snapshot/read-set and typed-failure conformance evidence pass | Yes |

## 14. What Could Be Wrong

- Assumption: the versioned scenario fixture portfolio and declared hardware/toolchain profiles represent the supported capability claims.
- Consequence: a capability passes evaluation but fails on an omitted language, repository shape, hardware or failure mode.
- Validation method: compare the capability matrix with raw §21.5, §21.10, §21.11 and expand the trace portfolio when a new supported boundary is declared.

## 15. Done When

- VS10-A1–A9 have reproducible evidence tied to versioned scenario fixtures and declared profiles.
- No unverified capability is reported as GA or complete.
- Currentness, settlement, security, resilience, comprehension, delta quality and resource metrics remain separate.
- All applicable Verification Plan rows pass and no required check remains unexecuted.
- No commit, tag, push, production implementation or raw source mutation occurs in this slicing task.

## 16. Open Decisions

없음. release target과 fixture portfolio 및 결정 사항은 아래 Resolved Implementation Decisions로 확정되었다.

## 17. Resolved Implementation Decisions

- `SID-C1` (정규 payload 물리 구성): `schemas/release-benchmark-report.schema.json`, `schemas/slm-capability-state.schema.json` 독립 스키마 분할 및 BaseURL 등록.
- `SID-C2` (validation 경계): 운영체제(macOS/Linux/Windows), 하드웨어 프로파일(Apple Silicon, x86_64), 저장소 규모(1k~100k LOC)별 P95 3초 레이턴시 및 메모리 상한선 검증.
- `SID-C3` (계약 검증 범위): 버전 관리되는 시나리오 골든 코퍼스 픽스처, 로컬 SLM 품질 통과 픽스처 구축.
- `SID-C4` (기존 구현 재사용/교체): VS-01부터 VS-09까지 구축된 전체 파이프라인(어댑터, 스냅샷, 컴파일러, 발행 게이트, 델타, 영향, 디버그, 승인, 도메인)의 통합 E2E 벤치마크 및 검증 드라이버 실행.
- `SID-C5` & `SID-10` (GA 지원 범위, Benchmark Corpus, 품질 통과 기준, 로컬 SLM):
  - GA 릴리스 품질 기준 충족: 핵심 의미 폐포(Critical Semantic Closure) $\ge 95\%$, 관련 편집 후 검증 발행 P95 $\le 3$초.
  - 지원 환경(macOS Apple Silicon 등)에서만 로컬 SLM을 선택적 가속기로 활성화하며, 비지원 환경에서도 결정론적 컴파일러(VS-02) 기반으로 100% 안정적 동작을 보장 (`INV-05`).
  - GA 지원 범위 및 릴리스 통과 기준 확정은 제품/보안 경계를 결정하므로 최종 릴리스 전 사용자 명시적 승인을 거침.
