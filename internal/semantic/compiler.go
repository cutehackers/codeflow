package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"codeflow/internal/fusion"
	"codeflow/internal/rflscvs02"
	"codeflow/internal/slicing"
)

// CompileOptions contains the immutable basis supplied by VS-01/VS-02. A
// compiler call without snapshot bytes and complete identity is rejected.
type CompileOptions struct {
	ComputedBasisID            string
	WorkspaceEpoch             int64
	GenerationID               string
	ValidatedAgainstSnapshotID string
	SnapshotID                 string
	SnapshotTreeID             string
	RepositoryID               string
	WorktreeID                 string
	DependencyFingerprint      string
	ConfigurationFingerprint   string
	AdapterVersion             string
	AnalyzerRevision           string
	AnalysisReadSetID          string
	CausalObservationClosureID string
	SnapshotFiles              map[string]string
	SnapshotInput              *rflscvs02.SnapshotInput
}

// CompileDeterministicFeatureMap compiles a complete candidate SemanticMapIR
// from measured slice facts and immutable snapshot bytes. It never performs a
// filesystem read and never grants current or settled authority.
func CompileDeterministicFeatureMap(target *ResolvedTarget, intent *TaskIntent, sliceResult *slicing.SlicedPayload, opts CompileOptions) (*SemanticMapIR, *FlowViewProjection, error) {
	if target == nil {
		return nil, nil, fmt.Errorf("missing_precondition: target cannot be nil")
	}
	if sliceResult == nil {
		return nil, nil, fmt.Errorf("missing_precondition: sliceResult cannot be nil")
	}
	if intent != nil && intent.IntentStatus == "needs_confirmation" {
		return nil, nil, &QueryError{Code: ErrCodeAmbiguousTarget, Message: "task intent has unresolved interpretations and requires explicit user confirmation", CandidateTargets: intentConfirmationCandidates(intent)}
	}
	if strings.TrimSpace(target.FlowID) == "" || strings.TrimSpace(target.EntrySymbolPath) == "" {
		return nil, nil, fmt.Errorf("missing_precondition: target identity is incomplete")
	}
	if strings.TrimSpace(opts.ComputedBasisID) == "" {
		return nil, nil, fmt.Errorf("missing_precondition: computed basis identity is required")
	}
	if strings.TrimSpace(opts.SnapshotID) == "" || strings.TrimSpace(opts.ValidatedAgainstSnapshotID) == "" {
		return nil, nil, fmt.Errorf("missing_precondition: validated snapshot identity is required")
	}
	if opts.SnapshotInput == nil {
		return nil, nil, fmt.Errorf("missing_precondition: validated snapshot input is required")
	}
	if strings.TrimSpace(opts.SnapshotTreeID) == "" || strings.TrimSpace(opts.DependencyFingerprint) == "" {
		return nil, nil, fmt.Errorf("missing_precondition: snapshot tree and dependency identity are required")
	}
	if strings.TrimSpace(opts.AnalysisReadSetID) == "" {
		return nil, nil, fmt.Errorf("missing_precondition: analysis read-set identity is required")
	}
	if strings.TrimSpace(opts.CausalObservationClosureID) == "" {
		return nil, nil, fmt.Errorf("missing_precondition: causal observation closure identity is required")
	}
	input := opts.SnapshotInput
	if input.SnapshotID != opts.SnapshotID || input.SnapshotID != opts.ValidatedAgainstSnapshotID || input.ComputedBasisID != opts.ComputedBasisID || input.RootTreeID != opts.SnapshotTreeID || input.DependencyFingerprint != opts.DependencyFingerprint || input.WorkspaceEpoch != opts.WorkspaceEpoch {
		return nil, nil, fmt.Errorf("incomparable_basis: compiler options do not match validated snapshot input")
	}
	if err := validateCompilerSnapshot(input, opts.SnapshotFiles); err != nil {
		return nil, nil, fmt.Errorf("invalid_snapshot: %w", err)
	}
	if err := validateCompilerVS02Result(sliceResult, input, opts); err != nil {
		return nil, nil, fmt.Errorf("missing_precondition: %w", err)
	}
	evidenceRecords, err := ExtractAndRedactEvidenceFromSnapshot(target, sliceResult, *input)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid_evidence: %w", err)
	}
	evidenceByID := make(map[string]EvidenceRecord, len(evidenceRecords))
	for _, record := range evidenceRecords {
		evidenceByID[record.EvidenceID] = record
	}

	genID := opts.GenerationID
	if genID == "" {
		raw := strings.Join([]string{opts.ComputedBasisID, opts.SnapshotID, target.FlowID, fmt.Sprint(opts.WorkspaceEpoch)}, "\x00")
		h := sha256.Sum256([]byte(raw))
		genID = "gen-" + hex.EncodeToString(h[:])[:12]
	}

	semanticSteps := make([]SemanticStep, 0, len(sliceResult.Steps))
	evidence := make([]SemanticEvidence, 0, len(sliceResult.Steps))
	unknowns := make([]fusion.Unknown, 0)
	stepByOrdinal := make(map[int]SemanticStep, len(sliceResult.Steps))
	stepBySymbol := make(map[string][]SemanticStep, len(sliceResult.Steps))
	stepIDs := make(map[string]bool, len(sliceResult.Steps))
	boundaryTargets := make([]string, 0)
	for _, sourceStep := range sliceResult.Steps {
		if strings.TrimSpace(sourceStep.Anchor.RepoRelativePath) == "" {
			return nil, nil, fmt.Errorf("invalid_identity: step %d has no source path", sourceStep.Ordinal)
		}
		stepID, err := fusion.ComputeStructuralStepID(target.FlowID, sourceStep.Anchor, sourceStep.SymbolPath, sourceStep.Kind)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid_identity: %w", err)
		}
		if stepIDs[stepID] {
			stepID, err = fusion.ComputeDisambiguatedStructuralStepID(target.FlowID, sourceStep.Anchor, sourceStep.SymbolPath, sourceStep.Kind)
			if err != nil {
				return nil, nil, fmt.Errorf("ambiguous_target: %w", err)
			}
			if stepIDs[stepID] {
				return nil, nil, fmt.Errorf("ambiguous_target: duplicate structural step identity %s", stepID)
			}
		}
		stepIDs[stepID] = true
		evidenceID := EvidenceIDForAnchor(target.FlowID, sourceStep.Anchor)
		record, ok := evidenceByID[evidenceID]
		if !ok || record.ValidationStatus != "verified" || record.CodeLens == nil {
			return nil, nil, fmt.Errorf("invalid_evidence: step %s has no validated evidence", stepID)
		}
		semanticStep := SemanticStep{
			StepID:             stepID,
			StructuralIdentity: structuralIdentity(sourceStep.Anchor, sourceStep.SymbolPath, sourceStep.Kind),
			Ordinal:            sourceStep.Ordinal,
			Name:               sourceStep.Description,
			TechnicalName:      sourceStep.SymbolPath,
			Layer:              sourceStep.Layer,
			Kind:               sourceStep.Kind,
			Anchor:             sourceStep.Anchor,
			CodeLens:           record.CodeLens,
			SideEffect:         sourceStep.EffectTarget,
			Branch:             sourceStep.GuardCondition,
			EvidenceRefs:       []string{evidenceID},
		}
		if semanticStep.Name == "" {
			semanticStep.Name = sourceStep.SymbolPath
		}
		if sourceStep.StateBefore != nil || sourceStep.StateAfter != nil {
			semanticStep.StateDelta = &fusion.StateDelta{Before: deref(sourceStep.StateBefore), After: deref(sourceStep.StateAfter)}
		}
		semanticSteps = append(semanticSteps, semanticStep)
		stepByOrdinal[sourceStep.Ordinal] = semanticStep
		if sourceStep.SymbolPath != "" {
			stepBySymbol[sourceStep.SymbolPath] = append(stepBySymbol[sourceStep.SymbolPath], semanticStep)
			// Adapters carry canonical relation identities as
			// "repo/path.ext#Symbol" while step.SymbolPath is the language
			// symbol only. Keep both forms bound to the same canonical step so
			// an emitted edge never degrades to an unverified boundary merely
			// because the transport included its source path.
			canonicalSymbol := sourceStep.Anchor.RepoRelativePath + "#" + sourceStep.SymbolPath
			stepBySymbol[canonicalSymbol] = append(stepBySymbol[canonicalSymbol], semanticStep)
		}
		evidence = append(evidence, SemanticEvidence{
			EvidenceID:         evidenceID,
			Kind:               "source",
			SourceAuthority:    "code",
			ComputedBasisID:    opts.ComputedBasisID,
			DocumentRevisionID: record.DocumentRevisionID,
			Anchor:             sourceStep.Anchor,
			Producer:           &ProducerInfo{Name: sliceResult.Language, Version: sliceResult.AnalyzerVersion},
			ValidationStatus:   record.ValidationStatus,
			RedactionStatus:    record.RedactionStatus,
			SnapshotID:         opts.SnapshotID,
			ByteRange:          sourceStep.Anchor.ByteRange,
			LineRange:          [2]int{record.CodeLens.StartLine, record.CodeLens.EndLine},
		})
	}
	if result := sliceResult.ValidatedResult; result != nil && result.Closure.Status != "closed" {
		reason := strings.Join(result.Closure.IncompleteReasons, ", ")
		if reason == "" {
			reason = result.Closure.Status
		}
		unknowns = append(unknowns, fusion.Unknown{Subject: "causalObservationClosure", Reason: "closure is open: " + reason})
		for i := range evidence {
			evidence[i].ValidationStatus = "unknown"
		}
	}

	semanticEdges := make([]SemanticEdge, 0, len(sliceResult.Edges))
	for _, sourceEdge := range sliceResult.Edges {
		if sourceEdge.StepOrdinal == nil {
			return nil, nil, fmt.Errorf("invalid_identity: edge %q has no source step identity", sourceEdge.ToSymbolPath)
		}
		from, ok := stepByOrdinal[*sourceEdge.StepOrdinal]
		if !ok || from.StepID == "" {
			return nil, nil, fmt.Errorf("invalid_identity: edge source ordinal %d is not a sliced step", *sourceEdge.StepOrdinal)
		}
		if strings.TrimSpace(sourceEdge.ToSymbolPath) == "" {
			return nil, nil, fmt.Errorf("invalid_identity: edge target symbol is empty")
		}
		status := sourceEdge.ResolutionStatus
		if status == "" {
			status = "unknown"
		}
		toID := ""
		if candidates := stepBySymbol[sourceEdge.ToSymbolPath]; len(candidates) == 1 {
			toID = candidates[0].StepID
		} else if len(candidates) > 1 {
			// A relation-level ambiguity is not a query-target ambiguity. The
			// compiler must preserve the unresolved boundary and continue
			// building the candidate map rather than selecting one step or
			// failing the entire feature request. Query selection alone uses
			// ambiguous_target.
			toID = boundaryTargetID(target.FlowID, sourceEdge.ToSymbolPath, sourceEdge.Kind)
			if !containsString(boundaryTargets, toID) {
				boundaryTargets = append(boundaryTargets, toID)
			}
			unknowns = append(unknowns, fusion.Unknown{Subject: sourceEdge.ToSymbolPath, Reason: "edge target matches multiple canonical steps; relation remains unresolved"})
			continue
		} else {
			// An unresolved target is retained as an explicit unknown, but is
			// not emitted as an edge. Every emitted edge must point at a
			// canonical step in this map. BoundaryTargets remains a display
			// hint for callers that need the unresolved selector.
			toID = boundaryTargetID(target.FlowID, sourceEdge.ToSymbolPath, sourceEdge.Kind)
			if !containsString(boundaryTargets, toID) {
				boundaryTargets = append(boundaryTargets, toID)
			}
			unknowns = append(unknowns, fusion.Unknown{Subject: sourceEdge.ToSymbolPath, Reason: "edge target is outside the selected structural slice"})
			continue
		}
		if status != "resolved" {
			unknowns = append(unknowns, fusion.Unknown{Subject: sourceEdge.ToSymbolPath, Reason: "edge resolution is not verified: " + status})
		}
		if !stepIDs[toID] {
			return nil, nil, fmt.Errorf("invalid_identity: edge target %s is not a canonical step", sourceEdge.ToSymbolPath)
		}
		semanticEdges = append(semanticEdges, SemanticEdge{FromStepID: from.StepID, ToStepID: toID, ToSymbolPath: sourceEdge.ToSymbolPath, Kind: sourceEdge.Kind, ResolutionStatus: status})
	}

	sort.SliceStable(semanticSteps, func(i, j int) bool { return semanticSteps[i].Ordinal < semanticSteps[j].Ordinal })
	sort.SliceStable(evidence, func(i, j int) bool { return evidence[i].EvidenceID < evidence[j].EvidenceID })

	taskID, taskRevision, taskStatus, taskMode, requested := "task-unknown", 1, "parsed", "feature", target.Title
	var criteria []AcceptanceCriterion
	if intent != nil {
		taskID, taskRevision, taskStatus, taskMode, requested = intent.TaskID, intent.Revision, intent.IntentStatus, intent.Mode, intent.Request.RawRequest
		criteria = intent.AcceptanceCriteria
	}
	mapIR := &SemanticMapIR{
		SchemaID:                   SemanticMapSchemaID,
		SchemaVersion:              SemanticSchemaVersion,
		MapID:                      "map-" + target.FlowID,
		GenerationID:               genID,
		ComputedBasisID:            opts.ComputedBasisID,
		ValidatedAgainstSnapshotID: opts.ValidatedAgainstSnapshotID,
		PublicationKind:            "initial",
		Freshness:                  "historical",
		Settlement:                 "pending",
		EnrichmentStatus:           "not_requested",
		Authority:                  "candidate",
		Task:                       MapTaskContext{TaskID: taskID, IntentRevision: taskRevision, IntentStatus: taskStatus, Mode: taskMode},
		Basis:                      MapBasisContext{RepositoryID: opts.RepositoryID, WorktreeID: opts.WorktreeID, WorkspaceEpoch: opts.WorkspaceEpoch, ComputedWorkspaceSnapshotID: opts.SnapshotID, ComputedBasisID: opts.ComputedBasisID, SnapshotTreeID: opts.SnapshotTreeID, DependencyFingerprint: opts.DependencyFingerprint, ConfigurationFingerprint: opts.ConfigurationFingerprint, AnalysisReadSetID: opts.AnalysisReadSetID, CausalObservationClosureID: opts.CausalObservationClosureID},
		Summary:                    MapSummary{Requested: requested, Current: candidateSummary(semanticSteps)},
		Steps:                      semanticSteps,
		Edges:                      semanticEdges,
		BoundaryTargets:            boundaryTargets,
		Evidence:                   evidence,
		Unknowns:                   unknowns,
		Coverage:                   &CoverageBoundary{IncludedSourceRoots: sourceRoots(semanticSteps), ExcludedReasons: []string{}},
	}
	if len(criteria) > 0 {
		mapIR.RequirementAlignment = ComputeRequirementAlignment(criteria, mapIR, AlignmentOptions{})
	}
	mapIR.Quality = qualityForCandidate(mapIR)
	projection := BuildFlowViewProjection(mapIR)
	if err := ValidateFlowViewProjectionAgainstMap(projection, mapIR); err != nil {
		return nil, nil, err
	}
	return mapIR, projection, nil
}

func validateCompilerVS02Result(sliceResult *slicing.SlicedPayload, input *rflscvs02.SnapshotInput, opts CompileOptions) error {
	if sliceResult == nil || sliceResult.ValidatedResult == nil {
		return fmt.Errorf("validated VS-02 slice result is required")
	}
	result := sliceResult.ValidatedResult
	if result.Operation != "slice" || result.RequestID == "" {
		return fmt.Errorf("validated VS-02 slice result has incomplete operation identity")
	}
	request, err := rflscvs02.NewAnalyzerRequest(result.RequestID, result.Operation, *input, nil, result.Closure.RequiredObservations)
	if err != nil {
		return fmt.Errorf("construct validation request: %w", err)
	}
	if err := rflscvs02.ValidateResult(request, *result); err != nil {
		return err
	}
	if result.ReadSet.ReadSetID != opts.AnalysisReadSetID {
		return fmt.Errorf("analysis read-set identity does not match compiler options")
	}
	if result.Closure.ClosureID != opts.CausalObservationClosureID {
		return fmt.Errorf("causal observation closure identity does not match compiler options")
	}
	if result.ComputedBasisID != opts.ComputedBasisID || result.WorkspaceEpoch != opts.WorkspaceEpoch || result.SnapshotID != opts.SnapshotID || result.SnapshotTreeDigest != opts.SnapshotTreeID || result.DependencyFingerprint != opts.DependencyFingerprint {
		return fmt.Errorf("validated VS-02 result basis does not match compiler options")
	}
	if result.AnalyzerRevision == "" || result.AdapterVersion == "" || sliceResult.AnalyzerVersion == "" || sliceResult.AnalyzerVersion != result.AnalyzerRevision {
		return fmt.Errorf("validated VS-02 analyzer identity is incomplete or inconsistent")
	}
	if opts.AnalyzerRevision != "" && opts.AnalyzerRevision != result.AnalyzerRevision {
		return fmt.Errorf("analyzer revision does not match compiler options")
	}
	if opts.AdapterVersion != "" && opts.AdapterVersion != result.AdapterVersion {
		return fmt.Errorf("adapter version does not match compiler options")
	}
	if !result.Coverage.Measured || len(result.Coverage.IncludedSourceRoots) == 0 {
		return fmt.Errorf("validated VS-02 coverage is not measured")
	}
	if err := validateCoverageRoots(*input, result.Coverage.IncludedSourceRoots); err != nil {
		return err
	}
	if !metadataMatchesResult(sliceResult, *result) {
		return fmt.Errorf("slice payload metadata does not match validated VS-02 result")
	}
	var canonical slicing.SlicedPayload
	if err := json.Unmarshal(result.Payload, &canonical); err != nil {
		return fmt.Errorf("validated slice operation payload is invalid: %w", err)
	}
	if !sameSliceOperationPayload(sliceResult, &canonical) {
		return fmt.Errorf("slice payload does not match validated operation payload")
	}
	return nil
}

func metadataMatchesResult(payload *slicing.SlicedPayload, result rflscvs02.Result) bool {
	if payload.ComputedBasisID != result.ComputedBasisID || payload.WorkspaceEpoch != result.WorkspaceEpoch || payload.SnapshotID != result.SnapshotID || payload.RootTreeID != result.SnapshotTreeDigest || payload.DependencyFingerprint != result.DependencyFingerprint || payload.AnalyzerVersion != result.AnalyzerRevision || payload.AdapterVersion != result.AdapterVersion {
		return false
	}
	if stringMetadata(payload.AnalysisReadSet, "readSetId") != result.ReadSet.ReadSetID || stringMetadata(payload.AnalysisReadSet, "computedBasisId") != result.ReadSet.ComputedBasisID || intMetadata(payload.AnalysisReadSet, "workspaceEpoch") != result.ReadSet.WorkspaceEpoch {
		return false
	}
	if stringMetadata(payload.CausalObservationClosure, "closureId") != result.Closure.ClosureID || stringMetadata(payload.CausalObservationClosure, "analysisReadSetId") != result.Closure.AnalysisReadSetID || stringMetadata(payload.CausalObservationClosure, "computedBasisId") != result.Closure.ComputedBasisID || intMetadata(payload.CausalObservationClosure, "workspaceEpoch") != result.Closure.WorkspaceEpoch {
		return false
	}
	return true
}

func stringMetadata(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

// MetadataString reads a protocol-bound string identity from an unwrapped
// slice payload. Callers should pass values populated by the v2 protocol gate.
func MetadataString(values map[string]any, key string) string {
	return stringMetadata(values, key)
}

func intMetadata(values map[string]any, key string) int64 {
	switch value := values[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		parsed, _ := value.Int64()
		return parsed
	default:
		return -1
	}
}

func sameSliceOperationPayload(left, right *slicing.SlicedPayload) bool {
	if left == nil || right == nil {
		return false
	}
	return left.CandidateID == right.CandidateID && left.Language == right.Language && left.EntrySymbolPath == right.EntrySymbolPath && reflect.DeepEqual(left.Steps, right.Steps) && reflect.DeepEqual(left.Edges, right.Edges) && left.Truncated == right.Truncated && left.VisitedCycleDetected == right.VisitedCycleDetected && left.RedactedCount == right.RedactedCount
}

func validateCoverageRoots(input rflscvs02.SnapshotInput, roots []string) error {
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" || strings.HasPrefix(root, "/") || root == ".." || strings.HasPrefix(root, "../") || strings.Contains(root, "/../") {
			return fmt.Errorf("coverage root %q is not repository-relative", root)
		}
		matched := false
		for _, document := range input.Documents {
			if root == "." || document.Path == root || strings.HasPrefix(document.Path, root+"/") {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("coverage root %q is outside the validated snapshot", root)
		}
	}
	return nil
}

func qualityForCandidate(mapIR *SemanticMapIR) MapQuality {
	obligations := make([]CriticalObligation, 0)
	if len(mapIR.Steps) > 0 {
		entry := mapIR.Steps[0]
		obligations = append(obligations, CriticalObligation{ObligationID: "ob-entry-01", Kind: "entry", Required: true, TargetRef: entry.StepID, Status: "verified", EvidenceRefs: append([]string(nil), entry.EvidenceRefs...)})
		last := mapIR.Steps[len(mapIR.Steps)-1]
		obligations = append(obligations, CriticalObligation{ObligationID: "ob-result-01", Kind: "result", Required: true, TargetRef: last.StepID, Status: "verified", EvidenceRefs: append([]string(nil), last.EvidenceRefs...)})
		obligations = append(obligations, CriticalObligation{ObligationID: "ob-causal-01", Kind: "causal_chain", Required: true, TargetRef: mapIR.MapID, Status: "pending"})
	}
	for _, step := range mapIR.Steps {
		if step.Kind == "decision" || step.Kind == "guard" || step.Kind == "branch" {
			obligations = append(obligations, CriticalObligation{ObligationID: "ob-branch-" + step.StepID, Kind: "critical_branch", Required: true, TargetRef: step.StepID, Status: "verified", EvidenceRefs: append([]string(nil), step.EvidenceRefs...)})
		}
	}
	unknownStatus := "verified"
	unresolved := 0
	if len(mapIR.Unknowns) > 0 {
		unknownStatus, unresolved = "unknown", 1
	}
	obligations = append(obligations, CriticalObligation{ObligationID: "ob-unknown-01", Kind: "no_critical_unknown", Required: true, TargetRef: mapIR.MapID, Status: unknownStatus})
	return MapQuality{Stage: "Q1", CriticalObligations: obligations, CriticalCoverageSummary: &CriticalCoverageSummary{Required: len(obligations), Verified: len(obligations) - unresolved}, UnresolvedCriticalCount: unresolved, Degradations: []QualityDegradation{{Code: "awaiting_current_proof", Impact: "candidate map has no VS-03 current authority", RecoveryCondition: "run VS-03 current proof"}}}
}

func validateCompilerSnapshot(input *rflscvs02.SnapshotInput, files map[string]string) error {
	if input.SnapshotID == "" || input.ComputedBasisID == "" || input.RootTreeID == "" || input.DependencyFingerprint == "" || input.WorkspaceEpoch < 0 {
		return fmt.Errorf("snapshot identity is incomplete")
	}
	seen := make(map[string]bool, len(input.Documents))
	for _, document := range input.Documents {
		if document.Path == "" || seen[document.Path] {
			return fmt.Errorf("snapshot document inventory is invalid")
		}
		seen[document.Path] = true
		if document.RevisionID == "" || document.ContentID == "" || document.DocumentVersion < 1 || document.ByteLength != len(document.Bytes) {
			return fmt.Errorf("snapshot document %s identity is incomplete", document.Path)
		}
	}
	if files != nil {
		if len(files) != len(input.Documents) {
			return fmt.Errorf("snapshot files do not match validated document inventory")
		}
		for _, document := range input.Documents {
			content, ok := files[document.Path]
			if !ok || string(document.Bytes) != content {
				return fmt.Errorf("snapshot file %s differs from validated bytes", document.Path)
			}
		}
	}
	return nil
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func structuralIdentity(anchor slicing.Anchor, symbolPath, kind string) string {
	// Stable identity is based on the task-scoped structural location and
	// relation, not the AST/content fingerprint. The latter is useful only to
	// disambiguate duplicate structural candidates. Keeping it out of the base
	// identity lets a behavior edit remain the same step and be classified as
	// changed rather than as an add/remove pair.
	return strings.Join([]string{anchor.RepoRelativePath, anchor.EnclosingSymbolPath, symbolPath, kind}, "\x00")
}

func boundaryTargetID(flowID, symbolPath, kind string) string {
	h := sha256.Sum256([]byte(strings.Join([]string{flowID, symbolPath, kind}, "\x00")))
	return "boundary-" + hex.EncodeToString(h[:])[:16]
}

func candidateSummary(steps []SemanticStep) string {
	if len(steps) == 0 {
		return "candidate flow has no observed steps"
	}
	return fmt.Sprintf("candidate flow from %s through %d observed steps to %s", steps[0].Name, len(steps), steps[len(steps)-1].Name)
}

func intentConfirmationCandidates(intent *TaskIntent) []string {
	if intent == nil || intent.ScopeHints == nil {
		return nil
	}
	return append([]string(nil), intent.ScopeHints.EntrySymbols...)
}

func sourceRoots(steps []SemanticStep) []string {
	set := make(map[string]bool)
	for _, step := range steps {
		path := step.Anchor.RepoRelativePath
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			path = path[:idx]
		}
		if path != "" {
			set[path] = true
		}
	}
	roots := make([]string, 0, len(set))
	for root := range set {
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return roots
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
