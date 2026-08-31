'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { redactSecrets } = require('../lib/secret');
const { sliceFlow } = require('../lib/slice');

function run() {
  console.log('--- Test Suite: Single-Gate Secret Redaction ---');

  // TS-SEC-01: apiKey variable assignment
  const s1 = 'const apiKey = "sk-live-1234567890abcdef";';
  const r1 = redactSecrets(s1);
  assert.strictEqual(r1.count, 1);
  assert(r1.text.includes('***REDACTED***'));
  assert(!r1.text.includes('sk-live-1234567890abcdef'));

  // TS-SEC-02: Object with api_key, password, token
  const s2 = 'const config = { api_key: "secret-val", password: "pass123", token: "bearer-xyz" };';
  const r2 = redactSecrets(s2);
  assert.strictEqual(r2.count, 3);
  assert(!r2.text.includes('secret-val'));
  assert(!r2.text.includes('pass123'));
  assert(!r2.text.includes('bearer-xyz'));

  // TS-SEC-03: Constructor object
  const s3 = 'const client = new Client({ API_KEY: "ABC_123", Secret: "XYZ_789" });';
  const r3 = redactSecrets(s3);
  assert.strictEqual(r3.count, 2);

  // Negative Tests: Safe domain identifiers must NEVER be redacted
  // TS-SEC-05: tokenCount, isPasswordValid
  const sNeg1 = 'const tokenCount = 42; const isPasswordValid = true;';
  const rNeg1 = redactSecrets(sNeg1);
  assert.strictEqual(rNeg1.count, 0, 'tokenCount/isPasswordValid must not be redacted');
  assert.strictEqual(rNeg1.text, sNeg1);

  // TS-SEC-06: tokenize function
  const sNeg2 = 'function tokenize(input: string) { return tokenizer.parse(input); }';
  const rNeg2 = redactSecrets(sNeg2);
  assert.strictEqual(rNeg2.count, 0, 'tokenize function must not be redacted');
  assert.strictEqual(rNeg2.text, sNeg2);

  // TS-SEC-07: user.hasSecretKey, passwordPolicy
  const sNeg3 = 'if (user.hasSecretKey()) { return passwordPolicy.minLength; }';
  const rNeg3 = redactSecrets(sNeg3);
  assert.strictEqual(rNeg3.count, 0, 'Method calls and properties must not be redacted');
  assert.strictEqual(rNeg3.text, sNeg3);

  // TS-SEC-08: Slicer integration test with embedded secrets in guards & methods
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-sec-'));
  try {
    const srcDir = path.join(tmpDir, 'src');
    fs.mkdirSync(srcDir, { recursive: true });

    fs.writeFileSync(path.join(srcDir, 'AuthService.ts'), `
export class AuthService {
  async authenticate(req: any) {
    if (req.apiKey === 'super-secret-token-12345') {
      return;
    }
    this.state = { token: 'jwt-token-abcdef' };
  }
}
`, 'utf8');

    const res = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-sec-001',
      entrySymbolPath: 'src/AuthService.ts#AuthService.authenticate',
      opts: { maxDepth: 5 },
    });

    assert(res.redactedCount > 0, 'Slice payload must record redactedCount > 0');
    const jsonStr = JSON.stringify(res);
    assert(!jsonStr.includes('super-secret-token-12345'), 'Payload must not contain raw secret token');
    assert(!jsonStr.includes('jwt-token-abcdef'), 'Payload must not contain raw state token');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }

  console.log('✓ All Single-Gate Secret Redaction tests passed.');
}

module.exports = { run };

if (require.main === module) {
  run();
}
