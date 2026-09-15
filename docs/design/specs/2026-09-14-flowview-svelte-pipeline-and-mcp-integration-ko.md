# FlowView Svelte 파이프라인 및 MCP 연동 복구 스펙

- Contract ID: `FLOWVIEW-SVELTE-PIPELINE-MCP`
- Contract Status: Superseded
- Superseded By: `docs/design/specs/2026-09-14-flowview-code-comprehension-ko.md`
- Created: 2026-09-14
- Intent Status: Hardened
- Source: 사용자 요청 ("Now i get it. this cause in the process of improving frontend to svelt. 그럼 이 문제를 해결하기 위한 slices들을 바로 작성하라.")
- Decision Records: [`docs/design/decisions/2026-09-14-flowview-svelte-pipeline-and-mcp-integration-ko.md`](../decisions/2026-09-14-flowview-svelte-pipeline-and-mcp-integration-ko.md)
- Glossary: [`docs/design/glossary.md`](../glossary.md)

---

## 1. Problem and Goal

FlowView 프론트엔드를 레거시 단일 HTML에서 모듈형 Svelte 5 단일 번들로 현대화하는 과정에서 백엔드 데이터 파이프라인 및 MCP 연동 인터페이스 간의 단절이 발생했다.
이로 인해 사용자가 "~~~의 변경된 코드 흐름을 FlowView로 보여줘"라고 요청했을 때, 실제 소스 코드 조각을 가져오지 못하고(`flowContexts` 누락), 브라우저가 열려도 파라미터를 해석하지 못해 기본 샘플 데이터만 표시되거나 분석이 실패하는 문제가 발생했다.

본 스펙의 목표는 **사용자 프롬프트 요청 → MCP/CLI 코드 흐름 분석 → FlowView 실시간 렌더링(스토리보드, 소스 코드 조각, 레이더)**으로 이어지는 엔드투엔드 파이프라인의 데이터 정합성을 완전 복구하는 것이다.

### Intent and Goal IDs
- **INT-PIPELINE-01**: 프론트엔드-백엔드-MCP 간의 FlowView 데이터 파이프라인 단절을 해결하여 실제 소스 코드와 비즈니스 관문이 정확히 렌더링되도록 한다.
- **GOAL-PIPELINE-01**: MCP `query_task_view` 및 CLI `codeflow query` 응답에 `flowContexts` 소스 코드 조각을 완전 복구한다.
- **GOAL-PIPELINE-02**: FlowView Svelte 프론트엔드(`main.ts`)에서 `flow` 및 `flowId` URL 파라미터를 정상 파싱하여 백엔드 API와 양방향 연동한다.
- **GOAL-PIPELINE-03**: 자연어 질의 실패 시 유효 후보 목록을 명확히 제공하고 심볼 해결 안정성을 보장한다.
- **GOAL-PIPELINE-04**: Antigravity CLI MCP 스키마 등록 및 프롬프트 → 시각화 E2E 파이프라인 검증을 완료한다.

---

## 2. Scope

### In Scope
1. **MCP 및 CLI `flowContexts` 응답 주입**:
   - `internal/mcp/semantic_handlers.go`: `handleQueryTaskView`에 `DeriveFlowContext` 호출 및 `"flowContexts": flowContexts` 반환 추가
   - `cmd/codeflow/query.go`: `--json` 출력에 `flowContexts` 필드 포함
2. **FlowView Svelte 프론트엔드 URL 부트스트랩 연동**:
   - `web/flowview/src/main.ts`: URL 쿼리 파라미터 `flow`, `flowId` 파싱 및 `/api/task/view?mode=feature&flowId=...` 호출
   - 검색창 질의 시 한글/자연어 검색 실패 안내 및 후보군 선택 UX 개선
3. **심볼 해결 및 다국어 질의 폴백**:
   - `internal/semantic/query.go`: `ResolveFeatureQueryTarget`의 후보 타겟 에러 상세화
4. **Antigravity CLI MCP 스키마 배포**:
   - `HOME/.gemini/antigravity-cli/mcp/codeflow/` 디렉토리에 툴 스키마 JSON 배포 및 `install.sh` 동기화

### Non-Goals
- FlowView UI에 컴파일러 내부 텔레메트리(에포크 번호, 지연시간 ms 등) 노출 (Anti-Telemetry 규칙 준수)
- 퍼블릭 JSON 스키마의 하위 호환성 파괴

---

## 3. Actors and Preconditions
- **Primary Actor**: 프롬프트나 웹 UI를 통해 코드베이스의 비즈니스 흐름을 탐색하려는 개발자
- **System Preconditions**:
  - `codeflow` 바이너리 설치 및 `codeflow view` 또는 `codeflow mcp` 구동 가능 환경

---

## 4. Feature-Level Acceptance
- **FA-01**: WHEN MCP `query_task_view` 또는 CLI `codeflow query --json`이 성공적으로 실행될 때, THE 시스템은 각 단계별 소스 코드 라인(`displayedLines`, `canonicalPath`, `isHit`, `isStruct`)이 담긴 `flowContexts` 객체를 반드시 반환해야 한다.
- **FA-02**: WHEN 사용자가 `?flow=<flowId>` 또는 `?flowId=<flowId>` URL로 FlowView에 접속할 때, THE 프론트엔드는 해당 흐름의 데이터를 백엔드에서 조회하여 스토리보드 및 코드 패널에 즉시 렌더링해야 한다.
- **FA-03**: WHEN 사용자가 프롬프트로 특정 기능의 흐름 시각화를 요청할 때, THE 에이전트는 올바른 심볼 타겟을 식별하여 FlowView를 열고 실제 소스 코드 조각을 표시할 수 있어야 한다.

---

## 5. Done When
- 모든 슬라이스 테스트 통과
- `make fmt && make vet && make test` 전체 성공
- `make build-ui` 후 번들된 단일 파일에서 `flowContexts` 및 `flowId`가 정상 동작함을 검증
