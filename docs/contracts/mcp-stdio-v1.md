# CodeFlow MCP stdio v1

The first JSON-RPC request must be `initialize` with `protocolVersion` set to
either `2026-07-28` or `2025-11-25`. Any other version returns the typed
`UNSUPPORTED_PROTOCOL` error and performs no repository work.

Both negotiations expose `tools/list` and one `tools/call` handler for
`workspace`, `current`, `diff`, `step`, `unknowns`, `refresh`, and `open`.
Tool results put the requested causal data in `structuredContent` and a short
human-readable summary in text content. The MCP projection removes duplicate
full manifests while retaining repository, revision, dirty state, worktree
fingerprint, and `manifest_count`; per-step anchors and facts remain unchanged.
The stdio layer attaches to `.codeflow/runtime.json`; it never opens SQLite,
launches an adapter, or starts a competing Core.
