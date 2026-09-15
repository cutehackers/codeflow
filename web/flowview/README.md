# FlowView Frontend (`web/flowview`)

The next-generation modular frontend for CodeFlow FlowView, built with **Svelte 5 Runes**, **TypeScript**, and **Vite Singlefile**.

It compiles into a zero-dependency, self-contained single HTML bundle (`dist/index.html`) embedded directly into the Go distribution binary.

For the comprehensive coding rules, conventions, and architectural guardrails, refer to [`docs/guides/FRONTEND_STANDARDS.md`](../../docs/guides/FRONTEND_STANDARDS.md).

## Quick Start

```bash
# Install dependencies
npm install

# Run Vite development server
npm run dev

# Strict TypeScript & Svelte diagnostics check
npm run check

# Run Vitest unit tests
npm test

# Build singlefile bundle (dist/index.html)
npm run build
```

Or use the root Makefile:
```bash
make build-ui
make test-ui
```

## Architecture

- **State Management**: Svelte 5 Runes store (`src/stores/flowStore.svelte.ts`) with `$state` and `$derived`.
- **Macro Context Storyboard**: Horizontal sequence track showing end-to-end business execution gates (`src/components/MacroStoryboard.svelte`).
- **Blast Radius Radar**: Pure SVG concentric circles showing direct callers, state mutations, and tests (`src/components/BlastRadiusRadar.svelte`).
- **Code & Process Panels**: Synchronized source citations with before/after diffs (`src/components/CodeFlowPanel.svelte`) and business behavior rules (`src/components/ProcessFlowPanel.svelte`).
- **Zero-Telemetry Guard**: Only business flow traversals, architecture layers, and verifiable source code are shown. Internal compiler metrics and heuristics are strictly prohibited.
