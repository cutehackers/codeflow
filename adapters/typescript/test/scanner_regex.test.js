'use strict';

const assert = require('assert');
const { scanSource, isRegexStart } = require('../lib/scanner');

function run() {
  console.log('--- Test Suite: Scanner Regex Literals & Braces ---');

  // TS-REG-01: Regex literal containing closing brace /}/
  const codeReg1 = `
function testReg1() {
  const r = /}/;
  return 1;
}
`;
  const scan1 = scanSource(codeReg1);
  assert.strictEqual(scan1.topLevelFunctions.length, 1);
  assert.strictEqual(scan1.topLevelFunctions[0].name, 'testReg1');
  assert.strictEqual(codeReg1[scan1.topLevelFunctions[0].bodyEnd], '}');

  // TS-REG-02: Regex containing character class /[{}]/g
  const codeReg2 = `
function testReg2() {
  const r = /[{}]/g;
  return 2;
}
`;
  const scan2 = scanSource(codeReg2);
  assert.strictEqual(scan2.topLevelFunctions.length, 1);
  assert.strictEqual(scan2.topLevelFunctions[0].name, 'testReg2');
  assert.strictEqual(codeReg2[scan2.topLevelFunctions[0].bodyEnd], '}');

  // TS-REG-03: Regex with escaped opening brace /(?:\{)/i
  const codeReg3 = `
function testReg3() {
  const r = /(?:\\{)/i;
  return 3;
}
`;
  const scan3 = scanSource(codeReg3);
  assert.strictEqual(scan3.topLevelFunctions.length, 1);
  assert.strictEqual(scan3.topLevelFunctions[0].name, 'testReg3');
  assert.strictEqual(codeReg3[scan3.topLevelFunctions[0].bodyEnd], '}');

  // TS-REG-04: Regex with escaped slash and escaped brace
  const codeReg4 = `
function testReg4() {
  const r = /\\/\\}/g;
  return 4;
}
`;
  const scan4 = scanSource(codeReg4);
  assert.strictEqual(scan4.topLevelFunctions.length, 1);
  assert.strictEqual(scan4.topLevelFunctions[0].name, 'testReg4');
  assert.strictEqual(codeReg4[scan4.topLevelFunctions[0].bodyEnd], '}');

  // TS-REG-05: Division operators / vs RegExp literals
  const codeReg5 = `
function testReg5() {
  const a = b / c / d;
  { return a; }
}
`;
  const scan5 = scanSource(codeReg5);
  assert.strictEqual(scan5.topLevelFunctions.length, 1);
  assert.strictEqual(scan5.topLevelFunctions[0].name, 'testReg5');
  assert.strictEqual(codeReg5[scan5.topLevelFunctions[0].bodyEnd], '}');

  // Combined multiple regexes in method body
  const codeClass = `
class RegexValidator {
  validate(text: string): boolean {
    const isSingleBrace = /}/.test(text);
    const hasBraces = /[{}]/g.test(text);
    const hasQuantifier = /a{2,4}/.test(text);
    return isSingleBrace || hasBraces || hasQuantifier;
  }
}
`;
  const scanClass = scanSource(codeClass);
  assert.strictEqual(scanClass.classes.length, 1);
  assert.strictEqual(scanClass.classes[0].name, 'RegexValidator');
  assert.strictEqual(scanClass.classes[0].methods.length, 1);
  assert.strictEqual(scanClass.classes[0].methods[0].name, 'validate');
  assert.strictEqual(codeClass[scanClass.classes[0].methods[0].bodyEnd], '}');

  // Test isRegexStart helper
  assert.strictEqual(isRegexStart('const x = /', 10), true);
  assert.strictEqual(isRegexStart('return /', 7), true);
  assert.strictEqual(isRegexStart('x /', 2), false);

  console.log('✓ All Scanner Regex Literals & Braces tests passed.');
}

module.exports = { run };

if (require.main === module) {
  run();
}
