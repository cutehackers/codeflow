'use strict';

const fs = require('fs');
const path = require('path');
const { harvestCandidates } = require('./harvest');
const { sliceFlow } = require('./slice');
const { analysisMetadata, overlayFor, createAnalysisTracker, SCHEMA_ID, ANALYZER_VERSION } = require('./analysis');
const { redactDiagnostic } = require('./secret');

const PROTOCOL_VERSION = 1;
const ADAPTER_VERSION = '0.1.0';
const ANALYZER_REQUEST_SCHEMA_ID = 'https://codeflow.local/schemas/rflsc.analyzer-request.v2.schema.json';
const ANALYZER_RESULT_SCHEMA_ID = 'https://codeflow.local/schemas/rflsc.analyzer-result.v2.schema.json';
const READ_SET_V2_SCHEMA_ID = 'https://codeflow.local/schemas/rflsc.analysis-read-set.v2.schema.json';
const CLOSURE_V2_SCHEMA_ID = 'https://codeflow.local/schemas/rflsc.observation-closure.v2.schema.json';
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
    return legacyError('', 'E_BAD_REQUEST', 'request must be a JSON object');
  }

  const id = typeof req.id === 'string' ? req.id : '';

  if (req.v !== PROTOCOL_VERSION) {
    return legacyError(id, 'E_UNSUPPORTED_VERSION',
      `unsupported protocol version ${JSON.stringify(req.v)}; expected ${PROTOCOL_VERSION}`);
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
          return legacyError(id, 'E_BAD_REQUEST', 'params.repoRoot (non-empty string) is required');
        }
        const result = detectRepo(repoRoot, params, params.__analysisTracker || null);
        return { id, ok: true, result };
      }

      case 'harvest_candidates': {
        const repoRoot = params.repoRoot;
        if (!repoRoot || typeof repoRoot !== 'string') {
          return legacyError(id, 'E_BAD_REQUEST', 'params.repoRoot (non-empty string) is required');
        }
        const result = harvestCandidates(params, params.__analysisTracker || null);
        return { id, ok: true, result };
      }

      case 'slice': {
        const repoRoot = params.repoRoot;
        const candidateId = params.candidateId;
        const entrySymbolPath = params.entrySymbolPath;

        if (!repoRoot || !candidateId || !entrySymbolPath) {
          return legacyError(id, 'E_BAD_REQUEST',
            'params.repoRoot, candidateId, and entrySymbolPath are required');
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
        return legacyError(id, 'E_BAD_REQUEST', `unknown op: ${op}`);
    }
  } catch (err) {
    return legacyError(id, 'E_ADAPTER_INTERNAL', String(err && err.message ? err.message : err));
  }
}

function legacyError(id, code, message, retryable = false) {
  return {
    id,
    ok: false,
    err: { code, message: redactDiagnostic(message), retryable: Boolean(retryable) },
  };
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

  if (['detect', 'harvest_candidates', 'slice'].includes(method)) {
    const requestError = validateAnalyzerRequestV2(id, method, req.params);
    if (requestError) return rpcError(id, 'E_BAD_REQUEST', requestError);
  }

  const snapshotOverlay = overlayFor(req.params);
  if (['detect', 'harvest_candidates', 'slice'].includes(method) && !snapshotOverlay) {
    return rpcError(id, 'E_BAD_REQUEST', 'snapshot.files (immutable protocol content) is required');
  }
  // The legacy helper still accepts repoRoot for direct package tests. The
  // production RPC path supplies a disposable synthetic root only so the
  // parser APIs can resolve relative names. All reads are forced through the
  // snapshot overlay above.
  const operationPayload = req.params.payload && typeof req.params.payload === 'object'
    ? req.params.payload : {};
  const tracker = createAnalysisTracker(req.params, method);
  const internalParams = { ...req.params, ...operationPayload, __analysisTracker: tracker };
  internalParams.repoRoot = typeof operationPayload.repoRoot === 'string' && operationPayload.repoRoot
    ? operationPayload.repoRoot : process.cwd();
  const legacy = handleRequest({ v: PROTOCOL_VERSION, id, op: method, params: internalParams });
  if (!legacy.ok) return rpcError(id, legacy.err.code, legacy.err.message, legacy.err.retryable, legacy.err.detail);
  let result = legacy.result || {};
  if (['detect', 'harvest_candidates', 'slice'].includes(method)) {
    const explicitPaths = method === 'slice' && typeof req.params.entrySymbolPath === 'string'
      ? [req.params.entrySymbolPath.split('#')[0]]
      : [];
    const metadata = analysisMetadata(req.params, method, explicitPaths, [], tracker);
    result = analyzerResultV2(id, method, req.params, result, metadata);
  }
  return rpcSuccess(id, result);
}

function validateAnalyzerRequestV2(id, operation, params) {
  if (params.schemaId !== ANALYZER_REQUEST_SCHEMA_ID || params.schemaVersion !== 2) {
    return 'analysis request must use rflsc.analyzer-request.v2';
  }
  if (params.requestId !== id) return 'analysis requestId must match JSON-RPC id';
  if (params.operation !== operation) return 'analysis operation does not match JSON-RPC method';
  if (!params.snapshot || typeof params.snapshot !== 'object' || Array.isArray(params.snapshot)) {
    return 'analysis snapshot is required';
  }
  for (const field of ['snapshotId', 'workspaceEpoch', 'computedBasisId', 'rootTreeId', 'dependencyFingerprint', 'documents', 'files', 'repositoryPathWriteAudit']) {
    if (!(field in params.snapshot)) return `analysis snapshot is missing ${field}`;
  }
  return null;
}

function analyzerResultV2(id, operation, params, payload, metadata) {
  const snapshot = params.snapshot;
  const oldReadSet = metadata.analysisReadSet || {};
  const oldClosure = metadata.causalObservationClosure || {};
  const oldCapability = metadata.capabilityProfile || {};
  const coverage = oldClosure.coverageBoundary || oldCapability.coverageBoundary || {
    includedSourceRoots: ['.'], measured: true,
  };
  const readSet = {
    schemaId: READ_SET_V2_SCHEMA_ID,
    schemaVersion: 2,
    readSetId: oldReadSet.readSetId || `readset-${id}`,
    computedBasisId: snapshot.computedBasisId,
    workspaceEpoch: snapshot.workspaceEpoch,
    documents: Array.isArray(oldReadSet.documents) ? oldReadSet.documents : [],
    negativeObservations: Array.isArray(oldReadSet.negativeObservations) ? oldReadSet.negativeObservations : [],
    membershipObservations: Array.isArray(oldReadSet.membershipObservations) ? oldReadSet.membershipObservations : [],
    dependencyFrontiers: Array.isArray(oldReadSet.dependencyFrontiers) ? oldReadSet.dependencyFrontiers : [],
  };
  const closure = {
    schemaId: CLOSURE_V2_SCHEMA_ID,
    schemaVersion: 2,
    closureId: oldClosure.closureId || `closure-${id}`,
    analysisReadSetId: readSet.readSetId,
    computedBasisId: snapshot.computedBasisId,
    workspaceEpoch: snapshot.workspaceEpoch,
    closureStatus: oldClosure.closureStatus === 'open' ? 'open' : 'closed',
    negativeObservations: readSet.negativeObservations,
    membershipObservations: readSet.membershipObservations,
    dependencyFrontiers: readSet.dependencyFrontiers,
    requiredObservations: Array.isArray(oldClosure.requiredObservations) ? oldClosure.requiredObservations : [],
    measuredObservations: Array.isArray(oldClosure.measuredObservations) ? oldClosure.measuredObservations : [],
    incompleteReasons: Array.isArray(oldClosure.incompleteReasons) ? oldClosure.incompleteReasons : [],
  };
  const features = Array.isArray(oldCapability.features) && oldCapability.features.length > 0
    ? oldCapability.features : ['snapshot_bytes'];
  return {
    schemaId: ANALYZER_RESULT_SCHEMA_ID,
    schemaVersion: 2,
    requestId: id,
    operation,
    adapterVersion: ADAPTER_VERSION,
    analyzerRevision: ANALYZER_VERSION,
    workspaceEpoch: snapshot.workspaceEpoch,
    computedBasisId: snapshot.computedBasisId,
    snapshotId: snapshot.snapshotId,
    snapshotTreeDigest: snapshot.rootTreeId,
    dependencyFingerprint: snapshot.dependencyFingerprint,
    analysisReadSet: readSet,
    causalObservationClosure: closure,
    capabilityProfile: {
      adapter: 'typescript',
      adapterVersion: ADAPTER_VERSION,
      analyzerRevision: ANALYZER_VERSION,
      features,
      unsupported: Array.isArray(oldCapability.unsupported) ? oldCapability.unsupported : [],
    },
    coverage,
    diagnostics: Array.isArray(metadata.diagnostics) ? metadata.diagnostics : [],
    payload,
  };
}

function rpcSuccess(id, result) {
  return { jsonrpc: '2.0', id, result };
}

// Serializes a response once and applies the common negotiated body limit
// before the stdio writer emits a frame. Oversized or unserializable values
// become one small typed error without recursively passing through the writer.
function encodeBoundedResponse(value, maxBytes = CAPABILITIES.maxMessageBytes) {
  const limit = Number.isSafeInteger(maxBytes) && maxBytes > 0
    ? maxBytes : CAPABILITIES.maxMessageBytes;
  let bytes;
  try {
    if (Buffer.isBuffer(value)) {
      bytes = value;
    } else {
      const serialized = JSON.stringify(value);
      if (typeof serialized !== 'string') throw new Error('response is not JSON serializable');
      bytes = Buffer.from(serialized, 'utf8');
    }
  } catch (_) {
    bytes = null;
  }
  if (bytes && bytes.length <= limit) return bytes;

  const id = value && typeof value === 'object' && typeof value.id === 'string' ? value.id : '';
  const fallback = {
    jsonrpc: '2.0',
    id,
    error: {
      code: -32000,
      message: 'adapter response exceeds maxMessageBytes',
      data: { code: 'E_ADAPTER_INTERNAL', retryable: false },
    },
  };
  try {
    let fallbackBytes = Buffer.from(JSON.stringify(fallback), 'utf8');
    if (fallbackBytes.length > limit) {
      // An untrusted id can consume the remaining bound. Drop it and retry
      // the fixed-size typed error before declaring the bound too small.
      fallback.id = '';
      fallbackBytes = Buffer.from(JSON.stringify(fallback), 'utf8');
    }
    return fallbackBytes.length <= limit ? fallbackBytes : null;
  } catch (_) {
    return null;
  }
}

function rpcError(id, code, message, retryable = false, detail = undefined) {
  const data = { code, retryable: Boolean(retryable) };
  if (detail !== undefined) data.detail = redactDiagnostic(detail);
  const rpcCode = code === 'E_BAD_REQUEST' || code === 'E_UNSUPPORTED_VERSION' ? -32602 : -32000;
  return { jsonrpc: '2.0', id, error: { code: rpcCode, message: redactDiagnostic(message), data } };
}

function boundedDiagnostic(value) {
  return redactDiagnostic(value);
}

function detectRepo(repoRoot, params = {}, tracker = null) {
  const root = path.resolve(repoRoot);
  if (tracker) {
    const packageJSON = tracker.read('package.json');
    const tsConfig = tracker.read('tsconfig.json');
    if (packageJSON !== null) tracker.recordDependency('package.json');
    if (tsConfig !== null) tracker.recordDependency('tsconfig.json');
    const hasPkg = packageJSON !== null;
    const hasTs = tsConfig !== null;
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
    if (packageJSON) {
      try {
        const pkg = JSON.parse(packageJSON);
        if (pkg.name) projectName = pkg.name;
        const deps = { ...(pkg.dependencies || {}), ...(pkg.devDependencies || {}) };
        if (deps.react) frameworks.push('react');
        if (deps.next) frameworks.push('nextjs');
        if (deps['@reduxjs/toolkit'] || deps.redux) frameworks.push('redux');
        if (deps.zustand) frameworks.push('zustand');
        if (deps.express) frameworks.push('express');
        if (deps.fastify) frameworks.push('fastify');
      } catch (_) {}
    }
    return {
      matched: true,
      language: 'typescript',
      confident: true,
      projectName,
      frameworks,
      entryRoot: tracker.overlay
        ? ([...tracker.overlay.keys()].some((rel) => rel.startsWith('src/')) ? 'src' : '.')
        : '.',
      sourceExtensions: ['.ts', '.tsx', '.js', '.jsx'],
    };
  }
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
  ANALYZER_REQUEST_SCHEMA_ID,
  ANALYZER_RESULT_SCHEMA_ID,
  handleRequest,
  handleRPCRequest,
  encodeBoundedResponse,
  detectRepo,
  rpcError,
  redactDiagnostic,
};
