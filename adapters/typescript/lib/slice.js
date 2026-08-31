'use strict';

const fs = require('fs');
const path = require('path');
const { sha256Hex, canonicalAstFingerprint, byteOffset } = require('./sha256');
const { redactSecrets } = require('./secret');
const { humanizeIdentifier } = require('./humanize');
const { scanSource } = require('./scanner');

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
  const maxDepth = (params.opts && params.opts.maxDepth) || 5;

  const [relPath, initialSymbol] = entrySymbolPath.split('#');
  const fileCache = new Map();
  const scanCache = new Map();
  const tsConfig = loadTsConfig(repoRoot);

  function readFile(p) {
    if (fileCache.has(p)) return fileCache.get(p);
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
    const scan = scanSource(content);
    scanCache.set(p, scan);
    return scan;
  }

  const steps = [];
  const edges = [];
  const activeStack = new Set();
  let truncated = false;
  let visitedCycleDetected = false;
  let totalRedactedCount = 0;

  function sliceSymbolBody({ currentRelPath, className, methodName, depth }) {
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

    try {
      const code = readFile(currentRelPath);
      if (!code) return;
      const scan = getScan(currentRelPath);
      if (!scan) return;

      let bodyStart = -1;
      let bodyEnd = -1;

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
      } else {
        for (const fn of scan.topLevelFunctions) {
          if (fn.name === methodName) {
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

      const stmts = extractStatements(bodyText, bodyStart, code);

      for (const stmt of stmts) {
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

        const stepOrdinal = steps.length + 1;

        if (stmt.type === 'guard') {
          const condRedact = redactSecrets(stmt.guardCondition || '');
          const descRedact = redactSecrets(`if (${condRedact.text}) return`);
          totalRedactedCount += condRedact.count + descRedact.count;

          steps.push({
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
        } else if (stmt.type === 'mutation') {
          const descRedact = redactSecrets(stmt.description || stmt.rawText);
          totalRedactedCount += descRedact.count;

          steps.push({
            ordinal: stepOrdinal,
            kind: 'mutation',
            description: descRedact.text,
            symbolPath: fullSym,
            anchor,
            guardCondition: null,
            stateBefore: stmt.stateBefore || null,
            stateAfter: stmt.stateAfter || null,
            effectTarget: null,
          });
        } else if (stmt.type === 'call' || stmt.type === 'effect') {
          const descRedact = redactSecrets(stmt.description || stmt.rawText);
          totalRedactedCount += descRedact.count;

          const isBoundary = isBoundaryTarget(stmt.receiver, stmt.methodName);
          const effectTarget = isBoundary ? `${stmt.receiver ? stmt.receiver + '.' : ''}${stmt.methodName}` : null;

          steps.push({
            ordinal: stepOrdinal,
            kind: isBoundary ? 'effect' : 'call',
            description: descRedact.text,
            symbolPath: fullSym,
            anchor,
            guardCondition: null,
            stateBefore: null,
            stateAfter: null,
            effectTarget,
          });

          if (isBoundary) {
            edges.push({
              kind: 'boundary_call',
              toSymbolPath: `${currentRelPath}#${stmt.receiver ? stmt.receiver + '.' : ''}${stmt.methodName}`,
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
            });

            if (target) {
              edges.push({
                kind: 'resolved_cross_file',
                toSymbolPath: `${target.relPath}#${target.className ? target.className + '.' : ''}${target.methodName}`,
                resolutionStatus: 'resolved',
                depth: depth + 1,
                stepOrdinal,
              });

              sliceSymbolBody({
                currentRelPath: target.relPath,
                className: target.className,
                methodName: target.methodName,
                depth: depth + 1,
              });
            } else {
              edges.push({
                kind: 'unknown_edge',
                toSymbolPath: `${currentRelPath}#${stmt.receiver ? stmt.receiver + '.' : ''}${stmt.methodName}`,
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
  const target = `${receiver || ''}.${methodName || ''}`;
  for (const suffix of boundarySuffixes) {
    if (
      (receiver && (receiver.endsWith(suffix) || receiver.toLowerCase().includes(suffix.toLowerCase()))) ||
      (methodName && (methodName.endsWith(suffix) || methodName.toLowerCase().includes(suffix.toLowerCase()))) ||
      target.toLowerCase().includes(suffix.toLowerCase())
    ) {
      return true;
    }
  }
  return false;
}

function loadTsConfig(repoRoot) {
  const configFiles = ['tsconfig.json', 'jsconfig.json'];
  for (const file of configFiles) {
    const full = path.join(repoRoot, file);
    if (fs.existsSync(full)) {
      try {
        let content = fs.readFileSync(full, 'utf8');
        content = content
          .replace(/\/\/[^\n]*/g, '')
          .replace(/\/\*[\s\S]*?\*\//g, '')
          .replace(/,(\s*[\]}])/g, '$1');
        const parsed = JSON.parse(content);
        const compilerOptions = parsed.compilerOptions || {};
        return {
          baseUrl: compilerOptions.baseUrl || '.',
          paths: compilerOptions.paths || {},
        };
      } catch (_) {}
    }
  }
  return { baseUrl: '.', paths: {} };
}

function resolveCallTarget({ repoRoot, currentRelPath, scan, receiver, methodName, readFile, getScan, tsConfig }) {
  const currentDir = path.dirname(currentRelPath);
  const cfg = tsConfig || { baseUrl: '.', paths: {} };

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
      if (fn.name === methodName) {
        return { relPath: currentRelPath, className: '', methodName: fn.name };
      }
    }
  }

  // 2. Resolve imports
  for (const imp of scan.imports) {
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
        candidatePaths.push(path.resolve(repoRoot, currentDir, imp));
      }
    }

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
          // Match class method
          for (const cls of targetScan.classes) {
            const matchesClass =
              !receiver ||
              receiver === 'this' ||
              cls.name.toLowerCase() === receiver.toLowerCase() ||
              receiver.toLowerCase().includes(cls.name.toLowerCase()) ||
              cls.name.toLowerCase().includes(receiver.toLowerCase());

            if (matchesClass) {
              for (const m of cls.methods) {
                if (m.name === methodName) {
                  return { relPath: targetRel, className: cls.name, methodName: m.name };
                }
              }
            }
          }

          // Fallback: check any class in file for method
          for (const cls of targetScan.classes) {
            for (const m of cls.methods) {
              if (m.name === methodName) {
                return { relPath: targetRel, className: cls.name, methodName: m.name };
              }
            }
          }

          // Match top-level functions
          for (const fn of targetScan.topLevelFunctions) {
            if (fn.name === methodName || (receiver && fn.name === receiver)) {
              return { relPath: targetRel, className: '', methodName: fn.name };
            }
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
          const callMatch = rawStmtText.match(/(?:(?:const|let|var)\s+[^=]+=\s*)?(?:return\s+)?(?:await\s+)?(?:this\.)?([A-Za-z0-9_$]+)\s*(?:\.\s*([A-Za-z0-9_$]+))?\s*\(/);
          if (callMatch) {
            const receiver = callMatch[2] ? callMatch[1] : '';
            const methodName = callMatch[2] ? callMatch[2] : callMatch[1];

            if (methodName && !['if', 'for', 'while', 'switch', 'catch', 'finally', 'function', 'class', 'return', 'throw'].includes(methodName)) {
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
  isBoundaryTarget,
  loadTsConfig,
};
