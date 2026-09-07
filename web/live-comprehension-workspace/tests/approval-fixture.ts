import { test as base, expect, Page } from '@playwright/test';
import { spawn } from 'child_process';
import path from 'path';

type ApprovalFixture = {
  url: string;
  controlUrl: string;
  token: string;
  payload: any;
  open(page: Page): Promise<void>;
  control(action: 'restart' | 'advance'): Promise<void>;
};

export const test = base.extend<{ approvalFixture: ApprovalFixture }>({
  approvalFixture: async ({ request }, use) => {
    const process = spawn('go', ['test', './internal/flowview', '-run', '^TestApprovalBrowserFixture$', '-count=1', '-v'], {
      cwd: path.resolve(__dirname, '../../..'),
      env: { ...global.process.env, CODEFLOW_APPROVAL_BROWSER_FIXTURE: '1' },
    });
    let output = '';
    const exited = new Promise<number | null>(resolve => process.on('exit', resolve));
    const fixture = await new Promise<ApprovalFixture>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error('Approval fixture startup timed out\n' + output)), 30000);
      process.stdout.on('data', data => {
        output += data.toString();
        const match = output.match(/APPROVAL_BROWSER_FIXTURE_JSON:(.*)\n/);
        if (match) { clearTimeout(timer); resolve(JSON.parse(match[1])); }
      });
      process.stderr.on('data', data => output += data.toString());
      process.on('error', error => { clearTimeout(timer); reject(error); });
      process.on('exit', code => { clearTimeout(timer); reject(new Error('Approval fixture exited ' + code + '\n' + output)); });
    });
    fixture.open = async page => {
      await page.goto(fixture.url);
      // Actual stored proposal and Q3 dependency artifacts enter the shipped
      // renderer. This fixture does not claim full semantic compiler coverage.
      await page.evaluate(payload => (window as any).renderSemanticTaskView(payload), fixture.payload);
      await expect(page.locator('#approval-history-status')).not.toHaveText('Loading durable history');
      await expect(page.locator('#badge-connection')).toHaveText('SSE: connected');
    };
    fixture.control = async action => {
      const response = await request.post(fixture.controlUrl + '/' + action, { headers: { 'X-CodeFlow-Token': fixture.token } });
      expect(response.status()).toBe(204);
    };
    try { await use(fixture); }
    finally {
      await request.post(fixture.controlUrl + '/stop', { headers: { 'X-CodeFlow-Token': fixture.token } });
      expect(await exited, output).toBe(0);
    }
  },
});

export { expect };
