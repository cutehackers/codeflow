'use strict';

const assert = require('assert');
const { findMatchingBrace, scanSource, countPrecedingBackslashes } = require('../lib/scanner');

function run() {
  console.log('--- Test Suite: Scanner Escaped Quotes & Strings ---');

  // TS-ESC-01: Double backslash before closing double quote
  const code1 = 'function test1() {\n  const s = "\\\\";\n  return 1;\n}';
  const scan1 = scanSource(code1);
  assert.strictEqual(scan1.topLevelFunctions.length, 1, 'Should find 1 top level function');
  assert.strictEqual(scan1.topLevelFunctions[0].name, 'test1');
  assert.strictEqual(code1[scan1.topLevelFunctions[0].bodyEnd], '}', 'Should find closing brace of test1');

  // TS-ESC-02: Double backslash before closing single quote
  const code2 = "function test2() {\n  const s = '\\\\';\n  return 2;\n}";
  const scan2 = scanSource(code2);
  assert.strictEqual(scan2.topLevelFunctions.length, 1);
  assert.strictEqual(scan2.topLevelFunctions[0].name, 'test2');
  assert.strictEqual(code2[scan2.topLevelFunctions[0].bodyEnd], '}');

  // TS-ESC-03: Triple backslash followed by quote (escaped quote after escaped backslash)
  const code3 = 'function test3() {\n  const s = "\\\\\\\"";\n  return 3;\n}';
  const scan3 = scanSource(code3);
  assert.strictEqual(scan3.topLevelFunctions.length, 1);
  assert.strictEqual(scan3.topLevelFunctions[0].name, 'test3');
  assert.strictEqual(code3[scan3.topLevelFunctions[0].bodyEnd], '}');

  // TS-ESC-04: Template literal with escaped backticks and escaped backslashes
  const code4 = 'function test4() {\n  const s = `hello \\` world \\\\`;\n  return 4;\n}';
  const scan4 = scanSource(code4);
  assert.strictEqual(scan4.topLevelFunctions.length, 1);
  assert.strictEqual(scan4.topLevelFunctions[0].name, 'test4');
  assert.strictEqual(code4[scan4.topLevelFunctions[0].bodyEnd], '}');

  // TS-ESC-05: Braces inside string literals
  const code5 = 'function test5() {\n  const a = "}"; const b = "{";\n  return 5;\n}';
  const scan5 = scanSource(code5);
  assert.strictEqual(scan5.topLevelFunctions.length, 1);
  assert.strictEqual(scan5.topLevelFunctions[0].name, 'test5');
  assert.strictEqual(code5[scan5.topLevelFunctions[0].bodyEnd], '}');

  // TS-ESC-06: Template literal with embedded expression containing nested object braces
  const code6 = 'function test6() {\n  const s = `val: ${ { a: "}" }.a }`;\n  return 6;\n}';
  const scan6 = scanSource(code6);
  assert.strictEqual(scan6.topLevelFunctions.length, 1);
  assert.strictEqual(scan6.topLevelFunctions[0].name, 'test6');
  assert.strictEqual(code6[scan6.topLevelFunctions[0].bodyEnd], '}');

  // Test backslash parity helper
  assert.strictEqual(countPrecedingBackslashes('foo\\bar', 4), 1);
  assert.strictEqual(countPrecedingBackslashes('foo\\\\bar', 5), 2);
  assert.strictEqual(countPrecedingBackslashes('foo\\\\\\bar', 6), 3);
  assert.strictEqual(countPrecedingBackslashes('foobar', 3), 0);

  console.log('✓ All Scanner Escaped Quotes & Strings tests passed.');
}

module.exports = { run };

if (require.main === module) {
  run();
}
