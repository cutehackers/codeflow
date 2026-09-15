# Decision: Svelte FlowView 파이프라인 및 MCP 연동 복구

- Decision ID: `DEC-FLOWVIEW-SVELTE-PIPELINE-MCP-01`
- Status: Approved
- Date: 2026-09-14
- Context: Svelte 5 모듈형 프론트엔드로 FlowView를 전환하는 과정에서, MCP `query_task_view` 및 CLI `codeflow query` 응답에서 소스 코드 조각(`flowContexts`)이 누락되고, 프론트엔드가 `flow`/`flowId` URL 파라미터를 읽지 못해 사용자 요청("~~~의 변경된 흐름을 FlowView로 보여줘") 시 흐름 분석 및 소스 코드 렌더링이 실패하는 문제 발생.
- Decision:
  1. `internal/mcp/semantic_handlers.go` 및 `cmd/codeflow/query.go`에서 `DeriveFlowContext`를 호출하여 `"flowContexts"`를 응답 JSON에 의무 포함한다.
  2. `web/flowview/src/main.ts`에 `flow` 및 `flowId` URL 파라미터 파싱을 추가하고, `/api/task/view?mode=feature&flowId=...` 또는 `/api/flow?id=...`를 통해 해당 흐름 데이터를 로드하도록 연결한다.
  3. `internal/semantic/query.go`의 `ResolveFeatureQueryTarget`에서 자연어 질의 실패 시 빈 에러를 던지기 전에 유효한 후보군 목록(`candidateTargets`)을 명확히 제공하고, 에이전트 워크플로가 사용자의 자연어 요청으로부터 심볼을 선제 탐색하도록 규정한다.
  4. Antigravity CLI 환경(`HOME/.gemini/antigravity-cli/mcp/codeflow/`)에 MCP 툴 스키마를 배포하여 에이전트의 도구 체인을 활성화한다.
- Consequences:
  - 장점: 사용자가 프롬프트나 UI에서 흐름을 요청했을 때, 실제 소스 코드 라인(`displayedLines`)과 매크로 스토리보드가 정확하게 즉시 렌더링됨.
  - 주의점: Anti-Telemetry 규칙을 준수하여 `flowContexts` 내에 컴파일러 내부 에포크나 벤치마크 지표가 노출되지 않도록 유지한다.
