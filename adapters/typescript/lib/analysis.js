'use strict';

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');

const SCHEMA_ID = 'https://codeflow.local/schemas/adapter-analysis.schema.json';
const READ_SET_SCHEMA_ID = 'https://codeflow.local/schemas/analysis-read-set.schema.json';
const CLOSURE_SCHEMA_ID = 'https://codeflow.local/schemas/causal-observation-closure.schema.json';
const PROTOCOL_VERSION = 1;
const ADAPTER_VERSION = '0.1.0';
const ANALYZER_VERSION = 'typescript-structural/0.1.0';
const MAX_DOCUMENTS = 4096;

function sha256(value) {
  return crypto.createHash('sha256').update(value).digest('hex');
}

function overlayFor(params) {
  const source = params && typeof params === 'object' ? params : {};
  const snapshot = source.snapshot && typeof source.snapshot === 'object' ? source.snapshot : {};
  const candidate = source.contentOverlay || snapshot.contentOverlay || snapshot.files;
  if (!candidate || typeof candidate !== 'object' || Array.isArray(candidate)) return null;
  const out = new Map();
  for (const [key, value] of Object.entries(candidate)) {
    const rel = String(key).replaceAll('\\', '/').replace(/^\.\//, '');
    if (!rel || rel.includes('..') || rel.startsWith('/')) continue;
    if (typeof value === 'string') out.set(rel, value);
    else if (value && typeof value === 'object' && typeof value.content === 'string') out.set(rel, value.content);
  }
  return out;
}

function suppliedBasis(params) {
  const source = params && typeof params === 'object' ? params : {};
  const snapshot = source.snapshot && typeof source.snapshot === 'object' ? source.snapshot : {};
  return typeof source.computedBasisId === 'string' && source.computedBasisId
    ? source.computedBasisId
    : (typeof snapshot.computedBasisId === 'string' && snapshot.computedBasisId ? snapshot.computedBasisId : '');
}

function suppliedEpoch(params) {
  const source = params && typeof params === 'object' ? params : {};
  const snapshot = source.snapshot && typeof source.snapshot === 'object' ? source.snapshot : {};
  const value = source.workspaceEpoch ?? snapshot.workspaceEpoch;
  return Number.isInteger(value) && value >= 0 ? value : 0;
}

function collectReadDocuments(params, operation, explicitPaths = []) {
  const overlay = overlayFor(params);
  if (overlay) {
    return [...overlay.entries()]
      .sort(([a], [b]) => a.localeCompare(b))
      .slice(0, MAX_DOCUMENTS)
      .map(([rel, content]) => ({ path: rel, content }));
  }

  const repoRoot = params && typeof params.repoRoot === 'string' ? path.resolve(params.repoRoot) : null;
  if (!repoRoot) return [];
  const paths = [...explicitPaths];
  if (operation === 'detect') paths.push('package.json', 'tsconfig.json', 'jsconfig.json');
  if (operation === 'harvest_candidates') {
    // Keep this dependency lazy to avoid a protocol/harvest module cycle.
    try {
      const { listSourceFiles } = require('./harvest');
      paths.push(...listSourceFiles(repoRoot));
    } catch (_) {}
  }
  const seen = new Set();
  const docs = [];
  for (const rel of paths) {
    const normalized = String(rel).replaceAll('\\', '/').replace(/^\.\//, '');
    if (!normalized || seen.has(normalized) || normalized.includes('..') || normalized.startsWith('/')) continue;
    seen.add(normalized);
    const full = path.join(repoRoot, normalized);
    try {
      const content = fs.readFileSync(full, 'utf8');
      docs.push({ path: normalized, content });
    } catch (_) {}
    if (docs.length >= MAX_DOCUMENTS) break;
  }
  return docs.sort((a, b) => a.path.localeCompare(b.path));
}

function analysisMetadata(params, operation, explicitPaths = [], diagnostics = []) {
  const docs = collectReadDocuments(params, operation, explicitPaths);
  const documentMetadata = docs.map(({ path: rel, content }) => ({
    path: rel,
    contentHash: sha256(Buffer.from(content, 'utf8')),
    byteLength: Buffer.byteLength(content, 'utf8'),
  }));
  const basis = suppliedBasis(params) || sha256(documentMetadata.map((doc) => `${doc.path}:${doc.contentHash}\n`).join(''));
  const workspaceEpoch = suppliedEpoch(params);
  const readSetId = `readset-${sha256(`${basis}:${workspaceEpoch}:${operation}`).slice(0, 24)}`;
  const closureId = `closure-${sha256(`${readSetId}:${operation}`).slice(0, 24)}`;
  const capabilityProfile = {
    adapter: 'typescript',
    features: ['symbols', 'calls', 'snapshot_overlay', 'negative_lookup', 'membership', 'dependency_frontier'],
    protocolVersions: [PROTOCOL_VERSION],
    coverageBoundary: { includedSourceRoots: ['.'], excludedReasons: [] },
  };
  const boundedDiagnostics = Array.isArray(diagnostics) ? diagnostics.slice(0, 64).map((item) => ({
    severity: ['info', 'warning', 'error'].includes(item.severity) ? item.severity : 'warning',
    message: String(item.message || 'adapter diagnostic').slice(0, 512),
    ...(item.path ? { path: String(item.path).slice(0, 1024) } : {}),
  })) : [];
  const analysisReadSet = {
    schemaId: READ_SET_SCHEMA_ID,
    schemaVersion: 1,
    readSetId,
    computedBasisId: basis,
    workspaceEpoch,
    documents: documentMetadata,
    indexes: [],
    negativeObservations: [],
    membershipObservations: [{ kind: 'source_membership', path: '.', valueHash: sha256(documentMetadata.map((doc) => doc.path).join('\n')) }],
    dependencyFrontiers: [{ kind: 'dependency_frontier', path: operation, detail: 'frontier bounded at adapter boundary' }],
    adapterVersions: { typescript: ADAPTER_VERSION },
  };
  const causalObservationClosure = {
    schemaId: CLOSURE_SCHEMA_ID,
    schemaVersion: 1,
    closureId,
    analysisReadSetId: readSetId,
    computedBasisId: basis,
    workspaceEpoch,
    closureStatus: 'closed',
    negativeObservations: [],
    membershipObservations: analysisReadSet.membershipObservations,
    dependencyFrontiers: analysisReadSet.dependencyFrontiers,
    capabilityProfile,
    coverageBoundary: capabilityProfile.coverageBoundary,
    incompleteReasons: [],
    closureDigest: sha256(JSON.stringify({ analysisReadSet, capabilityProfile })),
  };
  return {
    schemaId: SCHEMA_ID,
    schemaVersion: 1,
    operation,
    computedBasisId: basis,
    workspaceEpoch,
    analysisReadSet,
    causalObservationClosure,
    capabilityProfile,
    analyzerVersion: ANALYZER_VERSION,
    diagnostics: boundedDiagnostics,
  };
}

module.exports = {
  SCHEMA_ID,
  READ_SET_SCHEMA_ID,
  CLOSURE_SCHEMA_ID,
  PROTOCOL_VERSION,
  ADAPTER_VERSION,
  ANALYZER_VERSION,
  overlayFor,
  analysisMetadata,
  collectReadDocuments,
};
