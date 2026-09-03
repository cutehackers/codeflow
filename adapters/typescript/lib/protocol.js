'use strict';

const fs = require('fs');
const path = require('path');
const { harvestCandidates } = require('./harvest');
const { sliceFlow } = require('./slice');
const { analysisMetadata, SCHEMA_ID, ANALYZER_VERSION } = require('./analysis');

const PROTOCOL_VERSION = 1;
const ADAPTER_VERSION = '0.1.0';
const CAPABILITIES = Object.freeze({
  cancellation: true,
  progress: true,
  batchAck: true,
  snapshotOverlay: true,
  analysisMetadata: true,
  maxMessageBytes: 1024 * 1024,
  maxInFlight: 64,
});

/**
 * Handles a single parsed JSON-RPC request line and returns an envelope object.
 * @param {object} req
 * @returns {object}
 */
function handleRequest(req) {
  if (!req || typeof req !== 'object' || Array.isArray(req)) {
    return {
      id: '',
      ok: false,
      err: {
        code: 'E_BAD_REQUEST',
        message: 'request must be a JSON object',
        retryable: false,
      },
    };
  }

  const id = typeof req.id === 'string' ? req.id : '';

  if (req.v !== PROTOCOL_VERSION) {
    return {
      id,
      ok: false,
      err: {
        code: 'E_UNSUPPORTED_VERSION',
        message: `unsupported protocol version ${JSON.stringify(req.v)}; expected ${PROTOCOL_VERSION}`,
        retryable: false,
      },
    };
  }

  const op = req.op;
  const params = req.params && typeof req.params === 'object' ? req.params : {};

  try {
    switch (op) {
      case 'ping':
        return {
          id,
          ok: true,
          result: {
            adapterVersion: ADAPTER_VERSION,
            protocolVersion: PROTOCOL_VERSION,
          },
        };

      case 'detect': {
        const repoRoot = params.repoRoot;
        if (!repoRoot || typeof repoRoot !== 'string') {
          return {
            id,
            ok: false,
            err: {
              code: 'E_BAD_REQUEST',
              message: 'params.repoRoot (non-empty string) is required',
              retryable: false,
            },
          };
        }
        const result = detectRepo(repoRoot, params);
        return { id, ok: true, result };
      }

      case 'harvest_candidates': {
        const repoRoot = params.repoRoot;
        if (!repoRoot || typeof repoRoot !== 'string') {
          return {
            id,
            ok: false,
            err: {
              code: 'E_BAD_REQUEST',
              message: 'params.repoRoot (non-empty string) is required',
              retryable: false,
            },
          };
        }
        const result = harvestCandidates(params);
        return { id, ok: true, result };
      }

      case 'slice': {
        const repoRoot = params.repoRoot;
        const candidateId = params.candidateId;
        const entrySymbolPath = params.entrySymbolPath;

        if (!repoRoot || !candidateId || !entrySymbolPath) {
          return {
            id,
            ok: false,
            err: {
              code: 'E_BAD_REQUEST',
              message: 'params.repoRoot, candidateId, and entrySymbolPath are required',
              retryable: false,
            },
          };
        }
        const result = sliceFlow(params);
        return { id, ok: true, result };
      }

      case 'shutdown':
        return {
          id,
          ok: true,
          result: {
            acknowledged: true,
          },
        };

      default:
        return {
          id,
          ok: false,
          err: {
            code: 'E_BAD_REQUEST',
            message: `unknown op: ${op}`,
            retryable: false,
          },
        };
    }
  } catch (err) {
    return {
      id,
      ok: false,
      err: {
        code: 'E_ADAPTER_INTERNAL',
        message: String(err && err.message ? err.message : err),
        retryable: false,
      },
    };
  }
}

/**
 * Handles one production JSON-RPC request. The historical handleRequest
 * helper above remains available for direct adapter tests and legacy callers.
 * @param {object} req
 * @returns {object}
 */
function handleRPCRequest(req) {
  const id = req && typeof req.id === 'string' ? req.id : '';
  if (!req || typeof req !== 'object' || Array.isArray(req)) {
    return rpcError(id, 'E_BAD_REQUEST', 'request must be a JSON object');
  }
  if (req.jsonrpc !== '2.0') {
    return rpcError(id, 'E_UNSUPPORTED_VERSION', 'jsonrpc must be "2.0"');
  }
  if (!id) return rpcError(id, 'E_BAD_REQUEST', 'request id must be a non-empty string');
  if (typeof req.method !== 'string') return rpcError(id, 'E_BAD_REQUEST', 'method must be a string');
  if (!req.params || typeof req.params !== 'object' || Array.isArray(req.params)) {
    return rpcError(id, 'E_BAD_REQUEST', 'params must be a JSON object');
  }

  const method = req.method === 'ping' ? 'initialize' : req.method;
  if (!['initialize', 'detect', 'harvest_candidates', 'slice', 'shutdown'].includes(method)) {
    return rpcError(id, 'E_BAD_REQUEST', `unknown method: ${req.method}`);
  }
  if (method === 'initialize') {
    return rpcSuccess(id, {
      adapterVersion: ADAPTER_VERSION,
      protocolVersion: PROTOCOL_VERSION,
      protocolVersions: [PROTOCOL_VERSION],
      analyzerVersion: ANALYZER_VERSION,
      schemaId: SCHEMA_ID,
      schemaVersion: 1,
      capabilities: CAPABILITIES,
    });
  }

  const legacy = handleRequest({ v: PROTOCOL_VERSION, id, op: method, params: req.params });
  if (!legacy.ok) return rpcError(id, legacy.err.code, legacy.err.message, legacy.err.retryable, legacy.err.detail);
  let result = legacy.result || {};
  if (['detect', 'harvest_candidates', 'slice'].includes(method)) {
    const explicitPaths = method === 'slice' && typeof req.params.entrySymbolPath === 'string'
      ? [req.params.entrySymbolPath.split('#')[0]]
      : [];
    result = { ...result, ...analysisMetadata(req.params, method, explicitPaths) };
  }
  return rpcSuccess(id, result);
}

function rpcSuccess(id, result) {
  return { jsonrpc: '2.0', id, result };
}

function rpcError(id, code, message, retryable = false, detail = undefined) {
  const data = { code, retryable: Boolean(retryable) };
  if (detail !== undefined) data.detail = String(detail).replace(/\b(?:api[_-]?key|secret|token|password)\s*[:=]\s*['"]?[^\s;'"}]+['"]?/gi, '***REDACTED***').slice(0, 512);
  const rpcCode = code === 'E_BAD_REQUEST' || code === 'E_UNSUPPORTED_VERSION' ? -32602 : -32000;
  const safeMessage = String(message).replace(/\b(?:api[_-]?key|secret|token|password)\s*[:=]\s*['"]?[^\s;'"}]+['"]?/gi, '***REDACTED***');
  return { jsonrpc: '2.0', id, error: { code: rpcCode, message: safeMessage.slice(0, 512), data } };
}

function detectRepo(repoRoot, params = {}) {
  const root = path.resolve(repoRoot);
  const overlay = params && typeof params === 'object' ? require('./analysis').overlayFor(params) : null;
  const pkgPath = path.join(root, 'package.json');
  const tsconfigPath = path.join(root, 'tsconfig.json');

  const hasPkg = overlay ? overlay.has('package.json') : fs.existsSync(pkgPath);
  const hasTs = overlay ? overlay.has('tsconfig.json') : fs.existsSync(tsconfigPath);

  if (!hasPkg && !hasTs) {
    return {
      matched: false,
      language: 'typescript',
      confident: false,
      frameworks: [],
      entryRoot: 'src',
      sourceExtensions: ['.ts', '.tsx', '.js', '.jsx'],
    };
  }

  let projectName = path.basename(root);
  const frameworks = [];

  if (hasPkg) {
    try {
      const pkg = JSON.parse(overlay ? overlay.get('package.json') : fs.readFileSync(pkgPath, 'utf8'));
      if (pkg.name) projectName = pkg.name;
      const deps = { ...(pkg.dependencies || {}), ...(pkg.devDependencies || {}) };
      if (deps['react']) frameworks.push('react');
      if (deps['next']) frameworks.push('nextjs');
      if (deps['@reduxjs/toolkit'] || deps['redux']) frameworks.push('redux');
      if (deps['zustand']) frameworks.push('zustand');
      if (deps['express']) frameworks.push('express');
      if (deps['fastify']) frameworks.push('fastify');
    } catch (_) {}
  }

  return {
    matched: true,
    language: 'typescript',
    confident: true,
    projectName,
    frameworks,
    entryRoot: overlay
      ? ([...overlay.keys()].some((rel) => rel.startsWith('src/')) ? 'src' : '.')
      : (fs.existsSync(path.join(root, 'src')) ? 'src' : '.'),
    sourceExtensions: ['.ts', '.tsx', '.js', '.jsx'],
  };
}

module.exports = {
  PROTOCOL_VERSION,
  ADAPTER_VERSION,
  CAPABILITIES,
  SCHEMA_ID,
  ANALYZER_VERSION,
  handleRequest,
  handleRPCRequest,
  detectRepo,
};
