package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	pathpkg "path"
	"sort"
	"strings"
	"unicode"

	"codeflow/internal/fusion"
	"codeflow/internal/secret"
	"codeflow/internal/slicing"
)

// OnboardingQueryV2 is the explicit identity selector for an onboarding
// projection.  A query never resolves an implicit workspace or active basis.
// All four identities are required and must match one canonical SemanticMapIR.
type OnboardingQueryV2 struct {
	SchemaID                   string         `json:"schemaId"`
	SchemaVersion              int            `json:"schemaVersion"`
	RepositoryID               string         `json:"repositoryId"`
	ComputedBasisID            string         `json:"computedBasisId"`
	GenerationID               string         `json:"generationId"`
	ValidatedAgainstSnapshotID string         `json:"validatedAgainstSnapshotId"`
	Freshness                  string         `json:"freshness"`
	Level                      int            `json:"level"`
	Domain                     string         `json:"domain,omitempty"`
	DisplayBudget              *DisplayBudget `json:"displayBudget,omitempty"`
}

// OnboardingRequestV2 binds the query to the one canonical graph from which
// every domain and representative-flow claim is projected.
type OnboardingRequestV2 struct {
	Query      OnboardingQueryV2 `json:"query"`
	Map        *SemanticMapIR    `json:"-"`
	Candidates []CandidateEntry  `json:"candidates,omitempty"`
	// CurrentProof is deliberately supplied separately from the candidate or
	// historical map.  A query saying freshness=current is not authority.  The
	// proof must bind the selected map to the live-head publication gates.
	CurrentProof *GenerationProofManifest `json:"-"`
}

type OnboardingSummaryV2 struct {
	TotalDomains  int     `json:"totalDomains"`
	TotalFlows    int     `json:"totalFlows"`
	CoverageRatio float64 `json:"coverageRatio"`
}

// DomainCandidateV2 is an Evidence-backed domain projection.  The candidate
// remains explicit about epistemic state and coverage instead of becoming a
// fabricated confirmed business label.
type DomainCandidateV2 struct {
	DomainID                string           `json:"domainId"`
	Name                    string           `json:"name"`
	Responsibility          string           `json:"responsibility"`
	Description             string           `json:"description"`
	RepresentativeFlowCount int              `json:"representativeFlowCount"`
	EntryPoints             []string         `json:"entryPoints"`
	Rationale               string           `json:"rationale"`
	EvidenceRefs            []string         `json:"evidenceRefs"`
	OwnershipEvidenceRefs   []string         `json:"ownershipEvidenceRefs,omitempty"`
	GlossaryEvidenceRefs    []string         `json:"glossaryEvidenceRefs,omitempty"`
	Confidence              float64          `json:"confidence"`
	EpistemicState          string           `json:"epistemicState"`
	CoverageBoundary        CoverageBoundary `json:"coverageBoundary"`
	SelectionReason         string           `json:"selectionReason"`
	RedactionStatus         string           `json:"redactionStatus"`
	MissingEvidence         []string         `json:"missingEvidence,omitempty"`
}

// DomainOverviewV2 is the level-1 onboarding projection.  It deliberately
// keeps unmapped modules, unknowns and recovery guidance in the public shape.
type DomainOverviewV2 struct {
	SchemaID                   string                `json:"schemaId"`
	SchemaVersion              int                   `json:"schemaVersion"`
	RepositoryID               string                `json:"repositoryId"`
	ComputedBasisID            string                `json:"computedBasisId"`
	GenerationID               string                `json:"generationId"`
	ValidatedAgainstSnapshotID string                `json:"validatedAgainstSnapshotId"`
	Freshness                  string                `json:"freshness"`
	Domains                    []DomainCandidateV2   `json:"domains"`
	UnmappedModules            []string              `json:"unmappedModules"`
	Unknowns                   []OnboardingUnknownV2 `json:"unknowns"`
	CoverageBoundary           CoverageBoundary      `json:"coverageBoundary"`
	RecoveryGuidance           []string              `json:"recoveryGuidance"`
	Summary                    OnboardingSummaryV2   `json:"summary"`
	RedactionStatus            string                `json:"redactionStatus"`
}

type OnboardingUnknownV2 struct {
	Subject          string   `json:"subject"`
	Reason           string   `json:"reason"`
	RecoveryGuidance []string `json:"recoveryGuidance"`
}

type RepresentativeFlowV2 struct {
	FlowID             string              `json:"flowId"`
	Title              string              `json:"title"`
	EntrySymbol        string              `json:"entrySymbol"`
	ComplexityScore    float64             `json:"complexityScore"`
	KeyMutations       []string            `json:"keyMutations"`
	GroundedMapID      string              `json:"groundedMapId"`
	GenerationID       string              `json:"generationId"`
	SelectionReason    string              `json:"selectionReason"`
	Rationale          string              `json:"rationale"`
	EvidenceRefs       []string            `json:"evidenceRefs"`
	EntryEvidenceRefs  []string            `json:"entryEvidenceRefs"`
	ResultEvidenceRefs []string            `json:"resultEvidenceRefs"`
	Confidence         float64             `json:"confidence"`
	EpistemicState     string              `json:"epistemicState"`
	CoverageBoundary   CoverageBoundary    `json:"coverageBoundary"`
	MissingEvidence    []string            `json:"missingEvidence,omitempty"`
	RedactionStatus    string              `json:"redactionStatus"`
	Projection         *FlowViewProjection `json:"projection,omitempty"`
}

type RepresentativeFlowCatalogV2 struct {
	SchemaID                   string                 `json:"schemaId"`
	SchemaVersion              int                    `json:"schemaVersion"`
	CatalogID                  string                 `json:"catalogId"`
	DomainID                   string                 `json:"domainId"`
	ComputedBasisID            string                 `json:"computedBasisId"`
	GenerationID               string                 `json:"generationId"`
	ValidatedAgainstSnapshotID string                 `json:"validatedAgainstSnapshotId"`
	Freshness                  string                 `json:"freshness"`
	Flows                      []RepresentativeFlowV2 `json:"flows"`
	UnmappedModules            []string               `json:"unmappedModules"`
	Unknowns                   []OnboardingUnknownV2  `json:"unknowns"`
	CoverageBoundary           CoverageBoundary       `json:"coverageBoundary"`
	RecoveryGuidance           []string               `json:"recoveryGuidance"`
	RedactionStatus            string                 `json:"redactionStatus"`
}

// OnboardingFlowDrilldownV2 is a reference-only level-2 drilldown.  It
// returns step and Evidence IDs from the same canonical SemanticMapIR.  It
// does not create a second graph or reinterpret facts.
type OnboardingFlowDrilldownV2 struct {
	SchemaID                   string                `json:"schemaId"`
	SchemaVersion              int                   `json:"schemaVersion"`
	FlowID                     string                `json:"flowId"`
	DomainID                   string                `json:"domainId"`
	CanonicalMapID             string                `json:"canonicalMapId"`
	ComputedBasisID            string                `json:"computedBasisId"`
	GenerationID               string                `json:"generationId"`
	ValidatedAgainstSnapshotID string                `json:"validatedAgainstSnapshotId"`
	Freshness                  string                `json:"freshness"`
	StepRefs                   []string              `json:"stepRefs"`
	EvidenceRefs               []string              `json:"evidenceRefs"`
	CoverageBoundary           CoverageBoundary      `json:"coverageBoundary"`
	Unknowns                   []OnboardingUnknownV2 `json:"unknowns"`
	RecoveryGuidance           []string              `json:"recoveryGuidance"`
	RedactionStatus            string                `json:"redactionStatus"`
}

const (
	ErrCodeInvalidOnboardingIdentity = "invalid_identity"
	ErrCodeInvalidOnboardingEvidence = "invalid_evidence"
	ErrCodeOnboardingNoMatch         = "no_match"
)

type OnboardingError struct {
	Code    string
	Message string
}

func (e *OnboardingError) Error() string {
	return e.Code + ": " + e.Message
}

// ValidateOnboardingQueryV2 validates only the query's local contract.  Map
// identity and repository resolution are checked by ExploreDomainsV2.
func ValidateOnboardingQueryV2(query OnboardingQueryV2) error {
	if query.SchemaID != OnboardingQuerySchemaID || query.SchemaVersion != 2 {
		return &OnboardingError{Code: ErrCodeInvalidOnboardingIdentity, Message: "onboarding query schema identity is not v2 canonical"}
	}
	for name, value := range map[string]string{
		"repositoryId":               query.RepositoryID,
		"computedBasisId":            query.ComputedBasisID,
		"generationId":               query.GenerationID,
		"validatedAgainstSnapshotId": query.ValidatedAgainstSnapshotID,
	} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return &OnboardingError{Code: ErrCodeMissingPrecondition, Message: name + " is required and must be canonical"}
		}
	}
	if query.Freshness != "current" && query.Freshness != "historical" {
		return &OnboardingError{Code: ErrCodeInvalidOnboardingIdentity, Message: "freshness must be current or historical"}
	}
	if query.Level != 1 && query.Level != 2 {
		return &OnboardingError{Code: ErrCodeInvalidOnboardingIdentity, Message: "level must be 1 or 2"}
	}
	if query.DisplayBudget != nil && (query.DisplayBudget.TargetMin < 1 || query.DisplayBudget.TargetMax < 1 || query.DisplayBudget.TargetMax < query.DisplayBudget.TargetMin || query.DisplayBudget.Enforcement != "soft") {
		return &OnboardingError{Code: ErrCodeInvalidOnboardingIdentity, Message: "display budget is invalid"}
	}
	return nil
}

// ExploreDomainsV2 projects domains from a canonical map and its Evidence.
// The supplied candidates are only accepted when they resolve to map steps
// and their Evidence refs resolve to the same immutable generation.
func ExploreDomainsV2(request OnboardingRequestV2) (*DomainOverviewV2, error) {
	if err := validateOnboardingRequest(request); err != nil {
		return nil, err
	}
	projections, unmapped, unknowns, err := projectOnboardingDomains(request)
	if err != nil {
		return nil, err
	}
	filter := strings.ToLower(strings.TrimSpace(request.Query.Domain))
	if filter != "" {
		filtered := projections[:0]
		for _, candidate := range projections {
			if strings.EqualFold(candidate.Name, filter) || strings.EqualFold(candidate.DomainID, filter) {
				filtered = append(filtered, candidate)
			}
		}
		projections = filtered
	}
	flowCount := 0
	for _, candidate := range projections {
		flowCount += candidate.RepresentativeFlowCount
	}
	total := flowCount + len(unmapped)
	coverage := 1.0
	if total > 0 {
		coverage = float64(flowCount) / float64(total)
	}
	status := "clean"
	for _, candidate := range projections {
		if candidate.RedactionStatus == "redacted" {
			status = "redacted"
			break
		}
	}
	return &DomainOverviewV2{
		SchemaID: DomainOverviewSchemaID, SchemaVersion: 2,
		RepositoryID: request.Query.RepositoryID, ComputedBasisID: request.Query.ComputedBasisID,
		GenerationID: request.Query.GenerationID, ValidatedAgainstSnapshotID: request.Query.ValidatedAgainstSnapshotID,
		Freshness: request.Query.Freshness, Domains: projections, UnmappedModules: unmapped,
		Unknowns: unknowns, CoverageBoundary: copyCoverage(request.Map.Coverage),
		RecoveryGuidance: onboardingRecovery(unmapped, unknowns),
		Summary:          OnboardingSummaryV2{TotalDomains: len(projections), TotalFlows: flowCount, CoverageRatio: coverage},
		RedactionStatus:  status,
	}, nil
}

// GetRepresentativeFlowCatalogV2 ranks flows for one selected domain using
// only canonical map steps and Evidence.  Stable flow identity is derived
// from the canonical entry symbol path, never from input order.
func GetRepresentativeFlowCatalogV2(request OnboardingRequestV2, domainID string) (*RepresentativeFlowCatalogV2, error) {
	if err := validateOnboardingRequest(request); err != nil {
		return nil, err
	}
	projections, unmapped, unknowns, err := projectOnboardingDomains(request)
	if err != nil {
		return nil, err
	}
	selected, ok := selectDomain(projections, domainID)
	if !ok {
		return nil, &OnboardingError{Code: ErrCodeOnboardingNoMatch, Message: fmt.Sprintf("domain %q is not present in the canonical onboarding projection", domainID)}
	}
	projectionBudget := DefaultDisplayBudget()
	if request.Query.DisplayBudget != nil {
		projectionBudget = *request.Query.DisplayBudget
	}
	projection := BuildFlowViewProjectionWithBudget(request.Map, projectionBudget)
	if projection == nil {
		return nil, &OnboardingError{Code: ErrCodeInvalidOnboardingIdentity, Message: "canonical flow projection could not be built"}
	}
	if err := ValidateFlowViewProjectionAgainstMap(projection, request.Map); err != nil {
		return nil, &OnboardingError{Code: ErrCodeInvalidOnboardingIdentity, Message: "canonical flow projection is not grounded in the selected map"}
	}
	flows := make([]RepresentativeFlowV2, 0, selected.RepresentativeFlowCount)
	for _, candidate := range canonicalCandidatesForDomain(request, selected.Name) {
		flowID := fusion.ComputeFlowID(candidate.EntrySymbolPath)
		entryRefs := validCandidateEvidence(request.Map, candidate)
		resultRefs := validRefs(request.Map, candidate.ResultEvidenceRefs)
		missing := make([]string, 0, 1)
		state := "confirmed"
		confidence := 1.0
		if len(entryRefs) == 0 {
			missing = append(missing, "entryEvidence")
			state = "unknown"
			confidence = 0
		} else if !refsAreVerified(request.Map, entryRefs) {
			missing = append(missing, "entryEvidenceValidation")
			state = "unknown"
			confidence = 0.25
		}
		if len(resultRefs) == 0 {
			missing = append(missing, "resultEvidence")
			if state != "unknown" {
				state = "candidate"
			}
			if confidence > 0 {
				confidence = 0.5
			}
		} else if !refsAreVerified(request.Map, resultRefs) {
			missing = append(missing, "resultEvidenceValidation")
			state = "unknown"
			if confidence > 0.25 {
				confidence = 0.25
			}
		}
		title, titleStatus := redactOnboardingText(candidate.Title)
		if title == "" {
			title = candidate.EntrySymbolPath
		}
		mutations := append([]string{}, candidate.KeyMutations...)
		if len(mutations) == 0 {
			mutations = deriveMutations(request.Map, candidate.EntrySymbolPath)
		}
		evidenceRefs := unionSorted(entryRefs, resultRefs)
		rationale := candidate.Rationale
		if strings.TrimSpace(rationale) == "" {
			rationale = fmt.Sprintf("selected from canonical entry Evidence with deterministic score %.3f", candidateScore(candidate, entryRefs, resultRefs))
		}
		if !refsAreVerified(request.Map, entryRefs) || !refsAreVerified(request.Map, resultRefs) {
			// Candidate rationale is descriptive input, not validation authority.
			// Always append the explicit status so a pre-existing phrase such as
			// "unresolved source note" cannot hide the validation gap.
			rationale += "; Evidence validation remains unresolved"
		}
		rationale, rationaleStatus := redactOnboardingText(rationale)
		redactionStatus := "clean"
		if titleStatus == "redacted" || rationaleStatus == "redacted" {
			redactionStatus = "redacted"
		}
		flows = append(flows, RepresentativeFlowV2{
			FlowID: flowID, Title: title, EntrySymbol: candidate.EntrySymbolPath,
			ComplexityScore: candidateScore(candidate, entryRefs, resultRefs), KeyMutations: mutations,
			GroundedMapID: request.Map.MapID, GenerationID: request.Map.GenerationID,
			SelectionReason: "deterministic evidence-backed ranking; tie-break by canonical flow ID",
			Rationale:       rationale, EvidenceRefs: evidenceRefs, EntryEvidenceRefs: entryRefs,
			ResultEvidenceRefs: resultRefs, Confidence: confidence, EpistemicState: state,
			CoverageBoundary: copyCoverage(request.Map.Coverage), MissingEvidence: missing,
			RedactionStatus: redactionStatus, Projection: projection,
		})
	}
	sort.SliceStable(flows, func(i, j int) bool {
		if flows[i].ComplexityScore != flows[j].ComplexityScore {
			return flows[i].ComplexityScore > flows[j].ComplexityScore
		}
		return flows[i].FlowID < flows[j].FlowID
	})
	status := "clean"
	for _, flow := range flows {
		if flow.RedactionStatus == "redacted" {
			status = "redacted"
			break
		}
	}
	return &RepresentativeFlowCatalogV2{
		SchemaID: RepresentativeFlowCatalogSchemaID, SchemaVersion: 2,
		CatalogID: "catalog-" + selected.DomainID, DomainID: selected.DomainID,
		ComputedBasisID: request.Map.ComputedBasisID, GenerationID: request.Map.GenerationID,
		ValidatedAgainstSnapshotID: request.Map.ValidatedAgainstSnapshotID, Freshness: request.Query.Freshness,
		Flows: flows, UnmappedModules: unmapped, Unknowns: unknowns,
		CoverageBoundary: copyCoverage(request.Map.Coverage), RecoveryGuidance: onboardingRecovery(unmapped, unknowns),
		RedactionStatus: status,
	}, nil
}

// DrilldownRepresentativeFlowV2 returns references into the canonical map
// selected by GetRepresentativeFlowCatalogV2.  It cannot resolve a flow from
// a different basis or manufacture a parallel fact graph.
func DrilldownRepresentativeFlowV2(request OnboardingRequestV2, domainID, flowID string) (*OnboardingFlowDrilldownV2, error) {
	if strings.TrimSpace(flowID) == "" {
		return nil, &OnboardingError{Code: ErrCodeMissingPrecondition, Message: "flowId is required for onboarding drilldown"}
	}
	catalog, err := GetRepresentativeFlowCatalogV2(request, domainID)
	if err != nil {
		return nil, err
	}
	var selected *RepresentativeFlowV2
	for index := range catalog.Flows {
		if catalog.Flows[index].FlowID == flowID {
			selected = &catalog.Flows[index]
			break
		}
	}
	if selected == nil {
		return nil, &OnboardingError{Code: ErrCodeOnboardingNoMatch, Message: "flowId is not present in the selected canonical catalog"}
	}
	stepRefs := make([]string, 0)
	for _, step := range request.Map.Steps {
		path := canonicalStepPath(step)
		if path == selected.EntrySymbol || strings.HasPrefix(path, strings.TrimSuffix(selected.EntrySymbol, "#")+"#") {
			stepRefs = append(stepRefs, step.StepID)
		}
	}
	if len(stepRefs) == 0 {
		return nil, &OnboardingError{Code: ErrCodeInvalidOnboardingEvidence, Message: "selected flow has no canonical map step"}
	}
	stepRefs = uniqueSorted(stepRefs)
	return &OnboardingFlowDrilldownV2{
		SchemaID: OnboardingFlowDrilldownSchemaID, SchemaVersion: 2, FlowID: selected.FlowID,
		DomainID: catalog.DomainID, CanonicalMapID: request.Map.MapID,
		ComputedBasisID: request.Map.ComputedBasisID, GenerationID: request.Map.GenerationID,
		ValidatedAgainstSnapshotID: request.Map.ValidatedAgainstSnapshotID, Freshness: request.Query.Freshness,
		StepRefs: stepRefs, EvidenceRefs: uniqueSorted(append([]string(nil), selected.EvidenceRefs...)),
		CoverageBoundary: copyCoverage(request.Map.Coverage), Unknowns: append([]OnboardingUnknownV2(nil), catalog.Unknowns...),
		RecoveryGuidance: append([]string(nil), catalog.RecoveryGuidance...), RedactionStatus: catalog.RedactionStatus,
	}, nil
}

func validateOnboardingRequest(request OnboardingRequestV2) error {
	if err := ValidateOnboardingQueryV2(request.Query); err != nil {
		return err
	}
	if request.Map == nil {
		return &OnboardingError{Code: ErrCodeMissingPrecondition, Message: "canonical SemanticMapIR is required"}
	}
	mapIR := request.Map
	if strings.TrimSpace(mapIR.MapID) == "" || strings.TrimSpace(mapIR.GenerationID) == "" || strings.TrimSpace(mapIR.ComputedBasisID) == "" || strings.TrimSpace(mapIR.ValidatedAgainstSnapshotID) == "" {
		return &OnboardingError{Code: ErrCodeInvalidOnboardingIdentity, Message: "canonical map identity is incomplete"}
	}
	if mapIR.SchemaID != SemanticMapSchemaID || mapIR.SchemaVersion != SemanticSchemaVersion {
		return &OnboardingError{Code: ErrCodeInvalidOnboardingIdentity, Message: "selected map is not the canonical SemanticMapIR v2"}
	}
	if mapIR.Basis.RepositoryID != request.Query.RepositoryID {
		return &OnboardingError{Code: ErrCodeMissingPrecondition, Message: "repository does not resolve to the canonical map"}
	}
	if mapIR.Basis.ComputedBasisID == "" || mapIR.Basis.SnapshotTreeID == "" || mapIR.Basis.ComputedWorkspaceSnapshotID == "" {
		return &OnboardingError{Code: ErrCodeMissingPrecondition, Message: "selected basis does not resolve to a complete canonical map"}
	}
	if mapIR.Basis.ComputedBasisID != request.Query.ComputedBasisID {
		return &OnboardingError{Code: ErrCodeInvalidOnboardingIdentity, Message: "selected basis does not match the canonical map"}
	}
	if mapIR.Basis.ComputedWorkspaceSnapshotID != request.Query.ValidatedAgainstSnapshotID {
		return &OnboardingError{Code: ErrCodeMissingPrecondition, Message: "selected snapshot does not resolve to the canonical map"}
	}
	if mapIR.GenerationID != request.Query.GenerationID || mapIR.ComputedBasisID != request.Query.ComputedBasisID || mapIR.ValidatedAgainstSnapshotID != request.Query.ValidatedAgainstSnapshotID {
		return &OnboardingError{Code: ErrCodeInvalidOnboardingIdentity, Message: "query identity does not match canonical map generation/basis/snapshot"}
	}
	if mapIR.Freshness != "historical" && mapIR.Freshness != "current" {
		return &OnboardingError{Code: ErrCodeInvalidOnboardingIdentity, Message: "canonical map freshness is invalid"}
	}
	if request.Query.Freshness == "historical" && mapIR.Freshness != "historical" {
		return &OnboardingError{Code: ErrCodeInvalidOnboardingIdentity, Message: "historical onboarding cannot project a current map"}
	}
	if request.Query.Freshness == "current" {
		if request.CurrentProof == nil {
			return &OnboardingError{Code: ErrCodeMissingPrecondition, Message: "current onboarding requires a supplied generation proof"}
		}
		if err := validateCurrentOnboardingProof(request.CurrentProof, mapIR); err != nil {
			return err
		}
	}
	return validateCanonicalOnboardingMap(mapIR)
}

func validateCurrentOnboardingProof(proof *GenerationProofManifest, mapIR *SemanticMapIR) error {
	if proof == nil {
		return &OnboardingError{Code: ErrCodeMissingPrecondition, Message: "current generation proof is required"}
	}
	if proof.SchemaID != GenerationProofSchemaID || proof.SchemaVersion != SemanticSchemaVersion || strings.TrimSpace(proof.ProofID) == "" {
		return &OnboardingError{Code: ErrCodeInvalidOnboardingIdentity, Message: "supplied current proof schema or identity is invalid"}
	}
	if proof.GenerationID != mapIR.GenerationID || proof.ComputedBasisID != mapIR.ComputedBasisID || proof.ComputedSnapshotID != mapIR.Basis.ComputedWorkspaceSnapshotID || proof.ValidatedAgainstSnapshotID != mapIR.ValidatedAgainstSnapshotID || proof.ExpectedLiveHeadSnapshotID != mapIR.ValidatedAgainstSnapshotID || proof.WorkspaceEpoch != mapIR.Basis.WorkspaceEpoch {
		return &OnboardingError{Code: ErrCodeInvalidOnboardingIdentity, Message: "supplied current proof does not bind the canonical map generation/basis/snapshot/live head"}
	}
	gates := proof.CurrentPublication
	if gates.Eligibility != "passed" || gates.SnapshotGate != "passed" || gates.ClosureGate != "passed" || gates.EvidenceGate != "passed" || gates.SemanticAtomicityGate != "passed" || gates.TaskRelevanceGate != "passed" || gates.ComprehensionGate != "passed" {
		return &OnboardingError{Code: ErrCodeMissingPrecondition, Message: "supplied current proof has not passed all publication gates"}
	}
	return nil
}

func validateCanonicalOnboardingMap(mapIR *SemanticMapIR) error {
	evidenceByID := make(map[string]SemanticEvidence, len(mapIR.Evidence))
	for _, evidence := range mapIR.Evidence {
		if strings.TrimSpace(evidence.EvidenceID) == "" || evidenceByID[evidence.EvidenceID].EvidenceID != "" {
			return &OnboardingError{Code: ErrCodeInvalidOnboardingEvidence, Message: "canonical map contains empty or duplicate Evidence ID"}
		}
		// Evidence is an item-level claim.  A stale, malformed, or unsafe item
		// is quarantined by validRefs and makes only the affected projection
		// unknown.  The map itself remains usable when its identity and graph
		// structure are sound.
		evidenceByID[evidence.EvidenceID] = evidence
	}
	seenSteps := map[string]bool{}
	for _, step := range mapIR.Steps {
		if strings.TrimSpace(step.StepID) == "" || seenSteps[step.StepID] || strings.TrimSpace(canonicalStepPath(step)) == "" {
			return &OnboardingError{Code: ErrCodeInvalidOnboardingEvidence, Message: "canonical map contains invalid or duplicate step identity"}
		}
		seenSteps[step.StepID] = true
		if err := validateOnboardingAnchor(step.Anchor); err != nil {
			return &OnboardingError{Code: ErrCodeInvalidOnboardingEvidence, Message: err.Error()}
		}
		// Unknown Evidence refs are also item-level gaps.  They are omitted by
		// validRefs and surfaced as recovery/unknown for the affected entry.
	}
	if mapIR.Coverage == nil {
		return &OnboardingError{Code: ErrCodeInvalidOnboardingEvidence, Message: "canonical map coverage boundary is required"}
	}
	return nil
}

func validateOnboardingAnchor(anchor slicing.Anchor) error {
	path := anchor.RepoRelativePath
	if path == "" || strings.TrimSpace(path) != path || hasOnboardingControlOrWhitespace(path) || pathpkg.IsAbs(path) || path == "." || path == ".." || strings.HasPrefix(path, "../") || strings.Contains(path, "/../") || strings.Contains(path, "\\") || strings.Contains(path, "//") || pathpkg.Clean(path) != path || strings.HasSuffix(path, "/") {
		return fmt.Errorf("invalid_anchor: repository-relative path is unsafe")
	}
	symbol := anchor.EnclosingSymbolPath
	if symbol == "" || strings.TrimSpace(symbol) != symbol || hasOnboardingControlOrWhitespace(symbol) || anchor.ByteRange[0] < 0 || anchor.ByteRange[1] <= anchor.ByteRange[0] {
		return fmt.Errorf("invalid_anchor: byte range or enclosing symbol is invalid")
	}
	return nil
}

func hasOnboardingControlOrWhitespace(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

func projectOnboardingDomains(request OnboardingRequestV2) ([]DomainCandidateV2, []string, []OnboardingUnknownV2, error) {
	stepByPath := make(map[string]SemanticStep, len(request.Map.Steps))
	for _, step := range request.Map.Steps {
		if path := canonicalStepPath(step); path != "" {
			stepByPath[path] = step
		}
	}
	type grouped struct {
		name                 string
		candidates           []CandidateEntry
		evidenceIDs          []string
		ownershipEvidenceIDs []string
		glossaryEvidenceIDs  []string
		missingEvidence      []string
		confidence           float64
		epistemicState       string
	}
	groups := map[string]*grouped{}
	unmapped := make([]string, 0)
	unknowns := make([]OnboardingUnknownV2, 0)
	// A source root is the measured inventory unit available from a map step.
	// It prevents coverage from being calculated only over caller-supplied
	// candidates and lets a missing candidate remain visible as unmapped.
	mappedRoots := map[string]bool{}
	unmappedRoots := map[string]bool{}
	for _, mapUnknown := range request.Map.Unknowns {
		if strings.TrimSpace(mapUnknown.Subject) == "" {
			continue
		}
		subject := redactTextOrFallback(mapUnknown.Subject, "unknown map item")
		unknowns = append(unknowns, OnboardingUnknownV2{Subject: subject, Reason: redactTextOrFallback(mapUnknown.Reason, "mapping is unresolved"), RecoveryGuidance: []string{"extend graph/index coverage and rerun onboarding"}})
		if root := sourceRootForPath(mapUnknown.Subject); root != "" {
			unmappedRoots[root] = true
		}
		unmapped = append(unmapped, subject)
	}
	for _, candidate := range request.Candidates {
		path := candidate.EntrySymbolPath
		if strings.TrimSpace(path) != path || path == "" {
			unmapped = append(unmapped, redactTextOrFallback(path, "unknown entry"))
			unknowns = append(unknowns, OnboardingUnknownV2{Subject: redactTextOrFallback(path, "unknown entry"), Reason: "candidate entry identity is missing or non-canonical", RecoveryGuidance: []string{"provide a canonical repo-relative path and symbol"}})
			continue
		}
		step, ok := stepByPath[path]
		if !ok {
			unmapped = append(unmapped, path)
			unknowns = append(unknowns, OnboardingUnknownV2{Subject: path, Reason: "entry is not present in the selected canonical graph", RecoveryGuidance: []string{"re-run analysis for this source root"}})
			if root := sourceRootForPath(path); root != "" {
				unmappedRoots[root] = true
			}
			continue
		}
		refs := candidate.EvidenceRefs
		if candidate.EvidenceRefs == nil {
			refs = step.EvidenceRefs
		}
		refs = validRefs(request.Map, refs)
		if len(refs) == 0 {
			unmapped = append(unmapped, path)
			unknowns = append(unknowns, OnboardingUnknownV2{Subject: path, Reason: "candidate has no valid Evidence in the selected snapshot", RecoveryGuidance: []string{"relink the source anchor and rerun analysis"}})
			if root := sourceRootForPath(path); root != "" {
				unmappedRoots[root] = true
			}
			continue
		}
		name, state, confidence, domainRefs, missingEvidence, selectionReason, ok := resolveCandidateDomain(request, candidate, step, refs)
		if !ok {
			unmapped = append(unmapped, path)
			unknowns = append(unknowns, OnboardingUnknownV2{Subject: path, Reason: selectionReason, RecoveryGuidance: []string{"provide explicit domain-label Evidence or a canonical module/package signal"}})
			if root := sourceRootForPath(path); root != "" {
				unmappedRoots[root] = true
			}
			continue
		}
		key := strings.ToLower(strings.TrimSpace(name))
		group := groups[key]
		if group == nil {
			group = &grouped{name: name, confidence: confidence, epistemicState: state, missingEvidence: append([]string(nil), missingEvidence...)}
			groups[key] = group
		} else {
			if confidence < group.confidence {
				group.confidence = confidence
			}
			group.epistemicState = mergeOnboardingEpistemicState(group.epistemicState, state)
			group.missingEvidence = append(group.missingEvidence, missingEvidence...)
		}
		candidate.EvidenceRefs = append([]string(nil), refs...)
		candidate.DomainEvidenceRefs = append([]string(nil), domainRefs...)
		candidate.Domain = name
		group.candidates = append(group.candidates, candidate)
		group.evidenceIDs = append(group.evidenceIDs, refs...)
		group.evidenceIDs = append(group.evidenceIDs, domainRefs...)
		group.ownershipEvidenceIDs = append(group.ownershipEvidenceIDs, domainRefs...)
		if state == "confirmed" {
			group.glossaryEvidenceIDs = append(group.glossaryEvidenceIDs, domainRefs...)
		}
		if root := sourceRootForPath(path); root != "" {
			mappedRoots[root] = true
		}
	}
	// Walk every canonical step, not just candidates.  The first step in each
	// uncovered source root is a deterministic unmapped inventory item.
	steps := append([]SemanticStep(nil), request.Map.Steps...)
	sort.SliceStable(steps, func(i, j int) bool { return canonicalStepPath(steps[i]) < canonicalStepPath(steps[j]) })
	for _, step := range steps {
		path := canonicalStepPath(step)
		root := sourceRootForPath(path)
		if root == "" || mappedRoots[root] || unmappedRoots[root] {
			continue
		}
		unmapped = append(unmapped, path)
		unmappedRoots[root] = true
		unknowns = append(unknowns, OnboardingUnknownV2{Subject: path, Reason: "canonical map module has no supplied domain candidate", RecoveryGuidance: []string{"supply a candidate for this measured source root and rerun onboarding"}})
	}
	sort.Strings(unmapped)
	unmapped = uniqueSorted(unmapped)
	result := make([]DomainCandidateV2, 0, len(groups))
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		sort.SliceStable(group.candidates, func(i, j int) bool {
			left, right := group.candidates[i], group.candidates[j]
			leftID, rightID := stableCandidateID(left), stableCandidateID(right)
			if leftID != rightID {
				return leftID < rightID
			}
			return left.EntrySymbolPath < right.EntrySymbolPath
		})
		refs := uniqueSorted(group.evidenceIDs)
		domainID := stableDomainID(group.name, refs)
		entryPoints := make([]string, 0, len(group.candidates))
		for _, candidate := range group.candidates {
			entryPoints = append(entryPoints, candidate.EntrySymbolPath)
		}
		entryPoints = uniqueSorted(entryPoints)
		name, nameStatus := redactOnboardingText(group.name)
		responsibility := fmt.Sprintf("Evidence-backed responsibility for %s source/module/package signals", name)
		responsibility, responsibilityStatus := redactOnboardingText(responsibility)
		rationale := buildDomainRationale(group.candidates, refs)
		rationale, rationaleStatus := redactOnboardingText(rationale)
		status := "clean"
		if nameStatus == "redacted" || responsibilityStatus == "redacted" || rationaleStatus == "redacted" {
			status = "redacted"
		}
		result = append(result, DomainCandidateV2{
			DomainID: domainID, Name: name, Responsibility: responsibility,
			Description: responsibility, RepresentativeFlowCount: len(group.candidates), EntryPoints: entryPoints,
			Rationale: rationale, EvidenceRefs: refs, OwnershipEvidenceRefs: uniqueSorted(group.ownershipEvidenceIDs),
			GlossaryEvidenceRefs: uniqueSorted(group.glossaryEvidenceIDs), Confidence: group.confidence, EpistemicState: group.epistemicState,
			CoverageBoundary: copyCoverage(request.Map.Coverage),
			SelectionReason:  groupSelectionReason(group.epistemicState),
			MissingEvidence:  uniqueSorted(group.missingEvidence),
			RedactionStatus:  status,
		})
	}
	return result, uniqueSorted(unmapped), uniqueUnknowns(unknowns), nil
}

func resolveCandidateDomain(request OnboardingRequestV2, candidate CandidateEntry, step SemanticStep, entryRefs []string) (name, state string, confidence float64, domainRefs, missingEvidence []string, selectionReason string, ok bool) {
	domainRefs = validDomainEvidenceRefs(request.Map, candidate.DomainEvidenceRefs)
	if len(domainRefs) == 0 {
		// Accept an explicitly typed domain-label item even when older callers
		// placed it in the general EvidenceRefs list.  A generic source item is
		// never promoted by this fallback because validDomainEvidenceRefs checks
		// the Evidence kind.
		domainRefs = validDomainEvidenceRefs(request.Map, candidate.EvidenceRefs)
	}
	provided := strings.TrimSpace(candidate.Domain)
	observedRoot := sourceRootForPath(candidate.EntrySymbolPath)
	if candidate.SourceRoot != "" && (strings.TrimSpace(candidate.SourceRoot) != candidate.SourceRoot || candidate.SourceRoot != observedRoot || hasOnboardingControlOrWhitespace(candidate.SourceRoot)) {
		return "", "", 0, nil, nil, "source-root signal does not match the canonical entry path", false
	}
	derived := deriveDomainSignal(candidate)
	state, confidence = "candidate", 0.75
	if !refsAreVerified(request.Map, entryRefs) {
		state, confidence = "unknown", 0.25
		missingEvidence = append(missingEvidence, "entryEvidenceValidation")
	}
	if len(domainRefs) > 0 {
		if provided == "" {
			provided = derived
		}
		if provided == "" {
			return "", "", 0, nil, nil, "domain-label Evidence has no canonical domain signal", false
		}
		if derived != "" && !sameDomainLabel(provided, derived) {
			return "", "", 0, nil, nil, "domain label conflicts with the canonical module/package signal", false
		}
		if !refsAreVerified(request.Map, domainRefs) {
			state, confidence = "unknown", 0.25
			missingEvidence = append(missingEvidence, "domainEvidenceValidation")
		} else {
			state, confidence = "confirmed", 1
		}
		return provided, state, confidence, domainRefs, missingEvidence, groupSelectionReason(state), true
	}
	if derived == "" {
		return "", "", 0, nil, nil, "domain ownership is unknown and no canonical module/package signal is present", false
	}
	if provided != "" && !sameDomainLabel(provided, derived) {
		// Generic source Evidence proves the entry, never an arbitrary business
		// label supplied by the caller.
		return "", "", 0, nil, nil, "arbitrary domain label is unsupported by canonical module/package Evidence", false
	}
	return derived, state, confidence, nil, missingEvidence, groupSelectionReason(state), true
}

func deriveDomainSignal(candidate CandidateEntry) string {
	for _, raw := range []string{candidate.Module, candidate.Package, candidate.SourceRoot, sourceRootForPath(candidate.EntrySymbolPath)} {
		if signal := canonicalDomainLabel(raw); signal != "" {
			return signal
		}
	}
	return ""
}

func canonicalDomainLabel(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || hasOnboardingControlOrWhitespace(raw) {
		return ""
	}
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '/' || r == '\\' || r == '.' || r == '-' || r == '_'
	})
	if len(parts) == 0 {
		return ""
	}
	label := parts[len(parts)-1]
	if label == "" {
		return ""
	}
	runes := []rune(strings.ToLower(label))
	if len(runes) == 0 {
		return ""
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func sameDomainLabel(left, right string) bool {
	return strings.EqualFold(strings.TrimSpace(left), strings.TrimSpace(right))
}

func groupSelectionReason(state string) string {
	if state == "confirmed" {
		return "domain label is confirmed only by explicit canonical domain Evidence"
	}
	if state == "unknown" {
		return "domain label follows a canonical source signal, but Evidence validation is unresolved"
	}
	return "domain label is derived from canonical module/package/source-root signals and remains a candidate"
}

func mergeOnboardingEpistemicState(left, right string) string {
	if left == "unknown" || right == "unknown" {
		return "unknown"
	}
	if left == "candidate" || right == "candidate" {
		return "candidate"
	}
	return "confirmed"
}

func canonicalCandidatesForDomain(request OnboardingRequestV2, name string) []CandidateEntry {
	result := make([]CandidateEntry, 0)
	for _, candidate := range request.Candidates {
		step, ok := findOnboardingStep(request.Map, candidate.EntrySymbolPath)
		if !ok {
			continue
		}
		entryRefs := validCandidateEvidence(request.Map, candidate)
		// projectOnboardingDomains quarantines candidates whose entry Evidence
		// is missing, stale, unsafe, or otherwise invalid. Do not re-admit that
		// same candidate while building the selected catalog, because doing so
		// would emit a flow with empty entryEvidenceRefs. The projector already
		// records the dropped entry in catalog unmapped/unknown recovery.
		if len(entryRefs) == 0 {
			continue
		}
		resolved, _, _, _, _, _, resolvedOK := resolveCandidateDomain(request, candidate, step, entryRefs)
		if resolvedOK && strings.EqualFold(strings.TrimSpace(resolved), strings.TrimSpace(name)) {
			result = append(result, candidate)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		leftID, rightID := stableCandidateID(result[i]), stableCandidateID(result[j])
		if leftID != rightID {
			return leftID < rightID
		}
		return result[i].EntrySymbolPath < result[j].EntrySymbolPath
	})
	return result
}

func selectDomain(domains []DomainCandidateV2, value string) (DomainCandidateV2, bool) {
	for _, domain := range domains {
		if strings.EqualFold(domain.DomainID, value) || strings.EqualFold(domain.Name, value) {
			return domain, true
		}
	}
	return DomainCandidateV2{}, false
}

func validCandidateEvidence(mapIR *SemanticMapIR, candidate CandidateEntry) []string {
	// A nil slice represents an omitted field after JSON decoding. A non-nil
	// empty slice is an explicit caller assertion and must not trigger the
	// legacy step-level fallback.
	if candidate.EvidenceRefs != nil {
		return validRefs(mapIR, candidate.EvidenceRefs)
	}
	for _, step := range mapIR.Steps {
		if canonicalStepPath(step) == candidate.EntrySymbolPath {
			return validRefs(mapIR, step.EvidenceRefs)
		}
	}
	return nil
}

func validRefs(mapIR *SemanticMapIR, refs []string) []string {
	known := map[string]bool{}
	for _, evidence := range mapIR.Evidence {
		if evidence.EvidenceID != "" && evidence.ComputedBasisID == mapIR.ComputedBasisID && evidence.SnapshotID == mapIR.ValidatedAgainstSnapshotID && onboardingEvidenceUsableStatus(evidence.ValidationStatus) && validOnboardingRedactionStatus(evidence.RedactionStatus) && validateOnboardingAnchor(evidence.Anchor) == nil {
			known[evidence.EvidenceID] = true
		}
	}
	result := make([]string, 0, len(refs))
	for _, ref := range refs {
		if known[ref] {
			result = append(result, ref)
		}
	}
	return uniqueSorted(result)
}

func validDomainEvidenceRefs(mapIR *SemanticMapIR, refs []string) []string {
	allowed := map[string]bool{"domain": true, "domain_label": true, "ownership": true, "glossary": true}
	valid := validRefs(mapIR, refs)
	result := make([]string, 0, len(valid))
	for _, ref := range valid {
		for _, evidence := range mapIR.Evidence {
			if evidence.EvidenceID == ref && allowed[strings.ToLower(strings.TrimSpace(evidence.Kind))] {
				result = append(result, ref)
				break
			}
		}
	}
	return uniqueSorted(result)
}

func validOnboardingRedactionStatus(status string) bool {
	return status == "clean" || status == "redacted" || status == "passed"
}

func onboardingEvidenceUsableStatus(status string) bool {
	return status == "verified" || status == "unknown" || status == "pending"
}

func refsAreVerified(mapIR *SemanticMapIR, refs []string) bool {
	if len(refs) == 0 {
		return false
	}
	byID := make(map[string]string, len(mapIR.Evidence))
	for _, evidence := range mapIR.Evidence {
		byID[evidence.EvidenceID] = evidence.ValidationStatus
	}
	for _, ref := range refs {
		if byID[ref] != "verified" {
			return false
		}
	}
	return true
}

func findOnboardingStep(mapIR *SemanticMapIR, entry string) (SemanticStep, bool) {
	if mapIR == nil {
		return SemanticStep{}, false
	}
	for _, step := range mapIR.Steps {
		if canonicalStepPath(step) == entry {
			return step, true
		}
	}
	return SemanticStep{}, false
}

func deriveMutations(mapIR *SemanticMapIR, entry string) []string {
	for _, step := range mapIR.Steps {
		if canonicalStepPath(step) != entry {
			continue
		}
		mutations := make([]string, 0, 2)
		if step.StateDelta != nil {
			mutations = append(mutations, step.StateDelta.After)
		}
		if step.SideEffect != nil {
			mutations = append(mutations, *step.SideEffect)
		}
		return uniqueSorted(mutations)
	}
	return []string{}
}

func candidateScore(candidate CandidateEntry, entryRefs, resultRefs []string) float64 {
	if candidate.SelectionScore > 0 {
		return candidate.SelectionScore
	}
	return float64(len(entryRefs)*2 + len(resultRefs))
}

func stableCandidateID(candidate CandidateEntry) string {
	if strings.TrimSpace(candidate.CandidateID) != "" {
		return candidate.CandidateID
	}
	return candidate.EntrySymbolPath
}

func stableDomainID(name string, refs []string) string {
	canonical := strings.ToLower(strings.TrimSpace(name)) + "\x00" + strings.Join(uniqueSorted(refs), "\x00")
	hash := sha256.Sum256([]byte(canonical))
	return "domain-" + hex.EncodeToString(hash[:])[:16]
}

func canonicalStepPath(step SemanticStep) string {
	// The public entry identity is the validated source anchor.  Compiler
	// StructuralIdentity is intentionally an internal NUL-delimited identity
	// and must not be compared directly with index paths (`path#symbol`).
	if step.Anchor.RepoRelativePath != "" && step.Anchor.EnclosingSymbolPath != "" {
		return step.Anchor.RepoRelativePath + "#" + step.Anchor.EnclosingSymbolPath
	}
	identity := step.StructuralIdentity
	if strings.Contains(identity, "\x00") {
		parts := strings.Split(identity, "\x00")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			return parts[0] + "#" + parts[1]
		}
	}
	return strings.TrimSpace(identity)
}

func buildDomainRationale(candidates []CandidateEntry, refs []string) string {
	roots, modules, packages := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, candidate := range candidates {
		root := candidate.SourceRoot
		if root == "" {
			root = sourceRootForPath(candidate.EntrySymbolPath)
		}
		if root != "" {
			roots[root] = true
		}
		if candidate.Module != "" {
			modules[candidate.Module] = true
		}
		if candidate.Package != "" {
			packages[candidate.Package] = true
		}
	}
	return fmt.Sprintf("derived from source roots %s, modules %s, packages %s and selected Evidence %s", joinSet(roots), joinSet(modules), joinSet(packages), strings.Join(uniqueSorted(refs), ","))
}

func sourceRootForPath(entry string) string {
	path := entry
	if index := strings.Index(path, "#"); index >= 0 {
		path = path[:index]
	}
	if index := strings.Index(path, "/"); index >= 0 {
		return path[:index]
	}
	return path
}

func joinSet(values map[string]bool) string {
	items := make([]string, 0, len(values))
	for value := range values {
		items = append(items, value)
	}
	sort.Strings(items)
	if len(items) == 0 {
		return "unknown"
	}
	return strings.Join(items, ",")
}

func copyCoverage(coverage *CoverageBoundary) CoverageBoundary {
	if coverage == nil {
		return CoverageBoundary{IncludedSourceRoots: []string{}, ExcludedReasons: []string{}}
	}
	return CoverageBoundary{IncludedSourceRoots: uniqueSorted(append([]string(nil), coverage.IncludedSourceRoots...)), ExcludedReasons: uniqueSorted(append([]string(nil), coverage.ExcludedReasons...))}
}

func onboardingRecovery(unmapped []string, unknowns []OnboardingUnknownV2) []string {
	if len(unmapped) == 0 && len(unknowns) == 0 {
		return []string{}
	}
	return []string{"extend graph/index/source-root coverage for unmapped items", "relink or provide verified Evidence before treating an unknown as confirmed"}
}

func unknownStrings(unmapped []string, unknowns []OnboardingUnknownV2) []string {
	result := append([]string(nil), unmapped...)
	for _, unknown := range unknowns {
		result = append(result, unknown.Subject)
	}
	return uniqueSorted(result)
}

func uniqueUnknowns(values []OnboardingUnknownV2) []OnboardingUnknownV2 {
	seen := map[string]bool{}
	result := make([]OnboardingUnknownV2, 0, len(values))
	for _, value := range values {
		if value.Subject == "" || seen[value.Subject] {
			continue
		}
		seen[value.Subject] = true
		result = append(result, value)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Subject < result[j].Subject })
	return result
}

func unionSorted(groups ...[]string) []string {
	result := make([]string, 0)
	for _, group := range groups {
		result = append(result, group...)
	}
	return uniqueSorted(result)
}

func uniqueSorted(values []string) []string {
	if values == nil {
		return []string{}
	}
	result := append([]string(nil), values...)
	if len(result) == 0 {
		return []string{}
	}
	sort.Strings(result)
	write := 0
	for _, value := range result {
		if strings.TrimSpace(value) == "" || (write > 0 && result[write-1] == value) {
			continue
		}
		result[write] = value
		write++
	}
	return result[:write]
}

func redactOnboardingText(value string) (string, string) {
	result := secret.Redact(value)
	if result.Count > 0 {
		return result.Text, "redacted"
	}
	return value, "clean"
}

func redactTextOrFallback(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		value = fallback
	}
	redacted, _ := redactOnboardingText(value)
	return redacted
}
