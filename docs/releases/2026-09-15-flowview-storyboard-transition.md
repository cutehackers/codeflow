# Release Notes: FlowView Storyboard Transition and Live Retirement

- Date: 2026-09-15
- Contract: `FLOWVIEW-STORYBOARD-TRANSITION`
- Slices: `FLOWVIEW-STORYBOARD-TRANSITION-VS-01` ~ `FLOWVIEW-STORYBOARD-TRANSITION-VS-04`

## 1. Summary of Changes

CodeFlow FlowView has transitioned to an end-to-end business Storyboard scene view. The default screen now presents grouped business gateway scenes (`entry`, `decision`, `process`, `effect`, `result`, `boundary`) and primary source contexts rather than raw step lists or automatic background replacements.

## 2. Public Seam Changes & Replacement Entries

| Retired / Superseded Seam | Classification | Replacement Entry | Description |
|---|---|---|---|
| `GET /live` | Superseded | `GET /` | The separate Live Semantic Map route is superseded by the unified FlowView view. Requests to `/live` are routed to the canonical FlowView interface. |
| `?live=1` Query Parameter | Superseded | `GET /` (Default) | FlowView operates as a unified view without requiring or distinguishing a live parameter. |
| SSE `generation.published` auto-refresh | Removed | Explicit "다시 분석" (Re-analyze) UI button & `/api/task/view` | Background workspace edits no longer arbitrarily displace reading position, scroll, or baseline. Analysis runs strictly upon explicit user command. |
| Change Pulse persistent view | Superseded | Comparison Mode (`compare === true`) | Delta tags, rule changes, and surgery indicators are disclosed exclusively when explicitly comparing two analyses. |

## 3. Backward Compatibility & Preserved Guarantees

- **CLI & MCP**: All CLI commands (`cmd/codeflow`) and MCP tools (`internal/mcp`) remain fully supported with zero regressions.
- **Data Schemas & Persistence**: Existing persisted schemas (`schemas/`) and storage readers are fully preserved. A new versioned `storyboard.schema.json` is added additively to task view responses.
- **Source Read-only & Security**: Source read-only invariants, secret redaction, and authorization boundaries remain enforced.
