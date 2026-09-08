# Live Semantic Map

Use this mode when a requested feature flow must be recompiled after edits and reported as either verified current or an explicit latest-vs-verified gap.

## Establish the Live Request

1. Call `query_task_view` in `feature` mode with the user's natural-language request, exact flow, entry symbol, or domain. Follow the tool's registered schema instead of inventing fields.
2. Resolve any `ambiguous_target` with the user. A feature query must be established before edit-driven compilation can produce a relevant current result.
3. If visual review was requested, call `open_review` through the same MCP server and target. Do not start a separate `codeflow view` process and expect MCP edit events to share its in-memory coordinator.

## Submit an Agent Edit

After making an authorized source edit, call `submit_versioned_edit` for each changed source document that participates in the task:

- `target`: exact project root used by the query
- `path`: repository-relative path
- `content`: complete post-edit file content, not a diff
- `documentVersion`: an integer greater than that document's current workspace version
- `source`: `agent_transaction` for agent work, `ide_versioned` for an IDE integration, or `watcher_fallback` only for a real watcher capture

The tool submits bytes to the semantic snapshot engine. It does not replace the actual repository edit. If CodeFlow returns a version conflict with the current version, use the reported value plus one and retry once with the same post-edit content. Do not guess a new version when the current value is unavailable.

Normal filesystem saves are not automatically ingested by the current product path. If no IDE or watcher integration called `submit_versioned_edit`, state that the live compiler has not observed the edit.

## Observe Compilation

1. Call `get_workspace_activity` after submission. Expected progress is `editing` or `reconciling`, then `analyzing`, then a terminal current-or-gap result. The default coalescing checkpoint is about two seconds.
2. Avoid tight polling. Recheck after the checkpoint or when the user asks for progress.
3. Call `get_current_answer` for the active request or flow.
4. If current is returned, call `get_generation_proof` before describing it as verified current.
5. If current proof is unavailable, call `get_verified_gap` and report its affected scope, lag, pending revisions, and causes. Keep the last verified generation historical.

## Interpret the Result

- `generation.published` plus a matching proof means the exact semantic generation passed current-publication validation.
- `generation.gap` means the latest edit was observed but could not be promoted to current. This is a valid safety result, not a failed answer to hide.
- `settlement=pending` may coexist with `freshness=current`; do not call it complete or passed.
- A new workspace epoch makes earlier generations historical. Never compare or merge them as if they shared one current basis.

For a visual session, FlowView should show SSE connection state, workspace activity, epoch, pending revisions, analysis lag, current proof, or the Verified Gap banner. Preserve the returned selection and identities when the UI refreshes.
