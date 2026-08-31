#!/usr/bin/env node
'use strict';

const readline = require('readline');
const { handleRequest } = require('../lib/protocol');

const rl = readline.createInterface({
  input: process.stdin,
  output: process.stdout,
  terminal: false,
});

rl.on('line', (line) => {
  const trimmed = line.trim();
  if (!trimmed) return;

  let req;
  try {
    req = JSON.parse(trimmed);
  } catch (e) {
    const errEnvelope = {
      id: '',
      ok: false,
      err: {
        code: 'E_BAD_REQUEST',
        message: `request line is not valid JSON: ${e.message}`,
        retryable: false,
      },
    };
    process.stdout.write(JSON.stringify(errEnvelope) + '\n');
    return;
  }

  const response = handleRequest(req);
  process.stdout.write(JSON.stringify(response) + '\n');

  if (req.op === 'shutdown') {
    rl.close();
    process.exit(0);
  }
});
