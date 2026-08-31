'use strict';

const assert = require('assert');
const path = require('path');
const { handleRequest } = require('../lib/protocol');

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

  console.log('✓ All Protocol Wire Framing & Envelopes tests passed.');
}

module.exports = { run };

if (require.main === module) {
  run();
}
