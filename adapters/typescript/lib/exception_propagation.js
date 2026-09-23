'use strict';

// A direct, synchronous callee throw can enter the enclosing caller catch only
// when both invocation identities and the resolved call target prove the path.
// The caller may be async because a synchronous callee still throws at that
// call site. Awaited asynchronous callees use the separate relation below.
function calledThrowEdges(steps, edges, directThrowInvocations, caughtCallTargets, directThrowCallerInvocations) {
  return throwRecoveryEdges(steps, edges, directThrowInvocations, caughtCallTargets, directThrowCallerInvocations);
}

// An explicit throw in an async callee rejects the promise returned by that
// invocation. We connect it to a recovery only when the exact direct call is
// awaited inside the caller's try block. Calls that are merely made from an
// async function do not gain a failure relationship.
function awaitedCalledThrowEdges(steps, edges, asyncThrowInvocations, awaitedCaughtCallTargets) {
  return throwRecoveryEdges(steps, edges, asyncThrowInvocations, awaitedCaughtCallTargets, null);
}

function throwRecoveryEdges(steps, edges, eligibleCalleeInvocations, caughtCallTargets, eligibleCallerInvocations) {
  const byOrdinal = new Map(steps.map(step => [step.ordinal, step]));
  const invocations = new Map();
  const outgoing = new Map();
  for (const step of steps) {
    if (!invocations.has(step.invocationId)) invocations.set(step.invocationId, []);
    invocations.get(step.invocationId).push(step);
  }
  for (const edge of edges) {
    if (!outgoing.has(edge.stepOrdinal)) outgoing.set(edge.stepOrdinal, []);
    outgoing.get(edge.stepOrdinal).push(edge);
  }
  const result = [];
  for (const call of edges) {
    if (call.kind !== 'resolved_cross_file' || call.resolutionStatus !== 'resolved' || !call.targetStepOrdinal) continue;
    const caller = byOrdinal.get(call.stepOrdinal);
    const calleeEntry = byOrdinal.get(call.targetStepOrdinal);
    const catchTargetOrdinal = caughtCallTargets.get(call.stepOrdinal);
    if (!caller || !calleeEntry || !catchTargetOrdinal || !eligibleCalleeInvocations.has(calleeEntry.invocationId) || (eligibleCallerInvocations && !eligibleCallerInvocations.has(caller.invocationId))) continue;
    const catchTarget = byOrdinal.get(catchTargetOrdinal);
    if (!catchTarget || catchTarget.invocationId !== caller.invocationId || catchTarget.ordinal <= caller.ordinal) continue;
    for (const step of invocations.get(calleeEntry.invocationId) || []) {
      if (step.kind !== 'throw') continue;
      const localCompletion = (outgoing.get(step.ordinal) || []).some(edge => edge.kind === 'failure' || edge.kind === 'finally');
      if (localCompletion) continue;
      result.push({
        kind: 'failure',
        stepOrdinal: step.ordinal,
        targetStepOrdinal: catchTarget.ordinal,
        toSymbolPath: `${catchTarget.anchor.repoRelativePath}#${catchTarget.symbolPath}`,
        resolutionStatus: 'resolved',
        depth: call.depth,
      });
    }
  }
  return result;
}

module.exports = { calledThrowEdges, awaitedCalledThrowEdges };
