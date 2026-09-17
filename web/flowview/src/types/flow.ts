export type ArchitectureLayer =
  | 'presentation'
  | 'page'
  | 'ui'
  | 'controller'
  | 'usecase'
  | 'application'
  | 'domain'
  | 'data'
  | 'repository'
  | 'infra'
  | 'external';

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
  stepId: string;
  structuralIdentity?: string;
  ordinal?: number;
  name: string;
  technicalName?: string;
  layer: ArchitectureLayer | string;
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
  stepId: string;
  generationId?: string;
  snapshotId?: string;
  canonicalPath?: string;
  precision?: 'exact' | 'unavailable' | string;
  sourceLimitation?: string;
  displayedLines: DisplayedLine[];
}

import type { FlowSequence, FlowSequenceFrame } from './flow_sequence';
export type { FlowSequence, FlowSequenceFrame };

export interface FlowTaskViewData {
  viewId?: string;
  flowId?: string;
  request?: { request?: string; entrySymbol?: string; flowId?: string; domain?: string };
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
