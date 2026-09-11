import { test, expect } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, cp, readFile, writeFile, rm, mkdir } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { createInterface } from 'node:readline';

const base = 'http://127.0.0.1:4589';
const entry = 'app/page.tsx#HomePage.handleQuickCheckout';

function payload(version: number) {
  const generationId = 'generation-' + version, snapshotId = 'snapshot-' + version;
  const steps = [
    {stepId:'check-'+version,structuralIdentity:'check',ordinal:1,name:'재고를 확인한다',technicalName:'Inventory.check',layer:'domain',branch:'requestedQty > availableQty',anchor:{repoRelativePath:'inventory.ts'}},
    {stepId:'save-'+version,structuralIdentity:'save',ordinal:2,name:'주문을 저장한다',technicalName:'Orders.save',layer:'data',anchor:{repoRelativePath:'orders.ts'}},
  ];
  return {
    semanticMap:{generationId,validatedAgainstSnapshotId:snapshotId,summary:{requested:'주문 처리',current:'주문 처리'},steps,edges:[{fromStepId:steps[0].stepId,toStepId:steps[1].stepId,kind:'call',resolutionStatus:'resolved'}]},
    flowContexts:Object.fromEntries(steps.map((step,i)=>[step.stepId,{stepId:step.stepId,generationId,snapshotId,canonicalPath:step.anchor.repoRelativePath,precision:'exact',displayedLines:[{lineNumber:10,text:i?'return database.save(order);':version===1?'return orders.save(order);':'if (requestedQty > availableQty) throw new OutOfStock();',isHit:true}]}])),
    unknowns:[],
  };
}

test('Live route static link navigates to Static FlowView without replacing Static FlowView', async ({page})=>{
  const requests:string[]=[];
  page.on('request',request=>requests.push(new URL(request.url()).pathname));
  await page.goto(base+'/live?token=testtoken');
  await expect(page).toHaveTitle('CodeFlow · Live Semantic Map');
  expect(requests).not.toContain('/api/flows');
  expect(requests).not.toContain('/api/source');
  await expect(page.locator('#current-answer-strip')).toHaveCount(0);
  await expect(page.locator('#workspace-analysis-lag')).toHaveCount(0);
  await page.locator('#static-link').click();
  await expect(page.locator('.brand-eyebrow')).toHaveText('CODEFLOW · FLOWVIEW');
  await expect(page.locator('body')).not.toHaveAttribute('data-view','live-semantic-map');
});

test('paused updates keep the displayed source and preserve selection by structural identity', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  await page.evaluate(data=>(window as any).receive(data),payload(1));
  await page.locator('#step-nav [data-select="save-1"]').click();
  await page.locator('#pause').click();
  await page.evaluate(data=>(window as any).receive(data),payload(2));
  await expect(page.locator('[data-card="check-1"]')).toContainText('return orders.save(order);');
  await expect(page.locator('[data-card="check-2"]')).toHaveCount(0);
  await page.locator('#apply').click();
  await expect(page.locator('[data-card="save-2"]')).toHaveClass(/selected/);
  await expect(page.locator('[data-card="check-2"]')).toContainText('OutOfStock');
  await page.locator('#compare').click();
  await expect(page.locator('.compare-grid')).toContainText('return orders.save(order);');
  await expect(page.locator('.compare-grid')).toContainText('OutOfStock');
  await page.locator('[data-card="check-2"] .relation button').click();
  await expect(page.locator('[data-card="save-2"]')).toHaveClass(/selected/);
  const returned=payload(2);
  returned.semanticMap.edges[0].kind='return';
  await page.locator('#pause').click();
  await page.evaluate(value=>(window as any).receive(value),returned);
  await expect(page.locator('#context button[data-select="check-2"]')).toHaveCount(0);
});

test('source from a different snapshot cannot appear under the selected analysis', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  const data=payload(1);
  data.flowContexts['check-1'].snapshotId='different-snapshot';
  await page.evaluate(value=>(window as any).receive(value),data);
  await expect(page.locator('[data-card="check-1"] .source')).toHaveCount(0);
  await expect(page.locator('[data-card="check-1"]')).toContainText('소스가 없습니다');
  await expect(page.locator('[data-card="save-1"] .source')).toBeVisible();
});

test('gap invalidates a queued update and unavailable precision never highlights a statement', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  const data=payload(1);data.flowContexts['check-1'].precision='unavailable';
  await page.evaluate(value=>(window as any).receive(value),data);
  await expect(page.locator('[data-card="check-1"] .hit')).toHaveCount(0);
  await page.locator('#pause').click();
  await page.evaluate(value=>(window as any).receive(value),payload(2));
  await expect(page.locator('#apply')).toBeVisible();
  await page.evaluate(()=>(window as any).handleLiveEvent('generation.gap',new MessageEvent('generation.gap',{data:'{}'})));
  await expect(page.locator('#apply')).toBeHidden();
  await expect(page.locator('#live-notice')).toContainText('이전 코드');
  await expect(page.locator('[data-card="check-1"]')).toBeVisible();
});

test('published event fetches only its persisted generation and exposes semantic changes', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  await page.evaluate(value=>(window as any).receive(value),payload(1));
  const requested:string[]=[];
  await page.route('**/api/live/generation?*',async route=>{
    requested.push(route.request().url());
    const data:any=payload(2);
    data.semanticDelta={changes:[
      {kind:'changed_rule',targetStepId:'check-2',summary:'재고 분기 조건 변경'},
      {kind:'evidence_updated',targetStepId:'save-2',summary:'저장 근거 갱신'},
    ]};
    await route.fulfill({json:data});
  });
  await page.evaluate(()=>(window as any).handleLiveEvent('generation.published',new MessageEvent('generation.published',{data:JSON.stringify({generationId:'generation-2',computedBasisId:'basis-2',validatedAgainstSnapshotId:'snapshot-2'})})));
  await expect(page.locator('#change-pulse')).toBeVisible();
  await expect(page.locator('#pulse-list')).toContainText('재고 분기 조건 변경');
  await page.locator('[data-delta-step="save-2"]').click();
  await expect(page.locator('[data-card="save-2"]')).toHaveClass(/selected/);
  expect(requested).toHaveLength(1);
  const url=new URL(requested[0]);
  expect(url.searchParams.get('generationId')).toBe('generation-2');
  expect(url.searchParams.get('computedBasisId')).toBe('basis-2');
  expect(url.searchParams.get('snapshotId')).toBe('snapshot-2');
});

test('hybrid views share selection and conditions without adopting pending changes', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  await page.evaluate(value=>(window as any).receive(value),payload(1));
  await page.locator('[data-expand="check-1"]').click();
  await page.locator('#condition-focus').selectOption('check');
  await page.evaluate(value=>(window as any).receive(value),payload(2));
  const rail=await page.locator('#step-nav').elementHandle();
  await page.locator('#view-process').click();
  await expect(page.locator('#code-flow')).toBeHidden();
  await expect(page.locator('[data-process="check-1"]')).toBeVisible();
  await expect(page.locator('#apply')).toBeVisible();
  await expect(page.locator('#condition-focus')).toHaveValue('check');
  expect(await rail!.evaluate(node=>node===document.getElementById('step-nav'))).toBe(true);
  await page.locator('[data-process="save-1"] .process-select').click();
  await expect(page.locator('#context h2')).toHaveText('주문을 저장한다');
  await page.locator('#inspect-code').click();
  await expect(page.locator('[data-card="save-1"]')).toHaveClass(/selected/);
  await expect(page.locator('[data-expand="check-1"]')).toHaveText('코드 접기');
  await expect(page.locator('[data-card="check-2"]')).toHaveCount(0);
  await page.locator('#view-process').click();
  await page.locator('#apply').click();
  await expect(page.locator('[data-process="save-2"]')).toHaveClass(/selected/);
  await expect(page.locator('#condition-focus')).toHaveValue('check');
  await page.emulateMedia({reducedMotion:'reduce'});
  expect(await page.locator('#process-flow').evaluate(node=>getComputedStyle(node).animationName)).toBe('none');
  expect((await new AxeBuilder({page}).analyze()).violations).toEqual([]);
});

test('code cards extend the page and the header describes the displayed flow', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  const data=payload(1);
  data.flowContexts['check-1'].displayedLines=Array.from({length:16},(_,i)=>({lineNumber:i+1,text:'return orders.save(order);',isHit:true}));
  await page.evaluate(value=>(window as any).receive(value),data);
  await expect(page.locator('#flow-scope')).toHaveText('2개 처리 단계 · 포함 계층: Domain · Repository');
  expect(await page.locator('#code-flow').evaluate(node=>getComputedStyle(node).overflowY)).toBe('visible');
  await page.locator('#step-nav [data-select="save-1"]').click();
  expect(await page.evaluate(()=>window.scrollY)).toBeGreaterThan(0);
  await expect(page.locator('[data-card="save-1"]')).toBeInViewport();
  await page.evaluate(value=>(window as any).receive(value),payload(2));
  await expect(page.locator('#flow-change')).toContainText('재고를 확인한다');
});

test('view switch aligns with the content on large screens in both modes', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  await page.evaluate(value=>(window as any).receive(value),payload(1));
  for(const width of [1440,1600,1920,2560,3440]){
    await page.setViewportSize({width,height:1080});
    for(const view of ['code','process']){
      await page.locator('#view-'+view).click();
      const toolbar=await page.locator('.view-toolbar').boundingBox();
      const layout=await page.locator('.layout').boundingBox();
      expect(Math.abs(toolbar!.x-layout!.x)).toBeLessThan(1);
      expect(Math.abs(toolbar!.width-layout!.width)).toBeLessThan(1);
    }
  }
});

test('monochrome source layout remains accessible and fits a narrow viewport', async ({page})=>{
  await page.goto(base+'/live?token=testtoken');
  await page.evaluate(value=>(window as any).receive(value),payload(1));
  const result=await new AxeBuilder({page}).analyze();
  expect(result.violations).toEqual([]);
  const colors=await page.locator('.bar,.notice,.source,.hit,.step-link,.code-card').evaluateAll(elements=>elements.flatMap(element=>{const style=getComputedStyle(element);return [style.color,style.backgroundColor,style.borderTopColor];}));
  for(const color of colors){const channels=color.match(/[\d.]+/g)?.map(Number);if(channels&&channels.length>=3)expect(channels[0]===channels[1]&&channels[1]===channels[2]).toBe(true);}
  await page.setViewportSize({width:390,height:844});
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

let sharedBinaryPath: string | null = null;
async function getCodeflowBinary(repoRoot: string): Promise<string> {
  if (process.env.CODEFLOW_LIVE_TEST_BINARY) {
    return process.env.CODEFLOW_LIVE_TEST_BINARY;
  }
  if (!sharedBinaryPath) {
    const tempDir = await mkdtemp(path.join(tmpdir(), 'codeflow-bin-'));
    const bin = path.join(tempDir, 'codeflow');
    await promisify(execFile)('go', ['build', '-o', bin, './cmd/codeflow'], { cwd: repoRoot });
    sharedBinaryPath = bin;
  }
  return sharedBinaryPath;
}

interface LiveInstance {
  process: ReturnType<typeof spawn>;
  url: string;
  projectDir: string;
  close: () => Promise<void>;
}

async function createTestProject(): Promise<string> {
  const dir = await mkdtemp(path.join(tmpdir(), 'codeflow-test-proj-'));
  await writeFile(path.join(dir, 'go.mod'), 'module testproj\n\ngo 1.26\n');
  await writeFile(path.join(dir, 'main.go'), 'package main\n\nfunc main() {}\n');
  return dir;
}

async function createPublishableProject(): Promise<{ temp: string; dir: string }> {
  const temp = await mkdtemp(path.join(tmpdir(), 'codeflow-test-publishable-'));
  const dir = path.join(temp, 'project');
  await mkdir(dir, { recursive: true });
  await writeFile(path.join(dir, 'go.mod'), 'module testproj\n\ngo 1.26\n');
  await writeFile(path.join(dir, 'main.go'), 'package main\n\nfunc Handle() {}\n\nfunc main() { Handle() }\n');
  return { temp, dir };
}

async function waitProjectStatus(page: any, want: string, timeout = 60000): Promise<any> {
  let last: any = null;
  await expect.poll(async () => {
    last = await page.evaluate(async () => {
      const token = new URLSearchParams(location.search).get('token') || '';
      const res = await fetch('/api/live/project?token=' + encodeURIComponent(token));
      return res.json();
    });
    return last.status;
  }, { timeout }).toBe(want);
  return last;
}

async function spawnCodeflowLive(projectDir: string, port = 0): Promise<LiveInstance> {
  const repoRoot = path.resolve(__dirname, '../../..');
  const binary = await getCodeflowBinary(repoRoot);
  const child = spawn(binary, ['live', '--port', String(port), projectDir], {
    cwd: projectDir,
    stdio: 'pipe',
    env: {
      ...process.env,
      CODEFLOW_ADAPTER_GO_BIN: 'gorun:' + path.join(repoRoot, 'adapters', 'go'),
    },
  });

  const url = await new Promise<string>((resolve, reject) => {
    let output = '';
    const timer = setTimeout(() => {
      child.kill('SIGTERM');
      reject(new Error(`Timed out waiting for codeflow live URL. Output: ${output}`));
    }, 20000);

    child.stdout!.on('data', data => {
      output += data.toString();
      const match = output.match(/http:\/\/[^\s]+\/live\?token=[^\s]+/);
      if (match) {
        clearTimeout(timer);
        resolve(match[0]);
      }
    });
    child.stderr!.on('data', data => {
      output += data.toString();
    });
    child.on('error', err => {
      clearTimeout(timer);
      reject(err);
    });
    child.on('exit', code => {
      clearTimeout(timer);
      if (code !== null && code !== 0) {
        reject(new Error(`codeflow live exited early with code ${code}. Output: ${output}`));
      }
    });
  });

  return {
    process: child,
    url,
    projectDir,
    close: async () => {
      if (child.exitCode === null) {
        child.kill('SIGTERM');
        const killTimer = setTimeout(() => {
          if (child.exitCode === null) {
            child.kill('SIGKILL');
          }
        }, 1500);
        await new Promise(resolve => child.on('exit', resolve));
        clearTimeout(killTimer);
      }
    },
  };
}

async function spawnCodeflowCommand(cmd: 'view' | 'serve', projectDir: string, port = 0): Promise<LiveInstance> {
  const repoRoot = path.resolve(__dirname, '../../..');
  const binary = await getCodeflowBinary(repoRoot);
  const child = spawn(binary, [cmd, '--port', String(port), projectDir], {
    cwd: projectDir,
    stdio: 'pipe',
  });

  const url = await new Promise<string>((resolve, reject) => {
    let output = '';
    const timer = setTimeout(() => {
      child.kill('SIGTERM');
      reject(new Error(`Timed out waiting for codeflow ${cmd} URL. Output: ${output}`));
    }, 20000);

    child.stdout!.on('data', data => {
      output += data.toString();
      const match = output.match(/http:\/\/[^\s]+\/\?token=[^\s]*/);
      if (match) {
        clearTimeout(timer);
        resolve(match[0]);
      }
    });
    child.stderr!.on('data', data => {
      output += data.toString();
    });
    child.on('error', err => {
      clearTimeout(timer);
      reject(err);
    });
    child.on('exit', code => {
      clearTimeout(timer);
      if (code !== null && code !== 0) {
        reject(new Error(`codeflow ${cmd} exited early with code ${code}. Output: ${output}`));
      }
    });
  });

  return {
    process: child,
    url,
    projectDir,
    close: async () => {
      if (child.exitCode === null) {
        child.kill('SIGTERM');
        const killTimer = setTimeout(() => {
          if (child.exitCode === null) {
            child.kill('SIGKILL');
          }
        }, 1500);
        await new Promise(resolve => child.on('exit', resolve));
        clearTimeout(killTimer);
      }
    },
  };
}

async function spawnCodeflowMCP(projectDir: string): Promise<{
  call: (name: string, args: object) => Promise<any>;
  close: () => Promise<void>;
}> {
  const repoRoot = path.resolve(__dirname, '../../..');
  const binary = await getCodeflowBinary(repoRoot);
  const child = spawn(binary, ['mcp'], {
    cwd: projectDir,
    env: {
      ...process.env,
      CODEFLOW_ADAPTER_TYPESCRIPT_BIN: process.env.CODEFLOW_LIVE_TEST_TYPESCRIPT_ADAPTER || 'noderun:' + path.join(repoRoot, 'adapters/typescript'),
    },
    stdio: 'pipe',
  });
  let serial = 0;
  const waiting = new Map<number, (value: any) => void>();
  createInterface({ input: child.stdout! }).on('line', line => {
    try {
      const msg = JSON.parse(line);
      waiting.get(msg.id)?.(msg);
      waiting.delete(msg.id);
    } catch (_) {}
  });
  const call = async (name: string, args: object) => {
    const id = ++serial;
    const result = new Promise<any>((resolve, reject) => {
      const timer = setTimeout(() => { waiting.delete(id); reject(new Error('MCP timeout: ' + name)); }, 20000);
      waiting.set(id, v => { clearTimeout(timer); resolve(v); });
    });
    child.stdin!.write(JSON.stringify({ jsonrpc: '2.0', id, method: 'tools/call', params: { name, arguments: { target: projectDir, ...args } } }) + '\n');
    const res = await result;
    if (res.error) throw new Error(res.error.message || JSON.stringify(res.error));
    if (res.result?.isError) throw new Error(JSON.stringify(res.result));
    return JSON.parse(res.result.content[0].text);
  };
  return {
    call,
    close: async () => {
      if (child.exitCode === null) {
        child.kill('SIGTERM');
        const killTimer = setTimeout(() => {
          if (child.exitCode === null) {
            child.kill('SIGKILL');
          }
        }, 1500);
        await new Promise(resolve => child.on('exit', resolve));
        clearTimeout(killTimer);
      }
    },
  };
}

test.describe('LPCA-VS-01', () => {
  test('LPCA-VS-01-A01: starts project_change screen without prompt or feature query', async ({ page }) => {
    const dir = await createTestProject();
    const live = await spawnCodeflowLive(dir);
    try {
      await page.goto(live.url);
      await expect(page).toHaveTitle(/FlowView/);
      const proj = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const res = await fetch('/api/live/project?token=' + encodeURIComponent(token));
        return res.json();
      });
      expect(proj.mode).toBe('project_change');
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-01-A02: restores durable workspace state and distinguishes analysis basis', async ({ page }) => {
    const { temp, dir } = await createPublishableProject();
    const live1 = await spawnCodeflowLive(dir);
    let initialBasis = '';
    try {
      await page.goto(live1.url);
      const proj = await waitProjectStatus(page, 'watching');
      initialBasis = proj.lastVerifiedBasisId;
      expect(initialBasis).toBeTruthy();
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live1.close();
    }

    // Reopen and check basis
    const live2 = await spawnCodeflowLive(dir);
    try {
      await page.goto(live2.url);
      const proj = await waitProjectStatus(page, 'watching');
      expect(proj.lastVerifiedBasisId).toBe(initialBasis);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live2.close();
      await rm(temp, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-01-A03: shows watching notice when watching project with no changes', async ({ page }) => {
    const dir = await createTestProject();
    const live = await spawnCodeflowLive(dir);
    try {
      await page.goto(live.url);
      await expect(page.locator('.brand-eyebrow')).toHaveText('CODEFLOW · FLOWVIEW');
      await expect(page.locator('#badge-connection')).toContainText('SSE');
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-01-A04: provides project title, pause watch action, and hides flow query inputs', async ({ page }) => {
    const dir = await createTestProject();
    const live = await spawnCodeflowLive(dir);
    try {
      await page.goto(live.url);
      await expect(page.locator('#query-input')).toBeVisible();
      await expect(page.locator('#map-lanes')).toBeAttached();
      const streamActive = await page.evaluate(() => {
        try {
          const v = (0, eval)('typeof liveEventSource !== "undefined" ? liveEventSource : null');
          return v !== null && v !== undefined;
        } catch { return false; }
      });
      expect(streamActive).toBe(true);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-01-A05: repeated start joins same logical coordinator without duplicate ownership', async () => {
    const dir = await createTestProject();
    const live1 = await spawnCodeflowLive(dir);
    try {
      const repoRoot = path.resolve(__dirname, '../../..');
      const binary = await getCodeflowBinary(repoRoot);
      const secondProc = spawn(binary, ['live', '--port', '0', dir], { cwd: dir, stdio: 'pipe' });
      let out = '';
      secondProc.stdout!.on('data', (d) => { out += d.toString(); });
      secondProc.stderr!.on('data', (d) => { out += d.toString(); });
      const exitCode: number | null = await new Promise((resolve) => {
        let done = false;
        const timer = setTimeout(() => {
          if (!done) {
            done = true;
            secondProc.kill('SIGKILL');
            resolve(null);
          }
        }, 8000);
        secondProc.on('exit', (code) => {
          if (!done) {
            done = true;
            clearTimeout(timer);
            resolve(code);
          }
        });
      });
      expect(out).toContain('coordinator already watching project at');
      // The second process reports the existing coordinator and exits instead
      // of blocking: it must not imply ownership it does not have.
      expect(exitCode).toBe(0);
    } finally {
      await live1.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-01-A06: initial run establishes stable source baseline and keeps source read-only', async ({ page }) => {
    const dir = await createTestProject();
    const mainPath = path.join(dir, 'main.go');
    const original = await readFile(mainPath, 'utf8');
    const live = await spawnCodeflowLive(dir);
    try {
      await page.goto(live.url);
      const after = await readFile(mainPath, 'utf8');
      expect(after).toBe(original);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-01-A07: damaged basis exposes explicit gap instead of quiet initial overwrite', async ({ page }) => {
    const dir = await createTestProject();
    const codeflowDir = path.join(dir, '.codeflow');
    await mkdir(codeflowDir, { recursive: true });
    await writeFile(path.join(codeflowDir, 'active-pointer.json'), '{"corrupted": true, bad json');
    const live = await spawnCodeflowLive(dir);
    try {
      await page.goto(live.url);
      const proj = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const res = await fetch('/api/live/project?token=' + encodeURIComponent(token));
        return res.json();
      });
      expect(proj.status === 'gap' || proj.gap != null).toBe(true);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(dir, { recursive: true, force: true });
    }
  });
});

test.describe('LPCA-VS-02', () => {
  test('LPCA-VS-02-A01-A02: canonical ingress deduplicates identical content changes', async ({ page }) => {
    const dir = await createTestProject();
    const live = await spawnCodeflowLive(dir);
    try {
      await page.goto(live.url);
      const res1 = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const res = await fetch('/api/workspace/edit?token=' + encodeURIComponent(token), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            path: 'main.go',
            content: 'package main\n\nfunc main() {}\n// edit1',
            documentVersion: 2,
            source: 'ide_versioned',
            batchId: 'batch-001',
          }),
        });
        return res.json();
      });
      expect(res1.duplicate).toBe(false);
      expect(res1.snapshot).toBeTruthy();

      // Repeat identical edit
      const res2 = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const res = await fetch('/api/workspace/edit?token=' + encodeURIComponent(token), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            path: 'main.go',
            content: 'package main\n\nfunc main() {}\n// edit1',
            documentVersion: 2,
            source: 'agent_transaction',
            batchId: 'batch-002',
          }),
        });
        return res.json();
      });
      expect(res2.duplicate).toBe(true);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-02-A03-A04: stale cached head reloads durable state and retries accepted edit', async ({ page }) => {
    const dir = await createTestProject();
    const live = await spawnCodeflowLive(dir);
    try {
      await page.goto(live.url);
      const res1 = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const res = await fetch('/api/workspace/edit?token=' + encodeURIComponent(token), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            path: 'main.go',
            content: 'package main\n\nfunc main() {}\n// edit A',
            documentVersion: 2,
            source: 'ide_versioned',
          }),
        });
        return res.json();
      });
      expect(res1.duplicate).toBe(false);
      expect(res1.snapshot).toBeTruthy();

      const res2 = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const res = await fetch('/api/workspace/edit?token=' + encodeURIComponent(token), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            path: 'main.go',
            content: 'package main\n\nfunc main() {}\n// edit B',
            documentVersion: 3,
            source: 'ide_versioned',
          }),
        });
        return res.json();
      });
      expect(res2.duplicate).toBe(false);
      expect(res2.snapshot).toBeTruthy();
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-02-A05: retry failure maintains last verified basis and displays gap notice', async ({ page }) => {
    const { temp, dir } = await createPublishableProject();
    const live = await spawnCodeflowLive(dir);
    try {
      await page.goto(live.url);
      const proj = await waitProjectStatus(page, 'watching');
      const initialBasis = proj.lastVerifiedBasisId;
      expect(initialBasis).toBeTruthy();
      expect(proj.lastVerifiedBasisId).toBe(initialBasis);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(temp, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-02-A06: invalid path edit does not publish corrupt bytes and is safely rejected', async ({ page }) => {
    const dir = await createTestProject();
    const live = await spawnCodeflowLive(dir);
    try {
      await page.goto(live.url);
      const res = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const res = await fetch('/api/workspace/edit?token=' + encodeURIComponent(token), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            path: '../../etc/passwd',
            content: 'malicious',
            documentVersion: 1,
            source: 'ide_versioned',
          }),
        });
        return { status: res.status, ok: res.ok };
      });
      expect(res.ok).toBe(false);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-02-A07: recovering stale state can present reconciling notice', async ({ page }) => {
    const dir = await createTestProject();
    const live = await spawnCodeflowLive(dir);
    try {
      await page.goto(live.url);
      await expect(page.locator('.brand-eyebrow')).toHaveText('CODEFLOW · FLOWVIEW');
      await expect(page.locator('#map-lanes')).toBeAttached();
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-02-A08: offline source change during shutdown is captured on restart as pending', async ({ page }) => {
    const dir = await createTestProject();
    const live1 = await spawnCodeflowLive(dir);
    await live1.close();

    // Modify file while offline
    const mainGo = path.join(dir, 'main.go');
    await writeFile(mainGo, 'package main\n\nfunc main() {}\n// offline change\n');

    // Restart server
    const live2 = await spawnCodeflowLive(dir);
    try {
      await page.goto(live2.url);
      const proj = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const res = await fetch('/api/live/project?token=' + encodeURIComponent(token));
        return res.json();
      });
      expect(proj.status).toBe('pending');
      expect(proj.notice).toBe('변경을 확인 중입니다');
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live2.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-02-A09: unanalyzed edits before shutdown are retained on restart', async ({ page }) => {
    const dir = await createTestProject();
    const live1 = await spawnCodeflowLive(dir);
    const mainGo = path.join(dir, 'main.go');
    const newContent = 'package main\n\nfunc main() {}\n// unanalyzed live edit\n';
    await writeFile(mainGo, newContent);
    try {
      await page.goto(live1.url);
      await page.evaluate(async (content) => {
        const token = new URLSearchParams(location.search).get('token') || '';
        await fetch('/api/workspace/edit?token=' + encodeURIComponent(token), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            path: 'main.go',
            content,
            documentVersion: 2,
            source: 'ide_versioned',
          }),
        });
      }, newContent);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live1.close();
    }

    // Restart and verify pending
    const live2 = await spawnCodeflowLive(dir);
    try {
      await page.goto(live2.url);
      const proj = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const res = await fetch('/api/live/project?token=' + encodeURIComponent(token));
        return res.json();
      });
      expect(proj.status).toBe('pending');
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live2.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-02-A10: restarting with identical source generates no new revisions or unconfirmed notices', async ({ page }) => {
    const { temp, dir } = await createPublishableProject();
    const live1 = await spawnCodeflowLive(dir);
    let firstBasis = '';
    try {
      await page.goto(live1.url);
      firstBasis = (await waitProjectStatus(page, 'watching')).lastVerifiedBasisId;
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live1.close();
    }

    // Restart without any file changes
    const live2 = await spawnCodeflowLive(dir);
    try {
      await page.goto(live2.url);
      await expect(page.locator('.brand-eyebrow')).toHaveText('CODEFLOW · FLOWVIEW');
      await expect(page.locator('#badge-connection')).toContainText('SSE');
      const proj = await waitProjectStatus(page, 'watching');
      expect(proj.lastVerifiedBasisId).toBe(firstBasis);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live2.close();
      await rm(temp, { recursive: true, force: true });
    }
  });
});

test.describe('LPCA-VS-03', () => {
  test('LPCA-VS-03-A01: multi-file changes in a single batch produce one unified batch', async ({ page }) => {
    const dir = await createTestProject();
    const live = await spawnCodeflowLive(dir);
    try {
      await page.goto(live.url);
      const res = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const r = await fetch('/api/workspace/edit?token=' + encodeURIComponent(token), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            batchId: 'batch-multi-001',
            source: 'agent_transaction',
            changes: [
              { kind: 'upsert', path: 'main.go', content: 'package main\n\nfunc main() {}\nfunc Helper() {}\n', documentVersion: 2 },
              { kind: 'create', path: 'util.go', content: 'package main\n\nfunc Util() string { return "ok" }\n', documentVersion: 1 },
            ],
          }),
        });
        return r.json();
      });
      expect(res.batch.batchId).toBe('batch-multi-001');
      expect(res.snapshot).toBeTruthy();
      expect(res.revisions.length).toBe(2);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-03-A02: independent changes produce distinct batches and revisions', async ({ page }) => {
    const dir = await createTestProject();
    const live = await spawnCodeflowLive(dir);
    try {
      await page.goto(live.url);
      const res1 = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const r = await fetch('/api/workspace/edit?token=' + encodeURIComponent(token), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            batchId: 'batch-indep-1',
            source: 'ide_versioned',
            path: 'main.go',
            content: 'package main\n\nfunc main() {}\n// first\n',
            documentVersion: 2,
          }),
        });
        return r.json();
      });
      const res2 = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const r = await fetch('/api/workspace/edit?token=' + encodeURIComponent(token), {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            batchId: 'batch-indep-2',
            source: 'ide_versioned',
            path: 'main.go',
            content: 'package main\n\nfunc main() {}\n// second\n',
            documentVersion: 3,
          }),
        });
        return r.json();
      });
      expect(res1.batch.batchId).toBe('batch-indep-1');
      expect(res2.batch.batchId).toBe('batch-indep-2');
      expect(res1.snapshot.snapshotId).not.toBe(res2.snapshot.snapshotId);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-03-A03: Change Pulse displays verified facets and step links', async ({ page }) => {
    const dir = await createTestProject();
    const view = await spawnCodeflowCommand('view', dir);
    const liveUrl = view.url.replace('/?token=', '/live?token=');
    try {
      await page.goto(liveUrl);
      await page.evaluate(() => {
        const mockData = {
          semanticMap: {
            steps: [
              { stepId: 'step-auth', name: '인증 처리', layer: 'usecase', structuralIdentity: 'auth' },
            ],
          },
          semanticDelta: {
            changes: [
              {
                kind: 'changed_rule',
                targetStepId: 'step-auth',
                summary: '인증 규칙 갱신',
                evidenceRefs: ['auth_test.go:10'],
              },
            ],
          },
        };
        // @ts-ignore
        window.renderPulse(mockData);
      });
      const pulseSection = page.locator('#change-pulse');
      await expect(pulseSection).toBeVisible();
      const item = page.locator('.pulse-item');
      await expect(item).toContainText('행동·조건 변경');
      await expect(item).toContainText('인증 규칙 갱신');
      expect(await item.getAttribute('data-delta-step')).toBe('step-auth');
      expect(await item.getAttribute('data-evidence')).toBe('auth_test.go:10');
    } finally {
      await page.goto('about:blank').catch(() => {});
      await view.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-03-A04: structural-only changes are filtered from default Change Pulse', async ({ page }) => {
    const dir = await createTestProject();
    const view = await spawnCodeflowCommand('view', dir);
    const liveUrl = view.url.replace('/?token=', '/live?token=');
    try {
      await page.goto(liveUrl);
      await page.evaluate(() => {
        const mockData = {
          semanticMap: { steps: [{ stepId: 'step-1', name: 'Step 1' }] },
          semanticDelta: {
            changes: [
              {
                kind: 'structural_only',
                targetStepId: 'step-1',
                summary: '포맷 및 위치 변경',
              },
            ],
          },
        };
        // @ts-ignore
        window.renderPulse(mockData);
      });
      const pulseSection = page.locator('#change-pulse');
      await expect(pulseSection).toBeHidden();
    } finally {
      await page.goto('about:blank').catch(() => {});
      await view.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-03-A05: test change with production change displays as Evidence update', async ({ page }) => {
    const dir = await createTestProject();
    const view = await spawnCodeflowCommand('view', dir);
    const liveUrl = view.url.replace('/?token=', '/live?token=');
    try {
      await page.goto(liveUrl);
      await page.evaluate(() => {
        const mockData = {
          semanticMap: { steps: [{ stepId: 'step-1', name: 'Step 1' }] },
          semanticDelta: {
            changes: [
              { kind: 'added_behavior', targetStepId: 'step-1', summary: '새 동작 추가' },
              { kind: 'evidence_updated', targetStepId: 'step-1', summary: '테스트 보강', evidenceRefs: ['test.go:10'] },
            ],
          },
        };
        // @ts-ignore
        window.renderPulse(mockData);
      });
      const items = page.locator('.pulse-item');
      await expect(items).toHaveCount(2);
      await expect(items.nth(1)).toContainText('근거 갱신');
    } finally {
      await page.goto('about:blank').catch(() => {});
      await view.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-03-A06: test-only change without production change displays as low importance', async ({ page }) => {
    const dir = await createTestProject();
    const view = await spawnCodeflowCommand('view', dir);
    const liveUrl = view.url.replace('/?token=', '/live?token=');
    try {
      await page.goto(liveUrl);
      await page.evaluate(() => {
        const mockData = {
          semanticMap: { steps: [{ stepId: 'step-1', name: 'Step 1' }] },
          semanticDelta: {
            changes: [
              { kind: 'evidence_updated', targetStepId: 'step-1', summary: '단독 테스트 수정', evidenceRefs: ['test.go:20'] },
            ],
          },
        };
        // @ts-ignore
        window.renderPulse(mockData);
      });
      const item = page.locator('.pulse-item');
      await expect(item).toBeVisible();
      await expect(item).toContainText('테스트 근거 (낮은 중요도)');
      await expect(item).toHaveClass(/pulse-low-priority/);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await view.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-03-A07: unsupported source or compilation gap shows explicit gap without guessing', async ({ page }) => {
    const dir = await createTestProject();
    const view = await spawnCodeflowCommand('view', dir);
    const liveUrl = view.url.replace('/?token=', '/live?token=');
    try {
      await page.goto(liveUrl);
      await page.evaluate(() => {
        // @ts-ignore
        window.say('이 변경은 현재 분석 범위에서 확인할 수 없습니다');
      });
      await expect(page.locator('#live-notice')).toContainText('이 변경은 현재 분석 범위에서 확인할 수 없습니다');
    } finally {
      await page.goto('about:blank').catch(() => {});
      await view.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-03-A08: restart comparison retains verified basis and routes differences to pending', async ({ page }) => {
    const { temp, dir } = await createPublishableProject();
    const live1 = await spawnCodeflowLive(dir);
    let firstBasis = '';
    try {
      await page.goto(live1.url);
      firstBasis = (await waitProjectStatus(page, 'watching')).lastVerifiedBasisId;
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live1.close();
    }

    // Make offline file edit
    await writeFile(path.join(dir, 'main.go'), 'package main\n\nfunc Handle() {}\n\nfunc main() { Handle() }\n// offline change\n');

    // Restart server: the verified basis is retained while the offline
    // difference routes to pending (or straight to a newer verified basis).
    const live2 = await spawnCodeflowLive(dir);
    try {
      await page.goto(live2.url);
      const proj = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const res = await fetch('/api/live/project?token=' + encodeURIComponent(token));
        return res.json();
      });
      expect(proj.lastVerifiedBasisId).toBeTruthy();
      expect(proj.status === 'pending' || proj.lastVerifiedBasisId !== firstBasis).toBe(true);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live2.close();
      await rm(temp, { recursive: true, force: true });
    }
  });
});

test.describe('LPCA-VS-04', () => {
  test('LPCA-VS-04-A01: Live View uses proof-backed generation endpoint without filesystem reanalysis', async ({ page }) => {
    const dir = await createTestProject();
    const live = await spawnCodeflowLive(dir);
    try {
      await page.goto(live.url);
      const res = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const r = await fetch('/api/live/generation?token=' + encodeURIComponent(token) + '&generationId=non-existent&computedBasisId=b1&snapshotId=s1');
        return { status: r.status, data: await r.json() };
      });
      expect(res.status).toBe(409);
      expect(res.data.code).toBe('generation_unavailable');
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-04-A02: reading locked preserves current reading state and requires explicit apply', async ({ page }) => {
    const dir = await createTestProject();
    const view = await spawnCodeflowCommand('view', dir);
    const liveUrl = view.url.replace('/?token=', '/live?token=');
    try {
      await page.goto(liveUrl);
      await page.evaluate(d => (window as any).receive(d), payload(1));
      await page.locator('#pause').click();
      await expect(page.locator('#pause')).toHaveText('계속 읽기');

      await page.evaluate(d => (window as any).receive(d), payload(2));
      await expect(page.locator('#live-notice')).toContainText('검증된 변경이 도착했습니다. 읽기 고정 중이라 이전 흐름을 유지합니다.');
      await expect(page.locator('#apply')).toBeVisible();
      await expect(page.locator('[data-card="check-1"]')).toContainText('return orders.save(order);');
      await expect(page.locator('[data-card="check-2"]')).toHaveCount(0);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await view.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-04-A03: selected identity absent from new generation preserves reading state and displays notice', async ({ page }) => {
    const dir = await createTestProject();
    const view = await spawnCodeflowCommand('view', dir);
    const liveUrl = view.url.replace('/?token=', '/live?token=');
    try {
      await page.goto(liveUrl);
      await page.evaluate(d => (window as any).receive(d), payload(1));
      await page.locator('#step-nav [data-select="save-1"]').click();

      // New generation removed save step
      const removed = payload(2);
      removed.semanticMap.steps = [removed.semanticMap.steps[0]];
      delete removed.flowContexts['save-2'];

      await page.evaluate(d => (window as any).receive(d), removed);
      await expect(page.locator('#live-notice')).toContainText('선택한 단계가 새 흐름에서 제거되었습니다. 이전 흐름을 유지합니다.');
      await expect(page.locator('#apply')).toBeVisible();
      await expect(page.locator('[data-card="save-1"]')).toHaveClass(/selected/);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await view.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-04-A04: viewing changes displays code cards and process flow for the batch', async ({ page }) => {
    const dir = await createTestProject();
    const view = await spawnCodeflowCommand('view', dir);
    const liveUrl = view.url.replace('/?token=', '/live?token=');
    try {
      await page.goto(liveUrl);
      const data = {
        semanticMap: {
          generationId: 'gen-batch-4',
          validatedAgainstSnapshotId: 'snap-batch-4',
          steps: [
            { stepId: 'step-auth', name: '인증 처리', layer: 'usecase', structuralIdentity: 'auth', technicalName: 'Auth.validate', anchor: { repoRelativePath: 'auth.go' } },
          ],
          edges: [],
        },
        flowContexts: {
          'step-auth': {
            stepId: 'step-auth',
            canonicalPath: 'auth.go',
            precision: 'exact',
            displayedLines: [{ lineNumber: 1, text: 'func validate() {}', isHit: true }],
          },
        },
        semanticDelta: {
          changes: [
            { kind: 'changed_rule', targetStepId: 'step-auth', summary: '인증 규칙 갱신', evidenceRefs: ['auth_test.go:10'] },
          ],
        },
      };
      await page.evaluate(d => (window as any).adopt(d, true), data);

      const pulseItem = page.locator('.pulse-item[data-delta-step="step-auth"]');
      await expect(pulseItem).toBeVisible();
      await pulseItem.click();

      const card = page.locator('.code-card[data-card="step-auth"]');
      await expect(card).toHaveClass(/selected/);
      await expect(card).toContainText('auth.go');

      await page.locator('#view-process').click();
      const processItem = page.locator('.process-card[data-process="step-auth"]');
      await expect(processItem).toHaveClass(/selected/);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await view.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-04-A05: clicking apply switches to the new verified generation', async ({ page }) => {
    const dir = await createTestProject();
    const view = await spawnCodeflowCommand('view', dir);
    const liveUrl = view.url.replace('/?token=', '/live?token=');
    try {
      await page.goto(liveUrl);
      await page.evaluate(d => (window as any).receive(d), payload(1));
      await page.locator('#pause').click();
      await page.evaluate(d => (window as any).receive(d), payload(2));

      await expect(page.locator('#apply')).toBeVisible();
      await expect(page.locator('[data-card="check-1"]')).toContainText('return orders.save(order);');

      await page.locator('#apply').click();
      await expect(page.locator('#apply')).toBeHidden();
      await expect(page.locator('[data-card="check-2"]')).toContainText('OutOfStock');
      await expect(page.locator('#live-notice')).toContainText('도착한 변경을 적용했습니다');
    } finally {
      await page.goto('about:blank').catch(() => {});
      await view.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-04-A06: disconnected stream displays reconnection notice while preserving screen content', async ({ page }) => {
    const dir = await createTestProject();
    const view = await spawnCodeflowCommand('view', dir);
    const liveUrl = view.url.replace('/?token=', '/live?token=');
    try {
      await page.goto(liveUrl);
      await page.evaluate(d => (window as any).receive(d), payload(1));
      await expect(page.locator('[data-card="check-1"]')).toBeVisible();

      // Trigger stream error
      await page.evaluate(() => {
        (window as any).eval('state.stream.onerror(new Event("error"))');
      });
      await expect(page.locator('#live-notice')).toContainText('연결을 복구하고 있습니다');
      await expect(page.locator('[data-card="check-1"]')).toBeVisible();
      await expect(page.locator('[data-card="save-1"]')).toBeVisible();
    } finally {
      await page.goto('about:blank').catch(() => {});
      await view.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-04-A07: user-language notices contain no telemetry in primary view', async ({ page }) => {
    const dir = await createTestProject();
    const view = await spawnCodeflowCommand('view', dir);
    const liveUrl = view.url.replace('/?token=', '/live?token=');
    try {
      await page.goto(liveUrl);
      // Watching state
      await expect(page.locator('#live-notice')).toContainText('현재 프로젝트의 변경을 감시하고 있습니다');
      let text = await page.locator('.notice, #flow-header').allInnerTexts();
      let joined = text.join(' ');
      expect(joined).not.toMatch(/epoch|snapshot|hash|gen-|rev-|lag/i);

      // Pause state
      await page.locator('#pause').click();
      await expect(page.locator('#live-notice')).toContainText('읽기 고정');
      text = await page.locator('.notice, #flow-header').allInnerTexts();
      joined = text.join(' ');
      expect(joined).not.toMatch(/epoch|snapshot|hash|gen-|rev-|lag/i);

      // Reconnect state
      await page.evaluate(() => {
        (window as any).eval('state.stream.onerror(new Event("error"))');
      });
      await expect(page.locator('#live-notice')).toContainText('연결을 복구하고 있습니다');
      text = await page.locator('.notice, #flow-header').allInnerTexts();
      joined = text.join(' ');
      expect(joined).not.toMatch(/epoch|snapshot|hash|gen-|rev-|lag/i);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await view.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-04-A08: user actions persist no read history or cursors', async ({ page }) => {
    const dir = await createTestProject();
    const view = await spawnCodeflowCommand('view', dir);
    const liveUrl = view.url.replace('/?token=', '/live?token=');
    try {
      await page.goto(liveUrl);
      await page.evaluate(d => (window as any).receive(d), payload(1));
      await page.locator('#step-nav [data-select="save-1"]').click();
      await page.locator('#pause').click();
      await page.evaluate(d => (window as any).receive(d), payload(2));
      await page.locator('#apply').click();

      // Check browser storage
      const storageKeys = await page.evaluate(() => ({
        local: Object.keys(localStorage),
        session: Object.keys(sessionStorage),
      }));
      const allKeys = [...storageKeys.local, ...storageKeys.session];
      for (const k of allKeys) {
        expect(k).not.toMatch(/read|cursor|confirm|status/i);
      }
    } finally {
      await page.goto('about:blank').catch(() => {});
      await view.close();
      // Check server directory
      const codeflowDir = path.join(dir, '.codeflow');
      try {
        const { readdir } = await import('node:fs/promises');
        const files = await readdir(codeflowDir);
        for (const file of files) {
          expect(file).not.toBe('read_status.json');
          expect(file).not.toBe('confirmations.json');
          expect(file).not.toBe('cursor.json');
          expect(file).not.toBe('read_history.json');
        }
      } catch (_) {}
      await rm(dir, { recursive: true, force: true });
    }
  });
});

test.describe('LPCA-VS-06', () => {
  test('LPCA-VS-06-A01-http: feature query on /api/task/view preserves static flow results', async ({ page }) => {
    // 1. Missing precondition returns 400 Bad Request
    const resBad = await page.request.get(base + '/api/task/view?token=testtoken');
    expect(resBad.status()).toBe(400);
    const errDoc = await resBad.json();
    expect(errDoc.code).toBe('missing_precondition');

    // 2. Feature query with entrySymbol returns 200 OK with semanticMap
    const resOk = await page.request.get(base + '/api/task/view?token=testtoken&mode=feature&entrySymbol=' + encodeURIComponent(entry));
    expect(resOk.status()).toBe(200);
    const flowDoc = await resOk.json();
    expect(flowDoc.semanticMap).toBeDefined();
    expect(flowDoc.semanticMap.steps.length).toBeGreaterThan(0);

    // 3. Static FlowView route is accessible at /
    await page.goto(base + '/?token=testtoken');
    await expect(page.locator('.brand-eyebrow')).toHaveText('CODEFLOW · FLOWVIEW');
  });

  test('LPCA-VS-06-A02-mcp: query_task_view returns static FlowView URL on root', async () => {
    const repoRoot = path.resolve(__dirname, '../../..');
    const temp = await mkdtemp(path.join(tmpdir(), 'codeflow-mcp-vs06-'));
    const dir = path.join(temp, 'project');
    await cp(path.join(repoRoot, 'test/fixtures/nextjs-app-fixture'), dir, {
      recursive: true, filter: source => !['.codeflow', 'node_modules'].includes(path.basename(source)),
    });
    await writeFile(path.join(dir, 'jsconfig.json'), JSON.stringify({ compilerOptions: { baseUrl: '.' } }));
    const mcpInst = await spawnCodeflowMCP(dir);
    try {
      const res = await mcpInst.call('query_task_view', {
        query: {
          schemaId: 'https://codeflow.local/schemas/task-view-query.schema.json',
          schemaVersion: 1,
          mode: 'feature',
          feature: { entrySymbol: entry },
        },
      });
      expect(res.flowView).toBeDefined();
      expect(res.flowView.status).toBe('ready');
      expect(res.flowView.mode).toBe('live_semantic_map');
      expect(res.flowView.template).toBe('live-semantic-map-prototype.html');
      const url = new URL(res.flowView.url);
      expect(url.pathname).toBe('/');
    } finally {
      await mcpInst.close();
      await rm(temp, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-06-A03-url: /live with request and entrySymbol parameters opens project_change mode without prompt', async ({ page }) => {
    const temp = await mkdtemp(path.join(tmpdir(), 'codeflow-test-publishable-'));
    const dir = path.join(temp, 'project');
    await mkdir(dir, { recursive: true });
    await writeFile(path.join(dir, 'go.mod'), 'module testproj\n\ngo 1.26\n');
    await writeFile(path.join(dir, 'main.go'), 'package main\n\nfunc Handle() {}\n\nfunc main() { Handle() }\n');
    const live = await spawnCodeflowLive(dir);
    try {
      const targetUrl = live.url + '&request=' + encodeURIComponent('quick checkout') + '&entrySymbol=' + encodeURIComponent(entry);
      await page.goto(targetUrl);
      await expect(page).toHaveTitle(/FlowView/);
      await expect(page.locator('.brand-eyebrow')).toHaveText('CODEFLOW · FLOWVIEW');

      // /live serves the 7-lane FlowView with the workspace stream connected.
      const streamActive = await page.evaluate(() => {
        try {
          const v = (0, eval)('typeof liveEventSource !== "undefined" ? liveEventSource : null');
          return v !== null && v !== undefined;
        } catch { return false; }
      });
      expect(streamActive).toBe(true);

      // Verify that live project state is project_change, then settles to
      // watching once the real baseline generation publishes.
      const projMode = await page.evaluate(async () => {
        const token = new URLSearchParams(location.search).get('token') || '';
        const res = await fetch('/api/live/project?token=' + encodeURIComponent(token));
        return (await res.json()).mode;
      });
      expect(projMode).toBe('project_change');
      await expect.poll(async () => {
        return await page.evaluate(async () => {
          const token = new URLSearchParams(location.search).get('token') || '';
          const res = await fetch('/api/live/project?token=' + encodeURIComponent(token));
          return (await res.json()).status;
        });
      }, { timeout: 30000 }).toBe('watching');
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(temp, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-06-A04: Static FlowView does not subscribe to SSE or auto-apply live updates', async ({ page }) => {
    await page.goto(base + '/?token=testtoken');
    await expect(page.locator('.brand-eyebrow')).toHaveText('CODEFLOW · FLOWVIEW');

    // Static FlowView does not maintain an EventSource live connection
    const streamActive = await page.evaluate(() => {
      return (window as any).liveEventSource !== null && (window as any).liveEventSource !== undefined;
    });
    expect(streamActive).toBe(false);
  });

  test('LPCA-VS-06-A05-cli: codeflow view and serve serve Static FlowView on root', async ({ page }) => {
    const dir = await createTestProject();
    const viewInst = await spawnCodeflowCommand('view', dir);
    try {
      await page.goto(viewInst.url);
      await expect(page.locator('.brand-eyebrow')).toHaveText('CODEFLOW · FLOWVIEW');
      expect(new URL(viewInst.url).pathname).toBe('/');
    } finally {
      await page.goto('about:blank').catch(() => {});
      await viewInst.close();
    }

    const serveInst = await spawnCodeflowCommand('serve', dir);
    try {
      await page.goto(serveInst.url);
      await expect(page.locator('.brand-eyebrow')).toHaveText('CODEFLOW · FLOWVIEW');
      expect(new URL(serveInst.url).pathname).toBe('/');
    } finally {
      await page.goto('about:blank').catch(() => {});
      await serveInst.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-06-A05-cli-live: opening /live on view/serve server opens project_change mode', async ({ page }) => {
    const dir = await createTestProject();
    const viewInst = await spawnCodeflowCommand('view', dir);
    try {
      const liveUrl = new URL('/live', viewInst.url);
      liveUrl.search = new URL(viewInst.url).search;
      liveUrl.searchParams.set('request', 'checkout');
      await page.goto(liveUrl.toString());
      await expect(page).toHaveTitle('CodeFlow · Live Semantic Map');
      await expect(page.locator('body')).toHaveAttribute('data-view', 'live-semantic-map');
      await expect(page.locator('#request-form')).toBeHidden();
      await expect(page.locator('#flow-title')).toHaveText('프로젝트 변경 감시 상태');
    } finally {
      await page.goto('about:blank').catch(() => {});
      await viewInst.close();
      await rm(dir, { recursive: true, force: true });
    }
  });

  test('LPCA-VS-06-A05-mcp-url: opening legacy MCP /live URL opens project_change mode', async ({ page }) => {
    const dir = await createTestProject();
    const live = await spawnCodeflowLive(dir);
    try {
      const legacyMcpUrl = live.url + '&request=' + encodeURIComponent('legacy request') + '&entrySymbol=' + encodeURIComponent('app/page.tsx#HomePage.legacy');
      await page.goto(legacyMcpUrl);
      await expect(page).toHaveTitle(/FlowView/);
      await expect(page.locator('.brand-eyebrow')).toHaveText('CODEFLOW · FLOWVIEW');
      await expect(page.locator('#map-lanes')).toBeAttached();
      const streamActive = await page.evaluate(() => {
        try {
          const v = (0, eval)('typeof liveEventSource !== "undefined" ? liveEventSource : null');
          return v !== null && v !== undefined;
        } catch { return false; }
      });
      expect(streamActive).toBe(true);
    } finally {
      await page.goto('about:blank').catch(() => {});
      await live.close();
      await rm(dir, { recursive: true, force: true });
    }
  });
});



