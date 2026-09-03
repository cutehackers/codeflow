package semantic

import (
	"time"
)

// GenerationProofManifest represents the canonical proof manifest for a published generation (Raw §10.11, VS-04).
type GenerationProofManifest struct {
	SchemaID                       string                   `json:"schemaId"`
	SchemaVersion                  int                      `json:"schemaVersion"`
	ProofID                        string                   `json:"proofId"`
	GenerationID                   string                   `json:"generationId"`
	ComputedBasisID                string                   `json:"computedBasisId"`
	ValidatedAgainstSnapshotID     string                   `json:"validatedAgainstSnapshotId"`
	ValidatedWorkspaceDeltaID      *string                  `json:"validatedWorkspaceDeltaId,omitempty"`
	TaskIntentRevision             int                      `json:"taskIntentRevision"`
	NormalizedQueryHash            string                   `json:"normalizedQueryHash"`
	AnalysisReadSetID              string                   `json:"analysisReadSetId"`
	CausalObservationClosureID     string                   `json:"causalObservationClosureId"`
	CausalObservationClosureDigest string                   `json:"causalObservationClosureDigest"`
	CapabilityProfileDigest        string                   `json:"capabilityProfileDigest,omitempty"`
	CurrentPublication             CurrentPublicationResult `json:"currentPublication"`
	SettlementEvaluation           SettlementEvaluation     `json:"settlementEvaluation"`
	ArtifactRefs                   ArtifactRefs             `json:"artifactRefs"`
	ExpectedLiveHeadSnapshotID     string                   `json:"expectedLiveHeadSnapshotId"`
	ExpectedPreviousGenerationID   *string                  `json:"expectedPreviousGenerationId,omitempty"`
	PublishedAt                    time.Time                `json:"publishedAt"`
}

// CurrentPublicationResult holds the evaluation results of the 6 Current Publication subgates (Raw §18.1).
type CurrentPublicationResult struct {
	Eligibility           string `json:"eligibility"`           // passed | rejected
	SnapshotGate          string `json:"snapshotGate"`          // passed | failed
	ClosureGate           string `json:"closureGate"`           // passed | failed
	EvidenceGate          string `json:"evidenceGate"`          // passed | failed
	SemanticAtomicityGate string `json:"semanticAtomicityGate"` // passed | failed
	TaskRelevanceGate     string `json:"taskRelevanceGate"`     // passed | failed
	ComprehensionGate     string `json:"comprehensionGate"`     // passed | failed
}

// SettlementEvaluation holds the settlement gate evaluation status (Raw §10.11, §18.1).
type SettlementEvaluation struct {
	Gate                   string     `json:"gate"` // pending | passed | failed
	EvaluatedAt            *time.Time `json:"evaluatedAt,omitempty"`
	BlockingObligationRefs []string   `json:"blockingObligationRefs"`
}

// ArtifactRefs contains CAS references for canonical artifacts of the generation.
type ArtifactRefs struct {
	SemanticMap   string `json:"semanticMap"`
	SemanticDelta string `json:"semanticDelta,omitempty"`
	EvidenceIndex string `json:"evidenceIndex,omitempty"`
	Projection    string `json:"projection,omitempty"`
}

// ActivePointer represents the atomic active generation pointer in storage (Raw §10.11).
type ActivePointer struct {
	SchemaID                     string    `json:"schemaId"`
	SchemaVersion                int       `json:"schemaVersion"`
	GenerationID                 string    `json:"generationId"`
	ManifestObjectRef            string    `json:"manifestObjectRef"`
	PublishedAt                  time.Time `json:"publishedAt"`
	ComputedBasisID              string    `json:"computedBasisId"`
	ValidatedAgainstSnapshotID   string    `json:"validatedAgainstSnapshotId"`
	ExpectedLiveHeadSnapshotID   string    `json:"expectedLiveHeadSnapshotId"`
	ExpectedPreviousGenerationID *string   `json:"expectedPreviousGenerationId,omitempty"`
	WorkspaceEpoch               string    `json:"workspaceEpoch"`
	TaskIntentRevision           int       `json:"taskIntentRevision"`
	NormalizedQueryHash          string    `json:"normalizedQueryHash"`
	FlowCount                    int       `json:"flowCount"`
}

// EventEnvelope represents sequenced SSE events for live generation and activity stream (Raw §10.14).
type EventEnvelope struct {
	SchemaID                   string    `json:"schemaId"`
	SchemaVersion              int       `json:"schemaVersion"`
	StreamID                   string    `json:"streamId"`
	Sequence                   int       `json:"sequence"`
	EventID                    string    `json:"eventId"`
	EventType                  string    `json:"eventType"` // activity.updated | generation.published | generation.gap | approval.updated | snapshot_sync
	OccurredAt                 time.Time `json:"occurredAt"`
	ComputedBasisID            *string   `json:"computedBasisId,omitempty"`
	ValidatedAgainstSnapshotID *string   `json:"validatedAgainstSnapshotId,omitempty"`
	GenerationID               *string   `json:"generationId,omitempty"`
	PayloadRef                 *string   `json:"payloadRef,omitempty"`
	Data                       any       `json:"data,omitempty"`
}

// CausalObservationClosure captures dependencies, negative lookups, memberships, and frontiers (Raw §10.5).
type CausalObservationClosure struct {
	SchemaID                       string                   `json:"schemaId"`
	SchemaVersion                  int                      `json:"schemaVersion"`
	ClosureID                      string                   `json:"closureId"`
	ComputedBasisID                string                   `json:"computedBasisId"`
	TaskIntentRevision             int                      `json:"taskIntentRevision"`
	NormalizedQueryHash            string                   `json:"normalizedQueryHash"`
	AnalysisReadSetID              string                   `json:"analysisReadSetId"`
	PositiveDependencies           PositiveDependencies     `json:"positiveDependencies"`
	NegativeObservations           []NegativeObservation    `json:"negativeObservations"`
	MembershipObservations         []MembershipObservation  `json:"membershipObservations"`
	DependencyFrontiers            []DependencyFrontier     `json:"dependencyFrontiers"`
	CapabilityProfile              *CapabilityProfile       `json:"capabilityProfile,omitempty"`
	CoverageBoundary               *CoverageBoundary        `json:"coverageBoundary,omitempty"`
	ClosureStatus                  string                   `json:"closureStatus"` // closed | open
	IncompleteReasons              []string                 `json:"incompleteReasons,omitempty"`
	ClosureDigest                  string                   `json:"closureDigest"`
}

type PositiveDependencies struct {
	DocumentRevisionRefs     []string `json:"documentRevisionRefs"`
	RelationRefs             []string `json:"relationRefs,omitempty"`
	ConfigurationFingerprint string   `json:"configurationFingerprint"`
}

type NegativeObservation struct {
	Kind                         string `json:"kind"`     // relation_absent | symbol_absent | file_absent
	Selector                     string `json:"selector"` // caller-of:symbol, etc.
	ScopeRef                     string `json:"scopeRef"` // package-path or directory
	ObservedAgainstIndexRevision string `json:"observedAgainstIndexRevision,omitempty"`
}

type MembershipObservation struct {
	Kind             string `json:"kind"` // package_sources | directory_files
	ContainerRef     string `json:"containerRef"`
	MembershipDigest string `json:"membershipDigest,omitempty"`
}

type DependencyFrontier struct {
	Direction     string `json:"direction"` // callers | callees
	RootRef       string `json:"rootRef"`
	BoundaryRef   string `json:"boundaryRef"`
	GraphRevision string `json:"graphRevision,omitempty"`
}

type CapabilityProfile struct {
	Adapter  string   `json:"adapter"`
	Features []string `json:"features"`
}

// VerifiedGap describes a verified gap between last verified generation and current workspace state (Raw §7.3, §7.4).
type VerifiedGap struct {
	Freshness         string    `json:"freshness"` // last_verified
	Activity          string    `json:"activity"`  // editing
	LastVerifiedGenID string    `json:"lastVerifiedGenId"`
	LatestSnapshotID  string    `json:"latestSnapshotId"`
	AffectedScope     []string  `json:"affectedScope"`
	AnalysisLagMs     int64     `json:"analysisLagMs"`
	PendingRevisions  int       `json:"pendingRevisions"`
	IntersectedCauses []string  `json:"intersectedCauses"`
	Timestamp         time.Time `json:"timestamp"`
}
