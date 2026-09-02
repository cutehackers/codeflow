# Semantic Map Layered Architecture

- Contract ID: `SMAP`
- Contract Status: Proposed
- Created: 2026-09-01
- Updated: 2026-09-02
- Previous Approval: 2026-09-01
- Amendment Basis: 2026-09-02 사용자가 legacy adapter protocol 호환 제외와 v2 전면 전환을 결정
- Source: `docs/design/raw/semantic-map-layered-architecture-draft.md`
- Applies To: CodeFlow Semantic Map capability

이 문서는 원문의 제품 의도와 기술 제안을 부모 계약으로 정리한다. 2026-09-02 protocol amendment로 다시 Proposed 상태이며 Section 12.1의 기존 계약을 자동으로 변경하거나 Vertical Slice 검토 없이 구현을 승인하지 않는다.

설계 주장 분류는 다음 의미를 가진다.

| 분류 | 의미 |
|---|---|
| Confirmed | 현재 저장소의 코드, schema, 테스트 또는 승인된 문서에서 확인됨 |
| Decision | 이 Approved 계약이 채택한 규범적 선택 |
| Assumption | 검증 전까지 필요한 가정 |
| External | 공식 외부 자료에서 확인했으며 구현 시 immutable revision으로 다시 고정해야 함 |
| Resolved Decision | 사용자가 방향을 확정하고 이 계약의 승인에 포함한 선택 |

## 1. Problem and Goal

AI 보조 개발이 만드는 코드 양이 사람의 검토와 이해 속도보다 빠르게 증가하면, 코드가 실제로 수행하는 동작과 팀이 이해하는 동작 사이에 차이가 생긴다. 테스트 통과, 모델 설명, 파일 diff만으로는 다음을 구분하기 어렵다.

- 현재 구현이 실제로 수행하는 동작
- 특정 실행 scenario에서만 관찰된 동작
- 아직 확인하지 못한 동적 경로
- 코드 변경으로 달라진 사용자 행동과 영향 범위
- 코드 밖의 근거가 필요한 변경 이유와 비즈니스 목적

Semantic Map의 목표는 저장소 전체를 그래프로 표시하는 것이 아니다. 개발자가 현재 작업에 필요한 구현 사실, 동작 의미, 변경 영향과 코드 근거를 제한된 흐름으로 확인하게 하는 것이다.

사용자가 얻어야 하는 결과는 다음과 같다.

- 기능이 어디서 시작하고 어떤 판단과 상태 변화를 거쳐 무엇을 결과로 만드는지 설명할 수 있다.
- 모든 중요한 설명에서 실제 source, test, contract 또는 runtime evidence로 이동할 수 있다.
- 확인된 사실, 특정 scenario에서만 관찰된 사실, 충돌, 미확인 영역과 오래된 의미를 구분할 수 있다.
- 모델이나 runtime 보강이 없어도 현재 코드에 근거한 기본 지도를 사용할 수 있다.

## 2. Scope

### 2.1 In Scope

- `Analysis Layer → Semantic Layer → Visualizer Layer` 책임 경계
- repository revision과 worktree fingerprint에 고정된 구현 사실 분석
- compiler-resolved 정적 분석과 hybrid-dynamic 분석
- Evidence Anchor, provenance, freshness, structural status와 coverage
- 작업 범위가 제한된 deterministic Semantic Map 생성
- 선택적 작은 모델 기반 Semantic Overlay 제안과 검증
- 사람 승인, 수정, 거절과 append-only 의미 이력
- `AnalysisSnapshot`, `SemanticMapIR`, `FlowViewProjection` 계약
- Behavior Delta, CodeLens, runtime path와 task-scoped FlowView
- Go Core, 언어별 adapter, local model host와 현재 FlowView surface의 책임 경계
- 성능, 보안, 복원력과 품질 release gate

### 2.2 Non-Goals

- 저장소 전체 symbol을 기본 화면에 표시하는 범용 graph — 현재 작업 이해에 필요한 범위를 넘는다.
- 코드 생성 도구 대체 — 이 기능은 생성된 코드를 이해하고 검증한다.
- 개발자 개인 평가 또는 감시 — ownership과 history는 지식 집중 위험과 검토 범위에만 사용한다.
- 모델이 구조 Fact의 최종 권위가 되는 동작 — 사실은 검증 가능한 분석 결과에서만 발행한다.
- 정적 분석만으로 비즈니스 목적 확정 — 목적에는 product spec, task, ADR 또는 사람 승인이 필요하다.
- 실행되지 않은 동적 경로의 target 확정 — 확인하지 못한 관계는 `unknown`으로 남긴다.
- 모든 언어에서 동일한 전체 경로 coverage 보장 — 발행 정확도와 coverage를 별도 관리한다.
- MVP 단계의 Fine-tuning, embedding retrieval, vector database, agent framework와 중앙 협업 서버 — 실제 부족이 측정된 뒤 별도 계약으로 검토한다.
- MVP Core의 전면 재작성 — Go 유지 여부는 측정된 병목으로 재검토한다.
- 1차 구현의 VS Code extension — 별도 Vertical Slice와 compatibility 계약으로 검토한다.

## 3. Actors and Preconditions

### 3.1 Actors

- Primary actor: 현재 기능, 변경, 장애 또는 PR을 이해하려는 개발자와 reviewer
- Secondary actor: CodeFlow를 호출하고 구조화된 의미 후보를 제출하는 AI coding agent
- System actors: CodeFlow Go Core, language adapter, optional runtime observer, optional semantic model provider, FlowView 또는 editor client

### 3.2 Permissions

- CodeFlow는 product source를 읽을 수 있다.
- CodeFlow는 `.codeflow/` 아래 분석 artifact, cache, semantic event와 generation만 쓸 수 있다.
- Runtime scenario는 격리 수준과 실행 command가 표시되고 정책상 허용된 경우에만 실행할 수 있다.
- 외부 model provider와 외부 network 사용은 workspace별 명시적 opt-in 없이는 허용하지 않는다.
- Optional model 설치 전에는 model ID, revision, license, download와 설치 크기, checksum, runtime, repository data 접근 범위, 활성화되는 기능, deterministic fallback, 비활성화와 제거 방법을 표시하고 사용자의 명시적 선택을 받아야 한다.

### 3.3 System Preconditions

- 대상 repository와 현재 worktree를 읽을 수 있다.
- 지원 언어의 adapter가 설치되어 있거나 capability가 `partial` 또는 `unavailable`로 선언된다.
- 모든 analyzer, schema, adapter와 optional runtime/model pack version을 식별할 수 있다.
- 이전 generation이 존재하면 완성된 active generation을 읽을 수 있다.

### 3.4 Data Preconditions

- 사용자는 자연어 query, 현재 change set, PR baseline, entry point 또는 기존 flow 중 하나로 task scope를 제공한다.
- Runtime observation이 필요한 경우 기존 test, fixture, 저장된 request/event 또는 승인된 synthetic scenario가 있어야 한다.
- 의미 승인은 대상 Fact와 현재 `basisSha`를 참조해야 한다.

## 4. Confirmed Facts

### 4.1 Current Repository Facts

- CF-01 `[Confirmed]` Core module은 Go 1.26을 사용한다. 근거: `go.mod`.
- CF-02 `[Confirmed]` 현재 pipeline은 language-neutral `SlicedPayload`를 `FlowSpec`으로 fusion하며, step에 anchor, provenance, freshness, confidence, state delta, side effect, branch, layer와 CodeLens를 보존한다. 근거: `internal/slicing/slice.go`, `internal/fusion/fusion.go`.
- CF-03 `[Confirmed]` 현재 anchor는 repository-relative path, byte range, file hash, span hash, enclosing symbol path와 canonical AST fingerprint를 가진다. 근거: `internal/slicing/slice.go`.
- CF-04 `[Confirmed]` 현재 의미 권위는 `approved > session > derived` 순서이며, 승인·거절·session draft는 append-only JSONL event로 저장된다. 근거: `internal/fusion/fusion.go`, `internal/fusion/eventlog.go`.
- CF-05 `[Confirmed]` unresolved dynamic edge와 depth truncation은 `unknowns[]`로 보존된다. 근거: `internal/fusion/fusion.go`.
- CF-06 `[Confirmed]` FlowSpec은 schema validation과 secret redaction을 통과한 뒤 반환된다. 근거: `internal/fusion/fusion.go`, `internal/secret/`, `schemas/flowspec.schema.json`.
- CF-07 `[Confirmed]` 현재 generation은 staging directory, generation rename, atomic `pointer.json` 교체 순서로 발행된다. 근거: `internal/storage/storage.go`.
- CF-08 `[Confirmed]` 현재 FlowView는 active generation의 FlowSpec을 읽고 loopback HTTP와 per-run token으로 제공한다. 근거: `internal/flowview/server.go`.
- CF-09 `[Confirmed]` 현재 language adapter protocol은 NDJSON v1 over stdio이며 request ID, timeout, message size, in-flight bound, typed error와 crash retry를 지원한다. 근거: `internal/protocol/`, `schemas/adapter-protocol.schema.json`.
- CF-10 `[Confirmed]` 현재 adapter는 Dart와 TypeScript/JavaScript가 존재한다. Dart는 Dart Analyzer를 사용하고 TypeScript/JavaScript adapter는 현재 저장소의 scanner 구현을 사용한다. 근거: `adapters/dart/`, `adapters/typescript/`, `docs/PROJECT-ko.md`.
- CF-11 `[Confirmed]` 현재 FlowSpec schema에는 이 계약이 요구하는 `structuralStatus`, `semanticStatus`, `evidenceScope`, `scenarioRefs`, `coverageSummary`, `deltas` 전체가 없다. 근거: `schemas/flowspec.schema.json`.
- CF-12 `[Confirmed]` 기존 제품 원칙은 현재 worktree 우선, 구조와 의미 분리, unknown 보존, source read-only, versioned schema와 모델 없는 deterministic flow다. 근거: `docs/design-v2.md`, `docs/codeflow-production-design-ko.md`, `docs/PROJECT-ko.md`.

### 4.2 External Evidence

- EF-01 `[External]` 원문이 인용한 코드 이해·navigation·trace visualization 연구는 task scope, evidence navigation과 runtime path UX의 설계 근거다. 이 계약은 해당 연구가 특정 UI 배치의 우월성을 증명한다고 간주하지 않는다.
- EF-02 `[External]` [Qwen3-4B-Instruct-2507 공식 model card](https://huggingface.co/Qwen/Qwen3-4B-Instruct-2507)는 Apache-2.0 license, 4B parameter, non-thinking mode, 256K context, multilingual·coding·instruction-following 개선과 llama.cpp 실행 경로를 명시한다.
- EF-03 `[External]` [Granite 4.2 3B 공식 model card](https://huggingface.co/ibm-granite/granite-4.2-3b)와 [공식 GGUF 배포](https://huggingface.co/ibm-granite/granite-4.2-3b-GGUF/tree/main)는 Apache-2.0 license, 3B class, 128K context, 한국어 평가 범위, reasoning/non-thinking mode와 llama.cpp용 양자화 artifact를 명시한다.
- EF-04 `[External]` [llama.cpp server](https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md)는 local OpenAI-compatible endpoint와 schema-constrained JSON response를 제공한다.
- EF-05 `[External]` 초기 benchmark corpus 후보는 공식 또는 원저자 repository인 [Flutter samples](https://github.com/flutter/samples), [LocalSend](https://github.com/localsend/localsend), [NestJS](https://github.com/nestjs/nest), [Backstage](https://github.com/backstage/backstage)다. Fixture 편입 시 commit SHA와 license를 manifest에 고정한다.
- EF-06 `[External]` TypeScript 7과 이후 public API 일정·호환성은 adapter 전환 시점에 공식 API와 contract fixture로 다시 검증해야 한다.

## 5. Assumptions

- ASM-01: 자연어 query, change set 또는 entry point에서 관련 task scope를 결정적으로 좁힐 수 있다.
  - Consequence if false: 모델 입력과 기본 map이 관련 없는 Fact를 포함하거나 중요한 행동을 누락한다.
  - Validation method: 실제 repository query set에서 scope precision, omission과 reviewer correction을 측정한다.
- ASM-02: 기본 7~15개 핵심 행동이 대부분의 이해, review, debug, onboarding과 impact task에 충분하다.
  - Consequence if false: 사용자가 지나치게 압축된 흐름이나 과도한 확장 사이에서 반복 탐색한다.
  - Validation method: task별 이해 시간, 정답률, 재확장률과 누락 신고를 측정한다.
- ASM-03: 최대 12K token의 Evidence Pack이 작은 모델의 relation label, behavior grouping, explanation과 abstention에 충분하다.
  - Consequence if false: 근거가 있어도 중요한 행동을 잘못 그룹화하거나 과도하게 abstain한다.
  - Validation method: 고정 Semantic Pack gold set에서 입력 크기별 품질과 latency를 비교한다.
- ASM-04: 중요한 unresolved dynamic boundary에는 안전하게 실행 가능한 test나 fixture가 존재한다.
  - Consequence if false: dynamic-language critical flow coverage가 제품 기준에 도달하지 못한다.
  - Validation method: 대상 framework fixture와 실제 repository에서 scenario availability와 boundary resolution rate를 측정한다.
- ASM-05: Go Core와 compact graph representation이 20만 LOC target의 CPU와 memory budget을 만족한다.
  - Consequence if false: incremental refresh와 query latency가 목표를 넘고 hot path 분리가 필요하다.
  - Validation method: 50K, 200K, 1M LOC fixture에서 wall time, RSS, allocation, GC CPU와 query p95를 측정한다.
- ASM-06: `SemanticMapIR`에서 versioned FlowSpec projection을 생성해 기존 CLI, MCP와 FlowView consumer를 migration 기간 동안 유지할 수 있다.
  - Consequence if false: 기존 consumer별 별도 compatibility adapter 또는 breaking major 전환이 필요하다.
  - Validation method: 기존 valid FlowSpec fixture와 새 SemanticMapIR projection fixture를 함께 검증하는 compatibility test를 작성한다.

## 6. Business Rules and Invariants

- INV-01 Current-State Authority: 현재 repository snapshot과 검증된 analyzer evidence가 구현 사실의 최종 권위다.
- INV-02 Fact Ownership: call, branch, mutation, state transition, target과 external effect는 Analysis Layer만 생성하거나 변경할 수 있다.
- INV-03 Evidence Grounding: 모든 발행 Fact와 중요한 Semantic claim은 현재 artifact에서 해석 가능한 Evidence Anchor를 가져야 한다.
- INV-04 Unknown Preservation: 확인하지 못한 관계, 충돌, 미지원 capability와 실행되지 않은 경로는 추측으로 채우거나 renderer에서 제거하지 않는다.
- INV-05 Precision and Coverage Separation: 모든 언어는 relation별 같은 Fact publication precision gate를 통과하며, 분석 coverage는 별도 지표로 표시한다.
- INV-06 Runtime Scope: runtime evidence는 해당 repository snapshot, runtime, dependency, environment, input과 scenario 범위만 증명한다.
- INV-07 Semantic Authority: 의미 권위는 `confirmed > inferred`이며 의미 overlay는 구조 Fact를 변경하지 않는다.
- INV-08 Deterministic Baseline: model과 runtime observer가 없어도 AnalysisSnapshot에서 기본 Semantic Map을 생성한다.
- INV-09 Generation Consistency: active generation의 모든 canonical artifact는 같은 `basisSha`, schema와 generation ID를 사용한다.
- INV-10 Stale Isolation: 현재 workspace epoch와 다른 결과, 취소된 request 결과와 변경된 evidence는 새 generation에 fresh 상태로 포함하지 않는다.
- INV-11 Source Read-Only: 분석, instrumentation과 model execution은 product source를 수정하지 않는다.
- INV-12 Security Boundary: secret scanner를 통과한 제한된 Evidence Pack만 model provider에 전달하고 raw source repository 권한을 model host에 주지 않는다.
- INV-13 Evidence Navigation: 모든 중요한 map node는 source, test, contract 또는 runtime scenario로 이동할 수 있어야 한다.
- INV-14 Schema Authority: process, language와 UI 경계의 canonical artifact는 versioned JSON Schema로 강제한다.
- INV-15 Approval Durability: 사람 승인·수정·거절은 append-only event로 보존하고 model proposal은 durable knowledge로 간주하지 않는다.
- INV-16 Model Install Disclosure: optional model은 Section 3.2의 설치 정보를 먼저 표시하고 사용자가 선택한 뒤에만 설치하거나 활성화한다.
- INV-17 Protocol Cutover: 다음 MAJOR release의 Core와 공식 adapter는 Content-Length framed JSON-RPC 2.0만 사용한다. NDJSON v1 bridge, fallback과 mixed-version compatibility를 제공하지 않는다.
- INV-18 Runtime Isolation: 모든 runtime observation은 `containerized`, `sandboxed`, `trusted_local`, `blocked` 중 하나를 기록하며 `trusted_local`은 실행할 때마다 사용자의 승인을 받는다.
- INV-19 Projection Compatibility: `SemanticMapIR`이 canonical artifact이며 FlowSpec은 versioned projection이다. Projection은 지원하는 consumer와 손실되는 field를 명시한다.
- INV-20 Dynamic-Language GA: 지원 feature subset 안의 dynamic-language critical-flow coverage는 compiler-resolved 언어에 적용하는 동일한 release threshold를 통과해야 한다.

## 7. Observable User Flow

### 7.1 Initial Understanding

1. 사용자가 feature, PR, bug, onboarding 또는 impact query를 선택한다.
2. CodeFlow가 repository snapshot과 language capability를 고정한다.
3. Analysis Layer가 현재 task와 연결된 Fact, relation, change impact, evidence와 unknown을 생성한다.
4. 시스템이 model과 runtime enrichment를 기다리지 않고 기본 Semantic Map을 표시한다.
5. 사용자는 7~15개의 핵심 행동에서 시작, 판단, 상태 변화, external effect와 결과를 읽는다.
6. 사용자가 step을 선택하면 caller, current, callee, test와 source evidence가 같은 선택 상태로 열린다.

### 7.2 Dynamic Evidence Enrichment

1. 시스템이 중요한 unresolved dynamic boundary를 식별한다.
2. 시스템이 관련 test, fixture 또는 승인된 scenario와 실제 isolation level을 제시한다.
3. 허용된 scenario만 실행하고 runtime event를 현재 static candidate와 연결한다.
4. 새 evidence가 일치하면 `observed_runtime` 또는 `corroborated`, 충돌하면 `conflicting`, 부족하면 `unknown`으로 새 generation을 발행한다.
5. 기존 Timeline 순서와 사용자 선택은 유지하고 해당 step의 evidence status만 갱신한다.

### 7.3 Semantic Enrichment and Approval

1. 시스템이 현재 flow의 검증된 Fact와 evidence만 포함한 Semantic Pack을 만든다.
2. optional model provider가 허용된 taxonomy 안에서 relation label, behavior group, 짧은 설명, 중요도와 질문을 제안한다.
3. Semantic Validator가 reference, basis, evidence policy, taxonomy, 크기와 secret을 검증한다.
4. 검증된 제안만 `inferred`로 표시한다.
5. 사용자는 의미 이름과 규칙을 승인, 수정 후 승인, 거절 또는 근거 부족으로 처리한다.
6. 승인 결과는 Semantic Overlay Ledger에 저장되고 다음 valid generation에 반영된다.

### 7.4 Change Review

1. 시스템이 baseline과 current snapshot을 같은 schema와 analyzer version으로 비교한다.
2. 사용자는 파일 diff보다 added, changed, removed behavior와 영향받는 flow, state, API, test를 먼저 확인한다.
3. 변경된 evidence와 semantic claim은 `stale` 또는 `orphaned`로 표시된다.
4. 사용자는 변경된 step의 source, test와 runtime verification을 확인한다.

### 7.5 Optional Model Installation

1. 사용자가 semantic enrichment를 선택하면 시스템이 model ID와 immutable revision, license, download와 설치 크기, checksum과 필요한 local runtime을 표시한다.
2. 시스템은 model 활성화로 추가되는 relation label, behavior grouping, 설명, 중요도와 질문 기능을 deterministic baseline과 비교해 표시한다.
3. 시스템은 repository data가 local process를 벗어나는지, model host가 받는 Evidence Pack 범위와 network 요구를 표시한다.
4. 사용자가 명시적으로 선택한 경우에만 model과 runtime을 설치하거나 활성화한다.
5. 설치 실패, 비활성화 또는 제거 후에도 deterministic Semantic Map은 동일하게 동작한다.

## 8. Failure and Boundary Behavior

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| Analysis 또는 adapter가 일부 실패 | 이전 valid generation을 유지하고 지원 가능한 범위와 capability limitation을 표시한다. | 불완전한 결과를 active generation으로 공개하지 않는다. | 실패 adapter를 제한 횟수 재시작하고 이후 `partial` 또는 `unavailable`로 표시한다. |
| Runtime scenario가 실패, timeout 또는 blocked | scenario 결과와 coverage gap을 표시하고 해당 동작을 `unknown`으로 유지한다. | 동작 부재 Fact를 만들지 않는다. | 환경 또는 승인 조건을 수정한 뒤 새 run으로 재시도한다. |
| Static candidate와 runtime target이 충돌 | 해당 edge를 `conflicting`으로 표시한다. | 자동 Fact 승격과 의미 보강을 금지한다. | 사용자 또는 추가 evidence가 충돌을 해소해야 한다. |
| Model이 미설치, crash 또는 timeout | deterministic map과 confirmed overlay를 계속 표시한다. | 실패 proposal을 ledger에 기록하지 않는다. | model 상태만 복구한 뒤 enrichment job을 다시 실행한다. |
| SemanticProposal이 schema 또는 evidence validation 실패 | 해당 proposal을 적용하지 않고 validation failure를 표시한다. | 구조 Fact와 active map은 변하지 않는다. | validation error를 제공해 최대 한 번 재시도한다. |
| 개별 claim의 근거가 부족 | 해당 claim만 `unknown` 또는 abstention으로 표시한다. | 다른 valid claim은 유지한다. | 필요한 evidence를 추가하거나 사람이 의미를 승인한다. |
| Workspace epoch가 바뀌거나 request가 취소됨 | 이전 request 결과를 폐기한다. | staging generation과 active pointer를 변경하지 않는다. | 현재 epoch에서 새 분석을 시작한다. |
| SQLite projection이 busy, corrupt 또는 migration 실패 | canonical JSON generation을 계속 사용한다. | 승인 이력과 canonical artifact를 잃지 않는다. | projection을 폐기하고 JSON artifact에서 재생성한다. |
| Renderer가 실패 | SemanticMapIR과 active generation을 보존한다. | canonical artifact를 수정하지 않는다. | CLI, MCP 또는 복구된 renderer에서 같은 generation을 연다. |
| 승인 저장이 실패 | UI는 승인 완료로 표시하지 않는다. | 기존 ledger와 구조 Fact를 변경하지 않는다. | 저장 오류를 해결한 뒤 동일 승인 요청을 다시 제출한다. |
| Active generation 발행 중 검증 실패 | 사용자는 이전 complete generation을 계속 본다. | pointer를 교체하지 않는다. | staging artifact를 수정하거나 폐기한 뒤 다시 발행한다. |

## 9. Decisions

모든 항목은 이 Approved 계약의 선택이다. 기존 승인 계약과 충돌하는 항목은 Section 12.1의 명시적 개정 절차를 거쳐야 한다.

| ID | Question | Decision | Rationale | Rejected Alternative |
|---|---|---|---|---|
| D-01 | 시스템 책임을 어떻게 분리하는가? | `Analysis → Semantic → Visualizer` 세 레이어와 versioned artifact 경계를 사용한다. | 사실, 의미, 표시의 권위와 장애를 분리한다. | 단일 model pipeline 또는 UI가 의미를 직접 추론하는 구조 |
| D-02 | 작은 모델은 무엇을 소유하는가? | relation label, behavior grouping, 설명, 중요도, 질문과 abstention만 제안한다. | 의미 압축은 얻되 구조 hallucination을 차단한다. | 모델이 Fact 또는 최종 FlowSpec을 직접 생성 |
| D-03 | 동적 언어를 어떻게 분석하는가? | static candidate, type constraint, framework rule, scoped runtime evidence와 evidence fusion을 결합한다. | 발행 precision을 유지하면서 coverage gap을 노출한다. | 정적 parser만 사용하거나 runtime trace를 전체 동작으로 일반화 |
| D-04 | Core 구현 언어는 무엇인가? | MVP의 lifecycle, orchestration, validation, graph, storage와 publish는 Go 1.26을 유지한다. | 현재 CodeFlow 자산과 단일 binary 배포를 재사용한다. | 측정된 병목 없이 Rust 또는 TypeScript로 전면 재작성 |
| D-05 | 언어별 분석과 model runtime은 어디서 실행하는가? | 각 생태계의 공식 analyzer와 llama.cpp를 Core 밖의 adapter 또는 sidecar로 실행한다. | 정확도, crash isolation과 cross-platform packaging 책임을 분리한다. | 모든 analyzer와 model을 Go process에 직접 링크 |
| D-06 | 기본 UI 범위는 무엇인가? | task별 7~15개 행동, Business Summary, Flow Story, Evidence Workbench와 Question Lens를 표시한다. | 전체 graph가 만드는 인지 부담을 제한한다. | 저장소 전체 dependency graph를 기본 표시 |
| D-07 | LSP의 역할은 무엇인가? | navigation fast path와 capability probe로만 사용하고 AnalysisSnapshot의 유일한 producer로 사용하지 않는다. | branch, state, dynamic dispatch와 runtime path에는 direct analyzer가 필요하다. | LSP를 전체 analysis authority로 사용 |
| D-08 | canonical storage는 무엇인가? | versioned JSON generation과 append-only semantic event를 canonical로 두고 SQLite는 삭제 가능한 query projection으로만 사용한다. | 재현성과 복구 가능성을 유지한다. | SQLite 또는 graph database만 canonical store로 사용 |
| D-09 | Fine-tuning은 언제 수행하는가? | prompt, schema, glossary와 retrieval 개선 후에도 반복 오류가 남고 충분한 승인 데이터가 쌓인 뒤 좁은 작업에만 적용한다. | 초기 비용과 repository별 과적합을 피한다. | MVP 시작부터 repository별 model Fine-tuning |
| D-10 | 보강 작업은 기본 map을 막는가? | Runtime과 semantic enrichment는 비동기이며 complete generation 단위로 갱신한다. | model과 runtime latency를 사용자 첫 화면에서 분리한다. | 모든 보강이 끝날 때까지 map을 표시하지 않음 |
| D-11 | 기존의 model 비의존 계약을 어떻게 변경하는가? | Deterministic Core를 필수 baseline으로 유지하고, 별도 local optional model capability를 허용하도록 기존 계약을 명시적으로 개정한다. 설치 전 Section 3.2 정보를 제공한다. | 기본 동작의 재현성을 유지하면서 선택적 semantic enrichment를 제공한다. | 기존 계약을 암묵적으로 우회하거나 model을 필수 dependency로 지정 |
| D-12 | Adapter protocol은 어떻게 전환하는가? | 다음 MAJOR release에서 Content-Length framed JSON-RPC 2.0으로 전면 전환한다. Dart와 TypeScript/JavaScript adapter를 함께 v2로 제공하고 NDJSON v1 bridge나 fallback은 제공하지 않는다. | Legacy compatibility 분기보다 코드 이해 evidence, 취소·streaming·bounded concurrency와 성능을 우선한다. | NDJSON 유지, bridge 기간 운영 또는 mixed-version compatibility |
| D-13 | 첫 product surface는 무엇인가? | 현재 loopback FlowView를 1차 구현 surface로 사용한다. VS Code extension은 별도 slice로 분리하고 1차 구현에서 제외한다. | 현재 배포와 generation 경계를 재사용하고 editor packaging을 독립 검토한다. | VS Code 우선 또는 두 surface 동시 구현 |
| D-14 | Runtime scenario를 어떻게 격리하는가? | `containerized`, `sandboxed`, `trusted_local`, `blocked`를 기록한다. `trusted_local`은 command, source·credential·network 접근을 표시하고 매 실행 승인을 받는다. | 환경 지원 범위와 실행 위험을 사용자에게 명시한다. | 격리 상태를 숨긴 자동 실행 또는 runtime 기능 전체 제외 |
| D-15 | 새 artifact와 FlowSpec의 관계는 무엇인가? | `SemanticMapIR`을 canonical artifact로 두고 FlowSpec은 versioned projection으로 유지한다. | 의미 지도 계약과 기존 consumer compatibility를 분리한다. | FlowSpec additive 확장만 사용하거나 즉시 breaking major 전환 |
| D-16 | CodeGraph는 어떤 권위를 가지는가? | Candidate와 cross-repository navigation source로 사용하되 Fact 발행에는 current source와 language analyzer 검증을 요구한다. | 탐색 범위를 줄이면서 stale index가 Fact authority가 되는 것을 막는다. | CodeGraph 단독 authority 또는 완전 제외 |
| D-17 | Dynamic-language GA 범위는 무엇인가? | 초기 subset은 module/import resolution, function·method call, inheritance/interface dispatch, async flow, framework route·event·DI binding과 scoped runtime으로 검증 가능한 callback target이다. 임의 reflection, runtime code generation, `eval`과 native FFI 내부 target은 `unknown`으로 남긴다. | 지원 경계를 측정 가능하게 고정하고 언어 간 동일한 coverage gate를 적용한다. | 언어 전체 지원을 선언하거나 precision만으로 GA 판단 |
| D-18 | 초기 local model pack은 무엇인가? | `Qwen3-4B-Instruct-2507`을 기본 model로 선택하고 `Granite 4.2 3B`를 고정 challenger로 평가한다. 둘 다 immutable revision과 artifact checksum으로 고정한다. | Qwen은 작은 non-thinking instruction model로 기본 출력 제어에 적합하고, Granite는 더 작은 한국어 평가·공식 GGUF 후보로 비교 가치가 있다. | 모든 후보를 로컬에서 전수 시험하거나 7B model을 초기 기본값으로 사용 |
| D-19 | Benchmark corpus와 hardware matrix는 무엇인가? | Section 10.5의 synthetic fixture와 네 개의 permissive-license open-source repository를 혼합한다. 성능 gate는 8-core/16GB macOS Apple Silicon Metal과 8-core/16GB Linux x86_64 CPU-only profile에서 측정한다. | 재현 가능한 edge case와 실제 framework·monorepo 대표성을 함께 확보한다. | Synthetic-only, 사용자 code 기본 수집 또는 branch head 기반 fixture |

## 10. Quality Constraints

아래 수치는 원문이 제안한 release target이다. 현재 구현 성능으로 확인된 사실이 아니며, 고정 fixture와 reference machine에서 검증해야 한다.

### 10.1 Analysis Quality

- QC-01 `[Decision]` Critical published edge precision은 99% 이상이어야 한다.
- QC-02 `[Decision]` 전체 published Fact precision은 97% 이상이어야 한다.
- QC-03 `[Decision]` 같은 relation 종류는 compiler-resolved와 hybrid-dynamic 언어에 같은 precision threshold를 적용한다.
- QC-04 `[Decision]` 중요한 unresolved dynamic boundary가 남은 flow는 `complete`로 표시하지 않는다.
- QC-11 `[Decision]` Section D-17의 지원 subset에는 compiler-resolved fixture와 같은 numeric critical-flow coverage threshold를 적용한다. 해당 숫자를 benchmark slice에서 고정하고 통과하기 전에는 dynamic-language capability를 GA로 표시하지 않는다.

### 10.2 Performance Budget

Reference machine은 8-core CPU, 16GB RAM, SSD로 정의한다.

| Operation | Proposed Release Target |
|---|---|
| Cached map open | p95 1초 이하 |
| Changed file parse와 impact candidate | p95 1초 이하 |
| Task-scoped deterministic refresh | p95 5초 이하 |
| Core cached query | p95 200ms 이하 |
| CodeLens evidence open | p95 500ms 이하 |
| Local 4B semantic proposal on Metal | p95 15초 이하 |
| Local 4B semantic proposal on CPU-only | p95 30초 이하 |
| Model host cold start | p95 10초 이하, UI 비차단 |
| Core idle memory | model 제외 250MB 이하 |
| 20만 LOC Core peak memory | adapter와 model 제외 300MB 이하 |
| Local model working set | 5GB 이하 target |

### 10.3 Semantic Model Gate

- QC-05 `[Decision]` Constrained generation 이후 JSON Schema validity는 100%여야 한다.
- QC-06 `[Decision]` 존재하지 않는 Fact reference와 unsupported structural claim의 acceptance는 0이어야 한다.
- QC-07 `[Decision]` Relation classification macro F1은 0.90 이상이어야 한다.
- QC-08 `[Decision]` Behavior grouping human acceptance는 85% 이상이어야 한다.
- QC-09 `[Decision]` 적절한 abstention precision은 95% 이상이어야 한다.
- QC-10 `[Decision]` 한국어 label human rating은 5점 기준 평균 4.0 이상이어야 한다.

### 10.4 Initial Model Pack

| Role | Model | Selection Basis | Release Condition |
|---|---|---|---|
| Default | `Qwen3-4B-Instruct-2507` | Apache-2.0, 4B, non-thinking, multilingual·coding·instruction profile, 256K context와 llama.cpp 지원 | 공식 revision, license, checksum, target quantization과 Section 10.3 gate를 통과해야 한다. |
| Challenger | `Granite 4.2 3B` | Apache-2.0, 3B class, 한국어 평가 범위, 128K context, non-thinking mode와 공식 GGUF | 동일 Evidence Pack과 gold set에서 default보다 품질·latency·memory 중 명확한 이점이 확인될 때만 default를 교체한다. |

전 후보의 로컬 전수 평가는 요구하지 않는다. 위 두 후보만 공식 자료로 shortlist하고, 배포 전 license·checksum·runtime compatibility·schema output smoke test와 Section 10.3 gold-set gate를 실행한다. 둘 다 gate를 통과하지 못하면 model enrichment는 `experimental`로 유지하고 deterministic baseline만 GA로 제공한다.

### 10.5 Benchmark Fixture Portfolio

| Fixture | Purpose | Required Pinning |
|---|---|---|
| 계획된 CodeFlow `semantic-map-benchmark-fixtures` | 50K, 200K, 1M LOC deterministic graph와 reflection, callback, generated boundary, conflict, stale evidence edge case | 공개 Apache-2.0 repository, generator revision, seed, expected graph와 expected unknown |
| `flutter/samples`의 `compass_app` | 공식 Dart/Flutter MVVM baseline과 작은 task-scoped flow | Commit SHA, BSD-3-Clause license, subpath, Flutter/Dart toolchain |
| `localsend/localsend` | 실제 Dart/Flutter state, isolate, network와 native boundary | Commit SHA, Apache-2.0 license, selected flows, Flutter/Dart toolchain |
| `nestjs/nest`의 `sample/` | TypeScript decorator, DI, route와 event framework rule | Commit SHA, MIT license, subpaths, Node/TypeScript toolchain |
| `backstage/backstage` | 대형 TypeScript monorepo, plugin과 cross-package graph | Commit SHA, Apache-2.0 license, selected packages and flows, Node/TypeScript toolchain |

Fixture manifest는 repository URL, immutable commit SHA, license, 허용 subpath, toolchain lock, entry query, expected critical edge, expected unknown과 scenario command를 기록한다. 사용자 repository는 기본 corpus에 포함하지 않는다.

성능과 model gate는 다음 두 reference profile에서 측정한다.

| Profile | Environment | Required Measurement |
|---|---|---|
| H-01 | macOS Apple Silicon, 8 CPU cores, 16GB unified memory, SSD, Metal | Core latency·memory, local model cold start·latency·working set |
| H-02 | Linux x86_64, 8 CPU cores, 16GB RAM, SSD, CPU-only | Core latency·memory, local model cold start·latency·working set |

### 10.6 Operational Quality

- 하나의 Cognitive Debt Score로 합치지 않는다.
- Analysis precision, coverage, semantic accuracy, comprehension time, review outcome, defect, revert, incident recovery와 latency를 별도 지표로 관찰한다.
- Model 또는 prompt 변경은 Analysis cache를 무효화하지 않는다.
- 재현할 수 없는 runtime evidence는 cache하지 않는다.

## 11. Feature-Level Acceptance

- FA-01: WHEN 사용자가 지원되는 repository의 feature 또는 change query를 요청하면, THE system SHALL model과 runtime enrichment를 기다리지 않고 현재 snapshot에 근거한 기본 Semantic Map을 제공한다.
- FA-02: THE system SHALL 모든 중요한 map node에서 source, test, contract 또는 runtime evidence로 이동할 수 있게 한다.
- FA-03: IF call target, branch 또는 relation을 확인할 근거가 부족하면, THEN THE system SHALL 해당 위치를 `unknown`, `unresolved` 또는 `unavailable`로 표시하고 확정 Fact를 만들지 않는다.
- FA-04: THE system SHALL structural status, semantic status, freshness와 evidence scope를 서로 독립된 상태로 표시한다.
- FA-05: WHERE dynamic-language Fact가 발행되면, THE system SHALL 지원 subset 안에서 같은 relation 종류의 compiler-resolved Fact와 동일한 publication precision 및 critical-flow coverage gate를 적용한다.
- FA-06: WHEN runtime evidence가 map에 반영되면, THE system SHALL scenario, input, runtime, dependency, environment와 isolation scope를 사용자에게 확인 가능하게 한다.
- FA-07: IF model proposal이 존재하지 않는 Fact, branch, target 또는 source range를 포함하면, THEN THE system SHALL 그 항목을 SemanticMapIR에 포함하지 않는다.
- FA-08: WHEN 사용자가 의미를 승인, 수정 후 승인 또는 거절하면, THE system SHALL append-only event를 저장하고 구조 Fact를 변경하지 않는다.
- FA-09: WHEN baseline과 current snapshot이 제공되면, THE system SHALL added, changed, removed behavior와 영향받는 flow, state, API와 test를 표시한다.
- FA-10: WHEN code, dependency, runtime 또는 framework rule이 변경되면, THE system SHALL 영향을 받는 evidence와 semantic claim을 `stale` 또는 `orphaned`로 표시하고 current Fact로 조용히 유지하지 않는다.
- FA-11: WHEN 새 generation의 schema, reference, checksum, coverage 또는 epoch 검증이 실패하면, THE system SHALL active pointer를 교체하지 않고 이전 complete generation을 유지한다.
- FA-12: IF model host, runtime observer, adapter, SQLite projection 또는 renderer가 실패하면, THEN THE system SHALL 가능한 deterministic artifact와 이전 valid generation을 보존한다.
- FA-13: THE system SHALL product source를 수정하지 않고 secret-redacted Evidence Pack만 optional model provider에 전달한다.
- FA-14: THE system SHALL 기본 화면을 7~15개의 task-scoped 핵심 행동으로 제한하고 전체 repository graph를 기본 표시하지 않는다.
- FA-15: THE system SHALL 기존 CLI, MCP, FlowView와 artifact consumer별 지원 protocol, schema, projection version과 migration 상태를 명시한다.
- FA-16: THE system SHALL reference fixture에서 Section 10의 release target을 측정하고 통과하지 못한 capability를 완료 또는 GA로 표시하지 않는다.
- FA-17: WHEN 사용자가 optional model 설치를 선택하면, THE system SHALL download 전에 Section 3.2의 model과 기능 변화 정보를 표시하고 명시적 선택 없이는 설치하거나 활성화하지 않는다.
- FA-18: WHEN framed JSON-RPC 2.0 adapter protocol을 도입하면, THE system SHALL Dart와 TypeScript/JavaScript native v2 adapter, shared conformance, v1 rejection과 MAJOR cutover disclosure를 제공한다.
- FA-19: THE first implementation SHALL 현재 FlowView에서 동작하며 VS Code extension을 필수 dependency 또는 완료 조건으로 포함하지 않는다.
- FA-20: WHEN runtime isolation이 `trusted_local`이면, THE system SHALL command와 접근 범위를 표시하고 매 실행 사용자의 승인을 받는다.
- FA-21: THE system SHALL Section 10.5 fixture를 immutable commit과 toolchain에 고정하고 같은 input에서 재현 가능한 expected Fact, unknown과 metric을 제공한다.

## 12. Resolved Decisions

2026-09-01에 Q-01부터 Q-09까지 다음과 같이 결정하고 부모 계약과 함께 승인했다. 이 결정은 Section 12.1의 기존 승인 계약을 자동으로 대체하지 않는다.

| ID | Resolution | Contract Impact |
|---|---|---|
| Q-01 | 추천안을 채택한다. Deterministic Core를 유지하고 optional local model capability를 허용하며 설치 전 기능과 운영 변화를 명확히 표시한다. | D-11, INV-16, Section 7.5, FA-17 |
| Q-02 | 2026-09-02 amendment로 migration bridge를 제거한다. 다음 MAJOR release는 framed JSON-RPC 2.0 Core와 Dart·TypeScript/JavaScript native v2 adapter만 지원한다. | D-12, INV-17, FA-18 |
| Q-03 | VS Code extension을 별도 slice로 분리하고 1차 구현에서 제외한다. | D-13, FA-19 |
| Q-04 | 추천 isolation과 매 실행 승인 정책을 채택한다. | D-14, INV-18, FA-20 |
| Q-05 | `SemanticMapIR` canonical과 FlowSpec versioned projection을 채택한다. | D-15, INV-19 |
| Q-06 | CodeGraph를 candidate·navigation source로 사용하고 analyzer 검증 후 Fact를 발행한다. | D-16 |
| Q-07 | 지원 feature subset을 고정하고 subset 안에서 compiler-resolved 언어와 같은 coverage threshold를 적용한다. | D-17, INV-20 |
| Q-08 | 공식 자료로 두 후보를 shortlist하고 `Qwen3-4B-Instruct-2507`을 초기 default, `Granite 4.2 3B`를 challenger로 지정한다. | D-18, Section 10.4 |
| Q-09 | Synthetic fixture와 지정한 네 open-source repository를 혼합하고 두 reference hardware profile을 사용한다. | D-19, Section 10.5, FA-21 |

Open Decision은 없다.

### 12.1 Existing Contract Amendments Required Before Implementation

- `docs/design-v2.md`의 model 비의존 원칙은 deterministic baseline을 유지하면서 optional local model capability를 허용하도록 명시적으로 개정하고 다시 승인해야 한다.
- `docs/spec/llm-language-adapter-protocol.md`와 새 v2 schema는 Content-Length JSON-RPC 2.0, v1 rejection, Dart·TypeScript/JavaScript native v2와 MAJOR cutover를 정의해야 한다.
- 위 변경은 해당 개정 계약의 승인 전에는 구현하지 않는다.

## 13. Done When

이 feature는 다음 evidence가 모두 있을 때 delivered로 간주한다.

- DW-01: 이 부모 계약이 명시적으로 Approved 상태이며 blocking Open Decision이 없다.
- DW-02: 승인된 부모 계약을 사용자 목표 기준 Vertical Slice 계약으로 분리하고 각 slice가 independent review와 명시적 승인을 통과했다.
- DW-03: `AnalysisSnapshot`, capability, runtime evidence, coverage, semantic proposal, overlay, SemanticMapIR과 projection schema가 valid·invalid fixture로 강제된다.
- DW-04: 모든 Feature-Level Acceptance 항목에 public seam을 통한 자동 또는 명시적 수동 evidence가 있다.
- DW-05: Model과 runtime observer가 없는 환경에서도 deterministic Semantic Map end-to-end test가 통과한다.
- DW-06: Dynamic-language fixture가 publication precision, unknown 보존, conflict와 coverage gate를 통과한다.
- DW-07: Stale epoch, cancelled request, adapter crash, runtime timeout, invalid proposal, index corruption과 publish failure test가 이전 valid generation 보존을 증명한다.
- DW-08: Secret redaction, source read-only, loopback 또는 local IPC authorization과 runtime isolation 검증이 통과한다.
- DW-09: Reference fixture와 hardware matrix에서 Section 10 성능·memory·model gate 결과가 기록되고 통과하지 못한 capability는 GA로 표시되지 않는다.
- DW-10: 사용자가 실제 task에서 시작, 핵심 판단과 상태 변화, 결과, change impact와 supporting evidence를 정확히 설명할 수 있다는 UX evidence가 있다.
- DW-11: `go test ./...`와 승인된 slice가 지정한 추가 contract, race, benchmark와 UI 검증이 통과한다.
- DW-12: Optional model installer가 Section 3.2 정보를 표시하고 opt-in, 실패, 비활성화와 제거 후 deterministic fallback test를 통과한다.
- DW-13: Core, Dart와 TypeScript/JavaScript adapter가 shared v2 conformance suite를 통과하고 installer가 v1 제거와 MAJOR cutover를 적용 전에 표시한다.
- DW-14: Section 10.5 fixture manifest가 immutable commit, license, toolchain과 expected result를 고정하고 H-01과 H-02 결과를 재현한다.

## 14. Approval Readiness

현재 상태는 protocol cutover amendment 검토를 위한 `Proposed`다.

- Confirmed scope: current-state authority, evidence anchor, unknown 보존, provenance/freshness, deterministic baseline과 atomic generation 확장
- Assumptions requiring evidence: task scope 품질, 7~15개 행동, 12K Evidence Pack, runtime scenario availability, Go graph budget, FlowSpec projection compatibility
- Resolved decisions: Q-01부터 Q-09까지의 public behavior, security, compatibility와 release fixture 방향
- Approval blockers: 없음.
- Approval evidence: 2026-09-01 사용자 명시 승인.
- Next permitted action: Section 12.1의 기존 계약 개정안을 승인한 뒤, 사용자 목표 기준 Vertical Slice 계약을 작성하고 개별 승인한다.
- Not permitted yet: 승인되지 않은 기존 계약 변경의 구현 또는 Vertical Slice 계약 승인 전 production implementation
