# CodeFlow Development Environment

This document prepares a workstation for the goal-driven tickets in [implementation-tickets.md](./implementation-tickets.md). It does not begin an implementation ticket.

## Repository state

The repository is initialized with Git. Before implementation begins, the owner may choose a remote and create the first commit. Development setup does not make either choice automatically.

Run the local environment check:

```bash
make doctor-dev
```

The command is read-only. It reports installed tools and exits non-zero when a required local tool is missing.

## Validated workstation

The following versions were present when this environment was prepared on 2026-08-19. They are reference versions, not the final compatibility floor; CF-G01 will establish enforceable version contracts.

| Tool | Validated version |
|---|---|
| Go | 1.26.2, darwin/arm64 |
| Flutter | 3.44.8 stable |
| Dart | 3.12.2 stable |
| Node.js | 22.23.2 |
| npm | 10.9.8 |
| SQLite CLI | 3.51.0 |
| uv | 0.8.12 |
| jq | 1.8.1 |

## CodeGraphContext

The `cgc` executable is not globally installed. The official recommended development path supports running it on demand through `uvx`:

```bash
uvx codegraphcontext --help
uvx codegraphcontext doctor
uvx codegraphcontext api start --host 127.0.0.1 --port 8080
```

Using `uvx` keeps the Python tool isolated from the repository. The first invocation may access the network and populate the user's uv cache. CodeFlow must still negotiate and validate the actual CodeGraph API version at runtime; successful installation alone is not compatibility evidence.

Developers who prefer a persistent isolated installation may use:

```bash
pipx install codegraphcontext
cgc doctor
```

Do not commit CodeGraph databases, CodeFlow runtime state, dependency caches, build outputs, or environment secrets.

## Repository conventions

- Text files committed by this repository use LF line endings.
- Go files use tabs as produced by `gofmt`; other source and configuration files default to two-space indentation.
- `.brv`, `.codeflow`, editor state, dependencies, generated bundles, coverage, logs, and secrets are local-only.
- `codeflow.yaml` is not globally ignored because the product contract treats it as tracked project configuration.
- Dart `pubspec.lock` is not globally ignored. Its tracking policy will be chosen according to whether the created Dart package is an application or a reusable package.

## Decisions intentionally deferred to implementation

- Go module path, until the repository's canonical remote or module identity is known.
- Minimum supported Go, Dart, Flutter, Node, SQLite, and CodeGraph versions, owned by CF-G01.
- Frontend package manager and bundler, owned by the first FlowView implementation in CF-G02.
- Release signing identity and Homebrew tap, owned by CF-G14.
- Plugin marketplace and distribution identity, owned by CF-G15.
