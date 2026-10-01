import { defineConfig } from '@playwright/test';
import { mkdtempSync, cpSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

const repoRoot = path.resolve(__dirname, '../..');
const fixture = process.env.CODEFLOW_BROWSER_FIXTURE || mkdtempSync(path.join(tmpdir(), 'codeflow-browser-'));
process.env.CODEFLOW_BROWSER_FIXTURE = fixture;
cpSync(path.join(repoRoot, 'test/fixtures/nextjs-app-fixture'), fixture, {
  recursive: true,
  filter: source => !source.split(path.sep).includes('.codeflow')
});
writeFileSync(path.join(fixture, 'go.mod'), 'module example.com/checkout\n\ngo 1.26\n');
writeFileSync(path.join(fixture, 'pubspec.yaml'), 'name: flowview_browser_fixture\nenvironment:\n  sdk: ^3.0.0\n');
writeFileSync(path.join(fixture, 'checkout.go'), `package checkout
type Service struct{}
func (s *Service) Checkout(amount int) int {
  fee := calculateFee(amount)
  return fee
}
func (s *Service) ProcessCheckout(amount int) int { return amount + 1 }
func calculateFee(amount int) int { return amount * 2 }
`);
export default defineConfig({
  testDir: './tests',
  outputDir: mkdtempSync(path.join(tmpdir(), 'codeflow-browser-results-')),
  testMatch: 'saved-flowview.spec.ts',
  timeout: 60000,
  workers: 1,
  use: { baseURL:'http://127.0.0.1:4593', headless:true },
  webServer: {
    command: `${process.env.CODEFLOW_BROWSER_EXECUTABLE || path.join(repoRoot, 'bin/codeflow')} view ${fixture} --port 4593 --token testtoken`,
    cwd: repoRoot,
    url:'http://127.0.0.1:4593/?token=testtoken',
    env: {
      CODEFLOW_ADAPTER_TYPESCRIPT_BIN: 'noderun:'+path.join(repoRoot,'adapters/typescript'),
      CODEFLOW_ADAPTER_GO_BIN: 'gorun:'+path.join(repoRoot,'adapters/go'),
      CODEFLOW_ADAPTER_DART_BIN: 'dartrun:'+path.join(repoRoot,'adapters/dart'),
    },
    reuseExistingServer:false,
    timeout:120000
  }
});
