import { defineConfig, devices } from '@playwright/test';
import path from 'path';

const repoRoot = path.resolve(__dirname, '../..');
const adapterBin = 'noderun:' + path.join(repoRoot, 'adapters/typescript');

export default defineConfig({
  testDir: './tests',
  timeout: 30000,
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
      },
    },
  ],
  webServer: {
    command: 'go run ./cmd/codeflow view test/fixtures/nextjs-app-fixture --port 4589 --token testtoken',
    cwd: repoRoot,
    url: 'http://127.0.0.1:4589/?token=testtoken',
    env: {
      CODEFLOW_ADAPTER_TYPESCRIPT_BIN: adapterBin,
    },
    reuseExistingServer: !process.env.CI,
    timeout: 30000,
  },
});
