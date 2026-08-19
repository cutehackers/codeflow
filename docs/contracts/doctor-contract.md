# `codeflow doctor` compatibility and output contract (v1)

`codeflow doctor` is a read-only readiness check. It never creates or modifies
`codeflow.yaml`, `.codeflow/`, caches, locks, Git refs, index state, or the
inspected worktree.

## Invocation

```text
codeflow doctor [--repo DIR] [--format human|json] [--codegraph-url URL] [--adapter PATH]
```

`--format json` writes exactly one JSON report to stdout; diagnostics never
precede or follow it. The JSON shape is represented by
[`doctor-ready.json`](./fixtures/doctor-ready.json).

## Exit status

| Code | Meaning |
| --- | --- |
| 0 | Every required check is `ready`; warnings, including absent config, are allowed. |
| 1 | One or more required checks are `unavailable` or `invalid`. |
| 2 | One or more checks are `incompatible`. |

An absent `codeflow.yaml` is never a required missing input: it produces an
`unconfigured` configuration check and warning, while unique-only discovery
remains available. Unknown fields are warnings. Only schema version `"1"` is
accepted. Feature aliases are validated only for their single `entry_point`
shape; resolving their source target belongs to CF-G04.

## Compatibility probes

- Git: `git rev-parse --show-toplevel`. An unborn HEAD is usable and reported.
- CodeGraph: read-only `GET /health`, `GET /api/v1/status`, and
  `GET /api/v1/tools` at `CODEFLOW_CODEGRAPH_URL` (default
  `http://127.0.0.1:8000`). Health must succeed, status must be valid JSON, and
  tools must list the CodeFlow v1 discovery profile:
  `add_code_to_graph`, `analyze_code_relationships`, `find_code`, and
  `list_indexed_repositories`. The status response is not interpreted as a
  version or capability declaration. Doctor never sends an index or analysis
  request.
- Dart/Flutter: prefer a repository-local `.fvm/flutter_sdk/bin/flutter` when
  present; otherwise use `dart` from `PATH`. The selected Dart SDK must be at
  least 3.10.
- Dart adapter: invokes its owned `--probe` health/version protocol and requires
  JSON with `protocol_version: "1"` and `status: "ready"`.

The fixture reports in this directory are stable examples for machine clients,
not a golden analysis corpus.
