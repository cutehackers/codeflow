export type StoryboardRole = 'entry' | 'decision' | 'process' | 'effect' | 'result' | 'boundary';

export interface CollapsedDetail {
  count: number;
  reason: string;
}

export interface StoryboardFrame {
  frameId: string;
  ordinal: number;
  role: StoryboardRole;
  title: string;
  narrative?: string;
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
}

export interface Storyboard {
  schemaId: string;
  schemaVersion: number;
  generationId: string;
  computedBasisId: string;
  snapshotId: string;
  frames: StoryboardFrame[];
}
