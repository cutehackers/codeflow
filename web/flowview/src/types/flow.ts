export type DeltaKind =
  | 'added_behavior'
  | 'changed_rule'
  | 'removed_behavior'
  | 'evidence_updated'
  | 'structural_only'
  | 'ambiguous_move';

export interface DeltaChange {
  changeId?: string;
  kind: DeltaKind;
  targetStepId: string;
  summary: string;
  description?: string;
  evidenceRefs?: string[];
}

export interface SemanticDelta {
  status?: string;
  changes: DeltaChange[];
}

export interface StepAnchor {
  repoRelativePath?: string;
  enclosingSymbolPath?: string;
}

export interface Step {
  rules?: string[];
  assignmentSourceOrdinal?: number;
  callerStepOrdinal?: number;
  invocationId?: string;
  kind?: string;
  stepId: string;
  structuralIdentity?: string;
  ordinal?: number;
  name: string;
  technicalName?: string;
  layer?: string;
  description?: string;
  branch?: string;
  sideEffect?: string;
  stateDelta?: {
    before: string;
    after: string;
  };
  anchor?: StepAnchor;
}

export interface Edge {
  conditions?: Array<{stepId: string; outcome: 'truthy' | 'falsy' | 'nullish' | 'non_nullish'}>;
  fromStepId: string;
  toStepId: string;
  kind: 'call' | 'calls' | 'successor' | 'return' | 'branch' | 'error' | 'failure' | 'async' | string;
  resolutionStatus: 'resolved' | 'unresolved' | string;
  toSymbolPath?: string;
}

export interface SemanticMapSummary {
  requested?: string;
  current?: string;
}

export interface SemanticMapBasis {
  computedBasisId?: string;
  computedWorkspaceSnapshotId?: string;
  workspaceEpoch?: number;
}

export interface SemanticMap {
  generationId: string;
  computedBasisId?: string;
  validatedAgainstSnapshotId?: string;
  basis?: SemanticMapBasis;
  summary?: SemanticMapSummary;
  steps: Step[];
  edges: Edge[];
  unknowns?: Array<{ reason?: string; subject?: string } | string>;
}

export interface DisplayedLine {
  lineNumber: number;
  text: string;
  isHit?: boolean;
  isStruct?: boolean;
  selection?: {
    before: string;
    text: string;
    after: string;
  };
}

export interface FlowContext {
  sourceValidationStatus?: string;
  stepId: string;
  generationId?: string;
  snapshotId?: string;
  canonicalPath?: string;
  precision?: 'exact' | 'unavailable' | string;
  sourceLimitation?: string;
  statement?: {byteRange: [number, number]};
  displayedLines: DisplayedLine[];
}

export interface FlowEvidence {
  candidateId: string;
  entrySymbolPath: string;
  description: string;
  snapshotId: string;
  computedBasisId: string;
  sourceAnchor?: {
    repoRelativePath: string;
    byteRange: [number, number];
    fileHash: string;
    spanHash: string;
    enclosingSymbolPath: string;
    canonicalAstFingerprint: string;
  };
}

export interface FlowResolution {
  schemaVersion: number;
  status: 'resolved' | 'ambiguous' | 'unresolved' | 'analysis_failed' | string;
  rawRequest: string;
  candidateId?: string;
  entrySymbolPath?: string;
  flowId?: string;
  evidence?: FlowEvidence[];
  candidates?: FlowEvidence[];
  failure?: {
    reason?: string;
    retryAdvice?: string;
  };
}

export interface RequestedFlowFocus {
  symbolPath: string;
  reason: string;
  callStepId: string;
  entryStepId: string;
  snapshotId: string;
  evidenceRefs: string[];
}

export interface CodeReadingRelation {
  fromNodeId: string;
  toNodeId: string;
  kind: 'direct_call' | 'control_flow' | 'async_handoff' | 'conditional' | 'boundary';
  status: 'verified' | 'partial' | 'unknown';
  edgeIndexes: number[];
}

export interface CodeReadingNode {
  nodeId: string;
  stepId: string;
  frameId: string;
  symbol: string;
  sourceAnchor: {
    repoRelativePath: string;
    enclosingSymbolPath: string;
    byteRange: [number, number];
    fileHash: string;
    spanHash: string;
  };
  role: 'entry' | 'decision' | 'transform' | 'state_change' | 'effect' | 'handoff' | 'outcome' | 'boundary';
  requestPart: string;
  inclusionReason: string;
  previousNodeIds: string[];
  nextNodeIds: string[];
  status: 'verified' | 'partial' | 'unknown';
  evidenceRefs: string[];
  codeViewAvailable: boolean;
}

export interface CodeReadingPath {
  schemaId: string;
  schemaVersion: number;
  request: string;
  snapshotId: string;
  computedBasisId: string;
  nodes: CodeReadingNode[];
  relations: CodeReadingRelation[];
}

import type { FlowSequence, FlowSequenceFrame } from './flow_sequence';
export type { FlowSequence, FlowSequenceFrame };

export interface RequestedFlowBlock {
  blockId: string;
  repoRelativePath: string;
  symbolPath: string;
  lineRange: [number, number];
  stepIds: string[];
  highlights: Array<{stepId: string; lineRange: [number, number]; byteRange: [number, number]; lineSelections: Array<{lineNumber: number; startColumn: number; endColumn: number}>}>;
}

export interface RequestedFlowSegment {
  segmentId: string;
  purpose: string;
  inclusionReason: string;
  stepIds: string[];
  entryStepId: string;
  blocks: RequestedFlowBlock[];
}

export interface RequestedFlowCategory {
  categoryId: string;
  title: string;
  segmentIds: string[];
}

export interface RequestedFlowOverview {
  schemaId: string;
  schemaVersion: number;
  request: string;
  snapshotId: string;
  computedBasisId: string;
  categories?: RequestedFlowCategory[];
  segments: RequestedFlowSegment[];
  relations: Array<{derived?: boolean; fromSegmentId?: string; toSegmentId?: string; fromStepId: string; toStepId: string; kind: string; status: string; edgeIndexes: number[]; edges: Edge[]}>;
  boundaries: Array<{fromStepId: string; toStepId?: string; target?: string; reason: string}>;
}

export interface FlowTaskViewData {
  viewId?: string;
  flowId?: string;
  request?: { request?: string; entrySymbol?: string; flowId?: string; domain?: string };
  flowResolution?: FlowResolution;
  requestedFocus?: RequestedFlowFocus;
  requestedFlowOverview?: RequestedFlowOverview;
  requestedFlowOverviewLimitation?: string;
  codeReadingPath?: CodeReadingPath;
  codeReadingPathLimitation?: string;
  flowResolutionMissing?: boolean;
  resolutionLimitation?: string;
  sourceNotice?: string;
  sourceContextMissing?: boolean;
  needsReanalysis?: boolean;
  sourceFiles?: Record<string, DisplayedLine[]>;
  requestId?: string;
  semanticMap: SemanticMap;
  flowSequence?: FlowSequence;
  semanticDelta?: SemanticDelta;
  flowContexts?: Record<string, FlowContext>;
  unknowns?: Array<{ reason?: string; subject?: string } | string>;
  sampleImpacts?: Record<string, any>;
}
