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
        const marker = classifyMarker(cls.name, method.name, code);
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

    // 2. Check top-level functions and handler consts
    for (const fn of scan.topLevelFunctions) {
      const entrySymbolPath = `${relPath}#${fn.name}`;
      const marker = classifyMarker(classNameFromPath, fn.name, code);
      if (marker) {
        candidates.push(buildCandidate({
          entrySymbolPath,
          className: classNameFromPath,
          methodName: fn.name,
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

function classifyMarker(enclosingName, symbolName, code) {
  const s = symbolName.toLowerCase();
  const enc = enclosingName.toLowerCase();

  // Clean Architecture UseCase invocation
  if (
    symbolName === 'execute' ||
    symbolName === 'call' ||
    symbolName === 'handle' ||
    symbolName === 'invoke' ||
    enc.includes('usecase') ||
    enc.includes('interactor') ||
    enc.includes('service')
  ) {
    if (symbolName === 'execute' || symbolName === 'call' || symbolName === 'handle' || enc.includes('usecase')) {
      return { triggerClass: triggerUseCaseInvocation, markerKind: markerUsecaseCall };
    }
  }

  // User Actions (UI Event Handlers)
  if (
    /^(on|handle)?[A-Z0-9_$]*(click|press|tap|submit|change|select|drag|drop)/i.test(symbolName) ||
    /^(handle|on)[A-Z]/.test(symbolName) ||
    enc.includes('button') ||
    enc.includes('screen') ||
    enc.includes('view') ||
    enc.includes('page') ||
    enc.includes('component')
  ) {
    if (
      /click|press|tap|submit|change|select|handle|on[A-Z]/i.test(symbolName) &&
      !symbolName.startsWith('get') &&
      !symbolName.startsWith('set')
    ) {
      return { triggerClass: triggerUserAction, markerKind: markerRouteCallback };
    }
  }

  // State Management / Redux / Zustand / Notifiers
  if (
    enc.includes('store') ||
    enc.includes('slice') ||
    enc.includes('reducer') ||
    enc.includes('notifier') ||
    enc.includes('context') ||
    /^(set|update|mutate|dispatch|emit|commit)[A-Z]/.test(symbolName)
  ) {
    return { triggerClass: triggerStateTransition, markerKind: markerNotifierMethod };
  }

  // System Events / Lifecycle / Webhooks / Queue
  if (
    /^(on|handle)?(message|event|webhook|mount|unmount|init|load|ready|start|shutdown|consume)/i.test(symbolName) ||
    enc.includes('consumer') ||
    enc.includes('listener') ||
    enc.includes('worker')
  ) {
    return { triggerClass: triggerSystemEvent, markerKind: markerLifecycleCallback };
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
  listSourceFiles,
};
