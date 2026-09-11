# Live View 프로젝트 변경 인지

- Contract ID: `LIVE-PROJECT-CHANGE-AWARENESS`
- Contract Status: Approved
- Created: 2026-09-10
- Intent Status: Hardened
- Source: User instruction, Live View product-direction discussion (2026-09-10), current conversation decision removing user confirmation history
- Amendment: 사용자 확인 이력 제거, 코드 분석 기준으로 재시작 변경 감지
- Decision Records: `docs/design/decisions/2026-09-10-live-project-change-awareness-ko.md`
- Glossary: `docs/design/glossary.md`
- Supersedes Scope: `.tasks/2026-09-02-requested-flow-live-semantic-compiler-ko/vs-03-see-automatic-current-or-gap-ko.md`의 Live 변경 인지·시작·coordinator 범위

## 1. 문제와 목표

현재 Live View는 요청 흐름을 입력하거나 MCP 도구를 호출해야 시작되고, 장기 실행 MCP 프로세스가 오래된 workspace live head를 들고 있으면 편집을 거절한다. 사용자는 MCP, entry symbol, snapshot, generation 또는 재시작을 알아서는 안 된다.

Live View의 목적은 현재 프로젝트에서 계속 작성·수정되는 코드를 의미 단위로 빠르게 인지하게 하는 것이다. 이는 FlowView의 요청 흐름 이해와 다른 제품 목적이다.

### Intent와 Goal

| Intent | Goal | 관찰 가능한 결과 |
|---|---|---|
| `INT-01` 사용자가 프로젝트 코드 변경을 즉시 이해한다. | `GOAL-01` 사용자는 프롬프트나 MCP 호출 없이 프로젝트 변경 인지 모드를 시작한다. | Live View가 기준 상태를 잡고 감시 상태를 표시한다. |
| `INT-01` | `GOAL-02` 사용자는 파일 목록 대신 변경된 행동과 관계를 이해한다. | 변경 묶음과 Change Pulse가 행동·분기·상태·외부 효과·호출 관계를 표시한다. |
| `INT-02` 변경 producer와 Live 화면 사이의 신뢰 가능한 연결을 유지한다. | `GOAL-03` 사용자는 재시작 없이 IDE·agent·filesystem 변경을 하나의 Live 결과로 받는다. | stale coordinator는 자동 복구하고 중복·충돌을 내부 처리한다. |

## 2. 범위

### In Scope

- 프롬프트 없는 프로젝트 단위 Live View 시작
- 재시작 시 마지막 검증 분석 기준과 현재 stable source를 비교해 변경 수집 재개
- 프로젝트별 단일 논리 Live coordinator와 stale state 자동 복구
- VS Code 저장, coding agent 변경, filesystem watcher fallback의 통합 변경 수집
- 변경 묶음의 semantic impact 분석과 proof-backed current 또는 gap 발행
- 기존 Live View의 Change Pulse, 코드 카드, 처리 흐름, 읽기 고정, Apply UX를 유지한 프로젝트 변경 데이터 입력
- 정적 FlowView와 Live View의 진입·상태·갱신 책임 분리
- MCP·HTTP·CLI의 기존 Live 진입 호환 처리

### Non-Goals

- 사용자별 읽음·미확인 상태, 확인 완료 기록, viewer cursor와 과거 미확인 알림 복원 — 현재 코드 변경 인지에 필요하지 않다.
- 확인 이력의 보관·삭제·다중 사용자 정책 — 확인 이력 자체를 만들지 않는다.

- FlowView의 요청 흐름 생성·선택·표시 UX 변경 — 별도 제품 범위다.
- 사용자가 기능명, entry symbol, MCP 도구 또는 revision을 입력하게 하는 Live View UX — 프로젝트 변경 인지 목적과 맞지 않는다.
- 전체 repository graph를 기본 화면에 표시 — Live View는 의미 있는 변경 묶음과 그 영향 근거를 우선한다.
- 현재 Live View의 코드 카드, 처리 흐름, Change Pulse, 읽기 고정과 Apply의 시각 디자인 재설계 — 데이터 시작점과 상태만 바꾼다.
- agent가 작성한 설명을 근거 없이 semantic change로 확정 — 검증된 Fact와 Evidence만 표시한다.

## 3. 행위자와 선행 조건

- Primary actor: 현재 프로젝트에서 작성되는 많은 코드의 변화를 이해하려는 개발자
- Supporting actor: VS Code, Codex, Claude Code, 터미널 또는 다른 파일 편집 도구
- Preconditions: 프로젝트 root가 식별되고 지원 가능한 소스 파일 또는 명시적 분석 범위가 존재한다.
- Permission: source는 read-only 분석 대상이다. Live View는 source 변경 권한을 요구하지 않는다.

## 4. 확인된 사실

- `CF-01`: 정적 FlowView와 `/live`는 별도 renderer와 갱신 책임을 가진다.
- `CF-02`: `submit_versioned_edit`는 MCP producer가 변경을 전달하는 경로다.
- `CF-03`: 타깃 프로젝트에서 서로 다른 MCP process가 같은 durable workspace head를 갱신하면 오래된 process는 `workspace live-head conflict`로 편집을 거절한다.
- `CF-04`: 해당 거절 뒤 generation이 발행되지 않아 Live View는 변경 결과를 표시할 수 없다.
- `CF-05`: 현재 `/live`는 기능 요청 input과 `흐름 보기`로 시작하며 프로젝트 변경 인지라는 목적과 다르다.

## 5. 결정과 용어

| ID | 결정 |
|---|---|
| `D-LIVE-01` | `codeflow live [path]`는 프롬프트 없이 Live Project Change Analysis mode를 시작하는 전용 public command다. feature query는 Static FlowView에 남고 Live의 시작 조건이 아니다. |
| `D-LIVE-02` | 프로젝트마다 하나의 논리 coordinator가 snapshot lineage, watcher, scheduler, event stream과 recovery를 소유한다. MCP는 producer이며 stale head 불일치 시 durable state를 다시 읽고 동일 idempotency key로 한 번 재시도한다. |
| `D-LIVE-03` | 관련 변경을 semantic batch로 분석하고 기존 Change Pulse, 코드 카드, 처리 흐름, 읽기 고정과 Apply UX에 검증된 의미 변화와 근거를 표시한다. 요청 input과 흐름 보기는 Live Project Change Analysis mode에서 숨긴다. |
| `D-LIVE-08` | 사용자 확인 이력을 저장·관리하지 않는다. 재시작 시 마지막 검증 분석 기준과 현재 stable source를 비교하며 Apply는 표시 전환만 담당한다. |

결정 ID는 연결된 결정 기록과 동일하다. 기존 D-LIVE-04의 미확인 복원 결정은 D-LIVE-08로 대체됐으며 현재 요구사항으로 사용하지 않는다.

## 6. 불변 조건

- `INV-LIVE-01`: Live View 시작은 user prompt, entry symbol, flow ID 또는 MCP 도구 이름을 요구하지 않는다.
- `INV-LIVE-02`: 한 프로젝트의 변경 수집·revision ordering·snapshot publication·recovery authority는 하나의 논리 coordinator에만 있다.
- `INV-LIVE-03`: stale process는 durable head와 불일치하는 snapshot을 publish하지 않는다. 내부 recovery가 실패하면 current라고 표시하지 않고 gap으로 전환한다.
- `INV-LIVE-04`: IDE, coding agent와 watcher fallback 변경은 하나의 canonical change ingress와 동일한 duplicate semantics를 사용한다.
- `INV-LIVE-05`: Live View는 파일 diff만으로 의미 변화를 확정하지 않는다. 검증된 behavior, branch, state, external effect, call relation, Evidence 또는 unresolved move 변화만 Change Pulse에 표시한다.
- `INV-LIVE-06`: 확인할 수 없는 영향은 gap 또는 `확인 필요`로 표시하며 추측한 호출 관계나 흐름을 만든다 하지 않는다.
- `INV-LIVE-07`: Static FlowView는 자동 변경 감지, Live event 구독 또는 generation 자동 적용을 하지 않는다.
- `INV-LIVE-08`: Live View는 proof-backed generation만 표시하고 읽기 고정·선택 identity 손실 시 기존 화면을 유지하며 명시적 Apply를 요구한다.
- `INV-LIVE-09`: 기본 화면은 MCP, snapshot, generation, revision, compiler lag 같은 구현 정보를 표시하지 않는다.

- `INV-LIVE-10`: 변경 보기, Apply와 선택 제거 확인은 사용자 읽음·미확인 이력이나 viewer cursor를 기록하지 않는다. 코드 비교 기준과 화면 읽기 고정 상태는 사용자 확인 이력이 아니다.

## 7. 관찰 가능한 사용자 흐름

1. 사용자는 프로젝트 root에서 `codeflow live .`를 실행한다.
2. 시스템은 durable workspace state와 마지막 검증 분석 기준을 복원하고 단일 coordinator, watcher와 Live View를 시작한다. 시작 시 현재 source를 stable capture하여 분석 기준과 비교하고 변경을 canonical ingress로 수집한다.
3. 비교 가능한 이전 분석 기준이 없으면 현재 source로 최초 기준을 만든다. 새 변경이 없으면 `현재 프로젝트의 변경을 감시하고 있습니다`를 표시하고, 변경이 있으면 동일한 분석·검증 경로로 처리한다. 기능 요청 input은 없다.
4. 사용자는 VS Code, Codex, Claude Code 또는 터미널에서 코드를 수정한다.
5. 시스템은 가까운 시간에 발생한 관련 변경을 하나의 변경 묶음으로 수집하고 stable bytes를 검증한다.
6. 시스템은 변경된 symbols와 관계의 영향을 분석하고 proof-backed generation 또는 verified gap을 만든다.
7. Live View는 Change Pulse에 사용자 의미 변화와 `변경 보기`, `적용` action을 표시한다.
8. 사용자가 `변경 보기`를 선택하면 기존 코드 카드와 처리 흐름에서 근거와 영향을 읽는다.
9. 사용자가 `적용`을 선택하면 새 generation으로 전환한다. 읽기 고정 또는 선택 identity 손실은 기존 화면을 유지한다. 이 동작은 사용자 확인 이력이나 source 변경을 만들지 않는다.

## 8. 변경 묶음 규칙

1. 짧은 시간에 같은 작업으로 발생한 multi-file change는 하나의 batch로 처리한다.
2. watcher와 MCP가 같은 content identity를 보고하면 하나의 batch로 deduplicate한다.
3. source formatting, comment-only, import-only change는 semantic impact가 없으면 기본 Change Pulse에서 숨긴다.
4. test-only change는 관련 production semantic change가 있으면 그 묶음의 Evidence update로 표시한다. 관련 production change가 없으면 별도 낮은 중요도 묶음으로 표시한다.
5. rename, delete, overflow, branch 또는 worktree identity change는 추정하지 않고 reconciliation 또는 gap으로 처리한다.
6. 서로 독립된 변경은 한 카드로 섞지 않는다.

## 9. 실패와 복구

| 조건 | 사용자에게 보이는 결과 | 금지되는 동작 | 내부 복구 |
|---|---|---|---|
| coordinator의 cached head가 stale | `변경을 다시 확인하고 있습니다` | MCP conflict, snapshot ID, 재시작 요구 노출 | durable state reload 후 한 번 재시도 |
| 재시도 뒤에도 head가 바뀜 | `새 변경을 확인하지 못했습니다`와 affected scope | stale generation을 current로 표시 | latest-vs-verified gap 유지 및 다음 batch 대기 |
| watcher stable capture 실패 | `변경을 확인 중입니다` | 불안정 bytes를 분석 입력으로 사용 | bounded reconciliation |
| 분석 또는 proof 검증 실패 | 마지막 검증 결과와 `확인 필요` | candidate 또는 raw diff를 새 분석 결과처럼 적용 | verified gap 발행 |
| stream disconnect | `연결을 복구하고 있습니다` | 자동으로 빈 화면 또는 다른 generation 표시 | replay 또는 full recovery |
| restart 뒤 분석 기준과 현재 source가 다름 | `변경을 확인 중입니다` 이후 검증 결과 또는 gap | 과거 읽음 여부로 변경을 숨기거나 과거 미확인 알림을 재생 | stable capture와 canonical ingress로 차이 수집 |
| unsupported source 범위 | `이 변경은 현재 분석 범위에서 확인할 수 없습니다` | 가짜 semantic relation 표시 | 범위·adapter 상태 유지 |

## 10. 화면 데이터 계약

### Live Project Change Analysis mode 시작 상태

- `mode`: `project_change`
- baseline: durable workspace state와 마지막 검증 분석 기준, 현재 source의 stable capture
- 화면 제목: 프로젝트 변경 감시 상태
- 기본 action: 감시 일시정지
- 숨김: request input, entry symbol input, `흐름 보기`

### 재시작 변경 감지

- coordinator는 기존 workspace snapshots, revisions, change batches, proof artifacts와 코드 비교에 필요한 분석 기준을 사용한다. 사용자 확인 상태를 추가하지 않는다.
- 마지막 검증 분석 기준과 현재 stable source의 차이를 canonical ingress로 제출하고 기존 의미 분석·proof 검증 경로로 처리한다. 최신 수집 head만을 분석 완료 기준으로 간주하지 않는다.
- 이전 기준이 없는 최초 실행은 현재 source로 기준을 만든다. 이전 기준이 손상되거나 lineage가 다르면 이를 최초 실행과 혼동하지 않고 reconciliation 또는 명시적 gap으로 처리한다.
- 분석 실패 시 마지막 검증 기준과 결과를 보존한다. 다음 실행에서도 아직 분석되지 않은 코드 차이를 잃지 않는다.
- 같은 분석 기준과 같은 source에서는 사용자의 읽음 여부 때문에 새 변경을 만들지 않는다. 종료 중의 모든 중간 편집을 재생할 의무는 없다.
- Apply는 저장 proof-backed view로 화면을 전환한다. `확인 완료` action, 읽음·미확인 기록, 사용자별 viewer cursor를 추가하지 않는다. 선택 identity 제거 확인은 기존 화면 선택 처리이며 과거 알림 확인 기록이 아니다.
- 기존 분석 artifact의 보관·호환 정책은 유지한다. 사용자 확인 이력의 보관·삭제·다중 사용자 기능은 범위 밖이다.

### Change Pulse

- 묶음 제목: 영향 받은 업무 또는 코드 책임을 짧게 설명한다.
- 항목 종류: behavior, branch, state, external effect, call relation, Evidence update, unresolved move, `확인 필요`.
- 근거: 각 항목은 generation, affected symbol과 Evidence link를 가진다.
- action: `변경 보기`, `적용`, 읽기 고정 중인 경우 `계속 읽기`.

### Compatibility

- `/` Static FlowView의 요청·선택·정적 표시 계약은 유지한다.
- `/live`는 Live Project Change Analysis mode의 canonical URL이다.
- 기존 `query_task_view` feature 호출은 static FlowView URL을 반환하도록 전환한다. 기존 `/live?request=...` 또는 `entrySymbol=...` URL은 Live Project Change Analysis mode를 열고 request parameter를 분석 시작 조건으로 사용하지 않는다.

## 11. Feature-Level Acceptance

- `FA-LIVE-01`: WHEN a user runs `codeflow live [path]`, THE system SHALL start Live Project Change Analysis mode without requiring a prompt, feature query, flow ID, entry symbol or MCP tool selection.
- `FA-LIVE-02`: WHEN Live Project Change Analysis mode starts, THE system SHALL restore durable workspace state and the last verified analysis basis, compare it with current stable source, and process detected differences through canonical change ingress; a first start without a prior basis establishes the current source baseline.
- `FA-LIVE-03`: WHEN VS Code, a coding agent or a filesystem fallback observes a source change, THE system SHALL submit it through one canonical change ingress and SHALL preserve source and batch identity.
- `FA-LIVE-04`: WHEN duplicate producer observations describe the same canonical content change, THE system SHALL create one revision and one user-visible change batch.
- `FA-LIVE-05`: WHEN several MCP processes address one project, THE system SHALL provide one logical coordinator authority and SHALL NOT reject a valid current edit solely because a process holds a stale cached head.
- `FA-LIVE-06`: WHEN durable head disagreement is detected, THE system SHALL reload durable coordinator state and retry the idempotent accepted edit once before exposing a gap.
- `FA-LIVE-07`: WHEN a retry cannot obtain a stable current head, THE system SHALL retain the last verified result and show a recoverable user-language gap state.
- `FA-LIVE-08`: WHEN related changes occur in a bounded editing interval, THE system SHALL present one semantic change batch rather than independent file notifications.
- `FA-LIVE-09`: WHEN a verified semantic delta exists, THE system SHALL identify only verified behavior, branch, state, external effect, call relation, Evidence or unresolved move changes.
- `FA-LIVE-10`: WHEN a change is formatting-only or comment-only, THE system SHALL NOT present it as a default semantic change.
- `FA-LIVE-11`: WHEN a generation is verified, THE system SHALL render only its proof-backed view and SHALL NOT reanalyze current filesystem bytes as that update.
- `FA-LIVE-12`: WHILE the user has reading fixed or the selected identity is absent from a new generation, THE system SHALL preserve the existing reading state and require explicit Apply or removal acknowledgement.
- `FA-LIVE-13`: THE Live View SHALL expose user-language states for watching, pending change, gap and reconnect without showing MCP, live head, snapshot, generation, revision or compiler telemetry in the primary view.
- `FA-LIVE-14`: THE Static FlowView SHALL NOT subscribe to project-change events or automatically apply Live View generations.
- `FA-LIVE-15`: WHEN a feature query is submitted through FlowView or MCP, THE system SHALL preserve that query's static FlowView behavior and SHALL NOT require it to begin Live Project Change Analysis mode.

- `FA-LIVE-16`: THE system SHALL NOT persist user read/unread or acknowledgement history, or use it to select project-change results; Apply SHALL only change the displayed verified view.

## 12. Quality and Compatibility Constraints

- No latency, throughput, repository-size or change-batch interval threshold is asserted without a declared environment profile and measured evidence.
- Existing stored workspace snapshots, revisions, batches and proof artifacts remain readable.
- Live View migration must not require source-code changes in target repositories.
- Existing MCP clients receive a compatibility response during the Live URL transition and do not receive an unannounced protocol break.

## 13. Assumptions

- `ASM-LIVE-01`: a project-scoped coordinator can be reached or recovered independently of a particular MCP client process.
  - Consequence if false: long-lived clients continue to reject edits after another writer advances durable state.
  - Validation: multi-process MCP edit trace with coordinator restart and durable-head advancement.
- `ASM-LIVE-02`: the existing Live View UX can render project-level change batches without changing its code-card and reading-state model.
  - Consequence if false: a separate UX decision is needed before implementation.
  - Validation: browser trace for independent, related, delete and identity-loss batches.
- `ASM-LIVE-04`: existing durable analysis artifacts can identify the last verified comparison basis independently of the latest captured workspace head.
  - Consequence if false: a captured but unanalyzed edit could disappear from restart comparison.
  - Validation: restart traces with a completed analysis, capture followed by analysis failure, missing initial basis, and incompatible or damaged saved basis. Preserve the last verified result and expose a gap when comparison cannot be proven.

## 14. Done When

- Feature-level acceptance has evidence from CLI, MCP multi-process, watcher, persistence, SSE and browser tests.
- A real target-project trace proves that an agent edit after another process advanced durable head is automatically recovered or truthfully shown as a gap.
- A user can begin and use Live Project Change Analysis mode with `codeflow live .`, without typing a feature request or naming an MCP tool.
- Restart traces prove current source changes are compared with the last verified analysis basis, including a previously captured but unanalyzed change, without user confirmation history. Apply and selection-removal acknowledgement tests prove that no read/unread record is created.
- Static FlowView regression tests prove its immutable behavior remains unchanged.

## 15. Amendment and Intent Hardening

- Confirmed user decision: 현재 코드베이스의 변경 인지가 목적이며 사용자 확인 내용을 일일이 기록·관리하지 않는다. 직전 합의의 결정1–3을 이번 문서 정리에 반영한다.
- Decision record: D-LIVE-08. 기존 decision record D-LIVE-04와 VS-05를 Superseded로 보존한다. `INV-LIVE-03A`, `FA-LIVE-02A`, `ASM-LIVE-03`은 폐기된 식별자로 재사용하지 않는다.
- Impact: §2·5–7·9–11·13–14의 사용자 확인 복원 요구 제거. VS-01은 기준 복원, VS-02는 재시작 차이 수집, VS-03은 차이의 의미 분석, VS-04는 이력 없는 Apply를 담당한다. 기존 데이터 삭제·프로토콜 권한 변경·제품 코드 변경은 없다.
- Hardening review: 현재 코드 변경이라는 결과와 범위, Apply 의미, 재시작·실패·lineage 경계, 기존 artifact 호환을 명시했다. 확인 이력 보관 정책 질문 Q-LIVE-01은 기능 제거로 해소된다. 추가 제품 결정 없음. 내부 분석 기준 연결 가능성은 ASM-LIVE-04의 구현 검증 대상이다.
- Open Decisions: 없음. 상위 승인과 slice 구현 승인은 별개이며 활성 slice는 독립 재검토 후에도 Proposed를 유지한다.
