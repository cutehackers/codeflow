#!/usr/bin/env node
'use strict';

const { handleRequest, handleRPCRequest, CAPABILITIES } = require('../lib/protocol');

const MAX_MESSAGE_BYTES = CAPABILITIES.maxMessageBytes;
const MAX_HEADER_BYTES = 8 * 1024;
let input = Buffer.alloc(0);
let mode = null;
let active = 0;
const cancelled = new Set();
let shuttingDown = false;

function writeBody(body) {
  const bytes = Buffer.isBuffer(body) ? body : Buffer.from(JSON.stringify(body), 'utf8');
  process.stdout.write(`Content-Length: ${bytes.length}\r\n\r\n`);
  process.stdout.write(bytes);
}

function writeLegacy(value) {
  process.stdout.write(`${JSON.stringify(value)}\n`);
}

function rpcError(id, code, message, retryable = false) {
  const rpcCode = code === 'E_BAD_REQUEST' || code === 'E_UNSUPPORTED_VERSION' ? -32602 : -32000;
  return {
    jsonrpc: '2.0',
    id: typeof id === 'string' ? id : '',
    error: { code: rpcCode, message: String(message).slice(0, 512), data: { code, retryable } },
  };
}

function writeNotification(method, params) {
  writeBody({ jsonrpc: '2.0', method, params });
}

function dispatchRPC(req) {
  const id = req && typeof req.id === 'string' ? req.id : '';
  if (req && req.method === '$/cancelRequest') {
    const target = req.params && req.params.id;
    if (typeof target === 'string') cancelled.add(target);
    return null;
  }
  if (req && req.params && typeof req.params.batchId === 'string') {
    writeNotification('codeflow/batchAck', { batchId: req.params.batchId, acknowledged: true });
  }
  const response = handleRPCRequest(req);
  if (response && response.result && req && req.method !== 'initialize' && req.method !== 'ping') {
    writeNotification('$/progress', { id, stage: 'complete' });
  }
  return response;
}

function processRPCBody(body) {
  let req;
  try {
    req = JSON.parse(body.toString('utf8'));
  } catch (err) {
    writeBody(rpcError('', 'E_BAD_REQUEST', `request body is not valid JSON: ${err.message}`));
    return;
  }
  if (active >= CAPABILITIES.maxInFlight) {
    writeBody(rpcError(req && req.id, 'E_BACKPRESSURE', 'adapter in-flight bound exceeded', true));
    return;
  }
  active += 1;
  setImmediate(() => {
    try {
      if (req && req.method === '$/cancelRequest') {
        dispatchRPC(req);
        return;
      }
      const response = dispatchRPC(req);
      if (!response) return;
      const id = req && req.id;
      if (typeof id === 'string' && cancelled.has(id)) {
        writeBody(rpcError(id, 'E_CANCELLED', 'request cancelled'));
        cancelled.delete(id);
      } else {
        writeBody(response);
      }
      if (req && req.method === 'shutdown') {
        shuttingDown = true;
        setImmediate(() => process.exit(0));
      }
    } finally {
      active -= 1;
    }
  });
}

function processLegacyLine(line) {
  const trimmed = line.trim();
  if (!trimmed) return;
  let req;
  try {
    req = JSON.parse(trimmed);
  } catch (err) {
    writeLegacy({ id: '', ok: false, err: { code: 'E_BAD_REQUEST', message: `request line is not valid JSON: ${err.message}`, retryable: false } });
    return;
  }
  const response = handleRequest(req);
  writeLegacy(response);
  if (req && req.op === 'shutdown') process.exit(0);
}

function chooseMode() {
  if (mode) return mode;
  const text = input.toString('utf8');
  if (/^\s*Content-Length\s*:/i.test(text)) mode = 'framed';
  else if (input.includes(0x0a)) mode = 'legacy';
  return mode;
}

function consumeLegacy() {
  let newline;
  while ((newline = input.indexOf(0x0a)) >= 0) {
    const line = input.subarray(0, newline).toString('utf8');
    input = input.subarray(newline + 1);
    processLegacyLine(line);
  }
}

function consumeFramed() {
  while (true) {
    const headerEnd = input.indexOf(Buffer.from('\r\n\r\n'));
    if (headerEnd < 0) {
      if (input.length > MAX_HEADER_BYTES) {
        input = Buffer.alloc(0);
        writeBody(rpcError('', 'E_BAD_REQUEST', 'frame header exceeds bound'));
      }
      return;
    }
    if (headerEnd > MAX_HEADER_BYTES) {
      input = input.subarray(headerEnd + 4);
      writeBody(rpcError('', 'E_BAD_REQUEST', 'frame header exceeds bound'));
      continue;
    }
    const header = input.subarray(0, headerEnd).toString('ascii');
    const match = header.match(/(?:^|\r\n)Content-Length\s*:\s*(\d+)\s*(?:\r\n|$)/i);
    if (!match) {
      input = input.subarray(headerEnd + 4);
      writeBody(rpcError('', 'E_BAD_REQUEST', 'frame is missing Content-Length'));
      continue;
    }
    const length = Number(match[1]);
    if (!Number.isSafeInteger(length) || length < 0) {
      input = input.subarray(headerEnd + 4);
      writeBody(rpcError('', 'E_BAD_REQUEST', 'invalid Content-Length'));
      continue;
    }
    if (length > MAX_MESSAGE_BYTES) {
      const frameEnd = headerEnd + 4 + length;
      if (input.length < frameEnd) return;
      input = input.subarray(frameEnd);
      writeBody(rpcError('', 'E_BAD_REQUEST', 'message exceeds maxMessageBytes'));
      continue;
    }
    const frameEnd = headerEnd + 4 + length;
    if (input.length < frameEnd) return;
    const body = input.subarray(headerEnd + 4, frameEnd);
    input = input.subarray(frameEnd);
    processRPCBody(body);
  }
}

process.stdin.on('data', (chunk) => {
  input = Buffer.concat([input, chunk]);
  const selected = chooseMode();
  if (selected === 'legacy') consumeLegacy();
  if (selected === 'framed') consumeFramed();
});

process.stdin.on('end', () => {
  if (mode === 'legacy') consumeLegacy();
  if (mode === 'framed' && input.length !== 0 && !shuttingDown) {
    writeBody(rpcError('', 'E_BAD_REQUEST', 'incomplete Content-Length frame'));
  }
});
