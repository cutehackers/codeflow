package semantic

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"codeflow/internal/secret"
)

const (
	ImpactQuerySchemaID       = "https://codeflow.local/schemas/rflsc.impact-query.v2.schema.json"
	ChangeImpactGraphSchemaID = "https://codeflow.local/schemas/rflsc.change-impact-graph.v2.schema.json"
	ImpactFrontierSchemaID    = "https://codeflow.local/schemas/rflsc.impact-frontier.v1.schema.json"
)

type ChangeImpactGraph struct {
	SchemaID                       string                  `json:"schemaId"`
	SchemaVersion                  int                     `json:"schemaVersion"`
	ImpactGraphID                  string                  `json:"impactGraphId"`
	Target                         ImpactTarget            `json:"target"`
	ComputedBasisID                string                  `json:"computedBasisId"`
	GenerationID                   string                  `json:"generationId"`
	ValidatedAgainstSnapshotID     string                  `json:"validatedAgainstSnapshotId"`
	Freshness                      string                  `json:"freshness"`
	DirectImpact                   DirectImpact            `json:"directImpact"`
	IndirectImpact                 IndirectImpact          `json:"indirectImpact"`
	Frontiers                      []ImpactFrontier        `json:"frontiers"`
	UnresolvedBoundaries           []UnresolvedBoundary    `json:"unresolvedBoundaries"`
	AdditionalExplorationAvailable bool                    `json:"additionalExplorationAvailable"`
	UnknownCount                   int                     `json:"unknownCount"`
	CoverageBoundary               *ImpactCoverageBoundary `json:"coverageBoundary"`
	Evidence                       []SemanticEvidence      `json:"evidence"`
}

type ImpactTarget struct {
	SymbolID      string `json:"symbolId,omitempty"`
	ChangeBatchID string `json:"changeBatchId,omitempty"`
}

type ImpactQuery struct {
	SchemaID          string       `json:"schemaId"`
	SchemaVersion     int          `json:"schemaVersion"`
	Target            ImpactTarget `json:"target"`
	ComputedBasisID   string       `json:"computedBasisId"`
	GenerationID      string       `json:"generationId"`
	Freshness         string       `json:"freshness"`
	MaxDepth          int          `json:"maxDepth"`
	MaxNodes          int          `json:"maxNodes"`
	RelationKinds     []string     `json:"relationKinds"`
	ChangeBatchSteps  []string     `json:"changeBatchStepRefs,omitempty"`
	CoverageComplete  bool         `json:"coverageComplete"`
	CurrentProofValid bool         `json:"currentProofVerified"`
}

type DirectImpact struct {
	Callers         []ImpactCallerNode     `json:"callers"`
	StateMutations  []StateMutationImpact  `json:"stateMutations"`
	ExternalEffects []ExternalEffectImpact `json:"externalEffects"`
	RelatedFlows    []RelatedFlowImpact    `json:"relatedFlows"`
	Tests           []TestImpact           `json:"tests"`
}

type IndirectImpact struct {
	Callers                 []ImpactCallerNode     `json:"callers"`
	StateMutations          []StateMutationImpact  `json:"stateMutations"`
	ExternalEffects         []ExternalEffectImpact `json:"externalEffects"`
	RelatedFlows            []RelatedFlowImpact    `json:"relatedFlows"`
	Tests                   []TestImpact           `json:"tests"`
	MaxDepth                int                    `json:"maxDepth"`
	MaxNodes                int                    `json:"maxNodes"`
	ExploredDepth           int                    `json:"exploredDepth"`
	TotalNodeCount          int                    `json:"totalNodeCount"`
	Bounded                 bool                   `json:"bounded"`
	CompletedWithinCoverage bool                   `json:"completedWithinCoverage"`
}

type ImpactCallerNode struct {
	SymbolPath   string   `json:"symbolPath"`
	Name         string   `json:"name"`
	RelationKind string   `json:"relationKind"`
	FilePath     string   `json:"filePath"`
	Depth        int      `json:"depth"`
	Path         []string `json:"path"`
	EvidenceRefs []string `json:"evidenceRefs"`
}
type StateMutationImpact struct {
	TargetState        string   `json:"targetState"`
	MutationKind       string   `json:"mutationKind"`
	TerminalSymbolPath string   `json:"terminalSymbolPath"`
	Depth              int      `json:"depth"`
	Path               []string `json:"path"`
	EvidenceRefs       []string `json:"evidenceRefs"`
}
type ExternalEffectImpact struct {
	EffectKind         string   `json:"effectKind"`
	Target             string   `json:"target"`
	TerminalSymbolPath string   `json:"terminalSymbolPath"`
	Depth              int      `json:"depth"`
	Path               []string `json:"path"`
	EvidenceRefs       []string `json:"evidenceRefs"`
}
type RelatedFlowImpact struct {
	FlowID             string   `json:"flowId"`
	Title              string   `json:"title"`
	TerminalSymbolPath string   `json:"terminalSymbolPath"`
	Depth              int      `json:"depth"`
	Path               []string `json:"path"`
	EvidenceRefs       []string `json:"evidenceRefs"`
}
type TestImpact struct {
	TestSymbolPath     string   `json:"testSymbolPath"`
	TestFile           string   `json:"testFile"`
	TerminalSymbolPath string   `json:"terminalSymbolPath"`
	Depth              int      `json:"depth"`
	Path               []string `json:"path"`
	EvidenceRefs       []string `json:"evidenceRefs"`
}

type ImpactFrontier struct {
	SchemaID      string   `json:"schemaId"`
	SchemaVersion int      `json:"schemaVersion"`
	FrontierID    string   `json:"frontierId"`
	BoundaryType  string   `json:"boundaryType"`
	Target        string   `json:"target"`
	Depth         int      `json:"depth"`
	Reason        string   `json:"reason"`
	Path          []string `json:"path"`
	EvidenceRefs  []string `json:"evidenceRefs"`
	Expandable    bool     `json:"expandable"`
}

type ImpactCoverageBoundary struct {
	IncludedSourceRoots []string `json:"includedSourceRoots"`
	ExcludedReasons     []string `json:"excludedReasons"`
}

type UnresolvedBoundary struct {
	BoundaryType string `json:"boundaryType"`
	Target       string `json:"target"`
	Description  string `json:"description"`
}

type ImpactOptions struct {
	MaxDepth                   int
	MaxNodes                   int
	ComputedBasisID            string
	GenerationID               string
	ValidatedAgainstSnapshotID string
	Freshness                  string
	CurrentProofVerified       bool
	ChangeBatchStepRefs        []string
	RelationKinds              []string
	SupportedRelationKinds     []string
	CoverageComplete           bool
}

type impactQueueNode struct {
	stepID string
	depth  int
	path   []string
}

// ComputeChangeImpact follows verified canonical edges. Reverse traversal
// reports callers and forward traversal reports state, effect and test impact.
func ComputeChangeImpact(target ImpactTarget, mapIR *SemanticMapIR, opts ImpactOptions) (*ChangeImpactGraph, error) {
	if strings.TrimSpace(target.SymbolID) == "" && strings.TrimSpace(target.ChangeBatchID) == "" {
		return nil, errors.New("missing_precondition: either symbolId or changeBatchId must be provided")
	}
	if strings.TrimSpace(target.SymbolID) != "" && strings.TrimSpace(target.ChangeBatchID) != "" {
		return nil, errors.New("invalid_precondition: symbolId and changeBatchId are mutually exclusive")
	}
	if mapIR == nil {
		return nil, errors.New("missing_precondition: a canonical semantic map is required")
	}
	if mapIR.SchemaID != SemanticMapSchemaID || mapIR.SchemaVersion != SemanticSchemaVersion || mapIR.GenerationID == "" || mapIR.ComputedBasisID == "" || mapIR.ValidatedAgainstSnapshotID == "" {
		return nil, errors.New("invalid_graph: semantic map identity is incomplete or non-canonical")
	}
	if opts.ComputedBasisID == "" || opts.GenerationID == "" || opts.ValidatedAgainstSnapshotID == "" || opts.Freshness == "" {
		return nil, errors.New("missing_precondition: explicit generation, basis, snapshot and freshness are required")
	}
	if opts.ComputedBasisID != mapIR.ComputedBasisID || opts.GenerationID != mapIR.GenerationID || opts.ValidatedAgainstSnapshotID != mapIR.ValidatedAgainstSnapshotID {
		return nil, errors.New("incomparable_basis: impact query identity does not match semantic map")
	}
	if opts.Freshness != "current" && opts.Freshness != "historical" {
		return nil, errors.New("invalid_precondition: freshness must be current or historical")
	}
	if opts.Freshness == "current" && !opts.CurrentProofVerified {
		return nil, errors.New("invalid_authority: current impact requires a validated current proof")
	}
	maxDepth := opts.MaxDepth
	if maxDepth <= 0 {
		maxDepth = 2
	}
	if maxDepth > 5 {
		return nil, errors.New("invalid_precondition: maxDepth exceeds 5")
	}
	maxNodes := opts.MaxNodes
	if maxNodes <= 0 {
		maxNodes = 50
	}
	if maxNodes > 50 {
		return nil, errors.New("invalid_precondition: maxNodes exceeds 50")
	}
	opts.MaxDepth = maxDepth
	opts.MaxNodes = maxNodes

	steps, err := validateImpactMap(mapIR)
	if err != nil {
		return nil, err
	}
	roots, err := resolveImpactRoots(target, opts.ChangeBatchStepRefs, mapIR, steps)
	if err != nil {
		return nil, err
	}
	supported := map[string]bool{}
	for _, kind := range opts.SupportedRelationKinds {
		if kind = strings.TrimSpace(kind); kind != "" {
			supported[kind] = true
		}
	}
	requested := map[string]bool{}
	for _, kind := range opts.RelationKinds {
		if kind = strings.TrimSpace(kind); kind != "" {
			requested[kind] = true
		}
	}
	if len(requested) == 0 {
		return nil, errors.New("missing_precondition: explicit relationKinds are required")
	}
	evidenceByID := map[string]SemanticEvidence{}
	for _, evidence := range mapIR.Evidence {
		if evidence.EvidenceID != "" {
			evidenceByID[evidence.EvidenceID] = evidence
		}
	}
	direct := emptyDirectImpact()
	indirect := emptyIndirectImpact(maxDepth, maxNodes)
	frontiers := []ImpactFrontier{}
	frontierKeys := map[string]bool{}
	referencedEvidence := map[string]SemanticEvidence{}
	addFrontier := func(kind, targetID string, depth int, reason string, path, refs []string, expandable bool) {
		key := fmt.Sprintf("%s|%s|%d|%s", kind, targetID, depth, reason)
		if frontierKeys[key] {
			return
		}
		frontierKeys[key] = true
		frontiers = append(frontiers, ImpactFrontier{SchemaID: ImpactFrontierSchemaID, SchemaVersion: 1, FrontierID: fmt.Sprintf("frontier-%03d", len(frontiers)+1), BoundaryType: kind, Target: targetID, Depth: depth, Reason: reason, Path: append([]string{}, path...), EvidenceRefs: append([]string{}, refs...), Expandable: expandable})
	}
	validatedRefs := func(refs []string) ([]string, bool) {
		if len(refs) == 0 {
			return nil, false
		}
		out := []string{}
		seen := map[string]bool{}
		for _, ref := range refs {
			if seen[ref] {
				continue
			}
			evidence, ok := evidenceByID[ref]
			if !ok || !validImpactEvidence(evidence, mapIR.ComputedBasisID, mapIR.ValidatedAgainstSnapshotID) {
				return nil, false
			}
			seen[ref] = true
			out = append(out, ref)
			referencedEvidence[ref] = evidence
		}
		sort.Strings(out)
		return out, true
	}
	visitedAll := map[string]bool{}
	for _, root := range roots {
		visitedAll[root] = true
	}
	nodeCount, exploredDepth := 0, 0
	traversalKinds := impactTraversalRelationKinds(requested)

	reverseVisited := cloneImpactSet(visitedAll)
	reverseQueue := []impactQueueNode{}
	for _, root := range roots {
		reverseQueue = append(reverseQueue, impactQueueNode{stepID: root, path: []string{stepSymbol(steps[root])}})
	}
	for len(reverseQueue) > 0 {
		node := reverseQueue[0]
		reverseQueue = reverseQueue[1:]
		incoming := incomingImpactEdges(mapIR.Edges, node.stepID)
		if node.depth >= maxDepth {
			if hasUnvisitedImpactEdge(incoming, requested, reverseVisited, true) {
				addFrontier("budget_exhausted", stepSymbol(steps[node.stepID]), node.depth, "reverse traversal reached maxDepth", node.path, nil, true)
			}
			continue
		}
		for _, edge := range incoming {
			relationKind := impactEdgeRelationKind(edge.Kind)
			from := steps[edge.FromStepID]
			path := appendImpactPath(node.path, stepSymbol(from))
			if !requested[relationKind] {
				continue
			}
			if !verifiedImpactEdge(edge.ResolutionStatus) {
				addFrontier("unresolved_dynamic_caller", stepSymbol(from), node.depth+1, "caller relation is not statically verified", path, nil, true)
				continue
			}
			if !supported[relationKind] {
				addFrontier("unsupported_capability", stepSymbol(from), node.depth+1, "adapter capability does not cover relation kind "+relationKind, path, nil, true)
				continue
			}
			if reverseVisited[from.StepID] {
				continue
			}
			isNewNode := !visitedAll[from.StepID]
			if isNewNode && nodeCount >= maxNodes {
				addFrontier("budget_exhausted", stepSymbol(from), node.depth+1, "impact node budget exhausted", path, nil, true)
				continue
			}
			refs, valid := validatedRefs(from.EvidenceRefs)
			if !valid {
				addFrontier("invalid_evidence", stepSymbol(from), node.depth+1, "caller relation lacks same-basis verified Evidence", path, nil, false)
				continue
			}
			depth := node.depth + 1
			claim := ImpactCallerNode{SymbolPath: stepSymbol(from), Name: from.Name, RelationKind: relationKind, FilePath: from.Anchor.RepoRelativePath, Depth: depth, Path: path, EvidenceRefs: refs}
			if depth == 1 {
				direct.Callers = append(direct.Callers, claim)
			} else {
				indirect.Callers = append(indirect.Callers, claim)
			}
			reverseVisited[from.StepID] = true
			if isNewNode {
				visitedAll[from.StepID] = true
				nodeCount++
			}
			if depth > exploredDepth {
				exploredDepth = depth
			}
			reverseQueue = append(reverseQueue, impactQueueNode{stepID: from.StepID, depth: depth, path: path})
		}
	}

	forwardVisited := map[string]bool{}
	forwardQueue := []impactQueueNode{}
	for _, root := range roots {
		forwardVisited[root] = true
		path := []string{stepSymbol(steps[root])}
		collectStepImpacts(steps[root], 0, path, mapIR, requested, supported, &direct, &indirect, validatedRefs, addFrontier)
		forwardQueue = append(forwardQueue, impactQueueNode{stepID: root, path: path})
	}
	for len(forwardQueue) > 0 {
		node := forwardQueue[0]
		forwardQueue = forwardQueue[1:]
		outgoing := outgoingImpactEdges(mapIR.Edges, node.stepID)
		if node.depth >= maxDepth {
			if hasUnvisitedImpactEdge(outgoing, traversalKinds, forwardVisited, false) {
				addFrontier("budget_exhausted", stepSymbol(steps[node.stepID]), node.depth, "forward traversal reached maxDepth", node.path, nil, true)
			}
			continue
		}
		for _, edge := range outgoing {
			relationKind := impactEdgeRelationKind(edge.Kind)
			to := steps[edge.ToStepID]
			path := appendImpactPath(node.path, stepSymbol(to))
			if !traversalKinds[relationKind] {
				continue
			}
			if !verifiedImpactEdge(edge.ResolutionStatus) {
				addFrontier("unresolved_dynamic_caller", stepSymbol(to), node.depth+1, "forward relation is not statically verified", path, nil, true)
				continue
			}
			if !supported[relationKind] {
				addFrontier("unsupported_capability", stepSymbol(to), node.depth+1, "adapter capability does not cover relation kind "+relationKind, path, nil, true)
				continue
			}
			if forwardVisited[to.StepID] {
				continue
			}
			isNewNode := !visitedAll[to.StepID]
			if isNewNode && nodeCount >= maxNodes {
				addFrontier("budget_exhausted", stepSymbol(to), node.depth+1, "impact node budget exhausted", path, nil, true)
				continue
			}
			depth := node.depth + 1
			forwardVisited[to.StepID] = true
			if isNewNode {
				visitedAll[to.StepID] = true
				nodeCount++
			}
			if depth > exploredDepth {
				exploredDepth = depth
			}
			collectStepImpacts(to, depth, path, mapIR, requested, supported, &direct, &indirect, validatedRefs, addFrontier)
			forwardQueue = append(forwardQueue, impactQueueNode{stepID: to.StepID, depth: depth, path: path})
		}
	}
	for _, unknown := range mapIR.Unknowns {
		if !unknownTouchesImpact(unknown.Subject, visitedAll, steps) {
			continue
		}
		kind, expandable := "coverage_boundary", false
		lower := strings.ToLower(unknown.Reason)
		if strings.Contains(lower, "dynamic") || strings.Contains(lower, "reflection") || strings.Contains(lower, "dispatch") {
			kind, expandable = "unresolved_dynamic_caller", true
		}
		addFrontier(kind, unknown.Subject, exploredDepth, unknown.Reason, []string{}, nil, expandable)
	}
	if !opts.CoverageComplete && len(frontiers) == 0 {
		addFrontier("coverage_boundary", strings.Join(roots, ","), exploredDepth, "declared capability or coverage is incomplete", []string{}, nil, true)
	}
	sortImpactClaims(&direct, &indirect)
	sort.Slice(frontiers, func(i, j int) bool {
		if frontiers[i].Depth != frontiers[j].Depth {
			return frontiers[i].Depth < frontiers[j].Depth
		}
		if frontiers[i].BoundaryType != frontiers[j].BoundaryType {
			return frontiers[i].BoundaryType < frontiers[j].BoundaryType
		}
		return frontiers[i].Target < frontiers[j].Target
	})
	for i := range frontiers {
		frontiers[i].FrontierID = fmt.Sprintf("frontier-%03d", i+1)
	}
	evidence := []SemanticEvidence{}
	for _, item := range referencedEvidence {
		evidence = append(evidence, item)
	}
	sort.Slice(evidence, func(i, j int) bool { return evidence[i].EvidenceID < evidence[j].EvidenceID })
	unresolved := []UnresolvedBoundary{}
	additional := false
	for _, frontier := range frontiers {
		unresolved = append(unresolved, UnresolvedBoundary{BoundaryType: frontier.BoundaryType, Target: frontier.Target, Description: frontier.Reason})
		additional = additional || frontier.Expandable
	}
	indirect.TotalNodeCount = nodeCount
	indirect.ExploredDepth = exploredDepth
	indirect.CompletedWithinCoverage = opts.CoverageComplete && len(frontiers) == 0
	graph := &ChangeImpactGraph{SchemaID: ChangeImpactGraphSchemaID, SchemaVersion: 2, ImpactGraphID: stableImpactID(target, mapIR, opts), Target: target, ComputedBasisID: mapIR.ComputedBasisID, GenerationID: mapIR.GenerationID, ValidatedAgainstSnapshotID: mapIR.ValidatedAgainstSnapshotID, Freshness: opts.Freshness, DirectImpact: direct, IndirectImpact: indirect, Frontiers: frontiers, UnresolvedBoundaries: unresolved, AdditionalExplorationAvailable: additional, UnknownCount: len(frontiers), CoverageBoundary: cloneImpactCoverage(mapIR.Coverage), Evidence: evidence}
	return redactChangeImpactGraph(graph)
}

func validateImpactMap(mapIR *SemanticMapIR) (map[string]*SemanticStep, error) {
	steps := map[string]*SemanticStep{}
	for i := range mapIR.Steps {
		step := &mapIR.Steps[i]
		if step.StepID == "" || stepSymbol(step) == "" || step.Anchor.RepoRelativePath == "" {
			return nil, errors.New("invalid_graph: step identity is incomplete")
		}
		if steps[step.StepID] != nil {
			return nil, fmt.Errorf("invalid_graph: duplicate step id %q", step.StepID)
		}
		steps[step.StepID] = step
	}
	for _, edge := range mapIR.Edges {
		if edge.FromStepID == "" || edge.ToStepID == "" || edge.ToSymbolPath == "" || edge.Kind == "" || steps[edge.FromStepID] == nil || steps[edge.ToStepID] == nil {
			return nil, errors.New("invalid_graph: edge source or target identity is unresolved")
		}
		to := steps[edge.ToStepID]
		if edge.ToSymbolPath != to.TechnicalName && edge.ToSymbolPath != to.Anchor.EnclosingSymbolPath {
			return nil, fmt.Errorf("invalid_graph: edge target symbol %q does not match step %q", edge.ToSymbolPath, edge.ToStepID)
		}
	}
	return steps, nil
}
func resolveImpactRoots(target ImpactTarget, batchRefs []string, mapIR *SemanticMapIR, steps map[string]*SemanticStep) ([]string, error) {
	if target.SymbolID != "" {
		roots := []string{}
		for _, step := range mapIR.Steps {
			if step.TechnicalName == target.SymbolID || step.Anchor.EnclosingSymbolPath == target.SymbolID {
				roots = append(roots, step.StepID)
			}
		}
		if len(roots) == 0 {
			return nil, fmt.Errorf("not_observed: symbol %q is not in the requested generation", target.SymbolID)
		}
		sort.Strings(roots)
		return roots, nil
	}
	if len(batchRefs) == 0 {
		return nil, errors.New("missing_precondition: changeBatchId requires resolved changed step references")
	}
	seen := map[string]bool{}
	roots := []string{}
	for _, ref := range batchRefs {
		if steps[ref] == nil {
			return nil, fmt.Errorf("invalid_graph: change batch references unknown step %q", ref)
		}
		if !seen[ref] {
			seen[ref] = true
			roots = append(roots, ref)
		}
	}
	sort.Strings(roots)
	return roots, nil
}
func validImpactEvidence(e SemanticEvidence, basis, snapshot string) bool {
	path := filepath.Clean(e.Anchor.RepoRelativePath)
	if e.EvidenceID == "" || strings.TrimSpace(e.Kind) == "" || e.ValidationStatus != "verified" || e.ComputedBasisID != basis || e.SnapshotID != snapshot || path == "." || filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) || !allowedEvidenceAuthority(e.SourceAuthority) {
		return false
	}
	r := e.Anchor.ByteRange
	if e.ByteRange != [2]int{} {
		r = e.ByteRange
	}
	line := e.LineRange
	return r[0] >= 0 && r[1] > r[0] && line[0] >= 1 && line[1] >= line[0]
}
func collectStepImpacts(step *SemanticStep, depth int, path []string, mapIR *SemanticMapIR, requested, supported map[string]bool, direct *DirectImpact, indirect *IndirectImpact, validatedRefs func([]string) ([]string, bool), addFrontier func(string, string, int, string, []string, []string, bool)) {
	if step == nil {
		return
	}
	wantsState := requested["state_mutation"] && step.StateDelta != nil
	wantsEffect := requested["external_effect"] && step.SideEffect != nil && strings.TrimSpace(*step.SideEffect) != ""
	wantsTest := requested["related_test"] && hasImpactTestRule(step.Rules)
	wantsFlow := depth == 0 && requested["related_flow"]
	for kind, wanted := range map[string]bool{"state_mutation": wantsState, "external_effect": wantsEffect, "related_test": wantsTest, "related_flow": wantsFlow} {
		if wanted && !supported[kind] {
			addFrontier("unsupported_capability", stepSymbol(step), depth, "adapter capability does not cover relation kind "+kind, path, nil, true)
		}
	}
	wantsSupportedClaim := wantsState && supported["state_mutation"] || wantsEffect && supported["external_effect"] || wantsTest && supported["related_test"] || wantsFlow && supported["related_flow"]
	var refs []string
	valid := false
	if wantsSupportedClaim {
		refs, valid = validatedRefs(step.EvidenceRefs)
		if !valid {
			addFrontier("invalid_evidence", stepSymbol(step), depth, "impact relation lacks same-basis verified Evidence", path, nil, false)
			return
		}
	}
	if wantsState && supported["state_mutation"] {
		claim := StateMutationImpact{TargetState: fmt.Sprintf("%s → %s", step.StateDelta.Before, step.StateDelta.After), MutationKind: "transition", TerminalSymbolPath: stepSymbol(step), Depth: depth, Path: append([]string{}, path...), EvidenceRefs: refs}
		if depth <= 1 {
			direct.StateMutations = append(direct.StateMutations, claim)
		} else {
			indirect.StateMutations = append(indirect.StateMutations, claim)
		}
	}
	if wantsEffect && supported["external_effect"] {
		kind, target := splitImpactEffect(*step.SideEffect)
		claim := ExternalEffectImpact{EffectKind: kind, Target: target, TerminalSymbolPath: stepSymbol(step), Depth: depth, Path: append([]string{}, path...), EvidenceRefs: refs}
		if depth <= 1 {
			direct.ExternalEffects = append(direct.ExternalEffects, claim)
		} else {
			indirect.ExternalEffects = append(indirect.ExternalEffects, claim)
		}
	}
	if wantsTest && supported["related_test"] {
		for _, rule := range step.Rules {
			if strings.HasPrefix(strings.ToLower(rule), "test:") || strings.Contains(strings.ToLower(rule), "test") {
				claim := TestImpact{TestSymbolPath: rule, TestFile: step.Anchor.RepoRelativePath, TerminalSymbolPath: stepSymbol(step), Depth: depth, Path: append([]string{}, path...), EvidenceRefs: refs}
				if depth <= 1 {
					direct.Tests = append(direct.Tests, claim)
				} else {
					indirect.Tests = append(indirect.Tests, claim)
				}
			}
		}
	}
	if wantsFlow && supported["related_flow"] && valid {
		direct.RelatedFlows = append(direct.RelatedFlows, RelatedFlowImpact{FlowID: mapIR.MapID, Title: mapIR.Summary.Requested, TerminalSymbolPath: stepSymbol(step), Depth: depth, Path: append([]string{}, path...), EvidenceRefs: refs})
	}
}

// SupportedImpactRelationKinds intersects a query's explicit output relation
// kinds with measured adapter capability features and retains any measured
// connectivity capability required to reach those output claims. A feature
// may use the relation name directly or the relation:/impact_relation:
// namespace. No broad analyzer feature implicitly grants an impact relation.
func SupportedImpactRelationKinds(features, requested []string) []string {
	declared := map[string]bool{}
	for _, feature := range features {
		feature = strings.TrimSpace(feature)
		if feature == "" {
			continue
		}
		declared[feature] = true
		for _, prefix := range []string{"relation:", "impact_relation:"} {
			if strings.HasPrefix(feature, prefix) && len(feature) > len(prefix) {
				declared[strings.TrimPrefix(feature, prefix)] = true
			}
		}
	}
	seen := map[string]bool{}
	out := []string{}
	requestedSet := map[string]bool{}
	for _, relation := range requested {
		relation = strings.TrimSpace(relation)
		if relation != "" {
			requestedSet[relation] = true
		}
		if relation != "" && declared[relation] && !seen[relation] {
			seen[relation] = true
			out = append(out, relation)
		}
	}
	for relation := range impactTraversalRelationKinds(requestedSet) {
		if declared[relation] && !seen[relation] {
			seen[relation] = true
			out = append(out, relation)
		}
	}
	sort.Strings(out)
	return out
}

// ImpactCoverageComplete reports only measured, closed coverage for which
// every requested impact relation and required forward connectivity capability
// are explicitly supported.
func ImpactCoverageComplete(measured bool, excludedReasons []string, closureStatus string, requested, supported []string) bool {
	if !measured || len(excludedReasons) != 0 || closureStatus != "closed" || len(requested) == 0 {
		return false
	}
	supportedSet := map[string]bool{}
	for _, relation := range supported {
		supportedSet[relation] = true
	}
	for _, relation := range requested {
		if !supportedSet[relation] {
			return false
		}
	}
	for relation := range impactTraversalRelationKinds(stringSet(requested)) {
		if !supportedSet[relation] {
			return false
		}
	}
	return true
}
func emptyDirectImpact() DirectImpact {
	return DirectImpact{Callers: []ImpactCallerNode{}, StateMutations: []StateMutationImpact{}, ExternalEffects: []ExternalEffectImpact{}, RelatedFlows: []RelatedFlowImpact{}, Tests: []TestImpact{}}
}
func emptyIndirectImpact(depth, nodes int) IndirectImpact {
	return IndirectImpact{Callers: []ImpactCallerNode{}, StateMutations: []StateMutationImpact{}, ExternalEffects: []ExternalEffectImpact{}, RelatedFlows: []RelatedFlowImpact{}, Tests: []TestImpact{}, MaxDepth: depth, MaxNodes: nodes, Bounded: true}
}
func incomingImpactEdges(edges []SemanticEdge, target string) []SemanticEdge {
	out := []SemanticEdge{}
	for _, edge := range edges {
		if edge.ToStepID == target {
			out = append(out, edge)
		}
	}
	return out
}
func outgoingImpactEdges(edges []SemanticEdge, source string) []SemanticEdge {
	out := []SemanticEdge{}
	for _, edge := range edges {
		if edge.FromStepID == source {
			out = append(out, edge)
		}
	}
	return out
}
func stepSymbol(step *SemanticStep) string {
	if step == nil {
		return ""
	}
	if step.TechnicalName != "" {
		return step.TechnicalName
	}
	return step.Anchor.EnclosingSymbolPath
}
func cloneImpactSet(input map[string]bool) map[string]bool {
	out := map[string]bool{}
	for k, v := range input {
		out[k] = v
	}
	return out
}
func appendImpactPath(path []string, step string) []string {
	out := append([]string{}, path...)
	return append(out, step)
}
func hasImpactTestRule(rules []string) bool {
	for _, r := range rules {
		if strings.HasPrefix(strings.ToLower(r), "test:") || strings.Contains(strings.ToLower(r), "test") {
			return true
		}
	}
	return false
}
func splitImpactEffect(effect string) (string, string) {
	for _, p := range []struct{ prefix, kind string }{{"api:", "api"}, {"db:", "database"}, {"queue:", "message_queue"}, {"fs:", "filesystem"}} {
		if strings.HasPrefix(effect, p.prefix) {
			return p.kind, strings.TrimPrefix(effect, p.prefix)
		}
	}
	return "api", effect
}
func unknownTouchesImpact(subject string, visited map[string]bool, steps map[string]*SemanticStep) bool {
	for id := range visited {
		step := steps[id]
		if subject == "" || subject == id || subject == stepSymbol(step) || subject == step.Name {
			return true
		}
	}
	// SemanticMapIR is already a task-scoped structural slice. An unresolved
	// boundary without an explicit source identity cannot safely be proven
	// unrelated to a target inside that slice, so it remains a conservative
	// frontier instead of disappearing from impact output.
	return subject != ""
}

func verifiedImpactEdge(status string) bool {
	return status == "verified" || status == "resolved"
}

func impactEdgeRelationKind(kind string) string {
	normalized := strings.ToLower(strings.TrimSpace(kind))
	switch {
	case normalized == "resolved_cross_file", strings.Contains(normalized, "call"):
		return "calls"
	case strings.Contains(normalized, "override"):
		return "overrides"
	case strings.Contains(normalized, "instantiat"), strings.Contains(normalized, "construct"):
		return "instantiates"
	default:
		return normalized
	}
}

func hasUnvisitedImpactEdge(edges []SemanticEdge, traversable, visited map[string]bool, incoming bool) bool {
	for _, edge := range edges {
		next := edge.ToStepID
		if incoming {
			next = edge.FromStepID
		}
		if traversable[impactEdgeRelationKind(edge.Kind)] && !visited[next] {
			return true
		}
	}
	return false
}

func stringSet(values []string) map[string]bool {
	set := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = true
		}
	}
	return set
}

// impactTraversalRelationKinds identifies edge capabilities needed to reach
// terminal claims. Calls are connectivity, not an implied output category.
// Caller output remains controlled solely by the requested relation kinds.
func impactTraversalRelationKinds(requested map[string]bool) map[string]bool {
	traversable := map[string]bool{}
	for _, relation := range []string{"calls", "overrides", "instantiates"} {
		if requested[relation] {
			traversable[relation] = true
		}
	}
	for _, relation := range []string{"state_mutation", "external_effect", "related_test"} {
		if requested[relation] {
			traversable["calls"] = true
			break
		}
	}
	return traversable
}
func cloneImpactCoverage(input *CoverageBoundary) *ImpactCoverageBoundary {
	if input == nil {
		return &ImpactCoverageBoundary{IncludedSourceRoots: []string{}, ExcludedReasons: []string{"coverage_unmeasured"}}
	}
	return &ImpactCoverageBoundary{IncludedSourceRoots: append([]string{}, input.IncludedSourceRoots...), ExcludedReasons: append([]string{}, input.ExcludedReasons...)}
}
func stableImpactID(target ImpactTarget, mapIR *SemanticMapIR, opts ImpactOptions) string {
	relations := append([]string{}, opts.RelationKinds...)
	supported := append([]string{}, opts.SupportedRelationKinds...)
	batchRefs := append([]string{}, opts.ChangeBatchStepRefs...)
	sort.Strings(relations)
	sort.Strings(supported)
	sort.Strings(batchRefs)
	identity := struct {
		Target       ImpactTarget `json:"target"`
		Basis        string       `json:"basis"`
		Generation   string       `json:"generation"`
		Snapshot     string       `json:"snapshot"`
		Freshness    string       `json:"freshness"`
		MaxDepth     int          `json:"maxDepth"`
		MaxNodes     int          `json:"maxNodes"`
		Relations    []string     `json:"relations"`
		Supported    []string     `json:"supported"`
		BatchRefs    []string     `json:"batchRefs"`
		CoverageDone bool         `json:"coverageComplete"`
	}{target, mapIR.ComputedBasisID, mapIR.GenerationID, mapIR.ValidatedAgainstSnapshotID, opts.Freshness, opts.MaxDepth, opts.MaxNodes, relations, supported, batchRefs, opts.CoverageComplete}
	raw, _ := json.Marshal(identity)
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("impact-%x", sum[:])
}
func sortImpactClaims(d *DirectImpact, i *IndirectImpact) {
	sort.Slice(d.Callers, func(a, b int) bool { return d.Callers[a].SymbolPath < d.Callers[b].SymbolPath })
	sort.Slice(i.Callers, func(a, b int) bool {
		if i.Callers[a].Depth != i.Callers[b].Depth {
			return i.Callers[a].Depth < i.Callers[b].Depth
		}
		return i.Callers[a].SymbolPath < i.Callers[b].SymbolPath
	})
}
func redactChangeImpactGraph(graph *ChangeImpactGraph) (*ChangeImpactGraph, error) {
	raw, err := json.Marshal(graph)
	if err != nil {
		return nil, fmt.Errorf("marshal impact graph for redaction: %w", err)
	}
	redacted, _, err := secret.RedactJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("redact impact graph: %w", err)
	}
	var out ChangeImpactGraph
	if err := json.Unmarshal(redacted, &out); err != nil {
		return nil, fmt.Errorf("decode redacted impact graph: %w", err)
	}
	for i := range out.Evidence {
		evidenceBytes, marshalErr := json.Marshal(graph.Evidence[i])
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal impact evidence for redaction status: %w", marshalErr)
		}
		_, count, redactErr := secret.RedactJSON(evidenceBytes)
		if redactErr != nil {
			return nil, fmt.Errorf("inspect impact evidence redaction status: %w", redactErr)
		}
		if count > 0 {
			out.Evidence[i].RedactionStatus = "redacted"
		} else {
			out.Evidence[i].RedactionStatus = "passed"
		}
	}
	return &out, nil
}
