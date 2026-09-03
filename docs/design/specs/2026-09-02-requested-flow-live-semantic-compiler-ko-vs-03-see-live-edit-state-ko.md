# 요청 흐름 이해와 실시간 Semantic Compiler — VS-03 편집 상태와 snapshot을 즉시 확인한다

- Contract ID: `REQUESTED-FLOW-LIVE-SEMANTIC-COMPILER-VS-03`
- Contract Status: Proposed
- Independent Review: Passed (`gpt-5.6-terra`, high)
- Parent Contract: `docs/design/specs/2026-09-02-requested-flow-live-semantic-compiler-ko.md`
- Created: 2026-09-02
- Source: `docs/design/raw/requested-flow-live-semantic-compiler-architecture-draft-ko.md`
- Decision Records: `docs/design/decisions/requested-flow-live-semantic-compiler-decisions-ko.md`

## 1. Intent and Goal

- Intent ID: `INT-02`
- Parent Intent: coding agent 구현 변경이 현재 요청 흐름의 의미를 최신성·근거·차이와 함께 계속 파악한다.
- Goal ID: `GOAL-03`
- Slice Goal: 사용자는 coding agent 또는 IDE 편집이 immutable Document Revision과 Workspace Snapshot으로 수집되고 `editing` 또는 관련 활동으로 표시되는 것을 확인한다.
- User or Caller Value: 분석 중인 입력 시점을 고정해 서로 다른 파일 버전이 한 결과에 섞이는 것을 막고, 사용자는 현재 분석이 어디까지 따라왔는지 안다.
- Contribution to Parent: Raw P2의 versioned virtual workspace와 Activity Channel을 후속 currentness·publication 검증이 신뢰할 수 있는 입력으로 만든다.
- Parent Acceptance: `A5–A6`, `A9`, `A25–A27`

## 2. User Outcome

편집 이벤트가 수신되면 시스템은 변경 bytes와 document version을 불변 revision으로 기록하고 새 liveHead snapshot을 만든다. 사용자는 활동 상태, pending revision과 영향받을 수 있는 task scope를 확인하며, 분석기는 snapshot VFS를 사용한다.

## 3. Scope

### In Scope

- versioned Edit Ingress와 명시적 multi-file edit transaction
- immutable DocumentRevision, persistent WorkspaceSnapshot, `liveHead`, workspace epoch와 sequence
- content-addressed bytes, dirty content, optional Git provenance와 snapshot VFS
- watcher fallback의 `stat-before → read → stat-after + content hash`
- rename, delete, event loss, recrawl과 Watchman/전체 스캔 reconciliation 신호
- `changeBatch`와 2초 publication coalescing에 사용할 pending revision/activity metadata
- `editing`, `analyzing`, `reconciling` activity 표시와 `analysisLagMs`, `pendingRevisions`
- Dart, TypeScript/JavaScript와 Go parser/hot-path adapter가 snapshot lease/overlay 밖의 OS filesystem을 읽지 않는 경계

### Out of Scope

- closure validation, Generation Proof Manifest, active pointer CAS와 current/gap publication — VS-04가 담당한다.
- semantic meaning, delta, requirement alignment와 user projection 변경 — VS-02, VS-05가 담당한다.
- source mutation, model enrichment와 runtime execution — raw의 별도 경계를 따른다.

## 4. Preconditions

- Raw P0 Exit Gate와 VS-02 baseline seam이 존재한다.
- VS-01 adapter boundary가 snapshot basis와 overlay를 전달받을 수 있다.
- edit source가 agent transaction, IDE/LSP versioned change 또는 watcher fallback 중 하나로 식별된다.
- `.codeflow` 상태를 위한 local storage 권한이 있다.

## 5. Public Seam

Edit Ingress, workspace snapshot query, activity/status stream과 adapter snapshot lease가 공개 seam이다. 사용자는 live status에서 최신 snapshot sequence, pending revision과 분석 활동을 보고, caller는 snapshot ID와 basis를 후속 분석 요청에 전달한다.

## 6. Boundary Coverage

Agent/IDE versioned edit 또는 filesystem signal → Edit Ingress → DocumentRevision/content CAS → WorkspaceSnapshot/liveHead → snapshot lease/VFS → activity event와 pending revision status → downstream analysis input.

## 7. Inherited Invariants

- `INV-07`, `INV-14`, `INV-15`, `INV-16`, `INV-19`, `INV-21`
- Raw D8, D18, D23, §7.1–§7.2, §7.5, §10.2–§10.6, §18.4 R8.
- 분석 중인 snapshot은 변경되지 않으며 watcher event는 분석 bytes의 권위가 아니다.

## 8. Slice-Specific Rules

- `contentId`, `documentVersion`, path, source와 workspace epoch를 가진 revision은 생성 후 수정하지 않는다.
- 명시적 multi-file transaction은 staging된 revision을 transaction 경계에서 하나의 snapshot으로 반영한다. transaction이 없으면 versioned edit마다 독립 snapshot을 만든다.
- watcher capture는 stat/hash 일치가 확인될 때만 revision을 만들고 rename, delete와 event loss는 reconciliation 대상으로 보존한다.
- parser와 adapter는 snapshot lease를 통해서만 source bytes를 읽으며 product source를 수정하지 않는다.
- 2초 window는 편집 수집을 멈추는 debounce가 아니라 downstream publication coalescing을 위한 상태다.
- branch/worktree 전환은 새 workspace epoch를 만들고 이전 epoch 결과의 current 승격은 VS-04에서 금지한다.

## 9. Acceptance Criteria

- VS03-A1. WHEN versioned edit가 수신되면, THE system SHALL bytes, path, monotonic document version과 source를 가진 immutable DocumentRevision을 생성한다.
- VS03-A2. WHEN revision이 수락되면, THE system SHALL 해당 revision을 참조하는 immutable WorkspaceSnapshot을 만들고 `liveHead`를 원자적으로 갱신한다.
- VS03-A3. WHEN 여러 파일의 명시적 edit transaction이 종료되면, THE system SHALL transaction의 변경을 하나의 snapshot sequence로 관찰할 수 있게 한다.
- VS03-A4. WHEN watcher fallback이 사용되면, THE system SHALL stat-before/read/stat-after와 content hash가 일치한 bytes만 revision으로 저장하고 event loss·rename·delete를 reconciliation 대상으로 표시한다.
- VS03-A5. THE system SHALL Dart, TypeScript/JavaScript와 Go parser/hot-path adapter가 snapshot VFS의 `computedBasisId`와 동일한 bytes만 읽도록 하며 OS filesystem 재읽기를 current 분석 입력으로 허용하지 않는다.
- VS03-A6. WHEN edit가 수락되면, THE system SHALL raw activity가 아닌 `editing` 상태, pending revision, 영향 가능 scope와 analysis lag를 사용자에게 P95 300ms 안에 표시한다.
- VS03-A7. WHEN branch, worktree 또는 compiler configuration이 바뀌면, THE system SHALL workspace epoch를 분리하고 이전 epoch 결과를 새 current 입력으로 재사용하지 않는다.
- VS03-A8. THE system SHALL revision, snapshot, change batch와 activity payload에 등록된 schema identity와 basis reference를 제공하며 source를 수정하지 않는다.

## 10. Failure Semantics

| Condition | Observable Result | Side Effects | Recovery |
|---|---|---|---|
| edit bytes와 version 불일치 | invalid edit 또는 retry 상태 | 잘못된 revision 생성 없음 | caller가 monotonic version으로 재전송 |
| capture 중 파일 재변경 | editing/capture retry 상태 | 일관되지 않은 bytes 저장 없음 | stat-read-stat을 제한적으로 재수행 |
| watcher event loss/recrawl | reconciling과 영향 범위 | 누락 event를 조용히 삭제하지 않음 | clock 또는 전체 scan으로 snapshot 재구성 |
| rename/delete | 명시적 path change | before/after identity 보존 | downstream closure가 새 snapshot을 검증 |
| transaction 중단 | incomplete transaction 상태 | partial snapshot을 liveHead로 교체하지 않음 | transaction 취소 또는 재제출 |
| branch/worktree 전환 | 새 epoch와 이전 결과 historical 표시 | epoch 혼합 없음 | 새 epoch에서 baseline/analysis 시작 |
| storage write failure | activity error | liveHead와 이전 snapshot 보존 | storage 복구 후 같은 edit 재제출 |

## 11. Data and Interaction Contract

- Input: versioned edit `{path, bytes, documentVersion}`, optional transaction ID/close boundary, or watcher signal.
- Output: `codeflow.document-revision`, `codeflow.workspace-snapshot`, `codeflow.change-batch`, activity status와 `liveHead` reference.
- Snapshot은 `parentSnapshotId`, repository/worktree, epoch, sequence, configuration fingerprint와 changed entries를 보유한다.
- Downstream consumer는 `computedBasisId`와 snapshot lease를 받아야 하며, filesystem path만 받아 임의로 재읽지 않는다.
- Activity 상태는 SemanticMap generation artifact와 별도 stream으로 유지한다.

## 12. Test Seam and Evidence

- Public seam: Edit Ingress, snapshot query, activity stream, adapter snapshot VFS conformance.
- Required test level: rapid edit, multi-file transaction, version ordering, stat-read-stat capture, watcher gap/reconciliation, epoch switch, source read-only와 Dart/TypeScript/Go snapshot-read conformance.
- Replaceable external boundaries: IDE/LSP event source, filesystem watcher, Watchman clock, Git CLI, clock, content CAS and local storage.
- Evidence per criterion: VS03-A1/A2 revision·snapshot fixture, A3 transaction fixture, A4 watcher adversarial fixture, A5 Dart/TypeScript/Go adapter overlay conformance, A6 activity acknowledgement trace, A7 epoch fixture, A8 schema and immutability checks.

## 13. Verification Plan

| Check | Applicability / Trigger | Exact Command | Expected Evidence | Required for Completion |
|---|---|---|---|---|
| Acceptance behavior | Always | `go test ./internal/workspace ./internal/watch ./internal/storage ./internal/e2e` | immutable capture, liveHead, transaction, watcher/reconciliation과 epoch behavior pass | Yes |
| Slice tests | Always | `go test ./internal/workspace ./internal/watch ./internal/storage` | VS-03 package tests pass | Yes |
| Type, static analysis, and lint | Go storage/watch/workspace affected | `go vet ./...` | no applicable findings | When applicable |
| Affected build | Core workspace path affected | `go build ./...` | Core builds successfully | When applicable |
| Architecture and policy | revision/snapshot boundary changes | `go test ./internal/contractharness -run 'TestValidateGoldenFixtures|TestValidateExportedContractBoundary'` | workspace, edit and change-batch fixtures validate | Yes |
| Regression | shared storage or watch behavior affected | `go test ./...` | full Go regression suite passes | Yes |
| Security | source bytes and snapshot artifacts are captured | `go test ./internal/secret ./internal/workspace ./internal/storage` | no source mutation and redaction policy passes | Yes |
| Data and migration compatibility | persistent workspace artifacts affected | `go test ./internal/contractharness` | schema identity, version and fixture compatibility pass | Yes |
| Performance and concurrency | raw A5 activity acknowledgement and VS03-A6 are in scope | `go test ./internal/e2e -count=1` | the edit trace reports activity acknowledgement P95 ≤300ms, pending revision and analysis-lag evidence | Yes |
| Reliability and flake | rapid edits, watcher gap or capture retry affected | `go test ./internal/workspace ./internal/watch ./internal/e2e -count=1` | repeated capture/reconciliation behavior is stable | Yes |
| Coverage | No repository coverage threshold is configured | `N/A — raw defines behavior and SLO, not a Go coverage percentage` | reason recorded | No |
| Adapter snapshot conformance | adapter reads workspace snapshot | `cd adapters/dart && dart test` | Dart snapshot overlay tests pass | When applicable |
| Adapter snapshot conformance | TypeScript/JavaScript adapter reads workspace snapshot | `node adapters/typescript/test/index.test.js` | TypeScript/JavaScript snapshot overlay tests pass | When applicable |
| Adapter snapshot conformance | Go adapter reads workspace snapshot | `go test ./adapters/go/...` | Go snapshot overlay, read-set and no-OS-re-read tests pass | When applicable |
| Browser UX | activity status, pending revision and snapshot context are user-facing; raw §21.10 UX verification is required before completion | `npm --prefix web/live-comprehension-workspace run test:e2e -- --project=chromium` | Playwright proves edit acknowledgement, activity state, pending revision, snapshot context and epoch transition | Yes |
| Accessibility | activity/status surface is user-facing | `npm --prefix web/live-comprehension-workspace run test:a11y` | `@axe-core/playwright`, keyboard navigation, screen-reader outline, reduced motion and contrast evidence pass | Yes |

## 14. What Could Be Wrong

- Assumption: IDE/LSP versioned edit input or the watcher reconciliation path can observe every content change needed by the active task.
- Consequence: a missing revision causes a false currentness result or an unexplained stale gap.
- Validation method: rapid-edit, rename, delete, recrawl and event-loss traces compare captured snapshot sequences with ground-truth file identities and hashes.

## 15. Done When

- VS03-A1–A8 are covered by immutable fixture and public status evidence.
- No downstream consumer needs to read the mutable filesystem for hot-path analysis.
- Event loss, recapture, epoch switch and transaction failure are visible and recoverable.
- Every applicable Verification Plan row passes or each N/A reason is recorded.
- No production code, schema or raw source is changed by this slicing task.

## 16. Open Decisions

없음. revision·snapshot의 물리 저장 구조는 raw D8과 Contract Gate를 따른다.
