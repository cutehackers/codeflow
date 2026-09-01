'use strict';

const scannerQuotes = require('./scanner_quotes_strings.test');
const scannerRegex = require('./scanner_regex.test');
const scannerComments = require('./scanner_comments.test');
const scannerGenerics = require('./scanner_generics.test');
const scannerReactComponents = require('./scanner_react_components.test');
const sliceMultiline = require('./slice_multiline.test');
const sliceEmptyFallback = require('./slice_empty_fallback.test');
const sliceControlFlow = require('./slice_control_flow.test');
const sliceDagCycles = require('./slice_dag_cycles.test');
const sliceDepthLimits = require('./slice_depth_limits.test');
const secretRedaction = require('./secret_redaction.test');
const protocolWire = require('./protocol_wire.test');
const harvestDeterminism = require('./harvest_determinism.test');
const harvestFrontend = require('./harvest_frontend.test');
const sliceFrontendChaining = require('./slice_frontend_chaining.test');
const adversarialChallenge = require('./adversarial_challenge.test');

console.log('======================================================');
console.log('  Running TypeScript Adapter Comprehensive Test Suite ');
console.log('======================================================\n');

try {
  scannerQuotes.run();
  scannerRegex.run();
  scannerComments.run();
  scannerGenerics.run();
  scannerReactComponents.run();
  sliceMultiline.run();
  sliceEmptyFallback.run();
  sliceControlFlow.run();
  sliceDagCycles.run();
  sliceDepthLimits.run();
  secretRedaction.run();
  protocolWire.run();
  harvestDeterminism.run();
  harvestFrontend.run();
  sliceFrontendChaining.run();
  adversarialChallenge.run();

  console.log('\n======================================================');
  console.log('  ALL TypeScript Adapter Tests Passed Successfully!   ');
  console.log('======================================================');
} catch (err) {
  console.error('\n❌ Test Suite Failed with Error:');
  console.error(err);
  process.exit(1);
}
