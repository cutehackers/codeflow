# FlowView Storyboard 전환과 Live 제거

- Contract ID: `FLOWVIEW-STORYBOARD-TRANSITION`
- Contract Status: Approved
- Created: 2026-09-15
- Intent Status: Hardened
- Implementation Status: Implemented and verified (VS-01 ~ VS-04).
- Source: 사용자 지시와 현재 구현 조사
- Decision Record: [Storyboard 전환 결정](../decisions/2026-09-15-flowview-storyboard-transition-ko.md)

## 1. 목적

FlowView의 기본 화면은 전체 semantic step과 자동 변경 상태가 아니라 storyboard 장면을 표시한다. 사용자는 현재 디자인 안에서 장면, 제한된 source, 직접 relation을 읽고, 명시적으로 요청했을 때만 재분석하거나 비교한다. Live Semantic Map은 제거한다.

| Intent            | Goal               | 관찰 가능한 결과                                                 |
| ----------------- | ------------------ | ---------------------------------------------------------------- |
| INT-STORYBOARD-01 | GOAL-STORYBOARD-01 | 사용자는 storyboard 장면과 source로 현재 코드를 읽는다.          |
| INT-STORYBOARD-01 | GOAL-STORYBOARD-02 | 사용자는 검증된 직접 relation을 탐색하고 원래 장면으로 돌아온다. |
| INT-STORYBOARD-02 | GOAL-STORYBOARD-03 | 사용자는 재분석과 비교를 명시적으로 제어한다.                    |
| INT-STORYBOARD-02 | GOAL-STORYBOARD-04 | 사용자는 자동 Live 갱신 없이 FlowView 하나를 사용한다.           |

## 2. 기존 계약과의 관계

이 계약은 새 작업의 유일한 구현 계약이다. 다음 문서는 조사·reference 대상이며 수정, 상태 변경, 대체, 삭제를 이 계약 작성 단계에서 수행하지 않는다.

- `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md` 및 `.tasks/2026-09-14-flowview-code-comprehension-ko/`
- `docs/design/specs/2026-09-14-flowview-adaptive-layers-ko.md`
- `docs/design/specs/2026-09-14-flowview-macro-context-storyboard-ko.md`
- `docs/design/specs/2026-09-14-flowview-modular-frontend-ko.md`
- `docs/design/specs/2026-09-14-flowview-svelte-pipeline-and-mcp-integration-ko.md`
- `docs/design/specs/2026-09-04-codeflow-architectural-modernization.md` 및 `.tasks/2026-09-04-codeflow-architectural-modernization/`
- `.tasks/2026-09-10-live-project-change-awareness-ko/`

VS-04가 caller·consumer inventory에서 제거 가능하다고 확인한 Live 전용 계약·문서·코드만 별도 구현 변경으로 제거한다. 공유 분석, 보안, 저장, CLI·MCP 계약은 기존 계약대로 보존한다.

## 3. 범위

### 포함

- canonical semantic map에서 파생되는 versioned `storyboard` task-view 결과
- entry, decision, process, effect, result, boundary 장면과 접힌 내부 detail
- storyboard, rail, 코드 card, Radar의 동일 장면 선택과 relation 왕복
- 명시적 재분석, 선택 비교, 직접 relation
- Live route, 자동 watcher·stream 갱신, Live 전용 UI·asset·test·문서·계약의 inventory와 제거

### 제외

- 프로젝트마다 같은 architecture 또는 고정 관문을 강제하는 표시
- source 근거 없는 제목·관계·실행 결과
- edit event만으로 분석·화면 교체·baseline 이동을 하는 동작
- 전역 영향 또는 runtime 결과 단정

## 4. 규칙과 불변식

1. 장면은 entry, 결과를 바꾸는 조건·실패, 상태/transaction 경계, 외부 효과, 결과, 분석 경계에서만 분리한다. 나머지 연속 step은 묶는다.
2. `storyboard`는 `schemaId`, `schemaVersion`, snapshot·basis·generation identity, frame 목록을 가진다. 기존 task-view field의 의미는 바꾸지 않는다.
3. frame은 analysis-local `frameId`, `primaryStepRef`, `stepRefs`, source anchor, role, status를 가진다. 재분석 대응은 정규화한 callable/symbol identity와 role에서 만든 `frameMatchKey`가 정확히 하나일 때만 한다. snapshot ID와 line/byte offset은 key에 넣지 않는다.
4. 기본 화면은 frame만 표시한다. 전체 step, 전체 edge, 전체 source context는 사용자의 명시적 확장 전 표시하지 않는다.
5. 네 화면 표면은 같은 frame 선택을 공유한다. relation 탐색 뒤 원래 frame과 scroll 위치를 복원한다.
6. edit, watcher, SSE, background publication은 분석이나 화면 교체를 시작하지 않는다.
7. 모든 source·relation은 같은 immutable snapshot과 Evidence를 사용한다. partial·unknown은 추측으로 완성 상태를 만들지 않는다.
8. source read-only, secret redaction, authorization, canonical path, persisted schema, 비-Live CLI·MCP 결과를 유지한다.

## 5. 수용 기준

- FA-01: 조회 결과는 storyboard 장면과 source를 표시하며 raw step 목록을 기본 표시하지 않는다.
- FA-02: architecture 정보를 확인하지 못해도 장면을 구성한다.
- FA-03: 네 화면 표면의 장면 선택은 같은 frame·source 위치를 유지한다.
- FA-04: 재분석은 사용자 명령으로만 실행되고, 정확히 하나의 대응 frame만 복원한다.
- FA-05: 비교는 사용자가 고른 두 분석에만 적용하며 변경 표시는 비교 중에만 보인다.
- FA-06: direct relation은 같은 snapshot의 확인된 관계만 보여 주며, relation 왕복은 원래 frame·source 위치·scroll·비교 상태를 복원한다.
- FA-07: Live 전용 route, 자동 갱신, UI, asset, test, 문서, 계약은 inventory와 호환성 검증 뒤 제거한다.
- FA-08: 보존 대상의 schema, 저장, source read-only, redaction, authorization, CLI·MCP는 회귀하지 않는다.

## 6. 구현 순서와 검증

1. VS-01: versioned storyboard 결과와 기본 화면 선택 동기화
2. VS-02: 명시적 재분석·비교와 frame 대응
3. VS-03: 직접 relation과 source·Radar 왕복
4. VS-04: Live inventory, 제거, 호환성 검증

각 slice는 unit, HTTP/contract, browser interaction 증거를 제공한다. 적용 시 `make build-ui`, `make test-ui`, `make fmt`, `make check-naming`, `make vet`, `make test`를 실행한다. snapshot·storage·request 경계 변경에는 `go test -race ./internal/...`도 실행한다. 실제 구현 전에는 이 계약의 child가 Proposed 상태임을 유지한다.

## 7. 완료 기준

FA-01부터 FA-08의 실행 증거, Live caller·consumer inventory, 제거/보존 분류, source·schema·security 회귀 검증이 모두 있어야 완료다. 사용자 이해 평가는 별도 기록이며, 결과가 없으면 사용자 가치 향상을 주장하지 않는다.
FlowView에서 제공하는 모든 기능은 정상 작동 해야한다.
