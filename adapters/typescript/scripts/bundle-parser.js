'use strict';

const fs = require('fs');
const path = require('path');
const crypto = require('crypto');
const root = path.dirname(require.resolve('typescript/package.json'));
const destination = path.join(__dirname, '..', 'vendor', 'typescript');
fs.mkdirSync(destination, { recursive: true });
const files = ['lib/typescript.js', 'LICENSE.txt', 'ThirdPartyNoticeText.txt'];
const hashes = {};
for (const file of files) {
  const bytes = fs.readFileSync(path.join(root, file));
  const name = path.basename(file);
  fs.writeFileSync(path.join(destination, name), bytes);
  hashes[name] = crypto.createHash('sha256').update(bytes).digest('hex');
}
fs.writeFileSync(path.join(destination, 'manifest.json'), JSON.stringify({
  package: 'typescript', version: require(path.join(root, 'package.json')).version,
  source: 'https://www.npmjs.com/package/typescript', sha256: hashes,
}, null, 2) + '\n');
