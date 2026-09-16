import { defineConfig } from '@playwright/test';
import { mkdtempSync, cpSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

const repoRoot = path.resolve(__dirname, '../..');
const fixture = process.env.CODEFLOW_BROWSER_FIXTURE || mkdtempSync(path.join(tmpdir(), 'codeflow-browser-'));
process.env.CODEFLOW_BROWSER_FIXTURE = fixture;
cpSync(path.join(repoRoot, 'test/fixtures/nextjs-app-fixture'), fixture, {
  recursive: true,
  filter: source => !source.split(path.sep).includes('.codeflow')
});
export default defineConfig({
  testDir: './tests',
  outputDir: mkdtempSync(path.join(tmpdir(), 'codeflow-browser-results-')),
  testMatch: 'saved-flowview.spec.ts',
  timeout: 60000,
  workers: 1,
  use: { baseURL:'http://127.0.0.1:4593', headless:true },
  webServer: {
    command: `go run ./cmd/codeflow view ${fixture} --port 4593 --token testtoken`,
    cwd: repoRoot,
    url:'http://127.0.0.1:4593/?token=testtoken',
    env: { CODEFLOW_ADAPTER_TYPESCRIPT_BIN: 'noderun:'+path.join(repoRoot,'adapters/typescript') },
    reuseExistingServer:false,
    timeout:120000
  }
});
