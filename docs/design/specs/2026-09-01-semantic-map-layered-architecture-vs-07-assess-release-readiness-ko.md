# Semantic Map Layered Architecture — VS-07 출시 준비 평가

- Contract ID: `SMAP-VS-07`
- Contract Status: Proposed
- Independent Review: Passed
- Parent Contract: `docs/design/specs/2026-09-01-semantic-map-layered-architecture-ko.md`
- Created: 2026-09-01
- Depends On: SMAP-VS-01, SMAP-VS-02, SMAP-VS-03, SMAP-VS-04, SMAP-VS-05, SMAP-VS-06
- Parent Acceptance Coverage: FA-05, FA-16, FA-21

## 1. User Outcome

Release owner는 고정된 synthetic·open-source fixture와 reference hardware에서 Analysis, dynamic coverage, semantic model, UX와 운영 품질 결과를 재현하고 capability별 GA 가능 여부를 판단할 수 있다.

## 2. Scope

### In Scope

- 공개 Apache-2.0 synthetic benchmark fixture repository 계약
- Flutter samples `compass_app`, LocalSend, NestJS `sample/`, Backstage fixture manifest
- Immutable commit, license, subpath, toolchain, entry query, expected Fact·unknown과 scenario pinning
- H-01 macOS Apple Silicon Metal과 H-02 Linux x86_64 CPU-only profile
- Analysis precision, critical-flow coverage, semantic quality, performance, memory와 failure fallback gate
- Representative user의 기능·변경 이해 결과
- `Qwen3-4B-Instruct-2507` default와 `Granite 4.2 3B` challenger 비교
- Capability별 pass, fail, partial, experimental과 GA eligibility report

### Out of Scope

- 사용자 repository의 기본 corpus 수집 — privacy와 재현성 요구를 위반한다.
- Benchmark 결과를 하나의 Cognitive Debt Score로 합산 — 부모 정책에서 제외한다.
- Fine-tuning 실행 — 반복 오류와 충분한 승인 데이터가 확인된 뒤 별도 계약이 필요하다.
- Benchmark를 통과하지 못한 capability의 요구 완화 — 계약 amendment와 재승인이 필요하다.

## 3. Preconditions

- SMAP-VS-01부터 SMAP-VS-06까지의 public seam과 criterion별 test evidence가 존재한다.
- Fixture manifest와 toolchain lock을 읽을 수 있다.
- H-01 또는 H-02 profile의 실제 hardware와 실행 환경을 식별할 수 있다.
- External fixture의 license와 pinned commit이 검증되어 있다.

## 4. Public Seam

- Repository-native benchmark·release-gate runner
- Machine-readable result artifact와 사람이 읽을 수 있는 capability report
- CI와 release process의 capability status

Public seam은 fixture revision, environment, metric numerator·denominator, threshold, pass/fail과 evidence path를 노출한다. Private profiler나 graph representation은 요구하지 않는다.

## 5. Boundary Coverage

Release-gate invocation → fixture manifest·license·toolchain 검증 → pinned repository materialization → task query·scenario·model evaluation → metric calculation → threshold 비교 → immutable evidence artifact → capability별 GA·experimental·partial report

## 6. Inherited Invariants

- INV-01 Current-State Authority
- INV-03 Evidence Grounding
- INV-04 Unknown Preservation
- INV-05 Precision and Coverage Separation
- INV-08 Deterministic Baseline
- INV-09 Generation Consistency
- INV-10 Stale Isolation
- INV-11 Source Read-Only
- INV-12 Security Boundary
- INV-14 Schema Authority
- INV-16 Model Install Disclosure
- INV-17 Protocol Migration
- INV-18 Runtime Isolation
- INV-19 Projection Compatibility
- INV-20 Dynamic-Language GA

## 7. Slice-Specific Rules

- BR-01: Fixture manifest는 repository URL, immutable commit SHA, license, allowed subpath, toolchain lock, entry query, expected critical edge, expected unknown과 scenario command를 기록한다.
- BR-02: Synthetic fixture는 50K, 200K, 1M LOC graph와 reflection, callback, generated boundary, conflict와 stale evidence case를 deterministic generator revision과 seed로 재생성한다.
- BR-03: Supported subset의 critical-flow coverage는 fixture가 선언한 required critical boundary의 100%가 resolved일 때 통과한다. Dynamic과 compiler-resolved language에 같은 기준을 적용한다.
- BR-04: Critical published edge precision은 99% 이상, 전체 published Fact precision은 97% 이상이어야 한다.
- BR-05: Semantic model gate는 JSON Schema validity 100%, unsupported structural claim acceptance 0, relation macro F1 0.90 이상, behavior grouping human acceptance 85% 이상, abstention precision 95% 이상과 한국어 label 평균 4.0/5 이상이다.
- BR-06: Performance와 memory는 부모 Section 10.2의 operation별 p95와 working-set target을 H-01과 H-02에서 각각 보고한다.
- BR-07: 하나의 metric 실패를 다른 metric으로 상쇄하거나 단일 종합 점수로 숨기지 않는다.
- BR-08: Gate 미달 capability는 GA로 표시하지 않고 `partial` 또는 `experimental`로 표시한다.

## 8. Acceptance Criteria

- A1. THE system SHALL synthetic fixture와 네 external repository 각각에 immutable commit 또는 generator revision, license, subpath, toolchain, entry query, expected result와 scenario가 있는 manifest를 제공한다.
- A2. WHEN 같은 manifest, toolchain, seed와 input으로 benchmark를 반복하면, THE system SHALL deterministic artifact에 대해 같은 expected Fact, unknown과 metric denominator를 생성한다.
- A3. THE system SHALL Analysis precision, critical-flow coverage, semantic accuracy, latency, memory, fallback과 UX evidence를 metric별 numerator, denominator, threshold와 pass/fail로 보고한다.
- A4. WHERE dynamic-language capability가 GA 후보이면, THE system SHALL 지원 subset의 declared required critical boundary 100% coverage와 compiler-resolved language에 적용한 동일 threshold의 evidence를 제공한다.
- A5. IF capability가 parent quality 또는 failure-fallback gate를 통과하지 못하면, THEN THE system SHALL 해당 capability를 GA로 표시하지 않는다.
- A6. WHEN local model pack을 평가하면, THE system SHALL Qwen default와 Granite challenger를 같은 Evidence Pack, gold set과 hardware profile에서 비교하고 model·revision·quantization·checksum을 기록한다.
- A7. THE system SHALL H-01과 H-02 결과를 분리해 기록하고 한 profile 결과를 다른 profile의 통과 evidence로 사용하지 않는다.
- A8. IF fixture license, commit, checksum, toolchain 또는 expected-result validation이 실패하면, THEN THE system SHALL 해당 run을 valid release evidence로 발행하지 않는다.
- A9. THE system SHALL 사용자 repository를 명시적 별도 opt-in 없이 benchmark corpus 또는 result artifact에 포함하지 않는다.
- A10. THE release report SHALL representative user evaluation에서 사용자가 시작, 핵심 판단, 상태 변화, 결과, change impact와 supporting evidence를 정확히 설명할 수 있었는지와 그 근거를 기록한다. 부모가 정하지 않은 numeric UX threshold는 적용하지 않는다.

## 9. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| Fixture commit 또는 license 불일치 | Invalid fixture와 원인 표시 | Release evidence 발행 없음 | Manifest 또는 materialized fixture 복구 |
| Toolchain 또는 hardware profile 불일치 | Non-reference run 표시 | Reference gate를 통과한 것으로 기록하지 않음 | 지정 profile에서 재실행 |
| Benchmark 일부 timeout·crash | 해당 metric failed 또는 incomplete | 누락값을 pass로 계산하지 않음 | 원인 수정 후 전체 관련 gate 재실행 |
| Expected result drift | Regression diff와 invalid run | Baseline 자동 갱신 없음 | Contract-supported 변경 검토 후 fixture amendment |
| Model 둘 다 gate 미달 | Model enrichment experimental | Deterministic GA 차단 없음 | Prompt·pack 개선 후 재평가 |
| External repository 접근 불가 | Fixture unavailable | Pinned cached copy가 없으면 run incomplete | Verified mirror 또는 접근 복구 |

## 10. Data and Interaction Contract

- FixtureManifest: fixture ID, type, source URL, commit 또는 generator revision, license, subpaths, toolchain lock, seed, task query, scenario, expected Fact·relation·unknown·coverage
- RunEnvironment: OS, architecture, CPU core, memory, storage, accelerator, runtime, analyzer, adapter, schema, model과 prompt versions
- MetricResult: metric ID, capability, fixture, numerator, denominator, value, threshold, status와 evidence refs
- UserEvaluationResult: de-identified evaluation ID, task prompt, expected start·decision·state·outcome·impact concepts, evidence refs와 criterion별 correct·incorrect·not-observed result
- ReleaseReport: run ID, manifest digest, environments, capability status, failed gate, residual risk와 artifact checksums
- Persistence: report와 evidence는 immutable run directory에 저장하고 mutable branch head를 identity로 사용하지 않는다.

## 11. Test Seam and Evidence

- Public seam: benchmark runner exit status, machine-readable report와 release capability summary
- Required test level: manifest schema, deterministic generator, metric calculator unit test, synthetic full pipeline, pinned external smoke test와 H-01/H-02 benchmark execution
- Replaceable external boundaries: repository fetcher, clock, hardware probe, model host와 result publisher
- Evidence required per criterion:
  - A1, A8, A9: manifest validation, license fixture와 corpus audit
  - A2: repeated-run artifact digest comparison
  - A3, A4, A5: golden metric calculation과 status assertion
  - A6, A7: model/hardware matrix report
  - A10: task prompt, 사용자 설명, expected answer, evidence reference와 평가 결과

## 12. What Could Be Wrong

- Assumption: 지정 corpus와 두 hardware profile이 실제 target repository와 사용자 환경을 대표한다.
- Consequence: Release gate는 통과하지만 실제 repository에서 coverage, latency 또는 memory가 기준을 벗어난다.
- Validation method: GA 전 별도 opt-in pilot 결과를 fixture별 metric과 비교하되 사용자 code를 기본 corpus에 편입하지 않는다.

## 13. Done When

- Every criterion has passing evidence.
- Fixture manifest와 report schema에 valid·invalid contract fixture가 있다.
- Synthetic generator와 pinned external smoke fixture가 재현된다.
- H-01과 H-02 결과와 model comparison이 기록된다.
- Gate 미달 capability가 GA로 표시되지 않는다.
- Relevant repository verification passes.
- No contract requirement is weakened.
- No unrelated behavior changes.

## 14. Open Decisions

없음.
