# 요청 흐름 이해와 실시간 Semantic Compiler 통합

- Contract ID: `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER`
- Contract Status: Approved
- Intent Status: Hardened
- Slice Set Status: Exactly 10 vertical slices; VS-01 implemented and verified; VS-02–VS-10 remain proposed; independent `gpt-5.6-terra` high-reasoning contract review passed
- Created: 2026-09-02
- Source: `docs/design/raw/requested-flow-live-semantic-compiler-architecture-draft-ko.md`
- Source Authority: Raw Specification is canonical
- Decision Records: `docs/design/decisions/requested-flow-live-semantic-compiler-decisions-ko.md`
- Glossary: Section 7 and raw specification Section 3
- Supersedes: `docs/design/specs/2026-09-01-semantic-map-layered-architecture-ko.md`
- Historical Child Superseded: `docs/design/specs/2026-09-01-adapter-protocol-json-rpc-v2-migration-ko.md`

이 파일은 정규 요구사항을 다시 복사한 부모 명세가 아니다. 사용자가 승인한 raw 스팩을 유일한 정본으로 두고, Codify의 상태·식별자·traceability와 Vertical Slice 목록만 추적한다. raw 스팩과 이 파일이 충돌하면 raw 스팩이 우선하며, VS-01 구현 상태만 이 부모 tracker에 반영한다.

## 1. Authority and Approval

- Raw authority: raw 문서의 제품 의도, 설계 결정, 데이터 계약, 실패 의미, 수용 기준과 기술 기준선.
- Approval basis: 사용자가 raw 스팩을 여러 차례 리뷰를 거친 승인된 스팩으로 지정했고, `D1–D32` 결정 기록의 `Open Decisions: 없음`을 확인했다.
- Normalization exception: 이 부모 파일은 상태 추적과 glossary, intent·goal·acceptance·slice 연결만 보유한다. raw 요구사항의 대체 요약본으로 사용하지 않는다.
- Production code or schema changed: VS-01, VS-02 and VS-03.
- Implementation authorized: VS-01 (completed), VS-02 (completed) and VS-03 (in progress) by explicit user Deliver request. VS-04–VS-10 remain unauthorized.

## 2. Intent Registry

| ID | Parent intent | Raw authority |
|---|---|---|
| `INT-01` | 사용자가 요청한 흐름을 현재 코드 근거, 핵심 행동, 결과와 unknown을 포함한 이해 가능한 결과로 파악한다. | Raw §0.2 I1, §1, §2.1 G1/G4/G8/G10 |
| `INT-02` | coding agent의 구현 변경이 현재 요청 흐름의 의미를 어떻게 바꾸는지 최신성·근거·차이를 포함해 계속 파악한다. | Raw §0.2 I2, §1, §2.1 G2/G3/G5/G6/G7 |

## 3. Goal Registry

각 goal은 하나의 primary user 또는 caller outcome을 식별한다. 세부 요구사항은 raw의 acceptance와 해당 Vertical Slice가 권위로 참조한다.

| ID | Primary intent | Observable goal | Raw phase |
|---|---|---|---|
| `GOAL-01` | `INT-01` | 사용자는 자연어 feature 요청으로 시작점부터 결과까지의 deterministic 흐름과 근거·unknown을 확인한다. | P1 |
| `GOAL-02` | `INT-01` | Core caller는 지원 adapter에서 같은 snapshot 기준의 구조적 분석 근거와 typed failure를 받는다. | P2 |
| `GOAL-03` | `INT-02` | 사용자는 coding agent 또는 IDE의 편집이 불변 revision·snapshot으로 수집되고 현재 분석 활동으로 표시되는 것을 확인한다. | P2 |
| `GOAL-04` | `INT-02` | 관련 편집 뒤 사용자는 current 검증 결과 또는 명시적 latest-vs-verified gap을 보며 서로 다른 generation이 섞이지 않았음을 확인한다. | P2 |
| `GOAL-05` | `INT-02` | 사용자는 baseline과 current 사이의 added·changed·removed 행동과 요구사항 정렬 상태를 확인한다. | P3 |
| `GOAL-06` | `INT-01` | 사용자는 변경 symbol 또는 change batch의 제한된 caller·state·external effect·test 영향을 확인한다. | P3 |
| `GOAL-07` | `INT-01` | 사용자는 오류·장애의 확인된 경로, runtime 관찰 범위와 미확인 복구 경로를 구분해 조사한다. | P4 |
| `GOAL-08` | `INT-01` | 사용자는 Evidence에 근거한 의미 제안을 deterministic 사실과 구분해 승인·수정 후 승인·거절한다. | P5 |
| `GOAL-09` | `INT-01` | 사용자는 전체 graph를 펼치지 않고 domain 책임, 대표 흐름, glossary와 근거를 단계적으로 탐색한다. | P5 |
| `GOAL-10` | `INT-02` | maintainer는 reference trace와 보안·복원력·성능 evidence로 capability를 선언하고 미검증 기능을 GA로 표시하지 않는다. | P6 |

## 4. Parent Acceptance Registry

`A1–A28`의 전문은 raw §20에 있으며 이 표는 child trace만 관리한다.

| Parent acceptance | Child coverage |
|---|---|
| `A1–A4` | VS-02 |
| `A5` | VS-03 activity acknowledgement P95 ≤300ms |
| `A6` | VS-03 immutable revision and workspace snapshot |
| `A7` | VS-04 single-edit current/gap P95 ≤3s |
| `A8` | VS-04 continuous-edit checkpoint and gap P95 ≤3s |
| `A9` | VS-01 produces adapter closure evidence; VS-03 enforces snapshot-only reads; VS-04 consumes the closure for currentness |
| `A10–A11` | VS-04 |
| `A12–A14` | VS-05, VS-08 |
| `A15` | VS-02 deterministic flow, VS-05 Semantic Delta, VS-08 timeout/enrichment state |
| `A16–A17` | VS-02, VS-05, VS-06, VS-07, VS-09 |
| `A18` | VS-05 |
| `A19` | VS-06 |
| `A20–A21` | VS-07 |
| `A22` | VS-09 |
| `A23` | VS-04, VS-05, VS-06, VS-07, VS-08, VS-09, VS-10 |
| `A24` | VS-04, VS-08, VS-10 |
| `A25` | All slices as inherited invariant |
| `A26` | All slices as inherited invariant |
| `A27` | All slices at their first canonical payload boundary |
| `A28` | VS-04, VS-08, VS-10 |

## 5. Vertical Slice Plan

### P0 Exit Gate (not an additional slice)

The raw P0 phase must exit before any P1 or P2 child is approved or implemented. The gate is tracked here so the slice set remains exactly ten slices.

- `P0-GATE-01`: Contract Registry draft and six mode-specific Task View Query contracts, including typed preconditions and errors.
- `P0-GATE-02`: UX/state prototype that distinguishes requested flow, current flow, change meaning and unknown without requiring model enrichment.
- `P0-GATE-03`: schema boundary draft for the first canonical payloads, with registry ownership and producer-consumer handoff identified.
- Exit evidence: a reviewer can submit each query variant, observe the intended state distinctions, and identify the first canonical payload/schema owner. The gate does not claim production implementation.
- Downstream rule: VS-02 consumes this gate as its P1 entry condition. VS-01, VS-03 and VS-04 remain P2 work and cannot use an absent P0 gate as a reason to waive their contract checks.

| Slice | Primary goal | Dependencies | Parent acceptance | Status |
|---|---|---|---|---|
| VS-02 | `GOAL-01` | `P0 Exit Gate` and an available structural evidence seam | `A1–A4`, `A15`, `A16–A17`, `A25–A27` | Implemented, verification passing, independent review passed |
| VS-01 | `GOAL-02` | `P0 Exit Gate`, VS-02 baseline seam | `A9`, `A25–A27` and raw §21.8 | Implemented, verification passing, independent review passed |
| VS-03 | `GOAL-03` | `P0 Exit Gate`, VS-02 baseline seam, VS-01 snapshot-capable adapter boundary | `A5–A6`, `A9`, `A11`, `A25–A27` | Implemented, verification passing, independent review passed |
| VS-04 | `GOAL-04` | VS-02, VS-03 | `A7–A11`, `A23–A28` | Implemented, verification passing, independent review passed |
| VS-05 | `GOAL-05` | VS-02, VS-04 | `A12–A16`, `A18`, `A23`, `A25–A27` | Implemented, verification passing, independent review passed |
| VS-06 | `GOAL-06` | VS-02, VS-04 | `A16`, `A19`, `A23`, `A25–A27` | Implemented, verification passing, independent review passed |
| VS-07 | `GOAL-07` | VS-02, VS-04 | `A16`, `A20–A21`, `A23`, `A25–A27` | Implemented, verification passing, independent review passed |
| VS-08 | `GOAL-08` | VS-02, VS-04 | `A12–A15`, `A23–A25`, `A27–A28` | Implemented, verification passing, independent review passed |
| VS-09 | `GOAL-09` | VS-02, VS-04, VS-08 | `A16`, `A22–A23`, `A25–A27` | Implemented, verification passing, independent review passed |
| VS-10 | `GOAL-10` | VS-01–VS-09 as applicable | `A16`, `A23–A28` and raw §16–§18 | Implemented, verification passing, independent review passed |

This ordering follows the raw P0 → P1 → P2 → P3 → P4 → P5 → P6 phases. VS-01 is the only slice approved and implemented under the current request. VS-02–VS-10 remain proposed and are not authorized.

### 5.1 Slice 시작 공통 결정 Gate

각 Vertical Slice는 production 구현을 시작하기 전에 아래 항목을 검토하고 결정 결과와 근거를 해당 child specification의 `Open Decisions` 또는 후속 `Resolved Implementation Decisions`에 기록해야 한다. 이 Gate는 raw D1–D32와 A1–A28을 구체화하며 새로운 제품 요구사항을 만들지 않는다.

| ID | 시작 전에 결정할 사항 | 필수 결과 |
|---|---|---|
| `SID-C1` | Slice가 처음 생산하거나 소비하는 정규 payload의 물리 구성 | 독립 schema 파일 또는 부모 schema `$defs`, `schemaId`, `schemaVersion`, Registry 경로와 migration 필요 여부 |
| `SID-C2` | structural validation과 Semantic Validator의 책임 경계 | JSON Schema 검증 조건, cross-artifact·basis·authority·CAS 불변 조건과 typed failure |
| `SID-C3` | 계약 검증 범위 | valid·invalid fixture, producer-consumer compatibility test, replay·idempotency 적용 여부와 실행 command |
| `SID-C4` | 기존 구현의 재사용 또는 교체 | 후보 component, contract·SLO·failure behavior 검증 결과, 재사용·보강·교체 판단과 근거 |
| `SID-C5` | capability와 운영 한계 | 지원 language/framework/toolchain 범위, message·queue·concurrency·timeout·resource bound와 초과 시 fallback |
| `SID-C6` | 해당 mode의 Settlement Gate 입력 | `required=true` Critical Obligation 산출 규칙, verified 조건, critical unknown·conflict 판정 fixture |

적용 규칙:

1. `SID-C1`–`SID-C3`은 해당 payload를 사용하는 production code보다 먼저 완료한다.
2. `SID-C4`의 재사용률은 성공 지표가 아니다. 승인된 계약을 그대로 만족하는지가 판단 기준이다.
3. `SID-C5`의 수치는 correctness, Evidence, current publication 또는 settlement gate를 약화할 수 없다.
4. 결정이 기존 D1–D32, A1–A28, 보안 경계, 지원 범위 또는 release 약속을 변경하면 구현 결정을 중단하고 Parent amendment와 사용자 승인을 받는다.
5. 그 외의 schema 배치, 내부 자료구조, library 선택과 측정 기반 tuning은 child specification에 근거를 기록한 뒤 구현자가 결정한다.

### 5.2 Slice별 시작 결정 체크리스트

아래 항목은 `SID-C1`–`SID-C6`에 추가하여 각 Slice 시작 시 검토한다. 이미 구현된 Slice도 후속 변경이나 migration을 시작할 때 동일하게 재검토한다.

| Slice | ID | Slice 시작 시 결정할 사항 |
|---|---|---|
| VS-01 | `SID-01` | Dart·TypeScript/JavaScript·Go별 capability matrix와 analyzer/toolchain pin, adapter 재사용 여부, max message size·in-flight work·backpressure 한도 |
| VS-02 | `SID-02` | feature entry 후보 resolution·동순위 ambiguity 규칙의 구현과 fixture, TaskIntent·TaskViewQuery·SemanticMapIR·FlowViewProjection·Evidence 계약 구성, feature Critical Obligation |
| VS-03 | `SID-03` | DocumentRevision·WorkspaceSnapshot의 물리 저장·retention·GC, watcher backend, capture retry·reconciliation 주기, multi-file transaction 내부 표현 |
| VS-04 | `SID-04` | closure 교차 판정 구조, publication queue와 load-shedding 한도, Generation Proof Manifest·active pointer CAS transaction 구조, EventEnvelope 보존과 reconnect snapshot 전환 기준 |
| VS-05 | `SID-05` | stable step·claim identity matching, rename·move 판정, Semantic Delta 분류 fixture, Requirement Alignment validator와 review Critical Obligation |
| VS-06 | `SID-06` | 직접·한 단계 간접·추가 탐색의 실행 예산과 최대 확장량, unresolved dynamic caller 경계, impact Critical Obligation |
| VS-07 | `SID-07` | trace provider와 static/runtime correlation, 승인된 synthetic scenario, sandbox·resource limit 구현, debug·incident Critical Obligation |
| VS-08 | `SID-08` | `ModelProposalSchemaProfile` 지원 범위, Evidence Pack 구성·redaction, model host protocol과 한도, SemanticApproval event의 물리 계약 |
| VS-09 | `SID-09` | domain·ownership mining 신호, representative flow ranking과 selection Evidence, low-coverage 표시 기준, 대형 overview renderer, onboarding Critical Obligation |
| VS-10 | `SID-10` | release별 지원 OS·hardware·repository 규모·language/framework·부하·browser 범위, versioned benchmark corpus, critical semantic closure 목표, 분리된 품질 통과 기준, 기본 Local SLM 후보와 capability state |

`SID-07`에서 runtime의 source·credential·network 접근 범위를 확대하거나, `SID-10`에서 GA 지원 범위·품질 기준·기본 배포 모델을 확정하는 결정은 제품 및 보안 경계를 정하므로 사용자 승인을 요구한다. 나머지 항목은 승인된 raw 계약을 변경하지 않는 범위에서 구현 결정으로 처리한다.

## 6. Invariant Registry

The IDs below are trace labels. Their normative wording remains in the raw document and the accepted decision record.

| ID | Trace label | Raw authority |
|---|---|---|
| `INV-01` | Current implementation facts require current source and validated analyzer evidence. | Raw §3.16, §6.3, §11 |
| `INV-02` | Analysis scope is limited by Task Intent or explicit task query. | Raw §3.1, §6.1, §8 |
| `INV-03` | Published claims require evidence grounding and authority separation. | Raw §10.7–§10.9, §11 |
| `INV-04` | Unknown and unresolved relations remain explicit. | Raw §3.14, §6.3, §15 |
| `INV-05` | Precision and coverage are separate, with deterministic fallback. | Raw §6.4, §18.3–§18.6 |
| `INV-06` | Runtime evidence is bounded by its scenario and isolation scope. | Raw §11.5, §14–§15 |
| `INV-07` | Hot-path analysis reads one immutable workspace snapshot. | Raw §7.1, §21.5 |
| `INV-08` | Currentness requires closed causal observation, not read-set equality alone. | Raw §3.9–§3.10, §7.4 |
| `INV-09` | Canonical artifacts in a generation share basis, generation and schema identity. | Raw §10.10–§10.11, §13.1 |
| `INV-10` | Active publication is atomic and stale results cannot replace the active pointer. | Raw §6.9, §7.7, §13.2 |
| `INV-11` | Activity, freshness, quality, settlement, enrichment and connection are separate axes. | Raw §7.3, §9.13 |
| `INV-12` | SemanticMapIR preserves the complete flow and projection uses a soft display budget. | Raw §8.2, §9.7, §10.10, D32 |
| `INV-13` | Mode-specific query preconditions are validated as typed variants. | Raw §8.2 |
| `INV-14` | Adapter evidence includes basis, read set, negative lookup, membership and frontier. | Raw §7.1, §10.3–§10.5 |
| `INV-15` | Source is read-only and sensitive evidence is redacted before optional model or UX exposure. | Raw §14, §18.6 |
| `INV-16` | Event ordering, replay and mutation idempotency are explicit contracts. | Raw §10.14, §21.8, A28 |
| `INV-17` | The adapter boundary uses the raw protocol contract and does not mix incompatible protocol versions. | Raw §21.8, D18, D25–D26 |
| `INV-18` | Runtime isolation level and trusted-local approval are visible. | Raw §14, §18.6 R15 |
| `INV-19` | Canonical boundary payloads use registered JSON Schema and semantic validation. | Raw §10.0, §20 A27 |
| `INV-20` | Dynamic-language capability is bounded by measured supported subsets. | Raw §21.5, §21.10, §20 A16 |
| `INV-21` | P95 freshness SLO does not override correctness or evidence gates. | Raw §7.3, §16, §18.4 R5 |
| `INV-22` | Raw intent, normalized intent, confirmation and requirement alignment have separate lifecycles. | Raw §3.1, §6.2 C1, §10.1, D29 |
| `INV-23` | Degradation is represented by cause and impact, not a single status enum. | Raw §9.13, §10.10, §15, D30 |
| `INV-24` | Settlement passes only when all required Critical Obligations are verified with no critical unknown or conflict. | Raw §3.17, §10.10–§10.11, §18.1, D31 |
| `INV-25` | Projection cannot remove preserved critical boundaries from the canonical IR. | Raw §8.2, §9.7, §10.10, D32 |

## 7. Glossary Pointer

The raw glossary in §3 is normative. This compact registry only keeps the terms required to interpret the slices.

| Term | Meaning for slice tracing | Canonical source |
|---|---|---|
| Task Intent | Versioned user purpose, expected outcome, acceptance criteria and scope hint. | Raw §3.1, §10.1 |
| Requested Flow | Task Intent-selected flow from entry through decisions, effects and result. | Raw §3.2 |
| Current Implementation Flow | Evidence-confirmed implementation of the requested flow for a generation. | Raw §3.3 |
| Semantic Compiler | Snapshot, structural, scope, semantic, validation, delta and publication pipeline. | Raw §3.4, §6 |
| SemanticMapIR | Complete canonical flow and evidence model. | Raw §3.5, §10.10 |
| FlowViewProjection | Task-specific display projection that does not delete canonical facts. | Raw §10.10, D32 |
| Semantic Delta | User-understandable behavior difference between generations. | Raw §3.6, §10.12 |
| Document Revision | Immutable bytes for one path and document version. | Raw §3.7, §10.2 |
| Workspace Snapshot | Immutable repository/worktree view used as analysis basis. | Raw §3.8, §10.2 |
| Analysis Read Set | Inputs actually read by an analysis. | Raw §3.9, §10.3 |
| Causal Observation Closure | Closed positive, negative, membership and frontier observations for currentness proof. | Raw §3.10, §10.5 |
| Generation Proof Manifest | Publication proof connecting artifacts, basis, validation and active pointer conditions. | Raw §3.11, §10.11 |
| Evidence Anchor | Stable source, test, contract or runtime location tied to a revision. | Raw §3.13, §10.7 |
| Unknown | Fact or meaning not currently confirmable within declared coverage. | Raw §3.14, §15 |
| Requirement Alignment | Relation between an acceptance criterion and current Evidence. | Raw §3.15, §10.13 |
| Critical Obligation | Mode-specific required item that must be verified before settlement passes. | Raw §3.17, §10.16 |

## 8. Active Contract and Retirement Rules

- Exactly this file is the active normalized parent tracker for this feature scope.
- `2026-09-01-semantic-map-layered-architecture-ko.md` and `2026-09-01-adapter-protocol-json-rpc-v2-migration-ko.md` are historical and point to their replacements.
- The two raw predecessor documents remain preserved because the current raw spec still lists them as source material. They are not active contracts.
- No new parent may be created for the same raw source, feature goal, public seam or materially overlapping scope.
- A substantive change to the raw authority requires an amendment, updated decision record and renewed slice review.

## 9. Open Decisions and Done When

- Blocking Open Decisions: None, according to the accepted decision record and the user's approval of the raw source.
- Independent Contract Reviewer: the requested `gpt-5.6-terra` with high reasoning returned `PASS` after one correction cycle. All ten children are independently reviewed. VS-01 is implemented, and VS-02–VS-10 remain `Proposed`.
- This slicing task is done when VS-01 through VS-10 each contain intent traceability, one observable outcome, acceptance criteria, failure semantics and a complete Verification Plan, and the complete set passes independent Contract Review.
- This parent tracker does not include implementation details for VS-01 and does not authorize production implementation, test execution, or release declaration for VS-02–VS-10.
