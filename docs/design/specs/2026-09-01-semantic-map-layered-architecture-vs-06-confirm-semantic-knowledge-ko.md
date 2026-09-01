# Semantic Map Layered Architecture — VS-06 의미 지식 확정

- Contract ID: `SMAP-VS-06`
- Contract Status: Proposed
- Independent Review: Passed
- Parent Contract: `docs/design/specs/2026-09-01-semantic-map-layered-architecture-ko.md`
- Created: 2026-09-01
- Depends On: SMAP-VS-02; SMAP-VS-05는 inferred model proposal을 승인할 때만 필요
- Parent Acceptance Coverage: FA-04, FA-08, FA-10

## 1. User Outcome

개발자는 근거가 있는 semantic label과 rule을 승인, 수정 후 승인, 거절 또는 근거 부족으로 처리하고, 그 결정이 구조 Fact를 바꾸지 않은 채 current basis와 함께 다음 valid generation에 보존되는 것을 확인할 수 있다.

## 2. Scope

### In Scope

- Session draft, deterministic semantic candidate 또는 validated model proposal 검토
- 승인, 수정 후 승인, 거절과 근거 부족 처리
- Current basis와 Evidence Anchor validation
- Append-only Semantic Overlay Ledger
- `confirmed > inferred` authority와 generation materialization
- Code change 뒤 stale·orphaned overlay 처리
- FlowView와 MCP approval seam의 authorization과 저장 실패 의미
- 기존 `session_draft_submitted`, `step_approved`, `step_rejected` ledger event의 versioned replay compatibility

### Out of Scope

- 구조 Fact, branch, target 또는 source range 편집 — Analysis Layer 권위를 위반한다.
- Invalid model proposal 승인 — VS-05 validator를 우회할 수 없다.
- 여러 사용자의 중앙 동시 편집과 remote collaboration server — 부모 MVP Non-Goal이다.
- Approval event의 history rewrite 또는 삭제 — append-only 규칙을 위반한다.

## 3. Preconditions

- SMAP-VS-02의 current AnalysisSnapshot, SemanticMapIR과 Evidence Anchor가 존재한다.
- 승인 대상은 current basis와 existing Fact 또는 node reference를 가진다.
- FlowView semantic mutation은 per-run token을, token-required MCP server의 semantic mutation은 valid MCP session token을 제공한다.

## 4. Public Seam

- FlowView `POST /api/semantic-decisions`의 `approve`, `amend_and_approve`, `reject`, `insufficient_evidence` action과 per-run token
- MCP `submit_semantic_decision`의 같은 versioned action contract와 authenticated MCP session principal
- 기존 FlowView `POST /api/approve`와 MCP `approve_step`은 bridge 기간에 `approve` action의 compatibility alias로 유지하며 decision ID, accepted ledger revision과 deprecation notice를 반환한다.
- MCP `submit_flow_draft`는 proposal 제출로 유지하고 semantic decision 완료로 간주하지 않는다.
- 새 generation의 confirmed·inferred·unknown, decision status와 freshness 표시

Caller는 append-only event result와 materialized semantic status를 관찰하며 ledger 내부 저장 구현은 알 필요가 없다.

## 5. Boundary Coverage

FlowView 또는 MCP semantic decision → authorization·basis·Fact·evidence validation → append-only event write → versioned ledger replay와 overlay materialization → SemanticMapIR 재생성 → complete generation publish → confirmed·rejected·insufficient-evidence·stale·orphaned 상태 표시

## 6. Inherited Invariants

- INV-01 Current-State Authority
- INV-02 Fact Ownership
- INV-03 Evidence Grounding
- INV-04 Unknown Preservation
- INV-07 Semantic Authority
- INV-09 Generation Consistency
- INV-10 Stale Isolation
- INV-13 Evidence Navigation
- INV-15 Approval Durability

## 7. Slice-Specific Rules

- BR-01: Semantic decision event의 author와 opaque principal reference는 인증된 FlowView 또는 MCP principal에서 server가 결정한다. Caller가 제공한 author 문자열을 audit authority로 신뢰하지 않으며 raw authorization token은 저장하지 않는다.
- BR-02: 승인과 수정 후 승인은 `confirmed`, 검증된 미승인 proposal은 `inferred`, 근거 부족은 `unknown`으로 materialize한다.
- BR-03: Rejection은 해당 proposal을 confirmed knowledge로 승격하지 않으며 구조 Fact를 삭제하지 않는다.
- BR-04: Approval event는 append-only이며 기존 event를 수정하거나 history에서 제거하지 않는다.
- BR-05: Current basis와 맞지 않거나 evidence가 invalid인 approval request는 저장하지 않는다.
- BR-06: 저장 실패 시 UI와 caller는 approval 완료를 관찰하면 안 된다.
- BR-07: Code change로 evidence가 바뀌면 overlay를 `stale`, target Fact가 사라지면 `orphaned`로 표시한다.
- BR-08: Legacy `session_draft_submitted`는 `inferred/session`, `step_approved`는 `approved` authority, `step_rejected`는 rejected decision과 overlay 제거로 replay하되 basis와 evidence가 검증되기 전 freshness는 `stale`이다.
- BR-09: Legacy approved event는 current stable Fact와 valid Evidence Anchor가 확인된 경우에만 `confirmed/fresh`로 materialize한다. Fact가 바뀌거나 anchor가 invalid이면 `stale`, target이 없으면 `orphaned`이며 current confirmed overlay에서 제외한다.
- BR-10: Existing event bytes와 order는 rewrite하지 않으며 새 event schema version과 replay mapping으로 compatibility를 제공한다.
- BR-11: Legacy 또는 current event replay가 실패하면 새 materialized view와 generation을 발행하지 않고 기존 ledger와 active generation을 보존한다.
- BR-12: 새 semantic mutation은 caller-generated `decisionId`와 `expectedLedgerRevision`을 요구한다.
- BR-13: Canonical decision payload는 authorization token과 `expectedLedgerRevision`을 제외한 action, target, basis와 semantic content로 계산한다. Idempotency lookup은 revision 비교보다 먼저 수행한다. 같은 `decisionId`와 같은 canonical payload의 retry는 최초 저장 결과를 반환하고 event를 추가하지 않으며, 같은 `decisionId`의 다른 payload는 idempotency conflict다.
- BR-14: `expectedLedgerRevision`은 전역 canonical ledger revision이다. 서로 다른 decision이 같은 revision을 기준으로 경쟁하면 하나만 다음 event로 저장할 수 있고 나머지는 revision conflict를 반환한다. 이후 decision은 새 current revision을 명시해야 한다.
- BR-15: Idempotency identity는 authenticated principal reference와 caller-generated `decisionId`의 조합이다. 다른 principal의 같은 raw decision ID는 기존 event result를 조회하거나 재사용할 수 없다.
- BR-16: Legacy `/api/approve`와 `approve_step` bridge는 target의 latest accepted decision lineage를 먼저 확인한다. 같은 principal, alias kind, basis와 canonical payload의 stored decision이 target의 latest decision이면 그 결과를 retry로 반환한다. 그렇지 않으면 principal, alias kind, current basis, canonical payload digest와 current target-decision revision으로 새 deterministic decision ID를 생성하고 request admission 시 current global ledger revision을 사용한다.
- BR-17: Legacy alias bridge는 automatic revision retry를 수행하지 않는다. Revision conflict를 caller에 반환하며 성공 또는 idempotent retry에는 decision ID, accepted revision과 deprecation notice를 반환한다.

## 8. Acceptance Criteria

- A1. WHEN 사용자가 current evidence가 있는 semantic label 또는 rule을 승인하면, THE system SHALL append-only approval event를 저장하고 다음 valid generation에서 해당 의미를 `confirmed`로 표시한다.
- A2. WHEN 사용자가 label 또는 rule을 수정 후 승인하면, THE system SHALL 수정 내용과 원래 proposal reference를 새 event에 기록하고 수정된 의미만 `confirmed`로 표시한다.
- A3. WHEN 사용자가 proposal을 거절하면, THE system SHALL rejection event를 저장하고 다음 valid generation에서 decision status를 `rejected`로 관찰 가능하게 하며 해당 proposal을 inferred 또는 confirmed overlay에 포함하지 않는다.
- A4. WHEN 사용자가 근거 부족으로 처리하면, THE system SHALL `insufficient_evidence` event와 필요한 evidence를 저장하고 다음 valid generation에서 semantic status를 `unknown`으로 표시한다.
- A5. IF semantic decision request의 basis, Fact, node 또는 evidence reference가 current artifact에서 유효하지 않으면, THEN THE system SHALL request를 거부하고 ledger와 구조 Fact를 변경하지 않는다.
- A6. IF semantic decision event 저장이 실패하면, THEN THE system SHALL UI와 caller에 decision 완료를 표시하지 않고 기존 ledger와 active generation을 유지한다.
- A7. THE system SHALL approve, amend-and-approve, reject 또는 insufficient-evidence action으로 call, branch, mutation, state transition, target과 source range를 변경하지 않는다.
- A8. WHEN approved evidence가 code change로 invalid해지면, THE system SHALL overlay를 `stale` 또는 `orphaned`로 표시하고 fresh confirmed claim으로 유지하지 않는다.
- A9. THE system SHALL confirmed, inferred와 unknown semantic status를 decision status, structural status와 freshness에서 독립적으로 표시한다.
- A10. IF FlowView 또는 token-required MCP semantic mutation의 authorization token이 없거나 일치하지 않으면, THEN THE system SHALL unauthorized result를 반환하고 ledger와 active generation을 변경하지 않는다.
- A11. WHEN 기존 ledger를 replay하면, THE system SHALL `session_draft_submitted`, `step_approved`, `step_rejected`의 authority와 decision status를 보존하되 basis와 evidence가 검증되지 않은 의미를 `stale` 상태로 materialize하고 기존 event bytes와 order를 보존한다.
- A12. IF legacy 또는 current ledger replay가 실패하면, THEN THE system SHALL 새 materialized overlay를 발행하지 않고 기존 ledger와 active generation을 유지한다.
- A13. WHEN legacy approved event의 target이 current stable Fact와 valid Evidence Anchor에 연결되면, THE system SHALL 해당 의미를 `confirmed/fresh`로 materialize한다.
- A14. IF legacy event의 target Fact가 변경, 삭제되거나 Evidence Anchor가 invalid이면, THEN THE system SHALL 해당 의미를 각각 `stale` 또는 `orphaned`로 표시하고 current confirmed overlay에 포함하지 않는다.
- A15. WHEN caller가 같은 `decisionId`와 같은 canonical payload를 재시도하면, THE system SHALL 최초 저장 결과와 revision을 반환하고 ledger에 event를 추가하지 않는다.
- A16. IF caller가 기존 `decisionId`에 다른 canonical payload를 제출하면, THEN THE system SHALL idempotency conflict를 반환하고 ledger와 active generation을 변경하지 않는다.
- A17. WHEN target과 관계없이 서로 다른 semantic decision이 같은 global `expectedLedgerRevision`으로 동시에 제출되면, THE system SHALL 하나만 다음 ledger revision으로 저장하고 나머지에 revision conflict를 반환한다.
- A18. IF event 저장 후 response 전달이 실패하고 caller가 같은 decision을 재시도하면, THEN THE system SHALL 저장된 event의 결과를 반환하고 duplicate event 또는 partial generation을 만들지 않는다.
- A19. WHEN caller가 legacy `/api/approve` 또는 `approve_step`으로 새 intent를 제출하면, THE system SHALL authenticated principal, alias kind, current basis, canonical legacy payload와 current target-decision revision에서 deterministic decision ID를 생성하고 성공 response에 decision ID, accepted ledger revision과 deprecation notice를 반환한다.
- A20. WHEN 같은 principal이 같은 basis와 canonical payload로 legacy alias를 재시도하고 matching stored decision이 target의 latest accepted decision이면, THE system SHALL 기존 decision result를 반환하고 duplicate event를 만들지 않는다.
- A21. IF legacy alias가 stale global revision에서 경쟁하면, THEN THE system SHALL automatic retry 없이 revision conflict와 current revision을 반환하고 losing event를 저장하지 않는다.
- A22. IF semantic mutation payload가 authenticated principal과 다른 author identity를 주장하면, THEN THE system SHALL request를 거부하고 ledger와 active generation을 변경하지 않는다.
- A23. IF 다른 principal이 같은 raw `decisionId`를 사용하면, THEN THE system SHALL 이전 principal의 decision result 또는 audit content를 반환하지 않는다.
- A24. WHEN legacy approve 뒤에 같은 target의 reject 또는 changed decision이 accepted되고 caller가 이전과 동일한 legacy approve payload를 다시 제출하면, THE system SHALL intervening target-decision revision을 포함한 새 decision ID로 새 approve intent를 저장하고 latest materialized state에 반영한다.

## 9. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| Authorization token 없음 또는 불일치 | Unauthorized response | Ledger와 generation 변경 없음 | Valid session에서 재요청 |
| Stale basis 또는 invalid evidence | Conflict 또는 validation failure | Event 저장 없음 | Current evidence에서 다시 검토 |
| Ledger append 실패 | Semantic decision failed | 기존 event와 active generation 유지 | Storage 복구 후 동일 intent 재제출 |
| Materialization 실패 | 이전 valid generation과 pending event 상태 | 구조 Fact 변경 없음 | Event replay 또는 projector 복구 |
| Legacy event replay 실패 | Compatibility failure와 실패 event 위치 | 기존 ledger와 active generation 유지 | Replay mapping 또는 invalid event 복구 |
| 같은 decision ID, 다른 payload | Idempotency conflict | Event와 generation 변경 없음 | 새 decision ID와 current revision으로 명시적 재제출 |
| Expected ledger revision 불일치 | Revision conflict와 current revision | Losing event 저장과 partial generation 없음 | Current state를 다시 읽고 새 decision으로 제출 |
| Event 저장 후 response 손실 | Retry에서 기존 event result 반환 | Duplicate event 없음 | 같은 decision ID와 payload로 재시도 |
| Legacy alias revision 경쟁 | Revision conflict, current revision과 deprecation notice | Automatic retry와 losing event 없음 | Current state 확인 후 versioned semantic-decision API 사용 |
| Author principal 불일치 | Unauthorized 또는 invalid-author response | Event와 generation 변경 없음 | Authenticated principal identity 사용 |
| Target Fact 삭제 | Orphaned overlay 표시 | History 삭제 없음 | 새 Fact에 대한 별도 승인 |

## 10. Data and Interaction Contract

- Input: schema version, caller-generated decision ID, expected ledger revision, action `approve | amend_and_approve | reject | insufficient_evidence`, flow·node·Fact refs, current basis, proposed·final label과 rules, evidence와 required-evidence refs. Author는 caller input이 아니다.
- Event: immutable event ID, principal-scoped decision ID, authenticated principal reference, server-derived author, global ledger revision, predecessor target-decision revision, canonical payload digest, schema version, action, timestamp, basis, target refs, proposal ref, final semantic content, required evidence와 validation result
- Legacy replay: `session_draft_submitted → inferred/session/stale`, `step_approved → approved authority/stale`, `step_rejected → rejected/no overlay`. Current Fact와 anchor validation이 성공한 경우에만 BR-09에 따라 freshness를 승격하며 기존 event를 rewrite하지 않는다.
- Materialized overlay: target ref, semantic status, decision status, authority, freshness, supporting event와 evidence refs
- Persistence: JSONL append-only ledger가 canonical이며 materialized view는 ledger에서 재생성할 수 있다.
- Idempotency: authenticated principal reference, decision ID와 canonical payload digest의 저장 결과가 retry authority이며 전역 ledger revision은 accepted event마다 단조 증가한다.
- Authorization: FlowView mutation은 per-run token, token-required MCP mutation은 MCP session token을 요구하며 source navigation read와 분리한다.
- Compatibility: `/api/approve`와 `approve_step`은 BR-16의 latest-target-decision retry check와 lineage-based identity로 `approve`를 제출한다. 두 alias는 automatic conflict retry를 하지 않고 decision ID, accepted revision과 deprecation notice를 반환하며, 종료 조건은 consumer support와 migration evidence를 요구한다.

## 11. Test Seam and Evidence

- Public seam: MCP approval tools, FlowView approval API, ledger replay와 published generation
- Required test level: HTTP·MCP authorization, event-log integration, legacy freshness replay compatibility, idempotent retry, optimistic concurrency, storage failure, stale/orphaned identity와 end-to-end FlowView status test
- Replaceable external boundaries: clock, author identity provider, filesystem append와 generation publisher
- Evidence required per criterion:
  - A1, A2, A3, A4: action별 event record와 materialized semantic·decision status assertions
  - A5, A6, A10: ledger byte-for-byte unchanged 및 pointer preservation
  - A7: AnalysisSnapshot·Fact equality assertion
  - A8, A9: code-change fixture와 independent status rendering
  - A11, A12, A13, A14: unchanged, changed, missing-target와 invalid-anchor legacy/current mixed ledger replay fixture 및 failure preservation
  - A15, A16, A17, A18: ledger event count, global revision, payload digest, retry result와 pointer preservation
  - A19, A20, A21, A24: HTTP·MCP legacy alias deterministic ID, latest-lineage retry, approve→reject/change→identical re-approve, deprecation과 conflict fixture
  - A22, A23: principal spoofing과 cross-principal result non-disclosure assertion

## 12. What Could Be Wrong

- Assumption: Current stable Fact·node identity로 승인 지식을 code change 뒤에 안전하게 relink할 수 있다.
- Consequence: 유효한 승인이 orphaned되거나 오래된 승인이 다른 Fact에 잘못 연결된다.
- Validation method: line shift, rename, extraction, deletion과 conflicting evidence fixture에서 relink·stale·orphaned를 검증한다.

## 13. Done When

- Every criterion has passing evidence.
- 승인, 수정, 거절, 근거 부족, invalid basis, idempotent retry, concurrent conflict, append failure, legacy replay와 stale·orphaned test가 통과한다.
- `go test ./...`와 FlowView·MCP authorization 검증이 통과한다.
- Ledger replay가 같은 materialized overlay를 생성한다.
- No contract requirement is weakened.
- No unrelated behavior changes.

## 14. Open Decisions

없음.
