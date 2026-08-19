# Semantic overlay v1

`POST /api/v1/overlay/import` accepts an explicitly supplied JSONL export.
Only `decision`, `intent`, `rename`, and `user_message` events are normalized.
Events containing secret-like material are discarded before an overlay is made;
the raw export is never written to disk.

Imported candidates are runtime-only and have status `inferred`. `POST
/api/v1/overlay/approve` with a candidate ID writes only its normalized text,
source class, and stable overlay ID to `.codeflow/knowledge/confirmed.json`.
Neither importing, approving, nor deleting an overlay changes FlowIR or a delta.
