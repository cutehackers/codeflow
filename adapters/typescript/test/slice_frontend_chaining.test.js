'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { sliceFlow, extractStatements, findHookBinding } = require('../lib/slice');
const { sha256Hex, canonicalAstFingerprint } = require('../lib/sha256');

function validateStepAnchor(step, fileBytes) {
  assert(step.anchor, 'Step must have an anchor');
  const a = step.anchor;

  // 1. repoRelativePath
  assert(typeof a.repoRelativePath === 'string' && a.repoRelativePath.length > 0, 'repoRelativePath must be non-empty string');
  assert(!a.repoRelativePath.startsWith('/'), 'repoRelativePath must not start with /');
  assert(!a.repoRelativePath.includes('..'), 'repoRelativePath must not contain ..');

  // 2. byteRange
  assert(Array.isArray(a.byteRange) && a.byteRange.length === 2, 'byteRange must be [start, end]');
  const [start, end] = a.byteRange;
  assert(start >= 0, `startByte (${start}) must be >= 0`);
  assert(end >= start, `endByte (${end}) must be >= startByte (${start})`);
  assert(end <= fileBytes.length, `endByte (${end}) must be <= fileBytes.length (${fileBytes.length})`);

  // 3. fileHash
  assert.strictEqual(a.fileHash, sha256Hex(fileBytes.toString('utf8')), 'fileHash must match sha256 of file');

  // 4. spanHash
  const spanBytes = fileBytes.subarray(start, end).toString('utf8');
  assert.strictEqual(a.spanHash, sha256Hex(spanBytes), 'spanHash must match sha256 of span bytes');

  // 5. enclosingSymbolPath
  assert(typeof a.enclosingSymbolPath === 'string', 'enclosingSymbolPath must be string');
  assert(
    /^[A-Za-z_$][A-Za-z0-9_$]*(\.[A-Za-z_$][A-Za-z0-9_$]*)*$/.test(a.enclosingSymbolPath),
    `enclosingSymbolPath '${a.enclosingSymbolPath}' must match schema pattern`
  );

  // 6. canonicalAstFingerprint
  assert.strictEqual(a.canonicalAstFingerprint, canonicalAstFingerprint(spanBytes), 'canonicalAstFingerprint must match');

  // 7. symbolRange (optional)
  if (a.symbolRange) {
    assert(Array.isArray(a.symbolRange) && a.symbolRange.length === 2, 'symbolRange must be [start, end]');
    assert(a.symbolRange[0] >= 0 && a.symbolRange[1] >= a.symbolRange[0] && a.symbolRange[1] <= fileBytes.length);
  }
}

function run() {
  console.log('--- Test Suite: Frontend Slicing & Call Chaining Resolution ---');

  // =========================================================================
  // 1. Direct Unit Tests: extractStatements Multi-Dot Chaining
  // =========================================================================
  const chainSnippet = `
function testChaining() {
  if (!isValid) return;
  await api.v1.auth.login(credentials);
  const user = await client.users.create({ name: 'Alice' });
  const invoice = await service.billing.invoices.pay(invoiceId);
  const { login } = useAuth();
  const [ mutate ] = useMutation();
  this.state = { count: 1 };
  window.analytics.track('event');
}
`;
  const bodyStart = chainSnippet.indexOf('{') + 1;
  const bodyEnd = chainSnippet.lastIndexOf('}');
  const bodyText = chainSnippet.substring(bodyStart, bodyEnd);
  const stmts = extractStatements(bodyText, bodyStart, chainSnippet);
  assert.strictEqual(stmts.length, 8, 'Must extract 8 statements from chainSnippet');

  // Stmt 0: guard
  assert.strictEqual(stmts[0].type, 'guard');
  assert.strictEqual(stmts[0].guardCondition, '!isValid');

  // Stmt 1: api.v1.auth.login
  assert.strictEqual(stmts[1].type, 'call');
  assert.strictEqual(stmts[1].receiver, 'api.v1.auth');
  assert.strictEqual(stmts[1].methodName, 'login');

  // Stmt 2: client.users.create
  assert.strictEqual(stmts[2].type, 'call');
  assert.strictEqual(stmts[2].receiver, 'client.users');
  assert.strictEqual(stmts[2].methodName, 'create');

  // Stmt 3: service.billing.invoices.pay
  assert.strictEqual(stmts[3].type, 'call');
  assert.strictEqual(stmts[3].receiver, 'service.billing.invoices');
  assert.strictEqual(stmts[3].methodName, 'pay');

  // Stmt 4: const { login } = useAuth()
  assert.strictEqual(stmts[4].type, 'call');
  assert.strictEqual(stmts[4].receiver, '');
  assert.strictEqual(stmts[4].methodName, 'useAuth');

  // Stmt 5: const [ mutate ] = useMutation()
  assert.strictEqual(stmts[5].type, 'call');
  assert.strictEqual(stmts[5].receiver, '');
  assert.strictEqual(stmts[5].methodName, 'useMutation');

  // Stmt 6: mutation
  assert.strictEqual(stmts[6].type, 'mutation');

  // Stmt 7: window.analytics.track
  assert.strictEqual(stmts[7].type, 'call');
  assert.strictEqual(stmts[7].receiver, 'window.analytics');
  assert.strictEqual(stmts[7].methodName, 'track');

  // =========================================================================
  // 2. Direct Unit Tests: findHookBinding helper
  // =========================================================================
  const hookCode = `
const { login, logout } = useAuth();
const { mutate: doMutation } = useMutation();
const [ user, setUser ] = useState(null);
const cartApi = useCart();
`;
  assert.strictEqual(findHookBinding(hookCode, 'login'), 'useAuth');
  assert.strictEqual(findHookBinding(hookCode, 'logout'), 'useAuth');
  assert.strictEqual(findHookBinding(hookCode, 'doMutation'), 'useMutation');
  assert.strictEqual(findHookBinding(hookCode, 'user'), 'useState');
  assert.strictEqual(findHookBinding(hookCode, 'cartApi'), 'useCart');
  assert.strictEqual(findHookBinding(hookCode, 'nonExistent'), null);

  // =========================================================================
  // 3. Integration Test: Functional Component Nested Handler Slicing
  // =========================================================================
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-fe-chain-'));
  try {
    const srcDir = path.join(tmpDir, 'src');
    fs.mkdirSync(path.join(srcDir, 'pages'), { recursive: true });
    fs.mkdirSync(path.join(srcDir, 'services'), { recursive: true });
    fs.mkdirSync(path.join(srcDir, 'hooks'), { recursive: true });
    fs.mkdirSync(path.join(srcDir, 'lib'), { recursive: true });

    // 3.1 Create React Functional Component with nested handleSubmit
    const loginPageTsx = `
import React, { useState } from 'react';
import { AuthService } from '../services/AuthService';

export const LoginPage = () => {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!email || !password) {
      return;
    }
    await this.authService.login(email, password);
  };

  return (
    <form onSubmit={handleSubmit}>
      <input value={email} onChange={e => setEmail(e.target.value)} />
      <button type="submit">Submit</button>
    </form>
  );
};
`;
    fs.writeFileSync(path.join(srcDir, 'pages', 'LoginPage.tsx'), loginPageTsx, 'utf8');

    // 3.2 Create AuthService
    const authServiceTs = `
export class AuthService {
  async login(email: string, pass: string) {
    if (!email.includes('@')) {
      throw new Error('Invalid email');
    }
    this.state = { authenticated: true };
  }
}
`;
    fs.writeFileSync(path.join(srcDir, 'services', 'AuthService.ts'), authServiceTs, 'utf8');

    const res1 = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-fe-001',
      entrySymbolPath: 'src/pages/LoginPage.tsx#LoginPage.handleSubmit',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res1.candidateId, 'cand-fe-001');
    assert.strictEqual(res1.language, 'typescript');
    assert.strictEqual(res1.truncated, false);
    assert.strictEqual(res1.visitedCycleDetected, false);

    // Verify steps sliced directly from nested handleSubmit
    assert(res1.steps.length >= 2, `Expected >= 2 steps from handleSubmit, got ${res1.steps.length}`);
    assert.strictEqual(res1.steps[0].symbolPath, 'LoginPage.handleSubmit');
    assert.strictEqual(res1.steps[0].kind, 'guard');
    assert.strictEqual(res1.steps[0].guardCondition, '!email || !password');

    // Verify 6-field anchor compliance on all steps
    const loginBytes = Buffer.from(loginPageTsx, 'utf8');
    const authBytes = Buffer.from(authServiceTs, 'utf8');
    for (const step of res1.steps) {
      const bytes = step.anchor.repoRelativePath.includes('LoginPage') ? loginBytes : authBytes;
      validateStepAnchor(step, bytes);
    }

    // =========================================================================
    // 4. Integration Test: Multi-Dot Chained Call Extraction & Resolution
    // =========================================================================
    const userProfileTsx = `
import { api } from '../services/api';

export const UserProfile = () => {
  const handleUpdate = async (userData: any) => {
    if (!userData.id) {
      return;
    }
    await api.v1.users.update(userData.id, userData);
  };
};
`;
    fs.writeFileSync(path.join(srcDir, 'pages', 'UserProfile.tsx'), userProfileTsx, 'utf8');

    // api.ts that imports usersService
    const apiTs = `
import { UsersService } from './UsersService';

export const api = {
  v1: {
    users: UsersService,
  }
};
`;
    fs.writeFileSync(path.join(srcDir, 'services', 'api.ts'), apiTs, 'utf8');

    const usersServiceTs = `
export class UsersService {
  async update(id: string, data: any) {
    if (!id) return;
    this.state = { updated: true };
  }
}
`;
    fs.writeFileSync(path.join(srcDir, 'services', 'UsersService.ts'), usersServiceTs, 'utf8');

    const res2 = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-chain-001',
      entrySymbolPath: 'src/pages/UserProfile.tsx#UserProfile.handleUpdate',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res2.candidateId, 'cand-chain-001');
    assert(res2.steps.length >= 2, `Expected >= 2 steps from UserProfile.handleUpdate, got ${res2.steps.length}`);
    assert.strictEqual(res2.steps[0].symbolPath, 'UserProfile.handleUpdate');
    assert.strictEqual(res2.steps[0].kind, 'guard');

    // Verify cross-file resolution of api.v1.users.update to UsersService.update
    const edgeTargets = res2.edges.map(e => e.toSymbolPath);
    assert(
      edgeTargets.some(t => t.includes('UsersService.update')),
      `Must resolve edge to UsersService.update, found: ${JSON.stringify(edgeTargets)}`
    );

    const userProfileBytes = Buffer.from(userProfileTsx, 'utf8');
    const usersBytes = Buffer.from(usersServiceTs, 'utf8');
    for (const step of res2.steps) {
      const bytes = step.anchor.repoRelativePath.includes('UserProfile') ? userProfileBytes : usersBytes;
      validateStepAnchor(step, bytes);
    }

    // =========================================================================
    // 5. Integration Test: Destructured Hook Invocation Resolution
    // =========================================================================
    const checkoutViewTsx = `
import React from 'react';
import { useAuth } from '../hooks/useAuth';

export const CheckoutView = () => {
  const { login, logout } = useAuth();

  const handleCheckout = async () => {
    if (!cart.hasItems) {
      return;
    }
    await login('user@codeflow.dev', 'password');
  };
};
`;
    fs.writeFileSync(path.join(srcDir, 'pages', 'CheckoutView.tsx'), checkoutViewTsx, 'utf8');

    const useAuthTs = `
import { AuthClient } from '../lib/AuthClient';

export const useAuth = () => {
  const login = async (email: string, pass: string) => {
    if (!email) return;
    await this.authClient.authenticate(email, pass);
  };
  return { login };
};
`;
    fs.writeFileSync(path.join(srcDir, 'hooks', 'useAuth.ts'), useAuthTs, 'utf8');

    const authClientTs = `
export class AuthClient {
  async authenticate(email: string, pass: string) {
    this.state = { token: 'jwt-auth-token' };
  }
}
`;
    fs.writeFileSync(path.join(srcDir, 'lib', 'AuthClient.ts'), authClientTs, 'utf8');

    const res3 = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-hook-001',
      entrySymbolPath: 'src/pages/CheckoutView.tsx#CheckoutView.handleCheckout',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res3.candidateId, 'cand-hook-001');
    assert(res3.steps.length >= 2, `Expected >= 2 steps from CheckoutView, got ${res3.steps.length}`);
    assert.strictEqual(res3.steps[0].symbolPath, 'CheckoutView.handleCheckout');

    // Verify edge resolution via destructured binding
    const hookEdges = res3.edges.map(e => e.toSymbolPath);
    assert(
      hookEdges.some(t => t.includes('useAuth')),
      `Must resolve destructured login call to useAuth, found: ${JSON.stringify(hookEdges)}`
    );

    const checkoutBytes = Buffer.from(checkoutViewTsx, 'utf8');
    const hookBytes = Buffer.from(useAuthTs, 'utf8');
    for (const step of res3.steps) {
      const bytes = step.anchor.repoRelativePath.includes('CheckoutView') ? checkoutBytes : hookBytes;
      validateStepAnchor(step, bytes);
    }

    // =========================================================================
    // 6. Integration Test: Graceful Fallback on Dynamic Invocations
    // =========================================================================
    const dynamicTs = `
export class DynamicHandler {
  async runDynamic(pluginName: string, payload: any) {
    if (!pluginName) return;
    const fn = (window as any)[pluginName];
    if (fn) {
      await fn(payload);
    }
    await (globalThis as any).plugins[pluginName].execute();
  }
}
`;
    fs.writeFileSync(path.join(srcDir, 'lib', 'DynamicHandler.ts'), dynamicTs, 'utf8');

    const res4 = sliceFlow({
      repoRoot: tmpDir,
      candidateId: 'cand-dyn-001',
      entrySymbolPath: 'src/lib/DynamicHandler.ts#DynamicHandler.runDynamic',
      opts: { maxDepth: 5 },
    });

    assert.strictEqual(res4.candidateId, 'cand-dyn-001');
    assert.strictEqual(res4.truncated, false);
    assert(res4.steps.length >= 1, 'Must emit steps without crashing');
    
    // Dynamic edge should have resolutionStatus = 'unresolved_dynamic'
    const dynEdges = res4.edges.filter(e => e.kind === 'unknown_edge');
    assert(dynEdges.length >= 1, 'Must emit unknown_edge for unresolvable dynamic target');
    assert.strictEqual(dynEdges[0].resolutionStatus, 'unresolved_dynamic');

    const dynBytes = Buffer.from(dynamicTs, 'utf8');
    for (const step of res4.steps) {
      validateStepAnchor(step, dynBytes);
    }

    console.log('✓ All Frontend Slicing & Call Chaining Resolution tests passed.');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }
}

module.exports = { run };

if (require.main === module) {
  run();
}
