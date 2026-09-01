# Semantic Map Layered Architecture — VS-05 로컬 의미 보강 사용

- Contract ID: `SMAP-VS-05`
- Contract Status: Proposed
- Independent Review: Passed
- Parent Contract: `docs/design/specs/2026-09-01-semantic-map-layered-architecture-ko.md`
- Created: 2026-09-01
- Depends On: SMAP-VS-02, `docs/design-v2.md`의 optional local model capability 개정 승인
- Parent Acceptance Coverage: FA-07, FA-12, FA-13, FA-17

## 1. User Outcome

개발자는 설치되는 model과 기능·데이터 처리 변화를 확인한 뒤 optional local semantic enrichment를 선택하고, 구조 Fact를 바꾸지 않는 inferred 설명과 grouping을 deterministic map 위에서 사용할 수 있다.

## 2. Scope

### In Scope

- Optional model 설치 전 disclosure와 명시적 opt-in
- `Qwen3-4B-Instruct-2507` default와 `Granite 4.2 3B` challenger의 immutable artifact 관리
- Local llama.cpp-compatible model host
- Secret-redacted, task-scoped Evidence Pack
- Relation label, behavior grouping, 짧은 설명, 중요도, 질문과 abstention proposal
- Schema, reference, basis, taxonomy, size와 secret validation
- `inferred` semantic status와 deterministic fallback
- Model·prompt cache와 Analysis cache 분리
- Model 비활성화와 제거

### Out of Scope

- Model의 Fact, branch, target, state transition 또는 source range 생성 — Analysis Layer만 소유한다.
- 외부 model provider — 별도 workspace opt-in 계약 없이는 허용하지 않는다.
- 사람 승인과 durable confirmed overlay — VS-06이 담당한다.
- Fine-tuning, embeddings, vector database와 agent framework — 부모 Non-Goal이다.

## 3. Preconditions

- `docs/design-v2.md`가 deterministic baseline을 유지하면서 optional local model capability를 허용하도록 개정되고 Approved 상태다.
- SMAP-VS-02의 AnalysisSnapshot과 deterministic SemanticMapIR이 존재한다.
- Model manifest에 immutable revision, license, artifact size, checksum, quantization과 runtime compatibility가 있다.

## 4. Public Seam

- CodeFlow installer 또는 동등한 local setup surface의 model capability 선택
- Model 상태, 활성화, 비활성화와 제거 surface
- Semantic enrichment 요청과 generation 상태
- FlowView의 inferred relation, group, 설명, 중요도, 질문과 abstention 표시

Public seam은 설치와 데이터 처리 변화를 명시하되 raw prompt, raw response 또는 repository 전체 source를 기본 노출하지 않는다.

## 5. Boundary Coverage

사용자 model capability 선택 → disclosure와 opt-in → pinned manifest 기반 download·checksum validation → local model host 준비 → AnalysisSnapshot에서 redacted Evidence Pack 생성 → constrained semantic proposal → Semantic Validator → inferred overlay → 새 validated generation → FlowView semantic enrichment 또는 deterministic fallback

## 6. Inherited Invariants

- INV-01 Current-State Authority
- INV-02 Fact Ownership
- INV-03 Evidence Grounding
- INV-07 Semantic Authority
- INV-08 Deterministic Baseline
- INV-09 Generation Consistency
- INV-10 Stale Isolation
- INV-11 Source Read-Only
- INV-12 Security Boundary
- INV-14 Schema Authority
- INV-16 Model Install Disclosure

## 7. Slice-Specific Rules

- BR-01: Download 전 model ID, immutable revision, license, download·install size, checksum, quantization, runtime, repository data 접근, network, 추가 기능, fallback, disable과 remove 방법을 표시한다.
- BR-02: 사용자의 명시적 opt-in 없이는 model 또는 model runtime을 download, install, activate하지 않는다.
- BR-03: Initial default는 `Qwen3-4B-Instruct-2507`, challenger는 `Granite 4.2 3B`이며 artifact는 checksum으로 고정한다.
- BR-04: Model host에는 secret scanner를 통과한 task-scoped Evidence Pack만 전달하고 raw repository 권한을 주지 않는다.
- BR-05: Proposal은 existing Fact와 Evidence Anchor만 참조하고 허용 taxonomy 밖의 구조 claim을 만들지 않는다.
- BR-06: Invalid proposal은 개별 또는 전체 적용을 거부하며 구조 Fact와 active deterministic map을 변경하지 않는다.
- BR-07: Model, prompt 또는 quantization 변경은 Analysis cache를 무효화하지 않는다.

## 8. Acceptance Criteria

- A1. WHEN 사용자가 optional model 설치를 선택하면, THE system SHALL download 전에 BR-01의 model, 기능과 데이터 처리 정보를 표시한다.
- A2. IF 사용자가 명시적으로 opt-in하지 않으면, THEN THE system SHALL model과 runtime을 download, install 또는 activate하지 않는다.
- A3. IF model license, revision, checksum 또는 runtime compatibility validation이 실패하면, THEN THE system SHALL 설치를 완료로 표시하지 않고 deterministic baseline을 유지한다.
- A4. WHERE local semantic enrichment가 활성화되면, THE system SHALL secret-redacted task-scoped Evidence Pack만 model host에 전달한다.
- A5. WHEN model proposal이 존재하지 않는 Fact, branch, target 또는 source range를 참조하면, THE system SHALL 해당 proposal을 SemanticMapIR과 durable ledger에 포함하지 않는다.
- A6. THE system SHALL 적용된 relation label, behavior group, 설명, 중요도와 질문을 `inferred`로 표시하고 구조 Fact를 변경하지 않는다.
- A7. IF model이 미설치, disabled, removed, crash 또는 timeout 상태이면, THEN THE system SHALL deterministic Semantic Map과 confirmed overlay를 계속 제공한다.
- A8. WHEN model, prompt 또는 quantization이 바뀌면, THE system SHALL semantic cache만 필요한 범위에서 무효화하고 Analysis cache를 유지한다.
- A9. WHEN 사용자가 model capability를 제거하면, THE system SHALL model artifact와 owned runtime을 설치 기록에 따라 제거하고 deterministic artifact와 approved knowledge를 보존한다.
- A10. UNTIL SMAP-VS-07의 semantic model gate가 통과하면, THE system SHALL model enrichment를 `experimental`로 표시하고 GA evidence로 사용하지 않는다.

## 9. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| Opt-in 없음 | Deterministic-only capability 표시 | Download와 설정 변경 없음 | 사용자가 나중에 선택 가능 |
| Download 또는 checksum 실패 | Installation failed와 검증 정보 | Partial artifact를 active로 사용하지 않음 | 안전한 재시도 또는 제거 |
| Model host crash·timeout | Enrichment unavailable과 deterministic map | Failed proposal을 ledger에 기록하지 않음 | Host 복구 후 enrichment 재실행 |
| Evidence Pack secret 검출 | Proposal job blocked와 redaction 진단 | Unredacted input 전달 없음 | Scope 또는 redaction 수정 |
| Proposal schema·reference 위반 | Rejected proposal와 validation failure | Structure와 active map 변경 없음 | Error 근거로 최대 한 번 재시도 |
| Model 제거 실패 | Partial removal과 남은 owned artifact 표시 | 사용자 source와 approved ledger 변경 없음 | Install record로 복구·재제거 |

## 10. Data and Interaction Contract

- ModelManifest: model ID, revision, license, source, artifact size, install size, checksum, quantization, runtime version과 supported hardware
- InstallDecision: disclosure version, selected capability, timestamp와 opt-in result. Repository source나 secret을 포함하지 않는다.
- SemanticPack: basis SHA, task, allowed taxonomy, Fact·evidence reference와 redacted snippets. 기본적으로 raw pack을 영구 저장하지 않고 hash와 version만 기록한다.
- SemanticProposal: proposal ID, model·prompt version, basis SHA, existing Fact refs, proposed labels·groups·explanations·importance·questions와 abstention
- Output: validated inferred overlay 또는 deterministic-only status
- Persistence: owned model artifact, install record와 semantic cache는 product source 및 Analysis cache와 분리한다.

## 11. Test Seam and Evidence

- Public seam: installer process, model capability status, enrichment request와 FlowView projection
- Required test level: installer disclosure/opt-in lifecycle, checksum fixture, secret boundary test, fake local model contract, schema invalid proposal, timeout fallback과 uninstall integration
- Replaceable external boundaries: downloader, filesystem, checksum verifier, local model host, clock와 network
- Evidence required per criterion:
  - A1, A2, A3, A9: installer lifecycle test와 artifact presence/absence
  - A4: model host boundary capture와 secret absence
  - A5, A6: proposal validator fixtures와 structure equality assertion
  - A7, A8: fault injection과 cache-key assertion
  - A10: pre-gate capability status와 release report 연결

## 12. What Could Be Wrong

- Assumption: 제한된 Evidence Pack이 작은 local model의 grouping과 설명 품질에 충분하다.
- Consequence: Validator는 통과하지만 사용 가치가 낮은 proposal이나 과도한 abstention이 발생한다.
- Validation method: 고정 gold set에서 model별 품질, latency, memory와 사람 acceptance를 측정하고 gate 미달이면 capability를 experimental로 유지한다.

## 13. Done When

- Every criterion has passing evidence.
- Model disclosure, opt-in, checksum, disable, remove와 deterministic fallback test가 통과한다.
- Semantic proposal valid·invalid schema fixture와 unsupported structural claim rejection evidence가 있다.
- `go test ./...`와 model-host contract 검증이 통과한다.
- No contract requirement is weakened.
- No unrelated behavior changes.

## 14. Open Decisions

없음.
