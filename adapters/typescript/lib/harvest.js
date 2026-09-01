'use strict';

const fs = require('fs');
const path = require('path');
const { sha256Hex } = require('./sha256');
const { humanizeIdentifier } = require('./humanize');
const { scanSource } = require('./scanner');

const triggerUserAction = 'user_action';
const triggerUseCaseInvocation = 'use_case_invocation';
const triggerSystemEvent = 'system_event';
const triggerStateTransition = 'state_transition';

const markerRouteCallback = 'route_callback';
const markerUsecaseCall = 'usecase_call';
const markerLifecycleCallback = 'lifecycle_callback';
const markerNotifierMethod = 'notifier_method';
const markerStateMutation = 'state_mutation';

/**
 * Stage 1 Harvest: scans project source files for business flow entry points.
 * @param {object} params
 * @param {string} params.repoRoot
 * @returns {{ candidates: Array<object> }}
 */
function harvestCandidates(params) {
  const repoRoot = path.resolve(params.repoRoot);
  const candidates = [];

  // Determine packageName from package.json
  let packageName = path.basename(repoRoot) || 'root';
  const pkgPath = path.join(repoRoot, 'package.json');
  if (fs.existsSync(pkgPath)) {
    try {
      const pkg = JSON.parse(fs.readFileSync(pkgPath, 'utf8'));
      if (pkg && pkg.name) {
        packageName = pkg.name;
      }
    } catch (_) {}
  }

  // Walk files in sorted lexicographical order for determinism
  const sourceFiles = listSourceFiles(repoRoot);

  for (const relPath of sourceFiles) {
    const fullPath = path.join(repoRoot, relPath);
    let code;
    try {
      code = fs.readFileSync(fullPath, 'utf8');
    } catch (_) {
      continue;
    }

    const scan = scanSource(code);
    const classNameFromPath = path.basename(relPath, path.extname(relPath));

    // 1. Check class methods
    for (const cls of scan.classes) {
      for (const method of cls.methods) {
        const entrySymbolPath = `${relPath}#${cls.name}.${method.name}`;
        const methodBody = code.substring(method.bodyStart, method.bodyEnd);
        const marker = classifyMarker(cls.name, method.name, code, methodBody);
        if (marker) {
          candidates.push(buildCandidate({
            entrySymbolPath,
            className: cls.name,
            methodName: method.name,
            triggerClass: marker.triggerClass,
            markerKind: marker.markerKind,
            packageName,
          }));
        }
      }
    }

    // 2. Check top-level functions and nested handlers/hooks
    for (const fn of scan.topLevelFunctions) {
      const entrySymbolPath = `${relPath}#${fn.name}`;
      const fnBody = code.substring(fn.bodyStart, fn.bodyEnd);

      let enclosingName = classNameFromPath;
      let methodName = fn.name;

      if (fn.name.includes('.')) {
        const parts = fn.name.split('.');
        enclosingName = parts.slice(0, -1).join('.');
        methodName = parts[parts.length - 1];
      }

      const marker = classifyMarker(enclosingName, methodName, code, fnBody);
      if (marker) {
        candidates.push(buildCandidate({
          entrySymbolPath,
          className: enclosingName,
          methodName: methodName,
          triggerClass: marker.triggerClass,
          markerKind: marker.markerKind,
          packageName,
        }));
      }
    }
  }

  // Deduplicate and sort deterministically by entrySymbolPath
  const seenPaths = new Set();
  const deduped = [];
  for (const c of candidates) {
    if (!seenPaths.has(c.entrySymbolPath)) {
      seenPaths.add(c.entrySymbolPath);
      deduped.push(c);
    }
  }

  deduped.sort((a, b) => a.entrySymbolPath.localeCompare(b.entrySymbolPath));
  return { candidates: deduped };
}

/**
 * Classifies a symbol into a closed triggerClass and markerKind.
 * @param {string} enclosingName - Name of enclosing class/component/file
 * @param {string} symbolName - Name of the method/function/hook
 * @param {string} code - Full file source code
 * @param {string} [fnBody] - Body content of the function (optional)
 * @returns {{ triggerClass: string, markerKind: string } | null}
 */
function classifyMarker(enclosingName, symbolName, code, fnBody = '') {
  let enc = enclosingName || '';
  let sym = symbolName || '';

  // If symbol is dotted (e.g. LoginPage.handleSubmit), extract local symbol and parent scope
  if (sym.includes('.')) {
    const parts = sym.split('.');
    enc = parts.slice(0, -1).join('.');
    sym = parts[parts.length - 1];
  }

  const s = sym.toLowerCase();
  const encLower = enc.toLowerCase();
  const bodyText = fnBody || '';

  // Filter out pure utility/memoization hooks (non-entrypoints)
  const nonEntryHooks = new Set([
    'useMemo',
    'useRef',
    'useCallback',
    'useId',
    'useTransition',
    'useDeferredValue',
    'useDebugValue',
    'useImperativeHandle',
    'useSyncExternalStore',
  ]);
  if (nonEntryHooks.has(sym)) {
    return null;
  }

  // 1. Next.js Server Actions ('use server' directive in function body or file)
  if (
    bodyText.includes("'use server'") ||
    bodyText.includes('"use server"') ||
    code.includes("'use server'") ||
    code.includes('"use server"')
  ) {
    if (!sym.startsWith('get') && !sym.startsWith('set')) {
      return { triggerClass: triggerSystemEvent, markerKind: markerLifecycleCallback };
    }
  }

  // 2. Next.js App Router HTTP Route Handlers (GET, POST, PUT, DELETE, PATCH, HEAD, OPTIONS)
  const httpMethods = new Set(['GET', 'POST', 'PUT', 'DELETE', 'PATCH', 'HEAD', 'OPTIONS']);
  if (httpMethods.has(sym)) {
    return { triggerClass: triggerSystemEvent, markerKind: markerLifecycleCallback };
  }

  // 3. System Events / Lifecycle / Webhooks / Workers / Queue Consumers (evaluated before UI actions & custom hooks)
  if (
    sym === 'useEffect' ||
    sym === 'useLayoutEffect' ||
    sym === 'useInsertionEffect' ||
    sym === 'componentDidMount' ||
    sym === 'componentWillUnmount' ||
    sym === 'componentDidUpdate' ||
    /^(on|handle)?(message|event|webhook|mount|unmount|init|load|ready|start|shutdown|consume)/i.test(sym) ||
    encLower.includes('consumer') ||
    encLower.includes('listener') ||
    encLower.includes('worker') ||
    encLower.includes('queue')
  ) {
    return { triggerClass: triggerSystemEvent, markerKind: markerLifecycleCallback };
  }

  // 4. Clean Architecture / Domain UseCase Invocation
  if (
    sym === 'execute' ||
    sym === 'call' ||
    sym === 'handle' ||
    sym === 'invoke' ||
    encLower.includes('usecase') ||
    encLower.includes('interactor') ||
    encLower.includes('service')
  ) {
    if (
      sym === 'execute' ||
      sym === 'call' ||
      sym === 'handle' ||
      sym === 'invoke' ||
      encLower.includes('usecase') ||
      encLower.includes('interactor')
    ) {
      return { triggerClass: triggerUseCaseInvocation, markerKind: markerUsecaseCall };
    }
  }

  // 5. UI Actions (React Event Handlers, Form Actions, Click/Submit Callbacks)
  if (
    /^(handle|on)[A-Z0-9_$]*(click|press|tap|submit|change|select|drag|drop|input|blur|focus|toggle|close|open)/i.test(sym) ||
    /^(handle|on)[A-Z]/.test(sym) ||
    /^[A-Za-z0-9_$]+(Action|Handler)$/.test(sym) ||
    (
      (encLower.includes('button') ||
       encLower.includes('screen') ||
       encLower.includes('view') ||
       encLower.includes('page') ||
       encLower.includes('component') ||
       encLower.includes('form') ||
       encLower.includes('modal') ||
       encLower.includes('dialog') ||
       encLower.includes('card') ||
       encLower.includes('item') ||
       encLower.includes('header') ||
       encLower.includes('footer')) &&
      (/click|press|tap|submit|change|select|handle|^on[A-Z]|action/i.test(sym)) &&
      !sym.startsWith('get') &&
      !sym.startsWith('set')
    )
  ) {
    return { triggerClass: triggerUserAction, markerKind: markerRouteCallback };
  }

  // 6. State Management / Custom Business Hooks / Redux / Zustand / TanStack Mutations
  if (
    /^use[A-Z0-9_$]*(mutation|login|auth|cart|order|submit|update|query|state|form|checkout)/i.test(sym) ||
    /^use[A-Z]/.test(sym) ||
    /^(set|update|mutate|dispatch|emit|commit|reset)[A-Z]/.test(sym) ||
    encLower.includes('store') ||
    encLower.includes('slice') ||
    encLower.includes('reducer') ||
    encLower.includes('notifier') ||
    encLower.includes('context') ||
    encLower.includes('model')
  ) {
    return { triggerClass: triggerStateTransition, markerKind: markerNotifierMethod };
  }

  return null;
}

function buildCandidate({ entrySymbolPath, className, methodName, triggerClass, markerKind, packageName }) {
  const hash16 = sha256Hex(Buffer.from(entrySymbolPath, 'utf8')).substring(0, 16);
  const candidateId = `cand-${hash16}`;

  return {
    candidateId,
    triggerClass,
    markerKind,
    entrySymbolPath,
    intentSignals: {
      className,
      derivedName: humanizeIdentifier(methodName),
      docLine: null,
      packageName,
    },
    score: 0.5,
    fanIn: 1,
    boundaryReachable: true,
    rootEquivalenceKey: methodName || 'root',
    tieBreakRank: 0,
    manifestOverride: 'none',
    dedupedInto: null,
  };
}

function listSourceFiles(repoRoot, subDir = '') {
  const current = path.join(repoRoot, subDir);
  let files = [];
  if (!fs.existsSync(current)) return files;

  const entries = fs.readdirSync(current).sort();
  for (const entry of entries) {
    if (
      entry === 'node_modules' ||
      entry === '.git' ||
      entry === 'dist' ||
      entry === 'build' ||
      entry === '.codeflow' ||
      entry.startsWith('.')
    ) {
      continue;
    }
    const rel = subDir ? `${subDir}/${entry}` : entry;
    const full = path.join(repoRoot, rel);
    const stat = fs.statSync(full);
    if (stat.isDirectory()) {
      files = files.concat(listSourceFiles(repoRoot, rel));
    } else if (/\.(ts|tsx|js|jsx|mjs|cjs)$/.test(entry) && !entry.endsWith('.d.ts') && !entry.includes('.test.') && !entry.includes('.spec.')) {
      files.push(rel);
    }
  }
  return files;
}

module.exports = {
  harvestCandidates,
  classifyMarker,
  listSourceFiles,
};
