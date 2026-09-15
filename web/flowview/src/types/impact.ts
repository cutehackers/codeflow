export interface ImpactCallerNode {
  symbolPath: string;
  name?: string;
  relationKind?: string;
  filePath?: string;
  depth?: number;
  path?: string[];
  evidenceRefs?: string[];
}

export interface StateMutationImpact {
  targetState: string;
  mutationKind?: string;
  terminalSymbolPath?: string;
  depth?: number;
}

export interface ExternalEffectImpact {
  effectKind: string;
  target?: string;
}

export interface TestImpact {
  testSymbolPath: string;
  testFile?: string;
  terminalSymbolPath?: string;
  depth?: number;
  broken?: boolean;
}

export interface DirectImpact {
  callers: ImpactCallerNode[];
  stateMutations?: StateMutationImpact[];
  externalEffects?: ExternalEffectImpact[];
  tests: TestImpact[];
}

export interface IndirectImpact {
  callers: ImpactCallerNode[];
  tests?: TestImpact[];
  maxDepth?: number;
  totalNodeCount?: number;
}

export interface ChangeImpactGraph {
  schemaId?: string;
  impactGraphId?: string;
  target?: {
    symbolId?: string;
    changeBatchId?: string;
  };
  directImpact: DirectImpact;
  indirectImpact: IndirectImpact;
  freshness?: string;
}
