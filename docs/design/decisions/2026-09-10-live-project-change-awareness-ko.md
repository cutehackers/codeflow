# Live View 프로젝트 변경 인지 결정

- Status: Accepted
- Date: 2026-09-10
- Parent Contract: `docs/design/specs/2026-09-10-live-project-change-awareness-ko.md`
- Source: User instruction, 2026-09-10

## D-LIVE-01 · 프롬프트 없는 프로젝트 변경 인지 시작

- Context: 기능 요청은 Static FlowView의 시작점이다. Live View에 같은 입력을 요구하면 사용자는 현재 코드 변경을 보기 전에 기능명·entry symbol·MCP 도구를 알아야 한다.
- Decision: `codeflow live [path]`가 project-change mode를 시작한다. Live 화면은 request input과 `흐름 보기`를 표시하지 않는다.
- Rejected Alternative: 자연어 기능 요청 또는 FlowView 선택이 Live View 시작의 선행 조건이다.
- Rationale: Live View의 가치는 프로젝트에서 새로 작성되는 코드를 자동으로 인지하는 데 있다.
- Consequences: feature query는 Static FlowView에 남고 Live View는 변경 batch를 입력으로 받는다.
- Affected: `INT-01`, `GOAL-01`, `INV-LIVE-01`, `FA-LIVE-01`–`FA-LIVE-02`.

## D-LIVE-02 · 프로젝트별 단일 logical coordinator와 stale 자동 복구

- Context: 실제 MCP trace에서 한 process는 `snap-1-0001`을 들고 있었고 durable live head는 `snap-1-0005`였다. edit는 `workspace live-head conflict`로 거절됐고 generation이 발행되지 않았다.
- Decision: snapshot lineage, watcher, scheduler, event stream과 recovery authority는 프로젝트별 하나의 logical coordinator가 소유한다. stale cached head는 durable state reload와 idempotent edit 한 번 재시도로 복구한다.
- Rejected Alternative: 각 MCP process가 독립 SnapshotEngine을 소유하고 conflict 시 사용자에게 재시작을 요구한다.
- Rationale: producer process의 수명은 프로젝트 변경 인지 기능의 신뢰성 조건이 될 수 없다.
- Consequences: coordinator discovery·handoff·restart recovery와 multi-process integration trace가 필요하다.
- Affected: `INT-02`, `GOAL-03`, `INV-LIVE-02`–`INV-LIVE-04`, `FA-LIVE-03`–`FA-LIVE-07`.

## D-LIVE-03 · 의미 변경 묶음과 기존 Live UX 유지

- Context: 파일별 알림은 agent의 대량 수정에서 인지 부담을 늘린다. 사용자는 현재 Live View의 Change Pulse, 코드 카드, 처리 흐름, 읽기 고정과 Apply UX에는 만족한다.
- Decision: Live View는 related change를 semantic batch로 묶어 verified behavior, branch, state, external effect, call relation, Evidence update와 unresolved move만 표시한다. 기존 화면 구조와 Apply 동작은 유지하고, 데이터의 시작점을 기능 요청에서 프로젝트 변경 batch로 바꾼다.
- Rejected Alternative: Live View를 FlowView의 기능 후보 선택 화면으로 만들거나 파일 diff 목록으로 축소한다.
- Rationale: Live View는 흐름 요청을 설명하는 화면이 아니라 변경된 코드의 의미를 빠르게 이해하는 화면이다.
- Consequences: change grouping, semantic relevance, independent-change separation과 unknown/gap tests가 필요하다.
- Affected: `GOAL-02`, `INV-LIVE-05`–`INV-LIVE-09`, `FA-LIVE-08`–`FA-LIVE-15`.

## D-LIVE-04 · 미확인 저장 변경의 재시작 복원 (Superseded)

- Status: Superseded
- Superseded By: D-LIVE-08
- 아래 내용은 폐기된 결정의 기록이며 현재 구현 권위가 아니다.

- Context: 사용자는 Live View를 다시 열 때 최근 저장된 변경도 확인할 수 있어야 한다. 시작 시점 이후 변경만 보이면 종료 중 발생한 agent·IDE 변경을 놓친다.
- Decision: project-scoped durable viewer cursor 뒤의 unreviewed semantic change batch를 Live View 시작 시 복원한다. Apply 또는 명시적 확인 완료된 batch는 새 Change Pulse로 재알림하지 않는다.
- Rejected Alternative: Live session 시작 이후 변경만 보여 주거나, 모든 과거 변경을 매번 새 알림으로 재생한다.
- Rationale: 변경 누락 없이 재개하되 이미 읽은 변경이 계속 반복되는 인지 부담을 막는다.
- Consequences: durable cursor와 batch lifecycle, restart/reconnect traces, retention policy의 구현-level 결정이 필요하다.
- Affected: `GOAL-01`, `INV-LIVE-03A`, `FA-LIVE-02`–`FA-LIVE-02A`.

## D-LIVE-08 · 사용자 확인 이력 없이 현재 코드 변경 인지

- Status: Accepted
- Source: 현재 대화에서 사용자가 “중요한것은 현재 코드베이스에서 변경된 내용을 알아차리기만 하면될 것”, “사용자가 확인한 내용을 기록하고 관리할 필요는 없을 듯하다”라고 결정한 뒤 결정사항과 VS 정리를 요청했다.
- Context: 기존 VS-05는 읽음·미확인 상태와 cursor를 저장하고 재시작 때 미확인 batch를 복원하도록 확장돼 있었다. 이는 사용자가 원하는 코드 변경 인지보다 넓은 범위다.
- Decision: 사용자 확인 이력, viewer cursor, 확인 완료 action과 과거 미확인 알림 복원을 제거한다. 재시작 시 마지막 검증 분석 기준과 현재 stable source를 비교한다. Apply는 표시 중인 검증 결과를 바꾸며 확인 이력을 남기지 않는다.
- Alternatives: 사용자별 미확인 목록 관리, 확인 시각 하나로 알림 억제, 매번 모든 과거 알림 재생을 제외한다. 기존 기준이 있는데도 현재 코드로 덮어쓰고 시작하는 방식은 미분석 차이를 놓치므로 사용하지 않는다.
- Rationale: 현재 코드 변화와 검증 근거가 제품 가치이며 사용자의 읽음 상태는 변경의 판단 기준이 아니다.
- Consequences: 기존 VS-05 폐기, VS-01·VS-02에 재시작 기준과 수집 배분, VS-03에서 재시작 차이도 같은 의미 분석 사용, VS-04 Apply 의미 명시. VS 번호는 재사용하지 않는다. source 및 기존 저장 분석 artifacts를 삭제하지 않는다.
- Failure / Compatibility: 최초 기준 부재는 초기화, 손상·lineage 불일치는 reconciliation/gap. 수집 뒤 분석 실패 시 마지막 검증 기준을 보존한다. 저장 분석 데이터의 기존 읽기 호환과 local 접근 정책은 유지한다.
- Affected: INT-01, GOAL-01·GOAL-02, FA-LIVE-02·FA-LIVE-16, INV-LIVE-10, ASM-LIVE-04. FA-LIVE-02A·INV-LIVE-03A·ASM-LIVE-03 폐기.
- Related Open Decisions: Q-LIVE-01 Closed — 확인 이력 기능 제거로 보관·삭제·여러 사용자 cursor 선택 불필요.
