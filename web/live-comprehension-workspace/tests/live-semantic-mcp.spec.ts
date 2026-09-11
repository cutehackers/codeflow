import { test, expect } from '@playwright/test';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, cp, readFile, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { createInterface } from 'node:readline';

// Real MCP, adapter, publication and SSE. No injected results or browser events.
test('MCP Live template follows real edits and defers updates while reading', async ({ page }) => {
  test.setTimeout(120000);
  const repo = path.resolve(__dirname, '../../..');
  const temp = await mkdtemp(path.join(tmpdir(), 'codeflow-live-browser-'));
  const target = path.join(temp, 'project');
  let child: ReturnType<typeof spawn> | undefined;
  try {
    await cp(path.join(repo, 'test/fixtures/nextjs-app-fixture'), target, {
      recursive: true, filter: source => !['.codeflow', 'node_modules'].includes(path.basename(source)),
    });
    await writeFile(path.join(target, 'jsconfig.json'), JSON.stringify({ compilerOptions: { baseUrl: '.' } }));
    const binary = process.env.CODEFLOW_LIVE_TEST_BINARY || path.join(temp, 'codeflow');
    if (!process.env.CODEFLOW_LIVE_TEST_BINARY) {
      await promisify(execFile)('go', ['build', '-o', binary, './cmd/codeflow'], { cwd: repo });
    }
    child = spawn(binary, ['mcp'], { cwd: target, env: { ...process.env,
      CODEFLOW_ADAPTER_TYPESCRIPT_BIN: process.env.CODEFLOW_LIVE_TEST_TYPESCRIPT_ADAPTER || 'noderun:' + path.join(repo, 'adapters/typescript') }, stdio: 'pipe' });
    let serial = 0;
    const waiting = new Map<number, (value: any) => void>();
    createInterface({ input: child.stdout! }).on('line', line => {
      const message = JSON.parse(line);
      waiting.get(message.id)?.(message);
      waiting.delete(message.id);
    });
    const call = async (name: string, args: object) => {
      const id = ++serial;
      const result = new Promise<any>((resolve, reject) => {
        const timer = setTimeout(() => { waiting.delete(id); reject(Error('MCP timeout: ' + name)); }, 30000);
        waiting.set(id, value => { clearTimeout(timer); resolve(value); });
      });
      child!.stdin!.write(JSON.stringify({ jsonrpc: '2.0', id, method: 'tools/call', params: { name, arguments: { target, ...args } } }) + '\n');
      const response = await result;
      expect(response.error).toBeUndefined();
      expect(response.result.isError, JSON.stringify(response.result)).not.toBe(true);
      return JSON.parse(response.result.content[0].text);
    };
    const view = await call('query_task_view', { query: {
      schemaId: 'https://codeflow.local/schemas/task-view-query.schema.json', schemaVersion: 1,
      mode: 'feature', feature: { entrySymbol: 'app/page.tsx#HomePage.handleQuickCheckout' },
    } });
    expect(view.flowView.template).toBe('live-semantic-map-prototype.html');
    // VS-06-A02: query_task_view returns static FlowView URL on '/'
    const staticUrl = new URL(view.flowView.url);
    expect(staticUrl.pathname).toBe('/');
    await page.goto(view.flowView.url);
    await expect(page.locator('.brand-eyebrow')).toHaveText('CODEFLOW · FLOWVIEW');

    // VS-06-A05: coordinator /live opens project_change mode
    const liveUrl = new URL('/live', view.flowView.url);
    liveUrl.search = staticUrl.search;
    await page.goto(liveUrl.toString());
    await page.waitForFunction(() => (window as any).eval('state.stream.readyState') === 1);
    await page.evaluate(() => {
      (window as any).liveEvents = [];
      for (const type of ['generation.gap', 'generation.published', 'activity.updated']) {
        (window as any).eval('state.stream').addEventListener(type, (event: MessageEvent) => (window as any).liveEvents.push({ type, snapshot: JSON.parse(event.data).validatedAgainstSnapshotId, causes: JSON.parse(event.data).data?.intersectedCauses }));
      }
    });
    const sourcePath = path.join(target, 'app/page.tsx');
    const original = await readFile(sourcePath, 'utf8');
    const submit = async (version: number, label: string) => {
      const content = original.replace("setStatus('processing')", "setStatus('" + label + "')");
      await writeFile(sourcePath, content);
      return call('submit_versioned_edit', { path: 'app/page.tsx', content, documentVersion: version, source: 'agent_transaction' });
    };
    // First edit arrives in watching project and renders the flow
    const first = await submit(2, 'live-first-edit');
    await expect(page.locator('#code-flow')).toContainText('live-first-edit', { timeout: 30000 });
    expect(await page.evaluate(() => (window as any).eval('state.data.semanticMap.validatedAgainstSnapshotId'))).toBe(first.snapshot.snapshotId);
    // Pause reading to defer subsequent updates
    await page.locator('#pause').click();
    await submit(3, 'live-second-edit');
    await expect(page.locator('#apply')).toBeVisible({ timeout: 10000 }).catch(async error => {
      console.error('Paused events:', JSON.stringify(await page.evaluate(() => (window as any).liveEvents)));
      console.error('Notice:', await page.locator('#live-notice').textContent());
      throw error;
    });
    await expect(page.locator('#code-flow')).toContainText('live-first-edit');
    await expect(page.locator('#code-flow')).not.toContainText('live-second-edit');
    await page.locator('#apply').click();
    await expect(page.locator('#code-flow')).toContainText('live-second-edit');
    const isPaused = await page.evaluate(() => (window as any).eval('state.paused'));
    if (!isPaused) {
      await page.locator('#pause').click();
    }
    await submit(4, 'live-third-edit');
    await expect(page.locator('#apply')).toBeVisible({ timeout: 30000 });
    await page.locator('#apply').click();
    await expect(page.locator('#code-flow')).toContainText('live-third-edit', { timeout: 30000 });
    await expect(page.locator('#code-flow')).not.toContainText('live-second-edit');
    // An invalid resolution config must produce a real gap and keep prior code.
    const config = path.join(target, 'jsconfig.json');
    await writeFile(config, 'invalid config');
    await call('submit_versioned_edit', { path: 'jsconfig.json', content: 'invalid config', documentVersion: 2, source: 'agent_transaction' });
    await submit(5, 'live-fourth-edit');
    await expect(page.locator('#live-notice')).toContainText('최신 변경을 확인하지 못했습니다', { timeout: 15000 });
    await expect(page.locator('#code-flow')).toContainText('live-third-edit');
    await expect(page.locator('#code-flow')).not.toContainText('live-fourth-edit');
    const restoredConfig = JSON.stringify({ compilerOptions: { baseUrl: '.' } });
    await writeFile(config, restoredConfig);
    await call('submit_versioned_edit', { path: 'jsconfig.json', content: restoredConfig, documentVersion: 3, source: 'agent_transaction' });
    await expect(page.locator('#apply')).toBeVisible({ timeout: 15000 });
    await page.locator('#apply').click();
    await expect(page.locator('#code-flow')).toContainText('live-fourth-edit', { timeout: 15000 });
  } finally {
    await page.close();
    if (child && child.exitCode === null) {
      const exited = new Promise<void>(resolve => child!.once('exit', () => resolve()));
      child.kill('SIGTERM');
      await exited;
    }
    await rm(temp, { recursive: true, force: true });
  }
});
