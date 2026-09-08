'use strict';

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const { redactDiagnostic, redactSecrets } = require('./secret');

const SCHEMA_ID = 'https://codeflow.local/schemas/adapter-analysis.schema.json';
const READ_SET_SCHEMA_ID = 'https://codeflow.local/schemas/analysis-read-set.schema.json';
const CLOSURE_SCHEMA_ID = 'https://codeflow.local/schemas/causal-observation-closure.schema.json';
const PROTOCOL_VERSION = 1;
const ADAPTER_VERSION = '0.4.0';
const ANALYZER_VERSION = 'typescript-structural/0.4.0';
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
    const identities = snapshotDocumentIdentities(params);
    return [...overlay.entries()]
      .sort(([a], [b]) => a.localeCompare(b))
      .slice(0, MAX_DOCUMENTS)
      .map(([rel, content]) => ({ path: rel, content, ...(identities.get(rel) || {}) }));
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

function snapshotDocumentIdentities(params) {
  const raw = params && params.snapshot && Array.isArray(params.snapshot.documents)
    ? params.snapshot.documents
    : [];
  const out = new Map();
  for (const item of raw) {
    if (!item || typeof item !== 'object' || typeof item.path !== 'string') continue;
    out.set(item.path, {
      ...(typeof item.documentRevisionId === 'string' ? { documentRevisionId: item.documentRevisionId } : {}),
      ...(typeof item.contentId === 'string' ? { contentId: item.contentId } : {}),
      ...(Number.isInteger(item.documentVersion) ? { documentVersion: item.documentVersion } : {}),
    });
  }
  return out;
}

class AnalysisTracker {
  constructor(params, operation) {
    this.params = params && typeof params === 'object' ? params : {};
    this.operation = operation;
    this.overlay = overlayFor(this.params);
    this.repoRoot = typeof this.params.repoRoot === 'string' ? path.resolve(this.params.repoRoot) : null;
    this.identities = snapshotDocumentIdentities(this.params);
    this.documents = new Map();
    this.missing = new Map();
    this.membership = new Map();
    this.frontiers = new Map();
    this.coverageRoots = new Set(['.']);
  }

  normalize(rel) {
    const normalized = String(rel || '').replaceAll('\\', '/').replace(/^\.\//, '');
    if (!normalized || normalized.startsWith('/') || normalized.split('/').includes('..')) return null;
    return normalized;
  }

  read(rel) {
    const normalized = this.normalize(rel);
    if (!normalized) return null;
    let content = null;
    if (this.overlay) {
      if (!this.overlay.has(normalized)) {
        this.recordMissing(normalized);
        return null;
      }
      content = this.overlay.get(normalized);
    } else if (this.repoRoot) {
      try {
        content = fs.readFileSync(path.join(this.repoRoot, normalized), 'utf8');
      } catch (_) {
        this.recordMissing(normalized);
        return null;
      }
    } else {
      this.recordMissing(normalized);
      return null;
    }
    this.documents.set(normalized, { path: normalized, content: String(content) });
    this.missing.delete(normalized);
    return String(content);
  }

  recordMissing(rel, detail = 'lookup was absent from the captured snapshot') {
    const normalized = this.normalize(rel);
    if (!normalized || this.documents.has(normalized)) return;
    this.missing.set(normalized, {
      kind: 'negative_lookup',
      path: normalized,
      valueHash: sha256(Buffer.from(`${normalized}:absent`, 'utf8')),
      detail,
      measured: true,
    });
  }

  enumerateSourceFiles() {
    let files;
    if (this.overlay) {
      files = [...this.overlay.keys()]
        .filter((rel) => /\.(ts|tsx|js|jsx|mjs|cjs)$/.test(rel))
        .filter((rel) => !rel.endsWith('.d.ts') && !rel.includes('.test.') && !rel.includes('.spec.'))
        .sort();
    } else if (this.repoRoot) {
      try {
        const { listSourceFiles } = require('./harvest');
        files = listSourceFiles(this.repoRoot);
      } catch (_) {
        files = [];
      }
    } else {
      files = [];
    }
    this.recordMembership('.', files);
    return files;
  }

  recordMembership(scope, files) {
    const normalized = [...new Set((files || []).map((item) => this.normalize(item)).filter(Boolean))].sort();
    this.membership.set(scope, {
      kind: 'source_membership',
      path: scope,
      valueHash: sha256(Buffer.from(normalized.join('\n'), 'utf8')),
      detail: 'membership measured from the source enumeration used by the operation',
      measured: true,
    });
    this.coverageRoots.add(scope);
  }

  recordDependency(rel) {
    const normalized = this.normalize(rel);
    const document = normalized ? this.documents.get(normalized) : null;
    if (!document) return false;
    this.frontiers.set(normalized, {
      kind: 'dependency_frontier',
      path: normalized,
      valueHash: sha256(Buffer.from(document.content, 'utf8')),
      detail: 'dependency/configuration frontier established by the analyzer',
      measured: true,
    });
    return true;
  }

  metadata(diagnostics = []) {
    const snapshot = this.params.snapshot && typeof this.params.snapshot === 'object' ? this.params.snapshot : {};
    const documentMetadata = [...this.documents.values()].sort((a, b) => a.path.localeCompare(b.path)).map((document) => {
      const identity = this.identities.get(document.path) || {};
      const contentHash = sha256(Buffer.from(document.content, 'utf8'));
      return {
        path: document.path,
        ...(identity.documentRevisionId ? { documentRevisionId: identity.documentRevisionId } : {}),
        ...(identity.contentId ? { contentId: identity.contentId } : {}),
        ...(Number.isInteger(identity.documentVersion) ? { documentVersion: identity.documentVersion } : {}),
        contentHash,
        byteLength: Buffer.byteLength(document.content, 'utf8'),
      };
    });
    const basis = suppliedBasis(this.params) || sha256(Buffer.from(documentMetadata.map((doc) => `${doc.path}:${doc.contentHash}\n`).join(''), 'utf8'));
    const workspaceEpoch = suppliedEpoch(this.params);
    const readSetId = `readset-${sha256(Buffer.from(`${basis}:${workspaceEpoch}:${this.operation}`, 'utf8')).slice(0, 24)}`;
    const closureId = `closure-${sha256(Buffer.from(`${readSetId}:${this.operation}`, 'utf8')).slice(0, 24)}`;
    const requiredObservations = Array.isArray(this.params.requiredObservations)
      ? this.params.requiredObservations.filter((item) => typeof item === 'string' && item.length > 0)
      : [];
    const negativeObservations = [...this.missing.values()].sort((a, b) => a.path.localeCompare(b.path));
    const membershipObservations = [...this.membership.values()];
    const dependencyFrontiers = [...this.frontiers.values()].sort((a, b) => a.path.localeCompare(b.path));
    const measuredObservations = [];
    if (negativeObservations.length > 0) measuredObservations.push('negative_lookup');
    if (membershipObservations.length > 0) measuredObservations.push('membership');
    if (dependencyFrontiers.length > 0) measuredObservations.push('dependency_frontier');
    const unsupported = ['runtime_observation', 'dynamic_resolution'];
    const incompleteReasons = requiredObservations
      .filter((kind) => !measuredObservations.includes(kind))
      .map((kind) => unsupported.includes(kind) ? `${kind} is unsupported and was not measured` : `${kind} is not measured for ${this.operation}`);
    const capabilityProfile = {
      adapter: 'typescript', adapterVersion: ADAPTER_VERSION, analyzerRevision: ANALYZER_VERSION,
      features: ['symbols', 'calls', 'snapshot_overlay', 'negative_lookup', 'membership', 'dependency_frontier'],
      unsupported, protocolVersions: [PROTOCOL_VERSION],
      coverageBoundary: { includedSourceRoots: [...this.coverageRoots].sort(), excludedReasons: [], measured: true },
    };
    const boundedDiagnostics = Array.isArray(diagnostics) ? diagnostics.slice(0, 64).map((item) => ({
      severity: ['info', 'warning', 'error'].includes(item.severity) ? item.severity : 'warning',
      message: redactDiagnostic(String(item.message || 'adapter diagnostic'), 512),
      ...(item.path ? { path: redactDiagnostic(String(item.path), 256) } : {}),
    })) : [];
    const analysisReadSet = {
      schemaId: READ_SET_SCHEMA_ID, schemaVersion: 1, readSetId, computedBasisId: basis, workspaceEpoch,
      documents: documentMetadata, indexes: [], negativeObservations, membershipObservations, dependencyFrontiers,
      requiredObservations, adapterVersions: { typescript: ADAPTER_VERSION },
    };
    const causalObservationClosure = {
      schemaId: CLOSURE_SCHEMA_ID, schemaVersion: 1, closureId, analysisReadSetId: readSetId,
      computedBasisId: basis, workspaceEpoch,
      closureStatus: incompleteReasons.length > 0 ? 'open' : 'closed',
      negativeObservations, membershipObservations, dependencyFrontiers,
      requiredObservations, measuredObservations, capabilityProfile,
      coverageBoundary: capabilityProfile.coverageBoundary, incompleteReasons,
      closureDigest: sha256(Buffer.from(JSON.stringify({ analysisReadSet, capabilityProfile }), 'utf8')),
    };
    const snapshotIdentity = {};
    for (const field of ['snapshotId', 'rootTreeId', 'dependencyFingerprint', 'configurationFingerprint']) {
      if (typeof snapshot[field] === 'string' && snapshot[field]) snapshotIdentity[field] = snapshot[field];
    }
    return {
      schemaId: SCHEMA_ID, schemaVersion: 1, operation: this.operation, computedBasisId: basis, workspaceEpoch,
      analysisReadSet, causalObservationClosure, capabilityProfile, analyzerVersion: ANALYZER_VERSION,
      diagnostics: boundedDiagnostics, ...snapshotIdentity,
    };
  }
}

function createAnalysisTracker(params, operation) {
  return new AnalysisTracker(params, operation);
}

function analysisMetadata(params, operation, explicitPaths = [], diagnostics = [], tracker = null) {
  const active = tracker || createAnalysisTracker(params, operation);
  if (!tracker) {
    for (const rel of explicitPaths) active.read(rel);
  }
  return active.metadata(diagnostics);
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
  AnalysisTracker,
  createAnalysisTracker,
};
