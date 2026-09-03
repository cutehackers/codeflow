# 요청 흐름 이해와 실시간 Semantic Compiler — VS-04 current 결과 또는 검증 gap을 신뢰한다

- Contract ID: `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-VS-04`
- Contract Status: Proposed
- Independent Review: Passed (`gpt-5.6-terra`, high)
- Parent Contract: `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md`
- Created: 2026-09-02
- Source: `docs/design/raw/requested-flow-live-semantic-compiler-architecture-draft-ko.md`
- Decision Records: `docs/design/decisions/requested-flow-live-semantic-compiler-decisions-ko.md`

## 1. Intent and Goal

- Intent ID: `INT-02`
- Parent Intent: coding agent 구현 변경이 현재 요청 흐름의 의미를 최신성·근거·차이와 함께 계속 파악한다.
- Goal ID: `GOAL-04`
- Slice Goal: 관련 편집 뒤 사용자는 current 검증 generation 또는 명시적 latest-vs-verified gap을 보며 서로 다른 generation이 섞이지 않았음을 확인한다.
- User or Caller Value: 최신 결과가 늦거나 closure가 열려도 사용자는 마지막 검증 결과와 현재 변경 범위를 구분할 수 있고, 검증되지 않은 결과를 current 또는 settled로 오인하지 않는다.
- Contribution to Parent: Raw P2의 closure, proof, atomic publication, status axes와 continuous-change UX를 하나의 검증 가능한 live outcome으로 제공한다.
- Parent Acceptance: `A7–A8`, `A9` (closure consumer), `A10–A11`, `A23–A28`

## 2. User Outcome

시스템은 선택한 snapshot을 분석하고 Current Publication Gate를 통과한 경우 Generation Proof Manifest와 함께 current generation을 원자적으로 발행한다. 검증이 열려 있거나 최신 delta와 closure가 겹치면 이전 결과를 지우거나 current로 가장하지 않고 `last_verified + editing` 상태, affected scope, `analysisLagMs`와 `pendingRevisions`를 표시한다.

## 3. Scope

### In Scope

- 2초 quiet/max-wait publication coalescing과 최신 publication snapshot 선택
- Analysis Read Set, Causal Observation Closure와 computed-basis-to-liveHead Workspace Delta 검증
- Snapshot, Closure, Evidence, Semantic Atomicity, Task Relevance와 Comprehension publication gates
- Generation Proof Manifest, canonical artifact refs와 active pointer compare-and-swap
- 같은 `computedBasisId`의 late refinement와 stale/cancelled/old-epoch 결과 격리
- `current`, `last_verified`, quality stage, settlement, enrichment와 activity 상태 축 분리
- Q1/Q2 current + settlement pending, 명시적 failed, Q3 required Critical Obligation 기반 passed
- EventEnvelope sequence, SSE generation/activity stream, Last-Event-ID replay와 generation-gap recovery
- stable step identity, selection·scroll·Evidence 위치 보존과 no mixed-generation read

### Out of Scope

- 편집 revision과 snapshot capture 자체 — VS-03이 담당한다.
- initial feature flow의 entry resolution과 baseline semantics — VS-02가 담당한다.
- review/impact/debug/incident/onboarding의 mode-specific projection — VS-05 이후 slice가 담당한다.
- model proposal 내용과 semantic approval mutation — VS-08이 담당한다.

## 4. Preconditions

- VS-02의 SemanticMapIR, Evidence와 FlowViewProjection seam이 존재한다.
- VS-03의 immutable WorkspaceSnapshot, `liveHead`, activity와 change batch가 존재한다.
- staging/CAS object와 local active pointer를 기록할 수 있다.
- task intent revision, normalized query hash와 previous active generation을 식별할 수 있다.

## 5. Public Seam

Live generation query, active pointer, REST snapshot/query surface와 sequenced SSE activity/generation stream이 공개 seam이다. 사용자는 current 또는 last_verified 상태와 gap 정보를 보고, client는 `(streamId, sequence)`와 `eventId`로 replay·중복을 처리한다.

## 6. Boundary Coverage

WorkspaceSnapshot/liveHead → incremental flow·semantic compile → Workspace Delta + Closure + Evidence validation → generation artifacts/CAS → Generation Proof Manifest → expected-liveHead/previous-generation CAS → active query + EventEnvelope/SSE → stable FlowView state.

## 7. Inherited Invariants

- `INV-03`, `INV-04`, `INV-07`, `INV-08`, `INV-09`, `INV-10`, `INV-11`, `INV-12`, `INV-15`, `INV-16`, `INV-19`, `INV-21`, `INV-23`, `INV-24`, `INV-25`
- Raw D8, D9, D10, D13, D19, D21, D23, D24, D27, D28, D30, D31 및 §7.2–§7.7, §10.10–§10.16, §13, §15–§16.
- `computedBasisId`는 계산 계보이고 `validatedAgainstSnapshotId`는 freshness proof 대상이며 서로 대체하지 않는다.

## 8. Slice-Specific Rules

- 2초 window는 편집을 멈추는 debounce가 아니라 publication coalescing이다. 편집 수집·증분 분석·invalidation은 window 동안 진행한다.
- Closure가 닫혀 있고 computed basis와 liveHead delta가 closure와 교차하지 않을 때만 current proof를 만들 수 있다. read set이 동일하다는 사실만으로 rebase하지 않는다.
- CAS는 expected `liveHead`와 expected previous generation을 함께 검사한다. 실패하면 결과를 조용히 덮어쓰지 않고 최신 head 대상 검증을 다시 예약한다.
- Q1/Q2 current는 `settlement=pending`일 수 있다. `settlement=passed`는 Q3 이상, required Critical Obligation 전체 verified, critical unknown/conflict 0일 때만 허용한다.
- Q4 refinement는 Q3 사실, Requirement Alignment와 settlement를 바꾸지 않는다. model timeout은 enrichment 상태로만 표시한다.
- 하나의 화면의 summary, steps, delta, alignment, evidence, unknowns와 coverage는 하나의 proof manifest generation을 참조한다.
- stream gap은 마지막 정상 sequence부터 replay하거나 최신 proof manifest 전체를 읽어 복구한다. 서로 다른 generation을 merge-on-read하지 않는다.

## 9. Acceptance Criteria

- VS04-A1. WHEN 관련 편집이 발생하면, THE system SHALL 2초 quiet/max-wait 정책으로 publication snapshot을 선택하고 분석 활동과 pending revision을 표시한다.
- VS04-A2. WHEN Current Publication Gate의 Snapshot, Closure, Evidence, Semantic Atomicity, Task Relevance와 Comprehension 조건이 모두 통과하면, THE system SHALL 하나의 Generation Proof Manifest와 active pointer CAS로 current generation을 발행한다.
- VS04-A3. IF closure가 open이거나 Workspace Delta가 positive dependency, negative observation, membership, dependency frontier 또는 resolution input과 교차하면, THEN THE system SHALL current 발행을 거절하고 last verified generation, affected scope와 lag를 표시한다.
- VS04-A4. IF active pointer CAS의 expected liveHead 또는 previous generation이 현재 값과 다르면, THEN THE system SHALL stale writer를 활성화하지 않고 최신 head를 대상으로 closure 검증을 다시 수행한다.
- VS04-A5. WHEN current generation이 Q1 또는 Q2이면, THE system SHALL `settlement=pending`을 허용하고 시간·coverage 비율만으로 `settlement=passed`로 표시하지 않는다.
- VS04-A6. WHEN Settlement Gate가 평가되면, THE system SHALL 모든 required Critical Obligation이 verified이고 critical unknown/conflict가 0일 때만 `settlement=passed`를 허용하며, 명시적 평가 실패에만 `failed`와 blocking obligation을 기록한다.
- VS04-A7. WHEN same-basis late refinement가 도착하면, THE system SHALL 같은 `computedBasisId`, closure validation과 active pointer CAS를 확인한 경우에만 refinement로 발행한다. 다른 basis 또는 stale result는 current에 포함하지 않는다.
- VS04-A8. THE system SHALL current/last_verified, activity, quality, settlement, enrichment와 connection 상태를 독립 축으로 표시하고, model timeout을 deterministic fact의 degraded status로 바꾸지 않는다.
- VS04-A9. WHEN SSE connection이 끊기거나 sequence gap이 발견되면, THE system SHALL Last-Event-ID replay 또는 최신 proof manifest 재조회로 동일 generation 단위의 상태를 복구하고 duplicate event를 제거한다.
- VS04-A10. WHEN generation이 갱신되면, THE system SHALL stable step identity를 사용해 선택 위치, scroll과 Evidence context를 보존하고 전체 canvas를 재배치하지 않는다.
- VS04-A11. THE system SHALL 지원 환경에서 관련 편집 후 P95 3초 안에 current verified generation 또는 latest-vs-verified gap, affected scope와 lag를 표시한다. 이 SLO는 closure·evidence gate를 우회할 수 없다.
- VS04-A12. WHEN 관련 편집이 연속해서 발생하면, THE system SHALL 각 publication checkpoint와 latest-vs-verified gap을 동일 trace 기준 지원 환경에서 P95 3초 안에 갱신하고, checkpoint 선택을 위해 편집 수집을 중단하지 않는다.

## 10. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| closure open | last_verified/unknown과 coverage boundary | current pointer 교체 없음 | closure를 닫거나 최신 snapshot 재분석 |
| delta가 closure와 교차 | editing + affected scope + lag | 미검증 generation active 발행 없음 | 최신 snapshot에서 compile·검증 |
| CAS conflict | publish conflict 상태 | 이전 active generation 보존 | liveHead와 previous generation을 다시 읽고 검증 |
| proof/schema/reference 불일치 | invalid generation | partial artifact는 historical/CAS에만 보존 | artifact 수정 또는 폐기 후 재생성 |
| required Critical Obligation 미검증 | current + settlement pending 또는 failed | settled 표시 없음 | obligation evidence 추가 또는 명시적 실패 표시 |
| same-basis late result | refinement 또는 historical 표시 | stale basis가 current를 덮지 않음 | closure와 CAS 통과 시 재발행 |
| continuous edit queue overload | editing + lag와 최신 checkpoint | 저가치 enrichment 취소, revision 삭제 없음 | bounded queue와 latest-wins 재분석 |
| SSE sequence gap/disconnect | disconnected/replaying 상태 | 중복 generation merge 없음 | replay 또는 최신 manifest snapshot |
| branch/workspace epoch switch | 이전 결과 historical | epoch 혼합 없음 | 새 epoch에서 generation 시작 |

## 11. Data and Interaction Contract

- Input: `WorkspaceSnapshot`, `WorkspaceDelta`, `AnalysisReadSet`, `CausalObservationClosure`, previous generation, Task Intent revision과 active query.
- Output: `SemanticMapIR`, `SemanticDeltaIR`, Evidence index, FlowViewProjection, Unknown/Coverage, `GenerationProofManifest`, active pointer와 `EventEnvelope`.
- Current proof는 `computedBasisId`, `validatedAgainstSnapshotId`, closure digest, capability digest, artifact refs와 expected CAS values를 포함한다.
- `freshness=current`는 유효한 proof와 closure gate, CAS 조건을 요구한다. `last_verified`는 최신 snapshot과 이전 proof의 gap을 함께 표시한다.
- Event consumer는 stream ID/sequence로 순서를 검사하고 event ID로 중복을 제거한다. mutation command의 idempotency는 VS-08이 담당한다.
- source와 Evidence를 읽는 publication 경로는 product source를 수정하지 않으며, public status와 stream에는 secret-redacted payload만 사용한다.

## 12. Test Seam and Evidence

- Public seam: active generation query, proof manifest, active pointer CAS, REST snapshot/status와 SSE stream/reconnect.
- Required test level: closure intersection, open closure, cross-generation mixing, late result, CAS race, settlement gate, continuous edit, event replay/gap, stable selection.
- Replaceable external boundaries: clock, scheduler, storage transaction, SSE connection, browser reconciliation and analyzer/model jobs.
- Evidence per criterion: VS04-A1 activity/coalescing trace, A2/A3 proof and gap fixtures, A4 CAS race, A5/A6 settlement matrix, A7 late-result race, A8 status fixture, A9 replay fixture, A10 UI state fixture, A11 single-edit end-to-end trace, A12 continuous-edit checkpoint trace.

## 13. Verification Plan

| Check | Applicability / Trigger | Exact Command | Expected Evidence | Required for Completion |
|---|---|---|---|---|
| Acceptance behavior | Always | `go test ./internal/storage ./internal/mcp ./internal/flowview ./internal/e2e` | proof, gap, CAS, status, replay와 stable-update behavior pass | Yes |
| Slice tests | Always | `go test ./internal/storage ./internal/mcp ./internal/flowview` | VS-04 package tests pass | Yes |
| Type, static analysis, and lint | publication/storage/stream code affected | `go vet ./...` | no applicable findings | When applicable |
| Affected build | Core or FlowView surface affected | `go build ./...` | Core builds successfully | When applicable |
| Architecture and policy | manifest, pointer or EventEnvelope boundary changes | `go test ./internal/contractharness -run 'TestValidateGoldenFixtures|TestValidateExportedContractBoundary'` | canonical generation, proof and event fixtures validate | Yes |
| Regression | active generation or public stream behavior affected | `go test ./...` | full Go regression suite passes | Yes |
| Security | generation/evidence/status is exposed to UX | `go test ./internal/secret ./internal/storage ./internal/flowview ./internal/e2e` | no secret leakage and no unsafe publication | Yes |
| Data and migration compatibility | CAS, manifest, event or generation persistence affected | `go test ./internal/contractharness` | same-generation references, schema version and replay payloads validate | Yes |
| Performance and concurrency | raw A7–A8 publication SLO, continuous changes and CAS races are in scope | `go test ./internal/e2e -count=1` | the same trace ID reports activity P95 ≤300ms, current/gap P95 ≤3s, continuous-change checkpoint selection and CAS race behavior | Yes |
| Reliability and flake | continuous edit, cancellation, replay or late result is affected | `go test ./internal/storage ./internal/mcp ./internal/e2e -count=1` | repeatable no-mix, replay and recovery evidence | Yes |
| Coverage | Raw supplies release quality targets, not a repository threshold | `N/A — no coverage percentage command is configured` | reason recorded | No |
| Browser UX | current/gap, stable selection, stream replay and affected-scope states are user-facing; raw §21.10 UX verification is required before completion | `npm --prefix web/live-comprehension-workspace run test:e2e -- --project=chromium` | Playwright proves current/last_verified, gap, stable selection, reconnect/Last-Event-ID and no mixed-generation display | Yes |
| Accessibility | current/gap and settlement states are user-facing | `npm --prefix web/live-comprehension-workspace run test:a11y` | `@axe-core/playwright`, keyboard navigation, screen-reader outline, reduced motion and contrast evidence pass | Yes |

## 14. What Could Be Wrong

- Assumption: Causal Observation Closure can capture the relevant negative, membership and dependency-frontier observations within the publication budget.
- Consequence: the system either blocks valid current publication too often or incorrectly labels a stale result current.
- Validation method: new-caller, membership-change, graph-revision, CAS-race and rapid-edit fixtures replayed with an independent ground-truth snapshot sequence.

## 15. Done When

- VS04-A1–A12 evidence shows current publication and explicit gap behavior without mixed generation.
- `settlement=passed` cannot be reached through time, score or coverage ratio alone.
- replay, CAS conflict, late result, epoch switch and queue overload leave no stale current artifact.
- The P95 trace and all other applicable Verification Plan rows pass, with N/A reasons recorded where applicable.
- No production code, schema or raw source is changed by this slicing task.

## 16. Open Decisions

없음. raw D8–D32와 승인된 결정 기록을 따른다.
