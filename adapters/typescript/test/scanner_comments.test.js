'use strict';

const assert = require('assert');
const { scanSource } = require('../lib/scanner');
const { canonicalAstFingerprint } = require('../lib/sha256');

function run() {
  console.log('--- Test Suite: Scanner Comments & URL String Preservation ---');

  // TS-COM-01: Opening brace in single-line comment
  const code1 = 'function testComment1() {\n  // {\n  return 1;\n}';
  const scan1 = scanSource(code1);
  assert.strictEqual(scan1.topLevelFunctions.length, 1);
  assert.strictEqual(scan1.topLevelFunctions[0].name, 'testComment1');
  assert.strictEqual(code1[scan1.topLevelFunctions[0].bodyEnd], '}');

  // TS-COM-02: Closing brace in single-line comment
  const code2 = 'function testComment2() {\n  // }\n  return 2;\n}';
  const scan2 = scanSource(code2);
  assert.strictEqual(scan2.topLevelFunctions.length, 1);
  assert.strictEqual(scan2.topLevelFunctions[0].name, 'testComment2');
  assert.strictEqual(code2[scan2.topLevelFunctions[0].bodyEnd], '}');

  // TS-COM-03: Multi-line block comment with unbalanced braces
  const code3 = 'function testComment3() {\n  /*\n   * }\n   * {\n   */\n  return 3;\n}';
  const scan3 = scanSource(code3);
  assert.strictEqual(scan3.topLevelFunctions.length, 1);
  assert.strictEqual(scan3.topLevelFunctions[0].name, 'testComment3');
  assert.strictEqual(code3[scan3.topLevelFunctions[0].bodyEnd], '}');

  // TS-COM-04: JSDoc type braces inside block comments
  const code4 = 'function testComment4() {\n  /** @param {string} x */\n  return 4;\n}';
  const scan4 = scanSource(code4);
  assert.strictEqual(scan4.topLevelFunctions.length, 1);
  assert.strictEqual(scan4.topLevelFunctions[0].name, 'testComment4');
  assert.strictEqual(code4[scan4.topLevelFunctions[0].bodyEnd], '}');

  // TS-COM-05: Comment markers inside string literals
  const code5 = 'function testComment5() {\n  const url = "http://test.com/* not comment */";\n  return url;\n}';
  const scan5 = scanSource(code5);
  assert.strictEqual(scan5.topLevelFunctions.length, 1);
  assert.strictEqual(scan5.topLevelFunctions[0].name, 'testComment5');
  assert.strictEqual(code5[scan5.topLevelFunctions[0].bodyEnd], '}');

  // AST Fingerprint Comment Stripping & String Invariance
  const astWithComments = `
function handleComments() {
  // Line comment with { braces }
  /* Block comment with {
     more braces
  } */
  /** @type {string} */
  return "https://example.com/api/v1";
}
`;
  const astWithoutComments = `
function handleComments() {
  return "https://example.com/api/v1";
}
`;
  assert.strictEqual(
    canonicalAstFingerprint(astWithComments),
    canonicalAstFingerprint(astWithoutComments),
    'AST fingerprint must be invariant to comments'
  );

  // URL String Distinction: Two distinct URLs MUST produce different hashes
  const urlCodeA = 'const api = "https://example.com/endpointA";';
  const urlCodeB = 'const api = "https://example.com/endpointB";';
  assert.notStrictEqual(
    canonicalAstFingerprint(urlCodeA),
    canonicalAstFingerprint(urlCodeB),
    'Distinct URLs must not have colliding AST fingerprints'
  );

  console.log('✓ All Scanner Comments & URL String Preservation tests passed.');
}

module.exports = { run };

if (require.main === module) {
  run();
}
