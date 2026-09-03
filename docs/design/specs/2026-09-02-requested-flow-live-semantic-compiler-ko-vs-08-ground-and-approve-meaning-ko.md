# 요청 흐름 이해와 실시간 Semantic Compiler — VS-08 근거 있는 의미를 승인한다

- Contract ID: `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-VS-08`
- Contract Status: Proposed
- Independent Review: Passed (`gpt-5.6-terra`, high)
- Parent Contract: `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md`
- Created: 2026-09-02
- Source: `docs/design/raw/requested-flow-live-semantic-compiler-architecture-draft-ko.md`
- Decision Records: `docs/design/decisions/requested-flow-live-semantic-compiler-decisions-ko.md`

## 1. Intent and Goal

- Intent ID: `INT-01`
- Parent Intent: 사용자가 요청한 흐름을 현재 코드 근거와 unknown을 포함한 이해 가능한 결과로 파악한다.
- Goal ID: `GOAL-08`
- Slice Goal: 사용자는 Evidence에 근거한 의미 제안을 deterministic 사실과 구분해 승인·수정 후 승인·거절한다.
- User or Caller Value: 짧은 사용자 언어 설명을 얻으면서도 model/agent가 만든 설명을 코드 사실이나 요구사항 완료로 오인하지 않는다.
- Contribution to Parent: Raw P5의 선택적 semantic enrichment와 human confirmation을 authority·validation·fallback 경계까지 포함한 독립 사용자 결과로 만든다.
- Parent Acceptance: `A12–A15`, `A23–A27`, `A28`

## 2. User Outcome

시스템은 검증된 Fact와 Evidence만 포함한 redacted pack을 optional local model에 전달해 행동 이름·그룹·짧은 설명·질문 후보를 제안한다. 사용자는 제안의 근거와 상태를 확인하고 승인, 수정 후 승인 또는 거절할 수 있으며, 승인 event는 구조적 Fact를 변경하지 않는다.

## 3. Scope

### In Scope

- Evidence Pack builder와 secret/path policy
- local SLM/model host의 격리, bounded request, schema-constrained proposal
- `SemanticClaim`의 epistemic/sourceAuthority/validation/freshness 축
- model proposal의 target·Evidence reference·basis·taxonomy 검증
- deterministic semantic naming/grouping fallback과 model timeout/unavailable 처리
- SemanticApproval의 approve, edit-then-approve, reject, revoke/supersede lifecycle
- append-only approval log, expected intent revision/basis와 idempotency key
- model install/activation 전 model ID, revision, license, checksum, data boundary와 기능 변화를 disclosure
- Q4 refinement가 Q3 Fact, RequirementAlignment와 settlement를 바꾸지 않는 규칙

### Out of Scope

- 구조적 Fact, source anchor, call/branch/state/effect 생성 또는 수정 — VS-01/02/04의 authority다.
- Task Intent normalization과 review alignment 자체 — VS-02와 VS-05가 담당한다.
- model host를 독립 MCP authority로 노출하거나 외부 provider에 기본 전송하는 행위.
- model 후보의 최종 배포 선택과 전체 release evaluation — VS-10이 담당한다.

## 4. Preconditions

- VS-02와 VS-04가 current generation, verified Evidence와 proof metadata를 제공한다.
- Evidence Pack에서 secret/path policy와 active task scope를 계산할 수 있다.
- 사용자가 semantic enrichment 또는 approval 기능을 선택했고, approval command에는 expected intent revision/basis가 있다.
- model이 없거나 사용할 수 없는 경우 deterministic fallback이 준비되어 있다.

## 5. Public Seam

Semantic enrichment result, proposal status, semantic approval command, approval history와 Evidence Dock이 공개 seam이다. 사용자는 proposal/confirmed/rejected/unknown을 보고, caller는 append-only approval event와 idempotency 결과를 조회한다.

## 6. Boundary Coverage

Current verified Fact/Evidence → redacted Evidence Pack → isolated optional model host → schema/semantic validator → inferred SemanticClaim → user approval/rejection command → approval ledger → next valid generation/projection.

## 7. Inherited Invariants

- `INV-01`, `INV-03`, `INV-04`, `INV-05`, `INV-07`, `INV-09`, `INV-10`, `INV-11`, `INV-14`, `INV-15`, `INV-16`, `INV-19`, `INV-21`, `INV-22`, `INV-23`, `INV-24`
- Raw D3, D4, D5, D14, D15, D17, D20, D21, D24–D31 및 §6.3, §10.7–§10.11, §11, §12.6–§12.7, §14–§15, §21.6–§21.8.
- model proposal과 SemanticApproval은 Evidence가 아니며 Fact authority를 갖지 않는다.

## 8. Slice-Specific Rules

- model input은 active task에 필요한 검증된 Fact/Evidence만 포함하고 secret scanner와 path policy를 통과해야 한다.
- proposal은 기존 target과 Evidence reference를 가리켜야 하며 존재하지 않는 Fact, branch, target, range 또는 rule을 추가할 수 없다.
- model proposal은 `inferred`/proposal 상태로 시작하고 Semantic Validator를 통과한 것만 projection에 보조 의미로 표시한다.
- model timeout, crash 또는 미설치는 deterministic map을 막지 않고 `enrichmentStatus`로 표시한다. 이를 quality degradation이나 currentness failure로 오인하지 않는다.
- approval event는 claim 표현에만 권위를 부여한다. 구조 Fact, Evidence, RequirementAlignment와 generation basis를 변경하지 않는다.
- approval mutation은 `idempotencyKey`와 expected intent revision/basis를 검증하며 duplicate command는 한 번만 효과를 낸다.
- Q4 refinement는 Q3 사실, critical obligation, RequirementAlignment와 settlement 결과를 변경할 수 없다.

## 9. Acceptance Criteria

- VS08-A1. WHEN semantic enrichment is requested, THE system SHALL verified current Fact와 Evidence만 포함한 secret-redacted Evidence Pack을 만든다.
- VS08-A2. WHEN optional model host가 응답하면, THE system SHALL target, Evidence refs, basis와 schema-constrained proposal을 검증한 뒤 `inferred` semantic claim으로만 표시한다.
- VS08-A3. IF proposal이 존재하지 않는 Fact, branch, target, source range, Evidence 또는 unsupported rule을 참조하면, THEN THE system SHALL proposal을 적용하지 않고 deterministic map을 변경하지 않는다.
- VS08-A4. WHEN model host가 unavailable, timeout 또는 crash이면, THE system SHALL deterministic semantic map/Delta를 유지하고 `enrichmentStatus=unavailable|timed_out`을 별도로 표시한다.
- VS08-A5. WHEN 사용자가 의미를 승인, 수정 후 승인 또는 거절하면, THE system SHALL append-only SemanticApproval event를 저장하고 구조 Fact와 Evidence를 변경하지 않는다.
- VS08-A6. IF approval command의 idempotency key가 중복되거나 expected intent revision/basis가 현재와 다르면, THEN THE system SHALL duplicate effect 또는 stale approval을 적용하지 않고 typed conflict를 반환한다.
- VS08-A7. WHEN model을 설치하거나 활성화하기 전, THE system SHALL model ID/revision, license, checksum, local data boundary, required runtime과 기능 변화를 표시하고 명시적 선택 없이는 설치·활성화하지 않는다.
- VS08-A8. WHEN Q4 explanation refinement가 발행되면, THE system SHALL Q3 structural Fact, critical obligation, RequirementAlignment와 settlement를 변경하지 않는다.
- VS08-A9. THE system SHALL approval/proposal status와 RequirementAlignment confirmed status를 별도 표시하고 agent/model 설명만으로 requirement를 confirmed로 만들지 않는다.

## 10. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| no verified Evidence Pack | proposal unavailable/unknown | model 호출과 claim 승격 없음 | current Evidence 준비 후 재요청 |
| secret/path policy failure | redaction/policy failure | 원문 pack과 model 전송 없음 | scope 축소 또는 민감 정보 제거 |
| invalid model proposal | rejected proposal과 validation reason | map/Fact/generation 변경 없음 | 최대 한 번의 명시적 재요청 |
| model timeout/crash/unavailable | deterministic result + enrichment status | quality/currentness 변경 없음 | model 상태 복구 후 same-basis refinement |
| duplicate approval command | idempotent prior result | 중복 event/effect 없음 | 기존 event 결과 반환 |
| stale intent/basis approval | typed conflict | approval ledger와 claim 변경 없음 | 최신 intent/basis에서 재확인 |
| user rejects/revokes | rejected/revoked status | structural Fact 유지 | 새 proposal 또는 evidence 추가 |
| Q4 fact mutation attempt | invalid refinement | Q3 artifact/settlement 유지 | proposal 재검증 후 표현만 보강 |

## 11. Data and Interaction Contract

- Input: verified SemanticMapIR/Evidence, Task Intent revision, active scope, optional model selection과 approval command.
- Output: redacted Evidence Pack, SemanticClaim proposal, `enrichmentStatus`, SemanticApproval event/history와 updated projection.
- Model proposal은 model revision, prompt/schema profile, evidence hash, target refs와 basis를 보유한다.
- SemanticApproval은 target claim, approved text, task intent revision, basis constraint, actor, timestamp와 active/superseded/revoked 상태를 보유한다.
- Approval API는 idempotency key와 expected generation/intent revision을 요구한다. 실패 command는 durable approval로 표시하지 않는다.

## 12. Test Seam and Evidence

- Public seam: enrichment request/result, model host boundary, SemanticClaim validator, approval command/history, Evidence Dock.
- Required test level: redaction/path policy, proposal schema/reference/basis validation, deterministic fallback, model timeout/crash, approval lifecycle, idempotency/stale command, Q4 immutability.
- Replaceable external boundaries: model process, clock, secret scanner, approval store, CAS, network/provider and FlowView projector.
- Evidence per criterion: VS08-A1 redacted pack fixture, A2/A3 proposal validator, A4 fallback fault fixture, A5/A6 approval event/idempotency fixture, A7 install disclosure, A8 Q3/Q4 immutability, A9 status separation.

## 13. Verification Plan

| Check | Applicability / Trigger | Exact Command | Expected Evidence | Required for Completion |
|---|---|---|---|---|
| Acceptance behavior | Always | `go test ./internal/fusion ./internal/mcp ./internal/secret ./internal/contractharness ./internal/e2e` | proposal validation, fallback, approval, idempotency and no-Fact-mutation pass | Yes |
| Slice tests | Always | `go test ./internal/fusion ./internal/mcp ./internal/secret ./internal/contractharness` | VS-08 package/contract tests pass | Yes |
| Type, static analysis, and lint | semantic/approval/model boundary code affected | `go vet ./...` | no applicable findings | When applicable |
| Affected build | Core or contract loader affected | `go build ./...` | Core builds successfully | When applicable |
| Architecture and policy | model/approval/persistent payload changes | `go test ./internal/contractharness -run 'TestValidateGoldenFixtures|TestValidateExportedContractBoundary'` | claim, approval, pack, status and cross-artifact fixtures validate | Yes |
| Regression | shared fusion, secret or MCP behavior affected | `go test ./...` | full Go regression suite passes | Yes |
| Security | model/provider or UX receives Evidence Pack/approval data | `go test ./internal/secret ./internal/fusion ./internal/mcp ./internal/e2e` | secret redaction, local boundary and source read-only evidence | Yes |
| Data and migration compatibility | semantic/approval ledger and CAS payload affected | `go test ./internal/contractharness` | schema version, append-only event and reference compatibility pass | Yes |
| Performance and concurrency | model enrichment is optional and no new numeric target is set here | `N/A — raw model latency is non-blocking and release evaluation belongs to VS-10` | reason recorded | No |
| Reliability and flake | model timeout/crash or approval retry is affected | `go test ./internal/mcp ./internal/fusion ./internal/e2e -count=1` | deterministic fallback and idempotent approval are stable | Yes |
| Coverage | raw model quality thresholds are release-gated, not a repository coverage percentage | `N/A — no coverage percentage command is configured` | reason recorded | No |
| Browser UX | Evidence Pack, proposal, approval and enrichment states are user-facing; raw §21.10 UX verification is required before completion | `npm --prefix web/live-comprehension-workspace run test:e2e -- --project=chromium` | Playwright proves grounded proposal, abstention, approval lifecycle, stale/idempotent command result and redacted Evidence Dock | Yes |
| Accessibility | proposal and approval states are user-facing | `npm --prefix web/live-comprehension-workspace run test:a11y` | `@axe-core/playwright`, keyboard navigation, screen-reader outline, reduced motion and contrast evidence pass | Yes |

## 14. What Could Be Wrong

- Assumption: a bounded Evidence Pack and closed taxonomy are sufficient for useful semantic naming without introducing unsupported claims.
- Consequence: the model abstains too often or proposes plausible but ungrounded meaning.
- Validation method: fixed proposal gold set containing valid, nonexistent-target, missing-evidence, conflict and secret-bearing cases.

## 15. Done When

- VS08-A1–A9 prove grounded proposal, deterministic fallback, approval durability and no structural Fact mutation.
- Duplicate/stale approval commands have no unintended effect.
- Model install disclosure, redaction and local authority boundaries are visible.
- Applicable verification rows pass or N/A reasons are explicit.
- No production code, schema or raw source is changed by this slicing task.

## 16. Open Decisions

없음. model 후보와 성능 및 결정 사항은 아래 Resolved Implementation Decisions로 확정되었다.

## 17. Resolved Implementation Decisions

- `SID-C1` (정규 payload 물리 구성): `schemas/model-proposal.schema.json`, `schemas/semantic-approval.schema.json`, `schemas/evidence-pack.schema.json` 독립 스키마 분할 및 BaseURL 등록.
- `SID-C2` (validation 경계): 모델 제안 권위(`authority=model_proposal`)와 컴파일러 검증 권위(`authority=compiler_verified`)의 엄격한 분리 검증, 승인 이벤트의 멱등성 및 원본 구조 불변 검증.
- `SID-C3` (계약 검증 범위): Evidence Pack secret 마스킹 픽스처, 스키마 프로파일 적합성 픽스처, 중복/지연 승인 idempotent 픽스처 구축.
- `SID-C4` (기존 구현 재사용/교체): VS-02/VS-03의 시맨틱 컴파일러 및 CAS 기반 Evidence를 Evidence Pack으로 패키징.
- `SID-C5` & `SID-08` (Model Proposal Profile, Redaction, Approval 물리 계약):
  - `ModelProposalSchemaProfile`은 JSON Schema 제약 출력(Constrained Decoding)을 강제하여 포맷 환각 원천 차단.
  - 모델 호스트로 전송되기 전 모든 증거 바이트는 `internal/secret` 단일 게이트를 통과하여 마스킹. 모델 제안은 사용자의 명시적 `SemanticApproval` 이벤트가 기록되기 전까지 비공식(proposal) 상태로 유지되며 구조적 팩트를 대체하지 않음 (`INV-03`).
