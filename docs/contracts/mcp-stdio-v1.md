# CodeFlow MCP stdio v1

The first JSON-RPC request must be `initialize` with `protocolVersion` set to
either `2026-07-28` or `2025-11-25`. Any other version returns the typed
`UNSUPPORTED_PROTOCOL` error and performs no repository work.

Both negotiations expose `tools/list` and one `tools/call` handler for
`current`, `diff`, `step`, `unknowns`, `refresh`, and `open`. Tool results put
the unchanged CodeFlow response envelope in `structuredContent` and as JSON
text content. The stdio layer attaches to `.codeflow/runtime.json`; it never
opens SQLite, launches an adapter, or starts a competing Core.
