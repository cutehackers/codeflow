package semantic

import (
	"codeflow/internal/fusion"
	"codeflow/internal/slicing"
)

// SemanticMapIR mirrors schemas/semantic-map-ir.schema.json.
type SemanticMapIR struct {
	SchemaID                   string                 `json:"schemaId"`
	SchemaVersion              int                    `json:"schemaVersion"`
	MapID                      string                 `json:"mapId"`
	GenerationID               string                 `json:"generationId"`
	ComputedBasisID            string                 `json:"computedBasisId"`
	ValidatedAgainstSnapshotID string                 `json:"validatedAgainstSnapshotId,omitempty"`
	GenerationSequence         int                    `json:"generationSequence,omitempty"`
	DerivationParent           string                 `json:"derivationParent,omitempty"`
	Supersedes                 string                 `json:"supersedes,omitempty"`
	PublicationKind            string                 `json:"publicationKind"`   // initial | checkpoint | refinement
	Freshness                  string                 `json:"freshness"`         // current | historical | invalid
	Settlement                 string                 `json:"settlement"`        // pending | passed | failed
	EnrichmentStatus           string                 `json:"enrichmentStatus"`  // not_requested | pending | available | timed_out | unavailable
	Quality                    MapQuality             `json:"quality"`
	Task                       MapTaskContext         `json:"task"`
	Basis                      MapBasisContext        `json:"basis"`
	Summary                    MapSummary             `json:"summary"`
	Steps                      []SemanticStep         `json:"steps"`
	Edges                      []SemanticEdge         `json:"edges"`
	RequirementAlignment       []RequirementAlignment `json:"requirementAlignment,omitempty"`
	Evidence                   []SemanticEvidence     `json:"evidence,omitempty"`
	Unknowns                   []fusion.Unknown       `json:"unknowns"`
	Coverage                   *CoverageBoundary      `json:"coverage,omitempty"`
}

type MapQuality struct {
	Stage                    string                   `json:"stage"` // Q1 | Q2 | Q3 | Q4
	CriticalObligations      []CriticalObligation     `json:"criticalObligations,omitempty"`
	CriticalCoverageSummary  *CriticalCoverageSummary `json:"criticalCoverageSummary,omitempty"`
	UnresolvedCriticalCount  int                      `json:"unresolvedCriticalCount"`
	ConflictingCriticalCount int                      `json:"conflictingCriticalCount"`
	Degradations             []QualityDegradation     `json:"degradations,omitempty"`
}

type CriticalObligation struct {
	ObligationID string   `json:"obligationId"`
	Kind         string   `json:"kind"` // entry | result | critical_branch | external_effect | failure
	Required     bool     `json:"required"`
	TargetRef    string   `json:"targetRef,omitempty"`
	Status       string   `json:"status"` // pending | verified | unknown | conflicting | invalid
	EvidenceRefs []string `json:"evidenceRefs,omitempty"`
}

type CriticalCoverageSummary struct {
	Required int `json:"required"`
	Verified int `json:"verified"`
}

type QualityDegradation struct {
	Code              string   `json:"code"`
	ScopeRefs         []string `json:"scopeRefs,omitempty"`
	Impact            string   `json:"impact"`
	RecoveryCondition string   `json:"recoveryCondition"`
}

type MapTaskContext struct {
	TaskID         string `json:"taskId"`
	IntentRevision int    `json:"intentRevision"`
	IntentStatus   string `json:"intentStatus,omitempty"`
	Mode           string `json:"mode"`
}

type MapBasisContext struct {
	WorkspaceEpoch              int64  `json:"workspaceEpoch,omitempty"`
	ComputedWorkspaceSnapshotID string `json:"computedWorkspaceSnapshotId,omitempty"`
	ComputedBasisID             string `json:"computedBasisId,omitempty"`
	AnalysisReadSetID           string `json:"analysisReadSetId,omitempty"`
	CausalObservationClosureID  string `json:"causalObservationClosureId,omitempty"`
}

type MapSummary struct {
	Requested string `json:"requested"`
	Current   string `json:"current"`
}

type SemanticStep struct {
	StepID        string             `json:"stepId"`
	Ordinal       int                `json:"ordinal"`
	Name          string             `json:"name"`
	TechnicalName string             `json:"technicalName,omitempty"`
	Layer         string             `json:"layer,omitempty"`
	Kind          string             `json:"kind,omitempty"`
	Anchor        slicing.Anchor     `json:"anchor"`
	CodeLens      *fusion.CodeLens   `json:"codeLens,omitempty"`
	StateDelta    *fusion.StateDelta `json:"stateDelta,omitempty"`
	SideEffect    *string            `json:"sideEffect,omitempty"`
	Branch        *string            `json:"branch,omitempty"`
	Rules         []string           `json:"rules,omitempty"`
	EvidenceRefs  []string           `json:"evidenceRefs,omitempty"`
}

type SemanticEdge struct {
	FromStepID       string `json:"fromStepId,omitempty"`
	ToStepID         string `json:"toStepId,omitempty"`
	ToSymbolPath     string `json:"toSymbolPath"`
	Kind             string `json:"kind"`
	ResolutionStatus string `json:"resolutionStatus"`
}

type RequirementAlignment struct {
	CriterionID     string   `json:"criterionId"`
	Status          string   `json:"status"` // confirmed | partial | not_observed | conflicting | unknown
	CoveredStepRefs []string `json:"coveredStepRefs,omitempty"`
	EvidenceRefs    []string `json:"evidenceRefs,omitempty"`
}

type SemanticEvidence struct {
	EvidenceID         string         `json:"evidenceId"`
	Kind               string         `json:"kind"`
	SourceAuthority    string         `json:"sourceAuthority"`
	ComputedBasisID    string         `json:"computedBasisId,omitempty"`
	DocumentRevisionID string         `json:"documentRevisionId,omitempty"`
	Anchor             slicing.Anchor `json:"anchor"`
	Producer           *ProducerInfo  `json:"producer,omitempty"`
	ValidationStatus   string         `json:"validationStatus,omitempty"`
	RedactionStatus    string         `json:"redactionStatus,omitempty"`
}

type ProducerInfo struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

type CoverageBoundary struct {
	IncludedSourceRoots []string `json:"includedSourceRoots,omitempty"`
	ExcludedReasons     []string `json:"excludedReasons,omitempty"`
}

// FlowViewProjection mirrors schemas/flow-view-projection.schema.json.
type FlowViewProjection struct {
	SchemaID          string          `json:"schemaId"`
	SchemaVersion     int             `json:"schemaVersion"`
	ProjectionID      string          `json:"projectionId"`
	GenerationID      string          `json:"generationId"`
	ComputedBasisID   string          `json:"computedBasisId"`
	Mode              string          `json:"mode"`
	DisplayBudget     DisplayBudget   `json:"displayBudget"`
	VisibleStepRefs   []string        `json:"visibleStepRefs"`
	PreservedStepRefs []string        `json:"preservedStepRefs"`
	FoldedSubflows    []FoldedSubflow `json:"foldedSubflows"`
}

type DisplayBudget struct {
	TargetMin   int    `json:"targetMin"`
	TargetMax   int    `json:"targetMax"`
	Enforcement string `json:"enforcement"` // soft | strict
}

type FoldedSubflow struct {
	FoldID          string `json:"foldId"`
	EntryStepRef    string `json:"entryStepRef"`
	ExitStepRef     string `json:"exitStepRef"`
	HiddenCount     int    `json:"hiddenCount"`
	DrilldownTarget string `json:"drilldownTarget,omitempty"`
}
