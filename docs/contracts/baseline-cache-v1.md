# Baseline cache v1

`.codeflow/cache/baselines` contains reconstructable analysis inputs only. It
is never authoritative evidence and deleting it cannot change a published
FlowIR document, confirmed knowledge, Git data, or product source.

Each revision mirror may contain:

- `*.dart`
- `pubspec.yaml` and `pubspec_overrides.yaml`
- `analysis_options.yaml`
- `codeflow.yaml`
- `codeflow.external-contracts.json`

No documentation, binary, media, build output, secret file, runtime token,
SQLite state, or confirmed knowledge belongs in the cache. A mirror is built
in a staging directory and renamed into place only after all selected Git blobs
have been read successfully.

CodeFlow retains three recently used revision mirrors during normal
operation. Mirrors touched within the last hour are conservatively treated as
possibly active concurrent readers and are pruned on a later operation. This
grace period is hard-bounded to eight mirrors, so repeated local comparisons
cannot grow the cache without limit. Users can inspect exact usage with
`codeflow cache status` and explicitly delete all reconstructable baseline
mirrors with `codeflow cache clean`.
