# CodeFlow v2 설계 문서 — Business Flow First Engine

> 상태: **REVIEWED** — 외부 리뷰 P1 게이트 전부 해소(R1/R10/R7 사용자 결정 완료, 나머지 채택 반영). M1 착수 가능.
> 기반: `Flutter_Business_Flow_Specification.md` draft 1.0.0 + v1 한계 분석
> 갱신일: 2026-08-24

---

## 1. 제품 정의

### 1.1 한 문장 정의

**CodeFlow v2는 Flutter/Dart 코드베이스에서 비즈니스 흐름(Trigger → Business Rule → State Mutation → Side Effect)을 end-to-end로 추출해, 개발자가 구현된 코드를 빠르게 이해할 수 있도록 FlowView로 명쾌하게 요약·낭독하게 하는 로컬 도구다.**

설계 판단 기준은 하나다: **비즈니스 흐름을 명확히 이해하고, 그것을 코드로 명쾌하게 요약·설명하는 구조.**
이 기준에 도움 되지 않는 것(HTTP 클라이언트 내부, 직렬화 등 기술 세부)은 흐름에서 배제하되,
흐름을 **전반적으로 파악하는 데 필요한 요소는 빠짐없이 담는다**(§4.2 Stage 2).

### 1.2 비목표

- 제품 코드 수정 없음 (읽기 전용).
- 클라우드/협업 서버 없음 — 로컬 퍼스트.
- LLM 직접 호출 없음 (§3).
- transcript 로그 자동 파싱 없음 (§8.1 — 검토 결과 제외).
- 전체 흐름 지도 UI 없음 — 요청된 흐름만 렌더 (§9).

### 1.3 설계 원칙

1. **구조는 결정적, 의미는 증거원 융합.** 슬라이싱 결과는 재실행 시 동일하다.
2. **앵커 위조 불가.** 라인 번호가 아닌 canonical AST fingerprint로 검증한다(§7).
3. **확인 못 한 것은 unknown.** 추측으로 흐름을 완성하지 않는다.
4. **같은 입력은 두 번 분석하지 않는다.** 모든 semantic input이 키에 든 캐시(§6).
5. **경계는 버저닝된 JSON Schema다.**
6. **Secret은 진출 지점마다 공통 scanner로 redaction(§11.3).**
7. **제품 코드 읽기 전용.**
8. **publish는 단일 일관 시점만.** 서로 다른 작업트리 시점 혼합 금지(§6.3).

---

## 2. 출발점: v1의 한계와 draft 스펙의 조정

### 2.1 v1이 잘한 것 (계승)

- 현재 작업트리 진실 원칙, 증거 앵커, unknown 보존.
- FlowIR 버저닝 JSON + SQLite snapshot atomic publish.
- ontology 승인 파이프라인(inferred → approved).
- FlowView 세로 낭독 UX와 상단 내비게이션.

### 2.2 v1의 근본 한계 (극복 대상)

1. **코드는 동작은 증명하지만 의도를 증명하지 못한다** — v1은 빈칸을 unknown으로 남겨 기술 사실 나열에 머물렀다.
2. **진입점이 화면(route:) 중심** — 도메인 능력이 아닌 UI 구조가 축이었다.
3. **세맨틱을 채울 에너지원이 없었다.**

### 2.3 draft 스펙에서 가져온 것과 버린 것

| 계승 | 조정 |
|---|---|
| AST statement slicing (guard/mutation/throw/await/call) | 직접 LLM 합성 — **제외** |
| 순환호출 visited-set, depth 5 | trigger를 도메인 마커에서 |
| Secret redaction | 저장 최적화 + 보안 계약 강화(§11.3) |
| Schema 강제 | dashboard → FlowView Flow Story |

---

## 3. 핵심 논제: 세 증거원 융합, LLM 직접 호출 없음

> 비즈니스 흐름의 **구조**는 코드에서, **의미**는 세 증거원의 융합에서 나온다.

| 증거원 | 내용 | 성격 |
|---|---|---|
| **E1 구조 증거** | 언어 어댑터 AST 슬라이싱 — guard/mutation/effect/calls | 결정적, 재현 가능 |
| **E2 의도 증거** | Agent의 **MCP 구조화 제출**(`submit_flow_draft`) — 앵커 있는 journey 초안 | 개발 발생 시 무료 적립 |
| **E3 승인 지식** | 사람이 FlowView에서 in-place 승인한 이름·규칙 | 영속 자산, 최고 권위 |

- 권위 순서: `approved(E3) > session(E2) > derived(E1) > unknown`
- `derived`: 식별자 규칙 변환(`submitOrder` → "주문을 제출한다"). 결정적, LLM 아님.
- **E1 구조 사실은 E3/E2가 덮어쓰지 못한다.** 승인된 설명이 현재 코드와 모순되면
  해당 step은 `stale` 표시(§16 R2) — provenance(누가 만들었나)와 freshness(현재성)는 별개 필드.
- 빈 칸은 unknown으로 남고 E2/E3로만 충전된다. 파이프라인 어디에도 직접 LLM 호출이 없다.

---

## 4. 시스템 아키텍처

```text
                        ┌─────────────────────────────────┐
                        │  Agent Session (Codex 등)        │
                        │  codeflow MCP tools 호출         │
                        └───────────┬─────────────────────┘
                                    │ journey 초안·근거 (구조화 제출)
                                    ▼
[Target Repo] ──▶ ┌──────────────────────────────────────────┐
(read-only)       │  CORE (Go · 단일 네이티브 바이너리)         │
                  │                                          │
                  │ 1. Domain Harvest      ← E1 후보 발굴      │
                  │ 2. Structural Slice    ← E1 (언어 어댑터)   │
                  │ 3. Semantic Fusion     ← E1+E2+E3 통합     │
                  │ 4. Publish             ← FlowIR v2 발행    │
                  └──────┬───────────────────────┬───────────┘
                         ▼                       ▼
                 .codeflow/ (저장)         FlowView (loopback)
                 facts / semantics / ir    Flow Story 낭독 뷰
```

### 4.1 코어 언어: Go

워크로드는 CPU 집약이 아니라 **I/O 오케스트레이션**(서브프로세스 조율, 파일 감시,
JSON 직렬화, localhost serve, SQLite)이다. Go는 단일 정적 바이너리·병렬 I/O·embed FS에서
이 워크로드에 최적이며 성능 병목(어댑터 실행+파일 I/O)과 무관한 영역에서 개발 효율이 가장 높다.

### 4.2 처리 파이프라인

#### Stage 1 — Domain Harvest (결정적)

화면이 아닌 **도메인 마커**에서 흐름 후보 발굴:

| 트리거 클래스 | Dart 예 | Kotlin 예(향후) |
|---|---|---|
| 사용자 행동 | Notifier method, Bloc event handler, route callback | ViewModel method |
| 유스케이스 실행 | `*UseCase.call`, Service public method | `*UseCase`, Interactor |
| 시스템 이벤트 | FCM handler, deep link, lifecycle callback | WorkManager, BroadcastReceiver |
| 상태 전이 지점 | `state.copyWith`, notifier update | StateFlow emit |

후보 규범(R11): root-equivalence dedup(동일 흐름으로 수렴하는 후보 병합),
tie-breaker(마커 구체성 > 선언 순서), 상태변경 지점은 독립 root 후보가 아니라
root 흐름 내부 step으로 귀속.

#### Stage 2 — Structural Slice (E1)

draft 스펙의 슬라이싱 계승:

- 수집: IfStatement(guard), state 계열 AssignmentExpression(mutation), throw, await, business call.
- 폐기(denylist): Widget 생성자, TextStyle/BoxDecoration/EdgeInsets, print/debugPrint, toJson/fromJson.
- 순회 범위 — **직접 참조 심볼 추적**:
  - 시작 파일 안의 호출은 무조건 추적.
  - **import 해석이 확인된(resolved) 직접 참조는 파일이 달라도 따라간다**
    (예: `_useCase.execute` → 필드 타입 `SignupUseCase` 확인 → 해당 파일의 그 메서드).
    Controller→UseCase→Repository가 파일별로 분리된 일반 구조에서도 규칙·상태변경이 누락되지 않는다.
  - 동적/리플렉션/타입 미확인 호출은 따라가지 않고 **unknown edge 카드**로 명시.
  - depth ≤ 5, visited-set 순환 차단, 초과 시 `truncated` 플래그.
- 경계 종료: `Repository`/`ApiClient` 등 경계 마커(profile 선언) 도달 시 **외부 호출 카드**로
  기록하고 종료 — 외부 경계 너머(HTTP 내부, 직렬화)는 비즈니스 흐름 이해에 불필요.
  프로젝트 전체 DAG는 만들지 않는다.
- Secret redaction: `(api[_-]?key|secret|token|password)\s*[:=]\s*['"][^'"]+['"]` → `***REDACTED***`.

산출은 언어 중립 **SlicedPayload**.

#### Stage 3 — Semantic Fusion (E1+E2+E3)

- E2 제출물은 앵커 재검증(§7 anchor 계약) 통과분만 채택.
- 각 step의 이름·규칙·상태델타 해석에 provenance + freshness + confidence 기록.
- 충돌 시 권위 순서 적용하되 E1 구조 사실은 항상 우세(§3).

#### Stage 4 — Publish

FlowIR v2 문서를 flow 단위 발행. 원자성 계약은 §6.3.

---

## 5. 다국어 확장 (Dart 먼저)

### 5.1 Language Adapter 계약

CORE는 언어를 모른다. 어댑터 계약:

```text
LanguageAdapter:
  detect(repo) -> bool
  harvestCandidates(repo) -> Candidate[]
  slice(candidate, opts) -> SlicedPayload
```

### 5.2 어댑터 프로토콜 (NDJSON over stdio)

- 요청 id 상관, watch 모드용 영속 프로세스 풀. 포트 관리 불필요, 플랫폼 공통.
- 운영 계약(R11, normative): 요청별 timeout, cancellation, max-message-size,
  typed error codes, 어댑터 크래시 감지·재시작, backpressure(큐 상한 시 backpressure 에러).

### 5.3 Framework Profile Pack

선언형 YAML로 프레임워크 차이 흡수 — 새 프레임워크 = YAML, 새 언어 = 어댑터 바이너리 + profiles. CORE 무수정.

```yaml
# profiles/riverpod.yaml
domain_markers:
  - pattern: "*Notifier.{method}"
    trigger_class: user_action
state_mutations:
  - lhs_pattern: "state"
  - call_pattern: "copyWith"
boundary_markers:
  - suffix: Repository
  - suffix: ApiClient
ui_noise_denylist: [TextStyle, BoxDecoration, EdgeInsets]
```

### 5.4 로드맵

Dart(Riverpod/Bloc/go_router) → Kotlin(ViewModel/Hilt) → Java(Spring). Kotlin(M6)은 계약의 언어 중립성 검증을 겸한다.

---

## 6. 저장 구조

### 6.1 레이아웃 (v1 계승 + facts/semantics 분리)

```text
.codeflow/
  workspace.json              # repo 매니페스트, 어댑터 pin 버전, basis fingerprint
  facts/                      # E1: 재생성 가능, 캐시 (R4 — 이중 캐시)
    ast/<fileBytesHash+langVer+adapterVer>.json      # 언어 AST fact 캐시
    slice/<sliceCacheKey>.json                       # candidate별 slice 캐시
  semantics/                  # E2+E3: 소중, event-log append, provenance 추적
    events/<seq>.json         #   승인·수정 이벤트 원장 (절대 덮어쓰기 없음)
    view/<flowId>.json        #   이벤트 원장에서 파생한 현재 상태 뷰
  ir/<flowId>/{latest,<basisFingerprint>}.json
  index.sqlite                # snapshots(WAL) + semantic link + provenance 인덱스
  publish.pointer             # authoritative 현재 세대 포인터 (R6)
```

- slice 캐시 키 = 파일바이트해시 ∥ candidateId ∥ opts ∥ profile버전 ∥ 어댑터/스키마버전 ∥ 패키지 리졸루션
  — semantic input 전부 포함하므로 잘못된 slice 재사용 불가(R4).
- **append-only 원장**: 승인·인라인 수정은 이벤트 append이고 인라인 뷰는 파생물 —
  "append-only"와 수정 편의의 모순 해소(R9).
- v1 데이터: **fresh start** — 기존 `.codeflow` v1 산출물은 전량 삭제 후 시작(R10-C). importer 없음.

### 6.2 앵커 계약 (R3)

앵커는 라인 번호가 아니다:

```text
anchor = {
  repoRelativePath, byteRange, fileHash, spanHash,
  enclosingSymbolPath,          // 라인 변동에 불변
  canonicalAstFingerprint       // 공백/포맷 무관, 동작 변경 시 변경됨
}
```

- relink: 코드 변경 후 enclosingSymbolPath + AST fingerprint가 일치하면 재연결.
- 라인만 밀린 사소한 변경 → orphan 안 됨(재승인 피로도 방지).
- 동작이 바뀌면(AST fingerprint 불일치) → stale 처리, 승인 큐로.

### 6.3 Publish 원자성 (R5·R6)

- 분석 전 read-set manifest(worktree fingerprint) 생성 → 분석 후 재검증. 불일치 시 폐기·재분석;
  지속 변경 시 마지막 일관 snapshot 유지 + stale 표시.
- 발행: staging generation 디렉터리에 JSON+SQLite 준비 → **단일 commit pointer rename**으로 세대 전환
  → SQLite transaction은 pointer 세대만 참조. crash 시 기동 recovery가 pointer 기준으로 정합 복구.
- crash-injection 테스트 필수(§13).

---

## 7. 데이터 계약 (M1 산출물)

| 계약 | 생산→소비 | 불변식 |
|---|---|---|
| `candidate.schema.json` | Harvest → Slice | entry symbol 해석 가능, trigger class enum, dedup/tie-break 규칙 |
| `sliced-payload.schema.json` | Slice → Fusion | 언어 중립, 결정적 재현, redaction 완료, truncated/unknown edge 명시 |
| `session-artifact.schema.json` | MCP 제출 → Fusion | anchor 계약(§6.2) 통과 선행 |
| `flowspec.schema.json` | Fusion/Publish → FlowView | anchor 검증 통과, provenance+freshness+basisSha 필수 |
| `adapter-protocol.schema.json` | CORE ↔ Adapter | NDJSON 메시지, 버전 협상, §5.2 운영 계약 |
| `identity.schema.json` | 전 계약 공통 | flowId/stepId 생성·collision·supersedes·tombstone 규칙 (R9) |

- provenance enum: `approved | session | derived | unknown`.
- freshness enum: `fresh | stale | orphaned`. basisSha 별도 필드.
- flowId = canonical entry symbol path 기반 해시 — move/rename에 안정(relink 연계),
  split/merge는 supersedes 체인으로 추적.
- `codeflow.flows.yaml`(매니페스트): 고정·제외·이름 지정. M1에서 함께 정의(결정 #14).

---

## 8. 의미 공급 구조

### 8.1 Agent 세션 (E2) — MCP 구조화 제출만

```text
harvest_flows        # 후보 목록 (점수순, intent-matching 신호 포함)
get_flow_payload     # 특정 flow의 SlicedPayload
analyze_flow         # 마커가 놓친 임의 진입점(resolved symbol) 즉석 분석 요청
submit_flow_draft    # agent가 작성한 journey 초안 제출 (앵커 필수)
report_unknowns      # agent도 모르는 것 기록
open_review          # FlowView 해당 flow 열기
approve_step         # agent 제안의 승인 큐잉
```

> **자연어 프롬프트 시나리오**: 사용자가 "이메일 회원가입 흐름을 분석해서 flowview로"라고
> 요청하면, 의도 해석은 호스팅 agent(LLM)의 몫이고 CORE는 개입하지 않는다(결정 #5).
> agent는 harvest_flows의 신호 세트(심볼·클래스·trigger class·derived 이름·doc 라인)로
> 후보를 매칭하고, 놓친 진입점은 analyze_flow로 지정한다 — 이 절차를 codeflow 스킬이 각인한다.
> 검증: 이 경로의 종단 시연을 MCP 티켓(M3) 수용 기준으로 명시했다.

> **transcript 로그 파싱은 코어 스코프에서 제외했다 (검토 결론 R7·R12).**
>
> 고찰: transcript가 주는 것은 "의도"지만, 그 의도는 **같은 agent가 MCP로 구조화 제출할 수 있다**.
> 로그 속 유효 신호는 세션 전체의 극소수(나머지는 tool 호출·파일 읽기 노이즈)라 파싱 대비
> 고유 기여가 낮고, 포맷별 파서 유지·비결정성·secret 노출 면(R7 발견) 비용이 크다.
> codeflow 도입 이전 과거 세션의 소급 채굴이 필요해지면, `session-artifact` 계약이
> 확장점이므로 별도 ingest 어댑터로 나중에 추가한다 — 코어는 건드리지 않는다.

### 8.2 In-place 승인 (E3)

FlowView에서 session/derived 배지 항목 옆 `[승인]` → 인라인 수정 가능 → 승인 이벤트 append →
`view/` 파생 갱신. stale/orphaned 항목과 충돌은 승인 큐(상단 배너)로 모음.

---

## 9. FlowView — Flow Story (v1 개념 직계 계승)

### 9.1 디자인 방향

v1의 본질 — **"한 페이지에서 흐름을 따라 읽는 경험"** — 계승. 이해의 병목을 푸는 것은
세로 타임라인 + 단계 카드 + 인라인 코드 렌즈다. 흑백 모노크롬. **뷰 모드 없음 — 단일 개발자 뷰**:
목적이 구현 코드의 빠른 이해이므로 코드·심볼·permalink는 항상 노출.
swimlane은 선택적 축소지도로 격하.

### 9.2 화면 구조

```text
┌──────────────────────────────────────────────────────────────┐
│ [회원가입] [주문] [환불] [＋⌘K] · rev abc123 · clean · 14:32    │ ← 요청된 흐름 탭 + Quick Switcher
├──────────────────────────────────────────────────────────────┤
│ 회원가입                                                       │
│ ① 가입 정보를 입력한다                                 [approved]│
│    행동    이메일·비밀번호 입력 후 가입 버튼                      │
│    처리    SignupController.submit                            │
│ ──────────────────────────────────────────────────────────   │
│ ② 입력 규칙을 검증한다                                  [approved]│
│    규칙    · 이메일 형식 검사 · 비밀번호 8자 이상                 │
│    분기    실패 → ArgumentError, 진행 중단                      │
│    ▸ 핵심 코드 보기                                            │
│ ──────────────────────────────────────────────────────────   │
│ ⑤ ? 결과 코드가 어디서 결정되는지 모른다                  [unknown] │
└──────────────────────────────────────────────────────────────┘
```

- **FlowView는 요청된 흐름만 렌더한다.** 전체 흐름 목록 레일 없음.
- **Cmd+K Quick Switcher**(R13): 캐시된 후보 목록(`codeflow flows` 결과)에서 흐름을
  검색해 즉시 탭에 추가·전환 — CLI 복붙 번거로움 해소, "요청 단위" 원칙과 정합.
- **코드 렌즈**: 기본이 심볼(함수) 단위 뷰 — 감싸는 함수 본문 전체를 보여주고 단계 문장 줄을
  강조하며, 같은 함수 안의 다른 단계는 번호 마커로 함께 표시(클릭 시 해당 단계로 이동).
  뷰 전환: 함수 단위 / 단계 근거만 / 파일 전체. 심볼 범위가 없으면 문장 ±12줄 폴백 + 미확정 라벨.
- **배지**: `approved`(실선) / `session`(회색) / `derived`(점선) / `unknown`(물음표) /
  `stale`(경고) — freshness까지 표시(R2).
- **unknown·stale은 실제 카드로 서사에 존재** — 건너뛰지 않는다.
- watch 모드 자동 갱신 (v1 §11 계승).

### 9.3 단계 카드 명세

```text
{n}. {단계 이름}              [approved|session|derived|unknown] [fresh|stale]

행동  {트리거 행동}       처리  {시스템 처리: 심볼}
규칙  {businessRules}    상태  {before} → {after}    외부  {sideEffect}
분기  {guard → 결과}      [▸ 핵심 코드 보기]  [GitHub permalink]
```

### 9.4 기술

React + Vite + Zustand. 타임라인 DOM 렌더, mini-map에만 React Flow.

---

## 10. 흐름 발견과 우선순위

- 발견 경로: CLI `codeflow flows`(후보 목록, 점수순) · MCP `harvest_flows` · 매니페스트.
- **자동 점수순**: 마커 구체성(UseCase > Notifier method > 일반 call) × 진입점 팬인 × 경계 도달성.
- **매니페스트 오버라이드**: `codeflow.flows.yaml` 고정·제외·이름 — 항상 우선.
- 후보 dedup(root-equivalence)·tie-breaker는 M1 스키마의 normative rule(R11).

---

## 11. 설치, 업데이트, 보안

### 11.1 설치

원칙: **사용자 명령 1개, 런타임 의존 0, 첫 실행까지 30초.**

| 구성 | 형태 |
|---|---|
| `codeflow` 코어 | Go 단일 바이너리 (FlowView asset embed, 런타임 의존 0) |
| 언어 어댑터 | 언어별 네이티브 바이너리(Dart AOT 등), 필요 시 자동 다운로드 |
| MCP 등록 | `codeflow init` 자동 |

- **macOS: Homebrew 우선** (`brew tap <org>/codeflow && brew install codeflow`).
  GitHub Releases + curl 스크립트 보조. 개발은 `make local`.

### 11.2 어댑터 pin 정책

코어 릴리스가 호환 검증된 어댑터 버전 지정(v1 compatibility.json 계승). `init`은 pin 버전만 받고,
갱신은 코어 업그레이드 또는 명시적 `codeflow adapters update`뿐. 불일치 감지 시 에러+재설치 안내.

### 11.3 로컬 보안 계약 (R7·R8)

- **FlowView serve**: loopback-only bind + per-run token(URL 발급) + Host/Origin 검사 + CSRF 방어.
- **MCP 쓰기 authorization**: `submit_flow_draft` 등 쓰기 도구는 세션 토큰 소유자만.
- **공통 secret scanner 단일 관문**: 모든 저장·렌더 필드는 transcript/MCP/승인 텍스트 구분 없이
  scanner 통과 — redaction 우회 경로 원천 차단.
- v1의 인증 경계(로컬 단일 사용자 가정)를 명시적으로 계승.

성능: 전 AOT, 파일 해시 증분, 어댑터 프로세스 풀 재사용, 평상시 LLM 호출 0.

---

## 12. 장애 모드

| 상황 | 동작 |
|---|---|
| 어댑터 부재/버전 불일치 | 감지 → pin 버전 재설치 안내 |
| 어댑터 크래시/timeout | typed error → 재시작 1회 → 실패 시 해당 flow unknown edge |
| 세션 artifact 앵커 부정합 | 폐기 + `unlinked` 보관 |
| 순환호출/depth 초과 | visited set 차단, truncated=true |
| unresolved/dynamic 호출 | unknown edge 카드 |
| 승인 의미 anchor 소멸(AST 변경) | stale → 승인 큐 |
| read-set fingerprint 불일치 | 폐기·재분석, 지속 변경 시 마지막 일관 snapshot + stale |
| crash mid-publish | 기동 recovery가 commit pointer 기준 정합 복구 |
| Git remote 부재 | permalink 생략 |

## 13. 테스트 전략

- 어댑터: fixture 슬라이싱 단위, 프로토콜 호환(timeout/cancel/crash-restart 포함)
- Fusion: 권위 순서 충돌, stale 판정, 앵커 relink(AST fingerprint)
- Publish: staging generation 전환, **crash-injection**, worktree fingerprint
- E2E: examples 앱 — harvest→slice→fuse(mock artifact)→publish→FlowView
- 계약: 6종 스키마 ↔ 구현 드리프트 검사

## 14. 마일스톤

| 단계 | 산출물 |
|---|---|
| M1 | 계약 6종 스키마 + `codeflow.flows.yaml` (P1 게이트 필드 전부 normative 포함) |
| M2 | CORE 골격(Go): harvest/fusion/publish(staging generation) + Dart 어댑터(slice, NDJSON stdio) + v1 잔여 데이터 전량 삭제(fresh start) |
| M3 | MCP server (구조화 제출 경로) |
| M4 | FlowView: Flow Story + in-place 승인 + Cmd+K |
| M5 | 실제 repo 시험 + 우선순위 튜닝 |
| M6 | Kotlin 어댑터 착수 (계약 검증) |

## 15. 결정 로그 (최종)

| # | 결정 | 내용 |
|---|---|---|
| 1 | 코어 언어 | Go — I/O 오케스트레이션 워크로드, 단일 바이너리 |
| 2 | 세션 artifact | MCP 구조화 제출 중심 (transcript 파싱 제외 — #17 참조) |
| 3 | FlowView 범위 | 요청된 흐름만 렌더 (v1 상단 내비게이션 직계 계승) |
| 4 | 승인 UX | FlowView in-place 승격 + 승인 큐 |
| 5 | LLM | 완전 제외 |
| 6 | 추출 우선순위 | 마커 점수 자동순 + 매니페스트 오버라이드 |
| 7 | 크로스 파일 | **개정**: import-resolved 직접 참조 심볼 추적 + 경계 종료 + unknown edge. 전체 DAG 불필요 |
| 8 | 어댑터 IPC | NDJSON over stdio, 영속 프로세스 풀 |
| 9 | 설치 | macOS Homebrew 우선 + Releases/curl 보조 |
| 10 | 어댑터 갱신 | core 릴리스별 pin + 명시적 update 커맨드만 |
| 11 | 코드 위치 | 기존 코드 `legacy/` 보관, v2는 repo 루트에서 시작 |
| 12 | transcript 포맷 | ~~Codex JSONL + opencode~~ → **제외** (#17) |
| 13 | 뷰 모드 | 모드 없음 — 단일 개발자 뷰 |
| 14 | flows.yaml | M1에서 contracts와 함께 정의 |
| 15 | R1 개정 | 흐름 추적 목표 = 비즈니스 흐름의 명확한 이해와 코드로의 명쾌한 요약. 기술 세부 배제, 필요 흐름은 end-to-end 전반 파악 |
| 16 | R10 마이그레이션 | **C: fresh start** — v1 데이터 전량 삭제, importer 없음 |
| 17 | R7 transcript | **코어 스코프 제외** — MCP 구조화 제출이 유일 자동 E2 채널. session-artifact 계약을 향후 ingest 어댑터 확장점으로 유지 |

## 16. 리뷰 P1 게이트 — 해소 현황

| ID | 처분 | 상태 |
|---|---|---|
| R1 | #7 개정 — resolved-symbol 추적 + unknown edge (§4.2) | ✅ 확정 |
| R2 | provenance/freshness/confidence/basisSha 분리, E1은 E3가 덮지 않음 (§3, §7) | ✅ 채택 반영 |
| R3 | anchor = byteRange+hashes+symbolPath+AST fingerprint (§6.2) | ✅ 채택 반영 |
| R4 | ast/slice 이중 캐시 + 전체 semantic input 키 (§6.1) | ✅ 채택 반영 |
| R5·R6 | worktree fingerprint + staging generation + commit pointer (§6.3) | ✅ 채택 반영 |
| R7·R8 | 공통 scanner 관문 + loopback/token/CSRF/MCP auth (§11.3); transcript 자체 제외(#17) | ✅ 확정 |
| R9 | identity.schema + event-log append + 파생 뷰 (§6.1, §7) | ✅ 채택 반영 |
| R10 | fresh start, v1 데이터 전량 삭제 (M2 작업 포함) | ✅ 확정 |
| R11 | dedup/tie-breaker + 프로토콜 운영 계약 (§5.2, §10) | ✅ 채택 반영 |
| R12·R13 | transcript 단계화 → 제외로 종결; Cmd+K (§9.2) | ✅ 확정 |

**남은 미결정 0건.** M1 착수 가능.
