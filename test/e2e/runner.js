'use strict';

/**
 * CodeFlow E2E Test Suite Runner (Node.js)
 * Executes Tier 1, Tier 2, Tier 3, and Tier 4 tests directly using Node.js.
 */

const fs = require('fs');
const path = require('path');
const { harvestCandidates } = require('../../adapters/typescript/lib/harvest');
const { sliceFlow } = require('../../adapters/typescript/lib/slice');
const { sha256Hex } = require('../../adapters/typescript/lib/sha256');

const repoRoot = path.resolve(__dirname, '../..');
const fixturesDir = path.join(__dirname, '../fixtures');

let totalTests = 0;
let passedTests = 0;
let failedTests = 0;

function assert(condition, message) {
  totalTests++;
  if (!condition) {
    failedTests++;
    console.error(`  ❌ FAILED: ${message}`);
    throw new Error(message);
  } else {
    passedTests++;
    console.log(`  ✓ PASS: ${message}`);
  }
}

function testSection(title, fn) {
  console.log(`\n======================================================`);
  console.log(`  ${title}`);
  console.log(`======================================================`);
  try {
    fn();
  } catch (err) {
    console.error(`Section error: ${err.message}`);
  }
}

// ---------------------------------------------------------------------------
// TIER 1: Feature Coverage Tests
// ---------------------------------------------------------------------------
testSection('Tier 1: Feature Coverage (Next.js, FSD, React SPA, Clean Arch)', () => {
  // Feature 1-4: Candidate Harvesting & Marker Classification
  const nextjsHarvest = harvestCandidates({ repoRoot: path.join(fixturesDir, 'nextjs-app-fixture') });
  assert(nextjsHarvest.candidates.length >= 3, `Next.js harvested >= 3 candidates (got ${nextjsHarvest.candidates.length})`);
  
  const quickCheckout = nextjsHarvest.candidates.find(c => c.entrySymbolPath.includes('handleQuickCheckout'));
  assert(quickCheckout !== undefined, 'Next.js found HomePage.handleQuickCheckout candidate');
  assert(quickCheckout && quickCheckout.triggerClass === 'user_action', 'HomePage.handleQuickCheckout classified as user_action');

  const fsdHarvest = harvestCandidates({ repoRoot: path.join(fixturesDir, 'fsd-fixture') });
  assert(fsdHarvest.candidates.length >= 3, `FSD harvested >= 3 candidates (got ${fsdHarvest.candidates.length})`);
  const onLike = fsdHarvest.candidates.find(c => c.entrySymbolPath.includes('onLikeClick'));
  assert(onLike !== undefined, 'FSD found FeedList.onLikeClick candidate');
  assert(onLike && onLike.triggerClass === 'user_action', 'FeedList.onLikeClick classified as user_action');

  const spaHarvest = harvestCandidates({ repoRoot: path.join(fixturesDir, 'react-spa-fixture') });
  assert(spaHarvest.candidates.length >= 3, `React SPA harvested >= 3 candidates (got ${spaHarvest.candidates.length})`);
  const loginSubmit = spaHarvest.candidates.find(c => c.entrySymbolPath.includes('handleSubmit'));
  assert(loginSubmit !== undefined, 'React SPA found LoginForm.handleSubmit candidate');

  const cleanHarvest = harvestCandidates({ repoRoot: path.join(fixturesDir, 'clean-arch-fixture') });
  assert(cleanHarvest.candidates.length >= 2, `Clean Arch harvested >= 2 candidates (got ${cleanHarvest.candidates.length})`);
});

// ---------------------------------------------------------------------------
// TIER 2: Boundary & Corner Cases
// ---------------------------------------------------------------------------
testSection('Tier 2: Boundary & Corner Cases', () => {
  // 1. Empty handler slicing
  const emptySlice = sliceFlow({
    repoRoot: path.join(fixturesDir, 'react-spa-fixture'),
    candidateId: 'cand-0000000000000001',
    entrySymbolPath: 'src/components/LoginForm.tsx#LoginForm.nonExistent',
    opts: { maxDepth: 1 },
  });
  assert(emptySlice.steps.length >= 1, 'Empty/unknown fallback generates >= 1 step');

  // 2. Chained slicing in Next.js
  const nextSlice = sliceFlow({
    repoRoot: path.join(fixturesDir, 'nextjs-app-fixture'),
    candidateId: 'cand-0000000000000002',
    entrySymbolPath: 'app/page.tsx#HomePage.handleQuickCheckout',
    opts: { maxDepth: 3 },
  });
  assert(nextSlice.steps.length >= 1, 'HomePage.handleQuickCheckout sliced successfully');

  // 3. Anchor 6-field verification
  for (const step of nextSlice.steps) {
    const a = step.anchor;
    assert(typeof a.repoRelativePath === 'string' && a.repoRelativePath.length > 0, 'repoRelativePath non-empty');
    assert(Array.isArray(a.byteRange) && a.byteRange.length === 2 && a.byteRange[0] <= a.byteRange[1], 'byteRange valid');
    assert(typeof a.fileHash === 'string' && a.fileHash.length === 64, 'fileHash 64-char hex');
    assert(typeof a.spanHash === 'string' && a.spanHash.length === 64, 'spanHash 64-char hex');
    assert(typeof a.enclosingSymbolPath === 'string' && a.enclosingSymbolPath.length > 0, 'enclosingSymbolPath non-empty');
    assert(typeof a.canonicalAstFingerprint === 'string' && a.canonicalAstFingerprint.length === 64, 'canonicalAstFingerprint 64-char hex');
  }
});

// ---------------------------------------------------------------------------
// TIER 3: Cross-Feature Pairwise Combinations
// ---------------------------------------------------------------------------
testSection('Tier 3: Pairwise Cross-Feature Combinations', () => {
  const combinations = [
    { fix: 'nextjs-app-fixture', file: 'app/page.tsx', sym: 'HomePage.handleQuickCheckout', depth: 3 },
    { fix: 'fsd-fixture', file: 'src/widgets/FeedList.tsx', sym: 'FeedList.onLikeClick', depth: 3 },
    { fix: 'react-spa-fixture', file: 'src/components/LoginForm.tsx', sym: 'LoginForm.handleSubmit', depth: 3 },
    { fix: 'clean-arch-fixture', file: 'src/presentation/controllers/UserController.ts', sym: 'UserController.handleCreateUser', depth: 2 },
  ];

  for (const comb of combinations) {
    const res = sliceFlow({
      repoRoot: path.join(fixturesDir, comb.fix),
      candidateId: 'cand-0000000000000003',
      entrySymbolPath: `${comb.file}#${comb.sym}`,
      opts: { maxDepth: comb.depth },
    });
    assert(res.steps.length >= 1, `Combination ${comb.fix} -> ${comb.sym} sliced >= 1 step`);
  }
});

// ---------------------------------------------------------------------------
// TIER 4: Real-World Scenarios
// ---------------------------------------------------------------------------
testSection('Tier 4: Real-World Application Scenarios', () => {
  // Scenario 1: Next.js E-Commerce
  const s1 = sliceFlow({
    repoRoot: path.join(fixturesDir, 'nextjs-app-fixture'),
    candidateId: 'cand-0000000000000011',
    entrySymbolPath: 'app/page.tsx#HomePage.handleQuickCheckout',
    opts: { maxDepth: 4 },
  });
  assert(s1.steps.length >= 1, 'Scenario 1: Next.js E-Commerce flow sliced');

  // Scenario 2: FSD Feed Interaction
  const s2 = sliceFlow({
    repoRoot: path.join(fixturesDir, 'fsd-fixture'),
    candidateId: 'cand-0000000000000012',
    entrySymbolPath: 'src/widgets/FeedList.tsx#FeedList.onLikeClick',
    opts: { maxDepth: 4 },
  });
  assert(s2.steps.length >= 1, 'Scenario 2: FSD Feed flow sliced');

  // Scenario 3: React SPA Auth
  const s3 = sliceFlow({
    repoRoot: path.join(fixturesDir, 'react-spa-fixture'),
    candidateId: 'cand-0000000000000013',
    entrySymbolPath: 'src/components/LoginForm.tsx#LoginForm.handleSubmit',
    opts: { maxDepth: 4 },
  });
  assert(s3.steps.length >= 1, 'Scenario 3: React SPA Auth flow sliced');

  // Scenario 4: Clean Arch Service
  const s4 = sliceFlow({
    repoRoot: path.join(fixturesDir, 'clean-arch-fixture'),
    candidateId: 'cand-0000000000000014',
    entrySymbolPath: 'src/presentation/controllers/UserController.ts#UserController.handleCreateUser',
    opts: { maxDepth: 3 },
  });
  assert(s4.steps.length >= 1, 'Scenario 4: Clean Arch UserController flow sliced');
});

console.log(`\n======================================================`);
console.log(`  E2E Test Runner Summary`);
console.log(`  Total Tests:  ${totalTests}`);
console.log(`  Passed Tests: ${passedTests}`);
console.log(`  Failed Tests: ${failedTests}`);
console.log(`======================================================`);

if (failedTests > 0) {
  process.exit(1);
}
