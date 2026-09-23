'use strict';
const { normalReturnEdges } = require('./normal_return');
const { calledThrowEdges, awaitedCalledThrowEdges } = require('./exception_propagation');

const fs = require('fs');
const path = require('path');
const { sha256Hex, canonicalAstFingerprint, byteOffset } = require('./sha256');
const { redactSecrets } = require('./secret');
const { humanizeIdentifier } = require('./humanize');
const { scanSource } = require('./scanner');
const { resolveHookMember } = require('./hook_binding');
const { extractExecutionStatements } = require('./syntax_execution');
const { overlayFor, createAnalysisTracker } = require('./analysis');

const boundarySuffixes = [
  'Repository',
  'Service',
  'Client',
  'ApiClient',
  'Dao',
  'Gateway',
  'Vault',
  'DataSource',
  'RemoteSource',
  'Api',
];

const uiNoiseDenylist = new Set([
  'styled',
  'className',
  'style',
  'Box',
  'Flex',
  'Grid',
  'Container',
  'Spacer',
  'Divider',
  'Typography',
  'Text',
  'View',
  'e.preventDefault',
  'event.preventDefault',
]);

/**
 * Stage 2 Structural Slicing: traces statement execution flow across files.
 * @param {object} params
 * @returns {object} SlicedPayload
 */
function sliceFlow(params) {
  const repoRoot = path.resolve(params.repoRoot);
  const candidateId = params.candidateId;
  const entrySymbolPath = params.entrySymbolPath;
  const includeExecutionSemantics = params.opts?.includeExecutionSemantics === true;
  const maxDepth = (params.opts && params.opts.maxDepth) || 5;
  const tracker = params.__analysisTracker || createAnalysisTracker(params, 'slice');
  const overlay = tracker.overlay || overlayFor(params);

  const [relPath, initialSymbol] = entrySymbolPath.split('#');
  // Resolve the entry against the captured source set and retain the membership
  // observation used by that resolution for edit-driven publication.
  if (!tracker.enumerateSourceFiles().includes(relPath)) {
    throw new Error('entry source file is outside the captured source set: ' + relPath);
  }
  const fileCache = new Map();
  const scanCache = new Map();
  const tsConfig = loadTsConfig(repoRoot, overlay, params.__analysisTracker || tracker);

  function readFile(p) {
    if (fileCache.has(p)) return fileCache.get(p);
    if (params.__analysisTracker || tracker) {
      const content = tracker.read(p);
      if (content !== null) fileCache.set(p, content);
      return content;
    }
    if (overlay) return overlay.has(p) ? overlay.get(p) : null;
    const full = path.join(repoRoot, p);
    if (!fs.existsSync(full)) return null;
    const content = fs.readFileSync(full, 'utf8');
    fileCache.set(p, content);
    return content;
  }

  function getScan(p) {
    if (scanCache.has(p)) return scanCache.get(p);
    const content = readFile(p);
    if (content === null) return null;
    const scan = scanSource(content, p);
    scanCache.set(p, scan);
    return scan;
  }

  const steps = [];
  const edges = [];
  const activeStack = new Set();
  let truncated = false;
  let visitedCycleDetected = false;
  let totalRedactedCount = 0;
  const synchronousInvocations = new Set();
  const normalReturnCallerInvocations = new Set();
  const normalReturnExcludedOrdinals = new Set();
  const prevailingFinallyReturnOrdinals = new Set();
  const directThrowInvocations = new Set();
  const directThrowCallerInvocations = new Set();
  const asyncThrowInvocations = new Set();
  const caughtCallTargets = new Map();
  const awaitedCaughtCallTargets = new Map();

  function sliceSymbolBody({ currentRelPath, className, methodName, depth, invocationId, callerStepOrdinal }) {
    const fullSym = className ? `${className}.${methodName}` : methodName;
    const visitKey = `${currentRelPath}#${fullSym}`;

    if (activeStack.has(visitKey)) {
      visitedCycleDetected = true;
      return;
    }

    if (depth >= maxDepth) {
      truncated = true;
      return;
    }

    activeStack.add(visitKey);
    const firstStepIndex = steps.length;
    const invocation = includeExecutionSemantics
      ? { invocationId, ...(callerStepOrdinal ? { callerStepOrdinal } : {}) } : {};

    try {
      const code = readFile(currentRelPath);
      if (!code) return;
      const scan = getScan(currentRelPath);
      if (!scan) return;

      let bodyStart = -1;
      let bodyEnd = -1;

      // 1. Check class methods first (if className is present)
      if (className) {
        for (const cls of scan.classes) {
          if (cls.name === className) {
            for (const m of cls.methods) {
              if (m.name === methodName) {
                bodyStart = m.bodyStart;
                bodyEnd = m.bodyEnd;
                break;
              }
            }
            break;
          }
        }
      }

      // 2. If not found in classes, search scan.topLevelFunctions for functional components, nested handlers, or hooks
      if (bodyStart < 0) {
        // Priority 2a: Exact match on full symbol name (e.g. LoginPage.onSubmit)
        for (const fn of scan.topLevelFunctions) {
          if (fn.name === fullSym) {
            bodyStart = fn.bodyStart;
            bodyEnd = fn.bodyEnd;
            break;
          }
        }
      }

      if (bodyStart < 0 && className) {
        // Priority 2b: Match by parentScope === className and localName === methodName
        for (const fn of scan.topLevelFunctions) {
          if (fn.parentScope === className && (fn.localName === methodName || fn.name === `${className}.${methodName}`)) {
            bodyStart = fn.bodyStart;
            bodyEnd = fn.bodyEnd;
            break;
          }
        }
      }

      if (bodyStart < 0) {
        // Priority 2c: Match by fn.name === methodName or fn.localName === methodName
        for (const fn of scan.topLevelFunctions) {
          if (fn.name === methodName || fn.localName === methodName) {
            bodyStart = fn.bodyStart;
            bodyEnd = fn.bodyEnd;
            break;
          }
        }
      }

      if (bodyStart < 0 && className) {
        // Priority 2d: If className was the component itself
        for (const fn of scan.topLevelFunctions) {
          if (fn.name === className || fn.localName === className) {
            bodyStart = fn.bodyStart;
            bodyEnd = fn.bodyEnd;
            break;
          }
        }
      }

      if (bodyStart < 0 || bodyEnd < 0) {
        return;
      }

      const fileHash = sha256Hex(code);
      const bodyText = code.substring(bodyStart, bodyEnd);
      const symbolRange = [byteOffset(code, bodyStart), byteOffset(code, bodyEnd)];

      const stmts = includeExecutionSemantics
        ? extractExecutionStatements(code, currentRelPath, bodyStart, bodyEnd, extractStatements)
        : extractStatements(bodyText, bodyStart, code);

      if (stmts.normalReturnEligible) synchronousInvocations.add(invocationId);
      if (stmts.normalReturnCallerEligible) normalReturnCallerInvocations.add(invocationId);
      if (stmts.directThrowEligible) directThrowInvocations.add(invocationId);
      if (stmts.directThrowCallerEligible) directThrowCallerInvocations.add(invocationId);
      if (stmts.asyncThrowEligible) asyncThrowInvocations.add(invocationId);
      const statementOrdinals = new Map();
      for (const stmt of stmts) {
        const callName = stmt.methodName && `${stmt.receiver ? stmt.receiver + '.' : ''}${stmt.methodName}`;
        if (stmt.type === 'call' && callName && uiNoiseDenylist.has(callName)) continue;
        const spanBytes = code.substring(stmt.startOffset, stmt.endOffset);
        const spanHash = sha256Hex(spanBytes);
        const canonicalAst = canonicalAstFingerprint(spanBytes);

        const anchor = {
          repoRelativePath: currentRelPath,
          byteRange: [byteOffset(code, stmt.startOffset), byteOffset(code, stmt.endOffset)],
          fileHash,
          spanHash,
          enclosingSymbolPath: fullSym,
          canonicalAstFingerprint: canonicalAst,
          symbolRange,
        };

        const context = stmt.flowContext && params.snapshot?.snapshotId ? {flowContext: {...stmt.flowContext, canonicalPath: currentRelPath, snapshotId: params.snapshot.snapshotId, sourceHash: fileHash}} : {};
        const stepOrdinal = steps.length + 1;
        statementOrdinals.set(stmt, stepOrdinal);

        if (stmt.type === 'guard') {
          const condRedact = redactSecrets(stmt.guardCondition || '');
          const descRedact = redactSecrets(`if (${condRedact.text}) return`);
          totalRedactedCount += condRedact.count + descRedact.count;

          steps.push({
            ...invocation,
            ...context,
            ordinal: stepOrdinal,
            kind: 'guard',
            description: descRedact.text,
            symbolPath: fullSym,
            anchor,
            guardCondition: condRedact.text,
            stateBefore: null,
            stateAfter: null,
            effectTarget: null,
          });
        } else if (['mutation', 'await', 'break', 'continue'].includes(stmt.type)) {
          const descRedact = redactSecrets(stmt.description || stmt.rawText);
          totalRedactedCount += descRedact.count;

          steps.push({
            ...invocation,
            ...context,
            ordinal: stepOrdinal,
            kind: stmt.type,
            description: descRedact.text,
            symbolPath: fullSym,
            anchor,
            guardCondition: null,
            stateBefore: stmt.stateBefore || null,
            stateAfter: stmt.stateAfter || null,
            effectTarget: null,
          });
        } else if (stmt.type === 'call' || stmt.type === 'effect' || stmt.type === 'return' || stmt.type === 'throw') {
          const condRedact = redactSecrets(stmt.guardCondition || '');
          totalRedactedCount += condRedact.count;
          const descRedact = redactSecrets(stmt.description || stmt.rawText);
          totalRedactedCount += descRedact.count;

          const isBoundary = isBoundaryTarget(stmt.receiver, stmt.methodName);
          const effectTarget = isBoundary ? `${stmt.receiver ? stmt.receiver + '.' : ''}${stmt.methodName}` : null;

          steps.push({
            ...invocation,
            ...context,
            ordinal: stepOrdinal,
            kind: ['return', 'throw'].includes(stmt.type) ? stmt.type : 'call',
            description: descRedact.text,
            symbolPath: fullSym,
            anchor,
            guardCondition: condRedact.text || null,
            stateBefore: null,
            stateAfter: null,
            effectTarget,
          });

          if (isBoundary) {
            const rawTarget = `${stmt.receiver ? stmt.receiver + '.' : ''}${stmt.methodName}`;
            const safeTarget = rawTarget.replace(/[^A-Za-z0-9_.$-]/g, '_') || 'boundary';
            edges.push({
              kind: 'boundary_call',
              toSymbolPath: `${currentRelPath}#${safeTarget}`,
              resolutionStatus: 'resolved',
              depth: depth + 1,
              stepOrdinal,
            });
          } else if (stmt.methodName) {
            const target = resolveCallTarget({
              repoRoot,
              currentRelPath,
              scan,
              receiver: stmt.receiver,
              methodName: stmt.methodName,
              readFile,
              getScan,
              tsConfig,
              callOffset: stmt.startOffset,
              callEndOffset: stmt.endOffset,
            });

            if (target) {
              const edge = {
                kind: 'resolved_cross_file',
                toSymbolPath: `${target.relPath}#${target.className ? target.className + '.' : ''}${target.methodName}`,
                resolutionStatus: 'resolved',
                depth: depth + 1,
                stepOrdinal,
              };
              edges.push(edge);

              const targetStepOrdinal = sliceSymbolBody({
                currentRelPath: target.relPath,
                className: target.className,
                methodName: target.methodName,
                depth: depth + 1,
                invocationId: sha256Hex(JSON.stringify([invocationId, currentRelPath, stmt.startOffset, stmt.endOffset, edge.toSymbolPath])),
                callerStepOrdinal: stepOrdinal,
              });
              if (includeExecutionSemantics) {
                if (targetStepOrdinal) edge.targetStepOrdinal = targetStepOrdinal;
                else edge.resolutionStatus = depth + 1 >= maxDepth ? 'truncated' : 'unresolved_type';
              }
            } else {
              const rawTarget = `${stmt.receiver ? stmt.receiver + '.' : ''}${stmt.methodName}`;
              const safeTarget = rawTarget.replace(/[^A-Za-z0-9_.$-]/g, '_') || 'unknown';
              edges.push({
                kind: 'unknown_edge',
                toSymbolPath: `${currentRelPath}#${safeTarget}`,
                resolutionStatus: 'unresolved_dynamic',
                depth: depth + 1,
                stepOrdinal,
              });
            }
          }
        } else if (stmt.type === 'branch') {
          const descRedact = redactSecrets(stmt.description || 'branch execution');
          totalRedactedCount += descRedact.count;

          steps.push({
            ...invocation,
            ...context,
            ordinal: stepOrdinal,
            kind: 'branch',
            description: descRedact.text,
            symbolPath: fullSym,
            anchor,
            guardCondition: null,
            stateBefore: null,
            stateAfter: null,
            effectTarget: null,
          });
        }
      }
      if (includeExecutionSemantics) {
        for (const statement of stmts) {
          if (!statement.awaitedCall && !statement.normalReturnUnsupported) continue;
          const ordinal = statementOrdinals.get(statement);
          if (ordinal) normalReturnExcludedOrdinals.add(ordinal);
        }
        if (stmts.prevailingFinallyReturnIndex >= 0) {
          const prevailingOrdinal = statementOrdinals.get(stmts[stmts.prevailingFinallyReturnIndex]);
          if (prevailingOrdinal) prevailingFinallyReturnOrdinals.add(prevailingOrdinal);
        }
        for (const statement of stmts) {
          if (statement.catchTargetIndex === undefined) continue;
          const callerOrdinal = statementOrdinals.get(statement);
          const catchOrdinal = statementOrdinals.get(stmts[statement.catchTargetIndex]);
          if (callerOrdinal && catchOrdinal) {
            caughtCallTargets.set(callerOrdinal, catchOrdinal);
            if (statement.awaitedCatchCall) awaitedCaughtCallTargets.set(callerOrdinal, catchOrdinal);
          }
        }
        function emittedPredecessors(path, seen = new Set()) {
          if (seen.has(path.index)) return [];
          const nextSeen = new Set([...seen, path.index]);
          const statement = stmts[path.index];
          if (!statement) return [];
          if (statementOrdinals.has(statement)) return [{ stepOrdinal: statementOrdinals.get(statement), conditions: path.conditions, loopBack: path.loopBack, loopReentry: path.loopReentry, loopExit: path.loopExit, switchExit: path.switchExit, parallel: path.parallel, failure: path.failure, finally: path.finally }];
          return (statement.predecessorPaths || []).flatMap(previous => emittedPredecessors({ index: previous.index, conditions: [...previous.conditions, ...path.conditions], loopBack: path.loopBack || previous.loopBack, loopReentry: path.loopReentry || previous.loopReentry, loopExit: path.loopExit || previous.loopExit, switchExit: path.switchExit || previous.switchExit, parallel: path.parallel || previous.parallel, failure: path.failure || previous.failure, finally: path.finally || previous.finally }, nextSeen));
        }
        const emittedSteps = new Map(steps.slice(firstStepIndex).map(step => [step.ordinal, step]));
        for (const statement of stmts) {
          const targetStepOrdinal = statementOrdinals.get(statement);
          if (!targetStepOrdinal) continue;
          if (statement.assignmentSourceIndex !== undefined) {
            const sourceOrdinal = statementOrdinals.get(stmts[statement.assignmentSourceIndex]);
            const target = emittedSteps.get(targetStepOrdinal);
            if (sourceOrdinal && target) target.assignmentSourceOrdinal = sourceOrdinal;
          }
          const paths = (statement.predecessorPaths || []).flatMap(path => emittedPredecessors(path));
          const emitted = new Set();
          for (const path of paths) {
            const conditions = path.conditions.map(condition => ({ stepOrdinal: statementOrdinals.get(stmts[condition.index]), outcome: condition.outcome }));
            const key = JSON.stringify([path.stepOrdinal, conditions, !!path.loopBack, !!path.loopReentry, !!path.loopExit, !!path.switchExit, !!path.parallel, !!path.failure, !!path.finally]);
            if (emitted.has(key)) continue;
            emitted.add(key);
            if (conditions.some(condition => !condition.stepOrdinal)) {
              edges.push({ kind: 'unknown_edge', toSymbolPath: `${currentRelPath}#${fullSym}.control_flow.condition_${path.stepOrdinal}_${targetStepOrdinal}`, resolutionStatus: 'unresolved_type', depth,
                stepOrdinal: path.stepOrdinal, unresolvedReason: '조건 판단의 단계 참조를 확인하지 못했습니다.' });
              continue;
            }
            edges.push({ kind: path.finally ? 'finally' : path.failure ? 'failure' : path.switchExit ? 'switch_exit' : path.parallel ? 'parallel_wait' : path.loopExit && path.loopBack ? 'loop_exit_back' : path.loopExit ? 'loop_exit' : path.loopReentry ? 'loop_reentry' : path.loopBack && emittedSteps.get(path.stepOrdinal)?.kind === 'await' ? 'await_loop_back' : path.loopBack ? 'loop_back' : emittedSteps.get(path.stepOrdinal)?.kind === 'await' ? 'await_resume' : 'control_flow', toSymbolPath: `${currentRelPath}#${fullSym}`, resolutionStatus: 'resolved', depth, stepOrdinal: path.stepOrdinal, targetStepOrdinal,
              ...(conditions.length ? { conditions } : {}) });
          }
          if (statement.controlFlowLimitation) {
            edges.push({ kind: 'unknown_edge', toSymbolPath: `${currentRelPath}#${fullSym}.control_flow.step_${targetStepOrdinal}`, resolutionStatus: 'unresolved_type', depth,
              stepOrdinal: targetStepOrdinal, unresolvedReason: statement.controlFlowLimitation });
          }
        }
      }
      return steps.length > firstStepIndex ? steps[firstStepIndex].ordinal : undefined;
    } finally {
      activeStack.delete(visitKey);
    }
  }

  let initClass = '';
  let initMethod = initialSymbol;
  if (initialSymbol.includes('.')) {
    const dot = initialSymbol.indexOf('.');
    initClass = initialSymbol.substring(0, dot);
    initMethod = initialSymbol.substring(dot + 1);
  }

  sliceSymbolBody({
    currentRelPath: relPath,
    className: initClass,
    methodName: initMethod,
    depth: 0,
    invocationId: sha256Hex(entrySymbolPath),
  });

  // Fallback Root Step: schemas/sliced-payload.schema.json mandates minItems: 1 for steps.
  if (steps.length === 0) {
    const code = readFile(relPath) || '';
    const fileBytesLen = byteOffset(code, code.length);
    const fileHash = sha256Hex(code);
    const fullSym = initClass ? `${initClass}.${initMethod}` : initMethod;
    const rawDesc = humanizeIdentifier(initMethod);
    const descRedact = redactSecrets(rawDesc);
    totalRedactedCount += descRedact.count;

    steps.push({
      ordinal: 1,
      kind: 'call',
      description: descRedact.text,
      symbolPath: fullSym,
      guardCondition: null,
      stateBefore: null,
      stateAfter: null,
      effectTarget: null,
      anchor: {
        repoRelativePath: relPath,
        byteRange: [0, fileBytesLen],
        fileHash,
        spanHash: fileHash,
        enclosingSymbolPath: fullSym,
        canonicalAstFingerprint: sha256Hex(''),
        symbolRange: [0, fileBytesLen],
      },
    });
  }

  if (includeExecutionSemantics) {
    edges.push(...normalReturnEdges(steps, edges, synchronousInvocations, normalReturnCallerInvocations, normalReturnExcludedOrdinals, prevailingFinallyReturnOrdinals));
    edges.push(...calledThrowEdges(steps, edges, directThrowInvocations, caughtCallTargets, directThrowCallerInvocations));
    edges.push(...awaitedCalledThrowEdges(steps, edges, asyncThrowInvocations, awaitedCaughtCallTargets));
  }

  // Normalize ordinals
  for (let i = 0; i < steps.length; i++) {
    steps[i].ordinal = i + 1;
  }

  return {
    candidateId,
    language: 'typescript',
    entrySymbolPath,
    steps,
    edges,
    truncated,
    visitedCycleDetected,
    redactedCount: totalRedactedCount,
  };
}

function isBoundaryTarget(receiver, methodName) {
  if (!receiver && !methodName) return false;
  const parts = receiver ? receiver.split('.') : [];
  const lastReceiverPart = parts.length > 0 ? parts[parts.length - 1] : (receiver || '');
  const r = lastReceiverPart.toLowerCase();
  const m = (methodName || '').toLowerCase();

  for (const suffix of boundarySuffixes) {
    const s = suffix.toLowerCase();
    if (
      (lastReceiverPart && (lastReceiverPart.endsWith(suffix) || r.endsWith(s) || r === s)) ||
      (methodName && (methodName.endsWith(suffix) || m.endsWith(s) || m === s))
    ) {
      return true;
    }
  }
  return false;
}

function loadTsConfig(repoRoot, overlay = null, tracker = null) {
  const configFiles = ['tsconfig.json', 'jsconfig.json'];
  for (const file of configFiles) {
    const full = path.join(repoRoot, file);
    const overlayContent = tracker ? tracker.read(file) : (overlay && overlay.has(file) ? overlay.get(file) : null);
    if (overlayContent !== null || (!overlay && !tracker && fs.existsSync(full))) {
      try {
        let content = tracker || overlay ? overlayContent : fs.readFileSync(full, 'utf8');
        content = content
          .replace(/\/\/[^\n]*/g, '')
          .replace(/\/\*[\s\S]*?\*\//g, '')
          .replace(/,(\s*[\]}])/g, '$1');
        const parsed = JSON.parse(content);
        const compilerOptions = parsed.compilerOptions || {};
        if (tracker) tracker.recordDependency(file);
        return {
          baseUrl: compilerOptions.baseUrl || '.',
          paths: compilerOptions.paths || {},
        };
      } catch (_) {}
    }
  }
  return { baseUrl: '.', paths: {} };
}

/**
 * Scans code for destructured hook or function return bindings:
 * e.g. const { login } = useAuth();
 * e.g. const { login: authLogin } = useAuth();
 * e.g. const [ login ] = useLogin();
 * e.g. const auth = useAuth();
 * @param {string} code
 * @param {string} identifier
 * @returns {string|null} The source hook/function name (e.g. "useAuth")
 */
function findHookBinding(code, identifier) {
  if (!code || !identifier) return null;

  // 1. Object destructuring: const { login } = useAuth(...)
  const objDestructRe = /(?:const|let|var)\s+\{\s*([^}]+)\s*\}\s*=\s*(?:await\s+)?([A-Za-z0-9_$]+(?:\s*\.\s*[A-Za-z0-9_$]+)*)\s*\(/g;
  let m;
  while ((m = objDestructRe.exec(code)) !== null) {
    const members = m[1].split(',');
    const sourceCall = m[2].replace(/\s+/g, '');
    for (const member of members) {
      const parts = member.trim().split(':');
      const prop = parts[0].trim();
      const localName = parts.length > 1 ? parts[1].trim() : prop;
      if (localName === identifier || prop === identifier) {
        return sourceCall;
      }
    }
  }

  // 2. Array destructuring: const [ login ] = useAuth(...)
  const arrDestructRe = /(?:const|let|var)\s+\[\s*([^\]]+)\s*\]\s*=\s*(?:await\s+)?([A-Za-z0-9_$]+(?:\s*\.\s*[A-Za-z0-9_$]+)*)\s*\(/g;
  while ((m = arrDestructRe.exec(code)) !== null) {
    const elements = m[1].split(',').map(e => e.trim());
    const sourceCall = m[2].replace(/\s+/g, '');
    if (elements.includes(identifier)) {
      return sourceCall;
    }
  }

  // 3. Direct assignment: const auth = useAuth(...)
  const directAssignRe = /(?:const|let|var)\s+([A-Za-z0-9_$]+)\s*=\s*(?:await\s+)?([A-Za-z0-9_$]+(?:\s*\.\s*[A-Za-z0-9_$]+)*)\s*\(/g;
  while ((m = directAssignRe.exec(code)) !== null) {
    if (m[1] === identifier) {
      return m[2].replace(/\s+/g, '');
    }
  }

  return null;
}

function resolveCallTarget({ repoRoot, currentRelPath, scan, receiver, methodName, readFile, getScan, tsConfig, callOffset, callEndOffset }) {
  const currentDir = path.dirname(currentRelPath);
  const cfg = tsConfig || { baseUrl: '.', paths: {} };
  const currentCode = readFile(currentRelPath) || '';

  const receiverParts = receiver ? receiver.split('.') : [];
  const rootReceiver = receiverParts.length > 0 ? receiverParts[0] : '';

  // Helper to resolve candidates from an import specifier
  function getCandidatePaths(imp, fromDir = currentDir) {
    const candidatePaths = [];

    // A. Configured tsconfig paths
    let matchedAlias = false;
    for (const [pattern, targets] of Object.entries(cfg.paths || {})) {
      if (pattern.endsWith('/*') && imp.startsWith(pattern.slice(0, -1))) {
        matchedAlias = true;
        const sub = imp.slice(pattern.length - 2);
        for (const tgt of targets) {
          const tgtBase = tgt.endsWith('/*') ? tgt.slice(0, -2) : tgt;
          candidatePaths.push(path.resolve(repoRoot, cfg.baseUrl, tgtBase, sub));
        }
      } else if (pattern === imp) {
        matchedAlias = true;
        for (const tgt of targets) {
          candidatePaths.push(path.resolve(repoRoot, cfg.baseUrl, tgt));
        }
      }
    }

    // B. Convention fallbacks for @/ or ~/
    if (!matchedAlias) {
      if (imp.startsWith('@/') || imp.startsWith('~/')) {
        const sub = imp.slice(2);
        candidatePaths.push(path.resolve(repoRoot, 'src', sub));
        candidatePaths.push(path.resolve(repoRoot, sub));
      } else if (imp.startsWith('@app/')) {
        const sub = imp.slice(5);
        candidatePaths.push(path.resolve(repoRoot, 'src/app', sub));
        candidatePaths.push(path.resolve(repoRoot, 'src', sub));
      } else if (imp.startsWith('.')) {
        // C. Relative import
        candidatePaths.push(path.resolve(repoRoot, fromDir, imp));
      }
    }

    return candidatePaths;
  }

  const hookTarget = resolveHookMember({
    code: currentCode, scan, currentRelPath, receiver, methodName, offset: callOffset, endOffset: callEndOffset,
    resolveModule(imp) {
      const files = [];
      for (const base of getCandidatePaths(imp)) {
        for (const candidate of [base, ...['.ts', '.tsx', '.js', '.jsx'].map(ext => base + ext), ...['index.ts', 'index.tsx', 'index.js', 'index.jsx'].map(name => path.join(base, name))]) {
          const relPath = path.relative(repoRoot, candidate).replace(/\\/g, '/');
          if (relPath.startsWith('..')) continue;
          const targetScan = getScan(relPath);
          if (targetScan && !files.some(file => file.relPath === relPath)) files.push({ relPath, scan: targetScan, code: readFile(relPath) });
        }
      }
      return files;
    },
  });
  if (hookTarget !== undefined) return hookTarget;

  // 1. Same-file resolution (this.method or local function)
  if (!receiver || receiver === 'this') {
    for (const cls of scan.classes) {
      for (const m of cls.methods) {
        if (m.name === methodName) {
          return { relPath: currentRelPath, className: cls.name, methodName: m.name };
        }
      }
    }
    for (const fn of scan.topLevelFunctions) {
      if (fn.name === methodName || fn.localName === methodName) {
        return { relPath: currentRelPath, className: fn.parentScope || '', methodName: fn.localName || fn.name };
      }
    }
  }

  // Helper to search a target file scan for matching class methods or functions
  function searchTargetScan(targetScan, targetRel, visitedFiles = new Set()) {
    if (!targetScan || visitedFiles.has(targetRel)) return null;
    visitedFiles.add(targetRel);

    // 1. Direct class method match
    for (const cls of targetScan.classes) {
      const matchesClass =
        !receiver ||
        receiver === 'this' ||
        cls.name.toLowerCase() === receiver.toLowerCase() ||
        receiverParts.some(p => cls.name.toLowerCase() === p.toLowerCase() || cls.name.toLowerCase().includes(p.toLowerCase()) || p.toLowerCase().includes(cls.name.toLowerCase()));

      if (matchesClass) {
        for (const m of cls.methods) {
          if (m.name === methodName) {
            return { relPath: targetRel, className: cls.name, methodName: m.name };
          }
        }
      }
    }

    // 2. Fallback check: any class in file with matching method
    for (const cls of targetScan.classes) {
      for (const m of cls.methods) {
        if (m.name === methodName) {
          return { relPath: targetRel, className: cls.name, methodName: m.name };
        }
      }
    }

    // 3. Match top-level functions or nested functions
    for (const fn of targetScan.topLevelFunctions) {
      const fnLocal = fn.localName || fn.name;
      if (
        (receiver && fn.name === receiver + '.' + methodName) ||
        (!receiver && !fn.parentScope && fn.name === methodName)
      ) {
        return {
          relPath: targetRel,
          className: fn.parentScope || (fn.name.includes('.') ? fn.name.split('.')[0] : ''),
          methodName: fnLocal,
        };
      }
    }

    // 4. Follow re-exports in target file (e.g. barrel index.ts files)
    const targetCode = readFile(targetRel);
    if (targetCode) {
      const reExportRe = /(?:export\s+(?:\*|\{[^}]+\})\s+from\s+['"]([^'"]+)['"])|(?:export\s+\*\s+as\s+[A-Za-z0-9_$]+\s+from\s+['"]([^'"]+)['"])/g;
      let reExp;
      while ((reExp = reExportRe.exec(targetCode)) !== null) {
        const rePathSpec = reExp[1] || reExp[2];
        if (rePathSpec && rePathSpec.startsWith('.')) {
          const reDir = path.dirname(targetRel);
          const subCandidates = getCandidatePaths(rePathSpec, reDir);
          for (const cand of subCandidates) {
            const fileCandidates = [
              cand,
              cand + '.ts',
              cand + '.tsx',
              cand + '.js',
              cand + '.jsx',
              path.join(cand, 'index.ts'),
              path.join(cand, 'index.tsx'),
              path.join(cand, 'index.js'),
              path.join(cand, 'index.jsx'),
            ];
            for (const fc of fileCandidates) {
              const relCand = path.relative(repoRoot, fc).replace(/\\/g, '/');
              if (relCand.startsWith('..')) continue;
              const subScan = getScan(relCand);
              if (subScan) {
                const res = searchTargetScan(subScan, relCand, visitedFiles);
                if (res) return res;
              }
            }
          }
        }
      }
    }

    // 5. Follow imports in target file (e.g. aggregator modules like api.ts)
    if (targetScan.imports && targetScan.imports.length > 0) {
      const targetDir = path.dirname(targetRel);
      for (const subImp of targetScan.imports) {
        const subCandidates = getCandidatePaths(subImp, targetDir);
        for (const cand of subCandidates) {
          const fileCandidates = [
            cand,
            cand + '.ts',
            cand + '.tsx',
            cand + '.js',
            cand + '.jsx',
            cand + '.d.ts',
            path.join(cand, 'index.ts'),
            path.join(cand, 'index.tsx'),
            path.join(cand, 'index.js'),
            path.join(cand, 'index.jsx'),
          ];
          for (const fc of fileCandidates) {
            const relCand = path.relative(repoRoot, fc).replace(/\\/g, '/');
            if (relCand.startsWith('..')) continue;
            const subScan = getScan(relCand);
            if (subScan) {
              const res = searchTargetScan(subScan, relCand, visitedFiles);
              if (res) return res;
            }
          }
        }
      }
    }

    return null;
  }

  // 3. Search all imported modules
  for (const imp of scan.imports) {
    const candidatePaths = getCandidatePaths(imp);

    for (const basePath of candidatePaths) {
      const fileCandidates = [
        basePath,
        basePath + '.ts',
        basePath + '.tsx',
        basePath + '.js',
        basePath + '.jsx',
        basePath + '.d.ts',
        path.join(basePath, 'index.ts'),
        path.join(basePath, 'index.tsx'),
        path.join(basePath, 'index.js'),
        path.join(basePath, 'index.jsx'),
      ];

      for (const cand of fileCandidates) {
        const targetRel = path.relative(repoRoot, cand).replace(/\\/g, '/');
        if (targetRel.startsWith('..')) continue;

        const targetScan = getScan(targetRel);
        if (targetScan) {
          const match = searchTargetScan(targetScan, targetRel);
          if (match) {
            return match;
          }
        }
      }
    }
  }

  return null;
}

/**
 * Tokenizes JS/TS source code with accurate character offsets and nest tracking.
 * @param {string} text
 * @param {number} baseOffset
 * @returns {Array<object>}
 */
function tokenize(text, baseOffset) {
  const tokens = [];
  let i = 0;
  const len = text.length;

  while (i < len) {
    const ch = text[i];

    // Whitespace
    if (/\s/.test(ch)) {
      i++;
      continue;
    }

    // Line comment
    if (ch === '/' && text[i + 1] === '/') {
      const start = i;
      i += 2;
      while (i < len && text[i] !== '\n') i++;
      tokens.push({ type: 'comment', text: text.substring(start, i), start: baseOffset + start, end: baseOffset + i });
      continue;
    }

    // Block comment
    if (ch === '/' && text[i + 1] === '*') {
      const start = i;
      i += 2;
      while (i < len && !(text[i] === '*' && text[i + 1] === '/')) i++;
      if (i < len) i += 2;
      tokens.push({ type: 'comment', text: text.substring(start, i), start: baseOffset + start, end: baseOffset + i });
      continue;
    }

    // Strings: single / double quote
    if (ch === '"' || ch === "'") {
      const quote = ch;
      const start = i;
      i++;
      while (i < len) {
        if (text[i] === quote) {
          let backslashes = 0;
          let k = i - 1;
          while (k >= start && text[k] === '\\') {
            backslashes++;
            k--;
          }
          if (backslashes % 2 === 0) {
            i++;
            break;
          }
        }
        i++;
      }
      tokens.push({ type: 'string', text: text.substring(start, i), start: baseOffset + start, end: baseOffset + i });
      continue;
    }

    // Template literals
    if (ch === '`') {
      const start = i;
      i++;
      let exprDepth = 0;
      while (i < len) {
        if (text[i] === '`' && exprDepth === 0) {
          let backslashes = 0;
          let k = i - 1;
          while (k >= start && text[k] === '\\') {
            backslashes++;
            k--;
          }
          if (backslashes % 2 === 0) {
            i++;
            break;
          }
        } else if (text[i] === '$' && text[i + 1] === '{') {
          exprDepth++;
          i += 2;
          continue;
        } else if (text[i] === '}' && exprDepth > 0) {
          exprDepth--;
        }
        i++;
      }
      tokens.push({ type: 'string', text: text.substring(start, i), start: baseOffset + start, end: baseOffset + i });
      continue;
    }

    // Word / Identifier / Keyword / Number
    if (/[A-Za-z0-9_$]/.test(ch)) {
      const start = i;
      while (i < len && /[A-Za-z0-9_$]/.test(text[i])) i++;
      const word = text.substring(start, i);
      tokens.push({ type: 'word', text: word, start: baseOffset + start, end: baseOffset + i });
      continue;
    }

    // Multi-character operators
    const two = text.substring(i, i + 2);
    const three = text.substring(i, i + 3);
    if (['===', '!==', '>>>', '&&=', '||='].includes(three)) {
      tokens.push({ type: 'punct', text: three, start: baseOffset + i, end: baseOffset + i + 3 });
      i += 3;
      continue;
    }
    if (['==', '!=', '<=', '>=', '=>', '&&', '||', '++', '--', '+=', '-=', '*=', '/=', '??'].includes(two)) {
      tokens.push({ type: 'punct', text: two, start: baseOffset + i, end: baseOffset + i + 2 });
      i += 2;
      continue;
    }

    // Single character punctuation
    tokens.push({ type: 'punct', text: ch, start: baseOffset + i, end: baseOffset + i + 1 });
    i++;
  }

  return tokens;
}

/**
 * Statement Boundary Parser: segments token stream into multi-line statement AST facts.
 * @param {string} bodyText
 * @param {number} bodyOffset
 * @param {string} fullCode
 * @returns {Array<object>}
 */
function extractStatements(bodyText, bodyOffset, fullCode) {
  const allTokens = tokenize(bodyText, bodyOffset);
  const codeTokens = allTokens.filter(t => t.type !== 'comment');
  const stmts = [];

  function parseRange(startIdx, endIdx) {
    let i = startIdx;
    while (i < endIdx) {
      const tok = codeTokens[i];

      // 1. IF statement
      if (tok.type === 'word' && tok.text === 'if') {
        const ifStartToken = tok;
        let parenDepth = 0;
        let condStart = -1;
        let condEnd = -1;
        let j = i + 1;

        while (j < endIdx) {
          if (codeTokens[j].text === '(') {
            if (parenDepth === 0) condStart = j + 1;
            parenDepth++;
          } else if (codeTokens[j].text === ')') {
            parenDepth--;
            if (parenDepth === 0) {
              condEnd = j;
              j++;
              break;
            }
          }
          j++;
        }

        if (condStart >= 0 && condEnd >= condStart) {
          const condText = fullCode.substring(codeTokens[condStart].start, codeTokens[condEnd - 1].end).trim();

          // Body extraction
          let bodyStartTok = j;
          let bodyEndTok = j;
          let hasBraces = false;

          if (j < endIdx && codeTokens[j].text === '{') {
            hasBraces = true;
            let braceDepth = 0;
            while (j < endIdx) {
              if (codeTokens[j].text === '{') braceDepth++;
              else if (codeTokens[j].text === '}') {
                braceDepth--;
                if (braceDepth === 0) {
                  bodyEndTok = j + 1;
                  break;
                }
              }
              j++;
            }
          } else {
            while (j < endIdx) {
              if (codeTokens[j].text === ';') {
                bodyEndTok = j + 1;
                break;
              }
              j++;
            }
            if (bodyEndTok === bodyStartTok) bodyEndTok = Math.min(j, endIdx);
          }

          const ifBodyTokens = codeTokens.slice(hasBraces ? bodyStartTok + 1 : bodyStartTok, hasBraces ? bodyEndTok - 1 : bodyEndTok);
          const hasReturnOrThrow = ifBodyTokens.some(t => t.type === 'word' && (t.text === 'return' || t.text === 'throw'));

          // Check if followed by else
          let nextAfterIf = bodyEndTok;
          let hasElse = nextAfterIf < endIdx && codeTokens[nextAfterIf].type === 'word' && codeTokens[nextAfterIf].text === 'else';

          if (hasReturnOrThrow && !hasElse) {
            // Pure Guard
            const spanEnd = codeTokens[bodyEndTok - 1] ? codeTokens[bodyEndTok - 1].end : ifStartToken.end;
            stmts.push({
              type: 'guard',
              rawText: fullCode.substring(ifStartToken.start, spanEnd),
              startOffset: ifStartToken.start,
              endOffset: spanEnd,
              guardCondition: condText,
              description: `if (${condText}) return`,
            });
            i = bodyEndTok;
            continue;
          } else {
            // Branch
            const spanEnd = codeTokens[bodyEndTok - 1] ? codeTokens[bodyEndTok - 1].end : ifStartToken.end;
            stmts.push({
              type: 'branch',
              rawText: fullCode.substring(ifStartToken.start, spanEnd),
              startOffset: ifStartToken.start,
              endOffset: spanEnd,
              description: `if (${condText})`,
            });
            parseRange(hasBraces ? bodyStartTok + 1 : bodyStartTok, hasBraces ? bodyEndTok - 1 : bodyEndTok);
            i = bodyEndTok;
            continue;
          }
        }
      }

      // 2. TRY / CATCH / FINALLY
      if (tok.type === 'word' && tok.text === 'try') {
        let j = i + 1;
        if (j < endIdx && codeTokens[j].text === '{') {
          let braceDepth = 0;
          let tryStart = j + 1;
          let tryEnd = j;
          while (j < endIdx) {
            if (codeTokens[j].text === '{') braceDepth++;
            else if (codeTokens[j].text === '}') {
              braceDepth--;
              if (braceDepth === 0) {
                tryEnd = j;
                j++;
                break;
              }
            }
            j++;
          }
          parseRange(tryStart, tryEnd);

          // Check catch / finally
          while (j < endIdx && codeTokens[j].type === 'word' && (codeTokens[j].text === 'catch' || codeTokens[j].text === 'finally')) {
            const blockType = codeTokens[j].text;
            j++;
            if (blockType === 'catch' && j < endIdx && codeTokens[j].text === '(') {
              while (j < endIdx && codeTokens[j].text !== ')') j++;
              if (j < endIdx) j++;
            }
            if (j < endIdx && codeTokens[j].text === '{') {
              let bDepth = 0;
              let blkStart = j + 1;
              let blkEnd = j;
              while (j < endIdx) {
                if (codeTokens[j].text === '{') bDepth++;
                else if (codeTokens[j].text === '}') {
                  bDepth--;
                  if (bDepth === 0) {
                    blkEnd = j;
                    j++;
                    break;
                  }
                }
                j++;
              }
              parseRange(blkStart, blkEnd);
            }
          }
          i = j;
          continue;
        }
      }

      // 3. SWITCH statement
      if (tok.type === 'word' && tok.text === 'switch') {
        const switchStartTok = tok;
        let parenDepth = 0;
        let condStart = -1;
        let condEnd = -1;
        let j = i + 1;

        while (j < endIdx) {
          if (codeTokens[j].text === '(') {
            if (parenDepth === 0) condStart = j + 1;
            parenDepth++;
          } else if (codeTokens[j].text === ')') {
            parenDepth--;
            if (parenDepth === 0) {
              condEnd = j;
              j++;
              break;
            }
          }
          j++;
        }

        if (condStart >= 0 && condEnd >= condStart && j < endIdx && codeTokens[j].text === '{') {
          const switchExpr = fullCode.substring(codeTokens[condStart].start, codeTokens[condEnd - 1].end).trim();
          const openBraceTok = j;
          let braceDepth = 0;
          let closeBraceTok = j;

          while (j < endIdx) {
            if (codeTokens[j].text === '{') braceDepth++;
            else if (codeTokens[j].text === '}') {
              braceDepth--;
              if (braceDepth === 0) {
                closeBraceTok = j;
                break;
              }
            }
            j++;
          }

          stmts.push({
            type: 'branch',
            rawText: fullCode.substring(switchStartTok.start, codeTokens[openBraceTok].end),
            startOffset: switchStartTok.start,
            endOffset: codeTokens[openBraceTok].end,
            description: `switch (${switchExpr})`,
          });

          const bodyStart = openBraceTok + 1;
          const bodyEnd = closeBraceTok;
          let k = bodyStart;

          while (k < bodyEnd) {
            const cTok = codeTokens[k];
            if (cTok.type === 'word' && (cTok.text === 'case' || cTok.text === 'default')) {
              const isCase = cTok.text === 'case';
              let colonIdx = k + 1;
              let pDepth = 0;
              let bDepth = 0;
              let brDepth = 0;

              while (colonIdx < bodyEnd) {
                const ct = codeTokens[colonIdx];
                if (ct.text === '(') pDepth++;
                else if (ct.text === ')') pDepth--;
                else if (ct.text === '{') bDepth++;
                else if (ct.text === '}') bDepth--;
                else if (ct.text === '[') brDepth++;
                else if (ct.text === ']') brDepth--;
                else if (ct.text === ':' && pDepth === 0 && bDepth === 0 && brDepth === 0) {
                  break;
                }
                colonIdx++;
              }

              if (colonIdx < bodyEnd && codeTokens[colonIdx].text === ':') {
                const caseLabelRaw = fullCode.substring(cTok.start, codeTokens[colonIdx].end).trim();
                let caseDesc = caseLabelRaw;
                if (isCase) {
                  const expr = fullCode.substring(codeTokens[k + 1].start, codeTokens[colonIdx - 1].end).trim();
                  caseDesc = `case ${expr}:`;
                } else {
                  caseDesc = 'default:';
                }

                stmts.push({
                  type: 'branch',
                  rawText: caseLabelRaw,
                  startOffset: cTok.start,
                  endOffset: codeTokens[colonIdx].end,
                  description: caseDesc,
                });

                let nextCaseIdx = colonIdx + 1;
                let innerBraceDepth = 0;
                let innerParenDepth = 0;
                let innerBracketDepth = 0;

                while (nextCaseIdx < bodyEnd) {
                  const nt = codeTokens[nextCaseIdx];
                  if (nt.text === '{') innerBraceDepth++;
                  else if (nt.text === '}') innerBraceDepth--;
                  else if (nt.text === '(') innerParenDepth++;
                  else if (nt.text === ')') innerParenDepth--;
                  else if (nt.text === '[') innerBracketDepth++;
                  else if (nt.text === ']') innerBracketDepth--;
                  else if (innerBraceDepth === 0 && innerParenDepth === 0 && innerBracketDepth === 0 && nt.type === 'word' && (nt.text === 'case' || nt.text === 'default')) {
                    break;
                  }
                  nextCaseIdx++;
                }

                parseRange(colonIdx + 1, nextCaseIdx);
                k = nextCaseIdx;
                continue;
              }
            }
            k++;
          }

          i = closeBraceTok + 1;
          continue;
        }
      }

      // 4. FOR and WHILE loops
      if (tok.type === 'word' && (tok.text === 'for' || tok.text === 'while')) {
        let j = i + 1;
        if (tok.text === 'for' && j < endIdx && codeTokens[j].type === 'word' && codeTokens[j].text === 'await') {
          j++;
        }

        let parenDepth = 0;
        let parenClosed = false;

        while (j < endIdx) {
          if (codeTokens[j].text === '(') {
            parenDepth++;
          } else if (codeTokens[j].text === ')') {
            parenDepth--;
            if (parenDepth === 0) {
              parenClosed = true;
              j++;
              break;
            }
          }
          j++;
        }

        if (parenClosed && j < endIdx) {
          if (codeTokens[j].text === '{') {
            const openBrace = j;
            let braceDepth = 0;
            let closeBrace = j;
            while (j < endIdx) {
              if (codeTokens[j].text === '{') braceDepth++;
              else if (codeTokens[j].text === '}') {
                braceDepth--;
                if (braceDepth === 0) {
                  closeBrace = j;
                  break;
                }
              }
              j++;
            }
            parseRange(openBrace + 1, closeBrace);
            i = closeBrace + 1;
            continue;
          } else {
            // Unbraced single statement loop
            let stmtEnd = j;
            let pD = 0, bD = 0, curD = 0;
            while (stmtEnd < endIdx) {
              const st = codeTokens[stmtEnd];
              if (st.text === '(') pD++;
              else if (st.text === ')') pD--;
              else if (st.text === '{') curD++;
              else if (st.text === '}') curD--;
              else if (st.text === '[') bD++;
              else if (st.text === ']') bD--;
              else if (st.text === ';' && pD === 0 && curD === 0 && bD === 0) {
                stmtEnd++;
                break;
              }
              stmtEnd++;
            }
            parseRange(j, stmtEnd);
            i = stmtEnd;
            continue;
          }
        }
      }

      // 5. DO ... WHILE loop
      if (tok.type === 'word' && tok.text === 'do') {
        let j = i + 1;
        if (j < endIdx && codeTokens[j].text === '{') {
          const openBrace = j;
          let braceDepth = 0;
          let closeBrace = j;
          while (j < endIdx) {
            if (codeTokens[j].text === '{') braceDepth++;
            else if (codeTokens[j].text === '}') {
              braceDepth--;
              if (braceDepth === 0) {
                closeBrace = j;
                break;
              }
            }
            j++;
          }
          parseRange(openBrace + 1, closeBrace);
          j = closeBrace + 1;
          if (j < endIdx && codeTokens[j].type === 'word' && codeTokens[j].text === 'while') {
            while (j < endIdx && codeTokens[j].text !== ';') j++;
            if (j < endIdx) j++;
          }
          i = j;
          continue;
        }
      }

      // 6. Standalone Block { ... }
      if (tok.text === '{') {
        let braceDepth = 0;
        let closeBrace = i;
        let j = i;
        while (j < endIdx) {
          if (codeTokens[j].text === '{') braceDepth++;
          else if (codeTokens[j].text === '}') {
            braceDepth--;
            if (braceDepth === 0) {
              closeBrace = j;
              break;
            }
          }
          j++;
        }
        parseRange(i + 1, closeBrace);
        i = closeBrace + 1;
        continue;
      }

      // 7. Statement: Call / Mutation / Assignment
      let stmtStartTok = i;
      let pDepth = 0;
      let bDepth = 0;
      let curDepth = 0;
      let j = i;

      while (j < endIdx) {
        const t = codeTokens[j];
        if (t.text === '(') pDepth++;
        else if (t.text === ')') pDepth--;
        else if (t.text === '{') curDepth++;
        else if (t.text === '}') curDepth--;
        else if (t.text === '[') bDepth++;
        else if (t.text === ']') bDepth--;
        else if (t.text === ';' && pDepth === 0 && curDepth === 0 && bDepth === 0) {
          j++;
          break;
        }
        j++;
      }

      const stmtEndTok = j;
      const statementTokens = codeTokens.slice(stmtStartTok, stmtEndTok);
      if (statementTokens.length === 0) {
        i++;
        continue;
      }

      const stmtStartOffset = statementTokens[0].start;
      const stmtEndOffset = statementTokens[statementTokens.length - 1].end;
      const rawStmtText = fullCode.substring(stmtStartOffset, stmtEndOffset).trim();

      // UI Noise Filtering
      let isNoise = false;
      for (const noise of uiNoiseDenylist) {
        if (rawStmtText.startsWith(noise) || rawStmtText.includes(`<${noise}`) || rawStmtText.includes(`.${noise}(`)) {
          isNoise = true;
          break;
        }
      }

      if (!isNoise && rawStmtText.length > 0) {
        // Mutation check
        if (
          rawStmtText.includes('state =') ||
          rawStmtText.includes('this.state') ||
          /^(setState|set[A-Z]|dispatch|emit|commit)\(/.test(rawStmtText) ||
          rawStmtText.includes('+=') ||
          rawStmtText.includes('-=')
        ) {
          stmts.push({
            type: 'mutation',
            rawText: rawStmtText,
            startOffset: stmtStartOffset,
            endOffset: stmtEndOffset,
            description: rawStmtText.replace(/;$/, '').replace(/\s+/g, ' '),
          });
        } else {
          // Call check
          const callMatch = rawStmtText.match(/(?:(?:const|let|var)\s+[^=]+=\s*)?(?:return\s+)?(?:await\s+)?(?:this\.)?([A-Za-z0-9_$]+(?:\s*\.\s*[A-Za-z0-9_$]+)*)\s*\(/);
          if (callMatch) {
            const rawChain = callMatch[1].replace(/\s+/g, '');
            const parts = rawChain.split('.');
            const methodName = parts[parts.length - 1];
            const receiver = parts.length > 1 ? parts.slice(0, -1).join('.') : '';

            const reservedKeywords = new Set([
              'if', 'for', 'while', 'switch', 'catch', 'finally', 'function', 'class', 'return', 'throw',
              'typeof', 'void', 'delete', 'import', 'export', 'new', 'yield'
            ]);

            if (methodName && !reservedKeywords.has(methodName)) {
              stmts.push({
                type: 'call',
                rawText: rawStmtText,
                startOffset: stmtStartOffset,
                endOffset: stmtEndOffset,
                receiver,
                methodName,
                description: rawStmtText.replace(/;$/, '').replace(/\s+/g, ' '),
              });
            }
          }
        }
      }

      i = stmtEndTok;
    }
  }

  parseRange(0, codeTokens.length);
  return stmts;
}

module.exports = {
  sliceFlow,
  extractStatements,
  tokenize,
  resolveCallTarget,
  findHookBinding,
  isBoundaryTarget,
  loadTsConfig,
};
