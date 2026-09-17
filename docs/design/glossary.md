# CodeFlow 설계 용어집

## Official Product Terms

| Term | Operational Definition | Korean Term |
|---|---|---|
| FlowView Storyboard | 개발자가 코드 동작을 직관적으로 이해할 수 있도록 비즈니스 실행 흐름을 4~7개 핵심 관문과 1:N 미시 타임라인으로 요약·시각화하는 CodeFlow의 정본 인터랙티브 워크벤치. | FlowView 스토리보드 |
| FlowSequence | 사용자가 이해하려는 코드 동작을 시작, 판단, 처리, 외부 효과, 결과 또는 분석 경계 관문으로 묶어 보여 주는 FlowView의 기본 프로젝션 모델. | 플로우시퀀스 |
| FlowSequenceFrame | FlowSequence의 거시적 비즈니스 관문 단위 (`entry`, `decision`, `process`, `effect`, `result`, `boundary`). | 비즈니스 관문 프레임 |
| Execution Timeline | 각 비즈니스 관문 내부에 1:N으로 보존되는 구체적 코드 라인 실행 단계 모음 (`SemanticStep`: `call`, `guard`, `mutation`, `result`). | 실행 타임라인 |
| Anti-Telemetry Guard | 에포크, 지연 시간(lag), 내부 결재 락 등 엔진 내부 텔레메트리가 기본 화면이나 사용자 설명에 노출되지 않도록 차단하는 원칙. | 안티 텔레메트리 가드 |
| Content-Addressable Storage | 내용의 해시(SHA-256)가 객체 식별자를 결정하고 영속 루트가 수명주기를 결정하는 불변 객체 저장소. | 콘텐츠 주소 기반 저장소 |
| Live Semantic Compiler | [Deprecated / Superseded] 과거 실시간 파일 감시 및 자동 컴파일러 모델. 현재는 사용자 명시적 1회성 재분석 및 기준선 비교 기반의 FlowView 코드 이해 체제로 전면 대체됨. | [레거시] 실시간 의미 컴파일러 |
| Live Project Change Analysis | [Deprecated / Historical] 과거 실시간 프로젝트 변경 분석 모드. FlowView 코드 이해 체제로 대체됨. | [레거시] 실시간 프로젝트 변경 분석 |

FlowSequence는 사용자가 이해하려는 코드 동작을 핵심 관문과 실행 단계의 관계로 표현하는 FlowView의 기본 화면 모델이다. architecture는 관문을 구성하는 기준이 아니라, 관측된 경우에만 보조 정보로 표시한다.

## Core Semantic Terms

| Term | Operational Definition | Scope | Source | Status | Related Contract |
|---|---|---|---|---|---|
| Task Intent | 불변 `rawRequest`, 정규화된 목적·결과·수용 조건·scope hint와 `parsed`, `needs_confirmation`, `user_confirmed` lifecycle을 가진 versioned 사용자 의도다. 구현 완료 상태와 분리한다. | task-scoped query와 requirement alignment | Raw §3.1, §10.1, D29 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |
| FlowSequence | 사용자가 이해하려는 코드 동작을 시작, 판단, 처리, 외부 효과, 결과 또는 분석 경계 관문으로 묶어 보여 주는 FlowView 기본 projection이다. 관문은 함수·파일·architecture와 일대일 대응하지 않는다. | FlowView 기본 화면 | 사용자 지시 2026-09-16 | Confirmed | `docs/design/specs/2026-09-16-codeflow-architectural-reset-and-flow-sequence-spec-ko.md` |
| Semantic Labeler | 정적 FlowSequence frame에 검증 사실을 바꾸지 않는 의미 라벨 제안을 연결하는 선택적 port다. | FlowSequence 의미 제안 | 사용자 지시 2026-09-16 | Confirmed | `docs/design/specs/2026-09-16-codeflow-architectural-reset-and-flow-sequence-spec-ko.md` |
| SLM Labeler | Semantic Labeler 계약을 로컬 SLM HTTP runtime으로 구현한 adapter다. | 선택적 의미 라벨 제안 | 사용자 지시 2026-09-16 | Confirmed | `docs/design/specs/2026-09-16-codeflow-architectural-reset-and-flow-sequence-spec-ko.md` |
| Exact Step Evidence | 선택한 flow step을 실행하는 statement node임을 같은 snapshot에서 adapter가 검증한 byte range다. enclosing callback, condition, builder, function 또는 class 전체 범위는 이 Evidence가 아니다. 정확한 statement를 구별할 수 없으면 `unavailable` 또는 `unknown`이다. | Semantic Map code view | D37 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |
| Flow Context | Exact Step Evidence, 존재할 때 이를 감싸는 condition·callback·builder, enclosing callable signature와 직접 predecessor/successor 또는 call relation을 함께 보여 주는 코드 이해용 projection이다. 직접 enclosing 구조가 없으면 그 부재를 표시한다. 전체 callable 또는 file은 명시적 확장으로만 연다. | Semantic Map code view | D37 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |
| Workspace Epoch | 서로 current로 비교·재사용할 수 있는 repository와 worktree lineage를 식별하는 0 이상의 durable integer다. 같은 lineage에서 단조 증가하고 branch, worktree 또는 incompatible configuration/toolchain 전환 시 새 값이 필요하다. snapshot, generation, event, document sequence와 별개다. | 모든 canonical snapshot, analysis, map, proof, pointer와 event boundary | D33, user decision 2026-09-04 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |
| Document Revision | 한 repository-relative path와 document version의 불변 bytes를 content ID로 식별한 객체다. | edit ingress와 snapshot VFS | Raw §3.7, §10.2 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |
| Workspace Snapshot | 특정 epoch와 sequence에서 repository/worktree 전체 path를 불변 Document Revision에 연결한 persistent map이다. 분석 시작 뒤 OS filesystem을 다시 읽어 완성하지 않는다. | hot-path analysis basis | Raw §3.8, §7.1, D8, D18 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |
| Causal Observation Closure | 결과를 바꿀 수 있는 positive dependency, negative lookup, membership, dependency frontier, graph/index/configuration/capability revision과 coverage boundary를 기록한 currentness 계약이다. | publication validation | Raw §3.10, §7.4, §10.5, D23 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |
| Open Closure | required observation을 완성하지 못한 closure다. unknown과 coverage를 설명할 수 있지만 current proof에는 사용할 수 없다. | adapter result와 publication rejection | Raw §7.4, §10.5 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |
| Current | Generation Proof Manifest가 declared task, query, capability와 coverage 안에서 captured latest Workspace Snapshot과 모순되지 않음을 증명한 freshness 상태다. 전체 repository completeness 또는 settlement를 뜻하지 않는다. | active generation과 UX | Raw §7.3–§7.7, D24 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |
| Latest-vs-Verified Gap | 최신 snapshot을 current로 검증할 수 없을 때 마지막 검증 generation, 최신 snapshot, affected scope, lag, pending revision과 conflict reason을 함께 표시하는 상태다. | live UX와 failure recovery | Raw §7.3–§7.4 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |
| Critical Obligation | mode별로 완료 판정 전에 반드시 verified여야 하는 entry, result, branch, effect, failure, requirement link 등의 항목이다. | Settlement Gate | Raw §3.17, §10.16, D31 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |
| Settlement | 요청 흐름의 critical completion 상태다. Q1·Q2는 pending이며 Q3 이상에서 모든 required obligation verified, critical unknown 0, conflict 0일 때만 passed다. freshness와 별개다. | SemanticMapIR quality state | Raw §10.10–§10.11, §18.1, D27, D31 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |
| Semantic Approval | 인증된 로컬 사용자가 실제 proposal의 의미 표현을 특정 basis와 Task Intent revision에 대해 승인, 수정 후 승인, 거절, 취소 또는 대체한 append-only event다. Fact, Evidence, Requirement Alignment, freshness와 settlement를 변경하지 않는다. | optional model enrichment와 curated product language | Raw §10.9, D20, D34 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |
| Generation Proof Manifest | 한 generation의 map, delta, evidence, projection, closure와 gate 결과를 computed basis, validated head와 Content-Addressable Storage 조건에 연결하는 canonical proof다. | atomic publication과 query | Raw §3.11, §10.11 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |
| Workspace Change Ingress | [Deprecated] 과거 실시간 파일 감시 및 편집 수집 경계. FlowView 사용자 명시적 1회성 분석으로 대체됨. | Live Semantic View의 edit-driven snapshot 생성 | D39, D40 | Deprecated / Removed | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
| Watcher Fallback | [Deprecated] 파일시스템 이벤트를 감시하던 과거 기능. 에이전트 및 사용자의 명시적 요청 기반 분석으로 대체됨. | Live Semantic View의 변경 누락 복구 | D40 | Deprecated / Removed | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
| Generation-bound Live View | [Deprecated] 실시간 SSE 이벤트 기반 뷰 응답. 단일 Svelte 5 워크벤치(FlowView Storyboard)로 통합 대체됨. | 검증된 변경 반영과 reconnect | D41 | Deprecated / Removed | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
| Live Project Change Analysis | 기능 요청 없이 프로젝트 코드 변경을 감시하고, 재시작 시 마지막 검증 분석 기준과 현재 source의 차이를 검증해 표시하는 사용 모드다. 사용자 읽음·미확인 이력을 저장하거나 표시 기준으로 사용하지 않는다. | 기존 Live 계약·데이터 호환. 신규 FlowView 필수 architecture 아님 | §2·§7–§8 | Historical / compatibility only | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
| Live Analysis Basis | 마지막으로 검증된 분석에 결합된 snapshot·configuration·dependency·capability identity다. 성공한 publication만 전진시키며 candidate basis, 화면 baseline, 수집 head와 구별한다. 재시작 시 현재 stable source와 비교한다. | 기존 Live 계약·데이터 호환. 신규 FlowView 필수 architecture 아님 | §2·§7–§8 | Historical / compatibility only | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
| Logical Live coordinator | 특정 프로젝트의 workspace lineage, watcher, scheduler, event stream과 stale recovery를 단일 authority로 소유하는 논리 서비스다. MCP process는 이 coordinator의 producer이며 별도 authority가 아니다. | 기존 Live 계약·데이터 호환. 신규 FlowView 필수 architecture 아님 | §2·§7–§8 | Historical / compatibility only | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
| Semantic Change Batch | 안정된 Change Episode에서 facts·Evidence로 검증한 behavior·condition·state·external effect·relationship 변화와 unknown을 비교별로 묶은 출력이다. 시간적 근접성만으로 같은 업무 변화라고 확정하지 않는다. | 기존 Live 계약·데이터 호환. 신규 FlowView 필수 architecture 아님 | §2·§7–§8 | Historical / compatibility only | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
| Change Episode | 관련 편집을 안정화된 하나의 변경 묶음으로 표현한 객체다. 파일 이벤트 목록이 아니라 baseline·current 비교와 Semantic Delta 분석의 입력이다. | 기존 Live 계약·데이터 호환. 신규 FlowView 필수 architecture 아님 | §2·§7–§8 | Historical / compatibility only | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
| Comparison Root | 한 Live View 비교의 baseline/current generation·basis·comparison proof·Evidence와 lease를 보존하는 versioned root다. 전역 last-verified와 독립적이며 pending은 최신 후보만 가리킨다. | 기존 Live 계약·데이터 호환. 신규 FlowView 필수 architecture 아님 | §2·§7–§8 | Historical / compatibility only | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
| Comparison Proof | 이미 검증된 baseline/current generation에 대해 delta·Evidence·projection과 판정 profile을 결합한 비교 증명이다. project last-verified를 갱신하지 않는다. | 기존 Live 계약·데이터 호환. 신규 FlowView 필수 architecture 아님 | §2·§7–§8 | Historical / compatibility only | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
| Release Ready | 승인된 release profile의 versioned corpus에서 contract, correctness, security, resilience, comprehension, semantic quality와 end-to-end SLO evidence를 모두 충족한 상태다. 입력이나 threshold가 없으면 false다. | capability declaration | Raw §16–§18, D36 | Confirmed | `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md` |

## Architectural Modernization & Target Blueprint

| Term | Operational Definition | Scope | Source | Status | Related Contract |
|---|---|---|---|---|---|
| Hexagonal Architecture | 각 vertical feature slice 안에서 Domain Core와 외부 adapter를 Ports & Adapters 경계로 분리하는 구조. 전체 시스템에 하나의 거대한 계층을 만드는 방식이 아니다. | 전체 CodeFlow 서브시스템 | §2·§7–§8 | Confirmed | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
| Vertical Feature Slice | 하나의 기능을 입력, domain rule, application use case, port, adapter와 contract test까지 함께 소유하며 다른 slice와 공개 contract로만 연결하는 독립 기능 단위. | 전체 CodeFlow 서브시스템 | §2·§7–§8 | Confirmed | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
| CompilerService | 여러 vertical slice를 정해진 순서로 조합하고 cancellation, conflict retry와 publication을 조정하는 단일 Application coordinator. 개별 분석·표시·저장 규칙은 각 slice가 소유한다. | CLI, FlowView, MCP | §2·§7–§8 | Confirmed | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
| Epistemic Segregation | 결정론적 AST 사실, SLM의 확률적 제안, 런타임 관측치, 인간 승인자 서명을 데이터 구조 및 런타임 수준에서 엄격히 분리하는 불변식. | SemanticStep, ModelProposal, Approval | Raw §9, §10, ARCHITECTURE.md §1 | Confirmed | `docs/design/specs/2026-09-04-codeflow-architectural-modernization.md` |
| Content-Addressable Storage Dual-Namespace | 과거의 `.codeflow/cas/blobs/`와 `.codeflow/cas/manifests/` 분리 결정. 현재 기준에서는 content, tree, revision, snapshot, episode, semantic, proof의 typed object namespace와 참조 그래프의 하위 규칙이다. | 기존 Live 계약·데이터 호환. 신규 FlowView 필수 architecture 아님 | §2·§7–§8 | Historical / compatibility only | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
| Egress Redaction | FlowView HTTP 응답, 실시간 SSE 이벤트, MCP JSON-RPC 표준 출력 등 시스템 외부로 나가는 모든 데이터 스트림에 시크릿 마스킹 필터를 적용하는 보안 경계. | Presentation Layer, Security Filter | ARCH-D10, ARCHITECTURE.md §3.3 | Confirmed | `docs/design/specs/2026-09-04-codeflow-architectural-modernization.md` |
| Process Group Isolation | 언어 어댑터 서브프로세스 스폰 시 `Setpgid: true`를 강제하고 종료 시 음수 PID(`-cmd.Process.Pid`)로 시그널을 전달하여 좀비 프로세스 누수를 원천 차단하는 OS 레벨 프로세스 격리 기법. | Protocol Pool, Subprocess Lifecycle | ARCH-D12, ARCHITECTURE.md §3.3 | Confirmed | `docs/design/specs/2026-09-04-codeflow-architectural-modernization.md` |
| Server-Sent Events | 서버가 HTTP 연결을 통해 event를 보내는 전송 방식이다. 기존 사용은 호환성 대상이며 새 FlowView의 상시 갱신을 요구하지 않는다. | 기존 event transport | §2·§7–§8 | Confirmed | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |

## Storage Efficiency

| Term | Operational Definition | Scope | Source | Status | Related Contract |
|---|---|---|---|---|---|
| Storage Retention Budget | active pointer, last-verified, active comparison root와 lease를 보존하면서 무참조 객체만 정리하는 참조 기반 용량·개수 보존 메커니즘이다. 숫자 기본값은 설정으로 조정한다. | Content-Addressable Storage GC, `codeflow gc`, `doctor` | §2·§7–§8 | Confirmed | `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` |
