# CodeGraph HTTP profile v1

CodeFlow uses CodeGraph only through `GET /health`, `GET /api/v1/tools`, and
`POST /api/v1/tools/call`. It calls `analyze_code_relationships` with
`repository`, exact `entry_point`, and `contract_version: "1"`.

The result must contain `relationships`, directly or inside `result`/`data`.
Each relationship has `kind`, `from`, and `to`; anchors contain repository
relative `path`, `symbol`, byte range, `file_hash`, and `revision`. A `202`
means indexing is pending and is a typed unavailable result, never a flow.
CodeFlow verifies every returned anchor against its own current manifest.
