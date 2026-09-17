export type FlowSequenceRole = 'entry' | 'decision' | 'process' | 'effect' | 'result' | 'boundary';

export interface CollapsedDetail {
  count: number;
  reason: string;
}

export interface FlowSequenceFrame {
  frameID: string;
  ordinal: number;
  role: FlowSequenceRole;
  title: string;
  text?: string;
  technicalAnchor?: string;
  stepRefs: string[];
  primaryStepRef: string;
  sourceAnchor?: {
    repoRelativePath?: string;
    enclosingSymbolPath?: string;
  };
  condition?: string;
  outcomes?: string[];
  collapsedDetail?: CollapsedDetail;
  architecture?: string;
  status: 'verified' | 'partial' | 'unknown';
  frameMatchKey: string;
  isRecursion?: boolean;
}

export interface FlowSequence {
  schemaId: string;
  schemaVersion: number;
  generationId: string;
  computedBasisId: string;
  flowID: string;
  snapshotID: string;
  frames: FlowSequenceFrame[];
}
