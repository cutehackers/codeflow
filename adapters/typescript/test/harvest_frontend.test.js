'use strict';

const assert = require('assert');
const fs = require('fs');
const path = require('path');
const os = require('os');
const { harvestCandidates, classifyMarker } = require('../lib/harvest');

function run() {
  console.log('--- Test Suite: Frontend Candidate Harvesting & Trigger Classification ---');

  // 1. Unit Tests for classifyMarker
  // UI Actions (user_action / route_callback)
  const ui1 = classifyMarker('LoginPage', 'handleSubmit', '');
  assert.deepStrictEqual(ui1, { triggerClass: 'user_action', markerKind: 'route_callback' });

  const ui2 = classifyMarker('Button', 'onClick', '');
  assert.deepStrictEqual(ui2, { triggerClass: 'user_action', markerKind: 'route_callback' });

  const ui3 = classifyMarker('Header', 'handleGoogleLogin', '');
  assert.deepStrictEqual(ui3, { triggerClass: 'user_action', markerKind: 'route_callback' });

  const ui4 = classifyMarker('CheckoutForm', 'onFormSubmit', '');
  assert.deepStrictEqual(ui4, { triggerClass: 'user_action', markerKind: 'route_callback' });

  const ui5 = classifyMarker('AuthCard', 'submitAction', '');
  assert.deepStrictEqual(ui5, { triggerClass: 'user_action', markerKind: 'route_callback' });

  // State Mutation Hooks & Mutators (state_transition / notifier_method)
  const hook1 = classifyMarker('useAuth', 'useAuth', '');
  assert.deepStrictEqual(hook1, { triggerClass: 'state_transition', markerKind: 'notifier_method' });

  const hook2 = classifyMarker('useCart', 'useCart', '');
  assert.deepStrictEqual(hook2, { triggerClass: 'state_transition', markerKind: 'notifier_method' });

  const hook3 = classifyMarker('useUserMutation', 'useUserMutation', '');
  assert.deepStrictEqual(hook3, { triggerClass: 'state_transition', markerKind: 'notifier_method' });

  const mut1 = classifyMarker('CartStore', 'updateCart', '');
  assert.deepStrictEqual(mut1, { triggerClass: 'state_transition', markerKind: 'notifier_method' });

  const mut2 = classifyMarker('UserSlice', 'setLoading', '');
  assert.deepStrictEqual(mut2, { triggerClass: 'state_transition', markerKind: 'notifier_method' });

  // Next.js Route Handlers (system_event / lifecycle_callback)
  const routePost = classifyMarker('Route', 'POST', '');
  assert.deepStrictEqual(routePost, { triggerClass: 'system_event', markerKind: 'lifecycle_callback' });

  const routeGet = classifyMarker('Route', 'GET', '');
  assert.deepStrictEqual(routeGet, { triggerClass: 'system_event', markerKind: 'lifecycle_callback' });

  const routeDelete = classifyMarker('Route', 'DELETE', '');
  assert.deepStrictEqual(routeDelete, { triggerClass: 'system_event', markerKind: 'lifecycle_callback' });

  // Next.js Server Actions (system_event / lifecycle_callback)
  const serverAction = classifyMarker('AuthActions', 'loginAction', '', "'use server';\n await db.user.findFirst();");
  assert.deepStrictEqual(serverAction, { triggerClass: 'system_event', markerKind: 'lifecycle_callback' });

  // Clean Architecture UseCases (use_case_invocation / usecase_call)
  const usecase1 = classifyMarker('LoginUseCase', 'execute', '');
  assert.deepStrictEqual(usecase1, { triggerClass: 'use_case_invocation', markerKind: 'usecase_call' });

  const usecase2 = classifyMarker('OrderInteractor', 'invoke', '');
  assert.deepStrictEqual(usecase2, { triggerClass: 'use_case_invocation', markerKind: 'usecase_call' });

  // Lifecycle Hooks & System Events (system_event / lifecycle_callback)
  const effectHook = classifyMarker('UserProfile', 'useEffect', '');
  assert.deepStrictEqual(effectHook, { triggerClass: 'system_event', markerKind: 'lifecycle_callback' });

  const mountLifecycle = classifyMarker('LegacyView', 'componentDidMount', '');
  assert.deepStrictEqual(mountLifecycle, { triggerClass: 'system_event', markerKind: 'lifecycle_callback' });

  // Non-entry Utility Hooks (must return null)
  assert.strictEqual(classifyMarker('UserProfile', 'useMemo', ''), null);
  assert.strictEqual(classifyMarker('UserProfile', 'useRef', ''), null);
  assert.strictEqual(classifyMarker('UserProfile', 'useCallback', ''), null);
  assert.strictEqual(classifyMarker('UserProfile', 'useId', ''), null);

  // Server Actions in file with 'use server' (even without Action suffix)
  const serverActionWithoutSuffix = classifyMarker('userActions', 'createUser', "'use server';\n export async function createUser() {}", '');
  assert.deepStrictEqual(serverActionWithoutSuffix, { triggerClass: 'system_event', markerKind: 'lifecycle_callback' });

  // 2. Integration Test: Full Repository Candidate Harvesting & Schema Compliance
  const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'codeflow-fe-harvest-'));
  try {
    fs.writeFileSync(path.join(tmpDir, 'package.json'), JSON.stringify({
      name: '@test/frontend-app',
      version: '1.0.0',
    }), 'utf8');

    // Next.js App Router Page with nested handleSubmit
    const appDir = path.join(tmpDir, 'src/app/login');
    fs.mkdirSync(appDir, { recursive: true });
    fs.writeFileSync(path.join(appDir, 'page.tsx'), `
import React from 'react';
import { useAuth } from '@/hooks/useAuth';

export const LoginPage = () => {
  const { login } = useAuth();

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    await login('test@example.com', 'secret');
  };

  const handleReset = () => {
    console.log('reset');
  };

  return <form onSubmit={handleSubmit} />;
};
`, 'utf8');

    // Next.js API Route Handler
    const apiDir = path.join(tmpDir, 'src/app/api/auth');
    fs.mkdirSync(apiDir, { recursive: true });
    fs.writeFileSync(path.join(apiDir, 'route.ts'), `
import { NextResponse } from 'next/server';

export async function POST(req: Request) {
  return NextResponse.json({ success: true });
}

export async function GET() {
  return NextResponse.json({ status: 'ok' });
}
`, 'utf8');

    // Next.js Server Action
    const actionDir = path.join(tmpDir, 'src/actions');
    fs.mkdirSync(actionDir, { recursive: true });
    fs.writeFileSync(path.join(actionDir, 'authActions.ts'), `
'use server';

export async function submitLoginAction(formData: FormData) {
  return { ok: true };
}
`, 'utf8');

    // Custom hook with mutation
    const hookDir = path.join(tmpDir, 'src/hooks');
    fs.mkdirSync(hookDir, { recursive: true });
    fs.writeFileSync(path.join(hookDir, 'useAuth.ts'), `
export const useAuth = () => {
  const mutateLogin = async (email: string) => {
    return true;
  };
  return { mutateLogin };
};
`, 'utf8');

    const harvest = harvestCandidates({ repoRoot: tmpDir });
    assert(harvest.candidates.length >= 6, `Expected at least 6 candidates, got ${harvest.candidates.length}`);

    // Verify schema rules on every harvested candidate
    const allowedTriggerClasses = new Set(['user_action', 'use_case_invocation', 'system_event', 'state_transition']);
    const allowedMarkerKinds = new Set(['notifier_method', 'bloc_handler', 'route_callback', 'usecase_call', 'lifecycle_callback', 'state_mutation']);
    const candIdPattern = /^cand-[a-z0-9]{8,32}$/;
    const entrySymbolPattern = /^[^#\s]+#[A-Za-z_$][A-Za-z0-9_$]*(\.[A-Za-z_$][A-Za-z0-9_$]*)*$/;

    for (const c of harvest.candidates) {
      assert(candIdPattern.test(c.candidateId), `Invalid candidateId: ${c.candidateId}`);
      assert(allowedTriggerClasses.has(c.triggerClass), `Invalid triggerClass: ${c.triggerClass}`);
      assert(allowedMarkerKinds.has(c.markerKind), `Invalid markerKind: ${c.markerKind}`);
      assert(entrySymbolPattern.test(c.entrySymbolPath), `Invalid entrySymbolPath: ${c.entrySymbolPath}`);

      assert(c.intentSignals, 'intentSignals required');
      assert.strictEqual(typeof c.intentSignals.className, 'string');
      assert(c.intentSignals.className.length > 0);
      assert.strictEqual(typeof c.intentSignals.derivedName, 'string');
      assert(c.intentSignals.derivedName.length > 0);
      assert.strictEqual(c.intentSignals.packageName, '@test/frontend-app');

      assert(typeof c.score === 'number' && c.score >= 0 && c.score <= 1);
      assert(Number.isInteger(c.fanIn) && c.fanIn >= 0);
      assert.strictEqual(typeof c.boundaryReachable, 'boolean');
      assert(typeof c.rootEquivalenceKey === 'string' && c.rootEquivalenceKey.length > 0);
      assert(Number.isInteger(c.tieBreakRank) && c.tieBreakRank >= 0);
    }

    // Verify specific candidates discovered
    const loginSubmit = harvest.candidates.find(c => c.entrySymbolPath.includes('LoginPage.handleSubmit'));
    assert(loginSubmit, 'LoginPage.handleSubmit candidate must exist');
    assert.strictEqual(loginSubmit.triggerClass, 'user_action');
    assert.strictEqual(loginSubmit.markerKind, 'route_callback');
    assert.strictEqual(loginSubmit.intentSignals.derivedName, 'Handle submit');

    const routePostCand = harvest.candidates.find(c => c.entrySymbolPath.includes('route.ts#POST'));
    assert(routePostCand, 'POST route handler candidate must exist');
    assert.strictEqual(routePostCand.triggerClass, 'system_event');
    assert.strictEqual(routePostCand.markerKind, 'lifecycle_callback');

    const serverActCand = harvest.candidates.find(c => c.entrySymbolPath.includes('submitLoginAction'));
    assert(serverActCand, 'submitLoginAction candidate must exist');
    assert.strictEqual(serverActCand.triggerClass, 'system_event');

    const useAuthCand = harvest.candidates.find(c => c.entrySymbolPath.includes('useAuth'));
    assert(useAuthCand, 'useAuth candidate must exist');
    assert.strictEqual(useAuthCand.triggerClass, 'state_transition');
  } finally {
    fs.rmSync(tmpDir, { recursive: true, force: true });
  }

  console.log('✓ All Frontend Candidate Harvesting & Trigger Classification tests passed.');
}

module.exports = { run };

if (require.main === module) {
  run();
}
