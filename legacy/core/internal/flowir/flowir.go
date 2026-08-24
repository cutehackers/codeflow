// Package flowir owns CodeFlow's deterministic, evidence-backed flow document.
package flowir

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
)

const SchemaVersion = "1"

type Status string

const (
	Observed Status = "observed"
	Mixed    Status = "mixed"
	Unknown  Status = "unknown"
	Stale    Status = "stale"
)

type Anchor struct {
	Kind        string `json:"kind"`
	Path        string `json:"path"`
	Symbol      string `json:"symbol,omitempty"`
	LineRange   []int  `json:"line_range,omitempty"`
	ByteRange   []int  `json:"byte_range,omitempty"`
	FileHash    string `json:"file_hash"`
	SpanHash    string `json:"span_hash,omitempty"`
	Fingerprint string `json:"semantic_fingerprint,omitempty"`
	Revision    string `json:"captured_at_revision"`
}

type Basis struct {
	Repository          string          `json:"repository"`
	HeadRevision        string          `json:"head_revision"`
	BaselineRevision    string          `json:"baseline_revision,omitempty"`
	WorktreeFingerprint string          `json:"worktree_fingerprint"`
	Dirty               bool            `json:"dirty"`
	Manifest            []ManifestEntry `json:"manifest"`
}

// SameBasis is intentionally strict: multi-flow publication may share results
// only when every repository, Git, dirty-state, fingerprint, and manifest field
// describes the same captured worktree.
func SameBasis(left, right Basis) bool { return reflect.DeepEqual(left, right) }

// ManifestEntry is a byte-for-byte observation of one path in a worktree.
// It is deliberately data, not a claim about what the source means.
type ManifestEntry struct {
	Path      string `json:"path"`
	Type      string `json:"type"`
	Mode      string `json:"mode"`
	FileHash  string `json:"file_hash,omitempty"`
	GitState  string `json:"git_state"`
	Generated bool   `json:"generated"`
	Excluded  bool   `json:"excluded,omitempty"`
}

type Fact struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Subject string `json:"subject"`
	Object  string `json:"object,omitempty"`
	// Proof exposes the analysis layer that justified this claim. SymbolID is
	// present when Dart Analyzer resolved a declaration; neither field changes
	// merely because the source moved to another line.
	Proof    string   `json:"proof,omitempty"`
	SymbolID string   `json:"symbol_id,omitempty"`
	Evidence []Anchor `json:"evidence"`
	Status   Status   `json:"status"`
}

type Step struct {
	ID              string   `json:"id"`
	BehaviorKey     string   `json:"behavior_key"`
	Order           int      `json:"order"`
	Actor           string   `json:"actor"`
	TriggerFact     string   `json:"trigger_fact"`
	BehaviorFacts   []string `json:"behavior_facts"`
	ResultFacts     []string `json:"result_facts"`
	Branches        []Branch `json:"branches,omitempty"`
	PrimaryEvidence []Anchor `json:"primary_evidence"`
	Status          Status   `json:"status"`
}

type Branch struct {
	ID             string   `json:"id"`
	ConditionFact  string   `json:"condition_fact"`
	OutcomeStepIDs []string `json:"outcome_step_ids"`
	Evidence       []Anchor `json:"evidence"`
	Status         Status   `json:"status"`
}

// Unknown records an evidence-backed boundary in the flow where CodeFlow
// deliberately refused to choose a target. It is deterministic FlowIR data,
// rather than a runtime error or an invented fact.
type UnknownDetail struct {
	ID           string   `json:"id"`
	Question     string   `json:"question"`
	Reason       string   `json:"reason"`
	RelatedSteps []string `json:"related_steps"`
	RelatedEdges []string `json:"related_edges,omitempty"`
	Evidence     []Anchor `json:"evidence"`
	// DebtState deliberately does not make an unknown observed. It records the
	// review disposition while preserving the evidence boundary in FlowIR.
	DebtState          string   `json:"debt_state,omitempty"`
	ResolutionCriteria []string `json:"resolution_criteria,omitempty"`
	SuggestedEvidence  []string `json:"suggested_evidence,omitempty"`
	Impact             []string `json:"impact,omitempty"`
}

// CausalEdge is the explicit, evidence-backed relation between two facts.
// Steps are a readable projection of these edges, never the only source of
// causal meaning. Conditions name fact IDs that must hold for this edge.
type CausalEdge struct {
	ID         string   `json:"id"`
	FromFact   string   `json:"from_fact"`
	ToFact     string   `json:"to_fact"`
	Kind       string   `json:"kind"`
	Conditions []string `json:"conditions,omitempty"`
	Evidence   []Anchor `json:"evidence"`
	Status     Status   `json:"status"`
}

type Flow struct {
	ID             string `json:"id"`
	FlowKey        string `json:"flow_key"`
	EntryPointFact string `json:"entry_point_fact"`
	Steps          []Step `json:"steps"`
	Status         Status `json:"status"`
}

// Scenario is a deterministic, action-rooted projection of one screen flow.
// A screen may expose several independently selectable user actions. Keeping
// their step IDs separate prevents a reader from mistaking source-order for a
// causal sequence between, for example, two different sign-up methods.
//
// Scenario contains no business prose. Reader-facing domain names belong to
// the separately approved ontology layer, while this identity remains fully
// reproducible from current source evidence.
type Scenario struct {
	ID              string   `json:"id"`
	InteractionFact string   `json:"interaction_fact"`
	StepIDs         []string `json:"step_ids"`
	Status          Status   `json:"status"`
}

// ArchitectureSlice is intentionally just the components and relations that
// participate in this flow; it is not a repository-wide diagram.
type ArchitectureSlice struct {
	EntryPoints []string `json:"entry_points"`
	Boundaries  []string `json:"boundaries"`
	Components  []string `json:"components"`
	Relations   []string `json:"relations"`
}

// Document deliberately excludes timestamps, URLs, and runtime status.
type Document struct {
	SchemaVersion string            `json:"schema_version"`
	Basis         Basis             `json:"basis"`
	Facts         []Fact            `json:"facts"`
	CausalEdges   []CausalEdge      `json:"causal_edges,omitempty"`
	Architecture  ArchitectureSlice `json:"architecture"`
	Current       Flow              `json:"current"`
	Scenarios     []Scenario        `json:"scenarios,omitempty"`
	Unknowns      []UnknownDetail   `json:"unknowns,omitempty"`
}

func CausalEdgeID(from, to, kind string, conditions []string) string {
	conditions = append([]string(nil), conditions...)
	sort.Strings(conditions)
	parts := append([]string{"causal_edge", from, to, kind}, conditions...)
	return Hash(parts...)
}

// BranchID is the stable branch contract. It deliberately depends only on
// the condition fact and the ordered outcome behavior keys: source lines and
// display prose must never change identity.
func BranchID(conditionFactID string, orderedOutcomeBehaviorKeys []string) string {
	return Hash(append([]string{conditionFactID}, orderedOutcomeBehaviorKeys...)...)
}

// ScenarioID is stable across presentation and source-position changes. The
// action fact already incorporates the resolved callback identity and its
// evidence fingerprint, so a changed interaction gets a new scenario instead
// of retaining a misleading domain label.
func ScenarioID(flowID, interactionFactID string) string {
	return Hash("scenario", flowID, interactionFactID)
}

// DeriveScenarios projects a route or system flow into one scenario per
// observed user_action or system_event. A step joins its entry event when it either contains that
// action or is triggered by a fact already owned by the action's path. This
// follows the existing FlowIR causal shape and deliberately does not infer a
// relationship from source ordering.
func DeriveScenarios(document *Document) {
	if document == nil {
		return
	}
	facts := make(map[string]Fact, len(document.Facts))
	for _, fact := range document.Facts {
		facts[fact.ID] = fact
	}
	actionIDs := make([]string, 0)
	seenActions := map[string]bool{}
	for _, step := range document.Current.Steps {
		for _, id := range step.BehaviorFacts {
			if fact, ok := facts[id]; ok && (fact.Kind == "user_action" || fact.Kind == "system_event") && fact.Status != Stale && !seenActions[id] {
				actionIDs = append(actionIDs, id)
				seenActions[id] = true
			}
		}
	}
	scenarios := make([]Scenario, 0, len(actionIDs))
	for _, actionID := range actionIDs {
		ownedFacts := map[string]bool{actionID: true}
		ownedSteps := map[string]bool{}
		changed := true
		for changed {
			changed = false
			for _, step := range document.Current.Steps {
				if ownedSteps[step.ID] {
					continue
				}
				containsAction := false
				for _, id := range step.BehaviorFacts {
					if id == actionID {
						containsAction = true
						break
					}
				}
				if !containsAction && !ownedFacts[step.TriggerFact] {
					continue
				}
				ownedSteps[step.ID] = true
				changed = true
				for _, id := range step.BehaviorFacts {
					ownedFacts[id] = true
				}
				for _, id := range step.ResultFacts {
					ownedFacts[id] = true
				}
			}
		}
		stepIDs := make([]string, 0, len(ownedSteps))
		status := Observed
		hasObserved := false
		for _, step := range document.Current.Steps {
			if !ownedSteps[step.ID] {
				continue
			}
			stepIDs = append(stepIDs, step.ID)
			if step.Status == Observed {
				hasObserved = true
			}
			if step.Status == Unknown {
				status = Mixed
			}
			if step.Status == Mixed {
				status = Mixed
			}
		}
		if !hasObserved && len(stepIDs) > 0 {
			status = Unknown
		}
		if len(stepIDs) > 0 {
			scenarios = append(scenarios, Scenario{ID: ScenarioID(document.Current.ID, actionID), InteractionFact: actionID, StepIDs: stepIDs, Status: status})
		}
	}
	document.Scenarios = scenarios
}

func Hash(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// SHA256Bytes implements the evidence hash contract: no delimiter or normalization is applied.
func SHA256Bytes(bytes []byte) string {
	sum := sha256.Sum256(bytes)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// CanonicalJSON produces stable bytes: facts are a set sorted by ID; causal steps retain order.
func CanonicalJSON(document Document) ([]byte, error) {
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	var canonical Document
	if err := json.Unmarshal(raw, &canonical); err != nil {
		return nil, err
	}
	sort.Slice(canonical.Basis.Manifest, func(i, j int) bool { return canonical.Basis.Manifest[i].Path < canonical.Basis.Manifest[j].Path })
	sort.Slice(canonical.Facts, func(i, j int) bool { return canonical.Facts[i].ID < canonical.Facts[j].ID })
	sort.Slice(canonical.CausalEdges, func(i, j int) bool { return canonical.CausalEdges[i].ID < canonical.CausalEdges[j].ID })
	sort.Slice(canonical.Unknowns, func(i, j int) bool { return canonical.Unknowns[i].ID < canonical.Unknowns[j].ID })
	canonical.Architecture.EntryPoints = sortedUnique(canonical.Architecture.EntryPoints)
	canonical.Architecture.Boundaries = sortedUnique(canonical.Architecture.Boundaries)
	canonical.Architecture.Components = sortedUnique(canonical.Architecture.Components)
	canonical.Architecture.Relations = sortedUnique(canonical.Architecture.Relations)
	for i := range canonical.Facts {
		sort.Slice(canonical.Facts[i].Evidence, func(a, b int) bool {
			return anchorKey(canonical.Facts[i].Evidence[a]) < anchorKey(canonical.Facts[i].Evidence[b])
		})
	}
	for i := range canonical.CausalEdges {
		canonical.CausalEdges[i].Conditions = sortedUnique(canonical.CausalEdges[i].Conditions)
		sort.Slice(canonical.CausalEdges[i].Evidence, func(a, b int) bool {
			return anchorKey(canonical.CausalEdges[i].Evidence[a]) < anchorKey(canonical.CausalEdges[i].Evidence[b])
		})
	}
	for i := range canonical.Unknowns {
		canonical.Unknowns[i].RelatedSteps = sortedUnique(canonical.Unknowns[i].RelatedSteps)
		canonical.Unknowns[i].RelatedEdges = sortedUnique(canonical.Unknowns[i].RelatedEdges)
		canonical.Unknowns[i].SuggestedEvidence = sortedUnique(canonical.Unknowns[i].SuggestedEvidence)
		sort.Slice(canonical.Unknowns[i].Evidence, func(a, b int) bool {
			return anchorKey(canonical.Unknowns[i].Evidence[a]) < anchorKey(canonical.Unknowns[i].Evidence[b])
		})
	}
	for i := range canonical.Scenarios {
		canonical.Scenarios[i].StepIDs = sortedScenarioSteps(canonical.Scenarios[i].StepIDs, canonical.Current.Steps)
	}
	sort.Slice(canonical.Scenarios, func(i, j int) bool { return canonical.Scenarios[i].ID < canonical.Scenarios[j].ID })
	return json.Marshal(canonical)
}

func sortedScenarioSteps(ids []string, steps []Step) []string {
	order := make(map[string]int, len(steps))
	for i, step := range steps {
		order[step.ID] = i
	}
	ids = append([]string(nil), ids...)
	sort.Slice(ids, func(i, j int) bool {
		left, leftOK := order[ids[i]]
		right, rightOK := order[ids[j]]
		if leftOK && rightOK && left != right {
			return left < right
		}
		if leftOK != rightOK {
			return leftOK
		}
		return ids[i] < ids[j]
	})
	return slices.Compact(ids)
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return values
	}
	sort.Strings(values)
	return slices.Compact(values)
}

func anchorKey(anchor Anchor) string {
	return anchor.Path + "\x00" + anchor.Kind + "\x00" + anchor.FileHash
}

func Validate(document Document) error {
	if document.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported FlowIR schema_version %q", document.SchemaVersion)
	}
	if document.Basis.Repository == "" || document.Basis.WorktreeFingerprint == "" {
		return errors.New("basis repository and worktree_fingerprint are required")
	}
	seenPaths := map[string]bool{}
	for _, entry := range document.Basis.Manifest {
		if entry.Path == "" || entry.Path[0] == '/' || entry.Type == "" || entry.Mode == "" || entry.GitState == "" {
			return errors.New("manifest entries require relative path, type, mode, and git_state")
		}
		if seenPaths[entry.Path] {
			return fmt.Errorf("manifest contains duplicate path %s", entry.Path)
		}
		seenPaths[entry.Path] = true
	}
	facts := map[string]Fact{}
	for _, fact := range document.Facts {
		if fact.ID == "" || fact.Kind == "" || fact.Subject == "" {
			return errors.New("fact id, kind, and subject are required")
		}
		if fact.Proof != "" && fact.Proof != "resolved_ast" && fact.Proof != "framework_rule_v1" && fact.Proof != "contract_v1" {
			return fmt.Errorf("fact %s has unsupported proof layer %q", fact.ID, fact.Proof)
		}
		if fact.Proof == "resolved_ast" && fact.SymbolID == "" {
			return fmt.Errorf("resolved fact %s requires a canonical symbol_id", fact.ID)
		}
		if fact.Status == Observed && !hasObservedEvidence(fact.Evidence) {
			return fmt.Errorf("observed fact %s requires non-session evidence", fact.ID)
		}
		if fact.Status == Stale { /* valid in facts but cannot enter current flow */
		}
		facts[fact.ID] = fact
	}
	edges := map[string]bool{}
	for _, edge := range document.CausalEdges {
		if edge.ID == "" || edge.FromFact == "" || edge.ToFact == "" || edge.Kind == "" || edge.FromFact == edge.ToFact {
			return errors.New("causal edges require distinct endpoints, id, and kind")
		}
		if edges[edge.ID] || edge.ID != CausalEdgeID(edge.FromFact, edge.ToFact, edge.Kind, edge.Conditions) {
			return fmt.Errorf("causal edge %s has duplicate or non-deterministic identity", edge.ID)
		}
		edges[edge.ID] = true
		from, fromOK := facts[edge.FromFact]
		to, toOK := facts[edge.ToFact]
		if !fromOK || !toOK || from.Status == Stale || to.Status == Stale {
			return fmt.Errorf("causal edge %s references missing or stale fact", edge.ID)
		}
		if edge.Status != Observed && edge.Status != Unknown && edge.Status != Mixed {
			return fmt.Errorf("causal edge %s has invalid status", edge.ID)
		}
		if !hasObservedEvidence(edge.Evidence) {
			return fmt.Errorf("causal edge %s requires current evidence", edge.ID)
		}
		for _, conditionID := range edge.Conditions {
			condition, ok := facts[conditionID]
			if !ok || (condition.Kind != "condition" && condition.Kind != "confirmation_condition" && condition.Kind != "listener_condition") || condition.Status == Stale {
				return fmt.Errorf("causal edge %s references invalid condition", edge.ID)
			}
		}
	}
	scenarioSteps := map[string]Step{}
	for _, step := range document.Current.Steps {
		scenarioSteps[step.ID] = step
	}
	seenScenarios := map[string]bool{}
	for _, scenario := range document.Scenarios {
		if scenario.ID == "" || scenario.InteractionFact == "" || len(scenario.StepIDs) == 0 || seenScenarios[scenario.ID] {
			return errors.New("scenarios require unique id, interaction_fact, and step_ids")
		}
		if scenario.ID != ScenarioID(document.Current.ID, scenario.InteractionFact) {
			return fmt.Errorf("scenario %s has non-deterministic identity", scenario.ID)
		}
		action, ok := facts[scenario.InteractionFact]
		if !ok || (action.Kind != "user_action" && action.Kind != "system_event") || action.Status == Stale {
			return fmt.Errorf("scenario %s references an invalid interaction", scenario.ID)
		}
		seenSteps := map[string]bool{}
		for _, stepID := range scenario.StepIDs {
			if _, ok := scenarioSteps[stepID]; !ok || seenSteps[stepID] {
				return fmt.Errorf("scenario %s references an invalid step", scenario.ID)
			}
			seenSteps[stepID] = true
		}
		if scenario.Status != Observed && scenario.Status != Mixed && scenario.Status != Unknown {
			return fmt.Errorf("scenario %s has invalid status", scenario.ID)
		}
		seenScenarios[scenario.ID] = true
	}
	if _, ok := facts[document.Current.EntryPointFact]; !ok {
		return errors.New("current flow references missing entry point fact")
	}
	if len(document.Architecture.EntryPoints) > 0 && document.Architecture.EntryPoints[0] != document.Current.ID {
		return errors.New("architecture slice must start with the current flow entry point")
	}
	previous := 0
	steps := map[string]int{}
	keys := map[string]string{}
	for i, step := range document.Current.Steps {
		if step.ID != "" {
			steps[step.ID] = i
			keys[step.ID] = step.BehaviorKey
		}
	}
	for _, step := range document.Current.Steps {
		if step.ID == "" || step.Order <= previous || step.Actor == "" || step.BehaviorKey == "" {
			return fmt.Errorf("steps need ids, ordered positions, actors, and behavior keys")
		}
		previous = step.Order
		if len(step.PrimaryEvidence) > 3 {
			return fmt.Errorf("step %s exposes more than three primary anchors", step.ID)
		}
		if _, ok := facts[step.TriggerFact]; !ok {
			return fmt.Errorf("step %s references missing trigger fact", step.ID)
		}
		for _, id := range append(append([]string{}, step.BehaviorFacts...), step.ResultFacts...) {
			fact, ok := facts[id]
			if !ok {
				return fmt.Errorf("step %s references missing fact %s", step.ID, id)
			}
			if fact.Status == Stale {
				return fmt.Errorf("step %s references stale fact %s", step.ID, id)
			}
			if fact.Kind == "visible_result" && !hasVisibleEvidence(fact.Evidence) {
				return fmt.Errorf("visible result %s requires UI-state or route evidence", fact.ID)
			}
		}
		for _, branch := range step.Branches {
			condition, ok := facts[branch.ConditionFact]
			if !ok || (condition.Kind != "condition" && condition.Kind != "confirmation_condition" && condition.Kind != "listener_condition") {
				return fmt.Errorf("branch %s requires a condition fact", branch.ID)
			}
			if !hasObservedEvidence(branch.Evidence) {
				return fmt.Errorf("branch %s requires current condition evidence", branch.ID)
			}
			if len(branch.OutcomeStepIDs) == 0 || (branch.Status != Observed && branch.Status != Unknown) {
				return fmt.Errorf("branch %s requires ordered outcomes and observed or unknown status", branch.ID)
			}
			outcomeKeys := make([]string, 0, len(branch.OutcomeStepIDs))
			last := -1
			for _, outcome := range branch.OutcomeStepIDs {
				position, ok := steps[outcome]
				if !ok || position <= last {
					return fmt.Errorf("branch %s references unordered or missing outcome step %s", branch.ID, outcome)
				}
				last = position
				outcomeKeys = append(outcomeKeys, keys[outcome])
			}
			if branch.ID != BranchID(branch.ConditionFact, outcomeKeys) {
				return fmt.Errorf("branch %s has a non-deterministic id", branch.ID)
			}
		}
	}
	for _, unknown := range document.Unknowns {
		if unknown.ID == "" || unknown.Question == "" || unknown.Reason == "" || len(unknown.RelatedSteps) == 0 || !hasObservedEvidence(unknown.Evidence) {
			return fmt.Errorf("unknown requires identity, question, reason, related step, and current evidence")
		}
		for _, id := range unknown.RelatedSteps {
			if _, ok := steps[id]; !ok {
				return fmt.Errorf("unknown %s references missing step %s", unknown.ID, id)
			}
		}
		for _, id := range unknown.RelatedEdges {
			if !edges[id] {
				return fmt.Errorf("unknown %s references missing causal edge %s", unknown.ID, id)
			}
		}
		if unknown.DebtState != "" && unknown.DebtState != "open" && unknown.DebtState != "accepted" && unknown.DebtState != "resolved" {
			return fmt.Errorf("unknown %s has invalid debt state", unknown.ID)
		}
		if unknown.DebtState == "resolved" && len(unknown.ResolutionCriteria) == 0 {
			return fmt.Errorf("resolved unknown %s needs resolution criteria", unknown.ID)
		}
	}
	return nil
}

func hasObservedEvidence(anchors []Anchor) bool {
	for _, a := range anchors {
		if a.Kind != "session" && a.FileHash != "" {
			return true
		}
	}
	return false
}
func hasVisibleEvidence(anchors []Anchor) bool {
	for _, a := range anchors {
		if a.Kind == "code" && (a.Symbol == "route" || a.Symbol == "ui_state" || a.Symbol == "output") {
			return true
		}
	}
	return false
}
