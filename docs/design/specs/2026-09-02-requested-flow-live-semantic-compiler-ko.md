# 요청 흐름 이해와 실시간 Semantic Compiler

- Contract ID: `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER`
- Contract Status: Approved
- Created: 2026-09-02
- Last Amended: 2026-09-09
- Intent Status: Hardened
- Implementation Conformance: R2 VS-01–VS-10 prior scopes verified; Flow Context and Live change-ingress amendments require separate slice evidence; production release readiness remains unclaimed pending VS-10 real-environment evidence
- Source: `docs/design/raw/requested-flow-live-semantic-compiler-architecture-draft-ko.md`
- Supplemental Source: User-provided Review No.1–No.3 in the 2026-09-04 request; FlowView code-context user decision 2026-09-08; Live Semantic View change-ingress and notification user decision 2026-09-09
- Source Authority: the Raw specification is canonical; reviews are implementation evidence
- Decision Records: `docs/design/decisions/requested-flow-live-semantic-compiler-decisions-ko.md`
- Glossary: `docs/design/glossary.md`
- Supersedes: `docs/design/specs/2026-09-01-semantic-map-layered-architecture-ko.md`

이 계약은 Raw의 제품 의도를 변경하지 않는다. 세 차례 구현 리뷰에서 확인된 false-current, false-settlement, fabricated Evidence와 release self-certification을 차단할 수 있도록 Raw의 hard invariant를 실행 가능하고 관찰 가능한 상위 계약으로 정규화한다.

## 1. 문제와 목표

현재 구현은 schema와 package, 정상 경로 테스트를 갖추었지만 다음 상태를 실제 근거 없이 만들 수 있다.

- 최신 Workspace Snapshot을 증명하지 않은 `current`
- Q3 Critical Obligation을 충족하지 않은 `settlement=passed`
- 관찰 입력이 없는 `runtime_observed`
- 검증된 Evidence가 없는 `confirmed`
- 실측 benchmark artifact가 없는 `releaseReady=true`

이 문제는 사용자가 현재 코드와 다른 의미를 사실로 받아들이게 만든다. 목표는 요청한 흐름과 coding agent가 변경 중인 흐름을 동일한 불변 snapshot, 검증된 Evidence, 명시적 unknown, 원자적 publication을 통해 이해하게 하고, 지원 환경에서 관련 편집 후 P95 3초 안에 검증된 current 결과 또는 latest-vs-verified gap을 표시하는 것이다.

### 1.1 Intent와 Goal

| Intent | Goal | 관찰 가능한 결과 |
|---|---|---|
| `INT-01` 사용자가 요청 흐름을 현재 코드 근거로 이해한다. | `GOAL-01` 요청을 시작점부터 결과까지 추적한다. | 사용자는 구조적 흐름, Evidence, coverage와 unknown을 구분한다. |
| `INT-01` | `GOAL-02` 변경·오류·장애·도메인 책임을 인과 관계로 탐색한다. | impact, debug, incident, onboarding이 같은 검증 IR의 projection으로 동작한다. |
| `INT-01` | `GOAL-03` 사람이 근거 있는 의미 표현을 승인한다. | 승인은 실제 proposal과 basis에 결합되고 구조 Fact를 변경하지 않는다. |
| `INT-02` coding agent의 구현 변화와 이해 화면 사이의 지연을 통제한다. | `GOAL-04` 모든 편집을 불변 revision과 snapshot으로 수집한다. | 분석 중 서로 다른 시점의 bytes가 섞이지 않는다. |
| `INT-02` | `GOAL-05` 최신 검증 결과 또는 gap을 지속적으로 표시한다. | 편집 후 reactive pipeline이 자동 실행되고 current/gap을 발행한다. |
| `INT-02` | `GOAL-06` capability를 재현 가능한 증거로만 선언한다. | 측정되지 않은 capability는 GA 또는 complete가 되지 않는다. |

## 2. 범위

### 2.1 In Scope

- versioned edit ingress, filesystem reconciliation, 불변 Document Revision과 전체 Workspace Snapshot
- snapshot-only parser와 language adapter, Analysis Read Set과 Causal Observation Closure
- activity acknowledgement, 2초 publication coalescing, latest-wins scheduling과 background incremental analysis
- deterministic Fact, SemanticMapIR, Semantic Delta, Requirement Alignment, Evidence와 unknown
- Current Publication Gate, Settlement Gate, Generation Proof Manifest와 active pointer transaction
- feature, review, impact, debug, incident, onboarding query와 동일 IR projection
- Evidence Pack, optional model proposal, Semantic Approval과 deterministic fallback
- REST, MCP, SSE의 schema, idempotency, ordering, replay와 reconnect
- secret/path policy, runtime isolation, bounded diagnostics와 source read-only 보장
- versioned benchmark corpus와 release capability gate
- 기존 문자열 `workspaceEpoch` artifact의 compatibility 처리

### 2.2 Non-Goals

- coding agent 구현 또는 coding agent의 완료 선언을 사실로 채택 — agent 정보는 intent hint다.
- 모델이 구조 Fact, Evidence, Requirement Alignment 또는 settlement를 결정 — 모델은 검증된 Fact의 의미 표현만 제안한다.
- 전체 repository graph를 기본 화면에 표시 — task-scoped projection을 사용한다.
- cryptographic non-repudiation — 현재 Semantic Approval은 인증된 로컬 사용자와 append-only 저장을 요구하며 Ed25519 서명은 별도 보안 계약 대상이다.
- Raw에 없는 precision, recall 또는 critical coverage 비율을 GA threshold로 발명 — 측정 항목과 통과 기준은 release별 승인된 profile에서만 정한다.
- 정상 경로 단위 테스트나 schema 존재만으로 capability 완료를 선언 — 실제 boundary와 adversarial trace가 필요하다.

## 3. 행위자와 선행 조건

- Primary actor: 현재 요청 흐름과 coding agent의 구현 변화를 이해하려는 사용자
- Supporting actors: IDE 또는 coding agent edit producer, maintainer, 인증된 로컬 semantic approver
- Caller: FlowView, Core MCP client, language adapter, release evaluator
- Permission: source는 read-only이며 runtime 실행과 외부 모델 전송은 별도 동의와 명시적 범위를 요구한다.
- System precondition: repository, worktree, 정수 `workspaceEpoch`, Task Intent revision, adapter capability profile이 식별되어야 한다.
- Data precondition: current 후보의 모든 artifact가 같은 basis, generation, schema identity와 proof lineage를 가져야 한다.

## 4. 확인된 사실

`CF-02`–`CF-08`은 2026-09-04 amendment 당시 세 차례 리뷰의 구현 기준선이다. 현재 remediation 상태는 `CF-09`와 §13의 R2 slice table이 기록한다.

- `CF-01`: Raw D1–D32와 A1–A28이 제품 의도와 외부 동작의 정본이다.
- `CF-02`: 일반 Go, adapter, browser 테스트 통과는 확인됐지만 false-current, fabricated Evidence, release self-certification을 차단하지 못했다.
- `CF-03`: 현재 snapshot 경로는 분석 도중 OS filesystem을 다시 읽을 수 있고 실제 harvest/slicing 경로와 통합되지 않았다.
- `CF-04`: current gate와 active pointer write는 captured live head, basis, intent, epoch와 artifact 일치를 하나의 transaction으로 검증하지 않는다.
- `CF-05`: impact, failure, approval, onboarding과 release 경로에 빈 결과 또는 합성 객체를 성공처럼 반환하는 코드가 존재한다.
- `CF-06`: adapter·semantic·workspace·storage schema가 `workspaceEpoch`를 integer와 string으로 다르게 정의한다.
- `CF-07`: `go test -race ./internal/flowview ./internal/semantic ./internal/workspace ./internal/storage ./internal/secret`는 FlowView SSE 테스트의 동시 접근 race를 검출한다.
- `CF-08`: Review No.3에 언급된 별도 audit report 파일은 repository source로 확인되지 않았으므로 이 계약은 첨부된 리뷰 본문과 직접 확인된 repository evidence만 사용한다.
- `CF-09`: VS-01–VS-10 R2 implementation과 자동 검증은 2026-09-07 통과했다. VS-10의 실제 profile/corpus 측정과 quality/resource threshold 승인은 사용자 실행 항목이므로 production `releaseReady`는 아직 선언하지 않는다.
- `CF-10`: 현재 일반 filesystem save는 `submit_versioned_edit`를 자동 호출하지 않는다. `internal/watch`의 polling과 stable capture는 Live coordinator에 연결되지 않았고, `/live`는 `generation.published` 뒤 `/api/task/view`를 재요청하므로 event가 가리킨 immutable generation 대신 새 candidate를 표시할 수 있다. 이 상태는 Live Semantic View의 edit-driven current-or-gap 계약을 충족하지 않는다.

## 5. 가정

- `ASM-01`: 지원 환경은 release마다 OS, hardware, repository 규모, language/toolchain, active scope, 동시 부하와 browser 조건으로 선언한다.
  - Consequence if false: P95 결과를 어떤 사용자에게 보장하는지 판단할 수 없다.
  - Validation method: release profile과 benchmark artifact의 profile identity를 대조한다.
- `ASM-02`: 기존 문자열 epoch artifact는 live state로 계속 사용할 필요가 없다.
  - Consequence if false: history 조회 또는 migration 요구가 누락될 수 있다.
  - Validation method: 저장된 active pointer, manifest, snapshot의 사용처와 보존 정책을 migration 전에 조사한다.
- `ASM-03`: 인증된 로컬 사용자 identity와 append-only approval log가 현재 승인 추적 요구를 충족한다.
  - Consequence if false: 원격 다중 사용자 또는 법적 감사에서 승인 부인 방지가 부족하다.
  - Validation method: 원격 협업이나 non-repudiation 요구가 생기면 별도 보안 계약을 승인한다.
- `ASM-04`: adapter는 선택한 흐름 step의 정확한 statement anchor와 그 statement를 포함하는 구조적 문맥, enclosing callable을 같은 snapshot에서 구별할 수 있다.
  - Consequence if false: 넓은 function 또는 class 범위를 선택 step Evidence로 표시하면 사용자가 실제 실행 지점을 오해한다.
  - Validation method: nested callback, condition, builder 안의 entry와 broad-only anchor fixture에서 정확한 Evidence 또는 명시적 unavailable 상태를 검증한다.
- `ASM-05`: VS Code, coding agent와 watcher fallback은 repository-relative path와 변경 뒤의 안정된 bytes 또는 delete/rename 사실을 coordinator에 제공할 수 있다.
  - Consequence if false: coordinator가 변경을 관측하지 못하거나 서로 다른 bytes를 같은 edit로 취급할 수 있다.
  - Validation method: IDE save, agent multi-file transaction, watcher overflow, rename/delete와 branch transition의 end-to-end trace를 실행한다.

## 6. 비즈니스 규칙과 불변 조건

### 6.0 Raw invariant trace registry

기존 D1–D32와 VS 문서가 참조하는 `INV-*` ID는 안정적인 Raw trace ID로 보존한다. 이번 amendment의 구체적인 집행 조건은 `HINV-*`로 구분한다.

| ID | Raw invariant | Raw authority |
|---|---|---|
| `INV-01` | current implementation fact는 current source와 validated analyzer Evidence를 요구한다. | §3.16, §6.3, §11 |
| `INV-02` | 분석 범위는 Task Intent 또는 명시적 query로 제한한다. | §3.1, §6.1, §8 |
| `INV-03` | published claim은 Evidence grounding과 authority separation을 요구한다. | §10.7–§10.9, §11 |
| `INV-04` | unknown과 unresolved relation을 명시한다. | §3.14, §6.3, §15 |
| `INV-05` | precision과 coverage를 분리하고 deterministic fallback을 유지한다. | §6.4, §18.3–§18.6 |
| `INV-06` | runtime Evidence는 scenario와 isolation scope로 제한한다. | §11.5, §14–§15 |
| `INV-07` | hot-path analysis는 하나의 immutable workspace snapshot을 읽는다. | §7.1, §21.5 |
| `INV-08` | currentness는 read set이 아니라 closed causal observation을 요구한다. | §3.9–§3.10, §7.4 |
| `INV-09` | 한 generation의 canonical artifact는 basis, generation과 schema identity를 공유한다. | §10.10–§10.11, §13.1 |
| `INV-10` | active publication은 atomic이며 stale result가 pointer를 교체할 수 없다. | §6.9, §7.7, §13.2 |
| `INV-11` | activity, freshness, quality, settlement, enrichment와 connection은 별도 축이다. | §7.3, §9.13 |
| `INV-12` | SemanticMapIR은 전체 flow를 보존하고 projection은 soft display budget을 사용한다. | §8.2, §9.7, §10.10, D32 |
| `INV-13` | mode별 query precondition을 typed variant로 검증한다. | §8.2 |
| `INV-14` | adapter Evidence는 basis, read set, negative lookup, membership과 frontier를 포함한다. | §7.1, §10.3–§10.5 |
| `INV-15` | source는 read-only이며 sensitive Evidence는 exposure 전에 redact한다. | §14, §18.6 |
| `INV-16` | event ordering, replay와 mutation idempotency를 계약으로 정의한다. | §10.14, §21.8, A28 |
| `INV-17` | adapter boundary는 호환되지 않는 protocol version을 섞지 않는다. | §21.8, D18, D25–D26 |
| `INV-18` | runtime isolation과 trusted-local approval 범위를 표시한다. | §14, §18.6 R15 |
| `INV-19` | canonical boundary payload는 registered JSON Schema와 semantic validation을 사용한다. | §10.0, A27 |
| `INV-20` | dynamic-language capability는 측정된 supported subset으로 제한한다. | §21.5, §21.10, A16 |
| `INV-21` | P95 freshness SLO는 correctness와 Evidence gate를 약화하지 않는다. | §7.3, §16, §18.4 R5 |
| `INV-22` | raw intent, normalized intent, confirmation과 alignment lifecycle을 분리한다. | §3.1, §6.2 C1, §10.1, D29 |
| `INV-23` | degradation은 cause와 impact로 표현한다. | §9.13, §10.10, §15, D30 |
| `INV-24` | 모든 required obligation이 verified이고 critical unknown/conflict가 0일 때만 settlement가 passed다. | §3.17, §10.10–§10.11, §18.1, D31 |
| `INV-25` | projection은 canonical IR의 critical boundary를 제거하지 않는다. | §8.2, §9.7, §10.10, D32 |

### 6.1 Snapshot과 입력

- `HINV-01`: 각 accepted edit는 즉시 불변 Document Revision과 전체 workspace persistent snapshot을 만든다.
- `HINV-02`: snapshot VFS의 모든 path는 repository-relative canonical path여야 하며 root escape, symlink escape와 직접 OS 재읽기를 거절한다.
- `HINV-03`: parser, slicer, harvester와 hot-path adapter는 하나의 snapshot lease만 읽는다. SDK와 package cache는 immutable fingerprint로 고정한다.
- `HINV-04`: 반환된 revision, snapshot과 byte buffer를 caller가 수정해도 저장된 객체는 변하지 않아야 한다.
- `HINV-05`: `workspaceEpoch`는 schema 전체에서 0 이상의 durable integer이며 같은 repository/worktree lineage에서 단조 증가한다. equality는 같은 compatibility 범위인지 검증할 때만 사용하고 snapshot, generation, event와 document counter를 대신하지 않는다.
- `HINV-06`: branch, worktree 또는 호환되지 않는 configuration/toolchain 전환은 새 epoch를 만들고 이전 active result를 historical로 만든다.

### 6.2 Closure, Evidence와 권위

- `HINV-07`: Causal Observation Closure는 positive dependency, negative lookup, membership, dependency frontier, graph/index/configuration/capability revision과 coverage boundary를 기록한다.
- `HINV-08`: adapter가 필수 observation을 실측할 수 없으면 `closureStatus=open`과 incomplete reason을 반환한다. 빈 배열과 `closed`로 대체할 수 없다.
- `HINV-09`: open, missing 또는 delta와 교차하는 closure는 current proof를 만들 수 없다.
- `HINV-10`: Evidence는 snapshot revision에서 읽은 실제 source, compiler, test, contract 또는 scoped runtime observation만 가리킨다.
- `HINV-11`: file read 실패, invalid range 또는 누락된 Evidence를 description snippet으로 대체해 `verified`로 표시하지 않는다.
- `HINV-12`: agent message와 model output은 Fact 또는 Evidence가 아니며 `observed`, `runtime_observed`, `confirmed`로 승격할 수 없다.
- `HINV-13`: secret/path policy는 Evidence Pack, log, diagnostic, CAS, MCP, REST와 browser payload에 동일하게 적용한다. JSON key와 value를 모두 검사한다.

### 6.3 Graph와 의미

- `HINV-14`: SemanticMapIR은 모든 step과 edge의 source/target identity를 보존하며 stable ID는 ordinal이나 line number만으로 결정하지 않는다. Semantic Delta는 added, changed, removed와 structural-only를 구분하고 불확실한 rename/move를 조용히 확정하지 않는다.
- `HINV-15`: impact는 직접 caller/effect와 bounded indirect result, unresolved dynamic boundary, 추가 탐색 가능 여부를 실제 graph에서 계산한다.
- `HINV-16`: debug와 incident는 제공된 error, symptom, trace 또는 runtime Evidence에서 역추적한다. 관찰되지 않은 timeout, retry, circuit breaker와 recovery는 unknown이다.
- `HINV-17`: onboarding은 필수 repository identity와 분석된 graph/index Evidence로 domain, ownership, 대표 flow와 coverage boundary를 만든다.
- `HINV-18`: Requirement Alignment의 `confirmed`는 현재 basis의 verified Evidence를 요구한다. Evidence reference 문자열의 존재만으로 확인하지 않는다.
- `HINV-19`: Q4는 Q3의 구조 Fact, Critical Obligation, Requirement Alignment와 settlement를 변경하지 않고 표현과 projection만 보강한다.
- `HINV-20`: projection은 entry, result, critical branch, failure, external effect, security boundary와 unknown boundary를 제거하지 않는다.
- `HINV-37`: Semantic Map의 code view는 선택 step의 정확한 statement Evidence, 이를 포함하는 구조적 문맥, enclosing callable과 전체 source 범위를 구분한다. function 또는 class 전체 범위는 정확한 step Evidence로 표시하지 않는다.
- `HINV-38`: 기본 Flow Context는 정확한 선택 statement, 존재할 때 이를 감싸는 condition·callback·builder, enclosing callable signature와 직접 predecessor/successor 또는 call relation을 표시한다. 직접 enclosing 구조가 없으면 그 부재를 표시한다. 전체 callable 또는 file은 사용자의 명시적 확장 뒤에만 표시한다.
- `HINV-39`: adapter가 같은 snapshot에서 statement node임을 검증한 range를 제공하지 못하거나 anchor가 enclosing callback·condition·builder·function·class와 같거나 더 넓으면 precision 상태를 `unavailable` 또는 `unknown`으로 표시한다. 이 경우 bounded context는 제공할 수 있지만 전체 범위를 선택 step으로 강조하거나 `verified`로 승격하지 않는다.

### 6.4 Publication과 상태

- `HINV-21`: Current Publication Gate는 snapshot, closure, Evidence, semantic atomicity, task relevance와 comprehension을 순서대로 검증한다. 뒤 gate가 앞 gate 실패를 보완하지 않는다.
- `HINV-22`: 모든 current 후보 artifact는 repository, worktree, integer epoch, Task Intent revision, normalized query, `computedBasisId`, generation, schema identity와 closure digest가 일치해야 한다.
- `HINV-23`: `freshness=current`는 유효한 Generation Proof Manifest와 captured `liveHead` 검증 없이는 발행할 수 없다.
- `HINV-24`: Q1·Q2는 publication gate를 통과하면 `current + settlement=pending`만 가능하다.
- `HINV-25`: `settlement=passed`는 Q3 이상, 모든 required Critical Obligation verified, critical unknown 0, conflict 0일 때만 가능하다. coverage 비율과 deadline은 gate 입력이 아니다.
- `HINV-26`: same-basis late refinement도 closure와 workspace delta, epoch, intent, query, captured live head와 expected previous generation을 다시 검증한다.
- `HINV-27`: manifest, active pointer와 sequenced `generation.published` event ledger는 하나의 durable transaction에서 commit한다. CAS 실패 시 published event를 발행하지 않는다.
- `HINV-28`: query는 active pointer가 가리키는 manifest 하나에서 전체 generation을 읽으며 서로 다른 generation을 merge하지 않는다.

### 6.5 Live loop, approval과 release

- `HINV-29`: edit가 accepted되면 activity를 갱신하고 scheduler가 quiet 또는 max-wait checkpoint를 소비하여 background compile, validate, publish 또는 gap 경로를 자동 실행한다.
- `HINV-30`: overload에서 최신 checkpoint를 우선하고 superseded work를 취소한다. revision, delete, rename과 public contract change는 손실하지 않는다.
- `HINV-31`: EventEnvelope sequence는 stream별 단조 증가하며 subscriber overflow를 감지해 replay 또는 full snapshot recovery로 전환한다.
- `HINV-32`: Semantic Approval은 저장된 실제 proposal, verified Evidence Pack, current basis와 intent revision을 검증한 뒤에만 기록한다.
- `HINV-33`: approval은 인증된 로컬 actor, idempotency key, target claim, approved text, basis, intent revision, timestamp와 active/superseded/revoked 상태를 append-only로 영속화한다.
- `HINV-34`: approval은 구조 Fact, Evidence, Requirement Alignment, freshness 또는 settlement를 변경하지 않는다.
- `HINV-35`: release capability는 versioned corpus, declared profile과 재현 가능한 contract, security, resilience, comprehension, semantic quality와 end-to-end trace evidence가 모두 있을 때만 승격한다.
- `HINV-36`: 누락된 benchmark 입력을 기본 합격 수치로 채우지 않는다. 필수 evidence가 없으면 `incomplete`, `experimental`, `partial` 또는 `unsupported`이며 `releaseReady=false`다.
- `HINV-40`: Static FlowView는 사용자가 요청한 immutable snapshot의 흐름만 표시하며 workspace stream 구독, 변경 감지 알림 또는 자동 generation 교체를 수행하지 않는다. 이러한 동작은 `/live` Live Semantic View에만 속한다.
- `HINV-41`: IDE, coding agent와 watcher fallback에서 관측한 변경은 하나의 versioned workspace change ingress를 통해서만 revision과 snapshot으로 수락한다. ingress는 source, batch identity, repository-relative path, upsert/delete/rename 종류와 stable post-change bytes 또는 검증된 삭제 사실을 기록한다.
- `HINV-42`: 동일 canonical path와 content identity의 중복 제출은 하나의 revision으로 idempotent 처리한다. 다중 파일 agent edit는 하나의 batch로 보존하고, rename/delete, watcher overflow와 worktree lineage 전환은 손실하거나 upsert로 추정하지 않는다.
- `HINV-43`: filesystem event는 capture 신호일 뿐 source Fact가 아니다. watcher fallback은 안정화 뒤 stat-read-stat capture 또는 bounded whole-workspace reconciliation을 통과한 bytes만 ingress에 제출한다.
- `HINV-44`: Live Semantic View는 `generation.published` event의 generation, basis와 snapshot identity에 일치하는 저장된 proof-backed view만 반영한다. event 수신 뒤 live filesystem을 다시 읽거나 unverified candidate를 같은 update로 표시하지 않는다.
- `HINV-45`: Live Semantic View는 변경 감지, 검증된 semantic update, verified gap과 stream-disconnected 상태를 구분해 표시한다. 사용자 알림에는 semantic behavior, branch, state, external effect, call relation, Evidence 또는 unresolved move 변화만 포함하며 내부 compiler telemetry를 주 화면에 노출하지 않는다.
- `HINV-46`: Live Semantic View가 compatible generation을 반영할 때 stable step identity로 선택 step과 읽기 위치를 복원한다. 사용자가 읽기 고정을 선택했거나 새 generation에 선택 identity가 없으면 기존 화면을 유지하고 명시적 적용 또는 제거 상태를 표시한다.

## 7. 관찰 가능한 사용자 흐름

1. 사용자가 자연어 요청 또는 mode별 typed query를 제출한다.
2. 시스템은 Task Intent revision과 시작점을 결정하고 모호함이나 필수 입력 누락을 typed error로 반환한다.
3. VS Code, coding agent 또는 watcher fallback이 workspace change ingress에 변경을 제출한다. coordinator는 안정된 bytes 또는 검증된 파일 구조 변경만 수락해 immutable revision과 snapshot을 즉시 생성한다.
4. Activity Channel은 변경된 scope, latest snapshot, lag와 pending revision을 표시한다.
5. scheduler는 2초 quiet 또는 max-wait checkpoint를 소비하고 선택 snapshot을 background pipeline에 전달한다.
6. snapshot-aware adapter가 Fact, Analysis Read Set, negative lookup, membership, frontier, capability와 coverage를 반환한다.
7. compiler는 전체 SemanticMapIR, Evidence, unknown, mode별 Critical Obligation과 필요한 Delta를 만든다.
8. Publication Gate가 computed basis부터 captured live head까지의 delta와 closed closure를 교차 검증한다.
9. 검증 성공 시 artifact, proof manifest, active pointer와 event를 원자 commit하고 SSE가 immutable generation identity와 Semantic Delta를 전달한다.
10. Live Semantic View는 event identity와 일치하는 저장된 view를 읽는다. 읽기 고정이 아니면 semantic update를 반영하고, 읽기 고정이면 적용 전까지 기존 화면을 유지한다.
11. 검증 실패 또는 SLO 초과 시 Live Semantic View는 마지막 검증 generation과 latest snapshot의 affected scope와 원인을 표시한다.
12. 사용자가 Semantic Map step을 선택하면 시스템은 같은 snapshot의 Flow Context로 정확한 statement, 구조적 문맥, enclosing callable signature와 직접 흐름 관계를 구분해 표시한다. 정확한 anchor가 없으면 unavailable 또는 unknown과 bounded context를 표시한다.
13. 사용자가 명시적으로 확장할 때만 전체 callable 또는 file source를 표시한다.
14. optional model은 verified Evidence Pack만 받아 의미 표현을 제안하며 사용자는 근거를 보고 승인, 수정 후 승인, 거절 또는 취소한다.
15. maintainer는 versioned corpus와 declared profile의 실제 보고서로 capability를 평가하고 미검증 capability를 GA로 표시하지 않는다.

## 8. 실패와 경계 동작

| 조건 | 관찰 결과 | 금지되는 부작용 | 복구 |
|---|---|---|---|
| mode 필수 입력 누락 또는 target 모호 | typed `missing_precondition` 또는 `ambiguous_target` | 임의 기본 scope 생성 없음 | 명시 입력으로 재요청 |
| path가 repository root를 벗어남 | policy error와 Evidence 거절 | host file read 또는 snippet 노출 없음 | canonical repository-relative path 사용 |
| snapshot capture 중 파일 재변경 | `editing` 또는 `reconciling` | 혼합 bytes snapshot 없음 | bounded stat-read-stat 재시도 후 새 snapshot |
| closure missing/open/incomplete | `last_verified` 또는 scoped unknown | current proof 없음 | observation 보완 또는 최신 snapshot 재분석 |
| closure와 workspace delta 교차 | 실제 affected scope, `analysisLagMs`, `pendingRevisions`와 conflict cause를 포함한 gap | stale generation 활성화와 기본값 합성 없음 | captured latest head로 재분석 |
| manifest 또는 active pointer CAS 실패 | publish conflict | `generation.published` 없음 | 새 head와 active generation 기준 재검증 |
| Q1/Q2 또는 obligation 미완료 | current일 수 있으나 settlement pending | passed 없음 | Q3 evidence 수집과 명시적 settlement 평가 |
| runtime observation 없음 | possible path 또는 unknown | `runtime_observed`, corroborated timeline 없음 | 승인된 scenario/trace 연결 |
| model timeout/crash/unavailable | deterministic result와 enrichment status | Fact/quality/currentness 변경 없음 | same-basis proposal 재시도 가능 |
| proposal·Evidence·basis 불일치 | rejected 또는 stale approval conflict | approval log와 projection 변경 없음 | 최신 proposal과 Evidence 재검토 |
| duplicate approval mutation | 기존 idempotent result | 중복 approval event 없음 | 기존 결과 반환 |
| secret 또는 unsafe diagnostic 발견 | redacted/policy failure | 원문 model/browser/log 전송 없음 | scope 축소 또는 안전한 fact만 사용 |
| SSE gap, overflow 또는 reconnect | `replaying` 후 최신 상태 | 조용한 event loss 없음 | last sequence replay 또는 full manifest sync |
| Live stream disconnected | 마지막 검증 흐름과 `연결 끊김` 상태 | 새 filesystem candidate를 최신으로 표시하지 않음 | last event sequence로 reconnect 후 replay 또는 snapshot sync |
| Live update가 읽기 고정 중 | `검증된 변경 도착`과 적용 action | 읽고 있는 generation 교체 없음 | 사용자가 적용하거나 고정을 해제 |
| watcher overflow, rename 불명 또는 branch/worktree 전환 | `reconciling` 또는 verified gap | 부분 file event로 rename/delete 또는 current state를 추정하지 않음 | bounded whole-workspace reconciliation과 epoch 검증 |
| benchmark evidence 누락 | evaluation incomplete, not release ready | 기본 합격 metric과 GA 선언 없음 | versioned corpus/profile 실행 |
| incompatible string epoch artifact | historical/incompatible | integer epoch로 silent coercion 없음 | 원본 보존 후 새 live epoch 재생성 |
| 선택 step의 정확한 statement anchor가 없거나 enclosing scope와 같은 넓은 anchor | `unavailable` 또는 `unknown` precision과 bounded context | function/class 전체를 선택 step Evidence 또는 verified statement로 강조하지 않음 | adapter source reanalysis 또는 정확한 anchor를 가진 새 generation |

## 9. 결정

전체 맥락, 대안, 근거와 결과는 결정 기록에 있다.

| ID | 결정 | 근거 | 결과 |
|---|---|---|---|
| `D1–D6` | 사용자 이해와 task scope, authority separation을 제품 기준으로 사용 | Raw §0–§6 | Fact, meaning, approval을 분리한다. |
| `D7–D10` | immutable edit/snapshot, current 또는 gap, restricted late refinement | Raw §6–§7 | stale publication을 차단한다. |
| `D11–D15` | 동일 IR의 6개 mode와 Evidence 기반 alignment | Raw §8–§12 | mode별 파사드와 무근거 confirmed를 금지한다. |
| `D16–D20` | component는 계약으로 평가하고 publication/approval authority를 분리 | Raw §13, §21 | 기존 구현 존재는 재사용·완료 근거가 아니다. |
| `D21–D24` | 상태 축, typed query, closed closure와 proof를 강제 | Raw §7–§10 | currentness를 quality나 UI 상태로 추정하지 않는다. |
| `D25–D28` | schema/validator, publication과 settlement 분리, P95와 correctness 분리 | Raw §10, §16, §18 | schema success와 latency가 semantic gate를 대체하지 않는다. |
| `D29–D32` | intent lifecycle, degradation, obligation, complete IR/projection 분리 | Raw §10, §18 | false-complete와 critical boundary 손실을 막는다. |
| `D33` | `workspaceEpoch`를 non-negative integer로 통일 | 사용자 결정 2026-09-04 | incompatible string artifact는 보존 후 live state를 재생성한다. |
| `D34` | 로컬 Semantic Approval은 actor 인증과 durable append-only log를 요구 | 사용자 결정 2026-09-04 | cryptographic signature는 현재 계약의 비목표다. |
| `D35` | 세 차례 audit 뒤 기존 VS 완료·검증 상태를 무효화 | 사용자 결정 2026-09-04 | VS-01~10은 amendment와 독립 review 전까지 Proposed다. |
| `D36` | release gate는 실제 evidence만 소비하고 Raw에 없는 숫자를 만들지 않음 | Raw §16–§18, Review 1–3 | empty/default metric으로 releaseReady를 만들 수 없다. |
| `D37` | Semantic Map 기본 code view는 Flow Context를 표시 | 사용자 결정 2026-09-08 | 정확한 step Evidence와 구조·흐름 관계를 구분하고 full source는 명시적 확장으로 제한한다. |
| `D38` | Static FlowView와 Live Semantic View의 갱신 책임을 분리 | 사용자 결정 2026-09-09 | 자동 변경 감지와 SSE 반영은 `/live`에만 둔다. |
| `D39` | VS Code, coding agent와 watcher fallback은 하나의 change ingress를 사용 | 사용자 결정 2026-09-09 | source별 분석 pipeline이나 workspace lineage를 만들지 않는다. |
| `D40` | filesystem watcher는 누락 복구용 필수 fallback이며 stable capture만 제출 | 사용자 결정 2026-09-09 | watcher event 자체를 source Fact나 current proof로 사용하지 않는다. |
| `D41` | Live View는 proof-backed generation과 Semantic Delta로만 알리고 반영 | 사용자 결정 2026-09-09 | event 뒤 재분석 candidate를 표시하지 않고 읽기 상태를 보존한다. |

## 10. 품질 제약

| 지표 | 계약 |
|---|---|
| edit accepted → activity 표시 | 지원 환경에서 end-to-end P95 300ms 이하 |
| relevant edit → current verified 또는 explicit gap 표시 | 지원 환경에서 `edit.capture`부터 `ux.acknowledge`까지 동일 trace P95 3초 이하 |
| proof 없는 current publication | 0건 |
| gate 미통과 settlement publication | 0건 |
| model 없는 deterministic fallback | 지원 capability에서 100% 가능 |
| generation update의 selected step/scroll 보존 | 99% 이상 |

precision, recall, critical semantic closure 시간과 resource bound는 먼저 실제 분포를 측정한다. 새로운 GA threshold는 release profile과 별도 사용자 결정을 통해서만 추가한다.

## 11. Feature-Level Acceptance

- `FA-01`: THE system SHALL mode별 query의 필수 시작 조건을 검증하고 누락, 모호함, 비교 불가와 unsupported capability를 typed error로 반환한다.
- `FA-02`: WHEN a query is valid, THE system SHALL entry부터 result까지의 complete task-scoped flow, Evidence, coverage와 unknown을 같은 generation에서 반환한다.
- `FA-03`: WHEN an edit is accepted, THE system SHALL immutable Document Revision과 complete Workspace Snapshot을 즉시 만들고 activity를 표시한다.
- `FA-04`: THE system SHALL hot-path source와 configuration을 한 snapshot VFS에서만 읽고 repository root escape와 post-snapshot OS read를 거절한다.
- `FA-05`: THE system SHALL every adapter result에 동일 integer epoch와 basis의 read set, negative observation, membership, frontier, capability와 closure status를 포함한다.
- `FA-06`: IF an adapter cannot close its observation scope, THEN THE system SHALL return an open closure와 incomplete reason and SHALL NOT publish the result as current.
- `FA-07`: WHEN the scheduler selects a quiet or max-wait checkpoint, THE system SHALL automatically run incremental compile, validate and publish-or-gap processing without a manual view request.
- `FA-08`: WHEN edits continue, THE system SHALL prefer the latest checkpoint while preserving revision, delete, rename and public-contract change information.
- `FA-09`: THE system SHALL validate repository, worktree, epoch, intent, query, basis, closure, generation, artifact refs와 schema identity before current publication.
- `FA-10`: IF any Current Publication subgate or captured live-head comparison fails, THEN THE system SHALL keep the candidate historical and expose a latest-vs-verified gap with measured lag, pending revisions, affected scope and causes.
- `FA-11`: WHEN publication succeeds, THE system SHALL atomically commit manifest, active pointer and sequenced event ledger and emit `generation.published` only after commit.
- `FA-12`: IF active-pointer CAS fails, THEN THE system SHALL emit no published event and revalidate closure against the new head before retry.
- `FA-13`: WHEN a same-basis late result arrives, THE system SHALL rerun all applicable closure, delta, epoch, intent, query, live-head and previous-generation checks.
- `FA-14`: THE system SHALL publish Q1/Q2 only as `settlement=pending` and SHALL pass settlement only under `HINV-25`.
- `FA-15`: WHEN Q4 refinement is produced, THE system SHALL preserve Q3 Fact, obligations, alignment and settlement byte-for-byte or by canonical digest.
- `FA-16`: THE system SHALL preserve source and target step identities on every graph edge and compute impact/failure results from those relations.
- `FA-17`: IF direct or indirect graph coverage ends, THEN THE system SHALL identify the frontier, unknown relation and whether bounded exploration remains available.
- `FA-18`: THE system SHALL create runtime-observed or corroborated statements only from scoped runtime Evidence and SHALL represent unobserved behavior as unknown.
- `FA-19`: THE system SHALL confirm Requirement Alignment only when current verified Evidence supports the criterion and SHALL NOT promote agent/model text.
- `FA-20`: THE system SHALL calculate source ranges from snapshot bytes and reject invalid anchors rather than fabricate verified snippets.
- `FA-21`: THE system SHALL apply canonical path validation and key-aware/value-aware secret redaction before persistence or egress.
- `FA-22`: WHEN onboarding is requested, THE system SHALL require repository identity and derive domains, representative flows and coverage boundaries from current analysis evidence.
- `FA-23`: WHEN semantic approval is requested, THE system SHALL validate an existing proposal, verified pack, basis, intent revision, actor and idempotency key before append-only persistence.
- `FA-24`: THE system SHALL keep approval/proposal, Requirement Alignment, Fact validation, freshness and settlement as separate authority states.
- `FA-25`: THE system SHALL use a single non-negative integer `workspaceEpoch` contract and reject incompatible string epoch payloads at the new schema boundary.
- `FA-26`: WHEN epoch changes, THE system SHALL make the prior active generation historical and SHALL NOT compare or merge incompatible artifacts as current.
- `FA-27`: THE system SHALL provide monotonically sequenced EventEnvelope replay, duplicate elimination and full-manifest recovery after an unrecoverable gap.
- `FA-28`: THE system SHALL keep long-lived SSE usable without server-wide write timeout termination and SHALL expose disconnect/replay state.
- `FA-29`: IF benchmark corpus, profile, trace or required gate evidence is absent, THEN THE system SHALL return incomplete/not-ready and SHALL NOT synthesize passing metrics.
- `FA-30`: THE system SHALL declare capability state from reproducible versioned evidence and SHALL NOT report unverified behavior as supported, GA, implemented or complete.
- `FA-31`: THE system SHALL preserve source read-only behavior, runtime consent/isolation, consistently negotiated message/resource bounds and bounded diagnostics redacted before clipping under success and failure.
- `FA-32`: THE system SHALL pass race-enabled concurrency verification for scheduler, storage, SSE and approval paths without data races or leaked workers.
- `FA-33`: WHEN comparing compatible generations, THE system SHALL classify added, changed, removed and structural-only behavior, preserve sound delete/add evidence for rename, and expose an ambiguous move when stable identity cannot be proven.
- `FA-34`: WHEN a user selects a Semantic Map step, THE system SHALL show the exact statement Evidence, its enclosing condition, callback or builder context when present, the enclosing callable signature, and a direct predecessor, successor or call relation from the same snapshot. IF no direct enclosing structure exists, THEN THE system SHALL state that absence. Full callable and file source SHALL require explicit user expansion.
- `FA-35`: IF a same-snapshot adapter cannot prove that an anchor is a statement node, or the anchor is no narrower than its enclosing callback, condition, builder, function or class, THEN THE system SHALL show `unavailable` or `unknown` precision and may show bounded context, but SHALL NOT highlight that enclosing scope as exact step Evidence or represent it as verified.
- `FA-36`: THE system SHALL keep Static FlowView free of workspace stream subscription, edit notifications and automatic generation replacement.
- `FA-37`: WHEN VS Code, a coding agent or watcher fallback observes a supported workspace change, THE system SHALL submit it through one idempotent versioned workspace change ingress and preserve its source and batch identity.
- `FA-38`: IF direct IDE or agent submission is absent, THEN THE system SHALL detect supported filesystem changes through a coordinator-owned watcher fallback and SHALL submit only stable capture or reconciled workspace state.
- `FA-39`: WHEN `generation.published` is delivered to Live Semantic View, THE system SHALL fetch and render only the proof-backed view whose generation, basis and snapshot identities match the event.
- `FA-40`: WHEN a verified Semantic Delta is available, THE Live Semantic View SHALL identify the affected behavior or relation and preserve the user's reading state; IF current verification fails, THEN it SHALL retain the last verified generation and show an explicit gap state.

## 12. Open Decisions

Blocking Open Decisions: 없음.

Non-blocking follow-up decisions must be made before the affected capability can become GA:

- release별 supported environment와 benchmark corpus profile
- measured precision, recall, comprehension과 resource thresholds
- remote multi-user approval 또는 cryptographic non-repudiation 필요 여부
- incompatible persisted epoch artifact의 retention duration과 user-facing history support
- `VS-11`의 사용자 명시 승인

이 follow-up은 해당 capability를 `experimental`, `partial` 또는 `unsupported`로 유지하며 parent intent hardening을 막지 않는다.

## 13. Vertical Slice Plan

세 차례 리뷰는 기존 slice의 공통 snapshot, Evidence, publication, state와 release 전제를 무효화했다. 기존 `docs/design/specs/*-vs-*.md`는 superseded historical contract다. Slice Set Revision `R2`의 다음 `.tasks/` 계약만 active child set이며, 기존 Contract ID와 evidence를 재사용하지 않는다.

| Slice | Stable Contract ID | Contract path | Primary goal | User outcome | Dependencies | Parent acceptance | Status |
|---|---|---|---|---|---|---|---|
| VS-01 | `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-01` | `.tasks/2026-09-02-requested-flow-live-semantic-compiler-ko/vs-01-see-one-immutable-workspace-state-ko.md` | `GOAL-04` | accepted edit를 하나의 불변 workspace 상태로 본다. | 없음 | FA-03, FA-04, FA-25, FA-26, FA-31 | Implemented, verified 2026-09-04 |
| VS-02 | `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-02` | `.tasks/2026-09-02-requested-flow-live-semantic-compiler-ko/vs-02-receive-honest-analyzer-evidence-ko.md` | `GOAL-01` | 동일 snapshot의 정직한 analyzer Evidence와 coverage를 받는다. | VS-01 | FA-04–FA-06, FA-20, FA-21, FA-31 | Implemented, verified 2026-09-04 |
| VS-04 | `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-04` | `.tasks/2026-09-02-requested-flow-live-semantic-compiler-ko/vs-04-understand-grounded-flow-and-change-ko.md` | `GOAL-01` | explicit immutable basis에서 complete grounded flow와 semantic change를 이해한다. | VS-02 | FA-01, FA-02, FA-14–FA-16, FA-19, FA-20, FA-24, FA-33 | Implemented, verified 2026-09-04 |
| VS-03 | `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-03` | `.tasks/2026-09-02-requested-flow-live-semantic-compiler-ko/vs-03-see-automatic-current-or-gap-ko.md` | `GOAL-05` | 편집 뒤 validated flow를 자동 current로 발행하거나 측정된 gap을 본다. | VS-01, VS-02, VS-04 | FA-03, FA-07–FA-15, FA-19, FA-27, FA-28, FA-32, FA-36–FA-40 | Superseded for new Live project-change behavior by `docs/design/specs/2026-09-10-live-project-change-awareness-ko.md` (2026-09-10) |
| VS-05 | `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-05` | `.tasks/2026-09-02-requested-flow-live-semantic-compiler-ko/vs-05-trace-change-impact-ko.md` | `GOAL-02` | 실제 graph에서 변경 영향을 추적한다. | VS-04 | FA-01, FA-16, FA-17, FA-20, FA-21 | Implemented and verified (2026-09-05) |
| VS-06 | `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-06` | `.tasks/2026-09-02-requested-flow-live-semantic-compiler-ko/vs-06-investigate-evidence-bounded-failure-ko.md` | `GOAL-02` | Evidence 범위 안에서 failure를 조사한다. | VS-04 | FA-01, FA-16–FA-18, FA-20, FA-21, FA-31 | Implemented and verified (2026-09-05) |
| VS-07 | `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-07` | `.tasks/2026-09-02-requested-flow-live-semantic-compiler-ko/vs-07-explore-evidence-backed-domains-ko.md` | `GOAL-02` | repository Evidence에서 domain 책임을 탐색한다. | VS-04 | FA-01, FA-17, FA-20–FA-22 | Implemented and verified (2026-09-05) |
| VS-08 | `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-08` | `.tasks/2026-09-02-requested-flow-live-semantic-compiler-ko/vs-08-receive-grounded-semantic-proposal-ko.md` | `GOAL-03` | 근거 있는 optional semantic proposal 또는 fallback을 받는다. | VS-03, VS-04 | FA-18–FA-21, FA-24, FA-31 | Implemented and verified (2026-09-06) |
| VS-09 | `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-09` | `.tasks/2026-09-02-requested-flow-live-semantic-compiler-ko/vs-09-approve-semantic-meaning-durably-ko.md` | `GOAL-03` | 실제 proposal의 의미 표현을 durable하게 승인한다. | VS-03, VS-08 | FA-23, FA-24, FA-31, FA-32 | Implemented and verified (2026-09-07) |
| VS-10 | `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-10` | `.tasks/2026-09-02-requested-flow-live-semantic-compiler-ko/vs-10-declare-capability-from-evidence-ko.md` | `GOAL-06` | 실제 실행 근거로 capability를 선언한다. | VS-01–VS-09 | FA-29–FA-32와 FA-01–FA-33 release verification | Evaluator implemented and verified (2026-09-07); production evidence pending |
| VS-11 | `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-R2-VS-11` | `.tasks/2026-09-02-requested-flow-live-semantic-compiler-ko/vs-11-see-precise-flow-code-context-ko.md` | `GOAL-01` | 선택한 Semantic Map step의 정확한 실행 코드와 구조·흐름 문맥을 구분해 본다. | VS-02, VS-04 | FA-20, FA-34, FA-35 | Approved (2026-09-08 user instruction) |

실행 순서는 `VS-01 → VS-02 → VS-04 → VS-03 → VS-05/06/07/08 → VS-09 → VS-10`이며, VS-11은 VS-02와 VS-04 뒤에 독립적으로 실행할 수 있다. VS-04는 explicit immutable basis의 deterministic canonical IR만 소유하고 current authority를 갖지 않는다. VS-03은 그 IR과 proof를 검증해 current 또는 gap으로 발행한다. Current Evidence Pack을 요구하는 VS-08은 VS-03 뒤에 실행되므로 dependency cycle이 없다.

적용 규칙:

- 각 slice는 `Intent → Goal → Parent Acceptance → Slice Acceptance → Evidence`를 유지한다.
- Independent Review가 통과해도 자동 승인되지 않는다. 사용자가 선택한 slice를 명시적으로 승인해야 구현할 수 있다.
- VS-01–VS-10 R2 implementation conformance는 검증됐다. VS-10의 synthetic evidence는 evaluator 동작만 증명하며, production `releaseReady`에는 실제 profile/corpus 실행과 승인된 측정 threshold가 계속 필요하다.
- 2026-09-09 Live change-ingress amendment는 VS-03의 public seam, accepted edit 정의와 Live update rendering을 확장한다. 기존 VS-03 evidence는 `FA-36`–`FA-40`을 증명하지 않으므로 VS-03은 amendment와 independent review 전까지 Proposed다.
- Flow Context amendment는 별도 Proposed Vertical Slice와 independent review, 사용자 명시 승인 전까지 구현하지 않는다.
- `current`, `settled`, `runtime_observed`, `confirmed`, `releaseReady`는 해당 slice evidence 없이 신뢰 가능한 제품 상태로 선언할 수 없다.

## 14. 세 차례 리뷰 추적

| Review scope | 통합된 결함 | 집행 불변식 | Acceptance |
|---|---|---|---|
| `RR-01` Snapshot truth | Review 1 F1; Review 2 F1, F11–F12, F18; Review 3 DEF-04, DEF-14 | `HINV-01`–`HINV-06` | `FA-03`–`FA-06`, `FA-20`, `FA-25`–`FA-26` |
| `RR-02` Closure truth | Review 2 F2, F8; Review 1 F2 | `HINV-07`–`HINV-09` | `FA-05`–`FA-06`, `FA-09`–`FA-10` |
| `RR-03` Evidence and security | Review 1 F4, F6; Review 2 F6, F9–F10, F15; Review 3 DEF-02–DEF-04, DEF-07–DEF-08 | `HINV-10`–`HINV-13`, `HINV-16`, `HINV-18` | `FA-18`–`FA-21`, `FA-23`–`FA-24`, `FA-31` |
| `RR-04` Graph and task views | Review 1 F5; Review 2 F13–F17, F22–F23; Review 3 DEF-05–DEF-07; FlowView code-context user decision 2026-09-08 | `HINV-14`–`HINV-20`, `HINV-37`–`HINV-39` | `FA-16`–`FA-22`, `FA-33`–`FA-35` |
| `RR-05` Publication and settlement | Review 1 F2, F4; Review 2 F3–F5, F19–F20; Review 3 DEF-09, DEF-20–DEF-21 | `HINV-21`–`HINV-30` | `FA-07`–`FA-15`, `FA-25`–`FA-26` |
| `RR-06` Streaming and concurrency | Review 2 F21; Review 3 DEF-10–DEF-15; Live change-ingress user decision 2026-09-09 | `HINV-29`–`HINV-31`, `HINV-40`–`HINV-46` | `FA-07`–`FA-08`, `FA-27`–`FA-28`, `FA-32`, `FA-36`–`FA-40` |
| `RR-07` Semantic Approval | Review 1 F7; Review 2 F16; Review 3 §2 and DEF-02 | `HINV-32`–`HINV-34` | `FA-23`–`FA-24` |
| `RR-08` Release truth | Review 1 F3; Review 2 F7, F24; Review 3 §4 and DEF-01 | `HINV-35`–`HINV-36` | `FA-29`–`FA-31` |
| `RR-09` Contract lifecycle | Review 1 F8; Review 2 F25; all review conclusions | D35 and §13 | child amendment, independent review and explicit approval |

Review No.3의 `500ms`, precision `0.85`와 cryptographic signature 제안은 Raw 계약이 아니므로 각각 D36의 evidence-only threshold 정책과 D34의 현재 보안 경계로 정리했다. Review에서 숫자로 주장된 전체 결함 건수는 첨부 본문에 모든 개별 항목이 없으므로 acceptance count로 사용하지 않는다.

## 15. Done When

이 feature는 다음 evidence가 모두 있을 때만 delivered 또는 GA로 선언할 수 있다.

- `FA-01`–`FA-40` 각각에 정상, invalid, concurrent, stale, partial-failure와 recovery evidence가 연결된다.
- 모든 boundary, persistence, CAS, hash와 external publication payload가 registry schema, valid/invalid fixture와 cross-artifact Semantic Validator를 가진다.
- rapid edit, multi-file transaction, rename/delete, syntax error, branch switch, watcher gap, open closure, late result, CAS conflict, adapter/model crash와 reconnect trace가 재현 가능하다.
- activity P95 300ms와 current-or-gap P95 3초가 declared profile의 동일 end-to-end trace 분포에서 측정된다.
- false-current, false-settlement, fabricated runtime Evidence, secret/path leak와 release self-certification이 adversarial test에서 0건이다.
- Go, Dart, TypeScript/JavaScript adapter, MCP/REST/SSE, browser UX와 accessibility의 applicable verification plan이 통과한다.
- race-enabled scheduler, storage, streaming과 approval tests가 통과하고 failure recovery에서 worker/process leak가 없다.
- child slice complete set이 독립 review를 통과하고 사용자가 각 Proposed slice를 명시적으로 승인한다.
- 미측정 capability는 GA 또는 complete로 표시되지 않는다.
