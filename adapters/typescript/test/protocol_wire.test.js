'use strict';

const assert = require('assert');
const childProcess = require('child_process');
const crypto = require('crypto');
const path = require('path');
const {
  handleRequest,
  handleRPCRequest,
  ANALYZER_REQUEST_SCHEMA_ID,
  CAPABILITIES,
  encodeBoundedResponse,
} = require('../lib/protocol');

function digest(value) {
  return crypto.createHash('sha256').update(value).digest('hex');
}

function snapshotFor(files) {
  const documents = Object.entries(files).sort(([a], [b]) => a.localeCompare(b)).map(([file, content]) => {
    const hash = digest(Buffer.from(content, 'utf8'));
    return {
      path: file,
      documentRevisionId: `rev-${hash}`,
      contentId: hash,
      documentVersion: 1,
      contentHash: hash,
      byteLength: Buffer.byteLength(content, 'utf8'),
    };
  });
  const rootTreeId = digest(JSON.stringify(documents));
  return {
    schemaId: ANALYZER_REQUEST_SCHEMA_ID,
    schemaVersion: 2,
    snapshotId: `snapshot-${rootTreeId.slice(0, 12)}`,
    computedBasisId: `basis-${rootTreeId.slice(0, 12)}`,
    workspaceEpoch: 7,
    rootTreeId,
    dependencyFingerprint: `deps-${rootTreeId.slice(0, 12)}`,
    documents,
    files,
    repositoryPathWriteAudit: {
      codeflowWriteCount: 0,
      sourceIntegrityViolation: false,
      capturedSnapshotTreeDigest: rootTreeId,
    },
  };
}

function rpcAnalysis(id, method, files, payload = {}, requiredObservations = []) {
  const snapshot = snapshotFor(files);
  return handleRPCRequest({
    jsonrpc: '2.0',
    id,
    method,
    params: {
      schemaId: ANALYZER_REQUEST_SCHEMA_ID,
      schemaVersion: 2,
      requestId: id,
      operation: method,
      requiredObservations,
      snapshot,
      payload,
    },
  });
}

function run() {
  console.log('--- Test Suite: Protocol Wire Framing & Envelopes ---');

  // 1. Ping
  const ping = handleRequest({ v: 1, id: 'req-1', op: 'ping', params: {} });
  assert.strictEqual(ping.id, 'req-1');
  assert.strictEqual(ping.ok, true);
  assert.strictEqual(ping.result.protocolVersion, 1);
  assert.strictEqual(ping.result.adapterVersion, '0.1.0');

  // 2. Unsupported protocol version
  const badVer = handleRequest({ v: 2, id: 'req-2', op: 'ping', params: {} });
  assert.strictEqual(badVer.id, 'req-2');
  assert.strictEqual(badVer.ok, false);
  assert.strictEqual(badVer.err.code, 'E_UNSUPPORTED_VERSION');

  // 3. Unknown operation
  const badOp = handleRequest({ v: 1, id: 'req-3', op: 'invalid_op', params: {} });
  assert.strictEqual(badOp.id, 'req-3');
  assert.strictEqual(badOp.ok, false);
  assert.strictEqual(badOp.err.code, 'E_BAD_REQUEST');

  // 4. Missing required parameters
  const badDetect = handleRequest({ v: 1, id: 'req-4', op: 'detect', params: {} });
  assert.strictEqual(badDetect.ok, false);
  assert.strictEqual(badDetect.err.code, 'E_BAD_REQUEST');

  // 5. Detect repo
  const rootDir = path.resolve(__dirname, '..');
  const detect = handleRequest({ v: 1, id: 'req-5', op: 'detect', params: { repoRoot: rootDir } });
  assert.strictEqual(detect.ok, true);
  assert.strictEqual(detect.result.matched, true);
  assert.strictEqual(detect.result.language, 'typescript');

  // 6. Shutdown
  const shutdown = handleRequest({ v: 1, id: 'req-6', op: 'shutdown', params: {} });
  assert.strictEqual(shutdown.ok, true);
  assert.strictEqual(shutdown.result.acknowledged, true);

  // 7. Non-object request handling
  const nullReq = handleRequest(null);
  assert.strictEqual(nullReq.ok, false);
  assert.strictEqual(nullReq.err.code, 'E_BAD_REQUEST');

  // 8. Production framed entrypoint rejects an oversized body without
  // retaining an unbounded input buffer.
  const oversized = Buffer.alloc(1024 * 1024 + 1, 0x78);
  const frame = Buffer.concat([
    Buffer.from(`Content-Length: ${oversized.length}\r\n\r\n`, 'ascii'),
    oversized,
  ]);
  const child = childProcess.spawnSync(process.execPath, [
    path.resolve(__dirname, '../bin/codeflow_ts_adapter.js'),
  ], { input: frame, encoding: 'utf8', maxBuffer: 2 * 1024 * 1024 });
  assert.strictEqual(child.status, 0);
  assert.match(child.stdout, /negotiated bound|maxMessageBytes/);

  // 8b. The production response writer accepts the exact negotiated bound
  // and converts an oversized response into one bounded typed error.
  const responseLimit = CAPABILITIES.maxMessageBytes;
  const exactResponse = {
    jsonrpc: '2.0', id: 'bound-1', result: { padding: '' },
  };
  const emptyPaddingLength = encodeBoundedResponse(exactResponse, responseLimit).length;
  exactResponse.result.padding = 'x'.repeat(responseLimit - emptyPaddingLength);
  const exactBytes = encodeBoundedResponse(exactResponse, responseLimit);
  assert.strictEqual(exactBytes.length, responseLimit);
  const oversizedResponse = {
    jsonrpc: '2.0', id: 'bound-2',
    result: { padding: 'x'.repeat(responseLimit + 1) },
  };
  const fallbackBytes = encodeBoundedResponse(oversizedResponse, responseLimit);
  assert(fallbackBytes && fallbackBytes.length <= responseLimit);
  const fallback = JSON.parse(fallbackBytes.toString('utf8'));
  assert(fallback.error && !fallback.result);
  assert.strictEqual(fallback.error.data.code, 'E_ADAPTER_INTERNAL');
  const hugeIDFallback = encodeBoundedResponse({
    jsonrpc: '2.0', id: 'i'.repeat(512), result: { padding: 'x'.repeat(512) },
  }, 256);
  assert(hugeIDFallback && hugeIDFallback.length <= 256);
  const hugeIDDecoded = JSON.parse(hugeIDFallback.toString('utf8'));
  assert.strictEqual(hugeIDDecoded.id, '');
  assert(hugeIDDecoded.error);
  const productionHugeIDFallback = encodeBoundedResponse({
    jsonrpc: '2.0', id: 'i'.repeat(responseLimit - 64), result: { padding: 'x'.repeat(256) },
  }, responseLimit);
  assert(productionHugeIDFallback && productionHugeIDFallback.length <= responseLimit);
  assert.strictEqual(JSON.parse(productionHugeIDFallback.toString('utf8')).id, '');
  assert.strictEqual(encodeBoundedResponse({ id: 'x', result: 'x'.repeat(256) }, 64), null);

  // 9. Production v2 tracking records only operation-specific observations.
  const v2Detect = rpcAnalysis('v2-detect', 'detect', {
    'package.json': '{"name":"tracked-app","dependencies":{"react":"1"}}',
    'src/app.ts': 'export function run() { return 1; }',
  }, {}, ['negative_lookup', 'membership', 'dependency_frontier']);
  assert.strictEqual(v2Detect.error, undefined);
  assert.strictEqual(v2Detect.result.requestId, 'v2-detect');
  assert.deepStrictEqual(
    v2Detect.result.analysisReadSet.documents.map((doc) => doc.path),
    ['package.json'],
  );
  assert.deepStrictEqual(
    v2Detect.result.analysisReadSet.negativeObservations.map((item) => item.path),
    ['tsconfig.json'],
  );
  assert.deepStrictEqual(v2Detect.result.analysisReadSet.membershipObservations, []);
  assert.deepStrictEqual(
    v2Detect.result.analysisReadSet.dependencyFrontiers.map((item) => item.path),
    ['package.json'],
  );
  assert.strictEqual(v2Detect.result.causalObservationClosure.closureStatus, 'open');
  assert(v2Detect.result.causalObservationClosure.incompleteReasons.some((reason) => reason.includes('membership')));

  const harvestFiles = {
    'package.json': '{"name":"tracked-app"}',
    'src/app.ts': 'export function run() { return 1; }',
    'README.md': 'must not be read by harvest',
  };
  const harvest = rpcAnalysis('v2-harvest', 'harvest_candidates', harvestFiles, {}, ['negative_lookup']);
  assert.strictEqual(harvest.error, undefined);
  assert.deepStrictEqual(
    harvest.result.analysisReadSet.documents.map((doc) => doc.path),
    ['package.json', 'src/app.ts'],
  );
  assert.deepStrictEqual(harvest.result.analysisReadSet.negativeObservations, []);
  assert.strictEqual(harvest.result.analysisReadSet.membershipObservations.length, 1);
  assert.deepStrictEqual(
    harvest.result.analysisReadSet.dependencyFrontiers.map((item) => item.path),
    ['package.json'],
  );
  assert.strictEqual(harvest.result.causalObservationClosure.closureStatus, 'open');

  const harvestWithExtra = rpcAnalysis('v2-harvest-extra', 'harvest_candidates', {
    ...harvestFiles,
    'src/extra.ts': 'export const extra = 2;',
  });
  assert.notStrictEqual(
    harvest.result.analysisReadSet.membershipObservations[0].valueHash,
    harvestWithExtra.result.analysisReadSet.membershipObservations[0].valueHash,
  );

  const slice = rpcAnalysis('v2-slice', 'slice', {
    'package.json': '{"name":"tracked-app"}',
    'tsconfig.json': '{"compilerOptions":{"baseUrl":"."}}',
    'src/app.ts': "import { service } from './service'; export function run() { return service(); }",
    'src/service.ts': 'export function service() { return 1; }',
    'README.md': 'must not be read by slice',
  }, {
    candidateId: 'candidate-v2',
    entrySymbolPath: 'src/app.ts#run',
  }, ['membership']);
  assert.strictEqual(slice.error, undefined);
  assert.deepStrictEqual(
    slice.result.analysisReadSet.documents.map((doc) => doc.path),
    ['src/app.ts', 'tsconfig.json'],
  );
  assert.deepStrictEqual(
    slice.result.analysisReadSet.dependencyFrontiers.map((item) => item.path),
    ['tsconfig.json'],
  );
  assert.strictEqual(slice.result.causalObservationClosure.closureStatus, 'open');
  assert(slice.result.causalObservationClosure.incompleteReasons.some((reason) => reason.includes('membership')));

  const unsupported = rpcAnalysis('v2-unsupported', 'harvest_candidates', harvestFiles, {}, ['runtime_observation']);
  assert.strictEqual(unsupported.result.causalObservationClosure.closureStatus, 'open');
  assert(unsupported.result.causalObservationClosure.incompleteReasons.some((reason) => reason.includes('runtime_observation')));

  console.log('✓ All Protocol Wire Framing & Envelopes tests passed.');
}

module.exports = { run };

if (require.main === module) {
  run();
}
