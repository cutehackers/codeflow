import { samplePayload } from '../stores/sampleData';
import type { Edge } from '../types/flow';

export function conditionalData() {
  const data = samplePayload(1);
  const ids = ['root', 'nested', 'yes', 'no', 'joined'];
  data.semanticMap.steps = ids.map((stepId, i) => ({stepId, ordinal:i+1, name:stepId, layer:'application', kind: i < 2 ? 'branch' : 'call', invocationId:'root'}));
  const edge = (fromStepId:string, toStepId:string, outcome?:'truthy'|'falsy'):Edge => ({fromStepId,toStepId,kind:'control_flow',resolutionStatus:'resolved',...(outcome ? {conditions:[{stepId:fromStepId,outcome}]} : {})});
  data.semanticMap.edges = [edge('root','nested','truthy'),edge('root','no','falsy'),edge('nested','yes','truthy'),edge('nested','no','falsy'),edge('yes','joined'),edge('no','joined')];
  data.flowSequence!.frames = data.flowSequence!.frames.map((frame,i)=>({...frame,primaryStepRef:ids[i],stepRefs:[ids[i]]}));
  return data;
}

