'use strict';

// A return resumes the caller's existing expression continuation. This is a
// possible source connection, not a claim that every call completes normally.
function normalReturnEdges(steps, edges, synchronousInvocations, normalReturnCallerInvocations, normalReturnExcludedOrdinals, prevailingFinallyReturnOrdinals = new Set()) {
  const byOrdinal = new Map(steps.map(step => [step.ordinal, step]));
  const outgoing = new Map();
  const invocations = new Map();
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
    const entry = byOrdinal.get(call.targetStepOrdinal);
    if (!caller || normalReturnExcludedOrdinals.has(caller.ordinal) || !entry || !normalReturnCallerInvocations.has(caller.invocationId) || !synchronousInvocations.has(entry.invocationId)) continue;
    const members = invocations.get(entry.invocationId) || [];
    if (members.some(step => (outgoing.get(step.ordinal) || []).some(edge => edge.resolutionStatus !== 'resolved' || edge.kind === 'boundary_call'))) continue;
    const continuations = (outgoing.get(caller.ordinal) || []).filter(edge => edge.kind === 'control_flow');
    if (continuations.length !== 1 || continuations[0].conditions?.length) continue;
    const continuation = continuations[0];
    const target = byOrdinal.get(continuation.targetStepOrdinal);
    if (!target || target.invocationId !== caller.invocationId) continue;
    if ((outgoing.get(target.ordinal) || []).some(edge => edge.kind === 'unknown_edge' && edge.resolutionStatus !== 'resolved')) continue;
    const hasPrevailingFinallyReturn = members.some(step => prevailingFinallyReturnOrdinals.has(step.ordinal));
    for (const step of members) {
      if (step.kind !== 'return' || (hasPrevailingFinallyReturn && !prevailingFinallyReturnOrdinals.has(step.ordinal))) continue;
      result.push({kind:'return', stepOrdinal:step.ordinal, targetStepOrdinal:target.ordinal, toSymbolPath:`${target.anchor.repoRelativePath}#${target.symbolPath}`, resolutionStatus:'resolved', depth:call.depth});
    }
  }
  return result;
}
module.exports = { normalReturnEdges };
