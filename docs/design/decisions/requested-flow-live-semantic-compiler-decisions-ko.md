# 요청 흐름 이해와 실시간 Semantic Compiler 통합 결정 기록

- Record Status: Accepted
- Created: 2026-09-02
- Parent Contract: `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md`
- Source: `docs/design/raw/requested-flow-live-semantic-compiler-architecture-draft-ko.md` Section 4
- Approval Basis: 사용자가 D1–D32를 승인된 권위로 지정함
- Open Decisions: 없음

이 파일은 사용자가 지정한 project-specific consolidated decision log다. 각 항목은 최신 Raw Spec의 결정 의미를 변경하지 않고 context, rejected alternative, rationale, consequence와 contract trace를 명시한다. 이전 `SMAP`과 `ADAPTER-PROTOCOL-V2-MIGRATION`의 결정은 historical이며 이 기록과 Parent Contract가 현재 권위다.

<a id="d1"></a>
## D1 · 사용자 이해 결과를 제품 기준으로 사용

- Status: Accepted
- Context: 분석 기능이나 내부 artifact가 존재해도 사용자가 현재 흐름을 정확히 설명하지 못하면 인지 부채가 줄지 않는다.
- Decision: 제품의 기준은 현재 구현 자체가 아니라 사용자가 Evidence와 Unknown을 포함해 얻는 이해 결과다.
- Rejected Alternative: 분석 node 수, index coverage 또는 기존 UI 유지 자체를 성공 기준으로 사용한다.
- Rationale: 제품 목적은 코드 지식 생산이 아니라 요청자와 현재 코드 사이의 이해 차이를 줄이는 것이다.
- Consequences: comprehension accuracy, time, Evidence navigation과 misunderstanding을 품질 지표로 측정한다.
- Source / Evidence: Raw Sections 0, 1, 17, 24; 사용자 승인.
- Contract Trace: INT-01, GOAL-01–GOAL-04, INV-01, A1–A4.
- Follow-up: 없음.

<a id="d2"></a>
## D2 · 분석 범위를 Task Intent 또는 명시적 query로 제한

- Status: Accepted
- Context: 전체 repository graph는 요청과 무관한 정보를 포함하고 첫 이해를 방해한다.
- Decision: 모든 분석은 Task Intent 또는 명시적 Task View Query로 범위를 제한한다.
- Rejected Alternative: repository 전체 dependency graph를 기본 분석·표시 단위로 사용한다.
- Rationale: task working set이 사용자의 질문과 Evidence를 직접 연결한다.
- Consequences: scope resolution과 ambiguity 처리가 모든 mode의 선행 조건이 된다.
- Source / Evidence: Raw Sections 2, 6.4, 8, 9.2; 사용자 승인.
- Contract Trace: INV-02, INV-09, A1, A16–A22.
- Follow-up: mode별 scope fixture를 Vertical Slice에서 정의한다.

<a id="d3"></a>
## D3 · 권위 종류를 별도 저장

- Status: Accepted
- Context: 코드, 테스트, 실행, 사용자 요청, agent 설명과 model 추론은 증명 범위가 다르다.
- Decision: 구조 Fact, 사용자 의도, agent hint, model proposal과 사람 승인을 별도 authority로 저장한다.
- Rejected Alternative: 하나의 confidence 점수나 단일 status로 모든 근거를 합친다.
- Rationale: 설명이나 승인이 검증된 코드 사실처럼 사용되는 것을 차단한다.
- Consequences: claim은 epistemic, source authority, validation과 freshness 축을 독립적으로 가진다.
- Source / Evidence: Raw Sections 3.16, 10.7–10.9, 11; 사용자 승인.
- Contract Trace: INV-03, INV-12, A13–A15.
- Follow-up: authority 조합을 Semantic Validator fixture로 정의한다.

<a id="d4"></a>
## D4 · Semantic Compiler와 model을 구분

- Status: Accepted
- Context: 의미 처리 전체를 SLM 또는 LLM으로 정의하면 deterministic analysis와 publication 책임이 사라진다.
- Decision: Semantic Compiler는 snapshot, analysis, scope, semantic, validation, delta와 publication을 포함하는 전체 체계다.
- Rejected Alternative: Semantic Compiler를 특정 model 또는 inference call의 이름으로 사용한다.
- Rationale: 모델 유무와 무관하게 제품의 핵심 흐름이 동작해야 한다.
- Consequences: model은 optional stage가 되고 Core가 pipeline과 권위를 소유한다.
- Source / Evidence: Raw Sections 3.4, 6, 22.3; 사용자 승인.
- Contract Trace: INV-04, INV-05, A15.
- Follow-up: 없음.

<a id="d5"></a>
## D5 · model의 Fact 생성 금지

- Status: Accepted
- Context: model output은 존재하지 않는 target, branch 또는 이유를 생성할 수 있다.
- Decision: model은 의미 후보만 제안하며 코드 Fact나 Evidence anchor를 생성·수정할 수 없다.
- Rejected Alternative: model 출력을 구조 graph와 Requirement Alignment의 직접 권위로 사용한다.
- Rationale: 코드 사실은 재검증 가능한 source, compiler, test, contract 또는 scoped runtime Evidence에서만 나온다.
- Consequences: model proposal은 Evidence reference와 schema·semantic validation을 모두 통과해야 한다.
- Source / Evidence: Raw Sections 2.2, 6.3, 10.8, 11.4; 사용자 승인.
- Contract Trace: INV-03, INV-05, A14, A15.
- Follow-up: model proposal violation fixture를 구현 slice에 포함한다.

<a id="d6"></a>
## D6 · 기본 사용자 결과 구성

- Status: Accepted
- Context: 파일·symbol·graph 중심 화면은 사용자의 첫 질문과 변경 의미를 직접 답하지 못한다.
- Decision: 기본 결과는 Current Answer, Semantic Flow Rail, What Changed, Requirement Alignment와 Evidence다.
- Rejected Alternative: file tree, raw diff 또는 architecture lanes를 첫 화면의 중심으로 둔다.
- Rationale: 답, 흐름, 변화, 요구와 근거 순서가 사용자의 이해 과제를 직접 지원한다.
- Consequences: architecture와 raw source는 Context Ribbon과 Evidence Dock에서 점진적으로 연다.
- Source / Evidence: Raw Sections 5.2, 9.3–9.12; 사용자 승인.
- Contract Trace: INV-01, A1–A4, A11–A13.
- Follow-up: UX comprehension study로 배치 효율을 검증한다.

<a id="d7"></a>
## D7 · 변경을 검증된 Semantic Delta로 발행

- Status: Accepted
- Context: file event와 line diff는 사용자가 이해해야 할 행동 변화를 직접 설명하지 않는다.
- Decision: coding agent 변경은 구조·의미를 구분한 검증된 Semantic Delta로 발행한다.
- Rejected Alternative: 저장 이벤트와 raw diff를 사용자 변경 feed로 그대로 표시한다.
- Rationale: 인지 부채는 파일 변화보다 행동, 조건, 상태, effect와 requirement 변화에서 발생한다.
- Consequences: structural-only change는 기본 Change Pulse에서 접고 semantic precision을 측정한다.
- Source / Evidence: Raw Sections 3.6, 6.8, 9.9, 17.3; 사용자 승인.
- Contract Trace: INV-06, A12, A18.
- Follow-up: delta gold fixture를 P3 slice에서 정의한다.

<a id="d8"></a>
## D8 · 모든 편집의 불변 revision과 snapshot

- Status: Accepted
- Context: 분석 중인 파일을 다시 읽으면 서로 다른 시점의 bytes가 한 결과에 섞인다.
- Decision: 모든 편집은 즉시 불변 Document Revision과 Workspace Snapshot을 만들며 2초 window는 publication coalescing에만 사용한다.
- Rejected Alternative: 파일이 안정될 때까지 기다린 뒤 현재 filesystem을 읽는다.
- Rationale: 계산 입력을 시작 시점에 고정해야 mixed-version Fact를 구조적으로 방지할 수 있다.
- Consequences: parser와 adapter는 snapshot VFS만 읽고 watcher는 변경 신호로만 사용한다.
- Source / Evidence: Raw Sections 6.2 C2, 7.1–7.2, 18.4 R8; 사용자 승인.
- Contract Trace: INV-07, INV-14, A5, A6, A9.
- Follow-up: rapid-edit와 stat-read-stat conformance test가 필요하다.

<a id="d9"></a>
## D9 · P95 3초 current 또는 gap 표시

- Status: Accepted
- Context: 전체 의미 분석 완료를 기다리면 사용자의 이해가 현재 코드에서 장시간 뒤처질 수 있다.
- Decision: 지원 환경에서 관련 편집 후 P95 3초 안에 current 검증 결과 또는 latest-vs-verified gap, 영향 범위와 지연을 표시한다.
- Rejected Alternative: settled 결과가 완성될 때까지 이전 화면만 유지한다.
- Rationale: 시간 안에 검증된 결과가 없더라도 최신 snapshot과 미반영 범위를 알면 오래된 결과를 현재로 오인하지 않는다.
- Consequences: end-to-end trace로 SLO를 측정하고 gap-only와 current publication 성공률을 분리한다.
- Source / Evidence: Raw Sections 0.2, 7.3, 16; 사용자 승인.
- Contract Trace: INV-21, A5, A7, A8.
- Follow-up: release별 지원 환경을 선언한다.

<a id="d10"></a>
## D10 · late result의 제한된 refinement

- Status: Accepted
- Context: 먼저 발행된 결과 뒤에 같은 코드 basis의 고품질 분석이 도착할 수 있다.
- Decision: late result는 같은 `computedBasisId`를 참조하고 발행 시점 Causal Observation Closure 검증과 CAS를 통과할 때만 refinement할 수 있다.
- Rejected Alternative: late result를 모두 폐기하거나 이전 generation version이 정확히 같을 때만 보강한다.
- Rationale: 유효한 같은-basis 보강을 허용하면서 최신 변경과 충돌한 결과의 혼입을 막는다.
- Consequences: computation lineage와 freshness proof를 분리하고 late result도 active pointer CAS를 거친다.
- Source / Evidence: Raw Sections 7.4, 13.2; 사용자 승인.
- Contract Trace: INV-08, INV-11, INV-15, A10, A23.
- Follow-up: late-result race fixture를 구현한다.

<a id="d11"></a>
## D11 · 여섯 작업 mode를 동일 IR projection으로 구현

- Status: Accepted
- Context: feature, review, impact, debug, incident와 onboarding은 질문과 강조가 다르지만 같은 구현 사실을 사용한다.
- Decision: 여섯 mode를 동일 SemanticMapIR에 적용되는 query와 projection으로 정의한다.
- Rejected Alternative: mode마다 별도 저장 모델과 분석 pipeline을 만든다.
- Rationale: 동일 Fact와 Evidence 권위를 유지하면서 task별 탐색 방향만 바꿀 수 있다.
- Consequences: mode 변경은 canonical artifact를 복제하지 않고 projection을 교체한다.
- Source / Evidence: Raw Section 8; 사용자 승인.
- Contract Trace: INV-09, A16–A22.
- Follow-up: mode별 query schema와 completion fixture를 작성한다.

<a id="d12"></a>
## D12 · mode 사이의 기본 UX 위치 유지

- Status: Accepted
- Context: 화면 구조가 mode나 generation마다 재배치되면 사용자의 읽기 맥락이 끊긴다.
- Decision: Answer Strip, Flow Rail, Change Pulse와 Evidence Dock의 기본 위치를 유지한다.
- Rejected Alternative: 각 mode마다 완전히 다른 화면 구조를 사용한다.
- Rationale: stable location이 변경 추적과 interruption 이후 복귀를 지원한다.
- Consequences: mode별 차이는 강조와 탐색 방향으로 표현하며 selection과 scroll을 보존한다.
- Source / Evidence: Raw Sections 8.1, 9.14–9.15, 18.5 R11; 사용자 승인.
- Contract Trace: INV-10, A11.
- Follow-up: stable-layout 비교 UX test를 수행한다.

<a id="d13"></a>
## D13 · active generation 원자 교체

- Status: Accepted
- Context: 서로 다른 generation의 summary, step, Evidence와 alignment를 합치면 존재하지 않은 상태가 만들어진다.
- Decision: active generation은 완전한 단위로 교체하고 partial artifact를 current Fact처럼 merge하지 않는다.
- Rejected Alternative: 준비된 field별로 merge-on-read한다.
- Rationale: 한 화면의 모든 설명이 하나의 검증 basis와 proof를 가져야 한다.
- Consequences: staging artifact와 manifest를 먼저 만들고 active pointer만 원자 교체한다.
- Source / Evidence: Raw Sections 6.9, 10.11, 13.1; 사용자 승인.
- Contract Trace: INV-11, INV-15, A10, A23.
- Follow-up: cross-generation mix rejection fixture를 구현한다.

<a id="d14"></a>
## D14 · coding agent 정보는 intent hint

- Status: Accepted
- Context: agent의 계획과 완료 설명은 현재 source와 다르거나 아직 적용되지 않았을 수 있다.
- Decision: agent plan, message, tool result와 completion declaration은 scope와 semantic 후보를 위한 hint이며 코드 Fact가 아니다.
- Rejected Alternative: agent 완료 선언이나 test 메시지를 verified implementation으로 승격한다.
- Rationale: agent 설명과 현재 source Evidence의 권위를 분리해야 한다.
- Consequences: `completed_by_agent` 이후에도 Evidence와 Settlement Gate를 별도 평가한다.
- Source / Evidence: Raw Sections 11.3, 12.6; 사용자 승인.
- Contract Trace: INV-03, INV-12, A14, A23.
- Follow-up: agent-hint-only negative fixture를 구현한다.

<a id="d15"></a>
## D15 · Evidence 없는 Requirement Alignment는 unknown

- Status: Accepted
- Context: 요청 또는 설명이 명확해도 코드가 이를 구현했다는 근거가 없을 수 있다.
- Decision: Evidence가 없으면 requirement를 `unknown`으로 유지하고 agent나 model 설명만으로 `confirmed`하지 않는다.
- Rejected Alternative: 요청 승인, agent 완료 또는 model confidence를 implementation confirmation으로 사용한다.
- Rationale: 사용자 의도 권위와 현재 구현 사실은 서로 대체할 수 없다.
- Consequences: `not_observed`도 구현 부재 증명으로 자동 해석하지 않고 coverage를 함께 표시한다.
- Source / Evidence: Raw Sections 10.13, 11.2, 12.6; 사용자 승인.
- Contract Trace: INV-03, INV-12, A13, A14.
- Follow-up: alignment status별 Evidence fixture를 작성한다.

<a id="d16"></a>
## D16 · 기존 구현 재사용 판단 연기

- Status: Accepted
- Context: 현재 구현은 일부 유용한 seam을 가지지만 새 snapshot, proof와 gate 계약을 충족한다고 가정할 수 없다.
- Decision: 현재 구현 재사용 여부는 Parent Contract 승인 뒤 후속 구현 설계와 spike에서 판단한다.
- Rejected Alternative: 전면 재작성 또는 기존 구현 전면 유지 중 하나를 아키텍처 전제로 고정한다.
- Rationale: 사용자 계약을 구현 history보다 높은 권위로 유지한다.
- Consequences: component별로 contract와 test를 통과하면 재사용하고 그렇지 않으면 교체한다.
- Source / Evidence: Raw Sections 2.2, 18.6 R17; 현재 code evidence; 사용자 승인.
- Contract Trace: Scope 2.2, ASM-06, Section 15.
- Follow-up: Vertical Slice별 reuse assessment를 기록한다.

<a id="d17"></a>
## D17 · Local SLM을 Core 내부 격리 host로 사용

- Status: Accepted
- Context: coding agent가 model host를 직접 호출하면 Evidence validation과 task authority를 우회할 수 있다.
- Decision: Local SLM은 Semantic Compiler가 호출하는 격리 model host이며 coding agent는 Core MCP만 사용한다.
- Rejected Alternative: model host를 독립 MCP authority로 외부에 노출한다.
- Rationale: 검증과 publication authority를 Core 하나로 유지한다.
- Consequences: model process crash와 resource pressure를 Core에서 격리하고 request를 제한한다.
- Source / Evidence: Raw Sections 6.3, 21.3, 23; 사용자 승인.
- Contract Trace: INV-05, INV-13, A15, A25.
- Follow-up: model host contract와 isolation test를 구현 slice에서 정의한다.

<a id="d18"></a>
## D18 · hot-path adapter의 snapshot 입력 강제

- Status: Accepted
- Context: adapter가 OS filesystem을 직접 읽으면 analyzer 사이에 서로 다른 시점의 code가 섞일 수 있다.
- Decision: 모든 hot-path parser와 adapter는 Workspace Snapshot을 읽고 미지원 도구는 async enrichment에만 사용한다.
- Rejected Alternative: adapter가 요청 처리 중 현재 disk를 다시 읽도록 허용한다.
- Rationale: 모든 Fact가 같은 immutable basis에 고정되어야 currentness를 검증할 수 있다.
- Consequences: adapter protocol은 snapshot content overlay, basis ID와 closure observation을 반환한다.
- Source / Evidence: Raw Sections 7.1, 18.4 R8, 21.5; 사용자 승인.
- Contract Trace: INV-07, INV-14, A6, A9.
- Follow-up: adapter snapshot VFS conformance test를 작성한다.

<a id="d19"></a>
## D19 · proof manifest와 이중 CAS 조건의 단일 publication transaction

- Status: Accepted
- Context: artifact 저장과 active pointer 갱신 사이에 head나 previous generation이 바뀔 수 있다.
- Decision: Causal Observation Closure 검증, Generation Proof Manifest 생성, expected `liveHead`와 previous generation CAS를 단일 transaction으로 수행한다.
- Rejected Alternative: artifact별 pointer를 순차 갱신하거나 마지막 write wins를 사용한다.
- Rationale: stale writer와 partial generation publication을 동시에 차단한다.
- Consequences: CAS 실패는 조용히 재시도하지 않고 latest head 대상으로 closure를 다시 검증한다.
- Source / Evidence: Raw Sections 6.9, 10.11, 21.7; 사용자 승인.
- Contract Trace: INV-11, INV-15, A10.
- Follow-up: concurrent publisher와 CAS failure fixture를 구현한다.

<a id="d20"></a>
## D20 · Fact, 의미와 사람 승인의 promotion 분리

- Status: Accepted
- Context: Git commit은 code snapshot 확정이고 사람 승인은 의미 표현에 대한 권위다.
- Decision: Fact storage, semantic interpretation과 Semantic Approval을 별도 객체와 event로 관리한다.
- Rejected Alternative: commit 또는 user approval을 하나의 Base promotion operation으로 처리한다.
- Rationale: 서로 다른 권위와 취소·재검증 lifecycle을 보존한다.
- Consequences: commit은 provenance와 Fact reuse에, approval은 claim 표현에만 영향을 준다.
- Source / Evidence: Raw Sections 10.9, 11, 21.7; 사용자 승인.
- Contract Trace: INV-03, INV-16, A14.
- Follow-up: approval revoke와 supersede fixture를 작성한다.

<a id="d21"></a>
## D21 · live 상태 축 분리

- Status: Accepted
- Context: editing, currentness, quality, settlement, model 상태와 connection은 동시에 다른 값을 가질 수 있다.
- Decision: quality stage, freshness/display basis, settlement, enrichment, activity와 connection을 독립 상태 축으로 표현한다.
- Rejected Alternative: 하나의 `status` 또는 `degraded` enum으로 모든 조합을 표현한다.
- Rationale: `current + settlement=pending + enrichment=timed_out` 같은 유효 조합을 정확히 전달한다.
- Consequences: UI state machine과 contracts는 parallel region과 교차 invariant를 가진다.
- Source / Evidence: Raw Sections 7.3, 9.13, 10.10; 사용자 승인.
- Contract Trace: INV-17, INV-23, A15, A24.
- Follow-up: 상태 조합 fixture와 accessibility label을 작성한다.

<a id="d22"></a>
## D22 · mode별 필수 입력의 판별 합집합

- Status: Accepted
- Context: 모든 query field가 optional이면 같은 요청을 구현체마다 다르게 해석한다.
- Decision: `mode`를 discriminator로 사용하고 mode별 시작 조건을 required variant로 정의한다.
- Rejected Alternative: 공통 optional query object와 구현체 default scope를 사용한다.
- Rationale: 누락과 ambiguity를 요청 경계에서 deterministic typed error로 처리한다.
- Consequences: JSON Schema `oneOf`와 mode별 `const`, conditional basis ID 규칙이 필요하다.
- Source / Evidence: Raw Section 8.2; 사용자 승인.
- Contract Trace: INV-09, A16–A22.
- Follow-up: valid·invalid query fixture를 각 mode에 제공한다.

<a id="d23"></a>
## D23 · Analysis Read Set만으로 rebase 금지

- Status: Accepted
- Context: 분석 당시 없던 caller, implementation, route 또는 source member는 read set에 나타나지 않는다.
- Decision: negative lookup, membership, dependency frontier와 graph·index revision을 포함한 closed closure가 변경과 교차하지 않을 때만 최신 head proof를 만든다.
- Rejected Alternative: 실제 읽은 document가 unchanged이면 결과를 최신 snapshot으로 rebase한다.
- Rationale: unread location의 새 relation도 결과 의미를 바꿀 수 있다.
- Consequences: scope resolver와 adapter는 부재 관찰과 탐색 경계를 반환해야 한다.
- Source / Evidence: Raw Sections 3.9–3.10, 7.4, 18.4 R7; 사용자 승인.
- Contract Trace: INV-08, INV-14, INV-15, A9, A10.
- Follow-up: new-caller와 membership-change fixture를 구현한다.

<a id="d24"></a>
## D24 · current publication에 proof 필수

- Status: Accepted
- Context: 높은 quality stage라도 어떤 basis와 최신 head를 검증했는지 없으면 currentness를 증명할 수 없다.
- Decision: 유효한 Generation Proof Manifest가 없거나 검증 실패한 generation은 current가 될 수 없다.
- Rejected Alternative: Q3/Q4 또는 높은 confidence만으로 current label을 허용한다.
- Rationale: quality completeness와 temporal currentness는 다른 문제다.
- Consequences: proof 없는 artifact는 historical로 저장할 수 있지만 active pointer 대상이 아니다.
- Source / Evidence: Raw Sections 7.7, 10.11, 13.1; 사용자 승인.
- Contract Trace: INV-15, INV-21, A10, A23, A24.
- Follow-up: missing·invalid proof rejection fixture를 작성한다.

<a id="d25"></a>
## D25 · 정규 JSON Schema 계약

- Status: Accepted
- Context: 예시 YAML과 언어별 struct만으로는 Core, adapter, MCP와 UX가 같은 의미를 해석한다고 보장할 수 없다.
- Decision: 경계 통과, persistence, CAS hash, 외부 publication과 authority change payload는 `schemaId`, `schemaVersion`의 JSON Schema 계약을 가진다.
- Rejected Alternative: 문서 예시 또는 producer 구현을 사실상 schema로 사용한다.
- Rationale: 필수 field, enum, empty value, identifier와 compatibility를 실행 가능하게 검증한다.
- Consequences: structural schema와 cross-artifact Semantic Validator를 함께 운영하고 unknown version을 거절한다.
- Source / Evidence: Raw Sections 10.0, 21.10–21.11; 사용자 승인.
- Contract Trace: INV-18, A27, A28.
- Follow-up: Contract Registry와 schema identity check를 첫 slice 전에 만든다.

<a id="d26"></a>
## D26 · schema artifact를 Vertical Slice 구현 gate로 생성

- Status: Accepted
- Context: 모든 schema를 architecture approval 전에 작성하면 사용하지 않는 계약과 물리 구조를 조기 고정한다.
- Decision: 정규 schema와 valid·invalid fixture는 해당 payload를 처음 사용하는 Vertical Slice의 구현 시작 조건으로 작성한다.
- Rejected Alternative: 전체 contract schema를 upfront 구현하거나 schema 작업을 production implementation 뒤로 미룬다.
- Rationale: 필요한 계약은 구현 전에 강제하되 미래 slice의 세부 구조는 연기한다.
- Consequences: slice는 producer-consumer compatibility와 Semantic Validator plan 없이는 승인·구현할 수 없다.
- Source / Evidence: Raw Sections 10.0, 19, 20 A27; 사용자 승인.
- Contract Trace: INV-19, A27.
- Follow-up: 각 slice의 Contract Gate에서 실제 schema path와 command를 확정한다.

<a id="d27"></a>
## D27 · Current Publication과 Settlement Gate 분리

- Status: Accepted
- Context: 최신 코드에 대해 검증된 최소 결과와 요청 흐름 전체의 완료 검증은 서로 다른 시점에 가능하다.
- Decision: Q1·Q2는 Publication Gate 통과 후 `current + pending`으로 발행하고 Settlement Gate는 Q3 `passed` 승격에만 사용한다. `failed`는 명시적 평가 실패에만 사용하고 Q4는 사실을 바꾸지 않는다.
- Rejected Alternative: current와 settled를 하나의 gate로 묶거나 시간이 지나면 자동 settled 처리한다.
- Rationale: 최신성을 빠르게 제공하면서 완료로 오인되는 것을 막는다.
- Consequences: manifest에 current publication과 settlement evaluation을 별도 기록하고 UI도 두 상태를 분리한다.
- Source / Evidence: Raw Sections 10.11, 12.6, 18.1–18.2; 사용자 승인.
- Contract Trace: INV-17, INV-20, INV-24, A24.
- Follow-up: pending, failed, passed의 semantic fixture를 작성한다.

<a id="d28"></a>
## D28 · correctness invariant와 P95 UX SLO 분리

- Status: Accepted
- Context: 모든 분석을 3초 hard deadline으로 제한하면 Evidence와 closure 검증을 생략할 유인이 생긴다.
- Decision: currentness와 Evidence 정확성은 hard invariant이며 3초는 지원 환경의 end-to-end P95 UX SLO다.
- Rejected Alternative: 3초 안에 결과를 만들기 위해 partial 또는 미검증 result를 current로 발행한다.
- Rationale: 시간 목표 실패는 표시할 수 있지만 잘못된 current publication은 허용할 수 없다.
- Consequences: SLO 실패 시 latest snapshot, last verified, affected scope와 lag를 표시하고 실패로 측정한다.
- Source / Evidence: Raw Sections 7.3, 16, 18.4 R5; 사용자 승인.
- Contract Trace: INV-21, A5, A7, A8, A10, A24.
- Follow-up: 지원 환경과 end-to-end trace corpus를 release별 고정한다.

<a id="d29"></a>
## D29 · Task Intent 원문, 정규화와 확인 상태 분리

- Status: Accepted
- Context: parser가 만든 해석과 사용자가 확인한 의도, 현재 코드의 requirement confirmation은 같은 상태가 아니다.
- Decision: 불변 `rawRequest`, normalized intent와 `parsed|needs_confirmation|user_confirmed` lifecycle을 분리한다.
- Rejected Alternative: 하나의 normalized text와 `confirmed` boolean으로 요청 해석과 구현 정렬을 함께 표현한다.
- Rationale: 원문 손실과 confirmation 의미 충돌을 방지한다.
- Consequences: requirement 변경은 새 intent revision을 만들고 Requirement Alignment는 별도 상태를 유지한다.
- Source / Evidence: Raw Sections 3.1, 6.2 C1, 10.1, 12.5–12.6; 사용자 승인.
- Contract Trace: INV-22, A1, A13, A14.
- Follow-up: lifecycle transition과 ambiguity fixture를 작성한다.

<a id="d30"></a>
## D30 · 독립 degraded 상태 제거

- Status: Accepted
- Context: model timeout, adapter coverage와 구조 분석 실패는 원인과 영향이 다른데 하나의 degraded label은 결과 품질을 오해시킨다.
- Decision: 독립 `degraded` state를 두지 않고 `quality.degradations[]`, `enrichmentStatus`, quality stage와 coverage로 분리한다.
- Rejected Alternative: 선택 기능 실패를 generation 전체의 degraded status로 표시한다.
- Rationale: deterministic 결과의 유효성과 optional enrichment 실패를 구분한다.
- Consequences: degradation item은 stable code, scope, impact와 recovery condition을 가지며 model 실패를 포함하지 않는다.
- Source / Evidence: Raw Sections 9.13, 10.10, 10.16, 15; 사용자 승인.
- Contract Trace: INV-17, INV-23, A15, A23.
- Follow-up: adapter degradation과 model timeout UI fixture를 분리한다.

<a id="d31"></a>
## D31 · 모든 필수 Critical Obligation으로 settlement 판정

- Status: Accepted
- Context: coverage 비율이나 가중 점수는 entry, result 또는 failure 같은 필수 항목 하나의 누락을 숨길 수 있다.
- Decision: 모든 mode별 required Critical Obligation이 verified이고 critical unknown과 conflict가 0일 때만 `settlement=passed`를 허용한다.
- Rejected Alternative: weighted score, coverage threshold 또는 deadline으로 settled를 결정한다.
- Rationale: 완료 표시는 사용자에게 중요한 흐름 요소의 전부 검증을 의미해야 한다.
- Consequences: coverage summary는 표시·측정용 파생값일 뿐 gate authority가 아니다.
- Source / Evidence: Raw Sections 3.17, 10.10, 10.16, 18.1; 사용자 승인.
- Contract Trace: INV-20, INV-24, A24.
- Follow-up: mode별 obligation 산출 계약을 해당 slice에서 정의한다.

<a id="d32"></a>
## D32 · 전체 SemanticMapIR과 soft display budget 분리

- Status: Accepted
- Context: 7–15개 제한을 canonical IR에 적용하면 실제 흐름과 중요 경계가 영구 손실될 수 있다.
- Decision: SemanticMapIR은 확인된 전체 흐름을 보존하고 7–15개는 기본 FlowViewProjection의 soft display budget으로만 사용한다.
- Rejected Alternative: canonical flow를 최대 15개 step으로 잘라 저장하거나 짧은 flow를 7개로 채운다.
- Rationale: 이해를 위한 축약과 사실 보존을 분리한다.
- Consequences: 7개 미만은 전부 표시하고 15개 초과는 비핵심 subflow만 접으며 entry, result, critical branch, failure, external effect와 unknown boundary는 항상 보존한다.
- Source / Evidence: Raw Sections 8.2, 9.7, 10.10 FlowViewProjection, 18.5 R13; 사용자 승인.
- Contract Trace: INV-25, A2.
- Follow-up: hidden-critical rejection과 budget overflow fixture를 작성한다.

## Decision Set Completion

- Accepted decisions: D1–D32
- Missing decision records: 0
- Blocking Open Decisions: 0
- Parent acceptance trace: A1–A28 → the corresponding Vertical Slice acceptance and evidence records
- Superseded predecessor decision authority: `SMAP`, `ADAPTER-PROTOCOL-V2-MIGRATION`
