'use strict';

const fs = require('fs');
const path = require('path');
const { harvestCandidates } = require('./harvest');
const { sliceFlow } = require('./slice');

const PROTOCOL_VERSION = 1;
const ADAPTER_VERSION = '0.1.0';

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
        const result = detectRepo(repoRoot);
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

function detectRepo(repoRoot) {
  const root = path.resolve(repoRoot);
  const pkgPath = path.join(root, 'package.json');
  const tsconfigPath = path.join(root, 'tsconfig.json');

  const hasPkg = fs.existsSync(pkgPath);
  const hasTs = fs.existsSync(tsconfigPath);

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
      const pkg = JSON.parse(fs.readFileSync(pkgPath, 'utf8'));
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
    entryRoot: fs.existsSync(path.join(root, 'src')) ? 'src' : '.',
    sourceExtensions: ['.ts', '.tsx', '.js', '.jsx'],
  };
}

module.exports = {
  PROTOCOL_VERSION,
  ADAPTER_VERSION,
  handleRequest,
  detectRepo,
};
