'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const os = require('os');
const crypto = require('crypto');
const { execFileSync } = require('child_process');

function run() {
  const adapter = path.join(__dirname, '..');
  const vendor = path.join(adapter, 'vendor', 'typescript');
  const manifest = JSON.parse(fs.readFileSync(path.join(vendor, 'manifest.json'), 'utf8'));
  assert.strictEqual(manifest.version, require('../package.json').devDependencies.typescript);
  for (const [name, expected] of Object.entries(manifest.sha256)) {
    const bytes = fs.readFileSync(path.join(vendor, name));
    assert.strictEqual(crypto.createHash('sha256').update(bytes).digest('hex'), expected, name + ' bundle integrity');
  }
  const installation = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-parser-install-'));
  try {
    // Release/install scripts copy the adapter. No npm install is required to run it.
    for (const name of ['bin', 'lib', 'vendor']) fs.cpSync(path.join(adapter, name), path.join(installation, name), { recursive: true });
    const output = execFileSync(process.execPath, ['-e', `
      const {scanSource} = require(process.argv[1]);
      const scan = scanSource('const run: () => number = () => { return 42; };');
      process.stdout.write(scan.topLevelFunctions[0].name);
    `, path.join(installation, 'lib', 'scanner.js')], { encoding: 'utf8', timeout: 10000 });
    assert.strictEqual(output, 'run');
  } finally {
    fs.rmSync(installation, { recursive: true, force: true });
  }
}

module.exports = { run };
