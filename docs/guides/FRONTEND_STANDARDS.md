# CodeFlow Frontend Standards (TypeScript & Svelte 5)

This document establishes the official frontend architecture standards, design patterns, and engineering conventions for **FlowView** and web components in the CodeFlow project.

---

## 1. Core Principles & Architectural Guardrails

### 1.1 Code Comprehension First
FlowView exists to help developers understand end-to-end business execution paths across architecture layers. Present verifiable source code, explicit layer boundaries, and direct causal relations. Do not clutter the interface with speculative visualizations or arbitrary percentages.

### 1.2 Anti-Telemetry Guard (Mandatory)
> **Rule:** NEVER leak internal compiler, benchmark, or engine telemetry (such as workspace epochs, analysis lag, settlement flags, or arbitrary ML heuristics) into the primary view.

- **User Focus:** Developers need to understand code execution, not compiler execution.
- **Allowed Display:** Architecture layers (`UI`, `UseCase`, `Domain`, `Repository`), step names, conditions (`branch`), side-effects, verified line numbers, direct callers, and related test files.

### 1.3 Single-Binary Zero-Dependency Invariant
- FlowView is distributed as part of a single Go binary via `//go:embed flow_view.html`.
- Node.js or npm is **never** required on end-user machines.
- The web frontend builds into a single self-contained HTML file (using `vite-plugin-singlefile`) with inlined CSS, JavaScript, and SVG assets.
- No external CDN links (no Google Fonts, external scripts, or remote CSS).

### 1.4 Baseline & State Preservation
- Preserve reading position, active selection, and user scroll during background re-analysis.
- Pausing reading (`읽기 고정`) queues incoming updates into `pending` state instead of unexpectedly rewriting active DOM nodes.

---

## 2. Technology Stack & Directory Layout

### 2.1 Technology Stack
- **Framework:** Svelte 5 (Runes exclusively)
- **Language:** TypeScript 5.7+ (`strict: true`)
- **Bundler:** Vite 6 + `@sveltejs/vite-plugin-svelte` + `vite-plugin-singlefile`
- **Testing:** Vitest + `svelte-check`

### 2.2 Directory Layout (`web/flowview/`)
```
web/flowview/
├── dist/                      # Singlefile production bundle (dist/index.html)
├── src/
│   ├── types/                 # Pure TypeScript domain types & interfaces
│   │   ├── flow.ts            # FlowTaskViewData, Step, Edge, SemanticDelta
│   │   └── impact.ts          # ChangeImpactGraph, Caller, StateMutation, TestImpact
│   ├── stores/                # Svelte 5 Runes state stores & sample fixtures
│   │   ├── flowStore.svelte.ts# FlowStore class with $state and $derived
│   │   └── sampleData.ts      # Standalone & fallback sample data generator
│   ├── components/            # Modular UI components
│   │   ├── HeaderBar.svelte   # Top branding, mode badge, and navigation
│   │   ├── IntroSection.svelte# Title, scope summary, and query input form
│   │   ├── NoticeBar.svelte   # Live update notices, compare & pause controls
│   │   ├── ChangePulse.svelte # Verified semantic change pills
│   │   ├── MacroStoryboard.svelte # Horizontal macro gate sequence
│   │   ├── ViewToolbar.svelte # View mode switch (Code vs Process)
│   │   ├── NavRail.svelte     # Left step order navigation rail
│   │   ├── CodeFlowPanel.svelte # Center code cards with line citations & diffs
│   │   ├── ProcessFlowPanel.svelte # Center business process cards
│   │   ├── ContextAside.svelte# Right context pane
│   │   └── BlastRadiusRadar.svelte # Pure SVG concentric blast radius radar
│   ├── __tests__/             # Unit tests (Vitest)
│   │   └── flowStore.test.ts  # Store state transition & reactivity tests
│   ├── app.css                # Design tokens, CSS variables, and base typography
│   ├── App.svelte             # Root layout component (3-column workbench)
│   └── main.ts                # Application mount, SSE wireup, and global test bridge
├── index.html                 # HTML shell for Vite
├── package.json               # Scripts and dependencies
├── svelte.config.js           # Preprocessor configuration
├── tsconfig.json              # Strict TypeScript compiler options
└── vite.config.ts             # Singlefile inlining configuration
```

---

## 3. Svelte 5 & TypeScript Conventions

### 3.1 Svelte 5 Runes Only
- **Prohibited:** Legacy Svelte 3/4 reactive stores (`writable`, `readable`, `derived`, `subscribe`), `$:` reactive declarations, or `export let prop`.
- **Mandatory:** Svelte 5 Runes:
  - `$state(...)` for mutable state.
  - `$derived(...)` or `$derived.by(...)` for computed properties.
  - `$props()` with TypeScript interface for component inputs.
  - `$effect(...)` strictly for side-effects (DOM scrolling, external listeners), never for state synchronization that `$derived` can solve.

#### Component Props Example
```svelte
<script lang="ts">
  interface Props {
    title?: string;
    mode?: string;
    staticLinkUrl?: string;
  }

  let {
    title = 'CodeFlow',
    mode = 'Live Semantic Map',
    staticLinkUrl = '/'
  }: Props = $props();
</script>
```

#### State Store Example (`src/stores/flowStore.svelte.ts`)
```typescript
class FlowStore {
  data = $state<FlowTaskViewData | null>(null);
  selectedStepId = $state<string | null>(null);
  viewMode = $state<'code' | 'process'>('code');

  get steps(): Step[] {
    return this.data?.semanticMap?.steps || [];
  }

  get selectedStep(): Step | null {
    if (!this.steps.length) return null;
    return this.steps.find(s => s.stepId === this.selectedStepId) || this.steps[0] || null;
  }

  select(stepId: string) {
    this.selectedStepId = stepId;
  }
}

export const flowStore = new FlowStore();
```

### 3.2 TypeScript Guidelines
- **Strict Mode:** `tsconfig.json` must have `"strict": true`, `"noImplicitAny": true`, `"strictNullChecks": true`.
- **No `any`:** Use explicit union types, `unknown`, or generic parameters.
- **Initialism Casing:** Follow Google Go / CodeFlow conventions:
  - Use `FlowID`, `URL`, `SVG`, `SSE`, `API`, `HTML` (e.g., `stepId`, `sseUrl`, `staticLinkUrl`).
- **File & Identifier Naming:**
  - Components: `PascalCase.svelte` (e.g., `MacroStoryboard.svelte`)
  - Modules & Stores: `camelCase.ts` or `camelCase.svelte.ts` (e.g., `flowStore.svelte.ts`)
  - Types & Interfaces: `PascalCase` (e.g., `DeltaChange`, `ChangeImpactGraph`)
  - Functions & Variables: `camelCase` (e.g., `handleSelect`, `getDisplayedLines`)
  - CSS Classes: `kebab-case` (e.g., `macro-storyboard-section`, `story-card`)
  - CSS Variables: `--kebab-case` (e.g., `--ink`, `--line`, `--paper`)

---

## 4. UI & Accessibility (a11y) Standards

### 4.1 Semantic Elements & ARIA Roles
- Use semantic landmarks: `<header>`, `<main>`, `<nav>`, `<aside>`, `<footer>`, `<section>`.
- Every `<section>` must have an `aria-label` or `aria-labelledby`.
- Interactive elements must be `<button>` or `<a>` tags with appropriate `type="button"`, `aria-pressed`, or `aria-expanded` attributes.
- Scrollable containers (e.g., code blocks) must provide `role="region"` and accessible label if keyboard focusable.

### 4.2 Two-Way Interaction & Selection Sync
Selecting a step in any view surface must synchronously update:
1. **Macro Context Storyboard:** Active step card highlighted with `active-selected` badge.
2. **Nav Rail:** Left index pill highlighted with `aria-pressed="true"`.
3. **Center Viewport:** Smooth-scrolls to the corresponding `data-card` or `data-process` card.
4. **Right Context Pane:** Updates step metadata, incoming callers, and centers the **Blast Radius Radar** on the target symbol.

---

## 5. Build, Test, and Quality Automation

### 5.1 Make Targets
The repository root `Makefile` exposes frontend verification:
```bash
# Typecheck & build singlefile bundle
make build-ui

# Run Vitest unit tests
make test-ui
```

### 5.2 Verification Gates
Before finishing any frontend change:
1. `npm run check` (via `svelte-check`): **0 errors, 0 warnings**.
2. `npm test` (via `vitest run`): **All unit tests pass**.
3. `make check-naming`: All identifiers comply with CodeFlow domain vocabulary.
4. `go test ./internal/flowview`: Backend Go embed and HTTP serving pass without regression.
