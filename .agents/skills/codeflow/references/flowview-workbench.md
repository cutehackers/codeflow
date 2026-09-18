# Paths 2 & 3: FlowView Workbench & Re-analysis Guide

This guide covers opening interactive FlowView reviews, restoring saved workbenches, and updating analyses after code modifications.

---

## 1. Visual Review in FlowView (`open_review`)

Open the interactive 3-column FlowView workbench for visual inspection:

```json
{
  "name": "open_review",
  "arguments": {
    "target": ".",
    "flowId": "flow-a1b2c3d4e5f6..."
  }
}
```

### Parameter Contract
- **Pass `flowId`**: Always pass the `flowId` received from `analyze_flow` or `publish_core_flow`.
- **Do NOT pass `entrySymbolPath`**: `open_review` accepts only `flowId` or `viewId`. Passing `entrySymbolPath` will fail or produce an unlinked view.
- **Return Payload**:
  ```json
  {
    "status": "ready",
    "flowId": "flow-a1b2c3d4e5f6...",
    "viewId": "tv-9f8e7d6c...",
    "url": "http://127.0.0.1:45678/?token=sec-token...&flow=flow-a1b2c3d4e5f6...",
    "token": "sec-token..."
  }
  ```

### Presenting to the User
Provide the authenticated `url` directly as a clickable link. Inform the user of the 3-column layout:
- **Left Column (Macro Storyboard)**: 4 to 7 macro gateways (`entry`, `decision`, `process`, `effect`, `result`, `boundary`) representing high-level business milestones.
- **Center Column (Execution Canvas)**: Chronological timeline of 1:N micro-steps traversing architectural layers.
- **Right Column (Inspector & Radar)**: Verifiable source code snippets with exact line highlights, plus the 1st-degree Blast Radius Radar.

---

## 2. Restoring Saved Task Views

When returning to a previously saved review session, restore it using `viewId`:

```json
{
  "name": "open_review",
  "arguments": {
    "target": ".",
    "viewId": "tv-9f8e7d6c..."
  }
}
```

### Restoration Characteristics
- **Zero Re-analysis Overhead**: Restores the exact persisted view state instantly without re-running AST collectors or slicers.
- **Context Preservation**: Preserves user reading position, active gateway selection, and expanded accordion items.

---

## 3. Explicit Re-analysis Workflow (Post-Edit Protocol)

CodeFlow enforces a deliberate, watcher-free architecture. It does **not** run background file watchers or silently mutate view states while developers write code.

### Step-by-Step Re-analysis Protocol

```
Developer modifies code
          │
          ▼
1. analyze_flow(target, entrySymbolPath)  <── Explicit re-analysis trigger
          │
          ▼
2. open_review(target, flowId)             <── Refresh authenticated workbench
```

1. **Trigger Re-analysis**:
   ```json
   {
     "name": "analyze_flow",
     "arguments": {
       "target": ".",
       "entrySymbolPath": "lib/features/auth/login_controller.dart#LoginController.submit"
     }
   }
   ```
   - CodeFlow takes a new worktree snapshot, extracts fresh AST facts, and relinks step anchors against the updated file bytes.

2. **Re-open FlowView**:
   ```json
   {
     "name": "open_review",
     "arguments": {
       "target": ".",
       "flowId": "flow-a1b2c3d4e5f6..."
     }
   }
   ```
   - Present the refreshed workbench URL to the user.

3. **Comparison with Baseline (Optional)**:
   - If comparing before/after changes, compare against an explicitly pinned baseline rather than expecting live streaming deltas.
